// Package aws implements core.Provider on AWS Organizations, Budgets, and
// Cost Explorer. Every call uses management-account credentials from the
// default AWS credential chain. Account tags are the tool's only state.
package aws

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/budgets"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/service/identitystore"
	idsdoc "github.com/aws/aws-sdk-go-v2/service/identitystore/document"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	orgtypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"

	"playplace/internal/core"
)

// Config selects the AWS environment.
type Config struct {
	Region           string // default region for Organizations; empty uses the SDK chain
	Endpoint         string // optional override, e.g. http://localhost:4566 for LocalStack
	PlaygroundOUName string // OU that holds playground accounts, found by name directly under the root
	PlaygroundOUID   string // the same OU, found by ID at any depth. When set, PlaygroundOUName is not used
	Profile          string // optional named profile
	PermissionSet    string // Identity Center permission set granted to owners; empty disables access
}

// ouIDPattern is the OU ID format that the Organizations API documents for
// OrganizationalUnit.Id: "ou-", 4 to 32 lowercase letters or digits (the
// ID of the root), a dash, and 8 to 32 more lowercase letters or digits.
var ouIDPattern = regexp.MustCompile(`^ou-[0-9a-z]{4,32}-[a-z0-9]{8,32}$`)

// ValidateOUID refuses a string that AWS does not accept as an OU ID.
func ValidateOUID(id string) error {
	if !ouIDPattern.MatchString(id) {
		return fmt.Errorf("%q is not an OU ID. An OU ID is \"ou-\", 4 to 32 lowercase letters or digits, a dash, and 8 to 32 more, for example ou-ab12-cd34ef56", id)
	}
	return nil
}

// Provider talks to one AWS Organization.
type Provider struct {
	orgs    *organizations.Client
	budgets *budgets.Client
	ce      *costexplorer.Client
	sso     *ssoadmin.Client
	ids     *identitystore.Client
	cfg     Config

	mu   sync.Mutex
	info *core.OrgInfo
	idc  *idc
}

// LoadConfig resolves AWS credentials and region for cfg.
func LoadConfig(ctx context.Context, cfg Config) (aws.Config, error) {
	var opts []func(*config.LoadOptions) error
	if cfg.Region != "" {
		opts = append(opts, config.WithRegion(cfg.Region))
	}
	if cfg.Profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(cfg.Profile))
	}
	awscfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load aws config: %w", err)
	}
	if awscfg.Region == "" {
		awscfg.Region = "us-east-1"
	}
	return awscfg, nil
}

// New builds clients from the default credential chain plus cfg.
func New(ctx context.Context, cfg Config) (*Provider, error) {
	awscfg, err := LoadConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return NewFromConfig(awscfg, cfg), nil
}

// NewFromConfig builds clients from an already resolved AWS config.
func NewFromConfig(awscfg aws.Config, cfg Config) *Provider {
	if cfg.PlaygroundOUName == "" {
		cfg.PlaygroundOUName = "Playground"
	}
	endpoint := func(base **string) {
		if cfg.Endpoint != "" {
			*base = aws.String(cfg.Endpoint)
		}
	}
	p := &Provider{cfg: cfg}
	p.orgs = organizations.NewFromConfig(awscfg, func(o *organizations.Options) { endpoint(&o.BaseEndpoint) })
	// Budgets and Cost Explorer are global services served from us-east-1.
	p.budgets = budgets.NewFromConfig(awscfg, func(o *budgets.Options) {
		o.Region = "us-east-1"
		endpoint(&o.BaseEndpoint)
	})
	p.ce = costexplorer.NewFromConfig(awscfg, func(o *costexplorer.Options) {
		o.Region = "us-east-1"
		endpoint(&o.BaseEndpoint)
	})
	p.sso = ssoadmin.NewFromConfig(awscfg, func(o *ssoadmin.Options) { endpoint(&o.BaseEndpoint) })
	p.ids = identitystore.NewFromConfig(awscfg, func(o *identitystore.Options) { endpoint(&o.BaseEndpoint) })
	return p
}

// docString wraps a string for identitystore attribute values.
func docString(s string) idsdoc.Interface { return idsdoc.NewLazyDocument(s) }

func (p *Provider) Name() string { return "aws" }

// EnsureOrganization is `playplace init`: it creates the organization (all
// features) and the playground OU when they are missing. Nothing else
// creates them. Every other method resolves them through org. With
// PlaygroundOUID set, it creates nothing and only verifies the OU. The ID
// names an OU that exists already, and a new organization cannot hold it.
func (p *Provider) EnsureOrganization(ctx context.Context) (core.OrgInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	info, inUse, err := p.lookup(ctx)
	if err != nil {
		return info, err
	}
	if p.cfg.PlaygroundOUID != "" {
		if err := p.missing(info, inUse); err != nil {
			return info, err
		}
		p.info = &info
		return info, nil
	}
	if !inUse {
		created, err := p.orgs.CreateOrganization(ctx, &organizations.CreateOrganizationInput{
			FeatureSet: orgtypes.OrganizationFeatureSetAll,
		})
		if err != nil {
			return info, fmt.Errorf("create organization: %w", err)
		}
		info.Created = true
		info.OrgID = aws.ToString(created.Organization.Id)
		info.ManagementAccountID = aws.ToString(created.Organization.MasterAccountId)
		if err := p.findPlayground(ctx, &info); err != nil {
			return info, err
		}
	}
	if info.PlaygroundOUID == "" {
		out, err := p.orgs.CreateOrganizationalUnit(ctx, &organizations.CreateOrganizationalUnitInput{
			ParentId: aws.String(info.RootID),
			Name:     aws.String(p.cfg.PlaygroundOUName),
		})
		if err != nil {
			return info, fmt.Errorf("create OU %q: %w", p.cfg.PlaygroundOUName, err)
		}
		info.PlaygroundOUID = aws.ToString(out.OrganizationalUnit.Id)
		info.PlaygroundOUName = p.cfg.PlaygroundOUName
		info.OUCreated = true
	}
	cached := info
	cached.Created, cached.OUCreated = false, false // "on this call" only
	p.info = &cached
	return info, nil
}

// org finds the organization and the playground OU without creating either.
// A wrong profile, a typo in --ou-name or --ou-id, or a nested OU that
// --ou-name names then fails here instead of building a new OU that lacks
// the operator's SCPs. A failure is not cached.
func (p *Provider) org(ctx context.Context) (core.OrgInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.info != nil {
		return *p.info, nil
	}
	info, inUse, err := p.lookup(ctx)
	if err == nil {
		err = p.missing(info, inUse)
	}
	if err != nil {
		return info, err
	}
	p.info = &info
	return info, nil
}

// missing explains a lookup that did not find the organization or the
// playground OU. It returns nil when the lookup found both.
func (p *Provider) missing(info core.OrgInfo, inUse bool) error {
	id := p.cfg.PlaygroundOUID
	switch {
	case !inUse && id != "":
		return fmt.Errorf("the AWS credentials are not in an AWS Organization, so they cannot reach playground OU %s (--ou-id). Use the profile of the management account of the organization that holds the OU", id)
	case !inUse:
		return errors.New("the AWS credentials are not in an AWS Organization. Use the profile of the management account. Run `playplace init` only if this account must become the management account of a new organization")
	case info.PlaygroundOUID != "":
		return nil
	case id != "":
		// AWS reports an OU of another organization as not found, so a
		// wrong profile also ends here. init cannot fix it.
		return fmt.Errorf("playground OU %s (--ou-id) was not found in organization %s. Check --ou-id (PLAYPLACE_OU_ID), and check that the AWS profile is the management account of the organization that holds the OU. `playplace init` does not create an OU when --ou-id is set", id, info.OrgID)
	default:
		// --ou-name does not find an OU nested in another OU. init would then
		// create a second, empty OU under the root, so the message points to
		// --ou-id and does not offer init as the fix.
		return fmt.Errorf("playground OU %q is not a direct child of the organization root %s. --ou-name finds only an OU directly under the root. Check --ou-name (PLAYPLACE_OU_NAME). If the OU is nested in another OU, set --ou-id (PLAYPLACE_OU_ID) to its ID instead. Run `playplace init` only if no Playground OU exists yet, because it creates a new, empty OU under the root", p.cfg.PlaygroundOUName, info.RootID)
	}
}

// lookup reads the organization, its root, and the playground OU if they
// exist. inUse is false when the credentials belong to no organization.
func (p *Provider) lookup(ctx context.Context) (info core.OrgInfo, inUse bool, err error) {
	desc, err := p.orgs.DescribeOrganization(ctx, &organizations.DescribeOrganizationInput{})
	var notInUse *orgtypes.AWSOrganizationsNotInUseException
	if errors.As(err, &notInUse) {
		return info, false, nil
	}
	if err != nil {
		return info, false, fmt.Errorf("describe organization: %w", err)
	}
	info.OrgID = aws.ToString(desc.Organization.Id)
	info.ManagementAccountID = aws.ToString(desc.Organization.MasterAccountId)
	return info, true, p.findPlayground(ctx, &info)
}

// findPlayground sets the root and, when it exists, the playground OU.
func (p *Provider) findPlayground(ctx context.Context, info *core.OrgInfo) error {
	roots, err := p.orgs.ListRoots(ctx, &organizations.ListRootsInput{})
	if err != nil {
		return fmt.Errorf("list roots: %w", err)
	}
	if len(roots.Roots) == 0 {
		return errors.New("organization has no root")
	}
	info.RootID = aws.ToString(roots.Roots[0].Id)
	if p.cfg.PlaygroundOUID != "" {
		return p.describeOU(ctx, info)
	}
	info.PlaygroundOUID, err = p.findOU(ctx, info.RootID, p.cfg.PlaygroundOUName)
	if info.PlaygroundOUID != "" {
		info.PlaygroundOUName = p.cfg.PlaygroundOUName
	}
	return err
}

// describeOU reads the OU that --ou-id names, at any depth, with one
// read-only call. It leaves the OU unset when AWS does not find it. AWS does
// not find an OU of another organization, and the ARN check below refuses
// one too.
func (p *Provider) describeOU(ctx context.Context, info *core.OrgInfo) error {
	id := p.cfg.PlaygroundOUID
	out, err := p.orgs.DescribeOrganizationalUnit(ctx, &organizations.DescribeOrganizationalUnitInput{OrganizationalUnitId: aws.String(id)})
	var notFound *orgtypes.OrganizationalUnitNotFoundException
	if errors.As(err, &notFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("describe OU %s: %w", id, err)
	}
	ou := out.OrganizationalUnit
	if ou == nil {
		return fmt.Errorf("describe OU %s: AWS returned no OU", id)
	}
	// An OU ARN is arn:<partition>:organizations::<account>:ou/<org ID>/<OU ID>.
	ouARN, org := aws.ToString(ou.Arn), ""
	if a, err := arn.Parse(ouARN); err == nil {
		if parts := strings.Split(a.Resource, "/"); len(parts) == 3 && parts[0] == "ou" && parts[2] == id {
			org = parts[1]
		}
	}
	switch {
	case org == "" || aws.ToString(ou.Id) != id:
		return fmt.Errorf("AWS described playground OU %s (--ou-id) with ID %q and ARN %q, so playplace cannot tell which organization holds it", id, aws.ToString(ou.Id), ouARN)
	case org != info.OrgID:
		return fmt.Errorf("playground OU %s (--ou-id) is in organization %s, not in %s. Check --ou-id (PLAYPLACE_OU_ID), and check that the AWS profile is the management account of the organization that holds the OU", id, org, info.OrgID)
	}
	info.PlaygroundOUID = id
	info.PlaygroundOUName = aws.ToString(ou.Name)
	return nil
}

func (p *Provider) findOU(ctx context.Context, parentID, name string) (string, error) {
	pag := organizations.NewListOrganizationalUnitsForParentPaginator(p.orgs,
		&organizations.ListOrganizationalUnitsForParentInput{ParentId: aws.String(parentID)})
	for pag.HasMorePages() {
		page, err := pag.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("list OUs: %w", err)
		}
		for _, ou := range page.OrganizationalUnits {
			if aws.ToString(ou.Name) == name {
				return aws.ToString(ou.Id), nil
			}
		}
	}
	return "", nil
}

// ---- creation --------------------------------------------------------------

func (p *Provider) RequestAccount(ctx context.Context, req core.AccountRequest) (string, error) {
	out, err := p.orgs.CreateAccount(ctx, &organizations.CreateAccountInput{
		AccountName:            aws.String(req.Name),
		Email:                  aws.String(req.Email),
		IamUserAccessToBilling: orgtypes.IAMUserAccessToBillingAllow,
		Tags:                   toTags(req.Tags),
	})
	if err != nil {
		return "", fmt.Errorf("create account: %w", err)
	}
	return aws.ToString(out.CreateAccountStatus.Id), nil
}

func (p *Provider) CreateStatus(ctx context.Context, requestID string) (core.CreateRequest, error) {
	out, err := p.orgs.DescribeCreateAccountStatus(ctx, &organizations.DescribeCreateAccountStatusInput{
		CreateAccountRequestId: aws.String(requestID),
	})
	if err != nil {
		return core.CreateRequest{}, fmt.Errorf("describe create status: %w", err)
	}
	return fromCreateStatus(out.CreateAccountStatus), nil
}

func (p *Provider) ListCreateRequests(ctx context.Context) ([]core.CreateRequest, error) {
	var out []core.CreateRequest
	pag := organizations.NewListCreateAccountStatusPaginator(p.orgs, &organizations.ListCreateAccountStatusInput{
		States: []orgtypes.CreateAccountState{orgtypes.CreateAccountStateInProgress, orgtypes.CreateAccountStateSucceeded, orgtypes.CreateAccountStateFailed},
	})
	for pag.HasMorePages() {
		page, err := pag.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list create account status: %w", err)
		}
		for i := range page.CreateAccountStatuses {
			out = append(out, fromCreateStatus(&page.CreateAccountStatuses[i]))
		}
	}
	return out, nil
}

func fromCreateStatus(st *orgtypes.CreateAccountStatus) core.CreateRequest {
	r := core.CreateRequest{
		RequestID:     aws.ToString(st.Id),
		Name:          aws.ToString(st.AccountName),
		ProviderID:    aws.ToString(st.AccountId),
		FailureReason: string(st.FailureReason),
	}
	switch st.State {
	case orgtypes.CreateAccountStateSucceeded:
		r.State = core.CreateSucceeded
	case orgtypes.CreateAccountStateFailed:
		r.State = core.CreateFailed
	default:
		r.State = core.CreateInProgress
	}
	if st.RequestedTimestamp != nil {
		r.RequestedAt = *st.RequestedTimestamp
	}
	if st.CompletedTimestamp != nil {
		r.CompletedAt = *st.CompletedTimestamp
	}
	return r
}

// ---- accounts and tags -----------------------------------------------------

func (p *Provider) GetAccount(ctx context.Context, providerID string) (core.RemoteAccount, error) {
	out, err := p.orgs.DescribeAccount(ctx, &organizations.DescribeAccountInput{AccountId: aws.String(providerID)})
	if err != nil {
		return core.RemoteAccount{}, fmt.Errorf("describe account %s: %w", providerID, err)
	}
	tags, err := p.tagsFor(ctx, providerID)
	if err != nil {
		return core.RemoteAccount{}, err
	}
	return fromAccount(*out.Account, tags), nil
}

func (p *Provider) ListPlaygroundAccounts(ctx context.Context) ([]core.RemoteAccount, error) {
	info, err := p.org(ctx)
	if err != nil {
		return nil, err
	}
	return p.listAccounts(ctx, info.PlaygroundOUID, nil)
}

// ListRootAccounts lists the root's member accounts that can be playplace
// creations. Every inventory read lists the root, and the root of a small
// organization can hold many accounts, so it skips these before the
// per-account tag read: the management account (never a playground, and it
// cannot be moved), invited accounts (playplace only creates accounts), and
// names that playplace never gives.
func (p *Provider) ListRootAccounts(ctx context.Context) ([]core.RemoteAccount, error) {
	info, err := p.org(ctx)
	if err != nil {
		return nil, err
	}
	return p.listAccounts(ctx, info.RootID, func(a orgtypes.Account) bool {
		return aws.ToString(a.Id) != info.ManagementAccountID &&
			a.JoinedMethod != orgtypes.AccountJoinedMethodInvited &&
			core.ValidateName(aws.ToString(a.Name)) == nil
	})
}

// listAccounts returns the direct children of parent with their tags. A nil
// keep keeps every child.
func (p *Provider) listAccounts(ctx context.Context, parent string, keep func(orgtypes.Account) bool) ([]core.RemoteAccount, error) {
	var out []core.RemoteAccount
	pag := organizations.NewListAccountsForParentPaginator(p.orgs,
		&organizations.ListAccountsForParentInput{ParentId: aws.String(parent)})
	for pag.HasMorePages() {
		page, err := pag.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list accounts under %s: %w", parent, err)
		}
		for _, a := range page.Accounts {
			if keep != nil && !keep(a) {
				continue
			}
			tags, err := p.tagsFor(ctx, aws.ToString(a.Id))
			if err != nil {
				return nil, err
			}
			out = append(out, fromAccount(a, tags))
		}
	}
	return out, nil
}

func (p *Provider) tagsFor(ctx context.Context, id string) (map[string]string, error) {
	tags := map[string]string{}
	pag := organizations.NewListTagsForResourcePaginator(p.orgs,
		&organizations.ListTagsForResourceInput{ResourceId: aws.String(id)})
	for pag.HasMorePages() {
		page, err := pag.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list tags for %s: %w", id, err)
		}
		for _, t := range page.Tags {
			tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
		}
	}
	return tags, nil
}

// PlaceAccount moves an account only out of the root, where AWS creates it.
// An account under any other parent was moved there by someone on purpose,
// for example to a quarantine OU, so it is left alone.
func (p *Provider) PlaceAccount(ctx context.Context, providerID string, tags map[string]string) error {
	info, err := p.org(ctx)
	if err != nil {
		return err
	}
	parents, err := p.orgs.ListParents(ctx, &organizations.ListParentsInput{ChildId: aws.String(providerID)})
	if err != nil {
		return fmt.Errorf("list parents: %w", err)
	}
	if len(parents.Parents) == 0 {
		return fmt.Errorf("account %s has no parent", providerID)
	}
	switch src := aws.ToString(parents.Parents[0].Id); src {
	case info.PlaygroundOUID:
		// Already moved, for example by a placement whose tag write failed.
	case info.RootID:
		_, err := p.orgs.MoveAccount(ctx, &organizations.MoveAccountInput{
			AccountId:           aws.String(providerID),
			SourceParentId:      aws.String(src),
			DestinationParentId: aws.String(info.PlaygroundOUID),
		})
		if err != nil {
			return fmt.Errorf("move account: %w", err)
		}
	default:
		return fmt.Errorf("account %s is under %s, not the organization root. Someone moved it there, so playplace does not move it", providerID, src)
	}
	return p.SetTags(ctx, providerID, tags)
}

func (p *Provider) SetTags(ctx context.Context, providerID string, tags map[string]string) error {
	if len(tags) == 0 {
		return nil
	}
	_, err := p.orgs.TagResource(ctx, &organizations.TagResourceInput{
		ResourceId: aws.String(providerID),
		Tags:       toTags(tags),
	})
	if err != nil {
		return fmt.Errorf("tag account %s: %w", providerID, err)
	}
	return nil
}

func (p *Provider) RemoveTags(ctx context.Context, providerID string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	_, err := p.orgs.UntagResource(ctx, &organizations.UntagResourceInput{
		ResourceId: aws.String(providerID),
		TagKeys:    keys,
	})
	if err != nil {
		return fmt.Errorf("untag account %s: %w", providerID, err)
	}
	return nil
}

// OUTags reads the playground OU's tags, which hold the approval queue.
func (p *Provider) OUTags(ctx context.Context) (map[string]string, error) {
	info, err := p.org(ctx)
	if err != nil {
		return nil, err
	}
	return p.tagsFor(ctx, info.PlaygroundOUID)
}

func (p *Provider) SetOUTags(ctx context.Context, tags map[string]string) error {
	info, err := p.org(ctx)
	if err != nil {
		return err
	}
	return p.SetTags(ctx, info.PlaygroundOUID, tags)
}

func (p *Provider) RemoveOUTags(ctx context.Context, keys []string) error {
	info, err := p.org(ctx)
	if err != nil {
		return err
	}
	return p.RemoveTags(ctx, info.PlaygroundOUID, keys)
}

func (p *Provider) CloseAccount(ctx context.Context, providerID string) error {
	_, err := p.orgs.CloseAccount(ctx, &organizations.CloseAccountInput{AccountId: aws.String(providerID)})
	var already *orgtypes.AccountAlreadyClosedException
	if errors.As(err, &already) {
		return nil
	}
	// Two limits defer a close rather than fail it: the rolling 30-day quota
	// and the number of closures AWS will have in flight at once.
	var cv *orgtypes.ConstraintViolationException
	if errors.As(err, &cv) {
		switch cv.Reason {
		case orgtypes.ConstraintViolationExceptionReasonCloseAccountQuotaExceeded,
			orgtypes.ConstraintViolationExceptionReasonCloseAccountRequestsLimitExceeded:
			return core.ErrCloseQuota
		}
	}
	if err != nil {
		return fmt.Errorf("close account: %w", err)
	}
	return nil
}

// ---- budgets and costs -----------------------------------------------------

// Costs returns daily unblended cost for one linked account. Cost Explorer
// data lags by roughly a day and each call is billed.
func (p *Provider) Costs(ctx context.Context, providerID string, from, to time.Time) ([]core.CostPoint, error) {
	var out []core.CostPoint
	var token *string
	for {
		res, err := p.ce.GetCostAndUsage(ctx, &costexplorer.GetCostAndUsageInput{
			TimePeriod: &cetypes.DateInterval{
				Start: aws.String(from.UTC().Format("2006-01-02")),
				End:   aws.String(to.UTC().Format("2006-01-02")),
			},
			Granularity: cetypes.GranularityDaily,
			Metrics:     []string{"UnblendedCost"},
			Filter: &cetypes.Expression{
				Dimensions: &cetypes.DimensionValues{
					Key:    cetypes.DimensionLinkedAccount,
					Values: []string{providerID},
				},
			},
			NextPageToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("get cost and usage: %w", err)
		}
		for _, r := range res.ResultsByTime {
			d, err := time.Parse("2006-01-02", aws.ToString(r.TimePeriod.Start))
			if err != nil {
				continue
			}
			amt, _ := strconv.ParseFloat(aws.ToString(r.Total["UnblendedCost"].Amount), 64)
			out = append(out, core.CostPoint{Date: d, AmountUSD: amt})
		}
		if res.NextPageToken == nil || aws.ToString(res.NextPageToken) == "" {
			break
		}
		token = res.NextPageToken
	}
	return out, nil
}

func toTags(m map[string]string) []orgtypes.Tag {
	out := make([]orgtypes.Tag, 0, len(m))
	for k, v := range m {
		out = append(out, orgtypes.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return out
}

func fromAccount(a orgtypes.Account, tags map[string]string) core.RemoteAccount {
	r := core.RemoteAccount{
		ProviderID: aws.ToString(a.Id),
		Name:       aws.ToString(a.Name),
		Email:      aws.ToString(a.Email),
		Status:     string(a.State),
		Tags:       tags,
	}
	// State distinguishes PENDING_CLOSURE, CLOSED, and unrelated suspension.
	// Keep a conservative fallback for older emulators: legacy SUSPENDED
	// is ambiguous and must not be promoted to confirmed closure.
	if r.Status == "" {
		r.Status = string(a.Status)
	}
	if a.JoinedTimestamp != nil {
		r.JoinedAt = *a.JoinedTimestamp
	}
	return r
}

// Package aws implements core.Provider on AWS Organizations, Budgets, and
// Cost Explorer. Every call uses management-account credentials from the
// default AWS credential chain. Account tags are the tool's only state.
package aws

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/budgets"
	budgettypes "github.com/aws/aws-sdk-go-v2/service/budgets/types"
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
	PlaygroundOUName string // OU that holds playground accounts
	Profile          string // optional named profile
	PermissionSet    string // Identity Center permission set granted to owners; empty disables access
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

// EnsureOrganization creates the org (all features) and the playground OU if missing.
func (p *Provider) EnsureOrganization(ctx context.Context) (core.OrgInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.info != nil {
		return *p.info, nil
	}

	var info core.OrgInfo
	desc, err := p.orgs.DescribeOrganization(ctx, &organizations.DescribeOrganizationInput{})
	var notInUse *orgtypes.AWSOrganizationsNotInUseException
	switch {
	case errors.As(err, &notInUse):
		created, err := p.orgs.CreateOrganization(ctx, &organizations.CreateOrganizationInput{
			FeatureSet: orgtypes.OrganizationFeatureSetAll,
		})
		if err != nil {
			return info, fmt.Errorf("create organization: %w", err)
		}
		info.Created = true
		info.OrgID = aws.ToString(created.Organization.Id)
		info.ManagementAccountID = aws.ToString(created.Organization.MasterAccountId)
	case err != nil:
		return info, fmt.Errorf("describe organization: %w", err)
	default:
		info.OrgID = aws.ToString(desc.Organization.Id)
		info.ManagementAccountID = aws.ToString(desc.Organization.MasterAccountId)
	}

	roots, err := p.orgs.ListRoots(ctx, &organizations.ListRootsInput{})
	if err != nil {
		return info, fmt.Errorf("list roots: %w", err)
	}
	if len(roots.Roots) == 0 {
		return info, errors.New("organization has no root")
	}
	info.RootID = aws.ToString(roots.Roots[0].Id)

	ouID, err := p.findOU(ctx, info.RootID, p.cfg.PlaygroundOUName)
	if err != nil {
		return info, err
	}
	if ouID == "" {
		out, err := p.orgs.CreateOrganizationalUnit(ctx, &organizations.CreateOrganizationalUnitInput{
			ParentId: aws.String(info.RootID),
			Name:     aws.String(p.cfg.PlaygroundOUName),
		})
		if err != nil {
			return info, fmt.Errorf("create OU %q: %w", p.cfg.PlaygroundOUName, err)
		}
		ouID = aws.ToString(out.OrganizationalUnit.Id)
	}
	info.PlaygroundOUID = ouID

	p.info = &info
	return info, nil
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
	info, err := p.EnsureOrganization(ctx)
	if err != nil {
		return nil, err
	}
	var out []core.RemoteAccount
	pag := organizations.NewListAccountsForParentPaginator(p.orgs,
		&organizations.ListAccountsForParentInput{ParentId: aws.String(info.PlaygroundOUID)})
	for pag.HasMorePages() {
		page, err := pag.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list playground accounts: %w", err)
		}
		for _, a := range page.Accounts {
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

func (p *Provider) PlaceAccount(ctx context.Context, providerID string, tags map[string]string) error {
	info, err := p.EnsureOrganization(ctx)
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
	src := aws.ToString(parents.Parents[0].Id)
	if src != info.PlaygroundOUID {
		_, err := p.orgs.MoveAccount(ctx, &organizations.MoveAccountInput{
			AccountId:           aws.String(providerID),
			SourceParentId:      aws.String(src),
			DestinationParentId: aws.String(info.PlaygroundOUID),
		})
		if err != nil {
			return fmt.Errorf("move account: %w", err)
		}
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
	info, err := p.EnsureOrganization(ctx)
	if err != nil {
		return nil, err
	}
	return p.tagsFor(ctx, info.PlaygroundOUID)
}

func (p *Provider) SetOUTags(ctx context.Context, tags map[string]string) error {
	info, err := p.EnsureOrganization(ctx)
	if err != nil {
		return err
	}
	return p.SetTags(ctx, info.PlaygroundOUID, tags)
}

func (p *Provider) RemoveOUTags(ctx context.Context, keys []string) error {
	info, err := p.EnsureOrganization(ctx)
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

// EnsureBudget writes a monthly cost budget in the management account,
// filtered to the linked account, with alerts at 80% actual and 100% forecast.
func (p *Provider) EnsureBudget(ctx context.Context, providerID string, limitUSD float64, notifyEmail string) error {
	info, err := p.EnsureOrganization(ctx)
	if err != nil {
		return err
	}
	name := "playplace-" + providerID
	budget := budgettypes.Budget{
		BudgetName:  aws.String(name),
		BudgetType:  budgettypes.BudgetTypeCost,
		TimeUnit:    budgettypes.TimeUnitMonthly,
		BudgetLimit: &budgettypes.Spend{Amount: aws.String(strconv.FormatFloat(limitUSD, 'f', 2, 64)), Unit: aws.String("USD")},
		CostFilters: map[string][]string{"LinkedAccount": {providerID}},
	}

	_, err = p.budgets.DescribeBudget(ctx, &budgets.DescribeBudgetInput{
		AccountId:  aws.String(info.ManagementAccountID),
		BudgetName: aws.String(name),
	})
	var notFound *budgettypes.NotFoundException
	if err == nil {
		_, err = p.budgets.UpdateBudget(ctx, &budgets.UpdateBudgetInput{
			AccountId: aws.String(info.ManagementAccountID),
			NewBudget: &budget,
		})
		if err != nil {
			return fmt.Errorf("update budget: %w", err)
		}
		return nil
	}
	if !errors.As(err, &notFound) {
		return fmt.Errorf("describe budget: %w", err)
	}

	in := &budgets.CreateBudgetInput{
		AccountId: aws.String(info.ManagementAccountID),
		Budget:    &budget,
	}
	if notifyEmail != "" {
		sub := []budgettypes.Subscriber{{SubscriptionType: budgettypes.SubscriptionTypeEmail, Address: aws.String(notifyEmail)}}
		in.NotificationsWithSubscribers = []budgettypes.NotificationWithSubscribers{
			{
				Notification: &budgettypes.Notification{
					NotificationType:   budgettypes.NotificationTypeActual,
					ComparisonOperator: budgettypes.ComparisonOperatorGreaterThan,
					Threshold:          80,
					ThresholdType:      budgettypes.ThresholdTypePercentage,
				},
				Subscribers: sub,
			},
			{
				Notification: &budgettypes.Notification{
					NotificationType:   budgettypes.NotificationTypeForecasted,
					ComparisonOperator: budgettypes.ComparisonOperatorGreaterThan,
					Threshold:          100,
					ThresholdType:      budgettypes.ThresholdTypePercentage,
				},
				Subscribers: sub,
			},
		}
	}
	if _, err := p.budgets.CreateBudget(ctx, in); err != nil {
		return fmt.Errorf("create budget: %w", err)
	}
	return nil
}

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
		Status:     string(a.Status),
		Tags:       tags,
	}
	if a.JoinedTimestamp != nil {
		r.JoinedAt = *a.JoinedTimestamp
	}
	return r
}

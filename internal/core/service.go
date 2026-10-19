package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config tunes lifecycle behaviour.
type Config struct {
	DefaultTTL       time.Duration // how long a new account lives
	WarnBefore       time.Duration // how far ahead of expiry to send the expiring notice
	CloseAlertAfter  time.Duration // unconfirmed closure age that makes reconciliation fail
	DefaultBudgetUSD float64       // monthly budget when none is given
	EmailPattern     string        // e.g. "aws+pp-{name}@example.com"
	AlertEmail       string        // recipient for provider budget alerts
	BudgetPolicyID   string        // separately deployed provisioning restriction SCP
	BudgetActionRole string        // separately deployed AWS Budgets execution role

	InventoryTTL    time.Duration // how long a listed inventory is reused
	CostTTL         time.Duration // how long pulled costs are reused
	FailedRetention time.Duration // how long failed creations stay visible

	MaxTTL            time.Duration // longest lifetime a request may ask for; admins may override
	MaxBudgetUSD      float64       // largest monthly budget a request may ask for; admins may override
	MaxPerOwner       int           // open accounts plus pending requests one person may hold
	RequestTTL        time.Duration // pending requests older than this are dropped
	AllowSelfApproval bool          // let a requester approve their own request
	BaseURL           string        // web UI address for links in notifications
}

// DefaultConfig returns sane defaults for a small engineering org.
func DefaultConfig() Config {
	return Config{
		DefaultTTL:       14 * 24 * time.Hour,
		WarnBefore:       3 * 24 * time.Hour,
		CloseAlertAfter:  24 * time.Hour,
		DefaultBudgetUSD: 50,
		EmailPattern:     "aws+pp-{name}@example.com",
		InventoryTTL:     30 * time.Second,
		CostTTL:          time.Hour,
		FailedRetention:  7 * 24 * time.Hour,
		MaxTTL:           90 * 24 * time.Hour,
		MaxBudgetUSD:     500,
		MaxPerOwner:      1,
		RequestTTL:       7 * 24 * time.Hour,
	}
}

// Service drives account lifecycle against the provider. It keeps only
// short-lived caches; the provider is the source of truth.
type Service struct {
	provider Provider
	notifier Notifier
	auditor  Auditor
	cfg      Config
	log      *slog.Logger
	Now      func() time.Time

	mu        sync.Mutex
	inv       []*Account
	recs      []Request // request records read with the inventory, same age
	invAt     time.Time
	costs     map[string]costEntry
	costPulls uint64 // numbers each cost pull, so only the newest pull writes

	// opMu serializes refresh, placement, budget changes, close requests and
	// request-queue mutations within this process, so the worker, web UI, Slack and
	// TUI cannot run the same pass twice or approve the same request twice.
	// Separate processes are not covered; AWS tags have no compare-and-swap.
	opMu sync.Mutex
}

// costEntry is the cost state of one account. at and err describe the last
// pull. points and okAt come from the last pull that succeeded.
type costEntry struct {
	points []CostPoint
	okAt   time.Time // when the last good pull ran, zero if none did
	at     time.Time // when the last pull ran
	err    error     // why the last pull failed, nil after a good pull
	seq    uint64    // the number of the pull that wrote the entry
}

// NewService wires a Service. A nil logger disables logging.
func NewService(provider Provider, notifier Notifier, cfg Config, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{provider: provider, notifier: notifier, auditor: DiscardAuditor{}, cfg: cfg, log: log, Now: time.Now, costs: map[string]costEntry{}}
}

// SetLogger swaps the logger. The TUI uses it to turn warnings into
// notices instead of letting them print over the screen.
func (s *Service) SetLogger(log *slog.Logger) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s.log = log
}

// SetAuditor installs the history sink. Nil restores the discard sink.
func (s *Service) SetAuditor(a Auditor) {
	if a == nil {
		a = DiscardAuditor{}
	}
	s.auditor = a
}

// audit records one event, best effort.
func (s *Service) audit(ctx context.Context, event string, a *Account, actor, via, msg string, details map[string]string) {
	e := AuditEvent{ID: NewHistoryID(), At: s.Now().UTC(), Event: event, Actor: actor, Via: via, Message: msg, Details: details}
	if a != nil {
		e.Account, e.AccountID, e.Owner, e.JourneyID = a.Name, a.ProviderID, a.Owner, a.HistoryID()
	}
	if err := s.auditor.Record(ctx, e); err != nil {
		s.log.Warn("history not written", "event", event, "account", e.Account, "err", err)
	}
}

// Config exposes the effective configuration.
func (s *Service) Config() Config { return s.cfg }

// SetBaseURL records the web UI address for links in notifications.
func (s *Service) SetBaseURL(u string) { s.cfg.BaseURL = u }

// Init makes sure the organization and playground OU exist.
func (s *Service) Init(ctx context.Context) (OrgInfo, error) {
	info, err := s.provider.EnsureOrganization(ctx)
	return info, providerError("", err)
}

// Forecast holds the active accounts in the playground OU and the creations
// that wait in the organization root, with what the next refresh does to
// them, as it reads them. init prints it, so that an operator who names the
// wrong OU sees which accounts playplace takes over, closes or moves before
// it does. It leaves out the accounts in the OU that are not active: AWS is
// already closing most of them, and the refresh only tracks that closure.
type Forecast struct {
	// Unmanaged are the active accounts in the playground OU without the
	// playplace:managed=true tag. The refresh adopts each of them, unless the
	// account has close intent (its closure starts at once) or damaged tags
	// (it is left alone). An adopted account keeps each valid playplace
	// owner, expires and budget tag. Without them, it gets the owner
	// "unknown", the default lifetime or the default budget.
	Unmanaged []*Account
	// Managed are the active accounts in the playground OU with the
	// playplace:managed=true tag. The refresh resumes their management, and
	// it starts the closure of each one that expired or has close intent. An
	// operator who quarantined an account in another OU sees it here when
	// that OU is named by mistake.
	Managed []*Account
	// Waiting are the creations of playplace that wait in the organization
	// root. The refresh moves each of them into the playground OU. It places
	// an account only once, so it never moves one to another OU later. One
	// that is PENDING_ACTIVATION moves on the first refresh after AWS
	// activates it.
	Waiting []*Account
	// Blocked are creations of playplace in the organization root with
	// damaged tags. The refresh leaves them there, and reports the damage on
	// each pass until someone repairs the tags.
	Blocked []*Account
}

// Forecast reads the active accounts in the playground OU and the creations
// that wait in the organization root. It changes nothing.
func (s *Service) Forecast(ctx context.Context) (Forecast, error) {
	var f Forecast
	remote, err := s.provider.ListPlaygroundAccounts(ctx)
	if err != nil {
		return f, providerError("list playground accounts", err)
	}
	root, err := s.provider.ListRootAccounts(ctx)
	if err != nil {
		return f, providerError("list root accounts", err)
	}
	now := s.Now()
	inOU := map[string]bool{}
	for _, r := range remote {
		inOU[r.ProviderID] = true
		switch {
		case r.Status != "ACTIVE":
		case r.Tags[TagManaged] == "true":
			f.Managed = append(f.Managed, FromRemote(r, s.cfg, now))
		default:
			f.Unmanaged = append(f.Unmanaged, FromRemote(r, s.cfg, now))
		}
	}
	// The same checks as the first step of Refresh and place. A closed or
	// closing account in the root is never placed, and one with damaged tags
	// stays there.
	for _, r := range root {
		if inOU[r.ProviderID] || !awaitingPlacement(r) || (r.Status != "ACTIVE" && r.Status != "PENDING_ACTIVATION") {
			continue
		}
		if a := FromRemote(r, s.cfg, now); a.RepairError() != nil {
			f.Blocked = append(f.Blocked, a)
		} else {
			f.Waiting = append(f.Waiting, a)
		}
	}
	for _, list := range [][]*Account{f.Unmanaged, f.Managed, f.Waiting, f.Blocked} {
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	}
	return f, nil
}

// Invalidate drops the cached inventory so the next read hits the provider.
func (s *Service) Invalidate() {
	s.mu.Lock()
	s.invAt = time.Time{}
	s.mu.Unlock()
}

// ---- inventory -------------------------------------------------------------

// Inventory lists every playground account: OU members, creations that are
// still in flight or failed recently, and creations that wait in the
// organization root for placement. The result is cached for InventoryTTL
// unless force is set.
func (s *Service) Inventory(ctx context.Context, force bool) ([]*Account, error) {
	s.mu.Lock()
	if !force && s.inv != nil && s.Now().Sub(s.invAt) < s.cfg.InventoryTTL {
		out := cloneAll(s.inv)
		s.mu.Unlock()
		return out, nil
	}
	s.mu.Unlock()

	remote, err := s.provider.ListPlaygroundAccounts(ctx)
	if err != nil {
		return nil, providerError("list playground accounts", err)
	}
	// The root listing only adds unplaced creations. Without it the OU and
	// the queue still list, and Refresh reports the same failure.
	root, err := s.provider.ListRootAccounts(ctx)
	if err != nil {
		s.log.Warn("unplaced creations not listed", "err", err)
		root = nil
	}
	reqs, err := s.provider.ListCreateRequests(ctx)
	if err != nil {
		return nil, providerError("list create requests", err)
	}
	records, err := s.allRequests(ctx)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	inOU := map[string]bool{}
	var out []*Account
	for _, r := range remote {
		inOU[r.ProviderID] = true
		out = append(out, FromRemote(r, s.cfg, now))
	}
	inRoot := map[string]bool{}
	unplaced := map[string]RemoteAccount{}
	for _, r := range root {
		inRoot[r.ProviderID] = true
		if !inOU[r.ProviderID] && awaitingPlacement(r) {
			unplaced[r.ProviderID] = r
		}
	}
	approved := map[string]Request{} // create request id -> approved record
	for _, r := range records {
		if r.Approved {
			approved[r.CreateRequestID] = r
		} else {
			out = append(out, pendingAccount(r, now))
		}
	}
	// Creations know only the name at the provider; the approved record
	// supplies owner, budget, purpose, and expiry until placement.
	derive := func(cr CreateRequest) *Account {
		a := FromCreateRequest(cr)
		if rec, ok := approved[cr.RequestID]; ok {
			rec.fill(a)
		}
		return a
	}
	for _, r := range reqs {
		switch r.State {
		case CreateInProgress:
			out = append(out, derive(r))
		case CreateFailed:
			if now.Sub(r.CompletedAt) <= s.cfg.FailedRetention {
				out = append(out, derive(r))
			}
		case CreateSucceeded:
			if u, ok := unplaced[r.ProviderID]; ok {
				out = append(out, s.unplacedAccount(u, &r, now))
				delete(unplaced, r.ProviderID)
			} else if !inOU[r.ProviderID] && !inRoot[r.ProviderID] && now.Sub(r.CompletedAt) <= placementGrace {
				// AWS can list a new account a little after it reports the
				// creation done, and the root listing can fail. Show the
				// creation meanwhile so that its name stays taken.
				a := derive(r)
				a.ProviderID = r.ProviderID
				out = append(out, a)
			}
		}
	}
	// AWS stops listing old creation requests; the account still waits.
	for _, u := range unplaced {
		out = append(out, s.unplacedAccount(u, nil, now))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })

	s.mu.Lock()
	s.inv, s.recs, s.invAt = cloneAll(out), records, now
	s.mu.Unlock()
	return out, nil
}

// ListFilter narrows List.
type ListFilter struct {
	Statuses      []Status
	Owner         string
	IncludeClosed bool
}

// List returns accounts matching the filter from the (cached) inventory.
func (s *Service) List(ctx context.Context, f ListFilter) ([]*Account, error) {
	all, err := s.Inventory(ctx, false)
	if err != nil {
		return nil, err
	}
	var out []*Account
	for _, a := range all {
		if len(f.Statuses) > 0 {
			ok := false
			for _, st := range f.Statuses {
				if a.Status == st {
					ok = true
				}
			}
			if !ok {
				continue
			}
		} else if !f.IncludeClosed && a.Status == StatusClosed {
			continue
		}
		if f.Owner != "" && a.Owner != f.Owner {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

// Get returns one account by id (provider id or request id).
func (s *Service) Get(ctx context.Context, id string) (*Account, error) {
	all, err := s.Inventory(ctx, false)
	if err != nil {
		return nil, err
	}
	for _, a := range all {
		if a.ID == id {
			return a, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// ErrNotFound is returned when a lookup matches nothing.
var ErrNotFound = errors.New("not found")

// Resolve finds an account by exact id, then by name among open accounts,
// then by id prefix. It errors if a prefix is ambiguous. An exact id wins
// over every name: a name may look like another account's id, and the
// newer row must not take that account's place.
func (s *Service) Resolve(ctx context.Context, ref string) (*Account, error) {
	all, err := s.Inventory(ctx, false)
	if err != nil {
		return nil, err
	}
	for _, a := range all {
		if a.ID == ref {
			return a, nil
		}
	}
	var matches []*Account
	for _, a := range all {
		if a.Name == ref && a.Status.IsOpen() {
			return a, nil
		}
		if strings.HasPrefix(a.ID, ref) {
			matches = append(matches, a)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("%w: %s", ErrNotFound, ref)
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf("%q matches %d accounts, use a longer id", ref, len(matches))
	}
}

// ---- lifecycle -------------------------------------------------------------

// RequestInput is what a human supplies to get a new account.
type RequestInput struct {
	Name        string
	Owner       string
	Email       string        // optional, derived from EmailPattern when empty
	TTL         time.Duration // optional, DefaultTTL when zero
	BudgetUSD   float64       // optional, DefaultBudgetUSD when zero
	Purpose     string        // optional, short free text kept as a tag
	RequestedBy string        // who asked; defaults to Owner
	ApprovedBy  string        // who approved; recorded as a tag

	// OverrideLimits lets an admin exceed MaxTTL and MaxBudgetUSD. Callers
	// must only set it for admins.
	OverrideLimits bool

	journeyID string // carried by Approve; callers cannot choose a different request's identity
	viaQueue  bool   // set by Approve so Request does not log a direct create
}

// validateFreeText checks the fields that end up in tags against AWS's rules.
func validateFreeText(in RequestInput) error {
	if err := ValidateTagValue("owner", in.Owner); err != nil {
		return err
	}
	if err := ValidatePurpose(in.Purpose); err != nil {
		return err
	}
	for field, v := range map[string]string{"requested by": in.RequestedBy, "approved by": in.ApprovedBy} {
		if err := ValidateTagValue(field, v); err != nil {
			return err
		}
	}
	return nil
}

// checkBudget refuses values that are not real numbers. strconv.ParseFloat
// and pflag accept "NaN" and "Inf", which would pass the ceiling check
// (NaN compares false with everything) and then be written to a tag.
func checkBudget(v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return errors.New("budget must be a number")
	}
	if v != 0 && !validMoney(v) {
		return errors.New("budget must be positive USD with at most two decimal places")
	}
	return nil
}

// checkLimits enforces the TTL and budget ceilings unless overridden. The
// refusal shows both sides as the forms and the approvers see them. Lifetime
// rounds up to the hour, so a lifetime over a ceiling in whole hours never
// reads as equal to it.
func (s *Service) checkLimits(in RequestInput) error {
	if err := checkBudget(in.BudgetUSD); err != nil {
		return err
	}
	if in.OverrideLimits {
		return nil
	}
	if s.cfg.MaxTTL > 0 && in.TTL > s.cfg.MaxTTL {
		return fmt.Errorf("lifetime %s exceeds the maximum of %s; an admin can override", Lifetime(in.TTL), Lifetime(s.cfg.MaxTTL))
	}
	if s.cfg.MaxBudgetUSD > 0 && in.BudgetUSD > s.cfg.MaxBudgetUSD {
		return fmt.Errorf("budget %s exceeds the maximum of %s; an admin can override", Dollars(in.BudgetUSD), Dollars(s.cfg.MaxBudgetUSD))
	}
	return nil
}

// Request asks the provider to create an account. Owner, expiry, and budget
// travel as tags on the request and land on the account when it exists.
func (s *Service) Request(ctx context.Context, in RequestInput) (*Account, error) {
	if err := ValidateName(in.Name); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Owner) == "" {
		return nil, errors.New("owner is required")
	}
	if in.TTL <= 0 {
		in.TTL = s.cfg.DefaultTTL
	}
	if in.BudgetUSD <= 0 {
		in.BudgetUSD = s.cfg.DefaultBudgetUSD
	}
	if in.Email == "" {
		in.Email = strings.ReplaceAll(s.cfg.EmailPattern, "{name}", in.Name)
	}
	if !strings.Contains(in.Email, "@") {
		return nil, fmt.Errorf("email %q is not valid", in.Email)
	}
	if err := s.checkLimits(in); err != nil {
		return nil, err
	}
	if err := validateFreeText(in); err != nil {
		return nil, err
	}
	if s.provider.AccessEnabled() {
		user, err := s.provider.LookupUser(ctx, in.Owner)
		if errors.Is(err, ErrUserNotFound) {
			return nil, fmt.Errorf("owner %q is not a user in the identity store; use the engineer's email or user name", in.Owner)
		}
		if err != nil {
			return nil, providerError("look up owner", err)
		}
		in.Owner = user.Identity()
	}
	all, err := s.Inventory(ctx, true)
	if err != nil {
		return nil, err
	}
	if err := s.checkNameFree(all, in.Name, in.Email, in.journeyID); err != nil {
		return nil, err
	}

	if in.RequestedBy == "" {
		in.RequestedBy = in.Owner
	}
	now := s.Now()
	if in.journeyID == "" {
		in.journeyID = NewHistoryID()
	}
	a := &Account{
		JourneyID:   in.journeyID,
		Name:        in.Name,
		Email:       in.Email,
		Owner:       in.Owner,
		Status:      StatusCreating,
		BudgetUSD:   in.BudgetUSD,
		CreatedAt:   now,
		ExpiresAt:   now.Add(in.TTL),
		Managed:     true,
		RequestedBy: in.RequestedBy,
		ApprovedBy:  in.ApprovedBy,
		Purpose:     strings.TrimSpace(in.Purpose),
		handoff:     handoffPlacing,
	}
	reqID, err := s.provider.RequestAccount(ctx, AccountRequest{Name: a.Name, Email: a.Email, Tags: a.Tags()})
	if err != nil {
		return nil, providerError("request account", err)
	}
	a.ID, a.RequestID = reqID, reqID
	s.Invalidate()
	s.log.Info("requested", "account", a.Name, "request", reqID)
	if in.ApprovedBy != "" && in.ApprovedBy == in.RequestedBy && !in.viaQueue {
		s.audit(ctx, "created", a, in.ApprovedBy, "cli", fmt.Sprintf("%s created %s directly for %s: %s/month, %s", in.ApprovedBy, a.Name, a.Owner, Dollars(a.BudgetUSD), Lifetime(in.TTL)),
			map[string]string{"request_id": reqID, "budget": strconv.FormatFloat(a.BudgetUSD, 'f', -1, 64), "days": strconv.Itoa(int(in.TTL.Hours() / 24))})
	}
	return a, nil
}

// checkNameFree refuses a name that an open account already uses. It also
// refuses the name of a closed account when the root email would be derived
// from it, because AWS never frees an account's root email, so the creation
// would be accepted and then fail. A pending request holds its name too,
// except against its own approval: journeyID names the request being
// approved, and is empty for a direct creation or a new request. Approve
// marks the record approved first, so the exception only covers a read that
// still sees the old pending record. Failed creations do not hold a name.
func (s *Service) checkNameFree(all []*Account, name, email, journeyID string) error {
	derived := strings.ReplaceAll(s.cfg.EmailPattern, "{name}", name)
	for _, a := range all {
		if a.Name != name {
			continue
		}
		switch {
		case a.Status == StatusFailed:
			continue
		case a.Status == StatusPending:
			if journeyID != "" && a.JourneyID == journeyID {
				continue
			}
			return fmt.Errorf("a request named %s is waiting for approval; approve, deny, or withdraw it first", name)
		case a.Status == StatusClosed:
			if email == "" || email == derived || email == a.Email {
				return fmt.Errorf("a closed account named %s used root email %s, and AWS never frees an email; pick another name", name, a.Email)
			}
		default:
			return fmt.Errorf("an open account named %s already exists (%s)", name, a.Status)
		}
	}
	return nil
}

// CreatedError is the error of PollCreate when AWS created the account and a
// later step failed. Account names the account and has its provider ID.
// Placed reports whether playplace finished the placement of the account in
// the Playground OU. Err is the failure of the step.
type CreatedError struct {
	Account *Account
	Placed  bool
	Err     error
}

func (e *CreatedError) Error() string { return e.Err.Error() }

func (e *CreatedError) Unwrap() error { return e.Err }

// PollCreate checks one creation request and, on success, places the new
// account in the OU with its tags and budget. Loop on it to wait in place.
// When a step after the creation fails, the error is a *CreatedError.
//
// started is the account that Request or Approve returned for this
// creation, or nil. Creation status has no tags, so when no approved record
// names the creation, as for a direct creation, a failure takes its journey
// and owner from started. Such a failure is reported on every call that
// sees it; a wait makes one.
func (s *Service) PollCreate(ctx context.Context, requestID string, started *Account) (*Account, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	r, err := s.provider.CreateStatus(ctx, requestID)
	if err != nil {
		return nil, providerError("", err)
	}
	switch r.State {
	case CreateSucceeded:
		remote, err := s.provider.GetAccount(ctx, r.ProviderID)
		if err != nil {
			a := FromCreateRequest(r)
			a.ProviderID = r.ProviderID
			return nil, &CreatedError{Account: a, Err: providerError("", err)}
		}
		now := s.Now()
		if remote.Status == "PENDING_ACTIVATION" && awaitingPlacement(remote) {
			// AWS can report the creation done before it activates the
			// account. The poll shows the account as the inventory does, so
			// within placementGrace a wait keeps polling.
			return s.unplacedAccount(remote, &r, now), nil
		}
		// Polling an old creation must not move or prepare an unavailable or
		// closing account. Refresh owns closure progression.
		a := FromRemote(remote, s.cfg, now)
		if remote.Status != "ACTIVE" || a.CloseRequestedAt != nil {
			return a, nil
		}
		if err := a.RepairError(); err != nil {
			return a, &CreatedError{Account: a, Placed: a.Managed && !awaitingPlacement(remote), Err: err}
		}
		// Only a creation that was never placed is placed. Afterwards Refresh
		// owns the account, and one moved out of the OU is never moved back.
		if !awaitingPlacement(remote) {
			if !a.Managed {
				// For example an emulator that drops the CreateAccount tags.
				// Reporting it as active would hide that nothing places it.
				return a, &CreatedError{Account: a, Err: fmt.Errorf("account %s (%s) has no playplace creation tags, so playplace does not place or manage it", a.Name, a.ProviderID)}
			}
			return a, nil
		}
		if _, err := s.place(ctx, a); err != nil {
			return nil, &CreatedError{Account: a, Err: err}
		}
		// Another process can have placed the account and finished its
		// handoff since the read above. Readiness must see the live handoff
		// tag, as in Refresh.
		fresh, err := s.provider.GetAccount(ctx, a.ProviderID)
		if err != nil {
			return a, &CreatedError{Account: a, Placed: true, Err: providerError("read "+a.Name+" after placement", err)}
		}
		a = FromRemote(fresh, s.cfg, s.Now())
		if err := s.reconcileReadiness(ctx, a); err != nil {
			return a, &CreatedError{Account: a, Placed: true, Err: err}
		}
		return a, nil
	case CreateFailed:
		// Without the queue, a reported failure cannot be told from a new one.
		records, err := s.allRequests(ctx)
		if err != nil {
			return nil, err
		}
		a := FromCreateRequest(r)
		for _, rec := range records {
			if rec.Approved && rec.CreateRequestID == r.RequestID {
				rec.fill(a)
				if rec.FailureReported {
					return a, nil
				}
				return a, s.reportCreationFailure(ctx, rec, r)
			}
		}
		if started != nil && started.RequestID == r.RequestID {
			a.JourneyID, a.Owner = started.JourneyID, started.Owner
		}
		s.announceCreationFailure(ctx, a, r)
		return a, nil
	default:
		return s.creationAccount(ctx, r), nil
	}
}

// Creation status has no tags. Only the exact provider request ID may link it
// to a queued journey; a reused name is never sufficient evidence.
func (s *Service) creationAccount(ctx context.Context, cr CreateRequest) *Account {
	a := FromCreateRequest(cr)
	records, err := s.allRequests(ctx)
	if err != nil {
		s.log.Warn("creation history identity unavailable", "request", cr.RequestID, "err", err)
		return a
	}
	for _, r := range records {
		if r.Approved && r.CreateRequestID == cr.RequestID {
			r.fill(a)
			break
		}
	}
	return a
}

// placementGrace is how long a finished creation may wait in the
// organization root before the inventory and Refresh call it stuck.
const placementGrace = time.Hour

// awaitingPlacement reports whether a playplace creation was never placed.
// Request writes handoff=placing into the creation tags and readiness
// replaces it after the move, so a placed account never matches again,
// wherever someone moves it later. Legacy accounts without the tag do not
// match either.
func awaitingPlacement(r RemoteAccount) bool {
	return r.Tags[TagManaged] == "true" && r.Tags[TagHandoff] == handoffPlacing
}

// unplacedAccount shows a creation that AWS finished and that still waits in
// the organization root. Until placement it has no budget, no OU guardrails
// and no expiry handling. cr links it to its creation request when AWS
// still lists that.
func (s *Service) unplacedAccount(r RemoteAccount, cr *CreateRequest, now time.Time) *Account {
	a := FromRemote(r, s.cfg, now)
	if cr != nil {
		a.ID, a.RequestID, a.CreatedAt = cr.RequestID, cr.RequestID, cr.RequestedAt
	}
	if r.Status == "PENDING_ACTIVATION" && a.CloseRequestedAt == nil && now.Sub(r.JoinedAt) <= placementGrace {
		// AWS activates a new account within minutes, and Refresh does not
		// report this state before the grace ends.
		a.Status, a.LastError = StatusCreating, ""
		return a
	}
	if a.Status != StatusActive && a.Status != StatusExpiring {
		return a // closed, closing or unavailable says more than creating
	}
	a.Status = StatusCreating
	if now.Sub(r.JoinedAt) > placementGrace {
		// The inventory cannot tell whether a reconcile ran since, for
		// example with a slow reconcile schedule, so this does not claim
		// that placement failed.
		a.LastError = fmt.Sprintf("AWS created this account on %s. It waits outside the Playground OU for the next reconcile. Until then it has no budget, OU guardrail, or expiry. If this message stays, check the reconcile errors.",
			r.JoinedAt.UTC().Format("2006-01-02 15:04 MST"))
	}
	return a
}

// place moves a freshly created account into the OU and writes tags.
// Readiness is reconciled separately, including after a partial move failure.
func (s *Service) place(ctx context.Context, a *Account) (*Account, error) {
	if err := a.RepairError(); err != nil {
		return a, err
	}
	defer s.Invalidate() // a failed tag write may still have moved the account
	// The creation tags already say placing, and readiness owns the marker
	// from here. Writing it again from a stale listing would undo a handoff
	// that another process finished, and repeat the placed and ready notices.
	tags := a.Tags()
	delete(tags, TagHandoff)
	if err := s.provider.PlaceAccount(ctx, a.ProviderID, tags); err != nil {
		return nil, providerError("place "+a.Name, err)
	}
	a.Managed = true
	s.dropApprovedRecord(ctx, a)
	return a, nil
}

// dropApprovedRecord removes the approved request record of a placed
// account; it has done its job. Only the record of this account's journey
// goes: a record under the same name may be someone else's pending request,
// and a name never joins journeys. A record left behind is dropped by
// expireRequests a day after the creation succeeded.
func (s *Service) dropApprovedRecord(ctx context.Context, a *Account) {
	if a.JourneyID == "" {
		return
	}
	records, err := s.allRequests(ctx)
	if err != nil {
		s.log.Warn("approved record not removed", "account", a.Name, "err", err)
		return
	}
	for _, r := range records {
		if r.Name != a.Name || !r.Approved || r.JourneyID != a.JourneyID {
			continue
		}
		if err := s.provider.RemoveOUTags(ctx, []string{r.Key()}); err != nil {
			s.log.Warn("approved record not removed", "account", a.Name, "err", err)
		}
		return
	}
}

// grantAccess gives the owner console access and records the principal in a
// tag so Refresh can tell granted accounts from ones still waiting.
func (s *Service) grantAccess(ctx context.Context, a *Account) error {
	if !s.provider.AccessEnabled() || a.AccessGrantedTo != "" || a.ProviderID == "" {
		return nil
	}
	user, err := s.provider.LookupUser(ctx, a.Owner)
	if err != nil {
		return providerError("", err)
	}
	if err := s.provider.GrantAccess(ctx, a.ProviderID, user); err != nil {
		return providerError("", err)
	}
	if err := s.provider.SetTags(ctx, a.ProviderID, map[string]string{TagAccess: user.ID}); err != nil {
		return providerError("", err)
	}
	a.AccessGrantedTo = user.ID
	s.log.Info("access granted", "account", a.Name, "owner", user.Identity())
	s.audit(ctx, "access-granted", a, ActorSystem, "", fmt.Sprintf("%s was given console access to %s", user.Identity(), a.Name), map[string]string{"principal": user.ID})
	return nil
}

// revokeAccess removes the owner's grant before an account is closed.
func (s *Service) revokeAccess(ctx context.Context, a *Account) error {
	if !s.provider.AccessEnabled() || a.AccessGrantedTo == "" {
		return nil
	}
	if err := s.provider.RevokeAccess(ctx, a.ProviderID, User{ID: a.AccessGrantedTo}); err != nil {
		return providerError("", err)
	}
	if err := s.provider.RemoveTags(ctx, a.ProviderID, []string{TagAccess}); err != nil {
		return providerError("", err)
	}
	a.AccessGrantedTo = ""
	return nil
}

// Extend pushes the expiry out and clears any warning. by names who did it.
// The account's whole lifetime, from creation to the new expiry, must stay
// within MaxTTL unless override is set, which callers may only do for admins.
// Without that rule, repeated extensions would walk around the request-time
// ceiling.
func (s *Service) Extend(ctx context.Context, id string, until time.Time, by string, override bool) (*Account, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.Invalidate()
	a, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !a.Status.CanExtend() {
		return nil, fmt.Errorf("account %s is %s and cannot be extended", a.Name, a.Status)
	}
	if err := a.tagError(TagExpires, TagCloseRequested); err != nil {
		return nil, err
	}
	if !until.After(a.ExpiresAt) {
		return nil, fmt.Errorf("new expiry %s is not after current expiry %s", until.Format(time.RFC3339), a.ExpiresAt.Format(time.RFC3339))
	}
	if err := s.checkLifetime(a, until, override); err != nil {
		return nil, err
	}
	a.ExpiresAt = until
	a.WarnedAt = nil
	if err := s.provider.SetTags(ctx, a.ProviderID, map[string]string{TagExpires: until.UTC().Format(time.RFC3339)}); err != nil {
		return nil, providerError("tag "+a.Name, err)
	}
	if err := s.provider.RemoveTags(ctx, a.ProviderID, []string{TagWarnedAt}); err != nil {
		return nil, providerError("untag "+a.Name, err)
	}
	a.Status = StatusActive
	s.Invalidate()
	s.log.Info("extended", "account", a.Name, "until", until)
	s.audit(ctx, "extended", a, by, "", fmt.Sprintf("%s extended %s to %s", by, a.Name, until.Format("2006-01-02")),
		map[string]string{"expires": until.UTC().Format(time.RFC3339), "override": strconv.FormatBool(override)})
	return a, nil
}

// checkLifetime refuses an expiry that would give the account a lifetime
// longer than MaxTTL, counted from when it was created. Adopted accounts
// count from when they joined the organization.
func (s *Service) checkLifetime(a *Account, until time.Time, override bool) error {
	if override || s.cfg.MaxTTL <= 0 {
		return nil
	}
	start := a.CreatedAt
	if start.IsZero() {
		start = s.Now()
	}
	limit := start.Add(s.cfg.MaxTTL)
	if until.After(limit) {
		return LifetimeLimitError{Name: a.Name, Lifetime: until.Sub(start), Max: s.cfg.MaxTTL, Latest: limit}
	}
	return nil
}

// LifetimeLimitError refuses an expiry that would give an account a lifetime
// past MaxTTL. Latest is the latest expiry that the limit allows. An
// interface finds the refusal with errors.As and can then choose its own
// words.
type LifetimeLimitError struct {
	Name     string
	Lifetime time.Duration
	Max      time.Duration
	Latest   time.Time
}

// Error is the text of the CLI and the TUI. Lifetime rounds up to the hour,
// so a lifetime just over the ceiling never reads as equal to it. The limit
// can fall on the requested day, so the text shows the time too.
func (e LifetimeLimitError) Error() string {
	return fmt.Sprintf("%s would live %s, past the ceiling of %s; the latest expiry is %s and an admin can override",
		e.Name, Lifetime(e.Lifetime), Lifetime(e.Max), e.Latest.UTC().Format("2006-01-02 15:04 UTC"))
}

// OwnerLimitError refuses a request whose owner already holds Limit open or
// pending accounts (MaxPerOwner). An interface finds the refusal with
// errors.As and can then choose its own words.
type OwnerLimitError struct {
	Owner string
	Held  int
	Limit int
}

// Error is the text of the CLI and the TUI.
func (e OwnerLimitError) Error() string {
	return fmt.Sprintf("%s already holds %d open or pending playground account(s); the limit is %d", e.Owner, e.Held, e.Limit)
}

// RequestClose records close intent as a tag and tries the close right away.
// If the provider's quota refuses, the account stays in closing and a later
// Refresh retries. by names who asked.
func (s *Service) RequestClose(ctx context.Context, id string, by string) (*Account, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	a, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !a.Status.CanClose() {
		return nil, fmt.Errorf("account %s is %s and cannot be closed", a.Name, a.Status)
	}
	now := s.Now()
	a.CloseRequestedAt = &now
	if err := s.provider.SetTags(ctx, a.ProviderID, map[string]string{TagCloseRequested: now.UTC().Format(time.RFC3339)}); err != nil {
		return nil, providerError("tag "+a.Name, err)
	}
	a.Status = StatusClosing
	s.Invalidate()
	s.audit(ctx, "close-requested", a, by, "", fmt.Sprintf("%s asked to close %s", by, a.Name), nil)
	return s.tryClose(ctx, a)
}

func (s *Service) tryClose(ctx context.Context, a *Account) (*Account, error) {
	// A successful CloseAccount response only acknowledges an asynchronous
	// request. Never report closed until a fresh provider read confirms it.
	a.Status = StatusClosing
	s.Invalidate()
	if err := s.revokeAccess(ctx, a); err != nil {
		// Revocation failing must not leave an expired account open; AWS
		// suspends the account anyway, which ends console access.
		s.log.Warn("access not revoked", "account", a.Name, "err", err)
	}
	err := s.provider.CloseAccount(ctx, a.ProviderID)
	if errors.Is(err, ErrCloseQuota) {
		s.log.Info("closure deferred", "account", a.Name)
		return a, nil
	}
	if err != nil {
		return a, providerError("close "+a.Name, err)
	}
	r, err := s.provider.GetAccount(ctx, a.ProviderID)
	if err != nil {
		return a, providerError("verify closure of "+a.Name, err)
	}
	requestedAt := a.CloseRequestedAt
	a = FromRemote(r, s.cfg, s.Now())
	if a.CloseRequestedAt == nil {
		a.CloseRequestedAt = requestedAt // tolerate an eventually consistent tag read
	}
	if a.Status == StatusClosed {
		return a, errors.Join(s.confirmClosed(ctx, a), s.retireBudget(ctx, a))
	}
	if a.Status == StatusUnavailable {
		return a, fmt.Errorf("verify closure of %s: %s", a.Name, a.LastError)
	}
	// Even an eventually consistent ACTIVE response is not permission to
	// extend or grant access again: the durable close intent still applies.
	a.Status = StatusClosing
	return a, nil
}

// confirmClosed records observation, not acceptance of the close request.
// The marker prevents repeated notifications across refreshes and restarts.
func (s *Service) confirmClosed(ctx context.Context, a *Account) error {
	if a.ClosedAt != nil {
		return nil
	}
	now := s.Now().UTC()
	if err := s.provider.SetTags(ctx, a.ProviderID, map[string]string{TagClosedAt: now.Format(time.RFC3339)}); err != nil {
		return providerError("record confirmed closure of "+a.Name, err)
	}
	a.ClosedAt = &now
	s.Invalidate()
	s.audit(ctx, "closed", a, ActorSystem, "", fmt.Sprintf("AWS reports %s (%s) CLOSED", a.Name, a.ProviderID), nil)
	s.notify(ctx, a, "Playground account closed",
		fmt.Sprintf("AWS reports account %s (%s) owned by %s CLOSED. Prior usage, commitments, subscriptions, and central logging may still incur charges.", a.Name, a.ProviderID, a.Owner))
	return nil
}

// checkClosureDelay keeps cron failing and the worker logging until closure
// is observed. The notification is one-shot; tags keep it so across restarts.
func (s *Service) checkClosureDelay(ctx context.Context, a *Account) error {
	if a.Status == StatusClosed || a.CloseRequestedAt == nil || s.cfg.CloseAlertAfter <= 0 || s.Now().Sub(*a.CloseRequestedAt) < s.cfg.CloseAlertAfter {
		return nil
	}
	msg := fmt.Sprintf("closure of %s (%s) unconfirmed since %s (provider state %q); charges may continue; investigate AWS closure restrictions and run reconcile again",
		a.Name, a.ProviderID, a.CloseRequestedAt.UTC().Format(time.RFC3339), a.ProviderState)
	s.log.Error("closure overdue", "account", a.Name, "account_id", a.ProviderID, "since", *a.CloseRequestedAt, "provider_state", a.ProviderState)
	if a.CloseAlertedAt == nil {
		now := s.Now().UTC()
		if err := s.provider.SetTags(ctx, a.ProviderID, map[string]string{TagCloseAlertedAt: now.Format(time.RFC3339)}); err != nil {
			return errors.Join(errors.New(msg), providerError("record closure alert", err))
		}
		a.CloseAlertedAt = &now
		s.audit(ctx, "closure-overdue", a, ActorSystem, "", msg, nil)
		s.notify(ctx, a, "Playground account closure overdue", msg)
	}
	return errors.New(msg)
}

// Summary reports what a Refresh changed.
type Summary struct {
	Placed   []string // creations that landed and were moved into the OU
	Adopted  []string // OU accounts that lacked our tags and got defaults
	Granted  []string // owners given console access
	Warned   []string // accounts that got the expiring notice
	Closed   []string // closed at the provider
	Deferred []string // closure is not yet confirmed (quota, processing, or failure)
	Overdue  []string // unconfirmed closures older than CloseAlertAfter
	Expired  []string // pending requests dropped for lack of approval
}

// Empty reports whether nothing changed.
func (s Summary) Empty() bool {
	return len(s.Placed)+len(s.Adopted)+len(s.Granted)+len(s.Warned)+len(s.Closed)+len(s.Deferred)+len(s.Overdue)+len(s.Expired) == 0
}

func (s Summary) String() string {
	var parts []string
	add := func(label string, names []string) {
		if len(names) > 0 {
			parts = append(parts, fmt.Sprintf("%s %s", label, strings.Join(names, ", ")))
		}
	}
	add("placed", s.Placed)
	add("adopted", s.Adopted)
	add("granted access", s.Granted)
	add("warned", s.Warned)
	add("closed", s.Closed)
	add("awaiting closure", s.Deferred)
	add("closure overdue", s.Overdue)
	add("expired requests", s.Expired)
	return strings.Join(parts, "; ")
}

// Refresh is the reconciliation pass every command runs first. It finishes
// creations that landed, adopts untagged OU accounts, retries requested
// closes, sends the expiring notice ahead of expiry, and closes expired
// accounts.
func (s *Service) Refresh(ctx context.Context) (Summary, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	var sum Summary
	var errs []error
	now := s.Now()

	remote, err := s.provider.ListPlaygroundAccounts(ctx)
	if err != nil {
		return sum, providerError("list playground accounts", err)
	}
	inOU := map[string]bool{}
	for _, r := range remote {
		inOU[r.ProviderID] = true
	}

	// 1. Creations that AWS finished but that were never placed (the
	//    requester did not wait, was interrupted, or placement failed). They
	//    wait in the organization root for as long as it takes; an account
	//    that was placed once is never moved back, wherever it is now.
	root, err := s.provider.ListRootAccounts(ctx)
	if err != nil {
		errs = append(errs, providerError("list root accounts", err))
	}
	for _, acct := range root {
		if inOU[acct.ProviderID] || !awaitingPlacement(acct) {
			continue // someone else's account, or placed and moved out since
		}
		if acct.Status != "ACTIVE" {
			// PENDING_ACTIVATION passes quickly; a closed or closing account
			// has nothing left to place.
			if acct.Status != "CLOSED" && acct.Status != "PENDING_CLOSURE" && now.Sub(acct.JoinedAt) > placementGrace {
				errs = append(errs, fmt.Errorf("account %s (%s) is %s in the organization root and cannot be placed in the Playground OU", acct.Name, acct.ProviderID, acct.Status))
			}
			continue
		}
		if _, err := s.place(ctx, FromRemote(acct, s.cfg, now)); err != nil {
			errs = append(errs, err)
			continue
		}
		sum.Placed = append(sum.Placed, acct.Name)
		// The root listing can be stale: another process can have placed the
		// account and finished its handoff since. Readiness must see the live
		// handoff tag.
		fresh, err := s.provider.GetAccount(ctx, acct.ProviderID)
		if err != nil {
			errs = append(errs, providerError("read "+acct.Name+" after placement", err))
			continue
		}
		remote = append(remote, fresh)
	}

	for _, r := range remote {
		a := FromRemote(r, s.cfg, now)
		if a.Status == StatusClosed {
			if a.CloseRequestedAt != nil && a.ClosedAt == nil {
				if err := s.confirmClosed(ctx, a); err != nil {
					errs = append(errs, err)
				} else {
					sum.Closed = append(sum.Closed, a.Name)
				}
			}
			// Also retire for externally closed and previously confirmed accounts.
			// Cleanup failures must not undo or suppress confirmed closure.
			if err := s.retireBudget(ctx, a); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		// Keep processing requested closures even when AWS is no longer
		// ACTIVE. PENDING_CLOSURE is not CLOSED; SUSPENDED is not either.
		if a.CloseRequestedAt != nil || r.Status == "PENDING_CLOSURE" {
			if a.CloseRequestedAt == nil {
				// Observe externally initiated closure without resetting its
				// monitoring clock on every reconciliation.
				a.CloseRequestedAt = &now
				if err := s.provider.SetTags(ctx, a.ProviderID, map[string]string{TagCloseRequested: now.UTC().Format(time.RFC3339)}); err != nil {
					errs = append(errs, providerError("track closure of "+a.Name, err))
					continue
				}
			}
			if r.Status == "ACTIVE" {
				a, err = s.tryClose(ctx, a)
			} else {
				err = s.revokeAccess(ctx, a)
				if a.Status == StatusUnavailable {
					err = errors.Join(err, fmt.Errorf("closure of %s: %s", a.Name, a.LastError))
				}
			}
			if err != nil {
				errs = append(errs, err)
			}
			if a.Status == StatusClosed {
				sum.Closed = append(sum.Closed, a.Name)
			} else {
				sum.Deferred = append(sum.Deferred, a.Name)
				if err := s.checkClosureDelay(ctx, a); err != nil {
					sum.Overdue = append(sum.Overdue, a.Name)
					errs = append(errs, err)
				}
			}
			continue
		}
		if r.Status != "ACTIVE" {
			errs = append(errs, fmt.Errorf("account %s: %s", a.Name, a.LastError))
			continue
		}

		// 2. Adopt accounts in the OU that lack our tags.
		if !a.Managed {
			if err := a.RepairError(); err != nil {
				errs = append(errs, err)
				continue
			}
			if a.handoff == "" {
				a.handoff = handoffPending
			}
			if err := s.provider.SetTags(ctx, a.ProviderID, a.Tags()); err != nil {
				errs = append(errs, providerError("adopt "+a.Name, err))
				continue
			}
			a.Managed = true
			sum.Adopted = append(sum.Adopted, a.Name)
			s.audit(ctx, "adopted", a, ActorSystem, "", fmt.Sprintf("%s was found in the OU without tags and adopted for %s, expires %s", a.Name, a.Owner, a.ExpiresAt.Format("2006-01-02")), nil)
		}

		// Readiness errors never prevent expiry/closure or another account's work.
		beforeAccess := a.AccessGrantedTo
		if err := s.reconcileReadiness(ctx, a); err != nil {
			errs = append(errs, err)
		}
		if beforeAccess == "" && a.AccessGrantedTo != "" {
			sum.Granted = append(sum.Granted, a.Name)
		}

		// Only a known expiry can authorize expiry-based warning or closure.
		// Other tag damage must not block this independent evidence.
		if a.ExpiresAt.IsZero() {
			continue
		}
		left := a.ExpiresAt.Sub(now)
		switch {
		case left <= 0:
			a.CloseRequestedAt = &now
			if err := s.provider.SetTags(ctx, a.ProviderID, map[string]string{TagCloseRequested: now.UTC().Format(time.RFC3339)}); err != nil {
				errs = append(errs, providerError("tag "+a.Name, err))
				continue
			}
			s.audit(ctx, "expired", a, ActorSystem, "", fmt.Sprintf("%s passed its expiry %s and is being closed", a.Name, a.ExpiresAt.Format("2006-01-02")), nil)
			a, err = s.tryClose(ctx, a)
			if err != nil {
				errs = append(errs, err)
			}
			if a.Status == StatusClosed {
				sum.Closed = append(sum.Closed, a.Name)
			} else {
				sum.Deferred = append(sum.Deferred, a.Name)
			}
		case left <= s.cfg.WarnBefore && a.WarnedAt == nil:
			if err := s.provider.SetTags(ctx, a.ProviderID, map[string]string{TagWarnedAt: now.UTC().Format(time.RFC3339)}); err != nil {
				errs = append(errs, providerError("tag "+a.Name, err))
				continue
			}
			s.notify(ctx, a, "Playground account expiring",
				fmt.Sprintf("Account %s (%s) expires %s. Extend it or it will be closed.", a.Name, a.ProviderID, a.ExpiresAt.Format("2006-01-02")))
			sum.Warned = append(sum.Warned, a.Name)
			s.audit(ctx, "warned", a, ActorSystem, "", fmt.Sprintf("sent the expiring notice for %s (owner %s): expires %s", a.Name, a.Owner, a.ExpiresAt.Format("2006-01-02")), nil)
		}
	}
	if err := s.expireRequests(ctx, &sum); err != nil {
		errs = append(errs, err)
	}
	if !sum.Empty() {
		s.Invalidate()
		s.log.Info("refresh", "summary", sum.String())
	}
	return sum, errors.Join(errs...)
}

// ---- approval queue --------------------------------------------------------

// ErrNeedsApproval marks operations that must go through the queue.
var ErrNeedsApproval = errors.New("needs approval")

// allRequests reads every request record from the OU tags, oldest first,
// pending and approved alike.
func (s *Service) allRequests(ctx context.Context) ([]Request, error) {
	tags, err := s.provider.OUTags(ctx)
	if err != nil {
		return nil, providerError("read request queue", err)
	}
	var out []Request
	for k, v := range tags {
		if !strings.HasPrefix(k, RequestTagPrefix) {
			continue
		}
		r, err := DecodeRequest(k, v)
		if err != nil {
			s.log.Warn("skipping unreadable request tag", "key", k, "err", err)
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RequestedAt.Before(out[j].RequestedAt) })
	return out, nil
}

// PendingRequests returns the requests still waiting for an approver. It
// reads through the inventory cache, so a page that lists accounts and the
// queue costs one read of the OU tags, not two. Every change made through
// this Service invalidates the cache first.
func (s *Service) PendingRequests(ctx context.Context) ([]Request, error) {
	if _, err := s.Inventory(ctx, false); err != nil {
		return nil, err
	}
	s.mu.Lock()
	all := append([]Request(nil), s.recs...)
	s.mu.Unlock()
	var out []Request
	for _, r := range all {
		if !r.Approved {
			out = append(out, r)
		}
	}
	return out, nil
}

// GetRequest returns one pending request by name. It always reads live,
// because the callers are about to act on the record and another process
// may have changed it since the inventory was cached.
func (s *Service) GetRequest(ctx context.Context, name string) (Request, error) {
	all, err := s.allRequests(ctx)
	if err != nil {
		return Request{}, err
	}
	for _, r := range all {
		if r.Name == name && !r.Approved {
			return r, nil
		}
	}
	return Request{}, fmt.Errorf("%w: no pending request named %s", ErrNotFound, name)
}

// ErrRequestChanged is returned when a pending request no longer holds what
// the actor saw. The actor should look at the request again and act anew.
var ErrRequestChanged = errors.New("request changed")

// pendingAt returns the pending request ref names, and refuses it when its
// version is not the one the actor saw: the request was edited since, or
// it ended and someone submitted a new one under the same name. Every
// decision on a request goes through here, so a stale page, TUI row, or
// Slack message can never act on values its reader did not see.
//
// A refusal drops the cached inventory. The actor's view was stale, and the
// cache can be stale too when another process changed the queue, so the
// view the interface shows next reads the queue live and carries the
// current version.
func (s *Service) pendingAt(ctx context.Context, ref RequestRef) (Request, error) {
	r, err := s.GetRequest(ctx, ref.Name)
	if err == nil && ref.Version == r.Version() {
		return r, nil
	}
	s.Invalidate()
	if err != nil {
		return Request{}, err
	}
	switch {
	case ref.Version == "":
		return Request{}, fmt.Errorf("%w: no version given for the request for %s; load it again and retry", ErrRequestChanged, ref.Name)
	case !strings.HasPrefix(ref.Version, r.JourneyID+"."):
		return Request{}, fmt.Errorf("%w: the request for %s you loaded has ended, and %s submitted a new one with the same name; review it before you act", ErrRequestChanged, ref.Name, r.RequestedBy)
	default:
		return Request{}, fmt.Errorf("%w: the request for %s was edited after you loaded it; review the new values before you act", ErrRequestChanged, ref.Name)
	}
}

// SubmitRequest validates a request and puts it in the queue. It applies
// the same checks as Request plus the per-owner limit, then notifies approvers.
func (s *Service) SubmitRequest(ctx context.Context, in RequestInput, via string) (Request, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := ValidateName(in.Name); err != nil {
		return Request{}, err
	}
	if strings.TrimSpace(in.Owner) == "" {
		return Request{}, errors.New("owner is required")
	}
	if in.TTL <= 0 {
		in.TTL = s.cfg.DefaultTTL
	}
	if in.BudgetUSD <= 0 {
		in.BudgetUSD = s.cfg.DefaultBudgetUSD
	}
	if err := s.checkLimits(in); err != nil {
		return Request{}, err
	}
	if err := validateFreeText(in); err != nil {
		return Request{}, err
	}
	if s.provider.AccessEnabled() {
		user, err := s.provider.LookupUser(ctx, in.Owner)
		if errors.Is(err, ErrUserNotFound) {
			return Request{}, fmt.Errorf("owner %q is not a user in the identity store; use the engineer's email or user name", in.Owner)
		}
		if err != nil {
			return Request{}, providerError("look up owner", err)
		}
		in.Owner = user.Identity()
	}
	if in.RequestedBy == "" {
		in.RequestedBy = in.Owner
	}
	all, err := s.Inventory(ctx, true)
	if err != nil {
		return Request{}, err
	}
	held := 0
	for _, a := range all {
		if a.Name == in.Name && a.Status == StatusPending {
			return Request{}, fmt.Errorf("a request named %s is already waiting for approval", in.Name)
		}
		if strings.EqualFold(a.Owner, in.Owner) && a.Status.IsOpen() {
			held++
		}
	}
	if err := s.checkNameFree(all, in.Name, "", ""); err != nil {
		return Request{}, err
	}
	if s.cfg.MaxPerOwner > 0 && held >= s.cfg.MaxPerOwner {
		return Request{}, OwnerLimitError{Owner: in.Owner, Held: held, Limit: s.cfg.MaxPerOwner}
	}
	if err := s.checkQueueRoom(ctx); err != nil {
		return Request{}, err
	}
	r := Request{
		JourneyID: NewHistoryID(),
		Name:      in.Name, Owner: in.Owner, TTL: in.TTL, BudgetUSD: in.BudgetUSD,
		RequestedBy: in.RequestedBy, RequestedAt: s.Now().UTC().Truncate(time.Second), Via: via, Purpose: strings.TrimSpace(in.Purpose),
		OverrideLimits: in.OverrideLimits,
	}
	if err := r.Validate(); err != nil {
		return Request{}, err
	}
	// The write below replaces the record of a failed creation with this
	// name, so report that failure first.
	if err := s.reportReplacedFailure(ctx, r.Name); err != nil {
		return Request{}, err
	}
	if err := s.provider.SetOUTags(ctx, map[string]string{r.Key(): r.Encode()}); err != nil {
		return Request{}, providerError("queue request", err)
	}
	s.Invalidate()
	s.log.Info("request queued", "name", r.Name, "owner", r.Owner, "via", via)
	s.audit(ctx, "requested", pendingAccount(r, s.Now()), r.RequestedBy, via, fmt.Sprintf("%s requested %s for %s: %s/month, %s", r.RequestedBy, r.Name, r.Owner, Dollars(r.BudgetUSD), Lifetime(r.TTL)),
		map[string]string{"budget": strconv.FormatFloat(r.BudgetUSD, 'f', -1, 64), "days": strconv.Itoa(int(r.TTL.Hours() / 24)), "purpose": r.Purpose, "override": strconv.FormatBool(r.OverrideLimits)})
	var body strings.Builder
	fmt.Fprintf(&body, "%s requested playground account %s for %s: %s/month, %s.", r.RequestedBy, r.Name, r.Owner, Dollars(r.BudgetUSD), Lifetime(r.TTL))
	if r.OverrideLimits {
		body.WriteString(" Limits overridden by an admin.")
	}
	if r.Purpose != "" {
		body.WriteString(" Purpose: ")
		body.WriteString(r.Purpose)
		body.WriteByte('.')
	}
	if s.cfg.BaseURL != "" {
		body.WriteString(" Approve or deny at ")
		body.WriteString(strings.TrimRight(s.cfg.BaseURL, "/"))
		body.WriteString("/#pending")
	}
	s.notifyAll(ctx, "Playground account requested: "+r.Name, body.String(), "approvers", "req:"+r.Name)
	return r, nil
}

// checkQueueRoom refuses a new request when the OU has no tag left for it.
// AWS allows MaxTagsPerResource tags on a resource, and pending and
// approved-in-flight records share that budget with any other OU tags.
func (s *Service) checkQueueRoom(ctx context.Context) error {
	tags, err := s.provider.OUTags(ctx)
	if err != nil {
		return providerError("read request queue", err)
	}
	if len(tags) >= MaxTagsPerResource {
		return fmt.Errorf("the request queue is full: AWS allows %d tags on the OU and all are in use; approve or deny waiting requests, or run reconcile to drop stale ones", MaxTagsPerResource)
	}
	return nil
}

// restoreTimeout bounds the write that makes a claimed request pending again
// after its creation failed. The write ignores the cancellation of the
// caller's context, so it needs a limit of its own.
const restoreTimeout = 30 * time.Second

// Approve creates the account for a pending request and removes it from the queue.
//
// The record is marked approved before the account is requested, so a
// second approver, in this process or another, finds nothing pending. If the
// creation then fails the mark is removed and the request is pending again,
// also when the caller stopped waiting. ref must carry the version the
// approver saw. See pendingAt.
func (s *Service) Approve(ctx context.Context, ref RequestRef, approver string) (*Account, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	r, err := s.pendingAt(ctx, ref)
	if err != nil {
		return nil, err
	}
	name := r.Name
	if err := CheckApprover(r, approver, s.cfg.AllowSelfApproval); err != nil {
		return nil, err
	}
	pending := r
	if r.JourneyID == "" {
		r.JourneyID = NewHistoryID()
	}
	r.Approved, r.ApprovedBy, r.ApprovedAt = true, approver, s.Now().UTC().Truncate(time.Second)
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if err := s.provider.SetOUTags(ctx, map[string]string{r.Key(): r.Encode()}); err != nil {
		return nil, providerError("claim request", err)
	}
	a, err := s.Request(ctx, RequestInput{
		Name: r.Name, Owner: r.Owner, TTL: r.TTL, BudgetUSD: r.BudgetUSD,
		Purpose: r.Purpose, RequestedBy: r.RequestedBy, ApprovedBy: approver,
		OverrideLimits: r.OverrideLimits, // an admin already vouched for the values
		viaQueue:       true,
		journeyID:      r.JourneyID,
	})
	if err != nil {
		// The caller's context can be the reason the creation failed: the
		// browser went away, or Slack's deadline passed. The restore must
		// not fail for the same reason, so it ignores the cancellation and
		// has a time limit of its own.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), restoreTimeout)
		if uerr := s.provider.SetOUTags(rctx, map[string]string{pending.Key(): pending.Encode()}); uerr != nil {
			s.log.Error("request left marked approved after a failed creation; reconcile drops it after a day", "name", name, "err", uerr)
		}
		cancel()
		s.Invalidate()
		return nil, err
	}
	s.audit(ctx, "approved", a, approver, "", fmt.Sprintf("%s approved %s for %s: %s/month, %s", approver, r.Name, r.Owner, Dollars(r.BudgetUSD), Lifetime(r.TTL)),
		map[string]string{"request_id": a.RequestID, "requested_by": r.RequestedBy})
	// Keep the record, marked approved, until the account is placed. The
	// inventory uses it to show owner, budget, and expiry while AWS works.
	r.ApprovedAt, r.CreateRequestID = a.CreatedAt, a.RequestID
	if err := s.provider.SetOUTags(ctx, map[string]string{r.Key(): r.Encode()}); err != nil {
		// The account is on its way and the record is already marked
		// approved, so nobody can approve it again; the inventory just
		// cannot pair the creation with its owner until placement.
		s.log.Warn("approved record not updated with the create request id", "name", name, "err", err)
	}
	s.Invalidate()
	s.log.Info("request approved", "name", name, "by", approver)
	s.notifyAll(ctx, "Playground request approved: "+name,
		fmt.Sprintf("%s approved %s for %s. AWS is creating the account; it usually takes a few minutes.", approver, name, r.Owner), r.Owner, a.ID)
	return a, nil
}

// ErrSelfApproval is the sentinel of every CheckApprover refusal. An
// interface finds it with errors.Is and can then choose its own words.
var ErrSelfApproval = errors.New("self-approval refused")

// SelfApprovalError is a refusal of CheckApprover. Error returns the refusal
// alone, with no prefix from the sentinel, so the CLI and the TUI keep their
// words. WouldOwn tells the two refusals apart: it is true when the approver
// is the owner of the request, and false when the approver requested it.
type SelfApprovalError struct {
	WouldOwn bool
	msg      string
}

func (e SelfApprovalError) Error() string { return e.msg }
func (e SelfApprovalError) Unwrap() error { return ErrSelfApproval }

// CheckApprover applies the self-approval rule: nobody may approve a request
// they asked for or would own, unless self-approval is allowed. The web UI
// uses it to decide whether to show the button, so the click always matches.
// A refusal is a SelfApprovalError, which matches ErrSelfApproval.
func CheckApprover(r Request, approver string, allowSelf bool) error {
	if allowSelf {
		return nil
	}
	if strings.EqualFold(r.RequestedBy, approver) {
		return SelfApprovalError{msg: fmt.Sprintf("%s requested %s and cannot approve it; another approver must", approver, r.Name)}
	}
	if strings.EqualFold(r.Owner, approver) {
		return SelfApprovalError{WouldOwn: true, msg: fmt.Sprintf("%s would own %s and cannot approve it; another approver must", approver, r.Name)}
	}
	return nil
}

// RequestEdit carries the fields an admin may change on a pending request.
// Zero values and nil pointers leave a field as it was.
type RequestEdit struct {
	Owner          string
	TTL            time.Duration
	BudgetUSD      float64
	Purpose        *string // nil leaves it, empty string clears it
	OverrideLimits *bool   // nil leaves it; true lets an admin keep values above the ceilings
}

// UpdateRequest rewrites a pending request with the edited values. The same
// checks as SubmitRequest apply: name stays, owner must resolve, ceilings
// hold unless overridden, and a new owner must have room under the limit.
// ref must carry the version the edit form was filled from, so an edit
// never silently undoes another editor's change.
func (s *Service) UpdateRequest(ctx context.Context, ref RequestRef, editor string, e RequestEdit) (Request, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	r, err := s.pendingAt(ctx, ref)
	if err != nil {
		return Request{}, err
	}
	name := r.Name
	before := r
	if e.Owner != "" && !strings.EqualFold(e.Owner, r.Owner) {
		owner := strings.TrimSpace(e.Owner)
		if s.provider.AccessEnabled() {
			user, err := s.provider.LookupUser(ctx, owner)
			if errors.Is(err, ErrUserNotFound) {
				return Request{}, fmt.Errorf("owner %q is not a user in the identity store", owner)
			}
			if err != nil {
				return Request{}, providerError("look up owner", err)
			}
			owner = user.Identity()
		}
		all, err := s.Inventory(ctx, true)
		if err != nil {
			return Request{}, err
		}
		held := 0
		for _, a := range all {
			if strings.EqualFold(a.Owner, owner) && a.Status.IsOpen() && a.Name != r.Name {
				held++
			}
		}
		if s.cfg.MaxPerOwner > 0 && held >= s.cfg.MaxPerOwner {
			return Request{}, OwnerLimitError{Owner: owner, Held: held, Limit: s.cfg.MaxPerOwner}
		}
		r.Owner = owner
	}
	if e.TTL > 0 {
		r.TTL = e.TTL
	}
	if e.BudgetUSD > 0 {
		r.BudgetUSD = e.BudgetUSD
	}
	if e.Purpose != nil {
		r.Purpose = strings.TrimSpace(*e.Purpose)
	}
	if e.OverrideLimits != nil {
		r.OverrideLimits = *e.OverrideLimits
	}
	if err := s.checkLimits(RequestInput{TTL: r.TTL, BudgetUSD: r.BudgetUSD, OverrideLimits: r.OverrideLimits}); err != nil {
		return Request{}, err
	}
	if r == before {
		return r, nil
	}
	if err := r.Validate(); err != nil {
		return Request{}, err
	}
	if err := s.provider.SetOUTags(ctx, map[string]string{r.Key(): r.Encode()}); err != nil {
		return Request{}, providerError("update request", err)
	}
	s.Invalidate()
	s.log.Info("request edited", "name", name, "by", editor)
	var changes []string
	if before.Owner != r.Owner {
		changes = append(changes, "owner "+before.Owner+" → "+r.Owner)
	}
	if before.TTL != r.TTL {
		changes = append(changes, fmt.Sprintf("lifetime %s → %s", Lifetime(before.TTL), Lifetime(r.TTL)))
	}
	if before.BudgetUSD != r.BudgetUSD {
		changes = append(changes, fmt.Sprintf("budget %s → %s", Dollars(before.BudgetUSD), Dollars(r.BudgetUSD)))
	}
	if before.Purpose != r.Purpose {
		changes = append(changes, "purpose updated")
	}
	if before.OverrideLimits != r.OverrideLimits {
		if r.OverrideLimits {
			changes = append(changes, "limits overridden")
		} else {
			changes = append(changes, "limit override removed")
		}
	}
	if len(changes) > 0 {
		s.audit(ctx, "edited", pendingAccount(r, s.Now()), editor, "", fmt.Sprintf("%s edited %s: %s", editor, name, strings.Join(changes, "; ")), map[string]string{"changes": strings.Join(changes, "; ")})
		s.notifyAll(ctx, "Playground request edited: "+name,
			fmt.Sprintf("%s changed the request for %s before approval: %s.", editor, name, strings.Join(changes, "; ")), before.Owner, "req:"+name)
	}
	return r, nil
}

// Deny removes a pending request and sends a notice with the reason. ref
// must carry the version the approver saw; see pendingAt.
func (s *Service) Deny(ctx context.Context, ref RequestRef, approver, reason string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	r, err := s.pendingAt(ctx, ref)
	if err != nil {
		return err
	}
	name := r.Name
	if err := s.provider.RemoveOUTags(ctx, []string{r.Key()}); err != nil {
		return providerError("remove request", err)
	}
	s.Invalidate()
	s.log.Info("request denied", "name", name, "by", approver)
	s.audit(ctx, "denied", pendingAccount(r, s.Now()), approver, "", strings.TrimSpace(fmt.Sprintf("%s denied %s for %s. %s", approver, name, r.Owner, strings.TrimSpace(reason))), map[string]string{"reason": strings.TrimSpace(reason), "requested_by": r.RequestedBy})
	body := fmt.Sprintf("%s denied the request for %s.", approver, name)
	if strings.TrimSpace(reason) != "" {
		body += " Reason: " + strings.TrimSpace(reason)
	}
	s.notifyAll(ctx, "Playground request denied: "+name, body, r.Owner, "req:"+name)
	return nil
}

// Withdraw lets the requester (or an operator) pull a pending request. ref
// must carry the version the actor saw; see pendingAt.
func (s *Service) Withdraw(ctx context.Context, ref RequestRef, by string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	r, err := s.pendingAt(ctx, ref)
	if err != nil {
		return err
	}
	name := r.Name
	if err := s.provider.RemoveOUTags(ctx, []string{r.Key()}); err != nil {
		return providerError("remove request", err)
	}
	s.Invalidate()
	s.log.Info("request withdrawn", "name", name, "by", by)
	s.audit(ctx, "withdrawn", pendingAccount(r, s.Now()), by, "", fmt.Sprintf("%s withdrew the request for %s", by, name), nil)
	return nil
}

// expireRequests drops pending requests nobody acted on by their DropsAt,
// and approved records whose creation failed or is no longer known to the
// provider. It reports each failed creation of an approved record once,
// also when nobody waited for it.
func (s *Service) expireRequests(ctx context.Context, sum *Summary) error {
	all, err := s.allRequests(ctx)
	if err != nil {
		return err
	}
	creates, err := s.provider.ListCreateRequests(ctx)
	if err != nil {
		return providerError("", err)
	}
	byID := map[string]CreateRequest{}
	for _, c := range creates {
		byID[c.RequestID] = c
	}
	now := s.Now()
	var keys []string
	var expired []Request
	var errs []error
	for _, r := range all {
		if r.Approved {
			c, known := byID[r.CreateRequestID]
			if known && c.State == CreateFailed && !r.FailureReported {
				// Report before the removal below, so that a record past
				// FailedRetention is still reported once. Keep a record whose
				// report was not recorded, or the failure is never reported.
				if err := s.reportCreationFailure(ctx, r, c); err != nil {
					errs = append(errs, err)
					continue
				}
			}
			stale := !known && now.Sub(r.ApprovedAt) > 24*time.Hour
			failedLongAgo := known && c.State == CreateFailed && now.Sub(c.CompletedAt) > s.cfg.FailedRetention
			// Placement removes the record; if that call failed the record
			// would hold a queue slot until the provider forgets the
			// creation. A finished creation is shown from its own tags, so
			// the record can go a day after success.
			placedLongAgo := known && c.State == CreateSucceeded && now.Sub(c.CompletedAt) > 24*time.Hour
			if stale || failedLongAgo || placedLongAgo {
				keys = append(keys, r.Key())
			}
			continue
		}
		if now.After(r.DropsAt(s.cfg.RequestTTL)) {
			keys = append(keys, r.Key())
			expired = append(expired, r)
		}
	}
	if len(keys) > 0 {
		if err := s.provider.RemoveOUTags(ctx, keys); err != nil {
			return errors.Join(append(errs, providerError("", err))...)
		}
	}
	// A failed removal is not a terminal outcome. Announce expiry only after
	// the provider accepted it; durable unknown-outcome handling is separate.
	for _, r := range expired {
		sum.Expired = append(sum.Expired, r.Name)
		s.audit(ctx, "request-expired", pendingAccount(r, now), ActorSystem, "", fmt.Sprintf("nobody acted on the request for %s within %s; dropped", r.Name, Lifetime(s.cfg.RequestTTL)), nil)
		s.notifyAll(ctx, "Playground request expired: "+r.Name,
			fmt.Sprintf("Nobody approved the request for %s within %s; it was dropped. Submit it again if still needed.", r.Name, Lifetime(s.cfg.RequestTTL)), r.Owner, "req:"+r.Name)
	}
	return errors.Join(errs...)
}

func (s *Service) notifyAll(ctx context.Context, subject, body, owner, id string) {
	if s.notifier == nil {
		return
	}
	if err := s.notifier.Notify(ctx, Message{Subject: subject, Body: body, Owner: owner, AccountID: id}); err != nil {
		s.log.Error("notify", "subject", subject, "err", err)
	}
}

// ---- costs -----------------------------------------------------------------

// costRetryAfter is how long Costs waits after a failed pull of an account
// before a pull that is not forced runs again.
const costRetryAfter = 5 * time.Minute

// Costs returns daily spend for the last n days, pulled live and cached for
// CostTTL. Cost Explorer bills per call, so callers should batch with WarmCosts.
//
// After a failed pull, Costs returns that error again for costRetryAfter and
// does not pull. The web poll warms every visible account every 15 seconds,
// so without the wait an outage costs a call per account per poll. force
// pulls at once, also within the wait. Only a person forces a pull: Sync in
// the web, an account page whose spend is not known, or r and S in the TUI.
// So a forced pull is rare, and it gives the person a fresh answer. A pull that fails because the caller's context
// ended says nothing about AWS, so it changes no cost state.
//
// Pulls of one account can overlap, for example a poll and a forced pull.
// Each pull gets a number, and a pull writes only when no later pull wrote
// first. So a slow pull never hides the answer of a newer one.
func (s *Service) Costs(ctx context.Context, id string, days int, force bool) ([]CostPoint, error) {
	a, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if a.ProviderID == "" {
		return nil, nil
	}
	now := s.Now()
	s.mu.Lock()
	e, ok := s.costs[a.ProviderID]
	if ok && !force {
		if e.err == nil && now.Sub(e.at) < s.cfg.CostTTL {
			s.mu.Unlock()
			return window(e.points, days, now), nil
		}
		if e.err != nil && now.Sub(e.at) < costRetryAfter {
			s.mu.Unlock()
			return nil, providerError("costs for "+a.Name, e.err)
		}
	}
	s.costPulls++
	seq := s.costPulls
	s.mu.Unlock()
	to := now.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	from := to.Add(-45 * 24 * time.Hour)
	pts, err := s.provider.Costs(ctx, a.ProviderID, from, to)
	if err != nil {
		if ctx.Err() == nil {
			s.mu.Lock()
			if e := s.costs[a.ProviderID]; e.seq < seq {
				e.seq, e.at, e.err = seq, now, err
				s.costs[a.ProviderID] = e
			}
			s.mu.Unlock()
		}
		return nil, providerError("costs for "+a.Name, err)
	}
	s.mu.Lock()
	if s.costs[a.ProviderID].seq < seq {
		s.costs[a.ProviderID] = costEntry{points: pts, okAt: now, at: now, seq: seq}
	}
	s.mu.Unlock()
	return window(pts, days, now), nil
}

// WarmCosts pulls costs for many accounts with bounded parallelism.
func (s *Service) WarmCosts(ctx context.Context, ids []string, force bool) error {
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if _, err := s.Costs(ctx, id, 1, force); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	return errors.Join(errs...)
}

// CostState returns the cached points of the last n days without a pull. It
// reports known only when the last pull of the account succeeded. An account
// that was never pulled, or whose last pull failed, has unknown spend: show
// it as unavailable, never as zero. The points are as old as that pull, and
// WarmCosts pulls again after CostTTL, so callers warm first.
func (s *Service) CostState(providerID string, days int) ([]CostPoint, bool) {
	s.mu.Lock()
	e, ok := s.costs[providerID]
	s.mu.Unlock()
	if !ok || e.err != nil {
		return nil, false
	}
	return window(e.points, days, s.Now()), true
}

// CostsCached returns the cached points of the last pull that succeeded,
// without a pull. It returns them also after a later pull failed, so it
// cannot tell current spend from old spend. Use CostState for that.
func (s *Service) CostsCached(providerID string, days int) ([]CostPoint, bool) {
	s.mu.Lock()
	e, ok := s.costs[providerID]
	s.mu.Unlock()
	if !ok || e.okAt.IsZero() {
		return nil, false
	}
	return window(e.points, days, s.Now()), true
}

func window(pts []CostPoint, days int, now time.Time) []CostPoint {
	from := now.UTC().Truncate(24 * time.Hour).Add(-time.Duration(days-1) * 24 * time.Hour)
	var out []CostPoint
	for _, p := range pts {
		if !p.Date.Before(from) {
			out = append(out, p)
		}
	}
	return out
}

func (s *Service) notify(ctx context.Context, a *Account, subject, body string) {
	if s.notifier == nil {
		return
	}
	if err := s.notifier.Notify(ctx, Message{Subject: subject, Body: body, Owner: a.Owner, AccountID: a.ID}); err != nil {
		s.log.Error("notify", "account", a.Name, "err", err)
	}
}

func cloneAll(in []*Account) []*Account {
	out := make([]*Account, len(in))
	for i, a := range in {
		c := *a
		out[i] = &c
	}
	return out
}

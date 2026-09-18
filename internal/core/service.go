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
	WarnBefore       time.Duration // how far ahead of expiry to warn the owner
	DefaultBudgetUSD float64       // monthly budget when none is given
	EmailPattern     string        // e.g. "aws+pp-{name}@example.com"
	AlertEmail       string        // recipient for provider budget alerts

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

	mu    sync.Mutex
	inv   []*Account
	recs  []Request // request records read with the inventory, same age
	invAt time.Time
	costs map[string]costEntry

	// opMu serializes the refresh pass and every change to the request
	// queue within this process, so the worker, the web UI, Slack, and the
	// TUI cannot run the same pass twice or approve the same request twice.
	// Separate processes are not covered; AWS tags have no compare-and-swap.
	opMu sync.Mutex
}

type costEntry struct {
	points []CostPoint
	at     time.Time
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
	e := AuditEvent{At: s.Now().UTC(), Event: event, Actor: actor, Via: via, Message: msg, Details: details}
	if a != nil {
		e.Account, e.AccountID, e.Owner = a.Name, a.ProviderID, a.Owner
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
	return s.provider.EnsureOrganization(ctx)
}

// Invalidate drops the cached inventory so the next read hits the provider.
func (s *Service) Invalidate() {
	s.mu.Lock()
	s.invAt = time.Time{}
	s.mu.Unlock()
}

// ---- inventory -------------------------------------------------------------

// Inventory lists every playground account: OU members plus creations that
// are still in flight or failed recently. The result is cached for
// InventoryTTL unless force is set.
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
		return nil, fmt.Errorf("list playground accounts: %w", err)
	}
	reqs, err := s.provider.ListCreateRequests(ctx)
	if err != nil {
		return nil, fmt.Errorf("list create requests: %w", err)
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
			// Landed at the provider but not placed in the OU yet; refresh
			// will finish it. Show it as creating meanwhile.
			if !inOU[r.ProviderID] && now.Sub(r.CompletedAt) <= 24*time.Hour {
				a := derive(r)
				a.ProviderID = r.ProviderID
				out = append(out, a)
			}
		}
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
// then by id prefix. It errors if a prefix is ambiguous.
func (s *Service) Resolve(ctx context.Context, ref string) (*Account, error) {
	all, err := s.Inventory(ctx, false)
	if err != nil {
		return nil, err
	}
	var matches []*Account
	for _, a := range all {
		if a.ID == ref {
			return a, nil
		}
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

	viaQueue bool // set by Approve so Request does not log a direct create
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
	return nil
}

// checkLimits enforces the TTL and budget ceilings unless overridden.
func (s *Service) checkLimits(in RequestInput) error {
	if err := checkBudget(in.BudgetUSD); err != nil {
		return err
	}
	if in.OverrideLimits {
		return nil
	}
	if s.cfg.MaxTTL > 0 && in.TTL > s.cfg.MaxTTL {
		return fmt.Errorf("ttl %dd exceeds the maximum of %dd; an admin can override", int(in.TTL.Hours()/24), int(s.cfg.MaxTTL.Hours()/24))
	}
	if s.cfg.MaxBudgetUSD > 0 && in.BudgetUSD > s.cfg.MaxBudgetUSD {
		return fmt.Errorf("budget $%.0f exceeds the maximum of $%.0f; an admin can override", in.BudgetUSD, s.cfg.MaxBudgetUSD)
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
			return nil, fmt.Errorf("look up owner: %w", err)
		}
		in.Owner = user.Identity()
	}
	all, err := s.Inventory(ctx, true)
	if err != nil {
		return nil, err
	}
	if err := s.checkNameFree(all, in.Name, in.Email); err != nil {
		return nil, err
	}

	if in.RequestedBy == "" {
		in.RequestedBy = in.Owner
	}
	now := s.Now()
	a := &Account{
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
	}
	reqID, err := s.provider.RequestAccount(ctx, AccountRequest{Name: a.Name, Email: a.Email, Tags: a.Tags()})
	if err != nil {
		return nil, fmt.Errorf("request account: %w", err)
	}
	a.ID, a.RequestID = reqID, reqID
	s.Invalidate()
	s.log.Info("requested", "account", a.Name, "request", reqID)
	if in.ApprovedBy != "" && in.ApprovedBy == in.RequestedBy && !in.viaQueue {
		s.audit(ctx, "created", a, in.ApprovedBy, "cli", fmt.Sprintf("%s created %s directly for %s: %s/month, %d days", in.ApprovedBy, a.Name, a.Owner, money(a.BudgetUSD), int(in.TTL.Hours()/24)),
			map[string]string{"request_id": reqID, "budget": strconv.FormatFloat(a.BudgetUSD, 'f', -1, 64), "days": strconv.Itoa(int(in.TTL.Hours() / 24))})
	}
	return a, nil
}

// checkNameFree refuses a name that an open account already uses. It also
// refuses the name of a closed account when the root email would be derived
// from it, because AWS never frees an account's root email, so the creation
// would be accepted and then fail. A pending request with the name is fine:
// it is the one being approved. Failed creations do not hold a name.
func (s *Service) checkNameFree(all []*Account, name, email string) error {
	derived := strings.ReplaceAll(s.cfg.EmailPattern, "{name}", name)
	for _, a := range all {
		if a.Name != name {
			continue
		}
		switch {
		case a.Status == StatusPending, a.Status == StatusFailed:
			continue
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

// PollCreate checks one creation request and, on success, places the new
// account in the OU with its tags and budget. Loop on it to wait in place.
func (s *Service) PollCreate(ctx context.Context, requestID string) (*Account, error) {
	r, err := s.provider.CreateStatus(ctx, requestID)
	if err != nil {
		return nil, err
	}
	switch r.State {
	case CreateSucceeded:
		remote, err := s.provider.GetAccount(ctx, r.ProviderID)
		if err != nil {
			return nil, err
		}
		return s.place(ctx, remote)
	case CreateFailed:
		a := FromCreateRequest(r)
		s.audit(ctx, "failed", a, ActorSystem, "", fmt.Sprintf("AWS could not create %s: %s", a.Name, r.FailureReason), map[string]string{"reason": r.FailureReason, "request_id": r.RequestID})
		return a, nil
	default:
		return FromCreateRequest(r), nil
	}
}

// place moves a freshly created account into the OU, writes tags, and sets
// its budget. Idempotent.
func (s *Service) place(ctx context.Context, remote RemoteAccount) (*Account, error) {
	a := FromRemote(remote, s.cfg, s.Now())
	if err := s.provider.PlaceAccount(ctx, a.ProviderID, a.Tags()); err != nil {
		return nil, fmt.Errorf("place %s: %w", a.Name, err)
	}
	a.Managed = true
	// The approved request record has done its job once the account is placed.
	if err := s.provider.RemoveOUTags(ctx, []string{RequestTagPrefix + a.Name}); err != nil {
		s.log.Warn("approved record not removed", "account", a.Name, "err", err)
	}
	if err := s.provider.EnsureBudget(ctx, a.ProviderID, a.BudgetUSD, s.cfg.AlertEmail); err != nil {
		// A missing budget should not block use of the account.
		s.log.Warn("budget not created", "account", a.Name, "err", err)
	}
	if err := s.grantAccess(ctx, a); err != nil {
		// Refresh retries; the account is still usable by operators.
		s.log.Warn("access not granted", "account", a.Name, "owner", a.Owner, "err", err)
	}
	s.Invalidate()
	s.audit(ctx, "placed", a, ActorSystem, "", fmt.Sprintf("%s is active as account %s for %s, expires %s", a.Name, a.ProviderID, a.Owner, a.ExpiresAt.Format("2006-01-02")),
		map[string]string{"expires": a.ExpiresAt.UTC().Format(time.RFC3339), "budget": strconv.FormatFloat(a.BudgetUSD, 'f', -1, 64), "approved_by": a.ApprovedBy, "requested_by": a.RequestedBy})
	body := fmt.Sprintf("Account %s (%s) is ready for %s. It expires %s.", a.Name, a.ProviderID, a.Owner, a.ExpiresAt.Format("2006-01-02"))
	if a.AccessGrantedTo != "" {
		body += " Sign in through the access portal; the account is listed there."
	}
	s.notify(ctx, a, "Playground account ready", body)
	return a, nil
}

// grantAccess gives the owner console access and records the principal in a
// tag so Refresh can tell granted accounts from ones still waiting.
func (s *Service) grantAccess(ctx context.Context, a *Account) error {
	if !s.provider.AccessEnabled() || a.AccessGrantedTo != "" || a.ProviderID == "" {
		return nil
	}
	user, err := s.provider.LookupUser(ctx, a.Owner)
	if err != nil {
		return err
	}
	if err := s.provider.GrantAccess(ctx, a.ProviderID, user); err != nil {
		return err
	}
	if err := s.provider.SetTags(ctx, a.ProviderID, map[string]string{TagAccess: user.ID}); err != nil {
		return err
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
		return err
	}
	if err := s.provider.RemoveTags(ctx, a.ProviderID, []string{TagAccess}); err != nil {
		return err
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
	a, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !a.Status.CanExtend() {
		return nil, fmt.Errorf("account %s is %s and cannot be extended", a.Name, a.Status)
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
		return nil, fmt.Errorf("tag %s: %w", a.Name, err)
	}
	if err := s.provider.RemoveTags(ctx, a.ProviderID, []string{TagWarnedAt}); err != nil {
		return nil, fmt.Errorf("untag %s: %w", a.Name, err)
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
		return fmt.Errorf("%s would live %dd, past the %dd ceiling; the latest expiry is %s and an admin can override",
			a.Name, int(until.Sub(start).Hours()/24), int(s.cfg.MaxTTL.Hours()/24), limit.Format("2006-01-02"))
	}
	return nil
}

// RequestClose records close intent as a tag and tries the close right away.
// If the provider's quota refuses, the account stays in closing and a later
// Refresh retries. by names who asked.
func (s *Service) RequestClose(ctx context.Context, id string, by string) (*Account, error) {
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
		return nil, fmt.Errorf("tag %s: %w", a.Name, err)
	}
	a.Status = StatusClosing
	s.Invalidate()
	s.audit(ctx, "close-requested", a, by, "", fmt.Sprintf("%s asked to close %s", by, a.Name), nil)
	return s.tryClose(ctx, a)
}

func (s *Service) tryClose(ctx context.Context, a *Account) (*Account, error) {
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
		return a, fmt.Errorf("close %s: %w", a.Name, err)
	}
	a.Status = StatusClosed
	s.Invalidate()
	s.audit(ctx, "closed", a, ActorSystem, "", fmt.Sprintf("%s (%s) was closed at AWS", a.Name, a.ProviderID), nil)
	s.notify(ctx, a, "Playground account closed",
		fmt.Sprintf("Account %s (%s) owned by %s has been closed.", a.Name, a.ProviderID, a.Owner))
	return a, nil
}

// Summary reports what a Refresh changed.
type Summary struct {
	Placed   []string // creations that landed and were moved into the OU
	Adopted  []string // OU accounts that lacked our tags and got defaults
	Granted  []string // owners given console access
	Warned   []string // owners warned about expiry
	Closed   []string // closed at the provider
	Deferred []string // close wanted but quota refused
	Expired  []string // pending requests dropped for lack of approval
}

// Empty reports whether nothing changed.
func (s Summary) Empty() bool {
	return len(s.Placed)+len(s.Adopted)+len(s.Granted)+len(s.Warned)+len(s.Closed)+len(s.Deferred)+len(s.Expired) == 0
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
	add("deferred", s.Deferred)
	add("expired requests", s.Expired)
	return strings.Join(parts, "; ")
}

// Refresh is the reconciliation pass every command runs first. It finishes
// creations that landed, adopts untagged OU accounts, retries requested
// closes, warns owners ahead of expiry, and closes expired accounts.
func (s *Service) Refresh(ctx context.Context) (Summary, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	var sum Summary
	var errs []error
	now := s.Now()

	remote, err := s.provider.ListPlaygroundAccounts(ctx)
	if err != nil {
		return sum, fmt.Errorf("list playground accounts: %w", err)
	}
	inOU := map[string]bool{}
	for _, r := range remote {
		inOU[r.ProviderID] = true
	}

	// 1. Creations that succeeded but were never placed (the requester did
	//    not wait, or was interrupted).
	reqs, err := s.provider.ListCreateRequests(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("list create requests: %w", err))
	}
	for _, r := range reqs {
		if r.State != CreateSucceeded || inOU[r.ProviderID] || now.Sub(r.CompletedAt) > s.cfg.FailedRetention {
			continue
		}
		acct, err := s.provider.GetAccount(ctx, r.ProviderID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if acct.Tags[TagManaged] != "true" || acct.Status != "ACTIVE" {
			continue // someone else's CreateAccount
		}
		placed, err := s.place(ctx, acct)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		sum.Placed = append(sum.Placed, acct.Name)
		acct.Tags = placed.Tags() // includes the access grant, so step 3 does not repeat it
		remote = append(remote, acct)
	}

	for _, r := range remote {
		if r.Status != "ACTIVE" {
			continue
		}
		a := FromRemote(r, s.cfg, now)

		// 2. Adopt accounts in the OU that lack our tags.
		if !a.Managed {
			if err := s.provider.SetTags(ctx, a.ProviderID, a.Tags()); err != nil {
				errs = append(errs, fmt.Errorf("adopt %s: %w", a.Name, err))
				continue
			}
			if err := s.provider.EnsureBudget(ctx, a.ProviderID, a.BudgetUSD, s.cfg.AlertEmail); err != nil {
				s.log.Warn("budget not created", "account", a.Name, "err", err)
			}
			a.Managed = true
			sum.Adopted = append(sum.Adopted, a.Name)
			s.audit(ctx, "adopted", a, ActorSystem, "", fmt.Sprintf("%s was found in the OU without tags and adopted for %s, expires %s", a.Name, a.Owner, a.ExpiresAt.Format("2006-01-02")), nil)
		}

		// 3. Owners without console access yet (a grant failed earlier, or the
		//    account was adopted with a resolvable owner).
		if a.Status != StatusClosing && a.AccessGrantedTo == "" && s.provider.AccessEnabled() {
			if err := s.grantAccess(ctx, a); err != nil {
				if !errors.Is(err, ErrUserNotFound) {
					errs = append(errs, fmt.Errorf("grant %s: %w", a.Name, err))
				}
			} else {
				sum.Granted = append(sum.Granted, a.Name)
			}
		}

		// 4. Close intent left over from a quota refusal.
		if a.Status == StatusClosing {
			a, err = s.tryClose(ctx, a)
			if err != nil {
				errs = append(errs, err)
			} else if a.Status == StatusClosed {
				sum.Closed = append(sum.Closed, a.Name)
			} else {
				sum.Deferred = append(sum.Deferred, a.Name)
			}
			continue
		}

		// 5. Expiry: close when past due, warn when close.
		left := a.ExpiresAt.Sub(now)
		switch {
		case left <= 0:
			a.CloseRequestedAt = &now
			if err := s.provider.SetTags(ctx, a.ProviderID, map[string]string{TagCloseRequested: now.UTC().Format(time.RFC3339)}); err != nil {
				errs = append(errs, fmt.Errorf("tag %s: %w", a.Name, err))
				continue
			}
			s.audit(ctx, "expired", a, ActorSystem, "", fmt.Sprintf("%s passed its expiry %s and is being closed", a.Name, a.ExpiresAt.Format("2006-01-02")), nil)
			a, err = s.tryClose(ctx, a)
			if err != nil {
				errs = append(errs, err)
			} else if a.Status == StatusClosed {
				sum.Closed = append(sum.Closed, a.Name)
			} else {
				sum.Deferred = append(sum.Deferred, a.Name)
			}
		case left <= s.cfg.WarnBefore && a.WarnedAt == nil:
			if err := s.provider.SetTags(ctx, a.ProviderID, map[string]string{TagWarnedAt: now.UTC().Format(time.RFC3339)}); err != nil {
				errs = append(errs, fmt.Errorf("tag %s: %w", a.Name, err))
				continue
			}
			s.notify(ctx, a, "Playground account expiring",
				fmt.Sprintf("Account %s (%s) expires %s. Extend it or it will be closed.", a.Name, a.ProviderID, a.ExpiresAt.Format("2006-01-02")))
			sum.Warned = append(sum.Warned, a.Name)
			s.audit(ctx, "warned", a, ActorSystem, "", fmt.Sprintf("%s was warned that %s expires %s", a.Owner, a.Name, a.ExpiresAt.Format("2006-01-02")), nil)
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
		return nil, fmt.Errorf("read request queue: %w", err)
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
			return Request{}, fmt.Errorf("look up owner: %w", err)
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
	if err := s.checkNameFree(all, in.Name, ""); err != nil {
		return Request{}, err
	}
	if s.cfg.MaxPerOwner > 0 && held >= s.cfg.MaxPerOwner {
		return Request{}, fmt.Errorf("%s already holds %d open or pending playground account(s); the limit is %d", in.Owner, held, s.cfg.MaxPerOwner)
	}
	if err := s.checkQueueRoom(ctx); err != nil {
		return Request{}, err
	}
	r := Request{
		Name: in.Name, Owner: in.Owner, TTL: in.TTL, BudgetUSD: in.BudgetUSD,
		RequestedBy: in.RequestedBy, RequestedAt: s.Now().UTC().Truncate(time.Second), Via: via, Purpose: strings.TrimSpace(in.Purpose),
		OverrideLimits: in.OverrideLimits,
	}
	if err := r.Validate(); err != nil {
		return Request{}, err
	}
	if err := s.provider.SetOUTags(ctx, map[string]string{r.Key(): r.Encode()}); err != nil {
		return Request{}, fmt.Errorf("queue request: %w", err)
	}
	s.Invalidate()
	s.log.Info("request queued", "name", r.Name, "owner", r.Owner, "via", via)
	s.audit(ctx, "requested", pendingAccount(r, s.Now()), r.RequestedBy, via, fmt.Sprintf("%s requested %s for %s: %s/month, %d days", r.RequestedBy, r.Name, r.Owner, money(r.BudgetUSD), int(r.TTL.Hours()/24)),
		map[string]string{"budget": strconv.FormatFloat(r.BudgetUSD, 'f', -1, 64), "days": strconv.Itoa(int(r.TTL.Hours() / 24)), "purpose": r.Purpose, "override": strconv.FormatBool(r.OverrideLimits)})
	body := fmt.Sprintf("%s requested playground account %s for %s: %s/month, %d days.", r.RequestedBy, r.Name, r.Owner, money(r.BudgetUSD), int(r.TTL.Hours()/24))
	if r.OverrideLimits {
		body += " Limits overridden by an admin."
	}
	if r.Purpose != "" {
		body += " Purpose: " + r.Purpose + "."
	}
	if s.cfg.BaseURL != "" {
		body += " Approve or deny at " + strings.TrimRight(s.cfg.BaseURL, "/") + "/#pending"
	}
	s.notifyAll(ctx, "Playground account requested: "+r.Name, body, "approvers", "req:"+r.Name)
	return r, nil
}

// checkQueueRoom refuses a new request when the OU has no tag left for it.
// AWS allows MaxTagsPerResource tags on a resource, and pending and
// approved-in-flight records share that budget with any other OU tags.
func (s *Service) checkQueueRoom(ctx context.Context) error {
	tags, err := s.provider.OUTags(ctx)
	if err != nil {
		return fmt.Errorf("read request queue: %w", err)
	}
	if len(tags) >= MaxTagsPerResource {
		return fmt.Errorf("the request queue is full: AWS allows %d tags on the OU and all are in use; approve or deny waiting requests, or run reconcile to drop stale ones", MaxTagsPerResource)
	}
	return nil
}

// Approve creates the account for a pending request and removes it from the queue.
//
// The record is marked approved before the account is requested, so a
// second approver, in this process or another, finds nothing pending. If the
// creation then fails the mark is removed and the request is pending again.
func (s *Service) Approve(ctx context.Context, name, approver string) (*Account, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	r, err := s.GetRequest(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := CheckApprover(r, approver, s.cfg.AllowSelfApproval); err != nil {
		return nil, err
	}
	pending := r
	r.Approved, r.ApprovedBy, r.ApprovedAt = true, approver, s.Now().UTC().Truncate(time.Second)
	if err := s.provider.SetOUTags(ctx, map[string]string{r.Key(): r.Encode()}); err != nil {
		return nil, fmt.Errorf("claim request: %w", err)
	}
	a, err := s.Request(ctx, RequestInput{
		Name: r.Name, Owner: r.Owner, TTL: r.TTL, BudgetUSD: r.BudgetUSD,
		Purpose: r.Purpose, RequestedBy: r.RequestedBy, ApprovedBy: approver,
		OverrideLimits: r.OverrideLimits, // an admin already vouched for the values
		viaQueue:       true,
	})
	if err != nil {
		if uerr := s.provider.SetOUTags(ctx, map[string]string{pending.Key(): pending.Encode()}); uerr != nil {
			s.log.Error("request left marked approved after a failed creation; reconcile drops it after a day", "name", name, "err", uerr)
		}
		s.Invalidate()
		return nil, err
	}
	s.audit(ctx, "approved", a, approver, "", fmt.Sprintf("%s approved %s for %s: %s/month, %d days", approver, r.Name, r.Owner, money(r.BudgetUSD), int(r.TTL.Hours()/24)),
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

// CheckApprover applies the self-approval rule: nobody may approve a request
// they asked for or would own, unless self-approval is allowed. The web UI
// uses it to decide whether to show the button, so the click always matches.
func CheckApprover(r Request, approver string, allowSelf bool) error {
	if allowSelf {
		return nil
	}
	if strings.EqualFold(r.RequestedBy, approver) {
		return fmt.Errorf("%s requested %s and cannot approve it; another approver must", approver, r.Name)
	}
	if strings.EqualFold(r.Owner, approver) {
		return fmt.Errorf("%s would own %s and cannot approve it; another approver must", approver, r.Name)
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
func (s *Service) UpdateRequest(ctx context.Context, name, editor string, e RequestEdit) (Request, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	r, err := s.GetRequest(ctx, name)
	if err != nil {
		return Request{}, err
	}
	before := r
	if e.Owner != "" && !strings.EqualFold(e.Owner, r.Owner) {
		owner := strings.TrimSpace(e.Owner)
		if s.provider.AccessEnabled() {
			user, err := s.provider.LookupUser(ctx, owner)
			if errors.Is(err, ErrUserNotFound) {
				return Request{}, fmt.Errorf("owner %q is not a user in the identity store", owner)
			}
			if err != nil {
				return Request{}, fmt.Errorf("look up owner: %w", err)
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
			return Request{}, fmt.Errorf("%s already holds %d open or pending playground account(s); the limit is %d", owner, held, s.cfg.MaxPerOwner)
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
		return Request{}, fmt.Errorf("update request: %w", err)
	}
	s.Invalidate()
	s.log.Info("request edited", "name", name, "by", editor)
	var changes []string
	if before.Owner != r.Owner {
		changes = append(changes, "owner "+before.Owner+" → "+r.Owner)
	}
	if before.TTL != r.TTL {
		changes = append(changes, fmt.Sprintf("lifetime %dd → %dd", int(before.TTL.Hours()/24), int(r.TTL.Hours()/24)))
	}
	if before.BudgetUSD != r.BudgetUSD {
		changes = append(changes, fmt.Sprintf("budget %s → %s", money(before.BudgetUSD), money(r.BudgetUSD)))
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

// Deny removes a pending request and tells the requester why.
func (s *Service) Deny(ctx context.Context, name, approver, reason string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	r, err := s.GetRequest(ctx, name)
	if err != nil {
		return err
	}
	if err := s.provider.RemoveOUTags(ctx, []string{r.Key()}); err != nil {
		return fmt.Errorf("remove request: %w", err)
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

// Withdraw lets the requester (or an operator) pull a pending request.
func (s *Service) Withdraw(ctx context.Context, name, by string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	r, err := s.GetRequest(ctx, name)
	if err != nil {
		return err
	}
	if err := s.provider.RemoveOUTags(ctx, []string{r.Key()}); err != nil {
		return fmt.Errorf("remove request: %w", err)
	}
	s.Invalidate()
	s.log.Info("request withdrawn", "name", name, "by", by)
	s.audit(ctx, "withdrawn", pendingAccount(r, s.Now()), by, "", fmt.Sprintf("%s withdrew the request for %s", by, name), nil)
	return nil
}

// expireRequests drops pending requests nobody acted on within RequestTTL,
// and approved records whose creation failed or is no longer known to the
// provider.
func (s *Service) expireRequests(ctx context.Context, sum *Summary) error {
	all, err := s.allRequests(ctx)
	if err != nil {
		return err
	}
	creates, err := s.provider.ListCreateRequests(ctx)
	if err != nil {
		return err
	}
	byID := map[string]CreateRequest{}
	for _, c := range creates {
		byID[c.RequestID] = c
	}
	now := s.Now()
	var keys []string
	for _, r := range all {
		if r.Approved {
			c, known := byID[r.CreateRequestID]
			stale := !known && now.Sub(r.ApprovedAt) > 24*time.Hour
			failedLongAgo := known && c.State == CreateFailed && now.Sub(c.CompletedAt) > s.cfg.FailedRetention
			// Placement removes the record; if that call failed the record
			// would hold a queue slot until the provider forgets the
			// creation. The inventory stops using it a day after success.
			placedLongAgo := known && c.State == CreateSucceeded && now.Sub(c.CompletedAt) > 24*time.Hour
			if stale || failedLongAgo || placedLongAgo {
				keys = append(keys, r.Key())
			}
			continue
		}
		if now.Sub(r.RequestedAt) > s.cfg.RequestTTL {
			keys = append(keys, r.Key())
			sum.Expired = append(sum.Expired, r.Name)
			s.audit(ctx, "request-expired", pendingAccount(r, now), ActorSystem, "", fmt.Sprintf("nobody acted on the request for %s within %d days; dropped", r.Name, int(s.cfg.RequestTTL.Hours()/24)), nil)
			s.notifyAll(ctx, "Playground request expired: "+r.Name,
				fmt.Sprintf("Nobody approved the request for %s within %d days; it was dropped. Submit it again if still needed.", r.Name, int(s.cfg.RequestTTL.Hours()/24)), r.Owner, "req:"+r.Name)
		}
	}
	if len(keys) > 0 {
		return s.provider.RemoveOUTags(ctx, keys)
	}
	return nil
}

func money(v float64) string {
	if v == float64(int(v)) {
		return fmt.Sprintf("$%d", int(v))
	}
	return fmt.Sprintf("$%.2f", v)
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

// Costs returns daily spend for the last n days, pulled live and cached for
// CostTTL. Cost Explorer bills per call, so callers should batch with WarmCosts.
func (s *Service) Costs(ctx context.Context, id string, days int, force bool) ([]CostPoint, error) {
	a, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if a.ProviderID == "" {
		return nil, nil
	}
	s.mu.Lock()
	e, ok := s.costs[a.ProviderID]
	s.mu.Unlock()
	if ok && !force && s.Now().Sub(e.at) < s.cfg.CostTTL {
		return window(e.points, days, s.Now()), nil
	}
	now := s.Now()
	to := now.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	from := to.Add(-45 * 24 * time.Hour)
	pts, err := s.provider.Costs(ctx, a.ProviderID, from, to)
	if err != nil {
		return nil, fmt.Errorf("costs for %s: %w", a.Name, err)
	}
	s.mu.Lock()
	s.costs[a.ProviderID] = costEntry{points: pts, at: now}
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

// CostsCached returns cached points without hitting the provider.
func (s *Service) CostsCached(providerID string, days int) ([]CostPoint, bool) {
	s.mu.Lock()
	e, ok := s.costs[providerID]
	s.mu.Unlock()
	if !ok {
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

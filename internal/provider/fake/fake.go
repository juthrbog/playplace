// Package fake is an in-memory core.Provider for tests and local demos. It
// models an organization with a playground OU, tagged accounts, creation
// requests, budgets, and generated costs.
package fake

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"playplace/internal/core"
)

type account struct {
	core.RemoteAccount
	inOU bool
}

// Provider is safe for concurrent use.
type Provider struct {
	mu sync.Mutex

	Now         func() time.Time
	PollsToDone int    // creation succeeds after this many CreateStatus calls (0 = immediately)
	FailNext    string // if set, the next RequestAccount fails at the provider with this reason
	QuotaHit    bool   // CloseAccount returns ErrCloseQuota while true
	AsyncClose  bool   // CloseAccount transitions to PENDING_CLOSURE instead of CLOSED
	DailyCost   float64
	CostCalls   int // how many times Costs was called, for tests that watch spend on Cost Explorer

	accounts      map[string]*account
	requests      map[string]*request
	Budgets       map[string]float64
	BudgetStates  map[string]core.BudgetSnapshot
	BudgetFailure string
	BudgetCalls   int
	RetireCalls   int
	RetireFailure string
	Closed        []string
	seq           int

	// Identity: when AccessOn is true, owners must exist in Users (keyed by
	// email or user name) and grants are recorded in Grants by account id.
	AccessOn bool
	Users    map[string]core.User
	Grants   map[string][]string // provider id -> principal ids

	ouTags map[string]string
}

type request struct {
	core.CreateRequest
	polls int
	tags  map[string]string
	email string
}

func New() *Provider {
	return &Provider{
		Now:          time.Now,
		accounts:     map[string]*account{},
		requests:     map[string]*request{},
		Budgets:      map[string]float64{},
		BudgetStates: map[string]core.BudgetSnapshot{},
		DailyCost:    1.5,
		Users:        map[string]core.User{},
		Grants:       map[string][]string{},
		ouTags:       map[string]string{},
	}
}

func (p *Provider) OUTags(context.Context) (map[string]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[string]string{}
	maps.Copy(out, p.ouTags)
	return out, nil
}

// checkTags mirrors AWS: 50 tags per resource, keys up to 128 and values up
// to 256 characters, and only letters, numbers, spaces, and _ . : / = + - @.
func checkTags(existing map[string]string, tags map[string]string) error {
	n := len(existing)
	for k, v := range tags {
		if _, had := existing[k]; !had {
			n++
		}
		if len([]rune(k)) == 0 || len([]rune(k)) > core.MaxTagKey || !core.ValidTagValue(k) {
			return fmt.Errorf("InvalidInputException: INVALID_PATTERN:TAG_KEY %q", k)
		}
		if !core.ValidTagValue(v) {
			return fmt.Errorf("InvalidInputException: INVALID_PATTERN:TAG_VALUE for %s: %q", k, v)
		}
	}
	if n > core.MaxTagsPerResource {
		return fmt.Errorf("ConstraintViolationException: tag limit of %d reached", core.MaxTagsPerResource)
	}
	return nil
}

func (p *Provider) SetOUTags(_ context.Context, tags map[string]string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := checkTags(p.ouTags, tags); err != nil {
		return err
	}
	maps.Copy(p.ouTags, tags)
	return nil
}

func (p *Provider) RemoveOUTags(_ context.Context, keys []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, k := range keys {
		delete(p.ouTags, k)
	}
	return nil
}

// AddUser registers an identity-store user reachable by email and user name.
func (p *Provider) AddUser(id, userName, email string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	u := core.User{ID: id, UserName: userName, Email: email}
	p.Users[email] = u
	p.Users[userName] = u
}

func (p *Provider) AccessEnabled() bool { return p.AccessOn }

func (p *Provider) LookupUser(_ context.Context, identity string) (core.User, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if u, ok := p.Users[identity]; ok {
		return u, nil
	}
	return core.User{}, core.ErrUserNotFound
}

func (p *Provider) GrantAccess(_ context.Context, id string, user core.User) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.accounts[id]; !ok {
		return fmt.Errorf("no such account %s", id)
	}
	if slices.Contains(p.Grants[id], user.ID) {
		return nil
	}
	p.Grants[id] = append(p.Grants[id], user.ID)
	return nil
}

func (p *Provider) RevokeAccess(_ context.Context, id string, user core.User) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	kept := p.Grants[id][:0]
	for _, g := range p.Grants[id] {
		if g != user.ID {
			kept = append(kept, g)
		}
	}
	p.Grants[id] = kept
	return nil
}

func (p *Provider) Name() string { return "fake" }

func (p *Provider) EnsureOrganization(context.Context) (core.OrgInfo, error) {
	return core.OrgInfo{OrgID: "o-fake", ManagementAccountID: "000000000000", RootID: "r-fake", PlaygroundOUID: "ou-fake"}, nil
}

// Seed adds an account straight into the OU, for tests that need history.
func (p *Provider) Seed(r core.RemoteAccount) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.Tags == nil {
		r.Tags = map[string]string{}
	}
	if r.Status == "" {
		r.Status = "ACTIVE"
	}
	p.accounts[r.ProviderID] = &account{RemoteAccount: r, inOU: true}
}

func (p *Provider) RequestAccount(_ context.Context, req core.AccountRequest) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := checkTags(nil, req.Tags); err != nil {
		return "", err
	}
	p.seq++
	id := fmt.Sprintf("car-%04d", p.seq)
	r := &request{CreateRequest: core.CreateRequest{RequestID: id, Name: req.Name, State: core.CreateInProgress, RequestedAt: p.Now()}, tags: req.Tags, email: req.Email}
	// AWS accepts the request and fails it later when the root email is
	// already taken, including by a closed account.
	if p.FailNext == "" {
		for _, a := range p.accounts {
			if a.Email == req.Email {
				p.FailNext = "EMAIL_ALREADY_EXISTS"
			}
		}
		for _, other := range p.requests {
			if other.email == req.Email && other.State != core.CreateFailed {
				p.FailNext = "EMAIL_ALREADY_EXISTS"
			}
		}
	}
	if p.FailNext != "" {
		r.State, r.FailureReason, r.CompletedAt = core.CreateFailed, p.FailNext, p.Now()
		p.FailNext = ""
	}
	p.requests[id] = r
	return id, nil
}

func (p *Provider) settle(r *request) {
	if r.State != core.CreateInProgress {
		return
	}
	r.polls++
	if r.polls <= p.PollsToDone {
		return
	}
	p.seq++
	pid := fmt.Sprintf("%012d", 100000000000+p.seq)
	r.State, r.ProviderID, r.CompletedAt = core.CreateSucceeded, pid, p.Now()
	tags := map[string]string{}
	maps.Copy(tags, r.tags)
	p.accounts[pid] = &account{RemoteAccount: core.RemoteAccount{ProviderID: pid, Name: r.Name, Email: r.email, Status: "ACTIVE", JoinedAt: p.Now(), Tags: tags}}
}

func (p *Provider) CreateStatus(_ context.Context, id string) (core.CreateRequest, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.requests[id]
	if !ok {
		return core.CreateRequest{}, fmt.Errorf("no such request %s", id)
	}
	p.settle(r)
	return r.CreateRequest, nil
}

// Settle completes every pending creation, like time passing at the provider.
func (p *Provider) Settle() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range p.requests {
		r.polls = p.PollsToDone
		p.settle(r)
	}
}

func (p *Provider) ListCreateRequests(context.Context) ([]core.CreateRequest, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []core.CreateRequest
	for _, r := range p.requests {
		out = append(out, r.CreateRequest)
	}
	return out, nil
}

func (p *Provider) GetAccount(_ context.Context, id string) (core.RemoteAccount, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	a, ok := p.accounts[id]
	if !ok {
		return core.RemoteAccount{}, fmt.Errorf("no such account %s", id)
	}
	return cloneRemote(a.RemoteAccount), nil
}

func (p *Provider) ListPlaygroundAccounts(context.Context) ([]core.RemoteAccount, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []core.RemoteAccount
	for _, a := range p.accounts {
		if a.inOU {
			out = append(out, cloneRemote(a.RemoteAccount))
		}
	}
	return out, nil
}

func (p *Provider) PlaceAccount(_ context.Context, id string, tags map[string]string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	a, ok := p.accounts[id]
	if !ok {
		return fmt.Errorf("no such account %s", id)
	}
	if err := checkTags(a.Tags, tags); err != nil {
		return err
	}
	a.inOU = true
	maps.Copy(a.Tags, tags)
	return nil
}

func (p *Provider) SetTags(_ context.Context, id string, tags map[string]string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	a, ok := p.accounts[id]
	if !ok {
		return fmt.Errorf("no such account %s", id)
	}
	if err := checkTags(a.Tags, tags); err != nil {
		return err
	}
	maps.Copy(a.Tags, tags)
	return nil
}

func (p *Provider) RemoveTags(_ context.Context, id string, keys []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	a, ok := p.accounts[id]
	if !ok {
		return fmt.Errorf("no such account %s", id)
	}
	for _, k := range keys {
		delete(a.Tags, k)
	}
	return nil
}

func (p *Provider) CloseAccount(_ context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	a, ok := p.accounts[id]
	if !ok {
		return fmt.Errorf("no such account %s", id)
	}
	if a.Status != "ACTIVE" {
		return nil
	}
	if p.QuotaHit {
		return core.ErrCloseQuota
	}
	a.Status = "CLOSED"
	if p.AsyncClose {
		a.Status = "PENDING_CLOSURE"
	}
	p.Closed = append(p.Closed, id)
	return nil
}

func (p *Provider) EnsureBudget(_ context.Context, id string, spec core.BudgetSpec) (core.BudgetProtection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.BudgetCalls++
	if p.BudgetFailure != "" {
		return core.BudgetProtection{}, fmt.Errorf("%s", p.BudgetFailure)
	}
	if err := spec.Validate(); err != nil {
		return core.BudgetProtection{}, err
	}
	p.Budgets[id] = spec.LimitUSD
	s := p.BudgetStates[id]
	s.LimitUSD = spec.LimitUSD
	defer func() { p.BudgetStates[id] = s }()
	if !spec.Enforced() {
		state := "alerts-only"
		if spec.Email == "" {
			state = "alerts-disabled"
		}
		return core.BudgetProtection{State: state, Configured: true}, nil
	}
	if s.ActionID == "" {
		s.ActionID, s.ActionStatus = "fake-"+id, "STANDBY"
	}
	r := core.BudgetProtection{State: "recovering", ActionID: s.ActionID}
	if c := spec.Recovery; c != nil && c.Period == spec.Now.UTC().Format("2006-01") {
		if c.ActionID != s.ActionID || c.Limit != spec.LimitUSD {
			return r, fmt.Errorf("recovery mismatch")
		}
		if c.Phase == "reverse" {
			switch s.ActionStatus {
			case "EXECUTION_SUCCESS":
				s.ActionStatus = "REVERSE_IN_PROGRESS"
				return r, nil
			case "REVERSE_IN_PROGRESS":
				s.ActionStatus = "REVERSE_SUCCESS"
				r.RecoveryPhase = "reset"
				return r, nil
			case "REVERSE_SUCCESS":
				r.RecoveryPhase = "reset"
				return r, nil
			}
		} else {
			switch s.ActionStatus {
			case "REVERSE_SUCCESS":
				s.ActionStatus = "RESET_IN_PROGRESS"
				return r, nil
			case "RESET_IN_PROGRESS":
				s.ActionStatus = "STANDBY"
			}
		}
		r.RecoveryDone = true
	} else if spec.Recovery != nil {
		r.RecoveryDone = true
	}
	if s.ActionStatus == "STANDBY" && s.SpendKnown && s.SpendUSD >= spec.LimitUSD {
		s.ActionStatus = "EXECUTION_SUCCESS"
	}
	switch s.ActionStatus {
	case "STANDBY":
		r.State, r.Configured = "ready", true
	case "EXECUTION_SUCCESS":
		r.State, r.Configured = "restricted", true
	default:
		return r, fmt.Errorf("budget action state %s", s.ActionStatus)
	}
	return r, nil
}

func (p *Provider) InspectBudget(_ context.Context, id string, _ core.BudgetSpec) (core.BudgetSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.BudgetFailure != "" {
		return core.BudgetSnapshot{}, fmt.Errorf("%s", p.BudgetFailure)
	}
	s := p.BudgetStates[id]
	s.LimitUSD = p.Budgets[id]
	return s, nil
}

func (p *Provider) RetireBudgetAction(_ context.Context, id string, spec core.BudgetSpec) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.RetireCalls++
	if p.RetireFailure != "" {
		return false, fmt.Errorf("%s", p.RetireFailure)
	}
	a := p.accounts[id]
	if a == nil || a.Status != "CLOSED" || a.Tags[core.TagManaged] != "true" || spec.PolicyID == "" || spec.RoleARN == "" {
		return false, fmt.Errorf("retirement requires a closed managed account and enforcement configuration")
	}
	s := p.BudgetStates[id]
	if s.ActionID == "" {
		return true, nil
	}
	s.ActionID, s.ActionStatus = "", ""
	p.BudgetStates[id] = s // Preserve the budget and spend, just remove its action.
	return false, nil      // Observe absence on the next pass, like the AWS provider.
}

func (p *Provider) Costs(_ context.Context, _ string, from, to time.Time) ([]core.CostPoint, error) {
	p.mu.Lock()
	p.CostCalls++
	daily := p.DailyCost
	p.mu.Unlock()
	var out []core.CostPoint
	for d := from; d.Before(to); d = d.Add(24 * time.Hour) {
		out = append(out, core.CostPoint{Date: d, AmountUSD: daily})
	}
	return out, nil
}

// Tags returns a copy of an account's tags, for assertions.
func (p *Provider) Tags(id string) map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if a, ok := p.accounts[id]; ok {
		return cloneRemote(a.RemoteAccount).Tags
	}
	return nil
}

func cloneRemote(r core.RemoteAccount) core.RemoteAccount {
	tags := map[string]string{}
	maps.Copy(tags, r.Tags)
	r.Tags = tags
	return r
}

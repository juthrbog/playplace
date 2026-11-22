package core

import (
	"testing"
	"time"
)

// HandoffBlocker names the first gate of reconcileReadiness that holds the
// account back, for every kind.
func TestHandoffBlockerKinds(t *testing.T) {
	closeAt := t0.Add(-time.Hour)
	// placed is an account that readiness progresses: managed, ACTIVE,
	// active, not closing, not expired, protection ready, a real owner and
	// access granted.
	placed := func(change func(a *Account)) *Account {
		a := &Account{
			Name: "dev", Managed: true, ProviderState: "ACTIVE", Status: StatusActive,
			ExpiresAt: t0.Add(time.Hour), BudgetHealth: "ready", Owner: "alex@example.com",
			AccessGrantedTo: "principal-1", handoff: handoffPending,
		}
		if change != nil {
			change(a)
		}
		return a
	}
	cases := []struct {
		name   string
		a      *Account
		access bool
		want   HandoffBlock
	}{
		{"ready", placed(nil), true, ""},
		{"placing", placed(func(a *Account) { a.handoff = handoffPlacing }), true, ""},
		{"repair", placed(func(a *Account) { a.invalidTag(TagBudget, "is missing or invalid") }), true, HandoffStalled},
		{"not managed", placed(func(a *Account) { a.Managed = false }), true, HandoffStalled},
		{"suspended", placed(func(a *Account) { a.ProviderState = "SUSPENDED"; a.Status = StatusUnavailable }), true, HandoffStalled},
		{"provider state only", placed(func(a *Account) { a.ProviderState = "PENDING_ACTIVATION" }), true, HandoffStalled},
		{"creating", placed(func(a *Account) { a.Status = StatusCreating }), true, HandoffStalled},
		{"close requested", placed(func(a *Account) { a.CloseRequestedAt = &closeAt }), true, HandoffStalled},
		{"expired", placed(func(a *Account) { a.ExpiresAt = t0 }), true, HandoffStalled},
		{"expiring still progresses", placed(func(a *Account) { a.Status = StatusExpiring }), true, ""},
		// Stalled comes before the budget, as in reconcileReadiness.
		{"expired and unchecked", placed(func(a *Account) { a.ExpiresAt = t0; a.BudgetHealth = "" }), true, HandoffStalled},
		{"budget unchecked", placed(func(a *Account) { a.BudgetHealth = "" }), true, HandoffBudgetUnchecked},
		{"budget setup pending", placed(func(a *Account) { a.BudgetHealth = "setup-pending" }), true, HandoffBudgetSettling},
		{"budget restricting", placed(func(a *Account) { a.BudgetHealth = "restricting" }), true, HandoffBudgetSettling},
		{"budget recovering", placed(func(a *Account) { a.BudgetHealth = "recovering" }), true, HandoffBudgetSettling},
		{"budget restricted", placed(func(a *Account) { a.BudgetHealth = "restricted" }), true, HandoffBudgetProblem},
		{"budget error", placed(func(a *Account) { a.BudgetHealth = "error" }), true, HandoffBudgetProblem},
		{"alerts only", placed(func(a *Account) { a.BudgetHealth = "alerts-only" }), true, ""},
		{"alerts disabled", placed(func(a *Account) { a.BudgetHealth = "alerts-disabled" }), true, ""},
		// The budget comes before the owner.
		{"budget before owner", placed(func(a *Account) { a.BudgetHealth = ""; a.Owner = "unknown"; a.AccessGrantedTo = "" }), true, HandoffBudgetUnchecked},
		{"owner unknown, access on", placed(func(a *Account) { a.Owner = " Unknown "; a.AccessGrantedTo = "" }), true, HandoffOwnerUnknown},
		{"owner question mark, access off", placed(func(a *Account) { a.Owner = "?"; a.AccessGrantedTo = "" }), false, HandoffOwnerUnknown},
		{"owner empty, access off", placed(func(a *Account) { a.Owner = "" }), false, HandoffOwnerUnknown},
		// readiness checks only the grant once access is granted.
		{"owner unknown, access granted", placed(func(a *Account) { a.Owner = "unknown" }), true, ""},
		{"access pending", placed(func(a *Account) { a.AccessGrantedTo = "" }), true, HandoffAccessPending},
		{"access not managed", placed(func(a *Account) { a.AccessGrantedTo = "" }), false, ""},
		{"complete", placed(func(a *Account) { a.handoff = handoffComplete; a.AccessGrantedTo = ""; a.BudgetHealth = "" }), true, ""},
		// A legacy account has no handoff tag. It gets a kind only for
		// missing access that playplace manages, while it can be extended.
		{"legacy without access", placed(func(a *Account) { a.handoff = ""; a.AccessGrantedTo = ""; a.BudgetHealth = "" }), true, HandoffAccessPending},
		{"legacy, access not managed", placed(func(a *Account) { a.handoff = ""; a.AccessGrantedTo = "" }), false, ""},
		{"legacy with access", placed(func(a *Account) { a.handoff = ""; a.BudgetHealth = "error" }), true, ""},
		{"legacy closing", placed(func(a *Account) { a.handoff = ""; a.AccessGrantedTo = ""; a.Status = StatusClosing }), true, ""},
		{"legacy with damaged expiry", placed(func(a *Account) {
			a.handoff = ""
			a.AccessGrantedTo = ""
			a.invalidTag(TagExpires, "is missing or invalid")
		}), true, ""},
	}
	for _, tc := range cases {
		if got := tc.a.HandoffBlocker(tc.access, t0); got != tc.want {
			t.Errorf("%s: HandoffBlocker = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestLatestExpiry(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxTTL = 90 * 24 * time.Hour
	svc := NewService(nil, nil, cfg, nil)
	svc.Now = func() time.Time { return t0 }
	created := t0.Add(-10 * 24 * time.Hour)
	a := &Account{Name: "dev", CreatedAt: created}

	limit, ok := svc.LatestExpiry(a)
	if !ok || !limit.Equal(created.Add(cfg.MaxTTL)) {
		t.Fatalf("LatestExpiry = %s, %v; want %s", limit, ok, created.Add(cfg.MaxTTL))
	}
	// The boundary itself is allowed, and a second past it is refused with
	// the same date.
	if err := svc.checkLifetime(a, limit, false); err != nil {
		t.Fatalf("an expiry at the latest date is refused: %v", err)
	}
	err := svc.checkLifetime(a, limit.Add(time.Second), false)
	if refusal, isLimit := err.(LifetimeLimitError); !isLimit || !refusal.Latest.Equal(limit) {
		t.Fatalf("a second past the latest date = %v, want a LifetimeLimitError at %s", err, limit)
	}
	if err := svc.checkLifetime(a, limit.Add(time.Second), true); err != nil {
		t.Fatalf("an override is refused: %v", err)
	}

	// With no creation time there is no fixed date, and the check keeps
	// counting from now, so nothing that works today is refused.
	unknown := &Account{Name: "old"}
	if got, ok := svc.LatestExpiry(unknown); ok {
		t.Fatalf("LatestExpiry with no creation time = %s, want none", got)
	}
	if err := svc.checkLifetime(unknown, t0.Add(cfg.MaxTTL), false); err != nil {
		t.Fatalf("no creation time: the limit no longer counts from now: %v", err)
	}
	if err := svc.checkLifetime(unknown, t0.Add(cfg.MaxTTL+time.Second), false); err == nil {
		t.Fatal("no creation time: an expiry past now plus the ceiling is not refused")
	}

	// With no ceiling there is no date, and nothing is refused.
	cfg.MaxTTL = 0
	open := NewService(nil, nil, cfg, nil)
	if got, ok := open.LatestExpiry(a); ok {
		t.Fatalf("LatestExpiry with no ceiling = %s, want none", got)
	}
	if err := open.checkLifetime(a, created.Add(10*365*24*time.Hour), false); err != nil {
		t.Fatalf("no ceiling: %v", err)
	}
}

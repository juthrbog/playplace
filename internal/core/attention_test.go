package core_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"playplace/internal/core"
)

// remote is an account in the OU with valid tags, as AWS lists it.
func remote(extra map[string]string) core.RemoteAccount {
	tags := map[string]string{core.TagManaged: "true", core.TagOwner: "anna@example.com", core.TagExpires: t0.Add(10 * 24 * time.Hour).Format(time.RFC3339), core.TagBudget: "50"}
	for k, v := range extra {
		tags[k] = v
	}
	return core.RemoteAccount{ProviderID: "111111111111", Name: "box", Status: "ACTIVE", JoinedAt: t0.Add(-24 * time.Hour), Tags: tags}
}

func TestAttentionNamesEveryReasonInLedgerOrder(t *testing.T) {
	cfg := core.DefaultConfig()
	at := func(d time.Duration) string { return t0.Add(d).Format(time.RFC3339) }
	// A close request older than CloseAlertAfter makes the closure overdue.
	overdue := core.FromRemote(remote(map[string]string{core.TagCloseRequested: at(-cfg.CloseAlertAfter)}), cfg, t0)
	young := core.FromRemote(remote(map[string]string{core.TagCloseRequested: at(-cfg.CloseAlertAfter + time.Second)}), cfg, t0)
	suspended := core.FromRemote(core.RemoteAccount{ProviderID: "222222222222", Name: "held", Status: "SUSPENDED", Tags: remote(nil).Tags}, cfg, t0)
	damaged := core.FromRemote(remote(map[string]string{core.TagExpires: "broken"}), cfg, t0)
	damagedClosed := core.FromRemote(core.RemoteAccount{ProviderID: "333333333333", Name: "gone", Status: "CLOSED", Tags: map[string]string{core.TagManaged: "true", core.TagBudget: "broken"}}, cfg, t0)
	settling := func(health, checked string) *core.Account {
		return &core.Account{Status: core.StatusActive, BudgetHealth: health, BudgetChecked: checked}
	}
	for _, c := range []struct {
		name string
		a    *core.Account
		want []core.AttentionKind
	}{
		{"active and ready", &core.Account{Status: core.StatusActive, BudgetHealth: "ready"}, nil},
		{"pending", &core.Account{Status: core.StatusPending}, nil},
		{"creating", &core.Account{Status: core.StatusCreating}, nil},
		{"closed", &core.Account{Status: core.StatusClosed, BudgetHealth: "retired"}, nil},
		{"alerts only", &core.Account{Status: core.StatusActive, BudgetHealth: "alerts-only"}, nil},
		{"alerts disabled", &core.Account{Status: core.StatusActive, BudgetHealth: "alerts-disabled"}, nil},
		{"closure overdue", overdue, []core.AttentionKind{core.AttentionClosureOverdue}},
		{"closure not yet overdue", young, nil},
		{"failed", &core.Account{Status: core.StatusFailed, LastError: "EMAIL_ALREADY_EXISTS"}, []core.AttentionKind{core.AttentionFailed}},
		{"restricted", &core.Account{Status: core.StatusActive, BudgetHealth: "restricted"}, []core.AttentionKind{core.AttentionRestricted}},
		{"budget error", &core.Account{Status: core.StatusActive, BudgetHealth: "error"}, []core.AttentionKind{core.AttentionBudgetError}},
		{"damaged tags", damaged, []core.AttentionKind{core.AttentionRepair}},
		// Nothing acts on the tags of a closed account any more.
		{"damaged tags on a closed account", damagedClosed, nil},
		{"setup-pending for 2 hours", settling("setup-pending", at(-2*time.Hour)), []core.AttentionKind{core.AttentionSettlingLong}},
		{"restricting for 2 hours", settling("restricting", at(-2*time.Hour)), []core.AttentionKind{core.AttentionSettlingLong}},
		{"recovering for 3 hours", settling("recovering", at(-3*time.Hour)), []core.AttentionKind{core.AttentionSettlingLong}},
		{"retiring on a closed account for 2 hours", &core.Account{Status: core.StatusClosed, Managed: true, BudgetPolicy: "p-x", BudgetHealth: "retiring", BudgetChecked: at(-2 * time.Hour)}, []core.AttentionKind{core.AttentionSettlingLong}},
		{"a retirement error on a closed account", &core.Account{Status: core.StatusClosed, Managed: true, BudgetPolicy: "p-x", BudgetHealth: "error"}, []core.AttentionKind{core.AttentionBudgetError}},
		// Retirement needs enforcement, so nothing ever clears these states.
		{"a budget error on a closed account with no policy", &core.Account{Status: core.StatusClosed, Managed: true, BudgetHealth: "error"}, nil},
		{"restricted on a closed account with no policy", &core.Account{Status: core.StatusClosed, Managed: true, BudgetHealth: "restricted"}, nil},
		{"settling on a closed account with no policy", &core.Account{Status: core.StatusClosed, Managed: true, BudgetHealth: "setup-pending", BudgetChecked: at(-3 * time.Hour)}, nil},
		{"a budget error on a closed account that is not managed", &core.Account{Status: core.StatusClosed, BudgetPolicy: "p-x", BudgetHealth: "error"}, nil},
		{"setup-pending for less than 2 hours", settling("setup-pending", at(-2*time.Hour+time.Second)), nil},
		{"settling with no check time", settling("restricting", ""), []core.AttentionKind{core.AttentionSettlingLong}},
		{"settling with a damaged check time", settling("recovering", "yesterday"), []core.AttentionKind{core.AttentionSettlingLong}},
		{"ready with a damaged check time", settling("ready", "yesterday"), nil},
		{"unavailable", suspended, []core.AttentionKind{core.AttentionUnavailable}},
		{"expiring", &core.Account{Status: core.StatusExpiring, BudgetHealth: "ready"}, []core.AttentionKind{core.AttentionExpiring}},
		{"several at once", func() *core.Account {
			a := core.FromRemote(remote(map[string]string{core.TagWarnedAt: at(-time.Hour), core.TagBudget: "lots"}), cfg, t0)
			a.BudgetHealth = "restricted"
			return a
		}(), []core.AttentionKind{core.AttentionRestricted, core.AttentionRepair, core.AttentionExpiring}},
		{"overdue with a budget error", func() *core.Account {
			a := *overdue
			a.BudgetHealth = "error"
			return &a
		}(), []core.AttentionKind{core.AttentionClosureOverdue, core.AttentionBudgetError}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.a.Attention(t0); !slices.Equal(got, c.want) {
				t.Fatalf("Attention = %v, want %v", got, c.want)
			}
		})
	}
}

// Without enforcement, refresh never retires the budget of a closed account,
// so a budget error from before the closure stays. Nobody can act on it, so
// it needs no attention.
func TestAClosedAccountWithoutEnforcementNeedsNoBudgetAttention(t *testing.T) {
	h := newHarness(t)
	h.prov.Seed(core.RemoteAccount{ProviderID: "444444444444", Name: "old-box", Status: "CLOSED", JoinedAt: t0.Add(-30 * 24 * time.Hour), Tags: map[string]string{
		core.TagManaged: "true", core.TagOwner: "anna@example.com", core.TagExpires: t0.Add(-24 * time.Hour).Format(time.RFC3339), core.TagBudget: "50",
		core.TagBudgetHealth: "error", core.TagClosedAt: t0.Add(-24 * time.Hour).Format(time.RFC3339),
	}})
	ctx := context.Background()
	for range 2 {
		if _, err := h.svc.Refresh(ctx); err != nil {
			t.Fatal(err)
		}
		h.advance(24 * time.Hour)
	}
	a, err := h.svc.Resolve(ctx, "444444444444")
	if err != nil {
		t.Fatal(err)
	}
	if a.BudgetHealth != "error" {
		t.Fatalf("refresh changed the budget state to %q, so the premise of this test no longer holds", a.BudgetHealth)
	}
	if got := a.Attention(h.svc.Now()); len(got) != 0 {
		t.Errorf("Attention = %v, want nothing for a state that nothing clears", got)
	}
}

// The web filter puts each kind in a page address, so the values are fixed.
func TestAttentionKindsAreTheirAddressValues(t *testing.T) {
	for kind, want := range map[core.AttentionKind]string{
		core.AttentionClosureOverdue: "closure-overdue",
		core.AttentionFailed:         "failed",
		core.AttentionRestricted:     "restricted",
		core.AttentionBudgetError:    "budget-error",
		core.AttentionRepair:         "repair",
		core.AttentionSettlingLong:   "settling-long",
		core.AttentionUnavailable:    "unavailable",
		core.AttentionExpiring:       "expiring",
	} {
		if string(kind) != want {
			t.Errorf("the address value is %q, want %q", string(kind), want)
		}
	}
}

// The web shows an overdue closure with the same rule as Attention.
func TestClosureOverdueIsAClosingAccountWithAnError(t *testing.T) {
	for _, c := range []struct {
		a    core.Account
		want bool
	}{
		{core.Account{Status: core.StatusClosing, LastError: "closure unconfirmed"}, true},
		{core.Account{Status: core.StatusClosing}, false},
		{core.Account{Status: core.StatusUnavailable, LastError: "closure unconfirmed"}, false},
		{core.Account{Status: core.StatusCreating, LastError: "waits outside the OU"}, false},
	} {
		if got := c.a.ClosureOverdue(); got != c.want {
			t.Errorf("%s with error %q: ClosureOverdue = %v, want %v", c.a.Status, c.a.LastError, got, c.want)
		}
	}
}

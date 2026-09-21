package core_test

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
)

func enforceBudgets(h *harness) {
	cfg := h.svc.Config()
	cfg.BudgetPolicyID, cfg.BudgetActionRole, cfg.AlertEmail = "p-budget123", "arn:aws:iam::000000000000:role/Budgets", "ops@example.com"
	h.svc = core.NewService(h.prov, h.note, cfg, nil)
	h.svc.Now = func() time.Time { return h.now }
}

func TestBudgetFailureWithholdsAccessAndRetriesManagedAccounts(t *testing.T) {
	h := newHarness(t)
	h.prov.AccessOn = true
	h.prov.Users["owner"] = core.User{ID: "user1", UserName: "owner"}
	h.prov.BudgetFailure = "temporary AWS failure"
	ctx := context.Background()
	a, err := h.svc.Request(ctx, core.RequestInput{Name: "budget-retry", Owner: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	a, err = h.svc.PollCreate(ctx, a.RequestID)
	if err == nil || a == nil {
		t.Fatalf("placement should report pending protection: %v", err)
	}
	if len(h.prov.Grants[a.ID]) != 0 {
		t.Fatal("access granted despite budget failure")
	}
	for _, m := range h.note.msgs {
		if strings.Contains(m.Subject, "account ready") {
			t.Fatal("premature ready notification")
		}
	}
	if h.prov.Tags(a.ID)[core.TagBudgetHealth] != "error" {
		t.Fatal("missing durable budget failure")
	}
	h.prov.BudgetFailure = ""
	if _, err := h.svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.prov.Grants[a.ID]) != 1 || h.prov.Budgets[a.ID] != a.BudgetUSD {
		t.Fatal("managed account was not repaired/granted")
	}
}

func TestBudgetFailureDoesNotBlockExpiry(t *testing.T) {
	h := newHarness(t)
	a := h.active(t, "budget-expiry", "owner", time.Hour)
	h.prov.BudgetFailure = "denied"
	h.advance(2 * time.Hour)
	if _, err := h.svc.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.prov.Closed) != 1 || h.prov.Closed[0] != a.ID {
		t.Fatal("budget API failure blocked closure")
	}
}

func TestBudgetIncreaseRecoversAndRearmsAcrossRestarts(t *testing.T) {
	h := newHarness(t)
	enforceBudgets(h)
	a := h.active(t, "budget-raise", "owner", 48*time.Hour)
	snapshot := h.prov.BudgetStates[a.ID]
	snapshot.SpendUSD, snapshot.SpendKnown, snapshot.ActionStatus = 60, true, "EXECUTION_SUCCESS"
	h.prov.BudgetStates[a.ID] = snapshot
	ctx := context.Background()
	changed, err := h.svc.SetBudget(ctx, a.ID, 100, "admin", "More testing", false)
	if err == nil || changed == nil || changed.BudgetHealth != "recovering" {
		t.Fatalf("want pending recovery, got %+v %v", changed, err)
	}
	if _, err := h.svc.SetBudget(ctx, a.ID, 120, "admin", "overlap", false); err == nil {
		t.Fatal("overlapping update accepted")
	}
	recovery, err := core.DecodeBudgetRecovery(h.prov.Tags(a.ID)[core.TagBudgetRecovery])
	if err != nil || recovery == nil || recovery.Spend != 60 {
		t.Fatalf("lost pre-update spend evidence: %+v %v", recovery, err)
	}
	// AWS temporarily reports zero after UpdateBudget. The durable approval,
	// not that zero, authorizes completion after a restart.
	snapshot = h.prov.BudgetStates[a.ID]
	snapshot.SpendUSD = 0
	h.prov.BudgetStates[a.ID] = snapshot
	for range 4 {
		enforceBudgets(h) // new Service, same provider tags: process restarts
		_, _ = h.svc.Refresh(ctx)
	}
	got, _ := h.svc.Get(ctx, a.ID)
	if got.BudgetHealth != "ready" || got.BudgetRecovery != "" || got.BudgetUSD != 100 {
		t.Fatalf("not recovered: %+v", got)
	}
	snapshot = h.prov.BudgetStates[a.ID]
	snapshot.SpendUSD = 110
	h.prov.BudgetStates[a.ID] = snapshot
	if _, err := h.svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = h.svc.Get(ctx, a.ID)
	if got.BudgetHealth != "restricted" {
		t.Fatal("enforcement was not rearmed")
	}
}

func TestInsufficientIncreaseAndUnknownSpendDoNotUnlock(t *testing.T) {
	for _, spend := range []float64{60, 0} {
		h := newHarness(t)
		enforceBudgets(h)
		a := h.active(t, "budget-insufficient", "owner", 48*time.Hour)
		snap := h.prov.BudgetStates[a.ID]
		snap.ActionStatus, snap.SpendKnown, snap.SpendUSD = "EXECUTION_SUCCESS", true, spend
		h.prov.BudgetStates[a.ID] = snap
		_, err := h.svc.SetBudget(context.Background(), a.ID, 55, "admin", "increase", false)
		if spend == 0 && err == nil {
			t.Fatal("recalculating zero accepted as sufficient spend evidence")
		}
		if h.prov.Tags(a.ID)[core.TagBudgetRecovery] != "" || h.prov.BudgetStates[a.ID].ActionStatus != "EXECUTION_SUCCESS" {
			t.Fatal("insufficient/unknown spend authorized reversal")
		}
	}
}

func TestBudgetValidationAndAudit(t *testing.T) {
	h := newHarness(t)
	a := h.active(t, "budget-validation", "owner", 48*time.Hour)
	ctx := context.Background()
	for _, n := range []float64{0, -1, math.NaN(), math.Inf(1), 1.001, 501} {
		if _, err := h.svc.SetBudget(ctx, a.ID, n, "admin", "test", false); err == nil {
			t.Fatalf("accepted %v", n)
		}
	}
	if _, err := h.svc.SetBudget(ctx, a.ID, 60, "admin", "", false); err == nil {
		t.Fatal("missing audit reason accepted")
	}
	audit := &recorderAudit{}
	h.svc.SetAuditor(audit)
	if _, err := h.svc.SetBudget(ctx, a.ID, 600, "admin", "approved exception", true); err != nil {
		t.Fatal(err)
	}
	if len(audit.events) == 0 || audit.events[0].Event != "budget-changed" || audit.events[0].Actor != "admin" || audit.events[0].Details["override"] != "true" {
		t.Fatalf("missing audit: %+v", audit.events)
	}
}

func TestBudgetRecoveryTagFitsAndRoundTrips(t *testing.T) {
	r := core.BudgetRecovery{ActionID: "12345678-abcd-abcd-abcd-123456789012", Phase: "reverse", Period: "2026-09", Limit: 1000000000000, Spend: 999999999999}
	s := r.Encode()
	if len(s) > 256 || !core.ValidTagValue(s) {
		t.Fatalf("invalid organization tag: %q", s)
	}
	decoded, err := core.DecodeBudgetRecovery(s)
	if err != nil || *decoded != r {
		t.Fatalf("roundtrip: %+v %v", decoded, err)
	}
	if _, err := core.DecodeBudgetRecovery("invalid"); err == nil {
		t.Fatal("malformed recovery accepted")
	}
}

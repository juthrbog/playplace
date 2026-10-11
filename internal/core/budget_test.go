package core_test

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
	"playplace/internal/provider/fake"
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
	a := h.ready(t, "budget-expiry", "owner", time.Hour)
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
	a := h.ready(t, "budget-raise", "owner", 48*time.Hour)
	snapshot := h.prov.BudgetStates[a.ID]
	snapshot.SpendUSD, snapshot.SpendKnown, snapshot.ActionStatus = 60, true, "EXECUTION_SUCCESS"
	h.prov.BudgetStates[a.ID] = snapshot
	ctx := context.Background()
	changed, err := h.svc.SetBudget(ctx, a.ID, 100, "admin", "More testing", false)
	if err != nil || changed == nil || changed.BudgetHealth != "recovering" {
		t.Fatalf("want saved change with recovery in progress, got %+v %v", changed, err)
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

// reportedFailure makes AWS report a failed reverse or reset on every
// observation while status is set. ExecuteBudgetAction is still accepted, as
// AWS accepts a resubmitted operation that then fails again.
type reportedFailure struct {
	*fake.Provider
	status string
}

func (p *reportedFailure) EnsureBudget(ctx context.Context, id string, spec core.BudgetSpec) (core.BudgetProtection, error) {
	r, err := p.Provider.EnsureBudget(ctx, id, spec)
	if p.status != "" && r.Action != nil {
		r.Action.Status, r.Action.RestrictionAttached = p.status, p.status == "REVERSE_FAILURE"
	}
	return r, err
}

// A recovery operation that AWS reports as failed is not settling protection.
// Playplace sends it again, and every such pass fails at once rather than
// after two hours. Health stays recovering, so notices do not flap, and the
// durable intent stays so that recovery resumes when AWS succeeds.
func TestReportedRecoveryFailureFailsEachPassAtOnce(t *testing.T) {
	for _, status := range []string{"REVERSE_FAILURE", "RESET_FAILURE"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			enforceBudgets(h)
			ctx := context.Background()
			a := h.ready(t, "reported-failure", "owner", 48*time.Hour)
			snap := h.prov.BudgetStates[a.ID]
			snap.SpendUSD, snap.SpendKnown, snap.ActionStatus = 60, true, "EXECUTION_SUCCESS"
			h.prov.BudgetStates[a.ID] = snap
			p := &reportedFailure{Provider: h.prov}
			svc := core.NewService(p, h.note, h.svc.Config(), nil)
			svc.Now = func() time.Time { return h.now }
			if got, err := svc.SetBudget(ctx, a.ID, 100, "admin", "raise", false); err != nil || got.BudgetHealth != "recovering" {
				t.Fatalf("saved increase: %+v %v", got, err)
			}
			if status == "RESET_FAILURE" {
				// The observed reverse success persists the reset phase.
				if _, err := svc.Refresh(ctx); err != nil {
					t.Fatal(err)
				}
				if r, err := core.DecodeBudgetRecovery(h.prov.Tags(a.ID)[core.TagBudgetRecovery]); err != nil || r == nil || r.Phase != "reset" {
					t.Fatalf("reset phase not persisted: %+v %v", r, err)
				}
			}
			p.status = status
			h.note.msgs = nil
			for range 2 {
				h.advance(10 * time.Minute)
				if _, err := svc.Refresh(ctx); err == nil || !strings.Contains(err.Error(), "AWS reported "+status) {
					t.Fatalf("reported %s did not fail the pass: %v", status, err)
				}
				if tags := h.prov.Tags(a.ID); tags[core.TagBudgetHealth] != "recovering" || tags[core.TagBudgetRecovery] == "" {
					t.Fatalf("health or intent changed: %v", tags)
				}
			}
			if len(h.note.msgs) != 0 {
				t.Fatalf("notices while AWS reports a failure: %+v", h.note.msgs)
			}
			p.status = ""
			for range 4 {
				if _, err := svc.Refresh(ctx); err != nil {
					t.Fatalf("recovery did not resume cleanly: %v", err)
				}
			}
			if got, err := svc.Get(ctx, a.ID); err != nil || got.BudgetHealth != "ready" || got.BudgetRecovery != "" {
				t.Fatalf("recovery did not finish once AWS succeeded: %+v %v", got, err)
			}
		})
	}
}

// A repair from error is announced when it starts. The step from setup-pending
// to ready is silent, so the start notice must not leave people expecting
// another one.
func TestBudgetRepairIsAnnouncedOnceWhenItStarts(t *testing.T) {
	h := newHarness(t)
	enforceBudgets(h)
	ctx := context.Background()
	a := h.ready(t, "repair", "owner", 48*time.Hour)
	h.prov.BudgetFailure = "temporary AWS failure"
	if _, err := h.svc.Refresh(ctx); err == nil {
		t.Fatal("budget failure did not fail the pass")
	}
	h.prov.BudgetFailure = ""
	// The action is gone, so the repair creates it again.
	snap := h.prov.BudgetStates[a.ID]
	snap.ActionID, snap.ActionStatus = "", ""
	h.prov.BudgetStates[a.ID] = snap
	h.note.msgs = nil
	for range 2 {
		if _, err := h.svc.Refresh(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := h.prov.Tags(a.ID)[core.TagBudgetHealth]; got != "ready" {
		t.Fatalf("health = %q, want ready", got)
	}
	if len(h.note.msgs) != 1 || !strings.Contains(h.note.msgs[0].Body, "being repaired (setup-pending)") || !strings.Contains(h.note.msgs[0].Body, "No further notice") {
		t.Fatalf("repair notices = %+v", h.note.msgs)
	}
}

func TestInsufficientIncreaseAndUnknownSpendDoNotUnlock(t *testing.T) {
	for _, spend := range []float64{60, 0} {
		h := newHarness(t)
		enforceBudgets(h)
		a := h.ready(t, "budget-insufficient", "owner", 48*time.Hour)
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
	a := h.ready(t, "budget-validation", "owner", 48*time.Hour)
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

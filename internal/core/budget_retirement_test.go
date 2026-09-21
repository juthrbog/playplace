package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"playplace/internal/core"
)

func TestBudgetActionRetiresOnlyAfterConfirmedClosure(t *testing.T) {
	for _, mode := range []string{"manual", "expiry", "external", "already-confirmed"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			enforceBudgets(h)
			ctx := context.Background()
			a := h.active(t, "retire-budget", "owner", time.Hour)
			// Interrupted/malformed recovery cannot prevent post-closure retirement.
			if err := h.prov.SetTags(ctx, a.ID, map[string]string{core.TagBudgetRecovery: "bad-intent"}); err != nil {
				t.Fatal(err)
			}
			h.note.msgs = nil
			switch mode {
			case "manual":
				closed, err := h.svc.RequestClose(ctx, a.ID, "admin")
				if err == nil || closed.Status != core.StatusClosed {
					t.Fatalf("closure should succeed while retirement awaits observation: %+v %v", closed, err)
				}
			case "expiry":
				h.advance(2 * time.Hour)
				sum, err := h.svc.Refresh(ctx)
				if err == nil || len(sum.Closed) != 1 || len(sum.Deferred) != 0 {
					t.Fatalf("cleanup must not hide closure: %+v %v", sum, err)
				}
			default:
				r, _ := h.prov.GetAccount(ctx, a.ID)
				r.Status = "CLOSED"
				if mode == "already-confirmed" {
					r.Tags[core.TagClosedAt] = h.now.Format(time.RFC3339)
					r.Tags[core.TagCloseRequested] = h.now.Format(time.RFC3339)
				}
				h.prov.Seed(r)
				if _, err := h.svc.Refresh(ctx); err == nil {
					t.Fatal("expected pending retirement")
				}
			}
			if h.prov.Tags(a.ID)[core.TagBudgetHealth] != "retiring" || h.prov.BudgetStates[a.ID].ActionID != "" {
				t.Fatal("delete was not requested and tracked")
			}
			budgetCalls := h.prov.BudgetCalls
			for range 2 {
				enforceBudgets(h) // restart: closed markers must not stop retirement
				if _, err := h.svc.Refresh(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if h.prov.BudgetCalls != budgetCalls || h.prov.Budgets[a.ID] != a.BudgetUSD {
				t.Fatal("budget was recreated, changed or deleted")
			}
			got, _ := h.svc.Get(ctx, a.ID)
			if got.Status != core.StatusClosed || got.BudgetHealth != "retired" || got.BudgetRecovery != "" || got.BudgetError != "" || got.BudgetPolicy == "" {
				t.Fatalf("retirement not persisted: %+v", got)
			}
			closures, retirements := 0, 0
			for _, msg := range h.note.msgs {
				if msg.Subject == "Playground account closed" {
					closures++
				}
				if msg.Body == "Budget protection is retired. " {
					retirements++
				}
			}
			wantClosures := 0
			if mode == "manual" || mode == "expiry" {
				wantClosures = 1
			}
			if closures != wantClosures || retirements != 1 {
				t.Fatalf("repeated/missing notifications: closed=%d retired=%d", closures, retirements)
			}
		})
	}
}

func TestBudgetRetirementFailureRetriesWithoutBlockingOtherClosures(t *testing.T) {
	h := newHarness(t)
	enforceBudgets(h)
	ctx := context.Background()
	a := h.active(t, "retire-retry", "owner", 48*time.Hour)
	other := h.active(t, "retire-other", "owner", time.Hour)
	h.prov.RetireFailure = "resource locked"
	closed, err := h.svc.RequestClose(ctx, a.ID, "admin")
	if err == nil || closed.Status != core.StatusClosed || h.prov.Tags(a.ID)[core.TagClosedAt] == "" {
		t.Fatalf("closure lost due to cleanup failure: %+v %v", closed, err)
	}
	h.advance(2 * time.Hour)
	sum, err := h.svc.Refresh(ctx)
	if err == nil || len(sum.Closed) != 1 || sum.Closed[0] != other.Name || h.prov.Tags(a.ID)[core.TagBudgetHealth] != "error" {
		t.Fatalf("retry/other closure failed: %+v %v", sum, err)
	}
	h.prov.RetireFailure = ""
	enforceBudgets(h)
	if _, err := h.svc.Refresh(ctx); err == nil {
		t.Fatal("accepted deletes must await confirmation")
	}
	if _, err := h.svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a.ID, other.ID} {
		if h.prov.Tags(id)[core.TagBudgetHealth] != "retired" || h.prov.Budgets[id] == 0 {
			t.Fatal("failed to retire while preserving budget")
		}
	}
}

func TestBudgetRetirementSkipsUnsafeLifecycleStates(t *testing.T) {
	for _, state := range []string{"ACTIVE", "PENDING_CLOSURE", "SUSPENDED", "PENDING_ACTIVATION", "NEW_STATE", "", "unmanaged-closed"} {
		t.Run(state, func(t *testing.T) {
			h := newHarness(t)
			enforceBudgets(h)
			a := h.active(t, "unsafe-retire", "owner", 48*time.Hour)
			r, _ := h.prov.GetAccount(context.Background(), a.ID)
			r.Status = state
			// Even old closure markers must not authorize deleting live protection.
			r.Tags[core.TagClosedAt] = h.now.Format(time.RFC3339)
			r.Tags[core.TagCloseRequested] = h.now.Format(time.RFC3339)
			if state == "unmanaged-closed" {
				r.Status = "CLOSED"
				delete(r.Tags, core.TagManaged)
			}
			h.prov.Seed(r)
			h.prov.QuotaHit = true // ACTIVE closure request stays pending.
			_, _ = h.svc.Refresh(context.Background())
			if h.prov.RetireCalls != 0 || h.prov.BudgetStates[a.ID].ActionID == "" {
				t.Fatal("retirement before confirmed managed closure")
			}
		})
	}
}

type retirementTagFailure struct{ core.Provider }

func (p retirementTagFailure) SetTags(ctx context.Context, id string, tags map[string]string) error {
	if _, ok := tags[core.TagBudgetHealth]; ok {
		return errors.New("tag write failed")
	}
	return p.Provider.SetTags(ctx, id, tags)
}

func TestBudgetRetirementRecoversAfterTagFailure(t *testing.T) {
	h := newHarness(t)
	enforceBudgets(h)
	a := h.active(t, "retire-tags", "owner", time.Hour)
	svc := core.NewService(retirementTagFailure{h.prov}, h.note, h.svc.Config(), nil)
	svc.Now = func() time.Time { return h.now }
	closed, err := svc.RequestClose(context.Background(), a.ID, "admin")
	if err == nil || closed.Status != core.StatusClosed || h.prov.BudgetStates[a.ID].ActionID != "" {
		t.Fatalf("expected deleted action with failed health write: %+v %v", closed, err)
	}
	enforceBudgets(h)
	if _, err := h.svc.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.prov.Tags(a.ID)[core.TagBudgetHealth] != "retired" {
		t.Fatal("lost retirement after restart")
	}
}

func TestBudgetRetirementRequiresOriginalConfiguration(t *testing.T) {
	for _, policy := range []string{"", "p-other123"} {
		h := newHarness(t)
		enforceBudgets(h)
		a := h.active(t, "retire-config", "owner", time.Hour)
		r, _ := h.prov.GetAccount(context.Background(), a.ID)
		r.Status = "CLOSED"
		h.prov.Seed(r)
		cfg := h.svc.Config()
		cfg.BudgetPolicyID = policy
		svc := core.NewService(h.prov, h.note, cfg, nil)
		if _, err := svc.Refresh(context.Background()); err == nil {
			t.Fatal("configuration drift not reported")
		}
		if h.prov.RetireCalls != 0 || h.prov.Tags(a.ID)[core.TagBudgetHealth] != "error" {
			t.Fatal("unsafe or invisible retirement")
		}
	}
}

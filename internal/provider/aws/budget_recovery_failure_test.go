package aws

import (
	"context"
	"encoding/base64"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	bt "github.com/aws/aws-sdk-go-v2/service/budgets/types"
	"playplace/internal/core"
)

func TestRecoveryApprovalNeedsKnownPositiveSufficientSpend(t *testing.T) {
	for _, tc := range []struct {
		name           string
		spend, amount  float64
		known, refused bool
	}{
		{"unknown", 60, 100, false, true},
		{"recalculating", 0, 100, true, true},
		{"insufficient", 60, 55, true, false},
		{"equal-to-spend", 60, 60, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRecoveryFlow(t)
			h.change(func(r *recoveryRemote) { r.spend, r.spendKnown = tc.spend, tc.known })
			a, err := h.svc.SetBudget(context.Background(), budgetTestID, tc.amount, "admin", "testing", false)
			if (err != nil) != tc.refused || (a == nil) != tc.refused {
				t.Fatalf("change=%+v err=%v", a, err)
			}
			r := h.snapshot()
			if r.tags[core.TagBudgetRecovery] != "" {
				t.Fatal("unsafe recovery approval persisted")
			}
			want := []string{"update"}
			if tc.refused {
				want = nil
			}
			if !slices.Equal(r.mutations, want) {
				t.Fatalf("mutations=%v want=%v", r.mutations, want)
			}
		})
	}
}

func TestRecoveryFailuresPreserveApprovalAndMutationOrder(t *testing.T) {
	for _, stage := range []string{"approval", "UpdateBudget", "DescribeNotificationsForBudget", "ExecuteBudgetAction"} {
		t.Run(stage, func(t *testing.T) {
			h := newRecoveryFlow(t)
			h.change(func(r *recoveryRemote) {
				if stage == "approval" {
					r.failPhase = "reverse"
				} else {
					r.failOperation = stage
				}
			})
			ctx := context.Background()
			a, err := h.svc.SetBudget(ctx, budgetTestID, 100, "admin", "testing", false)
			if err == nil || (a == nil) != (stage == "approval") {
				t.Fatalf("partial success contract: %+v %v", a, err)
			}
			r := h.snapshot()
			want := []string{"persist-reverse", "update"}
			switch stage {
			case "approval":
				want = nil
			case "UpdateBudget":
				want = []string{"persist-reverse"}
			}
			if !slices.Equal(r.mutations, want) {
				t.Fatalf("mutated past failure: %v", r.mutations)
			}
			h.change(func(r *recoveryRemote) { r.failPhase, r.failOperation = "", "" })
			h.restart()
			if stage == "approval" {
				if _, err := h.svc.Refresh(ctx); err != nil {
					t.Fatal(err)
				}
				if len(h.snapshot().mutations) != 0 {
					t.Fatal("failed approval was recovered without authorization")
				}
				return
			}
			if _, err := h.svc.SetBudget(ctx, budgetTestID, 120, "admin", "overlap", false); err == nil {
				t.Fatal("overlapping approval accepted")
			}
			if _, err := h.svc.Refresh(ctx); err == nil {
				t.Fatal("accepted reverse is not completion")
			}
			r = h.snapshot()
			if !slices.Equal(r.mutations, []string{"persist-reverse", "update", "reverse"}) {
				t.Fatalf("retry order: %v", r.mutations)
			}
			intent, err := core.DecodeBudgetRecovery(r.tags[core.TagBudgetRecovery])
			if err != nil || intent == nil || intent.Spend != 60 {
				t.Fatalf("pre-update evidence lost: %+v %v", intent, err)
			}
		})
	}
}

func TestRecoveryResumesExistingResetIntentWithoutReversingNewExecution(t *testing.T) {
	for _, status := range []bt.ActionStatus{bt.ActionStatusExecutionSuccess, bt.ActionStatusExecutionFailure} {
		t.Run(string(status), func(t *testing.T) {
			h := newRecoveryFlow(t)
			// Literal pre-refactor wire record, independent of the current encoder.
			old := base64.RawURLEncoding.EncodeToString([]byte(`{"a":"12345678-abcd-abcd-abcd-123456789012","p":"reset","m":"2026-09","l":100,"s":60}`))
			h.change(func(r *recoveryRemote) {
				r.limit, r.tags[core.TagBudget] = 100, "100.00"
				r.action.Status, r.attached = bt.ActionStatusReverseSuccess, false
				r.tags[core.TagBudgetRecovery] = old
			})
			ctx := context.Background()
			if _, err := h.svc.Refresh(ctx); err == nil {
				t.Fatal("reset acceptance reported complete")
			}
			if got := h.snapshot().mutations; !slices.Equal(got, []string{"reset"}) {
				t.Fatalf("legacy reset did not resume: %v", got)
			}
			h.change(func(r *recoveryRemote) {
				r.action.Status, r.attached = status, status == bt.ActionStatusExecutionSuccess
			})
			h.restart()
			_, err := h.svc.Refresh(ctx)
			failed := status == bt.ActionStatusExecutionFailure
			if (err != nil) != failed {
				t.Fatalf("completion/error semantics changed: %v", err)
			}
			r := h.snapshot()
			want := []string{"reset", "clear"}
			if failed {
				want = []string{"reset"}
				if r.tags[core.TagBudgetRecovery] != old || r.tags[core.TagBudgetHealth] != "error" {
					t.Fatal("error lost durable recovery")
				}
			} else if r.tags[core.TagBudgetRecovery] != "" || r.tags[core.TagBudgetHealth] != "restricted" {
				t.Fatal("re-execution was not retained as restriction")
			}
			if !slices.Equal(r.mutations, want) {
				t.Fatalf("old approval reused: %v", r.mutations)
			}
		})
	}
}

func TestRecoveryApprovalDoesNotCrossMonthsOrActionIdentity(t *testing.T) {
	for _, mode := range []string{"rollover", "action-mismatch", "limit-mismatch"} {
		t.Run(mode, func(t *testing.T) {
			h := newRecoveryFlow(t)
			ctx := context.Background()
			if a, err := h.svc.SetBudget(ctx, budgetTestID, 100, "admin", "testing", false); a == nil || err == nil {
				t.Fatalf("approval: %+v %v", a, err)
			}
			h.mu.Lock()
			h.now = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
			h.mu.Unlock()
			h.change(func(r *recoveryRemote) {
				r.action.Status, r.attached = bt.ActionStatusExecutionSuccess, true
				if mode == "action-mismatch" {
					r.action.ActionId = aws.String("a-different-owned-action")
				}
				if mode == "limit-mismatch" {
					r.tags[core.TagBudget], r.limit = "120.00", 120
				}
			})
			h.restart()
			_, err := h.svc.Refresh(ctx)
			if (err != nil) != (mode != "rollover") {
				t.Fatalf("rollover result: %v", err)
			}
			r := h.snapshot()
			want := []string{"persist-reverse", "update", "reverse"}
			if mode == "rollover" {
				want = append(want, "clear")
				if r.tags[core.TagBudgetRecovery] != "" || r.tags[core.TagBudgetHealth] != "restricted" {
					t.Fatal("old-month approval not retired safely")
				}
			} else if r.tags[core.TagBudgetRecovery] == "" {
				t.Fatal("mismatch discarded before month check")
			}
			if !slices.Equal(r.mutations, want) {
				t.Fatalf("old approval reused: %v", r.mutations)
			}
		})
	}
}

func TestEnsureBudgetOnlyObservesRecovery(t *testing.T) {
	h := newRecoveryFlow(t)
	spec := budgetSpec()
	spec.Recovery = &core.BudgetRecovery{ActionID: aws.ToString(h.snapshot().action.ActionId), Phase: "reverse", Period: "2026-09", Limit: 100, Spend: 60}
	h.change(func(r *recoveryRemote) { r.limit, r.tags[core.TagBudget] = 100, "100.00" })
	r, err := h.provider.EnsureBudget(context.Background(), budgetTestID, spec)
	if err != nil || r.Action == nil || r.Action.Status != "EXECUTION_SUCCESS" || !r.Action.RestrictionAttached {
		t.Fatalf("observation: %+v %v", r, err)
	}
	if len(h.snapshot().mutations) != 0 {
		t.Fatal("adapter made a recovery decision")
	}
}

func TestRecoveryMutationRevalidatesOwnedAction(t *testing.T) {
	for _, mode := range []string{"changed-action", "changed-role", "management", "unsupported-operation"} {
		t.Run(mode, func(t *testing.T) {
			h := newRecoveryFlow(t)
			spec := budgetSpec()
			id, actionID, operation := budgetTestID, aws.ToString(h.snapshot().action.ActionId), core.BudgetActionReverse
			switch mode {
			case "changed-action":
				actionID = "previous-action"
			case "changed-role":
				spec.RoleARN = strings.Replace(spec.RoleARN, "role/Budgets", "role/Other", 1)
			case "management":
				id = budgetTestInfo.ManagementAccountID
			case "unsupported-operation":
				operation = "EXECUTE_BUDGET_ACTION"
			}
			if err := h.provider.ExecuteBudgetAction(context.Background(), id, spec, actionID, operation); err == nil {
				t.Fatal("unsafe execution accepted")
			}
			if len(h.snapshot().mutations) != 0 {
				t.Fatal("unsafe execution reached AWS")
			}
		})
	}
}

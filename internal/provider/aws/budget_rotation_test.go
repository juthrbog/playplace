package aws

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	bt "github.com/aws/aws-sdk-go-v2/service/budgets/types"
	"playplace/internal/core"
)

// A new --alert-email changes only who AWS Budgets notifies. The pass that
// rotates the recipient keeps the observed action state: health, the settling
// clock and the notices stay as they were.
func TestAlertEmailRotationKeepsProtectionState(t *testing.T) {
	const rotated = "new-ops@example.com"
	for _, tc := range []struct {
		health   string
		status   bt.ActionStatus
		attached bool
	}{
		{"ready", bt.ActionStatusStandby, false},
		{"restricted", bt.ActionStatusExecutionSuccess, true},
		{"restricting", bt.ActionStatusExecutionInProgress, false},
		{"recovering", bt.ActionStatusReverseInProgress, true},
	} {
		t.Run(tc.health, func(t *testing.T) {
			h := newRecoveryFlow(t)
			checked := h.now.Add(-90 * time.Minute).UTC().Format(time.RFC3339)
			h.change(func(r *recoveryRemote) {
				r.action.Status, r.attached = tc.status, tc.attached
				r.tags[core.TagBudgetHealth], r.tags[core.TagBudgetChecked] = tc.health, checked
				if tc.health == "ready" {
					r.spend = 10
				}
				if tc.health == "recovering" {
					r.limit, r.tags[core.TagBudget] = 100, "100.00"
					r.tags[core.TagBudgetRecovery] = (core.BudgetRecovery{ActionID: aws.ToString(r.action.ActionId), Phase: "reverse", Period: "2026-09", Limit: 100, Spend: 60}).Encode()
				}
			})
			ctx := context.Background()
			if _, err := h.svc.Refresh(ctx); err != nil {
				t.Fatalf("baseline pass: %v", err)
			}
			if r := h.snapshot(); len(r.mutations) != 0 || r.tags[core.TagBudgetHealth] != tc.health || len(h.notices()) != 0 {
				t.Fatalf("baseline is not steady: %v %v %v", r.mutations, r.tags, h.notices())
			}

			h.email = rotated
			h.restart()
			for pass := range 2 {
				if _, err := h.svc.Refresh(ctx); err != nil {
					t.Fatalf("pass %d after rotation: %v", pass, err)
				}
				if notes := h.notices(); len(notes) != 0 {
					t.Fatalf("pass %d sent notices: %+v", pass, notes)
				}
				r := h.snapshot()
				if r.tags[core.TagBudgetHealth] != tc.health || r.tags[core.TagBudgetChecked] != checked {
					t.Fatalf("pass %d changed health or its clock: %s since %s", pass, r.tags[core.TagBudgetHealth], r.tags[core.TagBudgetChecked])
				}
			}
			r := h.snapshot()
			if !slices.Equal(r.mutations, []string{"rotate-alert", "rotate-alert", "update-action"}) {
				t.Fatalf("mutations = %v", r.mutations)
			}
			if r.alerts[string(bt.NotificationTypeActual)] != rotated || r.alerts[string(bt.NotificationTypeForecasted)] != rotated {
				t.Fatalf("budget alerts not rotated: %v", r.alerts)
			}
			update := r.actionUpdates[0]
			for _, field := range []string{"ActionThreshold", "ApprovalModel", "NotificationType", "Definition", "ExecutionRoleArn"} {
				if _, ok := update[field]; ok {
					t.Fatalf("recipient rotation also sent %s: %v", field, update)
				}
			}
			subs, _ := update["Subscribers"].([]any)
			if len(subs) != 1 || subs[0].(map[string]any)["Address"] != rotated {
				t.Fatalf("action recipient = %v", update["Subscribers"])
			}

			// The rotation did not restart the settling clock: a state that
			// was settling still fails the pass two hours after it began.
			h.mu.Lock()
			h.now = h.now.Add(31 * time.Minute)
			h.mu.Unlock()
			_, err := h.svc.Refresh(ctx)
			settling := tc.health == "restricting" || tc.health == "recovering"
			if !settling && err != nil {
				t.Fatalf("steady state failed after rotation: %v", err)
			}
			if settling && (err == nil || !strings.Contains(err.Error(), "still "+tc.health+" after more than two hours")) {
				t.Fatalf("overdue %s pass error = %v, want the two-hour alert", tc.health, err)
			}
		})
	}
}

// AWS locks a budget action while it changes state, so it can reject a
// recipient rotation in restricting or recovering. Each rejected pass fails
// with error health, and the owner gets one notice. The first pass that AWS
// accepts rotates the recipient, and health returns to the observed state
// with a repair notice. A repair notice that promises a later notice gets it.
func TestRejectedAlertEmailRotationFailsThePass(t *testing.T) {
	const rotated = "new-ops@example.com"
	for _, tc := range []struct {
		health   string
		status   bt.ActionStatus
		attached bool
		repair   string // the notice of the accepted pass
	}{
		{"restricting", bt.ActionStatusExecutionInProgress, false, "Budget protection is being repaired (restricting). AWS is applying the budget restriction, and a notice follows when it is in place."},
		{"recovering", bt.ActionStatusReverseInProgress, true, "Budget protection is recovering."},
	} {
		t.Run(tc.health, func(t *testing.T) {
			h := newRecoveryFlow(t)
			h.change(func(r *recoveryRemote) {
				r.action.Status, r.attached = tc.status, tc.attached
				r.tags[core.TagBudgetHealth] = tc.health
				r.tags[core.TagBudgetChecked] = h.now.Add(-90 * time.Minute).UTC().Format(time.RFC3339)
				if tc.health == "recovering" {
					r.limit, r.tags[core.TagBudget] = 100, "100.00"
					r.tags[core.TagBudgetRecovery] = (core.BudgetRecovery{ActionID: aws.ToString(r.action.ActionId), Phase: "reverse", Period: "2026-09", Limit: 100, Spend: 60}).Encode()
				}
			})
			ctx := context.Background()
			if _, err := h.svc.Refresh(ctx); err != nil {
				t.Fatalf("baseline pass: %v", err)
			}

			h.email = rotated
			h.restart()
			h.change(func(r *recoveryRemote) { r.lockOperation = "UpdateBudgetAction" })
			for pass := range 2 {
				_, err := h.svc.Refresh(ctx)
				if err == nil || !strings.Contains(err.Error(), "ResourceLockedException") {
					t.Fatalf("locked pass %d error = %v, want the rejection", pass, err)
				}
				if got := h.snapshot().tags[core.TagBudgetHealth]; got != "error" {
					t.Fatalf("locked pass %d health = %q, want error", pass, got)
				}
			}
			if notes := h.notices(); len(notes) != 1 || !strings.HasPrefix(notes[0].Body, "Budget protection is error.") {
				t.Fatalf("notices after two rejected passes = %+v, want one error notice", notes)
			}

			h.change(func(r *recoveryRemote) { r.lockOperation = "" })
			if _, err := h.svc.Refresh(ctx); err != nil {
				t.Fatalf("accepted pass: %v", err)
			}
			r := h.snapshot()
			if r.tags[core.TagBudgetHealth] != tc.health {
				t.Fatalf("health after the accepted pass = %q, want %s", r.tags[core.TagBudgetHealth], tc.health)
			}
			if subs := r.action.Subscribers; len(subs) != 1 || aws.ToString(subs[0].Address) != rotated {
				t.Fatalf("action recipient = %+v", subs)
			}
			notes := h.notices()
			if len(notes) != 2 || strings.TrimSpace(notes[1].Body) != tc.repair {
				t.Fatalf("notices after the accepted pass = %+v, want the repair notice %q", notes, tc.repair)
			}

			if tc.health == "restricting" {
				// The restriction lands, and the promised notice follows.
				h.change(func(r *recoveryRemote) { r.action.Status, r.attached = bt.ActionStatusExecutionSuccess, true })
				if _, err := h.svc.Refresh(ctx); err != nil {
					t.Fatal(err)
				}
				if notes := h.notices(); len(notes) != 3 || !strings.HasPrefix(notes[2].Body, "Budget protection is restricted.") {
					t.Fatalf("notices after the restriction = %+v", notes)
				}
			}
		})
	}
}

// A trigger repair changes when AWS acts, so the next pass observes it. Health
// goes through setup-pending, as for a new action.
func TestActionTriggerRepairStillSettles(t *testing.T) {
	h := newRecoveryFlow(t)
	h.change(func(r *recoveryRemote) {
		r.spend, r.attached = 10, false
		r.action.Status = bt.ActionStatusStandby
		r.action.ActionThreshold = &bt.ActionThreshold{ActionThresholdType: bt.ThresholdTypePercentage, ActionThresholdValue: 90}
		r.tags[core.TagBudgetHealth] = "ready"
	})
	ctx := context.Background()
	if _, err := h.svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	r := h.snapshot()
	if r.tags[core.TagBudgetHealth] != "setup-pending" || len(r.actionUpdates) != 1 || r.actionUpdates[0]["ActionThreshold"] == nil {
		t.Fatalf("trigger repair was not observed later: %v %v", r.tags, r.actionUpdates)
	}
	if _, err := h.svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got := h.snapshot().tags[core.TagBudgetHealth]; got != "ready" {
		t.Fatalf("health after the repair = %q, want ready", got)
	}
	if notes := h.notices(); len(notes) != 0 {
		t.Fatalf("trigger repair sent notices: %+v", notes)
	}
}

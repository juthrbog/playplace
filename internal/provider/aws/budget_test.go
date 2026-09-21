package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	bt "github.com/aws/aws-sdk-go-v2/service/budgets/types"
	"playplace/internal/core"
)

const budgetTestID = "111111111111"

var budgetTestInfo = core.OrgInfo{ManagementAccountID: "222222222222", PlaygroundOUID: "ou-test-12345678", RootID: "r-root"}

func budgetSpec() core.BudgetSpec {
	return core.BudgetSpec{LimitUSD: 100, Email: "ops@example.com", PolicyID: "p-budget12", RoleARN: "arn:aws:iam::222222222222:role/Budgets", Now: time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)}
}
func budgetAction(spec core.BudgetSpec, status bt.ActionStatus) bt.Action {
	return bt.Action{ActionId: aws.String("12345678-abcd-abcd-abcd-123456789012"), ActionType: bt.ActionTypeScp, ApprovalModel: bt.ApprovalModelAuto, NotificationType: bt.NotificationTypeActual,
		ActionThreshold:  &bt.ActionThreshold{ActionThresholdType: bt.ThresholdTypePercentage, ActionThresholdValue: 100},
		ExecutionRoleArn: aws.String(spec.RoleARN), Definition: &bt.Definition{ScpActionDefinition: &bt.ScpActionDefinition{PolicyId: aws.String(spec.PolicyID), TargetIds: []string{budgetTestID}}},
		Subscribers: []bt.Subscriber{{SubscriptionType: bt.SubscriptionTypeEmail, Address: aws.String(spec.Email)}}, Status: status}
}

// Every SDK request is sent to loopback. Unexpected APIs fail the test: in
// particular, runtime must never directly attach/detach/delete policies.
func budgetHTTP(t *testing.T, respond func(string, map[string]any) (any, int)) *Provider {
	t.Helper()
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var in map[string]any
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		parts := strings.Split(r.Header.Get("X-Amz-Target"), ".")
		body, status := respond(parts[len(parts)-1], in)
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		if status == 0 {
			status = 200
		}
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)
	p := NewFromConfig(aws.Config{Region: "us-east-1", RetryMaxAttempts: 1, Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "test", SecretAccessKey: "test"}, nil
	})}, Config{Endpoint: srv.URL})
	p.info = &budgetTestInfo
	return p
}

func unexpectedBudgetAPI(t *testing.T, name string) (any, int) {
	t.Helper()
	t.Errorf("unexpected API %s", name)
	return map[string]any{"__type": "AccessDeniedException", "Message": name}, 400
}

func TestBudgetNoopAndNotificationRepair(t *testing.T) {
	for _, tc := range []struct {
		name, email, oldEmail string
		limit                 float64
		wantUpdate            bool
	}{
		{"noop", "ops@example.com", "ops@example.com", 100, false},
		{"rotate", "ops@example.com", "old@example.com", 100, false},
		{"limit", "ops@example.com", "ops@example.com", 50, true},
		{"disable", "", "old@example.com", 100, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			calls := map[string]int{}
			p := budgetHTTP(t, func(name string, in map[string]any) (any, int) {
				mu.Lock()
				defer mu.Unlock()
				calls[name]++
				switch name {
				case "DescribeBudget":
					return map[string]any{"Budget": &bt.Budget{BudgetName: budgetName(budgetTestID), BudgetType: bt.BudgetTypeCost, TimeUnit: bt.TimeUnitMonthly, BudgetLimit: &bt.Spend{Amount: aws.String(fmt.Sprint(tc.limit)), Unit: aws.String("USD")}, FilterExpression: linkedAccountFilter(budgetTestID), Metrics: []bt.Metric{bt.MetricUnblendedCost}}}, 200
				case "UpdateBudget":
					b := in["NewBudget"].(map[string]any)
					if b["BudgetLimit"].(map[string]any)["Amount"] != "100.00" {
						t.Error("wrong limit")
					}
					if _, ok := b["CalculatedSpend"]; ok {
						t.Error("wrote calculated spend")
					}
					return map[string]any{}, 200
				case "DescribeNotificationsForBudget":
					// Exercise pagination and preserve an unrelated notification.
					if in["NextToken"] == nil {
						return map[string]any{"Notifications": []bt.Notification{ownedNotifications()[0]}, "NextToken": "page2"}, 200
					}
					return map[string]any{"Notifications": []bt.Notification{ownedNotifications()[1], {NotificationType: bt.NotificationTypeActual, Threshold: 35}}}, 200
				case "DescribeSubscribersForNotification":
					return map[string]any{"Subscribers": []bt.Subscriber{{SubscriptionType: bt.SubscriptionTypeEmail, Address: aws.String(tc.oldEmail)}, {SubscriptionType: bt.SubscriptionTypeSns, Address: aws.String("arn:aws:sns:us-east-1:222222222222:operator")}}}, 200
				case "UpdateSubscriber":
					if in["NewSubscriber"].(map[string]any)["Address"] != tc.email {
						t.Error("wrong recipient")
					}
					return map[string]any{}, 200
				case "DeleteSubscriber":
					if in["Subscriber"].(map[string]any)["SubscriptionType"] != "EMAIL" {
						t.Error("deleted operator SNS subscriber")
					}
					return map[string]any{}, 200
				default:
					return unexpectedBudgetAPI(t, name)
				}
			})
			s := budgetSpec()
			s.PolicyID, s.RoleARN, s.Email = "", "", tc.email
			r, err := p.EnsureBudget(context.Background(), budgetTestID, s)
			if err != nil || !r.Configured {
				t.Fatalf("ensure: %+v %v", r, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if (calls["UpdateBudget"] == 1) != tc.wantUpdate {
				t.Fatalf("updates: %v", calls)
			}
			if tc.name == "rotate" && calls["UpdateSubscriber"] != 2 {
				t.Fatalf("not rotated: %v", calls)
			}
			if tc.name == "disable" && calls["DeleteSubscriber"] != 2 {
				t.Fatalf("not disabled safely: %v", calls)
			}
			if tc.name == "noop" && (calls["UpdateSubscriber"]+calls["DeleteSubscriber"] != 0) {
				t.Fatal("no-op mutated recipients")
			}
		})
	}
}

func TestBudgetRecoveryTransitions(t *testing.T) {
	cases := []struct {
		status                                bt.ActionStatus
		phase, execute, next                  string
		attached, done, configured, wantError bool
	}{
		{bt.ActionStatusExecutionSuccess, "reverse", "REVERSE_BUDGET_ACTION", "", true, false, false, false},
		{bt.ActionStatusReverseInProgress, "reverse", "", "", true, false, false, false},
		{bt.ActionStatusReverseSuccess, "reverse", "", "reset", false, false, false, false},
		{bt.ActionStatusReverseSuccess, "reset", "RESET_BUDGET_ACTION", "", false, false, false, false},
		{bt.ActionStatusResetInProgress, "reset", "", "", false, false, false, false},
		{bt.ActionStatusStandby, "reset", "", "", false, true, true, false},
		// Crash after reset: a new execution must not use the old increase again.
		{bt.ActionStatusExecutionSuccess, "reset", "", "", true, true, true, false},
		{bt.ActionStatusExecutionFailure, "reset", "", "", false, true, false, true},
		{bt.ActionStatusReverseFailure, "reverse", "REVERSE_BUDGET_ACTION", "", true, false, false, false},
		{bt.ActionStatusResetFailure, "reset", "RESET_BUDGET_ACTION", "", false, false, false, false},
		{bt.ActionStatus(""), "reverse", "", "", false, false, false, true},
		{bt.ActionStatus("UNKNOWN"), "reset", "", "", false, false, false, true},
		{bt.ActionStatusStandby, "reverse", "", "", true, false, false, false},
		{bt.ActionStatusStandby, "reset", "", "", true, false, false, false},
		{bt.ActionStatusExecutionInProgress, "reverse", "", "", false, false, false, false},
		{bt.ActionStatusExecutionInProgress, "reset", "", "", false, true, false, false},
		{bt.ActionStatusPending, "reset", "", "", false, true, false, true},
	}
	for _, tc := range cases {
		t.Run(string(tc.status)+"/"+tc.phase, func(t *testing.T) {
			h := newRecoveryFlow(t)
			h.change(func(r *recoveryRemote) {
				r.limit, r.tags[core.TagBudget] = 100, "100.00"
				r.action.Status, r.attached = tc.status, tc.attached
				r.tags[core.TagBudgetRecovery] = (core.BudgetRecovery{ActionID: aws.ToString(r.action.ActionId), Phase: tc.phase, Period: "2026-09", Limit: 100, Spend: 60}).Encode()
			})
			_, err := h.svc.Refresh(context.Background())
			if (err != nil) != (!tc.configured || tc.wantError) {
				t.Fatalf("unexpected reconciliation result: %v", err)
			}
			r := h.snapshot()
			if (r.tags[core.TagBudgetHealth] == "error") != tc.wantError {
				t.Fatalf("wrong health: %v", r.tags)
			}
			intent, err := core.DecodeBudgetRecovery(r.tags[core.TagBudgetRecovery])
			if err != nil {
				t.Fatal(err)
			}
			// Completion accompanied by an error deliberately retains approval.
			if (intent == nil) != (tc.done && !tc.wantError) {
				t.Fatalf("wrong durable intent: %+v", intent)
			}
			if intent != nil {
				want := tc.phase
				if tc.next != "" {
					want = tc.next
				}
				if intent.Phase != want {
					t.Fatalf("phase=%s want=%s", intent.Phase, want)
				}
			}
			wantMutation := ""
			switch tc.execute {
			case "REVERSE_BUDGET_ACTION":
				wantMutation = "reverse"
			case "RESET_BUDGET_ACTION":
				wantMutation = "reset"
			}
			if tc.next != "" {
				wantMutation = "persist-" + tc.next
			}
			if tc.done && !tc.wantError {
				wantMutation = "clear"
			}
			if strings.Join(r.mutations, ",") != wantMutation {
				t.Fatalf("mutations=%v want=%s", r.mutations, wantMutation)
			}
		})
	}
}

func TestCreateBudgetActionIsScopedAndObservedLater(t *testing.T) {
	spec := budgetSpec()
	p := budgetHTTP(t, func(name string, in map[string]any) (any, int) {
		switch name {
		case "DescribeBudgetActionsForBudget":
			return map[string]any{"Actions": []any{}}, 200
		case "ListPoliciesForTarget":
			return map[string]any{"Policies": []any{}}, 200
		case "CreateBudgetAction":
			if in["ActionType"] != "APPLY_SCP_POLICY" || in["ApprovalModel"] != "AUTOMATIC" || in["NotificationType"] != "ACTUAL" || in["AccountId"] != budgetTestInfo.ManagementAccountID {
				t.Errorf("bad action: %v", in)
			}
			targets := in["Definition"].(map[string]any)["ScpActionDefinition"].(map[string]any)["TargetIds"].([]any)
			if len(targets) != 1 || targets[0] != budgetTestID {
				t.Errorf("bad targets: %v", targets)
			}
			if in["ActionThreshold"].(map[string]any)["ActionThresholdValue"] != float64(100) {
				t.Error("not 100 percent")
			}
			return map[string]any{"ActionId": "12345678-abcd-abcd-abcd-123456789012"}, 200
		default:
			return unexpectedBudgetAPI(t, name)
		}
	})
	r, err := p.reconcileBudgetAction(context.Background(), budgetTestInfo, budgetTestID, spec)
	if err != nil || r.Configured || r.State != "setup-pending" || r.ActionID == "" {
		t.Fatalf("creation reported ready prematurely: %+v %v", r, err)
	}
}

func TestBudgetActionDriftAndOwnership(t *testing.T) {
	for _, mode := range []string{"unowned", "duplicate", "wrong-target", "orphan"} {
		t.Run(mode, func(t *testing.T) {
			spec := budgetSpec()
			action := budgetAction(spec, bt.ActionStatusExecutionSuccess)
			if mode == "wrong-target" {
				action.Definition.ScpActionDefinition.TargetIds = []string{"999999999999"}
			}
			p := budgetHTTP(t, func(name string, in map[string]any) (any, int) {
				switch name {
				case "DescribeBudgetActionsForBudget":
					actions := []bt.Action{action}
					if mode == "orphan" {
						actions = nil
					}
					if mode == "duplicate" {
						actions = append(actions, action)
					}
					return map[string]any{"Actions": actions}, 200
				case "ListTagsForResource":
					tags := []bt.ResourceTag{{Key: aws.String(core.TagManaged), Value: aws.String("true")}, {Key: aws.String("playplace:account"), Value: aws.String(budgetTestID)}}
					if mode == "unowned" {
						tags = nil
					}
					return map[string]any{"ResourceTags": tags}, 200
				case "ListPoliciesForTarget":
					return map[string]any{"Policies": []map[string]any{{"Id": spec.PolicyID}}}, 200
				default:
					return unexpectedBudgetAPI(t, name)
				}
			})
			if _, err := p.reconcileBudgetAction(context.Background(), budgetTestInfo, budgetTestID, spec); err == nil {
				t.Fatal("unsafe action accepted")
			}
		})
	}
}

package aws

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	bt "github.com/aws/aws-sdk-go-v2/service/budgets/types"
	"playplace/internal/core"
)

// Remote state only: tests choose AWS observations explicitly. Approval and all
// recovery decisions run through the production SetBudget/Refresh interface.
type recoveryRemote struct {
	tags                     map[string]string
	limit, spend             float64
	spendKnown, attached     bool
	action                   bt.Action
	failPhase, failOperation string
	mutations                []string
	// alerts is the EMAIL recipient of each owned budget notification, by
	// notification type. actionUpdates records each UpdateBudgetAction input.
	alerts        map[string]string
	actionUpdates []map[string]any
}

type recoveryFlow struct {
	t        *testing.T
	mu       sync.Mutex
	remote   recoveryRemote
	now      time.Time
	provider *Provider
	svc      *core.Service
	email    string // --alert-email of the next restart
	notes    []core.Message
}

func newRecoveryFlow(t *testing.T) *recoveryFlow {
	spec := budgetSpec()
	h := &recoveryFlow{t: t, now: spec.Now, email: spec.Email, remote: recoveryRemote{
		limit: 50, spend: 60, spendKnown: true, attached: true,
		action: budgetAction(spec, bt.ActionStatusExecutionSuccess),
		alerts: map[string]string{string(bt.NotificationTypeActual): spec.Email, string(bt.NotificationTypeForecasted): spec.Email},
		tags:   map[string]string{core.TagManaged: "true", core.TagOwner: "owner", core.TagBudget: "50", core.TagBudgetPolicy: spec.PolicyID, core.TagBudgetHealth: "restricted", core.TagExpires: spec.Now.Add(60 * 24 * time.Hour).Format(time.RFC3339)},
	}}
	h.provider = budgetHTTP(t, h.respond)
	h.restart()
	return h
}

func (h *recoveryFlow) restart() {
	cfg := core.DefaultConfig()
	spec := budgetSpec()
	cfg.BudgetPolicyID, cfg.BudgetActionRole, cfg.AlertEmail = spec.PolicyID, spec.RoleARN, h.email
	cfg.InventoryTTL = 0
	h.svc = core.NewService(h.provider, h, cfg, nil)
	h.svc.Now = func() time.Time { h.mu.Lock(); defer h.mu.Unlock(); return h.now }
}

func (h *recoveryFlow) change(f func(*recoveryRemote)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f(&h.remote)
}

func (h *recoveryFlow) snapshot() recoveryRemote {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.remote
	r.tags, r.mutations = maps.Clone(r.tags), slices.Clone(r.mutations)
	r.alerts, r.actionUpdates = maps.Clone(r.alerts), slices.Clone(r.actionUpdates)
	return r
}

// Notify records lifecycle notices, so tests can count them.
func (h *recoveryFlow) Notify(_ context.Context, m core.Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.notes = append(h.notes, m)
	return nil
}

func (h *recoveryFlow) notices() []core.Message {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.notes)
}

func (h *recoveryFlow) respond(name string, in map[string]any) (any, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := &h.remote
	spec := budgetSpec()
	denied := func() (any, int) {
		return map[string]any{"__type": "AccessDeniedException", "Message": "injected failure"}, 400
	}
	if r.failOperation == name {
		return denied()
	}
	switch name {
	case "ListAccountsForParent":
		if in["ParentId"] == budgetTestInfo.RootID {
			return map[string]any{"Accounts": []any{}}, 200 // nothing waits for placement
		}
		return map[string]any{"Accounts": []map[string]any{{"Id": budgetTestID, "Name": "recovery-flow", "Email": "root@example.com", "State": "ACTIVE", "JoinedTimestamp": float64(spec.Now.Unix())}}}, 200
	case "ListCreateAccountStatus":
		return map[string]any{"CreateAccountStatuses": []any{}}, 200
	case "ListTagsForResource":
		if in["ResourceARN"] != nil {
			return map[string]any{"ResourceTags": []map[string]any{{"Key": core.TagManaged, "Value": "true"}, {"Key": "playplace:account", "Value": budgetTestID}}}, 200
		}
		tags := []map[string]any{}
		if in["ResourceId"] == budgetTestID {
			for k, v := range r.tags {
				tags = append(tags, map[string]any{"Key": k, "Value": v})
			}
		}
		return map[string]any{"Tags": tags}, 200
	case "TagResource":
		updates := map[string]string{}
		for _, v := range in["Tags"].([]any) {
			tag := v.(map[string]any)
			updates[tag["Key"].(string)] = tag["Value"].(string)
		}
		if value, ok := updates[core.TagBudgetRecovery]; ok {
			intent, err := core.DecodeBudgetRecovery(value)
			if err != nil {
				h.t.Error(err)
			}
			if intent != nil && intent.Phase == r.failPhase {
				return denied()
			}
			if intent == nil {
				r.mutations = append(r.mutations, "clear")
			} else {
				r.mutations = append(r.mutations, "persist-"+intent.Phase)
			}
		}
		maps.Copy(r.tags, updates)
		return map[string]any{}, 200
	case "ListParents":
		return map[string]any{"Parents": []map[string]any{{"Id": budgetTestInfo.PlaygroundOUID, "Type": "ORGANIZATIONAL_UNIT"}}}, 200
	case "ListRoots":
		return map[string]any{"Roots": []map[string]any{{"Id": budgetTestInfo.RootID, "PolicyTypes": []map[string]any{{"Type": "SERVICE_CONTROL_POLICY", "Status": "ENABLED"}}}}}, 200
	case "DescribePolicy":
		return map[string]any{"Policy": map[string]any{"PolicySummary": map[string]any{"Id": spec.PolicyID, "Type": "SERVICE_CONTROL_POLICY", "AwsManaged": false}}}, 200
	case "ListTargetsForPolicy":
		return map[string]any{"Targets": []any{}}, 200
	case "DescribeBudget":
		b := &bt.Budget{BudgetName: budgetName(budgetTestID), BudgetType: bt.BudgetTypeCost, TimeUnit: bt.TimeUnitMonthly, BudgetLimit: &bt.Spend{Amount: aws.String(fmt.Sprint(r.limit)), Unit: aws.String("USD")}, FilterExpression: linkedAccountFilter(budgetTestID), Metrics: []bt.Metric{bt.MetricUnblendedCost}}
		if r.spendKnown {
			b.CalculatedSpend = &bt.CalculatedSpend{ActualSpend: &bt.Spend{Amount: aws.String(fmt.Sprint(r.spend)), Unit: aws.String("USD")}}
		}
		return map[string]any{"Budget": b}, 200
	case "UpdateBudget":
		amount := in["NewBudget"].(map[string]any)["BudgetLimit"].(map[string]any)["Amount"].(string)
		if r.tags[core.TagBudget] != amount {
			h.t.Error("amount mutation preceded durable approval")
		}
		var err error
		r.limit, err = strconv.ParseFloat(amount, 64)
		if err != nil {
			h.t.Error(err)
		}
		r.spend = 0 // AWS temporarily resets calculated spend on amount updates.
		r.mutations = append(r.mutations, "update")
		return map[string]any{}, 200
	case "DescribeNotificationsForBudget":
		return map[string]any{"Notifications": ownedNotifications()}, 200
	case "DescribeSubscribersForNotification":
		kind := in["Notification"].(map[string]any)["NotificationType"].(string)
		return map[string]any{"Subscribers": []bt.Subscriber{{SubscriptionType: bt.SubscriptionTypeEmail, Address: aws.String(r.alerts[kind])}}}, 200
	case "UpdateSubscriber":
		kind := in["Notification"].(map[string]any)["NotificationType"].(string)
		if in["OldSubscriber"].(map[string]any)["Address"] != r.alerts[kind] {
			h.t.Errorf("replaced a subscriber that does not exist: %v", in)
		}
		r.alerts[kind] = in["NewSubscriber"].(map[string]any)["Address"].(string)
		r.mutations = append(r.mutations, "rotate-alert")
		return map[string]any{}, 200
	case "UpdateBudgetAction":
		r.actionUpdates = append(r.actionUpdates, in)
		if subs, ok := in["Subscribers"].([]any); ok {
			r.action.Subscribers = nil
			for _, v := range subs {
				sub := v.(map[string]any)
				r.action.Subscribers = append(r.action.Subscribers, bt.Subscriber{SubscriptionType: bt.SubscriptionType(sub["SubscriptionType"].(string)), Address: aws.String(sub["Address"].(string))})
			}
		}
		if threshold, ok := in["ActionThreshold"].(map[string]any); ok {
			r.action.ActionThreshold = &bt.ActionThreshold{ActionThresholdType: bt.ThresholdType(threshold["ActionThresholdType"].(string)), ActionThresholdValue: threshold["ActionThresholdValue"].(float64)}
		}
		r.mutations = append(r.mutations, "update-action")
		return map[string]any{}, 200
	case "DescribeBudgetActionsForBudget":
		return map[string]any{"Actions": []bt.Action{r.action}}, 200
	case "ListPoliciesForTarget":
		policies := []map[string]any{{"Id": "p-FullAWSAccess"}}
		if r.attached {
			policies = append(policies, map[string]any{"Id": spec.PolicyID})
		}
		return map[string]any{"Policies": policies}, 200
	case "ExecuteBudgetAction":
		if in["AccountId"] != budgetTestInfo.ManagementAccountID || in["BudgetName"] != "playplace-"+budgetTestID || in["ActionId"] != aws.ToString(r.action.ActionId) {
			h.t.Errorf("wrong execution target: %v", in)
		}
		intent, err := core.DecodeBudgetRecovery(r.tags[core.TagBudgetRecovery])
		if err != nil || intent == nil {
			h.t.Errorf("mutation without durable intent: %v", err)
			return denied()
		}
		switch in["ExecutionType"] {
		case "REVERSE_BUDGET_ACTION":
			if intent.Phase != "reverse" {
				h.t.Error("reverse reused reset approval")
			}
			r.action.Status = bt.ActionStatusReverseInProgress
			r.mutations = append(r.mutations, "reverse")
		case "RESET_BUDGET_ACTION":
			if intent.Phase != "reset" {
				h.t.Error("RESET before durable reset phase")
			}
			r.action.Status = bt.ActionStatusResetInProgress
			r.mutations = append(r.mutations, "reset")
		default:
			h.t.Errorf("unexpected execution %v", in)
		}
		return map[string]any{}, 200
	default:
		return unexpectedBudgetAPI(h.t, name)
	}
}

func TestRecoveryPersistsResetBeforeMutationAcrossRestarts(t *testing.T) {
	h := newRecoveryFlow(t)
	ctx := context.Background()
	a, err := h.svc.SetBudget(ctx, budgetTestID, 100, "admin", "approved testing", false)
	if a == nil || err != nil || a.BudgetHealth != "recovering" {
		t.Fatalf("saved change with recovery in progress: %+v %v", a, err)
	}
	r := h.snapshot()
	intent, err := core.DecodeBudgetRecovery(r.tags[core.TagBudgetRecovery])
	if err != nil || intent == nil || intent.Spend != 60 || r.spend != 0 {
		t.Fatalf("lost pre-update spend: %+v %v", intent, err)
	}
	if !slices.Equal(r.mutations, []string{"persist-reverse", "update", "reverse"}) {
		t.Fatalf("unsafe order: %v", r.mutations)
	}
	h.change(func(r *recoveryRemote) {
		r.action.Status, r.attached, r.failPhase = bt.ActionStatusReverseSuccess, false, "reset"
	})
	h.restart()
	if _, err := h.svc.Refresh(ctx); err == nil {
		t.Fatal("expected failed phase write")
	}
	if got := h.snapshot().mutations; !slices.Equal(got, r.mutations) {
		t.Fatalf("mutated after failed phase write: %v", got)
	}
	h.change(func(r *recoveryRemote) { r.failPhase = "" })
	h.restart()
	if _, err := h.svc.Refresh(ctx); err != nil || h.snapshot().tags[core.TagBudgetHealth] != "recovering" {
		t.Fatalf("phase persistence is still recovery in progress: %v", err)
	}
	if got := h.snapshot().mutations; !slices.Equal(got, []string{"persist-reverse", "update", "reverse", "persist-reset"}) {
		t.Fatalf("reset executed in phase-write pass: %v", got)
	}
	h.restart()
	if _, err := h.svc.Refresh(ctx); err != nil || h.snapshot().tags[core.TagBudgetHealth] != "recovering" {
		t.Fatalf("accepted reset is not completion: %v", err)
	}
	h.change(func(r *recoveryRemote) { r.action.Status = bt.ActionStatusStandby })
	h.restart()
	if _, err := h.svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	a, err = h.svc.Get(ctx, budgetTestID)
	if err != nil || a.BudgetHealth != "ready" || a.BudgetRecovery != "" || a.BudgetUSD != 100 {
		t.Fatalf("not recovered: %+v %v", a, err)
	}
	if got := h.snapshot().mutations; !slices.Equal(got, []string{"persist-reverse", "update", "reverse", "persist-reset", "reset", "clear"}) {
		t.Fatalf("unexpected mutations: %v", got)
	}
}

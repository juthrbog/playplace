package aws

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/budgets"
	bt "github.com/aws/aws-sdk-go-v2/service/budgets/types"
	"playplace/internal/core"
)

func budgetName(id string) *string { return aws.String("playplace-" + id) }
func spendValue(s *bt.Spend) (float64, bool) {
	if s == nil || aws.ToString(s.Unit) != "USD" {
		return 0, false
	}
	n, err := strconv.ParseFloat(aws.ToString(s.Amount), 64)
	return n, err == nil && !math.IsInf(n, 0) && !math.IsNaN(n) && n >= 0
}

func linkedAccountFilter(id string) *bt.Expression {
	return &bt.Expression{Dimensions: &bt.ExpressionDimensionValues{
		Key: bt.DimensionLinkedAccount, Values: []string{id}, MatchOptions: []bt.MatchOption{bt.MatchOptionEquals},
	}}
}

// Accept only the whole account, not a wider account set or a narrower service,
// tag or charge-type subset. AWS also returns this modern representation for
// legacy budgets; never fall back to deprecated fields when it is missing.
func isLinkedAccountFilter(e *bt.Expression, id string) bool {
	// Tolerate equivalent single-child wrappers, but bound traversal and reject
	// all multi-branch expressions rather than guessing about their scope.
	for range 32 {
		if e == nil || e.Not != nil || e.Tags != nil || e.CostCategories != nil {
			return false
		}
		if d := e.Dimensions; d != nil {
			return len(e.And) == 0 && len(e.Or) == 0 && d.Key == bt.DimensionLinkedAccount &&
				slices.Equal(d.Values, []string{id}) &&
				(len(d.MatchOptions) == 0 || slices.Equal(d.MatchOptions, []bt.MatchOption{bt.MatchOptionEquals}))
		}
		switch {
		case len(e.And) == 1 && len(e.Or) == 0:
			e = &e.And[0]
		case len(e.Or) == 1 && len(e.And) == 0:
			e = &e.Or[0]
		default:
			return false
		}
	}
	return false
}

func isCostMetric(metrics []bt.Metric) bool {
	if len(metrics) != 1 {
		return false
	}
	switch metrics[0] {
	case bt.MetricUnblendedCost, bt.MetricBlendedCost, bt.MetricAmortizedCost, bt.MetricNetUnblendedCost, bt.MetricNetAmortizedCost:
		return true
	default:
		return false
	}
}

func (p *Provider) readBudget(ctx context.Context, management, id string, now time.Time) (*bt.Budget, error) {
	out, err := p.budgets.DescribeBudget(ctx, &budgets.DescribeBudgetInput{AccountId: aws.String(management), BudgetName: budgetName(id)})
	if err != nil {
		return nil, err
	}
	if out.Budget == nil {
		return nil, errors.New("AWS returned no budget")
	}
	b := out.Budget
	if b.BudgetType != bt.BudgetTypeCost || b.TimeUnit != bt.TimeUnitMonthly || !isLinkedAccountFilter(b.FilterExpression, id) || !isCostMetric(b.Metrics) || len(b.PlannedBudgetLimits) != 0 || b.AutoAdjustData != nil {
		return nil, errors.New("existing budget is not a fixed monthly linked-account cost budget; review its configuration")
	}
	if period := b.TimePeriod; period != nil {
		if (period.Start != nil && period.Start.After(now)) || (period.End != nil && !period.End.After(now)) {
			return nil, errors.New("budget is outside its active time period; review its configuration")
		}
	}
	return b, nil
}

// EnsureBudget only changes the limit when necessary: UpdateBudget resets
// CalculatedSpend to zero. Notification and action repair are independent.
func (p *Provider) EnsureBudget(ctx context.Context, id string, spec core.BudgetSpec) (core.BudgetProtection, error) {
	result := core.BudgetProtection{State: "setup-pending"}
	if err := spec.Validate(); err != nil {
		return result, err
	}
	info, err := p.EnsureOrganization(ctx)
	if err != nil {
		return result, err
	}
	if id == info.ManagementAccountID {
		return result, errors.New("refusing to budget the management account as a playground")
	}
	if spec.Enforced() {
		if err := p.validateBudgetTarget(ctx, info, id, spec); err != nil {
			return result, err
		}
	}
	b, err := p.readBudget(ctx, info.ManagementAccountID, id, spec.Now)
	var missing *bt.NotFoundException
	switch {
	case errors.As(err, &missing):
		_, err = p.budgets.CreateBudget(ctx, &budgets.CreateBudgetInput{
			AccountId: aws.String(info.ManagementAccountID),
			Budget:    &bt.Budget{BudgetName: budgetName(id), BudgetType: bt.BudgetTypeCost, TimeUnit: bt.TimeUnitMonthly, BudgetLimit: &bt.Spend{Amount: aws.String(fmt.Sprintf("%.2f", spec.LimitUSD)), Unit: aws.String("USD")}, FilterExpression: linkedAccountFilter(id), Metrics: []bt.Metric{bt.MetricUnblendedCost}},
		})
		if err != nil {
			return result, fmt.Errorf("create budget: %w", err)
		}
	case err != nil:
		return result, fmt.Errorf("describe budget: %w", err)
	default:
		limit, ok := spendValue(b.BudgetLimit)
		if !ok {
			return result, errors.New("budget limit is not a valid USD amount")
		}
		if limit != spec.LimitUSD {
			// DescribeBudget can return both legacy and modern fields. AWS rejects
			// mixed writes: explicitly copy only writable modern fields, preserving
			// the existing metric, scope, billing view and period. Do not update an
			// unchanged budget merely to migrate its representation (spend resets).
			updated := &bt.Budget{
				BudgetName: b.BudgetName, BudgetType: b.BudgetType, TimeUnit: b.TimeUnit,
				BudgetLimit:      &bt.Spend{Amount: aws.String(fmt.Sprintf("%.2f", spec.LimitUSD)), Unit: aws.String("USD")},
				FilterExpression: b.FilterExpression, Metrics: b.Metrics,
				BillingViewArn: b.BillingViewArn, TimePeriod: b.TimePeriod,
			}
			if _, err := p.budgets.UpdateBudget(ctx, &budgets.UpdateBudgetInput{AccountId: aws.String(info.ManagementAccountID), NewBudget: updated}); err != nil {
				return result, fmt.Errorf("update budget: %w", err)
			}
		}
	}
	if err := p.reconcileNotifications(ctx, info.ManagementAccountID, id, spec.Email); err != nil {
		return result, fmt.Errorf("budget notifications: %w", err)
	}
	if !spec.Enforced() {
		result.State, result.Configured = "alerts-only", true
		if spec.Email == "" {
			result.State = "alerts-disabled"
		}
		return result, nil
	}
	return p.reconcileBudgetAction(ctx, info, id, spec)
}

func (p *Provider) InspectBudget(ctx context.Context, id string, spec core.BudgetSpec) (core.BudgetSnapshot, error) {
	var s core.BudgetSnapshot
	info, err := p.EnsureOrganization(ctx)
	if err != nil {
		return s, err
	}
	if err := p.validateBudgetTarget(ctx, info, id, spec); err != nil {
		return s, err
	}
	b, err := p.readBudget(ctx, info.ManagementAccountID, id, spec.Now)
	if err != nil {
		return s, err
	}
	var ok bool
	if s.LimitUSD, ok = spendValue(b.BudgetLimit); !ok {
		return s, errors.New("invalid budget limit")
	}
	if b.CalculatedSpend != nil {
		s.SpendUSD, s.SpendKnown = spendValue(b.CalculatedSpend.ActualSpend)
	}
	a, err := p.findBudgetAction(ctx, info, id, spec)
	if err != nil {
		return s, err
	}
	if a == nil {
		return s, errors.New("budget action missing; reconcile before changing the limit")
	}
	s.ActionID, s.ActionStatus = aws.ToString(a.ActionId), string(a.Status)
	return s, nil
}

func ownedNotifications() []bt.Notification {
	return []bt.Notification{
		{NotificationType: bt.NotificationTypeActual, ComparisonOperator: bt.ComparisonOperatorGreaterThan, Threshold: 80, ThresholdType: bt.ThresholdTypePercentage},
		{NotificationType: bt.NotificationTypeForecasted, ComparisonOperator: bt.ComparisonOperatorGreaterThan, Threshold: 100, ThresholdType: bt.ThresholdTypePercentage},
	}
}

func sameNotification(a, b bt.Notification) bool {
	return a.NotificationType == b.NotificationType && a.ComparisonOperator == b.ComparisonOperator && a.Threshold == b.Threshold && a.ThresholdType == b.ThresholdType
}

// playplace owns EMAIL subscribers on these two exact notification tuples.
// Preserve SNS subscribers and all other notifications, including operator ones.
func (p *Provider) reconcileNotifications(ctx context.Context, management, id, email string) error {
	account, name := aws.String(management), budgetName(id)
	var notifications []bt.Notification
	pag := budgets.NewDescribeNotificationsForBudgetPaginator(p.budgets, &budgets.DescribeNotificationsForBudgetInput{AccountId: account, BudgetName: name})
	for pag.HasMorePages() {
		page, err := pag.NextPage(ctx)
		if err != nil {
			return err
		}
		notifications = append(notifications, page.Notifications...)
	}
	for _, desired := range ownedNotifications() {
		found := false
		for _, n := range notifications {
			if sameNotification(n, desired) {
				found = true
				break
			}
		}
		want := bt.Subscriber{SubscriptionType: bt.SubscriptionTypeEmail, Address: aws.String(email)}
		if !found {
			if email != "" {
				if _, err := p.budgets.CreateNotification(ctx, &budgets.CreateNotificationInput{AccountId: account, BudgetName: name, Notification: &desired, Subscribers: []bt.Subscriber{want}}); err != nil {
					return err
				}
			}
			continue
		}
		var subs []bt.Subscriber
		sp := budgets.NewDescribeSubscribersForNotificationPaginator(p.budgets, &budgets.DescribeSubscribersForNotificationInput{AccountId: account, BudgetName: name, Notification: &desired})
		for sp.HasMorePages() {
			page, err := sp.NextPage(ctx)
			if err != nil {
				return err
			}
			subs = append(subs, page.Subscribers...)
		}
		var stale []bt.Subscriber
		present, others := false, 0
		for _, sub := range subs {
			if sub.SubscriptionType != bt.SubscriptionTypeEmail {
				others++
				continue
			}
			if aws.ToString(sub.Address) == email {
				present = true
			} else {
				stale = append(stale, sub)
			}
		}
		if email == "" && others == 0 {
			if _, err := p.budgets.DeleteNotification(ctx, &budgets.DeleteNotificationInput{AccountId: account, BudgetName: name, Notification: &desired}); err != nil {
				return err
			}
			continue
		}
		if email != "" && !present {
			if len(stale) > 0 {
				// Rotate in place: no gap, and works at the subscriber quota.
				if _, err := p.budgets.UpdateSubscriber(ctx, &budgets.UpdateSubscriberInput{AccountId: account, BudgetName: name, Notification: &desired, OldSubscriber: &stale[0], NewSubscriber: &want}); err != nil {
					return err
				}
				stale = stale[1:]
			} else {
				if _, err := p.budgets.CreateSubscriber(ctx, &budgets.CreateSubscriberInput{AccountId: account, BudgetName: name, Notification: &desired, Subscriber: &want}); err != nil {
					return err
				}
			}
		}
		for _, sub := range stale {
			if _, err := p.budgets.DeleteSubscriber(ctx, &budgets.DeleteSubscriberInput{AccountId: account, BudgetName: name, Notification: &desired, Subscriber: &sub}); err != nil {
				return err
			}
		}
	}
	return nil
}

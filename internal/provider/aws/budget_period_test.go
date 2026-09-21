package aws

import (
	"context"
	"testing"
	"time"
)

func TestBudgetMustBeInActivePeriod(t *testing.T) {
	now := budgetSpec().Now
	for _, mode := range []string{"active", "expired", "future"} {
		t.Run(mode, func(t *testing.T) {
			start, end := now.Add(-time.Hour), now.Add(time.Hour)
			if mode == "expired" {
				end = now
			}
			if mode == "future" {
				start = now.Add(time.Minute)
			}
			p := budgetHTTP(t, func(name string, _ map[string]any) (any, int) {
				if name != "DescribeBudget" {
					return unexpectedBudgetAPI(t, name)
				}
				return map[string]any{"Budget": map[string]any{"BudgetName": "playplace-" + budgetTestID, "BudgetType": "COST", "TimeUnit": "MONTHLY", "FilterExpression": linkedAccountFilter(budgetTestID), "Metrics": []string{"UnblendedCost"}, "TimePeriod": map[string]any{"Start": start.Unix(), "End": end.Unix()}}}, 200
			})
			_, err := p.readBudget(context.Background(), budgetTestInfo.ManagementAccountID, budgetTestID, now)
			if (err == nil) != (mode == "active") {
				t.Fatalf("%s: %v", mode, err)
			}
		})
	}
}

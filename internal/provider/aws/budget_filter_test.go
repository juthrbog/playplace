package aws

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// Wire fixtures intentionally do not use the production expression constructor.
func budgetFilterFixture() map[string]any {
	return map[string]any{"Dimensions": map[string]any{
		"Key": "LINKED_ACCOUNT", "Values": []any{budgetTestID}, "MatchOptions": []any{"EQUALS"},
	}}
}

func budgetFixture() map[string]any {
	return map[string]any{
		"BudgetName": "playplace-" + budgetTestID, "BudgetType": "COST", "TimeUnit": "MONTHLY",
		"BudgetLimit":      map[string]any{"Amount": "100.00", "Unit": "USD"},
		"FilterExpression": budgetFilterFixture(), "Metrics": []any{"UnblendedCost"},
	}
}

func TestBudgetFilterRejectsScopeDrift(t *testing.T) {
	for _, mode := range []string{
		"exact", "default-match", "translated-legacy", "and-wrapper", "or-wrapper", "empty-wrapper", "too-deep", "multi-branch", "legacy-only", "missing", "empty",
		"wrong-account", "multiple-accounts", "empty-values", "wrong-key", "prefix", "absent", "multiple-match-options", "unknown-match",
		"and", "or", "not", "tags", "cost-category", "missing-dimensions",
		"missing-metric", "multiple-metrics", "usage-metric", "unknown-metric",
	} {
		t.Run(mode, func(t *testing.T) {
			b := budgetFixture()
			e := b["FilterExpression"].(map[string]any)
			d := e["Dimensions"].(map[string]any)
			// Even correct legacy fields must not mask drift in the modern fields.
			b["CostFilters"] = map[string]any{"LinkedAccount": []any{budgetTestID}}
			b["CostTypes"] = map[string]any{"UseBlended": false, "IncludeTax": true}
			switch mode {
			case "exact":
				delete(b, "CostFilters")
				delete(b, "CostTypes")
			case "default-match":
				delete(d, "MatchOptions")
			case "and-wrapper":
				b["FilterExpression"] = map[string]any{"And": []any{e}}
			case "or-wrapper":
				b["FilterExpression"] = map[string]any{"Or": []any{e}}
			case "empty-wrapper":
				b["FilterExpression"] = map[string]any{"And": []any{}}
			case "too-deep":
				for range 40 {
					b["FilterExpression"] = map[string]any{"And": []any{b["FilterExpression"]}}
				}
			case "multi-branch":
				b["FilterExpression"] = map[string]any{"And": []any{e, map[string]any{"Dimensions": map[string]any{"Key": "SERVICE", "Values": []any{"Amazon EC2"}}}}}
			case "legacy-only", "missing":
				delete(b, "FilterExpression")
			case "empty":
				b["FilterExpression"] = map[string]any{}
			case "wrong-account":
				d["Values"] = []any{"333333333333"}
			case "multiple-accounts":
				d["Values"] = []any{budgetTestID, "333333333333"}
			case "empty-values":
				d["Values"] = []any{}
			case "wrong-key":
				d["Key"] = "SERVICE"
			case "prefix":
				d["MatchOptions"] = []any{"STARTS_WITH"}
			case "absent":
				d["MatchOptions"] = []any{"ABSENT"}
			case "multiple-match-options":
				d["MatchOptions"] = []any{"EQUALS", "CONTAINS"}
			case "unknown-match":
				d["MatchOptions"] = []any{"NEW_MATCH"}
			case "and":
				e["And"] = []any{map[string]any{"Dimensions": map[string]any{"Key": "SERVICE", "Values": []any{"Amazon EC2"}}}}
			case "or":
				e["Or"] = []any{map[string]any{"Dimensions": map[string]any{"Key": "LINKED_ACCOUNT", "Values": []any{"333333333333"}}}}
			case "not":
				e["Not"] = budgetFilterFixture()
			case "tags":
				e["Tags"] = map[string]any{"Key": "team", "Values": []any{"dev"}}
			case "cost-category":
				e["CostCategories"] = map[string]any{"Key": "team", "Values": []any{"dev"}}
			case "missing-dimensions":
				delete(e, "Dimensions")
			case "missing-metric":
				delete(b, "Metrics")
			case "multiple-metrics":
				b["Metrics"] = []any{"UnblendedCost", "AmortizedCost"}
			case "usage-metric":
				b["Metrics"] = []any{"UsageQuantity"}
			case "unknown-metric":
				b["Metrics"] = []any{"NewMetric"}
			}
			if mode == "missing" {
				delete(b, "CostFilters")
			}
			valid := mode == "exact" || mode == "default-match" || mode == "translated-legacy" || mode == "and-wrapper" || mode == "or-wrapper"
			p := budgetHTTP(t, func(name string, _ map[string]any) (any, int) {
				switch name {
				case "DescribeBudget":
					return map[string]any{"Budget": b}, 200
				case "DescribeNotificationsForBudget":
					if valid {
						return map[string]any{"Notifications": []any{}}, 200
					}
				}
				// No updates, notifications, or action calls for unsafe budgets.
				return unexpectedBudgetAPI(t, name)
			})
			spec := budgetSpec()
			spec.PolicyID, spec.RoleARN, spec.Email = "", "", ""
			state, err := p.EnsureBudget(context.Background(), budgetTestID, spec)
			if (err == nil) != valid || state.Configured != valid {
				t.Fatalf("configured=%v err=%v", state.Configured, err)
			}
		})
	}
}

func TestNewBudgetUsesModernFieldsOnly(t *testing.T) {
	created := false
	p := budgetHTTP(t, func(name string, in map[string]any) (any, int) {
		switch name {
		case "DescribeBudget":
			return map[string]any{"__type": "NotFoundException", "Message": "missing"}, 400
		case "CreateBudget":
			created = true
			if in["AccountId"] != budgetTestInfo.ManagementAccountID || !reflect.DeepEqual(in["Budget"], budgetFixture()) {
				t.Errorf("unexpected new budget payload: %+v", in)
			}
			return map[string]any{}, 200
		case "DescribeNotificationsForBudget":
			return map[string]any{"Notifications": []any{}}, 200
		default:
			return unexpectedBudgetAPI(t, name)
		}
	})
	spec := budgetSpec()
	spec.PolicyID, spec.RoleARN, spec.Email = "", "", ""
	if state, err := p.EnsureBudget(context.Background(), budgetTestID, spec); err != nil || !state.Configured || !created {
		t.Fatalf("create: %+v %v created=%v", state, err, created)
	}
}

func TestBudgetUpdatePreservesModernFieldsWithoutMixingLegacyFields(t *testing.T) {
	for _, metric := range []string{"UnblendedCost", "BlendedCost", "AmortizedCost", "NetUnblendedCost", "NetAmortizedCost"} {
		t.Run(metric, func(t *testing.T) {
			spec := budgetSpec()
			spec.PolicyID, spec.RoleARN, spec.Email = "", "", ""
			b := budgetFixture()
			b["BudgetLimit"] = map[string]any{"Amount": "50.00", "Unit": "USD"}
			b["Metrics"] = []any{metric}
			if metric == "AmortizedCost" {
				b["FilterExpression"] = map[string]any{"And": []any{b["FilterExpression"]}}
			}
			b["TimePeriod"] = map[string]any{"Start": float64(spec.Now.Add(-time.Hour).Unix()), "End": float64(spec.Now.Add(time.Hour).Unix())}
			b["BillingViewArn"] = "arn:aws:billing::222222222222:billingview/test"
			// Describing an existing legacy budget returns both representations.
			b["CostFilters"] = map[string]any{"LinkedAccount": []any{budgetTestID}}
			b["CostTypes"] = map[string]any{"UseBlended": metric == "BlendedCost", "IncludeTax": true}
			b["CalculatedSpend"] = map[string]any{"ActualSpend": map[string]any{"Amount": "60", "Unit": "USD"}}
			b["LastUpdatedTime"] = float64(spec.Now.Unix())
			b["HealthStatus"] = map[string]any{"Status": "HEALTHY"}
			updates := 0
			p := budgetHTTP(t, func(name string, in map[string]any) (any, int) {
				switch name {
				case "DescribeBudget":
					return map[string]any{"Budget": b}, 200
				case "UpdateBudget":
					updates++
					want := budgetFixture()
					want["FilterExpression"] = b["FilterExpression"]
					want["Metrics"], want["TimePeriod"], want["BillingViewArn"] = b["Metrics"], b["TimePeriod"], b["BillingViewArn"]
					if in["AccountId"] != budgetTestInfo.ManagementAccountID || !reflect.DeepEqual(in["NewBudget"], want) {
						t.Errorf("mixed fields or changed semantics: %+v; want %+v", in, want)
					}
					b["BudgetLimit"] = want["BudgetLimit"]
					return map[string]any{}, 200
				case "DescribeNotificationsForBudget":
					return map[string]any{"Notifications": []any{}}, 200
				default:
					return unexpectedBudgetAPI(t, name)
				}
			})
			for range 2 { // the second pass must not reset spend merely to migrate
				if state, err := p.EnsureBudget(context.Background(), budgetTestID, spec); err != nil || !state.Configured {
					t.Fatalf("update: %+v %v", state, err)
				}
			}
			if updates != 1 {
				t.Fatalf("updates=%d, want one", updates)
			}
		})
	}
}

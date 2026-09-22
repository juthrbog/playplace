package aws

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	bt "github.com/aws/aws-sdk-go-v2/service/budgets/types"
	"playplace/internal/core"
)

// Exercise the composition that adapter-only retirement tests cannot see:
// inventory decoding must not erase ownership before fresh AWS safety checks.
func TestOwnedDamagedAccountRetirementThroughRefresh(t *testing.T) {
	for _, mode := range []string{"missing-expiry", "bad-expiry", "ownership-drift"} {
		t.Run(mode, func(t *testing.T) {
			spec := budgetSpec()
			tags := map[string]string{core.TagManaged: "true", core.TagBudgetPolicy: spec.PolicyID, core.TagBudget: "broken", core.TagCloseRequested: "broken"}
			if mode != "missing-expiry" {
				tags[core.TagExpires] = "broken"
			}
			action := budgetAction(spec, bt.ActionStatusExecutionSuccess)
			deleted, stateReads, tagReads := 0, 0, 0
			p := budgetHTTP(t, func(name string, in map[string]any) (any, int) {
				switch name {
				case "ListAccountsForParent":
					return map[string]any{"Accounts": []map[string]any{{"Id": budgetTestID, "Name": "closed-damaged", "Email": "owner@example.com", "State": "CLOSED"}}}, 200
				case "ListCreateAccountStatus":
					return map[string]any{"CreateAccountStatuses": []any{}}, 200
				case "DescribeAccount":
					stateReads++
					return map[string]any{"Account": map[string]any{"Id": budgetTestID, "State": "CLOSED"}}, 200
				case "ListTagsForResource":
					if in["ResourceId"] != nil {
						if in["ResourceId"] != budgetTestID {
							return map[string]any{"Tags": []any{}}, 200
						} // OU request queue
						tagReads++
						var out []map[string]string
						for key, value := range tags {
							if mode == "ownership-drift" && tagReads > 1 && key == core.TagManaged {
								value = "false"
							}
							out = append(out, map[string]string{"Key": key, "Value": value})
						}
						return map[string]any{"Tags": out}, 200
					}
					return map[string]any{"ResourceTags": []bt.ResourceTag{{Key: aws.String(core.TagManaged), Value: aws.String("true")}, {Key: aws.String("playplace:account"), Value: aws.String(budgetTestID)}}}, 200
				case "ListParents":
					return map[string]any{"Parents": []map[string]any{{"Id": budgetTestInfo.PlaygroundOUID, "Type": "ORGANIZATIONAL_UNIT"}}}, 200
				case "DescribeBudgetActionsForBudget":
					if deleted > 0 {
						return map[string]any{"Actions": []bt.Action{}}, 200
					}
					return map[string]any{"Actions": []bt.Action{action}}, 200
				case "DeleteBudgetAction":
					if stateReads != 2 || in["ActionId"] != aws.ToString(action.ActionId) {
						t.Errorf("unsafe deletion: %d reads, %v", stateReads, in)
					}
					deleted++
					return map[string]any{}, 200
				case "TagResource":
					for _, raw := range in["Tags"].([]any) {
						tag := raw.(map[string]any)
						tags[tag["Key"].(string)] = tag["Value"].(string)
					}
					return map[string]any{}, 200
				default:
					// No active budget setup, reverse/reset, or policy mutation.
					return unexpectedBudgetAPI(t, name)
				}
			})
			cfg := core.DefaultConfig()
			cfg.BudgetPolicyID, cfg.BudgetActionRole = spec.PolicyID, spec.RoleARN
			ctx := context.Background()
			restart := func() *core.Service {
				s := core.NewService(p, nil, cfg, nil)
				s.Now = func() time.Time { return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC) }
				return s
			}
			if _, err := restart().Refresh(ctx); err == nil {
				t.Fatal("expected pending retirement or drift error")
			}
			if mode == "ownership-drift" {
				if deleted != 0 || tags[core.TagBudgetHealth] != "error" {
					t.Fatal("cached ownership bypassed fresh AWS validation")
				}
				return
			}
			if deleted != 1 || tags[core.TagBudgetHealth] != "retiring" {
				t.Fatalf("owned action was skipped: %v", tags)
			}
			if _, err := restart().Refresh(ctx); err != nil {
				t.Fatal(err)
			}
			if deleted != 1 || tags[core.TagBudgetHealth] != "retired" || tags[core.TagBudget] != "broken" || tags[core.TagCloseRequested] != "broken" {
				t.Fatalf("retirement/irrelevant facts: %v", tags)
			}
		})
	}
}

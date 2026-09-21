package aws

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	bt "github.com/aws/aws-sdk-go-v2/service/budgets/types"
	"playplace/internal/core"
)

func TestBudgetActionRetirement(t *testing.T) {
	for _, mode := range []string{
		"standby", "executed", "absent", "budget-absent", "delete-not-found", "response-lost", "later-page-missing",
		"locked", "denied", "describe-denied", "tags-not-found", "list-denied", "later-page-denied",
		"active", "pending", "suspended", "unknown", "legacy-closed", "reopened", "missing-account", "management",
		"unmanaged", "wrong-ou", "account-policy-drift", "wrong-role-config",
		"foreign-action", "foreign-policy-conflict", "wrong-account-tag", "wrong-target", "wrong-policy", "wrong-role",
		"duplicate", "paginated", "foreign-alongside",
	} {
		t.Run(mode, func(t *testing.T) {
			spec := budgetSpec()
			// Retirement is independent of invalid amounts, missing email and old recovery.
			spec.LimitUSD, spec.Email = 0, ""
			action := budgetAction(spec, bt.ActionStatusStandby)
			foreign := budgetAction(spec, bt.ActionStatusStandby)
			foreign.ActionId = aws.String("foreign")
			foreign.Definition.ScpActionDefinition.PolicyId = aws.String("p-else123")
			if mode == "executed" || mode == "locked" {
				action.Status = bt.ActionStatusExecutionSuccess
			}
			if mode == "wrong-target" {
				action.Definition.ScpActionDefinition.TargetIds = []string{budgetTestID, "333333333333"}
			}
			if mode == "wrong-policy" || mode == "foreign-action" {
				action.Definition.ScpActionDefinition.PolicyId = aws.String("p-else123")
			}
			if mode == "wrong-role" {
				action.ExecutionRoleArn = aws.String(spec.RoleARN + "Other")
			}
			if mode == "wrong-role-config" {
				spec.RoleARN = "arn:aws:iam::333333333333:role/Other"
			}
			id := budgetTestID
			if mode == "management" {
				id = budgetTestInfo.ManagementAccountID
			}
			deleted, reads := 0, 0
			p := budgetHTTP(t, func(name string, in map[string]any) (any, int) {
				fail := func(kind string) (any, int) {
					return map[string]any{"__type": kind, "Message": "test " + mode}, 400
				}
				switch name {
				case "DescribeAccount":
					reads++
					if in["AccountId"] != budgetTestID {
						t.Errorf("wrong member: %v", in)
					}
					if mode == "describe-denied" {
						return fail("AccessDeniedException")
					}
					if mode == "missing-account" {
						return map[string]any{}, 200
					}
					state := "CLOSED"
					switch mode {
					case "active":
						state = "ACTIVE"
					case "pending":
						state = "PENDING_CLOSURE"
					case "suspended":
						state = "SUSPENDED"
					case "unknown":
						state = "NEW_STATE"
					case "legacy-closed":
						state = ""
					case "reopened":
						if reads > 1 {
							state = "ACTIVE"
						}
					}
					return map[string]any{"Account": map[string]any{"Id": budgetTestID, "State": state, "Status": "CLOSED"}}, 200
				case "ListTagsForResource":
					if in["ResourceId"] != nil { // Organizations account tags
						managed, policy := "true", spec.PolicyID
						if mode == "unmanaged" {
							managed = "false"
						}
						if mode == "account-policy-drift" {
							policy = "p-else123"
						}
						return map[string]any{"Tags": []map[string]any{{"Key": core.TagManaged, "Value": managed}, {"Key": core.TagBudgetPolicy, "Value": policy}}}, 200
					}
					if mode == "tags-not-found" {
						return fail("NotFoundException")
					}
					managed, account := "true", budgetTestID
					if mode == "foreign-action" || mode == "foreign-policy-conflict" || strings.HasSuffix(in["ResourceARN"].(string), "/foreign") {
						managed = "false"
					}
					if mode == "wrong-account-tag" {
						account = "333333333333"
					}
					return map[string]any{"ResourceTags": []bt.ResourceTag{{Key: aws.String(core.TagManaged), Value: aws.String(managed)}, {Key: aws.String("playplace:account"), Value: aws.String(account)}}}, 200
				case "ListParents":
					parent := budgetTestInfo.PlaygroundOUID
					if mode == "wrong-ou" {
						parent = "ou-other-12345678"
					}
					return map[string]any{"Parents": []map[string]any{{"Id": parent, "Type": "ORGANIZATIONAL_UNIT"}}}, 200
				case "DescribeBudgetActionsForBudget":
					if in["AccountId"] != budgetTestInfo.ManagementAccountID || in["BudgetName"] != "playplace-"+budgetTestID {
						t.Errorf("wrong budget: %v", in)
					}
					if mode == "budget-absent" || (mode == "later-page-missing" && in["NextToken"] != nil) {
						return fail("NotFoundException")
					}
					if mode == "list-denied" || (mode == "later-page-denied" && in["NextToken"] != nil) {
						return fail("AccessDeniedException")
					}
					if mode == "absent" || (deleted > 0 && mode != "locked" && mode != "denied") {
						if mode == "foreign-alongside" {
							return map[string]any{"Actions": []bt.Action{foreign}}, 200
						}
						return map[string]any{"Actions": []bt.Action{}}, 200
					}
					if mode == "paginated" && in["NextToken"] == nil {
						return map[string]any{"Actions": []bt.Action{}, "NextToken": "next"}, 200
					}
					out := map[string]any{"Actions": []bt.Action{action}}
					if (mode == "duplicate" || mode == "later-page-denied" || mode == "later-page-missing") && in["NextToken"] == nil {
						out["NextToken"] = "next"
					}
					if mode == "foreign-alongside" {
						out["Actions"] = []bt.Action{action, foreign}
					}
					return out, 200
				case "DeleteBudgetAction":
					deleted++
					if reads != 2 || in["ActionId"] != aws.ToString(action.ActionId) || in["AccountId"] != budgetTestInfo.ManagementAccountID || in["BudgetName"] != "playplace-"+budgetTestID {
						t.Errorf("unsafe delete: reads=%d %v", reads, in)
					}
					switch mode {
					case "locked":
						return fail("ResourceLockedException")
					case "denied":
						return fail("AccessDeniedException")
					case "delete-not-found":
						return fail("NotFoundException")
					case "response-lost":
						return fail("InternalErrorException")
					}
					return map[string]any{}, 200
				default:
					// In particular: no reversal/reset, budget deletion, or SCP mutations.
					return unexpectedBudgetAPI(t, name)
				}
			})
			pending := mode == "standby" || mode == "executed" || mode == "paginated" || mode == "foreign-alongside" || mode == "delete-not-found"
			absent := mode == "absent" || mode == "budget-absent" || mode == "foreign-action"
			done, err := p.RetireBudgetAction(context.Background(), id, spec)
			if (err == nil) != (pending || absent) || done != absent {
				t.Fatalf("done=%v err=%v", done, err)
			}
			wantDeletes := 0
			if pending || mode == "locked" || mode == "denied" || mode == "response-lost" {
				wantDeletes = 1
			}
			if deleted != wantDeletes {
				t.Fatalf("deletes=%d want=%d", deleted, wantDeletes)
			}
			if pending || absent || mode == "response-lost" {
				for range 2 { // observation, then idempotence
					done, err = p.RetireBudgetAction(context.Background(), id, spec)
					if !done || err != nil || deleted != wantDeletes {
						t.Fatalf("repeat: done=%v err=%v deletes=%d", done, err, deleted)
					}
				}
			}
		})
	}
}

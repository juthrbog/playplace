package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	bt "github.com/aws/aws-sdk-go-v2/service/budgets/types"
)

func TestBudgetTargetValidation(t *testing.T) {
	for _, mode := range []string{"valid", "management", "wrong-role", "wrong-ou", "disabled", "wrong-policy", "inherited"} {
		t.Run(mode, func(t *testing.T) {
			spec := budgetSpec()
			id := budgetTestID
			if mode == "management" {
				id = budgetTestInfo.ManagementAccountID
			}
			if mode == "wrong-role" {
				spec.RoleARN = "arn:aws:iam::999999999999:role/Budgets"
			}
			p := budgetHTTP(t, func(name string, in map[string]any) (any, int) {
				switch name {
				case "ListParents":
					parent := budgetTestInfo.PlaygroundOUID
					if mode == "wrong-ou" {
						parent = "ou-else-12345678"
					}
					return map[string]any{"Parents": []map[string]any{{"Id": parent, "Type": "ORGANIZATIONAL_UNIT"}}}, 200
				case "ListRoots":
					status := "ENABLED"
					if mode == "disabled" {
						status = "PENDING_ENABLE"
					}
					return map[string]any{"Roots": []map[string]any{{"Id": budgetTestInfo.RootID, "PolicyTypes": []map[string]any{{"Type": "SERVICE_CONTROL_POLICY", "Status": status}}}}}, 200
				case "DescribePolicy":
					typeName := "SERVICE_CONTROL_POLICY"
					if mode == "wrong-policy" {
						typeName = "TAG_POLICY"
					}
					return map[string]any{"Policy": map[string]any{"PolicySummary": map[string]any{"Id": spec.PolicyID, "Type": typeName, "AwsManaged": false}}}, 200
				case "ListTargetsForPolicy":
					targets := []map[string]any{}
					if mode == "inherited" {
						targets = append(targets, map[string]any{"Type": "ORGANIZATIONAL_UNIT", "TargetId": budgetTestInfo.PlaygroundOUID})
					}
					return map[string]any{"Targets": targets}, 200
				default:
					return unexpectedBudgetAPI(t, name)
				}
			})
			err := p.validateBudgetTarget(context.Background(), budgetTestInfo, id, spec)
			if (err == nil) != (mode == "valid") {
				t.Fatalf("validation: %v", err)
			}
		})
	}
}

func TestMissingBudgetAndNotificationsAreCreated(t *testing.T) {
	spec := budgetSpec()
	spec.PolicyID, spec.RoleARN = "", ""
	p := budgetHTTP(t, func(name string, in map[string]any) (any, int) {
		switch name {
		case "DescribeBudget":
			return map[string]any{"__type": "NotFoundException", "Message": "missing"}, 400
		case "CreateBudget":
			b := in["Budget"].(map[string]any)
			if b["BudgetName"] != "playplace-"+budgetTestID || b["TimeUnit"] != "MONTHLY" || in["AccountId"] != budgetTestInfo.ManagementAccountID {
				t.Errorf("bad budget creation: %v", in)
			}
			return map[string]any{}, 200
		case "DescribeNotificationsForBudget":
			return map[string]any{"Notifications": []any{}}, 200
		case "CreateNotification":
			subs := in["Subscribers"].([]any)
			if len(subs) != 1 || subs[0].(map[string]any)["Address"] != spec.Email {
				t.Errorf("wrong subscribers: %v", in)
			}
			return map[string]any{}, 200
		default:
			return unexpectedBudgetAPI(t, name)
		}
	})
	r, err := p.EnsureBudget(context.Background(), budgetTestID, spec)
	if err != nil || !r.Configured {
		t.Fatalf("missing budget repair: %+v %v", r, err)
	}
}

func TestActionRecipientRotationDoesNotRewriteAnExecutedTrigger(t *testing.T) {
	spec := budgetSpec()
	action := budgetAction(spec, bt.ActionStatusExecutionSuccess)
	action.Subscribers[0].Address = aws.String("old@example.com")
	p := budgetHTTP(t, func(name string, in map[string]any) (any, int) {
		switch name {
		case "DescribeBudgetActionsForBudget":
			return map[string]any{"Actions": []bt.Action{action}}, 200
		case "ListTagsForResource":
			return map[string]any{"ResourceTags": []bt.ResourceTag{{Key: aws.String("playplace:managed"), Value: aws.String("true")}, {Key: aws.String("playplace:account"), Value: aws.String(budgetTestID)}}}, 200
		case "ListPoliciesForTarget":
			return map[string]any{"Policies": []map[string]any{{"Id": spec.PolicyID}}}, 200
		case "UpdateBudgetAction":
			if in["ActionThreshold"] != nil || in["Definition"] != nil || in["ApprovalModel"] != nil {
				t.Fatalf("rotation altered executed trigger: %v", in)
			}
			if in["Subscribers"].([]any)[0].(map[string]any)["Address"] != spec.Email {
				t.Error("recipient not rotated")
			}
			return map[string]any{}, 200
		default:
			return unexpectedBudgetAPI(t, name)
		}
	})
	r, err := p.reconcileBudgetAction(context.Background(), budgetTestInfo, budgetTestID, spec)
	if err != nil || r.Configured {
		t.Fatalf("must observe repaired configuration on next pass: %+v %v", r, err)
	}
}

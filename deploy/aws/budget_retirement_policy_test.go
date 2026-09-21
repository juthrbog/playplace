package aws_test

import (
	"encoding/json"
	"os"
	"path"
	"testing"
)

func TestBudgetRetirementPermissionIsScopedAndTagged(t *testing.T) {
	data, err := os.ReadFile("playplace-budget-actions-policy.json")
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Statement []struct {
			Effect, Resource string
			Action           json.RawMessage
			Condition        map[string]map[string]string
		}
	}
	if err := json.Unmarshal(data, &policy); err != nil {
		t.Fatal(err)
	}
	grants := 0
	for _, s := range policy.Statement {
		var actions []string
		if err := json.Unmarshal(s.Action, &actions); err != nil {
			var action string
			if err := json.Unmarshal(s.Action, &action); err != nil {
				t.Fatal(err)
			}
			actions = []string{action}
		}
		for _, action := range actions {
			if match, _ := path.Match(action, "budgets:DeleteBudgetAction"); match && s.Effect == "Allow" {
				grants++
				if action != "budgets:DeleteBudgetAction" || s.Resource != "arn:aws:budgets::MANAGEMENT_ACCOUNT_ID:budget/playplace-*/action/*" || s.Condition["StringEquals"]["aws:ResourceTag/playplace:managed"] != "true" {
					t.Fatalf("retirement permission must be narrowly scoped and require ownership: %+v", s)
				}
			}
		}
	}
	if grants != 1 {
		t.Fatalf("want exactly one retirement grant; got %d", grants)
	}
}

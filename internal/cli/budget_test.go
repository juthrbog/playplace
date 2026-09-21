package cli

import (
	"github.com/spf13/cobra"
	"testing"
)

func TestBudgetEnforcementConfig(t *testing.T) {
	for _, tc := range []struct {
		policy, role, email string
		bad                 bool
	}{
		{"", "", "", false},
		{"p-budget12", "", "ops@example.com", true},
		{"", "arn:aws:iam::222222222222:role/Budgets", "ops@example.com", true},
		{"p-budget12", "arn:aws:iam::222222222222:role/Budgets", "", true},
		{"p-budget12", "arn:aws:iam::222222222222:role/Budgets", "not-an-email", true},
		{"p-budget12", "arn:aws:iam::222222222222:role/Budgets", "ops@example.com", false},
	} {
		var o options
		cmd := &cobra.Command{Use: "test"}
		o.bind(cmd)
		o.BudgetPolicyID, o.BudgetActionRole, o.AlertEmail = tc.policy, tc.role, tc.email
		cfg, err := o.coreConfig()
		if (err != nil) != tc.bad {
			t.Fatalf("%+v: %v", tc, err)
		}
		if !tc.bad && cfg.BudgetPolicyID != tc.policy {
			t.Fatal("policy not passed to core")
		}
	}
}

func TestBudgetCommandRegistered(t *testing.T) {
	cmd, _, err := New().Find([]string{"budget"})
	if err != nil || cmd.Name() != "budget" || cmd.Flags().Lookup("amount") == nil || cmd.Flags().Lookup("reason") == nil {
		t.Fatalf("budget command not registered: %v", err)
	}
}

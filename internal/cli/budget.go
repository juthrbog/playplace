package cli

import (
	"context"
	"fmt"
	"github.com/spf13/cobra"
)

func budgetCmd(opts *options) *cobra.Command {
	var amount float64
	var reason string
	var override bool
	cmd := &cobra.Command{
		Use:   "budget ACCOUNT --amount USD --reason TEXT",
		Short: "Change an existing account's monthly budget (operators only)",
		Args:  cobra.ExactArgs(1),
		RunE: withApp(opts, true, func(ctx context.Context, a *app, args []string) error {
			acc, err := a.svc.SetBudget(ctx, args[0], amount, operator(), reason, override)
			if acc != nil {
				fmt.Printf("%s: $%.2f/month, protection %s\n", acc.Name, acc.BudgetUSD, acc.BudgetHealth)
			}
			return err
		}),
	}
	cmd.Flags().Float64Var(&amount, "amount", 0, "new recurring monthly USD limit, at most two decimal places")
	cmd.Flags().StringVar(&reason, "reason", "", "audit reason (required)")
	cmd.Flags().BoolVar(&override, "override-limits", false, "explicitly override --max-budget; audited")
	return cmd
}

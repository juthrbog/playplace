package cli

import (
	"fmt"

	"playplace/internal/core"
)

func accountBudget(a *core.Account) string {
	if a.BudgetUSD <= 0 {
		return "unknown"
	}
	return fmt.Sprintf("$%.2f", a.BudgetUSD)
}

func accountExpiry(a *core.Account, layout string) string {
	if a.ExpiresAt.IsZero() {
		return "unknown"
	}
	return a.ExpiresAt.Format(layout)
}

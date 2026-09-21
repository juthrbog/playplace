package core

import (
	"context"
	"errors"
	"fmt"
)

// advanceBudgetRecovery is private to the budget module. SetBudget stores the
// approval before EnsureBudget may change the amount; reconcileBudget observes
// configured actions here and persists progress through recordBudgetProtection.
// In particular, observing reverse success only records reset intent. RESET can
// run only on a later pass that has read the durable reset phase.
func (s *Service) advanceBudgetRecovery(ctx context.Context, a *Account, spec BudgetSpec, observed BudgetProtection) (budgetProgress, error) {
	r := budgetProgress{BudgetProtection: observed}
	if c := spec.Recovery; c != nil {
		if c.ActionID != r.ActionID || c.Limit != spec.LimitUSD {
			return r, errors.New("budget recovery does not match the action and approved limit")
		}
		if c.Period != spec.Now.UTC().Format("2006-01") {
			r.recoveryDone = true // old approval cannot reverse this month's action
		} else {
			r.State = "recovering"
			execute := func(kind BudgetActionOperation) (budgetProgress, error) {
				return r, s.provider.ExecuteBudgetAction(ctx, a.ProviderID, spec, r.ActionID, kind)
			}
			if c.Phase == "reverse" {
				switch observed.Action.Status {
				case "EXECUTION_SUCCESS", "REVERSE_FAILURE":
					return execute(BudgetActionReverse)
				case "REVERSE_SUCCESS":
					r.recoveryPhase = "reset"
					return r, nil
				case "REVERSE_IN_PROGRESS", "EXECUTION_IN_PROGRESS":
					return r, nil
				case "STANDBY":
					if observed.Action.RestrictionAttached {
						return r, nil
					}
					r.recoveryDone = true
				default:
					return r, fmt.Errorf("cannot reverse budget action in state %s", observed.Action.Status)
				}
			} else {
				switch observed.Action.Status {
				case "REVERSE_SUCCESS", "RESET_FAILURE":
					return execute(BudgetActionReset)
				case "RESET_IN_PROGRESS":
					return r, nil
				case "STANDBY":
					if observed.Action.RestrictionAttached {
						return r, nil
					}
					r.recoveryDone = true
				case "EXECUTION_SUCCESS", "EXECUTION_IN_PROGRESS", "EXECUTION_FAILURE", "PENDING":
					// Rearmed and evaluated again: never reuse this approval.
					r.recoveryDone = true
				default:
					return r, fmt.Errorf("cannot reset budget action in state %s", observed.Action.Status)
				}
			}
		}
	}
	switch observed.Action.Status {
	case "STANDBY":
		if observed.Action.RestrictionAttached {
			return r, errors.New("restriction attached while action is in STANDBY; verify ownership/propagation")
		}
		r.State, r.Configured = "ready", true
	case "EXECUTION_SUCCESS":
		if !observed.Action.RestrictionAttached {
			return r, errors.New("budget action completed but restriction attachment is missing")
		}
		r.State, r.Configured = "restricted", true
	case "EXECUTION_IN_PROGRESS":
		r.State = "restricting"
	default:
		return r, fmt.Errorf("budget action %s needs attention (state %s)", r.ActionID, observed.Action.Status)
	}
	return r, nil
}

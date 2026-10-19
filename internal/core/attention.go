package core

import "time"

// AttentionKind names one reason why a person must act on an account. Its
// value is also the value of the attention filter in a web address.
type AttentionKind string

// The kinds in the order of the Needs attention ledger. Over budget needs
// spend, which is not on Account, so the web adds that kind itself.
const (
	AttentionClosureOverdue AttentionKind = "closure-overdue" // AWS has not confirmed a closure in CloseAlertAfter
	AttentionFailed         AttentionKind = "failed"          // AWS did not create the account
	AttentionRestricted     AttentionKind = "restricted"      // budget protection restricts the account
	AttentionBudgetError    AttentionKind = "budget-error"    // budget protection failed
	AttentionRepair         AttentionKind = "repair"          // damaged tags need an operator
	AttentionSettlingLong   AttentionKind = "settling-long"   // protection settles for ProtectionSettleAlert or longer
	AttentionUnavailable    AttentionKind = "unavailable"     // AWS reports a state that is not closure
	AttentionExpiring       AttentionKind = "expiring"        // the expiring notice was sent
)

// ClosureOverdue reports whether AWS has not confirmed a requested closure
// in time. FromRemote sets LastError on a closing account only after
// CloseAlertAfter.
func (a *Account) ClosureOverdue() bool {
	return a.Status == StatusClosing && a.LastError != ""
}

// budgetSettling reports whether a budget protection state is one that a
// later pass completes.
func budgetSettling(health string) bool {
	switch health {
	case "setup-pending", "restricting", "recovering", "retiring":
		return true
	}
	return false
}

// settlingTooLong reports whether budget protection settles for
// ProtectionSettleAlert or longer at now. BudgetChecked records when the
// state began, and a value that does not parse counts as too long, as in the
// reconcile pass.
func (a *Account) settlingTooLong(now time.Time) bool {
	if !budgetSettling(a.BudgetHealth) {
		return false
	}
	since, err := time.Parse(time.RFC3339, a.BudgetChecked)
	return err != nil || now.Sub(since) >= ProtectionSettleAlert
}

// Attention lists why a person must act on the account at now, in the order
// of the Needs attention ledger. It is empty when nothing needs a person. A
// closed account can still need attention for its budget protection, because
// the retirement of a budget action can fail. Its damaged tags need no
// repair, because nothing acts on them any more.
func (a *Account) Attention(now time.Time) []AttentionKind {
	var out []AttentionKind
	if a.ClosureOverdue() {
		out = append(out, AttentionClosureOverdue)
	}
	if a.Status == StatusFailed {
		out = append(out, AttentionFailed)
	}
	switch a.BudgetHealth {
	case "restricted":
		out = append(out, AttentionRestricted)
	case "error":
		out = append(out, AttentionBudgetError)
	}
	if a.Status != StatusClosed && a.RepairError() != nil {
		out = append(out, AttentionRepair)
	}
	if a.settlingTooLong(now) {
		out = append(out, AttentionSettlingLong)
	}
	if a.Status == StatusUnavailable {
		out = append(out, AttentionUnavailable)
	}
	if a.Status == StatusExpiring {
		out = append(out, AttentionExpiring)
	}
	return out
}

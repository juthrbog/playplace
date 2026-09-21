package core

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	handoffPlacing  = "placing" // enrolled in the original creation tags
	handoffPending  = "pending" // placed or newly adopted; awaiting readiness
	handoffComplete = "complete"
)

// reconcileReadiness progresses an account already in the Playground OU. Both
// creation polling and Refresh enter here under opMu. Protection/access repair
// continues for legacy and handed-off accounts; only explicitly enrolled accounts
// get an initial handoff. The provider owns observed budget/access completion.
func (s *Service) reconcileReadiness(ctx context.Context, a *Account) error {
	if a.handoff == handoffPlacing {
		// Placement is a fact even if protection or access is still pending.
		// Persist before the best-effort history write, including after a crash
		// between the provider's move and tag operations.
		if err := s.provider.SetTags(ctx, a.ProviderID, map[string]string{TagHandoff: handoffPending}); err != nil {
			return fmt.Errorf("record placement of %s: %w", a.Name, err)
		}
		a.handoff = handoffPending
		s.Invalidate()
		s.audit(ctx, "placed", a, ActorSystem, "", fmt.Sprintf("%s is placed in the Playground OU as account %s for %s, expires %s", a.Name, a.ProviderID, a.Owner, a.ExpiresAt.Format("2006-01-02")),
			map[string]string{"expires": a.ExpiresAt.UTC().Format(time.RFC3339), "budget": strconv.FormatFloat(a.BudgetUSD, 'f', -1, 64), "approved_by": a.ApprovedBy, "requested_by": a.RequestedBy})
	}
	if !a.Managed || a.ProviderState != "ACTIVE" || !a.Status.CanExtend() || a.CloseRequestedAt != nil || !a.ExpiresAt.After(s.Now()) {
		return nil
	}
	if err := s.reconcileBudget(ctx, a); err != nil {
		return err
	}
	// A slow protection check can cross expiry; never issue a new grant then.
	if !a.ExpiresAt.After(s.Now()) {
		return nil
	}
	beforeAccess := a.AccessGrantedTo
	if err := s.grantAccess(ctx, a); err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil // adopted accounts may await an identifiable owner
		}
		return fmt.Errorf("grant %s: %w", a.Name, err)
	}
	if beforeAccess != a.AccessGrantedTo {
		s.Invalidate()
	}
	if a.handoff != handoffPending || !a.ExpiresAt.After(s.Now()) {
		return nil
	}
	// Configured protection may be actively restricting the account. Keep
	// reconciling it, but do not celebrate the initial handoff until usable.
	switch a.BudgetHealth {
	case "ready", "alerts-only", "alerts-disabled":
	default:
		return nil
	}
	if s.provider.AccessEnabled() {
		if a.AccessGrantedTo == "" {
			return nil
		}
	} else {
		switch strings.ToLower(strings.TrimSpace(a.Owner)) {
		case "", "unknown", "?":
			return nil
		}
	}
	// A failed durable write must not notify. Delivery itself stays best-effort:
	// a crash or notifier failure after this write can lose, not repeat, a notice.
	if err := s.provider.SetTags(ctx, a.ProviderID, map[string]string{TagHandoff: handoffComplete}); err != nil {
		return fmt.Errorf("record initial handoff of %s: %w", a.Name, err)
	}
	a.handoff = handoffComplete
	s.Invalidate()
	body := fmt.Sprintf("Account %s (%s) is ready for %s. It expires %s.", a.Name, a.ProviderID, a.Owner, a.ExpiresAt.Format("2006-01-02"))
	if a.AccessGrantedTo != "" {
		body += " Sign in through the access portal; the account is listed there."
	}
	s.notify(ctx, a, "Playground account ready", body)
	return nil
}

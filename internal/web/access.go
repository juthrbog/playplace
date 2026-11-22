package web

import (
	"strings"
	"time"

	"playplace/internal/core"
)

// accessKind is where the AWS console access of an account stands, as the
// pages show it.
type accessKind int

const (
	// accessNone: the account is not active or expiring, so the pages say
	// nothing about access. Its status label already says why.
	accessNone accessKind = iota
	// accessGranted: playplace granted the owner console access.
	accessGranted
	// accessReady: the account is still getting ready. Text says what it
	// waits for.
	accessReady
	// accessUnmanaged: playplace does not manage console access, so the
	// owner signs in with access that someone else gives.
	accessUnmanaged
)

// accessView is the access state of an account in the words of the pages.
// It never holds the principal ID or the permission set name.
type accessView struct {
	Kind accessKind
	Text string
	Icon string
}

// The words of the access states.
const (
	accessGrantedText   = "Access granted"
	accessUnmanagedText = "playplace does not manage AWS access for this account."
	accessReadyPrefix   = "Getting ready: "
)

// readyWords is one plain sentence for each kind of core.HandoffBlock. The
// budget sentences name no raw budget health value: the budget label and
// the facts show that.
var readyWords = map[core.HandoffBlock]string{
	core.HandoffStalled:         "setup has stopped. Ask an admin to check this account.",
	core.HandoffBudgetUnchecked: "waiting for budget protection to be checked.",
	core.HandoffBudgetSettling:  "waiting for budget protection to be ready.",
	core.HandoffBudgetProblem:   "budget protection needs an admin before the account can be used.",
	core.HandoffOwnerUnknown:    "waiting for the account to have a known owner.",
	core.HandoffAccessPending:   "waiting for owner access to be granted.",
}

// accessState is the access state of a at now. accessEnabled is
// Service.AccessEnabled. Only an active or expiring account has one. What
// blocks the initial handoff comes first, then the grant. An account that
// finished its handoff but has no grant while playplace manages access
// waits for the grant, as readiness retries it, or for a known owner first.
func accessState(a *core.Account, accessEnabled bool, now time.Time) accessView {
	if a.Status != core.StatusActive && a.Status != core.StatusExpiring {
		return accessView{}
	}
	block := a.HandoffBlocker(accessEnabled, now)
	switch {
	case block != "":
		return readyView(block)
	case !accessEnabled:
		return accessView{Kind: accessUnmanaged, Text: accessUnmanagedText, Icon: "minus"}
	case a.AccessGrantedTo != "":
		return accessView{Kind: accessGranted, Text: accessGrantedText, Icon: "check"}
	case core.OwnerUnknown(a.Owner):
		// The grant cannot find an owner who is not known.
		return readyView(core.HandoffOwnerUnknown)
	}
	return readyView(core.HandoffAccessPending)
}

// readyView is the getting ready state for block. A kind that the map does
// not know gets the stalled sentence, so the line never names a raw kind.
func readyView(block core.HandoffBlock) accessView {
	words, ok := readyWords[block]
	if !ok {
		words = readyWords[core.HandoffStalled]
	}
	return accessView{Kind: accessReady, Text: accessReadyPrefix + words, Icon: "clock"}
}

// accessClass is the class of the access line, which picks its icon color.
func accessClass(v accessView) string {
	switch v.Kind {
	case accessGranted:
		return "access-granted"
	case accessUnmanaged:
		return "access-unmanaged"
	}
	return "access-ready"
}

// portalLink is the address of the AWS access portal button for the
// viewer on a, or empty when the button does not show. It shows only when
// the address is set, the viewer is the owner (an admin who is not the
// owner sees the state in words only), the account is active or expiring,
// and access is granted or not managed by playplace.
func portalLink(url string, v Viewer, a *core.Account, state accessView) string {
	if url == "" || v.Email == "" || !strings.EqualFold(a.Owner, v.Email) {
		return ""
	}
	if state.Kind != accessGranted && state.Kind != accessUnmanaged {
		return ""
	}
	return url
}

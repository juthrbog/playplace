package web

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"playplace/internal/core"
)

// The home page puts the first job of each role first. An admin sees the
// Needs attention ledger beside the approval desk, then the open fleet by
// due date. An approver sees the desk, then their own accounts as loan
// cards. Anyone else sees their loan cards, then Your requests.

// homeFleetSize is the most open accounts that the fleet on the admin home
// shows. A link leads to the accounts page for the rest.
const homeFleetSize = fleetPageSize

// ledgerNames is the most account names that one row of the ledger names.
const ledgerNames = 3

// ledgerKind is how one kind of the ledger shows: its icon, its tone and its
// headline for one account and for more.
type ledgerKind struct {
	Kind string
	Icon string
	// Tone is the color class of the icon: fault (red), expiring (violet)
	// or ink. The words stay ink, because color alone never names a state.
	Tone string
	One  string // the headline for one account, after the count
	Many string // the headline for more than one account, after the count
}

// ledgerKinds are the kinds of the ledger in its order: the order of core,
// then over-budget, as in fleetAttention. The icons and tones follow the
// Needs attention labels of DESIGN.md.
var ledgerKinds = []ledgerKind{
	{string(core.AttentionClosureOverdue), "box", "fault", "closure overdue", "closures overdue"},
	{string(core.AttentionFailed), "x-circle", "fault", "failed creation", "failed creations"},
	{string(core.AttentionRestricted), "lock", "fault", "budget restricted", "budgets restricted"},
	{string(core.AttentionBudgetError), "alert", "fault", "budget error", "budget errors"},
	{string(core.AttentionRepair), "alert", "fault", "account needs repair", "accounts need repair"},
	{string(core.AttentionSettlingLong), "clock", "fault", "budget settling for over 2 hours", "budgets settling for over 2 hours"},
	{string(core.AttentionUnavailable), "ban", "ink", "account unavailable", "accounts unavailable"},
	{string(core.AttentionExpiring), "hourglass", "expiring", "expiring soon", "expiring soon"},
	{attentionOverBudget, "alert", "fault", "account over budget", "accounts over budget"},
}

// ledgerRow is one row of the Needs attention ledger: one kind and the
// accounts that have it.
type ledgerRow struct {
	Kind     string
	Icon     string
	Tone     string
	Count    int
	Headline string // such as "2 budgets restricted"
	Names    string // up to three names, such as "dev-a, dev-b and 4 more"
	URL      string // the accounts page filtered to the kind
}

// buildLedger groups the rows by why a person must act on them at now. A
// row with two kinds counts in each. Closed rows count too, because the
// budget protection of a closed account can still fail. A kind with no
// account has no row. The names are in name order, so two reads of the same
// fleet hash the same.
func buildLedger(rows []accountRow, now time.Time) []ledgerRow {
	names := map[string][]string{}
	closed := map[string]bool{}
	for _, r := range rows {
		for _, k := range rowAttention(r, now) {
			names[k] = append(names[k], r.Account.Name)
			if r.Account.Status == core.StatusClosed {
				closed[k] = true
			}
		}
	}
	var out []ledgerRow
	for _, k := range ledgerKinds {
		list := names[k.Kind]
		if len(list) == 0 {
			continue
		}
		slices.SortFunc(list, func(a, b string) int {
			return strings.Compare(strings.ToLower(a), strings.ToLower(b))
		})
		word := k.Many
		if len(list) == 1 {
			word = k.One
		}
		// The accounts page lists open accounts by default. A kind that a
		// closed account has asks for every status, so the link shows the
		// accounts that the row counts.
		f := defaultFleetView()
		f.Attention = k.Kind
		if closed[k.Kind] {
			f.Status = fleetStatusAny
		}
		out = append(out, ledgerRow{
			Kind:     k.Kind,
			Icon:     k.Icon,
			Tone:     k.Tone,
			Count:    len(list),
			Headline: strconv.Itoa(len(list)) + " " + word,
			Names:    nameList(list, ledgerNames),
			URL:      fleetURL(f),
		})
	}
	return out
}

// nameList joins the first n names, and says how many more there are.
func nameList(names []string, n int) string {
	if len(names) <= n {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:n], ", ") + " and " + strconv.Itoa(len(names)-n) + " more"
}

// homeRows are the rows that the home page shows from the rows that the
// viewer may see: every open account that is not a pending request, by due
// date, the soonest first. The desk and Your requests show the pending
// requests already. It also returns how many rows there are before the cut
// to homeFleetSize, and how many rows the accounts page lists by default,
// which counts the pending requests too.
func homeRows(rows []accountRow, v Viewer, now time.Time) (shown []accountRow, total, all int) {
	var open, notClosed []accountRow
	for _, r := range rows {
		if r.Account.Status == core.StatusClosed {
			continue
		}
		notClosed = append(notClosed, r)
		if r.Account.Status != core.StatusPending {
			open = append(open, r)
		}
	}
	shown, total, _ = defaultFleetView().apply(open, v, now)
	if len(shown) > homeFleetSize {
		shown = shown[:homeFleetSize]
	}
	_, all, _ = defaultFleetView().apply(notClosed, v, now)
	return shown, total, all
}

// limitLine says how the viewer stands against the per-owner limit. It
// counts what the core limit counts: the open accounts and the pending
// requests of the viewer.
func limitLine(pg page) string {
	limit := pg.Limits.MaxPerOwner
	switch {
	case limit <= 0:
		return "No per-owner limit applies."
	case pg.PendingFailed || pg.HoldsFailed:
		return "Your count against the per-owner limit is unknown, because the account list is unavailable."
	}
	held := len(pg.Holds[strings.ToLower(pg.Viewer.Email)])
	line := fmt.Sprintf("You have %d of %d open accounts and requests allowed.", held, limit)
	if held >= limit {
		line += " Close an account or withdraw a request before you ask for another."
	}
	return line
}

// hasLedger reports whether the answer carries the Needs attention ledger:
// only the home page of an admin has it.
func hasLedger(pg page) bool {
	return pg.Viewer.Admin && !pg.onAccounts()
}

// allAccounts is the link under the fleet of the admin home, such as "All
// 112 accounts". It leads to the accounts page, and total is what that page
// lists by default: the open accounts and the pending requests.
func allAccounts(total int) string {
	if total == 1 {
		return "All 1 account"
	}
	return "All " + strconv.Itoa(total) + " accounts"
}

// cardActions reports whether a loan card has any action.
func cardActions(a *core.Account) bool {
	return a.CanExtend() || a.Status.CanClose()
}

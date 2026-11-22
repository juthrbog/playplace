package web

import (
	"cmp"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"playplace/internal/core"
)

// The fleet view is the search, the filters, the sort and the page of the
// accounts page. It lives in the address, so a reload, Back and sign-in keep
// it. The server never refuses a view: a value it does not know falls back to
// the default.
const (
	// fleetPageSize is the number of rows on one page of the fleet.
	fleetPageSize = 50
	// fleetQueryMax is the most characters of search that the server keeps.
	fleetQueryMax = 100
	// fleetOwnerMax is the longest owner filter, the longest email address.
	fleetOwnerMax = 254
)

// The default values of the fleet view. fleetURL leaves them out.
const (
	fleetStatusOpen = "open" // every account but closed ones
	fleetStatusAny  = "any"  // every account, closed ones too
	fleetSortDue    = "due"
	fleetDirAsc     = "asc"
	fleetDirDesc    = "desc"
	fleetOwnerMe    = "me"
	fleetAnyReason  = "any" // the attention filter for any reason at all
	// fleetBudgetUnchecked is the budget filter for an account whose budget
	// protection was not checked yet.
	fleetBudgetUnchecked = "unchecked"
	// attentionOverBudget is the attention kind that the web adds to the
	// kinds of core: spend that is known and above the budget.
	attentionOverBudget = "over-budget"
)

// fleetStatuses are the statuses in lifecycle order. The status sort uses
// this order.
var fleetStatuses = []core.Status{
	core.StatusPending, core.StatusCreating, core.StatusActive, core.StatusExpiring,
	core.StatusClosing, core.StatusClosed, core.StatusUnavailable, core.StatusFailed,
}

// fleetAttention are the values of the attention filter besides "any": the
// kinds of core and the web kind over-budget.
var fleetAttention = []string{
	string(core.AttentionClosureOverdue), string(core.AttentionFailed), string(core.AttentionRestricted),
	string(core.AttentionBudgetError), string(core.AttentionRepair), string(core.AttentionSettlingLong),
	string(core.AttentionUnavailable), string(core.AttentionExpiring), attentionOverBudget,
}

// fleetSorts are the sort keys.
var fleetSorts = []string{fleetSortDue, "name", "owner", "account", "status", "spend"}

// fleetView is the view of the fleet that the address asks for. Every field
// holds a value that the server knows. Owner and Attention are empty for
// anyone but an admin, because only admins see the whole fleet.
type fleetView struct {
	Q         string // search, trimmed, at most fleetQueryMax characters
	Status    string // open, any or one status
	Budget    string // empty for any, a budget health value, or unchecked
	Owner     string // empty for every owner, me, or an email in lower case
	Attention string // empty for no filter, any, or one attention kind
	Sort      string // one of fleetSorts
	Dir       string // asc or desc
	Page      int    // from 1
}

// defaultFleetView is the view of a plain /accounts.
func defaultFleetView() fleetView {
	return fleetView{Status: fleetStatusOpen, Sort: fleetSortDue, Dir: fleetDirAsc, Page: 1}
}

// parseFleetView reads the view from get, which returns the value of a
// parameter: the query of a GET or the form body of an action. A value
// that the server does not know falls back to the default. The old Show
// closed parameter all=1 means status any when status is absent.
func parseFleetView(get func(string) string, v Viewer) fleetView {
	f := defaultFleetView()
	f.Q = fleetQuery(get("q"))
	status := enumValue(get("status"))
	switch {
	case status == "" && get("all") == "1":
		f.Status = fleetStatusAny
	case status == fleetStatusAny || slices.Contains(fleetStatuses, core.Status(status)):
		f.Status = status
	}
	if b := enumValue(get("budget")); b == fleetBudgetUnchecked {
		f.Budget = b
	} else if _, ok := budgetIcons[b]; ok {
		f.Budget = b
	}
	if v.Admin {
		f.Owner = fleetOwner(get("owner"))
		if a := enumValue(get("attention")); a == fleetAnyReason || slices.Contains(fleetAttention, a) {
			f.Attention = a
		}
	}
	if s := enumValue(get("sort")); slices.Contains(fleetSorts, s) {
		f.Sort = s
	}
	if enumValue(get("dir")) == fleetDirDesc {
		f.Dir = fleetDirDesc
	}
	if n, err := strconv.Atoi(strings.TrimSpace(get("page"))); err == nil && n > 1 {
		f.Page = n
	}
	return f
}

// enumValue is a parameter that names one of a fixed set of values.
func enumValue(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// fleetQuery trims the search and cuts it to fleetQueryMax characters.
func fleetQuery(s string) string {
	s = strings.TrimSpace(strings.ToValidUTF8(s, ""))
	if utf8.RuneCountInString(s) > fleetQueryMax {
		s = strings.TrimSpace(string([]rune(s)[:fleetQueryMax]))
	}
	return s
}

// fleetOwner is the owner filter in lower case, or empty for a value that
// cannot be an owner: one that is too long or holds a space or a control
// character.
func fleetOwner(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) > fleetOwnerMax || !utf8.ValidString(s) {
		return ""
	}
	if strings.IndexFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return ""
	}
	return s
}

// filtered reports whether the view hides accounts that the plain view or
// the view of every account shows. The empty fleet then says that nothing
// matches, not that there are no accounts.
func (f fleetView) filtered() bool {
	return f.Q != "" || f.Budget != "" || f.Owner != "" || f.Attention != "" ||
		(f.Status != fleetStatusOpen && f.Status != fleetStatusAny)
}

// fleetURL is the canonical address of the view: the keys in a fixed order,
// with the defaults left out. The poller asks for it, so the same view always
// asks for the same address and hashes the same.
func fleetURL(f fleetView) string {
	d := defaultFleetView()
	var parts []string
	add := func(key, val, def string) {
		if val != def {
			parts = append(parts, key+"="+url.QueryEscape(val))
		}
	}
	add("q", f.Q, d.Q)
	add("status", f.Status, d.Status)
	add("budget", f.Budget, d.Budget)
	add("owner", f.Owner, d.Owner)
	add("attention", f.Attention, d.Attention)
	add("sort", f.Sort, d.Sort)
	add("dir", f.Dir, d.Dir)
	if f.Page > 1 {
		parts = append(parts, "page="+strconv.Itoa(f.Page))
	}
	if len(parts) == 0 {
		return pathAccounts
	}
	return pathAccounts + "?" + strings.Join(parts, "&")
}

// overBudget reports whether the row spent more than its budget. Unknown
// spend is never over budget. A closed account needs nobody for its spend.
func overBudget(r accountRow) bool {
	return r.SpendKnown && r.Account.Status != core.StatusClosed &&
		r.Account.BudgetUSD > 0 && r.Spend > r.Account.BudgetUSD
}

// rowAttention lists why a person must act on the row at now: the kinds of
// core, then over-budget.
func rowAttention(r accountRow, now time.Time) []string {
	var out []string
	for _, k := range r.Account.Attention(now) {
		out = append(out, string(k))
	}
	if overBudget(r) {
		out = append(out, attentionOverBudget)
	}
	return out
}

// matches reports whether the row passes every filter of the view. v is the
// viewer, for the owner filter "me".
func (f fleetView) matches(r accountRow, v Viewer, now time.Time) bool {
	a := r.Account
	if f.Q != "" {
		q := strings.ToLower(f.Q)
		if !strings.Contains(strings.ToLower(a.Name), q) && !strings.Contains(strings.ToLower(a.Owner), q) &&
			!strings.Contains(strings.ToLower(a.ProviderID), q) {
			return false
		}
	}
	switch f.Status {
	case fleetStatusAny:
	case fleetStatusOpen:
		if a.Status == core.StatusClosed {
			return false
		}
	default:
		if string(a.Status) != f.Status {
			return false
		}
	}
	switch f.Budget {
	case "":
	case fleetBudgetUnchecked:
		if a.BudgetHealth != "" {
			return false
		}
	default:
		if a.BudgetHealth != f.Budget {
			return false
		}
	}
	// The owner and attention filters are for admins. parseFleetView drops
	// them for anyone else, and this check keeps it so.
	if !v.Admin {
		return true
	}
	if f.Owner != "" {
		owner := f.Owner
		if owner == fleetOwnerMe {
			owner = v.Email
		}
		if !strings.EqualFold(a.Owner, owner) {
			return false
		}
	}
	if f.Attention != "" {
		reasons := rowAttention(r, now)
		if f.Attention == fleetAnyReason {
			return len(reasons) > 0
		}
		return slices.Contains(reasons, f.Attention)
	}
	return true
}

// dueAt is when the row is due, and whether that is known. A pending row is
// due its lifetime from now. A failed or closed account is due no more, and a
// damaged expiry tag leaves the time unknown.
func dueAt(a *core.Account, now time.Time) (time.Time, bool) {
	switch {
	case a.Status == core.StatusPending:
		return now.Add(a.TTL), true
	case a.Status == core.StatusFailed || a.Status == core.StatusClosed || a.ExpiresAt.IsZero():
		return time.Time{}, false
	}
	return a.ExpiresAt, true
}

// sortKnown reports whether the row has a value for the sort key. A row
// with no value sorts last in both directions.
func sortKnown(r accountRow, key string, now time.Time) bool {
	switch key {
	case fleetSortDue:
		_, ok := dueAt(r.Account, now)
		return ok
	case "spend":
		return r.SpendKnown
	case "account":
		return r.Account.ProviderID != ""
	}
	return true
}

// sortCompare compares two rows that both have a value for the sort key, in
// ascending order.
func sortCompare(a, b accountRow, key string, now time.Time) int {
	switch key {
	case fleetSortDue:
		da, _ := dueAt(a.Account, now)
		db, _ := dueAt(b.Account, now)
		return da.Compare(db)
	case "name":
		return strings.Compare(strings.ToLower(a.Account.Name), strings.ToLower(b.Account.Name))
	case "owner":
		return strings.Compare(strings.ToLower(a.Account.Owner), strings.ToLower(b.Account.Owner))
	case "account":
		return strings.Compare(a.Account.ProviderID, b.Account.ProviderID)
	case "status":
		return cmp.Compare(slices.Index(fleetStatuses, a.Account.Status), slices.Index(fleetStatuses, b.Account.Status))
	case "spend":
		return cmp.Compare(a.Spend, b.Spend)
	}
	return 0
}

// apply filters, sorts and cuts the rows to the page of the view. It returns
// the rows of the page, the number of rows that pass the filters, and the
// page it shows. A page past the end shows the last page. The id breaks
// every tie, so the order is total and two reads of the same fleet hash the
// same.
func (f fleetView) apply(rows []accountRow, v Viewer, now time.Time) ([]accountRow, int, int) {
	var out []accountRow
	for _, r := range rows {
		if f.matches(r, v, now) {
			out = append(out, r)
		}
	}
	slices.SortStableFunc(out, func(a, b accountRow) int {
		ka, kb := sortKnown(a, f.Sort, now), sortKnown(b, f.Sort, now)
		c := 0
		switch {
		case ka && !kb:
			c = -1
		case !ka && kb:
			c = 1
		case ka && kb:
			c = sortCompare(a, b, f.Sort, now)
			if f.Dir == fleetDirDesc {
				c = -c
			}
		}
		return cmp.Or(c, strings.Compare(a.Account.ID, b.Account.ID))
	})
	total := len(out)
	pages := fleetPages(total)
	p := min(max(f.Page, 1), pages)
	start := (p - 1) * fleetPageSize
	end := min(start+fleetPageSize, total)
	return out[start:end], total, p
}

// fleetControl starts the id of every control that changes the fleet view:
// the filter bar, its segments, the sortable heads and the pager. The
// answer to such a control names the canonical address of the view, so the
// address bar follows it. The poller has its own id and never matches.
const fleetControl = "fleet-"

// fleetBudgets are the budget health values in the order of the Budget
// health select: quiet, then settling, then the faults.
var fleetBudgets = []string{
	"ready", "alerts-only", "alerts-disabled", "retired",
	"setup-pending", "restricting", "recovering", "retiring",
	"restricted", "error",
}

// fleetOption is one choice of a select in the filter bar.
type fleetOption struct {
	Value, Label string
}

// capital is s with its first letter in upper case.
func capital(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

// statusOptions are the choices of the Status select: open accounts (the
// default), each status, then every account.
func statusOptions() []fleetOption {
	out := []fleetOption{{fleetStatusOpen, "Open"}}
	for _, s := range fleetStatuses {
		out = append(out, fleetOption{string(s), capital(string(s))})
	}
	return append(out, fleetOption{fleetStatusAny, "Any status"})
}

// budgetOptions are the choices of the Budget health select: any (the
// default), each budget health value, then not checked.
func budgetOptions() []fleetOption {
	out := []fleetOption{{"", "Any"}}
	for _, b := range fleetBudgets {
		out = append(out, fleetOption{b, capital(b)})
	}
	return append(out, fleetOption{fleetBudgetUnchecked, "Not checked"})
}

// fleetSegment is one link of the segmented control of an admin: All, Mine
// or Needs attention. It sets the owner and attention filters.
type fleetSegment struct {
	ID, Label, Owner, Attention string
}

var fleetSegments = []fleetSegment{
	{ID: fleetControl + "all", Label: "All"},
	{ID: fleetControl + "mine", Label: "Mine", Owner: fleetOwnerMe},
	{ID: fleetControl + "attention", Label: "Needs attention", Attention: fleetAnyReason},
}

// segmentURL is the address of the view with the owner and attention of
// the segment, on its first page. The search and the other filters stay.
func segmentURL(f fleetView, s fleetSegment) string {
	f.Owner, f.Attention, f.Page = s.Owner, s.Attention, 1
	return fleetURL(f)
}

// segmentCurrent reports whether the view is the view of the segment. An
// owner filter that the segments do not name leaves none current.
func segmentCurrent(f fleetView, s fleetSegment) bool {
	return f.Owner == s.Owner && f.Attention == s.Attention
}

// fleetColumn is a sortable head of the fleet table.
type fleetColumn struct {
	Key, Label string
}

var fleetColumns = []fleetColumn{
	{"name", "Name"}, {"owner", "Owner"}, {"status", "Status"},
	{"account", "Account"}, {fleetSortDue, "Expires"}, {"spend", "Spend / Budget"},
}

// sortState is the aria-sort of a head: ascending or descending for the
// sorted column, and empty for the others.
func sortState(f fleetView, key string) string {
	switch {
	case f.Sort != key:
		return ""
	case f.Dir == fleetDirDesc:
		return "descending"
	}
	return "ascending"
}

// sortURL is the address that a head asks for. The sorted column turns its
// direction, and any other column sorts ascending. Both start at page 1.
func sortURL(f fleetView, key string) string {
	if f.Sort == key && f.Dir == fleetDirAsc {
		f.Dir = fleetDirDesc
	} else {
		f.Dir = fleetDirAsc
	}
	f.Sort, f.Page = key, 1
	return fleetURL(f)
}

// pageURL is the address of page p of the view.
func pageURL(f fleetView, p int) string {
	f.Page = p
	return fleetURL(f)
}

// fleetPages is the number of pages that total rows fill.
func fleetPages(total int) int {
	return max(1, (total+fleetPageSize-1)/fleetPageSize)
}

// fleetShowing is the line of the pager, such as "Showing 51–100 of 112".
func fleetShowing(f fleetView, total int) string {
	first := (f.Page-1)*fleetPageSize + 1
	last := min(f.Page*fleetPageSize, total)
	return "Showing " + strconv.Itoa(first) + "–" + strconv.Itoa(last) + " of " + strconv.Itoa(total)
}

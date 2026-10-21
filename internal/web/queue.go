package web

import (
	"cmp"
	"fmt"
	"hash/fnv"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"playplace/internal/core"
)

// slipView is one slip of the approval queue. The slip shows a request
// that waits, or a request that the decision post of this answer
// approved or denied. It also shows a request that left the queue before
// the post reached it.
type slipView struct {
	Req    core.Request // the record that the slip shows, only the name and the time for a gone slip
	Result *decision    // the result of the post for the request, or nil
	Note   string       // what the slip says about a result that did not decide the request
	Gone   bool         // the request left the queue, so the slip holds only its name and the note
}

// decided reports whether the post of this answer approved or denied the
// request of the slip.
func (s slipView) decided() bool { return s.Result != nil && s.Result.done() }

// state is the data-state of the slip: decided, gone or pending.
func (s slipView) state() string {
	switch {
	case s.decided():
		return "decided"
	case s.Gone:
		return "gone"
	default:
		return "pending"
	}
}

// changed reports whether the post found that the request changed after the
// page showed it.
func (s slipView) changed() bool { return s.Result != nil && s.Result.Kind == decisionChanged }

// wasLifetime is the lifetime that the page showed for a request that
// changed, when the lifetime is not the same now.
func (s slipView) wasLifetime() (string, bool) {
	if !s.changed() || !s.Result.Shown.ok || wholeHours(s.Result.Shown.TTL) == wholeHours(s.Req.TTL) {
		return "", false
	}
	return core.Lifetime(s.Result.Shown.TTL), true
}

// wasBudget is the budget that the page showed for a request that changed,
// when the budget is not the same now.
func (s slipView) wasBudget() (string, bool) {
	if !s.changed() || !s.Result.Shown.ok || s.Result.Shown.BudgetUSD == s.Req.BudgetUSD {
		return "", false
	}
	return core.Dollars(s.Result.Shown.BudgetUSD), true
}

// leftItem is one line of the bulk bar. It is a result of a decision
// post that has no place, or the request of the address that the queue
// does not list.
type leftItem struct{ Name, Text string }

// resultFor returns the first result of the post for the request name.
func (pg page) resultFor(name string) (decision, bool) {
	for _, d := range pg.Results {
		if d.Ref.Name == name {
			return d, true
		}
	}
	return decision{}, false
}

// hasTools reports whether the viewer gets Edit or Withdraw on q.
func hasTools(pg page, q core.Request) bool { return pg.Viewer.Admin || canWithdraw(pg.Viewer, q) }

// slipControls lists the opened parts that the name button of s
// controls: the details, and the tools when the viewer has them.
func slipControls(pg page, s slipView) string {
	ids := "details-" + s.Req.Name
	if hasTools(pg, s.Req) {
		ids += " tools-" + s.Req.Name
	}
	return ids
}

// slips returns the slips of the queue, oldest first, as the queue
// sorts its requests. A request that waits carries the result of the
// post for it, unless the request was gone when the post reached it. A
// request that the post approved or denied keeps a slip with its stamp
// in its place. A request that left the queue keeps a gone slip in its
// place, when the page sent the time of the request. A name that the
// queue lists again belongs to the new request, so every slip keeps a
// unique id. A queue that did not load has no slips.
func (pg page) slips() []slipView {
	if pg.PendingFailed {
		return nil
	}
	listed := map[string]bool{}
	out := make([]slipView, 0, len(pg.Pending)+len(pg.Results))
	for _, q := range pg.Pending {
		listed[q.Name] = true
		s := slipView{Req: q}
		if d, ok := pg.resultFor(q.Name); ok && !d.done() && d.Kind != decisionGone {
			s.Result, s.Note = &d, d.note(q)
		}
		out = append(out, s)
	}
	for _, d := range pg.Results {
		switch {
		case d.done() && d.Req.Name != "" && !listed[d.Req.Name]:
			out = append(out, slipView{Req: d.Req, Result: &d})
		case !d.done() && d.Shown.ok && !listed[d.Ref.Name]:
			gone := core.Request{Name: d.Ref.Name, RequestedAt: d.Shown.At}
			out = append(out, slipView{Req: gone, Result: &d, Note: refusal(core.ErrNotFound, d.Ref.Name), Gone: true})
		}
	}
	slices.SortStableFunc(out, func(a, b slipView) int {
		return cmp.Or(a.Req.RequestedAt.Compare(b.Req.RequestedAt), strings.Compare(a.Req.Name, b.Req.Name))
	})
	return out
}

// left returns the lines of the bulk bar, in the order of the post. Each
// line is a result that did not decide its request and has no place. A
// request that left the queue has no place when the page did not send
// its time. It has none either when a new request holds its name. When
// the queue did not load, each line names its request and its result.
func (pg page) left() []leftItem {
	listed := map[string]bool{}
	for _, q := range pg.Pending {
		listed[q.Name] = true
	}
	var out []leftItem
	for _, d := range pg.Results {
		switch {
		case d.done():
		case pg.PendingFailed:
			out = append(out, leftItem{d.Ref.Name, d.lostNote()})
		case d.Kind == decisionGone && listed[d.Ref.Name], !listed[d.Ref.Name] && !d.Shown.ok:
			out = append(out, leftItem{d.Ref.Name, refusal(core.ErrNotFound, d.Ref.Name)})
		}
	}
	return out
}

// resultText is what the answer shows for the result d: the note of its
// slip, or its line in the bar.
func (pg page) resultText(d decision) string {
	for _, s := range pg.slips() {
		if s.Req.Name == d.Ref.Name && s.Note != "" {
			return s.Note
		}
	}
	for _, it := range pg.left() {
		if it.Name == d.Ref.Name {
			return it.Text
		}
	}
	return d.Why
}

// outcome is the first words of the note of a request that a post did
// not decide.
func outcome(action string) string {
	if action == actionDeny {
		return "Not denied."
	}
	return "Not approved."
}

// note is what the slip of a request says about a result that did not
// decide it. current is the record that the queue lists now.
func (d decision) note(current core.Request) string {
	switch d.Kind {
	case decisionChanged:
		return staleNote(d.Action, d.Ref.Name, d.Shown.changes(current))
	case decisionOwn, decisionNotTried:
		return d.Why
	default:
		return outcome(d.Action) + " " + d.Why
	}
}

// lostNote is the line of a result when the queue did not load. It
// names the request, because no slip does.
func (d decision) lostNote() string {
	switch d.Kind {
	case decisionGone:
		return d.Why
	case decisionChanged:
		return staleNote(d.Action, d.Ref.Name, nil)
	default:
		return d.Ref.Name + ": " + d.note(core.Request{})
	}
}

// staleNote is the note of a request that changed after the page showed
// it. changes names each value that changed, when the page sent what it
// showed.
func staleNote(action, name string, changes []string) string {
	verb := "approve"
	if action == actionDeny {
		verb = "deny"
	}
	what := name + " changed after you loaded the page"
	if len(changes) > 0 {
		what += ": " + strings.Join(changes, ", ")
	}
	return outcome(action) + " " + what + ". Read the new values, then " + verb + " again."
}

// shownValues are the values that a slip showed for one request. Its
// forms post them, so the note of a request that changed names what
// changed, and a request that left keeps its place. They decide nothing.
type shownValues struct {
	Owner     string
	TTL       time.Duration
	BudgetUSD float64
	Purpose   string
	At        time.Time // when the request arrived, which places a gone slip
	ok        bool      // the post held every value
}

// shownField is one value that a slip posts with its decision.
type shownField struct{ Field, Value string }

// shownOf returns the values that the slip of q shows, as its forms post
// them. The lifetime is in whole hours and the budget has every digit,
// as the request version binds them. The time of the request is exact.
func shownOf(q core.Request) []shownField {
	return []shownField{
		{"owner", q.Owner},
		{"ttl", strconv.Itoa(wholeHours(q.TTL))},
		{"budget", strconv.FormatFloat(q.BudgetUSD, 'f', -1, 64)},
		{"purpose", strings.TrimSpace(q.Purpose)},
		{"at", q.RequestedAt.UTC().Format(time.RFC3339Nano)},
	}
}

// shownName is the name of a shown field. A post can carry the fields
// of many requests, so the name ends with the request name.
func shownName(field, request string) string { return "shown-" + field + "-" + request }

// maxShownHours bounds a shown lifetime, so a forged value never wraps
// the duration.
const maxShownHours = 100 * 366 * 24

// readShown reads the shown fields of the request name from a post. A
// post without them, or with a value that does not parse, gives values
// that are not ok. The note then names no change, and a request that
// left gets a bar line in place of a gone slip.
func readShown(form url.Values, name string) shownValues {
	hours, err := strconv.Atoi(form.Get(shownName("ttl", name)))
	if err != nil || hours <= 0 || hours > maxShownHours {
		return shownValues{}
	}
	budget, err := strconv.ParseFloat(form.Get(shownName("budget", name)), 64)
	if err != nil || math.IsNaN(budget) || math.IsInf(budget, 0) || budget <= 0 {
		return shownValues{}
	}
	at, err := time.Parse(time.RFC3339Nano, form.Get(shownName("at", name)))
	if err != nil {
		return shownValues{}
	}
	owner, purpose := form[shownName("owner", name)], form[shownName("purpose", name)]
	if len(owner) != 1 || len(purpose) != 1 {
		return shownValues{}
	}
	return shownValues{Owner: owner[0], TTL: time.Duration(hours) * time.Hour, BudgetUSD: budget, Purpose: purpose[0], At: at, ok: true}
}

// changes names each value that differs between what the page showed
// and now, in the order of the slip: owner, lifetime, budget, then
// purpose. A purpose can be long, so the note says only that it changed.
func (v shownValues) changes(now core.Request) []string {
	if !v.ok {
		return nil
	}
	var out []string
	if v.Owner != now.Owner {
		out = append(out, "owner "+v.Owner+" to "+now.Owner)
	}
	if wholeHours(v.TTL) != wholeHours(now.TTL) {
		out = append(out, "lifetime "+core.Lifetime(v.TTL)+" to "+core.Lifetime(now.TTL))
	}
	if v.BudgetUSD != now.BudgetUSD {
		out = append(out, "budget "+core.Dollars(v.BudgetUSD)+" to "+core.Dollars(now.BudgetUSD))
	}
	if v.Purpose != strings.TrimSpace(now.Purpose) {
		out = append(out, "the purpose")
	}
	return out
}

// wholeHours is a lifetime in whole hours, rounded up, as the request
// version binds it.
func wholeHours(d time.Duration) int { return int(math.Ceil(d.Hours())) }

// ownerHolds lists, for each owner in lower case, the names of the open
// accounts and the pending requests of that owner, sorted. The
// per-owner limit counts the same things when a request arrives.
func ownerHolds(accounts []*core.Account) map[string][]string {
	out := map[string][]string{}
	for _, a := range accounts {
		if a.Status.IsOpen() {
			owner := strings.ToLower(a.Owner)
			out[owner] = append(out[owner], a.Name)
		}
	}
	for _, names := range out {
		slices.Sort(names)
	}
	return out
}

// holdsUnknown is the text of a per-owner fact when the account list did
// not load.
const holdsUnknown = "Unknown, because the account list is unavailable."

// ownerStanding says how the owner of q stands against the per-owner
// limit, with q counted. Approving q does not change the count, because
// a pending request already counts.
func ownerStanding(pg page, q core.Request) string {
	limit := pg.Limits.MaxPerOwner
	switch {
	case limit <= 0:
		return "No per-owner limit applies."
	case pg.HoldsFailed:
		return holdsUnknown
	}
	names := pg.Holds[strings.ToLower(q.Owner)]
	held := len(names)
	if !slices.Contains(names, q.Name) {
		held++
	}
	switch {
	case held < limit:
		return fmt.Sprintf("Within the per-owner limit of %d.", limit)
	case held == limit:
		return fmt.Sprintf("At the per-owner limit of %d.", limit)
	default:
		return fmt.Sprintf("Over the per-owner limit of %d.", limit)
	}
}

// otherHoldings names the other open accounts and requests of the owner
// of q, for an admin.
func otherHoldings(pg page, q core.Request) string {
	if pg.HoldsFailed {
		return holdsUnknown
	}
	var others []string
	for _, name := range pg.Holds[strings.ToLower(q.Owner)] {
		if name != q.Name {
			others = append(others, name)
		}
	}
	if len(others) == 0 {
		return "Nothing else."
	}
	return strings.Join(others, ", ")
}

// limitsCheck says how q stands against the ceilings of lifetime and
// budget. A ceiling of 0 means no limit.
func limitsCheck(q core.Request, cfg core.Config) string {
	var over, ceilings []string
	if cfg.MaxTTL > 0 {
		ceilings = append(ceilings, core.Lifetime(cfg.MaxTTL))
		if q.TTL > cfg.MaxTTL {
			over = append(over, core.Lifetime(q.TTL)+" is over the ceiling of "+core.Lifetime(cfg.MaxTTL))
		}
	}
	if cfg.MaxBudgetUSD > 0 {
		ceilings = append(ceilings, core.Dollars(cfg.MaxBudgetUSD)+" a month")
		if q.BudgetUSD > cfg.MaxBudgetUSD {
			over = append(over, core.Dollars(q.BudgetUSD)+" a month is over the ceiling of "+core.Dollars(cfg.MaxBudgetUSD)+" a month")
		}
	}
	if len(over) > 0 {
		text := strings.Join(over, ", and ") + "."
		if q.OverrideLimits {
			return "An admin overrode the limits. " + text
		}
		return text + " Approval checks the ceilings again and can refuse this request."
	}
	switch len(ceilings) {
	case 0:
		return "No ceilings apply."
	case 1:
		return "Within the ceiling of " + ceilings[0] + "."
	default:
		return "Within the ceilings of " + ceilings[0] + " and " + ceilings[1] + "."
	}
}

// metaLine is the second line of a slip: the owner, who asked, how, and
// when, in UTC.
func metaLine(v Viewer, q core.Request) string {
	at := q.RequestedAt.UTC().Format("Jan 2, 15:04 UTC")
	if strings.EqualFold(q.RequestedBy, q.Owner) {
		return "Owner and requester " + q.Owner + ", via " + q.Via + ", " + at
	}
	by := q.RequestedBy
	if strings.EqualFold(by, v.Email) {
		by = "you"
	}
	return "Owner " + q.Owner + ", requested by " + by + " via " + q.Via + ", " + at
}

// countLine is the head line of a queue panel: how many requests wait
// for what, then when playplace drops them.
func countLine(pg page, what string) string {
	var drop string
	if pg.Limits.RequestTTL > 0 {
		drop = "playplace drops requests after " + core.Lifetime(pg.Limits.RequestTTL) + "."
	}
	var count string
	switch n := len(pg.Pending); {
	case pg.PendingFailed || n == 0:
	case n == 1:
		count = "1 request waiting for " + what + "."
	default:
		count = fmt.Sprintf("%d requests waiting for %s, oldest first.", n, what)
	}
	return strings.TrimSpace(count + " " + drop)
}

// deskLine is the head line of the approval queue.
func deskLine(pg page) string { return countLine(pg, "a decision") }

// ownLine is the head line of Your requests.
func ownLine(pg page) string { return countLine(pg, "an approver") }

// versionLabel is the request version as a slip shows it: the first 6
// characters of its digest. The forms carry the whole version.
func versionLabel(v string) string {
	d := v[strings.LastIndexByte(v, '.')+1:]
	if len(d) > 6 {
		d = d[:6]
	}
	return d
}

// stampTurn is the turn class of the stamp of key, a request name or an
// account ID. It comes from a hash of the key, so a stamp keeps its turn
// when rows move.
func stampTurn(key string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	switch h.Sum32() % 3 {
	case 0:
		return "turn-1"
	case 1:
		return "turn-2"
	default:
		return "turn-3"
	}
}

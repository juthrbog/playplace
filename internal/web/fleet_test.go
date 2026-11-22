package web

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

var (
	fleetAdmin    = Viewer{Email: "ops@example.com", Admin: true, Approver: true}
	fleetEngineer = Viewer{Email: "anna@example.com"}
)

// Each parameter of the fleet view takes the values it knows, and anything
// else falls back to the default. The server never refuses a view.
func TestParseFleetView(t *testing.T) {
	def := defaultFleetView()
	with := func(change func(*fleetView)) fleetView {
		f := defaultFleetView()
		change(&f)
		return f
	}
	long := strings.Repeat("é", fleetQueryMax+20)
	for _, c := range []struct {
		name  string
		query string
		v     Viewer
		want  fleetView
	}{
		{"nothing", "", fleetAdmin, def},
		{"search", "q=+annas+", fleetAdmin, with(func(f *fleetView) { f.Q = "annas" })},
		{"search keeps its case", "q=Anna", fleetAdmin, with(func(f *fleetView) { f.Q = "Anna" })},
		{"long search is cut", "q=" + url.QueryEscape(long), fleetAdmin, with(func(f *fleetView) { f.Q = strings.Repeat("é", fleetQueryMax) })},
		{"blank search", "q=+++", fleetAdmin, def},
		{"status", "status=active", fleetAdmin, with(func(f *fleetView) { f.Status = "active" })},
		{"status closed", "status=closed", fleetAdmin, with(func(f *fleetView) { f.Status = "closed" })},
		{"status in capitals", "status=FAILED", fleetAdmin, with(func(f *fleetView) { f.Status = "failed" })},
		{"status any", "status=any", fleetAdmin, with(func(f *fleetView) { f.Status = "any" })},
		{"status unknown", "status=gone", fleetAdmin, def},
		{"old all=1", "all=1", fleetAdmin, with(func(f *fleetView) { f.Status = "any" })},
		{"old all=1 under a status", "all=1&status=active", fleetAdmin, with(func(f *fleetView) { f.Status = "active" })},
		{"old all=1 under a bad status", "all=1&status=gone", fleetAdmin, def},
		{"old all with another value", "all=yes", fleetAdmin, def},
		{"budget", "budget=restricted", fleetAdmin, with(func(f *fleetView) { f.Budget = "restricted" })},
		{"budget alerts-only", "budget=alerts-only", fleetAdmin, with(func(f *fleetView) { f.Budget = "alerts-only" })},
		{"budget unchecked", "budget=unchecked", fleetAdmin, with(func(f *fleetView) { f.Budget = "unchecked" })},
		{"budget unknown", "budget=over", fleetAdmin, def},
		{"owner me", "owner=me", fleetAdmin, with(func(f *fleetView) { f.Owner = "me" })},
		{"owner email in lower case", "owner=Sam%40Example.com", fleetAdmin, with(func(f *fleetView) { f.Owner = "sam@example.com" })},
		{"owner with a space", "owner=sam+example", fleetAdmin, def},
		{"owner with a control byte", "owner=sam%07", fleetAdmin, def},
		{"owner too long", "owner=" + strings.Repeat("a", fleetOwnerMax+1), fleetAdmin, def},
		{"owner for an engineer", "owner=sam%40example.com", fleetEngineer, def},
		{"attention any", "attention=any", fleetAdmin, with(func(f *fleetView) { f.Attention = "any" })},
		{"attention of core", "attention=closure-overdue", fleetAdmin, with(func(f *fleetView) { f.Attention = "closure-overdue" })},
		{"attention over budget", "attention=over-budget", fleetAdmin, with(func(f *fleetView) { f.Attention = "over-budget" })},
		{"attention unknown", "attention=soon", fleetAdmin, def},
		{"attention for an engineer", "attention=any", fleetEngineer, def},
		{"sort", "sort=spend", fleetAdmin, with(func(f *fleetView) { f.Sort = "spend" })},
		{"sort unknown", "sort=created", fleetAdmin, def},
		{"dir", "dir=desc", fleetAdmin, with(func(f *fleetView) { f.Dir = "desc" })},
		{"dir unknown", "dir=down", fleetAdmin, def},
		{"page", "page=3", fleetAdmin, with(func(f *fleetView) { f.Page = 3 })},
		{"page zero", "page=0", fleetAdmin, def},
		{"page below zero", "page=-2", fleetAdmin, def},
		{"page not a number", "page=two", fleetAdmin, def},
		{"page with a fraction", "page=2.5", fleetAdmin, def},
		{"page too large for an int", "page=99999999999999999999999", fleetAdmin, def},
		{"engineer keeps the rest", "q=box&status=active&budget=ready&sort=name&dir=desc&page=2&owner=me&attention=any", fleetEngineer,
			fleetView{Q: "box", Status: "active", Budget: "ready", Sort: "name", Dir: "desc", Page: 2}},
	} {
		t.Run(c.name, func(t *testing.T) {
			q, err := url.ParseQuery(c.query)
			if err != nil {
				t.Fatal(err)
			}
			if got := parseFleetView(q.Get, c.v); got != c.want {
				t.Errorf("parseFleetView(%q) = %+v, want %+v", c.query, got, c.want)
			}
		})
	}
}

// The canonical address has its keys in a fixed order and leaves out the
// defaults. It reads back as the same view.
func TestFleetURL(t *testing.T) {
	for _, c := range []struct {
		name string
		f    fleetView
		want string
	}{
		{"default", defaultFleetView(), "/accounts"},
		{"page 1", fleetView{Status: "open", Sort: "due", Dir: "asc", Page: 1}, "/accounts"},
		{"every key", fleetView{Q: "a&b c", Status: "any", Budget: "error", Owner: "sam@example.com", Attention: "over-budget", Sort: "spend", Dir: "desc", Page: 4},
			"/accounts?q=a%26b+c&status=any&budget=error&owner=sam%40example.com&attention=over-budget&sort=spend&dir=desc&page=4"},
		{"one key", fleetView{Status: "open", Sort: "name", Dir: "asc", Page: 1}, "/accounts?sort=name"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := fleetURL(c.f)
			if got != c.want {
				t.Errorf("fleetURL = %q, want %q", got, c.want)
			}
			u, err := url.Parse(got)
			if err != nil {
				t.Fatal(err)
			}
			if back := parseFleetView(u.Query().Get, fleetAdmin); back != c.f {
				t.Errorf("the address reads back as %+v, want %+v", back, c.f)
			}
		})
	}
}

// fleetNow is the time of the fleet tests.
var fleetNow = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

// fleetRow is a row for the fleet tests: active, owned by anna, due in a day,
// with known spend of 10 of 50.
func fleetRow(id string, change func(*accountRow)) accountRow {
	r := accountRow{
		Account: &core.Account{ID: id, ProviderID: id, Name: "box-" + id, Owner: "anna@example.com", Status: core.StatusActive,
			BudgetUSD: 50, ExpiresAt: fleetNow.Add(24 * time.Hour)},
		Spend: 10, SpendKnown: true,
	}
	if change != nil {
		change(&r)
	}
	return r
}

func rowIDs(rows []accountRow) []string {
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.Account.ID)
	}
	return ids
}

// Each filter keeps the rows it names. Owner and attention are for admins
// only, also when a view that holds them reaches the filter.
func TestFleetFilters(t *testing.T) {
	rows := []accountRow{
		fleetRow("111111111111", func(r *accountRow) { r.Account.Name = "annas-box" }),
		fleetRow("222222222222", func(r *accountRow) { r.Account.Name = "sams-box"; r.Account.Owner = "Sam@Example.com" }),
		fleetRow("333333333333", func(r *accountRow) { r.Account.Status = core.StatusClosed }),
		fleetRow("444444444444", func(r *accountRow) { r.Account.Status = core.StatusExpiring; r.Account.BudgetHealth = "ready" }),
		fleetRow("555555555555", func(r *accountRow) { r.Account.BudgetHealth = "restricted" }),
		fleetRow("666666666666", func(r *accountRow) { r.Spend = 80 }),
		fleetRow("777777777777", func(r *accountRow) { r.Spend, r.SpendKnown = 0, false; r.Account.BudgetUSD = 1 }),
		fleetRow("888888888888", func(r *accountRow) { r.Account.Status = core.StatusClosed; r.Spend = 90 }),
		fleetRow("req:pending-box", func(r *accountRow) {
			r.Account.ProviderID, r.Account.Status, r.Account.Name, r.Account.TTL = "", core.StatusPending, "pending-box", 48*time.Hour
		}),
	}
	view := func(change func(*fleetView)) fleetView {
		f := defaultFleetView()
		change(&f)
		return f
	}
	open := []string{"111111111111", "222222222222", "444444444444", "555555555555", "666666666666", "777777777777", "req:pending-box"}
	for _, c := range []struct {
		name string
		f    fleetView
		v    Viewer
		want []string
	}{
		{"open", defaultFleetView(), fleetAdmin, open},
		{"any", view(func(f *fleetView) { f.Status = "any" }), fleetAdmin,
			[]string{"111111111111", "222222222222", "333333333333", "444444444444", "555555555555", "666666666666", "777777777777", "888888888888", "req:pending-box"}},
		{"one status", view(func(f *fleetView) { f.Status = "closed" }), fleetAdmin, []string{"333333333333", "888888888888"}},
		{"pending", view(func(f *fleetView) { f.Status = "pending" }), fleetAdmin, []string{"req:pending-box"}},
		{"search by name", view(func(f *fleetView) { f.Q = "SAMS" }), fleetAdmin, []string{"222222222222"}},
		{"search by owner", view(func(f *fleetView) { f.Q = "sam@example" }), fleetAdmin, []string{"222222222222"}},
		{"search by account id", view(func(f *fleetView) { f.Q = "5555" }), fleetAdmin, []string{"555555555555"}},
		{"search with no match", view(func(f *fleetView) { f.Q = "nobody" }), fleetAdmin, nil},
		{"budget health", view(func(f *fleetView) { f.Budget = "ready" }), fleetAdmin, []string{"444444444444"}},
		{"budget unchecked", view(func(f *fleetView) { f.Budget = "unchecked" }), fleetAdmin,
			[]string{"111111111111", "222222222222", "666666666666", "777777777777", "req:pending-box"}},
		{"owner by email without case", view(func(f *fleetView) { f.Owner = "sam@example.com" }), fleetAdmin, []string{"222222222222"}},
		{"owner me", view(func(f *fleetView) { f.Owner = "me" }), Viewer{Email: "SAM@example.com", Admin: true}, []string{"222222222222"}},
		{"owner for an engineer", view(func(f *fleetView) { f.Owner = "sam@example.com" }), fleetEngineer, open},
		{"attention any", view(func(f *fleetView) { f.Attention = "any" }), fleetAdmin, []string{"444444444444", "555555555555", "666666666666"}},
		{"attention of core", view(func(f *fleetView) { f.Attention = "restricted" }), fleetAdmin, []string{"555555555555"}},
		{"over budget needs known spend on an open account", view(func(f *fleetView) { f.Attention = "over-budget"; f.Status = "any" }), fleetAdmin,
			[]string{"666666666666"}},
		{"attention for an engineer", view(func(f *fleetView) { f.Attention = "restricted" }), fleetEngineer, open},
		{"filters combine", view(func(f *fleetView) { f.Q = "box-"; f.Budget = "restricted" }), fleetAdmin, []string{"555555555555"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.f.Sort = "account" // a plain order, so the test reads the set
			got, total, _ := c.f.apply(rows, c.v, fleetNow)
			want := slices.Clone(c.want)
			slices.SortFunc(want, func(a, b string) int {
				// rows with no account id sort last
				return compareLast(a, b)
			})
			if !slices.Equal(rowIDs(got), want) || total != len(want) {
				t.Errorf("rows = %v (total %d), want %v", rowIDs(got), total, want)
			}
		})
	}
}

// compareLast orders ids as the account sort does in the filter test: the
// pending row, which has no account id, goes last.
func compareLast(a, b string) int {
	pa, pb := strings.HasPrefix(a, "req:"), strings.HasPrefix(b, "req:")
	switch {
	case pa && !pb:
		return 1
	case !pa && pb:
		return -1
	}
	return strings.Compare(a, b)
}

// Every sort key orders the rows both ways. The id breaks ties. A row with
// no due date, no known spend or no account id sorts last in both
// directions. A pending row is due its lifetime from now.
func TestFleetSort(t *testing.T) {
	day := 24 * time.Hour
	rows := []accountRow{
		fleetRow("a3", func(r *accountRow) {
			r.Account.Name, r.Account.Owner, r.Account.ProviderID, r.Account.Status = "cedar", "Bob@example.com", "300000000000", core.StatusExpiring
			r.Account.ExpiresAt, r.Spend = fleetNow.Add(3*day), 30
		}),
		fleetRow("a1", func(r *accountRow) {
			r.Account.Name, r.Account.Owner, r.Account.ProviderID, r.Account.Status = "alder", "carol@example.com", "100000000000", core.StatusActive
			r.Account.ExpiresAt, r.Spend = fleetNow.Add(1*day), 10
		}),
		// same due date and spend as a1, so the id breaks the tie
		fleetRow("a2", func(r *accountRow) {
			r.Account.Name, r.Account.Owner, r.Account.ProviderID, r.Account.Status = "Birch", "alice@example.com", "200000000000", core.StatusActive
			r.Account.ExpiresAt, r.Spend = fleetNow.Add(1*day), 10
		}),
		// damaged expiry tag and unknown spend
		fleetRow("a4", func(r *accountRow) {
			r.Account.Name, r.Account.Owner, r.Account.ProviderID, r.Account.Status = "dogwood", "dave@example.com", "400000000000", core.StatusClosing
			r.Account.ExpiresAt, r.Spend, r.SpendKnown = time.Time{}, 0, false
		}),
		// pending: due in two days from now, no account id, no spend yet
		fleetRow("req:elm", func(r *accountRow) {
			r.Account.Name, r.Account.Owner, r.Account.ProviderID, r.Account.Status = "elm", "eve@example.com", "", core.StatusPending
			r.Account.ExpiresAt, r.Account.TTL, r.Spend = fleetNow.Add(-day), 2*day, 0
		}),
		// failed: due no more
		fleetRow("req:fir", func(r *accountRow) {
			r.Account.Name, r.Account.Owner, r.Account.ProviderID, r.Account.Status = "fir", "frank@example.com", "", core.StatusFailed
			r.Spend = 5
		}),
	}
	for _, c := range []struct {
		sort, dir string
		want      []string
	}{
		{"due", "asc", []string{"a1", "a2", "req:elm", "a3", "a4", "req:fir"}},
		{"due", "desc", []string{"a3", "req:elm", "a1", "a2", "a4", "req:fir"}},
		{"name", "asc", []string{"a1", "a2", "a3", "a4", "req:elm", "req:fir"}},
		{"name", "desc", []string{"req:fir", "req:elm", "a4", "a3", "a2", "a1"}},
		{"owner", "asc", []string{"a2", "a3", "a1", "a4", "req:elm", "req:fir"}},
		{"owner", "desc", []string{"req:fir", "req:elm", "a4", "a1", "a3", "a2"}},
		{"account", "asc", []string{"a1", "a2", "a3", "a4", "req:elm", "req:fir"}},
		{"account", "desc", []string{"a4", "a3", "a2", "a1", "req:elm", "req:fir"}},
		{"status", "asc", []string{"req:elm", "a1", "a2", "a3", "a4", "req:fir"}},
		{"status", "desc", []string{"req:fir", "a4", "a3", "a1", "a2", "req:elm"}},
		{"spend", "asc", []string{"req:elm", "req:fir", "a1", "a2", "a3", "a4"}},
		{"spend", "desc", []string{"a3", "a1", "a2", "req:fir", "req:elm", "a4"}},
	} {
		t.Run(c.sort+" "+c.dir, func(t *testing.T) {
			f := defaultFleetView()
			f.Status, f.Sort, f.Dir = fleetStatusAny, c.sort, c.dir
			// The input order must not matter.
			for _, in := range [][]accountRow{rows, reversed(rows)} {
				got, _, _ := f.apply(in, fleetAdmin, fleetNow)
				if !slices.Equal(rowIDs(got), c.want) {
					t.Errorf("order = %v, want %v", rowIDs(got), c.want)
				}
			}
		})
	}
}

func reversed(rows []accountRow) []accountRow {
	out := slices.Clone(rows)
	slices.Reverse(out)
	return out
}

// A page holds 50 rows. A page past the end shows the last page, and an
// empty fleet is page 1.
func TestFleetPages(t *testing.T) {
	var rows []accountRow
	for i := range 120 {
		rows = append(rows, fleetRow(fmt.Sprintf("%03d", i), nil))
	}
	for _, c := range []struct {
		rows               []accountRow
		page               int
		wantPage, wantLen  int
		wantFirst, wantTot string
	}{
		{rows, 1, 1, 50, "000", "120"},
		{rows, 2, 2, 50, "050", "120"},
		{rows, 3, 3, 20, "100", "120"},
		{rows, 9, 3, 20, "100", "120"},
		{rows[:50], 2, 1, 50, "000", "50"},
		{nil, 4, 1, 0, "", "0"},
	} {
		f := defaultFleetView()
		f.Sort, f.Page = "account", c.page
		got, total, p := f.apply(c.rows, fleetAdmin, fleetNow)
		first := ""
		if len(got) > 0 {
			first = got[0].Account.ID
		}
		if p != c.wantPage || len(got) != c.wantLen || first != c.wantFirst || fmt.Sprint(total) != c.wantTot {
			t.Errorf("page %d of %d rows = page %d, %d rows from %q, total %d; want page %d, %d rows from %q, total %s",
				c.page, len(c.rows), p, len(got), first, total, c.wantPage, c.wantLen, c.wantFirst, c.wantTot)
		}
	}
}

// Over budget joins the kinds of core, after them, and needs known spend
// on an account that is not closed.
func TestRowAttention(t *testing.T) {
	r := fleetRow("1", func(r *accountRow) { r.Spend = 60; r.Account.Status = core.StatusExpiring })
	if got := rowAttention(r, fleetNow); !slices.Equal(got, []string{"expiring", "over-budget"}) {
		t.Errorf("attention = %v, want expiring then over-budget", got)
	}
	for name, change := range map[string]func(*accountRow){
		"unknown spend": func(r *accountRow) { r.Spend, r.SpendKnown = 60, false },
		"closed":        func(r *accountRow) { r.Spend = 60; r.Account.Status = core.StatusClosed },
		"no budget":     func(r *accountRow) { r.Spend = 60; r.Account.BudgetUSD = 0 },
		"at budget":     func(r *accountRow) { r.Spend = 50 },
	} {
		if got := rowAttention(fleetRow("1", change), fleetNow); len(got) != 0 {
			t.Errorf("%s: attention = %v, want none", name, got)
		}
	}
}

// The accounts page reads the view from its address and from the body of
// an action, counts the rows that pass the filters and polls the canonical
// address. The home page keeps its plain list.
func TestAccountsPageFleetView(t *testing.T) {
	ts, _ := newServer(t)
	// A search on the page.
	_, body := do(t, ts, "GET", "/accounts?q=+ANNA&page=9", "ops@example.com", true, nil, false)
	if !strings.Contains(body, "annas-box") || strings.Contains(body, "sams-box") {
		t.Errorf("the search does not keep only annas-box:\n%s", body)
	}
	// A page past the end is the last page, so the address drops it.
	if !strings.Contains(body, `hx-get="/accounts?q=ANNA" hx-trigger="every 15s, poll-now"`) || !strings.Contains(body, `<span class="muted">(1)</span>`) {
		t.Errorf("the page does not poll the canonical address with the filtered count:\n%s", body)
	}
	// The poll from the page reads the same view.
	_, body = fromPage(t, ts, "GET", "/accounts?q=anna", "/accounts?q=anna", "ops@example.com", true, true, nil, "")
	if !strings.Contains(body, "annas-box") || strings.Contains(body, "sams-box") {
		t.Errorf("the poll does not keep the search:\n%s", body)
	}
	// An action reads the view from its body.
	_, body = fromPage(t, ts, "POST", "/accounts/222222222222/extend", "/accounts?q=sams", "ops@example.com", true, true,
		url.Values{"by": {"1"}, "q": {"sams"}}, "")
	if !strings.Contains(body, "sams-box") || strings.Contains(body, "annas-box") || !strings.Contains(body, `hx-get="/accounts?q=sams"`) {
		t.Errorf("the action answer does not keep the search:\n%s", body)
	}
	// No match says so and offers a way back.
	_, body = do(t, ts, "GET", "/accounts?status=failed", "ops@example.com", true, nil, false)
	if !strings.Contains(body, `<p class="empty">No accounts match these filters. <a href="/accounts">Clear filters</a></p>`) {
		t.Errorf("an empty filtered view does not say that nothing matches:\n%s", body)
	}
	// An engineer cannot use the owner filter to look at the fleet, and the
	// address drops it.
	_, body = do(t, ts, "GET", "/accounts?owner=sam%40example.com&attention=any", "anna@example.com", false, nil, false)
	if !strings.Contains(body, "annas-box") || strings.Contains(body, "sams-box") || !strings.Contains(body, `hx-get="/accounts" hx-trigger`) {
		t.Errorf("an engineer's owner filter is not ignored:\n%s", body)
	}
	// An admin can.
	_, body = do(t, ts, "GET", "/accounts?owner=SAM%40example.com", "ops@example.com", true, nil, false)
	if !strings.Contains(body, "sams-box") || strings.Contains(body, "annas-box") || !strings.Contains(body, `hx-get="/accounts?owner=sam%40example.com"`) {
		t.Errorf("an admin's owner filter does not work:\n%s", body)
	}
	// The home page and its poll ignore the view.
	_, body = do(t, ts, "GET", "/?q=anna&status=failed", "ops@example.com", true, nil, false)
	if !strings.Contains(body, "annas-box") || !strings.Contains(body, "sams-box") || !strings.Contains(body, `hx-get="/accounts" hx-trigger`) {
		t.Errorf("the home page reads the fleet view:\n%s", body)
	}
	_, body = fromPage(t, ts, "GET", "/accounts?q=anna", "/", "ops@example.com", true, true, nil, "")
	if !strings.Contains(body, "annas-box") || !strings.Contains(body, "sams-box") {
		t.Errorf("the home poll reads the fleet view:\n%s", body)
	}
}

// The home page lists the open accounts by due date, the soonest first,
// whatever the address says. The accounts page does too by default.
func TestHomeSortsByDueDate(t *testing.T) {
	ts, prov := newServer(t)
	prov.Seed(core.RemoteAccount{ProviderID: "333333333333", Name: "new-box", Email: "n@example.com", JoinedAt: t0.Add(time.Hour),
		Tags: map[string]string{core.TagManaged: "true", core.TagOwner: "anna@example.com", core.TagExpires: t0.Add(900 * time.Hour).Format(time.RFC3339), core.TagBudget: "50"}})
	for _, path := range []string{"/?sort=name&dir=desc", "/accounts"} {
		_, body := do(t, ts, "GET", path, "ops@example.com", true, nil, false)
		if i, j := strings.Index(body, ">new-box<"), strings.Index(body, ">annas-box<"); i < 0 || j < 0 || i < j {
			t.Errorf("%s does not list the soonest due date first:\n%s", path, body)
		}
	}
}

// Spend that did not load reads "unavailable" with no meter, never $0.00,
// on both pages. Unknown spend is never over budget.
func TestUnknownSpendIsUnavailable(t *testing.T) {
	fc := &flakyCosts{}
	ts, _ := newServerOn(t, func(p *fake.Provider) core.Provider { fc.Provider = p; return fc })
	fc.fail.Store(true)
	for _, path := range []string{"/", "/accounts", "/accounts?attention=over-budget"} {
		_, body := do(t, ts, "GET", path, "ops@example.com", true, nil, false)
		if strings.Contains(path, "over-budget") {
			if strings.Contains(body, "annas-box") {
				t.Errorf("%s: unknown spend counts as over budget", path)
			}
			continue
		}
		if !strings.Contains(body, `<div class="spend"><span class="muted">unavailable</span> / $50.00</div>`) {
			t.Errorf("%s: unknown spend does not read unavailable:\n%s", path, body)
		}
		if strings.Contains(body, "$0.00") || strings.Contains(body, "meter-line") {
			t.Errorf("%s: unknown spend shows a number or a meter:\n%s", path, body)
		}
	}
}

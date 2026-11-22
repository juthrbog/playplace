package web

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
)

// fromControl is fromPage for a request that a control with the id
// trigger sent, as htmx names it in HX-Trigger. It returns the answer and
// the HX-Replace-Url header.
func fromControl(t *testing.T, ts *httptest.Server, method, path, from, trigger string, form url.Values) (string, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, ts.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("X-User", "ops@example.com")
	req.Header.Set("X-Admin", "1")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Current-URL", ts.URL+from)
	if trigger != "" {
		req.Header.Set("HX-Trigger", trigger)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), res.Header.Get("HX-Replace-Url")
}

// fleetForm is the filter bar form of a page.
func fleetForm(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, `<form id="fleet-view"`)
	if start < 0 {
		t.Fatalf("the page has no filter bar:\n%s", body)
	}
	form, _, _ := strings.Cut(body[start:], "</form>")
	return form
}

// The filter bar of an admin: the search with its debounce and Enter, the
// segments, the two selects and the hidden parts of the view. Each request
// from the bar replaces the address. The page number sits outside the form.
func TestFilterBarOfAnAdmin(t *testing.T) {
	ts, _ := newServer(t)
	_, body := do(t, ts, "GET", "/accounts?q=box&status=any&budget=unchecked&attention=any&sort=name&dir=desc", "ops@example.com", true, nil, false)
	form := fleetForm(t, body)
	tag, _, _ := strings.Cut(form, ">")
	for _, want := range []string{
		`action="/accounts" method="get"`,
		`role="search" aria-label="Find accounts"`,
		`hx-get="/accounts" hx-target="#accounts" hx-swap="morph" hx-replace-url="true"`,
		`hx-trigger="submit, input changed delay:400ms target:#fleet-q, change target:select"`,
		`hx-sync="this:queue last"`,
	} {
		if !strings.Contains(tag, want) {
			t.Errorf("the form tag lacks %s: %s", want, tag)
		}
	}
	for _, want := range []string{
		`<label class="vh" for="fleet-q">Search accounts</label>`,
		`<input id="fleet-q" type="search" name="q" value="box" maxlength="100"`,
		`<span class="bar-pair"><label class="bar-field" for="fleet-status">Status</label> <select id="fleet-status" name="status">`,
		`<option value="open">Open</option>`,
		`<option value="any" selected>Any status</option>`,
		`</select></span> <span class="bar-pair"><label class="bar-field" for="fleet-budget">Budget health</label> <select id="fleet-budget" name="budget">`,
		`<option value="">Any</option>`,
		`<option value="unchecked" selected>Not checked</option>`,
		`<div class="segmented" role="group" aria-label="Accounts to show">`,
		`<a id="fleet-all" href="/accounts?q=box&amp;status=any&amp;budget=unchecked&amp;sort=name&amp;dir=desc"`,
		`<a id="fleet-mine" href="/accounts?q=box&amp;status=any&amp;budget=unchecked&amp;owner=me&amp;sort=name&amp;dir=desc"`,
		`hx-get="/accounts?q=box&amp;status=any&amp;budget=unchecked&amp;attention=any&amp;sort=name&amp;dir=desc" hx-target="#accounts" hx-swap="morph" hx-replace-url="true" hx-sync="#fleet-view:replace" data-fleet-nav aria-current="true">Needs attention</a>`,
		`<input type="hidden" name="owner" value="">`,
		`<input type="hidden" name="attention" value="any">`,
		`<input type="hidden" name="sort" value="name">`,
		`<input type="hidden" name="dir" value="desc">`,
	} {
		if !strings.Contains(form, want) {
			t.Errorf("the filter bar lacks %s", want)
		}
	}
	if n := strings.Count(form, `aria-current`); n != 1 {
		t.Errorf("%d segments are current, want 1", n)
	}
	if strings.Contains(form, `name="page"`) {
		t.Error("the form holds the page, so a filter change would keep it")
	}
	if !regexp.MustCompile(`</form>\s*<input type="hidden" id="fleet-page" name="page" value="1">`).MatchString(body) {
		t.Error("the page number does not sit right after the form")
	}
	// The status filter takes the place of Show closed on this page.
	if strings.Contains(body, `id="show-closed"`) || strings.Contains(body, "Search and filter all accounts") {
		t.Error("the accounts page shows the parts of the home page")
	}
	// Every id on the page is unique, so idiomorph pairs the controls.
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`\sid="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		if seen[m[1]] {
			t.Errorf("id %q repeats on the accounts page", m[1])
		}
		seen[m[1]] = true
	}
	if t.Failed() {
		t.Logf("form:\n%s", form)
	}
}

// Anyone but an admin sees only their own accounts, so they get no
// segments and no owner or attention fields.
func TestFilterBarOfAnEngineer(t *testing.T) {
	ts, _ := newServer(t)
	_, body := do(t, ts, "GET", "/accounts", "anna@example.com", false, nil, false)
	form := fleetForm(t, body)
	for _, no := range []string{`class="segmented"`, `name="owner"`, `name="attention"`, `aria-current`} {
		if strings.Contains(form, no) {
			t.Errorf("the filter bar of an engineer holds %s:\n%s", no, form)
		}
	}
	for _, want := range []string{`id="fleet-q"`, `id="fleet-status"`, `id="fleet-budget"`, `<option value="open" selected>Open</option>`, `<input type="hidden" name="sort" value="due">`} {
		if !strings.Contains(form, want) {
			t.Errorf("the filter bar of an engineer lacks %s:\n%s", want, form)
		}
	}
}

// The poller is the section, and the bar, the heads and the pager sit
// inside it. hx-replace-url is inherited, so it must not be on the section
// or on anything around it, or every poll would replace the address.
func TestThePollNeverReplacesTheAddress(t *testing.T) {
	ts, _ := newServer(t)
	_, body := do(t, ts, "GET", "/accounts?sort=name", "ops@example.com", true, nil, false)
	start := strings.Index(body, `<section id="accounts"`)
	if start < 0 {
		t.Fatalf("no accounts section:\n%s", body)
	}
	if strings.Contains(body[:start], "hx-replace-url") || strings.Contains(body[:start], "hx-push-url") {
		t.Error("an element before the section, an ancestor perhaps, replaces the address")
	}
	tag, _, _ := strings.Cut(body[start:], ">")
	if strings.Contains(tag, "hx-replace-url") {
		t.Errorf("the section replaces the address on each poll: %s", tag)
	}
	if !strings.Contains(body, "hx-replace-url") {
		t.Error("no control replaces the address")
	}
	// The answer to the poll names no address either.
	if _, replace := fromControl(t, ts, "GET", "/accounts?sort=name", "/accounts?sort=name", "accounts", nil); replace != "" {
		t.Errorf("the poll answer replaces the address with %q", replace)
	}
}

// A control of the view gets the canonical address of what it shows: no
// empty or default fields, and a page past the end becomes the last page.
// The home page and the poll never do.
func TestFleetControlsReplaceTheAddress(t *testing.T) {
	ts, _ := newServer(t)
	for _, c := range []struct {
		name, path, from, trigger, want string
	}{
		{"bar", "/accounts?q=+anna+&status=open&budget=&owner=&attention=&sort=due&dir=asc", "/accounts", "fleet-view", "/accounts?q=anna"},
		{"bar, no filter", "/accounts?q=&status=open&budget=&sort=due&dir=asc", "/accounts?q=anna", "fleet-view", "/accounts"},
		{"head", "/accounts?sort=name&dir=desc", "/accounts", "fleet-sort-name", "/accounts?sort=name&dir=desc"},
		{"page past the end", "/accounts?status=any&page=9", "/accounts", "fleet-next", "/accounts?status=any"},
		{"segment", "/accounts?attention=any", "/accounts", "fleet-attention", "/accounts?attention=any"},
		{"poll", "/accounts?q=anna", "/accounts?q=anna", "accounts", ""},
		{"home", "/accounts?all=1", "/", "show-closed", ""},
	} {
		_, got := fromControl(t, ts, "GET", c.path, c.from, c.trigger, nil)
		if got != c.want {
			t.Errorf("%s: HX-Replace-Url = %q, want %q", c.name, got, c.want)
		}
	}
}

// Each head but Actions is a button that sorts by its column. The sorted
// column sets aria-sort and shows an arrow in the color of its text, and a
// second press turns the direction. Every head starts again at page 1.
func TestSortableHeads(t *testing.T) {
	ts, _ := newServer(t)
	_, body := do(t, ts, "GET", "/accounts?sort=name&page=2", "ops@example.com", true, nil, false)
	for _, want := range []string{
		`<th scope="col" aria-sort="ascending"><button type="button" id="fleet-sort-name" class="sort" hx-get="/accounts?sort=name&amp;dir=desc" hx-target="#accounts" hx-swap="morph" hx-replace-url="true" hx-sync="#fleet-view:replace" data-fleet-nav>Name<svg class="i" aria-hidden="true" focusable="false"><use href="#i-arrow-up"></use></svg></button></th>`,
		`<th scope="col"><button type="button" id="fleet-sort-owner" class="sort" hx-get="/accounts?sort=owner" hx-target="#accounts" hx-swap="morph" hx-replace-url="true" hx-sync="#fleet-view:replace" data-fleet-nav>Owner</button></th>`,
		`id="fleet-sort-status"`, `id="fleet-sort-account"`, `id="fleet-sort-spend"`,
		`<button type="button" id="fleet-sort-due" class="sort" hx-get="/accounts" `,
		`<th scope="col"><span class="vh">Actions</span></th>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the heads lack %s", want)
		}
	}
	if n := strings.Count(body, "aria-sort="); n != 1 {
		t.Errorf("%d heads set aria-sort, want 1", n)
	}
	_, body = do(t, ts, "GET", "/accounts?sort=spend&dir=desc", "ops@example.com", true, nil, false)
	if !strings.Contains(body, `<th scope="col" aria-sort="descending"><button type="button" id="fleet-sort-spend" class="sort" hx-get="/accounts?sort=spend" `) ||
		!strings.Contains(body, `<use href="#i-arrow-down">`) {
		t.Errorf("a descending sort does not show:\n%s", body)
	}
	// The home page keeps plain heads.
	_, body = do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	if strings.Contains(body, `class="sort"`) || strings.Contains(body, "aria-sort") || !strings.Contains(body, `<th scope="col">Name</th>`) {
		t.Error("the home page has sortable heads")
	}
}

func TestSortURL(t *testing.T) {
	for _, c := range []struct {
		sort, dir, key, want string
	}{
		{"due", "asc", "due", "/accounts?dir=desc"},
		{"due", "desc", "due", "/accounts"},
		{"due", "desc", "name", "/accounts?sort=name"},
		{"name", "asc", "name", "/accounts?sort=name&dir=desc"},
		{"name", "desc", "spend", "/accounts?sort=spend"},
	} {
		f := defaultFleetView()
		f.Sort, f.Dir, f.Page, f.Q = c.sort, c.dir, 3, ""
		if got := sortURL(f, c.key); got != c.want {
			t.Errorf("sortURL(%s %s, %s) = %q, want %q", c.sort, c.dir, c.key, got, c.want)
		}
	}
}

// renderPanel renders the accounts panel of the accounts page for an
// admin, with total rows that pass the filters and the page p.
func renderPanel(t *testing.T, f fleetView, total int, errMsg string) string {
	t.Helper()
	pg := page{Viewer: fleetAdmin, SignIn: true, Asker: pathAccounts, Path: pathAccounts, Fleet: f, Total: total}
	var rows []accountRow
	if errMsg == "" && total > 0 {
		rows = []accountRow{fleetRow("111111111111", nil)}
	}
	var out strings.Builder
	if err := accountsPanel(pg, rows, errMsg, "", false).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// The pager shows when the view has more than one page. It names the rows
// it shows, and Previous and Next ask for the pages around it. At either
// end the button stays, marked aria-disabled, and asks for nothing. It also
// carries hx-disable: the morph keeps the button, and htmx would else keep
// the click handler from the time the button was enabled.
func TestPager(t *testing.T) {
	f := defaultFleetView()
	f.Q = "box"
	if got := renderPanel(t, f, fleetPageSize, ""); strings.Contains(got, "pager") {
		t.Errorf("one page has a pager:\n%s", got)
	}
	for _, c := range []struct {
		page       int
		line       string
		prev, next string
	}{
		{1, "Showing 1–50 of 112", `<button type="button" id="fleet-prev" aria-disabled="true" hx-disable>Previous</button>`,
			`<button type="button" id="fleet-next" hx-get="/accounts?q=box&amp;page=2" hx-target="#accounts" hx-swap="morph" hx-replace-url="true" hx-sync="#fleet-view:replace" data-fleet-nav>Next</button>`},
		{2, "Showing 51–100 of 112", `<button type="button" id="fleet-prev" hx-get="/accounts?q=box" `, `id="fleet-next" hx-get="/accounts?q=box&amp;page=3" `},
		{3, "Showing 101–112 of 112", `id="fleet-prev" hx-get="/accounts?q=box&amp;page=2" `, `<button type="button" id="fleet-next" aria-disabled="true" hx-disable>Next</button>`},
	} {
		f.Page = c.page
		got := renderPanel(t, f, 112, "")
		for _, want := range []string{`<nav class="pager" aria-label="Pages"><p>` + c.line + `</p>`, c.prev, c.next, `<input type="hidden" id="fleet-page" name="page" value="` + fmt.Sprint(c.page) + `">`} {
			if !strings.Contains(got, want) {
				t.Errorf("page %d lacks %s:\n%s", c.page, want, got)
			}
		}
		// The pager closes the panel, after the table.
		if !strings.HasSuffix(got, "</nav></section>") {
			t.Errorf("page %d: the pager is not the last part of the panel", c.page)
		}
	}
}

// The filter bar shows in the empty and failed branches too, so a viewer
// can always change or clear a filter.
func TestFilterBarInEveryBranch(t *testing.T) {
	f := defaultFleetView()
	f.Status = string(core.StatusFailed)
	for name, got := range map[string]string{
		"no match": renderPanel(t, f, 0, ""),
		"failed":   renderPanel(t, f, 0, "The list did not load."),
	} {
		if !strings.Contains(got, `<form id="fleet-view"`) || !strings.Contains(got, `id="fleet-page"`) {
			t.Errorf("%s: no filter bar:\n%s", name, got)
		}
	}
	if got := renderPanel(t, f, 0, ""); !strings.Contains(got, `No accounts match these filters. <a href="/accounts">Clear filters</a>`) {
		t.Errorf("no match lacks Clear filters:\n%s", got)
	}
}

// On the accounts page, +7d, Close and Sync send the filter bar and the
// page, so the answer keeps the view. On home they send Show closed.
func TestActionsIncludeTheView(t *testing.T) {
	ts, _ := newServer(t)
	_, body := do(t, ts, "GET", "/accounts", "ops@example.com", true, nil, false)
	if n := strings.Count(body, `hx-include="#fleet-view, #fleet-page"`); n != 5 {
		t.Errorf("%d controls include the view, want Sync and +7d and Close on two rows:\n%s", n, body)
	}
	if strings.Contains(body, `hx-include="#show-closed"`) {
		t.Error("a control on the accounts page includes Show closed, which is not there")
	}
	_, body = do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	if strings.Contains(body, "#fleet-view") || strings.Contains(body, "#fleet-page") {
		t.Error("a control on home includes the fleet view")
	}
}

// An action on page 2 of a filtered view answers with page 2 of that view.
func TestActionOnPageTwoKeepsThePage(t *testing.T) {
	ts, prov := newServer(t)
	for i := range 55 {
		prov.Seed(core.RemoteAccount{ProviderID: fmt.Sprintf("9000000000%02d", i), Name: fmt.Sprintf("load-%02d", i), Email: fmt.Sprintf("l%d@example.com", i), JoinedAt: t0,
			Tags: map[string]string{core.TagManaged: "true", core.TagOwner: "lee@example.com", core.TagExpires: t0.Add(time.Duration(100+i) * time.Hour).Format(time.RFC3339), core.TagBudget: "50"}})
	}
	form := url.Values{"by": {"7d"}, "q": {"load"}, "status": {"open"}, "budget": {""}, "owner": {""}, "attention": {""}, "sort": {"name"}, "dir": {"asc"}, "page": {"2"}}
	body, replace := fromControl(t, ts, "POST", "/accounts/900000000052/extend", "/accounts?q=load&sort=name&page=2", "", form)
	for _, want := range []string{"load-54", "Showing 51–55 of 55", `hx-get="/accounts?q=load&amp;sort=name&amp;page=2" hx-trigger`, `<input type="hidden" id="fleet-page" name="page" value="2">`} {
		if !strings.Contains(body, want) {
			t.Errorf("the answer lacks %s", want)
		}
	}
	if strings.Contains(body, "load-00") || strings.Contains(body, "annas-box") {
		t.Error("the answer shows rows of page 1 or of another view")
	}
	if replace != "" {
		t.Errorf("an action replaces the address with %q", replace)
	}
	if t.Failed() {
		t.Logf("body:\n%s", body)
	}
}

// The home page of an admin links to the whole fleet with its search and
// filters. Anyone else has no fleet to search.
func TestHomeLinksToTheFleetSearch(t *testing.T) {
	ts, _ := newServer(t)
	_, body := do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	if !strings.Contains(body, `<a class="link" href="/accounts">Search and filter all accounts</a> <label class="check"`) {
		t.Errorf("the home page of an admin lacks the link before Show closed:\n%s", body)
	}
	if strings.Contains(body, `id="fleet-view"`) {
		t.Error("the home page has the filter bar")
	}
	_, body = do(t, ts, "GET", "/", "anna@example.com", false, nil, false)
	if strings.Contains(body, "Search and filter all accounts") {
		t.Error("an engineer gets the link to the fleet search")
	}
}

// The Budget health select offers every budget health value the labels
// know, each once.
func TestBudgetOptionsCoverEveryState(t *testing.T) {
	got := slices.Sorted(slices.Values(fleetBudgets))
	want := slices.Sorted(maps.Keys(budgetIcons))
	if !slices.Equal(got, want) {
		t.Errorf("fleetBudgets = %v, want the keys of budgetIcons %v", got, want)
	}
	opts := budgetOptions()
	if opts[0] != (fleetOption{"", "Any"}) || opts[len(opts)-1] != (fleetOption{fleetBudgetUnchecked, "Not checked"}) || len(opts) != len(fleetBudgets)+2 {
		t.Errorf("budgetOptions = %v", opts)
	}
	st := statusOptions()
	if st[0].Value != fleetStatusOpen || st[len(st)-1].Value != fleetStatusAny || len(st) != len(fleetStatuses)+2 {
		t.Errorf("statusOptions = %v", st)
	}
}

// The stylesheet has a look for each new part, from the tokens.
func TestFleetControlsHaveTheirLook(t *testing.T) {
	rules := cssRules(t)
	for sel, want := range map[string]map[string]string{
		".fleet-bar select":                              {"height": "var(--control-h-sm)", "border": "1px solid var(--control-rule)"},
		".segmented":                                     {"border": "1px solid var(--control-rule)", "border-radius": "var(--r-group)"},
		".segmented > a[aria-current=true]":              {"background": "var(--ink)", "color": "var(--paper)"},
		".pager > button":                                {"height": "var(--control-h-sm)", "padding": "0 11px"},
		"button.sort:hover, th[aria-sort] > button.sort": {"color": "var(--ink)"},
	} {
		got, ok := rules[sel]
		if !ok {
			t.Errorf("app.css has no rule %s", sel)
			continue
		}
		for prop, val := range want {
			if got[prop] != val {
				t.Errorf("%s { %s: %q }, want %q", sel, prop, got[prop], val)
			}
		}
	}
	if _, ok := rules[".panel > .scroll:last-child, .panel > .pager:last-child"]; !ok {
		t.Error("the pager does not take the rounded corners of the panel")
	}
}

// The segments, the sort heads and the pager take the text that is in the
// search box now, not the search of the last answer, and share the sync of
// the form. A search that the debounce sends while one of them runs waits
// for its answer.
func TestFleetNavTakesTheTypedSearch(t *testing.T) {
	var nav string
	for _, body := range listeners(staticText(t, "app.js"), "htmx:configRequest") {
		if strings.Contains(body, "data-fleet-nav") {
			nav = body
		}
	}
	holdsEach(t, "the data-fleet-nav listener", nav,
		`document.getElementById("fleet-q")`, `elt.hasAttribute("data-fleet-nav")`,
		`url.searchParams.set("q", q)`, `url.searchParams.delete("q")`,
		`url.searchParams.delete("page")`, `ev.detail.path = url.pathname + url.search`)
	f := defaultFleetView()
	f.Page = 2
	got := renderPanel(t, f, 112, "")
	if n, want := strings.Count(got, ` hx-sync="#fleet-view:replace" data-fleet-nav`), len(fleetSegments)+len(fleetColumns)+2; n != want {
		t.Errorf("%d controls carry the nav sync, want %d", n, want)
	}
}

// A label and its select wrap as one item of the filter bar. The focus
// ring of a sort head sits inside it, so the scroll box does not clip it,
// and the ring of the current segment is paper on its ink.
func TestFleetBarFocusAndWrapRules(t *testing.T) {
	rules := cssRules(t)
	for sel, want := range map[string][2]string{
		".bar-pair":                 {"display", "inline-flex"},
		"button.sort:focus-visible": {"outline-offset", "-2px"},
		".segmented > a[aria-current=true]:focus-visible": {"outline-color", "var(--paper)"},
	} {
		if got := rules[sel][want[0]]; got != want[1] {
			t.Errorf("%s { %s: %q }, want %q", sel, want[0], got, want[1])
		}
	}
	if _, ok := rules[".bar-field"]["margin-right"]; ok {
		t.Error(".bar-field still pulls its select close with a margin")
	}
}

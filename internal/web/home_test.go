package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

// The ledger groups the rows by kind in the order of core, with over budget
// last. A row with two kinds counts in each, a closed row counts, and a kind
// with no account has no row. The names are in name order, at most three.
func TestBuildLedger(t *testing.T) {
	rows := []accountRow{
		fleetRow("1", func(r *accountRow) { r.Account.Name, r.Account.Status = "zed-box", core.StatusExpiring }),
		fleetRow("2", func(r *accountRow) { r.Account.Name, r.Account.Status = "amy-box", core.StatusExpiring }),
		fleetRow("3", func(r *accountRow) { r.Account.Name, r.Account.Status = "bo-box", core.StatusExpiring }),
		fleetRow("4", func(r *accountRow) {
			r.Account.Name, r.Account.Status = "cy-box", core.StatusExpiring
			r.Spend = 80 // over its budget of 50
		}),
		fleetRow("5", func(r *accountRow) {
			r.Account.Name, r.Account.Status, r.Account.BudgetHealth = "old-box", core.StatusClosed, "restricted"
			r.Account.Managed, r.Account.BudgetPolicy = true, "policy"
		}),
		fleetRow("6", func(r *accountRow) { r.Account.Name, r.Account.Status = "fine-box", core.StatusActive }),
		fleetRow("7", func(r *accountRow) { r.Account.Name, r.Account.Status = "bad-box", core.StatusFailed }),
	}
	got := buildLedger(rows, fleetNow)
	var kinds []string
	for _, l := range got {
		kinds = append(kinds, l.Kind)
	}
	if want := []string{"failed", "restricted", "expiring", "over-budget"}; !slices.Equal(kinds, want) {
		t.Fatalf("ledger kinds = %v, want %v", kinds, want)
	}
	for _, c := range []struct {
		i                          int
		headline, names, url, tone string
		icon                       string
	}{
		{0, "1 failed creation", "bad-box", "/accounts?attention=failed", "fault", "x-circle"},
		{1, "1 budget restricted", "old-box", "/accounts?status=any&attention=restricted", "fault", "lock"},
		{2, "4 expiring soon", "amy-box, bo-box, cy-box and 1 more", "/accounts?attention=expiring", "expiring", "hourglass"},
		{3, "1 account over budget", "cy-box", "/accounts?attention=over-budget", "fault", "alert"},
	} {
		l := got[c.i]
		if l.Headline != c.headline || l.Names != c.names || l.URL != c.url || l.Tone != c.tone || l.Icon != c.icon {
			t.Errorf("row %d = %+v, want %q %q %q %q %q", c.i, l, c.headline, c.names, c.url, c.tone, c.icon)
		}
	}
	if got := buildLedger([]accountRow{fleetRow("6", nil)}, fleetNow); len(got) != 0 {
		t.Errorf("a calm fleet has ledger rows: %+v", got)
	}
}

// Every kind of the attention filter has a ledger look, in the same order,
// and every icon is in the sprite.
func TestLedgerKindsFollowTheFilter(t *testing.T) {
	var kinds []string
	for _, k := range ledgerKinds {
		kinds = append(kinds, k.Kind)
		if k.One == "" || k.Many == "" || k.Icon == "" {
			t.Errorf("%s lacks a headline or an icon", k.Kind)
		}
		if !strings.Contains(renderSprite(t), `id="i-`+k.Icon+`"`) {
			t.Errorf("%s draws the icon %s, which the sprite lacks", k.Kind, k.Icon)
		}
		switch k.Tone {
		case "fault", "expiring", "ink":
		default:
			t.Errorf("%s has the tone %q", k.Kind, k.Tone)
		}
	}
	if !slices.Equal(kinds, fleetAttention) {
		t.Errorf("ledger kinds = %v, want the filter order %v", kinds, fleetAttention)
	}
}

func renderSprite(t *testing.T) string {
	t.Helper()
	var out strings.Builder
	if err := iconSprite().Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// The home page lists the open accounts that are not pending requests, by
// due date, and cuts the list to homeFleetSize. The count is before the cut.
// The count of the accounts page adds the pending requests.
func TestHomeRows(t *testing.T) {
	var rows []accountRow
	for i := range homeFleetSize + 5 {
		rows = append(rows, fleetRow(fmt.Sprintf("%03d", i), func(r *accountRow) {
			r.Account.ExpiresAt = fleetNow.Add(time.Duration(200-i) * time.Hour)
		}))
	}
	rows = append(rows,
		fleetRow("req:wait", func(r *accountRow) { r.Account.Status = core.StatusPending }),
		fleetRow("gone", func(r *accountRow) { r.Account.Status = core.StatusClosed }),
		fleetRow("dead", func(r *accountRow) { r.Account.Status = core.StatusFailed }),
	)
	shown, total, all := homeRows(rows, fleetAdmin, fleetNow)
	if total != homeFleetSize+6 || len(shown) != homeFleetSize {
		t.Fatalf("homeRows = %d rows of %d, want %d of %d", len(shown), total, homeFleetSize, homeFleetSize+6)
	}
	// The link to the accounts page counts what that page lists by
	// default, so it counts the pending request too.
	if all != homeFleetSize+7 {
		t.Errorf("homeRows counts %d rows for the accounts page, want %d", all, homeFleetSize+7)
	}
	if shown[0].Account.ID != fmt.Sprintf("%03d", homeFleetSize+4) {
		t.Errorf("the first row is %s, want the soonest due date", shown[0].Account.ID)
	}
	for _, r := range shown {
		if r.Account.Status == core.StatusPending || r.Account.Status == core.StatusClosed {
			t.Errorf("home lists %s, which is %s", r.Account.ID, r.Account.Status)
		}
	}
}

// The limit line counts the open accounts and pending requests of the
// viewer, as the core limit does. An unknown count says so.
func TestLimitLine(t *testing.T) {
	v := Viewer{Email: "Bob@example.com"}
	for _, c := range []struct {
		name  string
		pg    page
		limit int
		want  string
	}{
		{"none", page{Viewer: v}, 0, "No per-owner limit applies."},
		{"under", page{Viewer: v, Holds: map[string][]string{"bob@example.com": {"a"}}}, 3, "You have 1 of 3 open accounts and requests allowed."},
		{"at", page{Viewer: v, Holds: map[string][]string{"bob@example.com": {"a", "b"}}}, 2, "You have 2 of 2 open accounts and requests allowed. Close an account or withdraw a request before you ask for another."},
		{"zero", page{Viewer: v, Holds: map[string][]string{}}, 1, "You have 0 of 1 open accounts and requests allowed."},
		{"list failed", page{Viewer: v, HoldsFailed: true}, 1, "Your count against the per-owner limit is unknown, because the account list is unavailable."},
		{"queue failed", page{Viewer: v, PendingFailed: true}, 1, "Your count against the per-owner limit is unknown, because the account list is unavailable."},
	} {
		c.pg.Limits.MaxPerOwner = c.limit
		if got := limitLine(c.pg); got != c.want {
			t.Errorf("%s: limitLine = %q, want %q", c.name, got, c.want)
		}
	}
}

// partsIn lists the ids of the home parts in the order that body shows
// them.
func partsIn(body string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`<section (?:class="panel" )?id="(attention|pending|accounts|request)"`).FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

// Each role gets its first job first: the ledger and the desk for an admin,
// the desk for an approver, and the loan cards for anyone else. New request
// comes after them. Only an admin gets the ledger, and every home keeps one
// live region, in the queue panel.
func TestHomeByRole(t *testing.T) {
	ts, _ := newServer(t)
	for _, c := range []struct {
		name            string
		user            string
		admin, approver bool
		parts           []string
	}{
		{"admin", "ops@example.com", true, true, []string{"attention", "pending", "accounts", "request"}},
		{"approver", "lead@example.com", false, true, []string{"pending", "accounts", "request"}},
		{"engineer", "anna@example.com", false, false, []string{"accounts", "pending", "request"}},
	} {
		_, body := doAs(t, ts, "GET", "/", c.user, c.admin, c.approver, nil, false)
		if got := partsIn(body); !slices.Equal(got, c.parts) {
			t.Errorf("%s: the home parts come as %v, want %v", c.name, got, c.parts)
		}
		if c.admin && !strings.Contains(body, `<div class="home-top"><section id="attention" class="panel ledger" aria-labelledby="attention-title">`) {
			t.Errorf("%s: the ledger does not open the top row beside the desk:\n%s", c.name, body)
		}
		if !regionInQueue(body) || strings.Count(body, `aria-live="polite"`) != 1 {
			t.Errorf("%s: the home page does not keep one region in the queue panel:\n%s", c.name, body)
		}
		if strings.Count(body, `id="account-error"`) != 1 || strings.Count(body, `id="form-error"`) != 1 {
			t.Errorf("%s: the home page lacks one refusal slot of each kind:\n%s", c.name, body)
		}
		// The shell's New request link still finds the panel.
		if !strings.Contains(body, `href="/#request"`) || !strings.Contains(body, `<section class="panel" id="request"`) {
			t.Errorf("%s: /#request does not find the New request panel", c.name)
		}
	}
}

// The fleet of the admin home is compact, by due date, with plain heads and
// the due cell on card stock. It leaves out pending requests, which the
// desk shows, and ends with a link to every open account.
func TestAdminHomeFleet(t *testing.T) {
	ts, _ := newServer(t)
	if res, body := do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("submit = %s", body)
	}
	_, body := do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	_, section, _ := strings.Cut(body, `<section id="accounts"`)
	section, _, _ = strings.Cut(section, "</section>")
	for _, want := range []string{
		`<h2 id="accounts-title" tabindex="-1">Accounts <span class="muted">(2)</span></h2>`,
		`<table class="fleet fleet-compact">`,
		`<th scope="col">Account</th><th scope="col">Due</th><th scope="col">Spend / Budget</th>`,
		`<td class="due"><span class="due-date"><time id="stamp-111111111111-`,
		`class="stamp stamp-date stamp-soon ` + stampTurn("111111111111") + `" datetime="2026-09-18T12:00:00Z" title="2026-09-18 12:00 UTC">SEP 18 2026</time>`,
		`<p class="panel-foot"><a class="link" href="/accounts">All 3 accounts</a></p>`,
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the admin fleet lacks %s:\n%s", want, section)
		}
	}
	for _, bad := range []string{"req:bobs-box", "Show closed", "Search and filter all accounts", `class="sort"`, "Expires"} {
		if strings.Contains(section, bad) {
			t.Errorf("the admin fleet holds %s:\n%s", bad, section)
		}
	}
	// The accounts page names the column Due too and keeps its sort key.
	_, body = do(t, ts, "GET", "/accounts", "ops@example.com", true, nil, false)
	if !strings.Contains(body, `id="fleet-sort-due" class="sort" hx-get="/accounts?dir=desc"`) || !strings.Contains(body, `data-fleet-nav>Due<svg`) {
		t.Errorf("the accounts page lost its Due head:\n%s", body)
	}
	if !strings.Contains(body, `<td class="due">`) || strings.Contains(body, "fleet-compact") {
		t.Errorf("the accounts page lacks the due cell or is compact:\n%s", body)
	}
}

// An engineer gets one loan card for each open account: the name, the AWS
// account ID, the status, the large stamp, the spend on a full-width meter,
// and +7d and Close that name the account. A pending request is in Your
// requests, not on a card. A link leads to all their accounts.
func TestLoanCards(t *testing.T) {
	ts, _ := newServer(t)
	_, body := do(t, ts, "GET", "/", "anna@example.com", false, nil, false)
	_, section, _ := strings.Cut(body, `<section id="accounts"`)
	section, _, _ = strings.Cut(section, "</section>")
	for _, want := range []string{
		`<h2 id="accounts-title" tabindex="-1">Your accounts <span class="muted">(1)</span></h2>`,
		`<ul class="loans" role="list"><li id="acct-111111111111" class="loan">`,
		`<h3 class="loan-name"><a href="/accounts/111111111111">annas-box</a></h3><code class="loan-id">111111111111</code>`,
		`<span class="status status-active">`,
		`<div class="loan-due"><span class="due-date due-large"><time id="stamp-111111111111-`,
		`class="stamp stamp-date stamp-large stamp-soon `,
		`<p class="spend">$22.50 of $50.00 a month</p>`,
		`<svg class="meter-full" width="100%" height="6" aria-hidden="true" focusable="false"><rect class="meter-track" width="100%" height="6" rx="3"></rect><rect class="meter-fill" width="45%" height="6" rx="3"></rect></svg>`,
		`+7d<span class="vh"> annas-box</span>`,
		`data-dialog="close" data-name="annas-box" data-account="111111111111" data-owner="anna@example.com" data-action="close"><button type="submit">Close<span class="vh"> annas-box</span></button>`,
		`<p class="panel-foot"><a class="link" href="/accounts">All your accounts</a></p>`,
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the loan cards lack %s:\n%s", want, section)
		}
	}
	if strings.Contains(section, "<table") {
		t.Errorf("the cards come in a table:\n%s", section)
	}
	// Anna holds her account against the limit of 1.
	if !strings.Contains(body, `<p class="limit-line">You have 1 of 1 open accounts and requests allowed. Close an account or withdraw a request before you ask for another.</p>`) {
		t.Errorf("Your requests lacks the limit line:\n%s", body)
	}
	// Bob has only a pending request. It shows in Your requests, counts
	// against the limit, and gets no card.
	if res, body := do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("submit = %s", body)
	}
	_, body = do(t, ts, "GET", "/", "bob@example.com", false, nil, false)
	_, section, _ = strings.Cut(body, `<section id="accounts"`)
	section, _, _ = strings.Cut(section, "</section>")
	if strings.Contains(section, "bobs-box") || !strings.Contains(section, `<p class="empty">You have no open accounts. <a href="#request">New request</a></p>`) {
		t.Errorf("a pending request gets a card:\n%s", section)
	}
	if !strings.Contains(slipIn(body, "bobs-box"), "bobs-box") || !strings.Contains(body, `<p class="limit-line">You have 1 of 1 open accounts`) {
		t.Errorf("Your requests lacks the request or its count:\n%s", body)
	}
	// An approver gets the same cards after the desk.
	_, body = doAs(t, ts, "GET", "/", "anna@example.com", false, true, nil, false)
	if !strings.Contains(body, `<li id="acct-111111111111" class="loan">`) || strings.Contains(body, `class="limit-line"`) {
		t.Errorf("an approver does not get loan cards, or gets the limit line of Your requests:\n%s", body)
	}
}

// A loan card shows the actions and the due date that its state allows.
func TestLoanCardStates(t *testing.T) {
	render := func(change func(*accountRow)) string {
		t.Helper()
		r := fleetRow("111111111111", change)
		r.Now = fleetNow
		var out strings.Builder
		if err := loanCard(r, false).Render(context.Background(), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	creating := render(func(r *accountRow) {
		r.Account.Status, r.Account.ProviderID, r.Account.ExpiresAt = core.StatusCreating, "", time.Time{}
	})
	for _, want := range []string{`<span class="loan-id muted">not created yet</span>`, `<span class="date-unset">Set when it is ready</span>`} {
		if !strings.Contains(creating, want) {
			t.Errorf("a creating card lacks %s:\n%s", want, creating)
		}
	}
	if strings.Contains(creating, "loan-actions") {
		t.Errorf("a creating card has actions:\n%s", creating)
	}
	failed := render(func(r *accountRow) { r.Account.Status = core.StatusFailed })
	if !strings.Contains(failed, `<div class="loan-due"><span class="muted">—</span></div>`) || strings.Contains(failed, "loan-actions") {
		t.Errorf("a failed card shows a date or actions:\n%s", failed)
	}
	unknown := render(func(r *accountRow) { r.SpendKnown = false })
	if !strings.Contains(unknown, `<p class="spend"><span class="muted">unavailable</span> of $50.00 a month</p>`) || strings.Contains(unknown, "meter-full") {
		t.Errorf("unknown spend shows a number or a meter:\n%s", unknown)
	}
	over := render(func(r *accountRow) { r.Spend = 60 })
	if !strings.Contains(over, `<div class="meter-block meter-over"`) || !strings.Contains(over, `<rect class="meter-fill" width="100%" height="6" rx="3"></rect></svg>`) || !strings.Contains(over, "over budget") {
		t.Errorf("over budget does not fill the meter and say so:\n%s", over)
	}
}

// The due stamp shows the date, red ink in the last 3 days, "past due"
// after the date, the earlier date after an extension, and a note when
// there is no date.
func TestDueStamp(t *testing.T) {
	render := func(a *core.Account, large bool) string {
		t.Helper()
		var out strings.Builder
		if err := dueStamp(dueFor(a, fleetNow), "111111111111", large, false).Render(context.Background(), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	far := fleetNow.Add(10 * 24 * time.Hour)
	ok := render(&core.Account{Status: core.StatusActive, ExpiresAt: far}, false)
	if strings.Contains(ok, "stamp-soon") || strings.Contains(ok, "due-from") || !strings.Contains(ok, ">"+stampDate(far)+"</time>") {
		t.Errorf("a far due date = %s", ok)
	}
	renewed := render(&core.Account{Status: core.StatusActive, ExpiresAt: far, ExtendedFrom: far.Add(-7 * 24 * time.Hour)}, true)
	if !strings.Contains(renewed, `<span class="due-from">extended from <s>`+stampShort(far.Add(-7*24*time.Hour))+`</s></span> <time`) || !strings.Contains(renewed, "stamp-large") {
		t.Errorf("a renewed stamp lacks the earlier date above it: %s", renewed)
	}
	past := render(&core.Account{Status: core.StatusActive, ExpiresAt: fleetNow.Add(-time.Hour)}, false)
	if !strings.Contains(past, "stamp-soon") || !strings.Contains(past, `<span class="due-past">past due</span>`) {
		t.Errorf("a passed due date = %s", past)
	}
	for status, want := range map[core.Status]string{
		core.StatusPending: `<span class="date-unset">Stamped at approval</span>`,
		core.StatusClosed:  `<span class="muted">—</span>`,
	} {
		if got := render(&core.Account{Status: status, ExpiresAt: far}, false); got != want {
			t.Errorf("%s: %s, want %s", status, got, want)
		}
	}
	damaged := render(&core.Account{Status: core.StatusActive, TagErrors: map[string]string{core.TagExpires: "bad"}}, false)
	if damaged != `<span class="date-unset">unknown</span>` {
		t.Errorf("a damaged expiry = %s", damaged)
	}
}

// +7d lands the new stamp, with the earlier date above it. The poll hash
// leaves the landing out, so the next poll still gets 204.
func TestExtendLandsTheStamp(t *testing.T) {
	ts, _ := newServer(t)
	for _, c := range []struct {
		name, user string
		admin      bool
	}{
		{"loan card", "anna@example.com", false},
		{"admin fleet", "ops@example.com", true},
	} {
		res, body := fromPage(t, ts, "POST", "/accounts/111111111111/extend", "/", c.user, c.admin, false, url.Values{"by": {"7d"}}, "")
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: extend = %d:\n%s", c.name, res.StatusCode, body)
		}
		if !regexp.MustCompile(`<time id="stamp-111111111111-\d+" class="stamp stamp-date[^"]* stamp-new"`).MatchString(body) {
			t.Errorf("%s: the new stamp does not land:\n%s", c.name, body)
		}
		if !strings.Contains(body, `<span class="due-from">extended from <s>`) {
			t.Errorf("%s: the stamp lacks the earlier date:\n%s", c.name, body)
		}
		if res, _ := fromPage(t, ts, "GET", "/accounts", "/", c.user, c.admin, false, nil, pollHashIn(t, body)); res.StatusCode != http.StatusNoContent {
			t.Errorf("%s: the poll after the landing = %d, want 204", c.name, res.StatusCode)
		}
	}
}

// The ledger rides every home answer of an admin out of band, so the poll
// hash covers it. Anyone else, and the accounts page, get no ledger.
func TestLedgerRidesTheHomeAnswers(t *testing.T) {
	ts, prov := newServer(t)
	part := `<section id="attention" class="panel ledger" hx-swap-oob="morph" aria-labelledby="attention-title">`
	_, body := fromPage(t, ts, "GET", "/accounts", "/", "ops@example.com", true, false, nil, "")
	if !strings.Contains(body, part) || !strings.Contains(body, `<p class="empty">Nothing needs attention.</p>`) {
		t.Errorf("the admin poll lacks the ledger:\n%s", body)
	}
	calm := pollHashIn(t, body)
	// A restricted budget changes the ledger, so the poll gets 200.
	prov.Seed(core.RemoteAccount{ProviderID: "333333333333", Name: "tight-box", JoinedAt: t0,
		Tags: map[string]string{core.TagManaged: "true", core.TagOwner: "sam@example.com", core.TagExpires: t0.Add(900 * time.Hour).Format(time.RFC3339), core.TagBudget: "50", core.TagBudgetHealth: "restricted"}})
	res, body := fromPage(t, ts, "GET", "/accounts", "/", "ops@example.com", true, false, nil, calm)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `<a class="ledger-row" href="/accounts?attention=restricted">`) || !strings.Contains(body, "1 budget restricted") {
		t.Errorf("the poll after a restriction = %d, want the ledger row:\n%s", res.StatusCode, body)
	}
	for _, c := range []struct {
		name, from, user string
		admin            bool
	}{
		{"engineer home", "/", "anna@example.com", false},
		{"admin accounts page", "/accounts", "ops@example.com", true},
	} {
		if _, body := fromPage(t, ts, "GET", "/accounts", c.from, c.user, c.admin, false, nil, ""); strings.Contains(body, `id="attention"`) {
			t.Errorf("%s: the answer carries the ledger:\n%s", c.name, body)
		}
	}
	// A decision from the desk carries it too.
	if res, body := do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("submit = %s", body)
	}
	_, home := do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	ref := refIn(t, home, "bobs-box")
	_, body = fromPage(t, ts, "POST", "/requests/bobs-box/deny", "/", "ops@example.com", true, true, url.Values{"version": {ref.Version}, "reason": {"no"}}, "")
	if !strings.Contains(body, part) {
		t.Errorf("the desk answer lacks the ledger:\n%s", body)
	}
}

// A refused +7d or Close on the home page fills the slot above the
// accounts, because the New request form sits below them. On the accounts
// page it fills the slot under the heading, and on an account page the slot
// of the account.
func TestAccountRefusalsOnHomeFillTheSlotAboveTheAccounts(t *testing.T) {
	ts, _ := newServer(t)
	for _, c := range []struct{ from, slot string }{
		{"/", "account-error"},
		{"/accounts", "form-error"},
		{"/accounts/111111111111", "account-error"},
	} {
		_, body := fromPage(t, ts, "POST", "/accounts/111111111111/extend", c.from, "anna@example.com", false, false, url.Values{"by": {"100d"}}, "")
		if !strings.Contains(body, `<div id="`+c.slot+`" hx-swap-oob="innerHTML">`) {
			t.Errorf("%s: the refusal does not fill %s:\n%s", c.from, c.slot, body)
		}
	}
	_, body := fromPage(t, ts, "POST", "/accounts/222222222222/close", "/", "anna@example.com", false, false, url.Values{}, "")
	if !strings.Contains(body, `<div id="account-error" hx-swap-oob="innerHTML">`) {
		t.Errorf("a forbidden close on home does not fill the slot above the accounts:\n%s", body)
	}
	if !strings.Contains(staticText(t, "app.js"), `var slots = ["form-error", "account-error"];`) {
		t.Error("app.js does not empty the slot above the accounts after a success")
	}
}

// On the home page the New request panel comes last. So only a refused New
// request fills the slot under its form. A refusal of an action on a
// request fills the slot of the request panel, and a failed Sync fills the
// slot above the accounts.
func TestHomeRefusalsFillTheSlotNearTheirControl(t *testing.T) {
	ts, _ := newServer(t)
	if res, body := do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("request = %s", body)
	}
	_, home := do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	edit := refForm(refIn(t, home, "bobs-box"))
	edit.Set("budget", "lots")
	for _, c := range []struct {
		name, path string
		form       url.Values
		slot       string
	}{
		{"new request", "/requests", url.Values{"name": {"big-one"}, "budget": {"lots"}}, "form-error"},
		{"edit a request", "/requests/bobs-box/edit", edit, "pending-error"},
	} {
		_, body := fromPage(t, ts, "POST", c.path, "/", "ops@example.com", true, true, c.form, "")
		if !strings.Contains(body, `<div id="`+c.slot+`" hx-swap-oob="innerHTML">`) {
			t.Errorf("%s: the refusal does not fill %s:\n%s", c.name, c.slot, body)
		}
	}
	flaky := &flakyList{}
	failing, _ := newServerOn(t, func(p *fake.Provider) core.Provider { flaky.Provider = p; return flaky })
	flaky.fail.Store(true)
	for _, c := range []struct{ from, slot string }{
		{"/", "account-error"},
		{"/accounts", "form-error"},
	} {
		res, body := fromPage(t, failing, "POST", "/refresh", c.from, "ops@example.com", true, true, url.Values{}, "")
		if res.Header.Get("X-Form-Error") != "1" || !strings.Contains(body, `<div id="`+c.slot+`" hx-swap-oob="innerHTML">`) {
			t.Errorf("a failed Sync on %s does not fill %s:\n%s", c.from, c.slot, body)
		}
	}
}

// The loan card and the due cell are card stock, with card rules, and so is
// the meter track of a loan card. The due cell keeps card stock on hover.
// The ledger sits on paper. The link of an empty state is a touch target.
func TestHomeSurfaces(t *testing.T) {
	top, media := layerRules(t, "components")
	wantDecls(t, "components", top, ".loan", map[string]string{
		"background": "var(--card)", "border": "1px solid var(--card-rule)", "border-radius": "var(--r-panel)", "padding": "var(--space-lg)",
	})
	wantDecls(t, "components", top, ".fleet td.due, .fleet tbody tr:hover td.due, .fleet tbody tr:last-child td.due", map[string]string{
		"background": "var(--card)", "border-bottom": "1px solid var(--card-rule)",
	})
	wantDecls(t, "components", top, ".fleet-compact td", map[string]string{"padding-top": "var(--space-xs)", "padding-bottom": "var(--space-xs)"})
	wantDecls(t, "components", top, ".meter-full", map[string]string{"width": "100%", "height": "6px"})
	wantDecls(t, "components", top, ".loan .meter-track", map[string]string{"fill": "var(--card-rule)"})
	if _, ok := top[".ledger"]; ok {
		t.Error("the ledger has its own surface, but it is a paper panel")
	}
	layout, lmedia := layerRules(t, "layout")
	wantDecls(t, "layout", layout, ".home-top", map[string]string{"display": "grid", "grid-template-columns": "minmax(300px, 384px) minmax(0, 1fr)"})
	wantDecls(t, "layout (max-width: 1200px)", lmedia["(max-width: 1200px)"], ".home-top", map[string]string{"grid-template-columns": "minmax(0, 1fr)"})
	_ = media
	_, utilities := layerRules(t, "utilities")
	wantDecls(t, "utilities (max-width: 640px)", utilities["(max-width: 640px)"], ".loan-name a, .ledger-row", map[string]string{"min-height": "var(--touch)"})
	wantDecls(t, "utilities (max-width: 640px)", utilities["(max-width: 640px)"], ".empty a", map[string]string{"min-height": "var(--touch)"})
}

// A focused ledger row that a poll takes away gives the focus to the next
// row, or else to the Needs attention heading, which takes focus from
// script only.
func TestLedgerKeepsTheFocus(t *testing.T) {
	js := staticText(t, "app.js")
	holdsEach(t, "notePlace", funcBody(t, js, "function notePlace(xhr, source) {"),
		`active.closest(ledgerRows)`,
		`rows: ledgerRows`,
		`want: "a.ledger-row"`)
	settle := listeners(js, "htmx:afterSettle")
	if len(settle) != 1 {
		t.Fatalf("app.js has %d htmx:afterSettle listeners, want 1", len(settle))
	}
	holdsEach(t, "the focus rule", settle[0], `was.rows === ledgerRows`, `document.getElementById("attention-title")`)
	ts, _ := newServer(t)
	_, body := do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	if !strings.Contains(body, `<h2 id="attention-title" tabindex="-1">Needs attention</h2>`) {
		t.Errorf("the ledger heading cannot take the focus:\n%s", body)
	}
}

package web

import (
	"bytes"
	"context"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
)

// renderPageCard renders the loan card body of the account page for r.
func renderPageCard(t *testing.T, r accountRow) string {
	t.Helper()
	var buf bytes.Buffer
	if err := loanBody(r, false, true).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// Every page with a Close button also holds the close dialog, so a Close
// never needs to post without it. app.js fails closed all the same: with
// no dialog, or no name on the form, openClose sends nothing, says to
// reload the page and still reports that it held the request back.
func TestCloseFailsClosedOnEveryPage(t *testing.T) {
	ts, _ := newServer(t)
	for _, c := range []struct {
		path, user string
		admin      bool
	}{
		{"/", "anna@example.com", false},
		{"/", "ops@example.com", true},
		{"/accounts", "anna@example.com", false},
		{"/accounts", "ops@example.com", true},
		{"/accounts/111111111111", "anna@example.com", false},
		{"/accounts/222222222222", "ops@example.com", true},
	} {
		_, body := do(t, ts, "GET", c.path, c.user, c.admin, nil, false)
		if !strings.Contains(body, `data-dialog="close"`) {
			t.Fatalf("%s as %s has no Close button to check:\n%s", c.path, c.user, body)
		}
		if n := strings.Count(body, `<dialog id="close-dialog"`); n != 1 {
			t.Errorf("%s as %s has a Close button and %d close dialogs, want 1", c.path, c.user, n)
		}
	}
	js := staticText(t, "app.js")
	holdsEach(t, "askFirst", funcBody(t, js, "function askFirst(elt, issue) {"),
		`if (kind === "close") {`, `return openClose(elt, issue);`)
	body := funcBody(t, js, "function openClose(elt, issue) {")
	inOrder(t, "openClose", body,
		"if (dialogOpen()) {\n    return true;\n  }",
		"if (!dialog || !name) {\n    say(noCloseDialog);\n    return true;\n  }",
		"dialog.showModal();",
		"return true;")
	if strings.Contains(body, "return false") {
		t.Errorf("openClose can let a closure post with no dialog:\n%s", body)
	}
	holdsEach(t, "app.js", js, `var noCloseDialog = "Reload the page to close this account.";`)
}

// The account page holds its close dialog outside the section that the
// refresh redraws, keeps one live region and unique ids, and no form
// sits in another.
func TestAccountPageHoldsTheCloseDialogOutsideTheRefresh(t *testing.T) {
	ts, _ := newServer(t)
	_, body := do(t, ts, "GET", "/accounts/111111111111", "ops@example.com", true, nil, false)
	section, _, _ := strings.Cut(body[strings.Index(body, `<section id="account"`):], `<section class="panel" aria-labelledby="budget-title">`)
	if strings.Contains(section, "<dialog") {
		t.Errorf("the refreshed section holds a dialog:\n%s", section)
	}
	for attr, want := range map[string]int{`id="notice"`: 1, "aria-live=": 1, `role="status"`: 1, `<dialog id="close-dialog"`: 1} {
		if got := strings.Count(body, attr); got != want {
			t.Errorf("the account page has %d %s, want %d", got, attr, want)
		}
	}
	seen := map[string]bool{}
	for _, m := range idAttr.FindAllStringSubmatch(body, -1) {
		if seen[m[1]] {
			t.Errorf("the id %s shows twice", m[1])
		}
		seen[m[1]] = true
	}
	assertNoNestedForms(t, body)
	// An answer to the refresh carries no dialog.
	_, frag := fromPage(t, ts, "GET", "/accounts/111111111111", "/accounts/111111111111", "ops@example.com", true, true, nil, "")
	if strings.Contains(frag, "<dialog") {
		t.Errorf("the refresh carries a dialog:\n%s", frag)
	}
}

// The account page shows the account as a loan card: the name is the page
// heading, which can take the focus, the stamp is the 22 px one, and +7d
// and Close answer the account section. A loan card of the home page keeps
// its linked name and answers the accounts section.
func TestAccountPageCard(t *testing.T) {
	ts, _ := newServer(t)
	const page = "/accounts/111111111111"
	_, body := do(t, ts, "GET", page, "anna@example.com", false, nil, false)
	for _, want := range []string{
		`<div class="loan loan-page"><div class="loan-head"><h1 id="account-title" class="account-name" tabindex="-1">annas-box</h1><code class="loan-id">111111111111</code></div>`,
		`<div class="loan-status"><span class="status status-active">`,
		`class="stamp stamp-date stamp-large`,
		`<form class="inline" hx-post="/accounts/111111111111/extend" hx-target="#account" hx-swap="morph" hx-sync="this:drop">`,
		`<form class="inline" hx-post="/accounts/111111111111/close" hx-target="#account" hx-swap="morph" hx-sync="this:drop" data-dialog="close"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the account page lacks %s:\n%s", want, body)
		}
	}
	for _, bad := range []string{`hx-target="#accounts"`, "<li", `class="loan-name"`} {
		if strings.Contains(between(body, `<section id="account"`, `<section class="panel"`), bad) {
			t.Errorf("the card of the account page holds %s", bad)
		}
	}
	_, home := do(t, ts, "GET", "/", "anna@example.com", false, nil, false)
	for _, want := range []string{
		`<li id="acct-111111111111" class="loan"><div class="loan-head"><h3 class="loan-name"><a href="/accounts/111111111111">annas-box</a></h3>`,
		`hx-post="/accounts/111111111111/extend" hx-target="#accounts"`,
	} {
		if !strings.Contains(home, want) {
			t.Errorf("the home page lacks %s:\n%s", want, home)
		}
	}
	// +7d on the account page lands the new stamp in the answer.
	_, answer := fromPage(t, ts, "POST", page+"/extend", page, "anna@example.com", false, false, url.Values{"by": {"7d"}}, "")
	if !strings.Contains(answer, "stamp-large") || !strings.Contains(answer, "stamp-new") || !strings.Contains(answer, `<span class="due-from">extended from <s>SEP 18</s></span>`) {
		t.Errorf("the answer to +7d does not land the stamp with the renewal line:\n%s", answer)
	}
}

// The meter of the account page is the full-width one with the spend this
// month against the budget and what is left, and the line that cost data
// lags. Unknown spend says "unavailable" with no bar and nothing left.
func TestAccountPageMeter(t *testing.T) {
	a := func(budget float64) *core.Account {
		return &core.Account{ID: "x", Name: "x", ProviderID: "111111111111", Status: core.StatusActive, BudgetUSD: budget, CreatedAt: t0, ExpiresAt: t0.Add(72 * time.Hour)}
	}
	lag := `<p class="help">Cost data lags about a day.</p>`
	for _, c := range []struct {
		name string
		row  accountRow
		want []string
		bad  []string
	}{
		{"below 75 percent", accountRow{Account: a(50), Spend: 22.5, SpendKnown: true},
			[]string{`<p class="spend">$22.50 of $50.00 a month</p>`, `<div class="meter-block meter-ok" title="$22.50 of $50.00">`, `<rect class="meter-fill" width="45%" height="6" rx="3">`, `<p class="spend-left">$27.50 left this month</p>`, lag},
			[]string{"meter-label"}},
		{"from 75 percent", accountRow{Account: a(50), Spend: 40, SpendKnown: true},
			[]string{`<div class="meter-block meter-high"`, `width="80%"`, `<span class="meter-label">80% used</span>`, `<p class="spend-left">$10.00 left this month</p>`, lag},
			nil},
		{"over budget", accountRow{Account: a(50), Spend: 60, SpendKnown: true},
			[]string{`<div class="meter-block meter-over"`, `width="100%" height="6" rx="3"></rect></svg>`, "over budget", `<p class="spend-left">$10.00 over the budget this month</p>`, lag},
			nil},
		{"unknown spend", accountRow{Account: a(50)},
			[]string{`<p class="spend"><span class="muted">unavailable</span> of $50.00 a month</p>`, lag},
			[]string{"meter-block", "spend-left", "$0.00"}},
		{"no budget", accountRow{Account: a(0), Spend: 5, SpendKnown: true},
			[]string{`<p class="spend">$5.00 of unknown a month</p>`, lag},
			[]string{"meter-block", "spend-left"}},
	} {
		out := renderPageCard(t, c.row)
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: lacks %s:\n%s", c.name, w, out)
			}
		}
		for _, b := range c.bad {
			if strings.Contains(out, b) {
				t.Errorf("%s: holds %s:\n%s", c.name, b, out)
			}
		}
		// The budget protection is a fact of the page, not a line of the card.
		if strings.Contains(out, "budget-") {
			t.Errorf("%s: the card holds the budget label:\n%s", c.name, out)
		}
	}
}

// The facts are in plain words and in this order. A fact that the account
// does not have says "not recorded". The creation request ID sits in a
// small line under the facts. The problem words sit under the status of
// the card, so the facts do not repeat them.
func TestAccountPageFacts(t *testing.T) {
	a := &core.Account{
		ID: "111111111111", Name: "box", ProviderID: "111111111111", RequestID: "car-123", Email: "a@example.com",
		Owner: "anna@example.com", Status: core.StatusActive, BudgetUSD: 50, CreatedAt: t0, ExpiresAt: t0.Add(72 * time.Hour),
		Purpose: "try bedrock", RequestedBy: "anna@example.com", ApprovedBy: "lead@example.com", Managed: true,
	}
	var buf bytes.Buffer
	if err := accountPage(page{Now: t0}, viewFor(a), spendView{Known: true}, historyView{}, "").Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	inOrder(t, "the facts", out,
		"<dt>Owner</dt><dd>anna@example.com</dd>",
		`<dt>Account ID</dt><dd><code>111111111111</code> <button type="button" class="copy-id" data-copy="111111111111">Copy ID</button></dd>`,
		"<dt>Purpose</dt><dd>try bedrock</dd>",
		"<dt>Requested by</dt><dd>anna@example.com</dd>",
		"<dt>Approved by</dt><dd>lead@example.com</dd>",
		"<dt>Created</dt><dd>2026-09-15 12:00 UTC</dd>",
		`<dt>Due</dt><dd>2026-09-18 12:00 UTC <span class="muted">(in 3d)</span></dd>`,
		"<dt>Lifetime</dt><dd>3 days",
		"<dt>Monthly budget</dt><dd>$50.00</dd>",
		`<dt>Budget protection</dt><dd><span class="status budget-`,
		"<dt>Root email</dt><dd>a@example.com</dd>",
		`</dl>`, `<p class="help">Creation request ID: car-123</p>`)
	for _, bad := range []string{"<dt>failure</dt>", "<dt>placement</dt>", "<dt>request id</dt>", "spend this month"} {
		if strings.Contains(out, bad) {
			t.Errorf("the facts hold %s", bad)
		}
	}
	a.Purpose, a.RequestedBy, a.ApprovedBy, a.RequestID = "", "", "", ""
	buf.Reset()
	if err := accountPage(page{Now: t0}, viewFor(a), spendView{Known: true}, historyView{}, "").Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	out = buf.String()
	for _, fact := range []string{"Purpose", "Requested by", "Approved by"} {
		if want := "<dt>" + fact + `</dt><dd><span class="muted">not recorded</span></dd>`; !strings.Contains(out, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(out, "Creation request ID") {
		t.Error("an account with no creation request shows the line")
	}
}

// The page shows the card and the facts, then the budget change of an
// admin, the history and the daily spend.
func TestAccountPageOrder(t *testing.T) {
	ts, _ := newServer(t)
	_, body := do(t, ts, "GET", "/accounts/111111111111", "ops@example.com", true, nil, false)
	inOrder(t, "the account page", body,
		`<div id="account-error" class="refusal"></div>`,
		`<div class="loan loan-page">`,
		`<h2 id="facts-title">Details</h2>`,
		`<h2 id="budget-title">Change monthly budget</h2>`,
		`<h2 id="history-title">History</h2>`,
		`<h2 id="spend-title">Daily spend</h2>`,
		`<dialog id="close-dialog"`)
}

// Copy ID is a button with the ID in data-copy. It needs script, so the
// stylesheet shows it only under html.js. One click listener copies the
// ID and says so in the live region, and a browser with no clipboard gets
// a note, so nothing breaks. An account with no AWS account ID has none.
func TestCopyID(t *testing.T) {
	js := staticText(t, "app.js")
	var copies []string
	for _, l := range listeners(js, "click") {
		if strings.Contains(l, "data-copy") {
			copies = append(copies, l)
		}
	}
	if len(copies) != 1 || !strings.Contains(copies[0], `closest("button[data-copy]")`) || !strings.Contains(copies[0], `copyID(button.getAttribute("data-copy"))`) {
		t.Fatalf("app.js does not have one click listener for Copy ID: %q", copies)
	}
	inOrder(t, "copyID", funcBody(t, js, "function copyID(id) {"),
		"var clip = navigator.clipboard;",
		`if (!id || !clip || typeof clip.writeText !== "function") {`, "say(copyFailed);", "return;",
		"clip.writeText(id).then(function () {",
		`tell("Copied " + id + ".");`,
		"say(copyFailed);")
	top, _ := layerRules(t, "components")
	wantDecls(t, "components", top, ".copy-id", map[string]string{"display": "none"})
	wantDecls(t, "components", top, "html.js .copy-id", map[string]string{"display": "inline-flex"})

	for _, status := range []core.Status{core.StatusCreating, core.StatusFailed} {
		var buf bytes.Buffer
		a := &core.Account{ID: "x", Name: "x", Status: status, CreatedAt: t0}
		if err := accountPage(page{}, viewFor(a), spendView{Known: true}, historyView{}, "").Render(context.Background(), &buf); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(buf.String(), "data-copy") {
			t.Errorf("%s: an account with no AWS account ID has Copy ID", status)
		}
	}
}

// Close takes its own button away on the account page, and a refresh can
// take a control away too. The focus then goes to the page heading, as it
// does after a dialog or the key list whose control left. A viewer who
// moved on keeps their place.
func TestAccountPageFocusFallsToTheHeading(t *testing.T) {
	js := staticText(t, "app.js")
	holdsEach(t, "inAccount", funcBody(t, js, "function inAccount(el) {"), `el.closest("#account")`)
	holdsEach(t, "focusAccount", funcBody(t, js, "function focusAccount() {"), `document.getElementById("account-title")`, "title.focus();")
	note := funcBody(t, js, "function notePlace(xhr, source) {")
	inOrder(t, "notePlace", note, "if (inAccount(active)) {", "account: true,", "!!focusNext[act] && inAccount(source)", "return;",
		"active.closest(slipRows + \", \" + fleetRows)")
	settle := listeners(js, "htmx:afterSettle")
	if len(settle) != 1 {
		t.Fatalf("app.js has %d htmx:afterSettle listeners, want 1", len(settle))
	}
	inOrder(t, "the focus rule", settle[0], "if (active && active !== document.body && !ended) {", "if (was.account) {", "focusAccount() && was.acted", "repeatRegion(was.said);")
	submit := listeners(js, "submit")
	if len(submit) != 1 {
		t.Fatalf("app.js has %d submit listeners, want 1", len(submit))
	}
	holdsEach(t, "the submit listener", submit[0], "if (!target && close) {\n      target = document.getElementById(\"account-title\");")
	holdsEach(t, "openKeys", funcBody(t, js, "function openKeys() {"), "account: inAccount(active)")
	inOrder(t, "keysClosed", funcBody(t, js, "function keysClosed() {"), "document.body.contains(from.el)", "if (from.account) {", "focusAccount();", "if (!from.rows) {")
}

// Only ? works as a key on the account page: it has no list for j and k,
// no search and no slips.
func TestAccountPageKeys(t *testing.T) {
	pg := polling(page{Path: "/accounts/111111111111", Viewer: Viewer{Admin: true, Approver: true}})
	got := keyList(pg)
	if len(got) != 1 || !slices.Equal(got[0].Keys, []string{"?"}) {
		t.Errorf("keyList on the account page = %+v, want only ?", got)
	}
	ts, _ := newServer(t)
	_, body := do(t, ts, "GET", "/accounts/111111111111", "ops@example.com", true, nil, false)
	keys := between(body, `<dl class="key-list">`, "</dl>")
	if n := strings.Count(keys, "<kbd>"); n != 1 || !strings.Contains(keys, "<kbd>?</kbd>") {
		t.Errorf("the key list of the account page holds %d keys:\n%s", n, keys)
	}
}

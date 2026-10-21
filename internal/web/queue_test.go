package web

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"

	"playplace/internal/audit"
	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

// renderPart renders one component of the page with no request around it.
func renderPart(t *testing.T, c templ.Component) string {
	t.Helper()
	var out strings.Builder
	if err := c.Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// slipOrder returns the names of every slip in body, in page order.
func slipOrder(body string) []string {
	var names []string
	for _, m := range slipStart.FindAllStringSubmatch(body, -1) {
		names = append(names, m[1])
	}
	return names
}

// between returns the part of s after from and before to, or "" when s
// does not hold both in that order.
func between(s, from, to string) string {
	_, rest, ok := strings.Cut(s, from)
	if !ok {
		return ""
	}
	part, _, ok := strings.Cut(rest, to)
	if !ok {
		return ""
	}
	return part
}

// The approval queue is a card-stock desk. The bulk bar holds the region and
// the refusal slot, and the slips come oldest first. A slip renders closed,
// with every deciding fact before the actions and the tools after the note.
// Its forms post the version and the values that the slip shows.
func TestQueueIsADeskOfSlips(t *testing.T) {
	d := newDesk(t, nil, nil, nil)
	d.ask(t, "older-box", "bob@example.com")
	d.ask(t, "newer-box", "carl@example.com")
	_, home := doAs(t, d.ts, "GET", "/", "lead@example.com", false, true, nil, false)
	head := `<section id="pending" class="panel desk" aria-labelledby="pending-title"><div class="panel-head"><h2 id="pending-title" tabindex="-1">Approval queue</h2><p class="panel-line">2 requests waiting for a decision, oldest first. playplace drops requests after 7 days.</p></div><div id="desk-bar" class="desk-bar">` +
		emptyRegion + `<div id="pending-error" class="refusal"></div></div><ol class="slips" role="list">`
	if !strings.Contains(home, head) {
		t.Fatalf("the home page lacks the desk head and its bar:\n%s", home)
	}
	if got := slipOrder(home); !slices.Equal(got, []string{"older-box", "newer-box"}) {
		t.Errorf("the slips come in the order %v, want the oldest first", got)
	}
	slip := slipIn(home, "older-box")
	start := `<li id="req-older-box" class="slip" data-state="pending"><span class="slip-pick"></span><div class="slip-main"><div class="slip-top"><h3 class="slip-title"><button type="button" id="name-older-box" class="slip-name" aria-expanded="false" aria-controls="details-older-box">`
	if !strings.HasPrefix(slip, start) {
		t.Errorf("the slip does not start with its closed name button:\n%s", slip)
	}
	ref := refIn(t, home, "older-box")
	for _, want := range []string{`<div class="slip-details" id="details-older-box" hidden>`, `<p class="slip-version">version <code>` + versionLabel(ref.Version) + `</code></p>`} {
		if !strings.Contains(slip, want) {
			t.Errorf("the slip lacks %s:\n%s", want, slip)
		}
	}
	// lead has neither Edit nor Withdraw on the request of bob.
	if strings.Contains(slip, "slip-tools") {
		t.Errorf("the slip holds tools that lead does not have:\n%s", slip)
	}
	if i, j := strings.Index(slip, `class="slip-details"`), strings.Index(slip, `class="slip-actions"`); i < 0 || j < i {
		t.Errorf("the details do not come before the actions:\n%s", slip)
	}
	want := url.Values{
		"version":                 {ref.Version},
		"shown-owner-older-box":   {"bob@example.com"},
		"shown-ttl-older-box":     {"336"},
		"shown-budget-older-box":  {"50"},
		"shown-purpose-older-box": {""},
		"shown-at-older-box":      {"2026-09-15T12:00:00Z"},
	}
	if got := slipForm(t, home, "older-box", "approve"); !reflect.DeepEqual(got, want) {
		t.Errorf("the approve form posts %v, want %v", got, want)
	}
	if got := slipForm(t, home, "older-box", "deny"); !reflect.DeepEqual(got, want) {
		t.Errorf("the deny form posts %v, want %v", got, want)
	}
	section := between(home, `<section id="pending"`, "</section>")
	if strings.Contains(home, `<table class="pending">`) || strings.Contains(section, "<td>") {
		t.Errorf("the queue is still a table:\n%s", section)
	}

	// An admin also gets Edit and Withdraw, after the note.
	_, admin := do(t, d.ts, "GET", "/", "ops@example.com", true, nil, false)
	slip = slipIn(admin, "older-box")
	for _, want := range []string{`aria-controls="details-older-box tools-older-box"`, `<div class="slip-tools" id="tools-older-box" hidden>`} {
		if !strings.Contains(slip, want) {
			t.Errorf("the slip of an admin lacks %s:\n%s", want, slip)
		}
	}
	if i, j := strings.Index(slip, `class="slip-actions"`), strings.Index(slip, `class="slip-tools"`); i < 0 || j < i {
		t.Errorf("the tools do not come after the actions:\n%s", slip)
	}
}

// The head line says how many requests wait and when playplace drops them,
// with the singular and the zero forms.
func TestQueueHeadLineForms(t *testing.T) {
	line := regexp.MustCompile(`<p class="panel-line">(.*?)</p>`)
	queue := func(n int) []core.Request {
		var out []core.Request
		for i := range n {
			out = append(out, core.Request{Name: fmt.Sprintf("box-%c", 'a'+i), Owner: "bob@example.com", RequestedBy: "bob@example.com", Via: "web", TTL: 24 * time.Hour, BudgetUSD: 50, RequestedAt: t0})
		}
		return out
	}
	cfg := core.DefaultConfig()
	short := cfg
	short.RequestTTL = 36 * time.Hour
	never := cfg
	never.RequestTTL = 0
	approver := Viewer{Email: "lead@example.com", Approver: true}
	for _, c := range []struct {
		name string
		pg   page
		desk bool
		want string
	}{
		{"none", page{Viewer: approver, Limits: cfg}, true, "playplace drops requests after 7 days."},
		{"one", page{Viewer: approver, Limits: cfg, Pending: queue(1)}, true, "1 request waiting for a decision. playplace drops requests after 7 days."},
		{"three", page{Viewer: approver, Limits: cfg, Pending: queue(3)}, true, "3 requests waiting for a decision, oldest first. playplace drops requests after 7 days."},
		{"36 hours", page{Viewer: approver, Limits: short, Pending: queue(1)}, true, "1 request waiting for a decision. playplace drops requests after 36 hours."},
		{"failed", page{Viewer: approver, Limits: cfg, PendingFailed: true}, true, "playplace drops requests after 7 days."},
		{"never dropped", page{Viewer: approver, Limits: never, Pending: queue(2)}, true, "2 requests waiting for a decision, oldest first."},
		{"engineer", page{Viewer: Viewer{Email: "bob@example.com"}, Limits: cfg, Pending: queue(2)}, false, "2 requests waiting for an approver, oldest first. playplace drops requests after 7 days."},
	} {
		part := approvalDesk(c.pg, false)
		if !c.desk {
			part = ownRequests(c.pg, false)
		}
		if m := line.FindStringSubmatch(renderPart(t, part)); m == nil || m[1] != c.want {
			t.Errorf("%s: the head line is %v, want %q", c.name, m, c.want)
		}
	}
	if out := renderPart(t, approvalDesk(page{Viewer: approver, Limits: never}, false)); strings.Contains(out, `class="panel-line"`) {
		t.Errorf("an empty queue that playplace never drops has a head line:\n%s", out)
	}
}

// An empty desk is a note on card stock. It names Slack only when new
// requests show there.
func TestEmptyQueueSaysWhereNewRequestsShow(t *testing.T) {
	const slackLine = "New requests appear here and in Slack."
	approver := Viewer{Email: "lead@example.com", Approver: true}
	for _, inSlack := range []bool{false, true} {
		out := renderPart(t, approvalDesk(page{Viewer: approver, Limits: core.DefaultConfig(), InSlack: inSlack}, false))
		if !strings.Contains(out, `<div id="pending-empty" class="desk-empty">`) || !strings.Contains(out, "No requests are waiting.") {
			t.Errorf("the empty desk lacks its note:\n%s", out)
		}
		if strings.Contains(out, slackLine) != inSlack {
			t.Errorf("InSlack %v: the Slack line shows %v:\n%s", inSlack, !inSlack, out)
		}
	}

	prov := fake.New()
	prov.Now = func() time.Time { return t0 }
	cfg := core.DefaultConfig()
	cfg.InventoryTTL = 0
	svc := core.NewService(prov, nil, cfg, nil)
	svc.Now = func() time.Time { return t0 }
	srv := New(svc, slog.New(slog.DiscardHandler), headerAuth{}, nil, &audit.Memory{})
	srv.SetNoticesInSlack(true)
	slacked := httptest.NewServer(srv.Handler())
	t.Cleanup(slacked.Close)
	if _, home := doAs(t, slacked, "GET", "/", "lead@example.com", false, true, nil, false); !strings.Contains(home, slackLine) {
		t.Errorf("a server with notices in Slack does not say so:\n%s", home)
	}
	ts, _ := newServer(t)
	if _, home := doAs(t, ts, "GET", "/", "lead@example.com", false, true, nil, false); strings.Contains(home, slackLine) {
		t.Errorf("a server without Slack names Slack:\n%s", home)
	}
}

// A queue that did not load keeps the region and the refusal slot in their
// place, and says that it did not load. An empty list would read as an
// empty queue.
func TestFailedQueueKeepsTheBarAndTheRegion(t *testing.T) {
	failed := `<div id="pending-failed" class="refusal">` + alertIcon + `<div class="note-text"><p class="note-title">The pending requests are unavailable.</p><p class="note-body">The page tries again with the next live update.</p></div></div>`
	for _, c := range []struct {
		name string
		part templ.Component
		want string
	}{
		{"desk", approvalDesk(page{Viewer: Viewer{Approver: true}, Limits: core.DefaultConfig(), PendingFailed: true}, false),
			`<div id="desk-bar" class="desk-bar">` + emptyRegion + `<div id="pending-error" class="refusal"></div></div>` + failed},
		{"your requests", ownRequests(page{Viewer: Viewer{Email: "bob@example.com"}, Limits: core.DefaultConfig(), PendingFailed: true}, false),
			emptyRegion + `<div id="pending-error" class="refusal"></div>` + failed},
	} {
		out := renderPart(t, c.part)
		if !strings.Contains(out, c.want) {
			t.Errorf("%s: the failed queue lacks its bar and note:\n%s", c.name, out)
		}
		if strings.Contains(out, `<ol class="slips"`) || strings.Contains(out, "No requests are waiting.") || strings.Contains(out, "You have no requests waiting.") {
			t.Errorf("%s: the failed queue reads as a list:\n%s", c.name, out)
		}
	}
}

// Anyone who is not an approver gets Your requests: plain rows on paper with
// the region under the head. A row shows Withdraw and when playplace drops
// the request. It has no name button and no decision.
func TestYourRequestsForAnEngineer(t *testing.T) {
	ts, _ := newServer(t)
	if res, body := do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("submit = %s", body)
	}
	_, home := do(t, ts, "GET", "/", "bob@example.com", false, nil, false)
	for _, want := range []string{
		`<section id="pending" class="panel" aria-labelledby="pending-title">`,
		`<h2 id="pending-title" tabindex="-1">Your requests</h2>`,
		`<ol class="slips" role="list">`,
	} {
		if !strings.Contains(home, want) {
			t.Errorf("the home page of bob lacks %s:\n%s", want, home)
		}
	}
	if !regionInQueue(home) {
		t.Errorf("Your requests does not hold the region under its head:\n%s", home)
	}
	slip := slipIn(home, "bobs-box")
	for _, want := range []string{
		`<h3 class="slip-title" tabindex="-1"><span class="slip-name">bobs-box</span></h3>`,
		`<form class="inline" hx-post="/requests/bobs-box/withdraw"`,
		`<p class="slip-meta">An approver must decide by <time datetime="2026-09-22T12:00:00Z">Sep 22, 12:00 UTC</time>, or playplace drops the request.</p>`,
	} {
		if !strings.Contains(slip, want) {
			t.Errorf("the row of bobs-box lacks %s:\n%s", want, slip)
		}
	}
	for _, bad := range []string{`class="slip-name" aria-expanded`, "/approve", "desk-bar"} {
		if strings.Contains(home, bad) {
			t.Errorf("the home page of bob holds %s:\n%s", bad, home)
		}
	}
	if _, anna := do(t, ts, "GET", "/", "anna@example.com", false, nil, false); !strings.Contains(anna, "You have no requests waiting.") {
		t.Errorf("the home page of anna does not say that nothing waits:\n%s", anna)
	}
}

// The opened slip holds every fact that decides the request. Admins also see
// what else the owner holds, and Edit and Withdraw.
func TestOpenedSlipHoldsEveryDecidingFact(t *testing.T) {
	ts, _ := newServer(t)
	if res, body := do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}, "purpose": {"try bedrock"}}, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("submit = %s", body)
	}
	_, admin := do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	slip := slipIn(admin, "bobs-box")
	for _, want := range []string{
		`<dt>Requested</dt><dd><time datetime="2026-09-15T12:00:00Z">Sep 15, 12:00 UTC</time></dd>`,
		`<dt>Lifetime</dt><dd>14 days</dd>`,
		`<dt>Budget</dt><dd>$50 a month</dd>`,
		`<dt>Limits check</dt><dd>Within the ceilings of 90 days and $500 a month.</dd>`,
		`<dt>Per-owner limit</dt><dd>At the per-owner limit of 1.</dd>`,
		`<dt>Owner also holds</dt><dd>Nothing else.</dd>`,
		`<dt>Purpose</dt><dd>try bedrock</dd>`,
		`<dt>Decide by</dt><dd><time datetime="2026-09-22T12:00:00Z">Sep 22, 12:00 UTC</time></dd>`,
	} {
		if !strings.Contains(slip, want) {
			t.Errorf("the opened slip lacks %s:\n%s", want, slip)
		}
	}
	tools := between(slip, `<div class="slip-tools" id="tools-bobs-box" hidden>`, `</div>`+"</li>")
	if !strings.Contains(tools, `data-action="edit"`) || !strings.Contains(tools, "/requests/bobs-box/withdraw") {
		t.Errorf("the tools of an admin lack Edit or Withdraw:\n%s", slip)
	}
	_, lead := doAs(t, ts, "GET", "/", "lead@example.com", false, true, nil, false)
	slip = slipIn(lead, "bobs-box")
	for _, bad := range []string{"Owner also holds", `data-action="edit"`, "/requests/bobs-box/withdraw", "slip-tools"} {
		if strings.Contains(slip, bad) {
			t.Errorf("the slip of an approver who is not an admin holds %s:\n%s", bad, slip)
		}
	}
}

// An approver who requested a request sees the chip in place of Approve and
// Deny. A hidden sentence after the chip and the opened slip say why, because
// a title never shows on a touch screen or to a keyboard.
func TestOwnRequestSaysWhy(t *testing.T) {
	ts, _ := newServer(t)
	if res, body := do(t, ts, "POST", "/requests", "lead@example.com", false, url.Values{"name": {"lead-box"}}, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("submit = %s", body)
	}
	_, home := doAs(t, ts, "GET", "/", "lead@example.com", false, true, nil, false)
	slip := slipIn(home, "lead-box")
	if want := `<div class="row-actions"><span class="chip-mine">Your request</span><span class="vh"> Another approver must decide this request.</span></div>`; !strings.Contains(slip, want) {
		t.Errorf("the own slip lacks the chip and its sentence:\n%s", slip)
	}
	details := between(slip, `<div class="slip-details"`, `<div class="slip-actions">`)
	if !strings.Contains(details, `<p class="slip-own">Another approver must decide this request.</p>`) {
		t.Errorf("the opened slip does not say why:\n%s", slip)
	}
	if strings.Contains(slip, "/approve") || strings.Contains(slip, `<span class="chip-mine" title=`) {
		t.Errorf("the own slip holds Approve or a chip title:\n%s", slip)
	}
	_, admin := do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	if slip := slipIn(admin, "lead-box"); !strings.Contains(slip, "/approve") || strings.Contains(slip, "slip-own") {
		t.Errorf("another approver cannot decide lead-box:\n%s", slip)
	}
}

// The texts of a slip and of its opened details.
func TestSlipTexts(t *testing.T) {
	cfg := core.DefaultConfig()
	within := core.Request{Name: "box", Owner: "bob@example.com", TTL: 14 * 24 * time.Hour, BudgetUSD: 50}
	big := core.Request{Name: "box", Owner: "bob@example.com", TTL: 120 * 24 * time.Hour, BudgetUSD: 900}
	bigOverride := big
	bigOverride.OverrideLimits = true
	longOnly := within
	longOnly.TTL = 120 * 24 * time.Hour
	noBudget, noTTL, none := cfg, cfg, cfg
	noBudget.MaxBudgetUSD = 0
	noTTL.MaxTTL = 0
	none.MaxTTL, none.MaxBudgetUSD = 0, 0
	for _, c := range []struct {
		q    core.Request
		cfg  core.Config
		want string
	}{
		{within, cfg, "Within the ceilings of 90 days and $500 a month."},
		{within, noBudget, "Within the ceiling of 90 days."},
		{within, noTTL, "Within the ceiling of $500 a month."},
		{big, none, "No ceilings apply."},
		{bigOverride, cfg, "An admin overrode the limits. 120 days is over the ceiling of 90 days, and $900 a month is over the ceiling of $500 a month."},
		{longOnly, cfg, "120 days is over the ceiling of 90 days. Approval checks the ceilings again and can refuse this request."},
	} {
		if got := limitsCheck(c.q, c.cfg); got != c.want {
			t.Errorf("limitsCheck(%s, $%v) = %q, want %q", core.Lifetime(c.q.TTL), c.q.BudgetUSD, got, c.want)
		}
	}

	one, two, off := cfg, cfg, cfg
	two.MaxPerOwner = 2
	off.MaxPerOwner = 0
	q := core.Request{Name: "box", Owner: "Bob@Example.com"}
	holds := func(names ...string) map[string][]string { return map[string][]string{"bob@example.com": names} }
	for _, c := range []struct {
		name string
		pg   page
		want string
	}{
		{"no limit", page{Limits: off, Holds: holds("box")}, "No per-owner limit applies."},
		{"failed", page{Limits: one, HoldsFailed: true}, "Unknown, because the account list is unavailable."},
		{"within", page{Limits: two, Holds: holds("box")}, "Within the per-owner limit of 2."},
		{"at", page{Limits: one, Holds: holds("box")}, "At the per-owner limit of 1."},
		{"at, before the request", page{Limits: one}, "At the per-owner limit of 1."},
		{"over, before the request", page{Limits: one, Holds: holds("old-box")}, "Over the per-owner limit of 1."},
	} {
		if got := ownerStanding(c.pg, q); got != c.want {
			t.Errorf("ownerStanding %s = %q, want %q", c.name, got, c.want)
		}
	}
	for _, c := range []struct {
		name string
		pg   page
		want string
	}{
		{"others", page{Holds: holds("box", "old-box", "try-box")}, "old-box, try-box"},
		{"none", page{Holds: holds("box")}, "Nothing else."},
		{"failed", page{HoldsFailed: true}, "Unknown, because the account list is unavailable."},
	} {
		if got := otherHoldings(c.pg, q); got != c.want {
			t.Errorf("otherHoldings %s = %q, want %q", c.name, got, c.want)
		}
	}
	if got := ownerHolds([]*core.Account{
		{Name: "try-box", Owner: "Bob@example.com", Status: core.StatusActive},
		{Name: "box", Owner: "bob@example.com", Status: core.StatusPending},
		{Name: "gone-box", Owner: "bob@example.com", Status: core.StatusClosed},
		{Name: "bad-box", Owner: "bob@example.com", Status: core.StatusFailed},
		{Name: "annas-box", Owner: "anna@example.com", Status: core.StatusExpiring},
	}); !reflect.DeepEqual(got, map[string][]string{"bob@example.com": {"box", "try-box"}, "anna@example.com": {"annas-box"}}) {
		t.Errorf("ownerHolds = %v", got)
	}

	self := core.Request{Owner: "bob@example.com", RequestedBy: "Bob@example.com", Via: "web", RequestedAt: t0}
	other := core.Request{Owner: "bob@example.com", RequestedBy: "lead@example.com", Via: "web", RequestedAt: t0.In(time.FixedZone("EDT", -4*60*60))}
	for _, c := range []struct {
		v    Viewer
		q    core.Request
		want string
	}{
		{Viewer{Email: "lead@example.com"}, self, "Owner and requester bob@example.com, via web, Sep 15, 12:00 UTC"},
		{Viewer{Email: "ops@example.com"}, other, "Owner bob@example.com, requested by lead@example.com via web, Sep 15, 12:00 UTC"},
		{Viewer{Email: "Lead@example.com"}, other, "Owner bob@example.com, requested by you via web, Sep 15, 12:00 UTC"},
	} {
		if got := metaLine(c.v, c.q); got != c.want {
			t.Errorf("metaLine(%s) = %q, want %q", c.v.Email, got, c.want)
		}
	}

	for v, want := range map[string]string{"01ABC.0123456789abcdef": "012345", ".abc": "abc", "abcdefgh": "abcdef"} {
		if got := versionLabel(v); got != want {
			t.Errorf("versionLabel(%q) = %q, want %q", v, got, want)
		}
	}

	pending := []core.Request{{Name: "box-a"}, {Name: "box-b"}, {Name: "box-c"}}
	never := cfg
	never.RequestTTL = 0
	for _, c := range []struct {
		pg   page
		what string
		want string
	}{
		{page{Limits: cfg, Pending: pending}, "a decision", "3 requests waiting for a decision, oldest first. playplace drops requests after 7 days."},
		{page{Limits: cfg, Pending: pending[:1]}, "an approver", "1 request waiting for an approver. playplace drops requests after 7 days."},
		{page{Limits: cfg}, "a decision", "playplace drops requests after 7 days."},
		{page{Limits: cfg, PendingFailed: true}, "a decision", "playplace drops requests after 7 days."},
		{page{Limits: never}, "a decision", ""},
	} {
		if got := countLine(c.pg, c.what); got != c.want {
			t.Errorf("countLine(%d pending) = %q, want %q", len(c.pg.Pending), got, c.want)
		}
	}

	for _, c := range []struct {
		action  string
		changes []string
		want    string
	}{
		{actionApprove, []string{"lifetime 14 days to 12 days", "budget $20 to $400"}, "Not approved. tweak changed after you loaded the page: lifetime 14 days to 12 days, budget $20 to $400. Read the new values, then approve again."},
		{actionApprove, nil, "Not approved. tweak changed after you loaded the page. Read the new values, then approve again."},
		{actionDeny, []string{"the purpose"}, "Not denied. tweak changed after you loaded the page: the purpose. Read the new values, then deny again."},
		{actionDeny, nil, "Not denied. tweak changed after you loaded the page. Read the new values, then deny again."},
	} {
		if got := staleNote(c.action, "tweak", c.changes); got != c.want {
			t.Errorf("staleNote(%s, %v) = %q, want %q", c.action, c.changes, got, c.want)
		}
	}
	if staleText(actionApprove, "tweak") != staleNote(actionApprove, "tweak", nil) || staleText(actionDeny, "tweak") != staleNote(actionDeny, "tweak", nil) {
		t.Error("staleText of the tests and staleNote disagree")
	}

	at := t0.Add(1500 * time.Millisecond)
	full := url.Values{
		"shown-owner-box":   {"bob@example.com"},
		"shown-ttl-box":     {"36"},
		"shown-budget-box":  {"12.5"},
		"shown-purpose-box": {"try bedrock"},
		"shown-at-box":      {at.Format(time.RFC3339Nano)},
	}
	with := func(key string, values ...string) url.Values {
		form := url.Values{}
		for k, v := range full {
			form[k] = slices.Clone(v)
		}
		if values == nil {
			form.Del(key)
		} else {
			form[key] = values
		}
		return form
	}
	got := readShown(full, "box")
	if !got.At.Equal(at) {
		t.Errorf("readShown(all five) reads the time %s, want %s", got.At, at)
	}
	got.At = time.Time{}
	if want := (shownValues{Owner: "bob@example.com", TTL: 36 * time.Hour, BudgetUSD: 12.5, Purpose: "try bedrock", ok: true}); got != want {
		t.Errorf("readShown(all five) = %+v, want %+v", got, want)
	}
	for name, form := range map[string]url.Values{
		"no owner":          with("shown-owner-box"),
		"no purpose":        with("shown-purpose-box"),
		"no time":           with("shown-at-box"),
		"a bad lifetime":    with("shown-ttl-box", "1.5"),
		"a zero lifetime":   with("shown-ttl-box", "0"),
		"a huge lifetime":   with("shown-ttl-box", fmt.Sprint(maxShownHours+1)),
		"a bad budget":      with("shown-budget-box", "NaN"),
		"a negative budget": with("shown-budget-box", "-5"),
		"a bad time":        with("shown-at-box", "yesterday"),
		"two owners":        with("shown-owner-box", "bob@example.com", "anna@example.com"),
	} {
		if got := readShown(form, "box"); got.ok {
			t.Errorf("readShown(%s) = %+v, want values that are not ok", name, got)
		}
	}
	if got := readShown(full, "other-box"); got.ok {
		t.Errorf("readShown reads the fields of box for other-box: %+v", got)
	}

	shown := shownValues{Owner: "bob@example.com", TTL: 14 * 24 * time.Hour, BudgetUSD: 20, Purpose: "old", ok: true}
	now := core.Request{Owner: "anna@example.com", TTL: 12 * 24 * time.Hour, BudgetUSD: 400, Purpose: " new "}
	if got, want := shown.changes(now), []string{"owner bob@example.com to anna@example.com", "lifetime 14 days to 12 days", "budget $20 to $400", "the purpose"}; !slices.Equal(got, want) {
		t.Errorf("changes = %q, want %q", got, want)
	}
	same := core.Request{Owner: "bob@example.com", TTL: 14*24*time.Hour - time.Minute, BudgetUSD: 20, Purpose: " old"}
	if got := shown.changes(same); len(got) != 0 {
		t.Errorf("changes of the same values = %q, want none", got)
	}
	shown.ok = false
	if got := shown.changes(now); got != nil {
		t.Errorf("changes without shown values = %q, want nil", got)
	}

	for _, c := range []struct {
		v    Viewer
		want string
	}{
		{Viewer{Email: "lead@example.com", Approver: true}, goneText("gone-box")},
		{Viewer{Email: "bob@example.com"}, "gone-box is not in Your requests. It is not yours, or it is no longer waiting."},
	} {
		if got := missingText(c.v, "gone-box"); got != c.want {
			t.Errorf("missingText(approver %v) = %q, want %q", c.v.Approver, got, c.want)
		}
	}
}

// A stamp keeps its turn wherever its slip moves, because the turn comes
// from the name.
func TestStampTurnIsStable(t *testing.T) {
	seen := map[string]bool{}
	for c := 'a'; c <= 'z'; c++ {
		key := string(c)
		turn := stampTurn(key)
		if turn != stampTurn(key) {
			t.Errorf("stampTurn(%q) changes", key)
		}
		if turn != "turn-1" && turn != "turn-2" && turn != "turn-3" {
			t.Errorf("stampTurn(%q) = %q", key, turn)
		}
		seen[turn] = true
	}
	if len(seen) != 3 {
		t.Errorf("the keys a to z give only %v", seen)
	}
}

// The queue merges the requests that wait with the results of a decision
// post, each in its place by the time of the request. A result with no
// place goes to the bar.
func TestSlipsMergeTheQueueAndTheDecided(t *testing.T) {
	at := func(minutes int) time.Time { return t0.Add(time.Duration(minutes) * time.Minute) }
	req := func(name string, minutes int) core.Request {
		return core.Request{Name: name, Owner: name + "@example.com", RequestedBy: name + "@example.com", Via: "web", TTL: 24 * time.Hour, BudgetUSD: 50, RequestedAt: at(minutes)}
	}
	gone := func(name string, shown shownValues) decision {
		d := undecided(actionApprove, core.RequestRef{Name: name, Version: "01OLD.v1"}, core.ErrNotFound)
		d.Action, d.Shown = actionApprove, shown
		return d
	}
	b := req("b", 1)
	pg := page{
		Viewer:  Viewer{Email: "lead@example.com", Approver: true},
		Limits:  core.DefaultConfig(),
		Pending: []core.Request{req("a", 0), req("c", 2), req("z", 4)},
		Results: []decision{
			{Ref: b.Ref(), Kind: decidedApproved, Action: actionApprove, Req: b},
			gone("x", shownValues{}),
			gone("y", shownValues{ok: true, At: at(3)}),
			gone("z", shownValues{ok: true, At: at(0)}),
			{Ref: core.RequestRef{Name: "a", Version: "01A.v1"}, Kind: decisionOwn, Action: actionApprove, Why: ownRequest(actionApprove, false)},
		},
	}
	own := "Not approved. You requested it, so another approver must approve it."
	slips := pg.slips()
	var names []string
	for _, s := range slips {
		names = append(names, s.Req.Name)
	}
	if !slices.Equal(names, []string{"a", "b", "c", "y", "z"}) {
		t.Fatalf("slips = %v, want a, b, c, y, z", names)
	}
	if s := slips[0]; s.Note != own || s.state() != "pending" || s.Result == nil || s.Result.Kind != decisionOwn {
		t.Errorf("the slip of a = %+v, want the own note", s)
	}
	if s := slips[1]; !s.decided() || s.state() != "decided" || s.Note != "" {
		t.Errorf("the slip of b = %+v, want a decided slip", s)
	}
	if s := slips[3]; !s.Gone || s.state() != "gone" || s.Note != goneText("y") {
		t.Errorf("the slip of y = %+v, want a gone slip", s)
	}
	if s := slips[4]; s.Result != nil || s.state() != "pending" || s.Note != "" {
		t.Errorf("the slip of z = %+v, want the request that waits now, with no result", s)
	}
	left := pg.left()
	if want := []leftItem{{"x", goneText("x")}, {"z", goneText("z")}}; !reflect.DeepEqual(left, want) {
		t.Errorf("left = %v, want %v", left, want)
	}
	for _, name := range []string{"a", "x", "y", "z"} {
		d, _ := pg.resultFor(name)
		want := goneText(name)
		if name == "a" {
			want = own
		}
		if got := pg.resultText(d); got != want {
			t.Errorf("resultText(%s) = %q, want %q", name, got, want)
		}
	}

	// A decided result whose name the queue lists gives no second slip.
	c := req("c", 2)
	listed := pg
	listed.Results = append(slices.Clone(pg.Results), decision{Ref: c.Ref(), Kind: decidedApproved, Action: actionApprove, Req: c})
	if got := len(listed.slips()); got != 5 {
		t.Errorf("a decided result for a listed name gives %d slips, want 5", got)
	}

	failed := pg
	failed.Pending, failed.PendingFailed = nil, true
	if got := failed.slips(); got != nil {
		t.Errorf("a queue that did not load has slips: %v", got)
	}
	want := []leftItem{{"x", goneText("x")}, {"y", goneText("y")}, {"z", goneText("z")}, {"a", "a: " + own}}
	if got := failed.left(); !reflect.DeepEqual(got, want) {
		t.Errorf("left of a failed queue = %v, want %v", got, want)
	}
}

// A request that the viewer approved or denied keeps a slip in its place
// with its stamp, until the next redraw of the queue.
func TestDecidedSlipKeepsItsPlace(t *testing.T) {
	d := newDesk(t, nil, nil, nil)
	d.ask(t, "box-a", "bob@example.com")
	d.ask(t, "box-b", "carl@example.com")
	d.ask(t, "box-c", "dina@example.com")
	_, home := doAs(t, d.ts, "GET", "/", "lead@example.com", false, true, nil, false)
	ref := refIn(t, home, "box-b")
	res, body := doAs(t, d.ts, "POST", "/requests/box-b/approve", "lead@example.com", false, true, slipForm(t, home, "box-b", "approve"), true)
	if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" || !queueAnswer(body) {
		t.Fatalf("approve = %d %v:\n%s", res.StatusCode, res.Header, body)
	}
	if got := slipOrder(body); !slices.Equal(got, []string{"box-a", "box-b", "box-c"}) {
		t.Errorf("the slips come in the order %v, want box-b in its place", got)
	}
	slip := slipIn(body, "box-b")
	if !strings.HasPrefix(slip, `<li id="req-box-b" class="slip" data-state="decided" data-result="approved">`) {
		t.Errorf("box-b is not a decided slip:\n%s", slip)
	}
	if word, turn := stampIn(body, "box-b"); word != "approved" || turn != stampTurn("box-b") {
		t.Errorf("the stamp of box-b = %q %q, want approved %s", word, turn, stampTurn("box-b"))
	}
	for _, want := range []string{"by you at version <code>" + versionLabel(ref.Version) + "</code>", "AWS is creating the account. This takes a few minutes."} {
		if !strings.Contains(slip, want) {
			t.Errorf("the decided slip lacks %s:\n%s", want, slip)
		}
	}
	if !strings.Contains(body, "2 requests waiting") || !strings.Contains(body, `>2<span class="vh"> pending requests`) {
		t.Errorf("the head line or the count counts the decided slip:\n%s", body)
	}

	// The next poll redraws the queue without the decided slip.
	res, polled := poll(t, d.ts, "/accounts", "lead@example.com", false, true, pollHashIn(t, body))
	if res.StatusCode != http.StatusOK || len(decidedIn(polled)) != 0 || slipIn(polled, "box-b") != "" {
		t.Errorf("the poll after the decision = %d, want 200 without box-b:\n%s", res.StatusCode, polled)
	}
	if res, _ := poll(t, d.ts, "/accounts", "lead@example.com", false, true, pollHashIn(t, polled)); res.StatusCode != http.StatusNoContent {
		t.Errorf("the poll after the redraw = %d, want 204", res.StatusCode)
	}

	deny := slipForm(t, polled, "box-c", "deny")
	deny.Set("reason", "no budget")
	_, body = doAs(t, d.ts, "POST", "/requests/box-c/deny", "lead@example.com", false, true, deny, true)
	if word, _ := stampIn(body, "box-c"); word != "denied" || !strings.Contains(slipIn(body, "box-c"), "Reason: no budget") {
		t.Errorf("the denied slip lacks its stamp or its reason:\n%s", body)
	}
}

// A request that changed after the page showed it stays in its slip, which
// names what changed and shows the old values struck.
func TestStaleSlipNamesWhatChanged(t *testing.T) {
	ts, _ := newServer(t)
	if res, body := do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"tweak"}, "budget": {"20"}}, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("submit = %s", body)
	}
	_, home := doAs(t, ts, "GET", "/", "lead@example.com", false, true, nil, false)
	old := refIn(t, home, "tweak")
	_, edit := do(t, ts, "GET", "/requests/tweak/edit", "ops@example.com", true, nil, true)
	saved := editFormValues(t, edit, "tweak")
	saved.Set("ttl", "12")
	saved.Set("budget", "400")
	if res, body := do(t, ts, "POST", "/requests/tweak/edit", "ops@example.com", true, saved, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("edit = %s", body)
	}

	res, body := doAs(t, ts, "POST", "/requests/tweak/approve", "lead@example.com", false, true, slipForm(t, home, "tweak", "approve"), true)
	if res.Header.Get("X-Form-Error") != "1" || res.Header.Get("HX-Reswap") != "" || !queueAnswer(body) {
		t.Fatalf("stale approve = %v, want the queue with X-Form-Error:\n%s", res.Header, body)
	}
	slip := slipIn(body, "tweak")
	if m := slipStart.FindStringSubmatch(slip); m == nil || m[2] != "pending" || m[3] != "changed" {
		t.Errorf("the slip of tweak is not marked changed:\n%s", slip)
	}
	for _, want := range []string{
		`<span class="flag flag-changed">changed after you loaded the page</span>`,
		`<span class="fact-was">was <s>14 days</s>`,
		`<span class="fact-was">was <s>$20</s>`,
		`<button type="submit" class="btn-approve" aria-describedby="result-tweak">`,
		`<button type="submit" aria-describedby="result-tweak">`,
		`</code>, you saw <s><code>` + versionLabel(old.Version) + `</code></s></p>`,
	} {
		if !strings.Contains(slip, want) {
			t.Errorf("the changed slip lacks %s:\n%s", want, slip)
		}
	}
	if lifetime, budget := factsIn(t, body, "tweak"); lifetime != "12 days" || budget != "$400" {
		t.Errorf("the changed slip shows %s and %s, want 12 days and $400", lifetime, budget)
	}
	note := "Not approved. tweak changed after you loaded the page: lifetime 14 days to 12 days, budget $20 to $400. Read the new values, then approve again."
	if got := resultIn(body, "tweak"); got != note {
		t.Errorf("the note = %q, want %q", got, note)
	}
	if m := hiddenCopy.FindStringSubmatch(body); m == nil || html.UnescapeString(m[1]) != note {
		t.Errorf("the region does not hold the note as a hidden copy:\n%s", body)
	}

	deny := slipForm(t, home, "tweak", "deny")
	deny.Set("reason", "no budget")
	_, body = doAs(t, ts, "POST", "/requests/tweak/deny", "lead@example.com", false, true, deny, true)
	if got, want := resultIn(body, "tweak"), "Not denied. tweak changed after you loaded the page: lifetime 14 days to 12 days, budget $20 to $400. Read the new values, then deny again."; got != want {
		t.Errorf("the deny note = %q, want %q", got, want)
	}
	_, body = doAs(t, ts, "POST", "/requests/tweak/approve", "lead@example.com", false, true, refForm(old), true)
	if got := resultIn(body, "tweak"); got != staleText(actionApprove, "tweak") {
		t.Errorf("the note for an older page = %q, want %q", got, staleText(actionApprove, "tweak"))
	}
}

// Every result shows beside its request. A request that left the queue
// keeps a gone slip in its old place when the page sent its time. Without
// the time, it gets a line in the bulk bar.
func TestResultsHaveAPlace(t *testing.T) {
	d := newDesk(t, nil, nil, nil)
	d.ask(t, "lead-box", "lead@example.com")
	d.ask(t, "gone-box", "bob@example.com")
	d.ask(t, "ok-box", "carl@example.com")
	_, home := doAs(t, d.ts, "GET", "/", "lead@example.com", false, true, nil, false)
	refs := d.refs(t, "lead-box", "gone-box", "ok-box")
	if res, body := do(t, d.ts, "POST", "/requests/gone-box/withdraw", "bob@example.com", false, refForm(refs[1]), true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("withdraw = %s", body)
	}
	form := decideForm("", refs...)
	for key, values := range slipForm(t, home, "gone-box", "approve") {
		if strings.HasPrefix(key, "shown-") {
			form[key] = values
		}
	}
	res, first := d.decide(t, "lead@example.com", true, actionApprove, form, true)
	if res.StatusCode != http.StatusOK || !queueAnswer(first) {
		t.Fatalf("decide = %d %v:\n%s", res.StatusCode, res.Header, first)
	}
	if got := decidedIn(first); !slices.Equal(got, []string{"ok-box"}) {
		t.Errorf("decidedIn = %v, want [ok-box]", got)
	}
	if got := goneIn(first); !slices.Equal(got, []string{"gone-box"}) {
		t.Errorf("goneIn = %v, want [gone-box]", got)
	}
	if got := slipOrder(first); !slices.Equal(got, []string{"lead-box", "gone-box", "ok-box"}) {
		t.Errorf("the slips come in the order %v, want gone-box in its place", got)
	}
	slip := slipIn(first, "gone-box")
	if !strings.HasPrefix(slip, `<li id="req-gone-box" class="slip" data-state="gone" data-result="gone">`) ||
		!strings.Contains(slip, `<h3 class="slip-title"><span class="slip-name">gone-box</span></h3>`) ||
		strings.Contains(slip, "<button") || strings.Contains(slip, "<form") {
		t.Errorf("gone-box is not a gone slip with no control:\n%s", slip)
	}
	if got := resultIn(first, "gone-box"); got != goneText("gone-box") {
		t.Errorf("the note of gone-box = %q", got)
	}
	if got, want := resultIn(first, "lead-box"), "Not approved. You requested it, so another approver must approve it."; got != want {
		t.Errorf("the note of lead-box = %q, want %q", got, want)
	}
	if got := undecidedIn(first); !slices.Equal(got, []string{"lead-box", "gone-box"}) {
		t.Errorf("undecidedIn = %v, want [lead-box gone-box]", got)
	}
	if !strings.Contains(first, `<div id="pending-error" class="refusal"></div>`) {
		t.Errorf("the bar holds a line, but every result has a place:\n%s", first)
	}

	// A post without the shown fields, as from an older page, gives the
	// request that left no place. It gets a line in the bar.
	res, body := d.decide(t, "lead@example.com", true, actionApprove, decideForm("", refs[1]), true)
	if res.Header.Get("X-Form-Error") != "1" || len(goneIn(body)) != 0 {
		t.Errorf("a gone result without its time = %v:\n%s", res.Header, body)
	}
	if want := `<span data-name="gone-box">` + goneText("gone-box") + `</span>`; !strings.Contains(body, want) {
		t.Errorf("the bar lacks %s:\n%s", want, body)
	}
	if m := hiddenCopy.FindStringSubmatch(body); m == nil || m[1] != goneText("gone-box") {
		t.Errorf("the region does not hold the bar line as a hidden copy:\n%s", body)
	}

	res, polled := poll(t, d.ts, "/accounts", "lead@example.com", false, true, pollHashIn(t, first))
	if res.StatusCode != http.StatusOK || len(goneIn(polled)) != 0 {
		t.Errorf("the poll after the decision = %d, want 200 with no gone slip:\n%s", res.StatusCode, polled)
	}
}

// The server renders every slip closed, except three. They are the slip that
// Cancel puts back, the slip that an admin saved, and the slip of the
// address. The poll hash leaves the open state out.
func TestASlipComesBackOpen(t *testing.T) {
	d := newDesk(t, nil, nil, nil)
	d.ask(t, "box-x", "bob@example.com")
	d.ask(t, "box-y", "carl@example.com")
	opened := []string{
		`aria-expanded="true" aria-controls="details-box-x tools-box-x"`,
		`<div class="slip-details" id="details-box-x">`,
		`<div class="slip-tools" id="tools-box-x">`,
	}
	_, row := do(t, d.ts, "GET", "/requests/box-x/row", "ops@example.com", true, nil, true)
	for _, want := range opened {
		if !strings.Contains(row, want) {
			t.Errorf("the slip that Cancel puts back lacks %s:\n%s", want, row)
		}
	}

	_, edit := do(t, d.ts, "GET", "/requests/box-x/edit", "ops@example.com", true, nil, true)
	saved := editFormValues(t, edit, "box-x")
	saved.Set("budget", "61")
	res, body := do(t, d.ts, "POST", "/requests/box-x/edit", "ops@example.com", true, saved, true)
	if res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("edit = %s", body)
	}
	for _, want := range append(opened, `<div class="slip-details" id="details-box-y" hidden>`) {
		if !strings.Contains(body, want) {
			t.Errorf("the answer to the save lacks %s:\n%s", want, body)
		}
	}

	_, home := do(t, d.ts, "GET", "/?req=box-x", "ops@example.com", true, nil, false)
	for _, want := range append(opened, `<div class="slip-details" id="details-box-y" hidden>`) {
		if !strings.Contains(home, want) {
			t.Errorf("the page of the address lacks %s:\n%s", want, home)
		}
	}
	if res, body := poll(t, d.ts, "/accounts", "ops@example.com", true, true, pollHashIn(t, home)); res.StatusCode != http.StatusNoContent {
		t.Errorf("the first poll of a page with an open slip = %d, want 204:\n%s", res.StatusCode, body)
	}
}

// A link can name a request that no longer waits by the time someone opens
// it. The bulk bar then says why, from the server, because a screen reader
// often skips a live region that changes while the page loads. A name that
// is not a request name, and a queue that did not load, name nothing.
func TestAddressNamesAMissingRequest(t *testing.T) {
	ts, _ := newServer(t)
	_, home := doAs(t, ts, "GET", "/?req=gone-box", "lead@example.com", false, true, nil, false)
	if want := `<div id="pending-error" class="refusal">` + alertIcon + `<span data-name="gone-box">` + goneText("gone-box") + `</span></div>`; !strings.Contains(home, want) {
		t.Errorf("the bar lacks the line of gone-box:\n%s", home)
	}
	// The line belongs to the page visit, so the first poll still matches.
	if res, body := poll(t, ts, "/accounts", "lead@example.com", false, true, pollHashIn(t, home)); res.StatusCode != http.StatusNoContent {
		t.Errorf("the first poll of a page with the line = %d, want 204:\n%s", res.StatusCode, body)
	}

	if res, body := do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("submit = %s", body)
	}
	_, bob := do(t, ts, "GET", "/?req=other-box", "bob@example.com", false, nil, false)
	if want := `<span data-name="other-box">other-box is not in Your requests. It is not yours, or it is no longer waiting.</span>`; !strings.Contains(bob, want) {
		t.Errorf("the bar of bob lacks the line of other-box:\n%s", bob)
	}
	_, listed := doAs(t, ts, "GET", "/?req=bobs-box", "lead@example.com", false, true, nil, false)
	if strings.Contains(listed, "data-name") || !strings.Contains(listed, `<div class="slip-details" id="details-bobs-box">`) {
		t.Errorf("the page of a request that waits has a bar line, or its slip is closed:\n%s", listed)
	}
	if _, bad := doAs(t, ts, "GET", "/?req=Bad%20Name", "lead@example.com", false, true, nil, false); strings.Contains(bad, "data-name") {
		t.Errorf("the bar names an address that is not a request name:\n%s", bad)
	}

	// The line comes last, after the results of a decision post.
	gone := undecided(actionApprove, core.RequestRef{Name: "gone-x", Version: "01OLD.v1"}, core.ErrNotFound)
	pg := page{Viewer: Viewer{Email: "lead@example.com", Approver: true}, Results: []decision{gone}, Missing: "gone-y"}
	if got, want := pg.left(), []leftItem{{"gone-x", goneText("gone-x")}, {"gone-y", goneText("gone-y")}}; !reflect.DeepEqual(got, want) {
		t.Errorf("left = %v, want %v", got, want)
	}

	flaky := &flakyList{}
	failing, _ := newServerOn(t, func(p *fake.Provider) core.Provider { flaky.Provider = p; return flaky })
	flaky.fail.Store(true)
	if _, home := do(t, failing, "GET", "/?req=gone-box", "ops@example.com", true, nil, false); strings.Contains(home, "data-name") || !strings.Contains(home, `id="pending-failed"`) {
		t.Errorf("a queue that did not load names the request of the address:\n%s", home)
	}
}

// One page never holds two elements with the same id, so the morph pairs
// every slip, bar and part with its own new node.
func TestQueueIDsAreUnique(t *testing.T) {
	d := newDesk(t, nil, nil, nil)
	d.ask(t, "box-a", "lead@example.com")
	d.ask(t, "box-b", "bob@example.com")
	d.ask(t, "box-c", "carl@example.com")
	_, lead := doAs(t, d.ts, "GET", "/", "lead@example.com", false, true, nil, false)
	_, bob := do(t, d.ts, "GET", "/", "bob@example.com", false, nil, false)
	_, answer := doAs(t, d.ts, "POST", "/requests/box-b/approve", "lead@example.com", false, true, slipForm(t, lead, "box-b", "approve"), true)
	ids := regexp.MustCompile(`\sid="([^"]+)"`)
	for name, body := range map[string]string{"lead's home": lead, "bob's home": bob, "the decide answer": regionPart.ReplaceAllString(answer, "")} {
		seen := map[string]bool{}
		for _, m := range ids.FindAllStringSubmatch(body, -1) {
			if seen[m[1]] {
				t.Errorf("%s: the id %q occurs twice", name, m[1])
			}
			seen[m[1]] = true
		}
		if len(seen) < 5 {
			t.Errorf("%s: found only %d ids:\n%s", name, len(seen), body)
		}
	}
}

// The open state of a slip belongs to the viewer. app.js opens a slip from
// its name button, writes both opened parts, and guards the state in every
// morph.
func TestSlipOpenStateIsTheViewers(t *testing.T) {
	js := staticText(t, "app.js")
	open := funcBody(t, js, "function openState(name, node) {")
	for _, want := range []string{`name === "aria-expanded"`, `"slip-name"`, `name === "hidden"`, `"slip-details"`, `"slip-tools"`} {
		if !strings.Contains(open, want) {
			t.Errorf("openState lacks %s:\n%s", want, open)
		}
	}
	clicked := false
	for _, l := range listeners(js, "click") {
		clicked = clicked || (strings.Contains(l, `closest("button.slip-name")`) && strings.Contains(l, "setOpen("))
	}
	if !clicked {
		t.Error("no click listener opens a slip from its name button")
	}
	set := funcBody(t, js, "function setOpen(button, open) {")
	for _, want := range []string{`(button.getAttribute("aria-controls") || "").split(" ")`, `setAttribute("aria-expanded", open ? "true" : "false")`, `removeAttribute("hidden")`, `setAttribute("hidden", "")`} {
		if !strings.Contains(set, want) {
			t.Errorf("setOpen lacks %s:\n%s", want, set)
		}
	}
	if !strings.Contains(js, "#pending li.slip") || strings.Contains(js, "#pending tbody") {
		t.Error("the focus code does not find the slips of the queue")
	}
	if !strings.Contains(funcBody(t, js, "function keepNode(node) {"), `node.tagName === "LI"`) || strings.Contains(js, `node.tagName === "TR"`) {
		t.Error("keepNode does not keep an open edit slip")
	}
}

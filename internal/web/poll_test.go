package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

// poll sends the 15 s poll as app.js does: an htmx GET that carries the
// hash of what the page shows, or no hash when hash is empty.
func poll(t *testing.T, ts *httptest.Server, path, user string, admin, approver bool, hash string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+path, nil)
	req.Header.Set("X-User", user)
	req.Header.Set("HX-Request", "true")
	if admin {
		req.Header.Set("X-Admin", "1")
	}
	if approver {
		req.Header.Set("X-Approver", "1")
	}
	if hash != "" {
		req.Header.Set(pollHashHeader, hash)
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
	return res, string(b)
}

var pollHashAttr = regexp.MustCompile(`<section id="accounts"[^>]*\sdata-poll-hash="([0-9a-f]{32})">`)

// pollHashIn reads the hash that the accounts section carries in body.
func pollHashIn(t *testing.T, body string) string {
	t.Helper()
	m := pollHashAttr.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no poll hash on the accounts section in:\n%s", body)
	}
	return m[1]
}

// A poll with no hash gets the whole answer: the section, which ends its
// tag with the hash, then the pending card, and the queue count for an
// approver. Nothing in it may be cached.
func TestPollWithoutAHashIsAFullAnswer(t *testing.T) {
	ts, _ := newServer(t)
	for _, tc := range []struct {
		who      string
		approver bool
	}{
		{"anna@example.com", false},
		{"lead@example.com", true},
	} {
		res, body := poll(t, ts, "/accounts", tc.who, false, tc.approver, "")
		if res.StatusCode != http.StatusOK || res.Header.Get("Cache-Control") != "private, no-cache" {
			t.Fatalf("%s: poll = %d, Cache-Control %q", tc.who, res.StatusCode, res.Header.Get("Cache-Control"))
		}
		tag, _, _ := strings.Cut(body, ">")
		if !strings.HasPrefix(body, `<section id="accounts"`) || !regexp.MustCompile(`\sdata-poll-hash="[0-9a-f]{32}"$`).MatchString(tag) {
			t.Errorf("%s: the answer does not start with the section, its hash last: %s>", tc.who, tag)
		}
		if !strings.Contains(body, `id="pending" class="panel" hx-swap-oob="morph"`) {
			t.Errorf("%s: the poll leaves out the pending card:\n%s", tc.who, body)
		}
		if got := strings.Contains(body, `<span id="queue-count" hx-swap-oob="true">`); got != tc.approver {
			t.Errorf("%s: queue count in the poll = %v, want %v", tc.who, got, tc.approver)
		}
	}
}

// The home page shows the hash that a poll of the same view computes, so the
// first poll with nothing new gets 204 and no body.
func TestPollWithTheShownHashGets204(t *testing.T) {
	ts, _ := newServer(t)
	for _, tc := range []struct{ page, poll string }{
		{"/", "/accounts"},
		{"/?all=1", "/accounts?all=1"},
	} {
		_, home := do(t, ts, "GET", tc.page, "ops@example.com", true, nil, false)
		if !strings.Contains(home, `hx-get="`+tc.poll+`"`) {
			t.Fatalf("%s does not poll %s", tc.page, tc.poll)
		}
		res, body := poll(t, ts, tc.poll, "ops@example.com", true, false, pollHashIn(t, home))
		if res.StatusCode != http.StatusNoContent || body != "" {
			t.Errorf("%s: poll with the shown hash = %d:\n%s", tc.poll, res.StatusCode, body)
		}
		if res.Header.Get("Cache-Control") != "private, no-cache" || res.Header.Get("Content-Type") != "" {
			t.Errorf("%s: 204 headers: Cache-Control %q, Content-Type %q", tc.poll, res.Header.Get("Cache-Control"), res.Header.Get("Content-Type"))
		}
	}
}

// The hash covers the pending card. An approver who is not an admin sees no
// new account row for a new request, only a new row in the card, so the
// poll gets 200 only because the card changed.
func TestPollAfterAChangeGets200(t *testing.T) {
	ts, _ := newServer(t)
	_, home := doAs(t, ts, "GET", "/", "lead@example.com", false, true, nil, false)
	before := pollHashIn(t, home)
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true)

	res, body := poll(t, ts, "/accounts", "lead@example.com", false, true, before)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "bobs-box") {
		t.Fatalf("poll after a new request = %d:\n%s", res.StatusCode, body)
	}
	after := pollHashIn(t, body)
	if after == before {
		t.Error("the new answer carries the old hash")
	}
	if res, _ := poll(t, ts, "/accounts", "lead@example.com", false, true, after); res.StatusCode != http.StatusNoContent {
		t.Errorf("the next poll with the new hash = %d, want 204", res.StatusCode)
	}
}

// Every answer that swaps the accounts section carries the hash of the new
// state, so the poll after an action gets 204.
func TestActionAnswersCarryTheNextPollHash(t *testing.T) {
	ts, _ := newServer(t)
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true)
	_, home := doAs(t, ts, "GET", "/", "lead@example.com", false, true, nil, false)

	for _, tc := range []struct {
		name, path, poll, who string
		admin, approver       bool
		form                  url.Values
	}{
		{"extend", "/accounts/111111111111/extend", "/accounts", "anna@example.com", false, false, url.Values{}},
		{"extend, closed shown", "/accounts/111111111111/extend", "/accounts?all=1", "anna@example.com", false, false, url.Values{"all": {"1"}}},
		{"close", "/accounts/111111111111/close", "/accounts", "anna@example.com", false, false, url.Values{}},
		{"approve", "/requests/bobs-box/approve", "/accounts", "lead@example.com", false, true, refForm(refIn(t, home, "bobs-box"))},
		{"sync", "/refresh", "/accounts", "ops@example.com", true, true, url.Values{}},
	} {
		res, body := doAs(t, ts, "POST", tc.path, tc.who, tc.admin, tc.approver, tc.form, true)
		if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" {
			t.Fatalf("%s = %d:\n%s", tc.name, res.StatusCode, body)
		}
		if res, body := poll(t, ts, tc.poll, tc.who, tc.admin, tc.approver, pollHashIn(t, body)); res.StatusCode != http.StatusNoContent {
			t.Errorf("%s: the poll after the answer = %d, want 204:\n%s", tc.name, res.StatusCode, body)
		}
	}
}

// Rows and requests with the same time come in map order from core. The
// web layer breaks the ties, so the same data always hashes the same. The
// two seeded accounts joined at the same time, and svc.Now is fixed, so the
// two requests have the same time too.
func TestPollHashIsStableAcrossReads(t *testing.T) {
	ts, _ := newServer(t)
	for _, who := range []string{"bob", "carl"} {
		do(t, ts, "POST", "/requests", who+"@example.com", false, url.Values{"name": {who + "s-box"}}, true)
	}
	_, home := do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	hash := pollHashIn(t, home)
	for i := range 20 {
		if res, body := poll(t, ts, "/accounts", "ops@example.com", true, false, hash); res.StatusCode != http.StatusNoContent {
			t.Fatalf("read %d of the same data = %d:\n%s", i+1, res.StatusCode, body)
		}
	}
}

// flakyList fails the account listing while fail is set. Core then fails the
// inventory, so the queue does not load either. Each error names a new
// request, as an AWS error does.
type flakyList struct {
	*fake.Provider
	fail  atomic.Bool
	calls atomic.Int64
}

func (p *flakyList) ListPlaygroundAccounts(ctx context.Context) ([]core.RemoteAccount, error) {
	if p.fail.Load() {
		return nil, fmt.Errorf("AccessDenied: the role cannot list accounts, RequestID: req-%d", p.calls.Add(1))
	}
	return p.Provider.ListPlaygroundAccounts(ctx)
}

// A failed poll shows the failed panel inside the section, with the error,
// and never a refusal under New request. It leaves out the pending card and
// the count, so the page keeps what it shows and never reads "Nothing
// waiting". The same outage gets 204, also when the error text changes on
// each call, and the first good poll brings the table back and drops the
// error note.
func TestFailedPollShowsTheUnavailablePanel(t *testing.T) {
	flaky := &flakyList{}
	ts, _ := newServerOn(t, func(p *fake.Provider) core.Provider { flaky.Provider = p; return flaky })
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true)
	flaky.fail.Store(true)

	_, home := do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	if !strings.Contains(home, "The pending requests are unavailable.") || strings.Contains(home, "Nothing waiting") {
		t.Errorf("the home page shows a queue that did not load as empty:\n%s", home)
	}

	res, body := poll(t, ts, "/accounts", "ops@example.com", true, false, "")
	if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" || res.Header.Get("HX-Reswap") != "" || strings.Contains(body, `id="form-error"`) {
		t.Fatalf("a failed poll is a refusal: %d %v\n%s", res.StatusCode, res.Header, body)
	}
	for _, want := range []string{`<div id="accounts-error" class="refusal">`, `<p class="note-title">The account list is unavailable.</p>`, `<p class="note-body">List playground accounts: AccessDenied: the role cannot list accounts, RequestID: req-`, `. The page tries again every 15 seconds.</p>`} {
		if !strings.Contains(body, want) {
			t.Errorf("the failed panel lacks %s:\n%s", want, body)
		}
	}
	for _, bad := range []string{"Nothing waiting", `id="pending"`, `id="queue-count"`} {
		if strings.Contains(body, bad) {
			t.Errorf("the failed poll holds %s, which would replace the card or the count on the page:\n%s", bad, body)
		}
	}
	failed := pollHashIn(t, body)
	if res, _ := poll(t, ts, "/accounts", "ops@example.com", true, false, failed); res.StatusCode != http.StatusNoContent {
		t.Errorf("the same outage with a new error text = %d, want 204", res.StatusCode)
	}
	if flaky.calls.Load() < 3 {
		t.Fatalf("the list failed %d times, so the error text did not change between polls", flaky.calls.Load())
	}

	flaky.fail.Store(false)
	res, body = poll(t, ts, "/accounts", "ops@example.com", true, false, failed)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `<table class="fleet">`) || strings.Contains(body, "accounts-error") {
		t.Errorf("the recovered poll = %d, want the table and no error note:\n%s", res.StatusCode, body)
	}
	if !strings.Contains(body, "bobs-box") || !strings.Contains(body, `<span id="queue-count" hx-swap-oob="true">`) {
		t.Errorf("the recovered poll does not bring the card and the count back:\n%s", body)
	}
}

// The poll answer is a fragment of the home page, so a page visit goes to
// the home page at the panel, with the same view.
func TestDirectVisitToThePollGoesHome(t *testing.T) {
	ts, _ := newServer(t)
	for path, want := range map[string]string{"/accounts": "/#accounts", "/accounts?all=1": "/?all=1#accounts"} {
		res, _ := do(t, ts, "GET", path, "anna@example.com", false, nil, false)
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != want {
			t.Errorf("%s = %d to %q, want 303 to %q", path, res.StatusCode, res.Header.Get("Location"), want)
		}
	}
}

// app.js sends the hash under the header name the server reads.
func TestAppJSSendsThePollHash(t *testing.T) {
	js := staticText(t, "app.js")
	for _, want := range []string{`"htmx:configRequest"`, `"` + pollHashHeader + `"`, `"data-poll-hash"`} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
}

// Any request that is not the poll counts as an action, the answer of a
// poll that started before the latest action is dropped, and a poll waits
// while another request runs. A late poll answer would otherwise put the
// state from before an action back on the page. The poll is not aborted,
// because htmx logs an abort as two console errors.
func TestActionsStopTheRunningPoll(t *testing.T) {
	js := staticText(t, "app.js")
	listener := regexp.MustCompile(`(?s)"htmx:beforeRequest", function \(ev\) \{.*?\n\}\);`).FindString(js)
	for _, want := range []string{"actionSeq++", "ev.preventDefault()", "requestRunning()"} {
		if !strings.Contains(listener, want) {
			t.Errorf("the htmx:beforeRequest listener lacks %s:\n%s", want, listener)
		}
	}
	sent := regexp.MustCompile(`(?s)"htmx:beforeSend", function \(ev\) \{.*?\n\}\);`).FindString(js)
	for _, want := range []string{"running.push(ev.detail.xhr)", "ev.detail.xhr.pollSeq = actionSeq"} {
		if !strings.Contains(sent, want) {
			t.Errorf("the htmx:beforeSend listener lacks %s:\n%s", want, sent)
		}
	}
	swap := listeners(js, "htmx:beforeSwap")
	if len(swap) != 1 || !strings.Contains(swap[0], "xhr.pollSeq !== actionSeq") || !strings.Contains(swap[0], "shouldSwap = false") {
		t.Errorf("the htmx:beforeSwap listener does not drop a late poll answer:\n%v", swap)
	}
	if strings.Contains(js, "htmx:abort") {
		t.Error("app.js aborts a request, which htmx logs as an error")
	}
}

// A pending request shows its expiry counted from the time of the read.
// The row must not show that time exactly, or every read of the same queue
// hashes differently and no poll gets 204. A real expiry keeps its title.
func TestPendingRowDoesNotChangeWithTheClock(t *testing.T) {
	row := func(status core.Status, expires time.Time) string {
		t.Helper()
		var out strings.Builder
		a := &core.Account{ID: "req:bobs-box", Name: "bobs-box", Owner: "bob@example.com", Status: status, ExpiresAt: expires, BudgetUSD: 50}
		if err := accountTR(accountRow{Account: a}).Render(context.Background(), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	later := t0.Add(30 * time.Second)
	if a, b := row(core.StatusPending, t0), row(core.StatusPending, later); a != b {
		t.Errorf("a pending row changes when only the read time moves:\n%s\n%s", a, b)
	}
	if !strings.Contains(row(core.StatusActive, t0), `title="`+t0.Format(time.RFC3339)+`"`) {
		t.Error("an active row lost the exact expiry in its title")
	}
}

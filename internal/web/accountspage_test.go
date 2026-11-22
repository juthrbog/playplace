package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fromPage sends an htmx request as the page at from would send it: htmx
// names the address of the page in HX-Current-URL. hash is the poll hash,
// or empty for none.
func fromPage(t *testing.T, ts *httptest.Server, method, path, from, user string, admin, approver bool, form url.Values, hash string) (*http.Response, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, ts.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("X-User", user)
	if admin {
		req.Header.Set("X-Admin", "1")
	}
	if approver {
		req.Header.Set("X-Approver", "1")
	}
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Current-URL", ts.URL+from)
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

// Only the path of HX-Current-URL names the page. A page visit is its own
// page. An account page with a valid account reference is a page of its
// own. Anything else, a missing or broken header included, is the home
// page, which shows the most.
func TestAskingPage(t *testing.T) {
	for _, c := range []struct {
		name, path, current string
		htmx                bool
		want                string
	}{
		{"accounts page", "/accounts", "http://box.example/accounts", true, pathAccounts},
		{"accounts page with a query and a hash", "/accounts?all=1", "http://box.example/accounts?all=1#acct-1", true, pathAccounts},
		{"relative address", "/accounts", "/accounts", true, pathAccounts},
		{"action from the accounts page", "/accounts/1/extend", "https://box.example:8443/accounts", true, pathAccounts},
		{"home page", "/accounts", "http://box.example/", true, "/"},
		{"home page with a slip", "/accounts", "http://box.example/?req=bobs-box", true, "/"},
		{"account page", "/accounts", "http://box.example/accounts/111111111111", true, "/accounts/111111111111"},
		{"action from an account page", "/accounts/111111111111/close", "http://box.example/accounts/111111111111?history_cursor=x#history", true, "/accounts/111111111111"},
		{"account page of a creation", "/accounts/car-0001", "http://box.example/accounts/car-0001", true, "/accounts/car-0001"},
		{"account page by name", "/accounts/annas-box", "http://box.example/accounts/annas-box", true, "/accounts/annas-box"},
		{"under an account page", "/accounts", "http://box.example/accounts/111111111111/extend", true, "/"},
		{"bad account reference", "/accounts", "http://box.example/accounts/a%20b", true, "/"},
		{"escaped slash", "/accounts", "http://box.example/accounts/a%2Fb", true, "/"},
		{"leading dash", "/accounts", "http://box.example/accounts/-1", true, "/"},
		{"long reference", "/accounts", "http://box.example/accounts/" + strings.Repeat("1", 65), true, "/"},
		{"trailing slash", "/accounts", "http://box.example/accounts/", true, "/"},
		{"other case", "/accounts", "http://box.example/Accounts", true, "/"},
		{"no header", "/accounts", "", true, "/"},
		{"broken header", "/accounts", "http://[::1/accounts", true, "/"},
		{"control bytes", "/accounts", "http://box.example/\x7f", true, "/"},
		{"page visit", "/accounts", "", false, pathAccounts},
		{"page visit ignores the header", "/accounts", "http://box.example/", false, pathAccounts},
		{"page visit home", "/", "http://box.example/accounts", false, "/"},
		{"page visit of an account", "/accounts/111111111111", "", false, "/accounts/111111111111"},
	} {
		r := httptest.NewRequest("GET", c.path, nil)
		if c.htmx {
			r.Header.Set("HX-Request", "true")
		}
		if c.current != "" {
			r.Header.Set("HX-Current-URL", c.current)
		}
		if got := askingPage(r); got != c.want {
			t.Errorf("%s: askingPage = %q, want %q", c.name, got, c.want)
		}
	}
}

// A page visit to /accounts gets a full page. Admins see the fleet under
// Accounts, and everyone else sees their own accounts under Your accounts.
// The title and the hidden h1 say what the panel head says. The page has
// the live region first in main, the close dialog, a refusal slot and no
// queue panel. The same address is a fragment for htmx, so the answer says
// that it varies with HX-Request.
func TestAccountsPageByRole(t *testing.T) {
	ts, _ := newServer(t)
	for _, c := range []struct {
		who             string
		user            string
		admin, approver bool
		title           string
		see, notSee     []string
	}{
		{"admin", "ops@example.com", true, true, "Accounts", []string{"annas-box", "sams-box"}, nil},
		{"approver", "lead@example.com", false, true, "Your accounts", nil, []string{"annas-box", "sams-box"}},
		{"engineer", "anna@example.com", false, false, "Your accounts", []string{"annas-box"}, []string{"sams-box"}},
	} {
		t.Run(c.who, func(t *testing.T) {
			res, body := doAs(t, ts, "GET", "/accounts", c.user, c.admin, c.approver, nil, false)
			if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
				t.Fatalf("GET /accounts = %d %q, want 200 html", res.StatusCode, res.Header.Get("Content-Type"))
			}
			if !strings.Contains(res.Header.Get("Vary"), "HX-Request") || res.Header.Get("Cache-Control") != "private, no-cache" {
				t.Errorf("headers = %v, want Vary with HX-Request and no cache", res.Header)
			}
			for _, w := range []string{
				"<title>" + c.title + " · playplace</title>",
				`<h1 class="vh">` + c.title + `</h1>`,
				`<h2 id="accounts-title" tabindex="-1">` + c.title + ` <span class="muted">`,
				regionStart + `<h1 class="vh">`,
				`<div id="form-error" class="refusal"></div>`,
				`id="close-dialog"`,
				`hx-get="/accounts" hx-trigger="every 15s, poll-now"`,
			} {
				if !strings.Contains(body, w) {
					t.Errorf("the page lacks %s", w)
				}
			}
			for _, n := range []string{`id="pending"`, `id="request"`, `id="deny-dialog"`, `hx-swap-oob`, "<title>Home"} {
				if strings.Contains(body, n) {
					t.Errorf("the page holds %s", n)
				}
			}
			for _, name := range c.see {
				if !strings.Contains(body, name) {
					t.Errorf("the page does not show %s", name)
				}
			}
			for _, name := range c.notSee {
				if strings.Contains(body, name) {
					t.Errorf("the page shows %s, which is not theirs", name)
				}
			}
			pollHashIn(t, body)
			if t.Failed() {
				t.Logf("body:\n%s", body)
			}
		})
	}

	// An old Show closed address survives a reload of the page. The old
	// all=1 is the status filter any, the Status select shows it, and the
	// poller asks for the canonical address. The page has no Show closed.
	_, body := do(t, ts, "GET", "/accounts?all=1", "ops@example.com", true, nil, false)
	if !strings.Contains(body, `<option value="any" selected>Any status</option>`) || strings.Contains(body, `id="show-closed"`) ||
		!strings.Contains(body, `hx-get="/accounts?status=any" hx-trigger="every 15s, poll-now"`) {
		t.Errorf("the page with closed accounts does not keep the view:\n%s", body)
	}
	// The fragment for htmx says the same.
	res, _ := poll(t, ts, "/accounts", "ops@example.com", true, true, "")
	if !strings.Contains(res.Header.Get("Vary"), "HX-Request") {
		t.Errorf("the fragment Vary = %q, want HX-Request", res.Header.Get("Vary"))
	}
}

// On the accounts page, the poll and the row actions answer with the
// accounts section. The page has no queue panel, so the panel never rides
// out of band. The shell shows an approver the count beside Queue, so an
// approver gets the count out of band, and other viewers do not. Each answer
// carries the hash that the next poll of that page matches.
func TestAccountsPageAnswersHaveNoQueue(t *testing.T) {
	ts, _ := newServer(t)
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true)
	for _, c := range []struct {
		name, method, path, poll, user string
		admin, approver                bool
		form                           url.Values
	}{
		{"poll as admin", "GET", "/accounts", "/accounts", "ops@example.com", true, true, nil},
		{"poll as approver", "GET", "/accounts", "/accounts", "lead@example.com", false, true, nil},
		{"poll, closed shown", "GET", "/accounts?all=1", "/accounts?all=1", "ops@example.com", true, true, nil},
		{"extend", "POST", "/accounts/111111111111/extend", "/accounts", "ops@example.com", true, true, url.Values{"by": {"7d"}}},
		{"extend, closed shown", "POST", "/accounts/111111111111/extend", "/accounts?all=1", "ops@example.com", true, true, url.Values{"by": {"7d"}, "all": {"1"}}},
		{"close", "POST", "/accounts/222222222222/close", "/accounts", "ops@example.com", true, true, url.Values{}},
		{"sync", "POST", "/refresh", "/accounts", "ops@example.com", true, true, url.Values{}},
		{"extend as engineer", "POST", "/accounts/111111111111/extend", "/accounts", "anna@example.com", false, false, url.Values{"by": {"7d"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			res, body := fromPage(t, ts, c.method, c.path, "/accounts", c.user, c.admin, c.approver, c.form, "")
			if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" {
				t.Fatalf("%s %s = %d:\n%s", c.method, c.path, res.StatusCode, body)
			}
			if !strings.HasPrefix(body, `<section id="accounts"`) {
				t.Errorf("the answer does not start with the accounts section:\n%s", body)
			}
			if queueOOB(body) || strings.Contains(body, `id="pending"`) {
				t.Errorf("the answer carries the queue, which has no place on the accounts page:\n%s", body)
			}
			if got := strings.Contains(body, `<span id="queue-count" hx-swap-oob="true">`); got != c.approver {
				t.Errorf("the answer carries the count = %v, want %v:\n%s", got, c.approver, body)
			}
			if c.method == "POST" && !strings.Contains(body, `<p id="notice" hx-swap-oob="innerHTML">`) {
				t.Errorf("the action answer does not say what it did:\n%s", body)
			}
			if res, again := fromPage(t, ts, "GET", c.poll, "/accounts", c.user, c.admin, c.approver, nil, pollHashIn(t, body)); res.StatusCode != http.StatusNoContent {
				t.Errorf("the next poll of the accounts page = %d, want 204:\n%s", res.StatusCode, again)
			}
		})
	}
}

// The home page keeps its answers: the poll and the row actions carry the
// queue panel, and the count for an approver, out of band.
func TestHomeAnswersStillCarryTheQueue(t *testing.T) {
	ts, _ := newServer(t)
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true)
	for _, from := range []string{"/", "/?all=1", "/?req=bobs-box"} {
		for _, c := range []struct {
			name, method, path string
			form               url.Values
		}{
			{"poll", "GET", "/accounts", nil},
			{"extend", "POST", "/accounts/111111111111/extend", url.Values{"by": {"7d"}}},
			{"sync", "POST", "/refresh", url.Values{}},
		} {
			res, body := fromPage(t, ts, c.method, c.path, from, "ops@example.com", true, true, c.form, "")
			if res.StatusCode != http.StatusOK || !queueOOB(body) || !strings.Contains(body, `<span id="queue-count" hx-swap-oob="true">`) {
				t.Errorf("%s from %s = %d, want the queue and the count out of band:\n%s", c.name, from, res.StatusCode, body)
			}
		}
	}
}

// The two pages show different things, so each has its own hash and its
// own 204. The hash of one page never stops the poll of the other.
func TestEachPageGetsItsOwn204(t *testing.T) {
	ts, _ := newServer(t)
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true)
	for _, c := range []struct {
		who             string
		user            string
		admin, approver bool
	}{
		{"admin", "ops@example.com", true, true},
		{"engineer", "bob@example.com", false, false},
	} {
		t.Run(c.who, func(t *testing.T) {
			_, home := doAs(t, ts, "GET", "/", c.user, c.admin, c.approver, nil, false)
			_, accounts := doAs(t, ts, "GET", "/accounts", c.user, c.admin, c.approver, nil, false)
			homeHash, accountsHash := pollHashIn(t, home), pollHashIn(t, accounts)
			if homeHash == accountsHash {
				t.Fatalf("the two pages share the hash %s", homeHash)
			}
			for _, p := range []struct {
				from, hash string
				want       int
			}{
				{"/", homeHash, http.StatusNoContent},
				{"/accounts", accountsHash, http.StatusNoContent},
				{"/", accountsHash, http.StatusOK},
				{"/accounts", homeHash, http.StatusOK},
			} {
				if res, body := fromPage(t, ts, "GET", "/accounts", p.from, c.user, c.admin, c.approver, nil, p.hash); res.StatusCode != p.want {
					t.Errorf("poll from %s with the hash %s = %d, want %d:\n%s", p.from, p.hash, res.StatusCode, p.want, body)
				}
			}
			// A poll with no HX-Current-URL is a home poll.
			if res, _ := poll(t, ts, "/accounts", c.user, c.admin, c.approver, homeHash); res.StatusCode != http.StatusNoContent {
				t.Errorf("a poll with no page address = %d, want the 204 of the home page", res.StatusCode)
			}
		})
	}
}

// The accounts page shows an approver the count beside Queue. A new request
// changes that count, so the next poll of the page answers 200 with the new
// count and not 204.
func TestAccountsPagePollKeepsTheCount(t *testing.T) {
	ts, _ := newServer(t)
	_, page := doAs(t, ts, "GET", "/accounts", "lead@example.com", false, true, nil, false)
	if !strings.Contains(page, `<span id="queue-count"></span>`) {
		t.Fatalf("the page does not show an empty count:\n%s", page)
	}
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true)
	res, body := fromPage(t, ts, "GET", "/accounts", "/accounts", "lead@example.com", false, true, nil, pollHashIn(t, page))
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `<span id="queue-count" hx-swap-oob="true"><span class="count-pill">1<span class="vh"> pending requests</span></span></span>`) {
		t.Errorf("the poll after a new request = %d, want 200 with the count 1:\n%s", res.StatusCode, body)
	}
}

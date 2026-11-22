package web

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

// viewFor is the view of an account page for a with known spend of zero.
func viewFor(a *core.Account) accountView {
	return accountView{Row: accountRow{Account: a, SpendKnown: true}}
}

var accountHashAttr = regexp.MustCompile(`<section id="account"[^>]*\sdata-poll-hash="([0-9a-f]{32})">`)

// accountHashIn reads the hash that the account section carries in body.
func accountHashIn(t *testing.T, body string) string {
	t.Helper()
	m := accountHashAttr.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no poll hash on the account section in:\n%s", body)
	}
	return m[1]
}

// countedCosts counts the cost pulls that reach the provider, and fails
// them while fail is set.
type countedCosts struct {
	core.Provider
	fail  atomic.Bool
	calls atomic.Int32
}

func (p *countedCosts) Costs(ctx context.Context, id string, from, to time.Time) ([]core.CostPoint, error) {
	p.calls.Add(1)
	if p.fail.Load() {
		return nil, context.DeadlineExceeded
	}
	return p.Provider.Costs(ctx, id, from, to)
}

// The account page is the poller of its own address. It has the slot of
// the account outside the section that the refresh redraws, the live line
// in the footer, and an answer that no cache keeps or mixes with the
// fragment.
func TestAccountPagePollsItsOwnAddress(t *testing.T) {
	ts, _ := newServer(t)
	res, body := do(t, ts, "GET", "/accounts/111111111111", "anna@example.com", false, nil, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET = %d", res.StatusCode)
	}
	if res.Header.Get("Cache-Control") != "private, no-cache" || res.Header.Get("Vary") != "HX-Request, HX-Current-URL" {
		t.Errorf("headers = %v, want no cache and Vary on HX-Request and HX-Current-URL", res.Header)
	}
	for _, want := range []string{
		`<div id="account-error" class="refusal"></div><section id="account" class="account" aria-labelledby="account-title" hx-get="/accounts/111111111111" hx-trigger="every 15s, poll-now" hx-swap="morph" data-poll data-poll-hash="`,
		`<div class="loan loan-page"><div class="loan-head"><h1 id="account-title" class="account-name" tabindex="-1">annas-box</h1>`,
		liveLine,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q:\n%s", want, body)
		}
	}
	if n := strings.Count(body, " data-poll "); n != 1 {
		t.Errorf("%d pollers, want 1", n)
	}
	section, _, _ := strings.Cut(body[strings.Index(body, `<section id="account"`):], "</section>")
	for _, bad := range []string{"History", "Daily spend", "Change monthly budget", "account-error"} {
		if strings.Contains(section, bad) {
			t.Errorf("the refreshed section holds %q:\n%s", bad, section)
		}
	}
}

// The refresh of the account page gets the account section with its hash,
// or 204 when the page still shows the same. The page visit carries the
// hash of the same section, so its first refresh gets 204.
func TestAccountRefreshAnswers204WhenNothingChanged(t *testing.T) {
	ts, _ := newServer(t)
	const page = "/accounts/111111111111"
	_, body := do(t, ts, "GET", page, "anna@example.com", false, nil, false)
	visit := accountHashIn(t, body)
	res, _ := fromPage(t, ts, "GET", page, page, "anna@example.com", false, false, nil, visit)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("the first refresh = %d, want 204", res.StatusCode)
	}
	res, body = fromPage(t, ts, "GET", page, page, "anna@example.com", false, false, nil, "")
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(body, `<section id="account" `) {
		t.Fatalf("a refresh with no hash = %d:\n%s", res.StatusCode, body)
	}
	if res.Header.Get("Cache-Control") != "private, no-cache" || res.Header.Get("Vary") != "HX-Request, HX-Current-URL" {
		t.Errorf("headers = %v", res.Header)
	}
	if got := accountHashIn(t, body); got != visit {
		t.Errorf("refresh hash %s, page hash %s", got, visit)
	}
	for _, bad := range []string{`id="accounts"`, `id="pending"`, `id="attention"`, `id="queue-count"`, "<html", `id="notice"`} {
		if strings.Contains(body, bad) {
			t.Errorf("the refresh holds %s:\n%s", bad, body)
		}
	}
	// A change of the account changes the hash.
	fromPage(t, ts, "POST", page+"/extend", page, "anna@example.com", false, false, url.Values{"by": {"1"}}, "")
	res, _ = fromPage(t, ts, "GET", page, page, "anna@example.com", false, false, nil, visit)
	if res.StatusCode != http.StatusOK {
		t.Errorf("a refresh after an extension = %d, want 200", res.StatusCode)
	}
}

// The refresh reads spend from the cost cache only. When the last pull
// failed, it says that spend is unavailable and does not pull again. Only
// the page visit, a request of a person, pulls at once.
func TestAccountRefreshNeverPullsCosts(t *testing.T) {
	cc := &countedCosts{}
	ts, _ := newServerOn(t, func(p *fake.Provider) core.Provider { cc.Provider = p; return cc })
	const page = "/accounts/111111111111"
	cc.fail.Store(true)
	do(t, ts, "GET", "/accounts", "ops@example.com", true, nil, true)
	cc.fail.Store(false)
	before := cc.calls.Load()
	for range 3 {
		_, body := fromPage(t, ts, "GET", page, page, "ops@example.com", true, true, nil, "")
		if !strings.Contains(body, `<p class="spend"><span class="muted">unavailable</span> of $50.00 a month</p>`) || strings.Contains(body, "meter-block") {
			t.Fatalf("the refresh does not say that spend is unavailable:\n%s", body)
		}
	}
	if got := cc.calls.Load(); got != before {
		t.Fatalf("the refresh pulled costs %d times", got-before)
	}
	_, body := do(t, ts, "GET", page, "ops@example.com", true, nil, false)
	if cc.calls.Load() != before+1 || !strings.Contains(body, "$22.50 of $50.00 a month") {
		t.Fatalf("the page visit did not pull the unknown spend once (%d calls):\n%s", cc.calls.Load()-before, body)
	}
	// The cache now holds the spend, and the refresh shows it with no pull.
	_, body = fromPage(t, ts, "GET", page, page, "ops@example.com", true, true, nil, "")
	if cc.calls.Load() != before+1 || !strings.Contains(body, "$22.50 of $50.00 a month") {
		t.Errorf("the refresh after the pull (%d calls):\n%s", cc.calls.Load()-before, body)
	}
}

// Only the owner and an admin get the account page or its refresh. Anyone
// else, an approver included, gets the 403 page on a visit. The refresh
// gets the plain notice, which shows nothing of the account and stops the
// poll, so it does not repeat a refusal every 15 s.
func TestAccountRefreshIsScoped(t *testing.T) {
	ts, _ := newServer(t)
	const page = "/accounts/222222222222"
	for _, c := range []struct {
		who      string
		approver bool
	}{
		{"anna@example.com", false},
		{"lead@example.com", true},
	} {
		res, body := fromPage(t, ts, "GET", page, page, c.who, false, c.approver, nil, "")
		if res.StatusCode != http.StatusOK || res.Header.Get("HX-Reswap") != "" {
			t.Errorf("%s: refresh = %d, reswap %q, want 200 and a swap", c.who, res.StatusCode, res.Header.Get("HX-Reswap"))
		}
		if !strings.Contains(body, `<h1 id="account-title" tabindex="-1">Account unavailable</h1>`) {
			t.Errorf("%s: the refresh is not the plain notice:\n%s", c.who, body)
		}
		for _, bad := range []string{"data-poll", "account-error", "sams-box", "s@example.com", "sam@example.com"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: the refusal shows %s:\n%s", c.who, bad, body)
			}
		}
		if res, _ := doAs(t, ts, "GET", page, c.who, false, c.approver, nil, false); res.StatusCode != http.StatusForbidden {
			t.Errorf("%s: page visit = %d, want 403", c.who, res.StatusCode)
		}
	}
	if res, body := fromPage(t, ts, "GET", page, page, "sam@example.com", false, false, nil, ""); res.StatusCode != http.StatusOK || !strings.Contains(body, "sams-box") {
		t.Errorf("the owner's refresh = %d:\n%s", res.StatusCode, body)
	}
	if res, body := fromPage(t, ts, "GET", page, page, "ops@example.com", true, true, nil, ""); res.StatusCode != http.StatusOK || !strings.Contains(body, "sams-box") {
		t.Errorf("the admin's refresh = %d:\n%s", res.StatusCode, body)
	}
}

// An action from the account page gets the account section and the
// message for the live region. It carries no fleet, no ledger and no
// queue, because the page has none.
func TestAccountPageActionsAnswerTheAccount(t *testing.T) {
	ts, _ := newServer(t)
	const page = "/accounts/111111111111"
	for _, c := range []struct {
		who          string
		admin        bool
		path         string
		form         url.Values
		message      string
		status, more string
	}{
		{"anna@example.com", false, page + "/extend", url.Values{"by": {"1"}}, "Extended annas-box to Sep 19 12:00 UTC.", "active", "<dt>Lifetime</dt><dd>4 days <span class=\"muted\">(the limit is 90 days)</span></dd>"},
		{"ops@example.com", true, page + "/close", url.Values{}, "Requested closure of annas-box.", "closed", "<dt>Close requested</dt>"},
	} {
		res, body := fromPage(t, ts, "POST", c.path, page, c.who, c.admin, c.admin, c.form, "")
		if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" {
			t.Fatalf("%s = %d:\n%s", c.path, res.StatusCode, body)
		}
		if !strings.HasPrefix(body, `<section id="account" `) {
			t.Errorf("%s: the main swap is not the account section:\n%s", c.path, body)
		}
		accountHashIn(t, body)
		for _, want := range []string{c.message, `status status-` + c.status, c.more} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: the answer lacks %q:\n%s", c.path, want, body)
			}
		}
		for _, bad := range []string{`id="accounts"`, `id="pending"`, `id="attention"`, `id="queue-count"`, "sams-box"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: the answer holds %s:\n%s", c.path, bad, body)
			}
		}
	}
}

// An answer to an account page whose account is gone, or that the viewer
// may not see, is a plain notice that shows nothing of the account, also
// when the request names that page by hand. bob and cat own no account.
func TestAccountAnswerForAGoneOrHiddenAccount(t *testing.T) {
	ts, _ := newServer(t)
	for _, c := range []struct{ from, who, name string }{
		{"/accounts/222222222222", "bob@example.com", "bobs-box"},
		{"/accounts/999999999999", "cat@example.com", "cats-box"},
	} {
		from := c.from
		res, body := fromPage(t, ts, "POST", "/requests", from, c.who, false, false, url.Values{"name": {c.name}}, "")
		if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" {
			t.Fatalf("%s: request = %d:\n%s", from, res.StatusCode, body)
		}
		if !strings.Contains(body, `<h1 id="account-title" tabindex="-1">Account unavailable</h1>`) || !strings.Contains(body, "Sent the request for "+c.name+".") {
			t.Errorf("%s: the answer is not the plain notice:\n%s", from, body)
		}
		for _, bad := range []string{"sams-box", "222222222222", "s@example.com", "data-poll", `id="accounts"`, `id="pending"`} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: the answer holds %s:\n%s", from, bad, body)
			}
		}
		// The accounts address answers that page as the page itself does.
		res, body = fromPage(t, ts, "GET", "/accounts", from, c.who, false, false, nil, "")
		if res.StatusCode != http.StatusOK || !strings.Contains(body, "Account unavailable") || strings.Contains(body, "sams-box") || strings.Contains(body, `id="accounts"`) {
			t.Errorf("%s: GET /accounts = %d:\n%s", from, res.StatusCode, body)
		}
	}
	// The owner's own page gets the account from the accounts address too.
	_, body := fromPage(t, ts, "GET", "/accounts", "/accounts/111111111111", "anna@example.com", false, false, nil, "")
	if !strings.HasPrefix(body, `<section id="account" `) || !strings.Contains(body, "annas-box") {
		t.Errorf("GET /accounts from the account page:\n%s", body)
	}
}

// On an account page a refused action fills the slot of the account, and
// a refused budget change of an admin fills the slot under its form.
func TestAccountPageRefusalSlots(t *testing.T) {
	ts, _ := newServer(t)
	const page = "/accounts/111111111111"
	for _, c := range []struct {
		path, slot string
		form       url.Values
	}{
		{page + "/extend", "account-error", url.Values{"by": {"nope"}}},
		{page + "/close", "account-error", url.Values{}},
		{page + "/budget", "form-error", url.Values{"budget": {"-1"}, "reason": {"r"}}},
	} {
		user, admin := "ops@example.com", true
		if strings.HasSuffix(c.path, "/close") {
			// Close of another owner's account is refused for an engineer.
			user, admin = "sam@example.com", false
		}
		_, body := fromPage(t, ts, "POST", c.path, page, user, admin, admin, c.form, "")
		if !strings.Contains(body, `<div id="`+c.slot+`" hx-swap-oob="innerHTML">`) {
			t.Errorf("%s: the refusal does not fill %s:\n%s", c.path, c.slot, body)
		}
	}
}

// The card says the spend this month against the budget, what is left and
// that cost data lags, and the facts say the lifetime with its ceiling.
func TestAccountSpendAndLifetimeWords(t *testing.T) {
	for _, c := range []struct {
		spend, budget float64
		want          string
	}{
		{22.5, 50, "$27.50 left this month"},
		{50, 50, "$0.00 left this month"},
		{61.25, 50, "$11.25 over the budget this month"},
		{10, 0, ""},
	} {
		if got := leftThisMonth(c.spend, c.budget); got != c.want {
			t.Errorf("leftThisMonth(%v, %v) = %q, want %q", c.spend, c.budget, got, c.want)
		}
	}
	a := &core.Account{CreatedAt: t0, ExpiresAt: t0.Add(72 * time.Hour)}
	if got := lifetime(a); got != "3 days" {
		t.Errorf("lifetime = %q", got)
	}
	if got := lifetime(&core.Account{ExpiresAt: t0}); got != "" {
		t.Errorf("lifetime with no creation = %q", got)
	}
	if got := lifetimeLimit(accountView{MaxTTL: 90 * 24 * time.Hour, HasLatest: true}); got != "the limit is 90 days" {
		t.Errorf("lifetimeLimit = %q", got)
	}
	if got := lifetimeLimit(accountView{MaxTTL: 90 * 24 * time.Hour}); got != "" {
		t.Errorf("lifetimeLimit with no latest date = %q", got)
	}
	ts, _ := newServer(t)
	_, body := do(t, ts, "GET", "/accounts/111111111111", "anna@example.com", false, nil, false)
	for _, want := range []string{
		`<p class="spend">$22.50 of $50.00 a month</p><div class="meter-block meter-ok" title="$22.50 of $50.00"><svg class="meter-full" width="100%" height="6"`,
		`<rect class="meter-fill" width="45%" height="6" rx="3"></rect>`,
		`<p class="spend-left">$27.50 left this month</p><p class="help">Cost data lags about a day.</p>`,
		`<dt>Lifetime</dt><dd>3 days <span class="muted">(the limit is 90 days)</span></dd>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q:\n%s", want, body)
		}
	}
}

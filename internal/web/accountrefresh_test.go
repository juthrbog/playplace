package web

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

// The refresh of an account page polls the address of the page. An account
// that is gone, or that the viewer may not see, gets the plain notice,
// which does not poll, so the page stops asking. It never gets a refusal
// that would come back every 15 s. bob and cat own no account.
func TestAccountRefreshOfAGoneOrHiddenAccount(t *testing.T) {
	ts, _ := newServer(t)
	for _, c := range []struct{ page, who string }{
		{"/accounts/999999999999", "anna@example.com"},
		{"/accounts/222222222222", "bob@example.com"},
		{"/accounts/111111111111", "cat@example.com"},
	} {
		res, body := fromPage(t, ts, "GET", c.page, c.page, c.who, false, false, nil, "")
		if res.StatusCode != http.StatusOK || res.Header.Get("HX-Reswap") != "" || res.Header.Get("X-Form-Error") != "" {
			t.Errorf("%s by %s = %d %v, want 200 and a swap", c.page, c.who, res.StatusCode, res.Header)
		}
		if !strings.Contains(body, `<h1 id="account-title" tabindex="-1">Account unavailable</h1>`) {
			t.Errorf("%s by %s: the refresh is not the plain notice:\n%s", c.page, c.who, body)
		}
		for _, bad := range []string{"data-poll", "account-error", `id="notice"`, "annas-box", "sams-box"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s by %s: the refresh holds %s:\n%s", c.page, c.who, bad, body)
			}
		}
	}
}

// An account that did not load is a note inside the section, not a
// refusal in the slot of the account, which no poll clears. The section
// keeps polling and carries data-stale, so the Updated time does not
// reset. The same outage gets 204, and the first good poll brings the card
// back.
func TestAccountRefreshWhenTheListFails(t *testing.T) {
	flaky := &flakyList{}
	ts, _ := newServerOn(t, func(p *fake.Provider) core.Provider { flaky.Provider = p; return flaky })
	const page = "/accounts/111111111111"
	flaky.fail.Store(true)
	res, body := fromPage(t, ts, "GET", page, page, "anna@example.com", false, false, nil, "")
	if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" || res.Header.Get("HX-Reswap") != "" {
		t.Fatalf("a failed refresh is a refusal: %d %v\n%s", res.StatusCode, res.Header, body)
	}
	for _, want := range []string{
		`hx-get="` + page + `"`,
		`<div id="account-stale" class="refusal" data-stale>`,
		`<p class="note-title" id="account-title">The account is unavailable right now.</p>`,
		`<p class="note-body">The call to AWS failed. If it keeps failing, ask an admin to read the playplace log. ` + retryNote + `</p>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the failed refresh lacks %s:\n%s", want, body)
		}
	}
	for _, bad := range []string{"account-error", "AccessDenied", "RequestID"} {
		if strings.Contains(body, bad) {
			t.Errorf("the failed refresh holds %s:\n%s", bad, body)
		}
	}
	failed := accountHashIn(t, body)
	if res, _ := fromPage(t, ts, "GET", page, page, "anna@example.com", false, false, nil, failed); res.StatusCode != http.StatusNoContent {
		t.Errorf("the same outage with a new error text = %d, want 204", res.StatusCode)
	}
	// The accounts address answers the account page the same way.
	if _, body := fromPage(t, ts, "GET", "/accounts", page, "anna@example.com", false, false, nil, ""); !strings.Contains(body, "account-stale") {
		t.Errorf("GET /accounts from the account page in an outage:\n%s", body)
	}

	flaky.fail.Store(false)
	res, body = fromPage(t, ts, "GET", page, page, "anna@example.com", false, false, nil, failed)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "annas-box") || strings.Contains(body, "data-stale") {
		t.Errorf("the first good refresh = %d:\n%s", res.StatusCode, body)
	}
}

// An account page lives at the ID of the account. A page visit by name
// moves to the ID, so the poll and the actions never follow the name to a
// newer account that takes it.
func TestAccountPageByNameMovesToTheID(t *testing.T) {
	ts, _ := newServer(t)
	res, _ := do(t, ts, "GET", "/accounts/annas-box", "anna@example.com", false, nil, false)
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/accounts/111111111111" {
		t.Errorf("page visit by name = %d to %q, want 302 to the ID", res.StatusCode, res.Header.Get("Location"))
	}
	res, body := do(t, ts, "GET", "/accounts/111111111111", "anna@example.com", false, nil, false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `hx-get="/accounts/111111111111"`) {
		t.Errorf("page visit by ID = %d:\n%s", res.StatusCode, body)
	}
}

// A creation has the creation request ID until AWS places the account in
// the OU. A page opened at that ID still finds the account after that: the
// refresh moves the page to the AWS account ID, and a page visit redirects
// there.
func TestCreationPageFollowsThePlacedAccount(t *testing.T) {
	now := t0
	prov := fake.New()
	prov.Now = func() time.Time { return now }
	cfg := core.DefaultConfig()
	cfg.InventoryTTL = 0
	svc := core.NewService(prov, nil, cfg, nil)
	svc.Now = func() time.Time { return now }
	a, err := svc.Request(context.Background(), core.RequestInput{Name: "placed", Owner: "anna@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	prov.Settle()
	cr, err := prov.CreateStatus(context.Background(), a.RequestID)
	if err != nil || cr.ProviderID == "" {
		t.Fatalf("creation %+v: %v", cr, err)
	}
	prov.MoveAccount(cr.ProviderID, fake.OUID)
	ts := httptest.NewServer(New(svc, slog.New(slog.DiscardHandler), headerAuth{}, nil, nil).Handler())
	t.Cleanup(ts.Close)

	page, placed := "/accounts/"+a.RequestID, "/accounts/"+cr.ProviderID
	res, body := fromPage(t, ts, "GET", page, page, "admin@example.com", true, false, nil, "")
	if res.StatusCode != http.StatusOK || res.Header.Get("HX-Replace-Url") != placed || !strings.Contains(body, `hx-get="`+placed+`"`) || !strings.Contains(body, cr.ProviderID) {
		t.Errorf("refresh after placement = %d, HX-Replace-Url %q:\n%s", res.StatusCode, res.Header.Get("HX-Replace-Url"), body)
	}
	res, _ = do(t, ts, "GET", page, "admin@example.com", true, nil, false)
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != placed {
		t.Errorf("page visit after placement = %d to %q, want 302 to %s", res.StatusCode, res.Header.Get("Location"), placed)
	}
}

// A pending request is an account with the ID "req:" and its name. Its
// page polls its own address, not the fleet of the home page.
func TestPendingRequestPagePollsItself(t *testing.T) {
	ts, _ := newServer(t)
	if res, body := do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-req"}}, true); res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("request = %d:\n%s", res.StatusCode, body)
	}
	const page = "/accounts/req:bobs-req"
	res, body := do(t, ts, "GET", page, "bob@example.com", false, nil, false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `hx-get="`+page+`"`) {
		t.Fatalf("page visit = %d:\n%s", res.StatusCode, body)
	}
	res, body = fromPage(t, ts, "GET", page, page, "bob@example.com", false, false, nil, "")
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(body, `<section id="account" `) || strings.Contains(body, `id="accounts"`) {
		t.Errorf("refresh = %d:\n%s", res.StatusCode, body)
	}
}

// A pending request has the ID "req:" and its name. After an approver
// approves it, a page opened at that ID still finds the account that the
// approval creates: the refresh moves the page to the creation request ID,
// and, once AWS places the account, to the AWS account ID. The page never
// says the account is gone.
func TestPendingRequestPageFollowsTheApprovedAccount(t *testing.T) {
	now := t0
	prov := fake.New()
	prov.Now = func() time.Time { return now }
	cfg := core.DefaultConfig()
	cfg.InventoryTTL = 0
	svc := core.NewService(prov, nil, cfg, nil)
	svc.Now = func() time.Time { return now }
	ctx := context.Background()
	r, err := svc.SubmitRequest(ctx, core.RequestInput{Name: "move-a", Owner: "bob@example.com", RequestedBy: "bob@example.com"}, "web")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(svc, slog.New(slog.DiscardHandler), headerAuth{}, nil, nil).Handler())
	t.Cleanup(ts.Close)

	const page = "/accounts/req:move-a"
	if res, body := fromPage(t, ts, "GET", page, page, "bob@example.com", false, false, nil, ""); res.StatusCode != http.StatusOK || !strings.Contains(body, `hx-get="`+page+`"`) {
		t.Fatalf("refresh before approval = %d:\n%s", res.StatusCode, body)
	}
	a, err := svc.Approve(ctx, r.Ref(), "lead@example.com")
	if err != nil {
		t.Fatal(err)
	}
	created := "/accounts/" + a.ID
	res, body := fromPage(t, ts, "GET", page, page, "bob@example.com", false, false, nil, "")
	if res.StatusCode != http.StatusOK || res.Header.Get("HX-Replace-Url") != created || !strings.Contains(body, `hx-get="`+created+`"`) || strings.Contains(body, "Account unavailable") {
		t.Errorf("refresh after approval = %d, HX-Replace-Url %q, want %s:\n%s", res.StatusCode, res.Header.Get("HX-Replace-Url"), created, body)
	}

	prov.Settle()
	cr, err := prov.CreateStatus(ctx, a.ID)
	if err != nil || cr.ProviderID == "" {
		t.Fatalf("creation %+v: %v", cr, err)
	}
	prov.MoveAccount(cr.ProviderID, fake.OUID)
	placed := "/accounts/" + cr.ProviderID
	res, body = fromPage(t, ts, "GET", page, page, "bob@example.com", false, false, nil, "")
	if res.StatusCode != http.StatusOK || res.Header.Get("HX-Replace-Url") != placed || strings.Contains(body, "Account unavailable") {
		t.Errorf("refresh after placement = %d, HX-Replace-Url %q, want %s:\n%s", res.StatusCode, res.Header.Get("HX-Replace-Url"), placed, body)
	}
}

// The lifetime is in whole days for a span of a day or more, because the
// creation time and the due date seldom differ by whole days.
func TestLifetimeIsInWholeDays(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{3 * time.Hour, "3 hours"},
		{24 * time.Hour, "1 day"},
		{71 * time.Hour, "3 days"},
		{72 * time.Hour, "3 days"},
		{8761 * time.Hour, "366 days"},
	} {
		if got := lifetime(&core.Account{CreatedAt: t0, ExpiresAt: t0.Add(c.d)}); got != c.want {
			t.Errorf("lifetime(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

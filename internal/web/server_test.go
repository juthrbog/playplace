package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"playplace/internal/audit"
	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

var t0 = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

// headerAuth is a test authenticator: X-User sets the viewer, X-Admin makes
// them an admin, no header means not signed in.
type headerAuth struct{}

func (headerAuth) Register(*http.ServeMux) {}
func (headerAuth) LoginPath() string       { return "/auth/login" }
func (headerAuth) Enabled() bool           { return true }
func (headerAuth) Authenticate(r *http.Request) (Viewer, bool) {
	u := r.Header.Get("X-User")
	if u == "" {
		return Viewer{}, false
	}
	admin := r.Header.Get("X-Admin") == "1"
	return Viewer{Email: u, Name: u, Admin: admin, Approver: admin || r.Header.Get("X-Approver") == "1"}, true
}

func newServer(t *testing.T) (*httptest.Server, *fake.Provider) {
	t.Helper()
	prov := fake.New()
	prov.Now = func() time.Time { return t0 }
	tags := func(owner string) map[string]string {
		return map[string]string{core.TagManaged: "true", core.TagOwner: owner, core.TagExpires: t0.Add(72 * time.Hour).Format(time.RFC3339), core.TagBudget: "50"}
	}
	prov.Seed(core.RemoteAccount{ProviderID: "111111111111", Name: "annas-box", Email: "a@example.com", JoinedAt: t0, Tags: tags("anna@example.com")})
	prov.Seed(core.RemoteAccount{ProviderID: "222222222222", Name: "sams-box", Email: "s@example.com", JoinedAt: t0, Tags: tags("sam@example.com")})
	cfg := core.DefaultConfig()
	cfg.InventoryTTL = 0
	svc := core.NewService(prov, nil, cfg, nil)
	svc.Now = func() time.Time { return t0 }
	hist := &audit.Memory{}
	svc.SetAuditor(hist)
	srv := New(svc, slog.New(slog.DiscardHandler), headerAuth{}, nil, hist)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, prov
}

func doAs(t *testing.T, ts *httptest.Server, method, path, user string, admin, approver bool, form url.Values, htmx bool) (*http.Response, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, ts.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if user != "" {
		req.Header.Set("X-User", user)
	}
	if admin {
		req.Header.Set("X-Admin", "1")
	}
	if approver {
		req.Header.Set("X-Approver", "1")
	}
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res, string(b)
}

func do(t *testing.T, ts *httptest.Server, method, path, user string, admin bool, form url.Values, htmx bool) (*http.Response, string) {
	return doAs(t, ts, method, path, user, admin, false, form, htmx)
}

func TestUnauthenticatedIsSentToLogin(t *testing.T) {
	ts, _ := newServer(t)
	res, _ := do(t, ts, "GET", "/", "", false, nil, false)
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/auth/login" {
		t.Fatalf("browser should be redirected to login, got %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	res, _ = do(t, ts, "GET", "/accounts", "", false, nil, true)
	if res.StatusCode != http.StatusUnauthorized || res.Header.Get("HX-Redirect") != "/auth/login" {
		t.Fatalf("htmx should get 401 with HX-Redirect, got %d", res.StatusCode)
	}
	if res, _ := do(t, ts, "GET", "/healthz", "", false, nil, false); res.StatusCode != http.StatusOK {
		t.Fatal("healthz must not need sign-in")
	}
}

func TestEngineerSeesOnlyOwnAccounts(t *testing.T) {
	ts, _ := newServer(t)
	_, body := do(t, ts, "GET", "/", "anna@example.com", false, nil, false)
	if !strings.Contains(body, "annas-box") || strings.Contains(body, "sams-box") {
		t.Fatalf("anna should see only her account:\n%s", body)
	}
	if !strings.Contains(body, "You see the accounts you own") || strings.Contains(body, "Sync with AWS") {
		t.Fatal("engineer view should explain scoping and hide the sync button")
	}
	if !strings.Contains(body, `value="anna@example.com" disabled`) {
		t.Fatal("owner field should be fixed to the signed-in engineer")
	}
	_, body = do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	if !strings.Contains(body, "annas-box") || !strings.Contains(body, "sams-box") || !strings.Contains(body, "Sync with AWS") {
		t.Fatalf("admin should see everything:\n%s", body)
	}
}

func TestExtendIsCappedForEngineersNotAdmins(t *testing.T) {
	ts, prov := newServer(t)
	// annas-box joined at t0 and expires in 3 days; 100 days pushes its
	// lifetime past the 90 day ceiling.
	res, body := do(t, ts, "POST", "/accounts/111111111111/extend", "anna@example.com", false, url.Values{"by": {"100d"}}, true)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "past the 90d ceiling") {
		t.Fatalf("engineer extend past the ceiling should be refused with an explanation, got %d:\n%s", res.StatusCode, body)
	}
	if exp := prov.Tags("111111111111")[core.TagExpires]; exp != t0.Add(72*time.Hour).Format(time.RFC3339) {
		t.Fatalf("refused extend must not change the tag, got %s", exp)
	}
	// Within the ceiling it works.
	res, _ = do(t, ts, "POST", "/accounts/111111111111/extend", "anna@example.com", false, url.Values{"by": {"7d"}}, true)
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(prov.Tags("111111111111")[core.TagExpires], t0.Add(10*24*time.Hour).Format("2006-01-02")) {
		t.Fatalf("extend within the ceiling should work, tags=%v", prov.Tags("111111111111"))
	}
	// Admins may go past it.
	res, _ = do(t, ts, "POST", "/accounts/111111111111/extend", "root@example.com", true, url.Values{"by": {"100d"}}, true)
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(prov.Tags("111111111111")[core.TagExpires], t0.Add(110*24*time.Hour).Format("2006-01-02")) {
		t.Fatalf("admin extend past the ceiling should work, tags=%v", prov.Tags("111111111111"))
	}
}

func TestPagesShipTheirOwnScriptsUnderACSP(t *testing.T) {
	ts, _ := newServer(t)
	res, body := do(t, ts, "GET", "/", "anna@example.com", false, nil, false)
	if res.StatusCode != http.StatusOK || strings.Contains(body, "unpkg.com") || !strings.Contains(body, `src="/static/htmx.min.js"`) || strings.Contains(body, "hx-on") {
		t.Fatalf("page should load scripts from this origin and carry no inline handlers:\n%s", body)
	}
	if got := res.Header.Get("Content-Security-Policy"); !strings.Contains(got, "script-src 'self'") || strings.Contains(got, "unsafe-eval") {
		t.Fatalf("csp = %q", got)
	}
	// Icon and logo links carry a version so browsers drop cached copies
	// when the files change, and the versioned URL still resolves.
	if !strings.Contains(body, `href="/static/favicon.svg?v=`) || !strings.Contains(body, `src="/static/logo.svg?v=`) {
		t.Fatalf("icon and logo links should be versioned:\n%s", body)
	}
	if res, _ := do(t, ts, "GET", asset("favicon.svg"), "", false, nil, false); res.StatusCode != http.StatusOK {
		t.Fatalf("versioned favicon = %d", res.StatusCode)
	}
	// Static files need no sign-in and really are there.
	res, body = do(t, ts, "GET", "/static/htmx.min.js", "", false, nil, false)
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(body, "var htmx=") {
		t.Fatalf("htmx should be served from the binary, got %d", res.StatusCode)
	}
	if res, _ := do(t, ts, "GET", "/static/app.js", "", false, nil, false); res.StatusCode != http.StatusOK {
		t.Fatalf("app.js = %d", res.StatusCode)
	}
}

func TestPendingRowIsNotVisibleToStrangers(t *testing.T) {
	ts, _ := newServer(t)
	if res, body := do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-req"}, "purpose": {"secret plans"}}, true); res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("request = %d:\n%s", res.StatusCode, body)
	}
	if res, body := do(t, ts, "GET", "/requests/bobs-req/row", "sam@example.com", false, nil, true); res.StatusCode != http.StatusForbidden || strings.Contains(body, "secret plans") {
		t.Fatalf("a stranger should not see the row, got %d:\n%s", res.StatusCode, body)
	}
	if res, body := do(t, ts, "GET", "/requests/bobs-req/row", "bob@example.com", false, nil, true); res.StatusCode != http.StatusOK || !strings.Contains(body, "secret plans") {
		t.Fatalf("the requester should see their own row, got %d", res.StatusCode)
	}
	if res, _ := doAs(t, ts, "GET", "/requests/bobs-req/row", "lead@example.com", false, true, nil, true); res.StatusCode != http.StatusOK {
		t.Fatalf("an approver should see the row, got %d", res.StatusCode)
	}
}

func TestEngineerCannotTouchOthersAccounts(t *testing.T) {
	ts, prov := newServer(t)
	res, _ := do(t, ts, "GET", "/accounts/222222222222", "anna@example.com", false, nil, false)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("detail of another's account should be 403, got %d", res.StatusCode)
	}
	res, _ = do(t, ts, "POST", "/accounts/222222222222/close", "anna@example.com", false, url.Values{}, true)
	if res.StatusCode != http.StatusForbidden || len(prov.Closed) != 0 {
		t.Fatalf("close of another's account should be refused, got %d closed=%v", res.StatusCode, prov.Closed)
	}
	res, _ = do(t, ts, "POST", "/refresh", "anna@example.com", false, url.Values{}, true)
	if res.StatusCode != http.StatusForbidden || len(prov.Closed) != 0 {
		t.Fatalf("refresh by an engineer should be forbidden, got %d", res.StatusCode)
	}
	_, body := do(t, ts, "POST", "/refresh", "anna@example.com", false, url.Values{}, true)
	if !strings.Contains(body, "only admins") {
		t.Fatalf("refresh refusal should be explained:\n%s", body)
	}
	// Her own account she may close.
	res, _ = do(t, ts, "POST", "/accounts/111111111111/close", "anna@example.com", false, url.Values{}, true)
	if res.StatusCode != http.StatusOK || len(prov.Closed) != 1 || prov.Closed[0] != "111111111111" {
		t.Fatalf("own close should work, got %d closed=%v", res.StatusCode, prov.Closed)
	}
}

func TestRequestApprovalFlow(t *testing.T) {
	ts, prov := newServer(t)
	// bob has no account, so he is under the per-owner limit of 1.
	form := url.Values{"name": {"bobs-box"}, "owner": {"sam@example.com"}, "purpose": {"try bedrock"}}
	res, body := do(t, ts, "POST", "/requests", "bob@example.com", false, form, true)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("request = %d %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "bobs-box") || !strings.Contains(body, "Your pending requests") {
		t.Fatalf("requester should see the pending card:\n%s", body)
	}
	svc := core.NewService(prov, nil, core.DefaultConfig(), nil)
	svc.Now = func() time.Time { return t0 }
	q, err := svc.GetRequest(context.Background(), "bobs-box")
	if err != nil || q.Owner != "bob@example.com" || q.RequestedBy != "bob@example.com" || q.Via != "web" || q.Purpose != "try bedrock" {
		t.Fatalf("queued = %+v %v (owner from the form must be ignored)", q, err)
	}
	// Nothing was created yet.
	if reqs, _ := prov.ListCreateRequests(context.Background()); len(reqs) != 0 {
		t.Fatal("no account may be created before approval")
	}
	// Anna (engineer) cannot see or approve bob's request.
	_, body = do(t, ts, "GET", "/", "anna@example.com", false, nil, false)
	if strings.Contains(body, "bobs-box") {
		t.Fatal("another engineer must not see the request")
	}
	if res, _ := do(t, ts, "POST", "/requests/bobs-box/approve", "anna@example.com", false, url.Values{}, true); res.StatusCode != http.StatusForbidden {
		t.Fatalf("engineer approve should be 403, got %d", res.StatusCode)
	}
	// Bob cannot approve his own request even if he were an approver.
	if _, body := doAs(t, ts, "POST", "/requests/bobs-box/approve", "bob@example.com", false, true, url.Values{}, true); !strings.Contains(body, "cannot approve it") {
		t.Fatalf("self-approval should be refused:\n%s", body)
	}
	// A configured approver (not admin) sees the queue and approves.
	_, body = doAs(t, ts, "GET", "/", "lead@example.com", false, true, nil, false)
	if !strings.Contains(body, "Pending approvals") || !strings.Contains(body, "bobs-box") || !strings.Contains(body, "/requests/bobs-box/approve") {
		t.Fatalf("approver should see the approve button:\n%s", body)
	}
	if strings.Contains(body, "Sync with AWS") {
		t.Fatal("an approver who is not admin must not get the sync button")
	}
	res, _ = doAs(t, ts, "POST", "/requests/bobs-box/approve", "lead@example.com", false, true, url.Values{}, true)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("approve = %d", res.StatusCode)
	}
	prov.Settle()
	if sum, _ := svc.Refresh(context.Background()); len(sum.Placed) != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	a, err := svc.Resolve(context.Background(), "bobs-box")
	if err != nil || a.Owner != "bob@example.com" || a.ApprovedBy != "lead@example.com" || a.RequestedBy != "bob@example.com" {
		t.Fatalf("created = %+v %v", a, err)
	}
	if q, _ := svc.PendingRequests(context.Background()); len(q) != 0 {
		t.Fatal("queue should be empty after approval")
	}

	// Deny path with a reason from the htmx prompt header; withdraw by requester.
	do(t, ts, "POST", "/requests", "carl@example.com", false, url.Values{"name": {"carls-box"}}, true)
	req, _ := http.NewRequest("POST", ts.URL+"/requests/carls-box/deny", strings.NewReader(""))
	req.Header.Set("X-User", "lead@example.com")
	req.Header.Set("X-Approver", "1")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Prompt", "no capacity")
	if r2, err := http.DefaultClient.Do(req); err != nil || r2.StatusCode != http.StatusOK {
		t.Fatalf("deny = %v %v", r2, err)
	}
	if _, err := svc.GetRequest(context.Background(), "carls-box"); err == nil {
		t.Fatal("denied request should be gone")
	}
	do(t, ts, "POST", "/requests", "carl@example.com", false, url.Values{"name": {"carls-two"}}, true)
	if res, _ := do(t, ts, "POST", "/requests/carls-two/withdraw", "anna@example.com", false, url.Values{}, true); res.StatusCode != http.StatusForbidden {
		t.Fatal("only the requester or an admin may withdraw")
	}
	if res, _ := do(t, ts, "POST", "/requests/carls-two/withdraw", "carl@example.com", false, url.Values{}, true); res.StatusCode != http.StatusOK {
		t.Fatal("requester withdraw should work")
	}
	// Admins may request on someone else's behalf.
	do(t, ts, "POST", "/requests", "ops@example.com", true, url.Values{"name": {"for-dana"}, "owner": {"dana@example.com"}}, true)
	if q, err := svc.GetRequest(context.Background(), "for-dana"); err != nil || q.Owner != "dana@example.com" || q.RequestedBy != "ops@example.com" {
		t.Fatalf("admin request = %+v %v", q, err)
	}
}

func TestSelfApprovalSettingShowsTheButton(t *testing.T) {
	prov := fake.New()
	cfg := core.DefaultConfig()
	cfg.InventoryTTL = 0
	cfg.AllowSelfApproval = true
	svc := core.NewService(prov, nil, cfg, nil)
	ts := httptest.NewServer(New(svc, slog.New(slog.DiscardHandler), NoAuth{}, nil, nil).Handler())
	defer ts.Close()
	// No-auth mode: the operator requests and must be able to approve alone.
	res, body := do(t, ts, "POST", "/requests", "", false, url.Values{"name": {"solo"}, "owner": {"john@example.com"}}, true)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "/requests/solo/approve") || strings.Contains(body, ">yours<") {
		t.Fatalf("with self-approval on, the requester should see Approve:\n%s", body)
	}
	if res, _ := do(t, ts, "POST", "/requests/solo/approve", "", false, url.Values{}, true); res.StatusCode != http.StatusOK {
		t.Fatalf("approve = %d", res.StatusCode)
	}
	if q, _ := svc.PendingRequests(context.Background()); len(q) != 0 {
		t.Fatal("request should be approved")
	}
	// The owner field must not be prefilled with the fake "operator" identity.
	_, body = do(t, ts, "GET", "/", "", false, nil, false)
	if strings.Contains(body, `value="operator"`) {
		t.Fatal("owner field should be empty in no-auth mode")
	}
}

func TestFormErrorsKeepFieldsAndDaysAreBareNumbers(t *testing.T) {
	ts, prov := newServer(t)
	// A validation error carries the header the form uses to skip its reset.
	res, body := do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"Bad Name"}, "ttl": {"5"}}, true)
	if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "1" || res.Header.Get("HX-Reswap") != "none" || !strings.Contains(body, "form-error") {
		t.Fatalf("error response should keep the form: %d %v\n%s", res.StatusCode, res.Header, body)
	}
	// A bare number is days; the queued TTL is 5 days.
	res, _ = do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"five-days"}, "ttl": {"5"}}, true)
	if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("valid request = %d %v", res.StatusCode, res.Header)
	}
	q, err := core.NewService(prov, nil, core.DefaultConfig(), nil).GetRequest(context.Background(), "five-days")
	if err != nil || q.TTL != 5*24*time.Hour {
		t.Fatalf("ttl = %v %v", q.TTL, err)
	}
	if d, _ := parseDuration("2"); d != 48*time.Hour {
		t.Fatalf("bare 2 should be two days, got %v", d)
	}
	if d, _ := parseDuration("72h"); d != 72*time.Hour {
		t.Fatal("Go durations still accepted")
	}
	if _, err := parseDuration("0"); err == nil {
		t.Fatal("zero days should be refused")
	}
	// The engineer form is days-only with the ceiling as max; the footer says days.
	_, body = do(t, ts, "GET", "/", "bob@example.com", false, nil, false)
	if !strings.Contains(body, `name="ttl" type="number" min="1" max="90"`) || !strings.Contains(body, "Limits: 90 days and $500") {
		t.Fatalf("days field or footer missing:\n%s", body)
	}
}

func TestAdminEditsPendingRequestInline(t *testing.T) {
	ts, prov := newServer(t)
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"tweak"}, "ttl": {"5"}, "budget": {"20"}, "purpose": {"old"}}, true)
	// Engineers and plain approvers get no Edit button and cannot open the form.
	_, body := doAs(t, ts, "GET", "/", "lead@example.com", false, true, nil, false)
	if strings.Contains(body, "/requests/tweak/edit") {
		t.Fatal("approver without admin should not see Edit")
	}
	if res, _ := doAs(t, ts, "GET", "/requests/tweak/edit", "lead@example.com", false, true, nil, true); res.StatusCode != http.StatusForbidden {
		t.Fatalf("edit form for non-admin = %d", res.StatusCode)
	}
	// Admin opens the inline form, sees current values, saves changes.
	_, body = do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	if !strings.Contains(body, "/requests/tweak/edit") {
		t.Fatal("admin should see Edit")
	}
	res, body := do(t, ts, "GET", "/requests/tweak/edit", "ops@example.com", true, nil, true)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `value="bob@example.com"`) || !strings.Contains(body, `value="5"`) || !strings.Contains(body, `value="old"`) {
		t.Fatalf("edit form should be prefilled:\n%s", body)
	}
	res, body = do(t, ts, "POST", "/requests/tweak/edit", "ops@example.com", true, url.Values{"owner": {"bob@example.com"}, "ttl": {"12"}, "budget": {"40"}, "purpose": {"new"}}, true)
	if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" || !strings.Contains(body, "12d") || !strings.Contains(body, "$40") || !strings.Contains(body, ">new") {
		t.Fatalf("save should re-render the pending card with new values: %d %v\n%s", res.StatusCode, res.Header, body)
	}
	q, err := core.NewService(prov, nil, core.DefaultConfig(), nil).GetRequest(context.Background(), "tweak")
	if err != nil || q.TTL != 12*24*time.Hour || q.BudgetUSD != 40 || q.Purpose != "new" || q.RequestedBy != "bob@example.com" {
		t.Fatalf("stored = %+v %v", q, err)
	}
	// Over the ceiling without override is an error that keeps the form.
	res, _ = do(t, ts, "POST", "/requests/tweak/edit", "ops@example.com", true, url.Values{"budget": {"900"}}, true)
	if res.Header.Get("X-Form-Error") != "1" {
		t.Fatal("over-ceiling edit should be refused")
	}
	// Cancel returns the plain row.
	_, body = do(t, ts, "GET", "/requests/tweak/row", "ops@example.com", true, nil, true)
	if !strings.Contains(body, `id="req-tweak"`) || strings.Contains(body, `name="ttl"`) {
		t.Fatalf("cancel should return the plain row:\n%s", body)
	}
}

func TestAccountPageShowsHistory(t *testing.T) {
	ts, _ := newServer(t)
	do(t, ts, "POST", "/accounts/111111111111/extend", "anna@example.com", false, url.Values{"by": {"2"}}, true)
	_, body := do(t, ts, "GET", "/accounts/111111111111", "anna@example.com", false, nil, false)
	if !strings.Contains(body, "<h3 style=\"margin-top:0\">History</h3>") || !strings.Contains(body, "anna@example.com extended annas-box") || !strings.Contains(body, `<span class="pill">extended</span>`) {
		t.Fatalf("history card missing the extension:\n%s", body)
	}
	// Another owner's page shows only its own events.
	_, body = do(t, ts, "GET", "/accounts/222222222222", "ops@example.com", true, nil, false)
	if strings.Contains(body, "extended annas-box") {
		t.Fatal("history must be scoped to the account")
	}
}

func TestShowClosedSwitchInsteadOfHeaderLink(t *testing.T) {
	ts, prov := newServer(t)
	prov.Seed(core.RemoteAccount{ProviderID: "333333333333", Name: "gone-box", Status: "CLOSED", JoinedAt: t0,
		Tags: map[string]string{core.TagManaged: "true", core.TagOwner: "anna@example.com", core.TagExpires: t0.Format(time.RFC3339), core.TagBudget: "50"}})
	_, body := do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	if strings.Contains(body, `href="/?all=1"`) {
		t.Fatal("header link should be gone")
	}
	if !strings.Contains(body, `id="show-closed"`) || strings.Contains(body, `id="show-closed" type="checkbox" name="all" value="1" checked`) {
		t.Fatalf("switch should be present and off by default:\n%s", body)
	}
	if strings.Contains(body, "gone-box") {
		t.Fatal("closed account hidden by default")
	}
	// Turning the switch on fetches the table with closed accounts and keeps the switch on.
	_, body = do(t, ts, "GET", "/accounts?all=1", "ops@example.com", true, nil, true)
	if !strings.Contains(body, "gone-box") || !strings.Contains(body, `checked`) || !strings.Contains(body, `hx-get="/accounts?all=1"`) {
		t.Fatalf("all=1 should show closed accounts, check the switch, and poll with all=1:\n%s", body)
	}
	// An action posted with the switch state keeps it.
	_, body = do(t, ts, "POST", "/accounts/111111111111/extend", "ops@example.com", true, url.Values{"all": {"1"}, "by": {"1"}}, true)
	if !strings.Contains(body, "gone-box") {
		t.Fatal("an action should keep the show-closed view")
	}
	// Every action form includes the switch so the view survives.
	_, body = do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	if strings.Count(body, `hx-include="#show-closed"`) < 4 {
		t.Fatalf("action forms should include the switch:\n%s", body)
	}
}

func TestPurposeLimitsInForm(t *testing.T) {
	ts, _ := newServer(t)
	_, body := do(t, ts, "GET", "/", "bob@example.com", false, nil, false)
	if !strings.Contains(body, `maxlength="120"`) || !strings.Contains(body, `pattern="`+tagPattern+`"`) {
		t.Fatalf("purpose input should carry the AWS tag limits:\n%s", body)
	}
	res, _ := do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"pct"}, "purpose": {"100% legit"}}, true)
	if res.Header.Get("X-Form-Error") != "1" {
		t.Fatal("server must refuse a purpose the browser pattern would have caught")
	}
}

func TestOnlyAdminsMayOverrideLimits(t *testing.T) {
	ts, prov := newServer(t)
	over := url.Values{"name": {"huge"}, "ttl": {"365d"}, "budget": {"5000"}, "override": {"on"}}
	// An engineer sending the override field is still held to the ceilings.
	_, body := do(t, ts, "POST", "/requests", "bob@example.com", false, over, true)
	if !strings.Contains(body, "exceeds the maximum") {
		t.Fatalf("engineer override must be ignored:\n%s", body)
	}
	// An admin without the box ticked is also refused.
	noBox := url.Values{"name": {"huge"}, "ttl": {"365d"}, "budget": {"5000"}}
	if _, body := do(t, ts, "POST", "/requests", "ops@example.com", true, noBox, true); !strings.Contains(body, "exceeds the maximum") {
		t.Fatalf("admin without override must be refused:\n%s", body)
	}
	// With the box ticked the request is queued and flagged.
	res, body := do(t, ts, "POST", "/requests", "ops@example.com", true, over, true)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "limits overridden") {
		t.Fatalf("admin override should queue and show the badge: %d\n%s", res.StatusCode, body)
	}
	svc := core.NewService(prov, nil, core.DefaultConfig(), nil)
	q, err := svc.GetRequest(context.Background(), "huge")
	if err != nil || !q.OverrideLimits || q.BudgetUSD != 5000 {
		t.Fatalf("queued = %+v %v", q, err)
	}
	// The form tells everyone the ceilings; only admins see the checkbox.
	_, body = do(t, ts, "GET", "/", "bob@example.com", false, nil, false)
	if !strings.Contains(body, "Limits: 90 days and $500 per month.") || strings.Contains(body, `name="override"`) {
		t.Fatalf("engineer form should show limits without the override box:\n%s", body)
	}
	_, body = do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	if !strings.Contains(body, `name="override"`) {
		t.Fatal("admin form should show the override box")
	}
}

func TestSlackSignatureAndInteract(t *testing.T) {
	sl := &Slack{SigningSecret: "s3cret"}
	body := []byte("payload=%7B%22type%22%3A%22block_actions%22%7D")
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	if !sl.verify(ts, sl.sign(ts, body), body, time.Now()) {
		t.Fatal("valid signature should verify")
	}
	if sl.verify(ts, sl.sign(ts, []byte("other")), body, time.Now()) {
		t.Fatal("signature over a different body must fail")
	}
	old := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	if sl.verify(old, sl.sign(old, body), body, time.Now()) {
		t.Fatal("stale timestamps must be rejected")
	}
	// The endpoint rejects unsigned requests before touching the service.
	srv := New(core.NewService(fake.New(), nil, core.DefaultConfig(), nil), slog.New(slog.DiscardHandler), headerAuth{}, sl, nil)
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()
	res, err := http.Post(hs.URL+"/slack/interact", "application/x-www-form-urlencoded", strings.NewReader(string(body)))
	if err != nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned interact = %v %v", res, err)
	}
}

func TestSessionCookieSigning(t *testing.T) {
	a := &OIDCAuth{secret: []byte("test-secret"), cfg: OIDCConfig{RedirectURL: "http://localhost/cb", SessionTTL: time.Hour}, admins: map[string]bool{"ops@example.com": true}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	s := session{Email: "ops@example.com", Name: "Ops", Exp: time.Now().Add(time.Hour)}
	a.setSigned(rec, req, sessionCookie, s, s.Exp)
	cookie := rec.Result().Cookies()[0]

	req2 := httptest.NewRequest("GET", "/", nil)
	req2.AddCookie(cookie)
	v, ok := a.Authenticate(req2)
	if !ok || v.Email != "ops@example.com" || !v.Admin {
		t.Fatalf("viewer = %+v ok=%v", v, ok)
	}

	// Tampered payload is rejected: flip one character of the encoded payload.
	forged := *cookie
	payload, sig, _ := strings.Cut(cookie.Value, ".")
	flip := "A"
	if payload[0] == 'A' {
		flip = "B"
	}
	forged.Value = flip + payload[1:] + "." + sig
	req3 := httptest.NewRequest("GET", "/", nil)
	req3.AddCookie(&forged)
	if _, ok := a.Authenticate(req3); ok {
		t.Fatal("tampered cookie must not authenticate")
	}
	// Expired session is rejected.
	old := session{Email: "ops@example.com", Exp: time.Now().Add(-time.Minute)}
	rec = httptest.NewRecorder()
	a.setSigned(rec, req, sessionCookie, old, time.Now().Add(time.Hour))
	req4 := httptest.NewRequest("GET", "/", nil)
	req4.AddCookie(rec.Result().Cookies()[0])
	if _, ok := a.Authenticate(req4); ok {
		t.Fatal("expired session must not authenticate")
	}
}

func TestWithdrawButtonIgnoresEmailCase(t *testing.T) {
	ts, _ := newServer(t)
	// Identity Center may report the owner with different casing than the
	// sign-in email; the button and the handler must agree.
	if res, body := do(t, ts, "POST", "/requests", "Bob@Example.com", false, url.Values{"name": {"case-box"}}, true); res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("request = %d:\n%s", res.StatusCode, body)
	}
	_, body := do(t, ts, "GET", "/", "bob@example.com", false, nil, false)
	if !strings.Contains(body, "/requests/case-box/withdraw") {
		t.Fatalf("withdraw button should show regardless of email case:\n%s", body)
	}
	if res, _ := do(t, ts, "POST", "/requests/case-box/withdraw", "bob@example.com", false, url.Values{}, true); res.StatusCode != http.StatusOK {
		t.Fatalf("withdraw = %d", res.StatusCode)
	}
}

func TestEditRejectsBadNumbersAndKeepsOverride(t *testing.T) {
	ts, _ := newServer(t)
	svc := func() *core.Service { return nil } // documented: the server owns it; we assert through HTTP
	_ = svc
	if res, _ := doAs(t, ts, "POST", "/requests", "root@example.com", true, true, url.Values{"name": {"over"}, "owner": {"carl@example.com"}, "budget": {"900"}, "override": {"on"}}, true); res.StatusCode != http.StatusOK {
		t.Fatalf("request = %d", res.StatusCode)
	}
	// A bad budget is an error, not a silently unchanged field.
	res, body := doAs(t, ts, "POST", "/requests/over/edit", "root@example.com", true, true, url.Values{"owner": {"carl@example.com"}, "budget": {"lots"}, "override": {"on"}}, true)
	if res.Header.Get("X-Form-Error") != "1" || !strings.Contains(body, "budget must be a positive number") {
		t.Fatalf("bad budget should be reported: %v\n%s", res.Header, body)
	}
	// Unticking override while the budget is over the ceiling is refused.
	res, body = doAs(t, ts, "POST", "/requests/over/edit", "root@example.com", true, true, url.Values{"owner": {"carl@example.com"}, "budget": {"900"}}, true)
	if res.Header.Get("X-Form-Error") != "1" || !strings.Contains(body, "exceeds the maximum") {
		t.Fatalf("removing the override should be refused: %v\n%s", res.Header, body)
	}
	// Saving with the box still ticked keeps it.
	res, body = doAs(t, ts, "POST", "/requests/over/edit", "root@example.com", true, true, url.Values{"owner": {"carl@example.com"}, "budget": {"900"}, "purpose": {"more"}, "override": {"on"}}, true)
	if res.Header.Get("X-Form-Error") != "" || !strings.Contains(body, "limits overridden") {
		t.Fatalf("override should survive an edit: %v\n%s", res.Header, body)
	}
}

func TestCrossOriginPostsAreRefused(t *testing.T) {
	ts, _ := newServer(t)
	post := func(hdr, val string) int {
		req, _ := http.NewRequest("POST", ts.URL+"/accounts/111111111111/close", strings.NewReader(""))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-User", "anna@example.com")
		if hdr != "" {
			req.Header.Set(hdr, val)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if got := post("Sec-Fetch-Site", "cross-site"); got != http.StatusForbidden {
		t.Fatalf("cross-site POST = %d, want 403", got)
	}
	if got := post("Origin", "https://evil.example"); got != http.StatusForbidden {
		t.Fatalf("foreign-origin POST = %d, want 403", got)
	}
	// Same-origin browsers and non-browser clients still work.
	if got := post("Sec-Fetch-Site", "same-origin"); got != http.StatusOK {
		t.Fatalf("same-origin POST = %d, want 200", got)
	}
	// Slack signs its own requests and sends no browser headers; the route is
	// bypassed and fails on its own signature check, not on origin.
	sl := &Slack{SigningSecret: "s3cret"}
	srv := New(core.NewService(fake.New(), nil, core.DefaultConfig(), nil), slog.New(slog.DiscardHandler), headerAuth{}, sl, nil)
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()
	req, _ := http.NewRequest("POST", hs.URL+"/slack/interact", strings.NewReader("payload="))
	req.Header.Set("Origin", "https://evil.example")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("slack route should reach the signature check, got %d", res.StatusCode)
	}
}

func TestBaseURLIsATrustedOrigin(t *testing.T) {
	prov := fake.New()
	cfg := core.DefaultConfig()
	cfg.BaseURL = "https://pp.example/"
	svc := core.NewService(prov, nil, cfg, nil)
	srv := New(svc, slog.New(slog.DiscardHandler), headerAuth{}, nil, nil)
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()
	// A browser without Sec-Fetch-Site behind a proxy that rewrites Host:
	// Origin is the public address, r.Host is the backend's.
	req, _ := http.NewRequest("POST", hs.URL+"/refresh", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-User", "root@example.com")
	req.Header.Set("X-Admin", "1")
	req.Header.Set("Origin", "https://pp.example")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST from the base url origin = %d, want 200", res.StatusCode)
	}
}

func TestBudgetAndLifetimeMustBeRealPositiveNumbers(t *testing.T) {
	ts, prov := newServer(t)
	for _, tc := range []struct{ field, value string }{
		{"budget", "NaN"}, {"budget", "Inf"}, {"budget", "-5"}, {"budget", "0"},
		{"ttl", "-5d"}, {"ttl", "-72h"}, {"ttl", "NaN"}, {"ttl", "Inf"}, {"ttl", "0d"}, {"ttl", "0"},
	} {
		form := url.Values{"name": {"bad-one"}, "owner": {"anna@example.com"}, tc.field: {tc.value}}
		res, body := doAs(t, ts, "POST", "/requests", "anna@example.com", false, false, form, true)
		if res.Header.Get("X-Form-Error") != "1" || !strings.Contains(body, "positive number") {
			t.Errorf("%s=%s should be refused: %v\n%s", tc.field, tc.value, res.Header, body)
		}
	}
	if tags, _ := prov.OUTags(context.Background()); len(tags) != 0 {
		t.Fatalf("nothing should have been queued: %v", tags)
	}
}

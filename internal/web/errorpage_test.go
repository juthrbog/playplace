package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// alertIcon is how the icon template renders the alert symbol.
const alertIcon = `<svg class="i" aria-hidden="true" focusable="false"><use href="#i-alert"></use></svg>`

// note is the refusal note of an error page: the message as the first line
// and the recovery as the body.
func note(msg, recovery string) string {
	return `<div class="refusal">` + alertIcon + `<div class="note-text"><p class="note-title">` + msg + `</p><p class="note-body">` + recovery + `</p></div></div>`
}

func TestUnknownPathIsAStyledNotFound(t *testing.T) {
	ts, _ := newServer(t)
	for _, path := range []string{"/nope", "/accounts/111111111111/x"} {
		res, body := do(t, ts, "GET", path, "anna@example.com", false, nil, false)
		if res.StatusCode != http.StatusNotFound || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
			t.Fatalf("GET %s = %d %q, want a 404 page", path, res.StatusCode, res.Header.Get("Content-Type"))
		}
		if !strings.Contains(res.Header.Get("Content-Security-Policy"), "script-src 'self'") {
			t.Fatalf("GET %s has no CSP", path)
		}
		for _, want := range []string{
			`<title>Page not found · playplace</title>`,
			`<header class="shell">`,
			`<h1 id="error-title">Page not found</h1><span class="muted">404</span>`,
			note("There is no page at this address.", "Check the link, or go back to Home."),
			`<a class="btn btn-primary" href="/">Go to Home</a>`,
			// A signed-in viewer keeps the whole shell.
			`Sign out`,
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("GET %s should contain %q:\n%s", path, want, body)
			}
		}
	}
	// A path outside sign-in has no viewer, so the shell shows only the brand.
	res, body := do(t, ts, "GET", "/auth/nope", "", false, nil, false)
	if res.StatusCode != http.StatusNotFound || !strings.Contains(body, `<h1 id="error-title">Page not found</h1>`) || strings.Contains(body, "Sign out") || strings.Contains(body, "menu-toggle") {
		t.Fatalf("GET /auth/nope = %d, want the signed-out 404 page:\n%s", res.StatusCode, body)
	}
	// A missing static file stays the file server's plain 404.
	res, body = do(t, ts, "GET", "/static/missing.js", "", false, nil, false)
	if res.StatusCode != http.StatusNotFound || strings.Contains(body, "error-title") {
		t.Fatalf("GET /static/missing.js = %d, want the plain 404:\n%s", res.StatusCode, body)
	}
}

func TestWrongMethodStays405(t *testing.T) {
	ts, _ := newServer(t)
	res, _ := do(t, ts, "GET", "/refresh", "anna@example.com", false, nil, false)
	if res.StatusCode != http.StatusMethodNotAllowed || res.Header.Get("Allow") != "POST" {
		t.Fatalf("GET /refresh = %d, Allow %q, want 405 with Allow POST", res.StatusCode, res.Header.Get("Allow"))
	}
	res, _ = do(t, ts, "POST", "/", "anna@example.com", false, url.Values{}, false)
	if res.StatusCode != http.StatusMethodNotAllowed || !strings.Contains(res.Header.Get("Allow"), "GET") {
		t.Fatalf("POST / = %d, Allow %q, want 405 with GET", res.StatusCode, res.Header.Get("Allow"))
	}
}

func TestRefusalsWithoutHTMXAreStyledPages(t *testing.T) {
	ts, _ := newServer(t)
	for _, c := range []struct {
		name, method, path string
		form               url.Values
		code               int
		want               []string
	}{
		{"another's account", "GET", "/accounts/222222222222", nil, http.StatusForbidden, []string{
			`<title>Not allowed · playplace</title>`,
			note("that account belongs to someone else", "Go back to Home to see your own accounts."),
		}},
		{"an unknown account", "GET", "/accounts/999999999999", nil, http.StatusNotFound, []string{
			`<title>Account not found · playplace</title>`,
			note("No account matches 999999999999.", "Go back to Home to see the accounts you can open."),
		}},
		{"a bad extend", "POST", "/accounts/111111111111/extend", url.Values{"by": {"xyz"}}, http.StatusBadRequest, []string{
			`<title>Not done · playplace</title>`,
			note("bad duration xyz", "Go back to Home and try again."),
		}},
	} {
		res, body := do(t, ts, c.method, c.path, "anna@example.com", false, c.form, false)
		if res.StatusCode != c.code || !strings.Contains(body, `<header class="shell">`) || !strings.Contains(body, `<a class="btn btn-primary" href="/">Go to Home</a>`) {
			t.Fatalf("%s = %d, want %d with the shell and the way back:\n%s", c.name, res.StatusCode, c.code, body)
		}
		for _, want := range c.want {
			if !strings.Contains(body, want) {
				t.Fatalf("%s should contain %q:\n%s", c.name, want, body)
			}
		}
	}
}

// TestForbiddenNamesTheRule checks every 403 reason. The htmx answer puts the
// reason in the refusal slot, and the page names it with its recovery.
func TestForbiddenNamesTheRule(t *testing.T) {
	ts, _ := newServer(t)
	if res, body := do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-req"}}, true); res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("request = %d:\n%s", res.StatusCode, body)
	}
	_, home := do(t, ts, "GET", "/", "bob@example.com", false, nil, false)
	version := versionIn(t, home, "bobs-req")
	const home403 = "Go back to Home."
	for _, c := range []struct {
		name, method, path, user string
		approver                 bool
		form                     url.Values
		why, recovery            string
	}{
		{"approve by an engineer", "POST", "/requests/bobs-req/approve", "anna@example.com", false, version, approversOnly, home403},
		{"deny by an engineer", "POST", "/requests/bobs-req/deny", "anna@example.com", false, version, approversOnly, home403},
		{"edit form by an approver", "GET", "/requests/bobs-req/edit", "lead@example.com", true, nil, adminsEditOnly, home403},
		{"edit save by an approver", "POST", "/requests/bobs-req/edit", "lead@example.com", true, version, adminsEditOnly, home403},
		{"row by a stranger", "GET", "/requests/bobs-req/row", "sam@example.com", false, nil, notYourRequest, "Go back to Home to see your own requests."},
		{"withdraw by a stranger", "POST", "/requests/bobs-req/withdraw", "sam@example.com", false, version, cannotWithdraw, home403},
		{"another's account", "GET", "/accounts/222222222222", "anna@example.com", false, nil, notYourAccount, "Go back to Home to see your own accounts."},
		{"sync by an engineer", "POST", "/refresh", "anna@example.com", false, url.Values{}, "only admins can sync with AWS", home403},
		{"budget by an engineer", "POST", "/accounts/111111111111/budget", "anna@example.com", false, url.Values{"budget": {"60"}, "reason": {"more"}}, "only admins can change existing account budgets", home403},
	} {
		res, body := doAs(t, ts, c.method, c.path, c.user, false, c.approver, c.form, true)
		if res.StatusCode != http.StatusForbidden || !strings.Contains(body, `<span>`+c.why+`</span>`) {
			t.Fatalf("%s (htmx) = %d, want 403 with %q:\n%s", c.name, res.StatusCode, c.why, body)
		}
		res, body = doAs(t, ts, c.method, c.path, c.user, false, c.approver, c.form, false)
		if res.StatusCode != http.StatusForbidden || !strings.Contains(body, note(c.why, c.recovery)) {
			t.Fatalf("%s = %d, want the 403 page with %q and %q:\n%s", c.name, res.StatusCode, c.why, c.recovery, body)
		}
	}
}

// signedOutShell matches a shell that holds only the brand link. templ
// writes a space after the link, which a flex row does not draw.
var signedOutShell = regexp.MustCompile(`<header class="shell"><a class="brand" href="/">.*?</a>\s*</header>`)

func TestSignInErrorsAreStyledPages(t *testing.T) {
	a := &OIDCAuth{secret: []byte("test-secret"), cfg: OIDCConfig{RedirectURL: "http://localhost/cb", SessionTTL: time.Hour}}
	rec := httptest.NewRecorder()
	a.callback(rec, httptest.NewRequest("GET", "/auth/callback", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusBadRequest || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("callback without a flow = %d %q, want a 400 page", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !signedOutShell.MatchString(body) || !strings.Contains(body, `<title>Sign-in failed · playplace</title>`) ||
		!strings.Contains(body, note("sign-in expired, start again", signInAgain)) || !strings.Contains(body, `<a class="btn btn-primary" href="/auth/login">Sign in again</a>`) {
		t.Fatalf("the sign-in error page should have the brand-only shell, the note and the way back:\n%s", body)
	}
	for _, bad := range []string{"Sign out", `class="nav"`, "New request", "menu-toggle"} {
		if strings.Contains(body, bad) {
			t.Fatalf("a page for nobody should not show %q:\n%s", bad, body)
		}
	}

	// The provider's error value is echoed, so it must arrive escaped.
	f := flow{State: "st", Nonce: "no", Verifier: oauth2.GenerateVerifier(), Exp: time.Now().Add(time.Minute)}
	rec = httptest.NewRecorder()
	a.setSigned(rec, httptest.NewRequest("GET", "/", nil), flowCookie, f, f.Exp)
	cookie := rec.Result().Cookies()[0]
	req := httptest.NewRequest("GET", "/auth/callback?state=st&error="+url.QueryEscape("<b>x</b>"), nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	a.callback(rec, req)
	body = rec.Body.String()
	if rec.Code != http.StatusUnauthorized || !strings.Contains(body, "provider refused: &lt;b&gt;x&lt;/b&gt;") || strings.Contains(body, "<b>x</b>") {
		t.Fatalf("provider error = %d, want it escaped:\n%s", rec.Code, body)
	}
}

// TestSignInErrorsNameTheRecovery runs the callback to the claim checks. A
// token endpoint returns an unsigned ID token, and the verifier skips the
// signature check, so each refusal comes from the real callback code.
func TestSignInErrorsNameTheRecovery(t *testing.T) {
	const issuer, client = "https://issuer.test", "playplace"
	var claims map[string]any
	tokens := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		enc := base64.RawURLEncoding
		payload, err := json.Marshal(claims)
		if err != nil {
			t.Error(err)
		}
		idToken := enc.EncodeToString([]byte(`{"alg":"none"}`)) + "." + enc.EncodeToString(payload) + "."
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"access_token": "at", "token_type": "Bearer", "id_token": idToken}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(tokens.Close)
	a := &OIDCAuth{
		secret:   []byte("test-secret"),
		cfg:      OIDCConfig{RedirectURL: "http://localhost/cb", SessionTTL: time.Hour, AllowedDomain: "example.com"},
		oauth:    oauth2.Config{ClientID: client, ClientSecret: "s", Endpoint: oauth2.Endpoint{TokenURL: tokens.URL, AuthStyle: oauth2.AuthStyleInParams}, RedirectURL: "http://localhost/cb"},
		verifier: oidc.NewVerifier(issuer, nil, &oidc.Config{ClientID: client, InsecureSkipSignatureCheck: true}),
	}
	signIn := func(c map[string]any) *httptest.ResponseRecorder {
		claims = map[string]any{"iss": issuer, "aud": client, "sub": "1", "exp": time.Now().Add(time.Hour).Unix(), "nonce": "no"}
		for k, v := range c {
			claims[k] = v
		}
		f := flow{State: "st", Nonce: "no", Verifier: oauth2.GenerateVerifier(), Exp: time.Now().Add(time.Minute)}
		rec := httptest.NewRecorder()
		a.setSigned(rec, httptest.NewRequest("GET", "/", nil), flowCookie, f, f.Exp)
		req := httptest.NewRequest("GET", "/auth/callback?state=st&code=c", nil)
		req.AddCookie(rec.Result().Cookies()[0])
		rec = httptest.NewRecorder()
		a.callback(rec, req)
		return rec
	}
	// A good token signs in, so the cases below fail at their own check.
	if rec := signIn(map[string]any{"email": "ok@example.com"}); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("a good sign-in = %d %q:\n%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	for _, c := range []struct {
		name          string
		claims        map[string]any
		code          int
		msg, recovery string
	}{
		{"nonce", map[string]any{"email": "ok@example.com", "nonce": "other"}, http.StatusUnauthorized, "nonce mismatch", signInAgain},
		{"no email", map[string]any{}, http.StatusUnauthorized, "id token has no email claim", "Sign in with an account that has an email address."},
		{"unverified", map[string]any{"email": "ok@example.com", "email_verified": false}, http.StatusUnauthorized, "email is not verified at the provider", "Verify your email at the provider, then sign in again."},
		{"other domain", map[string]any{"email": "ok@other.test"}, http.StatusForbidden, "this sign-in is limited to example.com", "Sign in with an account at example.com."},
	} {
		rec := signIn(c.claims)
		if body := rec.Body.String(); rec.Code != c.code || !strings.Contains(body, note(c.msg, c.recovery)) || !signedOutShell.MatchString(body) {
			t.Fatalf("%s = %d, want %d with %q and %q:\n%s", c.name, rec.Code, c.code, c.msg, c.recovery, body)
		}
	}
}

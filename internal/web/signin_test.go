package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// A sign-in returns only to a path on this host. localPath refuses an
// address with a scheme or a host, and a path that a browser reads as
// another host. It also refuses a control character, a fragment, a long
// path and the sign-in pages.
func TestLocalPathKeepsTheSignInOnThisHost(t *testing.T) {
	for _, s := range []string{"/", "/?req=box", "/accounts/111111111111?history_cursor=x", "/%2F%2Fevil.example"} {
		if got := localPath(s); got != s {
			t.Errorf("localPath(%q) = %q, want it kept", s, got)
		}
	}
	// url.Parse finds no host in "///evil.example" and accepts the C1
	// control U+0085, so only the checks before it refuse those two.
	for _, s := range []string{
		"", "box", "//evil.example", "///evil.example", "https://evil.example/", "/\\evil.example",
		"/auth/login", "/auth/callback?x=1", "/x\r\nSet-Cookie: a=b", "/x\u0085y", "/x#frag",
		"/" + strings.Repeat("a", maxReturnPath),
	} {
		if got := localPath(s); got != "" {
			t.Errorf("localPath(%.60q) = %.60q, want it refused", s, got)
		}
	}
}

// A page visit without a session passes its path and query to the sign-in,
// so a link to a slip opens the slip after sign-in. The home page needs no
// return path, and a path that a browser reads as another host gets none.
// An htmx request and a form post keep the plain login path.
func TestAPageVisitKeepsItsAddressThroughSignIn(t *testing.T) {
	ts, _ := newServer(t)
	for _, c := range []struct{ method, path, want string }{
		{"GET", "/", "/auth/login"},
		{"GET", "/?req=bobs-box", "/auth/login?next=%2F%3Freq%3Dbobs-box"},
		{"GET", "/accounts/111111111111", "/auth/login?next=%2Faccounts%2F111111111111"},
		{"GET", "//evil.example", "/auth/login"},
	} {
		if res, _ := do(t, ts, c.method, c.path, "", false, nil, false); res.StatusCode != http.StatusFound || res.Header.Get("Location") != c.want {
			t.Errorf("%s %s = %d %q, want 302 to %q", c.method, c.path, res.StatusCode, res.Header.Get("Location"), c.want)
		}
	}
	if res, _ := do(t, ts, "GET", "/accounts", "", false, nil, true); res.StatusCode != http.StatusUnauthorized || res.Header.Get("HX-Redirect") != "/auth/login" {
		t.Errorf("htmx GET /accounts = %d %q, want 401 with HX-Redirect /auth/login", res.StatusCode, res.Header.Get("HX-Redirect"))
	}
	if res, _ := do(t, ts, "POST", "/refresh", "", false, nil, false); res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/auth/login" {
		t.Errorf("POST /refresh = %d %q, want 302 to /auth/login", res.StatusCode, res.Header.Get("Location"))
	}
}

// The login stores the asked page in the signed flow cookie, and the
// callback returns there. Both check the path, so a crafted link never
// sends the person to another host.
func TestSignInReturnsToTheAskedPage(t *testing.T) {
	a, signIn := testSignIn(t)
	nextOf := func(target string) string {
		t.Helper()
		rec := httptest.NewRecorder()
		a.login(rec, httptest.NewRequest("GET", target, nil))
		cookies := rec.Result().Cookies()
		if rec.Code != http.StatusFound || len(cookies) != 1 || cookies[0].Name != flowCookie {
			t.Fatalf("login %s = %d with cookies %v, want 302 with the flow cookie", target, rec.Code, cookies)
		}
		req := httptest.NewRequest("GET", "/auth/callback", nil)
		req.AddCookie(cookies[0])
		var f flow
		if err := a.readSigned(req, flowCookie, &f); err != nil {
			t.Fatalf("login %s: the flow cookie does not read back: %v", target, err)
		}
		return f.Next
	}
	if got := nextOf("/auth/login?next=%2F%3Freq%3Dbox"); got != "/?req=box" {
		t.Errorf("the flow of a login with next /?req=box holds %q", got)
	}
	if got := nextOf("/auth/login?next=%2F%2Fevil.example"); got != "" {
		t.Errorf("the flow of a login with next //evil.example holds %q, want nothing", got)
	}
	for next, want := range map[string]string{"/?req=box": "/?req=box", "https://evil.example/": "/"} {
		f := flow{State: "st", Nonce: "no", Verifier: oauth2.GenerateVerifier(), Exp: time.Now().Add(time.Minute), Next: next}
		if rec := signIn(map[string]any{"email": "ok@example.com"}, f); rec.Code != http.StatusFound || rec.Header().Get("Location") != want {
			t.Errorf("a sign-in with next %q = %d %q, want 302 to %q:\n%s", next, rec.Code, rec.Header().Get("Location"), want, rec.Body.String())
		}
	}
}

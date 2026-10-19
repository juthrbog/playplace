package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"playplace/internal/audit"
	"playplace/internal/core"
)

// emptyRegion is the live region as a page renders it.
const emptyRegion = `<p id="notice" class="notice" role="status" aria-live="polite"></p>`

// regionStart is the start of main on every page but home: the empty live
// region comes first.
const regionStart = `<main>` + emptyRegion

// cardRegion is the live region of the home page: in the pending card, under
// its head and above the refusal slot of the card.
var cardRegion = regexp.MustCompile(`<section id="pending" class="panel" aria-labelledby="pending-title">(?:<!--.*?-->)?<div class="panel-head">.*?</div>` + regexp.QuoteMeta(emptyRegion) + `<div id="pending-error" class="refusal"></div>`)

var (
	otherLiveRoles = regexp.MustCompile(`role="(alert|log|timer|marquee)"`)
	// regionPart is what an htmx answer writes into the live region.
	regionPart = regexp.MustCompile(`<p id="notice" hx-swap-oob="innerHTML">(.*?)</p>`)
	// slotPart and hiddenCopy are the two parts of a refusal.
	slotPart   = regexp.MustCompile(`<div id="(form-error|pending-error)" hx-swap-oob="innerHTML">` + regexp.QuoteMeta(alertIcon) + `<span>(.*?)</span></div>`)
	hiddenCopy = regexp.MustCompile(`<p id="notice" hx-swap-oob="innerHTML"><span class="vh">(.*?)</span></p>`)
	// cssRule is one innermost rule of a stylesheet: its selector list and
	// its declarations.
	cssRule = regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)
)

// incompleteHistory reads one event and reports that other records were
// unreadable, so the account page shows the incomplete banner.
type incompleteHistory struct{}

func (incompleteHistory) Where() string { return "test" }
func (incompleteHistory) Search(context.Context, audit.Filter) (audit.Page, error) {
	return audit.Page{Incomplete: true, Events: []core.AuditEvent{{ID: "e1", JourneyID: "new-journey", At: t0, Event: "requested", Message: "asked"}}}, nil
}

// Every page has one live region, the empty notice. The home page holds it
// in the pending card, next to the decisions, and every other page at the
// top of main. The other notes on a page are not live, so a screen reader
// hears each message once and from one place.
func TestOnePoliteLiveRegionPerPage(t *testing.T) {
	ts, _ := newServer(t)
	page := func(path, user string, admin bool, want int) string {
		t.Helper()
		res, body := do(t, ts, "GET", path, user, admin, nil, false)
		if res.StatusCode != want {
			t.Fatalf("GET %s as %s = %d, want %d", path, user, res.StatusCode, want)
		}
		return body
	}
	journey := func(h History, path string) string {
		t.Helper()
		_, body := do(t, journeyServer(t, h), "GET", path, "bob@example.com", false, nil, false)
		return body
	}
	oidc := &OIDCAuth{secret: []byte("test-secret"), cfg: OIDCConfig{RedirectURL: "http://localhost/cb", SessionTTL: time.Hour}}
	rec := httptest.NewRecorder()
	oidc.callback(rec, httptest.NewRequest("GET", "/auth/callback", nil))

	for _, tc := range []struct {
		name, body, holds string
		home              bool
	}{
		{"admin home", page("/", "ops@example.com", true, http.StatusOK), `id="pending"`, true},
		{"engineer home", page("/", "anna@example.com", false, http.StatusOK), `id="form-error"`, true},
		{"admin account page", page("/accounts/111111111111", "ops@example.com", true, http.StatusOK), `id="form-error"`, false},
		{"bad history cursor", journey(&audit.Memory{}, "/accounts/111111111111?history_cursor=bad"), "Invalid history cursor", false},
		{"history unavailable", journey(unavailableHistory{}, "/accounts/111111111111"), "History is unavailable", false},
		{"history incomplete", journey(incompleteHistory{}, "/accounts/111111111111"), "History is incomplete: some stored records", false},
		{"not found", page("/nope", "anna@example.com", false, http.StatusNotFound), "Page not found", false},
		{"forbidden", page("/accounts/222222222222", "anna@example.com", false, http.StatusForbidden), "Not allowed", false},
		{"sign-in error", rec.Body.String(), "Sign-in failed", false},
	} {
		// Each page shows the note it is chosen for, so the checks below
		// run on that note.
		if !strings.Contains(tc.body, tc.holds) {
			t.Errorf("%s: the page lacks %q:\n%s", tc.name, tc.holds, tc.body)
			continue
		}
		if tc.home && (strings.Contains(tc.body, regionStart) || !cardRegion.MatchString(tc.body)) {
			t.Errorf("%s: the empty live region is not in the pending card, under its head:\n%s", tc.name, tc.body)
		}
		if !tc.home && !strings.Contains(tc.body, regionStart) {
			t.Errorf("%s: main does not start with the empty live region:\n%s", tc.name, tc.body)
		}
		for attr, want := range map[string]int{`id="notice"`: 1, "aria-live=": 1, `aria-live="polite"`: 1, `role="status"`: 1, "<output": 0} {
			if n := strings.Count(tc.body, attr); n != want {
				t.Errorf("%s: %s occurs %d times, want %d", tc.name, attr, n, want)
			}
		}
		if m := otherLiveRoles.FindString(tc.body); m != "" {
			t.Errorf("%s: a second live region: %s", tc.name, m)
		}
	}
}

// The answers write into the region node, never over it, so the node with
// its role stays and screen readers keep following it. A refusal slot keeps
// its class the same way. A pending card in an answer holds the empty
// region, which app.js keeps as it is on the page.
func TestMessagesWriteIntoTheRegionNode(t *testing.T) {
	ts, _ := newServer(t)
	_, sync := do(t, ts, "POST", "/refresh", "ops@example.com", true, url.Values{}, true)
	syncIcon := `<svg class="i" aria-hidden="true" focusable="false"><use href="#i-sync"></use></svg>`
	if m := regionPart.FindStringSubmatch(sync); m == nil || !strings.HasPrefix(m[1], syncIcon+"<span>Synced with AWS") {
		t.Errorf("the Sync answer does not write its message with the sync icon into the region:\n%s", sync)
	}
	_, refused := do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"Bad Name"}}, true)
	if !strings.Contains(refused, `<div id="form-error" hx-swap-oob="innerHTML">`) {
		t.Errorf("a refused request does not write into its slot:\n%s", refused)
	}
	for _, body := range []string{sync, refused} {
		if regexp.MustCompile(`<[^>]+id="(notice|form-error|pending-error)"[^>]*hx-swap-oob="true"`).MatchString(body) {
			t.Errorf("an answer replaces the region or a slot node:\n%s", body)
		}
		if strings.Contains(strings.ReplaceAll(body, emptyRegion, ""), `class="notice"`) || strings.Contains(body, `class="refusal" hx-swap-oob`) {
			t.Errorf("an answer carries the class of a node it writes into:\n%s", body)
		}
	}
}

// Every action says what it did in the region, with the check in shelf ink.
func TestActionsAnswerInTheRegion(t *testing.T) {
	ts, _ := newServer(t)
	// Each owner may hold one open or pending account, so four people ask.
	for name, who := range map[string]string{"to-approve": "bob", "to-deny": "carl", "to-withdraw": "dina", "to-edit": "erin"} {
		if res, body := do(t, ts, "POST", "/requests", who+"@example.com", false, url.Values{"name": {name}}, true); res.Header.Get("X-Form-Error") != "" {
			t.Fatalf("submit %s = %s", name, body)
		}
	}
	_, home := do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	_, edit := do(t, ts, "GET", "/requests/to-edit/edit", "ops@example.com", true, nil, true)
	saved := editFormValues(t, edit, "to-edit")
	saved.Set("budget", "77")
	deny := versionIn(t, home, "to-deny")
	deny.Set("reason", "not now")

	check := `<svg class="i i-ok" aria-hidden="true" focusable="false"><use href="#i-check"></use></svg>`
	for _, tc := range []struct {
		path string
		form url.Values
		msg  string
	}{
		{"/requests", url.Values{"name": {"from-the-form"}}, "Sent the request for from-the-form."},
		{"/requests/to-approve/approve", versionIn(t, home, "to-approve"), "Approved to-approve."},
		{"/requests/to-deny/deny", deny, "Denied to-deny."},
		{"/requests/to-withdraw/withdraw", versionIn(t, home, "to-withdraw"), "Withdrew the request for to-withdraw."},
		{"/requests/to-edit/edit", saved, "Saved to-edit."},
		// The seed expires 72 hours after t0, and +7d moves it a week on.
		{"/accounts/111111111111/extend", url.Values{"by": {"7d"}}, "Extended annas-box to Sep 25 12:00 UTC."},
		{"/accounts/222222222222/close", url.Values{}, "Requested closure of sams-box."},
	} {
		res, body := do(t, ts, "POST", tc.path, "ops@example.com", true, tc.form, true)
		if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" {
			t.Fatalf("%s = %d %v\n%s", tc.path, res.StatusCode, res.Header, body)
		}
		if n := len(regionPart.FindAllString(body, -1)); n != 1 {
			t.Errorf("%s: the answer writes the region %d times, want once", tc.path, n)
		}
		if m := regionPart.FindStringSubmatch(body); m == nil || m[1] != check+"<span>"+tc.msg+"</span>" {
			t.Errorf("%s: the region does not say %q with the check:\n%s", tc.path, tc.msg, body)
		}
	}
	// The check is the healthy green. The Sync icon keeps the region's grey.
	if got := cssRules(t)[".notice .i-ok"]["color"]; got != "var(--shelf-ink)" {
		t.Errorf(".notice .i-ok color = %q, want var(--shelf-ink)", got)
	}
}

// A refusal shows in its slot and reaches the region as a hidden copy with
// the same text, so a screen reader hears it and nobody sees it twice.
func TestRefusalsAlsoReachTheRegion(t *testing.T) {
	ts, _ := newServer(t)
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"stale-one"}}, true)
	_, page := doAs(t, ts, "GET", "/", "lead@example.com", false, true, nil, false)
	seen := versionIn(t, page, "stale-one")
	if res, body := do(t, ts, "POST", "/requests/stale-one/withdraw", "bob@example.com", false, seen, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("withdraw = %s", body)
	}
	for _, tc := range []struct {
		name, path, user string
		admin, approver  bool
		form             url.Values
		code             int
		slot             string
	}{
		{"stale approve", "/requests/stale-one/approve", "lead@example.com", false, true, seen, http.StatusOK, "pending-error"},
		{"bad budget", "/requests", "ops@example.com", true, true, url.Values{"name": {"big-one"}, "budget": {"lots"}}, http.StatusOK, "form-error"},
		{"engineer sync", "/refresh", "anna@example.com", false, false, url.Values{}, http.StatusForbidden, "form-error"},
		{"account gone", "/accounts/999999999999/extend", "ops@example.com", true, true, url.Values{}, http.StatusOK, "form-error"},
	} {
		res, body := doAs(t, ts, "POST", tc.path, tc.user, tc.admin, tc.approver, tc.form, true)
		if res.StatusCode != tc.code || res.Header.Get("X-Form-Error") != "1" || res.Header.Get("HX-Reswap") != "none" {
			t.Errorf("%s = %d %v, want %d with the refusal headers", tc.name, res.StatusCode, res.Header, tc.code)
			continue
		}
		slot, region := slotPart.FindStringSubmatch(body), hiddenCopy.FindStringSubmatch(body)
		if slot == nil || region == nil || slot[1] != tc.slot || slot[2] == "" || slot[2] != region[1] {
			t.Errorf("%s: want the refusal in %s and the same text hidden in the region:\n%s", tc.name, tc.slot, body)
		}
		if n := len(regionPart.FindAllString(body, -1)); n != 1 {
			t.Errorf("%s: the answer writes the region %d times, want once", tc.name, n)
		}
	}
}

// The web layer parses the days and the budget itself, so it knows which
// field a parse error is about. The answer names the field and the slot,
// and app.js marks the field and ties it to the refusal. A core refusal
// names no field.
func TestFieldRefusalsNameTheirInput(t *testing.T) {
	ts, _ := newServer(t)
	do(t, ts, "POST", "/requests", "erin@example.com", false, url.Values{"name": {"to-edit"}}, true)
	_, edit := do(t, ts, "GET", "/requests/to-edit/edit", "ops@example.com", true, nil, true)
	badEdit := editFormValues(t, edit, "to-edit")
	badEdit.Set("budget", "lots")
	for _, tc := range []struct {
		name, path  string
		form        url.Values
		field, slot string
	}{
		{"request days", "/requests", url.Values{"name": {"some-days"}, "ttl": {"soon"}}, "ttl", "form-error"},
		{"request budget", "/requests", url.Values{"name": {"some-money"}, "budget": {"lots"}}, "budget", "form-error"},
		{"edit budget", "/requests/to-edit/edit", badEdit, "budget", "pending-error"},
		{"account budget", "/accounts/111111111111/budget", url.Values{"budget": {"lots"}, "reason": {"more"}}, "budget", "form-error"},
		{"core refusal", "/requests", url.Values{"name": {"Bad Name"}}, "", ""},
	} {
		res, body := do(t, ts, "POST", tc.path, "ops@example.com", true, tc.form, true)
		if res.Header.Get("X-Form-Error") != "1" {
			t.Errorf("%s was not refused:\n%s", tc.name, body)
			continue
		}
		if f, s := res.Header.Get(invalidFieldHeader), res.Header.Get(invalidSlotHeader); f != tc.field || s != tc.slot {
			t.Errorf("%s names field %q in slot %q, want %q in %q", tc.name, f, s, tc.field, tc.slot)
		}
	}
	js := staticText(t, "app.js")
	for _, want := range []string{`"` + invalidFieldHeader + `"`, `"` + invalidSlotHeader + `"`, `setAttribute("aria-invalid", "true")`, `setAttribute("aria-describedby", slot)`, `removeAttribute("aria-invalid")`, `removeAttribute("aria-describedby")`} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
}

// The poll runs every 15 seconds without the viewer doing anything, so it
// never speaks. Its pending card holds only the empty region node.
func TestPollNeverSpeaks(t *testing.T) {
	ts, _ := newServer(t)
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true)
	res, body := poll(t, ts, "/accounts", "ops@example.com", true, true, "")
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `<section id="accounts"`) {
		t.Fatalf("poll = %d:\n%s", res.StatusCode, body)
	}
	if regionPart.MatchString(body) || strings.Count(body, `id="notice"`) != strings.Count(body, emptyRegion) {
		t.Errorf("the poll writes into the live region:\n%s", body)
	}
}

// cssRuleSet is one innermost rule of app.css: its selectors, each with its
// spaces made single, and its declarations.
type cssRuleSet struct {
	sels  []string
	decls map[string]string
}

// splitSelectors splits a selector list at the commas outside parentheses,
// so :is(button, .btn) stays one piece.
func splitSelectors(list string) []string {
	var out []string
	depth, from := 0, 0
	for i := 0; i <= len(list); i++ {
		if i == len(list) || (list[i] == ',' && depth == 0) {
			out = append(out, strings.Join(strings.Fields(list[from:i]), " "))
			from = i + 1
			continue
		}
		switch list[i] {
		case '(':
			depth++
		case ')':
			depth--
		}
	}
	return out
}

// cssRuleSets returns every innermost rule of app.css.
func cssRuleSets(t *testing.T) []cssRuleSet {
	t.Helper()
	var out []cssRuleSet
	for _, m := range cssRule.FindAllStringSubmatch(appCSS(t), -1) {
		decls := map[string]string{}
		for _, d := range declarations("{" + m[2] + "}") {
			decls[d.prop] = d.value
		}
		out = append(out, cssRuleSet{splitSelectors(m[1]), decls})
	}
	return out
}

// cssRules returns the declarations of each innermost rule of app.css by its
// selector list, with the selectors joined by ", ".
func cssRules(t *testing.T) map[string]map[string]string {
	t.Helper()
	out := map[string]map[string]string{}
	for _, r := range cssRuleSets(t) {
		out[strings.Join(r.sels, ", ")] = r.decls
	}
	return out
}

// The region stays in the accessibility tree. Empty, or holding only the
// hidden copy of a refusal, it is hidden the .vh way. No rule for it sets
// display: none.
func TestEmptyRegionHidesTheVhWay(t *testing.T) {
	rules := cssRules(t)
	for sel, decls := range rules {
		if (strings.Contains(sel, ".notice") || strings.Contains(sel, "#notice")) && decls["display"] == "none" {
			t.Errorf("%s hides the live region with display: none", sel)
		}
	}
	hidden, ok := rules[".notice:empty, .notice:has(> .vh:only-child)"]
	if !ok {
		t.Fatal("app.css has no rule for an empty region and a region with only the hidden copy")
	}
	for prop, want := range map[string]string{"position": "absolute", "width": "1px", "height": "1px", "overflow": "hidden", "clip-path": "inset(50%)"} {
		if hidden[prop] != want {
			t.Errorf("the hidden region sets %s: %q, want %q", prop, hidden[prop], want)
		}
	}
}

// htmx does not swap a 4xx answer, so app.js takes the out-of-band parts of
// a 403 that asks for no main swap. The answer keeps its status.
func TestForbiddenAnswersReachThePage(t *testing.T) {
	js := staticText(t, "app.js")
	_, listener, _ := strings.Cut(js, `document.addEventListener("htmx:beforeSwap"`)
	listener, _, _ = strings.Cut(listener, "\n});")
	for _, want := range []string{"status === 403", `getResponseHeader("HX-Reswap") === "none"`, "shouldSwap = true", "isError = false"} {
		if !strings.Contains(listener, want) {
			t.Errorf("the beforeSwap listener lacks %s", want)
		}
	}
	ts, _ := newServer(t)
	res, body := do(t, ts, "POST", "/refresh", "anna@example.com", false, url.Values{}, true)
	if res.StatusCode != http.StatusForbidden || res.Header.Get("HX-Reswap") != "none" || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("an htmx 403 = %d %v, want 403 with HX-Reswap none and HTML", res.StatusCode, res.Header)
	}
	if !strings.HasPrefix(body, `<div id="form-error" hx-swap-oob="innerHTML">`) || !hiddenCopy.MatchString(body) {
		t.Errorf("the 403 answer lacks the slot or the region part:\n%s", body)
	}
}

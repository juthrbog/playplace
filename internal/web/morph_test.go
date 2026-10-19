package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var startTag = regexp.MustCompile(`<[a-z][a-z0-9]*\s[^>]*>`)

// tagsWith returns the start tags in body that hold attr.
func tagsWith(body, attr string) []string {
	var out []string
	for _, tag := range startTag.FindAllString(body, -1) {
		if strings.Contains(tag, attr) {
			out = append(out, tag)
		}
	}
	return out
}

// Every swap that changes the accounts panel or the pending card is a morph,
// so focus, scroll and typed values stay. The Edit and Cancel buttons keep
// outerHTML, because they change the row type. Every form that posts drops
// a second click while its first request runs.
func TestMorphSwapsEverywhereTheAccountsPanelChanges(t *testing.T) {
	ts, _ := newServer(t)
	if res, body := do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("submit = %s", body)
	}
	_, admin := do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	_, approver := doAs(t, ts, "GET", "/", "lead@example.com", false, true, nil, false)
	_, account := do(t, ts, "GET", "/accounts/111111111111", "ops@example.com", true, nil, false)
	_, edit := do(t, ts, "GET", "/requests/bobs-box/edit", "ops@example.com", true, nil, true)

	for _, tc := range []struct {
		name, body                     string
		page                           bool
		accounts, pending, posts, gets int // minimum counts, so an empty match set fails
	}{
		// Sync, New request, +7d and Close on two rows. Approve, Deny and
		// Withdraw on bob's row. Those 9 forms post.
		{"admin home", admin, true, 6, 3, 9, 1},
		{"approver home", approver, true, 1, 2, 3, 0},
		{"account page", account, true, 0, 0, 1, 0},
		{"edit row", edit, false, 0, 1, 1, 1},
	} {
		if tc.page && !strings.Contains(tc.body, `<body hx-ext="morph">`) {
			t.Errorf("%s: the body does not load the morph extension", tc.name)
		}
		accounts := tagsWith(tc.body, `hx-target="#accounts"`)
		pending := tagsWith(tc.body, `hx-target="#pending"`)
		for _, tag := range append(accounts, pending...) {
			if !strings.Contains(tag, `hx-swap="morph"`) {
				t.Errorf("%s: a swap into the panel is not a morph: %s", tc.name, tag)
			}
		}
		posts := tagsWith(tc.body, "hx-post=")
		for _, tag := range posts {
			if !strings.HasSuffix(tag, ` hx-sync="this:drop">`) && !strings.Contains(tag, ` hx-sync="this:drop" `) {
				t.Errorf("%s: a form that posts sends a second click: %s", tc.name, tag)
			}
			// A decision in the pending card answers with the card.
			if regexp.MustCompile(`hx-post="/requests/[^"]+/(approve|deny|withdraw|edit)"`).MatchString(tag) && !strings.Contains(tag, `hx-target="#pending"`) {
				t.Errorf("%s: a pending card form does not target the card: %s", tc.name, tag)
			}
		}
		gets := regexp.MustCompile(`<button [^>]*hx-get="/requests/[^"]+/(edit|row)"[^>]*>`).FindAllString(tc.body, -1)
		for _, tag := range gets {
			if !strings.Contains(tag, `hx-swap="outerHTML"`) {
				t.Errorf("%s: Edit or Cancel no longer swaps the whole row: %s", tc.name, tag)
			}
		}
		if len(accounts) < tc.accounts || len(pending) < tc.pending || len(posts) < tc.posts || len(gets) < tc.gets {
			t.Errorf("%s: found %d #accounts targets, %d #pending targets, %d posts and %d row buttons, want at least %d, %d, %d and %d",
				tc.name, len(accounts), len(pending), len(posts), len(gets), tc.accounts, tc.pending, tc.posts, tc.gets)
		}
	}

	_, section, ok := strings.Cut(admin, `<section id="accounts"`)
	if tag, _, _ := strings.Cut(section, ">"); !ok || !strings.Contains(tag, `hx-swap="morph"`) {
		t.Errorf("the poll does not morph the section: <section id=\"accounts\"%s>", tag)
	}
	if strings.Contains(admin, `hx-swap-oob`) {
		t.Error("the home page carries an out-of-band marker that only an htmx answer needs")
	}
}

// A decision made in the pending card answers with the card as the main
// swap and the accounts section out of band. htmx swaps out-of-band parts
// first, and the main swap finds the morph extension from the form. If the
// card were out of band, the form would leave the page first, and htmx would
// fall back to an innerHTML swap that nests a section in itself. Requests
// from outside the card keep the accounts section as the main swap.
func TestPendingCardAnswersWithTheCard(t *testing.T) {
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

	card := `<section id="pending" class="panel" aria-labelledby="pending-title">`
	for _, tc := range []struct {
		path string
		form url.Values
	}{
		{"/requests/to-approve/approve", versionIn(t, home, "to-approve")},
		{"/requests/to-deny/deny", deny},
		{"/requests/to-withdraw/withdraw", versionIn(t, home, "to-withdraw")},
		{"/requests/to-edit/edit", saved},
	} {
		res, body := do(t, ts, "POST", tc.path, "ops@example.com", true, tc.form, true)
		if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" {
			t.Fatalf("%s = %d %v\n%s", tc.path, res.StatusCode, res.Header, body)
		}
		if !strings.HasPrefix(body, card) {
			t.Errorf("%s: the answer does not start with the card as its main part:\n%s", tc.path, body)
		}
		_, section, ok := strings.Cut(body, `<section id="accounts"`)
		if tag, _, _ := strings.Cut(section, ">"); !ok || !strings.Contains(tag, `hx-swap-oob="morph"`) {
			t.Errorf("%s: the accounts section is not an out-of-band morph: <section id=\"accounts\"%s>", tc.path, tag)
		}
		if strings.Count(body, `id="pending"`) != 1 || strings.Count(body, `id="accounts"`) != 1 {
			t.Errorf("%s: the answer holds the card or the section more than once:\n%s", tc.path, body)
		}
		if !strings.Contains(body, `<span id="queue-count" hx-swap-oob="true">`) {
			t.Errorf("%s: the queue count is missing:\n%s", tc.path, body)
		}
	}

	for _, tc := range []struct {
		path string
		form url.Values
	}{
		{"/requests", url.Values{"name": {"from-the-form"}}},
		{"/refresh", url.Values{}},
	} {
		_, body := do(t, ts, "POST", tc.path, "ops@example.com", true, tc.form, true)
		tag, _, _ := strings.Cut(body, ">")
		if !strings.HasPrefix(body, `<section id="accounts"`) || strings.Contains(tag, "hx-swap-oob") {
			t.Errorf("%s: the accounts section is not the main part: %s", tc.path, tag)
		}
		if !strings.Contains(body, `<section id="pending" class="panel" hx-swap-oob="morph"`) {
			t.Errorf("%s: the pending card is not an out-of-band morph:\n%s", tc.path, body)
		}
	}
}

// Account rows carry the account id, so idiomorph pairs each row with its
// own new row. A focused +7d then never turns into another account's button.
func TestAccountRowsHaveStableIDs(t *testing.T) {
	ts, _ := newServer(t)
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true)
	_, body := do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	for _, want := range []string{`<tr id="acct-111111111111">`, `<tr id="acct-222222222222">`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`\sid="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		if seen[m[1]] {
			t.Errorf("the id %q occurs twice", m[1])
		}
		seen[m[1]] = true
	}
	if len(seen) < 10 {
		t.Fatalf("found only %d ids, so the page did not render:\n%s", len(seen), body)
	}
}

// The edit form is marked, so a morph keeps the open edit row and what the
// admin typed in it.
func TestEditFormIsMarked(t *testing.T) {
	ts, _ := newServer(t)
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true)
	_, body := do(t, ts, "GET", "/requests/bobs-box/edit", "ops@example.com", true, nil, true)
	if !regexp.MustCompile(`<form [^>]*\sdata-editing[\s>]`).MatchString(body) {
		t.Errorf("the edit form has no data-editing:\n%s", body)
	}
	if !regexp.MustCompile(`(?s)<tr id="req-bobs-box">.*?</tr>`).MatchString(body) {
		t.Errorf("the edit row lost its plain id:\n%s", body)
	}
}

// The morph extension in app.js needs no eval, writes no style, and never
// throws into htmx. A focused field keeps its value through the value guard,
// not through ignoreActiveValue, which also freezes a focused button.
func TestMorphExtensionNeedsNoEval(t *testing.T) {
	js := staticText(t, "app.js")
	for _, want := range []string{`htmx.defineExtension("morph"`, `Idiomorph.morph(`, `style === "morph"`, "nodeType === 1", "catch", "beforeAttributeUpdated", `name === "value"`, "document.activeElement", `"pending-error"`, "form[data-editing]"} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
	// Each of the two callbacks checks for an element first.
	if n := strings.Count(js, "nodeType === 1"); n < 2 {
		t.Errorf("app.js checks the node type %d times, want one check in each callback", n)
	}
	for _, bad := range []string{"Function(", "eval(", ".style.", `setAttribute("style"`, "ignoreActiveValue", `classList.contains("refusal")`} {
		if strings.Contains(js, bad) {
			t.Errorf("app.js holds %s", bad)
		}
	}
}

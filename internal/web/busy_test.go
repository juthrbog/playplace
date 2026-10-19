package web

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// busyMarkup is how busyWord renders a word and its verb in progress.
func busyMarkup(word, busy string) string {
	return `<span class="word"><span>` + word + `</span><span class="busy">` + busy + `</span></span>`
}

// The buttons whose request takes a moment hold their verb in progress next
// to their word. +7d, Close, Edit and Cancel keep their word and only set
// aria-busy.
func TestBusyButtonsNameTheirVerb(t *testing.T) {
	ts, _ := newServer(t)
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true)
	_, home := do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	_, edit := do(t, ts, "GET", "/requests/bobs-box/edit", "ops@example.com", true, nil, true)
	_, account := do(t, ts, "GET", "/accounts/111111111111", "ops@example.com", true, nil, false)
	vh := `<span class="vh"> bobs-box</span>`
	for _, tc := range []struct {
		name, body, want string
	}{
		{"Approve", home, busyMarkup("Approve", "Approving") + vh},
		{"Deny", home, busyMarkup("Deny", "Denying") + vh},
		{"Withdraw", home, busyMarkup("Withdraw", "Withdrawing") + vh},
		{"Request", home, `<button type="submit" class="btn-primary">` + busyMarkup("Request", "Sending") + `</button>`},
		// The Sync word is also the word the phone hides.
		{"Sync", home, `<span class="btn-word word"><span>Sync with AWS</span><span class="busy">Syncing</span></span>`},
		{"edit Save", edit, `<button type="submit" class="btn-primary">` + busyMarkup("Save", "Saving") + `</button>`},
		{"Save budget", account, `<button type="submit" class="btn-primary">` + busyMarkup("Save budget", "Saving") + `</button>`},
	} {
		if !strings.Contains(tc.body, tc.want) {
			t.Errorf("%s lacks its busy word %s", tc.name, tc.want)
		}
	}
	quiet := regexp.MustCompile(`(?s)<form class="inline" hx-post="/accounts/[^"]+/(extend|close)".*?</form>`).FindAllString(home, -1)
	quiet = append(quiet, regexp.MustCompile(`<button [^>]*hx-get="/requests/[^"]+/(edit|row)"[^>]*>.*?</button>`).FindAllString(home+edit, -1)...)
	if len(quiet) < 6 {
		t.Fatalf("found %d +7d, Close, Edit and Cancel controls, want at least 6", len(quiet))
	}
	for _, c := range quiet {
		if strings.Contains(c, `class="busy"`) {
			t.Errorf("a control that keeps its word has a busy verb: %s", c)
		}
	}
}

// A busy button keeps its variant colors. The busy rules change only the
// pointer and which word shows, and only on the busy button itself. The
// word rules only lay the two words out in one cell.
func TestBusyKeepsTheVariantColors(t *testing.T) {
	const busy = ":is(button, .btn)[aria-busy=true]"
	layout := map[string]bool{"display": true, "grid-area": true, "visibility": true, "justify-content": true, "justify-self": true, "width": true, "white-space": true, "translate": true}
	var busyRules, wordRules int
	for _, r := range cssRuleSets(t) {
		for _, sel := range r.sels {
			if strings.Contains(sel, "aria-busy") {
				busyRules++
				if sel != busy && !strings.HasPrefix(sel, busy+" > ") {
					t.Errorf("%s reaches past the busy button with a descendant combinator", sel)
				}
				for prop := range r.decls {
					if prop != "cursor" && prop != "visibility" {
						t.Errorf("%s sets %s, want only cursor or visibility", sel, prop)
					}
				}
			}
			if strings.Contains(sel, ".word") || strings.Contains(sel, ".busy") {
				wordRules++
				for prop := range r.decls {
					if !layout[prop] {
						t.Errorf("%s sets %s, want only layout properties", sel, prop)
					}
				}
			}
		}
	}
	if busyRules < 3 || wordRules < 5 {
		t.Fatalf("found %d busy selectors and %d word selectors, want at least 3 and 5", busyRules, wordRules)
	}
}

// The busy verb takes no width in the word cell, so a button at rest is as
// wide as its word, and the pending table fits at 1440 as it did before the
// busy verbs. The verb is centered and spills into the side padding.
func TestBusyVerbTakesNoWidth(t *testing.T) {
	for _, r := range cssRuleSets(t) {
		if len(r.sels) == 1 && r.sels[0] == ".word > .busy" && r.decls["width"] != "" {
			for prop, want := range map[string]string{"width": "0", "justify-self": "center", "justify-content": "center", "white-space": "nowrap"} {
				if r.decls[prop] != want {
					t.Errorf(".word > .busy sets %s: %q, want %q", prop, r.decls[prop], want)
				}
			}
			return
		}
	}
	t.Fatal("app.css has no .word > .busy rule that sets the width of the busy verb")
}

// The disabled look sits in the last layer, so it beats every button
// variant. A busy button is never disabled, so it keeps its variant.
func TestDisabledLookIsInTheLastLayer(t *testing.T) {
	css := appCSS(t)
	order := regexp.MustCompile(`@layer\s+([\w\s,-]+);`).FindStringSubmatch(css)
	if order == nil || !strings.HasSuffix(strings.Join(strings.Fields(order[1]), " "), ", utilities") {
		t.Fatalf("utilities is not the last layer: %v", order)
	}
	start := regexp.MustCompile(`@layer\s+utilities\s*\{`).FindStringIndex(css)
	if start == nil {
		t.Fatal("app.css has no utilities layer block")
	}
	utilities, _ := block(t, css, start[1]-1)
	if !regexp.MustCompile(`(?:^|[\s}]):is\(button, \.btn\):disabled,`).MatchString(utilities) {
		t.Error("the disabled rule is not in the utilities layer")
	}
	if n := strings.Count(css, ":is(button, .btn):disabled"); n != 1 {
		t.Errorf("app.css has %d disabled button rules, want 1", n)
	}
}

// listeners returns the body of each document listener for event in app.js.
func listeners(js, event string) []string {
	var out []string
	for _, part := range strings.Split(js, `document.addEventListener("`+event+`", function (ev) {`)[1:] {
		body, _, _ := strings.Cut(part, "\n});")
		out = append(out, body)
	}
	return out
}

// app.js sets the busy state when a request starts and clears it when the
// request ends, also on an error or an abort. It never disables a button,
// because the disabled look would take the button's colors.
func TestBusyStateLivesInAppJS(t *testing.T) {
	js := staticText(t, "app.js")
	holds := func(event, want string) bool {
		for _, body := range listeners(js, event) {
			if strings.Contains(body, want) {
				return true
			}
		}
		return false
	}
	if !holds("htmx:beforeRequest", `setAttribute("aria-busy", "true")`) {
		t.Error("no htmx:beforeRequest listener sets aria-busy")
	}
	if !holds("htmx:afterRequest", `removeAttribute("aria-busy")`) {
		t.Error("no htmx:afterRequest listener clears aria-busy")
	}
	// The poll and Show closed are never busy, a poll has no submitter, and
	// only a button gets the state.
	for _, want := range []string{`cfg.verb === "get"`, "cfg.triggeringEvent && cfg.triggeringEvent.submitter", `elt.tagName === "FORM"`, `submitter.tagName === "BUTTON"`, `name === "aria-busy"`} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
	for _, bad := range []string{".disabled", `"disabled"`} {
		if strings.Contains(js, bad) {
			t.Errorf("app.js writes %s", bad)
		}
	}
}

// Every control whose answer takes the focused button away names its
// action: a decision takes its row out of the pending card, Edit, Save and
// Cancel swap the row type, and Close takes the buttons of the account row
// away. app.js then puts the focus on the next control in that row, or on
// the same button of the next row, and the heading takes the focus when
// the card is empty.
func TestFocusHasAPlaceAfterARowLeaves(t *testing.T) {
	ts, _ := newServer(t)
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true)
	_, home := do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	_, edit := do(t, ts, "GET", "/requests/bobs-box/edit", "ops@example.com", true, nil, true)
	for _, tc := range []struct {
		action, body, tag string
	}{
		{"approve", home, `<form class="inline" hx-post="/requests/bobs-box/approve"[^>]*>`},
		{"deny", home, `<form class="inline" hx-post="/requests/bobs-box/deny"[^>]*>`},
		{"withdraw", home, `<form class="inline" hx-post="/requests/bobs-box/withdraw"[^>]*>`},
		{"edit", home, `<button hx-get="/requests/bobs-box/edit"[^>]*>`},
		{"save", edit, `<form hx-post="/requests/bobs-box/edit"[^>]*>`},
		{"cancel", edit, `<button type="button" hx-get="/requests/bobs-box/row"[^>]*>`},
		{"close", home, `<form class="inline" hx-post="/accounts/111111111111/close"[^>]*>`},
	} {
		if tag := regexp.MustCompile(tc.tag).FindString(tc.body); !strings.HasSuffix(tag, ` data-action="`+tc.action+`">`) {
			t.Errorf("the %s control does not end with its data-action: %q", tc.action, tag)
		}
	}
	if !strings.Contains(home, `<h2 id="pending-title" tabindex="-1">`) {
		t.Error("the pending heading cannot take the focus")
	}
	js := staticText(t, "app.js")
	// Each action names the control that takes the focus after its answer.
	for _, want := range []string{
		`approve: 'form[data-action="approve"] button'`,
		`deny: 'form[data-action="deny"] button'`,
		`withdraw: 'form[data-action="withdraw"] button'`,
		`edit: "form[data-editing] input:not([type=hidden])"`,
		`save: 'button[data-action="edit"]'`,
		`cancel: 'button[data-action="edit"]'`,
		`close: "td a"`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks the focus target %s", want)
		}
	}
	settle := listeners(js, "htmx:afterSettle")
	if len(settle) != 1 {
		t.Fatalf("app.js has %d htmx:afterSettle listeners, want 1", len(settle))
	}
	for _, want := range []string{"ev.detail.xhr !== leaving.xhr", "document.activeElement !== document.body", "document.getElementById(was.row)", "row.querySelector(was.want)", `getElementById("pending-title")`, "rows.length - 1"} {
		if !strings.Contains(settle[0], want) {
			t.Errorf("the focus move lacks %s", want)
		}
	}
}

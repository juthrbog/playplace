package web

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var (
	// deskFormTag is the desk form in the bulk bar, with its own fields.
	deskFormTag = regexp.MustCompile(`(?s)<form id="desk-form" class="desk-form">(.*?)</form>`)
	// deskJoined is a field that joins the desk form from outside it.
	deskJoined = regexp.MustCompile(`<input [^>]*form="desk-form"[^>]*>`)
	// attrName and attrValue read one attribute of a start tag.
	attrName  = regexp.MustCompile(` name="([^"]*)"`)
	attrValue = regexp.MustCompile(` value="([^"]*)"`)
)

// deskFields returns what the desk form of page sends when the boxes of the
// requests checked are checked, as a browser builds it: the fields inside
// the form, then each field that joins it with form="desk-form". A box
// sends its value only when it is checked. It fails the test when page has
// no desk form.
func deskFields(t *testing.T, page string, checked ...string) url.Values {
	t.Helper()
	form := deskFormTag.FindStringSubmatch(page)
	if form == nil {
		t.Fatalf("the page has no desk form:\n%s", page)
	}
	out := url.Values{}
	for _, m := range hiddenInput.FindAllStringSubmatch(form[1], -1) {
		out.Add(m[1], html.UnescapeString(m[2]))
	}
	for _, tag := range deskJoined.FindAllString(page, -1) {
		n, v := attrName.FindStringSubmatch(tag), attrValue.FindStringSubmatch(tag)
		if n == nil || v == nil {
			t.Fatalf("a field of the desk form has no name or no value: %s", tag)
		}
		value := html.UnescapeString(v[1])
		if strings.Contains(tag, `type="checkbox"`) {
			name, _, _ := strings.Cut(value, " ")
			if !slices.Contains(checked, name) {
				continue
			}
		}
		out.Add(n[1], value)
	}
	return out
}

// pickIn returns the start tag of the checkbox in the slip of name, or ""
// when the slip has none.
func pickIn(body, name string) string {
	return regexp.MustCompile(`<input type="checkbox" name="req" [^>]*>`).FindString(slipIn(body, name))
}

// Each slip that waits and that the viewer can approve has a checkbox in its
// pick slot. The box joins the desk form, posts the ref of the request as the
// page shows it, and carries the facts of the slip for the summary. Any
// other slip keeps the empty slot. The server never renders a box checked,
// and no form nests in another. The bulk controls show only when a box
// does.
func TestDeskSlipsHaveACheckbox(t *testing.T) {
	d := newDesk(t, nil, nil, nil)
	d.ask(t, "bobs-box", "bob@example.com")
	d.ask(t, "leads-box", "lead@example.com")
	_, home := doAs(t, d.ts, "GET", "/", "lead@example.com", false, true, nil, false)

	ref := refIn(t, home, "bobs-box")
	want := `<input type="checkbox" name="req" value="` + refValue(ref) + `" form="desk-form" data-owner="bob@example.com" data-lifetime="14 days" data-budget="$50 a month" data-limits="At the per-owner limit of 1.">`
	if got := pickIn(home, "bobs-box"); got != want {
		t.Errorf("the box of bobs-box = %s, want %s", got, want)
	}
	if !strings.Contains(slipIn(home, "bobs-box"), `<span class="vh">Select bobs-box</span>`) {
		t.Errorf("the box of bobs-box has no name for screen readers:\n%s", slipIn(home, "bobs-box"))
	}
	// lead cannot approve the request of lead.
	if own := slipIn(home, "leads-box"); pickIn(home, "leads-box") != "" || !strings.Contains(own, `<span class="slip-pick"></span>`) {
		t.Errorf("the own slip of lead has a box:\n%s", own)
	}
	section := between(home, `<section id="pending"`, "</section>")
	if strings.Count(section, `<form id="desk-form"`) != 1 || strings.Count(section, `id="desk-all"`) != 1 {
		t.Errorf("the desk does not hold one desk form and one select-all box:\n%s", section)
	}
	if regexp.MustCompile(`<input [^>]*\bchecked\b[^>]*>`).MatchString(section) {
		t.Errorf("the server renders a checked box, which would enter the poll hash:\n%s", section)
	}
	if all := regexp.MustCompile(`<input type="checkbox" id="desk-all"[^>]*>`).FindString(section); all == "" || strings.Contains(all, "name=") {
		t.Errorf("the select-all box is missing or has a name, so the form would send it: %q", all)
	}
	assertNoNestedForms(t, home)

	// The decided slip loses its box.
	_, answer := d.decide(t, "lead@example.com", true, actionApprove, deskFields(t, home, "bobs-box"), true)
	if pickIn(answer, "bobs-box") != "" || !slices.Equal(decidedIn(answer), []string{"bobs-box"}) {
		t.Errorf("the decided slip keeps a box:\n%s", slipIn(answer, "bobs-box"))
	}
	// Now only the own request of lead waits, so the desk has no box and no
	// bulk controls.
	for _, part := range []string{`id="desk-form"`, `class="desk-controls"`, `name="req"`} {
		if strings.Contains(answer, part) {
			t.Errorf("a desk with no box holds %s:\n%s", part, answer)
		}
	}

	// Someone who is not an approver gets Your requests, with no box.
	_, bobs := do(t, d.ts, "GET", "/", "bob@example.com", false, nil, false)
	for _, part := range []string{`id="desk-form"`, `name="req"`, `form="desk-form"`} {
		if strings.Contains(bobs, part) {
			t.Errorf("Your requests holds %s:\n%s", part, bobs)
		}
	}
}

// assertNoNestedForms fails the test when a form of body starts inside
// another form. HTML drops a nested form, so its fields would join the
// outer one.
func assertNoNestedForms(t *testing.T, body string) {
	t.Helper()
	depth := 0
	for _, m := range regexp.MustCompile(`<form[ >]|</form>`).FindAllString(body, -1) {
		if m == "</form>" {
			depth--
			continue
		}
		if depth > 0 {
			t.Fatalf("a form starts inside another form:\n%s", body)
		}
		depth++
	}
}

// A bulk post built from the page's own desk form decides each checked
// request and reports a result for each row. The form sends the ref of
// each checked box and the shown values of every slip, so a stale request
// names what changed. A request that changed comes back with a new value
// in its box, so app.js clears that box. A slip that the post did not
// decide keeps its box for a retry.
func TestBulkPostFromTheDeskForm(t *testing.T) {
	d := newDesk(t, nil, nil, nil)
	d.ask(t, "gone-box", "gone@example.com")
	d.ask(t, "stale-box", "stale@example.com")
	d.ask(t, "ok-box", "ok@example.com")
	d.ask(t, "later-box", "later@example.com")
	_, home := doAs(t, d.ts, "GET", "/", "lead@example.com", false, true, nil, false)
	refs := d.refs(t, "gone-box", "stale-box")

	if res, body := do(t, d.ts, "POST", "/requests/gone-box/withdraw", "gone@example.com", false, refForm(refs[0]), true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("withdraw = %s", body)
	}
	_, edit := do(t, d.ts, "GET", "/requests/stale-box/edit", "ops@example.com", true, nil, true)
	saved := editFormValues(t, edit, "stale-box")
	saved.Set("budget", "61")
	if res, body := do(t, d.ts, "POST", "/requests/stale-box/edit", "ops@example.com", true, saved, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("edit = %s", body)
	}

	form := deskFields(t, home, "ok-box", "stale-box", "gone-box")
	if got := form["req"]; len(got) != 3 || form.Get("reason") != "" || !form.Has("reason") {
		t.Fatalf("the desk form sends req %v and reason %v, want 3 refs and an empty reason", got, form["reason"])
	}
	if form.Get("shown-budget-stale-box") != "50" || form.Get("shown-owner-later-box") != "later@example.com" {
		t.Errorf("the desk form does not send the shown values of every slip: %v", form)
	}
	res, body := d.decide(t, "lead@example.com", true, actionApprove, form, true)
	if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("decide = %d %v\n%s", res.StatusCode, res.Header, body)
	}
	if got, _ := regionIn(body); got != checkIcon+"<span>Approved 1 of 3 requests. AWS is creating 1 account. The queue shows why 2 requests were not approved.</span>" {
		t.Errorf("region = %q", got)
	}
	if got := decidedIn(body); !slices.Equal(got, []string{"ok-box"}) {
		t.Errorf("decided %v, want [ok-box]:\n%s", got, body)
	}
	if got := goneIn(body); !slices.Equal(got, []string{"gone-box"}) {
		t.Errorf("gone slips %v, want [gone-box]:\n%s", got, body)
	}
	for name, want := range map[string]string{
		"stale-box": "Not approved. stale-box changed after you loaded the page: budget $50 to $61. Read the new values, then approve again.",
		"gone-box":  goneText("gone-box"),
		"ok-box":    "",
		"later-box": "",
	} {
		if got := resultIn(body, name); got != want {
			t.Errorf("the result for %s = %q, want %q", name, got, want)
		}
	}
	// The changed request has a new ref, so its box no longer matches the
	// box that the approver checked. The request that the post left alone
	// keeps the same box.
	oldStale := attrValue.FindStringSubmatch(pickIn(home, "stale-box"))
	newStale := attrValue.FindStringSubmatch(pickIn(body, "stale-box"))
	if oldStale == nil || newStale == nil || oldStale[1] == newStale[1] {
		t.Errorf("the box of the changed request keeps its value: %v then %v", oldStale, newStale)
	}
	if pickIn(home, "later-box") != pickIn(body, "later-box") || pickIn(body, "later-box") == "" {
		t.Errorf("the box of an untouched request changed:\n%s\n%s", pickIn(home, "later-box"), pickIn(body, "later-box"))
	}
	if !d.pending(t, "stale-box") || !d.pending(t, "later-box") || d.pending(t, "ok-box") {
		t.Error("only ok-box may leave the queue")
	}

	// Deny selected sends the same form with the reason.
	form = deskFields(t, body, "stale-box", "later-box")
	form.Set("reason", "not this quarter")
	res, body = d.decide(t, "lead@example.com", true, actionDeny, form, true)
	if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("deny = %d %v\n%s", res.StatusCode, res.Header, body)
	}
	if got := decidedIn(body); !slices.Equal(got, []string{"stale-box", "later-box"}) {
		t.Errorf("denied %v, want [stale-box later-box]:\n%s", got, body)
	}
}

// Each bulk button carries the cap of one post and the refusal of the
// server above it, word for word, so app.js refuses a larger selection with
// the same sentence before it posts.
func TestBulkButtonsCarryTheServerCaps(t *testing.T) {
	d := newDesk(t, nil, nil, nil)
	d.ask(t, "bobs-box", "bob@example.com")
	_, home := doAs(t, d.ts, "GET", "/", "lead@example.com", false, true, nil, false)
	for _, tc := range []struct {
		action string
		cap    int
	}{{actionApprove, maxApprovals}, {actionDeny, maxDenials}} {
		button := regexp.MustCompile(`<button [^>]*data-bulk="` + tc.action + `"[^>]*>`).FindString(home)
		capAttr := regexp.MustCompile(` data-cap="(\d+)"`).FindStringSubmatch(button)
		note := regexp.MustCompile(` data-cap-note="([^"]*)"`).FindStringSubmatch(button)
		if capAttr == nil || note == nil {
			t.Fatalf("the %s button carries no cap: %s", tc.action, button)
		}
		form := decideForm("not now", madeRefs(tc.cap+1)...)
		res, body := d.decide(t, "lead@example.com", true, tc.action, form, true)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s over the cap = %d", tc.action, res.StatusCode)
		}
		if capAttr[1] != strconv.Itoa(tc.cap) || !strings.Contains(body, "<span>"+html.UnescapeString(note[1])+"</span>") {
			t.Errorf("the %s button carries %s and %q, but the server refuses with:\n%s", tc.action, capAttr[1], note[1], body)
		}
		for _, want := range []string{`form="desk-form"`, `hx-post="/requests/decide?action=` + tc.action + `"`, `type="button"`, `hx-target="#pending"`, `hx-include="#show-closed"`} {
			if !strings.Contains(button, want) {
				t.Errorf("the %s button lacks %s: %s", tc.action, want, button)
			}
		}
	}
}

// app.js keeps the selection through a morph while the ref of a box stays
// the same, keeps the count and the select-all box in step after every
// swap and change, and refuses a selection over the cap without a post.
func TestSelectionLivesInAppJS(t *testing.T) {
	js := staticText(t, "app.js")
	holdsEach(t, "keepPick", funcBody(t, js, "function keepPick(node, next) {"),
		`node.name !== "req" || !node.checked`,
		`next.getAttribute("value") === node.getAttribute("value")`,
		`next.checked = true;`)
	holdsEach(t, "the morph", js, "beforeNodeMorphed: function (node, next) {", "keepPick(node, next);")
	holdsEach(t, "syncPicks", funcBody(t, js, "function syncPicks() {"),
		`count.textContent = n + " selected"`,
		`all.checked = boxes.length > 0 && n === boxes.length`,
		`all.indeterminate = n > 0 && n < boxes.length`)
	settle := listeners(js, "htmx:afterSettle")
	if len(settle) != 1 || !strings.Contains(settle[0], "syncPicks();") {
		t.Errorf("the one afterSettle listener does not call syncPicks: %d listeners", len(settle))
	}
	change := listeners(js, "change")
	if len(change) != 1 || !strings.Contains(change[0], `box.id === "desk-all"`) || !strings.Contains(change[0], "syncPicks();") {
		t.Errorf("app.js has no change listener that keeps the selection in step")
	}
	confirm := listeners(js, "htmx:confirm")
	if len(confirm) != 1 || !strings.Contains(confirm[0], `hasAttribute("data-bulk") && overCap(elt)`) || !strings.Contains(confirm[0], "ev.preventDefault();") {
		t.Errorf("app.js has no htmx:confirm listener that stops a bulk post over the cap")
	}
	holdsEach(t, "overCap", funcBody(t, js, "function overCap(button) {"),
		`parseInt(button.getAttribute("data-cap"), 10)`,
		`pickedCount() <= cap`,
		`refuse(button.getAttribute("data-cap-note"))`)
	refuse := funcBody(t, js, "function refuse(text) {")
	holdsEach(t, "refuse", refuse,
		`getElementById("pending-error")`,
		`shown.textContent = text`,
		`slot.replaceChildren(alertIcon(), shown)`,
		`copy.className = "vh"`,
		`region.replaceChildren(copy)`)
	for _, bad := range []string{"innerHTML", "insertAdjacentHTML"} {
		if strings.Contains(refuse, bad) {
			t.Errorf("refuse writes markup with %s:\n%s", bad, refuse)
		}
	}
}

// The checkbox is native, 16 px and violet. The bar is 48 px with the bulk
// controls and selected card while any slip is selected. The box of a slip
// sits in the left gutter and is a 44 px target on a phone.
func TestDeskSelectionFollowsTheDesign(t *testing.T) {
	base, _ := layerRules(t, "base")
	wantDecls(t, "base", base, "input[type=checkbox]", map[string]string{"width": "16px", "height": "16px"})
	wantDecls(t, "base", base, ":root", map[string]string{"accent-color": "var(--violet)"})
	top, media := layerRules(t, "components")
	wantDecls(t, "components", top, ".desk .desk-bar:has(> .desk-controls)", map[string]string{"min-height": "48px", "padding-block": "6px"})
	wantDecls(t, "components", top, ".desk:has(.slip-pick input:checked) .desk-bar", map[string]string{"background": "var(--card-sel)"})
	wantDecls(t, "components", top, ".desk-form", map[string]string{"display": "none"})
	wantDecls(t, "components", top, "label.slip-pick", map[string]string{"position": "absolute", "left": "var(--space-lg)"})
	phone := media["(max-width: 640px)"]
	wantDecls(t, "components (max-width: 640px)", phone, "label.slip-pick", map[string]string{"width": "var(--touch)", "height": "var(--touch)"})
	wantDecls(t, "components (max-width: 640px)", phone, ".desk:has(.slip-pick > input) .slip", map[string]string{"padding-left": "48px"})
}

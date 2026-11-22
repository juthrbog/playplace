package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// keysListener returns the one keydown listener of app.js that holds the
// keys. The other keydown listener closes the phone menu on Escape.
func keysListener(t *testing.T, js string) string {
	t.Helper()
	var keys []string
	for _, body := range listeners(js, "keydown") {
		if strings.Contains(body, "moveRow(") {
			keys = append(keys, body)
		}
	}
	if len(keys) != 1 {
		t.Fatalf("app.js has %d keydown listeners for the keys, want 1", len(keys))
	}
	return keys[0]
}

// inOrder fails the test unless each string of want shows in part, each
// after the one before it.
func inOrder(t *testing.T, what, part string, want ...string) {
	t.Helper()
	at := 0
	for _, w := range want {
		i := strings.Index(part[at:], w)
		if i < 0 {
			t.Errorf("%s lacks %s after the parts before it", what, w)
			return
		}
		at += i + len(w)
	}
}

// A key never acts while a modifier is held, while the browser composes
// text, in a field that takes text, or while a dialog is open, because
// askFirst lets a request go when a dialog is open already. The switch
// turns off every key, ? too, so the keys meet WCAG 2.1.4. A held key
// repeats only j, k and /, so a held x never flips the box back and forth
// and a held a never approves again.
func TestKeysHaveTheirGuards(t *testing.T) {
	js := staticText(t, "app.js")
	keys := keysListener(t, js)
	inOrder(t, "the keys listener", keys,
		"ev.defaultPrevented || ev.isComposing || ev.ctrlKey || ev.metaKey || ev.altKey",
		"typing(ev.target) || dialogOpen()",
		"if (!keysOn) {\n    return;\n  }",
		`key === "?"`, "!ev.repeat && openKeys()",
		`key === "j" || key === "k"`, `moveRow(key === "j" ? 1 : -1)`,
		`key === "/"`, `getElementById("fleet-q")`, "box.focus();", "box.select();",
		"} else if (ev.repeat) {\n    return;\n  }",
		`key === "x"`, "pickKey()",
		`key === "a"`, `decideKey("approve")`,
		`key === "d"`, `decideKey("deny")`,
		"ev.preventDefault()")
	if strings.Contains(keys, "shiftKey") {
		t.Error("the keys listener rejects Shift, so ? cannot work")
	}
	holdsEach(t, "typing", funcBody(t, js, "function typing(el) {"),
		"el.isContentEditable", `el.tagName === "TEXTAREA"`, `el.tagName === "SELECT"`,
		`el.tagName === "INPUT" && el.type !== "checkbox" && el.type !== "radio"`)
	if n := strings.Count(js, `document.addEventListener("keydown", function (ev) {`); n != 2 {
		t.Errorf("app.js has %d keydown listeners, want the Escape one and the keys", n)
	}
	holdsEach(t, "app.js", js, "A held key repeats only j, k and /.")
}

// Escape on an open dialog closes only the dialog. The Escape listener of
// the phone menu then leaves the menu open, so the dialog gives the focus
// back to the menu link that opened it.
func TestMenuEscapeLeavesAnOpenDialog(t *testing.T) {
	js := staticText(t, "app.js")
	for _, body := range listeners(js, "keydown") {
		if strings.Contains(body, "menu-open") {
			holdsEach(t, "the menu listener", body, `if (ev.key !== "Escape" || dialogOpen()) {`)
			return
		}
	}
	t.Fatal("app.js has no keydown listener for the menu")
}

// j and k move the real focus, so app.js keeps no cursor, and the focus
// rule after a morph keeps the place. They move in the list that holds the
// focus and stop at each end. With the focus in no list, they go to the
// main list: the queue of an approver, else the accounts. A slip that no
// longer waits has no control, so they skip it. The row scrolls as little
// as it must.
func TestJAndKMoveTheFocus(t *testing.T) {
	js := staticText(t, "app.js")
	holdsEach(t, "keyLists", js,
		`{ rows: "#pending li.slip", want: "button.slip-name, .slip-title[tabindex]" }`,
		`{ rows: "#accounts tbody > tr, #accounts li.loan", want: "td a, .loan-name a" }`,
		`{ rows: "#attention li", want: "a.ledger-row" }`)
	move := funcBody(t, js, "function moveRow(step) {")
	holdsEach(t, "moveRow", move,
		"active.closest(keyLists[l].rows)",
		"i >= 0 && i < rows.length; i += step",
		`document.querySelector("#pending.desk") ? [0, 1, 2] : [1, 0, 2]`)
	if strings.Contains(move, "% rows.length") || strings.Contains(move, "rows.length - 1") {
		t.Error("moveRow wraps at the ends")
	}
	holdsEach(t, "focusRow", funcBody(t, js, "function focusRow(row, want) {"),
		"controlIn(row, want)", "control.focus({ preventScroll: true })", `row.scrollIntoView({ block: "nearest" })`)
}

// x clicks the box of the focused slip, so the change listener updates the
// count and the bulk controls. a and d act on the checked boxes through
// the bulk buttons, which keep the cap and the dialogs, or else on the
// focused slip. Before Approve or Deny the focus goes to the name of the
// slip, and the focus rule then moves on to the next name, never to the
// next Approve, so Enter after a never approves a second request. A slip that cannot be
// decided says why in the live region. The bulk act repeats the message of
// the region after the focus moved.
func TestDeskKeysClickTheControls(t *testing.T) {
	js := staticText(t, "app.js")
	holdsEach(t, "keySlip", funcBody(t, js, "function keySlip() {"), `document.querySelector("#pending.desk")`, `active.closest("#pending li.slip")`)
	holdsEach(t, "pickKey", funcBody(t, js, "function pickKey() {"),
		`row.querySelector('.slip-pick input[name="req"]')`, "box.click()", "tell(cannotPick(row))")
	holdsEach(t, "cannotPick", funcBody(t, js, "function cannotPick(row) {"),
		`row.getAttribute("data-state") !== "pending"`, `row.querySelector("form[data-editing]")`, `row.querySelector(".chip-mine")`)
	decide := funcBody(t, js, "function decideKey(kind) {")
	inOrder(t, "decideKey", decide,
		`kind === "approve" ? "desk-approve" : "desk-deny"`, "bulk && pickedCount() > 0", "bulk.click()",
		"var row = keySlip();",
		`row.querySelector('form[data-action="' + kind + '"] button')`, "tell(cannotPick(row))",
		`row.querySelector("button.slip-name")`, "name.focus({ preventScroll: true })",
		"button.click()")
	if strings.Contains(decide, `if (kind === "deny")`) {
		t.Error("decideKey moves the focus to the name only before Deny")
	}
	for _, bad := range []string{".checked =", "requestSubmit", "htmx.trigger"} {
		if strings.Contains(decide+funcBody(t, js, "function pickKey() {"), bad) {
			t.Errorf("a key uses %s, but it must click the control as a viewer does", bad)
		}
	}
	holdsEach(t, "notePlace", funcBody(t, js, "function notePlace(xhr, source) {"),
		`if (acted && (action === "approve" || action === "deny") && active.matches("button.slip-name")) {`+"\n"+`    focusPlace.want = "button.slip-name, .slip-title[tabindex]";`,
		`if (!acted && source && source.hasAttribute && source.hasAttribute("data-bulk")) {`+"\n    focusPlace.acted = true;")
}

// The switch lasts for the browser: app.js keeps it in localStorage, with
// every read and write in try/catch, and the state itself in a variable.
// The keys-off class is on <html> before the first paint, and the
// stylesheet shows the word of the switch from it, so app.js never writes
// the switch. A press writes one short message into the live region. Another tab can change it, and
// a page from the back/forward cache reads it again.
func TestKeysSwitchLastsForTheBrowser(t *testing.T) {
	js := staticText(t, "app.js")
	holdsEach(t, "app.js", js, `var keysKey = "playplace.keys";`, "var keysOn = true;")
	read := funcBody(t, js, "function readKeys() {")
	holdsEach(t, "readKeys", read, "try {", `keysOn = window.localStorage.getItem(keysKey) !== "off";`, "} catch (err) {", "showKeys();")
	show := funcBody(t, js, "function showKeys() {")
	holdsEach(t, "showKeys", show,
		`document.documentElement.classList.toggle("keys-off", !keysOn)`, "syncShortcuts();")
	for _, bad := range []string{"aria-pressed", "textContent", "keys-toggle"} {
		if strings.Contains(show, bad) {
			t.Errorf("showKeys writes %s into the switch", bad)
		}
	}
	if strings.Contains(js, "aria-pressed") {
		t.Error("app.js sets aria-pressed, but the switch changes its word")
	}
	holdsEach(t, "setKeys", funcBody(t, js, "function setKeys(on) {"),
		"keysOn = on;", "try {", "window.localStorage.removeItem(keysKey)", `window.localStorage.setItem(keysKey, "off")`, "} catch (err) {", "showKeys();")
	if n := strings.Count(js, "localStorage."); n != 3 {
		t.Errorf("app.js uses localStorage %d times, want the 3 guarded uses", n)
	}
	// The call at load runs before the first listener that reads the page.
	call := strings.Index(js, "\nreadKeys();\n")
	if call < 0 || call > strings.Index(js, `document.addEventListener("DOMContentLoaded"`) {
		t.Error("app.js does not read the switch at load, before the first paint")
	}
	var press string
	for _, body := range listeners(js, "click") {
		if strings.Contains(body, `"#keys-toggle"`) {
			press = body
		}
	}
	holdsEach(t, "the switch listener", press, "setKeys(!keysOn)", `tell(keysOn ? "Keys on." : "Keys off. The Key list button still lists them.")`, `"#keys-show"`, "openKeys()")
	holdsEach(t, "app.js", js, `window.addEventListener("storage", function (ev) {`, "ev.key === keysKey || ev.key === null")
	_, rest, _ := strings.Cut(js, `window.addEventListener("pageshow", function (ev) {`)
	holdsEach(t, "the pageshow listener", rest, "readKeys();\n  readPaused();")
	_, rest, _ = strings.Cut(js, `document.addEventListener("DOMContentLoaded", function () {`)
	holdsEach(t, "the DOMContentLoaded listener", rest, "readKeys();\n  watchBar();")
	holdsEach(t, "openKeys", funcBody(t, js, "function openKeys() {"),
		`getElementById("keys-dialog")`, "dialogOpen()", "asking = null;",
		`active.closest(slipRows + ", " + fleetRows + ", " + ledgerRows)`,
		`dialog.addEventListener("close", keysClosed, { once: true })`,
		"dialog.showModal()", `querySelector('button[value="cancel"]')`)
}

// When a poll takes the focused control away while the key list is open,
// the close of the list sends the focus to the same control in its own
// slip or row, or else to the first slip that waits or to the heading, as
// after the other dialogs. A slip that left says so in the region.
func TestKeyListGivesTheFocusBack(t *testing.T) {
	js := staticText(t, "app.js")
	inOrder(t, "keysClosed", funcBody(t, js, "function keysClosed() {"),
		"var from = keysFrom;", "keysFrom = null;",
		"document.body.contains(from.el)", "return;",
		"document.getElementById(from.row)",
		`"button.slip-name, .slip-title[tabindex]"`, `"a.ledger-row"`, `"td a, .loan-name a"`,
		"controlIn(row, want)", "firstWaiting()", `"attention-title"`, `"accounts-title"`,
		"target.focus();",
		"say(goneText(from.row.slice(4)))")
}

// Approve and Deny name their keys in aria-keyshortcuts only while the
// keys are on. While a box is checked, a and d press the bulk buttons, so
// only those name them. syncPicks runs after each morph and each change
// of a box, so it calls syncShortcuts, and so does showKeys.
func TestShortcutsFollowTheSwitch(t *testing.T) {
	js := staticText(t, "app.js")
	holdsEach(t, "syncShortcuts", funcBody(t, js, "function syncShortcuts() {"),
		`document.querySelectorAll("[data-key]")`, "pickedCount() > 0",
		`keysOn && (!picked || b.hasAttribute("data-bulk"))`,
		`b.setAttribute("aria-keyshortcuts", b.getAttribute("data-key"))`,
		`b.removeAttribute("aria-keyshortcuts")`)
	holdsEach(t, "syncPicks", funcBody(t, js, "function syncPicks() {"), "syncShortcuts();")
}

// Every page with a viewer has the switch and the Key list button in the
// footer, and the key list once, after the footer and outside the morph
// targets. The list holds only the keys that act on the page, and no live
// region. Approve and Deny carry their keys in data-key, and app.js names
// them in aria-keyshortcuts only while the keys are on.
func TestKeysOnEveryPageWithAViewer(t *testing.T) {
	ts, _ := newServer(t)
	const keysLine = `<div class="keys"><button type="button" id="keys-toggle" class="keys-toggle">` +
		`<span class="keys-on-word">Keys on</span><span class="keys-off-word">Keys off</span></button> ` +
		`<button type="button" id="keys-show" class="keys-show">Key list<kbd aria-hidden="true">?</kbd></button></div>`
	for _, c := range []struct {
		path     string
		admin    bool
		approver bool
		want     int
		keys     []string
		not      []string
	}{
		{"/", true, true, http.StatusOK, []string{"j", "k", "Enter", "x", "a", "d", "?"}, []string{"/"}},
		{"/", false, true, http.StatusOK, []string{"j", "k", "Enter", "x", "a", "d", "?"}, []string{"/"}},
		{"/", false, false, http.StatusOK, []string{"j", "k", "Enter", "?"}, []string{"/", "x", "a", "d"}},
		{"/accounts", true, true, http.StatusOK, []string{"/", "j", "k", "Enter", "?"}, []string{"x", "a", "d"}},
		{"/accounts/111111111111", true, true, http.StatusOK, []string{"?"}, []string{"/", "j", "k", "Enter", "x", "a", "d"}},
		{"/nope", false, false, http.StatusNotFound, []string{"?"}, []string{"/", "j", "x"}},
	} {
		res, body := doAs(t, ts, "GET", c.path, "ops@example.com", c.admin, c.approver, nil, false)
		if res.StatusCode != c.want {
			t.Fatalf("GET %s = %d, want %d", c.path, res.StatusCode, c.want)
		}
		foot := between(body, `<footer class="foot">`, "</footer>")
		if !strings.Contains(foot, keysLine) {
			t.Errorf("%s: the footer lacks the switch and the Key list button:\n%s", c.path, foot)
		}
		if n := strings.Count(body, `<dialog id="keys-dialog"`); n != 1 {
			t.Errorf("%s: %d key lists, want 1", c.path, n)
			continue
		}
		_, after, _ := strings.Cut(body, "</footer>")
		if !strings.HasPrefix(strings.TrimLeft(after, " "), `<dialog id="keys-dialog" class="dialog" aria-labelledby="keys-title">`) {
			t.Errorf("%s: the key list does not follow the footer:\n%s", c.path, after)
		}
		list := dialogIn(body, "keys-dialog")
		for _, k := range c.keys {
			if !strings.Contains(list, "<kbd>"+k+"</kbd>") {
				t.Errorf("%s: the key list lacks %s:\n%s", c.path, k, list)
			}
		}
		for _, k := range c.not {
			if strings.Contains(list, "<kbd>"+k+"</kbd>") {
				t.Errorf("%s: the key list names %s, which does nothing there", c.path, k)
			}
		}
		for _, bad := range []string{"aria-live", "role=", "<output"} {
			if strings.Contains(list, bad) {
				t.Errorf("%s: the key list holds %s, but the page has only one region", c.path, bad)
			}
		}
		holdsEach(t, c.path+" key list", list,
			`<h2 id="keys-title">Keys</h2>`,
			`<p class="help keys-off-note">The keys are off. Press Keys off in the footer to turn them on.</p>`,
			`<button type="submit" value="cancel" autofocus>Close</button>`)
	}

	d := newDesk(t, nil, nil, nil)
	d.ask(t, "bobs-box", "bob@example.com")
	_, home := doAs(t, d.ts, "GET", "/", "lead@example.com", false, true, nil, false)
	slip := slipIn(home, "bobs-box")
	holdsEach(t, "the slip", slip, `<button type="submit" class="btn-approve" data-key="a">`, `<button type="submit" data-key="d">`)
	holdsEach(t, "the bulk bar", home, `id="desk-approve" class="btn-approve" data-key="a"`, `id="desk-deny" data-key="d"`)
	if strings.Contains(home, "aria-keyshortcuts") {
		t.Error("the server names a key in aria-keyshortcuts, but it cannot know the switch")
	}
	_, answer := d.decide(t, "lead@example.com", true, actionApprove, decideForm("", refIn(t, home, "bobs-box")), true)
	if strings.Contains(answer, "keys-dialog") {
		t.Error("an htmx answer carries the key list")
	}
}

// keyList names only the keys that act on the page of pg.
func TestKeyListFitsThePage(t *testing.T) {
	keysOf := func(pg page) string {
		var out []string
		for _, l := range keyList(pg) {
			out = append(out, strings.Join(l.Keys, "+"))
		}
		return strings.Join(out, " ")
	}
	for _, c := range []struct {
		pg   page
		want string
	}{
		{page{Polls: true, Path: "/", Viewer: Viewer{Approver: true}}, "j+k Enter x a d ?"},
		{page{Polls: true, Path: "/"}, "j+k Enter ?"},
		{page{Polls: true, Path: pathAccounts, Viewer: Viewer{Approver: true}}, "/ j+k Enter ?"},
		{page{Path: "/accounts/1"}, "?"},
		{page{Path: "/"}, "?"},
	} {
		if got := keysOf(c.pg); got != c.want {
			t.Errorf("keyList(%+v) = %q, want %q", c.pg.Path, got, c.want)
		}
	}
	// Only an approver has slips that Enter opens. A row of Your requests
	// has no action for Enter.
	enter := func(pg page) string {
		for _, l := range keyList(pg) {
			if l.Keys[0] == "Enter" {
				return l.Does
			}
		}
		return ""
	}
	if got := enter(page{Polls: true, Path: "/", Viewer: Viewer{Approver: true}}); got != "Open the request, or follow the link of the row." {
		t.Errorf("Enter for an approver = %q", got)
	}
	if got := enter(page{Polls: true, Path: "/"}); got != "Follow the link of the row." {
		t.Errorf("Enter for an engineer = %q", got)
	}
}

// A keycap is 12 px semibold pencil grey on paper, with 4 px corners and a
// panel-rule border that is 2 px at the bottom, and never a shadow. The
// switch and the button need script, and the switch shows the word that
// fits the keys-off class. A row of the fleet, a loan card and a
// row of the ledger get the violet frame inside the row in utilities, as
// the slip does. The note of the key list shows only while the keys are
// off. A footer that holds only script parts is empty without script.
func TestKeysFollowTheDesign(t *testing.T) {
	top, _ := layerRules(t, "components")
	wantDecls(t, "components", top, "kbd", map[string]string{
		"padding": "2px 5px", "border": "1px solid var(--rule)", "border-bottom-width": "2px",
		"border-radius": "var(--r-control)", "background": "var(--paper)", "color": "var(--ink-2)",
		"font-size": "12px", "font-weight": "600", "line-height": "1",
	})
	wantDecls(t, "components", top, ".keys", map[string]string{"display": "none"})
	wantDecls(t, "components", top, "html.js .keys", map[string]string{"display": "flex"})
	wantDecls(t, "components", top, ".keys-toggle, .keys-show", map[string]string{"height": "var(--control-h-sm)", "padding": "0 11px"})
	wantDecls(t, "components", top, ".keys-off-word", map[string]string{"display": "none"})
	wantDecls(t, "components", top, "html.keys-off .keys-on-word", map[string]string{"display": "none"})
	wantDecls(t, "components", top, "html.keys-off .keys-off-word", map[string]string{"display": "inline"})
	wantDecls(t, "components", top, ".keys-off-note", map[string]string{"display": "none"})
	wantDecls(t, "components", top, "html.keys-off .keys-off-note", map[string]string{"display": "block"})
	utilities, _ := layerRules(t, "utilities")
	wantDecls(t, "utilities", utilities, ".fleet tbody tr:has(:focus-visible), li.loan:has(:focus-visible), .ledger-row:focus-visible",
		map[string]string{"outline": "2px solid var(--violet)", "outline-offset": "-2px"})
	layout, _ := layerRules(t, "layout")
	wantDecls(t, "layout", layout, "html:not(.js) .foot:not(:has(> :not(.live, .keys)))", map[string]string{"display": "none"})
}

// An open edit slip waits, so j and k stop on it. Its heading takes the
// focus, as the heading of a row of Your requests does, so the focus never
// lands on Save, and Enter never posts a half-finished edit.
func TestJAndKStopOnTheHeadingOfAnEditSlip(t *testing.T) {
	ts, _ := newServer(t)
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true)
	_, edit := do(t, ts, "GET", "/requests/bobs-box/edit", "ops@example.com", true, nil, true)
	holdsEach(t, "the edit slip", edit,
		`<li id="req-bobs-box" class="slip slip-edit" data-state="pending"><h3 class="slip-title" tabindex="-1">`)
}

package web

import (
	"strings"
	"testing"
)

// funcBody returns the body of the top-level function of js whose
// first line is head, up to its closing brace at column 0. It fails
// the test when js has no such function.
func funcBody(t *testing.T, js, head string) string {
	t.Helper()
	_, rest, ok := strings.Cut(js, "\n"+head)
	if !ok {
		t.Fatalf("app.js has no top-level %s", head)
	}
	body, _, ok := strings.Cut(rest, "\n}")
	if !ok {
		t.Fatalf("app.js does not close %s at column 0", head)
	}
	return body
}

// holdsEach fails the test for each string of want that part lacks. what
// names the part in the message.
func holdsEach(t *testing.T, what, part string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(part, w) {
			t.Errorf("%s lacks %s", what, w)
		}
	}
}

// A poll, or any other swap that the viewer did not start from the slip,
// moves the focus to the name of a slip. It never moves it to Approve or
// Deny. Only a control of focusNext in the slip counts as the act of the
// viewer. A decided or gone slip has no control. A slip that left while the
// viewer did nothing gets a message, and after an act the region says its
// message again.
func TestFocusNeverLandsOnADecisionAfterALiveUpdate(t *testing.T) {
	js := staticText(t, "app.js")
	holdsEach(t, "notePlace", funcBody(t, js, "function notePlace(xhr, source) {"),
		`var acted = !!action && !!focusNext[action] && row.contains(source)`,
		`acted ? focusNext[action] : (slip ? "button.slip-name, .slip-title[tabindex]" : "td a")`,
		`said: region ? region.lastElementChild : null`)
	holdsEach(t, "controlIn", funcBody(t, js, "function controlIn(row, want) {"),
		`row.hasAttribute("data-state") && row.getAttribute("data-state") !== "pending"`)
	settle := listeners(js, "htmx:afterSettle")
	if len(settle) != 1 {
		t.Fatalf("app.js has %d htmx:afterSettle listeners, want 1", len(settle))
	}
	holdsEach(t, "the focus rule", settle[0],
		`!was.acted && was.rows === slipRows && !document.getElementById(was.row)`,
		`say(goneText(was.row.slice(4)))`,
		`repeatRegion(was.said)`)
}

// A screen reader stops reading the region when the focus moves. So after
// an action that moved the focus, app.js writes the message of the region
// again, once, 300 ms later. It never repeats a message that the answer did
// not write.
func TestRegionRepeatsAfterAFocusMove(t *testing.T) {
	js := staticText(t, "app.js")
	holdsEach(t, "repeatRegion", funcBody(t, js, "function repeatRegion(before) {"),
		"last === before", "setTimeout(", "}, 300)", "region.lastElementChild === last", "last.textContent = text")
}

// Above 640 px the bulk bar sticks to the top of the window, and the page
// keeps its height clear above a focused control. app.js measures the bar
// and writes the height into a rule of its own, because the CSP allows no
// inline style. When a message makes the bar taller, app.js scrolls a
// covered control clear of it.
func TestBarHeightLivesInARule(t *testing.T) {
	js := staticText(t, "app.js")
	holdsEach(t, "app.js", js,
		`new ResizeObserver(`,
		`insertRule(":root {}"`,
		`getElementById("desk-bar")`,
		`typeof ResizeObserver === "function"`,
		`!bar.contains(active)`,
		`a.top < b.bottom && a.bottom > b.top`,
		`scrollIntoView({ block: "nearest" })`)
}

// A link names a request in its query and its hash. app.js opens and
// focuses the slip of that name, on load and on a hash change. A name with
// no slip that waits moves the focus to the heading. A message follows only
// when the page does not say why already. The message goes in as text, and
// it is the same sentence that the server writes. Then app.js drops the name
// from the address. A reload after a decision shows no stale line, and the
// browser finds no hash that takes the focus away from the name.
func TestAddressOpensItsSlip(t *testing.T) {
	js := staticText(t, "app.js")
	holdsEach(t, "app.js", js,
		`window.addEventListener("hashchange"`,
		`document.addEventListener("DOMContentLoaded"`,
		`new URLSearchParams(location.search).get("req")`,
		`location.hash.indexOf("#req-") === 0`,
		`/^[a-z0-9][a-z0-9-]{1,48}[a-z0-9]$/`,
		`setOpen(button, true)`,
		`getElementById("pending-title")`)
	holdsEach(t, "openFromAddress", funcBody(t, js, "function openFromAddress(useQuery) {"),
		`slip.getAttribute("data-state") === "pending"`,
		`getElementById("pending-failed")`,
		`'#pending-error [data-name="' + name + '"]'`)
	say := funcBody(t, js, "function say(text) {")
	holdsEach(t, "say", say, "span.textContent = text", "region.replaceChildren(svg, span)")
	for _, bad := range []string{"innerHTML", "insertAdjacentHTML"} {
		if strings.Contains(say, bad) {
			t.Errorf("say writes markup with %s:\n%s", bad, say)
		}
	}
	holdsEach(t, "forgetAddress", funcBody(t, js, "function forgetAddress() {"),
		`url.searchParams.delete("req")`, `url.hash = ""`, `history.replaceState(history.state, "", url.pathname + url.search + url.hash)`)
	for _, l := range []struct{ head, open string }{
		{`document.addEventListener("DOMContentLoaded", function () {`, "openFromAddress(true);"},
		{`window.addEventListener("hashchange", function () {`, "openFromAddress(false);"},
	} {
		_, rest, _ := strings.Cut(js, l.head)
		body, _, _ := strings.Cut(rest, "\n});")
		if open, forget := strings.Index(body, l.open), strings.Index(body, "forgetAddress();"); open < 0 || forget < open {
			t.Errorf("the listener %s does not call %s and then forgetAddress():%s", l.head, l.open, body)
		}
	}
	holdsEach(t, "goneText", funcBody(t, js, "function goneText(name) {"), strings.TrimPrefix(goneText("x"), "x"))
	holdsEach(t, "app.js", js, strings.TrimPrefix(missingText(Viewer{}, "x"), "x"))
}

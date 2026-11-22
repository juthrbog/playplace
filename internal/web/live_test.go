package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// retryNote is the recovery of an unavailable account list. The page
// shows the first part while live updates run and the second while the
// viewer paused them. The stylesheet picks one.
const retryNote = `<span class="retry-live">The page tries again every 15 seconds.</span>` +
	`<span class="retry-paused">Live updates are paused. Resume them to try again.</span>`

// liveLine is the live line that the server renders in the footer of a
// page that polls. app.js writes the time and the word of the button.
// templ writes a space between the parts, which a flex row does not draw.
const liveLine = `<div class="live"><span class="live-dot" aria-hidden="true"></span> ` +
	`<span id="live-ago" class="live-ago">Updated just now</span> ` +
	`<button type="button" id="live-pause" class="live-pause">Pause updates</button></div>`

// Every page with a viewer ends in the footer, after main. Only a page that
// polls holds the live line, and only that page has the poller. The line is
// not a live region, so the page keeps its one region.
func TestFooterOnEveryPageWithAViewer(t *testing.T) {
	ts, _ := newServer(t)
	for _, c := range []struct {
		path  string
		admin bool
		want  int
		polls bool
	}{
		{"/", false, http.StatusOK, true},
		{"/", true, http.StatusOK, true},
		{"/accounts", true, http.StatusOK, true},
		{"/accounts", false, http.StatusOK, true},
		{"/accounts/111111111111", true, http.StatusOK, false},
		{"/nope", false, http.StatusNotFound, false},
	} {
		res, body := do(t, ts, "GET", c.path, "ops@example.com", c.admin, nil, false)
		if res.StatusCode != c.want {
			t.Fatalf("GET %s = %d, want %d", c.path, res.StatusCode, c.want)
		}
		_, after, ok := strings.Cut(body, "</main>")
		if !ok || !strings.HasPrefix(after, `<footer class="foot">`) {
			t.Errorf("%s: the footer does not follow main:\n%s", c.path, after)
			continue
		}
		foot, _, _ := strings.Cut(after, "</footer>")
		if got := strings.Contains(foot, liveLine); got != c.polls {
			t.Errorf("%s: live line = %v, want %v:\n%s", c.path, got, c.polls, foot)
		}
		if n := strings.Count(body, " data-poll "); n != map[bool]int{true: 1, false: 0}[c.polls] {
			t.Errorf("%s: %d pollers, want one only on a page that polls", c.path, n)
		}
		for _, bad := range []string{"aria-live", "role=", "<output"} {
			if strings.Contains(foot, bad) {
				t.Errorf("%s: the footer holds %s, but the page has only one region", c.path, bad)
			}
		}
	}
}

// A page for nobody, such as a failed sign-in, has no footer: it has no
// poll, and nothing in a footer works there.
func TestNoFooterForNobody(t *testing.T) {
	a := &OIDCAuth{secret: []byte("test-secret"), cfg: OIDCConfig{RedirectURL: "http://localhost/cb", SessionTTL: time.Hour}}
	rec := httptest.NewRecorder()
	a.callback(rec, httptest.NewRequest("GET", "/auth/callback", nil))
	if body := rec.Body.String(); strings.Contains(body, "<footer") || strings.Contains(body, "live-pause") {
		t.Errorf("a page for nobody has a footer:\n%s", body)
	}
}

// app.js finds the poller by data-poll, not by the id of one section. The
// guard drops a poll tick while another request runs, while the viewer
// paused live updates and while the tab is hidden. The poll-now event makes
// the poller poll at once.
func TestPollerIsTheDataPollElement(t *testing.T) {
	js := staticText(t, "app.js")
	holdsEach(t, "poller", funcBody(t, js, "function poller() {"), `document.querySelector("[data-poll]")`)
	if strings.Contains(js, `getElementById("accounts")`) {
		t.Error(`app.js still finds the poller by getElementById("accounts")`)
	}
	guard := listeners(js, "htmx:beforeRequest")[0]
	holdsEach(t, "the poll guard", guard, "var poll = poller();", "ev.detail.elt === poll", "requestRunning() || pausedNow || document.hidden")
	holdsEach(t, "the beforeSend stamp", listeners(js, "htmx:beforeSend")[0], "var poll = poller();", "poll && ev.detail.elt === poll")
	holdsEach(t, "keepNode", funcBody(t, js, "function keepNode(node) {"), "swapSource === poller()")
	holdsEach(t, "pollNow", funcBody(t, js, "function pollNow() {"), "!pausedNow && !document.hidden", `htmx.trigger(poll, "poll-now")`)
}

// The pause lasts for the tab: app.js keeps it in sessionStorage, with
// every read and write in try/catch, and the state itself in a variable,
// so Pause works when the storage throws. Each press writes one short
// message into the live region, and Resume polls at once.
func TestPauseLastsForTheTab(t *testing.T) {
	js := staticText(t, "app.js")
	holdsEach(t, "app.js", js,
		`var pauseKey = "playplace.paused";`,
		"\nreadPaused();\n")
	holdsEach(t, "readPaused", funcBody(t, js, "function readPaused() {"),
		"try {", `pausedNow = window.sessionStorage.getItem(pauseKey) === "1";`, "} catch (err) {",
		`document.documentElement.classList.toggle("poll-paused", pausedNow);`)
	set := funcBody(t, js, "function setPaused(on) {")
	holdsEach(t, "setPaused", set, "pausedNow = on;", "try {", `window.sessionStorage.setItem(pauseKey, "1")`, "window.sessionStorage.removeItem(pauseKey)", "} catch (err) {")
	if n := strings.Count(js, "sessionStorage."); n != 3 {
		t.Errorf("app.js uses sessionStorage %d times, want the 3 guarded uses", n)
	}
	var press string
	for _, body := range listeners(js, "click") {
		if strings.Contains(body, `"#live-pause"`) {
			press = body
		}
	}
	holdsEach(t, "the Pause listener", press, "setPaused(!pausedNow)", `tell(pausedNow ? "Live updates paused." : "Live updates on.")`, "pollNow()")
	holdsEach(t, "tell", funcBody(t, js, "function tell(text) {"), `getElementById("notice")`, "span.textContent = text", "region.replaceChildren(span)")
}

// A hidden tab does not poll. When it shows again, the line catches up and
// the page polls once at once, unless the viewer paused live updates.
func TestHiddenTabPollsWhenItShows(t *testing.T) {
	js := staticText(t, "app.js")
	_, rest, ok := strings.Cut(js, "\n"+`document.addEventListener("visibilitychange", function () {`)
	if !ok {
		t.Fatal("app.js has no visibilitychange listener")
	}
	body, _, _ := strings.Cut(rest, "\n});")
	holdsEach(t, "the visibilitychange listener", body, "!document.hidden", "readPaused();\n    liveTick();\n    pollNow();")
}

// A page that Back or Forward shows from the back/forward cache reads the
// stored pause again, because another page of the tab may have changed it.
func TestCachedPageReadsThePauseAgain(t *testing.T) {
	js := staticText(t, "app.js")
	_, rest, ok := strings.Cut(js, "\n"+`window.addEventListener("pageshow", function (ev) {`)
	if !ok {
		t.Fatal("app.js has no pageshow listener")
	}
	body, _, _ := strings.Cut(rest, "\n});")
	holdsEach(t, "the pageshow listener", body, "!ev.persisted", "readPaused()", "liveTick()", "pollNow()")
}

// The live line says how old the data is. The time ticks each second in
// the first minute, then each minute. It resets after a poll that the
// server answered, 200 or 204, while the poller shows fresh data, unless
// the page dropped that answer. The dot blinks only on a 200 that the page
// swapped. Fresh data from an action answer resets the time too.
func TestLiveLineTicksAndBlinks(t *testing.T) {
	js := staticText(t, "app.js")
	holdsEach(t, "agoText", funcBody(t, js, "function agoText(ms) {"),
		`"just now"`, `s + " seconds ago"`, `"1 minute ago" : m + " minutes ago"`, `"1 hour ago" : h + " hours ago"`)
	holdsEach(t, "liveTick", funcBody(t, js, "function liveTick() {"),
		`pausedNow ? "Paused. " : ""`, `lead + "Updated " + agoText(ms)`, `lead + "Not updated yet"`,
		"ms < 60000 ? 1000 : 60000", "setTimeout(liveTick, step - (ms % step))",
		`pausedNow ? "Resume updates" : "Pause updates"`)
	var reset string
	for _, body := range listeners(js, "htmx:afterRequest") {
		if strings.Contains(body, "updatedAt = Date.now()") {
			reset = body
		}
	}
	holdsEach(t, "the live reset", reset,
		"d.elt !== poll", "d.xhr.status !== 200 && d.xhr.status !== 204",
		`poll.querySelector("[data-stale]")`,
		"if (d.xhr.pollSeq !== actionSeq) {\n    return;\n  }\n  updatedAt = Date.now();",
		"d.xhr.status === 200", "blinkDot()")
	oob := listeners(js, "htmx:oobAfterSwap")
	if len(oob) != 1 {
		t.Fatalf("app.js has %d htmx:oobAfterSwap listeners, want 1", len(oob))
	}
	holdsEach(t, "the out-of-band action reset", oob[0], "actionRefreshed(ev.detail && ev.detail.target, null)")
	swap := listeners(js, "htmx:afterSwap")
	if len(swap) != 1 {
		t.Fatalf("app.js has %d htmx:afterSwap listeners, want 1", len(swap))
	}
	holdsEach(t, "the main-swap action reset", swap[0], "actionRefreshed(ev.detail && ev.detail.target, ev.detail && ev.detail.elt)")
	holdsEach(t, "actionRefreshed", funcBody(t, js, "function actionRefreshed(target, source) {"),
		"target.id !== poll.id", "source === poll", "if (!poll.querySelector(\"[data-stale]\")) {\n    updatedAt = Date.now();\n  }", "liveTick()")
	holdsEach(t, "blinkDot", funcBody(t, js, "function blinkDot() {"),
		`dot.classList.remove("blink")`, "void dot.offsetWidth", `dot.classList.add("blink")`)
}

// The footer lines up with main. The live line shows only with script, as
// a pencil-grey line after an 8 px shelf-ink dot. The dot hides for one
// 90 ms step and does not blink under prefers-reduced-motion. While paused,
// the dot hides and an unavailable list names Resume as its recovery.
func TestLiveLineFollowsTheDesign(t *testing.T) {
	layout, layoutMedia := layerRules(t, "layout")
	wantDecls(t, "layout", layout, ".foot", map[string]string{"display": "flex", "max-width": "1600px", "padding": "0 var(--space-xl) var(--space-xl)"})
	wantDecls(t, "layout", layout, "html:not(.js) .foot:not(:has(> :not(.live, .keys)))", map[string]string{"display": "none"})
	wantDecls(t, "layout (max-width: 640px)", layoutMedia["(max-width: 640px)"], ".foot", map[string]string{"padding": "0 var(--space-lg) var(--space-lg)"})
	top, media := layerRules(t, "components")
	wantDecls(t, "components", top, ".live", map[string]string{"display": "none", "color": "var(--ink-2)"})
	wantDecls(t, "components", top, "html.js .live", map[string]string{"display": "flex"})
	wantDecls(t, "components", top, ".live-dot", map[string]string{"width": "8px", "height": "8px", "border-radius": "50%", "background": "var(--shelf-ink)"})
	wantDecls(t, "components", top, ".live-pause", map[string]string{"height": "var(--control-h-sm)", "padding": "0 11px"})
	wantDecls(t, "components", top, ".live-dot.blink", map[string]string{"animation": "live-blink 90ms steps(1)"})
	wantDecls(t, "components", top, "from, to", map[string]string{"visibility": "hidden"})
	wantDecls(t, "components (prefers-reduced-motion: reduce)", media["(prefers-reduced-motion: reduce)"], ".live-dot.blink", map[string]string{"animation": "none"})
	wantDecls(t, "components", top, ".retry-paused", map[string]string{"display": "none"})
	wantDecls(t, "components", top, "html.poll-paused .live-dot", map[string]string{"visibility": "hidden"})
	wantDecls(t, "components", top, "html.poll-paused .retry-live", map[string]string{"display": "none"})
	wantDecls(t, "components", top, "html.poll-paused .retry-paused", map[string]string{"display": "inline"})
}

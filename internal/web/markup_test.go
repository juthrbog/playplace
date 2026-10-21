package web

import (
	"context"
	"html"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"playplace/internal/core"
)

// The helpers in this file read one request and the live region from a page
// or an answer. They hold what the tests know about the queue markup. A new
// queue markup then changes their bodies, not the tests that call them.

var (
	// versionInput is the hidden field that binds a form to the version of
	// the request that the page shows.
	versionInput = regexp.MustCompile(`name="version" value="([^"]+)"`)
	// queueRegion is the empty live region in its place, right above its
	// refusal slot. That is the bulk bar of the approval queue, or the
	// head of Your requests.
	queueRegion = regexp.MustCompile(`<section id="pending" class="panel(?: desk)?" aria-labelledby="pending-title">(?:<!--.*?-->)?<div class="panel-head">.*?</div>(?:<div id="desk-bar" class="desk-bar">)?` + regexp.QuoteMeta(emptyRegion) + `<div id="pending-error" class="refusal"></div>`)
	// slipStart is the start tag of one slip.
	slipStart = regexp.MustCompile(`<li id="req-([a-z0-9-]+)" class="slip[^"]*" data-state="(pending|decided|gone)"(?: data-result="([a-z-]+)")?>`)
	// slipNote is the result note in a slip.
	slipNote = regexp.MustCompile(`<div class="slip-note" id="result-[a-z0-9-]+">` + regexp.QuoteMeta(alertIcon) + `<span>(.*?)</span></div>`)
	// barItem is one line of the bulk bar.
	barItem = regexp.MustCompile(`<(?:li|span) data-name="([a-z0-9-]+)">(.*?)</(?:li|span)>`)
	// factNow is the value of a fact as it is now.
	factNow = regexp.MustCompile(`<span class="fact-now">([^<]*)</span>`)
	// stampWord is the word stamp of a decided slip.
	stampWord = regexp.MustCompile(`<span class="stamp stamp-word stamp-(approved|denied) (turn-[123]) stamp-new">(approved|denied)</span>`)
	// queueStart is the queue as the main part of an answer, and queuePart
	// is the queue as an out-of-band part.
	queueStart = regexp.MustCompile(`^<section id="pending" class="panel(?: desk)?" aria-labelledby="pending-title">`)
	queuePart  = regexp.MustCompile(`<section id="pending" class="panel(?: desk)?" hx-swap-oob="morph"`)
	// hiddenInput is one hidden field of a form.
	hiddenInput = regexp.MustCompile(`<input type="hidden" name="([^"]+)" value="([^"]*)">`)
)

// slipIn returns the markup of the request name in body: its slip in the
// queue, its edit slip, or its row of Your requests. It returns "" when body
// does not show the request. A slip holds no li element, so the first </li>
// ends it.
func slipIn(body, name string) string {
	return regexp.MustCompile(`(?s)<li id="req-` + regexp.QuoteMeta(name) + `" .*?</li>`).FindString(body)
}

// refIn returns the reference that body carries for the request name: the
// name and the version that the buttons of the request post. core refuses a
// click with this reference when the request changed after body was made.
func refIn(t *testing.T, body, name string) core.RequestRef {
	t.Helper()
	m := versionInput.FindStringSubmatch(slipIn(body, name))
	if m == nil {
		t.Fatalf("no version for %s in:\n%s", name, body)
	}
	return core.RequestRef{Name: name, Version: m[1]}
}

// refForm returns the form that a button of one request posts for ref: the
// version alone, as the form of an older page posts it. A slip of this page
// also posts its shown fields, which slipForm reads.
func refForm(ref core.RequestRef) url.Values {
	return url.Values{"version": {ref.Version}}
}

// slipForm returns the hidden fields of the form that posts action for the
// request name in body, as a browser sends them: the version and the shown
// fields. It fails the test when the slip has no such form.
func slipForm(t *testing.T, body, name, action string) url.Values {
	t.Helper()
	form := regexp.MustCompile(`(?s)<form [^>]*hx-post="/requests/` + regexp.QuoteMeta(name) + `/` + regexp.QuoteMeta(action) + `"[^>]*>(.*?)</form>`).FindStringSubmatch(slipIn(body, name))
	if form == nil {
		t.Fatalf("no %s form for %s in:\n%s", action, name, body)
	}
	out := url.Values{}
	for _, m := range hiddenInput.FindAllStringSubmatch(form[1], -1) {
		out.Add(m[1], html.UnescapeString(m[2]))
	}
	return out
}

// undecidedIn returns the names of the requests that an answer to a
// decision did not decide: first the slips that hold a result and wait or
// left the queue, in page order, then the lines of the bulk bar.
func undecidedIn(body string) []string {
	var names []string
	for _, m := range slipStart.FindAllStringSubmatch(body, -1) {
		if m[3] != "" && m[2] != "decided" {
			names = append(names, m[1])
		}
	}
	for _, m := range barItem.FindAllStringSubmatch(body, -1) {
		names = append(names, m[1])
	}
	return names
}

// slipsIn returns the names of the slips in body whose state is state, in
// page order.
func slipsIn(body, state string) []string {
	var names []string
	for _, m := range slipStart.FindAllStringSubmatch(body, -1) {
		if m[2] == state {
			names = append(names, m[1])
		}
	}
	return names
}

// decidedIn returns the names of the slips that the answer decided, in page
// order.
func decidedIn(body string) []string { return slipsIn(body, "decided") }

// goneIn returns the names of the gone slips, in page order.
func goneIn(body string) []string { return slipsIn(body, "gone") }

// resultIn returns the reason that an answer to a decision gives for the
// request name: the note in its slip, or else its line in the bulk bar. It
// returns "" when the answer gives no reason for name, for example because
// the decision went through.
func resultIn(body, name string) string {
	if m := slipNote.FindStringSubmatch(slipIn(body, name)); m != nil {
		return html.UnescapeString(m[1])
	}
	for _, m := range barItem.FindAllStringSubmatch(body, -1) {
		if m[1] == name {
			return html.UnescapeString(m[2])
		}
	}
	return ""
}

// factsIn returns the lifetime and the budget that the slip of name shows
// now. It fails the test when the slip does not show both.
func factsIn(t *testing.T, body, name string) (lifetime, budget string) {
	t.Helper()
	m := factNow.FindAllStringSubmatch(slipIn(body, name), -1)
	if len(m) != 2 {
		t.Fatalf("the slip of %s shows %d facts, want 2:\n%s", name, len(m), body)
	}
	return html.UnescapeString(m[0][1]), html.UnescapeString(m[1][1])
}

// stampIn returns the word and the turn of the stamp in the slip of name,
// and "" when the slip has no stamp.
func stampIn(body, name string) (word, turn string) {
	m := stampWord.FindStringSubmatch(slipIn(body, name))
	if m == nil {
		return "", ""
	}
	return m[3], m[2]
}

// regionIn returns what an answer writes into the live region #notice, and
// false when the answer writes nothing there.
func regionIn(body string) (string, bool) {
	m := regionPart.FindStringSubmatch(body)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// queueAnswer reports whether an answer is the queue itself, as the answer
// to a decision in the queue is.
func queueAnswer(body string) bool {
	return queueStart.MatchString(body)
}

// queueOOB reports whether an answer carries the queue as an out-of-band
// part, as a poll does.
func queueOOB(body string) bool {
	return queuePart.MatchString(body)
}

// regionInQueue reports whether a page holds the empty live region in the
// queue, next to the decisions.
func regionInQueue(body string) bool {
	return queueRegion.MatchString(body)
}

// The helpers find one request by its name, so a check on one request never
// reads the markup of the request next to it. refIn gives the version that
// core stores, from the queue and from the edit form.
func TestHelpersReadOneRequest(t *testing.T) {
	ts, prov := newServer(t)
	names := map[string]string{"first-one": "second-one", "second-one": "first-one"}
	for name, who := range map[string]string{"first-one": "bob", "second-one": "carl"} {
		if res, body := do(t, ts, "POST", "/requests", who+"@example.com", false, url.Values{"name": {name}}, true); res.Header.Get("X-Form-Error") != "" {
			t.Fatalf("submit %s = %s", name, body)
		}
	}
	_, home := do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	stored := core.NewService(prov, nil, core.DefaultConfig(), nil)
	for name, other := range names {
		q, err := stored.GetRequest(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		slip := slipIn(home, name)
		if !strings.Contains(slip, "/requests/"+name+"/approve") || strings.Contains(slip, other) {
			t.Errorf("slipIn(%s) is not the markup of %s alone:\n%s", name, name, slip)
		}
		if got := refIn(t, home, name); got != q.Ref() {
			t.Errorf("refIn(%s) = %+v, want %+v", name, got, q.Ref())
		}
		_, edit := do(t, ts, "GET", "/requests/"+name+"/edit", "ops@example.com", true, nil, true)
		if got := refIn(t, edit, name); got != q.Ref() || !strings.Contains(slipIn(edit, name), `name="ttl"`) {
			t.Errorf("the edit form of %s gives %+v, want %+v:\n%s", name, got, q.Ref(), edit)
		}
		if form := refForm(q.Ref()); len(form) != 1 || form.Get("version") != q.Version() {
			t.Errorf("refForm(%s) = %v, want the version alone", name, form)
		}
	}
	if slip := slipIn(home, "no-such-request"); slip != "" {
		t.Errorf("slipIn finds a request that the page does not show:\n%s", slip)
	}
	if lifetime, budget := factsIn(t, home, "first-one"); lifetime != "14 days" || budget != "$50" {
		t.Errorf("factsIn(first-one) = %q, %q, want 14 days and $50", lifetime, budget)
	}

	// The home page holds the region in the queue and writes nothing into it.
	// An answer writes its message.
	if !regionInQueue(home) {
		t.Errorf("the home page does not hold the region in the queue:\n%s", home)
	}
	if got, ok := regionIn(home); ok {
		t.Errorf("regionIn(home) = %q, but a page writes nothing into the region", got)
	}
	_, account := do(t, ts, "GET", "/accounts/111111111111", "ops@example.com", true, nil, false)
	if regionInQueue(account) {
		t.Errorf("the account page holds the region in a queue:\n%s", account)
	}
	_, answer := do(t, ts, "POST", "/requests/first-one/approve", "ops@example.com", true, refForm(refIn(t, home, "first-one")), true)
	if got, ok := regionIn(answer); !ok || !strings.HasSuffix(got, "<span>Approved first-one. AWS is creating the account.</span>") {
		t.Errorf("regionIn(answer) = %q, %v, want the approval:\n%s", got, ok, answer)
	}
	if !queueAnswer(answer) || queueAnswer(home) {
		t.Errorf("queueAnswer is %v for the answer and %v for the page, want true and false", queueAnswer(answer), queueAnswer(home))
	}
	if got := decidedIn(answer); !slices.Equal(got, []string{"first-one"}) {
		t.Errorf("decidedIn(answer) = %v, want [first-one]:\n%s", got, answer)
	}
	if word, _ := stampIn(answer, "first-one"); word != "approved" {
		t.Errorf("stampIn(answer, first-one) = %q, want approved", word)
	}
	_, polled := poll(t, ts, "/accounts", "ops@example.com", true, true, "")
	if !queueOOB(polled) || queueOOB(home) {
		t.Errorf("queueOOB is %v for a poll and %v for the page, want true and false", queueOOB(polled), queueOOB(home))
	}
}

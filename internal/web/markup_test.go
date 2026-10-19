package web

import (
	"context"
	"net/url"
	"regexp"
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
	// queueRegion is the empty live region in its place: in the queue, under
	// its head, and right above its refusal slot.
	queueRegion = regexp.MustCompile(`<section id="pending" class="panel" aria-labelledby="pending-title">(?:<!--.*?-->)?<div class="panel-head">.*?</div>` + regexp.QuoteMeta(emptyRegion) + `<div id="pending-error" class="refusal"></div>`)
)

// slipIn returns the markup of the request name in body: its row in the
// queue, or its edit form. It returns "" when body does not show the request.
func slipIn(body, name string) string {
	return regexp.MustCompile(`(?s)<tr id="req-` + regexp.QuoteMeta(name) + `">.*?</tr>`).FindString(body)
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

// refForm returns the form that a button of one request posts for ref. The
// path of the post names the request, and the form carries the version.
func refForm(ref core.RequestRef) url.Values {
	return url.Values{"version": {ref.Version}}
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
	if got, ok := regionIn(answer); !ok || !strings.HasSuffix(got, "<span>Approved first-one.</span>") {
		t.Errorf("regionIn(answer) = %q, %v, want the approval:\n%s", got, ok, answer)
	}
}

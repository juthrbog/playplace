package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

// tweakChanged is the refusal of a stale action on the request tweak.
const tweakChanged = "tweak changed after you loaded the page. Read the new values, then try again."

// An approver's page loaded before an admin edit must not approve the new
// values; the refusal re-renders the pending card with what is there now.
func TestStaleApproveFromThePageIsRefused(t *testing.T) {
	ts, prov := newServer(t)
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"tweak"}, "budget": {"20"}}, true)
	_, page := doAs(t, ts, "GET", "/", "lead@example.com", false, true, nil, false)
	seen := refForm(refIn(t, page, "tweak"))

	_, form := do(t, ts, "GET", "/requests/tweak/edit", "ops@example.com", true, nil, true)
	edit := refForm(refIn(t, form, "tweak"))
	edit.Set("owner", "bob@example.com")
	edit.Set("budget", "400")
	if res, body := do(t, ts, "POST", "/requests/tweak/edit", "ops@example.com", true, edit, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("edit = %s", body)
	}

	res, body := doAs(t, ts, "POST", "/requests/tweak/approve", "lead@example.com", false, true, seen, true)
	if res.Header.Get("X-Form-Error") != "1" || !strings.Contains(body, tweakChanged) || !strings.Contains(body, "$400") {
		t.Fatalf("stale approve should be refused with the current values: %v\n%s", res.Header, body)
	}
	// The refusal sits in the pending card, beside the row, not under New
	// request. htmx swaps in order, so the card comes before the message
	// that fills its slot.
	card, slot := strings.Index(body, `id="pending"`), strings.Index(body, `<div id="pending-error" hx-swap-oob="innerHTML">`)
	if strings.Contains(body, `id="form-error"`) || card < 0 || slot < card {
		t.Fatalf("the refusal should fill the pending card's slot after the card:\n%s", body)
	}
	if creates, _ := prov.ListCreateRequests(context.Background()); len(creates) != 0 {
		t.Fatal("a stale approve created an account")
	}
	// The refreshed card carries the new version, and that click works.
	res, _ = doAs(t, ts, "POST", "/requests/tweak/approve", "lead@example.com", false, true, refForm(refIn(t, body, "tweak")), true)
	if res.Header.Get("X-Form-Error") != "" {
		t.Fatal("approving the refreshed row should work")
	}
	if creates, _ := prov.ListCreateRequests(context.Background()); len(creates) != 1 {
		t.Fatal("approval should request one account")
	}
}

// Deny and withdraw post the version the page showed, as approve does. After
// an admin edit, both clicks are refused and the request stays in the queue.
func TestStaleDenyAndWithdrawFromThePageAreRefused(t *testing.T) {
	ts, prov := newServer(t)
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"tweak"}, "budget": {"20"}}, true)
	_, page := doAs(t, ts, "GET", "/", "lead@example.com", false, true, nil, false)
	approverSaw := refForm(refIn(t, page, "tweak"))
	_, page = do(t, ts, "GET", "/", "bob@example.com", false, nil, false)
	requesterSaw := refForm(refIn(t, page, "tweak"))

	_, form := do(t, ts, "GET", "/requests/tweak/edit", "ops@example.com", true, nil, true)
	edit := refForm(refIn(t, form, "tweak"))
	edit.Set("owner", "bob@example.com")
	edit.Set("budget", "400")
	if res, body := do(t, ts, "POST", "/requests/tweak/edit", "ops@example.com", true, edit, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("edit = %s", body)
	}
	pending := func() bool {
		_, err := core.NewService(prov, nil, core.DefaultConfig(), nil).GetRequest(context.Background(), "tweak")
		return err == nil
	}

	// The reason rule runs before core, so the stale deny sends a reason.
	approverSaw.Set("reason", "no budget left")
	res, body := doAs(t, ts, "POST", "/requests/tweak/deny", "lead@example.com", false, true, approverSaw, true)
	if res.Header.Get("X-Form-Error") != "1" || !strings.Contains(body, tweakChanged) || !pending() {
		t.Fatalf("stale deny should be refused and keep the request: %v\n%s", res.Header, body)
	}
	res, body = do(t, ts, "POST", "/requests/tweak/withdraw", "bob@example.com", false, requesterSaw, true)
	if res.Header.Get("X-Form-Error") != "1" || !strings.Contains(body, tweakChanged) || !pending() {
		t.Fatalf("stale withdraw should be refused and keep the request: %v\n%s", res.Header, body)
	}
	// The refreshed card carries the new version, and that click works.
	res, body = do(t, ts, "POST", "/requests/tweak/withdraw", "bob@example.com", false, refForm(refIn(t, body, "tweak")), true)
	if res.Header.Get("X-Form-Error") != "" || pending() {
		t.Fatalf("withdrawing the refreshed row should work: %v\n%s", res.Header, body)
	}
}

// A requester whose page is stale can find that the name now holds someone
// else's request. The click is refused as stale, not as a permission error,
// and the reply names nobody, because the requester cannot see that request.
func TestStaleWithdrawAfterAResubmissionIsRefusedAsStale(t *testing.T) {
	ts, prov := newServer(t)
	do(t, ts, "POST", "/requests", "carl@example.com", false, url.Values{"name": {"shared"}, "budget": {"20"}}, true)
	_, page := do(t, ts, "GET", "/", "carl@example.com", false, nil, false)
	carlSaw := refForm(refIn(t, page, "shared"))
	// Carl withdraws in another tab, and bob takes the name.
	if res, body := do(t, ts, "POST", "/requests/shared/withdraw", "carl@example.com", false, carlSaw, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("withdraw = %s", body)
	}
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"shared"}, "budget": {"30"}}, true)

	res, body := do(t, ts, "POST", "/requests/shared/withdraw", "carl@example.com", false, carlSaw, true)
	if res.StatusCode == http.StatusForbidden || res.Header.Get("X-Form-Error") != "1" || !strings.Contains(body, "shared changed after you loaded the page. Read the new values, then try again.") || !strings.Contains(body, `id="pending"`) {
		t.Fatalf("a stale withdraw should be refused as stale with a fresh card: %d %v\n%s", res.StatusCode, res.Header, body)
	}
	if strings.Contains(body, "bob@example.com") || strings.Contains(body, `id="req-shared"`) {
		t.Fatalf("the refusal shows a request carl cannot see:\n%s", body)
	}
	if q, err := core.NewService(prov, nil, core.DefaultConfig(), nil).GetRequest(context.Background(), "shared"); err != nil || q.RequestedBy != "bob@example.com" {
		t.Fatalf("bob's request should stay pending: %+v %v", q, err)
	}
	// With the current version, a viewer who may not withdraw still gets 403.
	_, page = do(t, ts, "GET", "/", "bob@example.com", false, nil, false)
	if res, _ := do(t, ts, "POST", "/requests/shared/withdraw", "carl@example.com", false, refForm(refIn(t, page, "shared")), true); res.StatusCode != http.StatusForbidden {
		t.Fatalf("withdrawing someone else's current request = %d, want 403", res.StatusCode)
	}
}

// The pending row binds to the exact lifetime and budget, so it shows them
// exactly: a lifetime that is not whole days shows in hours.
func TestPendingRowShowsTheExactValues(t *testing.T) {
	ts, _ := newServer(t)
	if res, body := do(t, ts, "POST", "/requests", "ops@example.com", true, url.Values{"name": {"short"}, "owner": {"dana@example.com"}, "ttl": {"2h"}, "budget": {"412.5"}}, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("submit = %s", body)
	}
	_, page := do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	if !strings.Contains(page, "<td>2 hours</td>") || !strings.Contains(page, "<td>$412.50</td>") {
		t.Fatalf("the pending row should show 2 hours and $412.50:\n%s", page)
	}
}

// slackStub answers the Slack Web API and captures response_url posts.
type slackStub struct {
	mu        sync.Mutex
	posted    []string // button values of chat.postMessage
	texts     []string // section text of chat.postMessage
	responses chan string
}

func (s *slackStub) RoundTrip(r *http.Request) (*http.Response, error) {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
	}
	reply := `{"ok":true}`
	switch {
	case strings.HasSuffix(r.URL.Path, "/users.info"):
		reply = `{"ok":true,"user":{"profile":{"email":"lead@example.com"}}}`
	case strings.HasSuffix(r.URL.Path, "/chat.postMessage"):
		var m struct {
			Blocks []struct {
				Text struct {
					Text string `json:"text"`
				} `json:"text"`
				Elements []struct {
					Value string `json:"value"`
				} `json:"elements"`
			} `json:"blocks"`
		}
		if err := json.Unmarshal(body, &m); err != nil || len(m.Blocks) < 2 || len(m.Blocks[1].Elements) == 0 {
			return nil, fmt.Errorf("unexpected message %s: %v", body, err)
		}
		s.mu.Lock()
		s.posted = append(s.posted, m.Blocks[1].Elements[0].Value)
		s.texts = append(s.texts, m.Blocks[0].Text.Text)
		s.mu.Unlock()
	default: // response_url
		var m struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, err
		}
		s.responses <- m.Text
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(reply)), Header: http.Header{}}, nil
}

// slackServer runs the web server with Slack wired to a stub API.
func slackServer(t *testing.T) (*httptest.Server, *core.Service, *fake.Provider, *Slack, *slackStub) {
	t.Helper()
	return slackServerOn(t, func(p *fake.Provider) core.Provider { return p }, slog.New(slog.DiscardHandler))
}

// slackServerOn is slackServer with the fake wrapped and the server log
// written to log.
func slackServerOn(t *testing.T, wrap func(*fake.Provider) core.Provider, log *slog.Logger) (*httptest.Server, *core.Service, *fake.Provider, *Slack, *slackStub) {
	t.Helper()
	prov := fake.New()
	cfg := core.DefaultConfig()
	cfg.InventoryTTL = 0
	svc := core.NewService(wrap(prov), nil, cfg, nil)
	stub := &slackStub{responses: make(chan string, 1)}
	sl := &Slack{SigningSecret: "s3cret", Client: &http.Client{Transport: stub}, IsApprover: func(string) bool { return true }}
	ts := httptest.NewServer(New(svc, log, headerAuth{}, sl, nil).Handler())
	t.Cleanup(ts.Close)
	return ts, svc, prov, sl, stub
}

// slackClick sends a signed button click as lead@example.com and returns
// the reply posted to the response URL.
func slackClick(t *testing.T, ts *httptest.Server, sl *Slack, stub *slackStub, action, value string) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"type": "block_actions", "response_url": "https://hooks.example/r",
		"user": map[string]string{"id": "U1"}, "actions": []map[string]string{{"action_id": action, "value": value}}})
	body := []byte("payload=" + url.QueryEscape(string(payload)))
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	req, _ := http.NewRequest("POST", ts.URL+"/slack/interact", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Request-Timestamp", stamp)
	req.Header.Set("X-Slack-Signature", sl.sign(stamp, body))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("interact: %v", err)
	}
	_ = res.Body.Close() // the reply arrives through the response URL
	if res.StatusCode != http.StatusOK {
		t.Fatalf("interact = %d", res.StatusCode)
	}
	select {
	case text := <-stub.responses:
		return text
	case <-time.After(5 * time.Second):
		t.Fatal("no Slack response")
		return ""
	}
}

func TestSlackButtonsAreBoundToThePostedVersion(t *testing.T) {
	ts, svc, prov, sl, stub := slackServer(t)
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"slacked"}, "budget": {"20"}}, true)
	stub.mu.Lock()
	posted := append([]string(nil), stub.posted...)
	stub.mu.Unlock()
	if len(posted) != 1 || !strings.HasPrefix(posted[0], "slacked ") {
		t.Fatalf("posted = %v", posted)
	}
	click := func(action, value string) string { return slackClick(t, ts, sl, stub, action, value) }

	// An admin raises the budget after the message was posted.
	q, _ := svc.GetRequest(context.Background(), "slacked")
	if _, err := svc.UpdateRequest(context.Background(), q.Ref(), "ops@example.com", core.RequestEdit{BudgetUSD: 400}); err != nil {
		t.Fatal(err)
	}
	if text := click("approve", posted[0]); text != "Not approved. slacked changed after you loaded the page. Read the new values, then try again." {
		t.Fatalf("stale Slack approve = %q", text)
	}
	if text := click("deny", posted[0]); text != "Not denied. slacked changed after you loaded the page. Read the new values, then try again." {
		t.Fatalf("stale Slack deny = %q", text)
	}
	if text := click("deny", "slacked"); !strings.Contains(text, "older playplace") {
		t.Fatalf("a button without a version = %q", text)
	}
	if creates, _ := prov.ListCreateRequests(context.Background()); len(creates) != 0 {
		t.Fatal("a stale click created an account")
	}
	if _, err := svc.GetRequest(context.Background(), "slacked"); err != nil {
		t.Fatalf("a stale click ended the request: %v", err)
	}
	// A message showing the current values approves them.
	q, _ = svc.GetRequest(context.Background(), "slacked")
	if text := click("approve", refValue(q.Ref())); !strings.Contains(text, "approved `slacked`") {
		t.Fatalf("current Slack approve = %q", text)
	}
}

// The button value posted to Slack comes from the record SubmitRequest
// returns, not from the stored tag. Both must give the same version, or
// every fresh button is refused. The message must also show every value
// the button binds to: the exact budget, the exact lifetime, and the
// override.
func TestSlackPostedButtonApprovesTheUnchangedRequest(t *testing.T) {
	ts, svc, _, sl, stub := slackServer(t)
	for i, tc := range []struct {
		name, owner, ttl, budget, purpose string
		override                          bool
		text                              []string
	}{
		{name: "odd-a", owner: "anna@example.com", ttl: "0.05", budget: "12.34", purpose: "  padded purpose  ", text: []string{"$12.34/month, 2 hours."}},
		{name: "odd-b", owner: "carl@example.com", ttl: "36.5h", budget: "20", text: []string{"$20/month, 37 hours."}},
		{name: "odd-c", owner: "dana@example.com", ttl: "120", budget: "900.5", purpose: "x", override: true,
			text: []string{"$900.50/month", "120 days.", "Limits overridden by an admin."}},
	} {
		form := url.Values{"name": {tc.name}, "owner": {tc.owner}, "ttl": {tc.ttl}, "budget": {tc.budget}, "purpose": {tc.purpose}}
		if tc.override {
			form.Set("override", "on")
		}
		if res, body := do(t, ts, "POST", "/requests", "ops@example.com", true, form, true); res.Header.Get("X-Form-Error") != "" {
			t.Fatalf("submit %s: %s", tc.name, body)
		}
		stub.mu.Lock()
		posted, text := stub.posted[i], stub.texts[i]
		stub.mu.Unlock()
		for _, want := range tc.text {
			if !strings.Contains(text, want) {
				t.Errorf("%s: message %q is missing %q", tc.name, text, want)
			}
		}
		if !tc.override && strings.Contains(text, "overridden") {
			t.Errorf("%s: message %q claims an override", tc.name, text)
		}
		if reply := slackClick(t, ts, sl, stub, "approve", posted); !strings.Contains(reply, "approved `"+tc.name+"`") {
			q, _ := svc.GetRequest(context.Background(), tc.name)
			t.Errorf("%s: the posted button was refused: %q (posted %q, stored %q)", tc.name, reply, posted, refValue(q.Ref()))
		}
	}
}

// serve caches the inventory, and another process (the CLI or the TUI) can
// change the queue behind it. A refused click must show the current request
// with a version that works, not the cached card with the stale version.
func TestCrossProcessChangeShowsTheCurrentRequest(t *testing.T) {
	prov := fake.New()
	prov.Now = func() time.Time { return t0 }
	cfg := core.DefaultConfig() // the inventory cache stays on, as in production
	serve := core.NewService(prov, nil, cfg, nil)
	serve.Now = func() time.Time { return t0 }
	cli := core.NewService(prov, nil, cfg, nil) // a second process on the same OU
	cli.Now = func() time.Time { return t0 }
	ts := httptest.NewServer(New(serve, slog.New(slog.DiscardHandler), headerAuth{}, nil, nil).Handler())
	defer ts.Close()
	ctx := context.Background()

	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"tweak"}, "budget": {"20"}}, true)
	do(t, ts, "POST", "/requests", "carl@example.com", false, url.Values{"name": {"mine"}, "budget": {"20"}}, true)
	_, page := doAs(t, ts, "GET", "/", "lead@example.com", false, true, nil, false)
	seen := refForm(refIn(t, page, "tweak"))
	_, own := do(t, ts, "GET", "/", "carl@example.com", false, nil, false)
	mine := refForm(refIn(t, own, "mine"))

	q, err := cli.GetRequest(ctx, "tweak")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.UpdateRequest(ctx, q.Ref(), "ops@example.com", core.RequestEdit{BudgetUSD: 400}); err != nil {
		t.Fatal(err)
	}
	res, body := doAs(t, ts, "POST", "/requests/tweak/approve", "lead@example.com", false, true, seen, true)
	if res.Header.Get("X-Form-Error") != "1" || !strings.Contains(body, tweakChanged) || !strings.Contains(body, "$400") {
		t.Fatalf("the refusal should show the edited request:\n%s", body)
	}
	fresh := refForm(refIn(t, body, "tweak"))
	if fresh.Get("version") == seen.Get("version") {
		t.Fatal("the refusal rendered the stale version again")
	}
	if res, body := doAs(t, ts, "POST", "/requests/tweak/approve", "lead@example.com", false, true, fresh, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("approving the refreshed card = %s", body)
	}

	// The other process denies carl's request. His withdraw click finds
	// nothing, and the card he gets back no longer lists it.
	q, err = cli.GetRequest(ctx, "mine")
	if err != nil {
		t.Fatal(err)
	}
	if err := cli.Deny(ctx, q.Ref(), "lead@example.com", "no"); err != nil {
		t.Fatal(err)
	}
	res, body = do(t, ts, "POST", "/requests/mine/withdraw", "carl@example.com", false, mine, true)
	if res.Header.Get("X-Form-Error") != "1" || strings.Contains(body, `id="req-mine"`) {
		t.Fatalf("the refusal should drop the denied request from the card:\n%s", body)
	}
}

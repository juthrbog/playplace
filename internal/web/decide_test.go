package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"playplace/internal/audit"
	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

// checkIcon is how the region shows the result of an action that went
// through.
const checkIcon = `<svg class="i i-ok" aria-hidden="true" focusable="false"><use href="#i-check"></use></svg>`

// The refusals of a deny reason, as people read them.
const (
	reasonMissing    = "Write the reason for the denial. playplace cannot deny a request without one."
	reasonTooLong    = "The reason can have at most 500 characters."
	reasonCharacters = "The reason holds a hidden character, for example a control or text direction character. Type the reason again."
)

// The reasons that refusal gives for a request that changed or left the
// queue.
func changedText(name string) string {
	return name + " changed after you loaded the page. Read the new values, then try again."
}

func goneText(name string) string {
	return name + " is no longer waiting. Someone else decided it, the owner withdrew it, or it was dropped."
}

// clock is a time that a test moves. The service reads it, and the fakes
// of AWS move it.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// desk is the web server over a fake with no accounts, for the decision
// tests. The service and the fake read the time from clock.
type desk struct {
	ts      *httptest.Server
	handler http.Handler
	svc     *core.Service
	hist    *audit.Memory
	clock   *clock
}

// newDesk starts a desk. A nil c starts a clock at t0. wrap wraps the fake,
// and log gets the server log. Both can be nil.
func newDesk(t *testing.T, c *clock, wrap func(*fake.Provider) core.Provider, log *slog.Logger) *desk {
	t.Helper()
	if c == nil {
		c = &clock{now: t0}
	}
	if wrap == nil {
		wrap = func(p *fake.Provider) core.Provider { return p }
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	prov := fake.New()
	prov.Now = c.Now
	cfg := core.DefaultConfig()
	cfg.InventoryTTL = 0
	d := &desk{svc: core.NewService(wrap(prov), nil, cfg, nil), hist: &audit.Memory{}, clock: c}
	d.svc.Now = c.Now
	d.svc.SetAuditor(d.hist)
	d.handler = New(d.svc, log, headerAuth{}, nil, d.hist).Handler()
	d.ts = httptest.NewServer(d.handler)
	t.Cleanup(d.ts.Close)
	return d
}

// ask queues the request name for who, and moves the clock on a minute, so
// the queue order is the order of the calls.
func (d *desk) ask(t *testing.T, name, who string) {
	t.Helper()
	if res, body := do(t, d.ts, "POST", "/requests", who, false, url.Values{"name": {name}}, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("submit %s = %s", name, body)
	}
	d.clock.Add(time.Minute)
}

// refs returns the refs that the queue of the approver lead@example.com
// shows for names.
func (d *desk) refs(t *testing.T, names ...string) []core.RequestRef {
	t.Helper()
	_, home := doAs(t, d.ts, "GET", "/", "lead@example.com", false, true, nil, false)
	var out []core.RequestRef
	for _, name := range names {
		out = append(out, refIn(t, home, name))
	}
	return out
}

// pending reports whether the request name still waits.
func (d *desk) pending(t *testing.T, name string) bool {
	t.Helper()
	_, err := d.svc.GetRequest(context.Background(), name)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		t.Fatal(err)
	}
	return err == nil
}

// decide posts the decision action as user, an approver when approver is
// true.
func (d *desk) decide(t *testing.T, user string, approver bool, action string, form url.Values, htmx bool) (*http.Response, string) {
	t.Helper()
	return doAs(t, d.ts, "POST", "/requests/decide?action="+action, user, false, approver, form, htmx)
}

// decideForm is the form of a decision on refs. An empty reason is left
// out.
func decideForm(reason string, refs ...core.RequestRef) url.Values {
	form := url.Values{}
	for _, ref := range refs {
		form.Add("req", refValue(ref))
	}
	if reason != "" {
		form.Set("reason", reason)
	}
	return form
}

// madeRefs are n refs of requests that never existed.
func madeRefs(n int) []core.RequestRef {
	var out []core.RequestRef
	for i := range n {
		out = append(out, core.RequestRef{Name: fmt.Sprintf("made-%02d", i), Version: "01MADE.v1"})
	}
	return out
}

// slowAWS stands for AWS in the decision tests. It records the name of each
// account creation. Each creation and each removal from the queue moves the
// clock on by step. fail makes each creation fail, as AWS refuses it.
type slowAWS struct {
	core.Provider
	clock *clock

	mu      sync.Mutex
	step    time.Duration
	fail    bool
	created []string
}

func (p *slowAWS) wrap(f *fake.Provider) core.Provider {
	p.Provider = f
	return p
}

func (p *slowAWS) set(step time.Duration, fail bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.step, p.fail = step, fail
}

func (p *slowAWS) creations() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.created)
}

func (p *slowAWS) RequestAccount(ctx context.Context, req core.AccountRequest) (string, error) {
	p.mu.Lock()
	p.created = append(p.created, req.Name)
	step, fail := p.step, p.fail
	p.mu.Unlock()
	p.clock.Add(step)
	if fail {
		return "", errors.New(outageText)
	}
	return p.Provider.RequestAccount(ctx, req)
}

func (p *slowAWS) RemoveOUTags(ctx context.Context, keys []string) error {
	p.mu.Lock()
	step := p.step
	p.mu.Unlock()
	p.clock.Add(step)
	return p.Provider.RemoveOUTags(ctx, keys)
}

// The decide route takes the requests in queue order, oldest first, in
// whatever order the form lists them.
func TestDecideRunsTheQueueInOrder(t *testing.T) {
	c := &clock{now: t0}
	aws := &slowAWS{clock: c}
	d := newDesk(t, c, aws.wrap, nil)
	names := []string{"zeta-box", "alpha-box", "mid-box"}
	for _, name := range names {
		d.ask(t, name, name+"@example.com")
	}
	refs := d.refs(t, "mid-box", "alpha-box", "zeta-box")
	res, body := d.decide(t, "lead@example.com", true, actionApprove, decideForm("", refs...), true)
	if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("decide = %d %v\n%s", res.StatusCode, res.Header, body)
	}
	if got := aws.creations(); !slices.Equal(got, names) {
		t.Errorf("creations ran in the order %v, want the queue order %v", got, names)
	}
	if got, _ := regionIn(body); got != checkIcon+"<span>Approved 3 requests. AWS is creating 3 accounts.</span>" {
		t.Errorf("region = %q", got)
	}
	// The answer is the card, as for one row, with no refusal.
	if !queueAnswer(body) || len(undecidedIn(body)) != 0 {
		t.Errorf("the answer is not the card with an empty slot:\n%s", body)
	}
	for _, name := range names {
		if d.pending(t, name) {
			t.Errorf("%s still waits", name)
		}
	}
}

// One result never stops the rest. The answer gives each request that was
// not decided with its reason, in queue order. A request that left the
// queue goes last.
func TestDecideReportsEachResult(t *testing.T) {
	d := newDesk(t, nil, nil, nil)
	d.ask(t, "gone-box", "gone@example.com")
	d.ask(t, "stale-box", "stale@example.com")
	d.ask(t, "leads-box", "lead@example.com")
	d.ask(t, "ok-box", "ok@example.com")
	refs := d.refs(t, "ok-box", "leads-box", "stale-box", "gone-box")

	if res, body := do(t, d.ts, "POST", "/requests/gone-box/withdraw", "gone@example.com", false, refForm(refs[3]), true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("withdraw = %s", body)
	}
	_, edit := do(t, d.ts, "GET", "/requests/stale-box/edit", "ops@example.com", true, nil, true)
	saved := editFormValues(t, edit, "stale-box")
	saved.Set("budget", "61")
	if res, body := do(t, d.ts, "POST", "/requests/stale-box/edit", "ops@example.com", true, saved, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("edit = %s", body)
	}

	res, body := d.decide(t, "lead@example.com", true, actionApprove, decideForm("", refs...), true)
	if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("decide = %d %v\n%s", res.StatusCode, res.Header, body)
	}
	if got, _ := regionIn(body); got != checkIcon+"<span>Approved 1 of 4 requests. AWS is creating 1 account. The queue shows why 3 requests were not approved.</span>" {
		t.Errorf("region = %q", got)
	}
	if got, want := undecidedIn(body), []string{"stale-box", "leads-box", "gone-box"}; !slices.Equal(got, want) {
		t.Errorf("the answer lists %v, want %v:\n%s", got, want, body)
	}
	for name, want := range map[string]string{
		"stale-box": changedText("stale-box"),
		"leads-box": "Not approved. You requested it, so another approver must approve it.",
		"gone-box":  goneText("gone-box"),
		"ok-box":    "",
	} {
		if got := resultIn(body, name); got != want {
			t.Errorf("the reason for %s = %q, want %q", name, got, want)
		}
	}
	// The card shows the queue as it is now: the new budget, and no request
	// that left.
	if !strings.Contains(slipIn(body, "stale-box"), "$61") || slipIn(body, "ok-box") != "" || slipIn(body, "gone-box") != "" {
		t.Errorf("the card does not show the queue as it is now:\n%s", body)
	}
	for name, want := range map[string]bool{"ok-box": false, "stale-box": true, "leads-box": true} {
		if got := d.pending(t, name); got != want {
			t.Errorf("%s pending = %v, want %v", name, got, want)
		}
	}
}

// The rule against approving your own request holds for a denial too. The
// page shows no buttons for such a request, so only a forged post reaches
// it.
func TestDecideKeepsTheOwnRuleForDenials(t *testing.T) {
	d := newDesk(t, nil, nil, nil)
	// The admin ops asks for dana, and the admin boss asks for ops.
	for name, who := range map[string][2]string{"ops-asks": {"ops@example.com", "dana@example.com"}, "for-ops": {"boss@example.com", "ops@example.com"}} {
		if res, body := do(t, d.ts, "POST", "/requests", who[0], true, url.Values{"name": {name}, "owner": {who[1]}}, true); res.Header.Get("X-Form-Error") != "" {
			t.Fatalf("submit %s = %s", name, body)
		}
	}
	d.ask(t, "other-box", "bob@example.com")
	refs := d.refs(t, "ops-asks", "for-ops", "other-box")
	res, body := doAs(t, d.ts, "POST", "/requests/decide?action=deny", "ops@example.com", true, true, decideForm("not now", refs...), true)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("decide = %d\n%s", res.StatusCode, body)
	}
	if got, _ := regionIn(body); got != checkIcon+"<span>Denied 1 of 3 requests. The queue shows why 2 requests were not denied.</span>" {
		t.Errorf("region = %q", got)
	}
	for name, want := range map[string]string{
		"ops-asks": "Not denied. You requested it, so another approver must decide.",
		"for-ops":  "Not denied. It names you as the owner, so another approver must decide.",
	} {
		if got := resultIn(body, name); got != want {
			t.Errorf("the reason for %s = %q, want %q", name, got, want)
		}
	}
	if !d.pending(t, "ops-asks") || !d.pending(t, "for-ops") || d.pending(t, "other-box") {
		t.Error("only other-box may leave the queue")
	}
	// The single Approve button applies the rule as the decide route does.
	_, body = doAs(t, d.ts, "POST", "/requests/for-ops/approve", "ops@example.com", true, true, refForm(refs[1]), true)
	if slot := slotPart.FindStringSubmatch(body); slot == nil || slot[2] != "Not approved. It names you as the owner, so another approver must approve it." {
		t.Errorf("approving a request that names you as the owner:\n%s", body)
	}
}

// The own rule runs on the version that the viewer saw. An admin gives the
// request to the approver after the approver loaded the page. The click
// carries the old version, so it gets the changed text and the card with the
// new values, as core refuses it.
func TestDecideChecksTheOwnRuleOnTheVersionThatThePageShowed(t *testing.T) {
	d := newDesk(t, nil, nil, nil)
	d.ask(t, "box", "bob@example.com")
	old := d.refs(t, "box")[0]
	_, edit := do(t, d.ts, "GET", "/requests/box/edit", "ops@example.com", true, nil, true)
	saved := editFormValues(t, edit, "box")
	saved.Set("owner", "lead@example.com")
	if res, body := do(t, d.ts, "POST", "/requests/box/edit", "ops@example.com", true, saved, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("edit = %s", body)
	}
	for _, c := range []struct {
		path string
		form url.Values
	}{
		{"/requests/box/approve", refForm(old)},
		{"/requests/decide?action=approve", decideForm("", old)},
		{"/requests/decide?action=deny", decideForm("no", old)},
	} {
		_, body := doAs(t, d.ts, "POST", c.path, "lead@example.com", false, true, c.form, true)
		if slot := slotPart.FindStringSubmatch(body); slot == nil || slot[2] != changedText("box") {
			t.Errorf("%s with the old version gets %v, want the changed text:\n%s", c.path, slot, body)
		}
		if !strings.Contains(slipIn(body, "box"), "lead@") {
			t.Errorf("%s does not show the card with the new owner:\n%s", c.path, body)
		}
	}
	if !d.pending(t, "box") {
		t.Error("a stale click decided box")
	}
}

// After decideBudget the rest of the requests are not tried. The check runs
// before each request, so a request that started runs to its end.
func TestDecideStopsAtTheTimeBudget(t *testing.T) {
	for _, tc := range []struct {
		action, reason, region, last string
	}{
		{actionApprove, "", "Approved 3 of 4 requests. AWS is creating 3 accounts. The queue shows why 1 request was not approved.",
			"Not approved. playplace stopped before this request. Approve it again."},
		{actionDeny, "the queue is full", "Denied 3 of 4 requests. The queue shows why 1 request was not denied.",
			"Not denied. playplace stopped before this request. Deny it again."},
	} {
		t.Run(tc.action, func(t *testing.T) {
			c := &clock{now: t0}
			aws := &slowAWS{clock: c}
			d := newDesk(t, c, aws.wrap, nil)
			names := []string{"box-one", "box-two", "box-three", "box-four"}
			for _, name := range names {
				d.ask(t, name, name+"@example.com")
			}
			refs := d.refs(t, names...)
			// Each decision takes 15 seconds, so the 45 seconds are used up
			// before the fourth one.
			aws.set(15*time.Second, false)
			res, body := d.decide(t, "lead@example.com", true, tc.action, decideForm(tc.reason, refs...), true)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("decide = %d\n%s", res.StatusCode, body)
			}
			if got, _ := regionIn(body); got != checkIcon+"<span>"+tc.region+"</span>" {
				t.Errorf("region = %q", got)
			}
			if got := resultIn(body, "box-four"); got != tc.last {
				t.Errorf("the reason for box-four = %q, want %q", got, tc.last)
			}
			for i, name := range names {
				if got := d.pending(t, name); got != (i == 3) {
					t.Errorf("%s pending = %v", name, got)
				}
			}
		})
	}
}

// slowRead makes the next read of AWS take decideBudget on the clock, as a
// slow read of the queue does.
type slowRead struct {
	core.Provider
	clock *clock
	armed atomic.Bool
}

func (p *slowRead) wait() {
	if p.armed.CompareAndSwap(true, false) {
		p.clock.Add(decideBudget)
	}
}

func (p *slowRead) OUTags(ctx context.Context) (map[string]string, error) {
	p.wait()
	return p.Provider.OUTags(ctx)
}

func (p *slowRead) ListPlaygroundAccounts(ctx context.Context) ([]core.RemoteAccount, error) {
	p.wait()
	return p.Provider.ListPlaygroundAccounts(ctx)
}

// The budget counts from the start of the post, so the read of the queue
// before the first decision counts too. A read that uses up the budget
// leaves every request not tried.
func TestDecideCountsTheReadOfTheQueueInTheBudget(t *testing.T) {
	c := &clock{now: t0}
	slow := &slowRead{clock: c}
	d := newDesk(t, c, func(p *fake.Provider) core.Provider { slow.Provider = p; return slow }, nil)
	d.ask(t, "box-one", "one@example.com")
	d.ask(t, "box-two", "two@example.com")
	refs := d.refs(t, "box-one", "box-two")
	slow.armed.Store(true)
	res, body := d.decide(t, "lead@example.com", true, actionApprove, decideForm("", refs...), true)
	if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "1" {
		t.Fatalf("decide = %d %v\n%s", res.StatusCode, res.Header, body)
	}
	for _, name := range []string{"box-one", "box-two"} {
		if got := resultIn(body, name); got != notTried(actionApprove) {
			t.Errorf("the reason for %s = %q, want the not tried text", name, got)
		}
		if !d.pending(t, name) {
			t.Errorf("%s left the queue", name)
		}
	}
}

// A stop of the server waits for a decision post in flight: decideBudget
// and one more approval of up to 15 seconds. A shorter wait ends the
// process between the claim of a request and its restore.
func TestTheStopWaitsForADecisionPost(t *testing.T) {
	if shutdownDrain < decideBudget+15*time.Second {
		t.Errorf("the stop waits %s, want at least %s", shutdownDrain, decideBudget+15*time.Second)
	}
}

// The time limits of a decision work together, and a test that shortens a
// limit cannot see a change of its value. So this test pins each value, and
// a change becomes a deliberate act. Change the value, its reason and the
// docs together.
func TestTheDecisionLimitsArePinned(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want time.Duration
		why       string
	}{
		{"decideRefLimit", decideRefLimit, 2 * time.Minute, "a normal approval takes 2 to 6 seconds, so only a call that AWS never answers reaches the limit"},
		{"decideBudget", decideBudget, 45 * time.Second, "a common proxy cuts an answer at 60 seconds, and the last approval and the answer need the other 15 seconds"},
		{"shutdownDrain", shutdownDrain, 60 * time.Second, "a stop waits for decideBudget and one more approval of up to 15 seconds"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %s, want %s, because %s", c.name, c.got, c.want, c.why)
		}
	}
}

// heldApproval holds each account creation until release closes, as a slow
// call to AWS does. started receives a value when a creation begins.
type heldApproval struct {
	core.Provider
	started chan struct{}
	release chan struct{}
}

func (p *heldApproval) RequestAccount(ctx context.Context, req core.AccountRequest) (string, error) {
	select {
	case p.started <- struct{}{}:
	default:
	}
	<-p.release
	return p.Provider.RequestAccount(ctx, req)
}

// A Slack click answers Slack at once and decides after the answer, so the
// stop of the HTTP server does not wait for it. The stop of the server must
// still wait for the approval. A cut approval can leave a request claimed
// with no account.
func TestTheStopWaitsForASlackApproval(t *testing.T) {
	held := &heldApproval{Provider: fake.New(), started: make(chan struct{}, 1), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(held.release) })
	t.Cleanup(release)
	cfg := core.DefaultConfig()
	cfg.InventoryTTL = 0
	svc := core.NewService(held, nil, cfg, nil)
	q, err := svc.SubmitRequest(context.Background(), core.RequestInput{Name: "held-box", Owner: "anna@example.com"}, "web")
	if err != nil {
		t.Fatal(err)
	}
	stub := &slackStub{responses: make(chan string, 1)}
	sl := &Slack{SigningSecret: "s3cret", Client: &http.Client{Transport: stub}, IsApprover: func(string) bool { return true }}
	srv := New(svc, slog.New(slog.DiscardHandler), headerAuth{}, sl, nil)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- srv.ListenAndServe(ctx, addr) }()
	base := "http://" + addr
	// An open connection with no request counts as busy for 5 seconds when
	// the server stops, so each call has its own.
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	for start := time.Now(); ; {
		if res, err := client.Get(base + "/healthz"); err == nil {
			_ = res.Body.Close()
			break
		}
		if time.Since(start) > 10*time.Second {
			t.Fatal("the server did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}

	res, err := client.Do(slackClickRequest(base, sl, "approve", refValue(q.Ref())))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("interact = %d", res.StatusCode)
	}
	select {
	case <-held.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the click did not start the approval")
	}
	cancel()
	select {
	case err := <-stopped:
		t.Fatalf("the server stopped with a Slack approval in flight: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	release()
	select {
	case text := <-stub.responses:
		if !strings.Contains(text, "approved `held-box`") {
			t.Errorf("the Slack reply = %q", text)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no Slack reply")
	}
	select {
	case err := <-stopped:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the stop = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the server did not stop after the approval")
	}
}

// After the first failed call to AWS the rest are not tried, because each
// one repeats the outage. The answer never shows the raw error, and the log
// holds it.
func TestDecideStopsAfterTheFirstAWSFailure(t *testing.T) {
	logs := &logBuffer{}
	c := &clock{now: t0}
	aws := &slowAWS{clock: c}
	d := newDesk(t, c, aws.wrap, slog.New(slog.NewTextHandler(logs, nil)))
	names := []string{"box-one", "box-two", "box-three"}
	for _, name := range names {
		d.ask(t, name, name+"@example.com")
	}
	refs := d.refs(t, names...)
	aws.set(0, true)
	res, body := d.decide(t, "lead@example.com", true, actionApprove, decideForm("", refs...), true)
	// Nothing went through, so the answer is a refusal.
	if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "1" || res.Header.Get("HX-Reswap") != "none" {
		t.Fatalf("decide = %d %v\n%s", res.StatusCode, res.Header, body)
	}
	if got, _ := regionIn(body); got != alertIcon+"<span>No requests were approved. The queue shows why.</span>" {
		t.Errorf("region = %q", got)
	}
	if got := aws.creations(); !slices.Equal(got, []string{"box-one"}) {
		t.Errorf("creations = %v, want box-one alone", got)
	}
	notTried := "Not approved. playplace stopped before this request. Approve it again."
	for name, want := range map[string]string{"box-one": awsFailed, "box-two": notTried, "box-three": notTried} {
		if got := resultIn(body, name); got != want {
			t.Errorf("the reason for %s = %q, want %q", name, got, want)
		}
		if !d.pending(t, name) {
			t.Errorf("%s left the queue", name)
		}
	}
	if holdsRequestID(body) {
		t.Errorf("the answer shows the raw AWS error:\n%s", body)
	}
	if !logs.warned("/requests/decide") {
		t.Errorf("the log has no warning with the raw error:\n%s", logs.String())
	}
}

// leftStore stands for AWS with an identity store that the owner
// gone@example.com left after the request arrived. Access is off until the
// test sets on, so the requests can arrive first.
type leftStore struct {
	core.Provider
	on atomic.Bool
}

func (p *leftStore) AccessEnabled() bool { return p.on.Load() }

func (p *leftStore) LookupUser(_ context.Context, identity string) (core.User, error) {
	if identity == "gone@example.com" {
		return core.User{}, core.ErrUserNotFound
	}
	return core.User{ID: identity, UserName: identity, Email: identity}, nil
}

// A refusal of core that is not a failed call to AWS stops only its own
// request, so playplace still decides the next request. The reason says the
// refusal of core as sentences.
func TestDecideGoesOnAfterARefusalOfCore(t *testing.T) {
	store := &leftStore{}
	d := newDesk(t, nil, func(p *fake.Provider) core.Provider { store.Provider = p; return store }, nil)
	d.ask(t, "gone-box", "gone@example.com")
	d.ask(t, "next-box", "next@example.com")
	refs := d.refs(t, "gone-box", "next-box")
	store.on.Store(true)
	res, body := d.decide(t, "lead@example.com", true, actionApprove, decideForm("", refs...), true)
	if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("decide = %d %v\n%s", res.StatusCode, res.Header, body)
	}
	if got, _ := regionIn(body); got != checkIcon+"<span>Approved 1 of 2 requests. AWS is creating 1 account. The queue shows why 1 request was not approved.</span>" {
		t.Errorf("region = %q", got)
	}
	if got, want := resultIn(body, "gone-box"), `Owner "gone@example.com" is not a user in the identity store. Use the engineer's email or user name.`; got != want {
		t.Errorf("the reason for gone-box = %q, want %q", got, want)
	}
	if got := resultIn(body, "next-box"); got != "" {
		t.Errorf("next-box has the reason %q, want none", got)
	}
	if !d.pending(t, "gone-box") || d.pending(t, "next-box") {
		t.Errorf("gone-box pending = %v and next-box pending = %v, want only gone-box", d.pending(t, "gone-box"), d.pending(t, "next-box"))
	}
}

// leavingCaller cancels the request of the caller at the first account
// creation, as a browser that closes does. A creation refuses a context
// that ended, or one that ends before decideRefLimit.
type leavingCaller struct {
	core.Provider

	mu    sync.Mutex
	leave context.CancelFunc
}

func (p *leavingCaller) RequestAccount(ctx context.Context, req core.AccountRequest) (string, error) {
	p.mu.Lock()
	leave := p.leave
	p.mu.Unlock()
	if leave != nil {
		leave()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if end, ok := ctx.Deadline(); ok && time.Until(end) < decideRefLimit-time.Minute {
		return "", errors.New("the decision has a deadline shorter than decideRefLimit")
	}
	return p.Provider.RequestAccount(ctx, req)
}

// A decision ignores the end of the request that sent it, and its only
// deadline is decideRefLimit, so a closed browser never cuts a batch.
func TestDecideOutlivesTheCaller(t *testing.T) {
	lc := &leavingCaller{}
	d := newDesk(t, nil, func(p *fake.Provider) core.Provider { lc.Provider = p; return lc }, nil)
	d.ask(t, "box-one", "one@example.com")
	d.ask(t, "box-two", "two@example.com")
	refs := d.refs(t, "box-one", "box-two")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lc.mu.Lock()
	lc.leave = cancel
	lc.mu.Unlock()
	req := httptest.NewRequestWithContext(ctx, "POST", "/requests/decide?action=approve", strings.NewReader(decideForm("", refs...).Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-User", "lead@example.com")
	req.Header.Set("X-Approver", "1")
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	d.handler.ServeHTTP(rec, req)
	if ctx.Err() == nil {
		t.Fatal("the caller did not leave")
	}
	// Nobody reads the answer, so only the queue tells the result.
	if d.pending(t, "box-one") || d.pending(t, "box-two") {
		t.Error("a request still waits after the caller left")
	}
}

// hungAWS never answers the first account creation. The call ends when its
// context ends, as a call of the AWS SDK does. So that a test without the
// limit fails and does not hang, the call also gives up after 5 seconds.
type hungAWS struct {
	core.Provider

	mu    sync.Mutex
	calls int
}

func (p *hungAWS) RequestAccount(ctx context.Context, req core.AccountRequest) (string, error) {
	p.mu.Lock()
	p.calls++
	first := p.calls == 1
	p.mu.Unlock()
	if !first {
		return p.Provider.RequestAccount(ctx, req)
	}
	select {
	case <-ctx.Done():
		return "", fmt.Errorf("operation error Organizations: CreateAccount, %w", ctx.Err())
	case <-time.After(5 * time.Second):
		return "", errors.New("the call hung for 5 seconds")
	}
}

// A call that AWS never answers ends after decideRefLimit. Core puts the
// request back, and the rest are not tried. The lock of core is then free,
// so the next decision runs.
func TestDecideEndsACallThatAWSNeverAnswers(t *testing.T) {
	limit := decideRefLimit
	decideRefLimit = 200 * time.Millisecond
	t.Cleanup(func() { decideRefLimit = limit })
	hung := &hungAWS{}
	d := newDesk(t, nil, func(p *fake.Provider) core.Provider { hung.Provider = p; return hung }, nil)
	d.ask(t, "box-one", "one@example.com")
	d.ask(t, "box-two", "two@example.com")
	refs := d.refs(t, "box-one", "box-two")

	start := time.Now()
	res, body := d.decide(t, "lead@example.com", true, actionApprove, decideForm("", refs...), true)
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("the decision took %s, so the limit did not end the call", took)
	}
	if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "1" {
		t.Fatalf("decide = %d %v\n%s", res.StatusCode, res.Header, body)
	}
	for name, want := range map[string]string{"box-one": awsSlow, "box-two": notTried(actionApprove)} {
		if got := resultIn(body, name); got != want {
			t.Errorf("the reason for %s = %q, want %q", name, got, want)
		}
		if !d.pending(t, name) {
			t.Errorf("%s left the queue", name)
		}
	}

	decideRefLimit = limit
	res, body = d.decide(t, "lead@example.com", true, actionApprove, decideForm("", d.refs(t, "box-two")...), true)
	if got, _ := regionIn(body); res.StatusCode != http.StatusOK || got != checkIcon+"<span>Approved box-two. AWS is creating the account.</span>" {
		t.Errorf("the next decision = %d, region %q", res.StatusCode, got)
	}
}

// A form that playplace cannot take decides nothing. The htmx answer keeps
// its status and fills the refusal slot of the card, because app.js swaps
// such an answer. A page visit gets the styled page.
func TestDecideRefusesABadForm(t *testing.T) {
	d := newDesk(t, nil, nil, nil)
	d.ask(t, "real-box", "bob@example.com")
	real := d.refs(t, "real-box")[0]
	withValues := func(form url.Values, key string, values ...string) url.Values {
		form[key] = append(form[key], values...)
		return form
	}
	for _, c := range []struct {
		name, action string
		form         url.Values
		why          string
	}{
		{"no action", "", decideForm("", real), unknownDecision},
		{"an unknown action", "withdraw", decideForm("", real), unknownDecision},
		{"the action in the body", "", withValues(decideForm("", real), "action", actionApprove), unknownDecision},
		{"no request", actionApprove, url.Values{}, noRequests},
		{"11 approvals", actionApprove, decideForm("", append(madeRefs(10), real)...), "You can approve at most 10 requests at once, so playplace decided nothing."},
		{"51 denials", actionDeny, decideForm("full", append(madeRefs(50), real)...), "You can deny at most 50 requests at once, so playplace decided nothing."},
		{"a name with no version", actionApprove, withValues(decideForm("", real), "req", "made-00"), unreadableRef},
		{"an empty value", actionApprove, withValues(decideForm("", real), "req", ""), unreadableRef},
		{"two spaces", actionApprove, withValues(decideForm("", real), "req", "made-00  01MADE.v1"), unreadableRef},
		// No request in the queue has such a name, so playplace never reads
		// it from AWS.
		{"a name that is not valid", actionApprove, withValues(decideForm("", real), "req", "Bad_Name 01MADE.v1"), unreadableRef},
		{"a long name", actionDeny, withValues(decideForm("why", real), "req", strings.Repeat("a", 300)+" 01MADE.v1"), unreadableRef},
	} {
		res, body := d.decide(t, "lead@example.com", true, c.action, c.form, true)
		if res.StatusCode != http.StatusBadRequest || res.Header.Get("HX-Reswap") != "none" || res.Header.Get("X-Form-Error") != "1" {
			t.Errorf("%s = %d %v, want 400 with the refusal headers", c.name, res.StatusCode, res.Header)
			continue
		}
		slot, region := slotPart.FindStringSubmatch(body), hiddenCopy.FindStringSubmatch(body)
		if slot == nil || region == nil || slot[1] != "pending-error" || slot[2] != c.why || region[1] != c.why {
			t.Errorf("%s: want %q in pending-error and hidden in the region:\n%s", c.name, c.why, body)
		}
		if !d.pending(t, "real-box") {
			t.Fatalf("%s decided real-box", c.name)
		}
	}
	res, body := d.decide(t, "lead@example.com", true, "withdraw", decideForm("", real), false)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, `<title>Action refused · playplace</title>`) || !strings.Contains(body, note(unknownDecision, "Go back to Home and try again.")) {
		t.Errorf("a page visit = %d, want the 400 page:\n%s", res.StatusCode, body)
	}

	// A body that does not parse as a form.
	for _, htmx := range []bool{true, false} {
		req := httptest.NewRequest("POST", "/requests/decide?action=approve", strings.NewReader("req=%zz"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-User", "lead@example.com")
		req.Header.Set("X-Approver", "1")
		if htmx {
			req.Header.Set("HX-Request", "true")
		}
		rec := httptest.NewRecorder()
		d.handler.ServeHTTP(rec, req)
		body := rec.Body.String()
		if rec.Code != http.StatusBadRequest {
			t.Errorf("a body that does not parse (htmx %v) = %d, want 400", htmx, rec.Code)
			continue
		}
		if htmx {
			slot := slotPart.FindStringSubmatch(body)
			if rec.Header().Get("HX-Reswap") != "none" || slot == nil || slot[1] != "pending-error" || slot[2] != unreadableForm {
				t.Errorf("the htmx 400 lacks the refusal:\n%v\n%s", rec.Header(), body)
			}
		} else if !strings.Contains(body, note(unreadableForm, "Go back to Home and try again.")) {
			t.Errorf("the page visit is not the styled 400 page:\n%s", body)
		}
	}

	// A page visit that decides nothing of several requests names the reason
	// of each one, because the home page does not show them.
	gone := madeRefs(2)
	res, body = d.decide(t, "lead@example.com", true, actionApprove, decideForm("", gone...), false)
	want := "No requests were approved. made-00: " + goneText("made-00") + " made-01: " + goneText("made-01")
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, note(want, "Go back to Home and try again.")) {
		t.Errorf("a page visit with two refusals = %d, want the 400 page with each reason:\n%s", res.StatusCode, body)
	}

	// The limits hold at their edge: 10 approvals and 50 denials go through.
	res, body = d.decide(t, "lead@example.com", true, actionApprove, decideForm("", append(madeRefs(9), real)...), true)
	if got, _ := regionIn(body); res.StatusCode != http.StatusOK || got != checkIcon+"<span>Approved 1 of 10 requests. AWS is creating 1 account. The queue shows why 9 requests were not approved.</span>" {
		t.Errorf("10 approvals = %d, region %q", res.StatusCode, got)
	}
	d.ask(t, "deny-box", "carl@example.com")
	res, body = d.decide(t, "lead@example.com", true, actionDeny, decideForm("full", append(madeRefs(49), d.refs(t, "deny-box")...)...), true)
	if got, _ := regionIn(body); res.StatusCode != http.StatusOK || got != checkIcon+"<span>Denied 1 of 50 requests. The queue shows why 49 requests were not denied.</span>" {
		t.Errorf("50 denials = %d, region %q", res.StatusCode, got)
	}
	if got := resultIn(body, "made-00"); got != goneText("made-00") {
		t.Errorf("the reason for a request that never existed = %q", got)
	}
}

// A body over the cap of 256 KB answers 413 and decides nothing, on the
// decide route and on the single buttons. A body under it is read.
func TestDecideRefusesALargeBody(t *testing.T) {
	d := newDesk(t, nil, nil, nil)
	d.ask(t, "real-box", "bob@example.com")
	real := d.refs(t, "real-box")[0]
	res, body := d.decide(t, "lead@example.com", true, actionDeny, decideForm(strings.Repeat("x", 250<<10), real), true)
	if slot := slotPart.FindStringSubmatch(body); res.StatusCode != http.StatusOK || slot == nil || slot[2] != reasonTooLong {
		t.Errorf("a body of 250 KB = %d, want the refusal of the long reason:\n%.400s", res.StatusCode, body)
	}
	huge := strings.Repeat("x", 260<<10)
	for _, c := range []struct {
		path string
		form url.Values
	}{
		{"/requests/decide?action=deny", decideForm(huge, real)},
		{"/requests/real-box/deny", url.Values{"version": {real.Version}, "reason": {huge}}},
	} {
		for _, htmx := range []bool{true, false} {
			req := httptest.NewRequest("POST", c.path, strings.NewReader(c.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("X-User", "lead@example.com")
			req.Header.Set("X-Approver", "1")
			if htmx {
				req.Header.Set("HX-Request", "true")
			}
			rec := httptest.NewRecorder()
			d.handler.ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Errorf("%s (htmx %v) = %d, want 413", c.path, htmx, rec.Code)
				continue
			}
			if htmx {
				slot, region := slotPart.FindStringSubmatch(body), hiddenCopy.FindStringSubmatch(body)
				if rec.Header().Get("HX-Reswap") != "none" || slot == nil || region == nil || slot[2] != formTooLarge || region[1] != formTooLarge {
					t.Errorf("%s: the htmx 413 lacks the refusal:\n%v\n%s", c.path, rec.Header(), body)
				}
			} else if !strings.Contains(body, note(formTooLarge, "Go back to Home and try again.")) || !strings.Contains(body, `<span class="muted">413</span>`) {
				t.Errorf("%s: the page visit is not the styled 413 page:\n%s", c.path, body)
			}
		}
	}
	if !d.pending(t, "real-box") {
		t.Error("a body over the cap decided real-box")
	}
}

// A form that names a request twice decides it once, with the first ref.
func TestDecideDropsDuplicateRequests(t *testing.T) {
	d := newDesk(t, nil, nil, nil)
	d.ask(t, "dup-box", "bob@example.com")
	d.ask(t, "other-box", "carl@example.com")
	refs := d.refs(t, "dup-box", "other-box")
	old := func(name string) core.RequestRef { return core.RequestRef{Name: name, Version: "01OLD.v1"} }

	res, body := d.decide(t, "lead@example.com", true, actionApprove, decideForm("", refs[0], old("dup-box"), refs[0]), true)
	if got, _ := regionIn(body); res.StatusCode != http.StatusOK || got != checkIcon+"<span>Approved dup-box. AWS is creating the account.</span>" {
		t.Errorf("the current ref first = %d, region %q", res.StatusCode, got)
	}
	// The first ref of other-box is stale, so the current one after it never
	// counts.
	res, body = d.decide(t, "lead@example.com", true, actionApprove, decideForm("", old("other-box"), refs[1]), true)
	if slot := slotPart.FindStringSubmatch(body); res.Header.Get("X-Form-Error") != "1" || slot == nil || slot[2] != changedText("other-box") {
		t.Errorf("the stale ref first = %v:\n%s", res.Header, body)
	}
	if d.pending(t, "dup-box") || !d.pending(t, "other-box") {
		t.Error("want dup-box approved and other-box waiting")
	}
}

func TestDenyReasonRule(t *testing.T) {
	for raw, want := range map[string]string{
		"no budget":                         "no budget",
		"  padded  ":                        "padded",
		"two\r\n\r\nlines\tand   runs":      "two lines and runs",
		strings.Repeat("é", maxReason):      strings.Repeat("é", maxReason),
		strings.Repeat("a", 249) + "\n\n b": strings.Repeat("a", 249) + " b",
	} {
		if got, err := denyReason(raw); err != nil || got != want {
			t.Errorf("denyReason(%q) = %q, %v, want %q", raw, got, err, want)
		}
	}
	for raw, want := range map[string]string{
		"":                               reasonMissing,
		" \r\n\t ":                       reasonMissing,
		strings.Repeat("é", maxReason+1): reasonTooLong,
		strings.Repeat("a", 250) + "\n\n" + strings.Repeat("b", 250): reasonTooLong,
		// A control character, a bidi override and a byte that is not UTF-8.
		"\x1b[31mred":       reasonCharacters,
		"no\x00budget":      reasonCharacters,
		"a\u202eb":          reasonCharacters,
		"over \u2066budget": reasonCharacters,
		"bad \xff byte":     reasonCharacters,
	} {
		_, err := denyReason(raw)
		var fe fieldError
		if !errors.As(err, &fe) || fe.field != "reason" || refusal(err, "") != want {
			t.Errorf("denyReason(%q) = %v, want the reason field refused with %q", raw, err, want)
		}
	}
}

// A denial in the web needs a reason. The refusal names the field, and the
// checks run in order: the role, the reason, then core. The reason reaches
// core with its spaces folded.
func TestDenialsNeedAReason(t *testing.T) {
	d := newDesk(t, nil, nil, nil)
	d.ask(t, "deny-one", "bob@example.com")
	d.ask(t, "deny-two", "carl@example.com")
	refs := d.refs(t, "deny-one", "deny-two")
	long := refForm(refs[0])
	long.Set("reason", strings.Repeat("x", maxReason+1))
	for _, c := range []struct {
		name, path string
		form       url.Values
		why        string
	}{
		{"decide with no reason", "/requests/decide?action=deny", decideForm("", refs...), reasonMissing},
		{"decide with spaces", "/requests/decide?action=deny", decideForm(" \n ", refs...), reasonMissing},
		{"a button with no reason", "/requests/deny-one/deny", refForm(refs[0]), reasonMissing},
		{"a button with a long reason", "/requests/deny-one/deny", long, reasonTooLong},
	} {
		res, body := doAs(t, d.ts, "POST", c.path, "lead@example.com", false, true, c.form, true)
		if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "1" || res.Header.Get("HX-Reswap") != "none" {
			t.Errorf("%s = %d %v, want the refusal headers", c.name, res.StatusCode, res.Header)
			continue
		}
		if f, s := res.Header.Get(invalidFieldHeader), res.Header.Get(invalidSlotHeader); f != "reason" || s != "pending-error" {
			t.Errorf("%s names field %q in slot %q, want reason in pending-error", c.name, f, s)
		}
		if slot := slotPart.FindStringSubmatch(body); slot == nil || slot[2] != c.why {
			t.Errorf("%s: want %q in the slot:\n%s", c.name, c.why, body)
		}
	}
	// An engineer gets the role refusal, not the reason refusal.
	if res, _ := do(t, d.ts, "POST", "/requests/decide?action=deny", "anna@example.com", false, decideForm("", refs...), true); res.StatusCode != http.StatusForbidden {
		t.Errorf("an engineer with no reason = %d, want 403", res.StatusCode)
	}
	// A stale denial with no reason gets the reason refusal, because core
	// runs last.
	_, edit := do(t, d.ts, "GET", "/requests/deny-one/edit", "ops@example.com", true, nil, true)
	saved := editFormValues(t, edit, "deny-one")
	saved.Set("budget", "61")
	if res, body := do(t, d.ts, "POST", "/requests/deny-one/edit", "ops@example.com", true, saved, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("edit = %s", body)
	}
	_, body := doAs(t, d.ts, "POST", "/requests/deny-one/deny", "lead@example.com", false, true, refForm(refs[0]), true)
	if slot := slotPart.FindStringSubmatch(body); slot == nil || slot[2] != reasonMissing {
		t.Errorf("a stale denial with no reason:\n%s", body)
	}
	if !d.pending(t, "deny-one") || !d.pending(t, "deny-two") {
		t.Fatal("a refused denial ended a request")
	}

	// The decide route and the button fold the reason. The button reads the
	// prompt header when its form has no reason.
	fresh := d.refs(t, "deny-one", "deny-two")
	res, body := d.decide(t, "lead@example.com", true, actionDeny, decideForm("no\r\n\r\nbudget   left", fresh[0]), true)
	if got, _ := regionIn(body); res.StatusCode != http.StatusOK || got != checkIcon+"<span>Denied deny-one.</span>" {
		t.Errorf("decide = %d, region %q", res.StatusCode, got)
	}
	req, _ := http.NewRequest("POST", d.ts.URL+"/requests/deny-two/deny", strings.NewReader(refForm(fresh[1]).Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-User", "lead@example.com")
	req.Header.Set("X-Approver", "1")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Prompt", "  too many\tboxes ")
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("deny with the prompt = %v %v", res, err)
	} else if err := res.Body.Close(); err != nil {
		t.Fatal(err)
	}
	denied, err := d.hist.Query(context.Background(), audit.Filter{Event: "denied"})
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]string{}
	for _, e := range denied {
		reasons[e.Account] = e.Details["reason"]
	}
	if reasons["deny-one"] != "no budget left" || reasons["deny-two"] != "too many boxes" {
		t.Errorf("history reasons = %v", reasons)
	}
}

// htmx sends the answer of a prompt as Latin-1 bytes when every character
// fits in one byte. It sends any other answer as encodeURIComponent writes
// it, with a second header. Both read back as the approver typed them.
func TestPromptAnswerReadsWhatHtmxSends(t *testing.T) {
	for _, c := range []struct {
		value   string
		encoded bool
		want    string
	}{
		{"no budget", false, "no budget"},
		{"caf\xe9 \xfcber se\xf1or Stra\xdfe", false, "café über señor Straße"},
		// A header without the mark is never unescaped.
		{"100%25 full", false, "100%25 full"},
		{"%D0%BD%D0%B5%D1%82%20%F0%9F%93%A6", true, "нет 📦"},
		// encodeURIComponent writes a space as %20, so a plus stays a plus.
		{"a+b%20c", true, "a+b c"},
	} {
		h := http.Header{}
		h.Set("HX-Prompt", c.value)
		if c.encoded {
			h.Set("HX-Prompt-URI-AutoEncoded", "true")
		}
		if got, err := promptAnswer(h); err != nil || got != c.want {
			t.Errorf("promptAnswer(%q, encoded %v) = %q, %v, want %q", c.value, c.encoded, got, err, c.want)
		}
	}
	for _, bad := range []string{"%zz", "cut %E2%8"} {
		h := http.Header{}
		h.Set("HX-Prompt", bad)
		h.Set("HX-Prompt-URI-AutoEncoded", "true")
		_, err := promptAnswer(h)
		var fe fieldError
		if !errors.As(err, &fe) || fe.field != "reason" || refusal(err, "") != reasonCharacters {
			t.Errorf("promptAnswer(%q) = %v, want the reason field refused with %q", bad, err, reasonCharacters)
		}
	}
}

// The Deny button of a row sends its reason in the prompt header. A reason
// with accents and a reason with other scripts reach history as the
// approver typed them. A bad escape is refused and denies nothing.
func TestDenyReadsThePromptAsHtmxSendsIt(t *testing.T) {
	d := newDesk(t, nil, nil, nil)
	d.ask(t, "latin-box", "bob@example.com")
	d.ask(t, "other-box", "carl@example.com")
	d.ask(t, "broken-box", "dana@example.com")
	refs := d.refs(t, "latin-box", "other-box", "broken-box")
	deny := func(ref core.RequestRef, value string, encoded bool) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequest("POST", d.ts.URL+"/requests/"+ref.Name+"/deny", strings.NewReader(refForm(ref).Encode()))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-User", "lead@example.com")
		req.Header.Set("X-Approver", "1")
		req.Header.Set("HX-Request", "true")
		req.Header.Set("HX-Prompt", value)
		if encoded {
			req.Header.Set("HX-Prompt-URI-AutoEncoded", "true")
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return res, string(body)
	}
	if res, body := deny(refs[0], "Gr\xfc\xdfe, se\xf1or. No caf\xe9 budget", false); res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("deny with Latin-1 bytes = %d %v:\n%s", res.StatusCode, res.Header, body)
	}
	if res, body := deny(refs[1], "%D0%BD%D0%B5%D1%82%20%F0%9F%93%A6", true); res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("deny with an encoded reason = %d %v:\n%s", res.StatusCode, res.Header, body)
	}
	// A bad escape and an escape that is not UTF-8 get the field refusal.
	for _, bad := range []string{"%zz", "%E2%82"} {
		res, body := deny(refs[2], bad, true)
		if res.Header.Get("X-Form-Error") != "1" || res.Header.Get(invalidFieldHeader) != "reason" {
			t.Errorf("deny with %q = %v, want the reason field refused", bad, res.Header)
		}
		if slot := slotPart.FindStringSubmatch(body); slot == nil || slot[2] != reasonCharacters {
			t.Errorf("deny with %q: want %q in the slot:\n%s", bad, reasonCharacters, body)
		}
	}
	if !d.pending(t, "broken-box") {
		t.Fatal("a refused prompt denied the request")
	}
	denied, err := d.hist.Query(context.Background(), audit.Filter{Event: "denied"})
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]string{}
	for _, e := range denied {
		reasons[e.Account] = e.Details["reason"]
	}
	if reasons["latin-box"] != "Grüße, señor. No café budget" || reasons["other-box"] != "нет 📦" {
		t.Errorf("history reasons = %q", reasons)
	}
}

func TestDecisionSentences(t *testing.T) {
	// results reads each word as a request: a leading + went through, and a
	// leading - did not.
	results := func(words ...string) []decision {
		var out []decision
		for _, w := range words {
			d := decision{Ref: core.RequestRef{Name: w[1:]}, Kind: decisionGone}
			if w[0] == '+' {
				d.Kind = decidedApproved
			}
			out = append(out, d)
		}
		return out
	}
	for _, c := range []struct {
		action string
		words  []string
		want   string
	}{
		{actionApprove, []string{"+a"}, "Approved a. AWS is creating the account."},
		{actionApprove, []string{"+a", "+b", "+c"}, "Approved 3 requests. AWS is creating 3 accounts."},
		{actionApprove, []string{"+a", "+b", "-c"}, "Approved 2 of 3 requests. AWS is creating 2 accounts. The queue shows why 1 request was not approved."},
		{actionApprove, []string{"+a", "-b", "-c"}, "Approved 1 of 3 requests. AWS is creating 1 account. The queue shows why 2 requests were not approved."},
		{actionApprove, []string{"-a", "-b"}, "No requests were approved. The queue shows why."},
		{actionDeny, []string{"+a"}, "Denied a."},
		{actionDeny, []string{"+a", "+b", "+c"}, "Denied 3 requests."},
		{actionDeny, []string{"+a", "+b", "-c"}, "Denied 2 of 3 requests. The queue shows why 1 request was not denied."},
		{actionDeny, []string{"+a", "-b", "-c"}, "Denied 1 of 3 requests. The queue shows why 2 requests were not denied."},
		{actionDeny, []string{"-a", "-b"}, "No requests were denied. The queue shows why."},
	} {
		rs := results(c.words...)
		if c.action == actionDeny {
			for i := range rs {
				if rs[i].Kind == decidedApproved {
					rs[i].Kind = decidedDenied
				}
			}
		}
		if got := decisionSentence(c.action, rs); got != c.want {
			t.Errorf("%s %v = %q, want %q", c.action, c.words, got, c.want)
		}
	}
}

// The role comes first. An engineer gets the 403 in the slot of the card,
// also for a form that playplace refuses for an approver.
func TestDecideIsForApprovers(t *testing.T) {
	d := newDesk(t, nil, nil, nil)
	d.ask(t, "real-box", "bob@example.com")
	real := d.refs(t, "real-box")[0]
	for _, action := range []string{actionApprove, actionDeny, "withdraw"} {
		res, body := d.decide(t, "anna@example.com", false, action, decideForm("why", real), true)
		if res.StatusCode != http.StatusForbidden || !strings.Contains(body, `<div id="pending-error" hx-swap-oob="innerHTML">`+alertIcon+`<span>`+approversOnly+`</span></div>`) || !hiddenCopy.MatchString(body) {
			t.Errorf("%s by an engineer = %d:\n%s", action, res.StatusCode, body)
		}
	}
	res, body := d.decide(t, "anna@example.com", false, actionApprove, decideForm("", real), false)
	if res.StatusCode != http.StatusForbidden || !strings.Contains(body, note(sentence(approversOnly), "Go back to Home.")) {
		t.Errorf("a page visit by an engineer = %d:\n%s", res.StatusCode, body)
	}
	if !d.pending(t, "real-box") {
		t.Error("an engineer decided real-box")
	}
}

// The log has one line for each request of a decision, with its result.
func TestDecideLogsEachOutcome(t *testing.T) {
	logs := &logBuffer{}
	d := newDesk(t, nil, nil, slog.New(slog.NewTextHandler(logs, nil)))
	d.ask(t, "ok-box", "ok@example.com")
	d.ask(t, "gone-box", "gone@example.com")
	refs := d.refs(t, "ok-box", "gone-box")
	if res, body := do(t, d.ts, "POST", "/requests/gone-box/withdraw", "gone@example.com", false, refForm(refs[1]), true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("withdraw = %s", body)
	}
	d.decide(t, "lead@example.com", true, actionApprove, decideForm("", refs...), true)
	for i, kind := range []string{"approved", "gone"} {
		want := fmt.Sprintf("level=INFO msg=decision action=approve name=%s version=%s result=%s ", refs[i].Name, refs[i].Version, kind)
		if !strings.Contains(logs.String(), want) {
			t.Errorf("the log lacks %q:\n%s", want, logs.String())
		}
	}
}

// The account list can fail after the decisions. The answer still holds
// every result, and the accounts section shows its failed panel.
func TestDecideAnswersWhenTheListFails(t *testing.T) {
	flaky := &flakyList{}
	d := newDesk(t, nil, func(p *fake.Provider) core.Provider { flaky.Provider = p; return flaky }, nil)
	d.ask(t, "deny-one", "one@example.com")
	d.ask(t, "deny-two", "two@example.com")
	d.ask(t, "gone-box", "gone@example.com")
	refs := d.refs(t, "deny-one", "deny-two", "gone-box")
	if res, body := do(t, d.ts, "POST", "/requests/gone-box/withdraw", "gone@example.com", false, refForm(refs[2]), true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("withdraw = %s", body)
	}
	flaky.fail.Store(true)
	res, body := d.decide(t, "lead@example.com", true, actionDeny, decideForm("no", refs...), true)
	flaky.fail.Store(false)
	if res.StatusCode != http.StatusOK || res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("decide = %d %v\n%s", res.StatusCode, res.Header, body)
	}
	if got, _ := regionIn(body); got != checkIcon+"<span>Denied 2 of 3 requests. The queue shows why 1 request was not denied.</span>" {
		t.Errorf("region = %q", got)
	}
	if got := resultIn(body, "gone-box"); got != goneText("gone-box") {
		t.Errorf("the reason for gone-box = %q", got)
	}
	if !strings.Contains(body, `<div id="accounts-error" class="refusal">`) || holdsRequestID(body) {
		t.Errorf("the accounts section is not the failed panel:\n%s", body)
	}
	if d.pending(t, "deny-one") || d.pending(t, "deny-two") {
		t.Error("a denied request still waits")
	}
}

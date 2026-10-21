package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

// apiError stands for smithy.APIError of the AWS SDK: an error with a code.
type apiError struct{ code string }

func (e apiError) Error() string     { return "api error " + e.code + ": no, RequestID: 9c2e" }
func (e apiError) ErrorCode() string { return e.code }

// timeoutError stands for a network time-out, as net.Error reports one.
type timeoutError struct{}

func (timeoutError) Error() string { return "dial tcp 10.0.0.1:443: i/o timeout" }
func (timeoutError) Timeout() bool { return true }

func TestRefusalText(t *testing.T) {
	aws := errors.New("https response error StatusCode: 403, RequestID: 5d1b, api error AccessDeniedException: denied")
	failed := func(err error) error { return &core.ProviderError{Op: "read request queue", Err: err} }
	self := func(approver string) error {
		return core.CheckApprover(core.Request{Name: "box", Owner: "bob@example.com", RequestedBy: "anna@example.com"}, approver, false)
	}
	changed := fmt.Errorf("%w: the request for box was edited after you loaded it; review the new values before you act", core.ErrRequestChanged)
	gone := fmt.Errorf("%w: no pending request named box", core.ErrNotFound)
	const (
		changedText = "box changed after you loaded the page. Read the new values, then try again."
		goneText    = "box is no longer waiting. Someone else decided it, the owner withdrew it, or playplace dropped it."
	)
	for _, c := range []struct {
		name    string
		err     error
		request string
		want    string
	}{
		{"changed", changed, "box", changedText},
		{"requested", self("anna@example.com"), "box", "You requested box, so you cannot approve it. Another approver must approve it."},
		{"the owner", self("bob@example.com"), "box", "box names you as the owner, so you cannot approve it. Another approver must approve it."},
		{"gone", gone, "box", goneText},
		{"an account not found", fmt.Errorf("%w: 123456789012", core.ErrNotFound), "", "Not found: 123456789012."},
		{"aws", failed(aws), "box", awsFailed},
		{"aws in a join", errors.Join(errors.New("account x is SUSPENDED"), failed(aws)), "", awsFailed},
		{"aws with no Op", fmt.Errorf("budget for x: %w", &core.ProviderError{Err: aws}), "", awsFailed},
		{"throttle", failed(apiError{"ThrottlingException"}), "box", awsSlow},
		{"too many requests", failed(apiError{"TooManyRequestsException"}), "", awsSlow},
		{"request time-out", failed(apiError{"RequestTimeout"}), "", awsSlow},
		{"another code", failed(apiError{"AccessDeniedException"}), "", awsFailed},
		{"deadline", failed(fmt.Errorf("send: %w", context.DeadlineExceeded)), "", awsSlow},
		{"network time-out", failed(fmt.Errorf("send: %w", timeoutError{})), "", awsSlow},
		// A time limit of playplace can end a call where core does not mark
		// the error.
		{"a deadline that core did not mark", fmt.Errorf("claim request: %w", context.DeadlineExceeded), "box", awsSlow},
		{"an AWS error that core did not mark", fmt.Errorf("tag x: %w", apiError{"AccessDeniedException"}), "", awsFailed},
		{"a throttle that core did not mark", apiError{"ThrottlingException"}, "", awsSlow},
		// The checks run in order: a failed call that wraps a sentinel reads
		// as the sentinel.
		{"aws that wraps gone", failed(gone), "box", goneText},
		{"aws that wraps a change", failed(changed), "box", changedText},
		{"aws that wraps a self-approval", failed(self("anna@example.com")), "box", "You requested box, so you cannot approve it. Another approver must approve it."},
		{"aws that wraps a limit", failed(core.OwnerLimitError{Owner: "anna@example.com", Held: 1, Limit: 1}), "", "anna@example.com already holds 1 open account or request. The limit is 1."},
		// Without a request name, a sentinel keeps the words of core.
		{"gone with no name", gone, "", "Not found: no pending request named box."},
		// The limits keep the letters of the name and the email.
		{"the lifetime limit", core.LifetimeLimitError{Name: "box", Lifetime: 100 * 24 * time.Hour, Max: 90 * 24 * time.Hour, Latest: time.Date(2026, 12, 14, 12, 0, 0, 0, time.UTC)}, "",
			"box can live at most 90 days. The latest expiry is 2026-12-14 12:00 UTC. An admin can override the limit."},
		{"the per-owner limit", core.OwnerLimitError{Owner: "anna@example.com", Held: 1, Limit: 1}, "",
			"anna@example.com already holds 1 open account or request. The limit is 1."},
		{"the per-owner limit of 3", fmt.Errorf("edit box: %w", core.OwnerLimitError{Owner: "anna@example.com", Held: 3, Limit: 3}), "",
			"anna@example.com already holds 3 open accounts or requests. The limit is 3."},
		{"a core refusal", errors.New("lifetime 100 days exceeds the maximum of 90 days; an admin can override"), "",
			"Lifetime 100 days exceeds the maximum of 90 days. An admin can override."},
		{"a field", fieldError{"budget", errors.New("budget must be a positive number")}, "", "Budget must be a positive number."},
		{"joined refusals", errors.Join(errors.New("account x is SUSPENDED in the organization root"), errors.New("closure of y unconfirmed; charges may continue")), "",
			"Account x is SUSPENDED in the organization root. Closure of y unconfirmed. Charges may continue."},
		{"sentences already", errors.New("budget for x: AWS reported RESET_FAILURE. Playplace sent the operation again"), "",
			"Budget for x: AWS reported RESET_FAILURE. Playplace sent the operation again."},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := refusal(c.err, c.request); got != c.want {
				t.Errorf("refusal = %q, want %q", got, c.want)
			}
		})
	}
}

// logBuffer collects the server log. Each handler writes it on its own
// goroutine, so it locks.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// warned reports whether the log has a warning for path that holds the raw
// error, with its AWS request ID.
func (b *logBuffer) warned(path string) bool {
	for line := range strings.SplitSeq(b.String(), "\n") {
		if strings.Contains(line, "level=WARN") && strings.Contains(line, "path="+path+" ") && strings.Contains(line, "RequestID: 5d1b7c2e") {
			return true
		}
	}
	return false
}

// outageText is the text of an error of the AWS SDK. It holds an AWS request
// ID in both of the forms that the SDKs write.
const outageText = "operation error Organizations: ListAccountsForParent, https response error StatusCode: 400, RequestID: 5d1b7c2e, api error AccessDeniedException: denied, request id: 5d1b7c2e"

// outage fails every call that reads, or every call that writes, as an AWS
// outage or a missing permission does.
type outage struct {
	core.Provider
	reads, writes atomic.Bool
}

func (o *outage) read() error {
	if o.reads.Load() {
		return errors.New(outageText)
	}
	return nil
}

func (o *outage) write() error {
	if o.writes.Load() {
		return errors.New(outageText)
	}
	return nil
}

func (o *outage) OUTags(ctx context.Context) (map[string]string, error) {
	if err := o.read(); err != nil {
		return nil, err
	}
	return o.Provider.OUTags(ctx)
}

func (o *outage) ListPlaygroundAccounts(ctx context.Context) ([]core.RemoteAccount, error) {
	if err := o.read(); err != nil {
		return nil, err
	}
	return o.Provider.ListPlaygroundAccounts(ctx)
}

func (o *outage) SetOUTags(ctx context.Context, tags map[string]string) error {
	if err := o.write(); err != nil {
		return err
	}
	return o.Provider.SetOUTags(ctx, tags)
}

func (o *outage) RemoveOUTags(ctx context.Context, keys []string) error {
	if err := o.write(); err != nil {
		return err
	}
	return o.Provider.RemoveOUTags(ctx, keys)
}

func (o *outage) SetTags(ctx context.Context, id string, tags map[string]string) error {
	if err := o.write(); err != nil {
		return err
	}
	return o.Provider.SetTags(ctx, id, tags)
}

func (o *outage) RemoveTags(ctx context.Context, id string, keys []string) error {
	if err := o.write(); err != nil {
		return err
	}
	return o.Provider.RemoveTags(ctx, id, keys)
}

func (o *outage) RequestAccount(ctx context.Context, req core.AccountRequest) (string, error) {
	if err := o.write(); err != nil {
		return "", err
	}
	return o.Provider.RequestAccount(ctx, req)
}

func (o *outage) CloseAccount(ctx context.Context, id string) error {
	if err := o.write(); err != nil {
		return err
	}
	return o.Provider.CloseAccount(ctx, id)
}

// asAdmin sends one request as the admin ops@example.com and returns the
// body. shell sends Sync as the shell does on a page with no accounts
// section.
func asAdmin(t *testing.T, ts *httptest.Server, method, path string, form url.Values, htmx, shell bool) string {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-User", "ops@example.com")
	req.Header.Set("X-Admin", "1")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	if shell {
		req.Header.Set("HX-Target", syncFromShell)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Body.Close(); err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// holdsRequestID reports whether body shows any part of an AWS request ID.
func holdsRequestID(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "requestid") || strings.Contains(lower, "request id") || strings.Contains(body, "5d1b7c2e") || strings.Contains(body, "AccessDenied")
}

// No answer shows the raw text of a failed call to AWS, because it holds
// AWS request IDs. Each answer says that the call failed, in the same words,
// and the log holds the raw error with the path.
func TestNoPageShowsAnAWSRequestID(t *testing.T) {
	out := &outage{}
	logs := &logBuffer{}
	ts, _ := newServerLogged(t, func(p *fake.Provider) core.Provider { out.Provider = p; return out }, slog.New(slog.NewTextHandler(logs, nil)))
	// A request to act on, loaded while AWS still answers.
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true)
	_, home := do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	bobs := refIn(t, home, "bobs-box")
	ref := refForm(bobs)
	with := func(extra url.Values) url.Values {
		form := url.Values{}
		for k, v := range ref {
			form[k] = v
		}
		for k, v := range extra {
			form[k] = v
		}
		return form
	}
	for _, c := range []struct {
		method, path string
		form         url.Values
		htmx, shell  bool // shell: Sync from a page with no accounts section
		writes       bool // whether a failed write also fails the call
	}{
		{"GET", "/", nil, false, false, false},
		{"GET", "/accounts", nil, true, false, false},
		{"POST", "/requests", url.Values{"name": {"other-box"}}, true, false, true},
		{"POST", "/requests", url.Values{"name": {"other-box"}}, false, false, true},
		{"POST", "/requests/bobs-box/approve", ref, true, false, true},
		{"POST", "/requests/bobs-box/approve", ref, false, false, true},
		{"POST", "/requests/bobs-box/deny", with(url.Values{"reason": {"no"}}), true, false, true},
		{"POST", "/requests/decide?action=approve", decideForm("", bobs), true, false, true},
		{"POST", "/requests/decide?action=deny", decideForm("no", bobs), true, false, true},
		{"POST", "/requests/decide?action=deny", decideForm("no", bobs), false, false, true},
		{"POST", "/requests/bobs-box/withdraw", ref, true, false, true},
		{"GET", "/requests/bobs-box/edit", nil, true, false, false},
		{"POST", "/requests/bobs-box/edit", with(url.Values{"owner": {"bob@example.com"}, "budget": {"70"}}), true, false, true},
		{"POST", "/refresh", nil, true, false, true},
		{"POST", "/refresh", nil, true, true, true},
		{"POST", "/refresh", nil, false, false, true},
		{"POST", "/accounts/111111111111/extend", nil, true, false, true},
		{"POST", "/accounts/111111111111/close", nil, true, false, true},
		{"POST", "/accounts/111111111111/budget", url.Values{"budget": {"80"}, "reason": {"more"}}, true, false, true},
		{"GET", "/accounts/111111111111", nil, false, false, false},
	} {
		for _, mode := range []struct {
			name string
			on   *atomic.Bool
		}{{"reads", &out.reads}, {"writes", &out.writes}} {
			mode.on.Store(true)
			body := asAdmin(t, ts, c.method, c.path, c.form, c.htmx, c.shell)
			mode.on.Store(false)
			// A full page reports only its main part, after the icon sprite.
			_, shown, _ := strings.Cut(body, "<main>")
			if shown == "" {
				shown = body
			}
			if holdsRequestID(body) {
				t.Errorf("%s %s (htmx %v, %s fail) shows the raw AWS error:\n%s", c.method, c.path, c.htmx, mode.name, shown)
			}
			// The failed panel of the account list has its own words, with the
			// same start (listFailed).
			if (mode.name == "reads" || c.writes) && !strings.Contains(body, awsFailed) && !strings.Contains(body, listFailed) {
				t.Errorf("%s %s (htmx %v, %s fail) does not say that the call to AWS failed:\n%s", c.method, c.path, c.htmx, mode.name, shown)
			}
		}
	}
	for _, path := range []string{"/", "/accounts", "/requests/bobs-box/approve", "/refresh", "/accounts/111111111111/extend"} {
		if !logs.warned(path) {
			t.Errorf("the log has no warning for %s with the raw error:\n%s", path, logs.String())
		}
	}
}

// Core stores the raw text of a failed budget call in the budget error tag,
// cut to 150 bytes, and in the history details. No page shows it, to the
// owner or to an admin. A stored error of core keeps its words.
func TestNoPageShowsAStoredAWSError(t *testing.T) {
	raw := "describe budget: " + outageText
	tags := map[string]string{
		core.TagManaged: "true", core.TagOwner: "anna@example.com", core.TagExpires: t0.Add(72 * time.Hour).Format(time.RFC3339), core.TagBudget: "50",
		core.TagBudgetHealth: "error", core.TagBudgetChecked: t0.Format(time.RFC3339), core.TagBudgetError: base64.RawURLEncoding.EncodeToString([]byte(raw[:150])),
	}
	d := newDesk(t, nil, func(p *fake.Provider) core.Provider {
		p.Seed(core.RemoteAccount{ProviderID: "111111111111", Name: "annas-box", Email: "a@example.com", JoinedAt: t0, Tags: tags})
		return p
	}, nil)
	if err := d.hist.Record(context.Background(), core.AuditEvent{JourneyID: "account:111111111111", At: t0, Event: "budget-protection", Account: "annas-box", Owner: "anna@example.com",
		Actor: core.ActorSystem, Message: "Budget protection: error", Details: map[string]string{"action_id": "", "error": raw}}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path, user string
		admin      bool
	}{
		{"/accounts/111111111111", "anna@example.com", false},
		{"/accounts/111111111111", "ops@example.com", true},
		{"/", "anna@example.com", false},
		{"/", "ops@example.com", true},
	} {
		_, body := do(t, d.ts, "GET", c.path, c.user, c.admin, nil, false)
		if !strings.Contains(body, "annas-box") {
			t.Fatalf("GET %s as %s does not show the account:\n%s", c.path, c.user, body)
		}
		if holdsRequestID(body) {
			t.Errorf("GET %s as %s shows the stored AWS error:\n%s", c.path, c.user, body)
		}
		if !strings.Contains(body, awsFailed) {
			t.Errorf("GET %s as %s does not say that the call to AWS failed:\n%s", c.path, c.user, body)
		}
	}
	_, page := do(t, d.ts, "GET", "/accounts/111111111111", "ops@example.com", true, nil, false)
	if !strings.Contains(page, `<dt>budget error</dt><dd class="error">`+alertIcon+`<span>`+awsFailed+`</span></dd>`) || !strings.Contains(page, html.EscapeString(`"error": "`+awsFailed+`"`)) {
		t.Errorf("the account page does not show the failure text in the budget error and in the history:\n%s", page)
	}

	for text, want := range map[string]string{
		raw[:150]: awsFailed,
		"https response error StatusCode: 403, RequestID: 5d1b":                                               awsFailed,
		"budget enforcement configuration removed or changed; restore it before changing existing protection": "budget enforcement configuration removed or changed; restore it before changing existing protection",
		"": "",
	} {
		if got := storedFailure(text); got != want {
			t.Errorf("storedFailure(%q) = %q, want %q", text, got, want)
		}
	}
}

// Slack replies with the same words as the web. The log holds the raw error.
func TestSlackRepliesWithTheSafeText(t *testing.T) {
	out := &outage{}
	logs := &logBuffer{}
	ts, _, _, sl, stub := slackServerOn(t, func(p *fake.Provider) core.Provider { out.Provider = p; return out }, slog.New(slog.NewTextHandler(logs, nil)))
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"slacked"}}, true)
	stub.mu.Lock()
	posted := stub.posted[0]
	stub.mu.Unlock()

	out.writes.Store(true)
	for action, want := range map[string]string{"approve": "Not approved. " + awsFailed, "deny": "Not denied. " + awsFailed} {
		if text := slackClick(t, ts, sl, stub, action, posted); text != want {
			t.Errorf("Slack %s during an outage = %q, want %q", action, text, want)
		}
	}
	out.writes.Store(false)
	if !logs.warned("/slack/interact") {
		t.Errorf("the log has no warning for the Slack click with the raw error:\n%s", logs.String())
	}

	if text := slackClick(t, ts, sl, stub, "approve", "missing 01ABC.v1"); text != "Not approved. missing is no longer waiting. Someone else decided it, the owner withdrew it, or playplace dropped it." {
		t.Errorf("Slack approve of a request that is gone = %q", text)
	}

	// The clicker lead@example.com asked for one request and owns another.
	// Slack refuses both approvals with the words of the web UI.
	for _, q := range []struct{ name, by, owner string }{{"leads-box", "lead@example.com", "dana@example.com"}, {"for-lead", "ops@example.com", "lead@example.com"}} {
		if res, body := do(t, ts, "POST", "/requests", q.by, true, url.Values{"name": {q.name}, "owner": {q.owner}}, true); res.Header.Get("X-Form-Error") != "" {
			t.Fatalf("submit %s = %s", q.name, body)
		}
	}
	stub.mu.Lock()
	own := slices.Clone(stub.posted[1:])
	stub.mu.Unlock()
	if len(own) != 2 {
		t.Fatalf("posted = %v, want the two requests of lead", own)
	}
	for i, want := range []string{"Not approved. You requested it, so another approver must approve it.", "Not approved. It names you as the owner, so another approver must approve it."} {
		if text := slackClick(t, ts, sl, stub, "approve", own[i]); text != want {
			t.Errorf("Slack approve of an own request = %q, want %q", text, want)
		}
	}
}

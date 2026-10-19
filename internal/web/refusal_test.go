package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

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
		goneText    = "box is no longer waiting. Someone else decided it, the owner withdrew it, or it was dropped."
	)
	for _, c := range []struct {
		name    string
		err     error
		request string
		want    string
	}{
		{"changed", changed, "box", changedText},
		{"requested", self("anna@example.com"), "box", "You requested box, so you cannot approve it. Another approver must approve it."},
		{"would own", self("bob@example.com"), "box", "You would own box, so you cannot approve it. Another approver must approve it."},
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
		{"an AWS error that core did not mark", fmt.Errorf("tag x: %w", apiError{"AccessDeniedException"}), "", awsFailed},
		{"a throttle that core did not mark", apiError{"ThrottlingException"}, "", awsSlow},
		// The checks run in order: a failed call that wraps a sentinel reads
		// as the sentinel.
		{"aws that wraps gone", failed(gone), "box", goneText},
		{"aws that wraps a change", failed(changed), "box", changedText},
		{"aws that wraps a self-approval", failed(self("anna@example.com")), "box", "You requested box, so you cannot approve it. Another approver must approve it."},
		// Without a request name, a sentinel keeps the words of core.
		{"gone with no name", gone, "", "Not found: no pending request named box."},
		{"a core refusal", errors.New("box would live 100 days, past the ceiling of 90 days; the latest expiry is 2026-12-14 12:00 UTC and an admin can override"), "",
			"Box would live 100 days, past the ceiling of 90 days. The latest expiry is 2026-12-14 12:00 UTC and an admin can override."},
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
	ref := refForm(refIn(t, home, "bobs-box"))
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
			if (mode.name == "reads" || c.writes) && !strings.Contains(body, awsFailed) {
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

	if text := slackClick(t, ts, sl, stub, "approve", "missing 01ABC.v1"); text != "Not approved. missing is no longer waiting. Someone else decided it, the owner withdrew it, or it was dropped." {
		t.Errorf("Slack approve of a request that is gone = %q", text)
	}
}

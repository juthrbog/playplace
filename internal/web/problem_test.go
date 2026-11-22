package web

import (
	"bytes"
	"context"
	"errors"
	"html"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

// remote is a managed account of anna as AWS lists it, with the given AWS
// state and extra tags.
func remote(id, name, state string, extra map[string]string) core.RemoteAccount {
	tags := map[string]string{core.TagManaged: "true", core.TagOwner: "anna@example.com", core.TagExpires: t0.Add(72 * time.Hour).Format(time.RFC3339), core.TagBudget: "50"}
	for k, v := range extra {
		tags[k] = v
	}
	return core.RemoteAccount{ProviderID: id, Name: name, Email: name + "@example.com", Status: state, JoinedAt: t0, Tags: tags}
}

// Each kind of problem has its own plain words, with times in the UTC
// layout of the pages. No words hold the raw AWS state or the raw text of
// LastError.
func TestProblemWords(t *testing.T) {
	cfg := core.DefaultConfig()
	closing := map[string]string{core.TagCloseRequested: t0.Add(-25 * time.Hour).Format(time.RFC3339)}
	for _, tc := range []struct {
		name string
		a    *core.Account
		want problemView
	}{
		{"closure unconfirmed", core.FromRemote(remote("1", "a", "ACTIVE", closing), cfg, t0),
			problemView{Text: "AWS has not confirmed the closure since 2026-09-14 11:00 UTC. Charges may continue."}},
		{"suspended", core.FromRemote(remote("1", "a", "SUSPENDED", nil), cfg, t0),
			problemView{Text: "AWS reports this account as suspended, not closed. Ask an admin to check it in AWS."}},
		{"waiting for activation", core.FromRemote(remote("1", "a", "PENDING_ACTIVATION", nil), cfg, t0),
			problemView{Text: "AWS reports this account as waiting for activation, not closed. Ask an admin to check it in AWS."}},
		{"a state with no words", core.FromRemote(remote("1", "a", "SOMETHING_NEW", nil), cfg, t0),
			problemView{Text: stateUnknown}},
		{"creation failed with advice", core.FromCreateRequest(core.CreateRequest{RequestID: "car-1", Name: "a", State: core.CreateFailed, FailureReason: "EMAIL_ALREADY_EXISTS"}),
			problemView{Text: "AWS could not create the account. " + core.FailureAdvice("EMAIL_ALREADY_EXISTS")}},
		{"creation failed with no reason", core.FromCreateRequest(core.CreateRequest{RequestID: "car-1", Name: "a", State: core.CreateFailed}),
			problemView{Text: "AWS could not create the account. AWS gave no reason. An admin can check the request in AWS."}},
		{"creation failed with a reason AWS does not describe", core.FromCreateRequest(core.CreateRequest{RequestID: "car-1", Name: "a", State: core.CreateFailed, FailureReason: "NEW_REASON"}),
			problemView{Text: "AWS could not create the account. " + createdNoHint}},
		{"a LastError with no kind", &core.Account{ID: "x", Name: "x", Status: core.StatusUnavailable, LastError: `provider state "WEIRD" since 2026-09-14T11:00:00Z`},
			problemView{Text: problemUnknown}},
	} {
		got, ok := accountProblem(tc.a)
		if !ok || got != tc.want {
			t.Errorf("%s: got %+v %v, want %+v", tc.name, got, ok, tc.want)
		}
	}
	if _, ok := accountProblem(&core.Account{ID: "x", Name: "x", Status: core.StatusActive}); ok {
		t.Error("an account with no LastError has a problem")
	}
	// The placement wait is a qualifier, not a failure.
	p := problemWords(core.Problem{Kind: core.ProblemPlacementWait, Since: t0})
	if !p.Wait || p.Text != "Waiting for AWS to place the account since 2026-09-15 12:00 UTC." {
		t.Errorf("placement wait: %+v", p)
	}
	if p := problemWords(core.Problem{Kind: "kind-from-the-future"}); p.Text != problemUnknown || p.Wait {
		t.Errorf("unknown kind: %+v", p)
	}
}

// A rendered problem with no kind shows the generic sentence, never its raw
// text, in a row and on the account page.
func TestUnknownProblemShowsGenericWords(t *testing.T) {
	a := &core.Account{ID: "x", Name: "x", Status: core.StatusUnavailable, CreatedAt: t0, LastError: `provider state "WEIRD" since 2026-09-14T11:00:00Z`}
	var row, pageOut bytes.Buffer
	if err := statusLines(a).Render(context.Background(), &row); err != nil {
		t.Fatal(err)
	}
	if err := accountPage(page{}, viewFor(a), spendView{Known: true}, historyView{}, "").Render(context.Background(), &pageOut); err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{"row": row.String(), "account page": pageOut.String()} {
		if !strings.Contains(out, problemUnknown) || strings.Contains(out, "WEIRD") || strings.Contains(out, "provider state") {
			t.Errorf("%s: want the generic words and no raw text:\n%s", name, out)
		}
	}
}

// rfc3339 finds an RFC 3339 time, as LastError writes it.
var rfc3339 = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}`)

// tagRe finds a tag with its attributes. Attributes may hold exact times
// for machines, such as datetime; people read only the text.
var tagRe = regexp.MustCompile(`<[^>]*>`)

// visibleText is the text of markup that people read: tags and attributes
// removed, entities decoded.
func visibleText(markup string) string {
	return html.UnescapeString(tagRe.ReplaceAllString(markup, " "))
}

// No page shows the raw AWS state or an RFC 3339 time from LastError: rows
// on the home page and the accounts page, and the account page, for an
// unconfirmed closure and an unexpected AWS state. The log holds the raw
// text, once for each new text, not once for each poll.
func TestPagesNeverShowRawProblemText(t *testing.T) {
	var logs bytes.Buffer
	ts, prov := newServerLogged(t, func(p *fake.Provider) core.Provider { return p }, slog.New(slog.NewTextHandler(&logs, nil)))
	prov.Seed(remote("111111111111", "annas-box", "ACTIVE", map[string]string{core.TagCloseRequested: t0.Add(-25 * time.Hour).Format(time.RFC3339)}))
	prov.Seed(remote("333333333333", "odd-box", "SUSPENDED", nil))
	pages := []struct{ path, want string }{
		{"/", "AWS has not confirmed the closure since 2026-09-14 11:00 UTC."},
		{"/", "AWS reports this account as suspended, not closed."},
		{"/accounts", "AWS has not confirmed the closure since"},
		{"/accounts", "AWS reports this account as suspended"},
		{"/accounts/111111111111", "<dt>failure</dt>"},
		{"/accounts/111111111111", "AWS has not confirmed the closure since 2026-09-14 11:00 UTC. Charges may continue."},
		{"/accounts/333333333333", "AWS reports this account as suspended, not closed. Ask an admin to check it in AWS."},
	}
	for _, p := range pages {
		res, body := do(t, ts, "GET", p.path, "ops@example.com", true, nil, false)
		if res.StatusCode != 200 || !strings.Contains(body, p.want) {
			t.Errorf("%s: status %d, missing %q:\n%s", p.path, res.StatusCode, p.want, body)
		}
		text := visibleText(body)
		if strings.Contains(text, `provider state "`) || strings.Contains(body, "provider state") {
			t.Errorf("%s: shows a raw AWS state:\n%s", p.path, text)
		}
		if m := rfc3339.FindString(text); m != "" {
			t.Errorf("%s: shows the RFC 3339 time %s:\n%s", p.path, m, text)
		}
	}
	log := logs.String()
	for _, want := range []string{`provider_state=SUSPENDED`, `kind=closure-unconfirmed`, `closure unconfirmed since 2026-09-14T11:00:00Z`} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %q:\n%s", want, log)
		}
	}
	if n := strings.Count(log, "account=odd-box"); n != 1 {
		t.Errorf("the problem of odd-box is logged %d times, want once:\n%s", n, log)
	}
}

// A cost pull that fails leaves the spend of the account page unknown. The
// page says so in words and never shows $0.00 or "No cost data yet.".
func TestAccountPageSaysSpendIsUnavailable(t *testing.T) {
	fc := &flakyCosts{}
	ts, _ := newServerOn(t, func(p *fake.Provider) core.Provider { fc.Provider = p; return fc })
	fc.fail.Store(true)
	res, body := do(t, ts, "GET", "/accounts/111111111111", "ops@example.com", true, nil, false)
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	for _, want := range []string{`<dt>spend this month</dt><dd><span class="muted">unavailable</span> of $50.00 a month</dd>`, `<p class="empty">` + spendUnavailable + `</p>`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q:\n%s", want, body)
		}
	}
	for _, bad := range []string{"$0.00", "No cost data yet.", "LimitExceededException"} {
		if strings.Contains(body, bad) {
			t.Errorf("the page shows %q:\n%s", bad, body)
		}
	}
	// When AWS answers again, the page shows the spend.
	fc.fail.Store(false)
	_, body = do(t, ts, "GET", "/accounts/111111111111", "ops@example.com", true, nil, false)
	if !strings.Contains(body, `<dt>spend this month</dt><dd>$22.50 of $50.00 a month <span class="muted">($27.50 left this month)</span></dd>`) || strings.Contains(body, spendUnavailable) {
		t.Errorf("the spend is not back:\n%s", body)
	}
}

// The account page reads only what the shell needs: the queue for the count
// beside Queue, but no holds. A failed list of holds cannot touch it.
func TestAccountPageKeepsTheQueueCount(t *testing.T) {
	ts, _ := newServer(t)
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-box"}}, true)
	_, body := doAs(t, ts, "GET", "/accounts/111111111111", "ops@example.com", true, true, nil, false)
	if !strings.Contains(body, `<span id="queue-count"><span class="count-pill">1`) {
		t.Errorf("the account page lost the queue count:\n%s", body)
	}
}

// suspendOnClose closes an account, and then AWS reports it as SUSPENDED.
type suspendOnClose struct{ *fake.Provider }

func (p suspendOnClose) CloseAccount(ctx context.Context, id string) error {
	if err := p.Provider.CloseAccount(ctx, id); err != nil {
		return err
	}
	r, err := p.GetAccount(ctx, id)
	if err != nil {
		return err
	}
	r.Status = "SUSPENDED"
	p.Seed(r)
	return nil
}

// The refusals of Close and Sync show account problems in the words of the
// rows, never the raw LastError, an AWS state or an RFC 3339 time.
func TestCloseAndSyncRefusalsUseProblemWords(t *testing.T) {
	ts, prov := newServerLogged(t, func(p *fake.Provider) core.Provider { return suspendOnClose{p} }, slog.New(slog.DiscardHandler))
	raw := func(name, body string) {
		t.Helper()
		text := visibleText(body)
		if strings.Contains(text, "provider state") || strings.Contains(text, "SUSPENDED") || strings.Contains(text, "ACTIVE") {
			t.Errorf("%s shows a raw AWS state:\n%s", name, text)
		}
		if m := rfc3339.FindString(text); m != "" {
			t.Errorf("%s shows the RFC 3339 time %s:\n%s", name, m, text)
		}
	}
	_, body := do(t, ts, "POST", "/accounts/111111111111/close", "ops@example.com", true, nil, true)
	if !strings.Contains(body, "annas-box: AWS reports this account as suspended, not closed. Ask an admin to check it in AWS.") {
		t.Errorf("the close refusal lacks the problem words:\n%s", body)
	}
	raw("close", body)

	prov.Seed(remote("333333333333", "odd-box", "SUSPENDED", nil))
	prov.AsyncClose = true
	prov.Seed(remote("444444444444", "late-box", "ACTIVE", map[string]string{core.TagCloseRequested: t0.Add(-25 * time.Hour).Format(time.RFC3339)}))
	_, body = do(t, ts, "POST", "/refresh", "ops@example.com", true, url.Values{}, true)
	for _, want := range []string{
		"odd-box: AWS reports this account as suspended, not closed.",
		"late-box: AWS has not confirmed the closure since 2026-09-14 11:00 UTC. Charges may continue.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the sync refusal lacks %q:\n%s", want, body)
		}
	}
	raw("sync", body)
}

// A failed close that is not an account problem keeps its own words, and a
// join with a failed call to AWS still reads as that failure.
func TestProblemRefusal(t *testing.T) {
	suspended := core.AccountProblemError{Name: "a", Problem: core.Problem{Kind: core.ProblemStateUnexpected, State: "SUSPENDED"}}
	for _, c := range []struct {
		name string
		err  error
		want string
	}{
		{"one", suspended, "a: AWS reports this account as suspended, not closed. Ask an admin to check it in AWS."},
		{"with another refusal", errors.Join(errors.New("account b is SUSPENDED in the organization root"), errors.Join(nil, suspended)),
			"Account b is SUSPENDED in the organization root. a: AWS reports this account as suspended, not closed. Ask an admin to check it in AWS."},
		{"with a failed call", errors.Join(suspended, &core.ProviderError{Op: "tag a", Err: errors.New("RequestID: 1")}), awsFailed},
	} {
		if got := refusal(c.err, ""); got != c.want {
			t.Errorf("%s: refusal = %q, want %q", c.name, got, c.want)
		}
	}
}

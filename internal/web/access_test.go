package web

import (
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"playplace/internal/audit"
	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

const (
	testPortal    = "https://example.awsapps.com/start"
	testPrincipal = "pid-9f3c2a"
	portalAnchor  = `<a class="btn btn-primary portal" href="` + testPortal + `" rel="noopener noreferrer" target="_blank">Open AWS access portal<span class="vh"> (opens in a new tab)</span></a>`
)

// remoteWith is an ACTIVE managed account of anna with extra tags.
func remoteWith(extra map[string]string) core.RemoteAccount {
	tags := map[string]string{
		core.TagManaged: "true",
		core.TagOwner:   "anna@example.com",
		core.TagExpires: t0.Add(72 * time.Hour).Format(time.RFC3339),
		core.TagBudget:  "50",
	}
	for k, v := range extra {
		tags[k] = v
	}
	return core.RemoteAccount{ProviderID: "111111111111", Name: "annas-box", Email: "a@example.com", Status: "ACTIVE", JoinedAt: t0, Tags: tags}
}

// The access state has one plain sentence for each kind of handoff block.
// No sentence names a raw budget health value or the principal ID. Only an
// active or expiring account has a state.
func TestAccessStateWords(t *testing.T) {
	pending := func(extra map[string]string) map[string]string {
		tags := map[string]string{core.TagHandoff: "pending"}
		for k, v := range extra {
			tags[k] = v
		}
		return tags
	}
	for _, c := range []struct {
		name   string
		tags   map[string]string
		status string
		access bool
		kind   accessKind
		text   string
	}{
		{"budget unchecked", pending(nil), "ACTIVE", true, accessReady, "Getting ready: waiting for budget protection to be checked."},
		{"budget settling", pending(map[string]string{core.TagBudgetHealth: "setup-pending"}), "ACTIVE", true, accessReady, "Getting ready: waiting for budget protection to be ready."},
		{"budget problem", pending(map[string]string{core.TagBudgetHealth: "restricted"}), "ACTIVE", false, accessReady, "Getting ready: budget protection needs an admin before the account can be used."},
		{"owner unknown", pending(map[string]string{core.TagBudgetHealth: "ready", core.TagOwner: "unknown"}), "ACTIVE", true, accessReady, "Getting ready: waiting for the account to have a known owner."},
		{"access pending", pending(map[string]string{core.TagBudgetHealth: "ready"}), "ACTIVE", true, accessReady, "Getting ready: waiting for owner access to be granted."},
		{"stalled", pending(map[string]string{core.TagBudgetHealth: "ready", core.TagExpires: t0.Add(-time.Hour).Format(time.RFC3339)}), "ACTIVE", true, accessReady, "Getting ready: setup has stopped. Ask an admin to check this account."},
		{"legacy without a grant", nil, "ACTIVE", true, accessReady, "Getting ready: waiting for owner access to be granted."},
		{"legacy, unknown owner", map[string]string{core.TagOwner: "unknown"}, "ACTIVE", true, accessReady, "Getting ready: waiting for the account to have a known owner."},
		{"handed off, unknown owner, no grant", map[string]string{core.TagHandoff: "complete", core.TagOwner: "unknown"}, "ACTIVE", true, accessReady, "Getting ready: waiting for the account to have a known owner."},
		{"handed off, no grant", map[string]string{core.TagHandoff: "complete"}, "ACTIVE", true, accessReady, "Getting ready: waiting for owner access to be granted."},
		{"granted", map[string]string{core.TagAccess: testPrincipal}, "ACTIVE", true, accessGranted, "Access granted"},
		{"granted and expiring", map[string]string{core.TagAccess: testPrincipal, core.TagWarnedAt: t0.Format(time.RFC3339)}, "ACTIVE", true, accessGranted, "Access granted"},
		{"handed off, not managed", map[string]string{core.TagHandoff: "complete"}, "ACTIVE", false, accessUnmanaged, "playplace does not manage AWS access for this account."},
		{"legacy, not managed", nil, "ACTIVE", false, accessUnmanaged, "playplace does not manage AWS access for this account."},
		{"closing", map[string]string{core.TagAccess: testPrincipal, core.TagCloseRequested: t0.Format(time.RFC3339)}, "ACTIVE", true, accessNone, ""},
		{"closed", map[string]string{core.TagAccess: testPrincipal}, "CLOSED", true, accessNone, ""},
		{"pending closure", nil, "PENDING_CLOSURE", false, accessNone, ""},
		{"suspended", nil, "SUSPENDED", false, accessNone, ""},
	} {
		r := remoteWith(c.tags)
		r.Status = c.status
		a := core.FromRemote(r, core.DefaultConfig(), t0)
		got := accessState(a, c.access, t0)
		if got.Kind != c.kind || got.Text != c.text {
			t.Errorf("%s: state = %d %q, want %d %q", c.name, got.Kind, got.Text, c.kind, c.text)
		}
		for _, bad := range []string{testPrincipal, "setup-pending", "restricted", "budget-"} {
			if strings.Contains(got.Text, bad) {
				t.Errorf("%s: the words %q hold %q", c.name, got.Text, bad)
			}
		}
	}
	// Every kind of handoff block has its own sentence.
	for _, k := range []core.HandoffBlock{core.HandoffStalled, core.HandoffBudgetUnchecked, core.HandoffBudgetSettling, core.HandoffBudgetProblem, core.HandoffOwnerUnknown, core.HandoffAccessPending} {
		if readyWords[k] == "" {
			t.Errorf("the handoff block %s has no sentence", k)
		}
	}
	if got := readyView("new-kind"); got.Text != accessReadyPrefix+readyWords[core.HandoffStalled] {
		t.Errorf("an unknown kind reads %q", got.Text)
	}
}

// newPortalServer is a server whose fake holds annas-box with extra tags
// and sams-box. portal is the access portal setting, and access says
// whether playplace manages console access.
func newPortalServer(t *testing.T, portal string, access bool, extra map[string]string) (*httptest.Server, *fake.Provider) {
	t.Helper()
	ts, prov, _ := newPortalServerWithHistory(t, portal, access, extra)
	return ts, prov
}

// newPortalServerWithHistory is newPortalServer that also returns the
// history sink.
func newPortalServerWithHistory(t *testing.T, portal string, access bool, extra map[string]string) (*httptest.Server, *fake.Provider, *audit.Memory) {
	t.Helper()
	prov := fake.New()
	prov.Now = func() time.Time { return t0 }
	prov.AccessOn = access
	prov.Seed(remoteWith(extra))
	sam := remoteWith(nil)
	sam.ProviderID, sam.Name, sam.Email = "222222222222", "sams-box", "s@example.com"
	sam.Tags[core.TagOwner] = "sam@example.com"
	prov.Seed(sam)
	cfg := core.DefaultConfig()
	cfg.InventoryTTL = 0
	cfg.AccessPortalURL = portal
	svc := core.NewService(prov, nil, cfg, nil)
	svc.Now = func() time.Time { return t0 }
	hist := &audit.Memory{}
	svc.SetAuditor(hist)
	ts := httptest.NewServer(New(svc, slog.New(slog.DiscardHandler), headerAuth{}, nil, hist).Handler())
	t.Cleanup(ts.Close)
	return ts, prov, hist
}

// The AWS access portal button shows only when the address is set, the
// viewer is the owner, the account is active or expiring, and access is
// granted or not managed. An admin who is not the owner reads the state
// in words. No page ever shows the principal ID.
func TestPortalButtonRules(t *testing.T) {
	granted := map[string]string{core.TagAccess: testPrincipal}
	for _, c := range []struct {
		name   string
		portal string
		access bool
		tags   map[string]string
		user   string
		admin  bool
		button bool
		words  string
	}{
		{"owner, granted", testPortal, true, granted, "anna@example.com", false, true, "Access granted"},
		{"owner, expiring", testPortal, true, map[string]string{core.TagAccess: testPrincipal, core.TagWarnedAt: t0.Format(time.RFC3339)}, "anna@example.com", false, true, "Access granted"},
		{"owner who is an admin", testPortal, true, granted, "anna@example.com", true, true, "Access granted"},
		{"owner, not managed", testPortal, false, nil, "anna@example.com", false, true, accessUnmanagedText},
		{"admin, not the owner", testPortal, true, granted, "ops@example.com", true, false, "Access granted"},
		{"admin, not the owner, not managed", testPortal, false, nil, "ops@example.com", true, false, accessUnmanagedText},
		{"owner, no grant yet", testPortal, true, nil, "anna@example.com", false, false, "Getting ready: waiting for owner access to be granted."},
		{"owner, budget settling", testPortal, true, map[string]string{core.TagHandoff: "pending", core.TagBudgetHealth: "setup-pending"}, "anna@example.com", false, false, "Getting ready: waiting for budget protection to be ready."},
		{"owner, closing", testPortal, true, map[string]string{core.TagAccess: testPrincipal, core.TagCloseRequested: t0.Format(time.RFC3339)}, "anna@example.com", false, false, ""},
		{"owner, no setting", "", true, granted, "anna@example.com", false, false, "Access granted"},
		{"owner, no setting, not managed", "", false, nil, "anna@example.com", false, false, accessUnmanagedText},
	} {
		t.Run(c.name, func(t *testing.T) {
			ts, _ := newPortalServer(t, c.portal, c.access, c.tags)
			_, body := do(t, ts, "GET", "/accounts/111111111111", c.user, c.admin, nil, false)
			card := between(body, `<div class="loan loan-page">`, `aria-labelledby="facts-title"`)
			if card == "" {
				t.Fatalf("no card on the account page:\n%s", body)
			}
			if got := strings.Contains(card, portalAnchor); got != c.button {
				t.Errorf("the account card has the button: %v, want %v:\n%s", got, c.button, card)
			}
			if !c.button && strings.Contains(body, "Open AWS access portal") {
				t.Errorf("the page holds the button text:\n%s", body)
			}
			if c.words == "" {
				if strings.Contains(card, `class="qualifier access`) {
					t.Errorf("the card has an access line:\n%s", card)
				}
			} else if !strings.Contains(card, "<span>"+c.words+"</span>") {
				t.Errorf("the account card lacks %q:\n%s", c.words, card)
			}
			if strings.Contains(body, testPrincipal) {
				t.Errorf("the account page shows the principal ID")
			}
			if c.admin && c.user != "anna@example.com" {
				return
			}
			_, home := do(t, ts, "GET", "/", c.user, c.admin, nil, false)
			loan := between(home, `<li id="acct-111111111111" class="loan">`, `</li>`)
			if loan == "" {
				if c.admin {
					return // an admin home lists the fleet, not loan cards
				}
				t.Fatalf("no loan card on the home page:\n%s", home)
			}
			if got := strings.Contains(loan, portalAnchor); got != c.button {
				t.Errorf("the home loan card has the button: %v, want %v:\n%s", got, c.button, loan)
			}
			ready := strings.HasPrefix(c.words, accessReadyPrefix)
			if got := strings.Contains(loan, `<span class="qualifier">getting ready</span>`); got != ready {
				t.Errorf("the home loan card says getting ready: %v, want %v:\n%s", got, ready, loan)
			}
			if strings.Contains(loan, `class="qualifier access`) || strings.Contains(home, testPrincipal) {
				t.Errorf("the home loan card shows the full access line or the principal ID:\n%s", loan)
			}
		})
	}
}

// The history on the account page leaves out the principal ID of the
// access events. A row whose only detail is the principal shows no Details.
func TestAccountHistoryHidesThePrincipal(t *testing.T) {
	ts, _, hist := newPortalServerWithHistory(t, testPortal, true, map[string]string{core.TagAccess: testPrincipal})
	for _, e := range []core.AuditEvent{
		{JourneyID: "account:111111111111", At: t0, Event: "access-granted", Account: "annas-box", AccountID: "111111111111", Actor: core.ActorSystem, Message: "granted", Details: map[string]string{"principal": testPrincipal}},
		{JourneyID: "account:111111111111", At: t0.Add(time.Hour), Event: "access-revoked", Account: "annas-box", AccountID: "111111111111", Actor: core.ActorSystem, Message: "revoked", Details: map[string]string{"principal": testPrincipal, "note": "kept"}},
	} {
		if err := hist.Record(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}
	_, body := do(t, ts, "GET", "/accounts/111111111111", "anna@example.com", false, nil, false)
	if !strings.Contains(body, "access-granted") || !strings.Contains(body, "access-revoked") {
		t.Fatalf("the page lacks the access events:\n%s", body)
	}
	if strings.Contains(body, testPrincipal) {
		t.Errorf("the account page shows the principal ID")
	}
	if got := strings.Count(body, "<summary>Details</summary>"); got != 1 {
		t.Errorf("Details shows %d times, want 1 (only the row with another detail)", got)
	}
	if !strings.Contains(body, "kept") {
		t.Errorf("the other detail is gone")
	}
}

// The portal button is the only link on the card that opens a new tab, and
// it comes before +7d and Close.
func TestPortalButtonLeadsTheActions(t *testing.T) {
	ts, _ := newPortalServer(t, testPortal, false, nil)
	_, body := do(t, ts, "GET", "/accounts/111111111111", "anna@example.com", false, nil, false)
	inOrder(t, "the account card", body, `<div class="loan-actions">`+portalAnchor, `/extend"`, `/close"`)
}

// The refresh hash covers the access state and the button, so a grant
// that lands makes the next poll redraw the card.
func TestAccountRefreshHashCoversTheAccessState(t *testing.T) {
	ts, prov := newPortalServer(t, testPortal, true, nil)
	const page = "/accounts/111111111111"
	_, body := fromPage(t, ts, "GET", page, page, "anna@example.com", false, false, nil, "")
	before := accountHashIn(t, body)
	if strings.Contains(body, portalAnchor) || !strings.Contains(body, "waiting for owner access to be granted.") {
		t.Fatalf("before the grant:\n%s", body)
	}
	if res, _ := fromPage(t, ts, "GET", page, page, "anna@example.com", false, false, nil, before); res.StatusCode != 204 {
		t.Fatalf("an unchanged refresh = %d, want 204", res.StatusCode)
	}
	prov.Seed(remoteWith(map[string]string{core.TagAccess: testPrincipal}))
	res, body := fromPage(t, ts, "GET", page, page, "anna@example.com", false, false, nil, before)
	if res.StatusCode != 200 {
		t.Fatalf("the refresh after the grant = %d, want 200", res.StatusCode)
	}
	if after := accountHashIn(t, body); after == before {
		t.Errorf("the hash did not change with the access state")
	}
	if !strings.Contains(body, portalAnchor) || !strings.Contains(body, "<span>Access granted</span>") || strings.Contains(body, testPrincipal) {
		t.Errorf("after the grant:\n%s", body)
	}
}

// The portal button on card stock takes a pencil-grey border from the
// tokens, never shelf green, and the phone rule makes every .btn 44 px.
func TestPortalButtonStyle(t *testing.T) {
	css := staticText(t, "app.css")
	holdsEach(t, "app.css", css,
		":where(.loan) a.btn-primary {\n    border-color: var(--ink-2);\n  }",
		":where(.loan) a.btn-primary:hover {\n    background: var(--card-sel);\n    border-color: var(--ink);\n  }")
	// A rule in an earlier layer loses to .btn-primary in the components
	// layer whatever its specificity, so the rule must come after it there.
	components := strings.Index(css, "@layer components {")
	primary := strings.Index(css, "  .btn-primary:hover {")
	portal := strings.Index(css, ":where(.loan) a.btn-primary {")
	utilities := strings.Index(css, "@layer utilities {")
	if components < 0 || primary < components || portal < primary || portal > utilities {
		t.Errorf("the portal rule is not after .btn-primary in the components layer: components %d, .btn-primary:hover %d, portal %d, utilities %d", components, primary, portal, utilities)
	}
	rule := between(css, ":where(.loan) a.btn-primary {", ".btn-approve {")
	if strings.Contains(rule, "shelf") {
		t.Errorf("the portal button takes shelf colors:\n%s", rule)
	}
	phone := between(css, "/* Every touch target on a phone is at least 44 px tall. */", "}")
	holdsEach(t, "the phone touch rule", phone, ".btn,", "height: var(--touch);")
}

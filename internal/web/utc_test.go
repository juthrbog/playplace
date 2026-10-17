package web

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"playplace/internal/audit"
	"playplace/internal/core"
)

// renderAccount renders the account page of a with history on.
func renderAccount(t *testing.T, a *core.Account, events []core.AuditEvent) string {
	t.Helper()
	var buf bytes.Buffer
	h := historyView{Enabled: true, Page: audit.Page{Events: events}}
	if err := accountPage(page{}, a, nil, 0, h).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// A stored time keeps the zone it was read in. The account page writes every
// time in UTC, the same as the history table, so one page never mixes zones.
func TestAccountPageWritesEveryTimeInUTC(t *testing.T) {
	edt := time.FixedZone("EDT", -4*60*60)
	at := t0.In(edt)
	a := &core.Account{
		ID: "x", Name: "x", ProviderID: "111111111111", Status: core.StatusClosing,
		CreatedAt: at, ExpiresAt: at.Add(24 * time.Hour), WarnedAt: &at, CloseRequestedAt: &at,
		BudgetHealth: "ready", BudgetChecked: at.Format(time.RFC3339),
	}
	out := renderAccount(t, a, []core.AuditEvent{{At: at, Event: "requested", Actor: "anna@example.com", Message: "asked"}})
	for _, want := range []string{
		"<dt>created</dt><dd>2026-09-15 12:00 UTC</dd>",
		"<dt>expires</dt><dd>2026-09-16 12:00 UTC",
		"<dt>expiring notice sent</dt><dd>2026-09-15 12:00 UTC</dd>",
		"<dt>close requested</dt><dd>2026-09-15 12:00 UTC</dd>",
		"(in this state since 2026-09-15 12:00 UTC)",
		`<td title="2026-09-15T12:00:00Z">2026-09-15 12:00:00 UTC</td>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, local := range []string{"EDT", "08:00", "-04:00"} {
		if strings.Contains(out, local) {
			t.Errorf("the page shows the local time %q:\n%s", local, out)
		}
	}
}

// Core stores the time when the budget state began as an RFC 3339 value. It
// reads like every other time on the page. A value that does not parse shows
// as it is, so the page never hides what is stored.
func TestBudgetStateTimeReadsLikeTheOtherTimes(t *testing.T) {
	for _, tc := range []struct{ stored, want string }{
		{"2026-10-16T17:44:24Z", "(in this state since 2026-10-16 17:44 UTC)"},
		{"2026-10-16T13:44:24-04:00", "(in this state since 2026-10-16 17:44 UTC)"},
		{"last tuesday", "(in this state since last tuesday)"},
	} {
		a := &core.Account{ID: "x", Name: "x", Status: core.StatusActive, CreatedAt: t0, BudgetHealth: "ready", BudgetChecked: tc.stored}
		if out := renderAccount(t, a, nil); !strings.Contains(out, tc.want) {
			t.Errorf("%q: missing %q", tc.stored, tc.want)
		}
	}
}

// The queue writes the request time in UTC with its zone, the same as the
// account page. A request read in another zone still shows UTC.
func TestQueueWritesTheRequestTimeInUTC(t *testing.T) {
	edt := time.FixedZone("EDT", -4*60*60)
	q := core.Request{Name: "x", Owner: "anna@example.com", RequestedBy: "anna@example.com", Via: "web", TTL: 24 * time.Hour, BudgetUSD: 50, RequestedAt: t0.In(edt)}
	var buf bytes.Buffer
	if err := pendingRow(page{}, q).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if want := `<td title="2026-09-15T12:00:00Z">Sep 15 12:00 UTC</td>`; !strings.Contains(out, want) {
		t.Errorf("missing %s:\n%s", want, out)
	}
	for _, local := range []string{"EDT", "08:00", "-04:00"} {
		if strings.Contains(out, local) {
			t.Errorf("the queue shows the local time %q:\n%s", local, out)
		}
	}
}

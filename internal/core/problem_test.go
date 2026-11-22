package core

import (
	"testing"
	"time"
)

// Every place that writes LastError writes the matching Problem from the same
// values, and an account with no LastError has no problem, except a failed
// creation that AWS gave no reason for.
func TestProblemFollowsEveryLastErrorWriter(t *testing.T) {
	cfg := DefaultConfig() // CloseAlertAfter is 24 hours
	joined := t0.Add(-2 * time.Hour)
	remote := func(state string, tags map[string]string) RemoteAccount {
		all := map[string]string{TagManaged: "true", TagOwner: "alex", TagExpires: t0.Add(48 * time.Hour).Format(time.RFC3339), TagBudget: "25"}
		for k, v := range tags {
			all[k] = v
		}
		return RemoteAccount{ProviderID: "123456789012", Name: "dev", Status: state, JoinedAt: joined, Tags: all}
	}
	overdue := t0.Add(-25 * time.Hour)
	recent := t0.Add(-time.Hour)
	svc := NewService(nil, nil, cfg, nil)
	cases := []struct {
		name string
		a    *Account
		want Problem // Kind "" for none
	}{
		{"active", FromRemote(remote("ACTIVE", nil), cfg, t0), Problem{}},
		{"suspended", FromRemote(remote("SUSPENDED", nil), cfg, t0), Problem{Kind: ProblemStateUnexpected, State: "SUSPENDED"}},
		{"closing in time", FromRemote(remote("ACTIVE", map[string]string{TagCloseRequested: recent.Format(time.RFC3339)}), cfg, t0), Problem{}},
		{"closing overdue", FromRemote(remote("ACTIVE", map[string]string{TagCloseRequested: overdue.Format(time.RFC3339)}), cfg, t0), Problem{Kind: ProblemClosureUnconfirmed, Since: overdue, State: "ACTIVE"}},
		{"pending closure overdue", FromRemote(remote("PENDING_CLOSURE", map[string]string{TagCloseRequested: overdue.Format(time.RFC3339)}), cfg, t0), Problem{Kind: ProblemClosureUnconfirmed, Since: overdue, State: "PENDING_CLOSURE"}},
		// The closure write comes last, so it replaces the state write, as
		// in LastError.
		{"suspended and closure overdue", FromRemote(remote("SUSPENDED", map[string]string{TagCloseRequested: overdue.Format(time.RFC3339)}), cfg, t0), Problem{Kind: ProblemClosureUnconfirmed, Since: overdue, State: "SUSPENDED"}},
		{"suspended and closing in time", FromRemote(remote("SUSPENDED", map[string]string{TagCloseRequested: recent.Format(time.RFC3339)}), cfg, t0), Problem{Kind: ProblemStateUnexpected, State: "SUSPENDED"}},
		{"closed", FromRemote(remote("CLOSED", map[string]string{TagCloseRequested: overdue.Format(time.RFC3339)}), cfg, t0), Problem{}},
		{"creating", FromCreateRequest(CreateRequest{RequestID: "car-1", Name: "dev", RequestedAt: t0, State: CreateInProgress}), Problem{}},
		{"failed", FromCreateRequest(CreateRequest{RequestID: "car-1", Name: "dev", RequestedAt: t0, State: CreateFailed, FailureReason: "EMAIL_ALREADY_EXISTS"}), Problem{Kind: ProblemCreationFailed, Reason: "EMAIL_ALREADY_EXISTS"}},
		{"failed with no reason", FromCreateRequest(CreateRequest{RequestID: "car-1", Name: "dev", RequestedAt: t0, State: CreateFailed}), Problem{Kind: ProblemCreationFailed}},
		{"unplaced past the grace", svc.unplacedAccount(remote("ACTIVE", nil), &CreateRequest{RequestID: "car-1", RequestedAt: joined.Add(-time.Minute)}, t0), Problem{Kind: ProblemPlacementWait, Since: joined}},
		{"unplaced in the grace", svc.unplacedAccount(remote("ACTIVE", nil), nil, joined.Add(time.Minute)), Problem{}},
		{"activating in the grace", svc.unplacedAccount(remote("PENDING_ACTIVATION", nil), nil, joined.Add(time.Minute)), Problem{}},
	}
	for _, tc := range cases {
		got, ok := tc.a.Problem()
		if ok != (tc.want.Kind != "") || got != tc.want {
			t.Errorf("%s: Problem = %+v, %v; want %+v", tc.name, got, ok, tc.want)
		}
		if ok != (tc.a.LastError != "") && tc.a.Status != StatusFailed {
			t.Errorf("%s: Problem ok = %v, but LastError = %q", tc.name, ok, tc.a.LastError)
		}
	}
}

// An account built by hand with only LastError has no typed problem, so an
// interface falls back to its general words.
func TestProblemNeedsATypedWriter(t *testing.T) {
	a := &Account{Status: StatusClosing, LastError: "closure unconfirmed"}
	if p, ok := a.Problem(); ok {
		t.Fatalf("Problem = %+v, want none", p)
	}
}

// The Needs attention ledger and the overdue qualifier rely on this rule:
// an account is overdue when it is closing and FromRemote wrote LastError,
// which it does only after CloseAlertAfter. An unavailable account is never
// overdue, even when its closure is unconfirmed; it needs attention as
// unavailable instead.
func TestClosureOverdueKeepsItsRule(t *testing.T) {
	cfg := DefaultConfig()
	at := func(state string, requested time.Time) *Account {
		return FromRemote(RemoteAccount{ProviderID: "123456789012", Name: "dev", Status: state, JoinedAt: t0.Add(-48 * time.Hour), Tags: map[string]string{
			TagManaged: "true", TagOwner: "alex", TagExpires: t0.Add(48 * time.Hour).Format(time.RFC3339), TagBudget: "25",
			TagCloseRequested: requested.Format(time.RFC3339),
		}}, cfg, t0)
	}
	for _, tc := range []struct {
		name string
		a    *Account
		want bool
	}{
		{"just before the alert age", at("ACTIVE", t0.Add(-cfg.CloseAlertAfter+time.Second)), false},
		{"at the alert age", at("ACTIVE", t0.Add(-cfg.CloseAlertAfter)), true},
		{"pending closure past the age", at("PENDING_CLOSURE", t0.Add(-2*cfg.CloseAlertAfter)), true},
		{"suspended past the age", at("SUSPENDED", t0.Add(-2*cfg.CloseAlertAfter)), false},
		{"closed past the age", at("CLOSED", t0.Add(-2*cfg.CloseAlertAfter)), false},
		{"built by hand", &Account{Status: StatusClosing, LastError: "anything"}, true},
	} {
		if got := tc.a.ClosureOverdue(); got != tc.want {
			t.Errorf("%s: ClosureOverdue = %v, want %v (status %s, LastError %q)", tc.name, got, tc.want, tc.a.Status, tc.a.LastError)
		}
		if got := tc.a.ClosureOverdue(); got != (tc.a.Status == StatusClosing && tc.a.LastError != "") {
			t.Errorf("%s: ClosureOverdue = %v, does not follow closing and LastError", tc.name, got)
		}
	}
}

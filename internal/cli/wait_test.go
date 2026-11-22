package cli

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
	"playplace/internal/notify"
	"playplace/internal/provider/fake"
)

// approve and create must succeed once the account is placed, and name what
// still blocks the initial handoff. With budget enforcement, AWS shows a new
// budget action only on the next pass, so protection is the usual blocker.
func TestWaitForAccountSucceedsAndNamesTheBlocker(t *testing.T) {
	previousPoll := createPoll
	createPoll = time.Millisecond
	defer func() { createPoll = previousPoll }()
	for _, tc := range []struct {
		name            string
		enforce, access bool
		want            string // the not-ready line, or "" for none
	}{
		{name: "wait-pending", enforce: true, want: "wait-pending is not ready yet: budget protection is setup-pending. A later refresh continues it."},
		{name: "wait-access", access: true, want: "wait-access is not ready yet: owner access is not granted. A later refresh retries it."},
		{name: "wait-ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prov := fake.New()
			prov.PollsToDone = 1
			cfg := core.DefaultConfig()
			if tc.enforce {
				cfg.BudgetPolicyID, cfg.BudgetActionRole, cfg.AlertEmail = "p-budget123", "arn:aws:iam::000000000000:role/Budgets", "ops@example.com"
			}
			if tc.access {
				prov.AccessOn = true
				prov.AddUser("user1", "owner", "owner@example.com")
			}
			svc := core.NewService(prov, nil, cfg, nil)
			ctx := context.Background()
			acc, err := svc.Request(ctx, core.RequestInput{Name: tc.name, Owner: "owner"})
			if err != nil {
				t.Fatal(err)
			}
			if tc.access {
				// The owner leaves the identity store before placement, so
				// the grant waits without failing the poll.
				clear(prov.Users)
			}
			text, err := captureStdout(t, func() error { return waitForAccount(ctx, &app{svc: svc}, acc, time.Minute) })
			if err != nil {
				t.Fatalf("placed account reported as a failure: %v", err)
			}
			if !strings.Contains(text, tc.name+" is active") {
				t.Fatalf("output = %q", text)
			}
			if tc.want == "" && strings.Contains(text, "not ready yet") || tc.want != "" && !strings.Contains(text, tc.want+"\n") {
				t.Fatalf("output = %q, want not-ready line %q", text, tc.want)
			}
		})
	}
}

// AWS shows a new account as PENDING_ACTIVATION for a few minutes. Within
// the placement grace the wait keeps polling and says that the account waits
// for activation. After the grace the account is unavailable, as in the
// inventory, and the wait stops.
func TestWaitForAccountWaitsForActivation(t *testing.T) {
	previousPoll := createPoll
	createPoll = time.Millisecond
	defer func() { createPoll = previousPoll }()
	for _, tc := range []struct {
		name  string
		polls int           // how many polls see the account as PENDING_ACTIVATION
		step  time.Duration // how far each read moves the service clock
		fail  string        // the error, or "" for success
	}{
		{name: "wait-activated", polls: 3},
		{name: "wait-stuck", polls: 1000, step: 10 * time.Minute, fail: `wait-stuck is unavailable: provider state "PENDING_ACTIVATION"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prov := fake.New()
			prov.ActivationPolls = tc.polls
			// AWS creates the account at start. With a step, the service
			// clock passes the one-hour grace after a few polls.
			start := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
			now := start
			prov.Now = func() time.Time { return start }
			svc := core.NewService(prov, nil, core.DefaultConfig(), nil)
			svc.Now = func() time.Time {
				now = now.Add(tc.step)
				return now
			}
			ctx := context.Background()
			acc, err := svc.Request(ctx, core.RequestInput{Name: tc.name, Owner: "owner"})
			if err != nil {
				t.Fatal(err)
			}
			text, err := captureStdout(t, func() error { return waitForAccount(ctx, &app{svc: svc}, acc, time.Minute) })
			if !strings.Contains(text, "\n"+tc.name+" (account ") || strings.Count(text, "waits for AWS to activate it") != 1 {
				t.Fatalf("output = %q, want one activation line", text)
			}
			if tc.fail == "" && (err != nil || !strings.Contains(text, "\n"+tc.name+" is active")) {
				t.Fatalf("output = %q, err = %v", text, err)
			}
			if tc.fail != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.fail)) {
				t.Fatalf("err = %v, want %q", err, tc.fail)
			}
		})
	}
}

func TestHandoffBlockerFollowsReadinessOrder(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	// pending is a placed account that waits for its initial handoff, as
	// the provider lists it.
	pending := func(health, owner string, tags map[string]string, state string) *core.Account {
		all := map[string]string{
			core.TagManaged:      "true",
			core.TagOwner:        owner,
			core.TagExpires:      now.Add(7 * 24 * time.Hour).Format(time.RFC3339),
			core.TagBudget:       "50",
			core.TagHandoff:      "pending",
			core.TagBudgetHealth: health,
		}
		for k, v := range tags {
			all[k] = v
		}
		return core.FromRemote(core.RemoteAccount{ProviderID: "111111111111", Name: "dev", Status: state, JoinedAt: now.Add(-time.Hour), Tags: all}, core.DefaultConfig(), now)
	}
	for _, tc := range []struct {
		name   string
		acc    *core.Account
		access bool
		want   string // a prefix ending before the first period, or "" for none
	}{
		{"unchecked", pending("", "owner", nil, "ACTIVE"), true, "budget protection is not checked yet"},
		{"settling", pending("setup-pending", "unknown", nil, "ACTIVE"), true, "budget protection is setup-pending"},
		{"restricted", pending("restricted", "owner", nil, "ACTIVE"), true, "budget protection is restricted"},
		{"owner unknown", pending("ready", " Unknown ", nil, "ACTIVE"), true, "the owner is not identified"},
		{"owner question mark", pending("alerts-disabled", "?", nil, "ACTIVE"), false, "the owner is not identified"},
		{"access pending", pending("alerts-only", "owner", nil, "ACTIVE"), true, "owner access is not granted"},
		{"access granted", pending("alerts-only", "owner", map[string]string{core.TagAccess: "principal-1"}, "ACTIVE"), true, ""},
		// Without managed access nothing grants it, so the CLI does not say
		// that access is missing.
		{"access not managed", pending("alerts-only", "owner", nil, "ACTIVE"), false, ""},
		{"closing", pending("ready", "owner", map[string]string{core.TagCloseRequested: now.Format(time.RFC3339)}, "ACTIVE"), true, "readiness is stopped"},
		{"suspended", pending("ready", "owner", nil, "SUSPENDED"), true, "readiness is stopped"},
		{"expired", pending("ready", "owner", map[string]string{core.TagExpires: now.Add(-time.Minute).Format(time.RFC3339)}, "ACTIVE"), true, "readiness is stopped"},
	} {
		got := handoffBlocker(tc.acc, tc.access, now)
		if tc.want == "" {
			if got != "" {
				t.Errorf("%s: handoffBlocker = %q, want none", tc.name, got)
			}
			continue
		}
		if !strings.HasPrefix(got, tc.want+".") {
			t.Errorf("%s: handoffBlocker = %q, want prefix %q", tc.name, got, tc.want)
		}
	}
}

// On a terminal, stdout and stderr share one screen. A poll logs the ready
// notice at Warn on stderr, so the wait must end each progress line before
// it polls. Then every notice starts on its own line and no progress line is
// cut.
func TestWaitKeepsNoticesOnTheirOwnLines(t *testing.T) {
	previousPoll := createPoll
	createPoll = time.Millisecond
	defer func() { createPoll = previousPoll }()
	screen, err := os.CreateTemp(t.TempDir(), "tty")
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(screen, &slog.HandlerOptions{Level: slog.LevelWarn}))
	prov := fake.New()
	prov.ActivationPolls = 2
	svc := core.NewService(prov, &notify.Log{Logger: log}, core.DefaultConfig(), log)
	ctx := context.Background()
	acc, err := svc.Request(ctx, core.RequestInput{Name: "dev-one", Owner: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = screen
	waitErr := waitForAccount(ctx, &app{svc: svc}, acc, time.Minute)
	os.Stdout = previous
	if err := screen.Close(); err != nil {
		t.Fatal(err)
	}
	if waitErr != nil {
		t.Fatal(waitErr)
	}
	data, err := os.ReadFile(screen.Name())
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "\ntime=") || !strings.Contains(text, `msg=notify subject="Playground account ready"`) {
		t.Fatalf("the ready notice was not logged during the wait:\n%s", text)
	}
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		switch {
		case line == "waiting for AWS to create dev-one",
			strings.HasPrefix(line, "dev-one (account ") && strings.HasSuffix(line, ") waits for AWS to activate it"),
			strings.HasPrefix(line, "dev-one is active: account "),
			strings.HasPrefix(line, "time=") && strings.Count(line, "time=") == 1:
		default:
			t.Fatalf("line %q mixes progress and log output:\n%s", line, text)
		}
	}
}

// captureStdout runs fn with os.Stdout sent to a temporary file and returns
// what fn printed.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = out
	runErr := fn()
	os.Stdout = previous
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data), runErr
}

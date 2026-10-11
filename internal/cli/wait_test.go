package cli

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
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

func TestHandoffBlockerFollowsReadinessOrder(t *testing.T) {
	for _, tc := range []struct {
		health, owner, want string
	}{
		{"", "owner", "budget protection is not checked yet"},
		{"setup-pending", "unknown", "budget protection is setup-pending"},
		{"restricted", "owner", "budget protection is restricted"},
		{"ready", " Unknown ", "the owner is not identified"},
		{"alerts-disabled", "?", "the owner is not identified"},
		{"alerts-only", "owner", "owner access is not granted"},
	} {
		got := handoffBlocker(&core.Account{BudgetHealth: tc.health, Owner: tc.owner})
		if !strings.HasPrefix(got, tc.want+".") {
			t.Errorf("handoffBlocker(%q, %q) = %q, want prefix %q", tc.health, tc.owner, got, tc.want)
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

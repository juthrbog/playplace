package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
)

// A failed cost pull shows as unavailable in costs and show, never as $0.00
// or as the spend of another pull.
func TestCostsAndShowSayUnavailableAfterAFailedPull(t *testing.T) {
	cleanEnv(t)
	prov := useFake(t)
	tags := map[string]string{core.TagManaged: "true", core.TagOwner: "anna@example.com", core.TagExpires: time.Now().Add(10 * 24 * time.Hour).UTC().Format(time.RFC3339), core.TagBudget: "50"}
	prov.Seed(core.RemoteAccount{ProviderID: "111111111111", Name: "dev-good", JoinedAt: time.Now().Add(-24 * time.Hour), Tags: tags})
	prov.Seed(core.RemoteAccount{ProviderID: "222222222222", Name: "dev-bad", JoinedAt: time.Now().Add(-24 * time.Hour), Tags: tags})
	prov.CostFailures["222222222222"] = "AccessDeniedException: not allowed"
	run := func(args ...string) (string, string) {
		t.Helper()
		var out string
		errText := captureStderr(t, func() {
			var err error
			out, err = captureStdout(t, func() error {
				cmd := New()
				cmd.SetArgs(append(args, "--no-refresh", "--history", "none"))
				return cmd.ExecuteContext(context.Background())
			})
			if err != nil {
				t.Error(err)
			}
		})
		return out, errText
	}
	// line is the output line that names the account.
	line := func(out, name string) string {
		t.Helper()
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, name+" ") {
				return l
			}
		}
		t.Fatalf("no line for %s in:\n%s", name, out)
		return ""
	}

	out, errText := run("costs")
	if got := line(out, "dev-good"); !strings.HasSuffix(got, "$45.00") {
		t.Errorf("costs row of dev-good = %q, want the 30 day spend $45.00", got)
	}
	if got := line(out, "dev-bad"); !strings.HasSuffix(got, "unavailable") || strings.Contains(got, "$0.00") {
		t.Errorf("costs row of dev-bad = %q, want unavailable", got)
	}
	if !strings.Contains(errText, "AccessDeniedException") {
		t.Errorf("costs must write the failure to stderr:\n%s", errText)
	}

	out, errText = run("show", "dev-bad")
	if !strings.Contains(out, "spend (30d): unavailable\n") || strings.Contains(out, "$0.00") {
		t.Errorf("show of dev-bad:\n%s", out)
	}
	if !strings.Contains(errText, "costs: costs for dev-bad: AccessDeniedException") {
		t.Errorf("show must write the failure to stderr:\n%s", errText)
	}
	if out, _ = run("show", "dev-good"); !strings.Contains(out, "spend (30d): $45.00 over 30 days\n") {
		t.Errorf("show of dev-good:\n%s", out)
	}
}

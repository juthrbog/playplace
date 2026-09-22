package cli

import (
	"os"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
)

func TestListShowsOwnedDamageWithoutInventedFacts(t *testing.T) {
	a := core.FromRemote(core.RemoteAccount{ProviderID: "123456789012", Name: "damaged-owned", Status: "ACTIVE", Tags: map[string]string{
		core.TagManaged: "true", core.TagExpires: "broken", core.TagBudget: "broken",
	}}, core.DefaultConfig(), time.Now())
	out, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	previous := os.Stdout
	os.Stdout = out
	defer func() { os.Stdout = previous }()
	printAccounts([]*core.Account{a})
	data, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Count(text, "unknown") < 2 || !strings.Contains(text, "operator tag repair required") || !strings.Contains(text, core.TagExpires) || !strings.Contains(text, core.TagBudget) {
		t.Fatalf("missing unknown values or diagnostics: %s", text)
	}
	if strings.Contains(text, "0001-01-01") || strings.Contains(text, "$0.00") {
		t.Fatalf("invented facts: %s", text)
	}
}

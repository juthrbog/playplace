package web

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

func TestExistingBudgetIsAdminOnly(t *testing.T) {
	ts, prov := newServer(t)
	form := url.Values{"budget": {"100"}, "reason": {"approved testing"}, "actor": {"admin@example.com"}}
	for _, user := range []struct {
		name     string
		approver bool
	}{{"anna@example.com", false}, {"lead@example.com", true}} {
		res, _ := doAs(t, ts, "POST", "/accounts/111111111111/budget", user.name, false, user.approver, form, false)
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("non-admin could change budget: %d", res.StatusCode)
		}
	}
	if prov.Tags("111111111111")["playplace:budget"] != "50" {
		t.Fatal("forbidden request mutated budget")
	}
	res, _ := do(t, ts, "POST", "/accounts/111111111111/budget", "admin@example.com", true, form, false)
	if res.StatusCode != http.StatusSeeOther || prov.Budgets["111111111111"] != 100 {
		t.Fatalf("admin budget change failed: %d", res.StatusCode)
	}
}

// With enforcement, a sync that creates a budget action and a budget increase
// that starts recovery are progress, not form errors.
func TestEnforcedSyncAndRecoveryAreNotErrors(t *testing.T) {
	prov := fake.New()
	prov.Now = func() time.Time { return t0 }
	prov.Seed(core.RemoteAccount{ProviderID: "111111111111", Name: "annas-box", Email: "a@example.com", JoinedAt: t0, Tags: map[string]string{
		core.TagManaged: "true", core.TagOwner: "anna@example.com", core.TagExpires: t0.Add(72 * time.Hour).Format(time.RFC3339), core.TagBudget: "50",
	}})
	cfg := core.DefaultConfig()
	cfg.InventoryTTL = 0
	cfg.BudgetPolicyID, cfg.BudgetActionRole, cfg.AlertEmail = "p-budget123", "arn:aws:iam::000000000000:role/Budgets", "ops@example.com"
	svc := core.NewService(prov, nil, cfg, nil)
	svc.Now = func() time.Time { return t0 }
	ts := httptest.NewServer(New(svc, slog.New(slog.DiscardHandler), headerAuth{}, nil, nil).Handler())
	t.Cleanup(ts.Close)

	res, body := do(t, ts, "POST", "/refresh", "admin@example.com", true, url.Values{}, true)
	if res.Header.Get("X-Form-Error") != "" || !strings.Contains(body, "setup-pending") {
		t.Fatalf("first sync with a new budget action failed: %s", body)
	}
	if res, body = do(t, ts, "POST", "/refresh", "admin@example.com", true, url.Values{}, true); res.Header.Get("X-Form-Error") != "" || !strings.Contains(body, "ready") {
		t.Fatalf("second sync did not observe protection: %s", body)
	}
	snap := prov.BudgetStates["111111111111"]
	snap.ActionStatus, snap.SpendKnown, snap.SpendUSD = "EXECUTION_SUCCESS", true, 60
	prov.BudgetStates["111111111111"] = snap
	res, body = do(t, ts, "POST", "/accounts/111111111111/budget", "admin@example.com", true, url.Values{"budget": {"100"}, "reason": {"more testing"}}, true)
	if res.Header.Get("X-Form-Error") != "" || res.Header.Get("HX-Redirect") != "/accounts/111111111111" {
		t.Fatalf("saved increase with recovery in progress shown as an error: %s", body)
	}
	if prov.Tags("111111111111")[core.TagBudgetHealth] != "recovering" {
		t.Fatalf("recovery did not start: %v", prov.Tags("111111111111"))
	}
}

func TestBudgetFormVisibilityAndValidation(t *testing.T) {
	ts, _ := newServer(t)
	_, body := do(t, ts, "GET", "/accounts/111111111111", "anna@example.com", false, nil, false)
	if strings.Contains(body, "Change monthly budget") {
		t.Fatal("owner sees admin budget form")
	}
	_, body = do(t, ts, "GET", "/accounts/111111111111", "admin@example.com", true, nil, false)
	if !strings.Contains(body, "Change monthly budget") {
		t.Fatal("missing admin form")
	}
	for _, amount := range []string{"NaN", "Inf", "-1", "0", "10.001", "600"} {
		res, _ := do(t, ts, "POST", "/accounts/111111111111/budget", "admin@example.com", true, url.Values{"budget": {amount}, "reason": {"test"}}, false)
		if res.StatusCode == http.StatusSeeOther {
			t.Fatalf("accepted invalid/unapproved budget %s", amount)
		}
	}
}

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
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

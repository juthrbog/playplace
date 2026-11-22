package web

import (
	"context"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
)

func TestAsyncClosureAndOverdueWarningStayVisible(t *testing.T) {
	ts, prov := newServer(t)
	prov.AsyncClose = true
	res, body := do(t, ts, "POST", "/accounts/111111111111/close", "ops@example.com", true, nil, true)
	if res.StatusCode != 200 || !strings.Contains(body, `status status-closing`) || strings.Contains(body, "status-overdue") || strings.Contains(body, ">overdue<") {
		t.Fatalf("pending closure missing, or overdue too early: %d %s", res.StatusCode, body)
	}
	r, err := prov.GetAccount(context.Background(), "111111111111")
	if err != nil {
		t.Fatal(err)
	}
	r.Tags[core.TagCloseRequested] = t0.Add(-25 * time.Hour).Format(time.RFC3339)
	prov.Seed(r)
	_, body = do(t, ts, "GET", "/accounts", "ops@example.com", true, nil, true)
	// An overdue closure keeps the word closing, and its box turns red. The
	// word "overdue" follows as a qualifier, so color never names the state
	// alone. The problem is in plain words, with the time in the UTC layout
	// of the pages and no AWS state.
	for _, want := range []string{"annas-box", "status status-closing status-overdue", `<span class="qualifier">overdue</span>`,
		"AWS has not confirmed the closure since 2026-09-14 11:00 UTC. Charges may continue."} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q: %s", want, body)
		}
	}
	// The loan card of the account page shows the same label, qualifier
	// and problem words under the name, as a loan card of the home page.
	_, body = do(t, ts, "GET", "/accounts/111111111111", "ops@example.com", true, nil, false)
	_, card, _ := strings.Cut(body, `<div class="loan-status">`)
	card, _, _ = strings.Cut(card, `<div class="loan-due">`)
	for _, want := range []string{"status status-closing status-overdue", `<span class="qualifier">overdue</span>`,
		"AWS has not confirmed the closure since 2026-09-14 11:00 UTC. Charges may continue."} {
		if !strings.Contains(card, want) {
			t.Errorf("the status of the account page lacks %q: %s", want, body)
		}
	}
	r.Status = "SUSPENDED"
	prov.Seed(r)
	_, body = do(t, ts, "GET", "/accounts", "ops@example.com", true, nil, true)
	if !strings.Contains(body, "annas-box") || !strings.Contains(body, "status status-unavailable") {
		t.Fatalf("suspended account must remain visible as unavailable: %s", body)
	}
	if strings.Contains(body, "provider state") || strings.Contains(body, "SUSPENDED") {
		t.Errorf("a row shows the raw AWS state: %s", body)
	}
}

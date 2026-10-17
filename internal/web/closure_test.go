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
	if res.StatusCode != 200 || !strings.Contains(body, `status status-closing`) || strings.Contains(body, "status-overdue") {
		t.Fatalf("pending closure missing, or overdue too early: %d %s", res.StatusCode, body)
	}
	r, err := prov.GetAccount(context.Background(), "111111111111")
	if err != nil {
		t.Fatal(err)
	}
	r.Tags[core.TagCloseRequested] = t0.Add(-25 * time.Hour).Format(time.RFC3339)
	prov.Seed(r)
	_, body = do(t, ts, "GET", "/accounts", "ops@example.com", true, nil, true)
	// An overdue closure keeps the word closing, and its box turns red.
	for _, want := range []string{"annas-box", "status status-closing status-overdue", "closure unconfirmed", "charges may continue"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q: %s", want, body)
		}
	}
	r.Status = "SUSPENDED"
	prov.Seed(r)
	_, body = do(t, ts, "GET", "/accounts", "ops@example.com", true, nil, true)
	if !strings.Contains(body, "annas-box") || !strings.Contains(body, "status status-unavailable") {
		t.Fatalf("suspended account must remain visible as unavailable: %s", body)
	}
}

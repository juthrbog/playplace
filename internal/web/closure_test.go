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
	// alone.
	for _, want := range []string{"annas-box", "status status-closing status-overdue", `<span class="qualifier">overdue</span>`, "closure unconfirmed", "charges may continue"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q: %s", want, body)
		}
	}
	// The account page head shows the same label and qualifier. One wrapper
	// holds both, so the qualifier wraps with the label and never lands
	// alone under the name.
	_, body = do(t, ts, "GET", "/accounts/111111111111", "ops@example.com", true, nil, false)
	_, head, _ := strings.Cut(body, `<span class="head-status">`)
	head, _, _ = strings.Cut(head, "</div>")
	if !strings.Contains(head, "status status-closing status-overdue") || !strings.HasSuffix(head, `<span class="qualifier">overdue</span></span>`) {
		t.Errorf("the account head lacks the overdue label and qualifier in one wrapper: %s", body)
	}
	r.Status = "SUSPENDED"
	prov.Seed(r)
	_, body = do(t, ts, "GET", "/accounts", "ops@example.com", true, nil, true)
	if !strings.Contains(body, "annas-box") || !strings.Contains(body, "status status-unavailable") {
		t.Fatalf("suspended account must remain visible as unavailable: %s", body)
	}
}

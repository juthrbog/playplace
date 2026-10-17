package web

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

// A creation that waits in the organization root carries a note after one
// hour. The account page shows it as a placement note, not as a failure:
// with a slow reconcile schedule, nothing tried the placement yet.
func TestUnplacedCreationPageShowsAPlacementNote(t *testing.T) {
	now := t0
	prov := fake.New()
	prov.Now = func() time.Time { return now }
	cfg := core.DefaultConfig()
	cfg.InventoryTTL = 0
	svc := core.NewService(prov, nil, cfg, nil)
	svc.Now = func() time.Time { return now }
	a, err := svc.Request(context.Background(), core.RequestInput{Name: "waiting", Owner: "anna@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	prov.Settle()
	now = t0.Add(2 * time.Hour)
	ts := httptest.NewServer(New(svc, slog.New(slog.DiscardHandler), headerAuth{}, nil, nil).Handler())
	t.Cleanup(ts.Close)

	res, body := do(t, ts, "GET", "/accounts/"+a.RequestID, "admin@example.com", true, nil, false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "<dt>placement</dt>") || !strings.Contains(body, "waits outside the Playground OU") {
		t.Fatalf("status %d, body: %s", res.StatusCode, body)
	}
	if strings.Contains(body, "<dt>failure</dt>") || !strings.Contains(body, `<dd class="qualifier">`) {
		t.Fatalf("the placement note is shown as a failure: %s", body)
	}
	// The list row shows the same qualifier under the creating label.
	res, body = do(t, ts, "GET", "/", "admin@example.com", true, nil, false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "status-creating") || !regexp.MustCompile(`class="qualifier">(?:<svg.*?</svg>)?<span>AWS created this account`).MatchString(body) {
		t.Fatalf("list row: status %d, body: %s", res.StatusCode, body)
	}
}

// An account that AWS has not created has no id and no root email. The facts
// say so in words, and an unchecked budget has no empty parenthesis.
func TestAccountFactsNameWhatIsMissing(t *testing.T) {
	for _, tc := range []struct {
		status core.Status
		word   string
	}{
		{core.StatusCreating, "not created yet"},
		{core.StatusFailed, "not created"},
	} {
		var buf bytes.Buffer
		a := &core.Account{ID: "x", Name: "x", Status: tc.status, CreatedAt: t0}
		if err := accountPage(page{}, a, nil, 0, historyView{}).Render(context.Background(), &buf); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		for _, fact := range []string{"account id", "root email"} {
			want := `<dt>` + fact + `</dt><dd><span class="muted">` + tc.word + `</span></dd>`
			if !strings.Contains(out, want) {
				t.Errorf("%s: missing %s", tc.status, want)
			}
		}
		if strings.Contains(out, "<code></code>") || strings.Contains(out, "last state observation") {
			t.Errorf("%s: a missing value shows as a blank:\n%s", tc.status, out)
		}
	}
}

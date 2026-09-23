package web

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"playplace/internal/audit"
	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

func journeyServer(t *testing.T, history History) *httptest.Server {
	t.Helper()
	p := fake.New()
	p.Now = func() time.Time { return t0 }
	p.Seed(core.RemoteAccount{ProviderID: "111111111111", Name: "demo", Status: "ACTIVE", JoinedAt: t0, Tags: map[string]string{core.TagManaged: "true", core.TagOwner: "bob@example.com", core.TagExpires: t0.Add(time.Hour).Format(time.RFC3339), core.TagBudget: "20", core.TagJourney: "new-journey"}})
	cfg := core.DefaultConfig()
	cfg.InventoryTTL = 0
	s := core.NewService(p, nil, cfg, nil)
	s.Now = func() time.Time { return t0 }
	ts := httptest.NewServer(New(s, slog.New(slog.DiscardHandler), headerAuth{}, nil, history).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestAccountHistoryNeverJoinsByReusedName(t *testing.T) {
	h := &audit.Memory{}
	for _, e := range []core.AuditEvent{
		{ID: "old", JourneyID: "old-journey", At: t0, Account: "demo", Owner: "alice@example.com", Event: "denied", Message: "ALICE-PRIVATE-REASON"},
		{At: t0, Account: "demo", Owner: "bob@example.com", Event: "requested", Message: "UNCORRELATED-LEGACY-REASON"},
		{ID: "current", JourneyID: "new-journey", At: t0, Account: "demo", Owner: "bob@example.com", Event: "requested", Message: "BOB-CURRENT-JOURNEY", Details: map[string]string{"purpose": "<script>bad()</script>"}},
		{ID: "previous-owner", JourneyID: "new-journey", At: t0.Add(-time.Minute), Account: "demo", Owner: "anna@example.com", Event: "requested", Message: "EARLIER-SAME-JOURNEY"},
	} {
		if err := h.Record(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	ts := journeyServer(t, h)
	_, body := do(t, ts, "GET", "/accounts/111111111111?journey_id=old-journey&owner=alice@example.com", "bob@example.com", false, nil, false)
	if strings.Contains(body, "ALICE-PRIVATE-REASON") || strings.Contains(body, "UNCORRELATED-LEGACY-REASON") {
		t.Fatal("history disclosed another journey or uncorrelated legacy data")
	}
	if !strings.Contains(body, "BOB-CURRENT-JOURNEY") || !strings.Contains(body, "EARLIER-SAME-JOURNEY") {
		t.Fatal("current owner should see the complete established journey")
	}
	if strings.Contains(body, "<script>bad()") || !strings.Contains(body, "Details") {
		t.Fatal("structured details must be escaped")
	}
	if res, _ := do(t, ts, "GET", "/accounts/111111111111", "alice@example.com", false, nil, false); res.StatusCode != 403 {
		t.Fatalf("another owner read account: %d", res.StatusCode)
	}
}

func TestAccountHistoryPaginatesAllEntriesAndReversesOrder(t *testing.T) {
	h := &audit.Memory{}
	for i := 1; i <= 51; i++ {
		if err := h.Record(context.Background(), core.AuditEvent{ID: fmt.Sprintf("%03d", i), JourneyID: "new-journey", At: t0, Event: "extended", Message: fmt.Sprintf("marker-%03d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	ts := journeyServer(t, h)
	_, body := do(t, ts, "GET", "/accounts/111111111111", "bob@example.com", false, nil, false)
	if !strings.Contains(body, "marker-051") || strings.Contains(body, "marker-001") {
		t.Fatal("first page should contain latest 50")
	}
	next := regexp.MustCompile(`href="([^"]+)">Next history page`).FindStringSubmatch(body)
	if len(next) != 2 {
		t.Fatalf("missing next-page link: %s", body)
	}
	_, body = do(t, ts, "GET", html.UnescapeString(next[1]), "bob@example.com", false, nil, false)
	if !strings.Contains(body, "marker-001") || strings.Contains(body, "marker-051") || strings.Contains(body, "Next history page") {
		t.Fatal("last page must expose remaining event without repeating earlier entries")
	}
	_, body = do(t, ts, "GET", "/accounts/111111111111?history_order=oldest", "bob@example.com", false, nil, false)
	if !strings.Contains(body, "marker-001") || strings.Contains(body, "marker-051") {
		t.Fatal("oldest-first query did not reverse pagination")
	}
	_, body = do(t, ts, "GET", "/accounts/111111111111?history_cursor=bad", "bob@example.com", false, nil, false)
	if !strings.Contains(body, "Invalid history cursor") || strings.Contains(body, "Nothing recorded") {
		t.Fatal("invalid cursor should not be presented as empty history")
	}
}

type unavailableHistory struct{}

func (unavailableHistory) Where() string { return "test" }
func (unavailableHistory) Search(context.Context, audit.Filter) (audit.Page, error) {
	return audit.Page{}, errors.New("private backend details")
}

func TestAccountHistoryFailureIsNotEmpty(t *testing.T) {
	ts := journeyServer(t, unavailableHistory{})
	_, body := do(t, ts, "GET", "/accounts/111111111111", "bob@example.com", false, nil, false)
	if !strings.Contains(body, "History is unavailable") || strings.Contains(body, "Nothing recorded") || strings.Contains(body, "private backend details") {
		t.Fatal("history failure must be explicit without leaking backend details")
	}
}

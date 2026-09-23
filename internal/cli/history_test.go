package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"playplace/internal/audit"
	"playplace/internal/core"
)

func TestHistoryOutputIncludesPaginationAndIdentity(t *testing.T) {
	h := &audit.Memory{}
	at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"a", "b", "c"} {
		if err := h.Record(context.Background(), core.AuditEvent{ID: id, JourneyID: "journey", At: at, Event: "requested", Account: "demo", Actor: "alice", Message: id, Details: map[string]string{"purpose": "demo"}}); err != nil {
			t.Fatal(err)
		}
	}
	var out, warnings bytes.Buffer
	f := audit.Filter{JourneyID: "journey", Limit: 2}
	if err := printHistory(context.Background(), &out, &warnings, h, f, true); err != nil {
		t.Fatal(err)
	}
	var p audit.Page
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Events) != 2 || p.Events[0].ID != "c" || p.Next == "" || p.Events[0].Details["purpose"] != "demo" {
		t.Fatalf("json=%s", out.String())
	}
	f.Cursor = p.Next
	out.Reset()
	if err := printHistory(context.Background(), &out, &warnings, h, f, true); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Events) != 1 || p.Events[0].ID != "a" {
		t.Fatalf("last page=%s", out.String())
	}
	f.Cursor = ""
	out.Reset()
	warnings.Reset()
	if err := printHistory(context.Background(), &out, &warnings, h, f, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "JOURNEY") || !strings.Contains(out.String(), "journey") || !strings.Contains(warnings.String(), "--cursor") {
		t.Fatalf("text=%s warnings=%s", out.String(), warnings.String())
	}
}

func TestHistoryRelativeDatesResolveForContinuation(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 123, time.UTC)
	since, err := historyTime("30d", now)
	if err != nil || !since.Equal(now.Add(-30*24*time.Hour)) {
		t.Fatalf("relative=%v %v", since, err)
	}
	absolute, err := historyTime(since.Format(time.RFC3339Nano), now.Add(time.Hour))
	if err != nil || !absolute.Equal(since) {
		t.Fatalf("absolute=%v %v", absolute, err)
	}
	for _, value := range []string{"bad", "-1d", "0"} {
		if _, err := historyTime(value, now); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}

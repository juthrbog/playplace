package audit

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"playplace/internal/core"
)

func TestFileSinkRoundTripAndFilters(t *testing.T) {
	ctx := context.Background()
	f := &File{Path: filepath.Join(t.TempDir(), "sub", "history.jsonl")}
	t0 := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	events := []core.AuditEvent{
		{At: t0, Event: "requested", Account: "a-one", Owner: "anna@example.com", Actor: "anna@example.com", Via: "web", Message: "anna requested a-one"},
		{At: t0.Add(time.Minute), Event: "approved", Account: "a-one", Owner: "anna@example.com", Actor: "lead@example.com", Message: "lead approved a-one"},
		{At: t0.Add(2 * time.Minute), Event: "requested", Account: "b-two", Owner: "bob@example.com", Actor: "bob@example.com", Message: "bob requested b-two", Details: map[string]string{"budget": "50"}},
	}
	for _, e := range events {
		if err := f.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	all, err := f.Query(ctx, Filter{})
	if err != nil || len(all) != 3 || all[0].Account != "b-two" {
		t.Fatalf("all = %+v %v (newest first expected)", all, err)
	}
	byAcct, _ := f.Query(ctx, Filter{Account: "A-ONE"})
	if len(byAcct) != 2 || byAcct[0].Event != "approved" {
		t.Fatalf("by account = %+v", byAcct)
	}
	byOwner, _ := f.Query(ctx, Filter{Owner: "bob@example.com"})
	if len(byOwner) != 1 || byOwner[0].Details["budget"] != "50" {
		t.Fatalf("by owner = %+v", byOwner)
	}
	since, _ := f.Query(ctx, Filter{Since: t0.Add(90 * time.Second)})
	if len(since) != 1 {
		t.Fatalf("since = %+v", since)
	}
	limited, _ := f.Query(ctx, Filter{Limit: 1})
	if len(limited) != 1 {
		t.Fatalf("limit = %+v", limited)
	}
	empty := &File{Path: filepath.Join(t.TempDir(), "none.jsonl")}
	if got, err := empty.Query(ctx, Filter{}); err != nil || len(got) != 0 {
		t.Fatalf("missing file should be empty, got %v %v", got, err)
	}
}

func TestFileSinkSkipsTornLines(t *testing.T) {
	ctx := context.Background()
	f := &File{Path: filepath.Join(t.TempDir(), "history.jsonl")}
	t0 := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	if err := f.Record(ctx, core.AuditEvent{At: t0, Event: "requested", Account: "a-one", Actor: "anna"}); err != nil {
		t.Fatal(err)
	}
	// A write cut short by a full disk or a kill leaves a partial line.
	fh, _ := os.OpenFile(f.Path, os.O_APPEND|os.O_WRONLY, 0o600)
	fh.WriteString(`{"ts":"2026-09-16T12:01:00Z","event":"appr`)
	fh.Close()
	if err := f.Record(ctx, core.AuditEvent{At: t0.Add(2 * time.Minute), Event: "denied", Account: "a-one", Actor: "lead"}); err != nil {
		t.Fatal(err)
	}
	got, err := f.Query(ctx, Filter{})
	if err != nil || len(got) != 2 || got[0].Event != "denied" || got[1].Event != "requested" {
		t.Fatalf("torn line should be skipped, got %+v %v", got, err)
	}
}

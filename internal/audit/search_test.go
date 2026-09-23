package audit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"playplace/internal/core"
)

func TestSearchFiltersAndPagination(t *testing.T) {
	for _, kind := range []string{"file", "memory"} {
		t.Run(kind, func(t *testing.T) {
			var sink interface {
				Searcher
				core.Auditor
			}
			if kind == "file" {
				sink = &File{Path: filepath.Join(t.TempDir(), "history.jsonl")}
			} else {
				sink = &Memory{}
			}
			ctx := context.Background()
			at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
			for _, e := range []core.AuditEvent{
				{ID: "a", JourneyID: "old", At: at, Event: "requested", Account: "demo", Owner: "alice", Actor: "alice"},
				{ID: "b", JourneyID: "new", At: at, Event: "requested", Account: "demo", Owner: "bob", Actor: "bob"},
				{ID: "c", JourneyID: "new", AccountID: "123", At: at.Add(time.Minute), Event: "approved", Account: "demo", Owner: "bob", Actor: "lead"},
				{ID: "d", JourneyID: "new", AccountID: "123", At: at.Add(time.Minute), Event: "extended", Account: "demo", Owner: "bob", Actor: "bob"},
			} {
				if err := sink.Record(ctx, e); err != nil {
					t.Fatal(err)
				}
			}
			for _, tt := range []struct {
				name string
				f    Filter
				want []string
			}{
				{"journey", Filter{JourneyID: "new"}, []string{"d", "c", "b"}},
				{"name", Filter{Account: "DEMO"}, []string{"d", "c", "b", "a"}},
				{"account ID", Filter{AccountID: "123"}, []string{"d", "c"}},
				{"actor", Filter{Actor: "LEAD"}, []string{"c"}},
				{"owner", Filter{Owner: "ALICE"}, []string{"a"}},
				{"event", Filter{Event: "requested"}, []string{"b", "a"}},
				{"range", Filter{Since: at, Until: at.Add(time.Minute)}, []string{"b", "a"}},
				{"AND", Filter{Owner: "bob", Actor: "lead", Event: "approved"}, []string{"c"}},
				{"oldest", Filter{JourneyID: "new", OldestFirst: true}, []string{"b", "c", "d"}},
			} {
				t.Run(tt.name, func(t *testing.T) {
					p, err := sink.Search(ctx, tt.f)
					if err != nil {
						t.Fatal(err)
					}
					var ids []string
					for _, e := range p.Events {
						ids = append(ids, e.ID)
					}
					if !reflect.DeepEqual(ids, tt.want) {
						t.Fatalf("got %v, want %v", ids, tt.want)
					}
				})
			}
			for _, oldest := range []bool{false, true} {
				f := Filter{JourneyID: "new", Limit: 1, OldestFirst: oldest}
				var ids []string
				for i := 0; ; i++ {
					if i > 5 {
						t.Fatal("pagination did not terminate")
					}
					p, err := sink.Search(ctx, f)
					if err != nil {
						t.Fatal(err)
					}
					for _, e := range p.Events {
						ids = append(ids, e.ID)
					}
					if p.Next == "" {
						break
					}
					f.Cursor = p.Next
				}
				want := []string{"d", "c", "b"}
				if oldest {
					want = []string{"b", "c", "d"}
				}
				if !reflect.DeepEqual(ids, want) {
					t.Fatalf("pages=%v want=%v", ids, want)
				}
			}
			p, err := sink.Search(ctx, Filter{JourneyID: "new", Limit: 1})
			if err != nil || p.Next == "" {
				t.Fatalf("first page=%+v %v", p, err)
			}
			if _, err := sink.Search(ctx, Filter{JourneyID: "old", Limit: 1, Cursor: p.Next}); !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("cross-query cursor=%v", err)
			}
			if err := sink.Record(ctx, core.AuditEvent{ID: "e", JourneyID: "new", At: at.Add(2 * time.Minute), Event: "closed"}); err != nil {
				t.Fatal(err)
			}
			next, err := sink.Search(ctx, Filter{JourneyID: "new", Limit: 1, Cursor: p.Next})
			if err != nil || len(next.Events) != 1 || next.Events[0].ID != "c" {
				t.Fatalf("new write shifted cursor: %+v %v", next, err)
			}
		})
	}
}

func TestSearchRejectsInvalidQueriesAndCancellation(t *testing.T) {
	f := &File{Path: filepath.Join(t.TempDir(), "absent")}
	for _, query := range []Filter{{Cursor: "garbage"}, {Limit: -1}, {Limit: 501}, {Since: time.Now(), Until: time.Now().Add(-time.Hour)}} {
		if _, err := f.Search(context.Background(), query); err == nil {
			t.Fatalf("accepted invalid filter %+v", query)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Search(ctx, Filter{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
}

func TestSearchReportsDamageAndDoesNotAssociateLegacyByName(t *testing.T) {
	f := &File{Path: filepath.Join(t.TempDir(), "history.jsonl")}
	ctx := context.Background()
	at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	legacy := core.AuditEvent{At: at, Account: "demo", Event: "approved", Message: "legacy"}
	for i := 0; i < 2; i++ {
		if err := f.Record(ctx, legacy); err != nil {
			t.Fatal(err)
		}
	}
	fh, err := os.OpenFile(f.Path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fh.WriteString("{}\n{torn\n"); err != nil {
		t.Fatal(err)
	}
	fh.Close()
	p, err := f.Search(ctx, Filter{JourneyID: "new"})
	if err != nil || !p.Incomplete || len(p.Events) != 0 {
		t.Fatalf("scoped damaged page=%+v %v", p, err)
	}
	first, err := f.Search(ctx, Filter{Account: "demo", Limit: 1})
	if err != nil || len(first.Events) != 1 || first.Next == "" {
		t.Fatalf("first=%+v %v", first, err)
	}
	second, err := f.Search(ctx, Filter{Account: "demo", Limit: 1, Cursor: first.Next})
	if err != nil || len(second.Events) != 1 || second.Next != "" {
		t.Fatalf("second=%+v %v", second, err)
	}
}

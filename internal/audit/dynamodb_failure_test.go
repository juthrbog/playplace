package audit

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
)

func TestDynamoDBMissingCorruptUnavailable(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (*DynamoDB, *memoryDynamo, Journey, core.AuditEvent) {
		m := newMemoryDynamo()
		d := testStore(t, m, "history", Namespace{"org", "dev"})
		j := journey("journey", OriginDirect)
		j.Correlations = []Correlation{{CorrelationProviderRequest, "car-1"}}
		j = saveJourney(t, d, j)
		e := core.AuditEvent{ID: "event", JourneyID: j.ID, At: historyStart, Event: "failed"}
		if err := d.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
		return d, m, j, e
	}
	t.Run("missing metadata and dangling links", func(t *testing.T) {
		d, m, j, _ := setup(t)
		delete(m.rows, rowID(d.table, d.journeyKey(j.ID)))
		if _, err := d.GetJourney(ctx, j.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing: %v", err)
		}
		if _, err := d.ResolveJourney(ctx, j.Correlations[0]); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("dangling: %v", err)
		}
		p, err := d.Search(ctx, Filter{JourneyID: j.ID})
		if !errors.Is(err, ErrNotFound) || len(p.Events) != 0 {
			t.Fatalf("missing metadata looked like valid history: %+v %v", p, err)
		}
	})
	for _, field := range []string{"data", "schema", "revision", "PK"} {
		t.Run("corrupt metadata "+field, func(t *testing.T) {
			d, m, j, _ := setup(t)
			m.rows[rowID(d.table, d.journeyKey(j.ID))][field] = str("broken")
			if _, err := d.GetJourney(ctx, j.ID); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("corrupt: %v", err)
			}
		})
	}
	t.Run("unconfirmed correlation", func(t *testing.T) {
		d, m, j, _ := setup(t)
		saveJourney(t, d, journey("unrelated", OriginDirect))
		m.rows[rowID(d.table, d.correlationKey(j.Correlations[0]))]["data"] = str("unrelated")
		if _, err := d.ResolveJourney(ctx, j.Correlations[0]); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("unconfirmed: %v", err)
		}
	})
	for _, field := range []string{"data", "schema", "SK", "PK"} {
		t.Run("corrupt search row "+field, func(t *testing.T) {
			d, m, _, e := setup(t)
			k := key(d.index("all", ""), eventSortKey(e.At, e.ID))
			if field == "PK" {
				// An injected row still in this namespace's query but carrying
				// a different event ID must fail key/payload integrity checks.
				m.rows[rowID(d.table, k)]["data"] = str(`{"id":"other","journey_id":"journey","ts":"2026-09-15T12:00:00Z","event":"failed"}`)
			} else if field == "SK" {
				m.rows[rowID(d.table, k)][field] = str(eventSortKey(e.At, "wrong-id"))
			} else {
				m.rows[rowID(d.table, k)][field] = str("broken")
			}
			p, err := d.Search(ctx, Filter{})
			if !errors.Is(err, ErrCorrupt) || len(p.Events) != 0 {
				t.Fatalf("corrupt query: %+v %v", p, err)
			}
		})
	}
	t.Run("unavailable is not empty or missing", func(t *testing.T) {
		d, m, j, e := setup(t)
		outage := errors.New("offline")
		m.failure = outage
		if _, err := d.GetJourney(ctx, j.ID); !errors.Is(err, ErrUnavailable) || !errors.Is(err, outage) {
			t.Fatal(err)
		}
		if _, err := d.ResolveJourney(ctx, j.Correlations[0]); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		if _, err := d.PutJourney(ctx, journey("new", OriginRequest)); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		if err := d.Record(ctx, e); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		p, err := d.Search(ctx, Filter{})
		if !errors.Is(err, ErrUnavailable) || len(p.Events) != 0 || p.Next != "" {
			t.Fatalf("outage: %+v %v", p, err)
		}
		m.failure = nil
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := d.Search(canceled, Filter{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	})
	t.Run("query failure after readable candidates returns no partial success", func(t *testing.T) {
		d, m, _, e := setup(t)
		for i := range 20 {
			e.ID = core.NewHistoryID()
			e.At = historyStart.Add(time.Duration(i) * time.Second)
			if err := d.Record(ctx, e); err != nil {
				t.Fatal(err)
			}
		}
		m.queryFailureAfter = 1
		p, err := d.Search(ctx, Filter{Limit: 100})
		if !errors.Is(err, ErrUnavailable) || len(p.Events) != 0 || p.Next != "" {
			t.Fatalf("partial query looked complete: %+v %v", p, err)
		}
	})
	t.Run("lost write responses can be resolved without producer disk", func(t *testing.T) {
		d, m, _, _ := setup(t)
		j := journey("new", OriginDirect)
		j.Correlations = []Correlation{{CorrelationOperation, "operation"}}
		m.loseWriteResponse = true
		if _, err := d.PutJourney(ctx, j); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("ambiguous write: %v", err)
		}
		restarted := testStore(t, m, d.table, Namespace{"org", "dev"})
		j, err := restarted.ResolveJourney(ctx, j.Correlations[0])
		if err != nil || j.Revision != 1 {
			t.Fatalf("lost response metadata: %+v %v", j, err)
		}
		e := core.AuditEvent{ID: "lost-response", JourneyID: j.ID, At: historyStart, Event: "created"}
		m.loseWriteResponse = true
		if err := restarted.Record(ctx, e); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		if err := restarted.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
		p, err := restarted.Search(ctx, Filter{JourneyID: j.ID})
		if err != nil || len(p.Events) != 1 {
			t.Fatalf("retry duplicate: %+v %v", p, err)
		}
	})
}

func TestDynamoDBRejectsInvalidEvidence(t *testing.T) {
	ctx := context.Background()
	for _, ns := range []Namespace{{}, {Organization: "org"}, {Environment: "dev"}} {
		if _, err := NewDynamoDB(newMemoryDynamo(), "history", ns); err == nil {
			t.Fatalf("accepted namespace %+v", ns)
		}
	}
	mutations := map[string]func(*Journey){
		"missing identity":        func(j *Journey) { j.ID = "" },
		"unknown origin":          func(j *Journey) { j.Origin = "name-match" },
		"missing start":           func(j *Journey) { j.StartedAt = time.Time{} },
		"duplicate correlation":   func(j *Journey) { j.Correlations = []Correlation{{CorrelationAccount, "a"}, {CorrelationAccount, "a"}} },
		"two accounts":            func(j *Journey) { j.Correlations = []Correlation{{CorrelationAccount, "a"}, {CorrelationAccount, "b"}} },
		"name correlation":        func(j *Journey) { j.Correlations = []Correlation{{"name", j.Name}} },
		"self reported ownership": func(j *Journey) { j.Ownership = []OwnershipObservation{{historyStart, "owner", "cli-label", j.ID}} },
		"uncorrelated ownership": func(j *Journey) {
			j.Ownership = []OwnershipObservation{{historyStart, "owner", "provider-tags", "account"}}
		},
		"backdated ownership": func(j *Journey) {
			j.Ownership = []OwnershipObservation{{historyStart.Add(-time.Hour), "owner", "request-queue", j.ID}}
		},
		"mismatched request": func(j *Journey) {
			j.Ownership = []OwnershipObservation{{historyStart, "owner", "request-queue", "same-name"}}
		},
		"closure intent":       func(j *Journey) { j.Terminal = &TerminalOutcome{"close-requested", historyStart, "account"} },
		"account expiry":       func(j *Journey) { j.Terminal = &TerminalOutcome{"account-expired", historyStart, "account"} },
		"provisioning failure": func(j *Journey) { j.Terminal = &TerminalOutcome{"failed", historyStart, j.ID} },
		"uncorrelated closure": func(j *Journey) { j.Terminal = &TerminalOutcome{"account-closed", historyStart, "account"} },
		"fulfilled request": func(j *Journey) {
			j.Correlations = []Correlation{{CorrelationAccount, "account"}}
			j.Terminal = &TerminalOutcome{"request-expired", historyStart, j.ID}
		},
		"backdated terminal": func(j *Journey) { j.Terminal = &TerminalOutcome{"request-expired", historyStart.Add(-time.Hour), j.ID} },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			m := newMemoryDynamo()
			d := testStore(t, m, "history", Namespace{"org", "test"})
			j := journey("id", OriginRequest)
			mutate(&j)
			if _, err := d.PutJourney(ctx, j); err == nil {
				t.Fatal("accepted invalid evidence")
			}
			if len(m.rows) != 0 {
				t.Fatal("invalid evidence wrote rows")
			}
		})
	}
	t.Run("immutable accepted evidence", func(t *testing.T) {
		d := testStore(t, newMemoryDynamo(), "history", Namespace{"org", "test"})
		j := journey("id", OriginRequest)
		j.Ownership = []OwnershipObservation{{historyStart, "owner", "request-queue", j.ID}}
		j.Terminal = &TerminalOutcome{"request-denied", historyStart, j.ID}
		j = saveJourney(t, d, j)
		for name, mutate := range map[string]func(*Journey){
			"origin": func(j *Journey) { j.Origin = OriginDirect; j.Ownership = nil; j.Terminal = nil },
			"ownership": func(j *Journey) {
				j.Ownership = []OwnershipObservation{{historyStart, "different", "request-queue", j.ID}}
			},
			"terminal":          func(j *Journey) { j.Terminal = nil },
			"terminal identity": func(j *Journey) { j.Correlations = []Correlation{{CorrelationProviderRequest, "new-request"}} },
		} {
			t.Run(name, func(t *testing.T) {
				x := j
				mutate(&x)
				if _, err := d.PutJourney(ctx, x); !errors.Is(err, ErrConflict) {
					t.Fatalf("changed immutable evidence: %v", err)
				}
			})
		}
	})
	t.Run("document and transaction bounds", func(t *testing.T) {
		d := testStore(t, newMemoryDynamo(), "history", Namespace{"org", "test"})
		j := journey("id", OriginRequest)
		for range 91 {
			j.Correlations = append(j.Correlations, Correlation{CorrelationOperation, core.NewHistoryID()})
		}
		if _, err := d.PutJourney(ctx, j); err == nil {
			t.Fatal("accepted oversized transaction")
		}
		j.Correlations = nil
		saveJourney(t, d, j)
		e := core.AuditEvent{ID: "event", JourneyID: j.ID, At: historyStart, Event: "created", Message: strings.Repeat("x", maxHistoryDocument)}
		if err := d.Record(ctx, e); err == nil {
			t.Fatal("accepted oversized document")
		}
		for range 1000 {
			j.Ownership = append(j.Ownership, OwnershipObservation{historyStart, "owner", "request-queue", j.ID})
		}
		j.ID = "new"
		for i := range j.Ownership {
			j.Ownership[i].Reference = j.ID
		}
		if _, err := d.PutJourney(ctx, j); err == nil {
			t.Fatal("truncated oversized ownership evidence")
		}
	})
}

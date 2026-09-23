package audit

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"playplace/internal/core"
)

// memoryDynamo is deliberately limited to the adapter's SDK operations. The
// same behavioral contract also runs against DynamoDB Local to validate real
// expression/transaction semantics, which this fake cannot establish.
type memoryDynamo struct {
	mu                sync.Mutex
	rows              map[string]item
	failure           error
	queryFailureAfter int
	queries           int
	loseWriteResponse bool
}

func newMemoryDynamo() *memoryDynamo { return &memoryDynamo{rows: map[string]item{}} }
func rowID(table string, k item) string {
	return table + "\x00" + text(k, "PK") + "\x00" + text(k, "SK")
}
func copyItem(m item) item {
	out := item{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (m *memoryDynamo) GetItem(ctx context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.failure != nil {
		return nil, m.failure
	}
	if !aws.ToBool(in.ConsistentRead) {
		return nil, errors.New("expected strong read")
	}
	return &dynamodb.GetItemOutput{Item: copyItem(m.rows[rowID(aws.ToString(in.TableName), in.Key)])}, nil
}

func (m *memoryDynamo) TransactWriteItems(ctx context.Context, in *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.failure != nil {
		return nil, m.failure
	}
	if len(in.TransactItems) > 100 {
		return nil, errors.New("transaction too large")
	}
	seen := map[string]bool{}
	for _, w := range in.TransactItems {
		var k, values item
		var table, condition string
		if w.Put != nil {
			k, values, table, condition = w.Put.Item, w.Put.ExpressionAttributeValues, aws.ToString(w.Put.TableName), aws.ToString(w.Put.ConditionExpression)
		}
		if w.ConditionCheck != nil {
			k, values, table, condition = w.ConditionCheck.Key, w.ConditionCheck.ExpressionAttributeValues, aws.ToString(w.ConditionCheck.TableName), aws.ToString(w.ConditionCheck.ConditionExpression)
		}
		id := rowID(table, k)
		if seen[id] {
			return nil, errors.New("duplicate transaction key")
		}
		seen[id] = true
		old := m.rows[id]
		ok := false
		switch condition {
		case "attribute_not_exists(PK)":
			ok = len(old) == 0
		case "revision = :revision":
			ok = old != nil && reflect.DeepEqual(old["revision"], values[":revision"])
		case "attribute_not_exists(PK) OR #data = :journey":
			ok = len(old) == 0 || text(old, "data") == text(values, ":journey")
		default:
			return nil, fmt.Errorf("unsupported test condition %q", condition)
		}
		if !ok {
			return nil, &types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("ConditionalCheckFailed")}}}
		}
	}
	for _, w := range in.TransactItems {
		if w.Put != nil {
			m.rows[rowID(aws.ToString(w.Put.TableName), w.Put.Item)] = copyItem(w.Put.Item)
		}
	}
	if m.loseWriteResponse {
		m.loseWriteResponse = false
		return nil, errors.New("connection lost after commit")
	}
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

func (m *memoryDynamo) Query(ctx context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.queries++
	if m.failure != nil {
		return nil, m.failure
	}
	if m.queryFailureAfter > 0 && m.queries > m.queryFailureAfter {
		return nil, errors.New("query connection lost")
	}
	if !aws.ToBool(in.ConsistentRead) || in.IndexName != nil || in.FilterExpression != nil || aws.ToString(in.KeyConditionExpression) != "PK = :pk AND SK BETWEEN :lo AND :hi" {
		return nil, errors.New("unexpected query strategy")
	}
	pk, lo, hi := text(in.ExpressionAttributeValues, ":pk"), text(in.ExpressionAttributeValues, ":lo"), text(in.ExpressionAttributeValues, ":hi")
	start := text(in.ExclusiveStartKey, "SK")
	forward := aws.ToBool(in.ScanIndexForward)
	var rows []item
	for id, row := range m.rows {
		sk := text(row, "SK")
		if !strings.HasPrefix(id, aws.ToString(in.TableName)+"\x00") || text(row, "PK") != pk || sk < lo || sk > hi {
			continue
		}
		if start != "" && ((forward && sk <= start) || (!forward && sk >= start)) {
			continue
		}
		rows = append(rows, copyItem(row))
	}
	sort.Slice(rows, func(i, j int) bool {
		if forward {
			return text(rows[i], "SK") < text(rows[j], "SK")
		}
		return text(rows[i], "SK") > text(rows[j], "SK")
	})
	// Small provider pages exercise continuation even when every item on a
	// page fails the residual filters. Real DynamoDB also pages by byte size.
	n := min(len(rows), int(aws.ToInt32(in.Limit)), 7)
	out := &dynamodb.QueryOutput{Items: rows[:n]}
	if n < len(rows) {
		out.LastEvaluatedKey = key(pk, text(rows[n-1], "SK"))
	}
	return out, nil
}

var historyStart = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

func testStore(t *testing.T, api DynamoDBAPI, table string, ns Namespace) *DynamoDB {
	t.Helper()
	d, err := NewDynamoDB(api, table, ns)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func saveJourney(t *testing.T, d *DynamoDB, j Journey) Journey {
	t.Helper()
	got, err := d.PutJourney(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func journey(id string, origin JourneyOrigin) Journey {
	return Journey{ID: id, Name: "reused", Origin: origin, StartedAt: historyStart}
}

func TestDynamoDBContract(t *testing.T) { dynamoContract(t, newMemoryDynamo(), "history") }

func dynamoContract(t *testing.T, api DynamoDBAPI, table string) {
	ctx := context.Background()
	fresh := func(t *testing.T) (*DynamoDB, Namespace) {
		ns := Namespace{Organization: core.NewHistoryID(), Environment: "test"}
		return testStore(t, api, table, ns), ns
	}
	t.Run("restart exact joins and isolation", func(t *testing.T) {
		d, ns := fresh(t)
		j := journey("direct", OriginDirect)
		j.Correlations = []Correlation{{CorrelationOperation, "op-1"}}
		j = saveJourney(t, d, j)
		// A returned provider request ID is bound after AWS accepts creation.
		j.Correlations = append(j.Correlations, Correlation{CorrelationProviderRequest, "car-1"})
		j = saveJourney(t, d, j)
		other := journey("another-direct", OriginDirect)
		other.Correlations = []Correlation{{CorrelationProviderRequest, "car-2"}}
		saveJourney(t, d, other) // identical label and start time must not join
		failed := core.AuditEvent{ID: "failed-direct", JourneyID: j.ID, At: historyStart, Event: "failed", Account: j.Name}
		if err := d.Record(ctx, failed); err != nil {
			t.Fatal(err)
		}
		restarted := testStore(t, api, table, ns) // no producer memory or disk
		for _, c := range j.Correlations {
			got, err := restarted.ResolveJourney(ctx, c)
			if err != nil || !reflect.DeepEqual(got, j) {
				t.Fatalf("restart: %+v %v", got, err)
			}
		}
		p, err := restarted.Search(ctx, Filter{JourneyID: j.ID})
		if err != nil || len(p.Events) != 1 || p.Events[0].Event != "failed" || j.Terminal != nil {
			t.Fatalf("failed direct journey: %+v %v", p, err)
		}
		if _, err := restarted.ResolveJourney(ctx, Correlation{CorrelationProviderRequest, "reused"}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("name joined: %v", err)
		}
		if _, err := restarted.ResolveJourney(ctx, Correlation{"name", "reused"}); err == nil {
			t.Fatal("name accepted as correlation")
		}
		for _, isolated := range []Namespace{{ns.Organization, "production"}, {ns.Organization + "-other", ns.Environment}} {
			s := testStore(t, api, table, isolated)
			if _, err := s.ResolveJourney(ctx, j.Correlations[0]); !errors.Is(err, ErrNotFound) {
				t.Fatalf("cross namespace join: %v", err)
			}
			x := journey(j.ID, OriginDirect)
			x.Correlations = j.Correlations
			saveJourney(t, s, x) // same exact identities may exist in another deployment
			p, err := s.Search(ctx, Filter{})
			if err != nil || len(p.Events) != 0 {
				t.Fatalf("isolated query: %+v %v", p, err)
			}
			isolatedEvent := failed
			isolatedEvent.Message = "another deployment"
			if err := s.Record(ctx, isolatedEvent); err != nil {
				t.Fatalf("event identity was not namespaced: %v", err)
			}
			p, err = restarted.Search(ctx, Filter{})
			if err != nil || len(p.Events) != 1 || p.Events[0].Message != "" {
				t.Fatalf("cross-deployment events: %+v %v", p, err)
			}
		}
		// Delimiter-bearing namespace values cannot alias another namespace.
		x := testStore(t, api, table, Namespace{ns.Organization + "|test", "a"})
		y := testStore(t, api, table, Namespace{ns.Organization, "test|a"})
		saveJourney(t, x, journey("id", OriginAdoption))
		if _, err := y.GetJourney(ctx, "id"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("namespace key collision: %v", err)
		}
	})
	t.Run("archived requests and ownership evidence", func(t *testing.T) {
		d, _ := fresh(t)
		for _, terminal := range []string{"request-denied", "request-withdrawn", "request-expired"} {
			j := journey(terminal, OriginRequest)
			j.Ownership = []OwnershipObservation{{historyStart, "first", "request-queue", j.ID}}
			j = saveJourney(t, d, j)
			j.Ownership = append(j.Ownership, OwnershipObservation{historyStart.Add(time.Hour), "second", "request-queue", j.ID})
			j.Terminal = &TerminalOutcome{terminal, historyStart.Add(2 * time.Hour), j.ID}
			j = saveJourney(t, d, j)
			if err := d.Record(ctx, core.AuditEvent{ID: terminal, JourneyID: j.ID, At: j.Terminal.At, Event: terminal, Account: j.Name, Owner: "self-reported"}); err != nil {
				t.Fatal(err)
			}
			got, err := d.GetJourney(ctx, j.ID)
			if err != nil || !reflect.DeepEqual(got, j) {
				t.Fatalf("archived: %+v %v", got, err)
			}
			p, err := d.Search(ctx, Filter{JourneyID: j.ID})
			if err != nil || len(p.Events) != 1 || p.Incomplete {
				t.Fatalf("archived history: %+v %v", p, err)
			}
		}
		j := journey("account", OriginAdoption)
		j.Correlations = []Correlation{{CorrelationAccount, "123456789012"}}
		j.Ownership = []OwnershipObservation{{historyStart, "owner", "provider-tags", "123456789012"}}
		j = saveJourney(t, d, j)
		if j.Terminal != nil {
			t.Fatal("missing inventory must not manufacture termination")
		}
		j.Ownership = append(j.Ownership, OwnershipObservation{historyStart.Add(time.Hour), "", "provider-tags", "123456789012"})
		j.Terminal = &TerminalOutcome{"account-closed", historyStart.Add(2 * time.Hour), "123456789012"}
		j = saveJourney(t, d, j)
		got, err := d.ResolveJourney(ctx, j.Correlations[0])
		if err != nil || !reflect.DeepEqual(got, j) {
			t.Fatalf("archived account: %+v %v", got, err)
		}
	})
	t.Run("request to account evidence survives queue removal", func(t *testing.T) {
		d, ns := fresh(t)
		j := journey("request", OriginRequest)
		j.Ownership = []OwnershipObservation{{historyStart, "first", "request-queue", j.ID}}
		j = saveJourney(t, d, j)
		j.Correlations = []Correlation{{CorrelationOperation, "approve-op"}, {CorrelationProviderRequest, "car-approved"}, {CorrelationAccount, "account"}}
		j.Ownership = append(j.Ownership, OwnershipObservation{historyStart.Add(time.Hour), "second", "provider-tags", "account"})
		j = saveJourney(t, d, j)
		restarted := testStore(t, api, table, ns)
		for _, c := range j.Correlations {
			got, err := restarted.ResolveJourney(ctx, c)
			if err != nil || !reflect.DeepEqual(got, j) {
				t.Fatalf("lost request-account linkage: %+v %v", got, err)
			}
		}
	})
	t.Run("conditional identity and revision writes", func(t *testing.T) {
		d, _ := fresh(t)
		j := journey("original", OriginDirect)
		j.Correlations = []Correlation{{CorrelationProviderRequest, "car-1"}}
		j = saveJourney(t, d, j)
		impostor := journey("impostor", OriginDirect)
		impostor.Correlations = j.Correlations
		if _, err := d.PutJourney(ctx, impostor); !errors.Is(err, ErrConflict) {
			t.Fatalf("reassigned request: %v", err)
		}
		if _, err := d.GetJourney(ctx, impostor.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("partial metadata committed: %v", err)
		}
		stale := j
		j.Correlations = append(j.Correlations, Correlation{CorrelationAccount, "account-1"})
		j = saveJourney(t, d, j)
		if _, err := d.PutJourney(ctx, stale); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale write: %v", err)
		}
		j.Correlations = nil
		if _, err := d.PutJourney(ctx, j); !errors.Is(err, ErrConflict) {
			t.Fatalf("removed evidence: %v", err)
		}
		// Exactly one of two writers may advance the same revision.
		j, err := d.GetJourney(ctx, j.ID)
		if err != nil {
			t.Fatal(err)
		}
		errs := make(chan error, 2)
		for i := range 2 {
			go func() { x := j; x.Name = fmt.Sprintf("writer-%d", i); _, err := d.PutJourney(ctx, x); errs <- err }()
		}
		wins := 0
		for range 2 {
			err := <-errs
			if err == nil {
				wins++
			} else if !errors.Is(err, ErrConflict) && !errors.Is(err, ErrUnavailable) {
				t.Fatal(err)
			}
		}
		if wins != 1 {
			t.Fatalf("concurrent writes won %d times", wins)
		}
	})
	t.Run("complete filtered traversal and deterministic ties", func(t *testing.T) {
		d, ns := fresh(t)
		j := journey("journey", OriginRequest)
		j.Correlations = []Correlation{{CorrelationAccount, "account-1"}}
		saveJourney(t, d, j)
		saveJourney(t, d, journey("other-journey", OriginDirect))
		var events []core.AuditEvent
		for i := range 303 {
			e := core.AuditEvent{ID: fmt.Sprintf("event-%04d", i), JourneyID: j.ID, At: historyStart.Add(time.Duration(i/100) * time.Nanosecond), Event: "changed", Account: "reused", AccountID: "account-1", Owner: "Bob", Actor: "ſystem"}
			if i%61 == 0 {
				e.Event = "failed"
				e.Owner = "Alice"
			}
			if err := d.Record(ctx, e); err != nil {
				t.Fatal(err)
			}
			events = append(events, e)
		}
		e := core.AuditEvent{ID: "other", JourneyID: "other-journey", At: historyStart, Event: "changed", Account: "reused", Owner: "Bob", Actor: "system"}
		if err := d.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
		filters := []Filter{{}, {JourneyID: j.ID}, {AccountID: "account-1"}, {Account: "REUSED"}, {Owner: "bOB"}, {Actor: "SYSTEM"}, {Event: "failed"},
			{JourneyID: j.ID, AccountID: "account-1", Account: "REUSED", Owner: "ALICE", Actor: "SYSTEM", Event: "failed", Since: historyStart, Until: historyStart.Add(2 * time.Nanosecond)},
			{Owner: "absent"}, {Since: historyStart.Add(time.Nanosecond), Until: historyStart.Add(2 * time.Nanosecond)},
			{JourneyID: j.ID, Actor: "absent"},
		}
		for _, filter := range filters {
			for _, oldest := range []bool{false, true} {
				f := filter
				f.OldestFirst = oldest
				f.Limit = 17
				var got []core.AuditEvent
				for pages := 0; ; pages++ {
					if pages > 100 {
						t.Fatal("pagination loop")
					}
					p, err := d.Search(ctx, f)
					if err != nil || p.Incomplete {
						t.Fatalf("search %+v: %+v %v", f, p, err)
					}
					got = append(got, p.Events...)
					if p.Next == "" {
						break
					}
					f.Cursor = p.Next
				}
				f.Cursor = ""
				f.Limit = 500
				want, err := paginate(events, f, false)
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != len(want.Events) {
					t.Fatalf("filter %+v: got %d want %d", f, len(got), len(want.Events))
				}
				for i := range got {
					if got[i].ID != want.Events[i].ID {
						t.Fatalf("order %+v at %d: %s != %s", f, i, got[i].ID, want.Events[i].ID)
					}
				}
			}
		}
		p, err := d.Search(ctx, Filter{Limit: 1})
		if err != nil || p.Next == "" {
			t.Fatalf("cursor: %+v %v", p, err)
		}
		for _, changed := range []Filter{{Cursor: p.Next, Owner: "bob"}, {Cursor: p.Next, OldestFirst: true}, {Cursor: "garbage"}, {Cursor: strings.Repeat("a", 2049)}} {
			if _, err := d.Search(ctx, changed); !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("changed query accepted: %v", err)
			}
		}
		for _, s := range []*DynamoDB{testStore(t, api, table, Namespace{ns.Organization, "other"}), testStore(t, api, table+"-other", ns)} {
			if _, err := s.Search(ctx, Filter{Cursor: p.Next}); !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("cross-deployment cursor: %v", err)
			}
		}
		// A changed page size is allowed, and a new latest event cannot shift
		// traversal. Late older events may appear; this is not a snapshot.
		e = core.AuditEvent{ID: "newest", JourneyID: j.ID, At: historyStart.Add(time.Hour), Event: "changed"}
		if err := d.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
		next, err := d.Search(ctx, Filter{Cursor: p.Next, Limit: 500})
		if err != nil || len(next.Events) != len(events)-1 {
			t.Fatalf("shifted traversal: %d %v", len(next.Events), err)
		}
	})
	t.Run("event idempotency and account join guard", func(t *testing.T) {
		d, _ := fresh(t)
		saveJourney(t, d, journey("journey", OriginDirect))
		e := core.AuditEvent{ID: "event", JourneyID: "journey", At: historyStart, Event: "created"}
		if err := d.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
		e.At = e.At.In(time.FixedZone("offset", 3600))
		if err := d.Record(ctx, e); err != nil {
			t.Fatalf("retry: %v", err)
		}
		e.At = e.At.Add(time.Hour)
		if err := d.Record(ctx, e); !errors.Is(err, ErrConflict) {
			t.Fatalf("changed identity: %v", err)
		}
		p, err := d.Search(ctx, Filter{})
		if err != nil || len(p.Events) != 1 {
			t.Fatalf("duplicate displayed events: %+v %v", p, err)
		}
		e.ID = "unjoined"
		e.AccountID = "unknown-account"
		if err := d.Record(ctx, e); !errors.Is(err, ErrConflict) {
			t.Fatalf("unjoined account: %v", err)
		}
		e.ID = "orphan"
		e.JourneyID = "missing"
		e.AccountID = ""
		if err := d.Record(ctx, e); !errors.Is(err, ErrNotFound) {
			t.Fatalf("orphan: %v", err)
		}
		if _, err := d.Search(ctx, Filter{JourneyID: "missing"}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing journey looked empty: %v", err)
		}
		saveJourney(t, d, journey("empty", OriginDirect))
		p, err = d.Search(ctx, Filter{JourneyID: "empty"})
		if err != nil || p.Events == nil || len(p.Events) != 0 || p.Next != "" || p.Incomplete {
			t.Fatalf("empty journey: %+v %v", p, err)
		}
	})
}

package audit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"playplace/internal/core"
)

// DynamoDBAPI is the SDK subset used by the history adapter. It permits local
// contract tests without credentials or calls to a real AWS account.
type DynamoDBAPI interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

// DynamoDB implements Sink and durable journey metadata. It is deliberately
// not wired into the CLI: durable delivery and mutation gates must come first.
// The table has string PK/SK keys, no GSIs and no TTL. Search indexes are base
// table items written atomically with events and read strongly consistently.
// History metadata must never supply lifecycle guardrail facts to core.
type DynamoDB struct {
	client DynamoDBAPI
	table  string
	prefix string
}

var _ Sink = (*DynamoDB)(nil)

func NewDynamoDB(client DynamoDBAPI, table string, ns Namespace) (*DynamoDB, error) {
	if client == nil || !validIdentity(table) || !validIdentity(ns.Organization) || !validIdentity(ns.Environment) {
		return nil, errors.New("history requires a DynamoDB client, table, organization and environment")
	}
	return &DynamoDB{client: client, table: table, prefix: "H1|" + token(ns.Organization) + "|" + token(ns.Environment)}, nil
}

func token(s string) string       { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
func (d *DynamoDB) Where() string { return "dynamodb:" + d.table + "/" + d.prefix }
func (d *DynamoDB) Close() error  { return nil }

type item = map[string]types.AttributeValue

func str(s string) types.AttributeValue { return &types.AttributeValueMemberS{Value: s} }
func text(m item, key string) string {
	if a, ok := m[key].(*types.AttributeValueMemberS); ok {
		return a.Value
	}
	return ""
}
func key(pk, sk string) item                  { return item{"PK": str(pk), "SK": str(sk)} }
func (d *DynamoDB) journeyKey(id string) item { return key(d.prefix+"|J|"+token(id), "META") }
func (d *DynamoDB) correlationKey(c Correlation) item {
	return key(d.prefix+"|C|"+c.Kind+"|"+token(c.ID), "LINK")
}
func (d *DynamoDB) eventKey(id string) item { return key(d.prefix+"|E|"+token(id), "EVENT") }

// Keep copied event payloads and metadata bounded well below DynamoDB's item
// and transaction limits. Refuse growth explicitly; never truncate evidence.
const maxHistoryDocument = 64 * 1024

func document(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	if len(b) > maxHistoryDocument {
		return "", errors.New("history document exceeds 64 KiB")
	}
	return string(b), nil
}

func record(k item, data string) item {
	m := key(text(k, "PK"), text(k, "SK"))
	m["schema"], m["data"] = str("1"), str(data)
	return m
}

func decode(m, k item, v any) error {
	if text(m, "PK") != text(k, "PK") || text(m, "SK") != text(k, "SK") || text(m, "schema") != "1" ||
		len(text(m, "data")) > maxHistoryDocument || json.Unmarshal([]byte(text(m, "data")), v) != nil {
		return ErrCorrupt
	}
	return nil
}

func (d *DynamoDB) get(ctx context.Context, k item) (item, error) {
	out, err := d.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(d.table), Key: k, ConsistentRead: aws.Bool(true)})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if out == nil {
		return nil, ErrUnavailable
	}
	if len(out.Item) == 0 {
		return nil, ErrNotFound
	}
	return out.Item, nil
}

func (d *DynamoDB) transact(ctx context.Context, writes []types.TransactWriteItem) error {
	_, err := d.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
	if err == nil {
		return nil
	}
	var canceled *types.TransactionCanceledException
	if errors.As(err, &canceled) {
		for _, reason := range canceled.CancellationReasons {
			if aws.ToString(reason.Code) == "ConditionalCheckFailed" {
				return fmt.Errorf("%w: %w", ErrConflict, err)
			}
		}
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}

func (d *DynamoDB) put(m item, condition string, values item) types.TransactWriteItem {
	return types.TransactWriteItem{Put: &types.Put{TableName: aws.String(d.table), Item: m,
		ConditionExpression: aws.String(condition), ExpressionAttributeValues: values}}
}

// PutJourney creates at revision zero, or conditionally updates a read revision.
// The returned revision is the only new revision; callers must re-read after an
// ambiguous write response. All exact correlations are claimed atomically and
// cannot be reassigned to another journey, even after archival.
func (d *DynamoDB) PutJourney(ctx context.Context, j Journey) (Journey, error) {
	if err := j.validate(); err != nil {
		return Journey{}, err
	}
	if j.Revision > 0 {
		old, err := d.GetJourney(ctx, j.ID)
		if err != nil {
			return Journey{}, err
		}
		if old.Revision != j.Revision {
			return Journey{}, ErrConflict
		}
		if err := j.follows(old); err != nil {
			return Journey{}, err
		}
	}
	expected := j.Revision
	j.Revision++
	if j.Revision == 0 {
		return Journey{}, errors.New("history revision overflow")
	}
	data, err := document(j)
	if err != nil {
		return Journey{}, err
	}
	m := record(d.journeyKey(j.ID), data)
	m["revision"] = &types.AttributeValueMemberN{Value: strconv.FormatUint(j.Revision, 10)}
	condition := "attribute_not_exists(PK)"
	var values item
	if expected > 0 {
		condition = "revision = :revision"
		values = item{":revision": &types.AttributeValueMemberN{Value: strconv.FormatUint(expected, 10)}}
	}
	writes := []types.TransactWriteItem{d.put(m, condition, values)}
	for _, c := range j.Correlations {
		link := record(d.correlationKey(c), j.ID)
		// data is a plain target ID on LINK rows; it is not a JSON document.
		writes = append(writes, d.put(link, "attribute_not_exists(PK) OR #data = :journey", item{":journey": str(j.ID)}))
		writes[len(writes)-1].Put.ExpressionAttributeNames = map[string]string{"#data": "data"}
	}
	if err := d.transact(ctx, writes); err != nil {
		return Journey{}, err
	}
	return j, nil
}

func (d *DynamoDB) GetJourney(ctx context.Context, id string) (Journey, error) {
	if !validIdentity(id) {
		return Journey{}, errors.New("invalid journey ID")
	}
	k := d.journeyKey(id)
	m, err := d.get(ctx, k)
	if err != nil {
		return Journey{}, err
	}
	var j Journey
	if err := decode(m, k, &j); err != nil {
		return Journey{}, err
	}
	rev, ok := m["revision"].(*types.AttributeValueMemberN)
	if j.ID != id || j.Revision == 0 || !ok || rev.Value != strconv.FormatUint(j.Revision, 10) || j.validate() != nil {
		return Journey{}, ErrCorrupt
	}
	return j, nil
}

// ResolveJourney recovers direct creations across process loss using exact
// operation/provider-request IDs. Names, owner emails and timestamps are never
// accepted as correlation kinds. Missing links do not fall back to labels.
func (d *DynamoDB) ResolveJourney(ctx context.Context, c Correlation) (Journey, error) {
	if err := c.validate(); err != nil {
		return Journey{}, err
	}
	k := d.correlationKey(c)
	m, err := d.get(ctx, k)
	if err != nil {
		return Journey{}, err
	}
	if text(m, "PK") != text(k, "PK") || text(m, "SK") != text(k, "SK") || text(m, "schema") != "1" || !validIdentity(text(m, "data")) {
		return Journey{}, ErrCorrupt
	}
	j, err := d.GetJourney(ctx, text(m, "data"))
	if errors.Is(err, ErrNotFound) {
		return Journey{}, fmt.Errorf("%w: dangling correlation", ErrCorrupt)
	}
	if err != nil {
		return Journey{}, err
	}
	if !j.has(c) {
		return Journey{}, fmt.Errorf("%w: unconfirmed correlation", ErrCorrupt)
	}
	return j, nil
}

func validateEvent(e core.AuditEvent) error {
	if !validIdentity(e.ID) || !validIdentity(e.JourneyID) || !validIdentity(e.Event) || !validTime(e.At) {
		return errors.New("shared history event requires ID, journey, type and time")
	}
	for _, s := range []string{e.Account, e.AccountID, e.Owner, e.Actor} {
		if s != "" && !validIdentity(s) {
			return errors.New("invalid history event filter field")
		}
	}
	return nil
}

// Record atomically stores an event and all search rows. Re-delivery of the
// exact same event is harmless; reusing its ID for different content conflicts.
// This is storage idempotency, NOT the checkpoint-2 delivery/gating guarantee.
func (d *DynamoDB) Record(ctx context.Context, e core.AuditEvent) error {
	if err := validateEvent(e); err != nil {
		return err
	}
	e.At = e.At.UTC()
	data, err := document(e)
	if err != nil {
		return err
	}
	j, err := d.GetJourney(ctx, e.JourneyID)
	if err != nil {
		return err
	}
	if e.AccountID != "" && !j.has(Correlation{CorrelationAccount, e.AccountID}) {
		return fmt.Errorf("%w: event account is not correlated with journey", ErrConflict)
	}
	writes := []types.TransactWriteItem{{ConditionCheck: &types.ConditionCheck{
		TableName: aws.String(d.table), Key: d.journeyKey(e.JourneyID),
		ConditionExpression:       aws.String("revision = :revision"),
		ExpressionAttributeValues: item{":revision": &types.AttributeValueMemberN{Value: strconv.FormatUint(j.Revision, 10)}},
	}}}
	writes = append(writes, d.put(record(d.eventKey(e.ID), data), "attribute_not_exists(PK)", nil))
	for _, pk := range d.eventPartitions(e) {
		writes = append(writes, d.put(record(key(pk, eventSortKey(e.At, e.ID)), data), "attribute_not_exists(PK)", nil))
	}
	err = d.transact(ctx, writes)
	if !errors.Is(err, ErrConflict) {
		return err
	}
	// A response lost after commit is also safe to retry. Only a validated,
	// byte-identical original can establish that this event was already saved.
	m, readErr := d.get(ctx, d.eventKey(e.ID))
	if errors.Is(readErr, ErrNotFound) {
		return err
	}
	if readErr != nil {
		return readErr
	}
	var saved core.AuditEvent
	if decode(m, d.eventKey(e.ID), &saved) != nil || validateEvent(saved) != nil {
		return ErrCorrupt
	}
	if text(m, "data") == data {
		return nil
	}
	return ErrConflict
}

func (d *DynamoDB) Query(ctx context.Context, f Filter) ([]core.AuditEvent, error) {
	p, err := d.Search(ctx, f)
	return p.Events, err
}

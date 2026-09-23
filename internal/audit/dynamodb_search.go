package audit

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"playplace/internal/core"
)

// Fixed-width UTC timestamps preserve chronological order at nanosecond
// precision. Raw event IDs break ties with the same byte ordering as paginate.
const historyTimeLayout = "2006-01-02T15:04:05.000000000Z"

func eventSortKey(at time.Time, id string) string {
	return at.UTC().Format(historyTimeLayout) + "#" + id
}

// EqualFold uses Unicode simple folding, not merely strings.ToLower (e.g. final
// sigma, long s). Index canonicalization must not exclude matches readers accept.
func fold(s string) string {
	runes := []rune(s)
	for i, r := range runes {
		min := r
		for n := unicode.SimpleFold(r); n != r; n = unicode.SimpleFold(n) {
			if n < min {
				min = n
			}
		}
		runes[i] = min
	}
	return string(runes)
}

func (d *DynamoDB) index(kind, value string) string {
	return d.prefix + "|I|" + kind + "|" + token(value)
}

func (d *DynamoDB) eventPartitions(e core.AuditEvent) []string {
	out := []string{d.index("all", ""), d.index("journey", e.JourneyID), d.index("event", e.Event)}
	for _, field := range []struct{ kind, value string }{
		{"account-id", e.AccountID}, {"name", fold(e.Account)}, {"owner", fold(e.Owner)}, {"actor", fold(e.Actor)},
	} {
		if field.value != "" {
			out = append(out, d.index(field.kind, field.value))
		}
	}
	return out
}

// Choose one exact index, then apply the remaining predicates while paging the
// index to exhaustion (or one more matching result). No Scan, GSI lag, implicit
// lookback window, capped candidate set, or per-page DynamoDB FilterExpression.
func (d *DynamoDB) searchPartition(f Filter) string {
	for _, field := range []struct{ kind, value string }{
		{"journey", f.JourneyID}, {"account-id", f.AccountID}, {"name", fold(f.Account)},
		{"owner", fold(f.Owner)}, {"actor", fold(f.Actor)}, {"event", f.Event},
	} {
		if field.value != "" {
			return d.index(field.kind, field.value)
		}
	}
	return d.index("all", "")
}

func (d *DynamoDB) queryID(f Filter) string {
	sum := sha256.Sum256([]byte(d.Where() + "|" + queryID(f)))
	return hex.EncodeToString(sum[:])
}

func (d *DynamoDB) cursor(f Filter) (*position, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}
	for _, s := range []string{f.JourneyID, f.AccountID, f.Account, f.Owner, f.Actor, f.Event} {
		if s != "" && !validIdentity(s) {
			return nil, errors.New("invalid history filter")
		}
	}
	for _, at := range []time.Time{f.Since, f.Until} {
		if !at.IsZero() && !validTime(at) {
			return nil, errors.New("invalid history time range")
		}
	}
	if f.Cursor == "" {
		return nil, nil
	}
	if len(f.Cursor) > 2048 {
		return nil, ErrInvalidCursor
	}
	b, err := base64.RawURLEncoding.DecodeString(f.Cursor)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	var c searchCursor
	if json.Unmarshal(b, &c) != nil || c.Version != 2 || c.Query != d.queryID(f) || !validTime(c.Position.At) || !validIdentity(c.Position.Tie) ||
		(!f.Since.IsZero() && c.Position.At.Before(f.Since)) || (!f.Until.IsZero() && !c.Position.At.Before(f.Until)) {
		return nil, ErrInvalidCursor
	}
	return &c.Position, nil
}

func (d *DynamoDB) Search(ctx context.Context, f Filter) (Page, error) {
	after, err := d.cursor(f)
	if err != nil {
		return Page{}, err
	}
	if f.JourneyID != "" {
		if _, err := d.GetJourney(ctx, f.JourneyID); err != nil {
			return Page{}, err
		}
	}
	pk := d.searchPartition(f)
	lo, hi := "0001-01-01T00:00:00.000000000Z", "9999-12-31T23:59:59.999999999Z~"
	if !f.Since.IsZero() {
		lo = f.Since.UTC().Format(historyTimeLayout)
	}
	// No event at Until can compare <= its timestamp alone: every event key
	// includes a '#' suffix. Thus BETWEEN still implements an exclusive Until.
	if !f.Until.IsZero() {
		hi = f.Until.UTC().Format(historyTimeLayout)
	}
	in := &dynamodb.QueryInput{
		TableName: aws.String(d.table), ConsistentRead: aws.Bool(true), ScanIndexForward: aws.Bool(f.OldestFirst),
		KeyConditionExpression:    aws.String("PK = :pk AND SK BETWEEN :lo AND :hi"),
		ExpressionAttributeValues: item{":pk": str(pk), ":lo": str(lo), ":hi": str(hi)}, Limit: aws.Int32(128),
	}
	if after != nil {
		in.ExclusiveStartKey = key(pk, eventSortKey(after.At, after.Tie))
	}
	p := Page{Events: []core.AuditEvent{}}
	for {
		out, err := d.client.Query(ctx, in)
		if err != nil {
			return Page{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		if out == nil {
			return Page{}, ErrUnavailable
		}
		for _, m := range out.Items {
			var e core.AuditEvent
			if decode(m, key(pk, text(m, "SK")), &e) != nil || validateEvent(e) != nil || text(m, "SK") != eventSortKey(e.At, e.ID) {
				return Page{}, ErrCorrupt
			}
			indexed := false
			for _, partition := range d.eventPartitions(e) {
				if partition == pk {
					indexed = true
					break
				}
			}
			if !indexed {
				return Page{}, ErrCorrupt
			}
			if !f.matches(e) {
				continue
			}
			if len(p.Events) == f.limit() {
				b, err := json.Marshal(searchCursor{Version: 2, Query: d.queryID(f), Position: eventPosition(p.Events[len(p.Events)-1])})
				if err != nil {
					return Page{}, err
				}
				p.Next = base64.RawURLEncoding.EncodeToString(b)
				return p, nil
			}
			p.Events = append(p.Events, e)
		}
		if len(out.LastEvaluatedKey) == 0 {
			return p, nil
		}
		if text(out.LastEvaluatedKey, "PK") != pk || text(out.LastEvaluatedKey, "SK") == "" ||
			text(out.LastEvaluatedKey, "SK") == text(in.ExclusiveStartKey, "SK") {
			return Page{}, ErrCorrupt
		}
		in.ExclusiveStartKey = out.LastEvaluatedKey
	}
}

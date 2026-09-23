package audit

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"playplace/internal/core"
)

// Page distinguishes the end of a result set from a truncated or damaged log.
// Next is a query-bound cursor, not a record offset. Newer writes do not shift
// subsequent pages; late older writes may appear on a later page.
type Page struct {
	Events     []core.AuditEvent `json:"events"`
	Next       string            `json:"next,omitempty"`
	Incomplete bool              `json:"incomplete,omitempty"`
}

// Searcher is implemented by stores with complete, paginated retrieval. Keep
// Query as the bounded compatibility interface until all consumers use pages.
type Searcher interface {
	Search(context.Context, Filter) (Page, error)
}

var ErrInvalidCursor = errors.New("invalid history cursor or changed search filters")

type position struct {
	At  time.Time `json:"at"`
	Tie string    `json:"tie"`
}

type searchCursor struct {
	Version  int      `json:"v"`
	Query    string   `json:"q"`
	Position position `json:"p"`
}

func (f Filter) validate() error {
	if f.Limit < 0 || f.Limit > 500 {
		return errors.New("history limit must be between 1 and 500")
	}
	if !f.Since.IsZero() && !f.Until.IsZero() && !f.Since.Before(f.Until) {
		return errors.New("history since must be before until")
	}
	return nil
}

func queryID(f Filter) string {
	// Page size can change without changing the meaning or order of the query.
	f.Cursor, f.Limit = "", 0
	b, _ := json.Marshal(f)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func decodeCursor(f Filter) (*position, error) {
	if err := f.validate(); err != nil {
		return nil, err
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
	if json.Unmarshal(b, &c) != nil || c.Version != 1 || c.Query != queryID(f) || c.Position.At.IsZero() || c.Position.Tie == "" {
		return nil, ErrInvalidCursor
	}
	return &c.Position, nil
}

func eventPosition(e core.AuditEvent) position {
	tie := e.ID
	if tie == "" {
		// Legacy entries remain uncorrelated with journeys. A content fingerprint
		// gives their display a stable tie-breaker without inventing journey facts.
		b, _ := json.Marshal(e)
		sum := sha256.Sum256(b)
		tie = "legacy:" + hex.EncodeToString(sum[:])
	}
	return position{At: e.At, Tie: tie}
}

func compare(a, b position) int {
	if a.At.Before(b.At) {
		return -1
	}
	if a.At.After(b.At) {
		return 1
	}
	if a.Tie < b.Tie {
		return -1
	}
	if a.Tie > b.Tie {
		return 1
	}
	return 0
}

func paginate(events []core.AuditEvent, f Filter, incomplete bool) (Page, error) {
	after, err := decodeCursor(f)
	if err != nil {
		return Page{}, err
	}
	type entry struct {
		event core.AuditEvent
		pos   position
	}
	rows := make([]entry, 0, len(events))
	occurrences := map[string]int{}
	for _, e := range events {
		if !f.matches(e) {
			continue
		}
		pos := eventPosition(e)
		// Identical legacy lines have no idempotency identity. Keep both reachable.
		if e.ID == "" {
			occurrences[pos.Tie]++
			pos.Tie = fmt.Sprintf("%s:%012d", pos.Tie, occurrences[pos.Tie])
		}
		if after != nil {
			cmp := compare(pos, *after)
			if (!f.OldestFirst && cmp >= 0) || (f.OldestFirst && cmp <= 0) {
				continue
			}
		}
		rows = append(rows, entry{e, pos})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		cmp := compare(rows[i].pos, rows[j].pos)
		if f.OldestFirst {
			return cmp < 0
		}
		return cmp > 0
	})
	out := Page{Events: []core.AuditEvent{}, Incomplete: incomplete}
	n := min(len(rows), f.limit())
	for _, row := range rows[:n] {
		out.Events = append(out.Events, row.event)
	}
	if n < len(rows) {
		b, err := json.Marshal(searchCursor{Version: 1, Query: queryID(f), Position: rows[n-1].pos})
		if err != nil {
			return Page{}, err
		}
		out.Next = base64.RawURLEncoding.EncodeToString(b)
	}
	return out, nil
}

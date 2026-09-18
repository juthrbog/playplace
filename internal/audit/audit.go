// Package audit stores and queries playplace's history: one line per
// lifecycle event. Two sinks exist, a local JSON-lines file for setups that
// want nothing tied to AWS, and CloudWatch Logs for a shared, retained record.
package audit

import (
	"context"
	"sort"
	"strings"
	"time"

	"playplace/internal/core"
)

// Filter narrows a history query. Empty fields match everything.
type Filter struct {
	Account string    // account name
	Owner   string    // owner email
	Since   time.Time // zero means the sink's default window
	Limit   int       // 0 means 100
}

func (f Filter) matches(e core.AuditEvent) bool {
	if f.Account != "" && !strings.EqualFold(e.Account, f.Account) {
		return false
	}
	if f.Owner != "" && !strings.EqualFold(e.Owner, f.Owner) {
		return false
	}
	if !f.Since.IsZero() && e.At.Before(f.Since) {
		return false
	}
	return true
}

func (f Filter) limit() int {
	if f.Limit <= 0 {
		return 100
	}
	return f.Limit
}

// Sink writes events and reads them back, newest first.
type Sink interface {
	core.Auditor
	Query(ctx context.Context, f Filter) ([]core.AuditEvent, error)
	// Where names the sink for messages, e.g. a file path or log group.
	Where() string
	Close() error
}

// newestFirst sorts and trims a result set.
func newestFirst(events []core.AuditEvent, limit int) []core.AuditEvent {
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.After(events[j].At) })
	if len(events) > limit {
		events = events[:limit]
	}
	return events
}

// Memory keeps events in a slice, for tests.
type Memory struct {
	Events []core.AuditEvent
}

func (m *Memory) Record(_ context.Context, e core.AuditEvent) error {
	m.Events = append(m.Events, e)
	return nil
}

func (m *Memory) Query(_ context.Context, f Filter) ([]core.AuditEvent, error) {
	var out []core.AuditEvent
	for _, e := range m.Events {
		if f.matches(e) {
			out = append(out, e)
		}
	}
	return newestFirst(out, f.limit()), nil
}

func (m *Memory) Where() string { return "memory" }
func (m *Memory) Close() error  { return nil }

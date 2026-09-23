// Package audit stores and queries playplace's history: one line per
// lifecycle event. Two sinks exist, a local JSON-lines file for setups that
// want nothing tied to AWS, and CloudWatch Logs for a shared, retained record.
package audit

import (
	"context"
	"strings"
	"sync"
	"time"

	"playplace/internal/core"
)

// Filter narrows a history query. Empty fields match everything.
type Filter struct {
	Account     string // searchable name, not an authorization boundary
	AccountID   string // provider account ID
	JourneyID   string // exact opaque journey identity
	Owner       string // owner at event time
	Actor       string
	Event       string
	Since       time.Time // inclusive; zero means all available history
	Until       time.Time // exclusive
	Limit       int       // 0 means 100; Search accepts at most 500
	Cursor      string
	OldestFirst bool
}

func (f Filter) matches(e core.AuditEvent) bool {
	if f.JourneyID != "" && f.JourneyID != e.JourneyID {
		return false
	}
	if f.AccountID != "" && f.AccountID != e.AccountID {
		return false
	}
	if f.Actor != "" && !strings.EqualFold(f.Actor, e.Actor) {
		return false
	}
	if f.Event != "" && f.Event != e.Event {
		return false
	}
	if !f.Until.IsZero() && !e.At.Before(f.Until) {
		return false
	}
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
	Searcher
	Query(ctx context.Context, f Filter) ([]core.AuditEvent, error)
	// Where names the sink for messages, e.g. a file path or log group.
	Where() string
	Close() error
}

// Memory keeps events in a slice, for tests.
type Memory struct {
	mu     sync.RWMutex
	Events []core.AuditEvent
}

func (m *Memory) Record(ctx context.Context, e core.AuditEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Events = append(m.Events, e)
	return nil
}

func (m *Memory) Query(ctx context.Context, f Filter) ([]core.AuditEvent, error) {
	page, err := m.Search(ctx, f)
	return page.Events, err
}

func (m *Memory) Search(ctx context.Context, f Filter) (Page, error) {
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return paginate(m.Events, f, false)
}

func (m *Memory) Where() string { return "memory" }
func (m *Memory) Close() error  { return nil }

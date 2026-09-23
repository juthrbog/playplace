package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"playplace/internal/audit"
	"playplace/internal/core"
)

func historyModel(t *testing.T, h History) model {
	t.Helper()
	svc, _ := seeded(t)
	m := newModel(context.Background(), svc, Options{History: h})
	m.visible = []row{{Account: &core.Account{ID: "req:demo", Name: "demo", JourneyID: "one"}}}
	m.showDetail = true
	return m
}

func TestHistoryPagesAreFullyReachableAndDuplicateResponsesIgnored(t *testing.T) {
	h := &audit.Memory{}
	for i := 0; i < 55; i++ {
		if err := h.Record(context.Background(), core.AuditEvent{ID: fmt.Sprintf("%03d", i), JourneyID: "one", At: t0, Event: "extended", Message: fmt.Sprintf("entry-%03d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	m := historyModel(t, h)
	m = run(t, m, m.loadHistory())
	if len(m.hist["one"].page.Events) != 50 || m.hist["one"].page.Next == "" {
		t.Fatal("expected first page")
	}
	next, cmd := m.scrollHistory(1000)
	m = run(t, next.(model), cmd)
	next, cmd = m.scrollHistory(1)
	m = run(t, next.(model), cmd)
	if len(m.hist["one"].page.Events) != 55 || m.hist["one"].page.Next != "" {
		t.Fatal("older events were unreachable")
	}
	m = run(t, m, cmd)
	if len(m.hist["one"].page.Events) != 55 {
		t.Fatal("duplicate page response duplicated history")
	}
	next, cmd = m.scrollHistory(1000)
	m = run(t, next.(model), cmd)
	if !strings.Contains(plain(m.detailView(120, 30)), "entry-000") {
		t.Fatal("last event should be reachable in detail view")
	}
	next, cmd = m.scrollHistory(-1000)
	m = run(t, next.(model), cmd)
	if m.hist["one"].offset != 0 {
		t.Fatal("cannot scroll back to newest entries")
	}
}

func TestHistoryCacheUsesJourneyNotName(t *testing.T) {
	h := &audit.Memory{}
	for _, id := range []string{"one", "two"} {
		if err := h.Record(context.Background(), core.AuditEvent{ID: id, JourneyID: id, Account: "demo", At: t0, Event: "requested", Message: id}); err != nil {
			t.Fatal(err)
		}
	}
	m := historyModel(t, h)
	m = run(t, m, m.loadHistory())
	m.visible = []row{{Account: &core.Account{ID: "req:demo", Name: "demo", JourneyID: "two"}}}
	m = run(t, m, m.loadHistory())
	if events := m.hist["two"].page.Events; len(events) != 1 || events[0].JourneyID != "two" {
		t.Fatalf("reused name served stale history: %+v", events)
	}
}

type failedHistoryReader struct{}

func (failedHistoryReader) Search(context.Context, audit.Filter) (audit.Page, error) {
	return audit.Page{}, errors.New("backend unavailable")
}

func TestHistoryErrorsAreVisibleAndRetryable(t *testing.T) {
	m := historyModel(t, failedHistoryReader{})
	m = run(t, m, m.loadHistory())
	if status := m.hist["one"].status(); !strings.Contains(status, "unavailable") {
		t.Fatalf("error status=%q", status)
	}
	m.opts.History = &audit.Memory{}
	m.clearHistory()
	m = run(t, m, m.loadHistory())
	if status := m.hist["one"].status(); status != "nothing recorded yet" {
		t.Fatalf("retry=%q", status)
	}
}

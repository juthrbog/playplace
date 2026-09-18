package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
)

func TestAsyncClosureIsObservedBeforeReportingClosed(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "manual", true: "expiry"}[expired], func(t *testing.T) {
			h := newHarness(t)
			h.prov.AsyncClose = true
			ctx := context.Background()
			a := h.active(t, "async-close", "alex", time.Hour)
			h.note.msgs = nil
			audit := &recorderAudit{}
			h.svc.SetAuditor(audit)
			if expired {
				h.advance(time.Hour)
				sum, err := h.svc.Refresh(ctx)
				if err != nil || len(sum.Closed) != 0 || len(sum.Deferred) != 1 {
					t.Fatalf("expiry: %+v %v", sum, err)
				}
			} else {
				var err error
				a, err = h.svc.RequestClose(ctx, a.ID, "ops")
				if err != nil || a.Status != core.StatusClosing {
					t.Fatalf("request: %+v %v", a, err)
				}
			}
			if len(h.note.msgs) != 0 || strings.Contains(strings.Join(audit.kinds(), ","), "closed") {
				t.Fatalf("premature closure notification: %+v %+v", h.note.msgs, audit.kinds())
			}
			for range 2 {
				sum, err := h.svc.Refresh(ctx)
				if err != nil || len(sum.Closed) != 0 || len(sum.Deferred) != 1 {
					t.Fatalf("pending: %+v %v", sum, err)
				}
			}
			if len(h.prov.Closed) != 1 {
				t.Fatal("must not resubmit while AWS reports PENDING_CLOSURE")
			}
			if _, err := h.svc.Extend(ctx, a.ID, h.now.Add(48*time.Hour), "ops", false); err == nil {
				t.Fatal("pending closure must not be extendable")
			}
			r, err := h.prov.GetAccount(ctx, a.ID)
			if err != nil {
				t.Fatal(err)
			}
			r.Status = "CLOSED"
			h.prov.Seed(r)
			sum, err := h.svc.Refresh(ctx)
			if err != nil || len(sum.Closed) != 1 || len(h.note.msgs) != 1 || h.prov.Tags(a.ID)[core.TagClosedAt] == "" {
				t.Fatalf("confirmation: %+v %v notes=%+v", sum, err, h.note.msgs)
			}
			// A new process must not repeat closure notifications.
			restarted := core.NewService(h.prov, h.note, core.DefaultConfig(), nil)
			restarted.Now = func() time.Time { return h.now }
			if sum, err := restarted.Refresh(ctx); err != nil || !sum.Empty() || len(h.note.msgs) != 1 {
				t.Fatalf("repeated confirmation: %+v %v notes=%+v", sum, err, h.note.msgs)
			}
		})
	}
}

func TestOverdueClosureFailsReconcileAndNotifiesOnce(t *testing.T) {
	for _, quota := range []bool{false, true} {
		t.Run(map[bool]string{false: "processing", true: "quota"}[quota], func(t *testing.T) {
			h := newHarness(t)
			h.prov.AsyncClose = !quota
			h.prov.QuotaHit = quota
			ctx := context.Background()
			a := h.active(t, "overdue", "alex", time.Hour)
			if _, err := h.svc.RequestClose(ctx, a.ID, "ops"); err != nil {
				t.Fatal(err)
			}
			h.note.msgs = nil
			h.advance(24*time.Hour - time.Second)
			if _, err := h.svc.Refresh(ctx); err != nil {
				t.Fatalf("too early: %v", err)
			}
			h.advance(time.Second)
			for range 2 {
				sum, err := h.svc.Refresh(ctx)
				if err == nil || !strings.Contains(err.Error(), "charges may continue") || len(sum.Overdue) != 1 || len(sum.Closed) != 0 {
					t.Fatalf("overdue: %+v %v", sum, err)
				}
			}
			if len(h.note.msgs) != 1 || h.note.msgs[0].Subject != "Playground account closure overdue" {
				t.Fatalf("notifications: %+v", h.note.msgs)
			}
			restarted := core.NewService(h.prov, h.note, core.DefaultConfig(), nil)
			restarted.Now = func() time.Time { return h.now }
			if _, err := restarted.Refresh(ctx); err == nil || len(h.note.msgs) != 1 {
				t.Fatalf("restart lost overdue state: %v %+v", err, h.note.msgs)
			}
			observed, err := restarted.Get(ctx, a.ID)
			if err != nil || observed.LastError == "" || observed.Status != core.StatusClosing {
				t.Fatalf("overdue must be visible in inventory: %+v %v", observed, err)
			}
			r, _ := h.prov.GetAccount(ctx, a.ID)
			r.Status = "CLOSED"
			h.prov.Seed(r)
			if sum, err := restarted.Refresh(ctx); err != nil || len(sum.Closed) != 1 || len(sum.Overdue) != 0 {
				t.Fatalf("closure must clear alarm: %+v %v", sum, err)
			}
		})
	}
}

// Simulate a provider that accepts a close but continues to report ACTIVE,
// returns an error, or cannot be read back. None may become a closed account.
type uncertainClosure struct {
	core.Provider
	closeErr   error
	readErr    error
	omitIntent bool
}

func (p uncertainClosure) CloseAccount(context.Context, string) error { return p.closeErr }
func (p uncertainClosure) GetAccount(ctx context.Context, id string) (core.RemoteAccount, error) {
	if p.readErr != nil {
		return core.RemoteAccount{}, p.readErr
	}
	r, err := p.Provider.GetAccount(ctx, id)
	if p.omitIntent {
		delete(r.Tags, core.TagCloseRequested)
	}
	return r, err
}

func TestUncertainClosureNeverReportsSuccess(t *testing.T) {
	for _, mode := range []string{"still active", "stale tags", "close error", "read error"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			a := h.active(t, "uncertain", "alex", time.Hour)
			p := uncertainClosure{Provider: h.prov, omitIntent: mode == "stale tags"}
			if mode == "close error" {
				p.closeErr = errors.New("access denied")
			}
			if mode == "read error" {
				p.readErr = errors.New("read unavailable")
			}
			svc := core.NewService(p, h.note, core.DefaultConfig(), nil)
			svc.Now = func() time.Time { return h.now }
			h.note.msgs = nil
			a, err := svc.RequestClose(ctx, a.ID, "ops")
			if (p.closeErr != nil || p.readErr != nil) != (err != nil) {
				t.Fatalf("unexpected error: %v", err)
			}
			if a == nil || a.Status != core.StatusClosing || a.CloseRequestedAt == nil || len(h.note.msgs) != 0 {
				t.Fatalf("unconfirmed close: %+v notes=%+v", a, h.note.msgs)
			}
			h.advance(24 * time.Hour)
			if sum, err := svc.Refresh(ctx); err == nil || len(sum.Overdue) != 1 || len(sum.Closed) != 0 {
				t.Fatalf("uncertain closure must still age into alarm: %+v %v", sum, err)
			}
		})
	}
}

func TestUnavailableStatesAreNotClosed(t *testing.T) {
	for _, state := range []string{"SUSPENDED", "PENDING_ACTIVATION", "", "NEW_STATE"} {
		t.Run(state, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			a := h.active(t, "unavailable", "alex", time.Hour)
			r, _ := h.prov.GetAccount(ctx, a.ID)
			r.Status = state
			r.Tags[core.TagCloseRequested] = t0.Format(time.RFC3339)
			// fake.Seed defaults an empty state to ACTIVE; use an unknown token
			// here. The empty-state mapping is covered by account_test.go.
			if state == "" {
				r.Status = "UNKNOWN"
			}
			h.prov.Seed(r)
			h.note.msgs = nil
			sum, err := h.svc.Refresh(ctx)
			if err == nil || len(sum.Closed) != 0 || len(h.note.msgs) != 0 {
				t.Fatalf("unavailable must not be closed: %+v %v", sum, err)
			}
			open, err := h.svc.List(ctx, core.ListFilter{})
			if err != nil || len(open) != 1 || open[0].Status != core.StatusUnavailable || open[0].LastError == "" {
				t.Fatalf("unavailable must remain visible: %+v %v", open, err)
			}
		})
	}
}

func TestExternallyRequestedClosureGetsMonitoringClock(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.active(t, "external", "alex", time.Hour)
	r, _ := h.prov.GetAccount(ctx, a.ID)
	r.Status = "PENDING_CLOSURE"
	h.prov.Seed(r)
	if sum, err := h.svc.Refresh(ctx); err != nil || len(sum.Deferred) != 1 {
		t.Fatalf("external close: %+v %v", sum, err)
	}
	first := h.prov.Tags(a.ID)[core.TagCloseRequested]
	h.advance(24 * time.Hour)
	if sum, err := h.svc.Refresh(ctx); err == nil || len(sum.Overdue) != 1 || h.prov.Tags(a.ID)[core.TagCloseRequested] != first {
		t.Fatalf("monitoring clock must not reset: %+v %v", sum, err)
	}
}

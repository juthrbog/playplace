package core_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"playplace/internal/core"
)

// Spend is known only while the last pull of the account succeeded. An
// account that was never pulled, or whose last pull failed, has unknown
// spend, never zero and never the points of an older pull.
func TestCostStateIsKnownOnlyAfterAGoodPull(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.active(t, "spender", "alex", 0)
	if pts, ok := h.svc.CostState(a.ProviderID, 30); ok || pts != nil {
		t.Fatalf("never pulled: CostState = %v, %v, want unknown", pts, ok)
	}
	if _, err := h.svc.Costs(ctx, a.ID, 30, false); err != nil {
		t.Fatal(err)
	}
	if pts, ok := h.svc.CostState(a.ProviderID, 30); !ok || len(pts) != 30 || pts[0].AmountUSD != 1.5 {
		t.Fatalf("after a good pull: CostState = %d points, %v", len(pts), ok)
	}

	h.prov.CostFailures[a.ProviderID] = "AccessDeniedException: not allowed"
	if _, err := h.svc.Costs(ctx, a.ID, 30, true); err == nil || !strings.Contains(err.Error(), "AccessDeniedException") {
		t.Fatalf("a failed pull must report its error, got %v", err)
	}
	if pts, ok := h.svc.CostState(a.ProviderID, 30); ok || pts != nil {
		t.Fatalf("failure after success: CostState = %d points, %v, want unknown", len(pts), ok)
	}
	// CostsCached still gives the points of the last good pull.
	if pts, ok := h.svc.CostsCached(a.ProviderID, 30); !ok || len(pts) != 30 || pts[0].AmountUSD != 1.5 {
		t.Fatalf("CostsCached after a failure = %d points, %v", len(pts), ok)
	}

	delete(h.prov.CostFailures, a.ProviderID)
	h.prov.DailyCost = 2
	if _, err := h.svc.Costs(ctx, a.ID, 30, true); err != nil {
		t.Fatal(err)
	}
	if pts, ok := h.svc.CostState(a.ProviderID, 30); !ok || len(pts) != 30 || pts[0].AmountUSD != 2 {
		t.Fatalf("a good pull after a failure: CostState = %d points, %v", len(pts), ok)
	}
	if pts, ok := h.svc.CostState("", 30); ok || pts != nil {
		t.Fatalf("an account with no provider ID has no cost state, got %v, %v", pts, ok)
	}
}

// After a failed pull, an account waits five minutes before a pull that is
// not forced. The web poll warms every visible account every 15 seconds, and
// Cost Explorer bills per call. A forced pull (Sync, the TUI reload) tries at
// once, and a failed one starts the wait again.
func TestAFailedCostPullWaitsFiveMinutes(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.active(t, "spender", "alex", 0)
	h.prov.CostFailures[a.ProviderID] = "ThrottlingException: rate exceeded"
	if _, err := h.svc.Costs(ctx, a.ID, 30, false); err == nil {
		t.Fatal("the pull must fail")
	}
	if h.prov.CostCalls != 1 {
		t.Fatalf("calls = %d, want 1", h.prov.CostCalls)
	}

	h.advance(5*time.Minute - time.Second)
	if _, err := h.svc.Costs(ctx, a.ID, 30, false); err == nil || !strings.Contains(err.Error(), "ThrottlingException") {
		t.Fatalf("within the wait, Costs must report the last failure, got %v", err)
	}
	if err := h.svc.WarmCosts(ctx, []string{a.ID}, false); err == nil {
		t.Fatal("within the wait, WarmCosts must report the last failure")
	}
	if h.prov.CostCalls != 1 {
		t.Fatalf("a pull that is not forced ran within the wait: calls = %d", h.prov.CostCalls)
	}

	if _, err := h.svc.Costs(ctx, a.ID, 30, true); err == nil {
		t.Fatal("the forced pull must fail too")
	}
	if h.prov.CostCalls != 2 {
		t.Fatalf("a forced pull must try at once: calls = %d", h.prov.CostCalls)
	}

	h.advance(5*time.Minute - time.Second)
	if err := h.svc.WarmCosts(ctx, []string{a.ID}, false); err == nil || h.prov.CostCalls != 2 {
		t.Fatalf("the forced failure must start the wait again: calls = %d, err = %v", h.prov.CostCalls, err)
	}

	h.advance(time.Second)
	delete(h.prov.CostFailures, a.ProviderID)
	if pts, err := h.svc.Costs(ctx, a.ID, 30, false); err != nil || len(pts) != 30 || h.prov.CostCalls != 3 {
		t.Fatalf("five minutes after the failure: %d points, err = %v, calls = %d", len(pts), err, h.prov.CostCalls)
	}
	if _, ok := h.svc.CostState(a.ProviderID, 30); !ok {
		t.Fatal("the good pull must make spend known")
	}

	// A good pull is reused for CostTTL, as before.
	h.advance(h.svc.Config().CostTTL - time.Second)
	if _, err := h.svc.Costs(ctx, a.ID, 30, false); err != nil || h.prov.CostCalls != 3 {
		t.Fatalf("a good pull must be reused: calls = %d, err = %v", h.prov.CostCalls, err)
	}
}

// slowFirstCost holds the first cost pull until release closes. That pull
// then fails when firstFails is set. Every later pull goes to the provider.
type slowFirstCost struct {
	core.Provider
	firstFails bool
	started    chan struct{}
	release    chan struct{}

	mu    sync.Mutex
	calls int
}

func (p *slowFirstCost) Costs(ctx context.Context, id string, from, to time.Time) ([]core.CostPoint, error) {
	p.mu.Lock()
	p.calls++
	first := p.calls == 1
	p.mu.Unlock()
	if !first {
		return p.Provider.Costs(ctx, id, from, to)
	}
	close(p.started)
	<-p.release
	if p.firstFails {
		return nil, errors.New("ThrottlingException: rate exceeded")
	}
	return p.Provider.Costs(ctx, id, from, to)
}

// A poll pull starts, a forced pull (Sync, the TUI reload) starts later, and
// the poll pull ends last. The cost state keeps the answer of the newest
// pull, whichever pull ends last.
func TestTheNewestCostPullWins(t *testing.T) {
	for _, c := range []struct {
		name               string
		slowFails, forceOK bool
	}{
		{"an older failure after a newer success", true, true},
		{"an older success after a newer failure", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			a := h.active(t, "spender", "alex", 0)
			p := &slowFirstCost{Provider: h.prov, firstFails: c.slowFails, started: make(chan struct{}), release: make(chan struct{})}
			cfg := core.DefaultConfig()
			cfg.InventoryTTL = 0
			svc := core.NewService(p, h.note, cfg, nil)
			svc.Now = h.svc.Now
			ctx := context.Background()

			done := make(chan error, 1)
			go func() {
				_, err := svc.Costs(ctx, a.ID, 30, false)
				done <- err
			}()
			<-p.started
			if !c.forceOK {
				h.prov.CostFailures[a.ProviderID] = "AccessDeniedException: not allowed"
			}
			if _, err := svc.Costs(ctx, a.ID, 30, true); (err == nil) != c.forceOK {
				t.Fatalf("the forced pull = %v", err)
			}
			delete(h.prov.CostFailures, a.ProviderID)
			close(p.release)
			if err := <-done; (err != nil) != c.slowFails {
				t.Fatalf("the slow pull = %v", err)
			}
			if _, ok := svc.CostState(a.ProviderID, 30); ok != c.forceOK {
				t.Errorf("spend known = %v, want %v as the newest pull", ok, c.forceOK)
			}
			if _, err := svc.Costs(ctx, a.ID, 30, false); (err == nil) != c.forceOK {
				t.Errorf("a pull that is not forced = %v, want the result of the newest pull", err)
			}
		})
	}
}

// costCallerLeaves stands for a caller that stops waiting during a cost
// pull, such as a closed browser tab. The pull then fails with the error of
// the ended context, as the AWS SDK does.
type costCallerLeaves struct {
	core.Provider
	leave context.CancelFunc // when set, the caller leaves during the pull
}

func (p *costCallerLeaves) Costs(ctx context.Context, id string, from, to time.Time) ([]core.CostPoint, error) {
	if p.leave != nil {
		p.leave()
		return nil, ctx.Err()
	}
	return p.Provider.Costs(ctx, id, from, to)
}

// A pull that ends because its caller left says nothing about AWS. It keeps
// the cost state as it was and starts no wait.
func TestACallerThatLeavesDoesNotChangeTheCostState(t *testing.T) {
	h := newHarness(t)
	p := &costCallerLeaves{Provider: h.prov}
	cfg := core.DefaultConfig()
	cfg.InventoryTTL = 0
	svc := core.NewService(p, h.note, cfg, nil)
	svc.Now = h.svc.Now
	known := h.active(t, "known", "alex", 0)
	fresh := h.active(t, "fresh", "anna", 0)
	ctx := context.Background()
	if _, err := svc.Costs(ctx, known.ID, 30, false); err != nil {
		t.Fatal(err)
	}

	for _, a := range []*core.Account{known, fresh} {
		left, cancel := context.WithCancel(ctx)
		p.leave = cancel
		if _, err := svc.Costs(left, a.ID, 30, true); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s: a pull whose caller left = %v", a.Name, err)
		}
		p.leave = nil
	}
	if _, ok := svc.CostState(known.ProviderID, 30); !ok {
		t.Fatal("a caller that left must not make known spend unknown")
	}
	calls := h.prov.CostCalls
	if _, err := svc.Costs(ctx, fresh.ID, 30, false); err != nil || h.prov.CostCalls != calls+1 {
		t.Fatalf("a caller that left must start no wait: calls %d to %d, err = %v", calls, h.prov.CostCalls, err)
	}
}

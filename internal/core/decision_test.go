package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"playplace/internal/core"
)

func TestSelfApprovalRefusalMatchesTheSentinelAndKeepsItsWords(t *testing.T) {
	r := core.Request{Name: "box", Owner: "bob@example.com", RequestedBy: "anna@example.com"}
	for _, c := range []struct{ approver, want string }{
		{"anna@example.com", "anna@example.com requested box and cannot approve it; another approver must"},
		{"Bob@example.com", "Bob@example.com would own box and cannot approve it; another approver must"},
	} {
		err := core.CheckApprover(r, c.approver, false)
		if !errors.Is(err, core.ErrSelfApproval) {
			t.Errorf("refusal of %s must match ErrSelfApproval, got %v", c.approver, err)
		}
		if err == nil || err.Error() != c.want {
			t.Errorf("refusal of %s = %v, want %q", c.approver, err, c.want)
		}
		if err := core.CheckApprover(r, c.approver, true); err != nil {
			t.Errorf("allowSelf must let %s approve, got %v", c.approver, err)
		}
	}
	if err := core.CheckApprover(r, "ops@example.com", false); err != nil {
		t.Errorf("another approver must pass, got %v", err)
	}

	// Approve returns the refusal as it is, so the CLI, the TUI and the web
	// keep their words and can tell the refusal apart.
	h := newHarness(t)
	ctx := context.Background()
	q, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "box", Owner: "bob@example.com", RequestedBy: "anna@example.com"}, "web")
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.svc.Approve(ctx, q.Ref(), "anna@example.com")
	if !errors.Is(err, core.ErrSelfApproval) || err.Error() != "anna@example.com requested box and cannot approve it; another approver must" {
		t.Fatalf("approve by the requester = %v", err)
	}
}

func TestRequestDropsAtIsTheExpiryBoundary(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	ttl := h.svc.Config().RequestTTL
	r, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "waits", Owner: "anna@example.com"}, "web")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := r.DropsAt(ttl), t0.Add(ttl); !got.Equal(want) {
		t.Fatalf("DropsAt = %s, want %s", got, want)
	}
	// Exactly at DropsAt the request still waits.
	h.now = t0.Add(ttl)
	sum, err := h.svc.Refresh(ctx)
	if err != nil || len(sum.Expired) != 0 {
		t.Fatalf("a request at its DropsAt must stay: %+v %v", sum, err)
	}
	if _, err := h.svc.GetRequest(ctx, "waits"); err != nil {
		t.Fatalf("a request at its DropsAt must stay pending: %v", err)
	}
	// One second later the refresh drops it.
	h.now = t0.Add(ttl + time.Second)
	sum, err = h.svc.Refresh(ctx)
	if err != nil || len(sum.Expired) != 1 || sum.Expired[0] != "waits" {
		t.Fatalf("a request one second past its DropsAt must be dropped: %+v %v", sum, err)
	}
	if _, err := h.svc.GetRequest(ctx, "waits"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("a dropped request must leave the queue, got %v", err)
	}
}

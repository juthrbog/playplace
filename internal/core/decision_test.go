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

// callerLeaves stands for a caller that stops waiting while AWS works. Each
// account creation blocks until the caller's context ends and then fails, as
// the AWS SDK does. Each queue write fails on an ended context too, and
// keeps the deadline of its context.
type callerLeaves struct {
	core.Provider
	creating chan struct{} // receives once when a creation starts
	creates  int
	deadline time.Time // of the last queue write
	bounded  bool      // whether the last queue write had a deadline
}

func (p *callerLeaves) SetOUTags(ctx context.Context, tags map[string]string) error {
	p.deadline, p.bounded = ctx.Deadline()
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.Provider.SetOUTags(ctx, tags)
}

func (p *callerLeaves) RequestAccount(ctx context.Context, _ core.AccountRequest) (string, error) {
	p.creates++
	p.creating <- struct{}{}
	<-ctx.Done()
	return "", ctx.Err()
}

func TestFailedApprovalRestoresTheRequestAfterTheCallerLeft(t *testing.T) {
	h := newHarness(t)
	p := &callerLeaves{Provider: h.prov, creating: make(chan struct{}, 1)}
	cfg := core.DefaultConfig()
	cfg.InventoryTTL = 0
	svc := core.NewService(p, h.note, cfg, nil)
	svc.Now = h.svc.Now
	r, err := svc.SubmitRequest(context.Background(), core.RequestInput{Name: "left", Owner: "anna@example.com"}, "web")
	if err != nil {
		t.Fatal(err)
	}
	pendingTag := func() string {
		t.Helper()
		tags, err := h.prov.OUTags(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return tags[r.Key()]
	}

	// The claim keeps the caller's context. A caller that left before the
	// claim changes nothing and starts no creation.
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.Approve(gone, r.Ref(), "lead@example.com"); !errors.Is(err, context.Canceled) {
		t.Fatalf("approve after the caller left = %v", err)
	}
	if got := pendingTag(); p.creates != 0 || got != r.Encode() {
		t.Fatalf("a caller that left before the claim must change nothing: creates=%d tag=%q", p.creates, got)
	}

	// The caller leaves while AWS creates the account, and the creation
	// fails. The restore still puts the pending record back, within a time
	// limit of its own.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := svc.Approve(ctx, r.Ref(), "lead@example.com")
		done <- err
	}()
	<-p.creating
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("approve must report the failed creation, got %v", err)
	}
	if got := pendingTag(); got != r.Encode() {
		t.Fatalf("the request must be pending again after the caller left: tag=%q want %q", got, r.Encode())
	}
	if left := time.Until(p.deadline); !p.bounded || left <= 0 || left > 30*time.Second {
		t.Fatalf("the restore must have a deadline of its own within 30 s: bounded=%v left=%s", p.bounded, left)
	}
	if q, err := svc.GetRequest(context.Background(), "left"); err != nil || q.Version() != r.Version() {
		t.Fatalf("the restored request must keep its version: %+v %v", q, err)
	}
}

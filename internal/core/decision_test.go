package core_test

import (
	"context"
	"errors"
	"testing"

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

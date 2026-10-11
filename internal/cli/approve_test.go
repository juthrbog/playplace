package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

// approve shows the operator what they approve before it binds to it.
func TestApproveShowsTheRequestAndAsks(t *testing.T) {
	q := core.Request{Name: "demo", Owner: "anna@example.com", TTL: 7 * 24 * time.Hour, BudgetUSD: 12.5,
		RequestedBy: "bob@example.com", RequestedAt: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), Via: "web",
		Purpose: "try bedrock", OverrideLimits: true}
	var out bytes.Buffer
	if err := confirmApproval(q, false, strings.NewReader("\n"), &out); err == nil || err.Error() != "aborted" {
		t.Fatalf("an empty answer should abort, got %v", err)
	}
	for _, want := range []string{"demo for anna@example.com", "$12.50/month", "7 days", "requested by bob@example.com via web", "purpose: try bedrock", "limits overridden", "approve demo? [y/N]"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("prompt missing %q:\n%s", want, out.String())
		}
	}
	if err := confirmApproval(q, false, strings.NewReader("n\n"), &out); err == nil || err.Error() != "aborted" {
		t.Fatalf("n should abort, got %v", err)
	}
	if err := confirmApproval(q, false, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "pass -y") {
		t.Fatalf("input without an answer should name -y, got %v", err)
	}
	if err := confirmApproval(q, false, strings.NewReader("y\n"), &out); err != nil {
		t.Fatalf("y should confirm: %v", err)
	}
	if err := confirmApproval(q, false, strings.NewReader("Y"), &out); err != nil {
		t.Fatalf("Y without a newline should confirm: %v", err)
	}
	out.Reset()
	if err := confirmApproval(q, true, strings.NewReader(""), &out); err != nil || strings.Contains(out.String(), "[y/N]") || !strings.Contains(out.String(), "demo for anna@example.com") {
		t.Fatalf("--yes should print without asking: %v\n%s", err, out.String())
	}
}

func TestDescribeRequestShowsTheExactLifetime(t *testing.T) {
	for ttl, want := range map[time.Duration]string{
		24 * time.Hour:      "1 day;",
		90 * 24 * time.Hour: "90 days;",
		36 * time.Hour:      "36 hours;",
	} {
		q := core.Request{Name: "demo", Owner: "anna", TTL: ttl, BudgetUSD: 20, RequestedBy: "bob", Via: "cli"}
		if got := describeRequest(q); !strings.Contains(got, want) {
			t.Errorf("describeRequest(%s) = %q, want %q", ttl, got, want)
		}
	}
}

// editWhileAsking runs edit when the question is read, as if another
// operator changed the request while this one read the prompt.
type editWhileAsking struct {
	edit   func()
	answer io.Reader
}

func (r *editWhileAsking) Read(p []byte) (int, error) {
	if r.edit != nil {
		r.edit()
		r.edit = nil
	}
	return r.answer.Read(p)
}

// The approval binds to the version printed. An edit made while the
// question waits is refused, and the operator who may not approve the
// request is refused before the question.
func TestApproveBindsToThePrintedRequest(t *testing.T) {
	prov := fake.New()
	cfg := core.DefaultConfig()
	svc := core.NewService(prov, nil, cfg, nil)
	ctx := context.Background()
	if _, err := svc.SubmitRequest(ctx, core.RequestInput{Name: "demo", Owner: "anna", BudgetUSD: 20}, "cli"); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if _, err := approveRequest(ctx, svc, "demo", "anna", false, strings.NewReader("y\n"), &out); err == nil || !strings.Contains(err.Error(), "cannot approve") {
		t.Fatalf("the owner approving = %v", err)
	}
	if strings.Contains(out.String(), "[y/N]") {
		t.Fatalf("the owner was asked before the refusal:\n%s", out.String())
	}

	in := &editWhileAsking{answer: strings.NewReader("y\n"), edit: func() {
		q, err := svc.GetRequest(ctx, "demo")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.UpdateRequest(ctx, q.Ref(), "admin", core.RequestEdit{BudgetUSD: 400}); err != nil {
			t.Fatal(err)
		}
	}}
	out.Reset()
	if _, err := approveRequest(ctx, svc, "demo", "lead", false, in, &out); !errors.Is(err, core.ErrRequestChanged) {
		t.Fatalf("approving after an edit during the question = %v", err)
	}
	if !strings.Contains(out.String(), "$20/month") {
		t.Fatalf("the prompt should show the values before the edit:\n%s", out.String())
	}
	if creates, _ := prov.ListCreateRequests(ctx); len(creates) != 0 {
		t.Fatalf("a refused approval created %d accounts", len(creates))
	}

	out.Reset()
	acc, err := approveRequest(ctx, svc, "demo", "lead", true, strings.NewReader(""), &out)
	if err != nil || acc.BudgetUSD != 400 || !strings.Contains(out.String(), "$400/month") {
		t.Fatalf("approving the request as printed = %+v %v\n%s", acc, err, out.String())
	}
}

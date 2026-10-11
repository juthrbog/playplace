package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
)

// A decision names the version the actor saw. A stale page, TUI row, or
// Slack message must not act on a resubmitted or edited request.
func TestDecisionsAreBoundToTheVersionTheActorSaw(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	first, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "demo", Owner: "anna", BudgetUSD: 20}, "web")
	if err != nil {
		t.Fatal(err)
	}
	seen := first.Ref() // the approver's page or Slack message

	// Withdrawn and submitted again with bigger values: a new journey.
	if err := h.svc.Withdraw(ctx, first.Ref(), "anna"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "demo", Owner: "anna", BudgetUSD: 500, TTL: 90 * 24 * time.Hour}, "web"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Approve(ctx, seen, "lead"); !errors.Is(err, core.ErrRequestChanged) || !strings.Contains(err.Error(), "has ended") {
		t.Fatalf("approving a resubmission from a stale view = %v", err)
	}
	if err := h.svc.Deny(ctx, seen, "lead", "no"); !errors.Is(err, core.ErrRequestChanged) {
		t.Fatalf("denying a resubmission from a stale view = %v", err)
	}

	// An admin raises the budget after the approver loaded the request.
	seen = h.ref(t, "demo")
	ov := true
	if _, err := h.svc.UpdateRequest(ctx, seen, "admin", core.RequestEdit{BudgetUSD: 5000, OverrideLimits: &ov}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Approve(ctx, seen, "lead"); !errors.Is(err, core.ErrRequestChanged) || !strings.Contains(err.Error(), "was edited") {
		t.Fatalf("approving edited values from a stale view = %v", err)
	}
	if err := h.svc.Withdraw(ctx, seen, "anna"); !errors.Is(err, core.ErrRequestChanged) {
		t.Fatalf("withdrawing from a stale view = %v", err)
	}
	// A second editor's form was filled before the first edit; saving it
	// would undo the raise without anyone choosing to.
	p := "only the purpose"
	no := false
	if _, err := h.svc.UpdateRequest(ctx, seen, "admin-b", core.RequestEdit{Owner: "anna", BudgetUSD: 20, Purpose: &p, OverrideLimits: &no}); !errors.Is(err, core.ErrRequestChanged) {
		t.Fatalf("stale edit form = %v", err)
	}
	if _, err := h.svc.Approve(ctx, core.RequestRef{Name: "demo"}, "lead"); !errors.Is(err, core.ErrRequestChanged) {
		t.Fatalf("approving without a version = %v", err)
	}
	if creates, _ := h.prov.ListCreateRequests(ctx); len(creates) != 0 {
		t.Fatalf("a refused decision created %d accounts", len(creates))
	}

	// Looking again and approving what is there now works.
	acc, err := h.svc.Approve(ctx, h.ref(t, "demo"), "lead")
	if err != nil || acc.BudgetUSD != 5000 {
		t.Fatalf("approve current = %+v %v", acc, err)
	}
}

func TestRequestVersionCoversEveryShownValue(t *testing.T) {
	base := core.Request{JourneyID: "J1", Name: "demo", Owner: "anna", TTL: 90 * time.Minute, BudgetUSD: 12.5,
		RequestedBy: "bob", RequestedAt: t0, Via: "web", Purpose: " test ", OverrideLimits: false}
	decoded, err := core.DecodeRequest(base.Key(), base.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Version() != base.Version() {
		t.Fatalf("version changed through the tag: %s vs %s", decoded.Version(), base.Version())
	}
	if !strings.HasPrefix(base.Version(), "J1.") {
		t.Fatalf("version should start with the journey: %s", base.Version())
	}
	// Approval fields are not part of what the approver saw.
	approved := base
	approved.Approved, approved.ApprovedBy = true, "lead"
	if approved.Version() != base.Version() {
		t.Fatal("approval fields must not change the version")
	}
	for what, change := range map[string]func(*core.Request){
		"journey":  func(r *core.Request) { r.JourneyID = "J2" },
		"owner":    func(r *core.Request) { r.Owner = "eve" },
		"lifetime": func(r *core.Request) { r.TTL = 48 * time.Hour },
		"budget":   func(r *core.Request) { r.BudgetUSD = 13 },
		"purpose":  func(r *core.Request) { r.Purpose = "other" },
		"override": func(r *core.Request) { r.OverrideLimits = true },
		"asked by": func(r *core.Request) { r.RequestedBy = "carl" },
		"asked at": func(r *core.Request) { r.RequestedAt = t0.Add(time.Second) },
	} {
		r := base
		change(&r)
		if r.Version() == base.Version() {
			t.Errorf("changing the %s kept the version", what)
		}
	}
}

// A direct creation must not take the name of someone else's pending
// request; placing it used to delete that request without a trace.
func TestDirectCreateRefusesAPendingRequestsName(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "demo", Owner: "anna"}, "web"); err != nil {
		t.Fatal(err)
	}
	_, err := h.svc.Request(ctx, core.RequestInput{Name: "demo", Owner: "bob", RequestedBy: "op", ApprovedBy: "op"})
	if err == nil || !strings.Contains(err.Error(), "waiting for approval") {
		t.Fatalf("direct create over a pending request = %v", err)
	}
	if p, _ := h.svc.PendingRequests(ctx); len(p) != 1 {
		t.Fatalf("pending = %d, want the request kept", len(p))
	}
}

// Placement removes only its own journey's approved record. Another
// process can queue a request under the name while the creation runs.
func TestPlacementKeepsAnotherJourneysRecord(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a, err := h.svc.Request(ctx, core.RequestInput{Name: "demo", Owner: "bob", RequestedBy: "op", ApprovedBy: "op"})
	if err != nil {
		t.Fatal(err)
	}
	other := core.Request{JourneyID: core.NewHistoryID(), Name: "demo", Owner: "anna", TTL: 24 * time.Hour, BudgetUSD: 20,
		RequestedBy: "anna", RequestedAt: h.now, Via: "cli"}
	if err := h.prov.SetOUTags(ctx, map[string]string{other.Key(): other.Encode()}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.PollCreate(ctx, a.RequestID); err != nil {
		t.Fatal(err)
	}
	if q, err := h.svc.GetRequest(ctx, "demo"); err != nil || q.JourneyID != other.JourneyID {
		t.Fatalf("placement removed another journey's request: %+v %v", q, err)
	}
}

// A request named like an account id must not take that account's place
// in Resolve, which every web account route and the CLI use.
func TestResolvePrefersAnExactIDOverAName(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	acc := h.active(t, "sams-box", "sam", 0)
	h.advance(time.Hour) // the request sorts first, as it is newer
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: acc.ProviderID, Owner: "eve"}, "web"); err != nil {
		t.Fatal(err)
	}
	got, err := h.svc.Resolve(ctx, acc.ProviderID)
	if err != nil || got.ID != acc.ID || got.Owner != "sam" {
		t.Fatalf("Resolve(%s) = %+v %v", acc.ProviderID, got, err)
	}
	if got, err := h.svc.Resolve(ctx, "req:"+acc.ProviderID); err != nil || got.Owner != "eve" {
		t.Fatalf("the request stays reachable by its own id: %+v %v", got, err)
	}
}

// laggingQueue serves one stale read of the OU tags after an armed write,
// as AWS can: it does not promise that a read sees an earlier tag write.
type laggingQueue struct {
	core.Provider
	armed  bool              // snapshot before the next SetOUTags
	stale  map[string]string // served by the next OUTags
	served int
}

func (p *laggingQueue) SetOUTags(ctx context.Context, tags map[string]string) error {
	if p.armed {
		p.armed = false
		before, err := p.Provider.OUTags(ctx)
		if err != nil {
			return err
		}
		p.stale = before
	}
	return p.Provider.SetOUTags(ctx, tags)
}

func (p *laggingQueue) OUTags(ctx context.Context) (map[string]string, error) {
	if p.stale != nil {
		stale := p.stale
		p.stale = nil
		p.served++
		return stale, nil
	}
	return p.Provider.OUTags(ctx)
}

// Approve marks the record approved and then reads the inventory to create
// the account. If that read still sees the record as pending, the request's
// own journey must not block its approval.
func TestApprovalSurvivesAQueueReadThatLags(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	p := &laggingQueue{Provider: h.prov}
	cfg := core.DefaultConfig()
	cfg.InventoryTTL = 0
	svc := core.NewService(p, h.note, cfg, nil)
	svc.Now = h.svc.Now
	q, err := svc.SubmitRequest(ctx, core.RequestInput{Name: "lag", Owner: "anna"}, "web")
	if err != nil {
		t.Fatal(err)
	}
	p.armed = true // the claim write is the next one
	if _, err := svc.Approve(ctx, q.Ref(), "lead"); err != nil {
		t.Fatalf("approval refused by its own pending record: %v", err)
	}
	if p.served != 1 {
		t.Fatalf("stale reads served = %d, want 1; the test no longer models the lag", p.served)
	}
	if creates, _ := h.prov.ListCreateRequests(ctx); len(creates) != 1 {
		t.Fatalf("create requests = %d, want 1", len(creates))
	}
}

// Another process can change the queue behind this process's cached
// inventory. A refused decision must drop the cache, so the next view (a
// re-rendered web card, the TUI rows, the next Slack-driven read) shows the
// current request and a version that works.
func TestRefusedDecisionDropsTheCachedQueue(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	cfg := core.DefaultConfig() // the inventory cache stays on, as in serve
	serve := core.NewService(h.prov, h.note, cfg, nil)
	serve.Now = h.svc.Now
	other := core.NewService(h.prov, h.note, cfg, nil) // the CLI or the TUI
	other.Now = h.svc.Now

	q, err := serve.SubmitRequest(ctx, core.RequestInput{Name: "demo", Owner: "anna", BudgetUSD: 20}, "web")
	if err != nil {
		t.Fatal(err)
	}
	if p, err := serve.PendingRequests(ctx); err != nil || len(p) != 1 || p[0].BudgetUSD != 20 {
		t.Fatalf("pending = %+v %v", p, err)
	}
	if _, err := other.UpdateRequest(ctx, q.Ref(), "admin", core.RequestEdit{BudgetUSD: 400}); err != nil {
		t.Fatal(err)
	}
	if p, _ := serve.PendingRequests(ctx); p[0].BudgetUSD != 20 {
		t.Fatal("the test needs serve's cache to still hold the old request")
	}
	if _, err := serve.Approve(ctx, q.Ref(), "lead"); !errors.Is(err, core.ErrRequestChanged) {
		t.Fatalf("stale approve = %v", err)
	}
	p, err := serve.PendingRequests(ctx)
	if err != nil || len(p) != 1 || p[0].BudgetUSD != 400 {
		t.Fatalf("after the refusal the queue should be read live: %+v %v", p, err)
	}
	if _, err := serve.Approve(ctx, p[0].Ref(), "lead"); err != nil {
		t.Fatalf("approving the fresh view = %v", err)
	}
}

// Every interface shows an approver the lifetime and budget with Lifetime
// and Dollars. The version binds whole hours and the exact budget, so two
// values with different versions must never show the same text.
func TestLifetimeAndDollarsShowTheBoundValues(t *testing.T) {
	for d, want := range map[time.Duration]string{
		time.Hour:                     "1 hour",
		72 * time.Minute:              "2 hours", // the queue rounds up to whole hours
		36 * time.Hour:                "36 hours",
		24 * time.Hour:                "1 day",
		47*time.Hour + 30*time.Minute: "2 days",
		90 * 24 * time.Hour:           "90 days",
	} {
		if got := core.Lifetime(d); got != want {
			t.Errorf("Lifetime(%s) = %q, want %q", d, got, want)
		}
	}
	shown := map[string]int{}
	for h := 1; h <= 24*400; h++ {
		s := core.Lifetime(time.Duration(h) * time.Hour)
		if prev, dup := shown[s]; dup {
			t.Fatalf("%d and %d hours both show as %q", prev, h, s)
		}
		shown[s] = h
	}

	for v, want := range map[float64]string{
		20:     "$20",
		1250:   "$1250",
		412.5:  "$412.50",
		12.34:  "$12.34",
		12.345: "$12.345",
		0.05:   "$0.05",
	} {
		if got := core.Dollars(v); got != want {
			t.Errorf("Dollars(%v) = %q, want %q", v, got, want)
		}
	}
	for _, pair := range [][2]float64{{412.5, 412}, {12.345, 12.35}, {1250, 1300}, {0.001, 0}} {
		if core.Dollars(pair[0]) == core.Dollars(pair[1]) {
			t.Errorf("%v and %v both show as %q", pair[0], pair[1], core.Dollars(pair[0]))
		}
	}
}

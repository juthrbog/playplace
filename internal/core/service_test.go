package core_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

var t0 = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

type recorder struct{ msgs []core.Message }

func (r *recorder) Notify(_ context.Context, m core.Message) error {
	r.msgs = append(r.msgs, m)
	return nil
}

type harness struct {
	svc  *core.Service
	prov *fake.Provider
	note *recorder
	now  time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{prov: fake.New(), note: &recorder{}, now: t0}
	h.prov.Now = func() time.Time { return h.now }
	cfg := core.DefaultConfig()
	cfg.InventoryTTL = 0 // always live in tests
	h.svc = core.NewService(h.prov, h.note, cfg, nil)
	h.svc.Now = func() time.Time { return h.now }
	return h
}

func (h *harness) advance(d time.Duration) { h.now = h.now.Add(d) }

func (h *harness) active(t *testing.T, name, owner string, ttl time.Duration) *core.Account {
	t.Helper()
	ctx := context.Background()
	a, err := h.svc.Request(ctx, core.RequestInput{Name: name, Owner: owner, TTL: ttl})
	if err != nil {
		t.Fatal(err)
	}
	a, err = h.svc.PollCreate(ctx, a.RequestID)
	if err != nil || a.Status != core.StatusActive {
		t.Fatalf("poll: %+v %v", a, err)
	}
	return a
}

func TestRequestThenPollPlacesAndTags(t *testing.T) {
	h := newHarness(t)
	h.prov.PollsToDone = 1
	ctx := context.Background()

	a, err := h.svc.Request(ctx, core.RequestInput{Name: "dev-one", Owner: "alex", BudgetUSD: 25})
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != core.StatusCreating || a.RequestID == "" || a.Email != "aws+pp-dev-one@example.com" {
		t.Fatalf("request = %+v", a)
	}
	// While creating, the inventory shows it from the provider's request list.
	inv, _ := h.svc.Inventory(ctx, true)
	if len(inv) != 1 || inv[0].Status != core.StatusCreating || inv[0].Name != "dev-one" {
		t.Fatalf("inventory while creating = %+v", inv)
	}

	a, _ = h.svc.PollCreate(ctx, a.RequestID)
	if a.Status != core.StatusCreating {
		t.Fatalf("first poll should still be creating, got %s", a.Status)
	}
	a, err = h.svc.PollCreate(ctx, a.RequestID)
	if err != nil || a.Status != core.StatusActive || a.ProviderID == "" {
		t.Fatalf("second poll: %+v %v", a, err)
	}
	tags := h.prov.Tags(a.ProviderID)
	if tags[core.TagOwner] != "alex" || tags[core.TagBudget] != "25" || tags[core.TagManaged] != "true" || tags[core.TagExpires] == "" {
		t.Fatalf("tags = %v", tags)
	}
	if h.prov.Budgets[a.ProviderID] != 25 {
		t.Fatalf("budget = %v", h.prov.Budgets[a.ProviderID])
	}
	if len(h.note.msgs) != 1 || h.note.msgs[0].Owner != "alex" {
		t.Fatalf("notifications = %+v", h.note.msgs)
	}
	inv, _ = h.svc.Inventory(ctx, true)
	if len(inv) != 1 || inv[0].Status != core.StatusActive || inv[0].Owner != "alex" || inv[0].BudgetUSD != 25 {
		t.Fatalf("inventory after place = %+v", inv[0])
	}
	if _, err := h.svc.Request(ctx, core.RequestInput{Name: "dev-one", Owner: "sam"}); err == nil {
		t.Fatal("duplicate open name should be refused")
	}
}

func TestRefreshPlacesUnwaitedCreation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.Request(ctx, core.RequestInput{Name: "fire-forget", Owner: "sam"}); err != nil {
		t.Fatal(err)
	}
	h.prov.Settle() // AWS finished it while nobody was polling
	sum, err := h.svc.Refresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Placed) != 1 || sum.Placed[0] != "fire-forget" {
		t.Fatalf("summary = %+v", sum)
	}
	a, err := h.svc.Resolve(ctx, "fire-forget")
	if err != nil || a.Status != core.StatusActive || a.Owner != "sam" {
		t.Fatalf("resolve = %+v %v", a, err)
	}
	// Second refresh is a no-op.
	if sum, _ := h.svc.Refresh(ctx); !sum.Empty() {
		t.Fatalf("second refresh changed things: %s", sum)
	}
}

func TestRefreshAdoptsUntaggedAccounts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.prov.Seed(core.RemoteAccount{ProviderID: "999999999999", Name: "legacy", Email: "l@example.com", JoinedAt: t0.Add(-48 * time.Hour), Tags: map[string]string{core.TagOwner: "anna"}})
	sum, err := h.svc.Refresh(ctx)
	if err != nil || len(sum.Adopted) != 1 {
		t.Fatalf("summary = %+v err=%v", sum, err)
	}
	tags := h.prov.Tags("999999999999")
	if tags[core.TagManaged] != "true" || tags[core.TagOwner] != "anna" || tags[core.TagBudget] != "50" || tags[core.TagExpires] == "" {
		t.Fatalf("adopt should write full tags, got %v", tags)
	}
	if h.prov.Budgets["999999999999"] != 50 {
		t.Fatal("adopt should create the default budget")
	}
	a, _ := h.svc.Get(ctx, "999999999999")
	if !a.Managed || !a.ExpiresAt.Equal(t0.Add(14*24*time.Hour)) {
		t.Fatalf("adopted = %+v", a)
	}
}

func TestExpiryWarnsExtendsAndCloses(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.active(t, "short", "alex", 5*24*time.Hour)
	h.note.msgs = nil

	h.advance(24 * time.Hour)
	if sum, _ := h.svc.Refresh(ctx); !sum.Empty() {
		t.Fatalf("day 1 should be quiet: %s", sum)
	}
	h.advance(24 * time.Hour) // 3 days left: inside the warning window
	sum, _ := h.svc.Refresh(ctx)
	if len(sum.Warned) != 1 || len(h.note.msgs) != 1 {
		t.Fatalf("day 2: summary=%+v notes=%d", sum, len(h.note.msgs))
	}
	a, _ = h.svc.Get(ctx, a.ID)
	if a.Status != core.StatusExpiring || a.WarnedAt == nil {
		t.Fatalf("after warn = %+v", a)
	}
	if sum, _ := h.svc.Refresh(ctx); len(sum.Warned) != 0 {
		t.Fatal("warning must only fire once")
	}

	a, err := h.svc.Extend(ctx, a.ID, h.now.Add(10*24*time.Hour), "ops@example.com", false)
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != core.StatusActive || a.WarnedAt != nil {
		t.Fatalf("after extend = %+v", a)
	}
	if _, ok := h.prov.Tags(a.ProviderID)[core.TagWarnedAt]; ok {
		t.Fatal("extend should remove the warned-at tag")
	}
	if _, err := h.svc.Extend(ctx, a.ID, h.now.Add(time.Hour), "ops@example.com", false); err == nil {
		t.Fatal("extend to an earlier date should fail")
	}

	h.advance(11 * 24 * time.Hour)
	sum, _ = h.svc.Refresh(ctx)
	if len(sum.Closed) != 1 || len(h.prov.Closed) != 1 {
		t.Fatalf("expired account should close: %+v closed=%v", sum, h.prov.Closed)
	}
	a, _ = h.svc.Get(ctx, a.ID)
	if a.Status != core.StatusClosed {
		t.Fatalf("status = %s", a.Status)
	}
	open, _ := h.svc.List(ctx, core.ListFilter{})
	if len(open) != 0 {
		t.Fatal("closed accounts should be hidden by default")
	}
}

func TestCloseIntentSurvivesQuota(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.active(t, "quota", "alex", 0)
	h.prov.QuotaHit = true

	a, err := h.svc.RequestClose(ctx, a.ID, "ops@example.com")
	if err != nil || a.Status != core.StatusClosing {
		t.Fatalf("close under quota: %+v %v", a, err)
	}
	if _, ok := h.prov.Tags(a.ProviderID)[core.TagCloseRequested]; !ok {
		t.Fatal("close intent should be tagged")
	}
	sum, _ := h.svc.Refresh(ctx)
	if len(sum.Deferred) != 1 {
		t.Fatalf("refresh under quota should defer: %+v", sum)
	}
	if _, err := h.svc.Extend(ctx, a.ID, h.now.Add(48*time.Hour), "ops@example.com", false); err == nil {
		t.Fatal("a closing account cannot be extended")
	}

	h.prov.QuotaHit = false
	sum, _ = h.svc.Refresh(ctx)
	if len(sum.Closed) != 1 {
		t.Fatalf("refresh after quota should close: %+v", sum)
	}
	a, _ = h.svc.Get(ctx, a.ID)
	if a.Status != core.StatusClosed {
		t.Fatalf("status = %s", a.Status)
	}
}

func TestFailedCreationIsVisibleThenForgotten(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.prov.FailNext = "EMAIL_ALREADY_EXISTS"
	a, err := h.svc.Request(ctx, core.RequestInput{Name: "dupe", Owner: "alex"})
	if err != nil {
		t.Fatal(err)
	}
	a, _ = h.svc.PollCreate(ctx, a.RequestID)
	if a.Status != core.StatusFailed || a.LastError != "EMAIL_ALREADY_EXISTS" {
		t.Fatalf("poll = %+v", a)
	}
	inv, _ := h.svc.Inventory(ctx, true)
	if len(inv) != 1 || inv[0].Status != core.StatusFailed {
		t.Fatalf("inventory = %+v", inv)
	}
	// The name is free again for a retry.
	if _, err := h.svc.Request(ctx, core.RequestInput{Name: "dupe", Owner: "alex"}); err != nil {
		t.Fatalf("re-request after failure: %v", err)
	}
	h.advance(8 * 24 * time.Hour)
	inv, _ = h.svc.Inventory(ctx, true)
	for _, x := range inv {
		if x.Status == core.StatusFailed {
			t.Fatal("failed request older than retention should drop out")
		}
	}
}

func TestCostsAreCachedAndWindowed(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.active(t, "spender", "alex", 0)
	pts, err := h.svc.Costs(ctx, a.ID, 7, false)
	if err != nil || len(pts) != 7 {
		t.Fatalf("costs = %d %v", len(pts), err)
	}
	h.prov.DailyCost = 99
	pts, _ = h.svc.Costs(ctx, a.ID, 7, false)
	if pts[0].AmountUSD != 1.5 {
		t.Fatal("second call should come from cache")
	}
	pts, _ = h.svc.Costs(ctx, a.ID, 7, true)
	if pts[0].AmountUSD != 99 {
		t.Fatal("force should bypass the cache")
	}
	if c, ok := h.svc.CostsCached(a.ProviderID, 30); !ok || len(c) != 30 {
		t.Fatalf("cached window = %d %v", len(c), ok)
	}
}

func TestResolve(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.active(t, "res-one", "alex", 0)
	for _, ref := range []string{a.ID, a.ID[:6], "res-one"} {
		got, err := h.svc.Resolve(ctx, ref)
		if err != nil || got.ID != a.ID {
			t.Fatalf("resolve %q: %v %v", ref, got, err)
		}
	}
	if _, err := h.svc.Resolve(ctx, "nope"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestAccessGrantedOnPlaceAndRevokedOnClose(t *testing.T) {
	h := newHarness(t)
	h.prov.AccessOn = true
	h.prov.AddUser("u-alex", "alex", "alex@example.com")
	ctx := context.Background()

	if _, err := h.svc.Request(ctx, core.RequestInput{Name: "who", Owner: "nobody@example.com"}); err == nil {
		t.Fatal("unknown owner should be refused when access is enabled")
	}
	// Owner given by user name is normalised to the identity store email.
	a, err := h.svc.Request(ctx, core.RequestInput{Name: "granted", Owner: "alex"})
	if err != nil || a.Owner != "alex@example.com" {
		t.Fatalf("request = %+v %v", a, err)
	}
	a, err = h.svc.PollCreate(ctx, a.RequestID)
	if err != nil || a.Status != core.StatusActive {
		t.Fatalf("poll = %+v %v", a, err)
	}
	if a.AccessGrantedTo != "u-alex" || h.prov.Tags(a.ProviderID)[core.TagAccess] != "u-alex" {
		t.Fatalf("access should be granted and tagged: %+v tags=%v", a, h.prov.Tags(a.ProviderID))
	}
	if g := h.prov.Grants[a.ProviderID]; len(g) != 1 || g[0] != "u-alex" {
		t.Fatalf("provider grants = %v", g)
	}
	if !strings.Contains(h.note.msgs[0].Body, "access portal") {
		t.Fatalf("ready notification should mention the access portal: %q", h.note.msgs[0].Body)
	}

	if _, err := h.svc.RequestClose(ctx, a.ID, "ops@example.com"); err != nil {
		t.Fatal(err)
	}
	if len(h.prov.Grants[a.ProviderID]) != 0 {
		t.Fatal("close should revoke the grant")
	}
	if _, ok := h.prov.Tags(a.ProviderID)[core.TagAccess]; ok {
		t.Fatal("close should drop the access tag")
	}
}

func TestRefreshGrantsAccessToAdoptedAndRetriedAccounts(t *testing.T) {
	h := newHarness(t)
	h.prov.AccessOn = true
	h.prov.AddUser("u-anna", "anna", "anna@example.com")
	ctx := context.Background()
	exp := t0.Add(48 * time.Hour).Format(time.RFC3339)
	// Adopted with a resolvable owner: gets a grant.
	h.prov.Seed(core.RemoteAccount{ProviderID: "111111111111", Name: "annas", JoinedAt: t0, Tags: map[string]string{core.TagOwner: "anna@example.com"}})
	// Managed but never granted (an earlier grant failed) with an unknown owner: skipped without error.
	h.prov.Seed(core.RemoteAccount{ProviderID: "222222222222", Name: "orphan", JoinedAt: t0, Tags: map[string]string{core.TagManaged: "true", core.TagOwner: "ghost@example.com", core.TagExpires: exp, core.TagBudget: "50"}})

	sum, err := h.svc.Refresh(ctx)
	if err != nil {
		t.Fatalf("refresh should not fail on an unresolvable owner: %v", err)
	}
	if len(sum.Adopted) != 1 || len(sum.Granted) != 1 || sum.Granted[0] != "annas" {
		t.Fatalf("summary = %+v", sum)
	}
	if g := h.prov.Grants["111111111111"]; len(g) != 1 || g[0] != "u-anna" {
		t.Fatalf("grants = %v", g)
	}
	if len(h.prov.Grants["222222222222"]) != 0 {
		t.Fatal("unknown owner must not be granted anything")
	}
	if sum, _ := h.svc.Refresh(ctx); !sum.Empty() {
		t.Fatalf("second refresh should be quiet, got %s", sum)
	}
}

func TestRefreshPlacesAndGrantsExactlyOnce(t *testing.T) {
	h := newHarness(t)
	h.prov.AccessOn = true
	h.prov.AddUser("u-dana", "dana", "dana@example.com")
	ctx := context.Background()
	if _, err := h.svc.Request(ctx, core.RequestInput{Name: "unwaited", Owner: "dana@example.com"}); err != nil {
		t.Fatal(err)
	}
	h.prov.Settle()
	sum, err := h.svc.Refresh(ctx)
	if err != nil || len(sum.Placed) != 1 || len(sum.Granted) != 1 {
		t.Fatalf("refresh should report physical placement and the new grant: %+v %v", sum, err)
	}
	a, _ := h.svc.Resolve(ctx, "unwaited")
	if g := h.prov.Grants[a.ProviderID]; len(g) != 1 {
		t.Fatalf("expected exactly one grant, got %v", g)
	}
}

func TestAccessDisabledKeepsFreeTextOwners(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.active(t, "plain", "some team", 0)
	if a.AccessGrantedTo != "" || len(h.prov.Grants) != 0 {
		t.Fatal("no grants expected when access is disabled")
	}
	_ = ctx
}

func TestRequestTagRoundTripStaysWithinAWSTagRules(t *testing.T) {
	r := core.Request{Name: "dev-x", Owner: "a+pp@example.com", TTL: 72 * time.Hour, BudgetUSD: 12.5, RequestedBy: "b@example.com",
		RequestedAt: t0, Via: "web", Purpose: "try Bedrock: agents / tools = fun @ 10.5 with ünïcode", OverrideLimits: true}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	v := r.Encode()
	if !core.ValidTagValue(v) {
		t.Fatalf("encoded record must be a legal AWS tag value: %q", v)
	}
	back, err := core.DecodeRequest(r.Key(), v)
	if err != nil || back != r {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	// Spaces inside fields do not confuse the length-prefixed format.
	sp := r
	sp.Purpose = "  a   b  "
	if back, _ := core.DecodeRequest(sp.Key(), sp.Encode()); back.Purpose != "a   b" {
		t.Fatalf("purpose = %q", back.Purpose)
	}
	// Illegal characters and over-long purposes are refused before they reach AWS.
	for _, bad := range []string{"has | pipe", "50% off", "quote's", "semi;colon", strings.Repeat("x", core.MaxPurpose+1)} {
		b := r
		b.Purpose = bad
		if err := b.Validate(); err == nil {
			t.Errorf("purpose %q should be refused", bad)
		}
	}
	b := r
	b.Owner = "bad|owner"
	if err := b.Validate(); err == nil {
		t.Error("owner with an illegal character should be refused")
	}
	// The old v1 format still decodes.
	old, err := core.DecodeRequest(core.RequestTagPrefix+"legacy", "v1|x%40example.com|24|5|x%40example.com|1789570012|cli|old+purpose|o")
	if err != nil || old.Owner != "x@example.com" || old.Purpose != "old purpose" || !old.OverrideLimits {
		t.Fatalf("legacy decode = %+v %v", old, err)
	}
	if _, err := core.DecodeRequest("playplace:owner", "alex"); err == nil {
		t.Fatal("non-request tag must not decode")
	}
}

func TestFreeTextFieldsAreValidatedEverywhere(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "pur-one", Owner: "a@example.com", Purpose: "100% legit"}, "web"); err == nil || !strings.Contains(err.Error(), "purpose may only contain") {
		t.Fatalf("submit with %% should be refused, got %v", err)
	}
	if _, err := h.svc.Request(ctx, core.RequestInput{Name: "pur-two", Owner: "a@example.com", Purpose: strings.Repeat("y", 200)}); err == nil || !strings.Contains(err.Error(), "longer than 120") {
		t.Fatalf("direct create with a long purpose should be refused, got %v", err)
	}
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "pur-three", Owner: "a@example.com", Purpose: "fine: bedrock/agents = ok"}, "web"); err != nil {
		t.Fatalf("legal purpose refused: %v", err)
	}
	bad := "no | pipes"
	if _, err := h.svc.UpdateRequest(ctx, "pur-three", "ops@example.com", core.RequestEdit{Purpose: &bad}); err == nil {
		t.Fatal("edit with an illegal purpose should be refused")
	}
	// Approval writes the purpose as an account tag; the strict fake accepts it.
	acc, err := h.svc.Approve(ctx, "pur-three", "ops@example.com")
	if err != nil {
		t.Fatal(err)
	}
	acc, _ = h.svc.PollCreate(ctx, acc.RequestID)
	if h.prov.Tags(acc.ProviderID)[core.TagPurpose] != "fine: bedrock/agents = ok" {
		t.Fatalf("purpose tag = %q", h.prov.Tags(acc.ProviderID)[core.TagPurpose])
	}
}

func TestSubmitApproveFlow(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	r, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "req-one", Owner: "anna@example.com", TTL: 5 * 24 * time.Hour, BudgetUSD: 20, Purpose: "bedrock"}, "web")
	if err != nil {
		t.Fatal(err)
	}
	if r.RequestedBy != "anna@example.com" || r.Via != "web" {
		t.Fatalf("request = %+v", r)
	}
	if len(h.note.msgs) != 1 || !strings.Contains(h.note.msgs[0].Subject, "requested: req-one") || h.note.msgs[0].Owner != "approvers" {
		t.Fatalf("approvers should be notified: %+v", h.note.msgs)
	}
	// Visible in the inventory as pending, resolvable by name.
	a, err := h.svc.Resolve(ctx, "req-one")
	if err != nil || a.Status != core.StatusPending || a.Owner != "anna@example.com" || a.Purpose != "bedrock" {
		t.Fatalf("pending in inventory = %+v %v", a, err)
	}
	// Same name cannot be requested twice, and the owner is at the limit of 1.
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "req-one", Owner: "sam@example.com"}, "web"); err == nil {
		t.Fatal("duplicate name should be refused")
	}
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "req-two", Owner: "anna@example.com"}, "web"); err == nil || !strings.Contains(err.Error(), "limit is 1") {
		t.Fatalf("per-owner limit should apply to pending requests too, got %v", err)
	}
	// Self-approval is refused; another approver works.
	if _, err := h.svc.Approve(ctx, "req-one", "anna@example.com"); err == nil {
		t.Fatal("self-approval must be refused")
	}
	acc, err := h.svc.Approve(ctx, "req-one", "ops@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if acc.Status != core.StatusCreating || acc.RequestedBy != "anna@example.com" || acc.ApprovedBy != "ops@example.com" {
		t.Fatalf("approved = %+v", acc)
	}
	if pending, _ := h.svc.PendingRequests(ctx); len(pending) != 0 {
		t.Fatal("approved request should leave the queue")
	}
	acc, _ = h.svc.PollCreate(ctx, acc.RequestID)
	tags := h.prov.Tags(acc.ProviderID)
	if tags[core.TagRequestedBy] != "anna@example.com" || tags[core.TagApprovedBy] != "ops@example.com" || tags[core.TagPurpose] != "bedrock" || tags[core.TagBudget] != "20" {
		t.Fatalf("audit tags = %v", tags)
	}
	if h.note.msgs[len(h.note.msgs)-1].Owner != "anna@example.com" {
		t.Fatal("requester should be told about the approval")
	}
	if _, err := h.svc.Approve(ctx, "req-one", "ops@example.com"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("approving twice should be not found, got %v", err)
	}
}

func TestApprovedRequestFillsTheCreatingRowUntilPlaced(t *testing.T) {
	h := newHarness(t)
	h.prov.PollsToDone = 5 // stays in progress for a while
	ctx := context.Background()
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "slow", Owner: "dev@example.com", TTL: 10 * 24 * time.Hour, BudgetUSD: 100, Purpose: "demo"}, "web"); err != nil {
		t.Fatal(err)
	}
	acc, err := h.svc.Approve(ctx, "slow", "lead@example.com")
	if err != nil {
		t.Fatal(err)
	}
	// Not pending any more, but the record is kept and marked approved.
	if pending, _ := h.svc.PendingRequests(ctx); len(pending) != 0 {
		t.Fatal("approved request must leave the pending list")
	}
	tags, _ := h.prov.OUTags(ctx)
	rec, err := core.DecodeRequest(core.RequestTagPrefix+"slow", tags[core.RequestTagPrefix+"slow"])
	if err != nil || !rec.Approved || rec.ApprovedBy != "lead@example.com" || rec.CreateRequestID != acc.RequestID {
		t.Fatalf("approved record = %+v %v", rec, err)
	}
	// While AWS is creating, the inventory row carries the request's facts.
	inv, _ := h.svc.Inventory(ctx, true)
	var row *core.Account
	for _, a := range inv {
		if a.Name == "slow" {
			row = a
		}
	}
	if row == nil || row.Status != core.StatusCreating {
		t.Fatalf("expected a creating row, got %+v", row)
	}
	if row.Owner != "dev@example.com" || row.BudgetUSD != 100 || row.Purpose != "demo" || row.ApprovedBy != "lead@example.com" || !row.ExpiresAt.Equal(t0.Add(10*24*time.Hour)) {
		t.Fatalf("creating row should be filled from the record: %+v", row)
	}
	// The owner's slot is taken while it is creating.
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "another", Owner: "dev@example.com"}, "web"); err == nil {
		t.Fatal("creating account should count toward the per-owner limit")
	}
	// Placement removes the record.
	h.prov.Settle()
	if sum, _ := h.svc.Refresh(ctx); len(sum.Placed) != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	if tags, _ := h.prov.OUTags(ctx); len(tags) != 0 {
		t.Fatalf("record should be gone after placement: %v", tags)
	}
	if sum, _ := h.svc.Refresh(ctx); !sum.Empty() {
		t.Fatalf("second refresh should be quiet: %s", sum)
	}
}

func TestFailedCreationKeepsOwnerAndStaleRecordsAreCleaned(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.svc.SubmitRequest(ctx, core.RequestInput{Name: "doomed", Owner: "dev@example.com"}, "web")
	h.prov.FailNext = "EMAIL_ALREADY_EXISTS"
	if _, err := h.svc.Approve(ctx, "doomed", "lead@example.com"); err != nil {
		t.Fatal(err)
	}
	inv, _ := h.svc.Inventory(ctx, true)
	if len(inv) != 1 || inv[0].Status != core.StatusFailed || inv[0].Owner != "dev@example.com" || inv[0].LastError == "" {
		t.Fatalf("failed row should still name the owner: %+v", inv)
	}
	// Past the failed retention the record is dropped with the failure.
	h.advance(8 * 24 * time.Hour)
	if _, err := h.svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if tags, _ := h.prov.OUTags(ctx); len(tags) != 0 {
		t.Fatalf("stale approved record should be cleaned: %v", tags)
	}
}

func TestDenyWithdrawAndExpire(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "deny-me", Owner: "anna@example.com"}, "cli"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Deny(ctx, "deny-me", "ops@example.com", "no budget this quarter"); err != nil {
		t.Fatal(err)
	}
	last := h.note.msgs[len(h.note.msgs)-1]
	if last.Owner != "anna@example.com" || !strings.Contains(last.Body, "no budget this quarter") {
		t.Fatalf("deny notification = %+v", last)
	}
	// After a deny the owner may request again.
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "again", Owner: "anna@example.com"}, "cli"); err != nil {
		t.Fatalf("request after deny: %v", err)
	}
	if err := h.svc.Withdraw(ctx, "again", "anna@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "stale", Owner: "anna@example.com"}, "cli"); err != nil {
		t.Fatal(err)
	}
	h.advance(8 * 24 * time.Hour)
	sum, err := h.svc.Refresh(ctx)
	if err != nil || len(sum.Expired) != 1 || sum.Expired[0] != "stale" {
		t.Fatalf("stale request should expire: %+v %v", sum, err)
	}
	if pending, _ := h.svc.PendingRequests(ctx); len(pending) != 0 {
		t.Fatal("queue should be empty")
	}
}

func TestTTLAndBudgetCeilingsWithAdminOverride(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "long", Owner: "a@example.com", TTL: 120 * 24 * time.Hour}, "web"); err == nil || !strings.Contains(err.Error(), "maximum of 90d") {
		t.Fatalf("ttl over 90d should be refused, got %v", err)
	}
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "rich", Owner: "a@example.com", BudgetUSD: 501}, "web"); err == nil || !strings.Contains(err.Error(), "maximum of $500") {
		t.Fatalf("budget over $500 should be refused, got %v", err)
	}
	// At the ceiling is fine.
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "edge", Owner: "b@example.com", TTL: 90 * 24 * time.Hour, BudgetUSD: 500}, "web"); err != nil {
		t.Fatalf("values at the ceiling should pass: %v", err)
	}
	// Direct create is held to the same ceilings.
	if _, err := h.svc.Request(ctx, core.RequestInput{Name: "direct", Owner: "c@example.com", BudgetUSD: 9999}); err == nil {
		t.Fatal("direct create over the ceiling should be refused")
	}
	// An admin override is recorded on the request, survives the tag round trip, and lets approval create it.
	r, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "big", Owner: "d@example.com", TTL: 180 * 24 * time.Hour, BudgetUSD: 2000, OverrideLimits: true}, "web")
	if err != nil || !r.OverrideLimits {
		t.Fatalf("override submit = %+v %v", r, err)
	}
	if !strings.Contains(h.note.msgs[len(h.note.msgs)-1].Body, "Limits overridden") {
		t.Fatal("approvers should be told the limits were overridden")
	}
	stored, _ := h.svc.GetRequest(ctx, "big")
	if !stored.OverrideLimits || stored.TTL != 180*24*time.Hour || stored.BudgetUSD != 2000 {
		t.Fatalf("stored = %+v", stored)
	}
	acc, err := h.svc.Approve(ctx, "big", "ops@example.com")
	if err != nil || acc.BudgetUSD != 2000 || !acc.ExpiresAt.Equal(t0.Add(180*24*time.Hour)) {
		t.Fatalf("approval of an overridden request should create it: %+v %v", acc, err)
	}
	// Extensions are held to the same lifetime ceiling, counted from creation.
	h.advance(time.Minute)
	acc, err = h.svc.Approve(ctx, "edge", "ops@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if acc, err = h.svc.PollCreate(ctx, acc.RequestID); err != nil || acc.Status != core.StatusActive {
		t.Fatalf("place edge: %+v %v", acc, err)
	}
	if _, err := h.svc.Extend(ctx, acc.ID, acc.ExpiresAt.Add(24*time.Hour), "d@example.com", false); err == nil || !strings.Contains(err.Error(), "past the 90d ceiling") {
		t.Fatalf("extend past the lifetime ceiling should be refused, got %v", err)
	}
	if got, _ := h.svc.Get(ctx, acc.ID); !got.ExpiresAt.Equal(acc.ExpiresAt) {
		t.Fatal("a refused extend must not change the expiry")
	}
	ext, err := h.svc.Extend(ctx, acc.ID, acc.ExpiresAt.Add(24*time.Hour), "ops@example.com", true)
	if err != nil || !ext.ExpiresAt.Equal(acc.ExpiresAt.Add(24*time.Hour)) {
		t.Fatalf("an admin override should extend past the ceiling: %+v %v", ext, err)
	}
	// Old-format values without the flags field still decode.
	old, err := core.DecodeRequest(core.RequestTagPrefix+"legacy", "v1|x%40example.com|24|5|x%40example.com|1789570012|cli|")
	if err != nil || old.OverrideLimits || old.TTL != 24*time.Hour {
		t.Fatalf("legacy decode = %+v %v", old, err)
	}
}

func TestUpdateRequestBeforeApproval(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "edit-me", Owner: "anna@example.com", TTL: 5 * 24 * time.Hour, BudgetUSD: 20, Purpose: "old"}, "web"); err != nil {
		t.Fatal(err)
	}
	h.active(t, "busy-one", "sam@example.com", 0) // sam is at the limit
	h.note.msgs = nil

	// Moving the request to an owner at the limit is refused.
	if _, err := h.svc.UpdateRequest(ctx, "edit-me", "ops@example.com", core.RequestEdit{Owner: "sam@example.com"}); err == nil {
		t.Fatal("new owner at the limit should be refused")
	}
	// Ceilings apply to edits unless overridden.
	if _, err := h.svc.UpdateRequest(ctx, "edit-me", "ops@example.com", core.RequestEdit{BudgetUSD: 900}); err == nil {
		t.Fatal("budget over the ceiling should be refused")
	}
	newPurpose := "new purpose"
	yes, no := true, false
	r, err := h.svc.UpdateRequest(ctx, "edit-me", "ops@example.com", core.RequestEdit{Owner: "bob@example.com", TTL: 10 * 24 * time.Hour, BudgetUSD: 900, Purpose: &newPurpose, OverrideLimits: &yes})
	if err != nil {
		t.Fatal(err)
	}
	if r.Owner != "bob@example.com" || r.TTL != 10*24*time.Hour || r.BudgetUSD != 900 || r.Purpose != "new purpose" || !r.OverrideLimits {
		t.Fatalf("edited = %+v", r)
	}
	if r.RequestedBy != "anna@example.com" || !r.RequestedAt.Equal(t0) {
		t.Fatal("editing must not change who asked or when")
	}
	stored, _ := h.svc.GetRequest(ctx, "edit-me")
	if stored != r {
		t.Fatalf("stored = %+v want %+v", stored, r)
	}
	last := h.note.msgs[len(h.note.msgs)-1]
	if last.Owner != "anna@example.com" || !strings.Contains(last.Body, "owner anna@example.com → bob@example.com") || !strings.Contains(last.Body, "lifetime 5d → 10d") {
		t.Fatalf("original owner should be told what changed: %+v", last)
	}
	// A no-op edit sends nothing.
	n := len(h.note.msgs)
	if _, err := h.svc.UpdateRequest(ctx, "edit-me", "ops@example.com", core.RequestEdit{OverrideLimits: &yes}); err != nil || len(h.note.msgs) != n {
		t.Fatal("unchanged edit should be silent")
	}
	// An edit that says nothing about the override keeps it; one that
	// removes it is refused while the values still need it, and reported
	// when they do not.
	if r, err := h.svc.UpdateRequest(ctx, "edit-me", "ops@example.com", core.RequestEdit{Purpose: &newPurpose}); err != nil || !r.OverrideLimits {
		t.Fatalf("editing another field must keep the override: %+v %v", r, err)
	}
	if _, err := h.svc.UpdateRequest(ctx, "edit-me", "ops@example.com", core.RequestEdit{OverrideLimits: &no}); err == nil {
		t.Fatal("removing the override while the budget is over the ceiling should be refused")
	}
	if r, err := h.svc.UpdateRequest(ctx, "edit-me", "ops@example.com", core.RequestEdit{BudgetUSD: 100, OverrideLimits: &no}); err != nil || r.OverrideLimits || !strings.Contains(h.note.msgs[len(h.note.msgs)-1].Body, "limit override removed") {
		t.Fatalf("removing the override should be recorded: %+v %v", r, err)
	}
	// The owner of the account may not approve it either, even when someone else asked.
	if _, err := h.svc.Approve(ctx, "edit-me", "bob@example.com"); err == nil || !strings.Contains(err.Error(), "would own") {
		t.Fatalf("owner approving their own account should be refused, got %v", err)
	}
	// Approval uses the edited values.
	acc, err := h.svc.Approve(ctx, "edit-me", "ops@example.com")
	if err != nil || acc.Owner != "bob@example.com" || acc.BudgetUSD != 100 || acc.Purpose != "new purpose" {
		t.Fatalf("approved = %+v %v", acc, err)
	}
	if _, err := h.svc.UpdateRequest(ctx, "gone", "ops@example.com", core.RequestEdit{}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("editing a missing request = %v", err)
	}
}

// recorder captures audit events in order.
type recorderAudit struct{ events []core.AuditEvent }

func (r *recorderAudit) Record(_ context.Context, e core.AuditEvent) error {
	r.events = append(r.events, e)
	return nil
}

func (r *recorderAudit) kinds() []string {
	var out []string
	for _, e := range r.events {
		out = append(out, e.Event)
	}
	return out
}

func TestLifecycleIsWrittenToHistory(t *testing.T) {
	h := newHarness(t)
	rec := &recorderAudit{}
	h.svc.SetAuditor(rec)
	ctx := context.Background()

	h.svc.SubmitRequest(ctx, core.RequestInput{Name: "hist", Owner: "anna@example.com", TTL: 5 * 24 * time.Hour, BudgetUSD: 20}, "web")
	newP := "changed"
	h.svc.UpdateRequest(ctx, "hist", "ops@example.com", core.RequestEdit{Purpose: &newP})
	a, _ := h.svc.Approve(ctx, "hist", "lead@example.com")
	a, _ = h.svc.PollCreate(ctx, a.RequestID)
	h.svc.Extend(ctx, a.ID, a.ExpiresAt.Add(48*time.Hour), "anna@example.com", false)
	h.svc.RequestClose(ctx, a.ID, "anna@example.com")

	want := []string{"requested", "edited", "approved", "placed", "budget-protection", "extended", "close-requested", "closed"}
	got := rec.kinds()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for _, e := range rec.events {
		if e.Account != "hist" || e.Owner != "anna@example.com" || e.Actor == "" || e.Message == "" || e.At.IsZero() {
			t.Fatalf("event missing fields: %+v", e)
		}
	}
	if rec.events[0].Via != "web" || rec.events[2].Actor != "lead@example.com" || rec.events[3].Actor != core.ActorSystem || rec.events[3].AccountID == "" {
		t.Fatalf("actors or ids wrong: %+v", rec.events)
	}
	if rec.events[1].Details["changes"] != "purpose updated" {
		t.Fatalf("edit details = %v", rec.events[1].Details)
	}

	// Deny, withdraw, and request expiry are recorded with their reasons.
	rec.events = nil
	h.svc.SubmitRequest(ctx, core.RequestInput{Name: "no-go", Owner: "bob@example.com"}, "cli")
	h.svc.Deny(ctx, "no-go", "lead@example.com", "not now")
	h.svc.SubmitRequest(ctx, core.RequestInput{Name: "oops", Owner: "bob@example.com"}, "cli")
	h.svc.Withdraw(ctx, "oops", "bob@example.com")
	h.svc.SubmitRequest(ctx, core.RequestInput{Name: "stale", Owner: "bob@example.com"}, "cli")
	h.advance(8 * 24 * time.Hour)
	h.svc.Refresh(ctx)
	got = rec.kinds()
	if strings.Join(got, ",") != "requested,denied,requested,withdrawn,requested,request-expired" {
		t.Fatalf("events = %v", got)
	}
	if rec.events[1].Details["reason"] != "not now" {
		t.Fatalf("deny reason missing: %+v", rec.events[1])
	}
	// A failing sink never breaks the operation.
	h.svc.SetAuditor(failingAudit{})
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "still-works", Owner: "carl@example.com"}, "cli"); err != nil {
		t.Fatalf("a broken history sink must not block work: %v", err)
	}
}

type failingAudit struct{}

func (failingAudit) Record(context.Context, core.AuditEvent) error { return errors.New("disk full") }

func TestPerOwnerLimitCountsOpenAccounts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.active(t, "have-one", "anna@example.com", 0)
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "want-two", Owner: "anna@example.com"}, "web"); err == nil {
		t.Fatal("owner with an open account is at the limit of 1")
	}
	// Closing frees the slot.
	a, _ := h.svc.Resolve(ctx, "have-one")
	if _, err := h.svc.RequestClose(ctx, a.ID, "ops@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "want-two", Owner: "anna@example.com"}, "web"); err != nil {
		t.Fatalf("after close the owner may request again: %v", err)
	}
}

func TestApproveClaimsTheRecordFirst(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "twice", Owner: "dev@example.com", RequestedBy: "dev@example.com"}, "web"); err != nil {
		t.Fatal(err)
	}
	// Two approvers race. Exactly one wins; the other finds nothing pending
	// and no second account is requested.
	var wg sync.WaitGroup
	var mu sync.Mutex
	var oks, fails int
	for _, who := range []string{"lead@example.com", "root@example.com"} {
		wg.Add(1)
		go func(who string) {
			defer wg.Done()
			_, err := h.svc.Approve(ctx, "twice", who)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				fails++
			} else {
				oks++
			}
		}(who)
	}
	wg.Wait()
	creates, _ := h.prov.ListCreateRequests(ctx)
	if oks != 1 || fails != 1 || len(creates) != 1 {
		t.Fatalf("oks=%d fails=%d creates=%d", oks, fails, len(creates))
	}

	// When the creation itself fails, the claim is undone and the request
	// is pending again for someone to fix and retry.
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "ghost", Owner: "gone@example.com", RequestedBy: "dev@example.com"}, "web"); err != nil {
		t.Fatal(err)
	}
	h.prov.AccessOn = true // gone@example.com is not in the identity store, so Request refuses
	if _, err := h.svc.Approve(ctx, "ghost", "lead@example.com"); err == nil || !strings.Contains(err.Error(), "not a user") {
		t.Fatalf("approve should surface the creation error, got %v", err)
	}
	q, err := h.svc.GetRequest(ctx, "ghost")
	if err != nil || q.Approved || q.ApprovedBy != "" {
		t.Fatalf("failed approval should leave the request pending: %+v %v", q, err)
	}
}

func TestQueueRefusesWhenTheOUHasNoTagLeft(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	filler := map[string]string{}
	for i := 0; i < core.MaxTagsPerResource-1; i++ {
		filler[fmt.Sprintf("other:%d", i)] = "x"
	}
	if err := h.prov.SetOUTags(ctx, filler); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "last-one", Owner: "a@example.com"}, "cli"); err != nil {
		t.Fatalf("the last free tag should be usable: %v", err)
	}
	_, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "one-too-many", Owner: "b@example.com"}, "cli")
	if err == nil || !strings.Contains(err.Error(), "queue is full") {
		t.Fatalf("a full OU should be refused with an explanation, got %v", err)
	}
}

func TestClosedNamesAreNotReused(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.active(t, "reuse-me", "a@example.com", 5*24*time.Hour)
	if _, err := h.svc.RequestClose(ctx, a.ID, "ops"); err != nil {
		t.Fatal(err)
	}
	// AWS never frees the root email, so both the queue and direct create refuse the name.
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "reuse-me", Owner: "b@example.com"}, "web"); err == nil || !strings.Contains(err.Error(), "never frees") {
		t.Fatalf("queueing a closed name should be refused, got %v", err)
	}
	if _, err := h.svc.Request(ctx, core.RequestInput{Name: "reuse-me", Owner: "b@example.com"}); err == nil || !strings.Contains(err.Error(), "never frees") {
		t.Fatalf("direct create of a closed name should be refused, got %v", err)
	}
	// With a fresh root email the name itself is fine.
	if _, err := h.svc.Request(ctx, core.RequestInput{Name: "reuse-me", Owner: "b@example.com", Email: "fresh@example.com"}); err != nil {
		t.Fatalf("a closed name with a new email should be allowed: %v", err)
	}
	// A failed creation does not hold a name either.
	h.prov.FailNext = "INTERNAL_FAILURE"
	if _, err := h.svc.Request(ctx, core.RequestInput{Name: "flaky", Owner: "c@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "flaky", Owner: "c@example.com"}, "web"); err != nil {
		t.Fatalf("a failed creation should not block the name: %v", err)
	}
}

func TestSubHourTTLRoundsUpInsteadOfVanishing(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "blink", Owner: "a@example.com", TTL: 20 * time.Minute}, "cli"); err != nil {
		t.Fatal(err)
	}
	q, _ := h.svc.GetRequest(ctx, "blink")
	if q.TTL != time.Hour {
		t.Fatalf("a 20 minute TTL should be stored as one hour, got %s", q.TTL)
	}
	acc, err := h.svc.Approve(ctx, "blink", "ops@example.com")
	if err != nil || !acc.ExpiresAt.Equal(h.now.Add(time.Hour)) {
		t.Fatalf("approval must keep the short TTL, not fall back to the default: %+v %v", acc, err)
	}
}

func TestUnreadableRequestTagsDoNotPanic(t *testing.T) {
	// A length prefix that overflows int used to slice with a negative bound.
	for _, v := range []string{
		"v2 99999999999999999999:x 1:1 1:1 1:a 1:0 1:w 0: 0:",
		"v2 9223372036854775807:x",
		"v2 3:ab",
		"v2",
	} {
		if _, err := core.DecodeRequest("playplace:req:foo", v); err == nil {
			t.Errorf("%q should not decode", v)
		}
	}
}

func TestBudgetMustBeANumber(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	nan := math.NaN()
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "nan-one", Owner: "alex", BudgetUSD: nan}, "web"); err == nil {
		t.Fatal("SubmitRequest accepted a NaN budget")
	}
	if _, err := h.svc.Request(ctx, core.RequestInput{Name: "nan-two", Owner: "alex", BudgetUSD: nan}); err == nil {
		t.Fatal("Request accepted a NaN budget")
	}
	if _, err := h.svc.Request(ctx, core.RequestInput{Name: "inf-one", Owner: "alex", BudgetUSD: math.Inf(1), OverrideLimits: true}); err == nil {
		t.Fatal("Request accepted an infinite budget with override")
	}
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "edit-me", Owner: "alex"}, "web"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.UpdateRequest(ctx, "edit-me", "admin", core.RequestEdit{BudgetUSD: math.Inf(1)}); err == nil {
		t.Fatal("UpdateRequest accepted an infinite budget")
	}
	if tags, _ := h.prov.OUTags(ctx); len(tags) != 1 {
		t.Fatalf("only the valid request should be queued: %v", tags)
	}
}

func TestApprovedRecordOfPlacedAccountIsCleaned(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "sticky", Owner: "dev@example.com"}, "web"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Approve(ctx, "sticky", "lead@example.com"); err != nil {
		t.Fatal(err)
	}
	tags, _ := h.prov.OUTags(ctx)
	record := tags[core.RequestTagPrefix+"sticky"]
	if record == "" {
		t.Fatal("approved record should be in the queue until placement")
	}
	h.prov.Settle()
	if _, err := h.svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	// Placement removes the record; pretend that call failed and it stayed.
	h.prov.SetOUTags(ctx, map[string]string{core.RequestTagPrefix + "sticky": record})
	if _, err := h.svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if tags, _ := h.prov.OUTags(ctx); len(tags) != 1 {
		t.Fatalf("a fresh record should be kept while the inventory may still use it: %v", tags)
	}
	h.advance(25 * time.Hour)
	if _, err := h.svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if tags, _ := h.prov.OUTags(ctx); len(tags) != 0 {
		t.Fatalf("record of a placed account should be dropped after a day: %v", tags)
	}
}

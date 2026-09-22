package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
)

// These faults model remote observations and failed writes, not a second
// implementation of readiness policy. Tests enter through PollCreate/Refresh.
type readinessProvider struct {
	core.Provider
	pendingBudget    bool
	grantFailure     bool
	failTag          string
	failValue        string
	partialPlacement bool
}

func (p *readinessProvider) EnsureBudget(ctx context.Context, id string, spec core.BudgetSpec) (core.BudgetProtection, error) {
	if p.pendingBudget {
		return core.BudgetProtection{State: "setup-pending"}, nil
	}
	return p.Provider.EnsureBudget(ctx, id, spec)
}

func (p *readinessProvider) GrantAccess(ctx context.Context, id string, user core.User) error {
	if p.grantFailure {
		return errors.New("grant unavailable")
	}
	return p.Provider.GrantAccess(ctx, id, user)
}

func (p *readinessProvider) SetTags(ctx context.Context, id string, tags map[string]string) error {
	if value, ok := tags[p.failTag]; ok && (p.failValue == "" || value == p.failValue) {
		return errors.New("tag write unavailable")
	}
	return p.Provider.SetTags(ctx, id, tags)
}

func (p *readinessProvider) PlaceAccount(ctx context.Context, id string, tags map[string]string) error {
	if p.partialPlacement {
		// AWS can move successfully before failing to write placement tags.
		if err := p.Provider.PlaceAccount(ctx, id, nil); err != nil {
			return err
		}
		return errors.New("moved but tagging failed")
	}
	return p.Provider.PlaceAccount(ctx, id, tags)
}

func readinessService(h *harness, p core.Provider) *core.Service {
	s := core.NewService(p, h.note, h.svc.Config(), nil)
	s.Now = func() time.Time { return h.now }
	return s
}

func readyNotices(h *harness) int {
	n := 0
	for _, m := range h.note.msgs {
		if m.Subject == "Playground account ready" {
			n++
		}
	}
	return n
}

func TestReadinessPendingSetupResumesOnceAcrossRestarts(t *testing.T) {
	for _, entry := range []string{"poll", "refresh"} {
		t.Run(entry, func(t *testing.T) {
			h := newHarness(t)
			h.prov.AccessOn = true
			h.prov.AddUser("user1", "owner", "owner@example.com")
			p := &readinessProvider{Provider: h.prov, pendingBudget: true}
			h.svc = readinessService(h, p)
			audit := &recorderAudit{}
			h.svc.SetAuditor(audit)
			ctx := context.Background()
			request, err := h.svc.Request(ctx, core.RequestInput{Name: "delayed-ready", Owner: "owner"})
			if err != nil {
				t.Fatal(err)
			}
			if entry == "poll" {
				if a, err := h.svc.PollCreate(ctx, request.RequestID); err == nil || a == nil {
					t.Fatalf("want placed account awaiting protection: %+v %v", a, err)
				}
			} else {
				h.prov.Settle()
				sum, err := h.svc.Refresh(ctx)
				if err == nil || len(sum.Placed) != 1 {
					t.Fatalf("placement must not wait for readiness: %+v %v", sum, err)
				}
			}
			if readyNotices(h) != 0 || len(h.prov.Grants) != 0 {
				t.Fatal("premature handoff/access")
			}
			if !strings.Contains(strings.Join(audit.kinds(), ","), "placed") {
				t.Fatal("physical placement was not audited")
			}
			p.pendingBudget = false
			h.svc = readinessService(h, p)
			if _, err := h.svc.Refresh(ctx); err != nil {
				t.Fatal(err)
			}
			a, err := h.svc.Resolve(ctx, "delayed-ready")
			if err != nil || a.AccessGrantedTo != "user1" || readyNotices(h) != 1 {
				t.Fatalf("missing eventual handoff: %+v %v notices=%d", a, err, readyNotices(h))
			}
			for range 2 {
				h.svc = readinessService(h, p)
				h.svc.SetAuditor(audit)
				if _, err := h.svc.PollCreate(ctx, request.RequestID); err != nil {
					t.Fatal(err)
				}
				if _, err := h.svc.Refresh(ctx); err != nil {
					t.Fatal(err)
				}
			}
			p.pendingBudget = true
			_, _ = h.svc.Refresh(ctx)
			p.pendingBudget = false
			_, _ = h.svc.Refresh(ctx)
			if readyNotices(h) != 1 {
				t.Fatal("handoff repeated after polling, restart or repair")
			}
			placements := 0
			for _, event := range audit.events {
				if event.Event == "placed" {
					placements++
				}
			}
			if placements != 1 {
				t.Fatalf("placement history repeated: %d", placements)
			}
		})
	}
}

func TestReadinessWaitsForAccessAndItsDurableGrant(t *testing.T) {
	for _, failure := range []string{"grant", "grant-tag"} {
		t.Run(failure, func(t *testing.T) {
			h := newHarness(t)
			h.prov.AccessOn = true
			h.prov.AddUser("user1", "owner", "owner@example.com")
			p := &readinessProvider{Provider: h.prov, grantFailure: failure == "grant"}
			if failure == "grant-tag" {
				p.failTag = core.TagAccess
			}
			h.svc = readinessService(h, p)
			ctx := context.Background()
			r, err := h.svc.Request(ctx, core.RequestInput{Name: "access-retry", Owner: "owner"})
			if err != nil {
				t.Fatal(err)
			}
			a, err := h.svc.PollCreate(ctx, r.RequestID)
			if err == nil || a == nil || readyNotices(h) != 0 {
				t.Fatalf("premature readiness: %+v %v", a, err)
			}
			p.grantFailure, p.failTag = false, ""
			h.svc = readinessService(h, p)
			if _, err := h.svc.Refresh(ctx); err != nil {
				t.Fatal(err)
			}
			if readyNotices(h) != 1 || h.prov.Tags(a.ID)[core.TagAccess] != "user1" {
				t.Fatal("handoff did not resume")
			}
		})
	}
}

func TestReadinessSurvivesCreationAndPlacementInterruptions(t *testing.T) {
	for _, failure := range []string{"before-placement", "after-move"} {
		t.Run(failure, func(t *testing.T) {
			h := newHarness(t)
			p := &readinessProvider{Provider: h.prov, partialPlacement: failure == "after-move"}
			h.svc = readinessService(h, p)
			ctx := context.Background()
			r, err := h.svc.Request(ctx, core.RequestInput{Name: "placement-retry", Owner: "owner"})
			if err != nil {
				t.Fatal(err)
			}
			if failure == "after-move" {
				if _, err := h.svc.PollCreate(ctx, r.RequestID); err == nil {
					t.Fatal("expected placement error")
				}
			} else {
				h.prov.Settle()
			}
			p.partialPlacement = false
			h.svc = readinessService(h, p)
			if _, err := h.svc.Refresh(ctx); err != nil {
				t.Fatal(err)
			}
			if readyNotices(h) != 1 {
				t.Fatal("interrupted placement lost handoff eligibility")
			}
		})
	}
}

func TestReadinessAdoptionAndLegacyEligibility(t *testing.T) {
	for _, tc := range []struct {
		name, owner                    string
		managed, badExpiry, wantNotice bool
	}{
		{"adopted", "some team", false, false, true},
		{"ownerless", "", false, false, false},
		{"placeholder", " UnKnOwN ", false, false, false},
		{"legacy", "owner", true, false, false},
		{"legacy-repair", "owner", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			tags := map[string]string{core.TagOwner: tc.owner, core.TagBudget: "50", core.TagExpires: t0.Add(14 * 24 * time.Hour).Format(time.RFC3339)}
			if tc.managed {
				tags[core.TagManaged] = "true"
			}
			if tc.badExpiry {
				tags[core.TagExpires] = "broken"
			}
			h.prov.Seed(core.RemoteAccount{ProviderID: "111111111111", Name: tc.name, JoinedAt: t0, Tags: tags})
			p := &readinessProvider{Provider: h.prov, pendingBudget: true}
			h.svc = readinessService(h, p)
			_, _ = h.svc.Refresh(ctx) // genuine adoption persists, then setup waits
			if tc.badExpiry {
				if h.prov.Tags("111111111111")[core.TagExpires] != "broken" {
					t.Fatal("owned expiry damage was silently repaired")
				}
				// Explicit operator repair must not enroll a legacy handoff.
				if err := h.prov.SetTags(ctx, "111111111111", map[string]string{core.TagExpires: t0.Add(14 * 24 * time.Hour).Format(time.RFC3339)}); err != nil {
					t.Fatal(err)
				}
			}
			p.pendingBudget = false
			h.svc = readinessService(h, p)
			if _, err := h.svc.Refresh(ctx); err != nil {
				t.Fatal(err)
			}
			want := 0
			if tc.wantNotice {
				want = 1
			}
			if readyNotices(h) != want {
				t.Fatalf("notices=%d want=%d", readyNotices(h), want)
			}
			if !tc.managed && !tc.wantNotice {
				if err := h.prov.SetTags(ctx, "111111111111", map[string]string{core.TagOwner: "identified team"}); err != nil {
					t.Fatal(err)
				}
				if _, err := h.svc.Refresh(ctx); err != nil {
					t.Fatal(err)
				}
				if readyNotices(h) != 1 {
					t.Fatal("owner repair did not allow initial handoff")
				}
			}
		})
	}
}

func TestReadinessRestrictionDefersInitialHandoff(t *testing.T) {
	h := newHarness(t)
	enforceBudgets(h)
	ctx := context.Background()
	r, err := h.svc.Request(ctx, core.RequestInput{Name: "restricted-start", Owner: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	h.prov.Settle()
	created, err := h.prov.CreateStatus(ctx, r.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	h.prov.BudgetStates[created.ProviderID] = core.BudgetSnapshot{ActionID: "owned", ActionStatus: "EXECUTION_SUCCESS", SpendKnown: true, SpendUSD: 60}
	if _, err := h.svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if readyNotices(h) != 0 {
		t.Fatal("restricted account announced ready")
	}
	snap := h.prov.BudgetStates[created.ProviderID]
	snap.ActionStatus, snap.SpendUSD = "STANDBY", 0
	h.prov.BudgetStates[created.ProviderID] = snap
	if _, err := h.svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if readyNotices(h) != 1 {
		t.Fatal("handoff not completed after restriction lifted")
	}
}

func TestReadinessNeverHandsOffExpiredOrClosingAccounts(t *testing.T) {
	for _, mode := range []string{"expired", "intent", "pending-closure", "closed", "suspended"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			h.prov.AccessOn = true
			h.prov.AddUser("user1", "owner", "owner@example.com")
			ctx := context.Background()
			r, err := h.svc.Request(ctx, core.RequestInput{Name: "unsafe-handoff", Owner: "owner", TTL: time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			h.prov.Settle()
			created, _ := h.prov.CreateStatus(ctx, r.RequestID)
			a, _ := h.prov.GetAccount(ctx, created.ProviderID)
			switch mode {
			case "expired":
				h.advance(2 * time.Hour)
			case "intent":
				a.Tags[core.TagCloseRequested] = t0.Format(time.RFC3339)
			case "pending-closure":
				a.Status = "PENDING_CLOSURE"
			case "closed":
				a.Status = "CLOSED"
			case "suspended":
				a.Status = "SUSPENDED"
			}
			h.prov.Seed(a)
			_, _ = h.svc.PollCreate(ctx, r.RequestID)
			_, _ = h.svc.Refresh(ctx)
			if readyNotices(h) != 0 || len(h.prov.Grants[a.ProviderID]) != 0 {
				t.Fatal("unsafe handoff/access")
			}
			if mode == "expired" && len(h.prov.Closed) != 1 {
				t.Fatal("readiness blocked expiry")
			}
		})
	}
}

func TestReadinessDurableWritesMustSucceedBeforeHandoff(t *testing.T) {
	for _, phase := range []string{"pending", "complete"} {
		t.Run(phase, func(t *testing.T) {
			h := newHarness(t)
			p := &readinessProvider{Provider: h.prov, failTag: core.TagHandoff, failValue: phase}
			h.svc = readinessService(h, p)
			ctx := context.Background()
			r, err := h.svc.Request(ctx, core.RequestInput{Name: "durable-handoff", Owner: "owner"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h.svc.PollCreate(ctx, r.RequestID); err == nil {
				t.Fatal("expected handoff tag failure")
			}
			h.svc = readinessService(h, p)
			if _, err := h.svc.Refresh(ctx); err == nil {
				t.Fatal("write failure did not remain visible")
			}
			if readyNotices(h) != 0 {
				t.Fatal("notified without a durable milestone")
			}
			p.failTag = ""
			h.svc = readinessService(h, p)
			if _, err := h.svc.Refresh(ctx); err != nil {
				t.Fatal(err)
			}
			if readyNotices(h) != 1 {
				t.Fatal("handoff did not retry after tag repair")
			}
		})
	}
}

type handoffNotifier func(context.Context, core.Message) error

func (f handoffNotifier) Notify(ctx context.Context, m core.Message) error { return f(ctx, m) }

func TestReadinessDeliveryFailureDoesNotRepeatHandoff(t *testing.T) {
	for _, mode := range []string{"delivery-error", "crash-before-delivery"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			attempts := 0
			note := handoffNotifier(func(_ context.Context, m core.Message) error {
				if m.Subject != "Playground account ready" {
					return nil
				}
				attempts++
				if h.prov.Tags(m.AccountID)[core.TagHandoff] != "complete" {
					t.Error("notification preceded durable completion")
				}
				if mode == "crash-before-delivery" {
					panic("simulated crash")
				}
				return errors.New("notifier unavailable")
			})
			h.svc = core.NewService(h.prov, note, h.svc.Config(), nil)
			h.svc.Now = func() time.Time { return h.now }
			h.svc.SetAuditor(failingAudit{})
			ctx := context.Background()
			r, err := h.svc.Request(ctx, core.RequestInput{Name: "delivery-failure", Owner: "owner"})
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer func() {
					if v := recover(); v != nil && (mode != "crash-before-delivery" || v != "simulated crash") {
						panic(v)
					}
				}()
				if _, err := h.svc.PollCreate(ctx, r.RequestID); err != nil {
					t.Fatal(err)
				}
			}()
			if attempts != 1 {
				t.Fatalf("notification attempts=%d", attempts)
			}
			h.svc = readinessService(h, h.prov)
			if _, err := h.svc.Refresh(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := h.svc.PollCreate(ctx, r.RequestID); err != nil {
				t.Fatal(err)
			}
			if readyNotices(h) != 0 {
				t.Fatal("delivery was retried after durable completion")
			}
		})
	}
}

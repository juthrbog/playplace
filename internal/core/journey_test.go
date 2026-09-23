package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
)

func TestJourneySurvivesEditApprovalPlacementAndRestart(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	rec := &recorderAudit{}
	h.svc.SetAuditor(rec)
	r, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "journey", Owner: "anna@example.com"}, "web")
	if err != nil {
		t.Fatal(err)
	}
	if r.JourneyID == "" {
		t.Fatal("submission has no journey identity")
	}
	edited, err := h.svc.UpdateRequest(ctx, r.Name, "admin@example.com", core.RequestEdit{Owner: "bob@example.com"})
	if err != nil || edited.JourneyID != r.JourneyID {
		t.Fatalf("edit = %+v, %v", edited, err)
	}
	restarted := core.NewService(h.prov, nil, core.DefaultConfig(), nil)
	restarted.Now = h.svc.Now
	restarted.SetAuditor(rec)
	pending, err := restarted.GetRequest(ctx, r.Name)
	if err != nil || pending.JourneyID != r.JourneyID {
		t.Fatalf("reloaded = %+v, %v", pending, err)
	}
	a, err := restarted.Approve(ctx, r.Name, "lead@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if a.HistoryID() != r.JourneyID {
		t.Fatalf("approval lost journey: %+v", a)
	}
	inv, err := restarted.Inventory(ctx, true)
	if err != nil || len(inv) != 1 || inv[0].HistoryID() != r.JourneyID {
		t.Fatalf("creating inventory = %+v, %v", inv, err)
	}
	a, err = restarted.PollCreate(ctx, a.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if a.HistoryID() != r.JourneyID || h.prov.Tags(a.ProviderID)[core.TagJourney] != r.JourneyID {
		t.Fatalf("placement lost journey: %+v", a)
	}
	if _, err := restarted.Extend(ctx, a.ID, a.ExpiresAt.Add(time.Hour), "bob@example.com", false); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, e := range rec.events {
		if e.JourneyID != r.JourneyID || e.ID == "" || ids[e.ID] {
			t.Fatalf("invalid event identity: %+v", e)
		}
		ids[e.ID] = true
	}
	if rec.events[0].Owner != "anna@example.com" || rec.events[1].Owner != "bob@example.com" {
		t.Fatal("event ownership must be a snapshot")
	}
}

func TestReusedRequestNameStartsDistinctJourneyEvenAtSameTime(t *testing.T) {
	for _, terminal := range []string{"deny", "withdraw", "expire"} {
		t.Run(terminal, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			first, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "demo", Owner: "anna@example.com"}, "cli")
			if err != nil {
				t.Fatal(err)
			}
			switch terminal {
			case "deny":
				err = h.svc.Deny(ctx, "demo", "lead", "no")
			case "withdraw":
				err = h.svc.Withdraw(ctx, "demo", "anna@example.com")
			case "expire":
				h.advance(8 * 24 * time.Hour)
				_, err = h.svc.Refresh(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			next, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "demo", Owner: "bob@example.com"}, "web")
			if err != nil {
				t.Fatal(err)
			}
			if first.JourneyID == "" || next.JourneyID == "" || first.JourneyID == next.JourneyID {
				t.Fatalf("identities reused: %+v %+v", first, next)
			}
		})
	}
}

func TestDirectCreationAndAdoptionHaveAccountJourneys(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.active(t, "direct", "anna@example.com", time.Hour)
	if a.JourneyID == "" {
		t.Fatal("direct creation has no persisted journey")
	}
	h.prov.Seed(core.RemoteAccount{ProviderID: "987654321012", Name: "adopted", Status: "ACTIVE", JoinedAt: t0, Tags: map[string]string{core.TagOwner: "bob@example.com"}})
	if _, err := h.svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	b, err := h.svc.Resolve(ctx, "adopted")
	if err != nil || b.HistoryID() == "" || b.HistoryID() == a.HistoryID() || h.prov.Tags(b.ProviderID)[core.TagJourney] != b.HistoryID() {
		t.Fatalf("adoption = %+v, %v", b, err)
	}
}

func TestJourneyRequestEncodingPreservesApprovedIdentity(t *testing.T) {
	r := core.Request{Name: "encoded", JourneyID: "abcdefghijklmnopqrstuvwxyz", Owner: "a@example.com", RequestedBy: "a@example.com", RequestedAt: t0, Via: "web", TTL: 24 * time.Hour, BudgetUSD: 20, Approved: true, ApprovedBy: "lead@example.com", ApprovedAt: t0, CreateRequestID: "car-1234"}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if !core.ValidTagValue(r.Encode()) {
		t.Fatal("invalid AWS tag value")
	}
	got, err := core.DecodeRequest(r.Key(), r.Encode())
	if err != nil || got != r {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
	// Existing v2 records are readable, but a name alone cannot invent a journey.
	old, err := core.DecodeRequest("playplace:req:legacy", "v2 13:a@example.com 2:24 2:20 13:a@example.com 10:1789488000 3:web 0: 0:")
	if err != nil || old.JourneyID != "" {
		t.Fatalf("legacy = %+v, %v", old, err)
	}
	r.JourneyID = strings.Repeat("x", 257)
	if r.Validate() == nil {
		t.Fatal("oversized identity should be refused before provider writes")
	}
}

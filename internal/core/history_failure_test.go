package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"playplace/internal/core"
)

func TestFailedCreationRetainsRequestJourney(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	rec := &recorderAudit{}
	h.svc.SetAuditor(rec)
	r, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "failure", Owner: "alice@example.com"}, "web")
	if err != nil {
		t.Fatal(err)
	}
	h.prov.FailNext = "EMAIL_ALREADY_EXISTS"
	a, err := h.svc.Approve(ctx, r.Name, "lead@example.com")
	if err != nil {
		t.Fatal(err)
	}
	a, err = h.svc.PollCreate(ctx, a.RequestID)
	if err != nil || a.Status != core.StatusFailed || a.HistoryID() != r.JourneyID {
		t.Fatalf("failed creation=%+v %v", a, err)
	}
	e := rec.events[len(rec.events)-1]
	if e.Event != "failed" || e.JourneyID != r.JourneyID || e.Owner != r.Owner {
		t.Fatalf("failure event=%+v", e)
	}
}

type failingQueueRemoval struct {
	core.Provider
	fail bool
}

func (p *failingQueueRemoval) RemoveOUTags(ctx context.Context, keys []string) error {
	if p.fail {
		return errors.New("queue unavailable")
	}
	return p.Provider.RemoveOUTags(ctx, keys)
}

func TestRequestExpiryIsNotRecordedBeforeRemovalSucceeds(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	p := &failingQueueRemoval{Provider: h.prov}
	svc := core.NewService(p, h.note, core.DefaultConfig(), nil)
	svc.Now = h.svc.Now
	rec := &recorderAudit{}
	svc.SetAuditor(rec)
	if _, err := svc.SubmitRequest(ctx, core.RequestInput{Name: "expires", Owner: "alice@example.com"}, "web"); err != nil {
		t.Fatal(err)
	}
	h.advance(8 * 24 * time.Hour)
	p.fail = true
	before := len(h.note.msgs)
	sum, err := svc.Refresh(ctx)
	if err == nil || len(sum.Expired) != 0 || len(rec.events) != 1 || len(h.note.msgs) != before {
		t.Fatalf("failed removal claimed expiry: %+v %v %+v", sum, err, rec.events)
	}
	p.fail = false
	sum, err = svc.Refresh(ctx)
	if err != nil || len(sum.Expired) != 1 || len(rec.events) != 2 || rec.events[1].Event != "request-expired" {
		t.Fatalf("successful removal=%+v %v %+v", sum, err, rec.events)
	}
}

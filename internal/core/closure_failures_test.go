package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"playplace/internal/core"
)

type failClosureMarker struct{ core.Provider }

func (p failClosureMarker) SetTags(ctx context.Context, id string, tags map[string]string) error {
	if _, ok := tags[core.TagClosedAt]; ok {
		return errors.New("tagging unavailable")
	}
	return p.Provider.SetTags(ctx, id, tags)
}

func TestConfirmationMarkerFailureIsRetried(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.active(t, "marker-failure", "alex", 0)
	h.note.msgs = nil
	svc := core.NewService(failClosureMarker{h.prov}, h.note, core.DefaultConfig(), nil)
	svc.Now = func() time.Time { return h.now }
	if _, err := svc.RequestClose(ctx, a.ID, "ops"); err == nil {
		t.Fatal("confirmation marker failure must be surfaced")
	}
	if len(h.note.msgs) != 0 || h.prov.Tags(a.ID)[core.TagClosedAt] != "" {
		t.Fatal("failed confirmation should remain eligible for notification retry")
	}
	if _, err := svc.Refresh(ctx); err == nil {
		t.Fatal("closed-account observation must retry a failed marker write")
	}
	if sum, err := h.svc.Refresh(ctx); err != nil || len(sum.Closed) != 1 || len(h.note.msgs) != 1 {
		t.Fatalf("restored tagging should finish confirmation: %+v %v notes=%+v", sum, err, h.note.msgs)
	}
}

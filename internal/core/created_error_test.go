package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

// A creation poll that finds damaged guardrail tags fails with a
// CreatedError. Placed tells a creation that waits in the organization root
// from one that another process placed already. The poll moves neither.
func TestPollCreateSaysWhetherADamagedCreationIsPlaced(t *testing.T) {
	for _, placed := range []bool{false, true} {
		name := map[bool]string{false: "in-root", true: "in-ou"}[placed]
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			r, err := h.svc.Request(ctx, core.RequestInput{Name: name, Owner: "owner"})
			if err != nil {
				t.Fatal(err)
			}
			h.prov.Settle()
			created, err := h.prov.CreateStatus(ctx, r.RequestID)
			if err != nil || created.ProviderID == "" {
				t.Fatalf("creation: %+v %v", created, err)
			}
			id := created.ProviderID
			if placed {
				// Another process placed the account and recorded it.
				h.prov.MoveAccount(id, fake.OUID)
				if err := h.prov.SetTags(ctx, id, map[string]string{core.TagHandoff: "pending"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := h.prov.SetTags(ctx, id, map[string]string{core.TagExpires: "soon"}); err != nil {
				t.Fatal(err)
			}
			_, err = h.svc.PollCreate(ctx, r.RequestID)
			var ce *core.CreatedError
			if !errors.As(err, &ce) || ce.Placed != placed || ce.Account.ProviderID != id || ce.Account.Name != name {
				t.Fatalf("err = %v (%+v), want a CreatedError for %s with Placed %v", err, ce, id, placed)
			}
			if !strings.Contains(err.Error(), "operator tag repair required") {
				t.Fatalf("err = %v, want the repair error", err)
			}
			if inOU(t, h, id) != placed {
				t.Fatal("the poll moved an account with damaged tags")
			}
		})
	}
}

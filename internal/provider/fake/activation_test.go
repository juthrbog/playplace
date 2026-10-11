package fake

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"playplace/internal/core"
)

// ActivationPolls CreateStatus calls see a new account as PENDING_ACTIVATION.
// The count is the same when a CreateStatus call finishes the creation and
// when Settle finishes it.
func TestActivationPollsCountsTheSameAfterSettle(t *testing.T) {
	ctx := context.Background()
	for _, settle := range []bool{false, true} {
		t.Run(fmt.Sprintf("settle=%t", settle), func(t *testing.T) {
			for _, polls := range []int{0, 1, 2} {
				p := New()
				p.ActivationPolls = polls
				id, err := p.RequestAccount(ctx, core.AccountRequest{Name: "fresh", Email: "fresh@example.com"})
				if err != nil {
					t.Fatal(err)
				}
				if settle {
					p.Settle()
				}
				var states []string
				for range 3 {
					r, err := p.CreateStatus(ctx, id)
					if err != nil {
						t.Fatal(err)
					}
					a, err := p.GetAccount(ctx, r.ProviderID)
					if err != nil {
						t.Fatal(err)
					}
					states = append(states, a.Status)
				}
				want := []string{"ACTIVE", "ACTIVE", "ACTIVE"}
				for i := range polls {
					want[i] = "PENDING_ACTIVATION"
				}
				if !slices.Equal(states, want) {
					t.Fatalf("ActivationPolls = %d: states = %v, want %v", polls, states, want)
				}
			}
		})
	}
}

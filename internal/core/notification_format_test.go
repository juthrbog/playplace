package core_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"playplace/internal/core"
)

func TestReadyNotificationFormatting(t *testing.T) {
	for _, access := range []bool{false, true} {
		t.Run(fmt.Sprintf("access=%t", access), func(t *testing.T) {
			h := newHarness(t)
			h.prov.AccessOn = access
			h.prov.Users["alice"] = core.User{ID: "user-one", UserName: "alice"}
			a := h.active(t, "ready-output", "alice", 48*time.Hour)
			want := fmt.Sprintf("Account ready-output (%s) is ready for alice. It expires 2026-09-17.", a.ProviderID)
			if access {
				want += " Sign in through the access portal; the account is listed there."
			}
			if len(h.note.msgs) != 1 || h.note.msgs[0].Body != want || h.note.msgs[0].Subject != "Playground account ready" {
				t.Fatalf("notification=%+v; want body %q", h.note.msgs, want)
			}
		})
	}
}

func TestRequestNotificationFormatting(t *testing.T) {
	for _, override := range []bool{false, true} {
		for _, purpose := range []string{"", "  try S3  "} {
			for _, baseURL := range []string{"", "https://playplace.example///"} {
				t.Run(fmt.Sprintf("override=%t/purpose=%t/url=%t", override, purpose != "", baseURL != ""), func(t *testing.T) {
					h := newHarness(t)
					cfg := h.svc.Config()
					cfg.BaseURL = baseURL
					h.svc = core.NewService(h.prov, h.note, cfg, nil)
					h.svc.Now = func() time.Time { return h.now }
					_, err := h.svc.SubmitRequest(context.Background(), core.RequestInput{
						Name: "builder-output", Owner: "alice", RequestedBy: "bob", TTL: 48 * time.Hour,
						BudgetUSD: 50.25, Purpose: purpose, OverrideLimits: override,
					}, "cli")
					if err != nil {
						t.Fatal(err)
					}
					want := "bob requested playground account builder-output for alice: $50.25/month, 2 days."
					if override {
						want += " Limits overridden by an admin."
					}
					if purpose != "" {
						want += " Purpose: try S3."
					}
					if baseURL != "" {
						want += " Approve or deny at https://playplace.example/#pending"
					}
					if len(h.note.msgs) != 1 || h.note.msgs[0].Body != want || h.note.msgs[0].Subject != "Playground account requested: builder-output" {
						t.Fatalf("notification=%+v; want body %q", h.note.msgs, want)
					}
				})
			}
		}
	}
}

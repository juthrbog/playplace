package core_test

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
)

func TestOwnedGuardrailDamagePausesPreparationUntilOperatorRepair(t *testing.T) {
	for _, tc := range []struct{ tag, value string }{
		{core.TagExpires, ""}, {core.TagExpires, "broken"},
		{core.TagBudget, ""}, {core.TagBudget, "broken"},
		{core.TagBudget, "NaN"}, {core.TagBudget, "+Inf"},
		{core.TagBudget, "0"}, {core.TagBudget, "1.001"},
		{core.TagCloseRequested, "broken"},
	} {
		for _, handoff := range []string{"", "pending", "placing", "complete"} {
			t.Run(tc.tag+"/"+tc.value+"/"+handoff, func(t *testing.T) {
				h := newHarness(t)
				ctx := context.Background()
				a := h.active(t, "damaged-owned", "owner", 14*24*time.Hour)
				r, err := h.prov.GetAccount(ctx, a.ID)
				if err != nil {
					t.Fatal(err)
				}
				original := r.Tags[tc.tag]
				delete(r.Tags, core.TagHandoff)
				if handoff != "" {
					r.Tags[core.TagHandoff] = handoff
				}
				delete(r.Tags, tc.tag)
				if tc.value != "" {
					r.Tags[tc.tag] = tc.value
				}
				h.prov.Seed(r)
				h.prov.AccessOn = true
				h.prov.AddUser("user1", "owner", "owner@example.com")
				h.note.msgs = nil
				calls := h.prov.BudgetCalls
				audit := &recorderAudit{}
				for range 2 {
					h.svc = readinessService(h, h.prov)
					h.svc.SetAuditor(audit)
					sum, err := h.svc.Refresh(ctx)
					if err == nil || !strings.Contains(err.Error(), tc.tag) || !sum.Empty() {
						t.Fatalf("expected field diagnostic without adoption or preparation: %+v %v", sum, err)
					}
					got, err := h.svc.Get(ctx, a.ID)
					if err != nil || !got.Managed || got.Status != core.StatusActive || got.TagErrors[tc.tag] == "" {
						t.Fatalf("lost ownership or diagnostic: %+v %v", got, err)
					}
					requests, err := h.prov.ListCreateRequests(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := h.svc.PollCreate(ctx, requests[0].RequestID); err == nil {
						t.Fatal("poll bypassed repair requirement")
					}
					if _, err := h.svc.SetBudget(ctx, a.ID, 75, "admin", "not a repair", true); err == nil {
						t.Fatal("budget change bypassed repair requirement")
					}
					if tc.tag != core.TagBudget {
						if _, err := h.svc.Extend(ctx, a.ID, h.now.Add(21*24*time.Hour), "admin", true); err == nil {
							t.Fatal("extension invented a baseline or ignored ambiguous closure")
						}
					}
					if !maps.Equal(h.prov.Tags(a.ID), r.Tags) || h.prov.BudgetCalls != calls || len(h.prov.Grants) != 0 || len(h.prov.Closed) != 0 || len(h.note.msgs) != 0 || len(audit.kinds()) != 0 {
						t.Fatal("damaged facts authorized mutation, notification or audit event")
					}
				}
				// Model explicit operator restoration through the existing provider
				// interface, then restart. Neither normal command is a repair path.
				if original == "" {
					err = h.prov.RemoveTags(ctx, a.ID, []string{tc.tag})
				} else {
					err = h.prov.SetTags(ctx, a.ID, map[string]string{tc.tag: original})
				}
				if err != nil {
					t.Fatal(err)
				}
				h.svc = readinessService(h, h.prov)
				if sum, err := h.svc.Refresh(ctx); err != nil || len(sum.Adopted) != 0 {
					t.Fatalf("repair: %+v %v", sum, err)
				}
				wantNotices := 0
				if handoff == "pending" || handoff == "placing" {
					wantNotices = 1
				}
				if readyNotices(h) != wantNotices || len(h.prov.Grants[a.ID]) != 1 {
					t.Fatalf("repair lost readiness or repeated legacy handoff: notices=%d grants=%v", readyNotices(h), h.prov.Grants)
				}
			})
		}
	}
}

func TestOwnedClosedDamageDoesNotMaskRetirement(t *testing.T) {
	for _, expiry := range []string{"", "broken"} {
		for _, confirmed := range []bool{false, true} {
			t.Run(expiry+"/"+map[bool]string{false: "external", true: "confirmed"}[confirmed], func(t *testing.T) {
				h := newHarness(t)
				enforceBudgets(h)
				ctx := context.Background()
				a := h.active(t, "damaged-closed", "owner", 14*24*time.Hour)
				r, _ := h.prov.GetAccount(ctx, a.ID)
				r.Status = "CLOSED"
				delete(r.Tags, core.TagExpires)
				if expiry != "" {
					r.Tags[core.TagExpires] = expiry
				}
				r.Tags[core.TagBudget] = "broken"
				r.Tags[core.TagBudgetRecovery] = "broken"
				r.Tags[core.TagCloseRequested] = "broken"
				if confirmed {
					r.Tags[core.TagClosedAt] = h.now.Format(time.RFC3339)
				}
				h.prov.Seed(r)
				if _, err := h.svc.Refresh(ctx); err == nil {
					t.Fatal("accepted deletion must await observation")
				}
				enforceBudgets(h)
				if _, err := h.svc.Refresh(ctx); err != nil {
					t.Fatal(err)
				}
				tags := h.prov.Tags(a.ID)
				if h.prov.RetireCalls != 2 || tags[core.TagBudgetHealth] != "retired" || h.prov.Budgets[a.ID] != a.BudgetUSD || tags[core.TagExpires] != expiry || tags[core.TagBudget] != "broken" || tags[core.TagCloseRequested] != "broken" {
					t.Fatalf("retirement blocked, or irrelevant facts rewritten: %v", tags)
				}
			})
		}
	}
}

func TestGuardrailDamageDoesNotBlockIndependentClosure(t *testing.T) {
	for _, mode := range []string{"expired", "intent", "explicit", "provider-pending"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			a := h.active(t, "damaged-close", "owner", 14*24*time.Hour)
			r, _ := h.prov.GetAccount(ctx, a.ID)
			r.Tags[core.TagBudget] = "broken"
			r.Tags[core.TagExpires] = "broken"
			r.Tags[core.TagCloseRequested] = "broken"
			switch mode {
			case "expired":
				r.Tags[core.TagExpires] = h.now.Add(-time.Hour).Format(time.RFC3339)
			case "intent":
				r.Tags[core.TagCloseRequested] = h.now.Add(-time.Hour).Format(time.RFC3339)
			case "provider-pending":
				r.Status = "PENDING_CLOSURE"
			}
			h.prov.Seed(r)
			// One damaged account must also not block a different expired account.
			other := h.active(t, "other-expired", "owner", time.Hour)
			h.advance(2 * time.Hour)
			if mode == "explicit" {
				if _, err := h.svc.RequestClose(ctx, a.ID, "admin"); err != nil {
					t.Fatal(err)
				}
			}
			_, _ = h.svc.Refresh(ctx) // active damage remains visible alongside closure
			got, _ := h.svc.Get(ctx, a.ID)
			want := core.StatusClosed
			if mode == "provider-pending" {
				want = core.StatusClosing
			}
			if got.Status != want {
				t.Fatalf("independent closure blocked: %+v", got)
			}
			gotOther, _ := h.svc.Get(ctx, other.ID)
			if gotOther.Status != core.StatusClosed {
				t.Fatal("other account closure blocked")
			}
			if h.prov.Tags(a.ID)[core.TagBudget] != "broken" {
				t.Fatal("closure invented a budget")
			}
		})
	}
}

func TestDamagedFactsJSONAndTagRendering(t *testing.T) {
	a := core.FromRemote(core.RemoteAccount{ProviderID: "123", Name: "damaged", Status: "ACTIVE", Tags: map[string]string{
		core.TagManaged: "true", core.TagExpires: "broken", core.TagBudget: "NaN", core.TagCloseRequested: "broken",
	}}, core.DefaultConfig(), t0)
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["managed"] != true || !strings.Contains(string(data), `"expires_at":null`) || !strings.Contains(string(data), `"budget_usd":null`) || got["tag_errors"] == nil || got["status"] != "active" {
		t.Fatalf("JSON invented facts or lost ownership/diagnostics: %s", data)
	}
	for _, tag := range []string{core.TagExpires, core.TagBudget, core.TagCloseRequested} {
		if _, ok := a.Tags()[tag]; ok {
			t.Fatalf("Tags rendered damaged %s", tag)
		}
	}
}

func TestGuardrailDamagePreservesExistingAccessAndProtection(t *testing.T) {
	h := newHarness(t)
	enforceBudgets(h)
	h.prov.AccessOn = true
	h.prov.AddUser("user1", "owner", "owner@example.com")
	ctx := context.Background()
	a := h.active(t, "keep-protection", "owner", 14*24*time.Hour)
	before := h.prov.BudgetStates[a.ID]
	calls := h.prov.BudgetCalls
	if err := h.prov.SetTags(ctx, a.ID, map[string]string{core.TagExpires: "broken"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Refresh(ctx); err == nil {
		t.Fatal("missing repair diagnostic")
	}
	if h.prov.BudgetCalls != calls || h.prov.BudgetStates[a.ID] != before || len(h.prov.Grants[a.ID]) != 1 || h.prov.Tags(a.ID)[core.TagAccess] != "user1" {
		t.Fatal("damage altered existing access or protection")
	}
}

func TestDamagedCreationCannotBePlaced(t *testing.T) {
	for _, tag := range []string{core.TagExpires, core.TagBudget, core.TagCloseRequested} {
		t.Run(tag, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			req, err := h.svc.Request(ctx, core.RequestInput{Name: "damaged-creation", Owner: "owner"})
			if err != nil {
				t.Fatal(err)
			}
			h.prov.Settle()
			creation, err := h.prov.CreateStatus(ctx, req.RequestID)
			if err != nil {
				t.Fatal(err)
			}
			if err := h.prov.SetTags(ctx, creation.ProviderID, map[string]string{tag: "broken"}); err != nil {
				t.Fatal(err)
			}
			before := h.prov.Tags(creation.ProviderID)
			if _, err := h.svc.PollCreate(ctx, req.RequestID); err == nil {
				t.Fatal("poll placed damaged account")
			}
			if sum, err := h.svc.Refresh(ctx); err == nil || !sum.Empty() {
				t.Fatalf("refresh placed damaged account: %+v %v", sum, err)
			}
			accounts, err := h.prov.ListPlaygroundAccounts(ctx)
			if err != nil || len(accounts) != 0 || !maps.Equal(before, h.prov.Tags(creation.ProviderID)) || h.prov.BudgetCalls != 0 || readyNotices(h) != 0 {
				t.Fatalf("damaged creation caused side effects: %+v %v", accounts, err)
			}
		})
	}
}

func TestUnrelatedBudgetDamageDoesNotBlockExtension(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.active(t, "extend-known", "owner", 14*24*time.Hour)
	if err := h.prov.SetTags(ctx, a.ID, map[string]string{core.TagBudget: "broken"}); err != nil {
		t.Fatal(err)
	}
	until := h.now.Add(21 * 24 * time.Hour)
	got, err := h.svc.Extend(ctx, a.ID, until, "owner", false)
	if err != nil || !got.ExpiresAt.Equal(until) || h.prov.Tags(a.ID)[core.TagBudget] != "broken" {
		t.Fatalf("valid extension: %+v %v", got, err)
	}
}

func TestValidAccountJSONKeepsStoredFacts(t *testing.T) {
	h := newHarness(t)
	a := h.active(t, "json-valid", "owner", 14*24*time.Hour)
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["managed"] != true || got["expires_at"] != a.ExpiresAt.Format(time.RFC3339) || got["budget_usd"] != a.BudgetUSD || got["tag_errors"] != nil {
		t.Fatalf("valid representation changed: %s", data)
	}
}

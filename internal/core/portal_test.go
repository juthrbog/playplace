package core_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
)

func TestCheckAccessPortalURL(t *testing.T) {
	for _, ok := range []string{
		"",
		"https://example.awsapps.com/start",
		"https://example.awsapps.com/start/",
		"https://d-1234567890.awsapps.com/start#/",
		"HTTPS://example.awsapps.com/start",
		"https://portal.example:8443/start?x=1",
	} {
		if err := core.CheckAccessPortalURL(ok); err != nil {
			t.Errorf("CheckAccessPortalURL(%q) = %v, want nil", ok, err)
		}
	}
	for bad, want := range map[string]string{
		"http://example.awsapps.com/start":        "must start with https://",
		"javascript:alert(1)":                     "must start with https://",
		"data:text/html,hi":                       "must start with https://",
		"ftp://example.com/start":                 "must start with https://",
		"https://user:secret@example.com/start":   "user name or",
		"https://user@example.com/start":          "user name or",
		"https:///start":                          "must name a host",
		"https://:443/start":                      "must name a host",
		"https:example.com/start":                 "must name a host",
		"/start":                                  "full URL",
		"example.awsapps.com/start":               "full URL",
		"//example.awsapps.com/start":             "full URL",
		" https://example.awsapps.com/start":      "spaces",
		"https://example.com/\nstart":             "spaces",
		"https://exa mple.com/start":              "spaces",
		"https://example.awsapps.com/st art":      "spaces",
		"https://example.awsapps.com/st\u00a0art": "spaces",
		"https://[::1/start":                      "not a valid URL",
		"https://example.com/start\x7f":           "not a valid URL",
		"https://example.awsapps.com/start\t":     "spaces",
		"https://example.awsapps.com%2fevil/":     "not a valid URL",
		"https://example.awsapps.com/start\x00x":  "not a valid URL",
	} {
		err := core.CheckAccessPortalURL(bad)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("CheckAccessPortalURL(%q) = %v, want an error with %q", bad, err, want)
		}
	}
}

// With a portal address, the ready notice names it, so an owner can follow
// it from Slack or the log.
func TestReadyNotificationNamesThePortal(t *testing.T) {
	h := newHarness(t)
	cfg := h.svc.Config()
	cfg.AccessPortalURL = "https://example.awsapps.com/start"
	h.svc = core.NewService(h.prov, h.note, cfg, nil)
	h.svc.Now = func() time.Time { return h.now }
	h.prov.AccessOn = true
	h.prov.Users["alice"] = core.User{ID: "user-one", UserName: "alice"}
	a := h.active(t, "ready-portal", "alice", 48*time.Hour)
	want := fmt.Sprintf("Account ready-portal (%s) is ready for alice. It expires 2026-09-17. Sign in at https://example.awsapps.com/start; the account is listed there.", a.ProviderID)
	if len(h.note.msgs) != 1 || h.note.msgs[0].Body != want {
		t.Fatalf("notification=%+v; want body %q", h.note.msgs, want)
	}
}

// Without --permission-set, the ready notice still names the portal, as the
// web UI still shows the owner the portal button.
func TestReadyNotificationNamesThePortalWhenAccessIsNotManaged(t *testing.T) {
	h := newHarness(t)
	cfg := h.svc.Config()
	cfg.AccessPortalURL = "https://example.awsapps.com/start"
	h.svc = core.NewService(h.prov, h.note, cfg, nil)
	h.svc.Now = func() time.Time { return h.now }
	a := h.active(t, "ready-unmanaged", "bob@example.com", 48*time.Hour)
	want := fmt.Sprintf("Account ready-unmanaged (%s) is ready for bob@example.com. It expires 2026-09-17. Sign in at https://example.awsapps.com/start; the account is listed there.", a.ProviderID)
	if len(h.note.msgs) != 1 || h.note.msgs[0].Body != want {
		t.Fatalf("notification=%+v; want body %q", h.note.msgs, want)
	}
}

// Without a portal address and without --permission-set, the notice says
// nothing about signing in.
func TestReadyNotificationWithoutPortalOrAccess(t *testing.T) {
	h := newHarness(t)
	a := h.active(t, "ready-plain", "bob@example.com", 48*time.Hour)
	want := fmt.Sprintf("Account ready-plain (%s) is ready for bob@example.com. It expires 2026-09-17.", a.ProviderID)
	if len(h.note.msgs) != 1 || h.note.msgs[0].Body != want {
		t.Fatalf("notification=%+v; want body %q", h.note.msgs, want)
	}
}

// A legacy account, which has no handoff tag, with an unknown owner waits
// for a known owner, not for the grant: the grant cannot find the owner.
func TestLegacyAccountWithUnknownOwnerWaitsForOwner(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	r := core.RemoteAccount{ProviderID: "333333333333", Name: "adopted", Email: "x@example.com", Status: "ACTIVE", JoinedAt: now, Tags: map[string]string{
		core.TagManaged: "true",
		core.TagExpires: now.Add(72 * time.Hour).Format(time.RFC3339),
	}}
	a := core.FromRemote(r, core.DefaultConfig(), now)
	if !core.OwnerUnknown(a.Owner) {
		t.Fatalf("owner = %q, want an unknown owner", a.Owner)
	}
	if got := a.HandoffBlocker(true, now); got != core.HandoffOwnerUnknown {
		t.Fatalf("HandoffBlocker = %q, want %q", got, core.HandoffOwnerUnknown)
	}
	r.Tags[core.TagOwner] = "alice@example.com"
	a = core.FromRemote(r, core.DefaultConfig(), now)
	if got := a.HandoffBlocker(true, now); got != core.HandoffAccessPending {
		t.Fatalf("with an owner, HandoffBlocker = %q, want %q", got, core.HandoffAccessPending)
	}
}

// Removing a grant is recorded, so the history does not show a grant that
// never ends. The event has the same details as the grant event.
func TestAccessRevokedIsWrittenToHistory(t *testing.T) {
	for _, how := range []string{"close", "expiry"} {
		t.Run(how, func(t *testing.T) {
			h := newHarness(t)
			h.prov.AccessOn = true
			h.prov.AddUser("user-one", "alice", "alice@example.com")
			rec := &recorderAudit{}
			h.svc.SetAuditor(rec)
			ctx := context.Background()
			a := h.active(t, "revoke-"+how, "alice@example.com", 48*time.Hour)
			if a.AccessGrantedTo != "user-one" {
				t.Fatalf("not granted: %+v", a)
			}
			switch how {
			case "close":
				if _, err := h.svc.RequestClose(ctx, a.ID, "alice@example.com"); err != nil {
					t.Fatal(err)
				}
			case "expiry":
				h.advance(72 * time.Hour)
				if _, err := h.svc.Refresh(ctx); err != nil {
					t.Fatal(err)
				}
			}
			var granted, revoked []core.AuditEvent
			for _, e := range rec.events {
				switch e.Event {
				case "access-granted":
					granted = append(granted, e)
				case "access-revoked":
					revoked = append(revoked, e)
				}
			}
			if len(granted) != 1 || len(revoked) != 1 {
				t.Fatalf("events = %v", rec.kinds())
			}
			e := revoked[0]
			if e.Account != a.Name || e.AccountID != a.ProviderID || e.Actor != core.ActorSystem || e.Owner != "alice@example.com" {
				t.Fatalf("revoked event = %+v", e)
			}
			if len(e.Details) != len(granted[0].Details) || e.Details["principal"] != "user-one" || granted[0].Details["principal"] != "user-one" {
				t.Fatalf("details: revoked %v, granted %v", e.Details, granted[0].Details)
			}
			if want := "alice@example.com no longer has console access to " + a.Name; e.Message != want {
				t.Fatalf("message = %q, want %q", e.Message, want)
			}
			if got, err := h.svc.Get(ctx, a.ID); err == nil && got.AccessGrantedTo != "" {
				t.Fatalf("grant still recorded: %+v", got)
			}
		})
	}
}

// Without a grant, nothing is revoked and nothing is recorded.
func TestNoAccessRevokedWithoutAGrant(t *testing.T) {
	h := newHarness(t)
	rec := &recorderAudit{}
	h.svc.SetAuditor(rec)
	a := h.active(t, "no-grant", "alice@example.com", 48*time.Hour)
	if _, err := h.svc.RequestClose(context.Background(), a.ID, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	for _, e := range rec.events {
		if e.Event == "access-revoked" {
			t.Fatalf("revoked without a grant: %v", rec.kinds())
		}
	}
}

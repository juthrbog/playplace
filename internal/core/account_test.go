package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

func TestFromRemoteDerivesStatusFromTags(t *testing.T) {
	cfg := DefaultConfig()
	base := func(tags map[string]string, status string) RemoteAccount {
		return RemoteAccount{ProviderID: "123456789012", Name: "dev", Email: "d@example.com", Status: status, JoinedAt: t0.Add(-time.Hour), Tags: tags}
	}
	exp := t0.Add(48 * time.Hour).Format(time.RFC3339)
	cases := []struct {
		name    string
		r       RemoteAccount
		status  Status
		managed bool
		owner   string
		budget  float64
	}{
		{"active", base(map[string]string{TagManaged: "true", TagOwner: "alex", TagExpires: exp, TagBudget: "25"}, "ACTIVE"), StatusActive, true, "alex", 25},
		{"expiring", base(map[string]string{TagManaged: "true", TagOwner: "alex", TagExpires: exp, TagWarnedAt: t0.Format(time.RFC3339)}, "ACTIVE"), StatusExpiring, true, "alex", 0},
		{"closing", base(map[string]string{TagManaged: "true", TagOwner: "alex", TagExpires: exp, TagCloseRequested: t0.Format(time.RFC3339)}, "ACTIVE"), StatusClosing, true, "alex", 0},
		{"closed", base(map[string]string{TagManaged: "true", TagOwner: "alex", TagExpires: exp}, "CLOSED"), StatusClosed, true, "alex", 0},
		{"pending closure", base(map[string]string{TagManaged: "true", TagOwner: "alex", TagExpires: exp}, "PENDING_CLOSURE"), StatusClosing, true, "alex", 0},
		{"suspended", base(map[string]string{TagManaged: "true", TagOwner: "alex", TagExpires: exp}, "SUSPENDED"), StatusUnavailable, true, "alex", 0},
		{"pending activation", base(map[string]string{TagManaged: "true", TagOwner: "alex", TagExpires: exp}, "PENDING_ACTIVATION"), StatusUnavailable, true, "alex", 0},
		{"unknown state", base(map[string]string{TagManaged: "true", TagOwner: "alex", TagExpires: exp}, ""), StatusUnavailable, true, "alex", 0},
		{"untagged", base(map[string]string{}, "ACTIVE"), StatusActive, false, "unknown", 50},
		{"owner only", base(map[string]string{TagOwner: "sam"}, "ACTIVE"), StatusActive, false, "sam", 50},
	}
	for _, c := range cases {
		a := FromRemote(c.r, cfg, t0)
		if a.Status != c.status || a.Managed != c.managed || a.Owner != c.owner || a.BudgetUSD != c.budget {
			t.Errorf("%s: status=%s managed=%v owner=%s budget=%v", c.name, a.Status, a.Managed, a.Owner, a.BudgetUSD)
		}
		if !c.managed && !a.ExpiresAt.Equal(t0.Add(cfg.DefaultTTL)) {
			t.Errorf("%s: untagged expiry should default, got %s", c.name, a.ExpiresAt)
		}
	}
}

func TestTagsRoundTrip(t *testing.T) {
	warned := t0.Add(-time.Hour)
	a := &Account{Owner: "alex", ExpiresAt: t0.Add(72 * time.Hour), BudgetUSD: 12.5, WarnedAt: &warned, ClosedAt: &warned, CloseAlertedAt: &warned}
	tags := a.Tags()
	back := FromRemote(RemoteAccount{ProviderID: "1", Status: "ACTIVE", Tags: tags}, DefaultConfig(), t0)
	if back.Owner != "alex" || !back.ExpiresAt.Equal(a.ExpiresAt) || back.BudgetUSD != 12.5 || back.WarnedAt == nil || back.Status != StatusExpiring {
		t.Fatalf("round trip lost data: %+v", back)
	}
	if back.ClosedAt == nil || back.CloseAlertedAt == nil || !back.ClosedAt.Equal(warned) || !back.CloseAlertedAt.Equal(warned) {
		t.Fatalf("lost closure monitoring tags: %+v", back)
	}
	if _, ok := tags[TagCloseRequested]; ok {
		t.Fatal("no close-requested tag expected")
	}
}

func TestFromCreateRequest(t *testing.T) {
	a := FromCreateRequest(CreateRequest{RequestID: "car-1", Name: "dev", State: CreateInProgress, RequestedAt: t0})
	if a.Status != StatusCreating || a.ID != "car-1" || a.Owner != "?" {
		t.Fatalf("creating = %+v", a)
	}
	f := FromCreateRequest(CreateRequest{RequestID: "car-2", Name: "dev", State: CreateFailed, FailureReason: "EMAIL_ALREADY_EXISTS"})
	if f.Status != StatusFailed || f.LastError != "EMAIL_ALREADY_EXISTS" {
		t.Fatalf("failed = %+v", f)
	}
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"abc", "dev-alex", "team1-sandbox"} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "ab", "Abc", "-abc", "abc-", "a b", "a_b"} {
		if err := ValidateName(bad); err == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

// The expiry before the last extension rides in its own tag. It is only for
// display: a missing or bad value reads as never extended and is not a tag
// that needs repair.
func TestExtendedFromTag(t *testing.T) {
	from := t0.Add(24 * time.Hour)
	a := &Account{Owner: "alex", ExpiresAt: t0.Add(72 * time.Hour), BudgetUSD: 10, ExtendedFrom: from}
	tags := a.Tags()
	if tags[TagExtendedFrom] != "2026-09-16T12:00:00Z" {
		t.Fatalf("extended-from tag = %q", tags[TagExtendedFrom])
	}
	back := FromRemote(RemoteAccount{ProviderID: "1", Status: "ACTIVE", Tags: tags}, DefaultConfig(), t0)
	if !back.ExtendedFrom.Equal(from) {
		t.Fatalf("round trip extended-from = %s, want %s", back.ExtendedFrom, from)
	}
	if _, ok := (&Account{Owner: "alex", ExpiresAt: t0}).Tags()[TagExtendedFrom]; ok {
		t.Fatal("an account never extended must not write the tag")
	}
	for _, raw := range []string{"", "not a time", "2026-13-40"} {
		tags := map[string]string{TagManaged: "true", TagOwner: "alex", TagExpires: t0.Format(time.RFC3339), TagBudget: "10"}
		if raw != "" {
			tags[TagExtendedFrom] = raw
		}
		a := FromRemote(RemoteAccount{ProviderID: "1", Status: "ACTIVE", Tags: tags}, DefaultConfig(), t0)
		if !a.ExtendedFrom.IsZero() || len(a.TagErrors) != 0 || a.Status != StatusActive || !a.CanExtend() {
			t.Errorf("extended-from %q: got %s, tag errors %v, status %s", raw, a.ExtendedFrom, a.TagErrors, a.Status)
		}
	}
}

// The JSON of an account never extended is unchanged.
func TestExtendedFromJSON(t *testing.T) {
	plain, err := json.Marshal(Account{Owner: "alex", ExpiresAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "extended_from") {
		t.Fatalf("never extended account writes extended_from: %s", plain)
	}
	extended, err := json.Marshal(Account{Owner: "alex", ExpiresAt: t0, ExtendedFrom: t0.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(extended), `"extended_from":"2026-09-15T11:00:00Z"`) {
		t.Fatalf("extended account JSON = %s", extended)
	}
}

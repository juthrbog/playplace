package core

import (
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
		{"expiring", base(map[string]string{TagManaged: "true", TagOwner: "alex", TagExpires: exp, TagWarnedAt: t0.Format(time.RFC3339)}, "ACTIVE"), StatusExpiring, true, "alex", 50},
		{"closing", base(map[string]string{TagManaged: "true", TagOwner: "alex", TagExpires: exp, TagCloseRequested: t0.Format(time.RFC3339)}, "ACTIVE"), StatusClosing, true, "alex", 50},
		{"closed", base(map[string]string{TagManaged: "true", TagOwner: "alex", TagExpires: exp}, "SUSPENDED"), StatusClosed, true, "alex", 50},
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
	a := &Account{Owner: "alex", ExpiresAt: t0.Add(72 * time.Hour), BudgetUSD: 12.5, WarnedAt: &warned}
	tags := a.Tags()
	back := FromRemote(RemoteAccount{ProviderID: "1", Status: "ACTIVE", Tags: tags}, DefaultConfig(), t0)
	if back.Owner != "alex" || !back.ExpiresAt.Equal(a.ExpiresAt) || back.BudgetUSD != 12.5 || back.WarnedAt == nil || back.Status != StatusExpiring {
		t.Fatalf("round trip lost data: %+v", back)
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

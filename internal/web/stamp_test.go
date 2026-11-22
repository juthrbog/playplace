package web

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
)

func TestStampDate(t *testing.T) {
	for _, tc := range []struct {
		at    time.Time
		date  string
		short string
	}{
		{time.Date(2026, 11, 29, 17, 0, 0, 0, time.UTC), "NOV 29 2026", "NOV 29"},
		{time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC), "MAR 5 2026", "MAR 5"},
		// The stamp is in UTC: late evening in New York is the next day.
		{time.Date(2026, 12, 31, 21, 0, 0, 0, time.FixedZone("EST", -5*60*60)), "JAN 1 2027", "JAN 1"},
	} {
		if got := stampDate(tc.at); got != tc.date {
			t.Errorf("stampDate(%s) = %q, want %q", tc.at, got, tc.date)
		}
		if got := stampShort(tc.at); got != tc.short {
			t.Errorf("stampShort(%s) = %q, want %q", tc.at, got, tc.short)
		}
	}
	at := time.Date(2026, 11, 29, 12, 30, 0, 0, time.FixedZone("EDT", -4*60*60))
	if got := stampTitle(at); got != "2026-11-29 16:30 UTC" {
		t.Errorf("stampTitle = %q", got)
	}
}

func TestDueFor(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	damaged := map[string]string{core.TagExpires: "is missing or invalid"}
	for _, tc := range []struct {
		name string
		a    core.Account
		kind dueKind
		note string
		red  bool
	}{
		{"far", core.Account{Status: core.StatusActive, ExpiresAt: now.Add(10 * 24 * time.Hour)}, dueOK, "", false},
		{"just over 3 days", core.Account{Status: core.StatusActive, ExpiresAt: now.Add(stampSoon + time.Minute)}, dueOK, "", false},
		{"exactly 3 days", core.Account{Status: core.StatusExpiring, ExpiresAt: now.Add(stampSoon)}, dueSoon, "", true},
		{"one hour", core.Account{Status: core.StatusExpiring, ExpiresAt: now.Add(time.Hour)}, dueSoon, "", true},
		{"due now", core.Account{Status: core.StatusExpiring, ExpiresAt: now}, duePast, "", true},
		{"past", core.Account{Status: core.StatusClosing, ExpiresAt: now.Add(-time.Hour)}, duePast, "", true},
		{"creating, approved", core.Account{Status: core.StatusCreating, ExpiresAt: now.Add(7 * 24 * time.Hour)}, dueOK, "", false},
		{"creating, no record", core.Account{Status: core.StatusCreating}, dueUnset, "Set when it is ready", false},
		// A pending row counts its expiry from now. The stamp keeps no
		// clock, so every poll of the same queue hashes the same.
		{"pending", core.Account{Status: core.StatusPending, ExpiresAt: now.Add(7 * 24 * time.Hour)}, dueUnset, "Stamped at approval", false},
		{"damaged", core.Account{Status: core.StatusActive, TagErrors: damaged}, dueUnknown, "unknown", false},
		{"no date", core.Account{Status: core.StatusUnavailable}, dueUnknown, "unknown", false},
		{"closed", core.Account{Status: core.StatusClosed, ExpiresAt: now.Add(-time.Hour)}, dueNone, "—", false},
		{"closed, damaged", core.Account{Status: core.StatusClosed, TagErrors: damaged}, dueNone, "—", false},
		{"failed", core.Account{Status: core.StatusFailed}, dueNone, "—", false},
	} {
		d := dueFor(&tc.a, now)
		if d.Kind != tc.kind || d.Note != tc.note || d.red() != tc.red {
			t.Errorf("%s: got kind %s, note %q, red %v; want %s, %q, %v", tc.name, d.Kind, d.Note, d.red(), tc.kind, tc.note, tc.red)
		}
		if d.dated() != (tc.note == "") {
			t.Errorf("%s: dated = %v with note %q", tc.name, d.dated(), d.Note)
		}
		if d.dated() && !d.At.Equal(tc.a.ExpiresAt) {
			t.Errorf("%s: date %s, want %s", tc.name, d.At, tc.a.ExpiresAt)
		}
	}
}

// The renewal line shows only an earlier date that is before the due date.
func TestDueForRenewal(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	exp := now.Add(7 * 24 * time.Hour)
	for _, tc := range []struct {
		name string
		from time.Time
		want time.Time
	}{
		{"extended", exp.Add(-7 * 24 * time.Hour), exp.Add(-7 * 24 * time.Hour)},
		{"never", time.Time{}, time.Time{}},
		{"not earlier", exp, time.Time{}},
		{"later", exp.Add(time.Hour), time.Time{}},
	} {
		a := core.Account{Status: core.StatusActive, ExpiresAt: exp, ExtendedFrom: tc.from}
		if got := dueFor(&a, now).From; !got.Equal(tc.want) {
			t.Errorf("%s: from = %s, want %s", tc.name, got, tc.want)
		}
	}
	pending := core.Account{Status: core.StatusPending, ExtendedFrom: exp}
	if got := dueFor(&pending, now).From; !got.IsZero() {
		t.Errorf("a pending row has a renewal line from %s", got)
	}
}

// The time left counts from the clock it is given, not the wall clock.
func TestExpiresInUsesTheGivenClock(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{time.Time{}, "unknown"},
		{now.Add(-time.Minute), "expired"},
		{now.Add(5 * time.Hour), "in 5h"},
		{now.Add(3*24*time.Hour + time.Hour), "in 3d"},
	} {
		if got := expiresIn(tc.at, now); got != tc.want {
			t.Errorf("expiresIn(%s) = %q, want %q", tc.at, got, tc.want)
		}
	}
}

// The account page counts the time left from the page clock, which is the
// service clock, so a fake clock moves the text.
func TestAccountPageTimeLeftUsesThePageClock(t *testing.T) {
	a := &core.Account{ID: "x", Name: "x", ProviderID: "111111111111", Status: core.StatusActive, CreatedAt: t0, ExpiresAt: t0.Add(48 * time.Hour)}
	var buf bytes.Buffer
	if err := accountPage(page{Now: t0}, a, nil, 0, historyView{}).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `<span class="muted">(in 2d)</span>`) {
		t.Fatalf("account page does not count from the page clock:\n%s", buf.String())
	}
}

package web

import (
	"testing"
	"time"

	"playplace/internal/core"
)

// A ref value holds the name and the version of a request with one space
// between them, and it reads back to the same ref. The version is the rest
// of the value, so a version from a hand-edited journey tag with a space
// also reads back.
func TestRefValueRoundTrip(t *testing.T) {
	q := core.Request{Name: "bobs-box", Owner: "bob@example.com", RequestedBy: "bob@example.com", TTL: 36 * time.Hour, BudgetUSD: 12.5, RequestedAt: t0, Via: "web", JourneyID: "J4IBCDFP4KLII6HH67PPWGDEEY"}
	if v := refValue(q.Ref()); v != "bobs-box "+q.Version() {
		t.Fatalf("refValue = %q, want the name, one space and the version", v)
	}
	for _, ref := range []core.RequestRef{q.Ref(), {Name: "bobs-box", Version: "J4 IB.1752ff596eba805f"}} {
		v := refValue(ref)
		if got, ok := parseRefValue(v); !ok || got != ref {
			t.Errorf("parseRefValue(%q) = %+v, %v, want %+v", v, got, ok, ref)
		}
	}
}

// parseRefValue reads only what refValue writes. An empty value, a name with
// no version, and extra spaces around either part give no ref.
func TestParseRefValueRefusesOtherValues(t *testing.T) {
	version := core.Request{Name: "bobs-box", JourneyID: "J4IBCDFP4KLII6HH67PPWGDEEY"}.Version()
	for _, v := range []string{
		"",
		" ",
		"bobs-box",
		"bobs-box ",
		" " + version,
		"bobs-box  " + version,
		" bobs-box " + version,
		"bobs-box " + version + " ",
		"bobs-box " + version + "\n",
		"bobs-box\t " + version,
		"bobs-box\t" + version,
		"bobs-box\n" + version,
	} {
		if ref, ok := parseRefValue(v); ok {
			t.Errorf("parseRefValue(%q) = %+v, want no ref", v, ref)
		}
	}
}

package tui

import (
	"strings"
	"testing"

	"playplace/internal/core"
)

func TestClosureAndUnavailableStatusLabels(t *testing.T) {
	th := newTheme(true)
	for _, tc := range []struct {
		status       core.Status
		err          string
		glyph, label string
	}{
		{core.StatusClosing, "", "◌", "closing"},
		{core.StatusClosing, "closure unconfirmed", "!", "closing !"},
		{core.StatusUnavailable, "suspended", "!", "unavailable"},
		{core.StatusClosed, "", "○", "closed"},
		{core.StatusCreating, "", "◌", "creating"},
		{core.StatusCreating, "still outside the Playground OU", "◌", "creating !"},
	} {
		glyph, label, _ := th.status(&core.Account{Status: tc.status, LastError: tc.err}, t0)
		if glyph != tc.glyph || label != tc.label {
			t.Errorf("%s/%s: %s %s, want %s %s", tc.status, tc.err, glyph, label, tc.glyph, tc.label)
		}
	}
}

// A creation that waits outside the OU has a note, not a failure. Neither
// the row color nor the detail may call it one.
func TestUnplacedCreationNoteIsNotLabelledFailure(t *testing.T) {
	m := model{th: newTheme(true)}
	note := "It waits outside the Playground OU for the next reconcile."
	waiting := &core.Account{Status: core.StatusCreating, LastError: note}
	if _, _, c := m.th.status(waiting, t0); c != m.th.warning {
		t.Errorf("row color = %v, want the warning color", c)
	}
	got := plain(strings.Join(m.lifecycleLines(waiting, 120), "\n"))
	if !strings.Contains(got, "placing  "+note) || strings.Contains(got, "failure") {
		t.Errorf("creating detail = %q", got)
	}
	got = plain(strings.Join(m.lifecycleLines(&core.Account{Status: core.StatusUnavailable, LastError: "suspended"}, 120), "\n"))
	if !strings.Contains(got, "failure  suspended") {
		t.Errorf("unavailable detail = %q", got)
	}
}

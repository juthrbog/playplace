package tui

import (
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
	} {
		glyph, label, _ := th.status(&core.Account{Status: tc.status, LastError: tc.err}, t0)
		if glyph != tc.glyph || label != tc.label {
			t.Errorf("%s/%s: %s %s, want %s %s", tc.status, tc.err, glyph, label, tc.glyph, tc.label)
		}
	}
}

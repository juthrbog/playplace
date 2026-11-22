package web

import (
	"strings"
	"time"

	"playplace/internal/core"
)

// stampSoon is how close to the due date the stamp turns to red ink. It is
// fixed at 3 days, as DESIGN.md says, and does not follow the configured
// warning window.
const stampSoon = 72 * time.Hour

// dueKind says what the date stamp of a row shows.
type dueKind string

const (
	dueOK      dueKind = "ok"      // a due date more than 3 days away
	dueSoon    dueKind = "soon"    // a due date inside the last 3 days, red ink
	duePast    dueKind = "past"    // a due date that has passed, red ink and "past due"
	dueUnset   dueKind = "unset"   // no due date yet, a dashed box with a note
	dueUnknown dueKind = "unknown" // a damaged expiry tag, a dashed box that says "unknown"
	dueNone    dueKind = "none"    // a closed or failed row, a dash
)

// due is the date stamp of one row.
type due struct {
	Kind dueKind
	// At is the due date. It is zero for the unset, unknown and none kinds.
	// A pending row has no clock at all, so the poll still gets 204.
	At time.Time
	// From is the due date before the last extension, or zero.
	From time.Time
	// Note is the text of a row with no date: "Stamped at approval", "Set
	// when it is ready", "unknown" or "—".
	Note string
}

// dueFor classifies the due date of a against now, which is the service
// clock.
func dueFor(a *core.Account, now time.Time) due {
	switch {
	case a.Status == core.StatusPending:
		return due{Kind: dueUnset, Note: "Stamped at approval"}
	case a.Status == core.StatusClosed || a.Status == core.StatusFailed:
		return due{Kind: dueNone, Note: "—"}
	case a.TagErrors[core.TagExpires] != "":
		return due{Kind: dueUnknown, Note: "unknown"}
	case a.ExpiresAt.IsZero() && a.Status == core.StatusCreating:
		// A creation with no approved record has no due date until the
		// account is ready.
		return due{Kind: dueUnset, Note: "Set when it is ready"}
	case a.ExpiresAt.IsZero():
		return due{Kind: dueUnknown, Note: "unknown"}
	}
	d := due{Kind: dueOK, At: a.ExpiresAt}
	if !a.ExtendedFrom.IsZero() && a.ExtendedFrom.Before(a.ExpiresAt) {
		d.From = a.ExtendedFrom
	}
	switch left := a.ExpiresAt.Sub(now); {
	case left <= 0:
		d.Kind = duePast
	case left <= stampSoon:
		d.Kind = dueSoon
	}
	return d
}

// red reports whether the stamp uses red ink.
func (d due) red() bool { return d.Kind == dueSoon || d.Kind == duePast }

// dated reports whether the stamp shows a date, not a note.
func (d due) dated() bool { return !d.At.IsZero() }

// stampDate writes a due date the way the stamp shows it, such as
// "NOV 29 2026": the month in capitals, the day with no leading zero and the
// year. It is in UTC, like every other time in the web UI.
func stampDate(t time.Time) string {
	return strings.ToUpper(t.UTC().Format("Jan 2 2006"))
}

// stampShort is the earlier date in the renewal line, such as "NOV 20".
func stampShort(t time.Time) string {
	return strings.ToUpper(t.UTC().Format("Jan 2"))
}

// stampTitle is the full time in the title of a stamp, such as
// "2026-11-29 17:00 UTC".
func stampTitle(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

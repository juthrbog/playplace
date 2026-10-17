package web

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"playplace/internal/core"
)

func tableURL(showAll bool) string {
	if showAll {
		return "/accounts?all=1"
	}
	return "/accounts"
}

func expiresIn(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := time.Until(t)
	switch {
	case d < 0:
		return "expired"
	case d < 24*time.Hour:
		return fmt.Sprintf("in %dh", int(d.Hours()))
	default:
		return fmt.Sprintf("in %dd", int(d.Hours()/24))
	}
}

func expiryDate(t time.Time, layout string) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.Format(layout)
}

func budgetAmount(amount float64) string {
	if amount <= 0 {
		return "unknown"
	}
	return fmt.Sprintf("$%.2f", amount)
}

// notCreated is the word for a fact that AWS gives only when it creates the
// account, such as the account id. A failed creation never gets one, so it
// does not say "yet".
func notCreated(a *core.Account) string {
	if a.Status == core.StatusFailed {
		return "not created"
	}
	return "not created yet"
}

// meterWidth is the width in pixels of the inline spend meter.
const meterWidth = 96

// meter is one spend meter. Fill is the bar length in pixels. Level picks
// the color class: ok, high, near or over. Label is the word beside the bar,
// empty below 75 percent.
type meter struct {
	Fill  int
	Level string
	Label string
}

// spendMeter measures spend against the budget on a bar that is width
// pixels long. The CSP allows no inline style, so the bar length is a
// number for an SVG width attribute. Over budget means spend above the
// budget, and the bar is then full. The percent is cut down, so 99.6
// percent reads "99% used" and never claims the whole budget too early.
// Floating point puts 8.70 of 10 at 86.99999999999999 percent, so the cut
// adds a tiny margin first. Without it, that spend reads "86% used".
func spendMeter(spend, budget float64, width int) meter {
	if budget <= 0 {
		return meter{Level: "ok"}
	}
	if spend > budget {
		return meter{Fill: width, Level: "over", Label: "over budget"}
	}
	pct := spend / budget * 100
	whole := math.Floor(pct + 1e-9)
	m := meter{Fill: int(math.Round(math.Min(pct, 100) / 100 * float64(width)))}
	switch {
	case whole >= 95:
		m.Level = "near"
	case whole >= 75:
		m.Level = "high"
	default:
		m.Level = "ok"
		return m
	}
	m.Label = fmt.Sprintf("%d%% used", int(whole))
	return m
}

// statusIcons names the icon of each account status (DESIGN.md, Status
// label).
var statusIcons = map[core.Status]string{
	core.StatusPending:     "slip",
	core.StatusCreating:    "clock-arc",
	core.StatusActive:      "check",
	core.StatusExpiring:    "hourglass",
	core.StatusClosing:     "box",
	core.StatusClosed:      "door",
	core.StatusUnavailable: "ban",
	core.StatusFailed:      "x-circle",
}

// statusIcon is the icon of a status. A status that the map does not know
// gets the dashed circle, so the label never points at a missing symbol.
func statusIcon(s core.Status) string {
	if name, ok := statusIcons[s]; ok {
		return name
	}
	return "dashed-circle"
}

// budgetLook is how a budget protection state shows: its icon, its tone
// (the color class) and whether it is settling.
type budgetLook struct {
	Icon     string
	Tone     string
	Settling bool
}

// budgetIcons maps each budget protection state to its look (DESIGN.md,
// Status label). Normal states are quiet, work in progress is settling, and
// a state that needs a person is a fault.
var budgetIcons = map[string]budgetLook{
	"ready":           {Icon: "check", Tone: "ok"},
	"alerts-only":     {Icon: "bell", Tone: "quiet"},
	"alerts-disabled": {Icon: "bell-off", Tone: "quiet"},
	"retired":         {Icon: "minus", Tone: "quiet"},
	"setup-pending":   {Icon: "clock", Tone: "settling", Settling: true},
	"restricting":     {Icon: "lock", Tone: "settling", Settling: true},
	"recovering":      {Icon: "unlock", Tone: "settling", Settling: true},
	"retiring":        {Icon: "minus", Tone: "settling", Settling: true},
	"restricted":      {Icon: "lock", Tone: "fault"},
	"error":           {Icon: "alert", Tone: "fault"},
}

// budgetState is the look and the word of a budget protection state. No
// state yet reads "not checked". A state that the map does not know keeps
// its own word with the quiet dashed circle.
func budgetState(health string) (budgetLook, string) {
	if health == "" {
		return budgetLook{Icon: "dashed-circle", Tone: "quiet"}, "not checked"
	}
	if look, ok := budgetIcons[health]; ok {
		return look, health
	}
	return budgetLook{Icon: "dashed-circle", Tone: "quiet"}, health
}

// dayCount renders a duration as a whole number of days.
func dayCount(d time.Duration) string {
	return fmt.Sprintf("%d", int(d.Hours()/24))
}

// dayValue is the value that the edit form opens with for a request
// lifetime: the stored whole hours as days, with a fraction when needed,
// such as 1.5 for 36 hours. A number input holds it, and parseDuration reads
// it back exactly.
func dayValue(d time.Duration) string {
	return strconv.FormatFloat(math.Ceil(d.Hours())/24, 'f', -1, 64)
}

// dayLabel names the edit form's days input. A lifetime that is not a whole
// number of days also shows exactly, such as "days (now 36 hours)", next to
// the fraction that the input holds.
func dayLabel(d time.Duration) string {
	if int(math.Ceil(d.Hours()))%24 == 0 {
		return "days"
	}
	return "days (now " + core.Lifetime(d) + ")"
}

// budgetValue is the value that the edit form opens with for a budget, with
// every digit it has, such as 12.5.
func budgetValue(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// tagPattern is the HTML pattern for values AWS accepts in tags. Browsers
// compile it with the v flag, so unicode classes work, and - and / inside
// the class must be escaped or the whole pattern is ignored.
const tagPattern = `[\p{L}\p{N}\p{Z}_.:\/=+\-@]*`

// limitsNote is the sentence under the request form.
func limitsNote(pg page) string {
	note := fmt.Sprintf("Every request needs an approver. Limits: %s days and $%.0f per month", dayCount(pg.Limits.MaxTTL), pg.Limits.MaxBudgetUSD)
	if pg.Viewer.Admin {
		note += " unless you tick override"
	}
	return note + "."
}

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

func barWidth(spend, budget float64) string {
	if budget <= 0 {
		return "width:0"
	}
	pct := spend / budget * 100
	if pct > 100 {
		pct = 100
	}
	return fmt.Sprintf("width:%.0f%%", pct)
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

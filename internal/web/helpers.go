package web

import (
	"fmt"
	"time"
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

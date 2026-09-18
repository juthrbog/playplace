package tui

import (
	"image/color"
	"time"

	"charm.land/lipgloss/v2"

	"playplace/internal/core"
)

// theme holds every color and style. It is rebuilt when the terminal reports
// its background so the same binary reads well on light and dark screens.
type theme struct {
	isDark bool

	primary, fg, muted, faint      color.Color
	success, warning, danger, busy color.Color
	bgSelected                     color.Color

	// the mark: a cloud and the balls in its pit
	cloud                                    color.Color
	ballRed, ballYellow, ballBlue, ballGreen color.Color

	title, fill, fact, right                   lipgloss.Style
	tabActive, tabInactive, tabCount           lipgloss.Style
	colHeader, cell, cellMuted, cursorBar      lipgloss.Style
	paneBar, paneTitle, label, value           lipgloss.Style
	dialogBox, dialogTitle, dialogHint         lipgloss.Style
	button, buttonFocus, buttonDanger          lipgloss.Style
	helpBox, helpKey, helpDesc, flash, errText lipgloss.Style
}

func newTheme(isDark bool) theme {
	ld := lipgloss.LightDark(isDark)
	t := theme{isDark: isDark}

	t.primary = ld(lipgloss.Color("#5B3FE6"), lipgloss.Color("#8A7CFF"))
	t.fg = ld(lipgloss.Color("#1C1B22"), lipgloss.Color("#E6E4F0"))
	t.muted = ld(lipgloss.Color("#6B6B78"), lipgloss.Color("#9793A3"))
	t.faint = ld(lipgloss.Color("#B4B2BD"), lipgloss.Color("#5C5A66"))
	t.success = ld(lipgloss.Color("#0C8A5B"), lipgloss.Color("#3DDC97"))
	t.warning = ld(lipgloss.Color("#9A6B00"), lipgloss.Color("#F5C542"))
	t.danger = ld(lipgloss.Color("#C7254E"), lipgloss.Color("#FF6B8B"))
	t.busy = ld(lipgloss.Color("#0A6FB3"), lipgloss.Color("#5BB8FF"))
	t.bgSelected = ld(lipgloss.Color("#E9E7F1"), lipgloss.Color("#2E2C38"))

	t.cloud = ld(lipgloss.Color("#8FB8E8"), lipgloss.Color("#DCE8F7"))
	t.ballRed = ld(lipgloss.Color("#D62839"), lipgloss.Color("#FF6B7A"))
	t.ballYellow = ld(lipgloss.Color("#D99A00"), lipgloss.Color("#FFC531"))
	t.ballBlue = ld(lipgloss.Color("#1F6FC5"), lipgloss.Color("#5FA8F5"))
	t.ballGreen = ld(lipgloss.Color("#1F9A55"), lipgloss.Color("#3DDC97"))

	t.title = lipgloss.NewStyle().Bold(true).Foreground(t.primary)
	t.fill = lipgloss.NewStyle().Foreground(t.faint)
	t.fact = lipgloss.NewStyle().Foreground(t.muted)
	t.right = lipgloss.NewStyle().Foreground(t.muted)

	t.tabActive = lipgloss.NewStyle().Bold(true).Foreground(t.primary)
	t.tabInactive = lipgloss.NewStyle().Foreground(t.muted)
	t.tabCount = lipgloss.NewStyle().Foreground(t.faint)

	t.colHeader = lipgloss.NewStyle().Foreground(t.muted).Bold(true)
	t.cell = lipgloss.NewStyle().Foreground(t.fg)
	t.cellMuted = lipgloss.NewStyle().Foreground(t.muted)
	t.cursorBar = lipgloss.NewStyle().Foreground(t.primary)

	t.paneBar = lipgloss.NewStyle().Foreground(t.faint)
	t.paneTitle = lipgloss.NewStyle().Bold(true).Foreground(t.primary)
	t.label = lipgloss.NewStyle().Foreground(t.muted)
	t.value = lipgloss.NewStyle().Foreground(t.fg)

	t.dialogBox = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.primary).Padding(1, 2)
	t.dialogTitle = lipgloss.NewStyle().Bold(true).Foreground(t.primary)
	t.dialogHint = lipgloss.NewStyle().Foreground(t.faint)

	onAccent := ld(lipgloss.Color("#FFFFFF"), lipgloss.Color("#141319"))
	t.button = lipgloss.NewStyle().Padding(0, 2).Foreground(t.fg).Background(t.bgSelected)
	t.buttonFocus = lipgloss.NewStyle().Padding(0, 2).Bold(true).Foreground(onAccent).Background(t.primary)
	t.buttonDanger = lipgloss.NewStyle().Padding(0, 2).Bold(true).Foreground(onAccent).Background(t.danger)

	t.helpBox = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.faint).Padding(1, 2)
	t.helpKey = lipgloss.NewStyle().Foreground(t.fg)
	t.helpDesc = lipgloss.NewStyle().Foreground(t.muted)
	t.flash = lipgloss.NewStyle().Foreground(t.success)
	t.errText = lipgloss.NewStyle().Foreground(t.danger)
	return t
}

// status returns the glyph, label, and color for an account's lifecycle
// state. Glyphs carry the meaning on their own so NO_COLOR terminals still
// read correctly.
func (t theme) status(a *core.Account, now time.Time) (glyph, label string, c color.Color) {
	left := a.ExpiresAt.Sub(now)
	switch a.Status {
	case core.StatusActive:
		switch {
		case left <= 24*time.Hour:
			return "●", "active !", t.danger
		case left <= 7*24*time.Hour:
			return "●", "active !", t.warning
		}
		return "●", "active", t.success
	case core.StatusExpiring:
		if left <= 24*time.Hour {
			return "●", "expiring !", t.danger
		}
		return "●", "expiring !", t.warning
	case core.StatusPending:
		return "◇", "pending", t.warning
	case core.StatusClosing:
		if a.LastError != "" {
			return "!", "closing !", t.danger
		}
		return "◌", "closing", t.busy
	case core.StatusCreating:
		return "◌", string(a.Status), t.busy
	case core.StatusUnavailable:
		return "!", "unavailable", t.danger
	case core.StatusFailed:
		return "×", "failed", t.danger
	default:
		return "○", string(a.Status), t.muted
	}
}

// spendColor grades month-to-date spend against budget.
func (t theme) spendColor(pct float64) color.Color {
	switch {
	case pct >= 0.9:
		return t.danger
	case pct >= 0.7:
		return t.warning
	default:
		return t.success
	}
}

package web

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"playplace/internal/core"
)

func TestSpendMeter(t *testing.T) {
	for _, tc := range []struct {
		spend, budget float64
		fill          int
		level, label  string
	}{
		{0, 50, 0, "ok", ""},
		{37, 50, 71, "ok", ""}, // 74%
		{37.5, 50, 72, "high", "75% used"},
		{47.5, 50, 91, "near", "95% used"},
		{50, 50, 96, "near", "100% used"},   // at the budget, not over it
		{99.6, 100, 96, "near", "99% used"}, // cut down, never rounded up
		// Floating point lands these just under the whole percent.
		{8.7, 10, 84, "high", "87% used"},
		{23.4, 30, 75, "high", "78% used"},
		{68.6, 70, 94, "near", "98% used"},
		{7.5, 10, 72, "high", "75% used"},
		{28.5, 30, 91, "near", "95% used"},
		{24, 10, 96, "over", "over budget"},
		{5, 0, 0, "ok", ""}, // no budget, no bar
	} {
		m := spendMeter(tc.spend, tc.budget, meterWidth)
		if m.Fill != tc.fill || m.Level != tc.level || m.Label != tc.label {
			t.Errorf("spendMeter(%v, %v) = %+v, want fill %d, level %q, label %q", tc.spend, tc.budget, m, tc.fill, tc.level, tc.label)
		}
	}
}

// The old bar never turned red: its over class sat in a second class
// attribute, which browsers drop. The SVG meter has one class per element.
func TestOverBudgetMeterIsRed(t *testing.T) {
	var buf bytes.Buffer
	row := accountRow{Account: &core.Account{ID: "x", Name: "x", Status: core.StatusActive, BudgetUSD: 10}, Spend: 24}
	if err := accountTR(row).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{`class="meter-line meter-over"`, `over budget`, `<rect class="meter-fill" width="96"`, `<use href="#i-alert">`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	svg := regexp.MustCompile(`<svg class="meter"[^>]*>`).FindString(out)
	if svg == "" || strings.Count(svg, "class=") != 1 {
		t.Errorf("the meter needs exactly one class attribute: %q", svg)
	}
	if strings.Contains(out, "style=") || strings.Contains(out, `class="bar"`) {
		t.Errorf("the meter must not use inline style or the old bar:\n%s", out)
	}
}

func TestBudgetLabel(t *testing.T) {
	for _, tc := range []struct {
		health, word, icon, tone string
		settling                 bool
	}{
		{"ready", "ready", "check", "ok", false},
		{"alerts-only", "alerts-only", "bell", "quiet", false},
		{"alerts-disabled", "alerts-disabled", "bell-off", "quiet", false},
		{"retired", "retired", "minus", "quiet", false},
		{"", "not checked", "dashed-circle", "quiet", false},
		{"setup-pending", "setup-pending", "clock", "settling", true},
		{"restricting", "restricting", "lock", "settling", true},
		{"recovering", "recovering", "unlock", "settling", true},
		{"retiring", "retiring", "minus", "settling", true},
		{"restricted", "restricted", "lock", "fault", false},
		{"error", "error", "alert", "fault", false},
		{"something-new", "something-new", "dashed-circle", "quiet", false},
	} {
		var buf bytes.Buffer
		if err := budgetLabel(tc.health, "").Render(context.Background(), &buf); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		want := `<span class="status budget-` + tc.tone + `"><svg class="i" aria-hidden="true" focusable="false"><use href="#i-` + tc.icon + `"></use></svg><span>` + tc.word + `</span></span>`
		if !strings.Contains(out, want) {
			t.Errorf("%q: got %s, want %s", tc.health, out, want)
		}
		if strings.Contains(out, "settling protection") != tc.settling {
			t.Errorf("%q: settling qualifier = %v, want %v", tc.health, !tc.settling, tc.settling)
		}
		if strings.Contains(out, "title=") {
			t.Errorf("%q: no error text, so no tooltip: %s", tc.health, out)
		}
	}
}

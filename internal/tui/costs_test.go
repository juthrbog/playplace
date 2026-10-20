package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Spend that is not known shows as unavailable, never as $0. An account with
// no provider ID has no spend yet and keeps its $0.
func TestUnknownSpendShowsAsUnavailable(t *testing.T) {
	svc, prov := seeded(t)
	prov.CostFailures["628790588465"] = "AccessDeniedException: not allowed" // dev-alpha
	m := newModel(context.Background(), svc, Options{})
	m.now = func() time.Time { return t0 }
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = run(t, m, m.load(true))
	m = step(t, m, tea.KeyPressMsg{Code: '/'})
	for _, r := range "dev-alpha" {
		m = step(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	out := plain(m.content())
	if strings.Count(out, "unavailable / $25") != 2 {
		t.Errorf("the row and the pane must say unavailable / $25:\n%s", out)
	}
	if strings.Contains(out, "$0 / $25") || strings.Contains(out, "$0.00") {
		t.Errorf("unknown spend shows as zero:\n%s", out)
	}
	// The header total counts the known spend and says how many are unknown.
	if head := plain(m.headerView()); !strings.Contains(head, "$21.00 MTD (1 unavailable)") {
		t.Errorf("header = %q, want the known total and the unknown count", head)
	}

	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	out = plain(m.content())
	if !strings.Contains(out, "daily spend") || strings.Contains(out, "no cost data yet") || strings.Count(out, "unavailable") < 2 {
		t.Errorf("the detail must say that daily spend is unavailable:\n%s", out)
	}

	// The failed request has no provider ID, so it keeps its zero.
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	m = step(t, m, tea.KeyPressMsg{Code: '/'})
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	for _, r := range m.visible {
		if r.Account.Name == "broken" && r.CostUnknown {
			t.Error("an account with no provider ID must not have unknown spend")
		}
	}
	if !strings.Contains(plain(m.content()), "$0 / unknown") {
		t.Errorf("the failed request lost its zero:\n%s", plain(m.content()))
	}

	// The next good pull makes the spend known again.
	delete(prov.CostFailures, "628790588465")
	m = run(t, m, m.load(true))
	if out := plain(m.content()); strings.Contains(out, "unavailable /") || !strings.Contains(out, "$10.50 / $25") {
		t.Errorf("a good pull must show the spend again:\n%s", out)
	}
}

// The TUI does not pull a closed account, so its row and its pane show no
// spend. No pull failed, so the spend is not unavailable, and it is not $0.
func TestAClosedAccountShowsNoSpend(t *testing.T) {
	m := fixture(t, 120, 30)
	m = step(t, m, tea.KeyPressMsg{Code: '4'})
	if len(m.visible) != 1 || m.visible[0].Account.Name != "old-one" || m.visible[0].CostUnknown {
		t.Fatalf("section 4 = %+v, want old-one with spend that is not unknown", m.visible)
	}
	out := plain(m.content())
	if strings.Count(out, "— / $200") != 2 || strings.Contains(out, "unavailable") || strings.Contains(out, "$0 /") {
		t.Errorf("the row and the pane must say — / $200, never unavailable or $0:\n%s", out)
	}
}

// A narrow spend column has no room for the budget, so it says only
// unavailable.
func TestUnknownSpendFitsANarrowColumn(t *testing.T) {
	m := fixture(t, 60, 20)
	r := m.visible[0]
	r.CostUnknown = true
	cols := columns(m.width)
	cell := plain(m.spendCell(r, cols[len(cols)-1].width, m.th.cell))
	if strings.TrimSpace(cell) != "unavailable" {
		t.Fatalf("narrow cell = %q", cell)
	}
}

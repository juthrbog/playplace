package tui

import (
	"context"
	"errors"
	"image/color"
	"log/slog"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"playplace/internal/audit"
	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

var t0 = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

// seeded builds a service over a fake provider with a small fleet.
func seeded(t *testing.T) (*core.Service, *fake.Provider) {
	t.Helper()
	prov := fake.New()
	prov.Now = func() time.Time { return t0 }
	prov.DailyCost = 0.7
	tags := func(owner string, exp time.Duration, budget string, extra map[string]string) map[string]string {
		m := map[string]string{core.TagManaged: "true", core.TagOwner: owner, core.TagExpires: t0.Add(exp).Format(time.RFC3339), core.TagBudget: budget}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	prov.Seed(core.RemoteAccount{ProviderID: "628790588465", Name: "dev-alpha", Email: "dev-alpha@example.com", JoinedAt: t0.Add(-5 * 24 * time.Hour), Tags: tags("alex", 3*24*time.Hour, "25", nil)})
	prov.Seed(core.RemoteAccount{ProviderID: "794137730641", Name: "legacy-box", Email: "l@example.com", JoinedAt: t0.Add(-4 * 24 * time.Hour), Tags: tags("sam", 20*time.Hour, "50", map[string]string{core.TagWarnedAt: t0.Format(time.RFC3339)})})
	prov.Seed(core.RemoteAccount{ProviderID: "111111111111", Name: "ml-scratch", Email: "m@example.com", JoinedAt: t0.Add(-3 * 24 * time.Hour), Tags: tags("anna", 21*24*time.Hour, "1000", nil)})
	prov.Seed(core.RemoteAccount{ProviderID: "222222222222", Name: "old-one", Email: "o@example.com", Status: "CLOSED", JoinedAt: t0.Add(-2 * 24 * time.Hour), Tags: tags("sam", -48*time.Hour, "200", nil)})
	prov.FailNext = "EMAIL_ALREADY_EXISTS"
	cfg := core.DefaultConfig()
	cfg.InventoryTTL = 0
	svc := core.NewService(prov, nil, cfg, nil)
	svc.Now = func() time.Time { return t0 }
	// An old failed request, so it sorts below the live accounts.
	prov.Now = func() time.Time { return t0.Add(-6 * 24 * time.Hour) }
	if _, err := svc.Request(context.Background(), core.RequestInput{Name: "broken", Owner: "alex"}); err != nil {
		t.Fatal(err)
	}
	prov.Now = func() time.Time { return t0 }
	return svc, prov
}

func fixture(t *testing.T, w, h int) model {
	t.Helper()
	svc, _ := seeded(t)
	m := newModel(context.Background(), svc, Options{Context: "localstack"})
	m.now = func() time.Time { return t0 }
	m = step(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	m = run(t, m, m.load(true))
	return m
}

func step(t *testing.T, m tea.Model, msg tea.Msg) model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(model)
}

// run executes a command synchronously and feeds its message back.
func run(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			m = run(t, m, c)
		}
		return m
	}
	if msg == nil {
		return m
	}
	return step(t, m, msg)
}

func plain(s string) string { return ansi.Strip(s) }

func lines(s string) []string { return strings.Split(s, "\n") }

func TestTallLayoutHasHeaderTabsTablePane(t *testing.T) {
	m := fixture(t, 120, 30)
	out := plain(m.content())
	if got := len(lines(out)); got != 30 {
		t.Fatalf("frame has %d lines, want 30", got)
	}
	for _, l := range lines(out) {
		if w := ansi.StringWidth(l); w > 120 {
			t.Fatalf("line wider than terminal (%d): %q", w, l)
		}
	}
	for _, want := range []string{"playplace", "4 open", "2 expiring ≤7d", "aws: localstack",
		"Open 4", "Active 2", "Expiring 2", "Closed 1",
		"NAME", "OWNER", "ACCOUNT", "SPEND / BUDGET", "dev-alpha", "legacy-box", "ml-scratch", "broken",
		"× failed", "● expiring !", "● active !",
		"── ", "628790588465", "daily", "$0.70",
		"↑/↓ move", "? help"} {
		if !strings.Contains(out, want) {
			t.Errorf("tall frame missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "old-one") {
		t.Error("closed account should not show in Open section")
	}
	if !strings.Contains(out, "$10.50 / $25") {
		t.Errorf("month-to-date spend from the cost cache missing:\n%s", out)
	}
}

func TestShortLayoutHidesPaneAndEnterOpensDetail(t *testing.T) {
	m := fixture(t, 70, 20)
	out := plain(m.content())
	if strings.Contains(out, "── ") {
		t.Fatal("short terminal should not show the detail pane")
	}
	if strings.Contains(out, "ACCOUNT") {
		t.Fatal("narrow layout should drop the ACCOUNT column")
	}
	if got := len(lines(out)); got != 20 {
		t.Fatalf("frame has %d lines, want 20", got)
	}
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	out = plain(m.content())
	if !m.showDetail || !strings.Contains(out, "daily spend") || !strings.Contains(out, "@example.com") {
		t.Fatalf("enter should open full detail:\n%s", out)
	}
	if strings.Contains(out, "↑/↓ move") {
		t.Fatalf("detail ignores up/down, so its hint bar must not offer them:\n%s", out)
	}
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.showDetail {
		t.Fatal("esc should close detail")
	}
}

func TestSectionsAndFilter(t *testing.T) {
	m := fixture(t, 120, 30)
	m = step(t, m, tea.KeyPressMsg{Code: '4'})
	if m.section != secClosed || len(m.visible) != 1 || m.visible[0].Account.Name != "old-one" {
		t.Fatalf("section 4 should show only closed: %+v", m.visible)
	}
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.section != secOpen {
		t.Fatalf("tab should wrap to Open, got %d", m.section)
	}
	m = step(t, m, tea.KeyPressMsg{Code: '3'})
	names := map[string]bool{}
	for _, r := range m.visible {
		names[r.Account.Name] = true
	}
	if len(names) != 2 || !names["dev-alpha"] || !names["legacy-box"] {
		t.Fatalf("expiring section = %v", names)
	}

	m = step(t, m, tea.KeyPressMsg{Code: '1'})
	m = step(t, m, tea.KeyPressMsg{Code: '/'})
	for _, r := range "owner:sam" {
		m = step(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if len(m.visible) != 1 || m.visible[0].Account.Name != "legacy-box" {
		t.Fatalf("owner:sam in Open should match legacy-box only, got %d", len(m.visible))
	}
	if !strings.Contains(plain(m.content()), "1 of 4 shown") {
		t.Fatalf("header should show filter count:\n%s", plain(m.headerView()))
	}
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.filter.Value() != "" || len(m.visible) != 4 {
		t.Fatal("esc should clear the filter")
	}
}

func TestCloseDialogWritesIntentAndCloses(t *testing.T) {
	svc, prov := seeded(t)
	m := newModel(context.Background(), svc, Options{})
	m.now = func() time.Time { return t0 }
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = run(t, m, m.load(true))
	// Select ml-scratch (a plain active account) by filtering.
	m = step(t, m, tea.KeyPressMsg{Code: '/'})
	for _, r := range "ml-" {
		m = step(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	m = step(t, m, tea.KeyPressMsg{Code: 'x'})
	if m.dialog == nil || !m.dialog.destructive || m.dialog.selected != 1 {
		t.Fatalf("x should open a destructive dialog with Cancel selected: %+v", m.dialog)
	}
	out := plain(m.content())
	for _, want := range []string{"Close ml-scratch", "111111111111", "asynchronously"} {
		if !strings.Contains(out, want) {
			t.Errorf("dialog missing %q", want)
		}
	}
	if !strings.Contains(m.dialog.body, "charges may continue") {
		t.Fatal("close dialog must warn about continuing charges")
	}
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // inside grace: ignored
	if m.dialog == nil {
		t.Fatal("enter during grace period should be ignored")
	}
	m.now = func() time.Time { return t0.Add(time.Second) }
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'y'})
	m = run(t, next.(model), cmd)
	if m.dialog != nil || len(prov.Closed) != 1 || prov.Closed[0] != "111111111111" {
		t.Fatalf("y should close at the provider: dialog=%v closed=%v", m.dialog, prov.Closed)
	}
	if !strings.Contains(plain(m.content()), "✓ ml-scratch closed") {
		t.Fatalf("flash missing:\n%s", plain(m.content()))
	}
}

func TestTimerAndActionsDoNotPullCosts(t *testing.T) {
	svc, prov := seeded(t)
	m := newModel(context.Background(), svc, Options{})
	m.now = func() time.Time { return t0 }
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = run(t, m, m.load(true))
	after := prov.CostCalls
	if after == 0 {
		t.Fatal("the first load should warm the cost cache")
	}
	// The timer, and the reload after an action, read the inventory live but
	// must leave costs to the hourly cache. Cost Explorer bills per call.
	m = run(t, m, m.poll())
	m = step(t, m, tickMsg{})
	m = run(t, m, m.poll())
	if prov.CostCalls != after {
		t.Fatalf("poll pulled costs: %d calls before, %d after", after, prov.CostCalls)
	}
	// r is an explicit reload and may pull.
	m = run(t, m, m.load(true))
	if prov.CostCalls <= after {
		t.Fatal("an explicit reload should refresh costs")
	}
}

func TestExtendFormWritesTags(t *testing.T) {
	svc, prov := seeded(t)
	m := newModel(context.Background(), svc, Options{})
	m.now = func() time.Time { return t0 }
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = run(t, m, m.load(true))
	m = step(t, m, tea.KeyPressMsg{Code: '/'})
	for _, r := range "legacy" {
		m = step(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	m = step(t, m, tea.KeyPressMsg{Code: 'e'})
	if m.form == nil || m.form.fields[0].input.Value() != "7" {
		t.Fatalf("e should open the extend form: %+v", m.form)
	}
	m.form.openedAt = t0.Add(-time.Second)
	m.form.fields[0].input.SetValue("soon")
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.form == nil || m.form.errMsg == "" {
		t.Fatal("bad span should keep the form open with an error")
	}
	m.form.fields[0].input.SetValue("2026-10-01")
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = run(t, next.(model), cmd)
	m = run(t, m, m.load(true)) // run() drops follow-up commands; reload by hand
	tags := prov.Tags("794137730641")
	if m.form != nil || !strings.HasPrefix(tags[core.TagExpires], "2026-10-01") {
		t.Fatalf("extend should write the expires tag: form=%v tags=%v", m.form, tags)
	}
	if _, warned := tags[core.TagWarnedAt]; warned {
		t.Fatal("extend should clear the warned-at tag")
	}
	for _, r := range m.visible {
		if r.Account.Name == "legacy-box" && r.Account.Status != core.StatusActive {
			t.Fatalf("legacy-box should be active after extend, got %s", r.Account.Status)
		}
	}
}

func TestCreateFormQueuesAndOperatorApproves(t *testing.T) {
	svc, prov := seeded(t)
	prov.FailNext = ""
	m := newModel(context.Background(), svc, Options{Operator: "ops@example.com"})
	m.now = func() time.Time { return t0 }
	m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = run(t, m, m.load(true))

	m = step(t, m, tea.KeyPressMsg{Code: 'n'})
	if m.form == nil || len(m.form.fields) != 6 {
		t.Fatalf("n should open the request form: %+v", m.form)
	}
	m.form.openedAt = t0.Add(-time.Second)
	for _, r := range "dev-new" {
		m = step(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	for _, r := range "dana@example.com" {
		m = step(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m.form.fields[2].input.SetValue("5d")
	m.form.fields[3].input.SetValue("15")
	m.form.fields[4].input.SetValue("bedrock trial")
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = run(t, next.(model), cmd)
	if m.form != nil || !strings.Contains(plain(m.content()), "✓ queued dev-new for dana@example.com") {
		t.Fatalf("submit should queue and flash:\n%s", plain(m.content()))
	}
	m = run(t, m, m.load(true))
	var idx = -1
	for i, r := range m.visible {
		if r.Account.Name == "dev-new" {
			idx = i
		}
	}
	if idx < 0 || m.visible[idx].Account.Status != core.StatusPending {
		t.Fatalf("request should show as pending: %+v", m.visible)
	}
	if !strings.Contains(plain(m.content()), "1 pending") || !strings.Contains(plain(m.content()), "◇ pending") {
		t.Fatalf("header and row should show pending:\n%s", plain(m.content()))
	}
	m.grid.cursor = idx
	m.grid.clamp(len(m.visible))

	// e/x do not apply to a pending row; a opens the approve dialog.
	m = step(t, m, tea.KeyPressMsg{Code: 'x'})
	if m.dialog != nil {
		t.Fatal("close must not apply to a pending request")
	}
	m = step(t, m, tea.KeyPressMsg{Code: 'a'})
	if m.dialog == nil || !strings.Contains(plain(m.content()), "Approve dev-new") || !strings.Contains(plain(m.content()), "bedrock trial") {
		t.Fatalf("a should open the approve dialog:\n%s", plain(m.content()))
	}
	// The operator queued it, so the operator may not approve it.
	m.now = func() time.Time { return t0.Add(time.Second) }
	next, cmd = m.Update(tea.KeyPressMsg{Code: 'y'})
	m = run(t, next.(model), cmd)
	if !strings.Contains(plain(m.content()), "cannot approve it") {
		t.Fatalf("self-approval should be refused:\n%s", plain(m.content()))
	}
	if q, err := svc.GetRequest(context.Background(), "dev-new"); err != nil || q.RequestedBy != "ops@example.com" {
		t.Fatalf("the operator should be recorded as requester: %+v %v", q, err)
	}
	// Another operator can.
	m.opts.Operator = "lead@example.com"
	for i, r := range m.visible {
		if r.Account.Name == "dev-new" {
			m.grid.cursor = i
		}
	}
	m = step(t, m, tea.KeyPressMsg{Code: 'a'})
	m.dialog.openedAt = t0
	next, cmd = m.Update(tea.KeyPressMsg{Code: 'y'})
	m = run(t, next.(model), cmd)
	if !strings.Contains(plain(m.content()), "✓ approved dev-new for dana@example.com") {
		t.Fatalf("approve flash missing:\n%s", plain(m.content()))
	}
	prov.Settle()
	svc.Refresh(context.Background())
	acc, err := svc.Resolve(context.Background(), "dev-new")
	if err != nil || acc.Status != core.StatusActive || acc.BudgetUSD != 15 || acc.Owner != "dana@example.com" || acc.ApprovedBy != "lead@example.com" || acc.Purpose != "bedrock trial" {
		t.Fatalf("placed account = %+v err=%v", acc, err)
	}
}

func TestHistoryInPaneAndDetail(t *testing.T) {
	svc, _ := seeded(t)
	hist := &audit.Memory{}
	hist.Record(context.Background(), core.AuditEvent{At: t0.Add(-2 * time.Hour), Event: "approved", Account: "dev-alpha", Owner: "alex", Actor: "lead@example.com", Message: "lead@example.com approved dev-alpha for alex"})
	hist.Record(context.Background(), core.AuditEvent{At: t0.Add(-time.Hour), Event: "extended", Account: "dev-alpha", Owner: "alex", Actor: "alex", Message: "alex extended dev-alpha to 2026-09-30"})
	hist.Record(context.Background(), core.AuditEvent{At: t0, Event: "closed", Account: "old-one", Owner: "sam", Actor: "system", Message: "old-one was closed"})
	m := newModel(context.Background(), svc, Options{Context: "x", Operator: "ops", History: hist})
	m.now = func() time.Time { return t0 }
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = run(t, m, m.load(true))
	// Rows sort newest first, so select dev-alpha explicitly.
	m = step(t, m, tea.KeyPressMsg{Code: '/'})
	for _, r := range "dev-alpha" {
		m = step(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = run(t, m, m.loadHistory())
	out := plain(m.content())
	if !strings.Contains(out, "history") || !strings.Contains(out, "extended alex extended dev-alpha") || !strings.Contains(out, "approved lead@example.com approved") {
		t.Fatalf("pane should list dev-alpha's history newest first:\n%s", out)
	}
	if strings.Index(out, "extended alex") > strings.Index(out, "approved lead") {
		t.Fatal("newest event should come first")
	}
	if strings.Contains(out, "old-one was closed") {
		t.Fatal("pane history must be scoped to the selected account")
	}
	// Full detail shows spend on the left and history on the right.
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	out = plain(m.content())
	if !strings.Contains(out, "daily spend") || !strings.Contains(out, "history") || !strings.Contains(out, "alex extended dev-alpha") {
		t.Fatalf("detail should show both columns:\n%s", out)
	}
	// A row without events says so; without a sink the pane shows daily spend instead.
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEscape}) // leave detail
	m = step(t, m, tea.KeyPressMsg{Code: '/'})
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEscape}) // clear the filter; selection stays on dev-alpha
	m = step(t, m, tea.KeyPressMsg{Code: 'k'})           // legacy-box, which has no events
	m = run(t, m, m.loadHistory())
	if !strings.Contains(plain(m.content()), "nothing recorded yet") {
		t.Fatalf("row without events should say so:\n%s", plain(m.content()))
	}
	plainM := fixture(t, 120, 30)
	if strings.Contains(plain(plainM.content()), "history") || !strings.Contains(plain(plainM.content()), "daily") {
		t.Fatal("without a sink the pane should fall back to daily spend")
	}
}

func TestHelpOverlayAndThemeSwitch(t *testing.T) {
	m := fixture(t, 120, 30)
	m = step(t, m, tea.KeyPressMsg{Code: '?'})
	out := plain(m.content())
	if !m.showHelp || !strings.Contains(out, "w withdraw request") || !strings.Contains(out, "e extend/edit") || !strings.Contains(out, "any key to close") {
		t.Fatalf("? should show full help:\n%s", out)
	}
	if got := len(lines(out)); got != 30 {
		t.Fatalf("overlay changed frame height to %d", got)
	}
	m = step(t, m, tea.KeyPressMsg{Code: 'j'})
	if m.showHelp {
		t.Fatal("any key should close help")
	}
	m = step(t, m, tea.BackgroundColorMsg{Color: color.White})
	if m.th.isDark {
		t.Fatal("white background should switch to the light theme")
	}
	if !strings.Contains(plain(m.content()), "dev-alpha") {
		t.Fatal("light theme frame should still render rows")
	}
}

func TestHelpersAndBackgroundLeak(t *testing.T) {
	m := fixture(t, 120, 30)
	if ansi.StringWidth(bar(0.82, 10, m.th.danger, lipgloss.NewStyle())) != 10 {
		t.Fatal("bar should be exactly 10 cells")
	}
	if got := sparkline([]float64{0, 1, 2, 4}, 10); got != "▁▃▅█" {
		t.Fatalf("sparkline = %q", got)
	}
	for in, want := range map[float64]string{412: "$412", 4200: "$4.2k", 12600: "$13k", 20.5: "$20.50", 25: "$25"} {
		if got := money(in); got != want {
			t.Errorf("money(%v) = %q want %q", in, got, want)
		}
	}
	cols := columns(m.width)
	if strings.Contains(m.tableRow(m.visible[1], cols, false), "48;2;") {
		t.Fatal("unselected row carries a background color")
	}
	if !strings.Contains(m.tableRow(m.visible[0], cols, true), "48;2;") {
		t.Fatal("selected row should carry the selection background")
	}
}

func TestEmptyAndLoadingStates(t *testing.T) {
	svc := core.NewService(fake.New(), nil, core.DefaultConfig(), nil)
	m := newModel(context.Background(), svc, Options{})
	m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	if !strings.Contains(plain(m.content()), "Loading accounts") {
		t.Fatal("initial frame should show loading")
	}
	m = run(t, m, m.load(true))
	if !strings.Contains(plain(m.content()), "No accounts yet") {
		t.Fatalf("empty frame should explain how to create one:\n%s", plain(m.content()))
	}
	m = step(t, m, tea.WindowSizeMsg{Width: 40, Height: 8})
	if got := len(lines(plain(m.content()))); got != 8 {
		t.Fatalf("tiny terminal frame has %d lines, want 8", got)
	}
}

func TestEditAndWithdrawPendingRequests(t *testing.T) {
	svc, _ := seeded(t)
	ctx := context.Background()
	if _, err := svc.SubmitRequest(ctx, core.RequestInput{Name: "pend", Owner: "dana@example.com", RequestedBy: "dev@example.com", BudgetUSD: 20, TTL: 5 * 24 * time.Hour}, "web"); err != nil {
		t.Fatal(err)
	}
	m := newModel(ctx, svc, Options{Operator: "ops@example.com"})
	m.now = func() time.Time { return t0 }
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = run(t, m, m.load(true))
	sel := func() {
		for i, r := range m.visible {
			if r.Account.Name == "pend" {
				m.grid.cursor = i
			}
		}
	}
	sel()

	// e on a pending row edits the request rather than extending.
	m = step(t, m, tea.KeyPressMsg{Code: 'e'})
	if m.form == nil || !strings.HasPrefix(m.form.title, "Edit request") || m.form.fields[1].input.Value() != "5" || m.form.fields[2].input.Value() != "20" {
		t.Fatalf("e should open a prefilled edit form: %+v", m.form)
	}
	m.form.openedAt = t0.Add(-time.Second)
	m.form.fields[2].input.SetValue("30")
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = run(t, next.(model), cmd)
	if q, err := svc.GetRequest(ctx, "pend"); err != nil || q.BudgetUSD != 30 || q.TTL != 5*24*time.Hour || q.Owner != "dana@example.com" {
		t.Fatalf("edit should change only the budget: %+v %v", q, err)
	}
	if !strings.Contains(plain(m.content()), "✓ pend: dana@example.com, 5d, $30/month") {
		t.Fatalf("edit flash missing:\n%s", plain(m.content()))
	}

	// w withdraws behind a dialog that defaults to Cancel.
	m = run(t, m, m.load(true))
	sel()
	m = step(t, m, tea.KeyPressMsg{Code: 'w'})
	if m.dialog == nil || m.dialog.selected != 1 || !strings.Contains(plain(m.content()), "Withdraw pend") {
		t.Fatalf("w should open the withdraw dialog:\n%s", plain(m.content()))
	}
	m.dialog.openedAt = t0.Add(-time.Second)
	next, cmd = m.Update(tea.KeyPressMsg{Code: 'y'})
	m = run(t, next.(model), cmd)
	if _, err := svc.GetRequest(ctx, "pend"); err == nil {
		t.Fatal("request should be gone after withdraw")
	}
	if !strings.Contains(plain(m.content()), "✓ withdrew pend") {
		t.Fatalf("withdraw flash missing:\n%s", plain(m.content()))
	}
	// w on a real account refuses with a flash.
	m = run(t, m, m.load(true))
	m.grid.cursor = 0
	m = step(t, m, tea.KeyPressMsg{Code: 'w'})
	if m.dialog != nil || !strings.Contains(plain(m.content()), "select a pending request") {
		t.Fatal("withdraw must only apply to pending rows")
	}
}

func TestServiceWarningsBecomeToastsAndNotifications(t *testing.T) {
	svc, _ := seeded(t)
	feed := NewFeed()
	m := newModel(context.Background(), svc, Options{Feed: feed})
	m.now = func() time.Time { return t0 }
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = run(t, m, m.load(true))

	// A warning from the service logger arrives through the feed. Info is dropped.
	log := slog.New(feed.Handler()).With("account", "csp-check")
	log.Info("notify", "subject", "noise")
	log.Warn("budget not created", "err", "describe budget: operation error Budgets: DescribeBudget, https response error StatusCode: 501, api error InternalFailure: Sorry, the DescribeBudget operation on the budgets service is not currently supported by LocalStack.")
	msg := m.waitNotice()()
	n, ok := msg.(noticeMsg)
	if !ok || n.level != noticeWarn || !strings.Contains(n.text, "budget not created: account=csp-check, err=describe budget") {
		t.Fatalf("warning should arrive as a notice, got %#v", msg)
	}
	m = step(t, m, msg)
	select {
	case extra := <-feed.ch:
		t.Fatalf("info records must be dropped, got %+v", extra)
	default:
	}

	frame := plain(m.content())
	if !strings.Contains(frame, "! budget not created") || !strings.Contains(frame, "https response…") || !strings.Contains(frame, "N notifications") || m.unread != 1 {
		t.Fatalf("toast missing or unread not counted:\n%s", frame)
	}
	// The toast wraps rather than runs off the edge, and never pushes the
	// frame wider than the terminal.
	for i, l := range lines(frame) {
		if w := ansi.StringWidth(l); w > 120 {
			t.Fatalf("line %d is %d wide:\n%s", i, w, frame)
		}
	}
	t.Logf("frame with toast:\n%s", frame)

	// N opens the full list with the whole message, and clears the counter.
	m = step(t, m, tea.KeyPressMsg{Code: 'N', Text: "N"})
	frame = plain(m.content())
	if !m.showNotices || m.unread != 0 || !strings.Contains(frame, "Notifications") || !strings.Contains(frame, "not currently supported by LocalStack") {
		t.Fatalf("N should open the notifications window with the full text:\n%s", frame)
	}
	if strings.Contains(frame, "1 new") {
		t.Fatal("unread counter should reset when the window opens")
	}
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.showNotices {
		t.Fatal("esc should close the window")
	}

	// Toasts age out; the notice stays in the list, and a second one that
	// arrives later shows the counter in the header once the toast is gone.
	m.now = func() time.Time { return t0.Add(toastTTL + time.Second) }
	if frame := plain(m.content()); strings.Contains(frame, "! budget not created") {
		t.Fatalf("toast should age out:\n%s", frame)
	}
	if len(m.notices) != 1 {
		t.Fatalf("notices = %d", len(m.notices))
	}
	log.Error("access not granted", "err", "boom")
	m = step(t, m, m.waitNotice()())
	m.now = func() time.Time { return t0.Add(2*toastTTL + time.Second) }
	if frame := plain(m.content()); !strings.Contains(frame, "1 new · N") {
		t.Fatalf("header should count unread notices:\n%s", frame)
	}
	// c in the window clears everything.
	m = step(t, m, tea.KeyPressMsg{Code: 'N', Text: "N"})
	m = step(t, m, tea.KeyPressMsg{Code: 'c', Text: "c"})
	if len(m.notices) != 0 || m.unread != 0 || !strings.Contains(plain(m.content()), "Nothing yet") {
		t.Fatal("c should clear the list")
	}
}

// pendingFixture seeds a request queued by someone else and selects it.
func pendingFixture(t *testing.T) (model, *core.Service, *fake.Provider) {
	t.Helper()
	svc, prov := seeded(t)
	ctx := context.Background()
	if _, err := svc.SubmitRequest(ctx, core.RequestInput{Name: "pend", Owner: "dana@example.com", RequestedBy: "dev@example.com", BudgetUSD: 20, TTL: 5 * 24 * time.Hour}, "web"); err != nil {
		t.Fatal(err)
	}
	m := newModel(ctx, svc, Options{Operator: "ops@example.com"})
	m.now = func() time.Time { return t0 }
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = run(t, m, m.load(true))
	for i, r := range m.visible {
		if r.Account.Name == "pend" {
			m.grid.cursor = i
		}
	}
	return m, svc, prov
}

func TestApproveDialogDefaultsToCancel(t *testing.T) {
	m, _, _ := pendingFixture(t)
	m = step(t, m, tea.KeyPressMsg{Code: 'a'})
	if m.dialog == nil || m.dialog.buttons[1] != "Cancel" || m.dialog.selected != 1 {
		t.Fatalf("a should open the approve dialog with Cancel selected: %+v", m.dialog)
	}
	// Enter on the default button must not approve.
	m.dialog.openedAt = t0.Add(-time.Second)
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = run(t, next.(model), cmd)
	if m.dialog != nil || m.working {
		t.Fatal("enter on Cancel should just close the dialog")
	}
	if strings.Contains(plain(m.content()), "approved pend") {
		t.Fatal("enter on the default button must not approve")
	}
}

func TestFailedReloadKeepsRowsAndWarns(t *testing.T) {
	m := fixture(t, 120, 30)
	n := len(m.rows)
	if n == 0 {
		t.Fatal("fixture should load rows")
	}
	// step, not run: the flash's redraw tick would sleep for toastTTL.
	next, cmd := m.Update(loadedMsg{seq: m.loadSeq, err: errors.New("boom")})
	m = next.(model)
	if cmd == nil {
		t.Fatal("a failed reload should raise a flash")
	}
	if len(m.rows) != n || len(m.visible) == 0 {
		t.Fatalf("a failed reload must keep the old rows, got %d", len(m.rows))
	}
	if !m.stale {
		t.Fatal("a failed reload should mark the table stale")
	}
	last := m.notices[len(m.notices)-1]
	if last.level != noticeError || last.text != "reload failed, showing stale accounts: boom" {
		t.Fatalf("stale flash should be an error notice, got %+v", last)
	}
	out := plain(m.content())
	if !strings.Contains(out, "reload failed, showing stale accounts") || !strings.Contains(out, "dev-alpha") {
		t.Fatalf("frame should show the toast over the old rows:\n%s", out)
	}
	// The toast covers the header's right side, so read the header alone.
	if !strings.Contains(plain(m.headerView()), "stale · r reload") {
		t.Fatalf("header should mark the table stale:\n%s", plain(m.headerView()))
	}
	// The next good load clears the marker.
	m = run(t, m, m.load(true))
	if m.stale || strings.Contains(plain(m.headerView()), "stale") {
		t.Fatal("a good load should clear the stale marker")
	}
}

func TestStaleLoadResultIsIgnored(t *testing.T) {
	svc, _ := seeded(t)
	m := newModel(context.Background(), svc, Options{})
	m.now = func() time.Time { return t0 }
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	first := m.load(true)
	second := m.load(true)
	if m.loadSeq != 2 {
		t.Fatalf("each load should take a new sequence number, got %d", m.loadSeq)
	}
	m = step(t, m, second())
	n := len(m.rows)
	if n == 0 {
		t.Fatal("second load should fill the table")
	}
	// The first load finishes late and sees a fleet that has since changed.
	if _, err := svc.SubmitRequest(context.Background(), core.RequestInput{Name: "late", Owner: "x@example.com", RequestedBy: "y@example.com"}, "web"); err != nil {
		t.Fatal(err)
	}
	m = step(t, m, first())
	if len(m.rows) != n {
		t.Fatalf("a result older than the newest load must be dropped: rows %d -> %d", n, len(m.rows))
	}
	m = run(t, m, m.load(true))
	if len(m.rows) != n+1 {
		t.Fatalf("a fresh load should still land: rows %d, want %d", len(m.rows), n+1)
	}

	// History reads that started before the cache was cleared are dropped too.
	sel, ok := m.selected()
	if !ok {
		t.Fatal("no selection")
	}
	name := sel.Account.Name
	hist := &audit.Memory{}
	hist.Record(context.Background(), core.AuditEvent{At: t0, Event: "approved", Account: name, Owner: "a", Actor: "b", Message: "old"})
	m.opts.History = hist
	late := m.loadHistory()
	m.clearHistory()
	m = run(t, m, late)
	if _, ok := m.hist[name]; ok {
		t.Fatal("history read from before a clear must not fill the cache")
	}
	m = run(t, m, m.loadHistory())
	if _, ok := m.hist[name]; !ok {
		t.Fatal("a fresh history read should fill the cache")
	}
}

func TestConfirmFiresOnce(t *testing.T) {
	svc, prov := seeded(t)
	m := newModel(context.Background(), svc, Options{})
	m.now = func() time.Time { return t0 }
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = run(t, m, m.load(true))
	m = step(t, m, tea.KeyPressMsg{Code: '/'})
	for _, r := range "ml-" {
		m = step(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	m = step(t, m, tea.KeyPressMsg{Code: 'x'})
	m.dialog.openedAt = t0.Add(-time.Second)
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyLeft}) // select Close account
	next, first := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if first == nil || m.dialog != nil || !m.working {
		t.Fatalf("confirm should close the dialog and mark work in flight: dialog=%v working=%v", m.dialog, m.working)
	}
	if !strings.Contains(plain(m.content()), "working") {
		t.Fatalf("header should show the action running:\n%s", plain(m.content()))
	}
	// A second enter lands on the dashboard, not the dialog.
	next, second := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	m = run(t, m, second)
	m = run(t, m, first)
	if len(prov.Closed) != 1 {
		t.Fatalf("the action must run once, closed=%v", prov.Closed)
	}
	if m.working {
		t.Fatal("the action result should clear the working state")
	}

	// The same for forms: the request form closes on submit.
	prov.FailNext = ""
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEscape}) // leave detail opened by the stray enter
	m = step(t, m, tea.KeyPressMsg{Code: 'n'})
	if m.form == nil {
		t.Fatal("n should open the request form")
	}
	m.form.openedAt = t0.Add(-time.Second)
	m.form.fields[0].input.SetValue("dev-once")
	m.form.fields[1].input.SetValue("dana@example.com")
	next, first = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if first == nil || m.form != nil || !m.working {
		t.Fatalf("submit should close the form and mark work in flight: form=%v working=%v", m.form, m.working)
	}
	next, second = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	m = run(t, m, second)
	m = run(t, m, first)
	if m.working || !strings.Contains(plain(m.content()), "✓ queued dev-once") {
		t.Fatalf("submit should run once and flash:\n%s", plain(m.content()))
	}
}

func TestCtrlCQuitsFromEveryMode(t *testing.T) {
	ctrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	if ctrlC.String() != "ctrl+c" {
		t.Fatalf("key = %q", ctrlC.String())
	}
	quits := func(t *testing.T, m model, mode string) {
		t.Helper()
		_, cmd := m.Update(ctrlC)
		if cmd == nil {
			t.Fatalf("%s: ctrl+c produced no command", mode)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("%s: ctrl+c should quit", mode)
		}
	}
	m := fixture(t, 120, 30)
	quits(t, m, "dashboard")

	f := step(t, m, tea.KeyPressMsg{Code: '/'})
	if !f.filtering {
		t.Fatal("/ should open the filter")
	}
	quits(t, f, "filter")
	// q inside the filter is typed, not quit.
	f = step(t, f, tea.KeyPressMsg{Code: 'q', Text: "q"})
	if f.filter.Value() != "q" {
		t.Fatalf("q should be typed into the filter, got %q", f.filter.Value())
	}

	d := step(t, m, tea.KeyPressMsg{Code: 'x'})
	if d.dialog == nil {
		t.Fatal("x should open a dialog")
	}
	quits(t, d, "dialog")

	n := step(t, m, tea.KeyPressMsg{Code: 'n'})
	if n.form == nil {
		t.Fatal("n should open the form")
	}
	quits(t, n, "form")

	w := step(t, m, tea.KeyPressMsg{Code: 'N', Text: "N"})
	if !w.showNotices {
		t.Fatal("N should open notifications")
	}
	quits(t, w, "notifications")

	h := step(t, m, tea.KeyPressMsg{Code: '?'})
	quits(t, h, "help")
}

func TestParseSpan(t *testing.T) {
	from := t0
	cases := []struct {
		in      string
		from    time.Time
		want    time.Time
		wantErr string
	}{
		{"7", from, from.Add(7 * 24 * time.Hour), ""},
		{"7d", from, from.Add(7 * 24 * time.Hour), ""},
		{"72h", from, from.Add(72 * time.Hour), ""},
		{"2026-10-01", from, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), ""},
		{"5", time.Time{}, time.Time{}.Add(5 * 24 * time.Hour), ""},
		{"", from, time.Time{}, "enter a number of days"},
		{"0", from, time.Time{}, "days must be a positive number"},
		{"-5", from, time.Time{}, "days must be a positive number"},
		{"0d", from, time.Time{}, "days must be a positive number"},
		{"-5d", from, time.Time{}, "days must be a positive number"},
		{"NaN", from, time.Time{}, "days must be a positive number"},
		{"Inf", from, time.Time{}, "days must be a positive number"},
		{"-Inf", from, time.Time{}, "days must be a positive number"},
		{"NaNd", from, time.Time{}, "days must be a positive number"},
		{"1e300", from, time.Time{}, "days must be at most"},
		{"-72h", from, time.Time{}, "duration must be positive"},
		{"0h", from, time.Time{}, "duration must be positive"},
		{"soon", from, time.Time{}, "bad value"},
		{"xd", from, time.Time{}, "bad span"},
		// A lifetime (zero from) is a count of days, never a date.
		{"2026-10-01", time.Time{}, time.Time{}, "not a date"},
		{"", time.Time{}, time.Time{}, "enter a number of days"},
	}
	for _, c := range cases {
		got, err := parseSpan(c.in, c.from)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("parseSpan(%q) err = %v, want %q", c.in, err, c.wantErr)
			}
			continue
		}
		if err != nil || !got.Equal(c.want) {
			t.Errorf("parseSpan(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
}

func TestPaneRuleSurvivesTinyWidth(t *testing.T) {
	svc := core.NewService(fake.New(), nil, core.DefaultConfig(), nil)
	m := newModel(context.Background(), svc, Options{})
	m = run(t, m, m.load(true))
	for _, w := range []int{0, 1, 2, 3} {
		if got := len(lines(m.paneView(w, 3))); got != 3 {
			t.Fatalf("paneView(%d) has %d lines, want 3", w, got)
		}
	}
}

func TestGridClampPullsOffsetBackWhenTaller(t *testing.T) {
	g := grid{cursor: 9, offset: 5, height: 5}
	g.clamp(10)
	if g.offset != 5 {
		t.Fatalf("offset = %d, want 5", g.offset)
	}
	g.height = 8 // the terminal grew: rows 2..9 fit
	g.clamp(10)
	if g.offset != 2 || g.cursor != 9 {
		t.Fatalf("after growing offset = %d cursor = %d, want 2 and 9", g.offset, g.cursor)
	}
	g.height = 20
	g.clamp(10)
	if g.offset != 0 {
		t.Fatalf("all rows fit, offset = %d, want 0", g.offset)
	}
	g.clamp(0)
	if g.offset != 0 || g.cursor != 0 {
		t.Fatalf("empty grid: offset = %d cursor = %d", g.offset, g.cursor)
	}
}

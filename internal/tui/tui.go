// Package tui is a Bubble Tea v2 front end over core.Service: a fleet
// dashboard with section tabs, a detail pane, dialogs, and a help overlay.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"playplace/internal/audit"
	"playplace/internal/core"
)

// History reads past events for the selected account. nil hides history.
type History interface {
	Query(ctx context.Context, f audit.Filter) ([]core.AuditEvent, error)
}

// Options carries display-only facts the service does not know.
type Options struct {
	Context  string  // AWS profile or endpoint shown in the header
	Operator string  // who is at the keyboard, recorded as approver
	History  History // where lifecycle history is read from
	Feed     *Feed   // service log records to show as notices; nil shows only action results
}

// Run starts the TUI and blocks until the user quits.
func Run(ctx context.Context, svc *core.Service, opts Options) error {
	_, err := tea.NewProgram(newModel(ctx, svc, opts), tea.WithContext(ctx)).Run()
	return err
}

// Snapshot renders one frame of the dashboard at the given size using live
// data and returns it as styled text. keys is a space-separated list of key
// names (enter, esc, tab, up, down, or literal characters) to press first.
func Snapshot(ctx context.Context, svc *core.Service, opts Options, width, height int, keys string) string {
	m := newModel(ctx, svc, opts)
	m.width, m.height = width, height
	m.layout()
	apply := func(msg tea.Msg) {
		if msg == nil {
			return
		}
		next, _ := m.Update(msg)
		m = next.(model)
	}
	apply(m.load(false)())
	apply(m.loadHistory()())
	special := map[string]rune{"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "up": tea.KeyUp, "down": tea.KeyDown, "space": tea.KeySpace}
	for _, tok := range strings.Fields(keys) {
		if code, ok := special[tok]; ok {
			apply(tea.KeyPressMsg{Code: code})
			continue
		}
		for _, r := range tok {
			apply(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}
	return m.content()
}

type section int

const (
	secOpen section = iota
	secActive
	secExpiring
	secClosed
	sectionCount_
)

// row is an account plus the cost facts the dashboard shows beside it.
type row struct {
	Account *core.Account
	MTD     float64          // month-to-date spend
	Series  []float64        // daily spend, oldest first, last 30 days
	Points  []core.CostPoint // daily spend, oldest first
}

// grid tracks cursor and scroll offset for the account table.
type grid struct {
	cursor, offset, height int
}

func (g *grid) clamp(n int) {
	if g.cursor >= n {
		g.cursor = n - 1
	}
	if g.cursor < 0 {
		g.cursor = 0
	}
	if g.height <= 0 {
		g.height = 1
	}
	if g.cursor < g.offset {
		g.offset = g.cursor
	}
	if g.cursor >= g.offset+g.height {
		g.offset = g.cursor - g.height + 1
	}
	// A taller terminal shows more rows; pull the window back so it does not
	// leave blank lines under the last row.
	if max := n - g.height; g.offset > max {
		g.offset = max
	}
	if g.offset < 0 {
		g.offset = 0
	}
}

func (g *grid) move(delta, n int) {
	g.cursor += delta
	g.clamp(n)
}

type dialog struct {
	title, body string
	buttons     [2]string // confirm, cancel
	selected    int
	destructive bool
	openedAt    time.Time
	confirm     func() tea.Cmd
}

// dialogGrace ignores keys just after a dialog opens so a queued keystroke
// cannot confirm it.
const dialogGrace = 150 * time.Millisecond

type model struct {
	ctx  context.Context
	svc  *core.Service
	opts Options
	now  func() time.Time

	// data
	rows    []row
	visible []row
	loading bool
	stale   bool // the last reload failed; rows are from an earlier load
	err     error
	hist    map[string][]core.AuditEvent // account name -> newest first
	loadSeq int                          // newest load issued; older results are dropped
	histSeq int                          // bumped when hist is cleared; older results are dropped

	// chrome
	width, height int
	th            theme
	keys          keyMap
	help          help.Model
	spinner       spinner.Model
	filter        textinput.Model

	// state
	grid       grid
	section    section
	filtering  bool
	showHelp   bool
	showDetail bool
	syncing    bool
	working    bool // an action is running; its dialog or form is already closed
	dialog     *dialog
	form       *form

	// notices: action outcomes and service warnings, see notice.go
	feed         *Feed
	notices      []notice
	unread       int
	showNotices  bool
	noticeScroll int
}

type (
	tickMsg   struct{}
	loadedMsg struct {
		seq  int
		rows []row
		err  error
	}
	actionMsg struct {
		flash string
		err   error
	}
	syncMsg struct {
		sum core.Summary
		err error
	}
	histMsg struct {
		seq    int
		name   string
		events []core.AuditEvent
	}
)

func newModel(ctx context.Context, svc *core.Service, opts Options) model {
	if opts.Context == "" {
		opts.Context = "default"
	}
	if opts.Operator == "" {
		opts.Operator = "operator"
	}
	th := newTheme(true)
	fi := textinput.New()
	fi.Prompt = "/ "
	fi.Placeholder = "name, owner, id, or owner:NAME"
	fi.CharLimit = 64
	fi.SetWidth(36)
	m := model{
		ctx:     ctx,
		svc:     svc,
		opts:    opts,
		now:     time.Now,
		loading: true,
		hist:    map[string][]core.AuditEvent{},
		th:      th,
		keys:    newKeyMap(),
		help:    help.New(),
		spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		filter:  fi,
		feed:    opts.Feed,
		width:   100,
		height:  30,
	}
	m.applyTheme()
	m.layout()
	return m
}

func (m *model) applyTheme() {
	th := m.th
	m.help.Styles = help.DefaultStyles(th.isDark)
	m.help.Styles.ShortKey = th.helpKey
	m.help.Styles.ShortDesc = th.helpDesc
	m.help.Styles.ShortSeparator = th.dialogHint
	m.spinner.Style = lipgloss.NewStyle().Foreground(th.primary)
	st := textinput.DefaultStyles(th.isDark)
	st.Focused.Prompt = lipgloss.NewStyle().Foreground(th.primary)
	st.Focused.Text = lipgloss.NewStyle().Foreground(th.fg)
	st.Focused.Placeholder = lipgloss.NewStyle().Foreground(th.faint)
	m.filter.SetStyles(st)
}

// ---- layout ----------------------------------------------------------------

// showPane reports whether there is room for the detail pane under the table.
func (m model) showPane() bool { return m.height >= tallBreakpoint }

func (m model) mainHeight() int {
	h := m.height - chromeLines
	if h < 3 {
		h = 3
	}
	return h
}

// paneHeight is the rows given to the bottom detail pane, rule included.
func (m model) paneHeight() int {
	if !m.showPane() {
		return 0
	}
	h := m.mainHeight() * 2 / 5
	if h < 7 {
		h = 7
	}
	if h > 11 {
		h = 11
	}
	return h
}

func (m model) tableHeight() int {
	h := m.mainHeight() - m.paneHeight()
	if h < 3 {
		h = 3
	}
	return h
}

func (m *model) layout() {
	m.grid.height = m.tableHeight() - 1 // minus table header
	m.grid.clamp(len(m.visible))
	m.help.SetWidth(m.width - 2)
}

// ---- data ------------------------------------------------------------------

// load lists the inventory (live when force) and fills spend from the cost
// cache, warming it in parallel. With force set the costs are pulled fresh
// too, which is what r and S mean; everything else goes through poll.
// Each call takes a new sequence number so a slow result cannot overwrite a
// newer one.
func (m *model) load(force bool) tea.Cmd {
	m.loadSeq++
	return m.loadWith(force, force)
}

// poll reads the inventory live but leaves costs to the service's hourly
// cache. Cost Explorer bills per call, so the timer and the post-action
// reloads must never force a pull.
func (m *model) poll() tea.Cmd {
	m.loadSeq++
	return m.loadWith(true, false)
}

// loadWith stamps the result with the current sequence number. Init cannot
// change the model, so it calls this directly and its load keeps sequence 0.
func (m model) loadWith(forceInventory, forceCosts bool) tea.Cmd {
	svc, ctx, seq := m.svc, m.ctx, m.loadSeq
	return func() tea.Msg {
		accounts, err := svc.Inventory(ctx, forceInventory)
		if err != nil {
			return loadedMsg{seq: seq, err: err}
		}
		var ids []string
		for _, a := range accounts {
			if a.ProviderID != "" && a.Status != core.StatusClosed {
				ids = append(ids, a.ID)
			}
		}
		_ = svc.WarmCosts(ctx, ids, forceCosts)

		now := time.Now()
		monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		today := now.UTC().Truncate(24 * time.Hour)
		rows := make([]row, 0, len(accounts))
		for _, a := range accounts {
			r := row{Account: a}
			if pts, ok := svc.CostsCached(a.ProviderID, 31); ok && a.ProviderID != "" {
				r.Points = pts
				byDay := map[string]float64{}
				for _, p := range pts {
					byDay[p.Date.UTC().Format("2006-01-02")] = p.AmountUSD
					if !p.Date.Before(monthStart) {
						r.MTD += p.AmountUSD
					}
				}
				if len(pts) > 0 {
					for d := today.Add(-29 * 24 * time.Hour); !d.After(today); d = d.Add(24 * time.Hour) {
						r.Series = append(r.Series, byDay[d.Format("2006-01-02")])
					}
				}
			}
			rows = append(rows, r)
		}
		return loadedMsg{seq: seq, rows: rows}
	}
}

// loadHistory fetches events for the selected account unless cached.
// Returns nil when there is nothing to do.
func (m model) loadHistory() tea.Cmd {
	r, ok := m.selected()
	if !ok || m.opts.History == nil {
		return func() tea.Msg { return nil }
	}
	if _, cached := m.hist[r.Account.Name]; cached {
		return func() tea.Msg { return nil }
	}
	h, ctx, name, seq := m.opts.History, m.ctx, r.Account.Name, m.histSeq
	return func() tea.Msg {
		events, err := h.Query(ctx, audit.Filter{Account: name, Limit: 50})
		if err != nil {
			return nil
		}
		if events == nil {
			events = []core.AuditEvent{}
		}
		return histMsg{seq: seq, name: name, events: events}
	}
}

// clearHistory drops the cache and outranks every history read in flight.
func (m *model) clearHistory() {
	m.hist = map[string][]core.AuditEvent{}
	m.histSeq++
}

func tick() tea.Cmd {
	return tea.Tick(30*time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m model) inSection(r row, s section) bool {
	a := r.Account
	switch s {
	case secOpen:
		return a.Status != core.StatusClosed
	case secActive:
		return a.Status == core.StatusActive
	case secExpiring:
		return a.Status == core.StatusExpiring ||
			(a.Status == core.StatusActive && !a.ExpiresAt.IsZero() && a.ExpiresAt.Sub(m.now()) <= 7*24*time.Hour)
	case secClosed:
		return a.Status == core.StatusClosed || a.Status == core.StatusClosing
	}
	return false
}

func (m model) sectionCount(s section) int {
	n := 0
	for _, r := range m.rows {
		if m.inSection(r, s) {
			n++
		}
	}
	return n
}

func (m model) matchesFilter(r row) bool {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	if q == "" {
		return true
	}
	a := r.Account
	if strings.HasPrefix(q, "owner:") {
		return strings.Contains(strings.ToLower(a.Owner), strings.TrimPrefix(q, "owner:"))
	}
	hay := strings.ToLower(a.Name + " " + a.Owner + " " + a.ProviderID + " " + string(a.Status))
	return strings.Contains(hay, q)
}

func (m *model) applyFilter() {
	var keep string
	if r, ok := m.selected(); ok {
		keep = r.Account.ID
	}
	m.visible = m.visible[:0]
	for _, r := range m.rows {
		if m.inSection(r, m.section) && m.matchesFilter(r) {
			m.visible = append(m.visible, r)
		}
	}
	m.grid.clamp(len(m.visible))
	for i, r := range m.visible {
		if r.Account.ID == keep {
			m.grid.cursor = i
			m.grid.clamp(len(m.visible))
			break
		}
	}
}

func (m model) selected() (row, bool) {
	if m.grid.cursor < 0 || m.grid.cursor >= len(m.visible) {
		return row{}, false
	}
	return m.visible[m.grid.cursor], true
}

// ---- tea.Model -------------------------------------------------------------

func (m model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.loadWith(false, false), tick(), m.spinner.Tick, m.waitNotice())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		m.th = newTheme(msg.IsDark())
		m.applyTheme()
		return m, nil

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil

	case spinner.TickMsg:
		if !m.loading && !m.syncing && !m.working {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tickMsg:
		return m, tea.Batch(m.poll(), tick())

	case loadedMsg:
		if msg.seq < m.loadSeq {
			return m, nil // a newer load is in flight or has landed
		}
		m.loading = false
		m.err = msg.err
		if msg.err != nil {
			if len(m.rows) == 0 {
				return m, nil // the table body shows the error
			}
			m.stale = true
			return m, m.setFlash("reload failed, showing stale accounts: "+msg.err.Error(), true)
		}
		m.stale = false
		m.rows = msg.rows
		m.applyFilter()
		return m, m.loadHistory()

	case histMsg:
		if msg.seq < m.histSeq {
			return m, nil // cleared since this read started
		}
		m.hist[msg.name] = msg.events
		return m, nil

	case syncMsg:
		m.syncing = false
		m.clearHistory() // the pass may have added events
		var cmd tea.Cmd
		switch {
		case msg.err != nil:
			cmd = m.setFlash("sync: "+msg.err.Error(), true)
		case msg.sum.Empty():
			cmd = m.setFlash("✓ synced with AWS, nothing changed", false)
		default:
			cmd = m.setFlash("✓ synced: "+msg.sum.String(), false)
		}
		return m, tea.Batch(cmd, m.load(true))

	case actionMsg:
		m.working = false
		m.clearHistory() // the action added an event
		var cmd tea.Cmd
		if msg.err != nil {
			cmd = m.setFlash(msg.err.Error(), true)
		} else {
			cmd = m.setFlash(msg.flash, false)
		}
		return m, tea.Batch(cmd, m.poll())

	case noticeMsg:
		return m, tea.Batch(m.notify(msg.level, msg.text), m.waitNotice())

	case toastAgingMsg:
		return m, nil // redraw; the toast has aged out

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	// ctrl+c quits from every mode. q does not, since inputs may need it.
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.form != nil {
		cmd, done := m.form.update(msg, m.now())
		if done {
			m.form = nil
			if cmd != nil {
				return m, m.startAction(cmd)
			}
		}
		return m, cmd
	}
	if m.dialog != nil {
		return m.dialogKey(msg)
	}
	if m.showNotices {
		return m.noticesKey(msg)
	}
	if m.filtering {
		switch msg.String() {
		case "esc":
			m.filtering = false
			m.filter.Blur()
			m.filter.Reset()
			m.applyFilter()
			return m, nil
		case "enter":
			m.filtering = false
			m.filter.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.applyFilter()
		return m, cmd
	}
	if m.showHelp {
		m.showHelp = false
		return m, nil
	}
	if key.Matches(msg, k.Quit) {
		return m, tea.Quit
	}
	if key.Matches(msg, k.Help) {
		m.showHelp = true
		return m, nil
	}
	if key.Matches(msg, k.Notices) {
		m.showNotices, m.unread, m.noticeScroll = true, 0, 0
		return m, nil
	}

	if m.showDetail {
		switch {
		case key.Matches(msg, k.Back, k.Enter):
			m.showDetail = false
		case key.Matches(msg, k.Extend):
			return m.openExtend()
		case key.Matches(msg, k.Close):
			return m.openClose()
		case key.Matches(msg, k.Yank):
			return m.yank()
		}
		return m, nil
	}

	n := len(m.visible)
	switch {
	case key.Matches(msg, k.Up):
		m.grid.move(-1, n)
	case key.Matches(msg, k.Down):
		m.grid.move(1, n)
	case key.Matches(msg, k.Top):
		m.grid.move(-n, n)
	case key.Matches(msg, k.Bottom):
		m.grid.move(n, n)
	case key.Matches(msg, k.PageUp):
		m.grid.move(-m.grid.height, n)
	case key.Matches(msg, k.PageDown):
		m.grid.move(m.grid.height, n)
	case key.Matches(msg, k.NextTab):
		m.setSection((m.section + 1) % sectionCount_)
	case key.Matches(msg, k.PrevTab):
		m.setSection((m.section + sectionCount_ - 1) % sectionCount_)
	case key.Matches(msg, k.Tab1):
		m.setSection(secOpen)
	case key.Matches(msg, k.Tab2):
		m.setSection(secActive)
	case key.Matches(msg, k.Tab3):
		m.setSection(secExpiring)
	case key.Matches(msg, k.Tab4):
		m.setSection(secClosed)
	case key.Matches(msg, k.Filter):
		m.filtering = true
		return m, m.filter.Focus()
	case key.Matches(msg, k.New):
		m.form = m.newCreateForm()
	case key.Matches(msg, k.Sync):
		return m.sync()
	case key.Matches(msg, k.Enter):
		if _, ok := m.selected(); ok {
			m.showDetail = true
		}
	case key.Matches(msg, k.Extend):
		return m.openExtend()
	case key.Matches(msg, k.Close):
		return m.openClose()
	case key.Matches(msg, k.Approve):
		return m.openApprove()
	case key.Matches(msg, k.Deny):
		return m.openDeny()
	case key.Matches(msg, k.Withdraw):
		return m.openWithdraw()
	case key.Matches(msg, k.Yank):
		return m.yank()
	case key.Matches(msg, k.Refresh):
		m.loading = true
		m.clearHistory()
		return m, tea.Batch(m.load(true), m.spinner.Tick)
	}
	return m, m.loadHistory()
}

func (m *model) setSection(s section) {
	m.section = s
	m.grid.cursor, m.grid.offset = 0, 0
	m.applyFilter()
}

// ---- actions ---------------------------------------------------------------

// openExtend opens the extend form on a real account and the edit form on
// a pending request: e changes the lifetime of whatever is selected.
func (m model) openExtend() (tea.Model, tea.Cmd) {
	r, ok := m.selected()
	if !ok {
		return m, nil
	}
	a := r.Account
	if a.Status == core.StatusPending {
		m.form = m.newEditForm(a)
		return m, nil
	}
	if !a.CanExtend() {
		if err := a.RepairError(); err != nil {
			return m, m.setFlash(err.Error(), true)
		}
		return m, m.setFlash(fmt.Sprintf("%s is %s and cannot be extended", a.Name, a.Status), true)
	}
	m.form = m.newExtendForm(a)
	return m, nil
}

// openWithdraw pulls a pending request back, as the requester would in the
// web UI. The operator may withdraw anyone's.
func (m model) openWithdraw() (tea.Model, tea.Cmd) {
	r, ok := m.selected()
	if !ok || r.Account.Status != core.StatusPending {
		return m, m.setFlash("select a pending request to withdraw", true)
	}
	a := r.Account
	svc, ctx, name, who := m.svc, m.ctx, a.Name, m.opts.Operator
	m.dialog = &dialog{
		title:       "Withdraw " + a.Name,
		body:        fmt.Sprintf("Withdraw the request for %s by %s? It leaves the queue without a notice to approvers.", a.Name, a.RequestedBy),
		buttons:     [2]string{"Withdraw", "Cancel"},
		selected:    1,
		destructive: true,
		openedAt:    m.now(),
		confirm: func() tea.Cmd {
			return func() tea.Msg {
				if err := svc.Withdraw(ctx, name, who); err != nil {
					return actionMsg{err: err}
				}
				return actionMsg{flash: "✓ withdrew " + name}
			}
		},
	}
	return m, nil
}

// sync runs the refresh pass, the same as `playplace reconcile`.
func (m model) sync() (tea.Model, tea.Cmd) {
	if m.syncing {
		return m, nil
	}
	m.syncing = true
	svc, ctx := m.svc, m.ctx
	return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
		sum, err := svc.Refresh(ctx)
		return syncMsg{sum: sum, err: err}
	})
}

func (m model) openClose() (tea.Model, tea.Cmd) {
	r, ok := m.selected()
	if !ok {
		return m, nil
	}
	a := r.Account
	if !a.Status.CanClose() {
		return m, m.setFlash(fmt.Sprintf("%s is %s and cannot be closed", a.Name, a.Status), true)
	}
	svc, ctx, id, name, who := m.svc, m.ctx, a.ID, a.Name, m.opts.Operator
	m.dialog = &dialog{
		title:       "Close " + a.Name,
		body:        fmt.Sprintf("Request closure of %s (%s) owned by %s? AWS processes closure asynchronously; charges may continue while pending. Commitments and subscriptions can outlive closure. Recovery requires AWS Support during the 90-day post-closure period.", a.Name, a.ProviderID, a.Owner),
		buttons:     [2]string{"Close account", "Cancel"},
		selected:    1,
		destructive: true,
		openedAt:    m.now(),
		confirm: func() tea.Cmd {
			return func() tea.Msg {
				acc, err := svc.RequestClose(ctx, id, who)
				flash := "✓ " + name + " closed"
				if err == nil && acc.Status == core.StatusClosing {
					flash = "✓ " + name + " awaiting confirmed AWS closure; reconciliation will monitor and retry"
				}
				return actionMsg{flash: flash, err: err}
			}
		},
	}
	return m, nil
}

// openApprove confirms approving the selected pending request. The TUI runs
// with management credentials, so the operator's identity is the approver.
func (m model) openApprove() (tea.Model, tea.Cmd) {
	r, ok := m.selected()
	if !ok || r.Account.Status != core.StatusPending {
		return m, m.setFlash("select a pending request to approve", true)
	}
	a := r.Account
	svc, ctx, name, who := m.svc, m.ctx, a.Name, m.opts.Operator
	body := fmt.Sprintf("Approve %s for %s: %s/month for %d days?", a.Name, a.Owner, money(a.BudgetUSD), int(a.TTL.Hours()/24))
	if a.Purpose != "" {
		body += " Purpose: " + a.Purpose
	}
	if a.OverrideLimits {
		body += " Limits overridden by an admin."
	}
	m.dialog = &dialog{
		title:    "Approve " + a.Name,
		body:     body,
		buttons:  [2]string{"Approve", "Cancel"},
		selected: 1,
		openedAt: m.now(),
		confirm: func() tea.Cmd {
			return func() tea.Msg {
				acc, err := svc.Approve(ctx, name, who)
				if err != nil {
					return actionMsg{err: err}
				}
				return actionMsg{flash: fmt.Sprintf("✓ approved %s for %s; AWS is creating it", acc.Name, acc.Owner)}
			}
		},
	}
	return m, nil
}

func (m model) openDeny() (tea.Model, tea.Cmd) {
	r, ok := m.selected()
	if !ok || r.Account.Status != core.StatusPending {
		return m, m.setFlash("select a pending request to deny", true)
	}
	a := r.Account
	svc, ctx, name, who := m.svc, m.ctx, a.Name, m.opts.Operator
	m.dialog = &dialog{
		title:       "Deny " + a.Name,
		body:        fmt.Sprintf("Deny the request for %s by %s? The requester will be notified.", a.Name, a.RequestedBy),
		buttons:     [2]string{"Deny", "Cancel"},
		selected:    1,
		destructive: true,
		openedAt:    m.now(),
		confirm: func() tea.Cmd {
			return func() tea.Msg {
				if err := svc.Deny(ctx, name, who, "denied in the TUI"); err != nil {
					return actionMsg{err: err}
				}
				return actionMsg{flash: "✓ denied " + name}
			}
		},
	}
	return m, nil
}

func (m model) yank() (tea.Model, tea.Cmd) {
	r, ok := m.selected()
	if !ok || r.Account.ProviderID == "" {
		return m, m.setFlash("no account id to copy", true)
	}
	return m, tea.Batch(tea.SetClipboard(r.Account.ProviderID), m.setFlash("✓ copied "+r.Account.ProviderID, false))
}

func (m model) dialogKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	d := m.dialog
	if m.now().Sub(d.openedAt) < dialogGrace {
		return m, nil
	}
	switch msg.String() {
	case "y":
		m.dialog = nil
		return m, m.startAction(d.confirm())
	case "n", "esc", "q":
		m.dialog = nil
		return m, nil
	case "left", "right", "tab", "shift+tab", "h", "l":
		d.selected = 1 - d.selected
		return m, nil
	case "enter", "space":
		if d.selected == 0 {
			m.dialog = nil
			return m, m.startAction(d.confirm())
		}
		m.dialog = nil
		return m, nil
	}
	return m, nil
}

// startAction runs a confirmed action. The dialog or form is already closed
// so a second confirm cannot fire it twice; the header spins until the
// actionMsg lands.
func (m *model) startAction(cmd tea.Cmd) tea.Cmd {
	m.working = true
	return tea.Batch(m.spinner.Tick, cmd)
}

// ---- view ------------------------------------------------------------------

func (m model) View() tea.View {
	v := tea.NewView(m.content())
	v.AltScreen = true
	v.WindowTitle = "playplace"
	switch {
	case m.form != nil:
		if c := m.form.cursor(); c != nil {
			box := m.formView()
			x, y := overlayOrigin(box, m.width, m.height)
			c.Position.X += x + 3 // border + padding
			c.Position.Y += y + 2 // border + padding
			v.Cursor = c
		}
	case m.filtering && m.dialog == nil && !m.showHelp:
		if c := m.filter.Cursor(); c != nil {
			c.Position.X += m.filterX()
			c.Position.Y = 1
			v.Cursor = c
		}
	}
	return v
}

func (m model) content() string {
	mainH := m.mainHeight()
	var main string
	var hints []key.Binding
	switch {
	case m.showDetail:
		main = m.detailView(m.width, mainH)
		hints = m.keys.detailHelp()
	case m.showPane():
		table := m.tableView(m.width, m.tableHeight())
		pane := m.paneView(m.width, m.paneHeight())
		main = table + "\n" + pane
		hints = m.keys.ShortHelp()
	default:
		main = m.tableView(m.width, mainH)
		hints = m.keys.ShortHelp()
	}

	bottom := " " + m.help.ShortHelpView(hints)

	page := strings.Join([]string{m.headerView(), m.tabsView(), "", main, pad(bottom, m.width)}, "\n")
	page = fitHeight(page, m.height)

	switch {
	case m.form != nil:
		page = overlay(page, m.formView(), m.width, m.height)
	case m.dialog != nil:
		page = overlay(page, m.dialogView(), m.width, m.height)
	case m.showHelp:
		page = overlay(page, m.helpView(), m.width, m.height)
	case m.showNotices:
		page = overlay(page, m.noticesView(), m.width, m.height)
	}
	// Toasts sit on top of everything, in the top right, and age out on
	// their own. The notifications window already shows them, so skip then.
	if box := m.toastView(); box != "" && !m.showNotices {
		x := m.width - lipgloss.Width(box) - 1
		if x < 0 {
			x = 0
		}
		page = lipgloss.NewCompositor(lipgloss.NewLayer(page), lipgloss.NewLayer(box).X(x).Y(0).Z(1)).Render()
	}
	return page
}

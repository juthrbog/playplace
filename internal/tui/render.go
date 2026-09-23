package tui

import (
	"fmt"
	"image/color"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"playplace/internal/core"
)

const (
	tallBreakpoint = 22 // rows needed to show the detail pane under the table
	chromeLines    = 4  // header, tabs, blank, help
)

// ---- primitives ------------------------------------------------------------

// pad truncates or right-pads a styled string to exactly w cells.
func pad(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) > w {
		return ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", w-ansi.StringWidth(s))
}

// padLeft right-aligns s in w cells.
func padLeft(s string, w int) string {
	if ansi.StringWidth(s) >= w {
		return ansi.Truncate(s, w, "…")
	}
	return strings.Repeat(" ", w-ansi.StringWidth(s)) + s
}

// fitHeight pads or trims a block to exactly h lines.
func fitHeight(s string, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// money formats dollars compactly: $412, $4.2k, $1.3m.
func money(v float64) string {
	switch {
	case v >= 1_000_000:
		return fmt.Sprintf("$%.1fm", v/1_000_000)
	case v >= 10_000:
		return fmt.Sprintf("$%.0fk", v/1000)
	case v >= 1000:
		return fmt.Sprintf("$%.1fk", v/1000)
	case v >= 100 || v == math.Trunc(v):
		return fmt.Sprintf("$%.0f", v)
	default:
		return fmt.Sprintf("$%.2f", v)
	}
}

func expiryFact(t, now time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.Local().Format("2006-01-02 15:04") + "  " + expiresIn(t, now)
}

// expiresIn is the human form of time until expiry.
func expiresIn(t, now time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := t.Sub(now)
	switch {
	case d <= -24*time.Hour:
		return fmt.Sprintf("%dd ago", int(-d.Hours()/24))
	case d < 0:
		return "expired"
	case d < time.Hour:
		return fmt.Sprintf("in %dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("in %dh", int(d.Hours()))
	default:
		return fmt.Sprintf("in %dd", int(d.Hours()/24))
	}
}

// bar renders a filled/empty block bar for pct in [0, 1]. base carries any
// row background so the bar stays highlighted on the selected row.
func bar(pct float64, width int, c color.Color, base lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	pct = math.Max(0, math.Min(1, pct))
	filled := int(math.Round(pct * float64(width)))
	st := base.Foreground(c)
	return st.Render(strings.Repeat("█", filled)) + st.Faint(true).Render(strings.Repeat("░", width-filled))
}

var sparks = []rune("▁▂▃▄▅▆▇█")

// sparkline draws the last `width` values scaled to eight block heights.
func sparkline(vals []float64, width int) string {
	if width <= 0 {
		return ""
	}
	if len(vals) > width {
		vals = vals[len(vals)-width:]
	}
	var maxV float64
	for _, v := range vals {
		maxV = math.Max(maxV, v)
	}
	var b strings.Builder
	for _, v := range vals {
		i := 0
		if maxV > 0 {
			i = int(math.Round(v / maxV * float64(len(sparks)-1)))
		}
		b.WriteRune(sparks[i])
	}
	return b.String()
}

// ---- table -----------------------------------------------------------------

type column struct {
	title string
	width int
	right bool
}

// columns picks visible columns for the available width. NAME absorbs the
// remainder; ACCOUNT and OWNER drop first on narrow screens.
func columns(width int) []column {
	fixed := []column{
		{title: "OWNER", width: 12},
		{title: "STATUS", width: 12},
		{title: "ACCOUNT", width: 13},
		{title: "EXPIRES", width: 9},
		{title: "SPEND / BUDGET", width: 26},
	}
	drop := func(title string) {
		for i, c := range fixed {
			if c.title == title {
				fixed = append(fixed[:i], fixed[i+1:]...)
				return
			}
		}
	}
	if width < 96 {
		drop("ACCOUNT")
	}
	if width < 80 {
		drop("OWNER")
	}
	if width < 64 {
		fixed[len(fixed)-1].width = 14 // numbers only, no bar
	}
	used := 1 // cursor bar
	for _, c := range fixed {
		used += c.width + 2
	}
	name := width - used
	if name > 28 {
		name = 28
	}
	if name < 10 {
		name = 10
	}
	return append([]column{{title: "NAME", width: name}}, fixed...)
}

func (m model) tableHeader(cols []column) string {
	var b strings.Builder
	b.WriteString(" ")
	for i, c := range cols {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(m.th.colHeader.Render(pad(c.title, c.width)))
	}
	return b.String()
}

func (m model) tableRow(r row, cols []column, selected bool) string {
	th := m.th
	base := th.cell
	bar := " "
	if selected {
		base = base.Background(th.bgSelected).Bold(true)
		bar = th.cursorBar.Background(th.bgSelected).Render("▌")
	}
	gap := base.Render("  ")

	glyph, label, sc := th.status(r.Account, m.now())
	var b strings.Builder
	b.WriteString(bar)
	for i, c := range cols {
		if i > 0 {
			b.WriteString(gap)
		}
		var cell string
		switch c.title {
		case "NAME":
			cell = base.Render(pad(r.Account.Name, c.width))
		case "OWNER":
			cell = base.Render(pad(r.Account.Owner, c.width))
		case "STATUS":
			cell = base.Foreground(sc).Render(pad(glyph+" "+label, c.width))
		case "ACCOUNT":
			cell = base.Foreground(th.muted).Render(pad(r.Account.ProviderID, c.width))
		case "EXPIRES":
			txt := expiresIn(r.Account.ExpiresAt, m.now())
			if r.Account.ExpiresAt.IsZero() || r.Account.Status == core.StatusFailed {
				txt = "—"
			}
			st := base
			if r.Account.Status == core.StatusClosed {
				st = base.Foreground(th.muted)
			} else if strings.HasSuffix(label, "!") {
				st = base.Foreground(sc)
			}
			cell = st.Render(pad(txt, c.width))
		case "SPEND / BUDGET":
			cell = m.spendCell(r, c.width, base)
		}
		b.WriteString(cell)
	}
	return b.String()
}

// spendCell shows "$412 / $500" with a graded bar when there is room.
func (m model) spendCell(r row, width int, base lipgloss.Style) string {
	if r.Account.BudgetUSD <= 0 {
		return base.Render(pad(money(r.MTD)+" / unknown", width))
	}
	pct := 0.0
	if r.Account.BudgetUSD > 0 {
		pct = r.MTD / r.Account.BudgetUSD
	}
	nums := fmt.Sprintf("%s / %s", money(r.MTD), money(r.Account.BudgetUSD))
	c := m.th.spendColor(pct)
	if width < 20 {
		return base.Foreground(c).Render(pad(nums, width))
	}
	barW := width - 14 - 1
	if barW > 10 {
		barW = 10
	}
	numW := width - barW - 1
	if r.Account.Status == core.StatusClosed {
		c = m.th.muted
	}
	return base.Foreground(c).Render(padLeft(nums, numW)) + base.Render(" ") + bar(pct, barW, c, base)
}

// tableView renders header plus rows for the current window into width x height.
func (m model) tableView(width, height int) string {
	cols := columns(width)
	var lines []string
	lines = append(lines, m.tableHeader(cols))
	body := height - 1
	switch {
	case m.err != nil && len(m.rows) == 0:
		lines = append(lines, "", " "+m.th.errText.Render("Could not load accounts: "+m.err.Error()), " "+m.th.helpDesc.Render("r retry"))
	case m.loading && len(m.rows) == 0:
		lines = append(lines, m.splash(width, body, m.spinner.View()+m.th.fact.Render(" Loading accounts…"))...)
	case len(m.visible) == 0:
		msg := "No accounts in this section."
		if m.filter.Value() != "" {
			msg = "No accounts match the filter. Press / to change it or esc to clear."
		} else if len(m.rows) == 0 {
			msg = "No accounts yet. Press n to request one."
		}
		if len(m.rows) == 0 {
			lines = append(lines, m.splash(width, body, m.th.fact.Render(msg))...)
		} else {
			lines = append(lines, "", " "+m.th.fact.Render(msg))
		}
	default:
		start := m.grid.offset
		end := start + body
		if end > len(m.visible) {
			end = len(m.visible)
		}
		for i := start; i < end; i++ {
			lines = append(lines, m.tableRow(m.visible[i], cols, i == m.grid.cursor))
		}
	}
	out := strings.Join(lines, "\n")
	return fitHeight(pad2D(out, width), height)
}

// splash fills an empty table body with the banner and one line of text
// under it, or just the text when the body is too short for the art.
func (m model) splash(width, height int, text string) []string {
	art := m.banner(width)
	if art == nil || height < len(art)+3 {
		return []string{"", " " + text}
	}
	top := (height - len(art) - 2) / 3
	var out []string
	for i := 0; i < top; i++ {
		out = append(out, "")
	}
	out = append(out, art...)
	out = append(out, "")
	left := (width - ansi.StringWidth(text)) / 2
	if left < 1 {
		left = 1
	}
	return append(out, strings.Repeat(" ", left)+text)
}

// pad2D pads every line to width so JoinHorizontal keeps columns aligned.
func pad2D(s string, width int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = pad(l, width)
	}
	return strings.Join(lines, "\n")
}

// ---- header and tabs -------------------------------------------------------

func (m model) headerView() string {
	th := m.th
	open, soon := 0, 0
	var mtd float64
	for _, r := range m.rows {
		if r.Account.Status == core.StatusClosed {
			continue
		}
		open++
		mtd += r.MTD
		if r.Account.Status.IsOpen() && !r.Account.ExpiresAt.IsZero() && r.Account.ExpiresAt.Sub(m.now()) <= 7*24*time.Hour {
			soon++
		}
	}
	pending := 0
	for _, r := range m.rows {
		if r.Account.Status == core.StatusPending {
			pending++
		}
	}
	balls := th.bannerBalls()
	mark := lipgloss.NewStyle().Foreground(balls[0]).Render("●") + lipgloss.NewStyle().Foreground(balls[1]).Render("●") + lipgloss.NewStyle().Foreground(balls[2]).Render("●")
	left := " " + mark + " " + th.title.Render("playplace") + " "
	factText := fmt.Sprintf("%d open · %d expiring ≤7d · %s MTD", open-pending, soon, money(mtd))
	if pending > 0 {
		factText = fmt.Sprintf("%d pending · ", pending) + factText
	}
	facts := th.fact.Render(factText)
	right := th.right.Render("aws: "+m.opts.Context) + " "
	if m.unread > 0 {
		right = lipgloss.NewStyle().Foreground(th.warning).Render(fmt.Sprintf("%d new · N", m.unread)) + "  " + right
	}
	if m.syncing {
		right = m.spinner.View() + th.right.Render(" syncing") + "  " + right
	}
	if m.working {
		right = m.spinner.View() + th.right.Render(" working") + "  " + right
	}
	if m.stale {
		right = lipgloss.NewStyle().Foreground(th.warning).Render("stale · r reload") + "  " + right
	}
	if m.filter.Value() != "" {
		right = th.right.Render(fmt.Sprintf("%d of %d shown", len(m.visible), m.sectionCount(m.section))) + "  " + right
	}
	fillW := m.width - ansi.StringWidth(left) - ansi.StringWidth(facts) - ansi.StringWidth(right) - 2
	if fillW < 2 {
		return pad(left+facts, m.width)
	}
	return left + th.fill.Render(strings.Repeat("╱", fillW)) + " " + facts + " " + right
}

var sectionNames = [...]string{"Open", "Active", "Expiring", "Closed"}

func (m model) tabsView() string {
	th := m.th
	var b strings.Builder
	b.WriteString(" ")
	for i, name := range sectionNames {
		st := th.tabInactive
		if section(i) == m.section {
			st = th.tabActive
		}
		if i > 0 {
			b.WriteString("   ")
		}
		b.WriteString(st.Render(name) + " " + th.tabCount.Render(fmt.Sprint(m.sectionCount(section(i)))))
	}
	left := b.String()

	var right string
	switch {
	case m.filtering:
		right = m.filter.View()
	case m.filter.Value() != "":
		right = th.fact.Render("filter: ") + th.cell.Render(m.filter.Value()) + th.tabCount.Render("  esc clear")
	default:
		right = th.tabCount.Render("/ filter")
	}
	gapW := m.width - ansi.StringWidth(left) - ansi.StringWidth(right) - 1
	if gapW < 1 {
		return pad(left, m.width)
	}
	return left + strings.Repeat(" ", gapW) + right
}

// filterX is the column where the filter input starts, for cursor placement.
func (m model) filterX() int {
	return m.width - 1 - ansi.StringWidth(m.filter.View())
}

// ---- detail ----------------------------------------------------------------

// detailBlock is the key/value summary shared by the pane and the full screen.
func (m model) detailBlock(r row, width int) []string {
	th := m.th
	a := r.Account
	glyph, label, sc := th.status(a, m.now())
	kv := func(k, v string) string {
		return th.label.Render(pad(k, 9)) + th.value.Render(v)
	}
	lines := []string{
		th.paneTitle.Render(a.Name),
		lipgloss.NewStyle().Foreground(sc).Render(glyph + " " + label),
		"",
		kv("owner", a.Owner),
	}
	switch {
	case a.ProviderID != "":
		lines = append(lines, kv("account", a.ProviderID))
	case a.Status == core.StatusPending:
		lines = append(lines, kv("asked by", a.RequestedBy))
	default:
		lines = append(lines, kv("request", a.RequestID))
	}
	if a.Purpose != "" {
		lines = append(lines, kv("purpose", ansi.Truncate(a.Purpose, width-9, "…")))
	}
	if a.ApprovedBy != "" {
		lines = append(lines, kv("approved", a.ApprovedBy))
	}
	if a.Email != "" {
		lines = append(lines, kv("email", ansi.Truncate(a.Email, width-9, "…")))
	}
	if a.ProviderID != "" {
		lines = append(lines, kv("expires", expiryFact(a.ExpiresAt, m.now())))
	}
	lines = append(lines, m.spendLine(r, width))
	if len(r.Series) > 0 {
		sparkW := width - 9 - 10
		if sparkW > 30 {
			sparkW = 30
		}
		var total float64
		for _, v := range r.Series {
			total += v
		}
		if sparkW >= 5 {
			lines = append(lines, th.label.Render(pad("30d", 9))+lipgloss.NewStyle().Foreground(th.primary).Render(sparkline(r.Series, sparkW))+"  "+th.value.Render(money(total)))
		}
	}
	lines = append(lines, m.lifecycleLines(a, width)...)
	return lines
}

// spendLine is "spend  $x / $y  NN%  bar".
func (m model) spendLine(r row, width int) string {
	th := m.th
	a := r.Account
	if a.BudgetUSD <= 0 {
		return th.label.Render(pad("spend", 9)) + th.value.Render(money(r.MTD)+" / unknown")
	}
	pct := 0.0
	if a.BudgetUSD > 0 {
		pct = r.MTD / a.BudgetUSD
	}
	barW := width - 9 - 22
	if barW > 16 {
		barW = 16
	}
	spend := fmt.Sprintf("%s / %s  %3.0f%%", money(r.MTD), money(a.BudgetUSD), pct*100)
	line := th.label.Render(pad("spend", 9)) + lipgloss.NewStyle().Foreground(th.spendColor(pct)).Render(spend)
	if barW >= 4 {
		line += "  " + bar(pct, barW, th.spendColor(pct), lipgloss.NewStyle())
	}
	return line
}

// lifecycleLines shows the tag-derived facts that explain the status.
func (m model) lifecycleLines(a *core.Account, width int) []string {
	th := m.th
	kv := func(k, v string, st lipgloss.Style) string { return th.label.Render(pad(k, 9)) + st.Render(v) }
	var out []string
	if a.WarnedAt != nil {
		out = append(out, kv("warned", a.WarnedAt.Local().Format("2006-01-02 15:04"), lipgloss.NewStyle().Foreground(th.warning)))
	}
	if a.CloseRequestedAt != nil {
		out = append(out, kv("closing", "requested "+a.CloseRequestedAt.Local().Format("2006-01-02 15:04")+", waiting on AWS quota", lipgloss.NewStyle().Foreground(th.busy)))
	}
	if a.LastError != "" {
		out = append(out, kv("failure", ansi.Truncate(a.LastError, width-9, "…"), th.errText))
	}
	for _, tag := range slices.Sorted(maps.Keys(a.TagErrors)) {
		out = append(out, kv("repair", ansi.Truncate(tag+": "+a.TagErrors[tag], width-9, "…"), th.errText))
	}
	if a.RepairError() != nil {
		out = append(out, kv("action", "operator tag repair required", th.errText))
	}
	if !a.Managed && a.ProviderID != "" {
		out = append(out, kv("note", "untagged; the next sync adopts it", th.fact))
	}
	return out
}

// paneView is the summary strip under the table: a titled rule, then account
// facts on the left and spend plus recent daily costs on the right.
func (m model) paneView(width, height int) string {
	th := m.th
	r, ok := m.selected()
	if !ok {
		rule := ""
		if width > 2 {
			rule = strings.Repeat("─", width-2)
		}
		return fitHeight(pad2D(" "+th.paneBar.Render(rule), width), height)
	}
	a := r.Account
	glyph, label, sc := th.status(a, m.now())

	title := th.paneBar.Render("── ") + th.paneTitle.Render(a.Name) + " " +
		lipgloss.NewStyle().Foreground(sc).Render(glyph+" "+label) + " "
	rest := width - 1 - ansi.StringWidth(title)
	if rest > 0 {
		title += th.paneBar.Render(strings.Repeat("─", rest))
	}

	kv := func(k, v string) string { return th.label.Render(pad(k, 9)) + th.value.Render(v) }
	leftW := 44
	if leftW > width/2 {
		leftW = width / 2
	}
	left := []string{kv("owner", a.Owner)}
	switch {
	case a.ProviderID != "":
		left = append(left, kv("account", a.ProviderID))
	case a.Status == core.StatusPending:
		left = append(left, kv("asked by", a.RequestedBy))
	default:
		left = append(left, kv("request", a.RequestID))
	}
	if a.Purpose != "" {
		left = append(left, kv("purpose", ansi.Truncate(a.Purpose, leftW-9, "…")))
	}
	if a.Email != "" {
		left = append(left, kv("email", ansi.Truncate(a.Email, leftW-9, "…")))
	}
	if a.ProviderID != "" {
		left = append(left, kv("expires", expiryFact(a.ExpiresAt, m.now())))
	}
	left = append(left, m.lifecycleLines(a, leftW)...)

	rightW := width - 1 - leftW - 2
	right := []string{m.spendLine(r, rightW)}
	if len(r.Series) > 0 {
		sparkW := rightW - 9 - 10
		if sparkW > 30 {
			sparkW = 30
		}
		var total float64
		for _, v := range r.Series {
			total += v
		}
		if sparkW >= 5 {
			right = append(right, th.label.Render(pad("30d", 9))+lipgloss.NewStyle().Foreground(th.primary).Render(sparkline(r.Series, sparkW))+"  "+th.value.Render(money(total)))
		}
	}
	room := height - 1 - len(right)
	if state, ok := m.hist[historyKey(a)]; m.opts.History != nil && ok {
		if status := state.status(); status != "" {
			right = append(right, th.label.Render(pad("history", 9))+th.fact.Render(status))
			room--
		}
		for i, e := range state.page.Events {
			if room <= 0 {
				break
			}
			lbl := "history"
			if i > 0 {
				lbl = ""
			}
			right = append(right, th.label.Render(pad(lbl, 9))+m.historyLine(e, rightW-9))
			room--
		}
	} else {
		for i := len(r.Points) - 1; i >= 0 && room > 0; i-- {
			p := r.Points[i]
			lbl := "daily"
			if i != len(r.Points)-1 {
				lbl = ""
			}
			right = append(right, th.label.Render(pad(lbl, 9))+th.fact.Render(p.Date.Format("01-02"))+"  "+th.value.Render(fmt.Sprintf("$%.2f", p.AmountUSD)))
			room--
		}
	}

	body := lipgloss.JoinHorizontal(lipgloss.Top,
		pad2D(fitHeight(strings.Join(left, "\n"), height-1), leftW),
		"  ",
		pad2D(fitHeight(strings.Join(right, "\n"), height-1), rightW),
	)
	var out []string
	out = append(out, " "+title)
	for _, l := range strings.Split(body, "\n") {
		out = append(out, " "+l)
	}
	return fitHeight(pad2D(strings.Join(out, "\n"), width), height)
}

// historyLine renders one event compactly: time, event, message.
func (m model) historyLine(e core.AuditEvent, width int) string {
	th := m.th
	line := th.fact.Render(e.At.Local().Format("01-02 15:04")) + " " + th.value.Render(e.Event) + " " + th.fact.Render(e.Message)
	return ansi.Truncate(line, width, "…")
}

// detailView is the full-screen detail: the summary block, then daily spend
// on the left and history on the right when a sink is configured.
func (m model) detailView(width, height int) string {
	r, ok := m.selected()
	if !ok {
		return fitHeight(" "+m.th.fact.Render("Nothing selected."), height)
	}
	th := m.th
	lines := m.detailBlock(r, width-2)
	for i := range lines {
		lines[i] = " " + lines[i]
	}
	head := strings.Join(lines, "\n")
	bodyH := height - len(lines) - 1
	if bodyH < 3 {
		bodyH = 3
	}

	// Left: daily spend, newest first.
	costs := []string{th.label.Render("daily spend")}
	if len(r.Points) == 0 {
		costs = append(costs, th.fact.Render("no cost data yet"))
	}
	for i := len(r.Points) - 1; i >= 0 && len(costs) < bodyH; i-- {
		p := r.Points[i]
		costs = append(costs, th.fact.Render(p.Date.Format("01-02"))+"  "+th.value.Render(padLeft(fmt.Sprintf("$%.2f", p.AmountUSD), 9)))
	}
	costW := 22
	left := pad2D(fitHeight(strings.Join(costs, "\n"), bodyH), costW)

	if m.opts.History == nil {
		return fitHeight(head+"\n\n"+lipgloss.JoinHorizontal(lipgloss.Top, " ", left), height)
	}

	// Right: history, newest first.
	histW := width - 2 - costW - 3
	hist := []string{th.label.Render("history · ↑/↓ scroll · pgdn older · r reload")}
	state, loaded := m.hist[historyKey(r.Account)]
	if !loaded {
		hist = append(hist, th.fact.Render("loading…"))
	} else if status := state.status(); status != "" {
		hist = append(hist, th.fact.Render(status))
	}
	for _, e := range state.page.Events[min(state.offset, len(state.page.Events)):] {
		if len(hist) >= bodyH {
			break
		}
		hist = append(hist, m.historyLine(e, histW))
	}
	right := pad2D(fitHeight(strings.Join(hist, "\n"), bodyH), histW)
	return fitHeight(head+"\n\n"+lipgloss.JoinHorizontal(lipgloss.Top, " ", left, "   ", right), height)
}

// ---- overlays --------------------------------------------------------------

func (m model) dialogView() string {
	th := m.th
	d := m.dialog
	w := m.width - 8
	if w > 64 {
		w = 64
	}
	if w < 30 {
		w = 30
	}
	inner := w - 6
	body := lipgloss.NewStyle().Width(inner).Foreground(th.fg).Render(d.body)

	var btns []string
	for i, label := range d.buttons {
		st := th.button
		if i == d.selected {
			st = th.buttonFocus
			if d.destructive && i == 0 {
				st = th.buttonDanger
			}
		}
		btns = append(btns, st.Render(label))
	}
	row := lipgloss.JoinHorizontal(lipgloss.Top, btns[0], "  ", btns[1])
	hint := th.dialogHint.Render("y confirm · n cancel · ←/→ tab · enter · esc")

	box := th.dialogBox
	if d.destructive {
		box = box.BorderForeground(th.danger)
	}
	return box.Width(w).Render(strings.Join([]string{
		th.dialogTitle.Render(d.title), "", body, "", row, "", hint,
	}, "\n"))
}

func (m model) helpView() string {
	th := m.th
	h := m.help
	h.Styles.FullKey = th.helpKey
	h.Styles.FullDesc = th.helpDesc
	h.Styles.FullSeparator = th.dialogHint
	keys := h.FullHelpView(m.keys.FullHelp())
	head := th.dialogTitle.Render("Keys")
	if art := m.banner(lipgloss.Width(keys)); art != nil && m.height >= len(art)+16 {
		head = strings.Join(art, "\n") + "\n\n" + head
	}
	content := head + "\n\n" + keys + "\n\n" + th.dialogHint.Render("any key to close")
	return th.helpBox.Render(content)
}

// overlayOrigin is the top-left cell where a centered box lands.
func overlayOrigin(box string, width, height int) (x, y int) {
	bw, bh := lipgloss.Width(box), lipgloss.Height(box)
	x = (width - bw) / 2
	y = (height - bh) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return x, y
}

// overlay centers box on top of page using the Lip Gloss compositor.
func overlay(page, box string, width, height int) string {
	x, y := overlayOrigin(box, width, height)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(page),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	).Render()
}

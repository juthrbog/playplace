package tui

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Notices are everything the TUI wants to tell the operator: the outcome of
// an action, and warnings and errors the service logs while it works. They
// show as short toasts in the top right for a few seconds and stay in a
// list behind N. Nothing is ever written to the terminal outside the frame;
// a logger that printed to stderr would draw over the dashboard.

type noticeLevel int

const (
	noticeInfo noticeLevel = iota
	noticeSuccess
	noticeWarn
	noticeError
)

type notice struct {
	at    time.Time
	level noticeLevel
	text  string
}

const (
	toastTTL   = 6 * time.Second
	toastMax   = 3 // notices shown at once
	toastLines = 3 // lines each may take
	noticeKeep = 200
)

// Feed turns slog records into notices. Hand slog.New(feed.Handler()) to the
// service before starting the TUI. Records below Warn are dropped; the CLI's
// --log-level does not apply inside the TUI, where Info would only be noise.
type Feed struct {
	ch chan notice
}

// NewFeed makes a feed with room for a burst of records.
func NewFeed() *Feed { return &Feed{ch: make(chan notice, 256)} }

// Handler returns a slog.Handler writing into the feed.
func (f *Feed) Handler() slog.Handler { return feedHandler{feed: f} }

// push queues a notice without blocking; a full feed drops the oldest.
func (f *Feed) push(n notice) {
	select {
	case f.ch <- n:
	default:
		select {
		case <-f.ch:
		default:
		}
		f.ch <- n
	}
}

type feedHandler struct {
	feed  *Feed
	attrs []slog.Attr
	group string
}

func (h feedHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelWarn }

func (h feedHandler) Handle(_ context.Context, r slog.Record) error {
	var parts []string
	add := func(a slog.Attr) {
		if a.Key == "" {
			return
		}
		key := a.Key
		if h.group != "" {
			key = h.group + "." + key
		}
		parts = append(parts, key+"="+a.Value.String())
	}
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(func(a slog.Attr) bool { add(a); return true })
	text := r.Message
	if len(parts) > 0 {
		text += ": " + strings.Join(parts, ", ")
	}
	lvl := noticeWarn
	if r.Level >= slog.LevelError {
		lvl = noticeError
	}
	at := r.Time
	if at.IsZero() {
		at = time.Now()
	}
	h.feed.push(notice{at: at, level: lvl, text: text})
	return nil
}

func (h feedHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return h
}

func (h feedHandler) WithGroup(name string) slog.Handler {
	if name != "" {
		if h.group != "" {
			name = h.group + "." + name
		}
		h.group = name
	}
	return h
}

// ---- model side ------------------------------------------------------------

type (
	noticeMsg     notice
	toastAgingMsg struct{}
)

// waitNotice blocks on the feed and delivers the next record as a message.
func (m model) waitNotice() tea.Cmd {
	if m.feed == nil {
		return nil
	}
	ch := m.feed.ch
	return func() tea.Msg {
		n, ok := <-ch
		if !ok {
			return nil
		}
		return noticeMsg(n)
	}
}

// notify records a notice and schedules a redraw for when its toast ages out.
func (m *model) notify(level noticeLevel, text string) tea.Cmd {
	m.notices = append(m.notices, notice{at: m.now(), level: level, text: strings.TrimSpace(text)})
	if len(m.notices) > noticeKeep {
		m.notices = m.notices[len(m.notices)-noticeKeep:]
	}
	if !m.showNotices {
		m.unread++
	}
	return tea.Tick(toastTTL+100*time.Millisecond, func(time.Time) tea.Msg { return toastAgingMsg{} })
}

// setFlash keeps the old name for action outcomes: success or error.
func (m *model) setFlash(text string, isErr bool) tea.Cmd {
	if isErr {
		return m.notify(noticeError, text)
	}
	return m.notify(noticeSuccess, text)
}

// toasts are the notices still young enough to show in the corner.
func (m model) toasts() []notice {
	var out []notice
	for i := len(m.notices) - 1; i >= 0 && len(out) < toastMax; i-- {
		n := m.notices[i]
		if m.now().Sub(n.at) < toastTTL {
			out = append(out, n)
		}
	}
	return out
}

func (m model) noticeGlyph(l noticeLevel) (string, lipgloss.Style) {
	th := m.th
	switch l {
	case noticeSuccess:
		return "✓", lipgloss.NewStyle().Foreground(th.success)
	case noticeWarn:
		return "!", lipgloss.NewStyle().Foreground(th.warning)
	case noticeError:
		return "×", lipgloss.NewStyle().Foreground(th.danger)
	}
	return "·", lipgloss.NewStyle().Foreground(th.muted)
}

// toastView renders the corner box, or "" when nothing is fresh.
func (m model) toastView() string {
	ts := m.toasts()
	if len(ts) == 0 {
		return ""
	}
	th := m.th
	w := 48
	if w > m.width-4 {
		w = m.width - 4
	}
	if w < 20 {
		return ""
	}
	inner := w - 4 // border and one cell of padding each side
	worst := noticeInfo
	var lines []string
	for _, n := range ts {
		if n.level > worst {
			worst = n.level
		}
		g, st := m.noticeGlyph(n.level)
		// Wrap to a few lines so an error is readable; the full text is
		// always in the notifications window. Lines are padded by hand so
		// the box never re-wraps them.
		wrapped := wrapLines(n.text, inner-2, toastLines)
		for i, l := range wrapped {
			if i == 0 {
				lines = append(lines, pad(st.Render(g)+" "+th.value.Render(l), inner))
			} else {
				lines = append(lines, pad("  "+th.value.Render(l), inner))
			}
		}
	}
	if m.unread > 0 {
		lines = append(lines, pad(th.dialogHint.Render("N notifications"), inner))
	}
	_, border := m.noticeGlyph(worst)
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border.GetForeground()).Padding(0, 1)
	return box.Render(strings.Join(lines, "\n"))
}

// wrapLines word-wraps text to width and keeps at most max lines, marking a
// cut with an ellipsis. Zero max means no limit.
func wrapLines(text string, width, max int) []string {
	text = strings.Join(strings.Fields(text), " ")
	out := strings.Split(ansi.Wrap(text, width, ""), "\n")
	if max > 0 && len(out) > max {
		out = out[:max]
		last := out[max-1]
		if ansi.StringWidth(last) >= width {
			last = ansi.Truncate(last, width-1, "")
		}
		out[max-1] = strings.TrimRight(last, " ") + "…"
	}
	return out
}

// noticesView is the full list behind N, newest first, scrollable.
func (m model) noticesView() string {
	th := m.th
	w := m.width - 6
	if w > 96 {
		w = 96
	}
	if w < 30 {
		w = 30
	}
	inner := w - 6
	h := m.height - 6
	if h < 6 {
		h = 6
	}
	bodyH := h - 5 // title, blank, body, blank, hint
	var lines []string
	if len(m.notices) == 0 {
		lines = append(lines, th.fact.Render("Nothing yet. Action results and service warnings collect here."))
	}
	for i := len(m.notices) - 1; i >= 0; i-- {
		n := m.notices[i]
		g, st := m.noticeGlyph(n.level)
		head := th.fact.Render(n.at.Local().Format("15:04:05")) + " " + st.Render(g) + " "
		for j, l := range wrapLines(n.text, inner-11, 0) {
			if j == 0 {
				lines = append(lines, pad(head+th.value.Render(l), inner))
			} else {
				lines = append(lines, pad(strings.Repeat(" ", 11)+th.value.Render(l), inner))
			}
		}
	}
	maxScroll := len(lines) - bodyH
	if maxScroll < 0 {
		maxScroll = 0
	}
	scroll := m.noticeScroll
	if scroll > maxScroll {
		scroll = maxScroll
	}
	end := scroll + bodyH
	if end > len(lines) {
		end = len(lines)
	}
	shown := lines[scroll:end]
	for len(shown) < bodyH {
		shown = append(shown, "")
	}
	title := th.dialogTitle.Render("Notifications") + th.tabCount.Render(fmt.Sprintf("  %d", len(m.notices)))
	if maxScroll > 0 {
		title += th.tabCount.Render(fmt.Sprintf("  (%d-%d)", scroll+1, end))
	}
	hint := th.dialogHint.Render("↑/↓ scroll · c clear · esc close")
	all := append(append([]string{pad(title, inner), ""}, shown...), "", pad(hint, inner))
	return th.helpBox.Render(strings.Join(all, "\n"))
}

// noticesKey handles keys while the notifications window is open.
func (m model) noticesKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	switch {
	case pressed(msg, "esc", "q"), key.Matches(msg, k.Notices):
		m.showNotices = false
	case pressed(msg, "up", "k"):
		if m.noticeScroll > 0 {
			m.noticeScroll--
		}
	case pressed(msg, "down", "j"):
		m.noticeScroll++
	case pressed(msg, "pgup", "ctrl+u"):
		m.noticeScroll -= 10
		if m.noticeScroll < 0 {
			m.noticeScroll = 0
		}
	case pressed(msg, "pgdown", "ctrl+d"):
		m.noticeScroll += 10
	case pressed(msg, "g", "home"):
		m.noticeScroll = 0
	case pressed(msg, "G", "end"):
		m.noticeScroll = 1 << 20 // clamped when rendered
	case pressed(msg, "c"):
		m.notices, m.unread, m.noticeScroll = nil, 0, 0
	}
	return m, nil
}

func pressed(msg tea.KeyPressMsg, names ...string) bool {
	s := msg.String()
	for _, n := range names {
		if s == n {
			return true
		}
	}
	return false
}

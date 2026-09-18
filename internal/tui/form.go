package tui

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"playplace/internal/core"
)

// field is one labelled text input in a form.
type field struct {
	label string
	input textinput.Model
}

// form is a small modal with labelled inputs. Enter submits, tab moves,
// esc cancels. submit validates and returns the command to run.
type form struct {
	title, desc string
	fields      []field
	focus       int
	errMsg      string
	openedAt    time.Time
	submit      func(values []string) (tea.Cmd, error)
}

const formLabelWidth = 10

func newField(th theme, label, placeholder, value string, width int) field {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = placeholder
	in.CharLimit = 80
	in.SetWidth(width)
	in.SetValue(value)
	st := textinput.DefaultStyles(th.isDark)
	st.Focused.Text = lipgloss.NewStyle().Foreground(th.fg)
	st.Focused.Placeholder = lipgloss.NewStyle().Foreground(th.faint)
	st.Blurred.Text = lipgloss.NewStyle().Foreground(th.muted)
	st.Blurred.Placeholder = lipgloss.NewStyle().Foreground(th.faint)
	in.SetStyles(st)
	return field{label: label, input: in}
}

func (f *form) values() []string {
	out := make([]string, len(f.fields))
	for i, fl := range f.fields {
		out[i] = strings.TrimSpace(fl.input.Value())
	}
	return out
}

func (f *form) setFocus(i int) tea.Cmd {
	if i < 0 {
		i = len(f.fields) - 1
	}
	if i >= len(f.fields) {
		i = 0
	}
	for j := range f.fields {
		f.fields[j].input.Blur()
	}
	f.focus = i
	return f.fields[i].input.Focus()
}

// update handles a key. It returns done=true when the form should close.
func (f *form) update(msg tea.KeyPressMsg, now time.Time) (cmd tea.Cmd, done bool) {
	if now.Sub(f.openedAt) < dialogGrace {
		return nil, false
	}
	switch msg.String() {
	case "esc":
		return nil, true
	case "tab", "down":
		return f.setFocus(f.focus + 1), false
	case "shift+tab", "up":
		return f.setFocus(f.focus - 1), false
	case "enter":
		cmd, err := f.submit(f.values())
		if err != nil {
			f.errMsg = err.Error()
			return nil, false
		}
		return cmd, true
	}
	f.errMsg = ""
	var c tea.Cmd
	f.fields[f.focus].input, c = f.fields[f.focus].input.Update(msg)
	return c, false
}

// cursor returns the focused input's cursor, relative to the form body.
func (f *form) cursor() *tea.Cursor {
	c := f.fields[f.focus].input.Cursor()
	if c == nil {
		return nil
	}
	c.Position.X += formLabelWidth
	c.Position.Y = f.bodyOffset() + f.focus
	return c
}

// bodyOffset is the number of lines above the first field inside the box.
func (f *form) bodyOffset() int {
	n := 2 // title, blank
	if f.desc != "" {
		n += 2
	}
	return n
}

func (m model) formView() string {
	th := m.th
	f := m.form
	w := m.width - 8
	if w > 64 {
		w = 64
	}
	if w < 34 {
		w = 34
	}
	var lines []string
	lines = append(lines, th.dialogTitle.Render(f.title), "")
	if f.desc != "" {
		lines = append(lines, lipgloss.NewStyle().Width(w-6).Foreground(th.muted).Render(f.desc), "")
	}
	for i, fl := range f.fields {
		lbl := th.label
		if i == f.focus {
			lbl = th.value
		}
		lines = append(lines, lbl.Render(pad(fl.label, formLabelWidth))+fl.input.View())
	}
	lines = append(lines, "")
	if f.errMsg != "" {
		lines = append(lines, th.errText.Render(f.errMsg))
	} else {
		lines = append(lines, th.dialogHint.Render("tab next field · enter submit · esc cancel"))
	}
	return th.dialogBox.Width(w).Render(strings.Join(lines, "\n"))
}

// maxSpanDays caps a span so the duration cannot overflow; 100 years is far
// past any ceiling the service allows.
const maxSpanDays = 36500

// parseSpan accepts a number of days, a day count like 7d, a Go duration
// like 72h, or a YYYY-MM-DD date. For a date it returns the time; otherwise it
// returns from plus the span. With a zero from the caller wants a lifetime,
// not a deadline, so a date is refused there.
func parseSpan(s string, from time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		if from.IsZero() {
			return time.Time{}, errors.New("enter a number of days")
		}
		return time.Time{}, errors.New("enter a number of days or a date like 2026-10-01")
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		if from.IsZero() {
			return time.Time{}, errors.New("enter a number of days, not a date")
		}
		return t, nil
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		d, err := days(n)
		if err != nil {
			return time.Time{}, err
		}
		return from.Add(d), nil
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("bad span %q", s)
		}
		d, err := days(n)
		if err != nil {
			return time.Time{}, err
		}
		return from.Add(d), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		if from.IsZero() {
			return time.Time{}, fmt.Errorf("bad value %q, use a number of days", s)
		}
		return time.Time{}, fmt.Errorf("bad value %q, use a number of days or YYYY-MM-DD", s)
	}
	if d <= 0 {
		return time.Time{}, errors.New("duration must be positive")
	}
	if d > maxSpanDays*24*time.Hour {
		return time.Time{}, fmt.Errorf("duration must be at most %d days", maxSpanDays)
	}
	return from.Add(d), nil
}

// days turns a day count into a duration, refusing anything that is not a
// positive finite number within maxSpanDays.
func days(n float64) (time.Duration, error) {
	if math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 {
		return 0, errors.New("days must be a positive number")
	}
	if n > maxSpanDays {
		return 0, fmt.Errorf("days must be at most %d", maxSpanDays)
	}
	return time.Duration(n * float64(24*time.Hour)), nil
}

// ---- concrete forms --------------------------------------------------------

func (m model) newCreateForm() *form {
	th := m.th
	cfg := m.svc.Config()
	svc, ctx, operator := m.svc, m.ctx, m.opts.Operator
	f := &form{
		title:    "Request a playground account",
		desc:     "The request waits in the queue until an approver acts on it (a to approve, d to deny).",
		openedAt: m.now(),
		fields: []field{
			newField(th, "name", "dev-alex", "", 30),
			newField(th, "owner", "engineer email or user name", "", 30),
			newField(th, "days", fmt.Sprintf("%d (max %d)", int(cfg.DefaultTTL.Hours()/24), int(cfg.MaxTTL.Hours()/24)), "", 16),
			newField(th, "budget $", fmt.Sprintf("%.0f (max %.0f)", cfg.DefaultBudgetUSD, cfg.MaxBudgetUSD), "", 16),
			newField(th, "purpose", "what it is for", "", 30),
			newField(th, "override", "y to go past the ceilings", "", 30),
		},
	}
	f.fields[4].input.CharLimit = core.MaxPurpose
	f.submit = func(v []string) (tea.Cmd, error) {
		// The operator is the requester even when asking for someone else,
		// so the self-approval rule and the history name who acted.
		in := core.RequestInput{Name: v[0], Owner: v[1], RequestedBy: operator, OverrideLimits: yes(v[5])}
		if err := core.ValidateName(in.Name); err != nil {
			return nil, err
		}
		if in.Owner == "" {
			return nil, errors.New("owner is required")
		}
		if v[2] != "" {
			t, err := parseSpan(v[2], time.Time{})
			if err != nil {
				return nil, fmt.Errorf("ttl: %w", err)
			}
			in.TTL = t.Sub(time.Time{})
		}
		if v[3] != "" {
			b, err := strconv.ParseFloat(v[3], 64)
			if err != nil || b <= 0 {
				return nil, errors.New("budget must be a positive number")
			}
			in.BudgetUSD = b
		}
		in.Purpose = v[4]
		return func() tea.Msg {
			q, err := svc.SubmitRequest(ctx, in, "tui")
			if err != nil {
				return actionMsg{err: err}
			}
			return actionMsg{flash: fmt.Sprintf("✓ queued %s for %s; waiting for an approver", q.Name, q.Owner)}
		}, nil
	}
	f.setFocus(0)
	return f
}

// yes reads a y/n form field.
func yes(v string) bool { return strings.EqualFold(v, "y") || strings.EqualFold(v, "yes") }

// newEditForm changes a pending request before approval, like the Edit
// button in the web UI. Every field is sent; the service ignores what did
// not change.
func (m model) newEditForm(a *core.Account) *form {
	th := m.th
	svc, ctx, operator, name := m.svc, m.ctx, m.opts.Operator, a.Name
	override := ""
	if a.OverrideLimits {
		override = "y"
	}
	f := &form{
		title:    "Edit request " + a.Name,
		desc:     fmt.Sprintf("Asked for by %s. The requester is told what changed.", a.RequestedBy),
		openedAt: m.now(),
		fields: []field{
			newField(th, "owner", "engineer email or user name", a.Owner, 30),
			newField(th, "days", "lifetime in days", strconv.Itoa(int(a.TTL.Hours()/24)), 16),
			newField(th, "budget $", "monthly budget", strconv.FormatFloat(a.BudgetUSD, 'f', -1, 64), 16),
			newField(th, "purpose", "what it is for", a.Purpose, 30),
			newField(th, "override", "y to allow values past the ceilings", override, 30),
		},
	}
	f.fields[3].input.CharLimit = core.MaxPurpose
	f.submit = func(v []string) (tea.Cmd, error) {
		e := core.RequestEdit{Owner: v[0]}
		if v[1] != "" {
			t, err := parseSpan(v[1], time.Time{})
			if err != nil {
				return nil, fmt.Errorf("days: %w", err)
			}
			e.TTL = t.Sub(time.Time{})
		}
		if v[2] != "" {
			b, err := strconv.ParseFloat(v[2], 64)
			if err != nil || b <= 0 {
				return nil, errors.New("budget must be a positive number")
			}
			e.BudgetUSD = b
		}
		purpose, ov := v[3], yes(v[4])
		e.Purpose, e.OverrideLimits = &purpose, &ov
		return func() tea.Msg {
			q, err := svc.UpdateRequest(ctx, name, operator, e)
			if err != nil {
				return actionMsg{err: err}
			}
			return actionMsg{flash: fmt.Sprintf("✓ %s: %s, %dd, %s/month", q.Name, q.Owner, int(q.TTL.Hours()/24), money(q.BudgetUSD))}
		}, nil
	}
	f.setFocus(0)
	return f
}

func (m model) newExtendForm(a *core.Account) *form {
	th := m.th
	cfg := m.svc.Config()
	svc, ctx, operator := m.svc, m.ctx, m.opts.Operator
	id, name, from := a.ID, a.Name, a.ExpiresAt
	f := &form{
		title:    "Extend " + a.Name,
		desc:     fmt.Sprintf("Expires %s (%s). Enter days to add or a new date. Lifetime is capped at %dd from creation unless overridden.", a.ExpiresAt.Local().Format("Jan 2, 2006"), expiresIn(a.ExpiresAt, m.now()), int(cfg.MaxTTL.Hours()/24)),
		openedAt: m.now(),
		fields: []field{
			newField(th, "days", "7, or a date like 2026-10-01", "7", 24),
			newField(th, "override", "y to go past the lifetime ceiling", "", 24),
		},
	}
	f.submit = func(v []string) (tea.Cmd, error) {
		until, err := parseSpan(v[0], from)
		if err != nil {
			return nil, err
		}
		if !until.After(from) {
			return nil, fmt.Errorf("%s is not after the current expiry", until.Format("2006-01-02"))
		}
		override := yes(v[1])
		return func() tea.Msg {
			_, err := svc.Extend(ctx, id, until, operator, override)
			return actionMsg{flash: fmt.Sprintf("✓ %s now expires %s", name, until.Local().Format("Jan 2")), err: err}
		}, nil
	}
	f.setFocus(0)
	return f
}

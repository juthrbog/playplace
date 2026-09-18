package tui

import "charm.land/bubbles/v2/key"

// keyMap is the single source of truth for keys and their help text.
type keyMap struct {
	Up, Down, Top, Bottom, PageUp, PageDown key.Binding
	Move                                    key.Binding // help-only stand-in for up/down
	Enter, Back                             key.Binding
	NextTab, PrevTab                        key.Binding
	Tab1, Tab2, Tab3, Tab4                  key.Binding
	Filter, New, Extend, Close, Yank        key.Binding
	Approve, Deny, Withdraw                 key.Binding
	Sync, Refresh, Help, Quit               key.Binding
	Notices                                 key.Binding
}

func newKeyMap() keyMap {
	b := func(help, desc string, keys ...string) key.Binding {
		return key.NewBinding(key.WithKeys(keys...), key.WithHelp(help, desc))
	}
	return keyMap{
		Up:       b("↑/k", "up", "up", "k"),
		Down:     b("↓/j", "down", "down", "j"),
		Top:      b("g", "top", "g", "home"),
		Bottom:   b("G", "bottom", "G", "end"),
		PageUp:   b("pgup", "page up", "pgup", "ctrl+u"),
		PageDown: b("pgdn", "page down", "pgdown", "ctrl+d"),
		Move:     b("↑/↓", "move", "up", "down", "k", "j"),
		Enter:    b("enter", "detail", "enter"),
		Back:     b("esc", "back", "esc"),
		NextTab:  b("tab", "section", "tab"),
		PrevTab:  b("shift+tab", "previous section", "shift+tab"),
		Tab1:     b("1", "open", "1"),
		Tab2:     b("2", "active", "2"),
		Tab3:     b("3", "expiring", "3"),
		Tab4:     b("4", "closed", "4"),
		Filter:   b("/", "filter", "/"),
		New:      b("n", "new account", "n"),
		Sync:     b("S", "sync", "S"),
		Extend:   b("e", "extend/edit", "e"),
		Close:    b("x", "close", "x"),
		Approve:  b("a", "approve", "a"),
		Deny:     b("d", "deny request", "d"),
		Withdraw: b("w", "withdraw request", "w"),
		Yank:     b("y", "copy account id", "y"),
		Refresh:  b("r", "reload", "r"),
		Help:     b("?", "help", "?"),
		Notices:  b("N", "notifications", "N"),
		Quit:     b("q", "quit", "q", "ctrl+c"),
	}
}

// ShortHelp is the one-line hint bar on the dashboard.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Move, k.Enter, k.New, k.Approve, k.Extend, k.Close, k.Sync, k.Filter, k.Help, k.Quit}
}

// FullHelp feeds the ? overlay.
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Top, k.Bottom, k.PageUp, k.PageDown},
		{k.Enter, k.Back, k.NextTab, k.PrevTab, k.Tab1, k.Tab2, k.Tab3, k.Tab4},
		{k.New, k.Approve, k.Deny, k.Withdraw, k.Extend, k.Close, k.Yank, k.Filter},
		{k.Sync, k.Refresh, k.Notices, k.Help, k.Quit},
	}
}

// detailHelp is the hint bar while the full detail screen is open.
func (k keyMap) detailHelp() []key.Binding {
	return []key.Binding{k.Extend, k.Close, k.Yank, k.Back, k.Quit}
}

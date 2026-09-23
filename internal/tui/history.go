package tui

import (
	"errors"

	tea "charm.land/bubbletea/v2"

	"playplace/internal/audit"
	"playplace/internal/core"
)

var errHistoryIdentity = errors.New("history incomplete: journey identity unavailable")

type historyState struct {
	page    audit.Page
	err     error
	offset  int
	loading bool
}

func historyKey(a *core.Account) string {
	if id := a.HistoryID(); id != "" {
		return id
	}
	return "unresolved:" + a.ID
}

func (m model) fetchHistory(a *core.Account, cursor string) tea.Cmd {
	key := historyKey(a)
	h, ctx, id, seq := m.opts.History, m.ctx, a.HistoryID(), m.histSeq
	return func() tea.Msg {
		if id == "" {
			return histMsg{seq: seq, key: key, err: errHistoryIdentity}
		}
		page, err := h.Search(ctx, audit.Filter{JourneyID: id, Limit: 50, Cursor: cursor})
		return histMsg{seq: seq, key: key, cursor: cursor, page: page, err: err}
	}
}

// The viewport scrolls all loaded events, then fetches another page. Cached
// pages remain reachable in both directions; clearing history cancels their
// generation so late results cannot repopulate the next journey's viewport.
func (m model) scrollHistory(delta int) (tea.Model, tea.Cmd) {
	r, ok := m.selected()
	if !ok || m.opts.History == nil {
		return m, nil
	}
	key := historyKey(r.Account)
	state, ok := m.hist[key]
	if !ok {
		return m, m.loadHistory()
	}
	if delta > 0 && state.offset >= len(state.page.Events)-1 && state.page.Next != "" && !state.loading {
		state.loading = true
		m.hist[key] = state
		return m, m.fetchHistory(r.Account, state.page.Next)
	}
	state.offset = max(0, min(state.offset+delta, len(state.page.Events)-1))
	m.hist[key] = state
	return m, nil
}

func (s historyState) status() string {
	switch {
	case errors.Is(s.err, errHistoryIdentity):
		return "history incomplete: journey identity unavailable"
	case s.err != nil:
		return "history unavailable; press r to retry"
	case s.loading:
		return "loading history…"
	case s.page.Incomplete:
		return "history incomplete: unreadable records"
	case len(s.page.Events) == 0:
		return "nothing recorded yet"
	case s.page.Next != "":
		return "more history available below"
	default:
		return ""
	}
}

package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"playplace/internal/audit"
	"playplace/internal/core"
)

type historyView struct {
	Enabled     bool
	Page        audit.Page
	Error       string
	Notice      string
	NextURL     string
	OrderURL    string
	OldestFirst bool
}

// accountHistory accepts identity only from the authorized provider account,
// never from an HTTP filter or a reused name. Uncorrelated legacy records remain
// available to trusted administrative CLI searches, not owner-facing timelines.
func (s *Server) accountHistory(r *http.Request, a *core.Account) historyView {
	v := historyView{Enabled: s.history != nil}
	if !v.Enabled {
		return v
	}
	if a.HistoryID() == "" {
		v.Error = "History is incomplete: this record has no established journey identity."
		return v
	}
	if a.JourneyID == "" {
		v.Notice = "Earlier history may be incomplete: this account has no persisted journey tag. Uncorrelated records are not included."
	}
	q := r.URL.Query()
	order := q.Get("history_order")
	if order != "" && order != "newest" && order != "oldest" {
		v.Error = "Invalid history order."
		return v
	}
	v.OldestFirst = order == "oldest"
	p, err := s.history.Search(r.Context(), audit.Filter{JourneyID: a.HistoryID(), Limit: 50, Cursor: q.Get("history_cursor"), OldestFirst: v.OldestFirst})
	if err != nil {
		if errors.Is(err, audit.ErrInvalidCursor) {
			v.Error = "Invalid history cursor. Start from the first page."
		} else {
			s.log.Warn("history", "journey", a.HistoryID(), "err", err)
			v.Error = "History is unavailable. Please retry."
		}
	} else {
		v.Page = p
	}
	if p.Next != "" {
		v.NextURL = historyURL(r.URL, "history_cursor", p.Next)
	}
	q.Del("history_cursor")
	if v.OldestFirst {
		q.Set("history_order", "newest")
	} else {
		q.Set("history_order", "oldest")
	}
	u := *r.URL
	u.RawQuery = q.Encode()
	v.OrderURL = u.RequestURI()
	return v
}

func historyURL(u *url.URL, key, value string) string {
	copy := *u
	q := copy.Query()
	q.Set(key, value)
	copy.RawQuery = q.Encode()
	return copy.RequestURI()
}

func historyDetails(e core.AuditEvent) string {
	data, _ := json.MarshalIndent(e.Details, "", "  ")
	return string(data)
}

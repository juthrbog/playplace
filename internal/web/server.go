// Package web serves the htmx UI over core.Service. Sign-in is pluggable;
// with OIDC configured, engineers see and manage their own accounts and a
// list of admins sees the fleet.
package web

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"playplace/internal/audit"
	"playplace/internal/core"
)

// History reads past events for the account page. nil hides the card.
type History interface {
	audit.Searcher
	Where() string
}

// Server is the HTTP front end.
type Server struct {
	svc     *core.Service
	log     *slog.Logger
	auth    Authenticator
	slack   *Slack // optional approval messages with buttons
	history History
	mux     *http.ServeMux
}

// New wires routes. A nil auth means NoAuth; a nil slack disables Slack;
// a nil history hides the history card.
func New(svc *core.Service, log *slog.Logger, auth Authenticator, slack *Slack, history History) *Server {
	if auth == nil {
		auth = NoAuth{}
	}
	s := &Server{svc: svc, log: log, auth: auth, slack: slack, history: history, mux: http.NewServeMux()}
	if slack != nil {
		slack.svc, slack.log = svc, log
		s.mux.HandleFunc("POST /slack/interact", slack.interact)
	}
	s.mux.HandleFunc("GET /{$}", s.index)
	s.mux.HandleFunc("GET /accounts", s.table)
	s.mux.HandleFunc("POST /requests", s.submit)
	s.mux.HandleFunc("POST /requests/{name}/approve", s.approve)
	s.mux.HandleFunc("POST /requests/{name}/deny", s.deny)
	s.mux.HandleFunc("POST /requests/{name}/withdraw", s.withdraw)
	s.mux.HandleFunc("GET /requests/{name}/edit", s.editForm)
	s.mux.HandleFunc("GET /requests/{name}/row", s.editCancel)
	s.mux.HandleFunc("POST /requests/{name}/edit", s.editSave)
	s.mux.HandleFunc("POST /refresh", s.refresh)
	s.mux.HandleFunc("GET /accounts/{id}", s.show)
	s.mux.HandleFunc("POST /accounts/{id}/extend", s.extend)
	// Budget increases are admin-only; the handler authorizes before mutation.
	s.mux.HandleFunc("POST /accounts/{id}/budget", s.budget)
	s.mux.HandleFunc("POST /accounts/{id}/close", s.close)
	s.mux.Handle("GET /static/", staticHandler())
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	s.mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, static, "static/favicon.ico")
	})
	auth.Register(s.mux)
	return s
}

// Handler is the full stack: cross-origin protection, then authentication,
// then the routes. Every mutating route is a form POST, and the session
// cookie is SameSite=Lax (or absent with NoAuth), so without this check a
// page on another origin could submit an approve or close on behalf of a
// signed-in browser. Requests without Sec-Fetch-Site or Origin headers, such
// as curl, are allowed through; Slack signs its own calls.
func (s *Server) Handler() http.Handler {
	cop := http.NewCrossOriginProtection()
	cop.AddInsecureBypassPattern("/slack/")
	// Behind a proxy that does not forward the public Host, an Origin check
	// against r.Host would refuse browsers that omit Sec-Fetch-Site. The
	// configured base URL is the origin browsers actually use.
	if u, err := url.Parse(s.svc.Config().BaseURL); err == nil && u.Scheme != "" && u.Host != "" {
		if err := cop.AddTrustedOrigin(u.Scheme + "://" + u.Host); err != nil {
			s.log.Warn("base url not usable as a trusted origin", "url", s.svc.Config().BaseURL, "err", err)
		}
	}
	return cop.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		// Slack authenticates its own calls with a request signature. Static
		// files are public, they are the same for everyone.
		if r.URL.Path == "/healthz" || r.URL.Path == "/favicon.ico" || strings.HasPrefix(r.URL.Path, "/auth/") || strings.HasPrefix(r.URL.Path, "/slack/") || strings.HasPrefix(r.URL.Path, "/static/") {
			s.mux.ServeHTTP(w, r)
			return
		}
		v, ok := s.auth.Authenticate(r)
		if !ok {
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", s.auth.LoginPath())
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, s.auth.LoginPath(), http.StatusFound)
			return
		}
		s.mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), viewerKey{}, v)))
	}))
}

// ListenAndServe runs until ctx is cancelled, then drains connections.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
		return ctx.Err()
	}
}

// page carries what every template needs about the request.
type page struct {
	Viewer  Viewer
	SignIn  bool // whether sign-in is enforced (shows the sign-out control)
	ShowAll bool
	Pending []core.Request // requests the viewer may see: their own, or all for approvers
	Limits  core.Config    // ceilings and defaults shown in the form
}

func (s *Server) pageFor(r *http.Request) page {
	// "all" arrives as a query parameter on GET and, thanks to hx-include on
	// the switch, in the body of every action, so the view survives actions.
	pg := page{Viewer: ViewerFrom(r.Context()), SignIn: s.auth.Enabled(), ShowAll: r.FormValue("all") == "1", Limits: s.svc.Config()}
	if all, err := s.svc.PendingRequests(r.Context()); err == nil {
		for _, q := range all {
			if canSeeRequest(pg.Viewer, q) {
				pg.Pending = append(pg.Pending, q)
			}
		}
	} else {
		s.log.Warn("pending requests", "err", err)
	}
	return pg
}

// canApprove reports whether the viewer may approve this request. It asks
// the service rule so the button only shows when the click would succeed.
func canApprove(pg page, q core.Request) bool {
	return pg.Viewer.Approver && core.CheckApprover(q, pg.Viewer.Email, pg.Limits.AllowSelfApproval) == nil
}

// canWithdraw mirrors the withdraw handler: admins, the requester, and the owner.
func canWithdraw(v Viewer, q core.Request) bool {
	return v.Admin || strings.EqualFold(q.RequestedBy, v.Email) || strings.EqualFold(q.Owner, v.Email)
}

// ownerDefault prefills the admin's owner field with their own email, or
// leaves it empty when the viewer has no real email (no-auth mode).
func ownerDefault(v Viewer) string {
	if strings.Contains(v.Email, "@") {
		return v.Email
	}
	return ""
}

// canSee reports whether the viewer may see or act on the account.
func canSee(v Viewer, a *core.Account) bool {
	return v.Admin || strings.EqualFold(a.Owner, v.Email)
}

// rows lists the accounts the viewer may see, with month-to-date spend from
// the cost cache. Costs are warmed in parallel; a miss shows as zero until
// the next poll.
func (s *Server) rows(ctx context.Context, v Viewer, showAll bool, force bool) ([]accountRow, error) {
	accounts, err := s.svc.List(ctx, core.ListFilter{IncludeClosed: showAll})
	if err != nil {
		return nil, err
	}
	var mine []*core.Account
	var ids []string
	for _, a := range accounts {
		if !canSee(v, a) {
			continue
		}
		mine = append(mine, a)
		if a.ProviderID != "" {
			ids = append(ids, a.ID)
		}
	}
	if err := s.svc.WarmCosts(ctx, ids, force); err != nil {
		s.log.Warn("costs", "err", err)
	}
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	rows := make([]accountRow, 0, len(mine))
	for _, a := range mine {
		r := accountRow{Account: a}
		if pts, ok := s.svc.CostsCached(a.ProviderID, 31); ok {
			for _, c := range pts {
				if !c.Date.Before(monthStart) {
					r.Spend += c.AmountUSD
				}
			}
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// pendingCard re-renders the pending section for htmx responses.
func (s *Server) pendingAndTable(w http.ResponseWriter, r *http.Request) {
	pg := s.pageFor(r)
	rows, err := s.rows(r.Context(), pg.Viewer, pg.ShowAll, false)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	accountsTable(pg, rows).Render(r.Context(), w)
	pendingCard(pg).Render(r.Context(), w)
}

// submit puts a request in the queue. Engineers request for themselves;
// admins may name another owner.
func (s *Server) submit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, err)
		return
	}
	v := ViewerFrom(r.Context())
	in := core.RequestInput{Name: strings.TrimSpace(r.FormValue("name")), Owner: v.Email, RequestedBy: v.Email, Purpose: r.FormValue("purpose")}
	if v.Admin {
		if o := strings.TrimSpace(r.FormValue("owner")); o != "" {
			in.Owner = o
		}
		in.OverrideLimits = r.FormValue("override") == "on" // only admins may exceed the ceilings
	}
	if val := r.FormValue("ttl"); val != "" {
		d, err := parseDuration(val)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		in.TTL = d
	}
	if val := r.FormValue("budget"); val != "" {
		b, err := parseBudget(val)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		in.BudgetUSD = b
	}
	q, err := s.svc.SubmitRequest(r.Context(), in, "web")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if s.slack != nil {
		if err := s.slack.postRequest(r.Context(), q); err != nil {
			s.log.Warn("slack post", "err", err)
		}
	}
	s.pendingAndTable(w, r)
}

func (s *Server) approve(w http.ResponseWriter, r *http.Request) {
	v := ViewerFrom(r.Context())
	if !v.Approver {
		s.forbid(w, r)
		return
	}
	if _, err := s.svc.Approve(r.Context(), r.PathValue("name"), v.Email); err != nil {
		s.fail(w, r, err)
		return
	}
	s.pendingAndTable(w, r)
}

func (s *Server) deny(w http.ResponseWriter, r *http.Request) {
	v := ViewerFrom(r.Context())
	if !v.Approver {
		s.forbid(w, r)
		return
	}
	reason := r.FormValue("reason")
	if reason == "" {
		reason = r.Header.Get("HX-Prompt")
	}
	if err := s.svc.Deny(r.Context(), r.PathValue("name"), v.Email, reason); err != nil {
		s.fail(w, r, err)
		return
	}
	s.pendingAndTable(w, r)
}

// editForm swaps a pending row for an inline form. Admins only.
func (s *Server) editForm(w http.ResponseWriter, r *http.Request) {
	pg := s.pageFor(r)
	if !pg.Viewer.Admin {
		s.forbid(w, r)
		return
	}
	q, err := s.svc.GetRequest(r.Context(), r.PathValue("name"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, pendingEditRow(pg, q))
}

// editCancel puts the plain row back. The row shows the request's details,
// so the viewer must be someone who would see it in the pending card.
func (s *Server) editCancel(w http.ResponseWriter, r *http.Request) {
	pg := s.pageFor(r)
	q, err := s.svc.GetRequest(r.Context(), r.PathValue("name"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !canSeeRequest(pg.Viewer, q) {
		s.forbid(w, r)
		return
	}
	s.render(w, r, pendingRow(pg, q))
}

// canSeeRequest reports whether the viewer may see a pending request:
// approvers see the queue, everyone else only what they asked for or own.
func canSeeRequest(v Viewer, q core.Request) bool {
	return v.Approver || strings.EqualFold(q.Owner, v.Email) || strings.EqualFold(q.RequestedBy, v.Email)
}

// editSave applies an admin's changes and re-renders the pending card and table.
func (s *Server) editSave(w http.ResponseWriter, r *http.Request) {
	v := ViewerFrom(r.Context())
	if !v.Admin {
		s.forbid(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, err)
		return
	}
	e, err := RequestEditFromForm(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := s.svc.UpdateRequest(r.Context(), r.PathValue("name"), v.Email, e); err != nil {
		s.fail(w, r, err)
		return
	}
	s.pendingAndTable(w, r)
}

// RequestEditFromForm reads owner, days, budget, purpose, and override. Bad
// numbers are errors, as on the request form, not silently ignored fields.
// The edit form always carries the override checkbox, so its absence means
// the box was unticked.
func RequestEditFromForm(r *http.Request) (core.RequestEdit, error) {
	override := r.FormValue("override") == "on"
	e := core.RequestEdit{Owner: strings.TrimSpace(r.FormValue("owner")), OverrideLimits: &override}
	if val := r.FormValue("ttl"); val != "" {
		d, err := parseDuration(val)
		if err != nil {
			return e, err
		}
		e.TTL = d
	}
	if val := r.FormValue("budget"); val != "" {
		b, err := parseBudget(val)
		if err != nil {
			return e, err
		}
		e.BudgetUSD = b
	}
	if _, ok := r.Form["purpose"]; ok {
		p := r.FormValue("purpose")
		e.Purpose = &p
	}
	return e, nil
}

func (s *Server) withdraw(w http.ResponseWriter, r *http.Request) {
	v := ViewerFrom(r.Context())
	q, err := s.svc.GetRequest(r.Context(), r.PathValue("name"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !canWithdraw(v, q) {
		s.forbid(w, r)
		return
	}
	if err := s.svc.Withdraw(r.Context(), q.Name, v.Email); err != nil {
		s.fail(w, r, err)
		return
	}
	s.pendingAndTable(w, r)
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	pg := s.pageFor(r)
	rows, err := s.rows(r.Context(), pg.Viewer, pg.ShowAll, false)
	var msg string
	if err != nil {
		msg = err.Error()
	}
	s.render(w, r, indexPage(pg, rows, msg))
}

func (s *Server) table(w http.ResponseWriter, r *http.Request) {
	pg := s.pageFor(r)
	rows, err := s.rows(r.Context(), pg.Viewer, pg.ShowAll, false)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, accountsTable(pg, rows))
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	pg := s.pageFor(r)
	if !pg.Viewer.Admin {
		s.forbid(w, r, "only admins can sync with AWS")
		return
	}
	// the sync form has no fields of its own, so the switch is the only value
	sum, err := s.svc.Refresh(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.svc.Invalidate()
	rows, err := s.rows(r.Context(), pg.Viewer, pg.ShowAll, true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	msg := "synced, nothing changed"
	if !sum.Empty() {
		msg = "synced: " + sum.String()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	accountsTable(pg, rows).Render(r.Context(), w)
	pendingCard(pg).Render(r.Context(), w)
	noticeFragment(msg).Render(r.Context(), w)
}

// owned resolves an account the viewer may act on, or writes the error.
func (s *Server) owned(w http.ResponseWriter, r *http.Request, ref string) (*core.Account, bool) {
	a, err := s.svc.Resolve(r.Context(), ref)
	if errors.Is(err, core.ErrNotFound) {
		http.NotFound(w, r)
		return nil, false
	}
	if err != nil {
		s.fail(w, r, err)
		return nil, false
	}
	if !canSee(ViewerFrom(r.Context()), a) {
		s.forbid(w, r)
		return nil, false
	}
	return a, true
}

func (s *Server) show(w http.ResponseWriter, r *http.Request) {
	a, ok := s.owned(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	var costs []core.CostPoint
	if a.ProviderID != "" {
		var err error
		costs, err = s.svc.Costs(r.Context(), a.ID, 30, false)
		if err != nil {
			s.log.Warn("costs", "account", a.Name, "err", err)
		}
	}
	var total float64
	for _, c := range costs {
		total += c.AmountUSD
	}
	s.render(w, r, accountPage(s.pageFor(r), a, costs, total, s.accountHistory(r, a)))
}

func (s *Server) extend(w http.ResponseWriter, r *http.Request) {
	a, ok := s.owned(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	by := r.FormValue("by")
	if by == "" {
		by = "7d"
	}
	d, err := parseDuration(by)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Admins may push an account past the lifetime ceiling; engineers may not.
	v := ViewerFrom(r.Context())
	if _, err := s.svc.Extend(r.Context(), a.ID, a.ExpiresAt.Add(d), v.Email, v.Admin); err != nil {
		s.fail(w, r, err)
		return
	}
	s.table(w, r)
}

func (s *Server) close(w http.ResponseWriter, r *http.Request) {
	a, ok := s.owned(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if _, err := s.svc.RequestClose(r.Context(), a.ID, ViewerFrom(r.Context()).Email); err != nil {
		s.fail(w, r, err)
		return
	}
	s.table(w, r)
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(r.Context(), w); err != nil {
		s.log.Error("render", "path", r.URL.Path, "err", err)
	}
}

// forbid answers 403 with a reason; the default says the account is not theirs.
func (s *Server) forbid(w http.ResponseWriter, r *http.Request, why ...string) {
	msg := "that account belongs to someone else"
	if len(why) > 0 {
		msg = why[0]
	}
	s.log.Warn("forbidden", "path", r.URL.Path, "viewer", ViewerFrom(r.Context()).Email, "why", msg)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Reswap", "none")
		w.Header().Set("X-Form-Error", "1")
		w.WriteHeader(http.StatusForbidden)
		errorFragment(msg).Render(r.Context(), w)
		return
	}
	http.Error(w, msg, http.StatusForbidden)
}

// fail returns an error the page can show. htmx requests get an out-of-band
// fragment so the notice slot updates without replacing the table.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Warn("request failed", "path", r.URL.Path, "err", err)
	if r.Header.Get("HX-Request") == "true" {
		// 200 so htmx processes the out-of-band error slot; the header tells
		// the form not to reset its fields.
		w.Header().Set("HX-Reswap", "none")
		w.Header().Set("X-Form-Error", "1")
		s.render(w, r, errorFragment(err.Error()))
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

// parseBudget reads a monthly budget. ParseFloat also accepts "NaN", "Inf",
// and negatives, none of which core should see.
func parseBudget(s string) (float64, error) {
	b, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(b) || math.IsInf(b, 0) || b <= 0 {
		return 0, errors.New("budget must be a positive number")
	}
	return b, nil
}

// parseDuration reads a lifetime. A bare number is days; "7d" and Go
// durations such as "72h" are also accepted. Every form must be positive
// and finite.
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	days := func(n float64) (time.Duration, error) {
		if math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 || n > 366*100 {
			return 0, errors.New("days must be a positive number")
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return days(n)
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil {
			return 0, errors.New("bad duration " + s)
		}
		return days(n)
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, errors.New("bad duration " + s)
	}
	if d <= 0 {
		return 0, errors.New("days must be a positive number")
	}
	return d, nil
}

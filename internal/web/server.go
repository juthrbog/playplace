// Package web serves the htmx UI over core.Service. Sign-in is pluggable;
// with OIDC configured, engineers see and manage their own accounts and a
// list of admins sees the fleet.
package web

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"slices"
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

	noticesInSlack bool // lifecycle notices go to a Slack channel
}

// SetNoticesInSlack tells the pages that lifecycle notices go to a
// Slack channel, so an empty queue can say where new requests show.
func (s *Server) SetNoticesInSlack(on bool) { s.noticesInSlack = on }

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
	s.mux.HandleFunc("POST /requests/decide", s.decideAll)
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
			s.route(w, r)
			return
		}
		v, ok := s.auth.Authenticate(r)
		if !ok {
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", s.auth.LoginPath())
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			// A page visit keeps its address through sign-in, so a link to a
			// slip still opens the slip. The query escape keeps the address one
			// value of the sign-in address.
			to := s.auth.LoginPath()
			if next := returnPath(r); next != "" {
				to += "?next=" + url.QueryEscape(next)
			}
			http.Redirect(w, r, to, http.StatusFound)
			return
		}
		s.route(w, r.WithContext(context.WithValue(r.Context(), viewerKey{}, v)))
	}))
}

// returnPath is the address that a sign-in returns to: the path and
// query of a page visit, when it is a path on this host. The home page
// needs none, because the sign-in returns there by default.
func returnPath(r *http.Request) string {
	if r.Method != http.MethodGet {
		return ""
	}
	if next := r.URL.RequestURI(); next != "/" {
		return localPath(next)
	}
	return ""
}

// shutdownDrain is how long a stop waits for the answers in flight. A
// decision post can run for decideBudget and one more approval. Shutdown
// returns as soon as the last answer is out, so an idle server still stops
// at once.
const shutdownDrain = decideBudget + 15*time.Second

// ListenAndServe runs until ctx is cancelled, then drains connections. A
// Slack click decides after its answer, so the stop then waits for those
// decisions too, in the same drain. Shutdown returns nil only when every
// handler has returned, so no click starts a decision during that wait.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), shutdownDrain)
		defer cancel()
		if srv.Shutdown(shutdown) == nil && s.slack != nil {
			s.slack.wait(shutdown)
		}
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
	Path    string         // the request path, so the shell can mark the current page
	// SignedOut marks a page for nobody, such as a failed sign-in. The shell
	// then shows only the brand.
	SignedOut bool
	// PendingFailed marks a queue that did not load. The queue panel then
	// says so, and a poll leaves the panel on the page as it is.
	PendingFailed bool
	// RegionInCard marks the home page, which holds the live region in the
	// queue panel. The layout then leaves out its own.
	RegionInCard bool
	// Open names the request whose slip the page renders opened. That is
	// the request of the address (?req=), the slip that Cancel puts back,
	// or the request that an admin just saved. Every other slip renders
	// closed, and app.js keeps the state that the viewer chose.
	Open string
	// Results are the results of the decision post that the answer
	// answers, in queue order. The queue shows each one in the slip of its
	// request, a gone slip in the old place included. A result with no
	// place shows in the bulk bar.
	Results []decision
	// Holds lists, for each owner in lower case, the open accounts and
	// pending requests of that owner. The opened slip says how the owner
	// stands against the per-owner limit. HoldsFailed marks a list that
	// did not load.
	Holds       map[string][]string
	HoldsFailed bool
	// InSlack marks a deployment that posts new requests to Slack, through
	// the approval bot or the notice webhook.
	InSlack bool
	// Missing names the request of the address (?req=) when the queue
	// that loaded does not list it. The bar then says why no slip opens.
	Missing string
}

// regionInCard is pg for the home page layout.
func regionInCard(pg page) page {
	pg.RegionInCard = true
	return pg
}

func (s *Server) pageFor(r *http.Request) page {
	// "all" arrives as a query parameter on GET and, thanks to hx-include on
	// the switch, in the body of every action, so the view survives actions.
	pg := page{Viewer: ViewerFrom(r.Context()), SignIn: s.auth.Enabled(), ShowAll: r.FormValue("all") == "1", Limits: s.svc.Config(), Path: r.URL.Path, InSlack: s.slack != nil || s.noticesInSlack}
	all, err := s.svc.PendingRequests(r.Context())
	if err != nil {
		s.log.Warn("pending requests", "err", err)
		pg.PendingFailed = true
		return pg
	}
	for _, q := range all {
		if canSeeRequest(pg.Viewer, q) {
			pg.Pending = append(pg.Pending, q)
		}
	}
	// Core sorts the queue by time alone, over tags in map order. The name
	// breaks ties, so two reads of the same queue hash the same.
	slices.SortStableFunc(pg.Pending, func(a, b core.Request) int {
		return cmp.Or(a.RequestedAt.Compare(b.RequestedAt), strings.Compare(a.Name, b.Name))
	})
	// The opened slip says how the owner stands against the per-owner
	// limit. The list comes from the same cached inventory as the queue.
	if pg.Viewer.Approver {
		accounts, err := s.svc.List(r.Context(), core.ListFilter{})
		if err != nil {
			s.log.Warn("accounts for the per-owner limit", "path", r.URL.Path, "err", err)
			pg.HoldsFailed = true
		} else {
			pg.Holds = ownerHolds(accounts)
		}
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
// the cost cache. Costs are warmed in parallel. A row shows the spend of the
// last good pull, and zero when no pull succeeded yet.
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
	// Core sorts accounts by creation time alone, over a listing in map
	// order. The id breaks ties, so two reads of the same fleet hash the same.
	slices.SortStableFunc(mine, func(a, b *core.Account) int {
		return cmp.Or(b.CreatedAt.Compare(a.CreatedAt), strings.Compare(a.ID, b.ID))
	})
	if err := s.svc.WarmCosts(ctx, ids, force); err != nil {
		s.log.Warn("costs", "err", err)
	}
	now := s.svc.Now()
	rows := make([]accountRow, 0, len(mine))
	for _, a := range mine {
		r := accountRow{Account: a}
		if pts, ok := s.svc.CostsCached(a.ProviderID, 31); ok {
			r.Spend = core.MonthToDate(pts, now)
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// pendingAndTable re-renders the table and the pending section for htmx
// responses. fromCard is true for a decision made in the queue panel, which
// gets the panel as its main swap. open names the slip that the answer
// renders opened, such as the slip that an admin just saved. result is the
// message for the live region, which says what the action did.
func (s *Server) pendingAndTable(w http.ResponseWriter, r *http.Request, fromCard bool, open, result string) {
	pg := s.pageFor(r)
	pg.Open = open
	rows, err := s.rows(r.Context(), pg.Viewer, pg.ShowAll, false)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeAccounts(w, r, pg, rows, "", s.pollHash(r.Context(), pg, rows, ""), fromCard, noticeFragment("check", result))
}

// pollHashHeader carries the poll hash of the page. app.js sends it on the
// 15 s poll, and the poll answers 204 when it still matches.
const pollHashHeader = "X-Poll-Hash"

// pollHash is the hash of what a poll answer shows: the accounts section
// without its own hash, then the queue panel and the count. The home page,
// the poll and every action answer use it, so the section always carries
// the hash of what the page shows. It is empty if the render fails, and the
// section then carries none, so every poll gets the full answer. The hash
// covers a failed list as one state, also when its refusal text changes, for
// example from a throttle to another failure. The same outage then answers
// 204.
func (s *Server) pollHash(ctx context.Context, pg page, rows []accountRow, errMsg string) string {
	// The open state of a slip belongs to the viewer, so the hash leaves it
	// out. A page that renders a slip opened then gets 204 from its first
	// poll. The results of a decision post are in the hash, so the poll
	// after a decision answers 200 and redraws the queue without them. The
	// bar line of a missing request belongs to the page visit, so the hash
	// leaves it out too.
	pg.Open, pg.Missing = "", ""
	if errMsg != "" {
		errMsg = "unavailable"
	}
	h := sha256.New()
	if err := accountsAnswer(pg, rows, errMsg, "").Render(ctx, h); err != nil {
		s.log.Error("render poll hash", "err", err)
		return ""
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// writeAccounts answers with the accounts section, which carries hash for
// the next poll, and the queue panel. fromCard makes the panel the main
// swap, for a decision made in the panel. after holds parts that follow,
// such as the Sync message.
func (s *Server) writeAccounts(w http.ResponseWriter, r *http.Request, pg page, rows []accountRow, errMsg, hash string, fromCard bool, after ...templ.Component) {
	main := accountsAnswer(pg, rows, errMsg, hash)
	if fromCard {
		main = cardAnswer(pg, rows, errMsg, hash)
	}
	s.render(w, r, templ.Join(append([]templ.Component{main}, after...)...))
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
			s.fail(w, r, fieldError{"ttl", err})
			return
		}
		in.TTL = d
	}
	if val := r.FormValue("budget"); val != "" {
		b, err := parseBudget(val)
		if err != nil {
			s.fail(w, r, fieldError{"budget", err})
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
	s.pendingAndTable(w, r, false, "", "Sent the request for "+q.Name+".")
}

// requestRef is the request a form acts on: the name from the path and the
// version the page rendered into the form's hidden field.
func requestRef(r *http.Request) core.RequestRef {
	return core.RequestRef{Name: r.PathValue("name"), Version: r.FormValue("version")}
}

// failRequest reports a refused decision on a request. The refusal shows in
// the queue panel, beside the slips, not in the New request form. When the
// request changed or ended after the page was loaded, the queue panel is
// rendered again first, so the viewer sees what is waiting now. The log
// holds the raw error, and the slot holds the text of refusal.
func (s *Server) failRequest(w http.ResponseWriter, r *http.Request, err error) {
	if r.Header.Get("HX-Request") != "true" {
		s.fail(w, r, err)
		return
	}
	s.log.Warn("request failed", "path", r.URL.Path, "err", err)
	w.Header().Set("HX-Reswap", "none")
	w.Header().Set("X-Form-Error", "1")
	markField(w, err, "pending-error")
	if errors.Is(err, core.ErrRequestChanged) || errors.Is(err, core.ErrNotFound) {
		// Core drops the cache when it refuses a version. withdraw looks the
		// request up before core sees the version, so a request that is gone
		// arrives here with the cache untouched.
		s.svc.Invalidate()
		// htmx swaps out-of-band parts in order. The fresh queue lands
		// first with an empty slot, and the refusal then fills it.
		s.render(w, r, pendingUpdate(s.pageFor(r)))
	}
	s.render(w, r, errorFragment("pending-error", refusal(err, r.PathValue("name"))))
}

// editForm swaps a slip for an inline form. Admins only.
func (s *Server) editForm(w http.ResponseWriter, r *http.Request) {
	pg := s.pageFor(r)
	if !pg.Viewer.Admin {
		s.forbid(w, r, adminsEditOnly)
		return
	}
	q, err := s.svc.GetRequest(r.Context(), r.PathValue("name"))
	if err != nil {
		s.failRequest(w, r, err)
		return
	}
	s.render(w, r, slipEdit(pg, q))
}

// editCancel puts the plain slip back. Edit sits in the opened slip, so the
// slip comes back opened. The slip shows the request's details, so the
// viewer must be someone who would see it in the queue panel.
func (s *Server) editCancel(w http.ResponseWriter, r *http.Request) {
	pg := s.pageFor(r)
	q, err := s.svc.GetRequest(r.Context(), r.PathValue("name"))
	if err != nil {
		s.failRequest(w, r, err)
		return
	}
	if !canSeeRequest(pg.Viewer, q) {
		s.forbid(w, r, notYourRequest)
		return
	}
	pg.Open = q.Name
	s.render(w, r, requestSlip(pg, q))
}

// canSeeRequest reports whether the viewer may see a pending request:
// approvers see the queue, everyone else only what they asked for or own.
func canSeeRequest(v Viewer, q core.Request) bool {
	return v.Approver || strings.EqualFold(q.Owner, v.Email) || strings.EqualFold(q.RequestedBy, v.Email)
}

// editSave applies an admin's changes and re-renders the queue panel and
// table, with the saved slip opened.
func (s *Server) editSave(w http.ResponseWriter, r *http.Request) {
	v := ViewerFrom(r.Context())
	if !v.Admin {
		s.forbid(w, r, adminsEditOnly)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.failRequest(w, r, err)
		return
	}
	// The form was filled from the record that its version names. If the
	// record changed since, UpdateRequest refuses the version, whatever the
	// edit holds.
	q, err := s.svc.GetRequest(r.Context(), r.PathValue("name"))
	if err != nil {
		s.failRequest(w, r, err)
		return
	}
	e, err := RequestEditFromForm(r, q)
	if err != nil {
		s.failRequest(w, r, err)
		return
	}
	if _, err := s.svc.UpdateRequest(r.Context(), requestRef(r), v.Email, e); err != nil {
		s.failRequest(w, r, err)
		return
	}
	s.pendingAndTable(w, r, true, q.Name, "Saved "+q.Name+".")
}

// RequestEditFromForm reads owner, days, budget, purpose, and override from
// an edit form, where from is the request that filled the form. A field that
// still holds its prefill (the value the form opened with) is left out of
// the edit, so an untouched lifetime or budget keeps its exact value. Bad
// numbers are errors, as on the request form, not silently ignored fields.
// The edit form always carries the override checkbox, so its absence means
// the box was unticked.
func RequestEditFromForm(r *http.Request, from core.Request) (core.RequestEdit, error) {
	var e core.RequestEdit
	if owner := strings.TrimSpace(r.FormValue("owner")); owner != from.Owner {
		e.Owner = owner
	}
	if val := r.FormValue("ttl"); val != "" && val != dayValue(from.TTL) {
		d, err := parseDuration(val)
		if err != nil {
			return e, fieldError{"ttl", err}
		}
		e.TTL = d
	}
	if val := r.FormValue("budget"); val != "" && val != budgetValue(from.BudgetUSD) {
		b, err := parseBudget(val)
		if err != nil {
			return e, fieldError{"budget", err}
		}
		e.BudgetUSD = b
	}
	if _, ok := r.Form["purpose"]; ok && r.FormValue("purpose") != from.Purpose {
		p := r.FormValue("purpose")
		e.Purpose = &p
	}
	if override := r.FormValue("override") == "on"; override != from.OverrideLimits {
		e.OverrideLimits = &override
	}
	return e, nil
}

func (s *Server) withdraw(w http.ResponseWriter, r *http.Request) {
	v := ViewerFrom(r.Context())
	q, err := s.svc.GetRequest(r.Context(), r.PathValue("name"))
	if err != nil {
		s.failRequest(w, r, err)
		return
	}
	// Permission is checked on the current record; Withdraw then refuses
	// it unless it is still the version the page showed. A viewer whose
	// page is stale can fail the permission check because the request
	// changed hands: an admin gave it to another owner, or it ended and
	// someone else submitted a new one with the same name. That viewer gets
	// the stale-page refusal and a fresh queue, not a 403. The message names
	// nobody, because not every such viewer is allowed to see the request.
	ref := requestRef(r)
	if !canWithdraw(v, q) {
		if ref.Version != q.Version() {
			s.failRequest(w, r, fmt.Errorf("%w: the request for %s you loaded has changed or ended; look again", core.ErrRequestChanged, ref.Name))
			return
		}
		s.forbid(w, r, cannotWithdraw)
		return
	}
	if err := s.svc.Withdraw(r.Context(), ref, v.Email); err != nil {
		s.failRequest(w, r, err)
		return
	}
	s.pendingAndTable(w, r, true, "", "Withdrew the request for "+ref.Name+".")
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	pg := s.pageFor(r)
	// A link from Slack or a notice names its request, and the page renders
	// that slip opened. The name is only compared with request names, and
	// the bar below names it only when it is a valid request name.
	pg.Open = r.URL.Query().Get("req")
	// A link names a request that can be gone by the time someone opens
	// it. The bar says why, on the server, because screen readers often
	// skip a live region that changes while the page loads. A queue that
	// did not load names nothing.
	if pg.Open != "" && !pg.PendingFailed && core.ValidateName(pg.Open) == nil &&
		!slices.ContainsFunc(pg.Pending, func(q core.Request) bool { return q.Name == pg.Open }) {
		pg.Missing = pg.Open
	}
	rows, err := s.rows(r.Context(), pg.Viewer, pg.ShowAll, false)
	var msg string
	if err != nil {
		s.log.Warn("accounts", "path", r.URL.Path, "err", err)
		msg = listFailure(err)
	}
	s.render(w, r, indexPage(pg, rows, msg, s.pollHash(r.Context(), pg, rows, msg)))
}

// table answers the 15 s poll and the Show closed box. A poll that sends the
// hash of the current answer gets 204 and no body. A failed list is a panel
// that says so, with the text of refusal. Its hash leaves out that text, so
// the same outage answers 204, and the next good poll clears it.
func (s *Server) table(w http.ResponseWriter, r *http.Request) {
	// The answer is a fragment of the home page, so a page visit goes there.
	if r.Header.Get("HX-Request") != "true" {
		to := "/#accounts"
		if r.FormValue("all") == "1" {
			to = "/?all=1#accounts"
		}
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	pg := s.pageFor(r)
	rows, err := s.rows(r.Context(), pg.Viewer, pg.ShowAll, false)
	var errMsg string
	if err != nil {
		s.log.Warn("accounts poll", "path", r.URL.Path, "err", err)
		rows, errMsg = nil, listFailure(err)
	}
	// The answer changes with the data, so a cache must ask every time.
	w.Header().Set("Cache-Control", "private, no-cache")
	hash := s.pollHash(r.Context(), pg, rows, errMsg)
	if hash != "" && r.Header.Get(pollHashHeader) == hash {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.writeAccounts(w, r, pg, rows, errMsg, hash, false)
}

// syncFromShell is the HX-Target of Sync on a page with no accounts
// section. The answer then holds only the live region part.
const syncFromShell = "notice"

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	pg := s.pageFor(r)
	if !pg.Viewer.Admin {
		s.forbid(w, r, "only admins can sync with AWS")
		return
	}
	// the sync form has no fields of its own, so the switch is the only value
	sum, err := s.svc.Refresh(r.Context())
	if err != nil {
		s.failSync(w, r, err)
		return
	}
	s.svc.Invalidate()
	msg := "Synced with AWS. Nothing changed."
	if !sum.Empty() {
		msg = "Synced with AWS: " + sum.String() + "."
	}
	if r.Header.Get("HX-Target") == syncFromShell {
		s.render(w, r, noticeFragment("sync", msg))
		return
	}
	rows, err := s.rows(r.Context(), pg.Viewer, pg.ShowAll, true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeAccounts(w, r, pg, rows, "", s.pollHash(r.Context(), pg, rows, ""), false, noticeFragment("sync", msg))
}

// failSync reports a failed Sync. Off the home page there is no refusal slot
// near the button, so the refusal shows in the live region itself.
func (s *Server) failSync(w http.ResponseWriter, r *http.Request, err error) {
	if r.Header.Get("HX-Request") != "true" || r.Header.Get("HX-Target") != syncFromShell {
		s.fail(w, r, err)
		return
	}
	s.log.Warn("request failed", "path", r.URL.Path, "err", err)
	w.Header().Set("HX-Reswap", "none")
	w.Header().Set("X-Form-Error", "1")
	s.render(w, r, noticeFragment("alert", refusal(err, "")))
}

// owned resolves an account the viewer may act on, or writes the error.
func (s *Server) owned(w http.ResponseWriter, r *http.Request, ref string) (*core.Account, bool) {
	a, err := s.svc.Resolve(r.Context(), ref)
	if errors.Is(err, core.ErrNotFound) {
		// htmx does not swap a 404, so an action on an account that is
		// gone gets a refusal the page shows.
		if r.Header.Get("HX-Request") == "true" {
			s.fail(w, r, err)
		} else {
			s.accountNotFound(w, r, ref)
		}
		return nil, false
	}
	if err != nil {
		s.fail(w, r, err)
		return nil, false
	}
	if !canSee(ViewerFrom(r.Context()), a) {
		s.forbid(w, r, notYourAccount)
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
		// Opening the page is a request of a person, so spend that is not
		// known is pulled at once, also within the wait after a failed poll
		// pull. Only people force a pull, and the poll never does.
		_, known := s.svc.CostState(a.ProviderID, 30)
		var err error
		costs, err = s.svc.Costs(r.Context(), a.ID, 30, !known)
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
	extended, err := s.svc.Extend(r.Context(), a.ID, a.ExpiresAt.Add(d), v.Email, v.Admin)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.pendingAndTable(w, r, false, "", "Extended "+a.Name+" to "+extended.ExpiresAt.UTC().Format("Jan 2 15:04 MST")+".")
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
	s.pendingAndTable(w, r, false, "", "Requested closure of "+a.Name+".")
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(r.Context(), w); err != nil {
		s.log.Error("render", "path", r.URL.Path, "err", err)
	}
}

// forbid answers 403 with the reason, which names the rule that refused the
// viewer. Every route under /requests/ acts from the queue panel, so its
// refusal fills the slot of the panel. A page visit gets the styled 403 page.
func (s *Server) forbid(w http.ResponseWriter, r *http.Request, why string) {
	s.log.Warn("forbidden", "path", r.URL.Path, "viewer", ViewerFrom(r.Context()).Email, "why", why)
	if r.Header.Get("HX-Request") == "true" {
		// app.js swaps the out-of-band parts of a 403 only with HX-Reswap:
		// none, so the slot and the live region show the refusal.
		w.Header().Set("HX-Reswap", "none")
		w.Header().Set("X-Form-Error", "1")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		slot := "form-error"
		if strings.HasPrefix(r.URL.Path, "/requests/") {
			slot = "pending-error"
		}
		if err := errorFragment(slot, why).Render(r.Context(), w); err != nil {
			s.log.Error("render", "path", r.URL.Path, "err", err)
		}
		return
	}
	recovery := forbidRecovery[why]
	if recovery == "" {
		recovery = "Go back to Home."
	}
	s.errorPage(w, r, errorView{Code: http.StatusForbidden, Title: "Not allowed", Message: why, Recovery: recovery, Back: "/", BackLabel: "Go to Home"})
}

// fail returns an error the page can show. htmx requests get out-of-band
// parts, so the refusal slot and the live region update without replacing
// the table. A page visit gets the styled 400 page. The log holds the raw
// error, and the page holds the text of refusal. The name in the path is
// the request that the action was on, if any.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Warn("request failed", "path", r.URL.Path, "err", err)
	text := refusal(err, r.PathValue("name"))
	if r.Header.Get("HX-Request") == "true" {
		// 200 so htmx processes the out-of-band error slot; the header tells
		// the form not to reset its fields.
		w.Header().Set("HX-Reswap", "none")
		w.Header().Set("X-Form-Error", "1")
		markField(w, err, "form-error")
		s.render(w, r, errorFragment("form-error", text))
		return
	}
	s.errorPage(w, r, errorView{Code: http.StatusBadRequest, Title: "Action refused", Message: text, Recovery: "Go back to Home and try again.", Back: "/", BackLabel: "Go to Home"})
}

// The headers of a refused field. app.js marks the input with aria-invalid
// and ties it to the slot that holds the refusal with aria-describedby.
const (
	invalidFieldHeader = "X-Invalid-Field"
	invalidSlotHeader  = "X-Invalid-Slot"
)

// fieldError is a refusal of one form field. Only the numbers that the web
// layer parses itself carry a field. Core refusals name no field.
type fieldError struct {
	field string
	err   error
}

func (e fieldError) Error() string { return e.err.Error() }
func (e fieldError) Unwrap() error { return e.err }

// markField names the refused field and its slot when err is about one
// field. Any other refusal leaves both headers out.
func markField(w http.ResponseWriter, err error, slot string) {
	var fe fieldError
	if errors.As(err, &fe) {
		w.Header().Set(invalidFieldHeader, fe.field)
		w.Header().Set(invalidSlotHeader, slot)
	}
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

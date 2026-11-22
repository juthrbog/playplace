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
	"regexp"
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

	// problems logs the raw problem text of accounts that pages show in
	// plain words.
	problems problemLog
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
			// An htmx request, such as the poll of a page left open, names
			// that page in HX-Current-URL. The sign-in then returns there.
			if r.Header.Get("HX-Request") == "true" {
				to := s.auth.LoginPath()
				if next := htmxReturnPath(r); next != "" {
					to += "?next=" + url.QueryEscape(next)
				}
				w.Header().Set("HX-Redirect", to)
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
	return pageReturnPath(r.URL)
}

// htmxReturnPath is the address that a sign-in returns to after an htmx
// request: the path and query of the page in HX-Current-URL, when it is a
// path on this host. The home page needs none.
func htmxReturnPath(r *http.Request) string {
	u, err := url.Parse(r.Header.Get("HX-Current-URL"))
	if err != nil {
		return ""
	}
	return pageReturnPath(u)
}

// pageReturnPath is the path and query of u, when it is a path on this
// host. A query can make the address longer than localPath allows, for
// example a long search in other scripts, because each byte of a rune
// takes three characters. The sign-in then returns to the path alone, so
// the viewer still gets the page, in its default view.
func pageReturnPath(u *url.URL) string {
	next := u.RequestURI()
	if next == "/" {
		return ""
	}
	if p := localPath(next); p != "" {
		return p
	}
	if u.RawQuery == "" || u.EscapedPath() == "/" {
		return ""
	}
	return localPath(u.EscapedPath())
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
	Viewer Viewer
	SignIn bool // whether sign-in is enforced (shows the sign-out control)
	// Fleet is the search, filters, sort and page of the accounts page.
	// pageFor reads it only for that page. rows sets its Page to the page it
	// shows, and Total to the number of accounts that pass the filters. On
	// the home page Total counts the open accounts that the page lists
	// before the cut to homeFleetSize, and AllTotal counts what the
	// accounts page lists by default, which adds the pending requests.
	Fleet    fleetView
	Total    int
	AllTotal int
	Pending  []core.Request // requests the viewer may see: their own, or all for approvers
	Limits   core.Config    // ceilings and defaults shown in the form
	Path     string         // the request path, so the shell can mark the current page
	// Now is the service clock when the page was built. Time left on the
	// page counts from it, not from the wall clock.
	Now time.Time
	// SignedOut marks a page for nobody, such as a failed sign-in. The shell
	// then shows only the brand.
	SignedOut bool
	// PendingFailed marks a queue that did not load. The queue panel then
	// says so, and a poll leaves the panel on the page as it is.
	PendingFailed bool
	// RegionInCard marks the home page, which holds the live region in the
	// queue panel. The layout then leaves out its own.
	RegionInCard bool
	// Polls marks a page whose content polls: the home page, the accounts
	// page and the account page. Its footer then holds the live line.
	Polls bool
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
	// Asker is the page that sent the request, as askingPage reads it:
	// pathAccounts, the path of an account page or "/". The poll and the
	// action answers show only what that page holds, and the poll hash
	// covers exactly that.
	Asker string
	// Ledger is the Needs attention ledger of the admin home. rows fills
	// it from every account the admin can see, closed ones too.
	Ledger []ledgerRow
	// Landed is the ID of the account that the viewer just extended. Its
	// due stamp lands. The poll hash leaves it out, so the next poll still
	// gets 204.
	Landed string
}

// pathAccounts is the path of the accounts page.
const pathAccounts = "/accounts"

// askingPage is the page that sent the request. htmx sends the address of
// the page in HX-Current-URL on every request, and only its path matters. A
// page visit is its own page, so it uses the request path. The accounts
// page and an account page with a valid account reference are pages of
// their own. An htmx request with no such header, or with one that does not
// parse, and any other page count as the home page, which shows the most.
func askingPage(r *http.Request) string {
	path := r.URL.Path
	if r.Header.Get("HX-Request") == "true" {
		path = "/"
		if u, err := url.Parse(r.Header.Get("HX-Current-URL")); err == nil {
			path = u.Path
		}
	}
	if path == pathAccounts || isAccountPage(path) {
		return path
	}
	return "/"
}

// accountRefRe is a valid account reference in the address of an account
// page: an AWS account ID, a creation request ID, a name or an ID prefix,
// as core.Resolve reads them. Each is letters, digits and dashes. A
// pending request is an account whose ID is "req:" and the request name, so
// a colon is valid too.
var accountRefRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:-]{0,67}$`)

// isAccountPage reports whether path is the address of an account page:
// /accounts/ and one valid account reference.
func isAccountPage(path string) bool {
	ref, ok := strings.CutPrefix(path, pathAccounts+"/")
	return ok && accountRefRe.MatchString(ref)
}

// onAccounts reports whether the answer goes to the accounts page, which has
// no queue panel.
func (pg page) onAccounts() bool { return pg.Asker == pathAccounts }

// onAccount reports whether the answer goes to an account page. That page
// shows one account and has no queue panel, no ledger and no fleet.
func (pg page) onAccount() bool { return isAccountPage(pg.Asker) }

// accountRef is the account reference of the account page that asked.
func (pg page) accountRef() string { return strings.TrimPrefix(pg.Asker, pathAccounts+"/") }

// polling is pg for the layout of a page that polls.
func polling(pg page) page {
	pg.Polls = true
	return pg
}

// regionInCard is pg for the home page layout.
func regionInCard(pg page) page {
	pg.RegionInCard = true
	return pg
}

func (s *Server) pageFor(r *http.Request) page {
	pg := s.shellPage(r)
	if pg.PendingFailed {
		return pg
	}
	// The opened slip of an approver says how the owner stands against the
	// per-owner limit, and Your requests says how the viewer stands. The
	// list comes from the same cached inventory as the queue.
	accounts, err := s.svc.List(r.Context(), core.ListFilter{})
	if err != nil {
		s.log.Warn("accounts for the per-owner limit", "path", r.URL.Path, "err", err)
		pg.HoldsFailed = true
	} else {
		pg.Holds = ownerHolds(accounts)
	}
	return pg
}

// shellPage is the page of a request with what the shell needs: the
// viewer, the fleet view and the queue, whose count shows beside Queue on
// every page. It leaves out the holds of owners, which only the queue and
// Your requests use. The account page uses shellPage alone.
func (s *Server) shellPage(r *http.Request) page {
	pg := s.basePage(r)
	// The fleet view of the accounts page comes in the query of a GET and,
	// thanks to hx-include, in the body of an action, so the view survives
	// actions. The home page has no view of its own.
	if pg.onAccounts() {
		pg.Fleet = parseFleetView(r.FormValue, pg.Viewer)
	}
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
	return pg
}

// basePage is the page of a request with no queue: the viewer, the limits,
// the asking page and the clock. The refresh of an account page uses it
// alone, because the answer shows only the account.
func (s *Server) basePage(r *http.Request) page {
	return page{Viewer: ViewerFrom(r.Context()), SignIn: s.auth.Enabled(), Limits: s.svc.Config(), Path: r.URL.Path, Asker: askingPage(r), InSlack: s.slack != nil || s.noticesInSlack, Now: s.svc.Now()}
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
// the cost cache. Costs are warmed in parallel for every account the viewer
// may see, before any filter, so spend filters and the spend sort are right.
// Spend is known only when the last pull succeeded, as in the CLI. An
// account with no AWS account ID has no spend yet, which is known to be
// zero. The accounts page filters, sorts and cuts the list to the page of
// pg.Fleet, and sets pg.Fleet.Page and pg.Total. The home page lists the
// open accounts by due date (homeRows) and sets pg.Total. For an admin it
// also fills pg.Ledger from every account, closed ones too.
func (s *Server) rows(ctx context.Context, pg *page, force bool) ([]accountRow, error) {
	includeClosed := pg.Viewer.Admin
	if pg.onAccounts() {
		includeClosed = pg.Fleet.Status == fleetStatusAny || pg.Fleet.Status == string(core.StatusClosed)
	}
	accounts, err := s.svc.List(ctx, core.ListFilter{IncludeClosed: includeClosed})
	if err != nil {
		return nil, err
	}
	var mine []*core.Account
	var ids []string
	for _, a := range accounts {
		if !canSee(pg.Viewer, a) {
			continue
		}
		mine = append(mine, a)
		// The home page shows no spend of a closed account, and a closed
		// account is never over budget, so its costs stay cold there.
		if a.ProviderID != "" && (pg.onAccounts() || a.Status != core.StatusClosed) {
			ids = append(ids, a.ID)
		}
	}
	if err := s.svc.WarmCosts(ctx, ids, force); err != nil {
		s.log.Warn("costs", "err", err)
	}
	now := s.svc.Now()
	rows := make([]accountRow, 0, len(mine))
	for _, a := range mine {
		s.problems.note(s.log, a)
		r := accountRow{Account: a, SpendKnown: a.ProviderID == "", Now: now}
		if a.ProviderID != "" {
			if pts, ok := s.svc.CostState(a.ProviderID, 31); ok {
				r.Spend, r.SpendKnown = core.MonthToDate(pts, now), true
			}
		}
		rows = append(rows, r)
	}
	if pg.onAccounts() {
		var shown []accountRow
		shown, pg.Total, pg.Fleet.Page = pg.Fleet.apply(rows, pg.Viewer, now)
		return shown, nil
	}
	if pg.Viewer.Admin {
		pg.Ledger = buildLedger(rows, now)
	}
	var shown []accountRow
	shown, pg.Total, pg.AllTotal = homeRows(rows, pg.Viewer, now)
	return shown, nil
}

// pendingAndTable re-renders the table and the pending section for htmx
// responses. fromCard is true for a decision made in the queue panel, which
// gets the panel as its main swap. open names the slip that the answer
// renders opened, such as the slip that an admin just saved. result is the
// message for the live region, which says what the action did.
func (s *Server) pendingAndTable(w http.ResponseWriter, r *http.Request, fromCard bool, open, result string) {
	pg := s.pageFor(r)
	pg.Open = open
	s.answer(w, r, pg, fromCard, result)
}

// answer is pendingAndTable for a page that the caller built, such as the
// page of an extension, whose due stamp lands. An account page gets its own
// answer, which reads only its account.
func (s *Server) answer(w http.ResponseWriter, r *http.Request, pg page, fromCard bool, result string) {
	if pg.onAccount() {
		s.writeAccount(w, r, pg, noticeFragment("check", result))
		return
	}
	rows, err := s.rows(r.Context(), &pg, false)
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
// without its own hash, then, on the home page, the queue panel and the
// count, and on the accounts page the count alone. Each page, its poll and every action answer to it use it, so the
// section always carries the hash of what that page shows, and each page
// gets its own 204. It is empty if the render fails, and the
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
	pg.Open, pg.Missing, pg.Landed = "", "", ""
	if errMsg != "" {
		errMsg = "unavailable"
	}
	return s.hashOf(ctx, accountsAnswer(pg, rows, errMsg, ""))
}

// accountHash is the poll hash of an account page: the hash of its answer
// without its own hash. The landed stamp belongs to the extension, so the
// hash leaves it out, and the next poll still gets 204. It is empty if the
// render fails.
func (s *Server) accountHash(ctx context.Context, pg page, v *accountView) string {
	pg.Landed = ""
	return s.hashOf(ctx, accountAnswer(pg, v, ""))
}

// hashOf is the poll hash of what c renders, or empty if the render fails.
func (s *Server) hashOf(ctx context.Context, c templ.Component) string {
	h := sha256.New()
	if err := c.Render(ctx, h); err != nil {
		s.log.Error("render poll hash", "err", err)
		return ""
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// writeAccounts answers with the accounts section, which carries hash for
// the next poll, and on the home page the queue panel. fromCard makes the
// panel the main swap, for a decision made in the panel. The accounts page
// has no panel, so its answer is always the section, with the count for an
// approver. An account page gets its own answer. after holds parts that
// follow, such as the Sync message.
func (s *Server) writeAccounts(w http.ResponseWriter, r *http.Request, pg page, rows []accountRow, errMsg, hash string, fromCard bool, after ...templ.Component) {
	if pg.onAccount() {
		s.writeAccount(w, r, pg, after...)
		return
	}
	main := accountsAnswer(pg, rows, errMsg, hash)
	if fromCard && !pg.onAccounts() {
		main = cardAnswer(pg, rows, errMsg, hash)
	}
	s.render(w, r, templ.Join(append([]templ.Component{main}, after...)...))
}

// accountFor reads the account of the account page that asked, as the
// viewer may see it. It is nil when the account is gone or the viewer may
// not see it, so the answer then holds a plain notice and nothing of the
// account.
func (s *Server) accountFor(ctx context.Context, pg page) (*accountView, error) {
	a, err := s.svc.Resolve(ctx, pg.accountRef())
	if errors.Is(err, core.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !canSee(pg.Viewer, a) {
		return nil, nil
	}
	v := s.viewOf(pg, a)
	return &v, nil
}

// viewOf is what the account page shows of a. Spend is month to date from
// the cost cache. It never pulls, so a refresh or an action never waits for
// Cost Explorer. Spend is known only when the last pull succeeded, and an
// account with no AWS account ID has no spend yet, which is known to be
// zero.
func (s *Server) viewOf(pg page, a *core.Account) accountView {
	s.problems.note(s.log, a)
	row := accountRow{Account: a, SpendKnown: a.ProviderID == "", Now: pg.Now}
	if a.ProviderID != "" {
		if pts, ok := s.svc.CostState(a.ProviderID, 31); ok {
			row.Spend, row.SpendKnown = core.MonthToDate(pts, pg.Now), true
		}
	}
	v := accountView{Row: row, MaxTTL: pg.Limits.MaxTTL}
	v.Latest, v.HasLatest = s.svc.LatestExpiry(a)
	return v
}

// writeAccount answers an account page: the account section, which carries
// the hash for the next poll, then the parts in after, such as the message
// for the live region. It carries no ledger and no queue.
func (s *Server) writeAccount(w http.ResponseWriter, r *http.Request, pg page, after ...templ.Component) {
	v, err := s.accountFor(r.Context(), pg)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	main := accountAnswer(pg, v, s.accountHash(r.Context(), pg, v))
	s.render(w, r, templ.Join(append([]templ.Component{main}, after...)...))
}

// refreshAccount answers the refresh of the account page that asked. An
// account that is gone, or that the viewer may no longer see, gets the plain
// notice, which does not poll. An account that did not load gets a section
// that says so and keeps polling, so the next good poll clears it. A
// reference that now names an account with another ID, such as a creation
// request ID after AWS placed the account, moves the page to that ID.
func (s *Server) refreshAccount(w http.ResponseWriter, r *http.Request, pg page) {
	v, err := s.accountFor(r.Context(), pg)
	if err != nil {
		s.log.Warn("account poll", "path", r.URL.Path, "err", err)
		hash := s.hashOf(r.Context(), accountStale(pg, "", ""))
		if hash != "" && r.Header.Get(pollHashHeader) == hash {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.render(w, r, accountStale(pg, listFailure(err), hash))
		return
	}
	if v != nil {
		if id := pathAccounts + "/" + v.Row.Account.ID; id != pg.Asker && isAccountPage(id) {
			pg.Asker = id
			w.Header().Set("HX-Replace-Url", id)
		}
	}
	s.pollAccount(w, r, pg, v)
}

// pollAccount answers the refresh of an account page with the account
// section, or with 204 when the poll sends the hash of what the page shows.
func (s *Server) pollAccount(w http.ResponseWriter, r *http.Request, pg page, v *accountView) {
	hash := s.accountHash(r.Context(), pg, v)
	if hash != "" && r.Header.Get(pollHashHeader) == hash {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.render(w, r, accountAnswer(pg, v, hash))
}

// noCache marks an answer that changes with the data, so a cache must ask
// every time. The same address is a page or a fragment, and the fragment
// depends on the page that asked, so a cache keeps them apart.
func noCache(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Vary", "HX-Request, HX-Current-URL")
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
	rows, err := s.rows(r.Context(), &pg, false)
	var msg string
	if err != nil {
		s.log.Warn("accounts", "path", r.URL.Path, "err", err)
		msg = listFailure(err)
	}
	s.render(w, r, indexPage(pg, rows, msg, s.pollHash(r.Context(), pg, rows, msg)))
}

// table is the accounts page for a page visit. For htmx it answers the 15 s
// poll and the fleet view with the accounts section, and on the home page
// the queue panel and the ledger of an admin too. A poll that sends the hash of the current answer
// gets 204 and no body. A failed list is a panel that says so, with the text
// of refusal. Its hash leaves out that text, so the same outage answers 204,
// and the next good poll clears it.
func (s *Server) table(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	// An account page polls its own address. An htmx GET of this address
	// from an account page still gets the answer of that page, so it never
	// gets a fleet that the page does not show.
	if pg := s.basePage(r); pg.onAccount() && r.Header.Get("HX-Request") == "true" {
		s.refreshAccount(w, r, pg)
		return
	}
	pg := s.pageFor(r)
	rows, err := s.rows(r.Context(), &pg, false)
	var errMsg string
	if err != nil {
		s.log.Warn("accounts poll", "path", r.URL.Path, "err", err)
		rows, errMsg = nil, listFailure(err)
	}
	hash := s.pollHash(r.Context(), pg, rows, errMsg)
	if r.Header.Get("HX-Request") != "true" {
		s.render(w, r, accountsPage(pg, rows, errMsg, hash))
		return
	}
	// A control of the fleet view replaces the address with the canonical
	// address of the view it shows: no empty or default fields, and a page
	// past the end turned into the last page. The poll asks for that address
	// already, and its id does not match, so it never replaces the address.
	if pg.onAccounts() && strings.HasPrefix(r.Header.Get("HX-Trigger"), fleetControl) {
		w.Header().Set("HX-Replace-Url", fleetURL(pg.Fleet))
	}
	if hash != "" && r.Header.Get(pollHashHeader) == hash {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.writeAccounts(w, r, pg, rows, errMsg, hash, false)
}

// syncFromShell is the HX-Target of Sync on a page with no accounts
// section, which is every page but the home page and the accounts page. The answer then holds only the live region part.
const syncFromShell = "notice"

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	pg := s.pageFor(r)
	if !pg.Viewer.Admin {
		s.forbid(w, r, "only admins can sync with AWS")
		return
	}
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
	rows, err := s.rows(r.Context(), &pg, true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeAccounts(w, r, pg, rows, "", s.pollHash(r.Context(), pg, rows, ""), false, noticeFragment("sync", msg))
}

// failSync reports a failed Sync. On a page with no accounts section there
// is no refusal slot near the button, so the refusal shows in the live region itself.
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

// show is the account page. Only the owner and an admin may open it. For
// htmx it answers the 15 s refresh with the account section, or with 204
// when the poll sends the hash of what the page shows. The refresh reads
// spend from the cost cache only and never pulls.
func (s *Server) show(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "true" {
		// The answer is the section of this address, whatever page sent the
		// request, so it polls this address again.
		noCache(w)
		pg := s.basePage(r)
		pg.Asker = r.URL.Path
		s.refreshAccount(w, r, pg)
		return
	}
	a, ok := s.owned(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	// The page lives at the ID of the account, so its poll and its actions
	// never follow a name to a newer account, and a creation request ID
	// moves to the AWS account ID.
	if id := pathAccounts + "/" + a.ID; id != r.URL.Path && isAccountPage(id) {
		http.Redirect(w, r, id, http.StatusFound)
		return
	}
	noCache(w)
	daily := spendView{Known: a.ProviderID == ""}
	if a.ProviderID != "" {
		// Opening the page is a request of a person, so spend that is not
		// known is pulled at once, also within the wait after a failed poll
		// pull. Only people force a pull, and the poll never does.
		_, known := s.svc.CostState(a.ProviderID, 30)
		costs, err := s.svc.Costs(r.Context(), a.ID, 30, !known)
		if err != nil {
			// The page says that spend is unavailable, never $0.00. The
			// raw error stays in the log.
			s.log.Warn("costs", "account", a.Name, "err", err)
		} else {
			daily = spendView{Known: true, Days: costs}
		}
	}
	// The account page uses no queue slips and no holds, so it reads only
	// what the shell needs. The view reads the cost cache after the pull, so
	// the first refresh reads the same spend and gets 204.
	pg := s.shellPage(r)
	pg.Asker = r.URL.Path
	v := s.viewOf(pg, a)
	s.render(w, r, accountPage(polling(pg), v, daily, s.accountHistory(r, a), s.accountHash(r.Context(), pg, &v)))
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
	pg := s.pageFor(r)
	pg.Landed = a.ID
	s.answer(w, r, pg, false, "Extended "+a.Name+" to "+extended.ExpiresAt.UTC().Format("Jan 2 15:04 MST")+".")
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
		slot := refusalSlot(r)
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
		slot := refusalSlot(r)
		markField(w, err, slot)
		s.render(w, r, errorFragment(slot, text))
		return
	}
	s.errorPage(w, r, errorView{Code: http.StatusBadRequest, Title: "Action refused", Message: text, Recovery: "Go back to Home and try again.", Back: "/", BackLabel: "Go to Home"})
}

// refusalSlot is the id of the slot that shows the refusal of an htmx
// request. On an account page the budget change of an admin fills the slot
// under its form, and every other refusal, such as +7d or Close, fills the
// slot of the account. Off the home page it is the slot under a form or
// under the page heading. On the home page the New request panel comes
// last, so only New request itself fills the slot under its form. An action
// on a request fills the slot of the request panel, and every other
// refusal, such as +7d, Close or Sync, fills the slot above the accounts.
func refusalSlot(r *http.Request) string {
	u, err := url.Parse(r.Header.Get("HX-Current-URL"))
	if err == nil && isAccountPage(u.Path) {
		if strings.HasSuffix(r.URL.Path, "/budget") {
			return "form-error"
		}
		return "account-error"
	}
	if err != nil || u.Path != "/" {
		return "form-error"
	}
	switch path := r.URL.Path; {
	case path == "/requests":
		return "form-error"
	case strings.HasPrefix(path, "/requests/"):
		return "pending-error"
	default:
		return "account-error"
	}
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

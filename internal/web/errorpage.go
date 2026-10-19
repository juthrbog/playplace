package web

import (
	"net/http"
)

// errorView is what a styled error page says: the title and the status code
// in the head, then a refusal note with the problem and the recovery, then
// one way back.
type errorView struct {
	Code      int
	Title     string
	Message   string
	Recovery  string
	Back      string
	BackLabel string
}

// The reasons a viewer is refused with 403. Each one names the rule that
// refused them.
const (
	notYourAccount = "that account belongs to someone else"
	notYourRequest = "that request belongs to someone else"
	approversOnly  = "only approvers can approve or deny requests"
	adminsEditOnly = "only admins can edit requests"
	cannotWithdraw = "only admins, the requester or the owner can withdraw this request"
)

// forbidRecovery is the recovery line of the 403 page for the reasons that
// have a better one than going home.
var forbidRecovery = map[string]string{
	notYourAccount: "Go back to Home to see your own accounts.",
	notYourRequest: "Go back to Home to see your own requests.",
}

// writeErrorPage writes the status and renders the page. The caller logs
// the error, because the OIDC handler has no Server.
func writeErrorPage(w http.ResponseWriter, r *http.Request, pg page, e errorView) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(e.Code)
	return errorPage(pg, e).Render(r.Context(), w)
}

// errorPage renders a styled error page. A signed-in viewer gets the full
// shell, which needs the queue count. Without a viewer, the shell shows
// only the brand.
func (s *Server) errorPage(w http.ResponseWriter, r *http.Request, e errorView) {
	var pg page
	if _, ok := r.Context().Value(viewerKey{}).(Viewer); ok {
		pg = s.pageFor(r)
	} else {
		pg = page{SignIn: s.auth.Enabled(), SignedOut: true, Limits: s.svc.Config(), Path: r.URL.Path}
	}
	if err := writeErrorPage(w, r, pg, e); err != nil {
		s.log.Error("render", "path", r.URL.Path, "err", err)
	}
}

// route serves r through the mux, but answers an unknown path with the
// styled 404 page. A known path with the wrong method keeps the mux 405 and
// its Allow header. The mux gives both an empty pattern, so route asks again
// with GET and POST, the only methods the routes use (GET also covers HEAD).
func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	if _, pattern := s.mux.Handler(r); pattern == "" && !s.knownPath(r) {
		s.errorPage(w, r, errorView{
			Code:      http.StatusNotFound,
			Title:     "Page not found",
			Message:   "There is no page at this address.",
			Recovery:  "Check the link, or go back to Home.",
			Back:      "/",
			BackLabel: "Go to Home",
		})
		return
	}
	s.mux.ServeHTTP(w, r)
}

// knownPath reports whether any route matches the path of r with another
// method.
func (s *Server) knownPath(r *http.Request) bool {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if method == r.Method {
			continue
		}
		probe := r.Clone(r.Context())
		probe.Method = method
		if _, pattern := s.mux.Handler(probe); pattern != "" {
			return true
		}
	}
	return false
}

// accountNotFound is the 404 page for an account reference that matches
// nothing.
func (s *Server) accountNotFound(w http.ResponseWriter, r *http.Request, ref string) {
	s.errorPage(w, r, errorView{
		Code:      http.StatusNotFound,
		Title:     "Account not found",
		Message:   "No account matches " + ref + ".",
		Recovery:  "Go back to Home to see the accounts you can open.",
		Back:      "/",
		BackLabel: "Go to Home",
	})
}

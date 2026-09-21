package web

import (
	"net/http"
	"net/url"
)

func (s *Server) budget(w http.ResponseWriter, r *http.Request) {
	v := ViewerFrom(r.Context())
	if !v.Admin {
		s.forbid(w, r, "only admins can change existing account budgets")
		return
	}
	a, ok := s.owned(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	amount, err := parseBudget(r.FormValue("budget"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := s.svc.SetBudget(r.Context(), a.ID, amount, v.Email, r.FormValue("reason"), r.FormValue("override") == "on"); err != nil {
		s.fail(w, r, err)
		return
	}
	destination := "/accounts/" + url.PathEscape(a.ID)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", destination)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, destination, http.StatusSeeOther)
}

package web

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"playplace/internal/core"
)

// The two decisions on a request. The decide route reads one of them from
// the query of its URL.
const (
	actionApprove = "approve"
	actionDeny    = "deny"
)

// The rules of one decision post. Each approval runs a forced inventory and
// CreateAccount, about 2 to 6 seconds, and a common proxy cuts an answer at
// 60 seconds. So a post approves at most 10 requests and stops taking new
// ones after 45 seconds. A denial creates nothing in AWS, so a post can deny
// 50. The body cap holds 50 refs with room to spare.
const (
	decideBodyLimit = 256 << 10
	maxApprovals    = 10
	maxDenials      = 50
	decideBudget    = 45 * time.Second
	maxReason       = 500
)

// decideRefLimit ends a decision on one request when AWS never answers a
// call. A normal approval takes 2 to 6 seconds, so only a hung call reaches
// the limit. The AWS SDK sets no time limit on an answer, and core holds
// its lock for the whole decision. Without the limit, one hung call stops
// every other decision and the refresh pass. Tests make it short.
var decideRefLimit = 2 * time.Minute

// The refusals of a decision form. A decision page sends none of them. They
// answer a form that a script or an old page built.
const (
	unknownDecision = "The form asks for an unknown decision, so playplace decided nothing."
	noRequests      = "The form names no request, so playplace decided nothing. Select at least one request."
	unreadableRef   = "A request in the form is not readable, so playplace decided nothing. Load the page again."
	unreadableForm  = "The form is not readable, so playplace decided nothing. Load the page again."
	formTooLarge    = "The form is too large, so playplace decided nothing. Select fewer requests."
)

// The refusals of a deny reason. The reason goes into the denied notice,
// into Slack and into history, so a denial needs one. refusal writes each
// one as sentences.
var (
	errNoReason         = errors.New("write the reason for the denial. playplace cannot deny a request without one")
	errLongReason       = fmt.Errorf("the reason can have at most %d characters", maxReason)
	errReasonCharacters = errors.New("the reason holds a hidden character, for example a control or text direction character. Type the reason again")
)

// decisionKind is the result of a decision on one request.
type decisionKind int

const (
	decidedApproved  decisionKind = iota // AWS creates the account
	decidedDenied                        // the request left the queue
	decisionGone                         // the request no longer waits
	decisionChanged                      // the request changed after the page showed it
	decisionOwn                          // the viewer requested it, or it names the viewer as the owner
	decisionNotTried                     // playplace stopped before the request
	decisionFailed                       // a call to AWS failed
	decisionRefused                      // core refused it for another reason
)

// decisionKindNames are the words of each kind in the log.
var decisionKindNames = [...]string{"approved", "denied", "gone", "changed", "own", "not-tried", "aws-failure", "refused"}

func (k decisionKind) String() string { return decisionKindNames[k] }

// decision is the result of one request of a decision post. Why is the
// reason that people read when the request was not decided.
type decision struct {
	Ref  core.RequestRef
	Kind decisionKind
	Why  string
	// Action is the decision that the post asked for: approve or deny.
	Action string
	// Req is the record that a decision that went through acted on. Its
	// decided slip shows it. It is empty when the queue held another
	// version of the request.
	Req core.Request
	// Reason is the reason of a denial that went through.
	Reason string
	// Shown are the values that the page showed for a request that the
	// post did not decide, from its shown fields. They name what changed,
	// and they place the slip of a request that left the queue.
	Shown shownValues
}

// done reports whether the decision went through.
func (d decision) done() bool { return d.Kind == decidedApproved || d.Kind == decidedDenied }

func (s *Server) approve(w http.ResponseWriter, r *http.Request) { s.decideOne(w, r, actionApprove) }
func (s *Server) deny(w http.ResponseWriter, r *http.Request)    { s.decideOne(w, r, actionDeny) }

// decideOne is the Approve or Deny button of one slip. It decides one ref:
// the name from the path and the version from the form. The checks run in
// the same order as on the decide route: the role, the form, the reason,
// then core.
// The prompt of the Deny button sends its answer in the HX-Prompt header.
func (s *Server) decideOne(w http.ResponseWriter, r *http.Request, action string) {
	r.Body = http.MaxBytesReader(w, r.Body, decideBodyLimit)
	if !ViewerFrom(r.Context()).Approver {
		s.forbid(w, r, approversOnly)
		return
	}
	if !s.readDecisionForm(w, r) {
		return
	}
	var reason string
	if action == actionDeny {
		raw := r.FormValue("reason")
		var err error
		if raw == "" {
			raw, err = promptAnswer(r.Header)
		}
		if err == nil {
			reason, err = denyReason(raw)
		}
		if err != nil {
			s.failRequest(w, r, err)
			return
		}
	}
	s.decide(w, r, action, []core.RequestRef{requestRef(r)}, reason)
}

// decideAll is POST /requests/decide. The query names the action. The body
// holds one req field for each request, as refValue writes it, and the
// reason of a denial. The checks run in order: the role, the form, the
// reason, then core. A refused form decides nothing.
func (s *Server) decideAll(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, decideBodyLimit)
	if !ViewerFrom(r.Context()).Approver {
		s.forbid(w, r, approversOnly)
		return
	}
	if !s.readDecisionForm(w, r) {
		return
	}
	action := r.URL.Query().Get("action")
	limit := map[string]int{actionApprove: maxApprovals, actionDeny: maxDenials}[action]
	if limit == 0 {
		s.refuseForm(w, r, http.StatusBadRequest, unknownDecision)
		return
	}
	var refs []core.RequestRef
	seen := map[string]bool{}
	for _, v := range r.PostForm["req"] {
		// Every request in the queue has a valid name, because core checks
		// the name when a request arrives. A name that is not valid is never
		// read from AWS, so a forged form costs no calls.
		ref, ok := parseRefValue(v)
		if !ok || core.ValidateName(ref.Name) != nil {
			s.refuseForm(w, r, http.StatusBadRequest, unreadableRef)
			return
		}
		// A second ref for the same name is dropped, so the first one counts.
		if !seen[ref.Name] {
			seen[ref.Name] = true
			refs = append(refs, ref)
		}
	}
	switch {
	case len(refs) == 0:
		s.refuseForm(w, r, http.StatusBadRequest, noRequests)
		return
	case len(refs) > limit:
		s.refuseForm(w, r, http.StatusBadRequest, fmt.Sprintf("You can %s at most %d requests at once, so playplace decided nothing.", action, limit))
		return
	}
	var reason string
	if action == actionDeny {
		var err error
		if reason, err = denyReason(r.PostForm.Get("reason")); err != nil {
			s.failRequest(w, r, err)
			return
		}
	}
	s.decide(w, r, action, refs, reason)
}

// readDecisionForm parses the form of a decision. The caller caps the body
// first, so a body over the cap answers 413. A body that does not parse
// answers 400. Both decide nothing.
func (s *Server) readDecisionForm(w http.ResponseWriter, r *http.Request) bool {
	err := r.ParseForm()
	if err == nil {
		return true
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		s.refuseForm(w, r, http.StatusRequestEntityTooLarge, formTooLarge)
	} else {
		s.refuseForm(w, r, http.StatusBadRequest, unreadableForm)
	}
	return false
}

// refuseForm answers a decision form that playplace refused as a whole. The
// htmx answer keeps its status and fills the refusal slot of the queue
// panel, with a hidden copy in the region. app.js swaps it because it asks
// for no main swap. A page visit gets the styled error page.
func (s *Server) refuseForm(w http.ResponseWriter, r *http.Request, code int, why string) {
	s.log.Warn("decision form refused", "path", r.URL.Path, "status", code, "why", why)
	if r.Header.Get("HX-Request") != "true" {
		s.errorPage(w, r, errorView{Code: code, Title: "Action refused", Message: why, Recovery: "Go back to Home and try again.", Back: "/", BackLabel: "Go to Home"})
		return
	}
	w.Header().Set("HX-Reswap", "none")
	w.Header().Set("X-Form-Error", "1")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	if err := errorFragment("pending-error", why).Render(r.Context(), w); err != nil {
		s.log.Error("render", "path", r.URL.Path, "err", err)
	}
}

// promptAnswer reads the answer of an htmx prompt from the HX-Prompt header.
// A header holds bytes, so htmx sends an answer with no character above
// U+00FF as Latin-1, one byte for each character. Each byte is then the
// rune of the same value. htmx sends any other answer as encodeURIComponent
// writes it, and marks it with the HX-Prompt-URI-AutoEncoded header. An
// escape that does not decode gets the refusal of a hidden character.
func promptAnswer(h http.Header) (string, error) {
	v := h.Get("HX-Prompt")
	if h.Get("HX-Prompt-URI-AutoEncoded") == "true" {
		answer, err := url.PathUnescape(v)
		if err != nil {
			return "", fieldError{"reason", errReasonCharacters}
		}
		return answer, nil
	}
	var b strings.Builder
	for i := range len(v) {
		b.WriteRune(rune(v[i]))
	}
	return b.String(), nil
}

// denyReason reads the reason of a denial. It folds every run of spaces and
// line breaks into one space, so the notice and Slack show one line. The
// reason must then hold 1 to maxReason characters, counted as runes. It must
// be valid UTF-8 with no control characters and no bidi controls. A bidi
// control can change the order of the words in the notice and in history.
func denyReason(raw string) (string, error) {
	reason := strings.Join(strings.Fields(raw), " ")
	switch n := utf8.RuneCountInString(reason); {
	case n == 0:
		return "", fieldError{"reason", errNoReason}
	case n > maxReason:
		return "", fieldError{"reason", errLongReason}
	case !utf8.ValidString(reason) || strings.ContainsFunc(reason, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) }):
		return "", fieldError{"reason", errReasonCharacters}
	}
	return reason, nil
}

// decide makes the decision action on each ref, oldest request first, and
// answers with every result. One result never stops the rest, with two
// exceptions. After the first failed call to AWS, the rest are not tried,
// because each one repeats the outage with a forced inventory. After
// decideBudget, the rest are not tried, so the answer reaches the browser
// before a proxy cuts it. The budget counts from the start of the post.
//
// The context of a decision ignores the cancellation of the request, so a
// browser that goes away never cuts a decision. A cut call can write the
// claim in AWS while core sees an error, and then nothing puts the request
// back. decideRefLimit ends only a call that AWS never answers. A deadline
// error is a failed call to AWS, so the rest are then not tried. Core
// restores a failed approval with a context of its own.
func (s *Server) decide(w http.ResponseWriter, r *http.Request, action string, refs []core.RequestRef, reason string) {
	start := s.svc.Now()
	pg := s.pageFor(r)
	// The queue lists the oldest request first. A ref that the queue does
	// not list goes last, in the order of the form.
	at := map[string]int{}
	for i, q := range pg.Pending {
		at[q.Name] = i
	}
	place := func(ref core.RequestRef) int {
		if i, ok := at[ref.Name]; ok {
			return i
		}
		return len(pg.Pending)
	}
	refs = slices.Clone(refs)
	slices.SortStableFunc(refs, func(a, b core.RequestRef) int { return cmp.Compare(place(a), place(b)) })
	ctx := context.WithoutCancel(r.Context())
	stopped := false
	results := make([]decision, 0, len(refs))
	for _, ref := range refs {
		if !stopped && s.svc.Now().Sub(start) >= decideBudget {
			stopped = true
		}
		var d decision
		if stopped {
			d = decision{Ref: ref, Kind: decisionNotTried, Why: notTried(action)}
		} else {
			rctx, cancel := context.WithTimeout(ctx, decideRefLimit)
			d = s.decideRef(rctx, r, pg, action, ref, reason)
			cancel()
			stopped = d.Kind == decisionFailed
		}
		d.Action = action
		if !d.done() {
			d.Shown = readShown(r.PostForm, ref.Name)
		}
		s.log.Info("decision", "action", action, "name", ref.Name, "version", ref.Version, "result", d.Kind.String(), "by", pg.Viewer.Email)
		results = append(results, d)
	}
	s.answerDecisions(w, r, action, results)
}

// decideRef makes the decision action on one ref. The viewer is an
// approver, so canApprove refuses only through CheckApprover. The rule
// holds for a denial too. The queue of pg comes through the cache, so a
// request that it does not list is read live.
//
// The rule runs here only on the version that the viewer saw. A request of
// another version changed after the page showed it, so core refuses it as
// changed, and the viewer gets the new values. Core Approve applies the rule
// again to the live record.
func (s *Server) decideRef(ctx context.Context, r *http.Request, pg page, action string, ref core.RequestRef, reason string) decision {
	var q core.Request
	var err error
	if i := slices.IndexFunc(pg.Pending, func(p core.Request) bool { return p.Name == ref.Name }); i >= 0 {
		q = pg.Pending[i]
	} else {
		q, err = s.svc.GetRequest(ctx, ref.Name)
	}
	if err == nil && q.Version() == ref.Version {
		if self := core.CheckApprover(q, pg.Viewer.Email, pg.Limits.AllowSelfApproval); self != nil {
			return undecided(action, ref, self)
		}
	}
	if err == nil {
		if action == actionApprove {
			_, err = s.svc.Approve(ctx, ref, pg.Viewer.Email)
		} else {
			err = s.svc.Deny(ctx, ref, pg.Viewer.Email, reason)
		}
	}
	switch {
	case err != nil:
		s.log.Warn("decision failed", "path", r.URL.Path, "action", action, "name", ref.Name, "err", err)
		return undecided(action, ref, err)
	case action == actionApprove:
		return decision{Ref: ref, Kind: decidedApproved, Req: decidedRecord(q, ref)}
	default:
		return decision{Ref: ref, Kind: decidedDenied, Req: decidedRecord(q, ref), Reason: reason}
	}
}

// decidedRecord is q when it is the version that the decision acted
// on, and an empty record otherwise. A cached record of another
// version holds values that nobody decided, so it gets no slip.
func decidedRecord(q core.Request, ref core.RequestRef) core.Request {
	if q.Version() == ref.Version {
		return q
	}
	return core.Request{}
}

// undecided is the result of a ref that err refused. The checks run in the
// order of refusal, so the kind and the text always agree.
func undecided(action string, ref core.RequestRef, err error) decision {
	var self core.SelfApprovalError
	var failed *core.ProviderError
	var coded awsCoded
	d := decision{Ref: ref, Why: refusal(err, ref.Name)}
	switch {
	case errors.Is(err, core.ErrRequestChanged):
		d.Kind = decisionChanged
	case errors.As(err, &self):
		d.Kind, d.Why = decisionOwn, ownRequest(action, self.WouldOwn)
	case errors.Is(err, core.ErrNotFound):
		d.Kind = decisionGone
	case errors.As(err, &failed), errors.As(err, &coded), errors.Is(err, context.DeadlineExceeded):
		d.Kind = decisionFailed
	default:
		d.Kind = decisionRefused
	}
	return d
}

// ownRequest is the reason of a ref that the viewer requested or that names
// the viewer as the owner. The page shows no buttons for such a request, so
// only a forged post gets it in the web. Slack sends it for a click.
func ownRequest(action string, wouldOwn bool) string {
	who := "You requested it"
	if wouldOwn {
		who = "It names you as the owner"
	}
	if action == actionDeny {
		return "Not denied. " + who + ", so another approver must decide."
	}
	return "Not approved. " + who + ", so another approver must approve it."
}

// notTried is the reason of a ref that playplace did not try.
func notTried(action string) string {
	if action == actionDeny {
		return "Not denied. playplace stopped before this request. Deny it again."
	}
	return "Not approved. playplace stopped before this request. Approve it again."
}

// answerDecisions answers a decision post with every result. The
// queue is the main swap, as for one slip. A request that went through
// keeps a slip with its stamp, and a request that did not shows the
// reason in its slip. A request that left the queue keeps a gone slip
// in its place. With no place, it gets a line in the bulk bar. The
// region says how many went through. When none did, the answer is
// a refusal: one request gets a hidden copy of its reason, and several
// get the sentence. A failed account list shows its failed panel, so
// the results still reach the page. A page visit that decided nothing
// gets the styled page.
func (s *Server) answerDecisions(w http.ResponseWriter, r *http.Request, action string, results []decision) {
	done, stale := 0, false
	for _, d := range results {
		if d.done() {
			done++
		}
		stale = stale || d.Kind == decisionGone || d.Kind == decisionChanged
	}
	said := decisionSentence(action, results)
	// A request that changed or left makes the queue read live, so the
	// answer shows what waits now.
	if stale {
		s.svc.Invalidate()
	}
	if done == 0 && r.Header.Get("HX-Request") != "true" {
		s.refusePage(w, r, action, results)
		return
	}
	pg := s.pageFor(r)
	pg.Results = results
	rows, err := s.rows(r.Context(), pg.Viewer, pg.ShowAll, false)
	var errMsg string
	if err != nil {
		s.log.Warn("accounts", "path", r.URL.Path, "err", err)
		rows, errMsg = nil, listFailure(err)
	}
	region := noticeFragment("check", said)
	if done == 0 {
		w.Header().Set("X-Form-Error", "1")
		region = noticeFragment("alert", said)
		if len(results) == 1 {
			region = hiddenNotice(pg.resultText(results[0]))
		}
	}
	s.writeAccounts(w, r, pg, rows, errMsg, s.pollHash(r.Context(), pg, rows, errMsg), true, region)
}

// refusePage answers a page visit whose decision post decided
// nothing. The home page cannot show the results, so the styled page
// names the reason of each request.
func (s *Server) refusePage(w http.ResponseWriter, r *http.Request, action string, left []decision) {
	msg := left[0].Why
	if len(left) > 1 {
		verb := "approved"
		if action == actionDeny {
			verb = "denied"
		}
		parts := []string{"No requests were " + verb + "."}
		for _, d := range left {
			parts = append(parts, d.Ref.Name+": "+d.Why)
		}
		msg = strings.Join(parts, " ")
	}
	s.errorPage(w, r, errorView{Code: http.StatusBadRequest, Title: "Action refused", Message: msg, Recovery: "Go back to Home and try again.", Back: "/", BackLabel: "Go to Home"})
}

// decisionSentence is what the region says after a decision post. One
// request that went through gets its name. A post that decided some of its
// requests says how many, and that the queue shows why the rest were not
// decided.
func decisionSentence(action string, results []decision) string {
	n, done := len(results), 0
	for _, d := range results {
		if d.done() {
			done++
		}
	}
	verb, past := "approved", "Approved"
	if action == actionDeny {
		verb, past = "denied", "Denied"
	}
	switch {
	case done == 0:
		return "No requests were " + verb + ". The queue shows why."
	case n == 1 && action == actionApprove:
		return "Approved " + results[0].Ref.Name + ". AWS is creating the account."
	case n == 1:
		return "Denied " + results[0].Ref.Name + "."
	}
	var b strings.Builder
	if done == n {
		b.WriteString(past + " " + countOf(n, "request") + ".")
	} else {
		b.WriteString(past + " " + strconv.Itoa(done) + " of " + countOf(n, "request") + ".")
	}
	if action == actionApprove {
		b.WriteString(" AWS is creating " + countOf(done, "account") + ".")
	}
	if left := n - done; left > 0 {
		was := "were"
		if left == 1 {
			was = "was"
		}
		b.WriteString(" The queue shows why " + countOf(left, "request") + " " + was + " not " + verb + ".")
	}
	return b.String()
}

// countOf writes n and the noun, with the plural s when n is not 1.
func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

package web

import (
	"log/slog"
	"sync"
	"time"

	"playplace/internal/core"
)

// problemTime is the layout of a time in the words of a problem. It is the
// UTC layout of the pages.
const problemTime = "2006-01-02 15:04 UTC"

// The texts of a problem that has no words of its own. The raw text of
// LastError holds AWS states and RFC 3339 times, so pages show these words
// and the log holds the raw text.
const (
	problemUnknown = "Something went wrong with this account. An admin can see the details in the log."
	stateUnknown   = "AWS reports a state for this account that playplace does not expect. Ask an admin to check it in AWS."
	createdNoHint  = "An admin can see the reason in the log."
	createdNoWhy   = "AWS gave no reason. An admin can check the request in AWS."
)

// stateWords are the AWS account states that a page names, in plain words.
// ACTIVE, CLOSED and PENDING_CLOSURE are never a problem of their own.
var stateWords = map[string]string{
	"SUSPENDED":          "suspended",
	"PENDING_ACTIVATION": "waiting for activation",
}

// problemView is a problem of an account in the words of the pages. Wait
// marks a placement wait, which pages show as a qualifier and not as a
// failure.
type problemView struct {
	Text string
	Wait bool
}

// accountProblem is the problem of a, in plain words, and false when a has
// none. A LastError with no typed problem gets the generic text, because
// its raw text is for the log only.
func accountProblem(a *core.Account) (problemView, bool) {
	p, ok := a.Problem()
	if !ok {
		if a.LastError == "" {
			return problemView{}, false
		}
		return problemView{Text: problemUnknown}, true
	}
	return problemWords(p), true
}

// problemWords is the text of one typed problem.
func problemWords(p core.Problem) problemView {
	switch p.Kind {
	case core.ProblemPlacementWait:
		return problemView{Text: "Waiting for AWS to place the account since " + p.Since.UTC().Format(problemTime) + ".", Wait: true}
	case core.ProblemCreationFailed:
		advice := core.FailureAdvice(p.Reason)
		switch {
		case advice != "":
		case p.Reason == "":
			advice = createdNoWhy
		default:
			advice = createdNoHint
		}
		return problemView{Text: "AWS could not create the account. " + advice}
	case core.ProblemStateUnexpected:
		word, ok := stateWords[p.State]
		if !ok {
			return problemView{Text: stateUnknown}
		}
		return problemView{Text: "AWS reports this account as " + word + ", not closed. Ask an admin to check it in AWS."}
	case core.ProblemClosureUnconfirmed:
		return problemView{Text: "AWS has not confirmed the closure since " + p.Since.UTC().Format(problemTime) + ". Charges may continue."}
	}
	return problemView{Text: problemUnknown}
}

// problemLog writes the raw problem of each account to the log, once for
// each new text, so the log holds what the pages leave out. The pages poll,
// so a log line on every render would repeat the same text every few
// seconds.
type problemLog struct {
	mu   sync.Mutex
	seen map[string]string // account ID to the last text written
}

// note logs the raw problem of a when it changed since the last note, and
// forgets an account whose problem is gone.
func (l *problemLog) note(log *slog.Logger, a *core.Account) {
	p, typed := a.Problem()
	text := a.LastError
	if typed && text == "" {
		// A failed creation with no reason has no LastError.
		text = string(p.Kind)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if text == "" {
		delete(l.seen, a.ID)
		return
	}
	if l.seen[a.ID] == text {
		return
	}
	if l.seen == nil {
		l.seen = map[string]string{}
	}
	l.seen[a.ID] = text
	attrs := []any{"account", a.Name, "id", a.ID, "status", string(a.Status), "last_error", text}
	if typed {
		attrs = append(attrs, "kind", string(p.Kind))
		if p.State != "" {
			attrs = append(attrs, "provider_state", p.State)
		}
		if p.Reason != "" {
			attrs = append(attrs, "reason", p.Reason)
		}
		if !p.Since.IsZero() {
			attrs = append(attrs, "since", p.Since.UTC().Format(time.RFC3339))
		}
	}
	log.Warn("account problem", attrs...)
}

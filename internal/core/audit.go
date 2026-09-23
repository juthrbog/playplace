package core

import (
	"context"
	"crypto/rand"
	"time"
)

// AuditEvent is one lifecycle happening, written to the history sink. The
// tool never reads history to make decisions, so the sink is a log, not
// state: losing it costs the timeline and nothing else.
type AuditEvent struct {
	ID        string            `json:"id,omitempty"`
	JourneyID string            `json:"journey_id,omitempty"`
	At        time.Time         `json:"ts"`
	Event     string            `json:"event"`   // requested, approved, denied, ...
	Account   string            `json:"account"` // account name
	AccountID string            `json:"account_id,omitempty"`
	Owner     string            `json:"owner,omitempty"`
	Actor     string            `json:"actor"` // who did it, or "system" for the refresh pass
	Via       string            `json:"via,omitempty"`
	Message   string            `json:"message"` // one human-readable line
	Details   map[string]string `json:"details,omitempty"`
}

// NewHistoryID returns an opaque identity independent of account names and clocks.
func NewHistoryID() string { return rand.Text() }

// HistoryID correlates a request and its resulting account. For an existing
// account without a journey tag, only the provider account ID is a safe origin;
// never infer a request journey from a name. Stores must namespace deployments.
func (a *Account) HistoryID() string {
	if a.JourneyID != "" {
		return a.JourneyID
	}
	if a.ProviderID != "" {
		return "account:" + a.ProviderID
	}
	return ""
}

// Auditor receives events. Implementations must not block for long and
// should swallow their own failures; the service logs and moves on.
type Auditor interface {
	Record(ctx context.Context, e AuditEvent) error
}

// DiscardAuditor drops every event.
type DiscardAuditor struct{}

func (DiscardAuditor) Record(context.Context, AuditEvent) error { return nil }

// ActorSystem names the refresh pass as an actor.
const ActorSystem = "system"

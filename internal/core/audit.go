package core

import (
	"context"
	"time"
)

// AuditEvent is one lifecycle happening, written to the history sink. The
// tool never reads history to make decisions, so the sink is a log, not
// state: losing it costs the timeline and nothing else.
type AuditEvent struct {
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

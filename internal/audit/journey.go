package audit

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrNotFound    = errors.New("history record not found")
	ErrConflict    = errors.New("history identity or revision conflict")
	ErrCorrupt     = errors.New("history record corrupt")
	ErrUnavailable = errors.New("history store unavailable")
)

// Namespace is mandatory even for single-organization deployments. Values are
// exact, case-sensitive identifiers, not display names or authorization claims.
type Namespace struct {
	Organization string
	Environment  string
}

type JourneyOrigin string

const (
	OriginRequest  JourneyOrigin = "request"
	OriginDirect   JourneyOrigin = "direct"
	OriginAdoption JourneyOrigin = "adoption"
)

// Correlation is an exact identity. An operation ID is allocated before a
// mutation; a provider request ID is bound only after a reliable response.
// Request submissions use their journey ID, never their reusable queue tag/name.
type Correlation struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

const (
	CorrelationOperation       = "operation"
	CorrelationProviderRequest = "provider-request"
	CorrelationAccount         = "account"
)

// OwnershipObservation records authoritative evidence, not an event's owner
// label. ObservedAt establishes only when ownership was observed, not when an
// unobserved transfer happened. Gaps must not become inferred ownership periods.
// Source is request-queue or provider-tags; Reference is the journey ID or an
// exactly correlated provider account ID respectively. An empty Owner records
// an observation that ownership could not be established (fail closed).
type OwnershipObservation struct {
	ObservedAt time.Time `json:"observed_at"`
	Owner      string    `json:"owner"`
	Source     string    `json:"source"`
	Reference  string    `json:"reference"`
}

// TerminalOutcome accepts only confirmed closure or removal of an unfulfilled
// request. An expiry, closure intent, missing account, or failure is not terminal.
// Recording this evidence does not implement retention or authorize deletion.
type TerminalOutcome struct {
	Kind      string    `json:"kind"`
	At        time.Time `json:"at"`
	Reference string    `json:"reference"`
}

// Journey is history metadata, never a source of lifecycle guardrail facts.
// Correlations and observations are append-only; origin/start and an accepted
// terminal outcome are immutable. Revision provides optimistic concurrency.
// No producer may infer ownership evidence from a self-reported actor label.
type Journey struct {
	ID           string                 `json:"id"`
	Revision     uint64                 `json:"revision"`
	Origin       JourneyOrigin          `json:"origin"`
	Name         string                 `json:"name"`
	StartedAt    time.Time              `json:"started_at"`
	Correlations []Correlation          `json:"correlations,omitempty"`
	Ownership    []OwnershipObservation `json:"ownership,omitempty"`
	Terminal     *TerminalOutcome       `json:"terminal,omitempty"`
}

func validIdentity(s string) bool {
	return strings.TrimSpace(s) != "" && len(s) <= 256 && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n")
}

func validTime(t time.Time) bool {
	return !t.IsZero() && t.UTC().Year() >= 1 && t.UTC().Year() <= 9999
}

func (c Correlation) validate() error {
	if !validIdentity(c.ID) {
		return errors.New("history correlation requires an exact ID")
	}
	switch c.Kind {
	case CorrelationOperation, CorrelationProviderRequest, CorrelationAccount:
		return nil
	default:
		return errors.New("unknown history correlation kind")
	}
}

func (j Journey) has(c Correlation) bool {
	for _, existing := range j.Correlations {
		if existing == c {
			return true
		}
	}
	return false
}

func (j Journey) validate() error {
	if !validIdentity(j.ID) || !validIdentity(j.Name) || !validTime(j.StartedAt) {
		return errors.New("journey requires ID, name and start time")
	}
	switch j.Origin {
	case OriginRequest, OriginDirect, OriginAdoption:
	default:
		return errors.New("unknown journey origin")
	}
	// Keep transactions below DynamoDB's 100-action limit, including metadata.
	if len(j.Correlations) > 90 {
		return errors.New("too many journey correlations")
	}
	seen := map[Correlation]bool{}
	accountCount := 0
	for _, c := range j.Correlations {
		if err := c.validate(); err != nil {
			return err
		}
		if seen[c] {
			return errors.New("duplicate journey correlation")
		}
		seen[c] = true
		if c.Kind == CorrelationAccount {
			accountCount++
		}
	}
	if accountCount > 1 {
		return errors.New("a journey cannot join multiple accounts")
	}
	last := j.StartedAt
	for _, o := range j.Ownership {
		if !validTime(o.ObservedAt) || o.ObservedAt.Before(last) || (o.Owner != "" && !validIdentity(o.Owner)) {
			return errors.New("invalid or out-of-order ownership observation")
		}
		switch o.Source {
		case "request-queue":
			if j.Origin != OriginRequest || o.Reference != j.ID {
				return errors.New("ownership requires exact request identity")
			}
		case "provider-tags":
			if !j.has(Correlation{CorrelationAccount, o.Reference}) {
				return errors.New("ownership requires exact account identity")
			}
		default:
			return errors.New("ownership requires an authoritative source")
		}
		last = o.ObservedAt
	}
	if t := j.Terminal; t != nil {
		if !validTime(t.At) || t.At.Before(j.StartedAt) {
			return errors.New("invalid terminal time")
		}
		switch t.Kind {
		case "account-closed":
			if !j.has(Correlation{CorrelationAccount, t.Reference}) {
				return errors.New("closure requires exact account identity")
			}
		case "request-denied", "request-withdrawn", "request-expired":
			if j.Origin != OriginRequest || t.Reference != j.ID || accountCount != 0 {
				return errors.New("request termination requires an unfulfilled request journey")
			}
		default:
			return errors.New("unconfirmed outcome is not terminal")
		}
	}
	return nil
}

func (j Journey) follows(old Journey) error {
	if j.ID != old.ID || j.Origin != old.Origin || !j.StartedAt.Equal(old.StartedAt) ||
		len(j.Correlations) < len(old.Correlations) || len(j.Ownership) < len(old.Ownership) {
		return fmt.Errorf("%w: immutable journey identity/evidence", ErrConflict)
	}
	for i, c := range old.Correlations {
		if j.Correlations[i] != c {
			return fmt.Errorf("%w: correlation changed", ErrConflict)
		}
	}
	for i, o := range old.Ownership {
		n := j.Ownership[i]
		if !n.ObservedAt.Equal(o.ObservedAt) || n.Owner != o.Owner || n.Source != o.Source || n.Reference != o.Reference {
			return fmt.Errorf("%w: ownership evidence changed", ErrConflict)
		}
	}
	if old.Terminal != nil && (j.Terminal == nil || j.Terminal.Kind != old.Terminal.Kind ||
		!j.Terminal.At.Equal(old.Terminal.At) || j.Terminal.Reference != old.Terminal.Reference ||
		len(j.Correlations) != len(old.Correlations)) {
		return fmt.Errorf("%w: terminal journey changed", ErrConflict)
	}
	return nil
}

// Package core holds the domain model and lifecycle rules for playground
// accounts. There is no local state: the provider's playground OU and the
// tags on each account are the source of truth, and every Account here is
// derived from them.
package core

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// Status is the derived lifecycle state of a playground account.
type Status string

const (
	StatusPending     Status = "pending"     // requested, waiting for an approver
	StatusCreating    Status = "creating"    // provider is still creating it
	StatusActive      Status = "active"      // in the OU, usable
	StatusExpiring    Status = "expiring"    // owner has been warned
	StatusClosing     Status = "closing"     // close requested, waiting for confirmed closure
	StatusClosed      Status = "closed"      // provider explicitly reports CLOSED
	StatusUnavailable Status = "unavailable" // suspended, pending activation, or unknown provider state
	StatusFailed      Status = "failed"      // creation failed, see LastError
)

// IsOpen reports whether the account exists (or may exist) at the provider.
func (s Status) IsOpen() bool { return s != StatusClosed && s != StatusFailed }

// CanExtend reports whether the expiry can be pushed out.
func (s Status) CanExtend() bool { return s == StatusActive || s == StatusExpiring }

// CanClose reports whether a close can be requested.
func (s Status) CanClose() bool { return s == StatusActive || s == StatusExpiring }

// Tag keys written to the provider. Together they are the whole state.
const (
	TagManaged        = "playplace:managed"          // "true"
	TagOwner          = "playplace:owner"            // who is responsible
	TagExpires        = "playplace:expires"          // RFC3339
	TagBudget         = "playplace:budget"           // monthly USD, e.g. "50"
	TagWarnedAt       = "playplace:warned-at"        // RFC3339, set once the owner was warned
	TagCloseRequested = "playplace:close-requested"  // RFC3339, close intent
	TagClosedAt       = "playplace:closed-at"        // RFC3339, closure observed and reported
	TagCloseAlertedAt = "playplace:close-alerted-at" // RFC3339, overdue closure warning sent
	TagAccess         = "playplace:access"           // principal id the owner's console access was granted to
	TagRequestedBy    = "playplace:requested-by"     // who asked for the account
	TagApprovedBy     = "playplace:approved-by"      // who approved it
	TagPurpose        = "playplace:purpose"          // short free text from the request
)

// Account is a playground account as derived from the provider.
type Account struct {
	ID               string     `json:"id"`                    // provider id, or request id while creating or failed
	ProviderID       string     `json:"provider_id,omitempty"` // empty until creation succeeds
	RequestID        string     `json:"request_id,omitempty"`  // provider-side creation request id
	Name             string     `json:"name"`
	Email            string     `json:"email,omitempty"`
	Owner            string     `json:"owner"`
	Status           Status     `json:"status"`
	BudgetUSD        float64    `json:"budget_usd"`
	BudgetHealth     string     `json:"budget_health,omitempty"`
	BudgetPolicy     string     `json:"budget_policy,omitempty"`
	BudgetError      string     `json:"budget_error,omitempty"`
	BudgetRecovery   string     `json:"-"`
	BudgetChecked    string     `json:"budget_checked,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	ExpiresAt        time.Time  `json:"expires_at"`
	WarnedAt         *time.Time `json:"warned_at,omitempty"`
	CloseRequestedAt *time.Time `json:"close_requested_at,omitempty"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
	CloseAlertedAt   *time.Time `json:"close_alerted_at,omitempty"`
	ProviderState    string     `json:"provider_state,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	Managed          bool       `json:"managed"`             // has our tags
	AccessGrantedTo  string     `json:"access_to,omitempty"` // identity store principal id, empty until granted
	RequestedBy      string     `json:"requested_by,omitempty"`
	ApprovedBy       string     `json:"approved_by,omitempty"`
	Purpose          string     `json:"purpose,omitempty"`

	// Set on pending rows only, from the request record, so views can show
	// and edit the asked-for lifetime without a second read of the queue.
	TTL            time.Duration `json:"ttl,omitempty"`
	OverrideLimits bool          `json:"override_limits,omitempty"`
}

// Tags renders the account's lifecycle facts as provider tags.
func (a *Account) Tags() map[string]string {
	t := map[string]string{
		TagManaged: "true",
		TagOwner:   a.Owner,
		TagExpires: a.ExpiresAt.UTC().Format(time.RFC3339),
		TagBudget:  strconv.FormatFloat(a.BudgetUSD, 'f', -1, 64),
	}
	for key, value := range map[string]string{TagBudgetHealth: a.BudgetHealth, TagBudgetPolicy: a.BudgetPolicy, TagBudgetRecovery: a.BudgetRecovery, TagBudgetChecked: a.BudgetChecked, TagBudgetError: base64.RawURLEncoding.EncodeToString([]byte(a.BudgetError))} {
		if value != "" {
			t[key] = value
		}
	}
	if a.WarnedAt != nil {
		t[TagWarnedAt] = a.WarnedAt.UTC().Format(time.RFC3339)
	}
	if a.CloseRequestedAt != nil {
		t[TagCloseRequested] = a.CloseRequestedAt.UTC().Format(time.RFC3339)
	}
	if a.ClosedAt != nil {
		t[TagClosedAt] = a.ClosedAt.UTC().Format(time.RFC3339)
	}
	if a.CloseAlertedAt != nil {
		t[TagCloseAlertedAt] = a.CloseAlertedAt.UTC().Format(time.RFC3339)
	}
	if a.AccessGrantedTo != "" {
		t[TagAccess] = a.AccessGrantedTo
	}
	if a.RequestedBy != "" {
		t[TagRequestedBy] = a.RequestedBy
	}
	if a.ApprovedBy != "" {
		t[TagApprovedBy] = a.ApprovedBy
	}
	if a.Purpose != "" {
		t[TagPurpose] = a.Purpose
	}
	return t
}

// FromRemote derives an Account from a provider account and its tags.
// Accounts without our tags are reported with Managed=false so the refresh
// pass can adopt them.
func FromRemote(r RemoteAccount, defaults Config, now time.Time) *Account {
	a := &Account{
		ID:            r.ProviderID,
		ProviderID:    r.ProviderID,
		Name:          r.Name,
		Email:         r.Email,
		Owner:         r.Tags[TagOwner],
		CreatedAt:     r.JoinedAt,
		Managed:       r.Tags[TagManaged] == "true",
		ProviderState: r.Status,
	}
	if a.Owner == "" {
		a.Owner = "unknown"
	}
	if t, err := time.Parse(time.RFC3339, r.Tags[TagExpires]); err == nil {
		a.ExpiresAt = t
	} else {
		a.ExpiresAt = now.Add(defaults.DefaultTTL)
		a.Managed = false
	}
	if b, err := strconv.ParseFloat(r.Tags[TagBudget], 64); err == nil && b > 0 {
		a.BudgetUSD = b
	} else {
		a.BudgetUSD = defaults.DefaultBudgetUSD
	}
	if t, err := time.Parse(time.RFC3339, r.Tags[TagWarnedAt]); err == nil {
		a.WarnedAt = &t
	}
	if t, err := time.Parse(time.RFC3339, r.Tags[TagCloseRequested]); err == nil {
		a.CloseRequestedAt = &t
	}
	if t, err := time.Parse(time.RFC3339, r.Tags[TagClosedAt]); err == nil {
		a.ClosedAt = &t
	}
	if t, err := time.Parse(time.RFC3339, r.Tags[TagCloseAlertedAt]); err == nil {
		a.CloseAlertedAt = &t
	}
	a.BudgetHealth = r.Tags[TagBudgetHealth]
	a.BudgetPolicy = r.Tags[TagBudgetPolicy]
	a.BudgetRecovery = r.Tags[TagBudgetRecovery]
	a.BudgetChecked = r.Tags[TagBudgetChecked]
	if data, err := base64.RawURLEncoding.DecodeString(r.Tags[TagBudgetError]); err == nil {
		a.BudgetError = string(data)
	}
	a.AccessGrantedTo = r.Tags[TagAccess]
	a.RequestedBy = r.Tags[TagRequestedBy]
	a.ApprovedBy = r.Tags[TagApprovedBy]
	a.Purpose = r.Tags[TagPurpose]
	switch {
	case r.Status == "CLOSED":
		a.Status = StatusClosed
	case r.Status == "PENDING_CLOSURE":
		a.Status = StatusClosing
	case r.Status != "ACTIVE":
		a.Status = StatusUnavailable
		a.LastError = fmt.Sprintf("provider state %q is not confirmed closure; investigate with AWS", r.Status)
	case a.CloseRequestedAt != nil:
		a.Status = StatusClosing
	case a.WarnedAt != nil:
		a.Status = StatusExpiring
	default:
		a.Status = StatusActive
	}
	if a.Status != StatusClosed && a.CloseRequestedAt != nil && defaults.CloseAlertAfter > 0 && now.Sub(*a.CloseRequestedAt) >= defaults.CloseAlertAfter {
		a.LastError = fmt.Sprintf("closure unconfirmed since %s (provider state %q); charges may continue", a.CloseRequestedAt.UTC().Format(time.RFC3339), r.Status)
	}
	return a
}

// FromCreateRequest derives an Account for a creation that has not landed
// in the OU yet. Owner and budget are unknown until the account exists.
func FromCreateRequest(r CreateRequest) *Account {
	a := &Account{
		ID:        r.RequestID,
		RequestID: r.RequestID,
		Name:      r.Name,
		Owner:     "?",
		CreatedAt: r.RequestedAt,
		Status:    StatusCreating,
		Managed:   true,
	}
	if r.State == CreateFailed {
		a.Status = StatusFailed
		a.LastError = r.FailureReason
	}
	return a
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,48}[a-z0-9]$`)

// ValidateName checks that a name is safe to use in emails, tags, and URLs.
func ValidateName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("name %q must be 3-50 chars of lowercase letters, digits, and dashes", name)
	}
	return nil
}

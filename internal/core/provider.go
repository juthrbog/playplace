package core

import (
	"context"
	"errors"
	"time"
)

// OrgInfo describes the provider-side organization the tool manages accounts in.
type OrgInfo struct {
	OrgID               string
	ManagementAccountID string
	RootID              string
	PlaygroundOUID      string
	Created             bool // true if EnsureOrganization created the org on this call
}

// AccountRequest is what the provider needs to start creating an account.
// Tags are applied to the account once it exists.
type AccountRequest struct {
	Name  string
	Email string
	Tags  map[string]string
}

// CreateState is the provider's view of an account creation request.
type CreateState string

const (
	CreateInProgress CreateState = "in_progress"
	CreateSucceeded  CreateState = "succeeded"
	CreateFailed     CreateState = "failed"
)

// CreateRequest is one account creation request as the provider records it.
type CreateRequest struct {
	RequestID     string
	Name          string
	State         CreateState
	ProviderID    string // set once State is CreateSucceeded
	FailureReason string // set when State is CreateFailed
	RequestedAt   time.Time
	CompletedAt   time.Time
}

// RemoteAccount is an account as seen at the provider.
type RemoteAccount struct {
	ProviderID string
	Name       string
	Email      string
	Status     string // provider state: ACTIVE, PENDING_CLOSURE, CLOSED, SUSPENDED, etc.
	JoinedAt   time.Time
	Tags       map[string]string
}

// CostPoint is spend for one day.
type CostPoint struct {
	Date      time.Time `json:"date"`
	AmountUSD float64   `json:"amount_usd"`
}

// ErrCloseQuota is returned by CloseAccount when the provider's rolling
// closure quota is exhausted. The close intent stays in tags and is retried.
var ErrCloseQuota = errors.New("account closure quota reached; will retry on a later refresh")

// ErrUserNotFound is returned by LookupUser when no identity matches.
var ErrUserNotFound = errors.New("user not found in the identity store")

// User is a principal in the provider's identity store (IAM Identity Center on AWS).
type User struct {
	ID       string // principal id used in access grants
	UserName string
	Email    string
}

// Identity is the value written to the owner tag: the email when known,
// otherwise the user name.
func (u User) Identity() string {
	if u.Email != "" {
		return u.Email
	}
	return u.UserName
}

// Provider is the seam between the lifecycle service and a cloud vendor.
// Every call runs with management-account credentials. The tool never
// assumes a role into a playground account.
type Provider interface {
	Name() string

	// EnsureOrganization creates the organization and playground OU if missing.
	EnsureOrganization(ctx context.Context) (OrgInfo, error)

	// RequestAccount starts account creation and returns a request id.
	RequestAccount(ctx context.Context, req AccountRequest) (requestID string, err error)
	// CreateStatus reports one creation request.
	CreateStatus(ctx context.Context, requestID string) (CreateRequest, error)
	// ListCreateRequests returns recent creation requests in every state.
	ListCreateRequests(ctx context.Context) ([]CreateRequest, error)

	// GetAccount returns one member account with its tags.
	GetAccount(ctx context.Context, providerID string) (RemoteAccount, error)
	// ListPlaygroundAccounts returns the accounts under the playground OU, with tags.
	ListPlaygroundAccounts(ctx context.Context) ([]RemoteAccount, error)

	// PlaceAccount moves the account into the playground OU and writes tags.
	PlaceAccount(ctx context.Context, providerID string, tags map[string]string) error
	// SetTags adds or overwrites tags.
	SetTags(ctx context.Context, providerID string, tags map[string]string) error
	// RemoveTags deletes tags by key. Missing keys are not an error.
	RemoveTags(ctx context.Context, providerID string, keys []string) error

	// OUTags reads the tags on the playground OU, which hold pending requests.
	OUTags(ctx context.Context) (map[string]string, error)
	// SetOUTags adds or overwrites tags on the playground OU.
	SetOUTags(ctx context.Context, tags map[string]string) error
	// RemoveOUTags deletes tags from the playground OU. Missing keys are not an error.
	RemoveOUTags(ctx context.Context, keys []string) error

	// CloseAccount asks the provider to close the account. Idempotent.
	// Success acknowledges the request, not completion; observe CLOSED via
	// GetAccount or ListPlaygroundAccounts before reporting success.
	// Returns ErrCloseQuota when the provider refuses for quota reasons.
	CloseAccount(ctx context.Context, providerID string) error

	// EnsureBudget reconciles the monthly budget, owned notifications and optional
	// SCP action. Accepted action calls are not confirmation of completion.
	EnsureBudget(ctx context.Context, providerID string, spec BudgetSpec) (BudgetProtection, error)
	// InspectBudget reads spend and action state before an admin changes a limit.
	InspectBudget(ctx context.Context, providerID string, spec BudgetSpec) (BudgetSnapshot, error)
	// RetireBudgetAction deletes only an owned action after fresh CLOSED and
	// ownership checks. True means absence was observed, not merely requested.
	// Preserve the budget/alerts; never reverse actions or directly detach SCPs.
	RetireBudgetAction(ctx context.Context, providerID string, spec BudgetSpec) (bool, error)

	// AccessEnabled reports whether the provider can grant console access to
	// owners. When false, owners are free text and no grants are attempted.
	AccessEnabled() bool
	// LookupUser resolves an email or user name to a User, or ErrUserNotFound.
	LookupUser(ctx context.Context, identity string) (User, error)
	// GrantAccess gives the user the playground permission set on the account. Idempotent.
	GrantAccess(ctx context.Context, providerID string, user User) error
	// RevokeAccess removes that grant. Missing grants are not an error.
	RevokeAccess(ctx context.Context, providerID string, user User) error

	// Costs returns daily spend for the account in [from, to).
	Costs(ctx context.Context, providerID string, from, to time.Time) ([]CostPoint, error)
}

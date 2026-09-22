package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/mail"
	"strconv"
	"strings"
	"time"
)

const (
	TagBudgetHealth   = "playplace:budget-health"
	TagBudgetError    = "playplace:budget-error"
	TagBudgetRecovery = "playplace:budget-recovery"
	TagBudgetChecked  = "playplace:budget-checked"
	TagBudgetPolicy   = "playplace:budget-policy"
)

// BudgetRecovery is durable authorization to undo exactly one action after an
// approved increase. Persist Reset before sending RESET: after a crash a newly
// executed action must never be reversed a second time using the old approval.
// Organization tags cannot contain JSON punctuation; encode this as base64url.
type BudgetRecovery struct {
	ActionID string  `json:"a"`
	Phase    string  `json:"p"`
	Period   string  `json:"m"`
	Limit    float64 `json:"l"`
	Spend    float64 `json:"s"`
}

func (b BudgetRecovery) Encode() string {
	data, _ := json.Marshal(b)
	return base64.RawURLEncoding.EncodeToString(data)
}

func DecodeBudgetRecovery(s string) (*BudgetRecovery, error) {
	if s == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid budget recovery: %w", err)
	}
	var b BudgetRecovery
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("invalid budget recovery: %w", err)
	}
	if b.ActionID == "" || (b.Phase != "reverse" && b.Phase != "reset") || b.Period == "" || !validMoney(b.Limit) || b.Spend <= 0 || b.Spend >= b.Limit {
		return nil, errors.New("invalid budget recovery fields")
	}
	return &b, nil
}

type BudgetSpec struct {
	LimitUSD float64
	Email    string
	PolicyID string
	RoleARN  string
	Recovery *BudgetRecovery
	Now      time.Time
}

func (b BudgetSpec) Enforced() bool { return b.PolicyID != "" }
func (b BudgetSpec) Validate() error {
	if !validMoney(b.LimitUSD) {
		return errors.New("budget must be a finite positive USD amount with at most two decimal places")
	}
	if (b.PolicyID == "") != (b.RoleARN == "") {
		return errors.New("budget-scp-id and budget-action-role must be configured together")
	}
	if b.Enforced() && b.Email == "" {
		return errors.New("budget enforcement requires --alert-email")
	}
	if b.Email != "" {
		a, err := mail.ParseAddress(b.Email)
		if err != nil || a.Address != b.Email {
			return errors.New("alert-email must be a single email address without a display name")
		}
	}
	return nil
}

func validMoney(n float64) bool {
	return n > 0 && n <= 1e12 && !math.IsInf(n, 0) && !math.IsNaN(n) && math.Abs(n*100-math.Round(n*100)) < 0.0001
}

// BudgetProtection carries budget configuration observations. An enforced
// adapter returns Action only after observing the desired configuration;
// core owns recovery and derives protection health from that observation.
// Configured protection is not a spending cap.
type BudgetProtection struct {
	State      string
	ActionID   string
	Configured bool
	Action     *BudgetActionObservation
}

// A nil observation means action setup/repair is not yet confirmed. An observed
// empty or unknown status is different: core must report it as an error.
type BudgetActionObservation struct {
	Status              string
	RestrictionAttached bool
}

// Recovery progress never crosses the provider seam as instructions to callers.
// The budget module persists these facts before any subsequent recovery mutation.
type budgetProgress struct {
	BudgetProtection
	recoveryPhase string
	recoveryDone  bool
}

type BudgetActionOperation string

const (
	BudgetActionReverse BudgetActionOperation = "REVERSE_BUDGET_ACTION"
	BudgetActionReset   BudgetActionOperation = "RESET_BUDGET_ACTION"
)

type BudgetSnapshot struct {
	LimitUSD     float64
	SpendUSD     float64
	SpendKnown   bool
	ActionID     string
	ActionStatus string
}

func (s *Service) budgetSpec(a *Account) (BudgetSpec, error) {
	r, err := DecodeBudgetRecovery(a.BudgetRecovery)
	return BudgetSpec{LimitUSD: a.BudgetUSD, Email: s.cfg.AlertEmail, PolicyID: s.cfg.BudgetPolicyID, RoleARN: s.cfg.BudgetActionRole, Recovery: r, Now: s.Now()}, err
}

func (s *Service) reconcileBudget(ctx context.Context, a *Account) error {
	spec, err := s.budgetSpec(a)
	var p BudgetProtection
	if err == nil {
		err = spec.Validate()
	}
	if err == nil && a.BudgetPolicy != "" && a.BudgetPolicy != spec.PolicyID {
		err = errors.New("budget enforcement configuration removed or changed; restore it before changing existing protection")
	}
	if err == nil {
		p, err = s.provider.EnsureBudget(ctx, a.ProviderID, spec)
	}
	progress := budgetProgress{BudgetProtection: p}
	if err == nil && p.Action != nil {
		progress, err = s.advanceBudgetRecovery(ctx, a, spec, p)
	}
	return s.recordBudgetProtection(ctx, a, spec, progress, err)
}

// retireBudget runs independently of closure confirmation tags, so failures and
// process restarts cannot strand paid actions behind an already-closed marker.
// Retirement health is independent of lifecycle status: cleanup errors must not
// change a confirmed CLOSED account back to closing or prevent other closures.
func (s *Service) retireBudget(ctx context.Context, a *Account) error {
	if !a.Managed || a.Status != StatusClosed || (a.BudgetPolicy == "" && s.cfg.BudgetPolicyID == "") {
		return nil
	}
	// Recovery is irrelevant after closure, including malformed/stale intents.
	spec := BudgetSpec{PolicyID: s.cfg.BudgetPolicyID, RoleARN: s.cfg.BudgetActionRole}
	p := BudgetProtection{State: "retiring"}
	var err error
	if a.BudgetPolicy != "" && a.BudgetPolicy != spec.PolicyID {
		err = errors.New("restore the account's budget enforcement configuration before retirement")
	} else {
		p.Configured, err = s.provider.RetireBudgetAction(ctx, a.ProviderID, spec)
	}
	if err != nil {
		err = fmt.Errorf("budget action retirement: %w", err)
	} else if p.Configured {
		p.State = "retired"
	}
	return s.recordBudgetProtection(ctx, a, spec, budgetProgress{BudgetProtection: p}, err)
}

func (s *Service) recordBudgetProtection(ctx context.Context, a *Account, spec BudgetSpec, p budgetProgress, err error) error {
	state := p.State
	if state == "" {
		state = "setup-pending"
	}
	if err != nil {
		state = "error"
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	// Error messages must fit an Organizations tag and its restricted alphabet.
	short := []byte(message)
	if len(short) > 150 {
		short = short[:150]
	}
	tags := map[string]string{}
	if spec.Enforced() && a.BudgetPolicy == "" {
		tags[TagBudgetPolicy] = spec.PolicyID
	}
	if state != a.BudgetHealth || string(short) != a.BudgetError {
		tags[TagBudgetHealth] = state
		tags[TagBudgetError] = base64.RawURLEncoding.EncodeToString(short)
		tags[TagBudgetChecked] = s.Now().UTC().Format(time.RFC3339)
	}
	if state == "retired" && a.BudgetRecovery != "" {
		tags[TagBudgetRecovery] = ""
	}
	if spec.Recovery != nil && err == nil {
		if p.recoveryDone {
			tags[TagBudgetRecovery] = ""
		} else if p.recoveryPhase != "" && p.recoveryPhase != spec.Recovery.Phase {
			spec.Recovery.Phase = p.recoveryPhase
			tags[TagBudgetRecovery] = spec.Recovery.Encode()
		}
	}
	if len(tags) > 0 {
		if tagErr := s.provider.SetTags(ctx, a.ProviderID, tags); tagErr != nil {
			return errors.Join(err, tagErr)
		}
		s.Invalidate()
		if a.BudgetHealth != state {
			s.audit(ctx, "budget-protection", a, ActorSystem, "", "Budget protection: "+state, map[string]string{"action_id": p.ActionID, "error": message})
			if state == "error" || state == "restricted" || state == "retired" || a.BudgetHealth == "restricted" || a.BudgetHealth == "recovering" || a.BudgetHealth == "error" || a.BudgetHealth == "setup-pending" {
				s.notifyAll(ctx, "Playground budget: "+a.Name, "Budget protection is "+state+". "+message, a.Owner, a.ProviderID)
			}
		}
		a.BudgetHealth, a.BudgetError = state, string(short)
		if policy, ok := tags[TagBudgetPolicy]; ok {
			a.BudgetPolicy = policy
		}
		if checked, ok := tags[TagBudgetChecked]; ok {
			a.BudgetChecked = checked
		}
		if recovery, ok := tags[TagBudgetRecovery]; ok {
			a.BudgetRecovery = recovery
		}
	}
	if err != nil {
		return fmt.Errorf("budget for %s: %w", a.Name, err)
	}
	if !p.Configured {
		return fmt.Errorf("budget for %s: %s; reconciliation will retry", a.Name, state)
	}
	return nil
}

// SetBudget is an operator operation. HTTP callers MUST authorize Admin first;
// the CLI is trusted through its management-account IAM credentials. A durable
// intent is written before AWS mutations. Only one control-plane writer may run.
func (s *Service) SetBudget(ctx context.Context, ref string, amount float64, actor, reason string, override bool) (*Account, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if !validMoney(amount) {
		return nil, errors.New("budget must be finite, positive, and have at most two decimal places")
	}
	if strings.TrimSpace(reason) == "" || len(reason) > 500 {
		return nil, errors.New("a budget change reason of 1-500 bytes is required")
	}
	if s.cfg.MaxBudgetUSD > 0 && amount > s.cfg.MaxBudgetUSD && !override {
		return nil, fmt.Errorf("budget exceeds the $%.2f ceiling; an explicit override is required", s.cfg.MaxBudgetUSD)
	}
	s.Invalidate()
	a, err := s.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	if err := a.RepairError(); err != nil {
		return nil, err
	}
	if !a.Managed || !a.Status.CanExtend() || !a.ExpiresAt.After(s.Now()) {
		return nil, errors.New("only active managed accounts can have their budget changed")
	}
	if a.BudgetPolicy != "" && a.BudgetPolicy != s.cfg.BudgetPolicyID {
		return nil, errors.New("restore the account's budget enforcement configuration before changing its limit")
	}
	if a.BudgetRecovery != "" {
		return nil, errors.New("a budget recovery is pending; reconcile it before another change")
	}
	if amount == a.BudgetUSD {
		return a, s.reconcileBudget(ctx, a)
	}
	spec, err := s.budgetSpec(a)
	if err != nil {
		return nil, err
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	var recovery *BudgetRecovery
	if spec.Enforced() {
		before, err := s.provider.InspectBudget(ctx, a.ProviderID, spec)
		if err != nil {
			return nil, fmt.Errorf("inspect budget before change: %w", err)
		}
		if before.LimitUSD != a.BudgetUSD {
			return nil, errors.New("budget amount has drifted; reconcile before changing it")
		}
		switch before.ActionStatus {
		case "STANDBY", "EXECUTION_SUCCESS":
		default:
			return nil, fmt.Errorf("budget action is %s; resolve or finish its transition before changing the budget", before.ActionStatus)
		}
		if amount > a.BudgetUSD && before.ActionStatus == "EXECUTION_SUCCESS" {
			if !before.SpendKnown || before.SpendUSD <= 0 {
				return nil, errors.New("reported spend is unavailable or recalculating; cannot safely authorize recovery")
			}
			if amount > before.SpendUSD {
				recovery = &BudgetRecovery{ActionID: before.ActionID, Phase: "reverse", Period: s.Now().UTC().Format("2006-01"), Limit: amount, Spend: before.SpendUSD}
			}
		}
	}
	old := a.BudgetUSD
	tags := map[string]string{TagBudget: strconv.FormatFloat(amount, 'f', 2, 64)}
	if recovery != nil {
		tags[TagBudgetRecovery] = recovery.Encode()
	}
	if err := s.provider.SetTags(ctx, a.ProviderID, tags); err != nil {
		return nil, err
	}
	a.BudgetUSD = amount
	if recovery != nil {
		a.BudgetRecovery = recovery.Encode()
	}
	s.Invalidate()
	s.audit(ctx, "budget-changed", a, actor, "", reason, map[string]string{"old": fmt.Sprintf("%.2f", old), "new": fmt.Sprintf("%.2f", amount), "override": strconv.FormatBool(override)})
	err = s.reconcileBudget(ctx, a)
	if err != nil {
		return a, fmt.Errorf("budget change saved; protection reconciliation pending: %w", err)
	}
	return a, nil
}

package cli

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"playplace/internal/core"
	awsprovider "playplace/internal/provider/aws"
)

// options holds every flag. Each flag can also be set with PLAYPLACE_<NAME>
// where NAME is the flag name upper-cased with dashes turned to underscores.
type options struct {
	NoRefresh        bool
	LogLevel         string
	AWSRegion        string
	AWSProfile       string
	AWSEndpoint      string
	OUName           string
	PermissionSet    string
	EmailPattern     string
	AlertEmail       string
	BudgetPolicyID   string
	BudgetActionRole string
	SlackWebhook     string
	DefaultTTL       string
	WarnBefore       string
	CloseAlertAfter  string
	Budget           float64
	History          string
	HistoryFile      string
	HistoryGroup     string
	MaxPerOwner      int
	MaxTTL           string
	MaxBudget        float64
	RequestTTL       string
	SelfApprove      bool
}

func defaultHistoryFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "playplace-history.jsonl"
	}
	return filepath.Join(home, ".playplace", "history.jsonl")
}

func (o *options) bind(cmd *cobra.Command) {
	f := cmd.PersistentFlags()
	f.BoolVar(&o.NoRefresh, "no-refresh", false, "skip the refresh pass (finish creations, sweep expiry, retry closes) before the command")
	f.StringVar(&o.LogLevel, "log-level", "warn", "debug, info, warn, or error")
	f.StringVar(&o.AWSRegion, "aws-region", "", "AWS region for Organizations calls")
	f.StringVar(&o.AWSProfile, "aws-profile", "", "AWS named profile")
	f.StringVar(&o.AWSEndpoint, "aws-endpoint", "", "AWS endpoint override, e.g. http://localhost:4566 for LocalStack")
	f.StringVar(&o.OUName, "ou-name", "Playground", "organizational unit that holds playground accounts")
	f.StringVar(&o.PermissionSet, "permission-set", "", "IAM Identity Center permission set (name or ARN) granted to owners; empty disables access grants and owner validation")
	f.StringVar(&o.EmailPattern, "email-pattern", "aws+pp-{name}@example.com", "root email for new accounts, {name} is replaced")
	f.StringVar(&o.AlertEmail, "alert-email", "", "recipient for AWS Budgets alerts (required for enforcement)")
	f.StringVar(&o.BudgetPolicyID, "budget-scp-id", "", "separately deployed provisioning-deny SCP; empty selects alerts-only budgets")
	f.StringVar(&o.BudgetActionRole, "budget-action-role", "", "management-account IAM role used by AWS Budgets to attach/reverse the SCP")
	f.StringVar(&o.SlackWebhook, "slack-webhook", "", "Slack incoming webhook for lifecycle notifications")
	f.StringVar(&o.DefaultTTL, "default-ttl", "14d", "lifetime of a new account")
	f.StringVar(&o.WarnBefore, "warn-before", "3d", "how far ahead of expiry to warn owners")
	f.StringVar(&o.CloseAlertAfter, "close-alert-after", "24h", "alert and fail reconciliation when closure remains unconfirmed this long (positive duration, e.g. 24h)")
	f.Float64Var(&o.Budget, "default-budget", 50, "monthly budget in USD for a new account")
	f.StringVar(&o.History, "history", "file", "where lifecycle history is kept: file, cloudwatch, or none")
	f.StringVar(&o.HistoryFile, "history-file", defaultHistoryFile(), "JSON-lines file for --history file")
	f.StringVar(&o.HistoryGroup, "history-log-group", "/playplace/history", "CloudWatch log group for --history cloudwatch")
	f.IntVar(&o.MaxPerOwner, "max-per-owner", 1, "open accounts plus pending requests one person may hold (0 = no limit)")
	f.StringVar(&o.MaxTTL, "max-ttl", "90d", "longest lifetime a request may ask for; admins can override (0 = no limit)")
	f.Float64Var(&o.MaxBudget, "max-budget", 500, "largest monthly budget in USD a request may ask for; admins can override (0 = no limit)")
	f.StringVar(&o.RequestTTL, "request-ttl", "7d", "pending requests older than this are dropped")
	f.BoolVar(&o.SelfApprove, "allow-self-approval", false, "let a requester approve their own request")
}

// applyEnv fills flags that were not set on the command line from the
// environment. It goes through FlagSet.Set so the flag counts as Changed,
// which commands that ask cmd.Flags().Changed rely on.
func applyEnv(cmd *cobra.Command) error {
	var err error
	apply := func(fs *pflag.FlagSet) {
		fs.VisitAll(func(f *pflag.Flag) {
			if f.Changed {
				return
			}
			env := "PLAYPLACE_" + strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_"))
			if v, ok := os.LookupEnv(env); ok {
				if e := fs.Set(f.Name, v); e != nil && err == nil {
					err = fmt.Errorf("%s: %w", env, e)
				}
			}
		})
	}
	apply(cmd.Root().PersistentFlags())
	apply(cmd.Flags())
	return err
}

func (o *options) coreConfig() (core.Config, error) {
	cfg := core.DefaultConfig()
	var err error
	if cfg.DefaultTTL, err = parseDuration(o.DefaultTTL); err != nil {
		return cfg, fmt.Errorf("default-ttl: %w", err)
	}
	if cfg.WarnBefore, err = parseDuration(o.WarnBefore); err != nil {
		return cfg, fmt.Errorf("warn-before: %w", err)
	}
	if cfg.CloseAlertAfter, err = parseInterval(o.CloseAlertAfter); err != nil {
		return cfg, fmt.Errorf("close-alert-after: %w", err)
	}
	if cfg.RequestTTL, err = parseDuration(o.RequestTTL); err != nil {
		return cfg, fmt.Errorf("request-ttl: %w", err)
	}
	if cfg.MaxTTL, err = parseDuration(o.MaxTTL); err != nil {
		return cfg, fmt.Errorf("max-ttl: %w", err)
	}
	cfg.MaxBudgetUSD = o.MaxBudget
	cfg.DefaultBudgetUSD = o.Budget
	cfg.EmailPattern = o.EmailPattern
	cfg.AlertEmail = strings.TrimSpace(o.AlertEmail)
	cfg.BudgetPolicyID = strings.TrimSpace(o.BudgetPolicyID)
	cfg.BudgetActionRole = strings.TrimSpace(o.BudgetActionRole)
	if err := (core.BudgetSpec{LimitUSD: cfg.DefaultBudgetUSD, Email: cfg.AlertEmail, PolicyID: cfg.BudgetPolicyID, RoleARN: cfg.BudgetActionRole}).Validate(); err != nil {
		return cfg, err
	}
	if math.IsNaN(cfg.MaxBudgetUSD) || math.IsInf(cfg.MaxBudgetUSD, 0) || cfg.MaxBudgetUSD < 0 {
		return cfg, fmt.Errorf("max-budget must be finite and nonnegative")
	}
	// Core fills in the default before it checks the ceiling, so a default
	// above the ceiling refuses every request that leaves the budget empty.
	if cfg.MaxBudgetUSD > 0 && cfg.DefaultBudgetUSD > cfg.MaxBudgetUSD {
		return cfg, fmt.Errorf("default-budget (%g) is above max-budget (%g), so every request without a budget would be refused", cfg.DefaultBudgetUSD, cfg.MaxBudgetUSD)
	}
	cfg.MaxPerOwner = o.MaxPerOwner
	cfg.AllowSelfApproval = o.SelfApprove
	if err := o.checkLifetimes(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// checkLifetimes refuses lifetime settings that core would act on in a
// destructive way. parseDuration accepts 0 because --max-ttl uses it for
// "no limit", but core has no such meaning for the others:
//
//   - DefaultTTL 0: every new account expires at creation, and refresh adopts
//     and then closes every untagged account in the OU in the same pass
//     (core.FromRemote sets ExpiresAt = now + DefaultTTL).
//   - RequestTTL 0: expireRequests drops every pending request on the next
//     pass, because any age is greater than zero.
//   - WarnBefore >= DefaultTTL: the one-shot expiry warning is used up at
//     once, so the owner is never warned near the real expiry.
//   - DefaultTTL > MaxTTL (when MaxTTL is set): every request that keeps the
//     default lifetime is refused at the ceiling.
//
// The error text quotes the flags as the operator gave them.
func (o *options) checkLifetimes(cfg core.Config) error {
	switch {
	case cfg.DefaultTTL <= 0:
		return fmt.Errorf("default-ttl must be positive; %q would close every new and adopted account at once", o.DefaultTTL)
	case cfg.RequestTTL <= 0:
		return fmt.Errorf("request-ttl must be positive; %q would drop every pending request on the next refresh", o.RequestTTL)
	case cfg.WarnBefore >= cfg.DefaultTTL:
		return fmt.Errorf("warn-before (%s) must be shorter than default-ttl (%s), or owners are warned at creation instead of near expiry", o.WarnBefore, o.DefaultTTL)
	case cfg.MaxTTL > 0 && cfg.DefaultTTL > cfg.MaxTTL:
		return fmt.Errorf("default-ttl (%s) is above max-ttl (%s), so every request without a lifetime would be refused", o.DefaultTTL, o.MaxTTL)
	}
	return nil
}

func (o *options) awsConfig() awsprovider.Config {
	return awsprovider.Config{
		Region:           o.AWSRegion,
		Profile:          o.AWSProfile,
		Endpoint:         o.AWSEndpoint,
		PlaygroundOUName: o.OUName,
		PermissionSet:    o.PermissionSet,
	}
}

// parseDuration reads a lifetime. A bare number is days; "7d" and Go
// durations such as "72h" are also accepted. Zero is allowed because some
// limits use it for "no limit"; negative and non-finite values are not.
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	days := func(n float64) (time.Duration, error) {
		if math.IsNaN(n) || math.IsInf(n, 0) || n > 366*100 {
			return 0, fmt.Errorf("duration %q is not a usable number of days", s)
		}
		if n < 0 {
			return 0, fmt.Errorf("duration %q must not be negative", s)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return days(n)
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil {
			return 0, fmt.Errorf("bad duration %q", s)
		}
		return days(n)
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("bad duration %q", s)
	}
	if d < 0 {
		return 0, fmt.Errorf("duration %q must not be negative", s)
	}
	return d, nil
}

// parseInterval reads a wait or a period such as "30s" or "5m". Unlike
// lifetimes, a bare number is refused: "30" as thirty days is never what a
// refresh interval or a timeout means.
func parseInterval(s string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("bad interval %q; use a unit, such as 30s or 5m", s)
	}
	if d <= 0 {
		return 0, fmt.Errorf("interval %q must be positive", s)
	}
	return d, nil
}

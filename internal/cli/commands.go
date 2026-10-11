package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"log/slog"

	"github.com/spf13/cobra"

	"playplace/internal/core"
	"playplace/internal/tui"
	"playplace/internal/web"
	"playplace/internal/worker"
)

func withApp(opts *options, refresh bool, fn func(ctx context.Context, a *app, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		a, err := newApp(ctx, opts)
		if err != nil {
			return err
		}
		if refresh {
			a.refresh(ctx)
		}
		return fn(ctx, a, args)
	}
}

func initCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create the AWS Organization and playground OU if they do not exist",
		Long: `Create the AWS Organization and playground OU if they do not exist.

With --ou-id, init creates nothing. It verifies the OU that the ID names and
reports it.

init also lists the active accounts in the OU and the new accounts that wait
in the organization root, with what the next refresh does to each:

  - an active account in the OU that playplace does not manage: adopt it and
    close it at the end of its lifetime, start its closure at once, or leave
    it alone until someone repairs its tags
  - an active account in the OU that playplace manages: resume its
    management, and start its closure at once if it expired or has a
    playplace:close-requested tag
  - a new account of playplace that waits in the organization root: move it
    into the OU, or leave it in the root until someone repairs its tags.
    playplace places an account only once, so it never moves one to another
    OU later.

The next refresh also tracks the closure of the accounts in the OU that AWS
is already closing. init does not list them. The dates assume that the next
refresh runs today. Most commands start with a refresh, so run init before
any other command. Make sure that this is the right OU, and move every
account that playplace must not manage out of it.`,
		RunE: withApp(opts, false, func(ctx context.Context, a *app, _ []string) error {
			info, err := a.svc.Init(ctx)
			if err != nil {
				return err
			}
			printInit(info, strings.TrimSpace(opts.OUID) != "")
			forecast, err := a.svc.Forecast(ctx)
			if err != nil {
				return err
			}
			printForecast(forecast, a.svc.Now())
			return nil
		}),
	}
}

// printInit says what init found and what it created. A new OU has none of
// the operator's SCPs yet, so init says so. An OU that --ou-id names (byID)
// was not made by init, so init names it and asks for the safeguards SCP.
func printInit(info core.OrgInfo, byID bool) {
	if info.Created {
		fmt.Println("created organization", info.OrgID)
	} else {
		fmt.Println("using organization", info.OrgID)
	}
	fmt.Println("management account:", info.ManagementAccountID)
	switch {
	case info.OUCreated:
		fmt.Printf("created playground OU %s under the root. Attach the safeguards SCP to it before you grant access.\n", info.PlaygroundOUID)
	case byID:
		fmt.Printf("playground OU: %s (%s), found by --ou-id. init did not create or change it.\n", info.PlaygroundOUName, info.PlaygroundOUID)
		fmt.Println("Attach the safeguards SCP to this OU before you grant access.")
	default:
		fmt.Println("playground OU:", info.PlaygroundOUID)
	}
}

// printForecast lists the active accounts in the OU and the creations in the
// organization root, with what the next refresh does to each, as if it ran
// now. A valid --ou-id that names the wrong OU passes every check. This list
// is where the operator sees the mistake before playplace adopts, closes or
// moves accounts that are not playgrounds. It always prints something, so
// that an empty OU does not look like a skipped check.
func printForecast(f core.Forecast, now time.Time) {
	if len(f.Unmanaged)+len(f.Managed)+len(f.Waiting)+len(f.Blocked) == 0 {
		fmt.Println("No active account is in this OU, and no new account waits in the organization root.")
		return
	}
	if len(f.Unmanaged) > 0 {
		fmt.Printf("playplace does not manage %s in this OU:\n", countAccounts(len(f.Unmanaged)))
	}
	for _, a := range f.Unmanaged {
		// The order matches Refresh: close intent first, then tag damage,
		// then adoption.
		switch err := a.RepairError(); {
		case a.CloseRequestedAt != nil:
			fmt.Printf("  %s (%s): %s\n", a.Name, a.ProviderID, closure(a, now))
		case err != nil:
			fmt.Printf("  %s (%s): left alone until someone repairs its tags: %v\n", a.Name, a.ProviderID, err)
		default:
			fmt.Printf("  %s (%s): adopted with owner %s and %s/month, %s\n", a.Name, a.ProviderID, a.Owner, core.Dollars(a.BudgetUSD), closure(a, now))
		}
	}
	if len(f.Managed) > 0 {
		fmt.Printf("playplace manages %s in this OU:\n", countAccounts(len(f.Managed)))
	}
	for _, a := range f.Managed {
		fmt.Printf("  %s (%s): owner %s, %s\n", a.Name, a.ProviderID, a.Owner, closure(a, now))
	}
	if len(f.Waiting) > 0 {
		fmt.Printf("playplace moves %s from the organization root into this OU. It places an account only once, so it never moves one to another OU later:\n", countAccounts(len(f.Waiting)))
	}
	for _, a := range f.Waiting {
		when := ""
		if a.ProviderState == "PENDING_ACTIVATION" {
			when = "moves after AWS activates it, "
		}
		fmt.Printf("  %s (%s): %sowner %s, %s\n", a.Name, a.ProviderID, when, a.Owner, closure(a, now))
	}
	if len(f.Blocked) > 0 {
		fmt.Printf("playplace leaves %s in the organization root until someone repairs the tags:\n", countAccounts(len(f.Blocked)))
	}
	for _, a := range f.Blocked {
		fmt.Printf("  %s (%s): %v\n", a.Name, a.ProviderID, a.RepairError())
	}
	fmt.Println("The next refresh does this, and the dates assume that it runs today. Before you run another command, make sure that this is the right OU, and move every account that playplace must not manage out of it.")
}

// closure says when the next refresh starts to close a, in the order that
// Refresh decides it: close intent, then a missing expiry, then the expiry.
func closure(a *core.Account, now time.Time) string {
	switch {
	case a.CloseRequestedAt != nil:
		return "closure starts at once, because it has a " + core.TagCloseRequested + " tag"
	case a.ExpiresAt.IsZero():
		return "no closure, because it has no valid " + core.TagExpires + " tag"
	case !a.ExpiresAt.After(now):
		return "closure starts at once"
	default:
		return "closure starts on " + a.ExpiresAt.Format("2006-01-02")
	}
}

// countAccounts says "1 account" or "N accounts".
func countAccounts(n int) string {
	if n == 1 {
		return "1 account"
	}
	return fmt.Sprintf("%d accounts", n)
}

func requestCmd(opts *options) *cobra.Command {
	var owner, ttl, purpose string
	var budget float64
	var override bool
	cmd := &cobra.Command{
		Use:   "request NAME",
		Short: "Queue a playground account request for an approver",
		Args:  cobra.ExactArgs(1),
		RunE: withApp(opts, true, func(ctx context.Context, a *app, args []string) error {
			// The operator is the requester even when asking for someone
			// else, so the self-approval rule and the history name the
			// person who acted, as the web UI does.
			in := core.RequestInput{Name: args[0], Owner: owner, BudgetUSD: budget, Purpose: purpose, OverrideLimits: override, RequestedBy: operator()}
			if ttl != "" {
				d, err := parseDuration(ttl)
				if err != nil {
					return err
				}
				in.TTL = d
			}
			q, err := a.svc.SubmitRequest(ctx, in, "cli")
			if err != nil {
				return err
			}
			fmt.Printf("queued %s for %s: %s/month, %s. Approve with `playplace approve %s`.\n", q.Name, q.Owner, core.Dollars(q.BudgetUSD), core.Lifetime(q.TTL), q.Name)
			return nil
		}),
	}
	cmd.Flags().StringVar(&owner, "owner", "", "engineer who will own the account (required)")
	cmd.Flags().StringVar(&ttl, "ttl", "", "lifetime in days, e.g. 7; defaults to --default-ttl")
	cmd.Flags().Float64Var(&budget, "budget", 0, "monthly budget in USD, defaults to --default-budget")
	cmd.Flags().StringVar(&purpose, "purpose", "", "short note on what the account is for")
	cmd.Flags().BoolVar(&override, "override-limits", false, "exceed --max-ttl and --max-budget (operators only; recorded on the request)")
	cmd.MarkFlagRequired("owner")
	return cmd
}

func requestsCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "requests",
		Short: "List pending account requests",
		RunE: withApp(opts, true, func(ctx context.Context, a *app, _ []string) error {
			pending, err := a.svc.PendingRequests(ctx)
			if err != nil {
				return err
			}
			if len(pending) == 0 {
				fmt.Println("no pending requests")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tOWNER\tREQUESTED BY\tVIA\tTTL\tBUDGET\tWHEN\tPURPOSE")
			for _, q := range pending {
				note := q.Purpose
				if q.OverrideLimits {
					note = strings.TrimSpace(note + " [limits overridden]")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", q.Name, q.Owner, q.RequestedBy, q.Via, core.Lifetime(q.TTL), core.Dollars(q.BudgetUSD), q.RequestedAt.Local().Format("Jan 2 15:04"), note)
			}
			return w.Flush()
		}),
	}
}

func editRequestCmd(opts *options) *cobra.Command {
	var owner, ttl, purpose string
	var budget float64
	var override, clearPurpose bool
	var cmd *cobra.Command
	cmd = &cobra.Command{
		Use:   "edit-request NAME",
		Short: "Change a pending request's owner, days, budget, or purpose before approving it",
		Args:  cobra.ExactArgs(1),
		RunE: withApp(opts, true, func(ctx context.Context, a *app, args []string) error {
			e := core.RequestEdit{Owner: owner, BudgetUSD: budget}
			if cmd.Flags().Changed("override-limits") {
				// Only an explicit flag changes the override; editing the
				// budget alone must not silently drop an existing one.
				e.OverrideLimits = &override
			}
			if ttl != "" {
				d, err := parseDuration(ttl)
				if err != nil {
					return err
				}
				e.TTL = d
			}
			if clearPurpose {
				empty := ""
				e.Purpose = &empty
			} else if purpose != "" {
				e.Purpose = &purpose
			}
			// Only the flags given change; the version guards the moment
			// between this read and the write.
			cur, err := a.svc.GetRequest(ctx, args[0])
			if err != nil {
				return err
			}
			q, err := a.svc.UpdateRequest(ctx, cur.Ref(), operator(), e)
			if err != nil {
				return err
			}
			fmt.Printf("%s: owner %s, %s, %s/month", q.Name, q.Owner, core.Lifetime(q.TTL), core.Dollars(q.BudgetUSD))
			if q.Purpose != "" {
				fmt.Printf(", purpose %q", q.Purpose)
			}
			if q.OverrideLimits {
				fmt.Print(" [limits overridden]")
			}
			fmt.Println()
			return nil
		}),
	}
	cmd.Flags().StringVar(&owner, "owner", "", "new owner")
	cmd.Flags().StringVar(&ttl, "ttl", "", "new lifetime in days")
	cmd.Flags().Float64Var(&budget, "budget", 0, "new monthly budget in USD")
	cmd.Flags().StringVar(&purpose, "purpose", "", "new purpose")
	cmd.Flags().BoolVar(&clearPurpose, "clear-purpose", false, "remove the purpose")
	cmd.Flags().BoolVar(&override, "override-limits", false, "allow values above --max-ttl and --max-budget; --override-limits=false removes an existing override")
	return cmd
}

func withdrawCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "withdraw NAME",
		Short: "Pull a pending request back before anyone acts on it",
		Args:  cobra.ExactArgs(1),
		RunE: withApp(opts, true, func(ctx context.Context, a *app, args []string) error {
			q, err := a.svc.GetRequest(ctx, args[0])
			if err != nil {
				return err
			}
			if err := a.svc.Withdraw(ctx, q.Ref(), operator()); err != nil {
				return err
			}
			fmt.Printf("withdrew %s\n", describeRequest(q))
			return nil
		}),
	}
}

// describeRequest shows a pending request with the exact values an
// approval binds to.
func describeRequest(q core.Request) string {
	s := fmt.Sprintf("%s for %s: %s/month, %s; requested by %s via %s on %s",
		q.Name, q.Owner, core.Dollars(q.BudgetUSD), core.Lifetime(q.TTL),
		q.RequestedBy, q.Via, q.RequestedAt.Local().Format("Jan 2 15:04"))
	if q.Purpose != "" {
		s += "\npurpose: " + q.Purpose
	}
	if q.OverrideLimits {
		s += "\nlimits overridden by an admin"
	}
	return s
}

// confirmApproval prints the request and, unless yes is set, asks for y.
// Only y approves. An empty answer aborts, and input that ends before an
// answer names -y, because a script cannot answer the question.
func confirmApproval(q core.Request, yes bool, in io.Reader, out io.Writer) error {
	prompt := describeRequest(q) + "\n"
	if !yes {
		prompt += "approve " + q.Name + "? [y/N] "
	}
	if _, err := io.WriteString(out, prompt); err != nil || yes {
		return err
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	ans := strings.TrimSpace(line)
	if err != nil && ans == "" {
		return errors.New("no answer read; pass -y to approve without asking")
	}
	if !strings.EqualFold(ans, "y") {
		return errors.New("aborted")
	}
	return nil
}

// approveRequest reads the request, shows it, asks unless yes is set, and
// approves the version it showed. A change made while the question waits is
// refused, not approved. The approver check runs before the question, so an
// operator who cannot approve the request is not asked.
func approveRequest(ctx context.Context, svc *core.Service, name, approver string, yes bool, in io.Reader, out io.Writer) (*core.Account, error) {
	q, err := svc.GetRequest(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := core.CheckApprover(q, approver, svc.Config().AllowSelfApproval); err != nil {
		return nil, err
	}
	if err := confirmApproval(q, yes, in, out); err != nil {
		return nil, err
	}
	return svc.Approve(ctx, q.Ref(), approver)
}

func approveCmd(opts *options) *cobra.Command {
	var noWait, yes bool
	cmd := &cobra.Command{
		Use:   "approve NAME",
		Short: "Approve a pending request and create the account",
		Args:  cobra.ExactArgs(1),
		RunE: withApp(opts, true, func(ctx context.Context, a *app, args []string) error {
			acc, err := approveRequest(ctx, a.svc, args[0], operator(), yes, os.Stdin, os.Stdout)
			if err != nil {
				return err
			}
			fmt.Printf("approved %s for %s; request %s\n", acc.Name, acc.Owner, acc.RequestID)
			if noWait {
				return nil
			}
			return waitForAccount(ctx, a, acc, 10*time.Minute)
		}),
	}
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "return without waiting for AWS to finish")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the question and approve the request as printed")
	return cmd
}

func denyCmd(opts *options) *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "deny NAME",
		Short: "Deny a pending request",
		Args:  cobra.ExactArgs(1),
		RunE: withApp(opts, true, func(ctx context.Context, a *app, args []string) error {
			// The CLI acts on the record as read now and prints it, so the
			// operator sees what was denied.
			q, err := a.svc.GetRequest(ctx, args[0])
			if err != nil {
				return err
			}
			if err := a.svc.Deny(ctx, q.Ref(), operator(), reason); err != nil {
				return err
			}
			fmt.Printf("denied %s\n", describeRequest(q))
			return nil
		}),
	}
	cmd.Flags().StringVar(&reason, "reason", "", "why the request is denied, shown in the notice and in history")
	return cmd
}

// createPoll is how often waitForAccount asks AWS. Tests shorten it.
var createPoll = 5 * time.Second

// waitForAccount polls a creation until AWS finishes or the deadline passes.
// It succeeds once the account is placed. Readiness can take another pass,
// for example while AWS has not yet shown a new budget action. When a step
// after the creation fails, the error says that the account exists.
//
// Every progress line ends before the next poll. A poll can log, for example
// the ready notice at Warn on stderr. On a terminal, stdout and stderr share
// one screen, so a log line must not start in the middle of a progress line.
// The wait prints a line only when the state changes.
func waitForAccount(ctx context.Context, a *app, acc *core.Account, maxWait time.Duration) error {
	fmt.Printf("waiting for AWS to create %s\n", acc.Name)
	deadline := time.Now().Add(maxWait)
	var err error
	activating := false
	for acc.Status == core.StatusCreating {
		if acc.ProviderState == "PENDING_ACTIVATION" && !activating {
			// AWS created the account and activates it within minutes.
			activating = true
			fmt.Printf("%s (account %s) waits for AWS to activate it\n", acc.Name, acc.ProviderID)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("still creating after %s; it will finish on a later command or reconcile", maxWait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(createPoll):
		}
		if acc, err = a.svc.PollCreate(ctx, acc.RequestID); err != nil {
			return createdFailure(err)
		}
	}
	if acc.Status != core.StatusActive {
		return fmt.Errorf("%s is %s: %s", acc.Name, acc.Status, acc.LastError)
	}
	fmt.Printf("%s is active: account %s, owner %s, expires %s\n", acc.Name, acc.ProviderID, acc.Owner, acc.ExpiresAt.Format("2006-01-02"))
	if acc.HandoffPending() {
		fmt.Printf("%s is not ready yet: %s\n", acc.Name, handoffBlocker(acc))
	}
	return nil
}

// createdFailure tells the operator that AWS created the account when a step
// after the creation fails. The error names the account ID, says whether the
// account is placed in the Playground OU and whether a refresh retries the
// step, and ends with the cause. Other errors stay as they are, for example
// when AWS cannot report the status of the creation.
func createdFailure(err error) error {
	var created *core.CreatedError
	if !errors.As(err, &created) {
		return err
	}
	acc := created.Account
	switch {
	case !acc.Managed:
		return fmt.Errorf("%s was created as account %s, but playplace did not place it in the Playground OU. A refresh does not place it either: %w", acc.Name, acc.ProviderID, err)
	case !created.Placed:
		return fmt.Errorf("%s was created as account %s, but its placement in the Playground OU did not finish. Unless someone moved the account to another OU, the next refresh retries it: %w", acc.Name, acc.ProviderID, err)
	default:
		return fmt.Errorf("%s was created as account %s and placed in the Playground OU. The next refresh retries the step that failed: %w", acc.Name, acc.ProviderID, err)
	}
}

// handoffBlocker names what keeps a placed account from its initial handoff,
// in the order that readiness checks it. It does not promise the ready
// notice: a missing owner can block it for a long time.
func handoffBlocker(acc *core.Account) string {
	switch acc.BudgetHealth {
	case "ready", "alerts-only", "alerts-disabled":
	case "":
		return "budget protection is not checked yet. A later refresh continues it."
	default:
		return "budget protection is " + acc.BudgetHealth + ". A later refresh continues it."
	}
	switch strings.ToLower(strings.TrimSpace(acc.Owner)) {
	case "", "unknown", "?":
		return "the owner is not identified. A later refresh continues when the account has a real owner."
	}
	return "owner access is not granted. A later refresh retries it."
}

func createCmd(opts *options) *cobra.Command {
	var owner, email, ttl, purpose string
	var budget float64
	var noWait, override bool
	var timeout string
	cmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Create an account directly, skipping the approval queue (operators only)",
		Long:  "create is the operator escape hatch: it needs management-account credentials and records you as the approver.",
		Args:  cobra.ExactArgs(1),
		RunE: withApp(opts, true, func(ctx context.Context, a *app, args []string) error {
			in := core.RequestInput{Name: args[0], Owner: owner, Email: email, BudgetUSD: budget, Purpose: purpose, RequestedBy: operator(), ApprovedBy: operator(), OverrideLimits: override}
			if ttl != "" {
				d, err := parseDuration(ttl)
				if err != nil {
					return err
				}
				in.TTL = d
			}
			maxWait, err := parseInterval(timeout)
			if err != nil {
				return fmt.Errorf("timeout: %w", err)
			}
			acc, err := a.svc.Request(ctx, in)
			if err != nil {
				return err
			}
			fmt.Printf("%s  %s  request %s  expires %s\n", acc.Name, acc.Status, acc.RequestID, acc.ExpiresAt.Format("2006-01-02"))
			if noWait {
				fmt.Println("not waiting; the next command, `playplace reconcile`, or S in the TUI will finish it")
				return nil
			}
			return waitForAccount(ctx, a, acc, maxWait)
		}),
	}
	cmd.Flags().StringVar(&owner, "owner", "", "engineer who owns the account (required)")
	cmd.Flags().StringVar(&email, "email", "", "root email, defaults to --email-pattern")
	cmd.Flags().StringVar(&ttl, "ttl", "", "lifetime in days, e.g. 7; defaults to --default-ttl")
	cmd.Flags().Float64Var(&budget, "budget", 0, "monthly budget in USD, defaults to --default-budget")
	cmd.Flags().StringVar(&purpose, "purpose", "", "short note on what the account is for")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "return as soon as AWS accepts the request")
	cmd.Flags().BoolVar(&override, "override-limits", false, "exceed --max-ttl and --max-budget")
	cmd.Flags().StringVar(&timeout, "timeout", "10m", "how long to wait for AWS, with a unit such as 5m")
	cmd.MarkFlagRequired("owner")
	return cmd
}

func listCmd(opts *options) *cobra.Command {
	var all bool
	var owner string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List playground accounts",
		RunE: withApp(opts, true, func(ctx context.Context, a *app, _ []string) error {
			accounts, err := a.svc.List(ctx, core.ListFilter{IncludeClosed: all, Owner: owner})
			if err != nil {
				return err
			}
			printAccounts(accounts)
			return nil
		}),
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "include closed accounts")
	cmd.Flags().StringVar(&owner, "owner", "", "filter by owner")
	return cmd
}

func printAccounts(accounts []*core.Account) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tOWNER\tSTATUS\tACCOUNT\tEXPIRES\tBUDGET\tPROTECTION")
	for _, a := range accounts {
		exp := accountExpiry(a, "2006-01-02")
		if a.ProviderID == "" {
			exp = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", a.Name, a.Owner, a.Status, a.ProviderID, exp, accountBudget(a), a.BudgetHealth)
	}
	w.Flush()
	for _, a := range accounts {
		if err := a.RepairError(); err != nil {
			fmt.Println(err)
		}
		if a.Status == core.StatusCreating && a.LastError != "" {
			fmt.Printf("%s: %s\n", a.Name, a.LastError)
		}
	}
}

func showCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "show ACCOUNT",
		Short: "Show one account and its recent spend",
		Args:  cobra.ExactArgs(1),
		RunE: withApp(opts, true, func(ctx context.Context, a *app, args []string) error {
			acc, err := a.svc.Resolve(ctx, args[0])
			if err != nil {
				return err
			}
			fmt.Printf("name:        %s\nowner:       %s\nstatus:      %s\naccount id:  %s\n", acc.Name, acc.Owner, acc.Status, acc.ProviderID)
			fmt.Printf("protection:  %s (observed %s)\n", acc.BudgetHealth, acc.BudgetChecked)
			if acc.BudgetError != "" {
				fmt.Printf("budget error: %s\n", acc.BudgetError)
			}
			if acc.ProviderState != "" {
				fmt.Printf("AWS state:   %s\n", acc.ProviderState)
			}
			if acc.RequestID != "" {
				fmt.Printf("request id:  %s\n", acc.RequestID)
			}
			fmt.Printf("email:       %s\nbudget:      %s/month\ncreated:     %s\n", acc.Email, accountBudget(acc), acc.CreatedAt.Format(time.RFC3339))
			if acc.ProviderID != "" {
				fmt.Printf("expires:     %s\n", accountExpiry(acc, time.RFC3339))
			}
			if err := acc.RepairError(); err != nil {
				fmt.Printf("repair:      %s\n", err)
			}
			if acc.WarnedAt != nil {
				fmt.Printf("expiring:    notice sent %s\n", acc.WarnedAt.Format(time.RFC3339))
			}
			if acc.CloseRequestedAt != nil {
				fmt.Printf("close asked: %s\n", acc.CloseRequestedAt.Format(time.RFC3339))
			}
			if acc.ClosedAt != nil {
				fmt.Printf("closed seen: %s\n", acc.ClosedAt.Format(time.RFC3339))
			}
			if acc.LastError != "" && acc.Status == core.StatusCreating {
				// A creation that waits in the root for placement: a note, not a failure.
				fmt.Printf("placement:   %s\n", acc.LastError)
			} else if acc.LastError != "" {
				fmt.Printf("failure:     %s\n", acc.LastError)
			}
			if !acc.Managed {
				fmt.Println("note:        not yet tagged by playplace; the next refresh adopts it")
			}
			if acc.ProviderID != "" {
				costs, err := a.svc.Costs(ctx, acc.ID, 30, false)
				if err != nil {
					fmt.Fprintln(os.Stderr, "costs:", err)
					fmt.Println("spend (30d): unavailable")
				} else {
					var total float64
					for _, c := range costs {
						total += c.AmountUSD
					}
					fmt.Printf("spend (30d): $%.2f over %d days\n", total, len(costs))
				}
			}
			return nil
		}),
	}
}

func extendCmd(opts *options) *cobra.Command {
	var by, until string
	var override bool
	cmd := &cobra.Command{
		Use:   "extend ACCOUNT",
		Short: "Push an account's expiry out",
		Args:  cobra.ExactArgs(1),
		RunE: withApp(opts, true, func(ctx context.Context, a *app, args []string) error {
			acc, err := a.svc.Resolve(ctx, args[0])
			if err != nil {
				return err
			}
			var t time.Time
			switch {
			case until != "":
				t, err = time.Parse("2006-01-02", until)
				if err != nil {
					return fmt.Errorf("--until must be YYYY-MM-DD")
				}
			case by != "":
				d, err := parseDuration(by)
				if err != nil {
					return err
				}
				t = acc.ExpiresAt.Add(d)
			default:
				return fmt.Errorf("pass --by or --until")
			}
			acc, err = a.svc.Extend(ctx, acc.ID, t, operator(), override)
			if err != nil {
				return err
			}
			fmt.Printf("%s now expires %s\n", acc.Name, acc.ExpiresAt.Format("2006-01-02"))
			return nil
		}),
	}
	cmd.Flags().StringVar(&by, "by", "", "days to add, e.g. 7")
	cmd.Flags().StringVar(&until, "until", "", "new expiry date YYYY-MM-DD")
	cmd.Flags().BoolVar(&override, "override-limits", false, "let the account live past --max-ttl (operators only; recorded in history)")
	return cmd
}

func closeCmd(opts *options) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "close ACCOUNT",
		Short: "Close an account (irreversible at AWS after 90 days)",
		Args:  cobra.ExactArgs(1),
		RunE: withApp(opts, true, func(ctx context.Context, a *app, args []string) error {
			acc, err := a.svc.Resolve(ctx, args[0])
			if err != nil {
				return err
			}
			if !yes {
				fmt.Printf("close %s (%s) owned by %s? [y/N] ", acc.Name, acc.ProviderID, acc.Owner)
				var ans string
				fmt.Scanln(&ans)
				if !strings.EqualFold(ans, "y") {
					return fmt.Errorf("aborted")
				}
			}
			acc, err = a.svc.RequestClose(ctx, acc.ID, operator())
			if err != nil {
				return err
			}
			fmt.Printf("%s is %s\n", acc.Name, acc.Status)
			if acc.Status == core.StatusClosing {
				fmt.Println("Closure is not yet confirmed. The close intent is tagged; `playplace reconcile` monitors progress and retries when needed. Charges may continue until AWS reports CLOSED.")
			}
			return nil
		}),
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation")
	return cmd
}

func costsCmd(opts *options) *cobra.Command {
	var days int
	cmd := &cobra.Command{
		Use:   "costs [ACCOUNT]",
		Short: "Show spend per account, or daily spend for one account (live from Cost Explorer)",
		Args:  cobra.MaximumNArgs(1),
		RunE: withApp(opts, true, func(ctx context.Context, a *app, args []string) error {
			if len(args) == 1 {
				acc, err := a.svc.Resolve(ctx, args[0])
				if err != nil {
					return err
				}
				pts, err := a.svc.Costs(ctx, acc.ID, days, false)
				if err != nil {
					return err
				}
				var total float64
				for _, p := range pts {
					fmt.Printf("%s  $%8.2f\n", p.Date.Format("2006-01-02"), p.AmountUSD)
					total += p.AmountUSD
				}
				fmt.Printf("total       $%8.2f\n", total)
				return nil
			}
			accounts, err := a.svc.List(ctx, core.ListFilter{})
			if err != nil {
				return err
			}
			var ids []string
			for _, acc := range accounts {
				if acc.ProviderID != "" {
					ids = append(ids, acc.ID)
				}
			}
			if err := a.svc.WarmCosts(ctx, ids, false); err != nil {
				fmt.Fprintln(os.Stderr, "costs:", err)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintf(w, "NAME\tOWNER\tACCOUNT\tBUDGET\tSPEND %dD\n", days)
			for _, acc := range accounts {
				// A failed pull must not read as zero spend.
				spend := "unavailable"
				if pts, ok := a.svc.CostsCached(acc.ProviderID, days); ok {
					var total float64
					for _, p := range pts {
						total += p.AmountUSD
					}
					spend = fmt.Sprintf("$%.2f", total)
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", acc.Name, acc.Owner, acc.ProviderID, accountBudget(acc), spend)
			}
			return w.Flush()
		}),
	}
	cmd.Flags().IntVar(&days, "days", 30, "window in days")
	return cmd
}

func reconcileCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "reconcile",
		Short: "Run one refresh pass: finish creations, adopt untagged accounts, warn, close expired, retry closes",
		Long:  "Every other command runs this pass too (see --no-refresh); reconcile exists for cron and prints what changed.",
		RunE: withApp(opts, false, func(ctx context.Context, a *app, _ []string) error {
			sum, err := a.svc.Refresh(ctx)
			if sum.Empty() {
				fmt.Println("nothing to do")
			} else {
				fmt.Println(sum.String())
			}
			return err
		}),
	}
}

// oidcFlags are the serve flags that configure sign-in.
type oidcFlags struct {
	preset, issuer, clientID, clientSecret, redirectURL, admins, approvers, domain, sessionSecret string
}

const defaultRedirectURL = "http://localhost:8080/auth/callback"

// stray names the sign-in flags given without an issuer or preset. Sign-in
// turns on only with one of those, so a lost issuer setting would otherwise
// leave the rest ignored and make every visitor an admin.
func (o oidcFlags) stray() []string {
	if o.issuer != "" || o.preset != "" {
		return nil
	}
	var names []string
	for _, f := range []struct{ name, value string }{
		{"--oidc-client-id", o.clientID}, {"--oidc-client-secret", o.clientSecret},
		{"--admins", o.admins}, {"--approvers", o.approvers},
		{"--allowed-domain", o.domain}, {"--session-secret", o.sessionSecret},
	} {
		if f.value != "" {
			names = append(names, f.name)
		}
	}
	// Like the others, an empty value counts as unset; the default is the
	// flag's own value, so only a changed callback signals intended sign-in.
	if o.redirectURL != "" && o.redirectURL != defaultRedirectURL {
		names = append(names, "--oidc-redirect-url")
	}
	return names
}

func serveCmd(opts *options) *cobra.Command {
	var addr, every string
	var noWorker bool
	var oidc oidcFlags
	var baseURL string
	var slack struct{ signingSecret, botToken, channel string }
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the web UI and a periodic refresh",
		Long: `Run the web UI and a periodic refresh.

Sign-in is OpenID Connect. Give --oidc-issuer (or --oidc-preset google),
--oidc-client-id, --oidc-client-secret, and --oidc-redirect-url. Signed-in
engineers see and manage the accounts they own; emails in --admins see the
fleet. Without OIDC flags the UI has no sign-in and everyone is an admin,
which is only safe on a private network. Other sign-in flags without an
issuer or preset are an error.`,
		RunE: withApp(opts, false, func(ctx context.Context, a *app, _ []string) error {
			interval, err := parseInterval(every)
			if err != nil {
				return fmt.Errorf("every: %w", err)
			}
			if stray := oidc.stray(); len(stray) > 0 {
				return fmt.Errorf("%s set without --oidc-issuer or --oidc-preset; refusing to start with no sign-in, where every visitor is an admin", strings.Join(stray, ", "))
			}
			a.svc.SetBaseURL(baseURL)
			var auth web.Authenticator = web.NoAuth{}
			var oidcAuth *web.OIDCAuth
			if oidc.preset != "" || oidc.issuer != "" {
				issuer := oidc.issuer
				if issuer == "" {
					var ok bool
					if issuer, ok = web.Presets[oidc.preset]; !ok {
						return fmt.Errorf("unknown --oidc-preset %q; known: google", oidc.preset)
					}
				}
				split := func(v string) []string {
					if v == "" {
						return nil
					}
					return strings.Split(v, ",")
				}
				oidcAuth, err = web.NewOIDC(ctx, web.OIDCConfig{
					Issuer: issuer, ClientID: oidc.clientID, ClientSecret: oidc.clientSecret, RedirectURL: oidc.redirectURL,
					Admins: split(oidc.admins), Approvers: split(oidc.approvers), AllowedDomain: oidc.domain, SessionSecret: oidc.sessionSecret,
				})
				if err != nil {
					return err
				}
				oidcAuth.Log = a.log
				auth = oidcAuth
			} else {
				fmt.Fprintln(os.Stderr, "warning: no --oidc-* flags; the web UI has no sign-in and every visitor is an admin")
			}
			// Only serve warns: other commands run on every call or on a
			// schedule, and their notices are in their own output.
			if !opts.deliversNotices() {
				a.log.Warn("lifecycle notices are only logged: configure --slack-webhook to send them to a Slack channel")
			}
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			errc := make(chan error, 2)
			if !noWorker {
				go func() { errc <- worker.New(a.svc, interval, a.log).Run(ctx) }()
			}
			var sl *web.Slack
			if slack.botToken != "" || slack.signingSecret != "" {
				if slack.botToken == "" || slack.signingSecret == "" || slack.channel == "" {
					return fmt.Errorf("slack needs --slack-bot-token, --slack-signing-secret, and --slack-channel together")
				}
				if oidcAuth == nil {
					return fmt.Errorf("slack approvals need OIDC sign-in configured so approvers are known")
				}
				sl = &web.Slack{SigningSecret: slack.signingSecret, BotToken: slack.botToken, Channel: slack.channel, IsApprover: oidcAuth.IsApprover, BaseURL: baseURL}
			}
			var hist web.History
			if a.history != nil {
				hist = a.history
			}
			srv := web.New(a.svc, a.log, auth, sl, hist)
			go func() { errc <- srv.ListenAndServe(ctx, addr) }()
			fmt.Fprintf(os.Stderr, "serving on %s (refresh every %s: %v, sign-in: %v)\n", addr, every, !noWorker, auth.Enabled())
			err = <-errc
			cancel()
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}),
	}
	cmd.Flags().StringVar(&addr, "addr", ":8080", "listen address")
	cmd.Flags().StringVar(&every, "every", "1m", "refresh interval, with a unit such as 30s or 5m")
	cmd.Flags().BoolVar(&noWorker, "no-worker", false, "serve the UI without periodic refresh")
	cmd.Flags().StringVar(&oidc.preset, "oidc-preset", "", "well-known provider: google")
	cmd.Flags().StringVar(&oidc.issuer, "oidc-issuer", "", "OpenID Connect issuer URL (overrides the preset)")
	cmd.Flags().StringVar(&oidc.clientID, "oidc-client-id", "", "OAuth client id")
	cmd.Flags().StringVar(&oidc.clientSecret, "oidc-client-secret", "", "OAuth client secret (prefer PLAYPLACE_OIDC_CLIENT_SECRET)")
	cmd.Flags().StringVar(&oidc.redirectURL, "oidc-redirect-url", defaultRedirectURL, "callback URL registered with the provider")
	cmd.Flags().StringVar(&oidc.admins, "admins", "", "comma-separated emails that see and manage every account (admins can also approve)")
	cmd.Flags().StringVar(&oidc.approvers, "approvers", "", "comma-separated emails that may approve and deny requests")
	cmd.Flags().StringVar(&baseURL, "base-url", "", "public URL of this UI, used in notifications and Slack messages")
	cmd.Flags().StringVar(&slack.signingSecret, "slack-signing-secret", "", "Slack app signing secret (prefer PLAYPLACE_SLACK_SIGNING_SECRET); enables approve/deny buttons in Slack")
	cmd.Flags().StringVar(&slack.botToken, "slack-bot-token", "", "Slack bot token with chat:write and users:read.email (prefer PLAYPLACE_SLACK_BOT_TOKEN)")
	cmd.Flags().StringVar(&slack.channel, "slack-channel", "", "channel id or name where approval messages go")
	cmd.Flags().StringVar(&oidc.domain, "allowed-domain", "", "only allow sign-in from this email domain")
	cmd.Flags().StringVar(&oidc.sessionSecret, "session-secret", "", "HMAC key for session cookies; random per start when empty (prefer PLAYPLACE_SESSION_SECRET)")
	return cmd
}

// useFeed sends everything the app logs to a new TUI feed and returns it.
// The TUI owns the terminal, so the app must not write to stderr while it
// runs. Service warnings and lifecycle notices show as toasts and in the
// notifications window.
func (a *app) useFeed() *tui.Feed {
	feed := tui.NewFeed()
	a.setLogger(slog.New(feed.Handler()))
	return feed
}

func tuiCmd(opts *options) *cobra.Command {
	var snapshot, keys string
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Browse accounts in a terminal UI",
		RunE: withApp(opts, false, func(ctx context.Context, a *app, _ []string) error {
			label := "default"
			switch {
			case opts.AWSProfile != "":
				label = opts.AWSProfile
			case opts.AWSEndpoint != "":
				label = strings.TrimPrefix(strings.TrimPrefix(opts.AWSEndpoint, "http://"), "https://")
			}
			opts := tui.Options{Context: label, Operator: operator()}
			if a.history != nil {
				opts.History = a.history
			}
			opts.Feed = a.useFeed()
			if snapshot != "" {
				var w, h int
				if _, err := fmt.Sscanf(snapshot, "%dx%d", &w, &h); err != nil {
					return fmt.Errorf("--snapshot wants WIDTHxHEIGHT, e.g. 120x32")
				}
				fmt.Println(tui.Snapshot(ctx, a.svc, opts, w, h, keys))
				return nil
			}
			return tui.Run(ctx, a.svc, opts)
		}),
	}
	cmd.Flags().StringVar(&snapshot, "snapshot", "", "render one frame at WIDTHxHEIGHT and exit")
	cmd.Flags().StringVar(&keys, "keys", "", "keys to press before the snapshot")
	cmd.Flags().MarkHidden("snapshot")
	cmd.Flags().MarkHidden("keys")
	return cmd
}

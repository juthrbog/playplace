package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"log/slog"

	"github.com/spf13/cobra"

	"playplace/internal/audit"
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
		RunE: withApp(opts, false, func(ctx context.Context, a *app, _ []string) error {
			info, err := a.svc.Init(ctx)
			if err != nil {
				return err
			}
			if info.Created {
				fmt.Println("created organization", info.OrgID)
			} else {
				fmt.Println("using organization", info.OrgID)
			}
			fmt.Println("management account:", info.ManagementAccountID)
			fmt.Println("playground OU:", info.PlaygroundOUID)
			return nil
		}),
	}
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
			fmt.Printf("queued %s for %s: $%.0f/month, %dd. Approve with `playplace approve %s`.\n", q.Name, q.Owner, q.BudgetUSD, int(q.TTL.Hours()/24), q.Name)
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
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%dd\t$%.0f\t%s\t%s\n", q.Name, q.Owner, q.RequestedBy, q.Via, int(q.TTL.Hours()/24), q.BudgetUSD, q.RequestedAt.Local().Format("Jan 2 15:04"), note)
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
			q, err := a.svc.UpdateRequest(ctx, args[0], operator(), e)
			if err != nil {
				return err
			}
			fmt.Printf("%s: owner %s, %dd, $%.0f/month", q.Name, q.Owner, int(q.TTL.Hours()/24), q.BudgetUSD)
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
			if err := a.svc.Withdraw(ctx, args[0], operator()); err != nil {
				return err
			}
			fmt.Printf("withdrew %s\n", args[0])
			return nil
		}),
	}
}

func approveCmd(opts *options) *cobra.Command {
	var noWait bool
	cmd := &cobra.Command{
		Use:   "approve NAME",
		Short: "Approve a pending request and create the account",
		Args:  cobra.ExactArgs(1),
		RunE: withApp(opts, true, func(ctx context.Context, a *app, args []string) error {
			acc, err := a.svc.Approve(ctx, args[0], operator())
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
	return cmd
}

func denyCmd(opts *options) *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "deny NAME",
		Short: "Deny a pending request",
		Args:  cobra.ExactArgs(1),
		RunE: withApp(opts, true, func(ctx context.Context, a *app, args []string) error {
			if err := a.svc.Deny(ctx, args[0], operator(), reason); err != nil {
				return err
			}
			fmt.Printf("denied %s\n", args[0])
			return nil
		}),
	}
	cmd.Flags().StringVar(&reason, "reason", "", "sent to the requester")
	return cmd
}

// waitForAccount polls a creation until AWS finishes or the deadline passes.
func waitForAccount(ctx context.Context, a *app, acc *core.Account, maxWait time.Duration) error {
	fmt.Print("waiting for AWS ")
	deadline := time.Now().Add(maxWait)
	var err error
	for acc.Status == core.StatusCreating {
		if time.Now().After(deadline) {
			fmt.Println()
			return fmt.Errorf("still creating after %s; it will finish on a later command or reconcile", maxWait)
		}
		select {
		case <-ctx.Done():
			fmt.Println()
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
		fmt.Print(".")
		if acc, err = a.svc.PollCreate(ctx, acc.RequestID); err != nil {
			fmt.Println()
			return err
		}
	}
	fmt.Println()
	if acc.Status != core.StatusActive {
		return fmt.Errorf("%s is %s: %s", acc.Name, acc.Status, acc.LastError)
	}
	fmt.Printf("%s is active: account %s, owner %s, expires %s\n", acc.Name, acc.ProviderID, acc.Owner, acc.ExpiresAt.Format("2006-01-02"))
	return nil
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
	fmt.Fprintln(w, "NAME\tOWNER\tSTATUS\tACCOUNT\tEXPIRES\tBUDGET")
	for _, a := range accounts {
		exp := a.ExpiresAt.Format("2006-01-02")
		if a.ProviderID == "" {
			exp = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t$%.0f\n", a.Name, a.Owner, a.Status, a.ProviderID, exp, a.BudgetUSD)
	}
	w.Flush()
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
			if acc.ProviderState != "" {
				fmt.Printf("AWS state:   %s\n", acc.ProviderState)
			}
			if acc.RequestID != "" {
				fmt.Printf("request id:  %s\n", acc.RequestID)
			}
			fmt.Printf("email:       %s\nbudget:      $%.2f/month\ncreated:     %s\n", acc.Email, acc.BudgetUSD, acc.CreatedAt.Format(time.RFC3339))
			if acc.ProviderID != "" {
				fmt.Printf("expires:     %s\n", acc.ExpiresAt.Format(time.RFC3339))
			}
			if acc.WarnedAt != nil {
				fmt.Printf("warned:      %s\n", acc.WarnedAt.Format(time.RFC3339))
			}
			if acc.CloseRequestedAt != nil {
				fmt.Printf("close asked: %s\n", acc.CloseRequestedAt.Format(time.RFC3339))
			}
			if acc.ClosedAt != nil {
				fmt.Printf("closed seen: %s\n", acc.ClosedAt.Format(time.RFC3339))
			}
			if acc.LastError != "" {
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
				fmt.Fprintf(w, "%s\t%s\t%s\t$%.0f\t%s\n", acc.Name, acc.Owner, acc.ProviderID, acc.BudgetUSD, spend)
			}
			return w.Flush()
		}),
	}
	cmd.Flags().IntVar(&days, "days", 30, "window in days")
	return cmd
}

func historyCmd(opts *options) *cobra.Command {
	var owner, since string
	var limit int
	cmd := &cobra.Command{
		Use:   "history [ACCOUNT]",
		Short: "Show what happened to an account, an owner's accounts, or everything",
		Args:  cobra.MaximumNArgs(1),
		RunE: withApp(opts, false, func(ctx context.Context, a *app, args []string) error {
			if a.history == nil {
				return fmt.Errorf("history is off (--history none); use --history file or cloudwatch")
			}
			f := audit.Filter{Owner: owner, Limit: limit}
			if len(args) == 1 {
				f.Account = args[0]
			}
			if since != "" {
				d, err := parseDuration(since)
				if err != nil {
					return fmt.Errorf("since: %w", err)
				}
				f.Since = time.Now().Add(-d)
			}
			events, err := a.history.Query(ctx, f)
			if err != nil {
				return err
			}
			if len(events) == 0 {
				fmt.Printf("nothing recorded in %s\n", a.history.Where())
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "WHEN\tEVENT\tACCOUNT\tBY\tWHAT HAPPENED")
			for i := len(events) - 1; i >= 0; i-- { // oldest first reads as a story
				e := events[i]
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", e.At.Local().Format("2006-01-02 15:04"), e.Event, e.Account, e.Actor, e.Message)
			}
			return w.Flush()
		}),
	}
	cmd.Flags().StringVar(&owner, "owner", "", "only events for this owner's accounts")
	cmd.Flags().StringVar(&since, "since", "", "only events newer than this many days, e.g. 30")
	cmd.Flags().IntVar(&limit, "limit", 100, "most events to show")
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

func serveCmd(opts *options) *cobra.Command {
	var addr, every string
	var noWorker bool
	var oidc struct {
		preset, issuer, clientID, clientSecret, redirectURL, admins, approvers, domain, sessionSecret string
	}
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
which is only safe on a private network.`,
		RunE: withApp(opts, false, func(ctx context.Context, a *app, _ []string) error {
			interval, err := parseInterval(every)
			if err != nil {
				return fmt.Errorf("every: %w", err)
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
	cmd.Flags().StringVar(&oidc.redirectURL, "oidc-redirect-url", "http://localhost:8080/auth/callback", "callback URL registered with the provider")
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
			// The TUI owns the terminal, so nothing may write to stderr while
			// it runs. Service warnings become notices in the dashboard.
			feed := tui.NewFeed()
			opts.Feed = feed
			quiet := slog.New(feed.Handler())
			a.svc.SetLogger(quiet)
			if cw, ok := a.history.(*audit.CloudWatch); ok {
				cw.Log = quiet
			}
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

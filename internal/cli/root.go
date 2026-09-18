// Package cli defines the playplace command tree.
package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/user"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/spf13/cobra"

	"playplace/internal/audit"
	"playplace/internal/core"
	"playplace/internal/notify"
	awsprovider "playplace/internal/provider/aws"
)

// app is everything a command needs once flags are parsed.
type app struct {
	opts    *options
	log     *slog.Logger
	svc     *core.Service
	history audit.Sink // nil when --history none
}

// operator names the person running the CLI, for audit tags. Whoever holds
// management-account credentials is trusted; this only records who it was.
func operator() string {
	if v := os.Getenv("PLAYPLACE_OPERATOR"); v != "" {
		return v
	}
	name := "operator"
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	if host, _ := os.Hostname(); host != "" {
		return name + "@" + host
	}
	return name
}

// refresh is the pass most commands run first. Changes are reported on
// stderr so stdout stays clean for the command's own output.
func (a *app) refresh(ctx context.Context) {
	if a.opts.NoRefresh {
		return
	}
	sum, err := a.svc.Refresh(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "refresh:", err)
	}
	if !sum.Empty() {
		fmt.Fprintln(os.Stderr, "refresh:", sum.String())
	}
}

// New returns the root command.
func New() *cobra.Command {
	opts := &options{}
	root := &cobra.Command{
		Use:           "playplace",
		Short:         "Create, watch, and close engineer playground cloud accounts",
		Long:          "playplace keeps no database. The playground OU and the tags on each account are the state.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return applyEnv(cmd)
		},
	}
	opts.bind(root)

	root.AddCommand(
		initCmd(opts),
		requestCmd(opts),
		requestsCmd(opts),
		editRequestCmd(opts),
		approveCmd(opts),
		denyCmd(opts),
		withdrawCmd(opts),
		createCmd(opts),
		listCmd(opts),
		showCmd(opts),
		extendCmd(opts),
		closeCmd(opts),
		costsCmd(opts),
		historyCmd(opts),
		reconcileCmd(opts),
		serveCmd(opts),
		tuiCmd(opts),
	)
	return root
}

// newApp builds the provider and service.
func newApp(ctx context.Context, opts *options) (*app, error) {
	lvl := new(slog.LevelVar)
	if err := lvl.UnmarshalText([]byte(opts.LogLevel)); err != nil {
		return nil, fmt.Errorf("log-level: %w", err)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))

	cfg, err := opts.coreConfig()
	if err != nil {
		return nil, err
	}
	prov, err := awsprovider.New(ctx, opts.awsConfig())
	if err != nil {
		return nil, err
	}
	var notifier core.Notifier = notify.Log{Logger: log}
	if opts.SlackWebhook != "" {
		notifier = notify.Multi{notifier, notify.Slack{WebhookURL: opts.SlackWebhook}}
	}
	svc := core.NewService(prov, notifier, cfg, log)
	a := &app{opts: opts, log: log, svc: svc}

	switch opts.History {
	case "none", "":
	case "file":
		a.history = &audit.File{Path: opts.HistoryFile}
	case "cloudwatch":
		awscfg, err := awsprovider.LoadConfig(ctx, opts.awsConfig())
		if err != nil {
			return nil, err
		}
		client := cloudwatchlogs.NewFromConfig(awscfg, func(o *cloudwatchlogs.Options) {
			if opts.AWSEndpoint != "" {
				o.BaseEndpoint = aws.String(opts.AWSEndpoint)
			}
		})
		a.history = &audit.CloudWatch{Client: client, Group: opts.HistoryGroup, RetentionDays: 365, Log: log}
	default:
		return nil, fmt.Errorf("--history must be file, cloudwatch, or none, not %q", opts.History)
	}
	if a.history != nil {
		svc.SetAuditor(a.history)
	}
	return a, nil
}

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"playplace/internal/audit"
)

func historyCmd(opts *options) *cobra.Command {
	var filter audit.Filter
	var since, until string
	var asJSON bool
	cmd := &cobra.Command{Use: "history [NAME]", Short: "Search lifecycle history by journey, owner, actor, event, or date", Args: cobra.MaximumNArgs(1)}
	cmd.PreRunE = func(*cobra.Command, []string) error {
		if filter.Limit < 1 || filter.Limit > 500 {
			return errors.New("history limit must be between 1 and 500")
		}
		return nil
	}
	cmd.RunE = withApp(opts, false, func(ctx context.Context, a *app, args []string) error {
		if a.history == nil {
			return errors.New("history is disabled; configure a history store")
		}
		if len(args) == 1 {
			filter.Account = args[0]
		}
		now := time.Now().UTC()
		var err error
		filter.Since, err = historyTime(since, now)
		if err != nil {
			return fmt.Errorf("since: %w", err)
		}
		filter.Until, err = historyTime(until, now)
		if err != nil {
			return fmt.Errorf("until: %w", err)
		}
		return printHistory(ctx, cmd.OutOrStdout(), cmd.ErrOrStderr(), a.history, filter, asJSON)
	})
	f := cmd.Flags()
	f.StringVar(&filter.JourneyID, "journey", "", "exact journey ID (names can refer to several journeys)")
	f.StringVar(&filter.AccountID, "account-id", "", "exact AWS account ID")
	f.StringVar(&filter.Owner, "owner", "", "owner at event time")
	f.StringVar(&filter.Actor, "actor", "", "who initiated the event")
	f.StringVar(&filter.Event, "event", "", "event type, e.g. approved")
	f.StringVar(&since, "since", "", "inclusive start: RFC3339 timestamp or age, e.g. 30d")
	f.StringVar(&until, "until", "", "exclusive end: RFC3339 timestamp or age")
	f.IntVar(&filter.Limit, "limit", 100, "page size, from 1 to 500")
	f.StringVar(&filter.Cursor, "cursor", "", "continue a query using its next cursor and the same filters")
	f.BoolVar(&filter.OldestFirst, "oldest-first", false, "read oldest-first instead of newest-first")
	f.BoolVar(&asJSON, "json", false, "output events, next cursor, and completeness as JSON")
	return cmd
}

func historyTime(value string, now time.Time) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t.UTC(), nil
	}
	d, err := parseDuration(value)
	if err != nil || d <= 0 {
		return time.Time{}, errors.New("use an RFC3339 timestamp or a positive age such as 30d")
	}
	return now.Add(-d), nil
}

func printHistory(ctx context.Context, out, warnings io.Writer, h audit.Sink, f audit.Filter, asJSON bool) error {
	p, err := h.Search(ctx, f)
	if err != nil {
		return err
	}
	if asJSON {
		// Include the resolved bounds so relative-date queries can be continued
		// without changing the query's time window on the next invocation.
		return json.NewEncoder(out).Encode(struct {
			audit.Page
			Since time.Time `json:"since"`
			Until time.Time `json:"until"`
		}{p, f.Since, f.Until})
	}
	if p.Incomplete {
		fmt.Fprintln(warnings, "warning: history is incomplete; some stored records could not be read")
	}
	if len(p.Events) == 0 {
		message := "nothing recorded for this query"
		if p.Incomplete {
			message = "no readable events for this query; history is incomplete"
		}
		_, err = fmt.Fprintf(out, "%s in %s\n", message, h.Where())
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "WHEN (UTC)\tEVENT\tACCOUNT\tJOURNEY\tBY\tWHAT HAPPENED")
	for _, e := range p.Events {
		journey := e.JourneyID
		if journey == "" {
			journey = "uncorrelated"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", e.At.UTC().Format(time.RFC3339Nano), e.Event, e.Account, journey, e.Actor, e.Message)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if p.Next != "" {
		fmt.Fprintf(warnings, "next page: reuse these filters with --cursor %s\n", p.Next)
		if !f.Since.IsZero() {
			fmt.Fprintf(warnings, "resolved --since %s\n", f.Since.UTC().Format(time.RFC3339Nano))
		}
		if !f.Until.IsZero() {
			fmt.Fprintf(warnings, "resolved --until %s\n", f.Until.UTC().Format(time.RFC3339Nano))
		}
	}
	return nil
}

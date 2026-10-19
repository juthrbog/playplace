// Package notify delivers core.Message values to humans.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"playplace/internal/core"
)

// LogMessage is the message of every log record that Log writes. The TUI
// uses it to tell a lifecycle notice from a warning.
const LogMessage = "notify"

// Log writes each notice to the logger at Warn, so the default --log-level
// shows it. Without another notifier, the log is the only place a notice
// goes. Next to Slack, it records what was sent.
type Log struct{ Logger *slog.Logger }

func (l Log) Notify(_ context.Context, m core.Message) error {
	l.Logger.Warn(LogMessage, "subject", m.Subject, "owner", m.Owner, "body", m.Body)
	return nil
}

// Slack posts to an incoming webhook.
type Slack struct {
	WebhookURL string
	Client     *http.Client
}

// slackText escapes the three characters that Slack reads as markup in the
// text of a message. A notice holds text that people wrote, such as the
// reason of a denial. Without the escape, <!channel> in it notifies the
// whole channel. Slack shows the escaped characters as they were written.
var slackText = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func (s Slack) Notify(ctx context.Context, m core.Message) error {
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	body, _ := json.Marshal(map[string]string{
		"text": fmt.Sprintf("*%s*\n%s\nOwner: %s", slackText.Replace(m.Subject), slackText.Replace(m.Body), slackText.Replace(m.Owner)),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("slack webhook returned %s", res.Status)
	}
	return nil
}

// Multi fans out to several notifiers and joins their errors. Every notifier
// runs, also after an earlier one fails.
type Multi []core.Notifier

func (m Multi) Notify(ctx context.Context, msg core.Message) error {
	var errs []error
	for _, n := range m {
		if err := n.Notify(ctx, msg); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

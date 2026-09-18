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
	"time"

	"playplace/internal/core"
)

// Log writes notifications to the logger. Useful for local development.
type Log struct{ Logger *slog.Logger }

func (l Log) Notify(_ context.Context, m core.Message) error {
	l.Logger.Info("notify", "subject", m.Subject, "owner", m.Owner, "body", m.Body)
	return nil
}

// Slack posts to an incoming webhook.
type Slack struct {
	WebhookURL string
	Client     *http.Client
}

func (s Slack) Notify(ctx context.Context, m core.Message) error {
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	body, _ := json.Marshal(map[string]string{
		"text": fmt.Sprintf("*%s*\n%s\nOwner: %s", m.Subject, m.Body, m.Owner),
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

// Multi fans out to several notifiers and joins their errors.
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

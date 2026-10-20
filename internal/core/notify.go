package core

import (
	"context"
	"errors"
)

// Message is a notification about an account.
type Message struct {
	Subject   string
	Body      string
	Owner     string
	AccountID string
}

// Notifier delivers messages to humans.
type Notifier interface {
	Notify(ctx context.Context, m Message) error
}

// awsNoticeText takes the place of the text of a failed call to AWS in a
// notice. A notice goes to the log and to Slack, and the raw text holds AWS
// request IDs.
const awsNoticeText = "The call to AWS failed. An admin can read the details in the playplace log."

// noticeText is the text of err in a notice about the account a. A nil err
// gives "". A failed call to AWS gives awsNoticeText, and the log keeps the
// raw error at the warn level, so the detail stays. Core writes its other
// errors for people, so they keep their words.
func (s *Service) noticeText(a *Account, err error) string {
	var failed *ProviderError
	switch {
	case err == nil:
		return ""
	case errors.As(err, &failed):
		s.log.Warn("notice hides a failed AWS call", "account", a.Name, "account_id", a.ProviderID, "err", err)
		return awsNoticeText
	}
	return err.Error()
}

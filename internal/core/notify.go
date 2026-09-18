package core

import "context"

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

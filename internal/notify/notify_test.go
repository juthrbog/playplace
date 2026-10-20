package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"playplace/internal/core"
)

// A notice to Slack shows text that people wrote as they wrote it. Slack
// markup in it, such as a mention of the whole channel, stays plain text.
func TestSlackEscapesTheMarkupOfANotice(t *testing.T) {
	got := make(chan string, 1)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		var msg struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Error(err)
		}
		got <- msg.Text
	}))
	t.Cleanup(hook.Close)

	m := core.Message{Subject: "Playground request denied: box", Body: "ops denied box. Reason: <!channel> R&D said <no>", Owner: "anna@example.com"}
	if err := (Slack{WebhookURL: hook.URL}).Notify(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	want := "*Playground request denied: box*\nops denied box. Reason: &lt;!channel&gt; R&amp;D said &lt;no&gt;\nOwner: anna@example.com"
	if text := <-got; text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
}

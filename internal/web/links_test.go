package web

import (
	"net/url"
	"strings"
	"testing"
)

// The Slack message links to the slip of its request. The query survives
// sign-in, and the hash scrolls to the slip.
func TestSlackMessageLinksToTheSlip(t *testing.T) {
	ts, _, _, sl, stub := slackServer(t)
	sl.BaseURL = "https://pp.example/"
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"slacked"}}, true)
	stub.mu.Lock()
	texts := append([]string(nil), stub.texts...)
	stub.mu.Unlock()
	const want = "\n<https://pp.example/?req=slacked#req-slacked|Open in playplace>"
	if len(texts) != 1 || !strings.Contains(texts[0], want) {
		t.Fatalf("posted texts = %q, want one that holds %q", texts, want)
	}
}

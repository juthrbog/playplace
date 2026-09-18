package web

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"playplace/internal/core"
)

// Slack posts approval messages with Approve and Deny buttons and handles the
// clicks. It is optional; without it, approvers act in the web UI. The
// request itself lives in the OU tag queue, so a click here and a click in
// the UI act on the same record.
type Slack struct {
	SigningSecret string
	BotToken      string
	Channel       string
	IsApprover    func(email string) bool
	BaseURL       string
	Client        *http.Client

	svc *core.Service
	log *slog.Logger
}

func (s *Slack) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// postRequest sends a message with buttons to the approvals channel.
func (s *Slack) postRequest(ctx context.Context, q core.Request) error {
	text := fmt.Sprintf("*%s* requested playground account `%s` for *%s*: $%.0f/month, %d days.", q.RequestedBy, q.Name, q.Owner, q.BudgetUSD, int(q.TTL.Hours()/24))
	if q.Purpose != "" {
		text += "\nPurpose: " + q.Purpose
	}
	if s.BaseURL != "" {
		text += fmt.Sprintf("\n<%s/#pending|Open in playplace>", strings.TrimRight(s.BaseURL, "/"))
	}
	msg := map[string]any{
		"channel": s.Channel,
		"text":    "Playground account requested: " + q.Name,
		"blocks": []any{
			map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": text}},
			map[string]any{"type": "actions", "elements": []any{
				map[string]any{"type": "button", "action_id": "approve", "value": q.Name, "style": "primary", "text": map[string]any{"type": "plain_text", "text": "Approve"}},
				map[string]any{"type": "button", "action_id": "deny", "value": q.Name, "style": "danger", "text": map[string]any{"type": "plain_text", "text": "Deny"},
					"confirm": map[string]any{"title": map[string]any{"type": "plain_text", "text": "Deny " + q.Name + "?"}, "text": map[string]any{"type": "mrkdwn", "text": "The requester will be told it was denied."}, "confirm": map[string]any{"type": "plain_text", "text": "Deny"}, "deny": map[string]any{"type": "plain_text", "text": "Keep"}}},
			}},
		},
	}
	return s.api(ctx, "chat.postMessage", msg, nil)
}

// interact handles block_actions from Slack: verify the signature, map the
// clicker to an email, check they may approve, act, and rewrite the message.
func (s *Slack) interact(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read", http.StatusBadRequest)
		return
	}
	if !s.verify(r.Header.Get("X-Slack-Request-Timestamp"), r.Header.Get("X-Slack-Signature"), body, time.Now()) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	form, _ := parseForm(body)
	var payload struct {
		Type        string `json:"type"`
		ResponseURL string `json:"response_url"`
		User        struct {
			ID string `json:"id"`
		} `json:"user"`
		Actions []struct {
			ActionID string `json:"action_id"`
			Value    string `json:"value"`
		} `json:"actions"`
	}
	if err := json.Unmarshal([]byte(form["payload"]), &payload); err != nil || payload.Type != "block_actions" || len(payload.Actions) == 0 {
		http.Error(w, "unexpected payload", http.StatusBadRequest)
		return
	}
	// Slack wants a fast 200; do the work and report through response_url.
	w.WriteHeader(http.StatusOK)
	act := payload.Actions[0]
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		email, err := s.userEmail(ctx, payload.User.ID)
		if err != nil {
			s.respond(ctx, payload.ResponseURL, "Could not look up your Slack email: "+err.Error(), true)
			return
		}
		if s.IsApprover == nil || !s.IsApprover(email) {
			s.respond(ctx, payload.ResponseURL, email+" is not an approver.", true)
			return
		}
		switch act.ActionID {
		case "approve":
			a, err := s.svc.Approve(ctx, act.Value, email)
			if err != nil {
				s.respond(ctx, payload.ResponseURL, "Approve failed: "+err.Error(), true)
				return
			}
			s.respond(ctx, payload.ResponseURL, fmt.Sprintf(":white_check_mark: *%s* approved `%s` for %s. AWS is creating it.", email, a.Name, a.Owner), false)
		case "deny":
			if err := s.svc.Deny(ctx, act.Value, email, "denied in Slack"); err != nil {
				s.respond(ctx, payload.ResponseURL, "Deny failed: "+err.Error(), true)
				return
			}
			s.respond(ctx, payload.ResponseURL, fmt.Sprintf(":no_entry: *%s* denied `%s`.", email, act.Value), false)
		}
	}()
}

// verify checks Slack's v0 request signature and rejects replays older than 5 minutes.
func (s *Slack) verify(ts, sig string, body []byte, now time.Time) bool {
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || now.Sub(time.Unix(t, 0)) > 5*time.Minute || time.Unix(t, 0).Sub(now) > 5*time.Minute {
		return false
	}
	mac := hmac.New(sha256.New, []byte(s.SigningSecret))
	mac.Write([]byte("v0:" + ts + ":"))
	mac.Write(body)
	want := "v0=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(sig))
}

// sign produces the header value verify expects; used by tests.
func (s *Slack) sign(ts string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(s.SigningSecret))
	mac.Write([]byte("v0:" + ts + ":"))
	mac.Write(body)
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

func (s *Slack) userEmail(ctx context.Context, userID string) (string, error) {
	var out struct {
		OK   bool `json:"ok"`
		User struct {
			Profile struct {
				Email string `json:"email"`
			} `json:"profile"`
		} `json:"user"`
		Error string `json:"error"`
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://slack.com/api/users.info?user="+userID, nil)
	req.Header.Set("Authorization", "Bearer "+s.BotToken)
	res, err := s.client().Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", err
	}
	if !out.OK {
		return "", fmt.Errorf("users.info: %s", out.Error)
	}
	if out.User.Profile.Email == "" {
		return "", fmt.Errorf("the Slack app needs the users:read.email scope")
	}
	return strings.ToLower(out.User.Profile.Email), nil
}

// respond replaces the original message (or adds an ephemeral error).
func (s *Slack) respond(ctx context.Context, responseURL, text string, ephemeral bool) {
	msg := map[string]any{"text": text, "replace_original": !ephemeral}
	if ephemeral {
		msg["response_type"] = "ephemeral"
	}
	body, _ := json.Marshal(msg)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, responseURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if res, err := s.client().Do(req); err != nil {
		s.log.Warn("slack respond", "err", err)
	} else {
		res.Body.Close()
	}
}

func (s *Slack) api(ctx context.Context, method string, in any, out any) error {
	body, _ := json.Marshal(in)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/"+method, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.BotToken)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	res, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var envelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	raw, _ := io.ReadAll(res.Body)
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if !envelope.OK {
		return fmt.Errorf("%s: %s", method, envelope.Error)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func parseForm(body []byte) (map[string]string, error) {
	out := map[string]string{}
	for _, kv := range strings.Split(string(body), "&") {
		k, v, _ := strings.Cut(kv, "=")
		k, _ = unescape(k)
		v, _ = unescape(v)
		out[k] = v
	}
	return out, nil
}

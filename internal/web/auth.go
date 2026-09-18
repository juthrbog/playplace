package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Viewer is the signed-in person. Admins see and act on every account;
// everyone else sees only accounts they own.
type Viewer struct {
	Email    string
	Name     string
	Admin    bool
	Approver bool // may approve and deny requests; every admin is one
}

type viewerKey struct{}

// ViewerFrom returns the viewer stored by the auth middleware.
func ViewerFrom(ctx context.Context) Viewer {
	v, _ := ctx.Value(viewerKey{}).(Viewer)
	return v
}

// Authenticator decides who a request belongs to.
type Authenticator interface {
	// Register adds any routes the scheme needs (login, callback, logout).
	Register(mux *http.ServeMux)
	// Authenticate returns the viewer, or false when the request must sign in.
	Authenticate(r *http.Request) (Viewer, bool)
	// LoginPath is where unauthenticated browsers are sent.
	LoginPath() string
	// Enabled reports whether sign-in is enforced at all.
	Enabled() bool
}

// NoAuth treats every request as an operator with admin rights. It is the
// mode for local development and for deployments guarded by other means.
type NoAuth struct{}

func (NoAuth) Register(*http.ServeMux) {}
func (NoAuth) Authenticate(*http.Request) (Viewer, bool) {
	return Viewer{Email: "operator", Name: "operator", Admin: true, Approver: true}, true
}
func (NoAuth) LoginPath() string { return "/" }
func (NoAuth) Enabled() bool     { return false }

// OIDCConfig configures sign-in through any OpenID Connect provider.
type OIDCConfig struct {
	Issuer        string // discovery URL, e.g. https://accounts.google.com
	ClientID      string
	ClientSecret  string
	RedirectURL   string   // must match the provider's registered callback
	Admins        []string // emails with fleet-wide rights
	Approvers     []string // emails that may approve and deny requests
	AllowedDomain string   // optional: only emails at this domain may sign in
	SessionSecret string   // HMAC key for the session cookie; random when empty
	SessionTTL    time.Duration
}

// Presets fill in the issuer for well-known providers.
var Presets = map[string]string{
	"google": "https://accounts.google.com",
}

// OIDCAuth implements Authenticator with the authorization code flow plus
// PKCE, and a signed cookie session.
type OIDCAuth struct {
	// Log receives the detail of failed sign-ins; the browser gets a short
	// message without the provider's error body. nil discards.
	Log *slog.Logger

	cfg       OIDCConfig
	oauth     oauth2.Config
	verifier  *oidc.IDTokenVerifier
	secret    []byte
	admins    map[string]bool
	approvers map[string]bool
}

// NewOIDC discovers the provider and returns a ready authenticator.
func NewOIDC(ctx context.Context, cfg OIDCConfig) (*OIDCAuth, error) {
	if cfg.Issuer == "" || cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RedirectURL == "" {
		return nil, errors.New("oidc needs issuer, client id, client secret, and redirect url")
	}
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery at %s: %w", cfg.Issuer, err)
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 12 * time.Hour
	}
	secret := []byte(cfg.SessionSecret)
	if len(secret) == 0 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, err
		}
	}
	a := &OIDCAuth{
		cfg: cfg,
		oauth: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  cfg.RedirectURL,
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
		verifier:  provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		secret:    secret,
		admins:    map[string]bool{},
		approvers: map[string]bool{},
	}
	for _, e := range cfg.Admins {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			a.admins[e] = true
			a.approvers[e] = true
		}
	}
	for _, e := range cfg.Approvers {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			a.approvers[e] = true
		}
	}
	return a, nil
}

func (a *OIDCAuth) Enabled() bool     { return true }
func (a *OIDCAuth) LoginPath() string { return "/auth/login" }

func (a *OIDCAuth) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/login", a.login)
	mux.HandleFunc("GET /auth/callback", a.callback)
	mux.HandleFunc("POST /auth/logout", a.logout)
}

const (
	sessionCookie = "pp_session"
	flowCookie    = "pp_flow"
)

// session is what the signed cookie carries.
type session struct {
	Email string    `json:"e"`
	Name  string    `json:"n"`
	Exp   time.Time `json:"x"`
}

// flow holds the transient values of one login attempt.
type flow struct {
	State    string    `json:"s"`
	Nonce    string    `json:"o"`
	Verifier string    `json:"v"`
	Exp      time.Time `json:"x"`
}

func (a *OIDCAuth) login(w http.ResponseWriter, r *http.Request) {
	f := flow{State: random(), Nonce: random(), Verifier: oauth2.GenerateVerifier(), Exp: time.Now().Add(10 * time.Minute)}
	a.setSigned(w, r, flowCookie, f, f.Exp)
	url := a.oauth.AuthCodeURL(f.State, oidc.Nonce(f.Nonce), oauth2.S256ChallengeOption(f.Verifier))
	http.Redirect(w, r, url, http.StatusFound)
}

func (a *OIDCAuth) callback(w http.ResponseWriter, r *http.Request) {
	var f flow
	if err := a.readSigned(r, flowCookie, &f); err != nil || time.Now().After(f.Exp) {
		http.Error(w, "sign-in expired, start again", http.StatusBadRequest)
		return
	}
	a.clear(w, r, flowCookie)
	if r.URL.Query().Get("state") != f.State {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	if e := r.URL.Query().Get("error"); e != "" {
		http.Error(w, "provider refused: "+e, http.StatusUnauthorized)
		return
	}
	tok, err := a.oauth.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.VerifierOption(f.Verifier))
	if err != nil {
		a.warn("code exchange failed", err)
		http.Error(w, "sign-in failed: the provider did not accept the code; start again", http.StatusUnauthorized)
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	idt, err := a.verifier.Verify(r.Context(), raw)
	if err != nil {
		a.warn("id token invalid", err)
		http.Error(w, "sign-in failed: the provider's token could not be verified; start again", http.StatusUnauthorized)
		return
	}
	if idt.Nonce != f.Nonce {
		http.Error(w, "nonce mismatch", http.StatusUnauthorized)
		return
	}
	var claims struct {
		Email    string          `json:"email"`
		Verified json.RawMessage `json:"email_verified"`
		Name     string          `json:"name"`
		HD       string          `json:"hd"`
	}
	if err := idt.Claims(&claims); err != nil || claims.Email == "" {
		http.Error(w, "id token has no email claim", http.StatusUnauthorized)
		return
	}
	// Providers encode email_verified as bool or string; only an explicit
	// false is a rejection.
	if v := strings.Trim(string(claims.Verified), `"`); v == "false" {
		http.Error(w, "email is not verified at the provider", http.StatusUnauthorized)
		return
	}
	email := strings.ToLower(claims.Email)
	if d := strings.ToLower(a.cfg.AllowedDomain); d != "" && !strings.HasSuffix(email, "@"+d) && !strings.EqualFold(claims.HD, d) {
		http.Error(w, "this sign-in is limited to "+a.cfg.AllowedDomain, http.StatusForbidden)
		return
	}
	s := session{Email: email, Name: claims.Name, Exp: time.Now().Add(a.cfg.SessionTTL)}
	a.setSigned(w, r, sessionCookie, s, s.Exp)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (a *OIDCAuth) logout(w http.ResponseWriter, r *http.Request) {
	a.clear(w, r, sessionCookie)
	http.Redirect(w, r, "/auth/login", http.StatusFound)
}

// IsApprover reports whether an email may approve, for callers outside the
// request path such as the Slack adapter.
func (a *OIDCAuth) IsApprover(email string) bool { return a.approvers[strings.ToLower(email)] }

func (a *OIDCAuth) Authenticate(r *http.Request) (Viewer, bool) {
	var s session
	if err := a.readSigned(r, sessionCookie, &s); err != nil || time.Now().After(s.Exp) {
		return Viewer{}, false
	}
	return Viewer{Email: s.Email, Name: s.Name, Admin: a.admins[s.Email], Approver: a.approvers[s.Email]}, true
}

// ---- signed cookies --------------------------------------------------------

func (a *OIDCAuth) sign(payload []byte) string {
	mac := hmac.New(sha256.New, a.secret)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *OIDCAuth) verify(value string) ([]byte, error) {
	p, sig, ok := strings.Cut(value, ".")
	if !ok {
		return nil, errors.New("malformed cookie")
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return nil, err
	}
	want, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, a.secret)
	mac.Write(payload)
	if !hmac.Equal(mac.Sum(nil), want) {
		return nil, errors.New("bad signature")
	}
	return payload, nil
}

func (a *OIDCAuth) warn(msg string, err error) {
	if a.Log != nil {
		a.Log.Warn(msg, "err", err)
	}
}

func (a *OIDCAuth) setSigned(w http.ResponseWriter, r *http.Request, name string, v any, exp time.Time) {
	payload, _ := json.Marshal(v)
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    a.sign(payload),
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		Secure:   a.secure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *OIDCAuth) readSigned(r *http.Request, name string, v any) error {
	c, err := r.Cookie(name)
	if err != nil {
		return err
	}
	payload, err := a.verify(c.Value)
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, v)
}

// secure reports whether cookies should carry the Secure flag: the request
// came in over TLS, or the deployment is behind a TLS-terminating proxy,
// which the https redirect URL tells us.
func (a *OIDCAuth) secure(r *http.Request) bool {
	return r.TLS != nil || strings.HasPrefix(a.cfg.RedirectURL, "https://")
}

func (a *OIDCAuth) clear(w http.ResponseWriter, r *http.Request, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.secure(r), SameSite: http.SameSiteLaxMode})
}

func random() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// --access-portal-url arrives on the command line or through
// PLAYPLACE_ACCESS_PORTAL_URL. Only an absolute https address with a host
// and no user info is accepted.
func TestAccessPortalURLConfig(t *testing.T) {
	const good = "https://example.awsapps.com/start"
	for _, tc := range []struct {
		name string
		args []string
		env  string
		want string
		bad  string // substring of the error; empty means accepted
	}{
		{"unset", nil, "", "", ""},
		{"flag", []string{"--access-portal-url", good}, "", good, ""},
		{"environment", nil, " " + good + " ", good, ""},
		{"flag wins over environment", []string{"--access-portal-url", good}, "https://other.example/start", good, ""},
		{"blank environment", nil, "  ", "", ""},
		{"http", nil, "http://example.awsapps.com/start", "", "must start with https://"},
		{"javascript", nil, "javascript:alert(1)", "", "must start with https://"},
		{"user info", nil, "https://user:pw@example.awsapps.com/start", "", "user name or password"},
		{"missing host", nil, "https:///start", "", "must name a host"},
		{"relative", nil, "/start", "", "full URL"},
		{"no scheme", nil, "example.awsapps.com/start", "", "full URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanEnv(t)
			if tc.env != "" {
				t.Setenv("PLAYPLACE_ACCESS_PORTAL_URL", tc.env)
			}
			var o options
			cmd := &cobra.Command{Use: "test"}
			o.bind(cmd)
			if err := cmd.ParseFlags(tc.args); err != nil {
				t.Fatal(err)
			}
			if err := applyEnv(cmd); err != nil {
				t.Fatal(err)
			}
			cfg, err := o.coreConfig()
			switch {
			case tc.bad == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tc.bad != "" && (err == nil || !strings.Contains(err.Error(), tc.bad) || !strings.HasPrefix(err.Error(), "access-portal-url ")):
				t.Fatalf("err = %v, want it to name the flag and contain %q", err, tc.bad)
			case tc.bad == "" && cfg.AccessPortalURL != tc.want:
				t.Fatalf("AccessPortalURL = %q, want %q", cfg.AccessPortalURL, tc.want)
			}
		})
	}
}

// A portal address without --permission-set is allowed, but the log warns
// that playplace does not grant the access the link leads to.
func TestAccessPortalURLWithoutPermissionSetWarns(t *testing.T) {
	const warning = "access-portal-url is set but --permission-set is not"
	for _, tc := range []struct {
		name string
		args []string
		warn bool
	}{
		{"no portal", nil, false},
		{"portal without permission set", []string{"--access-portal-url", "https://example.awsapps.com/start"}, true},
		{"portal with permission set", []string{"--access-portal-url", "https://example.awsapps.com/start", "--permission-set", "Playground"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanEnv(t)
			useFake(t)
			args := append([]string{"--history", "none"}, tc.args...)
			text := captureStderr(t, func() {
				a, err := newApp(context.Background(), globalOptions(t, args...))
				if err != nil {
					t.Error(err)
					return
				}
				if want := globalOptions(t, args...).AccessPortalURL; a.svc.Config().AccessPortalURL != want {
					t.Errorf("service AccessPortalURL = %q, want %q", a.svc.Config().AccessPortalURL, want)
				}
			})
			if got := strings.Contains(text, warning); got != tc.warn {
				t.Fatalf("warning shown = %v, want %v:\n%s", got, tc.warn, text)
			}
		})
	}
}

package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestEnvMarksFlagsChanged(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	var limit float64
	root.PersistentFlags().Float64Var(&limit, "max-budget", 500, "")
	sub := &cobra.Command{Use: "edit", Run: func(*cobra.Command, []string) {}}
	var override bool
	sub.Flags().BoolVar(&override, "override-limits", false, "")
	root.AddCommand(sub)
	root.SetArgs([]string{"edit"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLAYPLACE_OVERRIDE_LIMITS", "true")
	t.Setenv("PLAYPLACE_MAX_BUDGET", "900")
	if err := applyEnv(sub); err != nil {
		t.Fatal(err)
	}
	// Commands decide by Changed whether a flag was given at all, so an
	// environment value must count.
	if !override || !sub.Flags().Changed("override-limits") {
		t.Fatalf("override-limits: value %v changed %v", override, sub.Flags().Changed("override-limits"))
	}
	if limit != 900 || !root.PersistentFlags().Changed("max-budget") {
		t.Fatalf("max-budget: value %v changed %v", limit, root.PersistentFlags().Changed("max-budget"))
	}
}

func TestDurationsAndIntervals(t *testing.T) {
	for _, s := range []string{"-7d", "-72h", "NaN", "Inf", "1e300"} {
		if _, err := parseDuration(s); err == nil {
			t.Errorf("parseDuration(%q) should fail", s)
		}
	}
	for s, want := range map[string]time.Duration{"0": 0, "7": 7 * 24 * time.Hour, "2d": 48 * time.Hour, "36h": 36 * time.Hour} {
		if got, err := parseDuration(s); err != nil || got != want {
			t.Errorf("parseDuration(%q) = %v %v, want %v", s, got, err, want)
		}
	}
	for _, s := range []string{"30", "0", "-1m", "soon"} {
		if _, err := parseInterval(s); err == nil {
			t.Errorf("parseInterval(%q) should fail", s)
		}
	}
	if got, err := parseInterval("5m"); err != nil || got != 5*time.Minute {
		t.Errorf("parseInterval(5m) = %v %v", got, err)
	}
}

// --ou-id must have the OU ID format that AWS documents. --ou-name next to
// it is refused, because the provider would ignore the name. Values arrive
// on the command line and through the environment, as an operator gives
// them.
func TestOUIDFlag(t *testing.T) {
	const id = "ou-ab12-cd34ef56"
	longest := "ou-" + strings.Repeat("a", 32) + "-" + strings.Repeat("9", 32)
	for _, tc := range []struct {
		name string
		args []string
		env  map[string]string
		want string // the OU ID that the provider gets
		bad  string // substring of the error. Empty means accepted
	}{
		{"name only", []string{"--ou-name", "Lab"}, nil, "", ""},
		{"id", []string{"--ou-id", id}, nil, id, ""},
		{"id from the environment", nil, map[string]string{"PLAYPLACE_OU_ID": " " + id + " "}, id, ""},
		{"longest id", []string{"--ou-id", longest}, nil, longest, ""},
		{"blank id", nil, map[string]string{"PLAYPLACE_OU_ID": "", "PLAYPLACE_OU_NAME": "Lab"}, "", ""},
		{"blank name from an env template", nil, map[string]string{"PLAYPLACE_OU_ID": id, "PLAYPLACE_OU_NAME": " "}, id, ""},
		{"root id", []string{"--ou-id", "r-ab12"}, nil, "", `"r-ab12" is not an OU ID`},
		{"uppercase", []string{"--ou-id", "OU-AB12-CD34EF56"}, nil, "", "is not an OU ID"},
		{"short root part", []string{"--ou-id", "ou-ab1-cd34ef56"}, nil, "", "is not an OU ID"},
		{"short OU part", []string{"--ou-id", "ou-ab12-cd34ef5"}, nil, "", "is not an OU ID"},
		{"long root part", []string{"--ou-id", "ou-" + strings.Repeat("a", 33) + "-cd34ef56"}, nil, "", "is not an OU ID"},
		{"ARN", []string{"--ou-id", "arn:aws:organizations::222222222222:ou/o-ab12cd34ef/" + id}, nil, "", "is not an OU ID"},
		{"name as id", nil, map[string]string{"PLAYPLACE_OU_ID": "Playground"}, "", "is not an OU ID"},
		{"name on the command line", []string{"--ou-id", id, "--ou-name", "Playground"}, nil, "", "--ou-name (\"Playground\") and --ou-id (" + id + ") are both set"},
		{"name in the environment", []string{"--ou-id", id}, map[string]string{"PLAYPLACE_OU_NAME": "Lab"}, "", "are both set"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range []string{"PLAYPLACE_OU_ID", "PLAYPLACE_OU_NAME"} {
				t.Setenv(k, "") // restored after the test
				if err := os.Unsetenv(k); err != nil {
					t.Fatal(err)
				}
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
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
			cfg, err := o.awsConfig()
			switch {
			case tc.bad == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tc.bad != "" && (err == nil || !strings.Contains(err.Error(), tc.bad)):
				t.Fatalf("err = %v, want it to contain %q", err, tc.bad)
			case tc.bad == "" && cfg.PlaygroundOUID != tc.want:
				t.Fatalf("OU ID = %q, want %q", cfg.PlaygroundOUID, tc.want)
			}
		})
	}
}

// Every command builds its app with newApp. newApp must refuse a malformed
// --ou-id, and --ou-name next to --ou-id, before it calls AWS. The AWS
// endpoint is a local server that counts the calls that reach it.
func TestCommandsRefuseABadOUIDBeforeAWS(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	// The environment of the developer must not change the result.
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "PLAYPLACE_") {
			t.Setenv(k, "") // restored after the test
			if err := os.Unsetenv(k); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Setenv("PLAYPLACE_AWS_ENDPOINT", srv.URL)
	t.Setenv("PLAYPLACE_AWS_REGION", "us-east-1")
	t.Setenv("PLAYPLACE_HISTORY", "none")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	const id = "ou-ab12-cd34ef56"
	for _, tc := range []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"root id", []string{"init", "--ou-id", "r-ab12"}, nil, `ou-id: "r-ab12" is not an OU ID`},
		{"both flags", []string{"init", "--ou-id", id, "--ou-name", "Lab"}, nil, `--ou-name ("Lab") and --ou-id (` + id + `) are both set`},
		{"both from the environment", []string{"init"}, map[string]string{"PLAYPLACE_OU_ID": id, "PLAYPLACE_OU_NAME": "Lab"}, "are both set"},
		{"root id for list", []string{"list", "--ou-id", "r-ab12"}, nil, "is not an OU ID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			cmd := New()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%v: err = %v, want it to contain %q", tc.args, err, tc.want)
			}
		})
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("the commands made %d AWS calls before they refused the flags", n)
	}
}

func TestSignInFlagsWithoutIssuerAreRefused(t *testing.T) {
	base := oidcFlags{redirectURL: defaultRedirectURL}
	if got := base.stray(); got != nil {
		t.Fatalf("no sign-in flags: stray = %v", got)
	}
	// An env template that leaves every sign-in value blank means no sign-in.
	if got := (oidcFlags{}).stray(); got != nil {
		t.Fatalf("blank sign-in values: stray = %v", got)
	}
	lost := base
	lost.clientID, lost.admins = "client", "ops@example.com"
	if got := lost.stray(); len(got) != 2 || got[0] != "--oidc-client-id" || got[1] != "--admins" {
		t.Fatalf("issuer missing: stray = %v", got)
	}
	moved := base
	moved.redirectURL = "https://playplace.example.com/auth/callback"
	if got := moved.stray(); len(got) != 1 || got[0] != "--oidc-redirect-url" {
		t.Fatalf("redirect without issuer: stray = %v", got)
	}
	for _, ok := range []oidcFlags{{issuer: "https://idp.example.com", clientID: "c"}, {preset: "google", admins: "a@example.com"}} {
		if got := ok.stray(); got != nil {
			t.Fatalf("%+v: stray = %v", ok, got)
		}
	}
}

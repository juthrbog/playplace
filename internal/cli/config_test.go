package cli

import (
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

package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestCloseAlertAfterConfig(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  time.Duration
		bad   bool
	}{
		{"24h", 24 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"0", 0, true},
		{"0s", 0, true},
		{"-1h", 0, true},
		{"24", 0, true},
		{"bad", 0, true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			var o options
			cmd := &cobra.Command{Use: "test"}
			o.bind(cmd)
			t.Setenv("PLAYPLACE_CLOSE_ALERT_AFTER", tc.value)
			if err := applyEnv(cmd); err != nil {
				t.Fatal(err)
			}
			cfg, err := o.coreConfig()
			if (err != nil) != tc.bad || (!tc.bad && cfg.CloseAlertAfter != tc.want) {
				t.Fatalf("got %v %v, want %v bad=%v", cfg.CloseAlertAfter, err, tc.want, tc.bad)
			}
		})
	}
}

// Zero lifetimes are not "no limit": core would close adopted accounts or
// drop every request at once. Values arrive through the environment, as an
// operator's deployment would set them.
func TestLifetimesThatCoreWouldMisuseAreRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		bad  string // substring of the error; empty means accepted
	}{
		{"defaults", nil, ""},
		{"no ttl ceiling", map[string]string{"PLAYPLACE_MAX_TTL": "0", "PLAYPLACE_DEFAULT_TTL": "400d"}, ""},
		{"no budget ceiling", map[string]string{"PLAYPLACE_MAX_BUDGET": "0", "PLAYPLACE_DEFAULT_BUDGET": "900"}, ""},
		{"warnings off", map[string]string{"PLAYPLACE_WARN_BEFORE": "0"}, ""},
		{"short default, shorter warning", map[string]string{"PLAYPLACE_DEFAULT_TTL": "2d", "PLAYPLACE_WARN_BEFORE": "36h"}, ""},
		{"zero default ttl", map[string]string{"PLAYPLACE_DEFAULT_TTL": "0"}, "default-ttl must be positive"},
		{"zero request ttl", map[string]string{"PLAYPLACE_REQUEST_TTL": "0d"}, "request-ttl must be positive"},
		{"warning as long as the lifetime", map[string]string{"PLAYPLACE_DEFAULT_TTL": "3d"}, "warn-before (3d) must be shorter than default-ttl (3d)"},
		{"warning longer than the lifetime", map[string]string{"PLAYPLACE_WARN_BEFORE": "36500d"}, "warn-before"},
		{"default ttl above ceiling", map[string]string{"PLAYPLACE_DEFAULT_TTL": "120d"}, "default-ttl (120d) is above max-ttl (90d)"},
		{"default budget above ceiling", map[string]string{"PLAYPLACE_DEFAULT_BUDGET": "600"}, "default-budget (600) is above max-budget (500)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var o options
			cmd := &cobra.Command{Use: "test"}
			o.bind(cmd)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if err := applyEnv(cmd); err != nil {
				t.Fatal(err)
			}
			_, err := o.coreConfig()
			switch {
			case tc.bad == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tc.bad != "" && (err == nil || !strings.Contains(err.Error(), tc.bad)):
				t.Fatalf("err = %v, want it to contain %q", err, tc.bad)
			}
		})
	}
}

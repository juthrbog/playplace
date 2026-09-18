package cli

import (
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

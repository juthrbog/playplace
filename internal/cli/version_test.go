package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestReleaseVersion(t *testing.T) {
	previous := version
	version = "1.2.3"
	t.Cleanup(func() { version = previous })
	for _, flag := range []string{"--version", "-v"} {
		cmd := New()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs([]string{flag})
		if err := cmd.Execute(); err != nil || out.String() != "playplace version 1.2.3\n" {
			t.Fatalf("%s = %q, %v", flag, out.String(), err)
		}
	}
}

func TestDistributionCommandsAreOffline(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	history := filepath.Join(t.TempDir(), "must-not-be-created.jsonl")
	t.Setenv("PLAYPLACE_AWS_ENDPOINT", srv.URL)
	t.Setenv("PLAYPLACE_AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("PLAYPLACE_HISTORY_FILE", history)
	// Distribution commands must not even construct/validate lifecycle config.
	t.Setenv("PLAYPLACE_CLOSE_ALERT_AFTER", "invalid-duration")
	for _, args := range [][]string{
		{"--version"}, {"--help"}, {"serve", "--help"},
		{"completion", "bash"}, {"completion", "zsh"}, {"completion", "fish"},
	} {
		cmd := New()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil || !strings.Contains(out.String(), "playplace") {
			t.Fatalf("%v: output=%q err=%v", args, out.String(), err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("distribution commands made %d AWS requests", calls.Load())
	}
	if _, err := os.Stat(history); !os.IsNotExist(err) {
		t.Fatalf("distribution commands touched history: %v", err)
	}
}

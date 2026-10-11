package cli

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	"playplace/internal/core"
	awsprovider "playplace/internal/provider/aws"
	"playplace/internal/provider/fake"
)

// cleanEnv unsets every PLAYPLACE_ variable for the test, so a developer's
// shell cannot change the flag defaults that the test checks.
func cleanEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "PLAYPLACE_") {
			t.Setenv(k, "") // restores the old value after the test
			if err := os.Unsetenv(k); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// useFake makes newApp build the service on the fake provider.
func useFake(t *testing.T) *fake.Provider {
	t.Helper()
	prov := fake.New()
	previous := newProvider
	newProvider = func(context.Context, awsprovider.Config) (core.Provider, error) { return prov, nil }
	t.Cleanup(func() { newProvider = previous })
	return prov
}

// captureStderr returns what fn writes to os.Stderr. newApp builds its log
// handler on os.Stderr, so the capture includes the log.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = out
	func() {
		defer func() { os.Stderr = previous }()
		fn()
	}()
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// globalOptions parses the global flags as the root command would.
func globalOptions(t *testing.T, args ...string) *options {
	t.Helper()
	opts := &options{}
	cmd := &cobra.Command{Use: "test"}
	opts.bind(cmd)
	if err := cmd.PersistentFlags().Parse(args); err != nil {
		t.Fatal(err)
	}
	return opts
}

// Without --slack-webhook the log is the only place a lifecycle notice
// goes, so it must show at the default --log-level. With the webhook, the
// log still records what was sent.
func TestNoticesShowAtTheDefaultLogLevel(t *testing.T) {
	for _, tc := range []struct {
		name    string
		webhook bool
	}{
		{"log only", false},
		{"log and Slack", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanEnv(t)
			useFake(t)
			args := []string{"--history", "none"}
			var posts atomic.Int32
			if tc.webhook {
				srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { posts.Add(1) }))
				t.Cleanup(srv.Close)
				args = append(args, "--slack-webhook", srv.URL)
			}
			ctx := context.Background()
			text := captureStderr(t, func() {
				a, err := newApp(ctx, globalOptions(t, args...))
				if err != nil {
					t.Error(err)
					return
				}
				submitRequest(ctx, t, a, "cli")
			})
			for _, want := range []string{
				`level=WARN msg=notify subject="Playground account requested: dev-alex" owner=approvers`,
				`body="ops requested playground account dev-alex for alex@example.com: $50/month, 14 days."`,
			} {
				if !strings.Contains(text, want) {
					t.Fatalf("log does not contain %q:\n%s", want, text)
				}
			}
			if want := map[bool]int32{false: 0, true: 1}[tc.webhook]; posts.Load() != want {
				t.Fatalf("webhook got %d posts, want %d", posts.Load(), want)
			}
		})
	}
}

// submitRequest submits a request for dev-alex. The service then sends the
// lifecycle notice for a new request.
func submitRequest(ctx context.Context, t *testing.T, a *app, via string) {
	t.Helper()
	if _, err := a.svc.SubmitRequest(ctx, core.RequestInput{Name: "dev-alex", Owner: "alex@example.com", RequestedBy: "ops"}, via); err != nil {
		t.Error(err)
	}
}

// checkNoticeOffStderr fails the test when a lifecycle notice reached
// stderr. The budget warning that newApp logs at startup proves that the
// capture holds the app's own log, so a missing notice means something.
func checkNoticeOffStderr(t *testing.T, text string) {
	t.Helper()
	if !strings.Contains(text, "budgets are alerts-only") {
		t.Fatalf("stderr does not hold the app's own log:\n%s", text)
	}
	if strings.Contains(text, "msg=notify") {
		t.Fatalf("a notice reached stderr while the TUI owns the terminal:\n%s", text)
	}
}

// The TUI owns the terminal. After setLogger, a lifecycle notice goes to the
// logger that the TUI gives, and nothing reaches stderr. newApp runs inside
// the capture, so the app's own log writes to the file that the test reads.
func TestSetLoggerMovesNoticesOffStderr(t *testing.T) {
	cleanEnv(t)
	useFake(t)
	ctx := context.Background()
	var feed bytes.Buffer
	text := captureStderr(t, func() {
		a, err := newApp(ctx, globalOptions(t, "--history", "none"))
		if err != nil {
			t.Error(err)
			return
		}
		a.setLogger(slog.New(slog.NewTextHandler(&feed, &slog.HandlerOptions{Level: slog.LevelWarn})))
		submitRequest(ctx, t, a, "tui")
	})
	checkNoticeOffStderr(t, text)
	if want := `level=WARN msg=notify subject="Playground account requested: dev-alex"`; !strings.Contains(feed.String(), want) {
		t.Fatalf("TUI logger does not contain %q:\n%s", want, feed.String())
	}
}

// tui calls useFeed before it draws. A lifecycle notice sent after that
// must not reach stderr, where it would draw over the TUI frame.
func TestUseFeedMovesNoticesOffStderr(t *testing.T) {
	cleanEnv(t)
	useFake(t)
	ctx := context.Background()
	text := captureStderr(t, func() {
		a, err := newApp(ctx, globalOptions(t, "--history", "none"))
		if err != nil {
			t.Error(err)
			return
		}
		if a.useFeed() == nil {
			t.Error("useFeed returned no feed")
		}
		submitRequest(ctx, t, a, "tui")
	})
	checkNoticeOffStderr(t, text)
}

// serve warns once at startup when only the log carries notices. Other
// commands run on every call or on a schedule, so they do not repeat it.
func TestOnlyServeWarnsWhenNoticesAreOnlyLogged(t *testing.T) {
	const warning = "lifecycle notices are only logged"
	serve := []string{"serve", "--no-worker", "--addr", "127.0.0.1:0"}
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"serve without a channel", serve, 1},
		{"serve with a webhook", append(serve[:len(serve):len(serve)], "--slack-webhook", "https://hooks.slack.invalid/T0/B0/x"), 0},
		{"reconcile", []string{"reconcile"}, 0},
		{"requests", []string{"requests"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanEnv(t)
			useFake(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.args[0] == "serve" {
				cancel() // serve starts, logs its startup lines, and stops
			}
			text := captureStderr(t, func() {
				_, err := captureStdout(t, func() error {
					cmd := New()
					cmd.SetArgs(append(tc.args, "--history", "none"))
					return cmd.ExecuteContext(ctx)
				})
				if err != nil {
					t.Error(err)
				}
			})
			if got := strings.Count(text, warning); got != tc.want {
				t.Fatalf("warning printed %d times, want %d:\n%s", got, tc.want, text)
			}
		})
	}
}

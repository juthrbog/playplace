package cli

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"playplace/internal/core"
	awsprovider "playplace/internal/provider/aws"
	"playplace/internal/provider/fake"
)

// heldCosts holds every cost pull until release closes, as a slow call to
// AWS does. started receives a value when the first pull begins.
type heldCosts struct {
	core.Provider
	started chan struct{}
	release chan struct{}
}

func (p *heldCosts) Costs(ctx context.Context, id string, from, to time.Time) ([]core.CostPoint, error) {
	select {
	case p.started <- struct{}{}:
	default:
	}
	<-p.release
	return p.Provider.Costs(ctx, id, from, to)
}

// A stop of serve waits for the answers in flight, also with the worker on.
// The worker stops at once, so serve must wait for the web server. A
// decision post can be in flight, and a cut one can leave a request claimed
// with no account.
func TestServeFinishesTheAnswersInFlightBeforeItStops(t *testing.T) {
	cleanEnv(t)
	prov := fake.New()
	tags := map[string]string{core.TagManaged: "true", core.TagOwner: "anna@example.com", core.TagExpires: time.Now().Add(10 * 24 * time.Hour).UTC().Format(time.RFC3339), core.TagBudget: "50"}
	prov.Seed(core.RemoteAccount{ProviderID: "111111111111", Name: "dev-box", JoinedAt: time.Now().Add(-24 * time.Hour), Tags: tags})
	held := &heldCosts{Provider: prov, started: make(chan struct{}, 1), release: make(chan struct{})}
	previous := newProvider
	newProvider = func(context.Context, awsprovider.Config) (core.Provider, error) { return held, nil }
	t.Cleanup(func() { newProvider = previous })

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan error, 1)
	go func() {
		cmd := New()
		cmd.SetArgs([]string{"serve", "--addr", addr, "--every", "1h", "--history", "none"})
		stopped <- cmd.ExecuteContext(ctx)
	}()
	base := "http://" + addr
	// A connection that the client keeps open with no request counts as
	// busy for 5 seconds when the server stops, so each call has its own.
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	for start := time.Now(); ; {
		if res, err := client.Get(base + "/healthz"); err == nil {
			_ = res.Body.Close()
			break
		}
		if time.Since(start) > 10*time.Second {
			t.Fatal("serve did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The account page pulls the spend, and the pull waits.
	answer := make(chan int, 1)
	go func() {
		res, err := client.Get(base + "/accounts/111111111111")
		if err != nil {
			answer <- 0
			return
		}
		// The whole page must arrive, not only its status.
		if _, err := io.Copy(io.Discard, res.Body); err != nil {
			answer <- 0
		} else {
			answer <- res.StatusCode
		}
		_ = res.Body.Close()
	}()
	select {
	case <-held.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the account page did not pull the spend")
	}
	cancel()
	select {
	case err := <-stopped:
		t.Fatalf("serve stopped with an answer in flight: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(held.release)
	if code := <-answer; code != http.StatusOK {
		t.Errorf("the answer in flight = %d, want 200", code)
	}
	select {
	case err := <-stopped:
		if err != nil {
			t.Errorf("serve = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop after the last answer")
	}
}

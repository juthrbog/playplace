// Package worker runs the refresh pass on a schedule for `serve`.
package worker

import (
	"context"
	"log/slog"
	"time"

	"playplace/internal/core"
)

// Worker calls Service.Refresh every Interval.
type Worker struct {
	svc      *core.Service
	log      *slog.Logger
	Interval time.Duration
}

func New(svc *core.Service, interval time.Duration, log *slog.Logger) *Worker {
	if interval <= 0 {
		interval = time.Minute
	}
	return &Worker{svc: svc, Interval: interval, log: log}
}

// Run blocks until ctx is cancelled. It refreshes once at start, then on the interval.
func (w *Worker) Run(ctx context.Context) error {
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	for {
		w.once(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

func (w *Worker) once(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	start := time.Now()
	defer func() {
		// A panic in one pass (an unexpected nil in a provider response, a
		// malformed tag) must not take the web UI down with it.
		if r := recover(); r != nil {
			w.log.Error("refresh panicked", "panic", r, "took", time.Since(start))
		}
	}()
	sum, err := w.svc.Refresh(ctx)
	if err != nil {
		w.log.Error("refresh failed", "err", err, "took", time.Since(start))
	}
	if !sum.Empty() {
		w.log.Info("refresh", "summary", sum.String(), "took", time.Since(start))
	}
}

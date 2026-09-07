package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// fakeSessions and fakeLive are the janitor's two dependencies with no
// database and no RAM store behind them. Both are touched from the
// janitor's goroutine and read from the test's, so every field is guarded —
// this file runs under -race in CI like the rest of the tree.
type fakeSessions struct {
	mu     sync.Mutex
	purged int64
	err    error
	calls  int
}

func (f *fakeSessions) PurgeExpired(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.purged, f.err
}

func (f *fakeSessions) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeLive struct {
	mu      sync.Mutex
	dropped int
	calls   int
	gotNow  time.Time
	// swept is closed on the FIRST sweep of a run. Sweep is the last thing
	// a tick does, so a receive on it means one whole tick completed —
	// which is what every case below waits on instead of sleeping.
	swept chan struct{}
	once  sync.Once
}

func newFakeLive(dropped int) *fakeLive {
	return &fakeLive{dropped: dropped, swept: make(chan struct{})}
}

func (f *fakeLive) Sweep(now time.Time) int {
	f.mu.Lock()
	f.calls++
	f.gotNow = now
	dropped := f.dropped
	f.mu.Unlock()
	f.once.Do(func() { close(f.swept) })
	return dropped
}

func (f *fakeLive) state() (calls int, gotNow time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.gotNow
}

// TestJanitorRun drives the loop at millisecond cadence and asserts the
// contract main.go depends on: every tick purges AND sweeps, a failing
// purge does not stop either, and ctx cancellation ends Run promptly.
func TestJanitorRun(t *testing.T) {
	t.Parallel()

	// The instant Sweep must be handed. A fixed clock rather than time.Now
	// so the assertion is an equality, not a window.
	fixed := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		purged  int64
		purgErr error
		dropped int
	}{
		{name: "nothing to do", purged: 0, dropped: 0},
		{name: "both report work", purged: 3, dropped: 2},
		{name: "purge error does not stop the sweep", purgErr: errors.New("database is locked"), dropped: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sessions := &fakeSessions{purged: tc.purged, err: tc.purgErr}
			live := newFakeLive(tc.dropped)
			j := newJanitor(sessions, live, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Millisecond)
			j.now = func() time.Time { return fixed }

			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() {
				defer close(done)
				j.Run(ctx)
			}()

			select {
			case <-live.swept:
			case <-time.After(5 * time.Second):
				cancel()
				<-done
				t.Fatal("janitor never completed a tick")
			}

			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Run did not return after ctx was cancelled")
			}

			if got := sessions.callCount(); got == 0 {
				t.Error("PurgeExpired was never called")
			}
			calls, gotNow := live.state()
			if calls == 0 {
				t.Error("Sweep was never called")
			}
			// The sweep runs on the same tick as the purge, error or not —
			// the one behaviour a naive `if err != nil { continue }` would
			// silently drop.
			if !gotNow.Equal(fixed) {
				t.Errorf("Sweep got now = %v, want the injected clock %v", gotNow, fixed)
			}
		})
	}
}

// TestNewJanitorDefaultsInterval pins the substitution: a zero interval
// would make time.NewTicker panic inside Run, on a goroutine main never
// hears from.
func TestNewJanitorDefaultsInterval(t *testing.T) {
	t.Parallel()

	for _, given := range []time.Duration{0, -time.Second} {
		j := newJanitor(&fakeSessions{}, newFakeLive(0), slog.New(slog.NewTextHandler(io.Discard, nil)), given)
		if j.interval != janitorInterval {
			t.Errorf("newJanitor(interval=%v): interval = %v, want %v", given, j.interval, janitorInterval)
		}
	}
	if j := newJanitor(&fakeSessions{}, newFakeLive(0), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Minute); j.interval != time.Minute {
		t.Errorf("newJanitor(interval=1m): interval = %v, want 1m", j.interval)
	}
}

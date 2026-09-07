package main

import (
	"context"
	"log/slog"
	"time"
)

// sessionPurger is *auth.Manager as the janitor needs it, and liveSweeper is
// *livestate.Store as the janitor needs it: one method each, so this file's
// own test can drive the loop with no database and no RAM store at all —
// the same reason internal/mockplane declares an interface per source
// instead of importing the concrete repository.
type sessionPurger interface {
	PurgeExpired(ctx context.Context) (int64, error)
}

type liveSweeper interface {
	Sweep(now time.Time) int
}

// janitor purges expired sessions and sweeps abandoned live-state
// directives on its own interval until ctx is done. It runs on its own
// goroutine and never returns an error to main: a failed purge just means
// expired rows (or stale directives) linger a bit longer, not a reason to
// bring the server down.
//
// It is a type rather than the free function it used to be for exactly the
// reason [traffic.Recorder] is one: a background loop with a tick, a
// cancellation contract and an error path nobody sees deserves a test of
// its own, and a free function reading the package-level janitorInterval
// could only be tested by waiting an hour. Both dependencies and the
// interval are injected, so the test drives many ticks in milliseconds
// against fakes.
type janitor struct {
	sessions sessionPurger
	live     liveSweeper
	log      *slog.Logger

	// interval is how often the loop runs. [newJanitor] substitutes
	// janitorInterval for a non-positive value, the way
	// [traffic.NewRecorder] defaults its own FlushEvery: a zero here would
	// make time.NewTicker panic, and a caller that forgot to set it wants
	// the production cadence, not a crash.
	interval time.Duration

	// now is the clock handed to [liveSweeper.Sweep]. Injected purely so a
	// test can assert WHICH instant the sweep is given without racing the
	// wall clock; production always leaves it as time.Now.
	now func() time.Time
}

// newJanitor builds the process's one housekeeping loop.
func newJanitor(sessions sessionPurger, live liveSweeper, log *slog.Logger, interval time.Duration) *janitor {
	if interval <= 0 {
		interval = janitorInterval
	}
	return &janitor{sessions: sessions, live: live, log: log, interval: interval, now: time.Now}
}

// Run ticks until ctx is done, then returns. It does no work on the way out:
// unlike [traffic.Recorder.Run], which owes a final flush because its queue
// holds records nothing else will ever write, a skipped purge costs nothing
// — the next process start sweeps the same rows.
func (j *janitor) Run(ctx context.Context) {
	ticker := time.NewTicker(j.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := j.sessions.PurgeExpired(ctx)
			if err != nil {
				j.log.Error("purge expired sessions", "err", err)
			} else if n > 0 {
				j.log.Info("purged expired sessions", "count", n)
			}

			// Runs even when the purge above failed: the two are
			// independent, and a database error is no reason to let RAM
			// grow. livestate.Store is pure RAM (see its own doc comment:
			// it must never reach SQLite), so an abandoned workspace's
			// directives would otherwise live for the lifetime of the
			// process instead of just DefaultTTL past their last Set.
			if dropped := j.live.Sweep(j.now()); dropped > 0 {
				j.log.Info("swept expired live-state directives", "count", dropped)
			}
		}
	}
}

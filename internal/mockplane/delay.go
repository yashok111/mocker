// The session layer's timing effects — delay and pause — as the serve path
// applies them. Split out of respond.go 2026-09-03; the text is unchanged.
package mockplane

import (
	"context"
	"net/http"
	"time"

	"github.com/yashok111/mocker/internal/livestate"
)

// maxSimulatedDelay caps settings.DelayMs (DESIGN §6's "задержка", part of
// response assembly): an admin fat-fingering a delay in milliseconds where
// they meant seconds must not be able to tie up a server goroutine — and,
// past this, a real HTTP client's own timeout — for longer than a generous
// upper bound on how slow a mock is ever useful to simulate. 30s is well
// past any sane frontend loading-state test while still nowhere near a
// typical client or proxy timeout.
const maxSimulatedDelay = 30 * time.Second

// effectiveDelayMs resolves the delay actually applied to a request across
// all three layers DESIGN §4 stacks a delay on: SESSION beats ROW beats
// WORKSPACE settings — the outermost layer always wins, the identical
// precedence a live-state STATUS force already has over an override, which
// itself already wins over the document/settings default.
//
// sessionMs is [livestate.Effect.DelayMs]: 0 means no delay directive
// matched (normalize/validateActionFields reject an ActionDelay directive
// whose Ms is 0 — see livestate.go — so 0 is unambiguous here, never a
// stored "delay of zero"). rowDelayMs is nil unless an active override pins
// one — respond.go's own call site passes nil for it unless overrideActive,
// because row itself is nil whenever there is no row at all and row.DelayMs
// would panic on every request to an operation without one; custom.go
// cannot share a row-TYPED helper at all, since it carries a *customep.Row
// where respond.go carries an *overrides.Row, which is why this function
// takes the already-resolved *int rather than either row type.
//
// Pulled out as its own pure function so "a per-operation delay is clamped
// exactly like a workspace-wide one" is directly testable —
// clampedDelay(effectiveDelayMs(...)) — without a test ever having to wait
// out [maxSimulatedDelay] itself: whichever value this returns still goes
// through the SAME [awaitDelay]/[clampedDelay] call serveGenerated always
// used, so an operator cannot use a per-operation override — or a session
// delay — to tie up a goroutine any longer than a workspace-wide setting
// already could.
func effectiveDelayMs(sessionMs int, rowDelayMs *int, settingsMs int) int {
	if sessionMs > 0 {
		return sessionMs
	}
	if rowDelayMs != nil {
		return *rowDelayMs
	}
	return settingsMs
}

// resolvePause resolves ONE request's pause outcome from the [livestate.Effect]
// Apply already returned, per DESIGN §14's pause rules. eff.Pause and
// eff.Refused are never both true — reserveParkLocked (livestate/store.go)
// sets exactly one — so the two branches below never race each other:
//
//   - Refused (rule 7: the workspace already held MaxPausedPerWorkspace
//     parked requests) is marked on the traffic row's own Notes and served
//     normally with no wait at all.
//   - Pause blocks through [awaitPause] and releases its park slot through
//     exactly one deferred Unpark, whichever way the wait ends — released,
//     canceled, or the hold cap expired.
//
// Reports whether the caller may go on to write a response: false only when
// the request's own context ended while parked, the one case nothing below
// this call may write anything.
func resolvePause(r *http.Request, eff livestate.Effect) bool {
	if eff.Refused {
		markPauseRefused(r) // rule 7: served normally, but an operator can see why on the traffic screen
		return true
	}
	if !eff.Pause {
		return true
	}
	defer eff.Unpark()
	return awaitPause(r.Context(), eff, livestate.MaxPauseHold)
}

// awaitPause blocks on a matched pause until a wakeup shows it has lifted,
// ctx ends, or hold has elapsed since the FIRST park — never reset by a
// spurious wakeup (rule 3) — whichever comes first. hold is a parameter
// rather than a hardcoded [livestate.MaxPauseHold] so a test can prove the
// cap actually fires without waiting out the real 10s, the identical split
// [clampedDelay]/[maxSimulatedDelay] already uses for the delay side;
// [resolvePause] is the one production caller, and it always passes
// [livestate.MaxPauseHold].
//
// Every wakeup re-evaluates through eff.Recheck, NEVER through
// [livestate.Store.Apply] again: Apply CONSUMES — it decrements a fail
// counter — and is documented (this file's own doc comment on serveGenerated)
// as running exactly once per request. A parked request that re-called Apply
// on every spurious wakeup would burn one unit of an armed fail directive per
// wakeup for a response nobody ever received, and a pause+fail pair would
// lose its forced status entirely. Recheck also handles the slot accounting
// (rule 2) atomically, so this loop never touches eff.Unpark itself — that
// stays the caller's job, exactly once, from a defer.
//
// Reports true when the caller should proceed to write a response — the
// pause lifted, or hold expired (rule 3: serve late rather than invent a
// status the operator never asked for) — and false only when ctx ended
// first, meaning the request is being torn down and nothing may be written.
func awaitPause(ctx context.Context, eff livestate.Effect, hold time.Duration) bool {
	if !eff.Pause {
		return true
	}
	wake := eff.Wake
	deadline := time.After(hold)
	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadline:
			return true
		case <-wake:
			stillPaused, nextWake := eff.Recheck()
			if !stillPaused {
				return true
			}
			wake = nextWake
		}
	}
}

// clampedDelay converts delayMs (domain.Settings.DelayMs) to a
// [time.Duration], hard-capped at [maxSimulatedDelay]. Pulled out of
// awaitDelay as its own pure function so the clamp itself is directly
// testable without a test ever having to actually wait out
// maxSimulatedDelay.
func clampedDelay(delayMs int) time.Duration {
	if delayMs <= 0 {
		return 0
	}
	d := time.Duration(delayMs) * time.Millisecond
	if d > maxSimulatedDelay {
		return maxSimulatedDelay
	}
	return d
}

// awaitDelay sleeps for [clampedDelay] and reports whether the sleep
// completed normally. false means ctx was canceled or its deadline passed
// first — the caller must write nothing further: the request is being torn
// down (client gone, or a graceful shutdown draining in-flight handlers),
// not answered late. delayMs<=0 is the overwhelmingly common case and
// returns true immediately without allocating a timer.
func awaitDelay(ctx context.Context, delayMs int) bool {
	d := clampedDelay(delayMs)
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

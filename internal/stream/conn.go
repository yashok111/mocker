package stream

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

// Conn is one live SSE connection, admitted by [Registry.Open]. It carries no
// session, no workspace record and no repository — only the workspace id it
// was opened for (used to match [Registry.Notify]'s fan-out) and the
// machinery D5 and D9 through D13 describe.
type Conn struct {
	registry    *Registry
	workspaceID int64

	ctx    context.Context
	cancel context.CancelFunc

	// nudge is D5's one-slot wakeup channel. Registry.Notify sends into it;
	// Serve's own loop is the only reader.
	nudge chan struct{}

	// P6c (decisions.md mocker-p6c-live-conns D2): a connection has an
	// identity. id is minted by the registry at Open — an int64 counter
	// from 1 per registry, monotonic for the life of the process, never
	// reused while it runs (and restarting at 1 after a restart, which D2
	// records rather than hides). openedAt is the Open instant. info is
	// what the mock plane's handler knows and this package does not (the
	// endpoint, the path, the peer), set once by SetInfo under the
	// registry's lock so Snapshot reads it consistently; the admin feed
	// never sets it and is never listed.
	id       int64
	openedAt time.Time
	info     Info

	// The three counters a listing reports live, advanced by the serving
	// loop on ITS goroutine and read by Snapshot on the admin's: frames
	// written (pushed ones included — they carry ordinals like every
	// other), frames pushed by an operator, frames skipped for size.
	frames  atomic.Int64
	pushed  atomic.Int64
	skipped atomic.Int64
	// framesIn counts inbound frames — P6d's WebSocket connections read;
	// an SSE connection never advances it and lists 0.
	framesIn atomic.Int64

	// closedByAdmin is set by CloseByAdmin right before the cancel, so the
	// mock plane can tell the traffic row an operator ended this
	// connection (D5, D7) rather than the client, the lifetime or shutdown.
	closedByAdmin atomic.Bool

	// inbox is D3's answer to §30.16's first open question: where a pushed
	// frame lives. A bounded channel on the connection it is addressed to
	// — RAM, never livestate (keyed by operation, not by connection), never
	// SQLite, never a bundle — drained by the serving loop as one more
	// select case and abandoned, never closed, when the loop exits (a
	// close races a Push in flight and panics it, the same reason nudge is
	// never closed). Nil on a connection whose registry is the admin
	// feed's: Serve has no inbox case, so nothing would ever drain it.
	inbox chan PushRequest
}

// Info is what the serving plane tells the registry about a connection so
// a listing can name it (D2, D8): the custom endpoint's id, its path and
// kind, and the peer as httpx.ResolvePeer renders it (the traffic row's
// peerIp, so the two agree). Set once, before the handshake.
type Info struct {
	EndpointID int64
	Path       string
	Kind       string
	Peer       string
}

// SetInfo records i on c. Called by the mock plane between Open and
// Handshake, exactly once; under the registry's lock because Snapshot
// reads the field there and a torn read across a listing would name a
// connection by half its identity.
func (c *Conn) SetInfo(i Info) {
	c.registry.mu.Lock()
	c.info = i
	c.registry.mu.Unlock()
}

// ID is the registry-minted connection id (D2).
func (c *Conn) ID() int64 { return c.id }

// RecordFrame advances the frames counter. Called by the serving loop after
// every successful write, a pushed frame's included.
func (c *Conn) RecordFrame() { c.frames.Add(1) }

// RecordPushed advances the pushed counter — called beside RecordFrame when
// the frame written was an operator's push (D4).
func (c *Conn) RecordPushed() { c.pushed.Add(1) }

// RecordSkipped advances the skipped counter (a tick body over the frame
// cap, P6b D4).
func (c *Conn) RecordSkipped() { c.skipped.Add(1) }

// RecordFrameIn advances the inbound counter (P6d: a WebSocket frame the
// reader goroutine took off the socket, matched or not).
func (c *Conn) RecordFrameIn() { c.framesIn.Add(1) }

// Info returns what SetInfo recorded — the kind above all, which the admin
// push handler reads to refuse an SSE event name on a WebSocket connection
// (P6d D9). Under the registry's lock, like SetInfo.
func (c *Conn) Info() Info {
	c.registry.mu.Lock()
	defer c.registry.mu.Unlock()
	return c.info
}

// CloseByAdmin is D5: mark the connection as closed by an operator, then
// cancel its context. The loop's existing cancelled case exits exactly as
// it does for a registry shutdown; no final frame is written (SSE has no
// close frame, and the client's own EventSource reconnects on its own).
// A compare-and-swap: the first call reports true and the connection is
// closed; every later call reports false and cancels nothing new, so two
// DELETEs racing each other cannot both answer 204 (round-1 finding 4) —
// the loser answers 404 exactly as a DELETE after deregistration would.
func (c *Conn) CloseByAdmin() bool {
	first := c.closedByAdmin.CompareAndSwap(false, true)
	c.cancel()
	return first
}

// closing reports whether the connection's context is already done — a
// closing connection is skipped by Lookup and Snapshot (round-1 finding
// 4): the loop may not have deregistered it yet, but nothing can be
// delivered into it and a listing must not show it as live.
func (c *Conn) closing() bool { return c.ctx.Err() != nil }

// ClosedByAdmin reports whether CloseByAdmin ran — read by the mock plane
// when it builds the traffic row (D7's `closed:admin` token).
func (c *Conn) ClosedByAdmin() bool { return c.closedByAdmin.Load() }

// ReadFunc reads one page of rows starting after since and returns them
// already encoded as the frame's JSON payload, alongside the cursor to
// resume from (lastID) and how many rows the page carried (n) — Serve needs
// n to decide whether the page was full (D5's inner loop) without knowing
// anything about the shape data holds.
//
// A non-nil err means nothing is written and the slot is not re-armed (D5):
// the connection waits for the next nudge rather than retrying against a
// database that may still be unavailable. Logging the failure is the
// caller's own job — this package has no logger, on purpose, for the same
// reason it has no repository. The one exception is an err wrapping
// [ErrRefused]: that closes the connection, and Serve returns it.
type ReadFunc func(ctx context.Context, since int64) (data []byte, lastID int64, n int, err error)

// RecheckFunc is D11's session/workspace re-validation, called on
// ServeConfig.SessionRecheck's own tick. A non-nil error closes the
// connection; Serve returns it unchanged so the caller (internal/admin, via
// the handler's own log) can tell a recheck failure from a write failure if
// it cares to.
type RecheckFunc func(ctx context.Context) error

// ServeConfig is everything Serve needs that this package cannot supply on
// its own — D4's "takes what it needs as parameters" in its literal shape.
// The three durations are D12's own variables, read once by the caller at
// construction and handed in here; this package never reads the environment.
type ServeConfig struct {
	// Since is the cursor Serve starts from — D7's resolution (Last-Event-ID
	// over ?since=) is the caller's own job; Serve treats this exactly as
	// Repo.Since treats its own "since" parameter.
	Since int64

	Read    ReadFunc
	Recheck RecheckFunc

	Ping           time.Duration // D12: MOCKER_STREAM_PING
	FrameTimeout   time.Duration // D12: MOCKER_STREAM_FRAME_TIMEOUT
	SessionRecheck time.Duration // D12: MOCKER_STREAM_SESSION_RECHECK
}

// Context is the connection's own lifetime: cancelled when the request
// ends, or by [Registry.Close] on shutdown (D13's second step).
func (c *Conn) Context() context.Context { return c.ctx }

// Release deregisters c. Serve does this itself on every return path; a
// caller that drives the connection through [Conn.Handshake] and a
// [Writer] of its own (the mock plane's stream loop, P6b) defers it
// instead, exactly once, so [Registry.Close]'s wait ends when the handler
// does.
func (c *Conn) Release() { c.registry.deregister(c) }

// Handshake runs D9's support check and writes the SSE response headers,
// answering ErrUnsupported (and counting it) when the writer cannot take a
// deadline or flush. On success the returned Writer is how frames reach
// the wire: each Write sets the per-frame deadline, writes, flushes and
// clears it — the exemption of §30.6 with ONE frame under a deadline at a
// time.
func (c *Conn) Handshake(w http.ResponseWriter, frameTimeout time.Duration) (*Writer, error) {
	rc := http.NewResponseController(w)
	if err := startStream(rc, w, frameTimeout); err != nil {
		if errors.Is(err, ErrUnsupported) {
			c.registry.refusedUnsupported.Add(1)
		}
		return nil, err
	}
	return &Writer{rc: rc, w: w, frameTimeout: frameTimeout}, nil
}

// Writer writes SSE frames on a connection whose handshake succeeded.
type Writer struct {
	rc           *http.ResponseController
	w            http.ResponseWriter
	frameTimeout time.Duration
}

// Write puts one already-encoded frame (see [EncodeFrame], [PingFrame]) on
// the wire under the per-frame deadline. An error wraps [ErrPeerGone].
func (wr *Writer) Write(payload []byte) error {
	if err := writeSSE(wr.rc, wr.w, wr.frameTimeout, payload); err != nil {
		return fmt.Errorf("%w: %w", ErrPeerGone, err)
	}
	return nil
}

// Serve drives one connection end to end: the D9 support check, the SSE
// handshake, then the loop that lives until the request's own context ends,
// D10's lifetime expires, a RecheckFunc refuses, or a write fails.
//
// It returns nil on every ordinary close — the client going away, the
// registry shutting down, the lifetime timer firing — and a non-nil error
// for [ErrUnsupported] (D9, before a single frame was written), a
// RecheckFunc's own error, or a write failure once the connection was live,
// which comes back wrapped in [ErrPeerGone]: the peer going away mid-stream
// is the ordinary case an SSE server exists to tolerate, and the caller has
// nothing to do about it beyond what deregistering the connection already
// does — the sentinel exists so a caller can tell it from the two that do
// deserve a log line.
//
// Serve always deregisters c from its registry before returning, on every
// path — the defer is unconditional, which is what makes [Registry.Close]'s
// wg.Wait() actually wait for this call rather than for some paths of it.
func (c *Conn) Serve(w http.ResponseWriter, cfg ServeConfig) error {
	defer c.registry.deregister(c)

	rc := http.NewResponseController(w)
	if err := startStream(rc, w, cfg.FrameTimeout); err != nil {
		if errors.Is(err, ErrUnsupported) {
			c.registry.refusedUnsupported.Add(1)
		}
		return err
	}

	cursor := cfg.Since
	lifetime := time.NewTimer(maxStreamLifetime)
	defer lifetime.Stop()
	ping := time.NewTicker(cfg.Ping)
	defer ping.Stop()
	recheck := time.NewTicker(cfg.SessionRecheck)
	defer recheck.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return nil
		case <-lifetime.C:
			// D10: the connection just ends. Last-Event-ID makes the seam
			// invisible in the rows — the client resumes at cursor and the
			// caller never sees an error out of this call for it.
			return nil
		case <-recheck.C:
			if err := runRecheck(c.ctx, cfg.Recheck); err != nil {
				return err
			}
		case <-ping.C:
			if err := writeSSE(rc, w, cfg.FrameTimeout, pingFrame); err != nil {
				return fmt.Errorf("%w: %w", ErrPeerGone, err)
			}
		case <-c.nudge:
			if err := c.drain(rc, w, cfg, lifetime, ping, recheck, &cursor); err != nil {
				if errors.Is(err, errLifetimeExpired) {
					// The same D10 exit as the outer case above: the
					// timer fired while a page was draining, and a
					// time.Timer fires ONCE — drain must say so, or the
					// outer select would wait on a channel that never
					// delivers again and the lifetime would be lost
					// exactly under the steady traffic that drains.
					return nil
				}
				return err
			}
		}
	}
}

// drain is D5's inner sub-loop: read a page, write it, and — only while the
// page came back exactly full — read again immediately rather than waiting
// for another nudge, because a workspace with steady traffic may otherwise
// never catch up (each nudge only promises "at least one more row", not
// "you are caught up").
//
// Every iteration still passes through ctx/lifetime/recheck/ping — via a
// NON-BLOCKING select, so it never waits a second time on those either —
// before the next read; only the nudge slot's own case is skipped, which is
// the one thing draining a full page must never wait on. Written as a bare
// "for n == MaxFrameRows" loop with no select at all, a workspace under
// steady load would hold the connection past D11's recheck and D12's ping
// for as long as traffic kept arriving — A14's stalled peer exercises an
// UNREAD socket; this is the busy one that drains, and it must still yield
// to shutdown, expiry and the ping/recheck ticks while it does.
func (c *Conn) drain(rc *http.ResponseController, w http.ResponseWriter, cfg ServeConfig, lifetime *time.Timer, ping, recheck *time.Ticker, cursor *int64) error {
	for {
		data, lastID, n, err := c.readPage(cfg, *cursor)
		switch {
		case errors.Is(err, errReadFailed):
			// A failed read does NOT re-arm the slot (D5): stop draining
			// and return to Serve's outer select, which waits for the next
			// nudge — D10's lifetime is the backstop that eventually
			// reconnects a connection stuck against a database that never
			// recovers. The connection stays open, because the error was
			// the database's and not the connection's.
			return nil
		case err != nil:
			// ErrRefused: the read itself closed the connection.
			return err
		}
		if werr := writeSSE(rc, w, cfg.FrameTimeout, encodeFrame(lastID, data)); werr != nil {
			return fmt.Errorf("%w: %w", ErrPeerGone, werr)
		}
		*cursor = lastID
		if n < MaxFrameRows {
			// A short page proves the connection has caught up; wait on
			// the slot again.
			return nil
		}

		// The one non-blocking select D5 requires: ctx, lifetime, recheck
		// and ping are all given a chance to fire BEFORE the next read — only
		// the nudge slot's own case is missing, which is the one wait this
		// loop must never re-enter while it is still catching a workspace up.
		select {
		case <-c.ctx.Done():
			// A closed Done stays readable, so Serve's own case sees it
			// again; the lifetime timer below does not, hence the sentinel.
			return nil
		case <-lifetime.C:
			return errLifetimeExpired
		case <-recheck.C:
			if err := runRecheck(c.ctx, cfg.Recheck); err != nil {
				return err
			}
		case <-ping.C:
			if werr := writeSSE(rc, w, cfg.FrameTimeout, pingFrame); werr != nil {
				return fmt.Errorf("%w: %w", ErrPeerGone, werr)
			}
		default:
			// Nothing fired: fall straight into the next read.
		}
	}
}

// errReadFailed classifies a ReadFunc error that is NOT a refusal: the
// caller (drain) keeps the connection and waits for the next nudge. Kept
// private — it is a control-flow label between readPage and drain, never a
// value Serve returns.
var errReadFailed = errors.New("stream: read failed")

// errLifetimeExpired is drain's way of telling Serve that D10's timer fired
// inside the inner loop; Serve turns it into the same nil return its own
// lifetime case has. Never returned to a caller.
var errLifetimeExpired = errors.New("stream: lifetime expired while draining")

// readPage runs cfg.Read once. A refusal ([ErrRefused], the read closing
// the connection) comes back unchanged for Serve to return; any other error
// comes back wrapped in errReadFailed, and stops here in every other
// respect: logging it is the ReadFunc's own job (see its doc comment — this
// package has no logger), and D5 decides that a failed read neither closes
// the connection nor retries.
func (c *Conn) readPage(cfg ServeConfig, since int64) (data []byte, lastID int64, n int, err error) {
	data, lastID, n, err = cfg.Read(c.ctx, since)
	if err != nil {
		if errors.Is(err, ErrRefused) {
			return nil, 0, 0, err
		}
		return nil, 0, 0, fmt.Errorf("%w: %w", errReadFailed, err)
	}
	return data, lastID, n, nil
}

// runRecheck calls fn, tolerating a nil RecheckFunc (a caller with nothing to
// re-validate — the package's own tests, chiefly) as "still valid".
func runRecheck(ctx context.Context, fn RecheckFunc) error {
	if fn == nil {
		return nil
	}
	return fn(ctx)
}

// startStream runs D9's check — before a single frame is written — and, only
// once it passes, writes the SSE response headers and flushes them so the
// client's EventSource opens without waiting on the first data frame.
//
// SetWriteDeadline is checked first because it is the one
// httptest.ResponseRecorder actually fails (D21): a ResponseRecorder DOES
// implement http.Flusher, so a Flush-only check would pass against the exact
// harness [ErrUnsupported]'s own test relies on failing.
func startStream(rc *http.ResponseController, w http.ResponseWriter, frameTimeout time.Duration) error {
	if err := rc.SetWriteDeadline(time.Now().Add(frameTimeout)); err != nil {
		if errors.Is(err, http.ErrNotSupported) {
			return ErrUnsupported
		}
		return err
	}
	// Flush support is checked WITHOUT flushing: the first rc.Flush would
	// itself commit a 200 header, and a writer that takes a deadline but
	// cannot flush would then be discovered one line too late for D9's
	// refusal to be a refusal. The same Unwrap walk http.ResponseController
	// performs, minus the side effect. (Second-reader finding, triaged as
	// real: no such writer exists in this tree, but the order of the two
	// checks is what makes the 501 a promise rather than a coincidence.)
	if !supportsFlush(w) {
		return ErrUnsupported
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	if err := rc.Flush(); err != nil {
		if errors.Is(err, http.ErrNotSupported) {
			return ErrUnsupported
		}
		return err
	}

	// The connection's own deadline stays cleared (§30.6's exemption) — only
	// ONE frame is ever under a deadline at a time, from here on.
	if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}

// supportsFlush reports whether w, or any writer it unwraps to, implements
// http.Flusher — the interface http.ResponseController.Flush resolves
// through the same Unwrap chain.
func supportsFlush(w http.ResponseWriter) bool {
	for {
		if _, ok := w.(http.Flusher); ok {
			return true
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return false
		}
		w = u.Unwrap()
	}
}

// writeSSE sets D12's per-frame deadline, writes payload, flushes, and clears
// the deadline again — always, even on failure, via defer: a deadline left
// set after a failed write would silently bound whatever this connection
// tries to write next, including nothing at all if it is about to close.
func writeSSE(rc *http.ResponseController, w http.ResponseWriter, frameTimeout time.Duration, payload []byte) error {
	if err := rc.SetWriteDeadline(time.Now().Add(frameTimeout)); err != nil {
		return err
	}
	defer func() {
		_ = rc.SetWriteDeadline(time.Time{})
	}()

	if _, err := w.Write(payload); err != nil {
		return err
	}
	return rc.Flush()
}

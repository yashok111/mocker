// Package stream is where P6a's concurrency lives: the connection registry,
// the SSE wire encoding, the write-deadline exemption from the global
// http.Server timeout, and the refusal path for a response that cannot
// stream at all (DESIGN §30, decisions D4/D8/D9).
//
// It knows nothing about sessions, workspaces or repositories — every fact
// this package needs about those is handed in as a parameter or a callback
// (ServeConfig.Read, ServeConfig.Recheck), never imported. That is what lets
// internal/admin own the handler (session, workspace, traffic.Repo) while
// this package owns only the mechanics a later mock-plane stream (P6b) can
// reuse without pulling in the admin plane's own dependencies.
package stream

import (
	"errors"
	"time"
)

// maxStreamConns is D8's process-wide concurrency cap, counted across every
// workspace: the cap protects file descriptors and memory, properties of the
// process, not of any one workspace, and it is deliberately a constant
// rather than a MOCKER_* variable — this slice's three variables are D12's,
// and a per-connection cap belongs to P6b's own §30.11 knobs.
const maxStreamConns = 64

// MaxFrameRows is D6's own limit on how many rows one SSE frame carries — the
// number the traffic screen already asks the poll route for
// (web/src/components/TrafficPage.tsx's POLL_LIMIT), not the poll route's own
// default (100) or ceiling (500). A connection further behind than this sends
// consecutive frames rather than a single oversized one.
const MaxFrameRows = 200

// maxStreamLifetime is D10's connection lifetime: a package-level var, not a
// const, because [A21] requires a test to shorten it. Every other place reads
// it as a value at Serve time — nothing snapshots it earlier — so a test that
// mutates it before opening a connection and restores it (via t.Cleanup,
// after joining the goroutine, per A21) sees the effect without changing
// anything about production behaviour, where it is never touched.
var maxStreamLifetime = 900 * time.Second

// Sentinel errors [Registry.Open] and [Conn.Serve] return. A caller
// (internal/admin's handler) distinguishes them with errors.Is to choose the
// HTTP status: D4's own open question records that the refusal's STATUS CODE
// is written by the admin handler, never by this package — only the SSE
// success headers are.
var (
	// ErrClosed is returned by Open once [Registry.Close] has set its closed
	// flag — the first of its three steps (D13) — so a handshake arriving
	// during shutdown is refused rather than registered into a registry that
	// will never close it.
	ErrClosed = errors.New("stream: registry is closing")

	// ErrCapExceeded is D8's 503: the process already holds maxStreamConns
	// live connections.
	ErrCapExceeded = errors.New("stream: connection cap exceeded")

	// ErrUnsupported is D9's 501: http.ResponseController reported
	// http.ErrNotSupported before a single frame was written — a test
	// recorder, an in-process loopback, or a proxy that unwraps into
	// something that cannot flush or take a write deadline.
	ErrUnsupported = errors.New("stream: response does not support streaming")

	// ErrPeerGone wraps a write failure on a connection that was already
	// live: the peer closed, or D12's per-frame deadline expired on a stalled
	// socket. It is the ORDINARY way an SSE connection ends and the caller
	// has nothing to do about it beyond what deregistration already did —
	// it is a distinct sentinel (rather than a nil return) so that Serve
	// never has to return nil from under a non-nil error, which is exactly
	// the shape the nilerr linter refuses, and so that a caller which wants
	// to log every OTHER failure can errors.Is this one away.
	ErrPeerGone = errors.New("stream: peer gone")

	// ErrRefused is what a [ReadFunc] wraps to CLOSE the connection instead
	// of merely skipping a page: the read path discovered the connection
	// must not be served any further (internal/admin's read re-validates
	// the workspace's identity before every Repo.Since, D11). Any other
	// error out of a ReadFunc is a failed read — logged by the caller, the
	// slot not re-armed, the connection kept (D5). Serve returns the
	// wrapped error as-is.
	ErrRefused = errors.New("stream: read refused the connection")

	// ErrInboxFull is P6c's refusal of a pushed frame: the connection's
	// inbox already holds inboxDepth frames the loop has not written yet
	// (decisions.md mocker-p6c-live-conns D3/D4). The frame was NOT queued.
	// The admin handler answers 409 inbox_full; the operator is told
	// rather than drop-and-counted, because the producer here is the
	// operator and can be told — §30.11's drop-and-count is for the
	// server's own producers.
	ErrInboxFull = errors.New("stream: connection inbox is full")

	// ErrConnClosed is what a pusher gets when the connection it queued a
	// frame into ended before the loop wrote it (the client left, the
	// lifetime expired, a close by an operator, a registry shutdown). The
	// frame was queued and is now lost with the connection — a value in
	// RAM that dies with its Conn (D3).
	ErrConnClosed = errors.New("stream: connection closed before the frame was written")

	// ErrPushTimeout is what a pusher gets when its OWN context ended
	// while the frame was still queued (D4: the handler waits two frame
	// timeouts). The frame STAYS queued and may still be written by the
	// loop — the sentinel says so, and the handler's message repeats it.
	ErrPushTimeout = errors.New("stream: timed out waiting for the frame to be written; it stays queued")
)

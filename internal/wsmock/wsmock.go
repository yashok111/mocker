// Package wsmock is the ONE importer of github.com/coder/websocket in this
// tree (decisions.md mocker-p6d-websocket D1; DESIGN §30.9): the timeout,
// the frame cap and the close discipline of a WebSocket connection sit here,
// the way internal/jsonx is the one importer of encoding/json and
// internal/probe the one outgoing HTTP client — a library that ever
// misbehaves is one package to replace, and boundary_test.go keeps the
// count at one.
//
// What this package deliberately does NOT do: it holds no registry, no
// loop, no behaviour. The reactive rules, the echo, the reply queue and the
// reader goroutine are internal/mockplane's (ws.go); this file only turns
// an *http.Request into a connection and a connection into typed frames.
package wsmock

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/coder/websocket"
)

// MessageType is a frame's opcode as this tree names it — its own type, so
// that no other package spells the library's.
type MessageType int

// The two data opcodes.
const (
	Text   MessageType = MessageType(websocket.MessageText)
	Binary MessageType = MessageType(websocket.MessageBinary)
)

// StatusCode is a close status code (RFC 6455 §7.4).
type StatusCode int

// The close codes this tree sends or names (D8).
const (
	StatusNormalClosure   StatusCode = StatusCode(websocket.StatusNormalClosure)   // 1000
	StatusGoingAway       StatusCode = StatusCode(websocket.StatusGoingAway)       // 1001
	StatusPolicyViolation StatusCode = StatusCode(websocket.StatusPolicyViolation) // 1008
	StatusMessageTooBig   StatusCode = StatusCode(websocket.StatusMessageTooBig)   // 1009
	StatusInternalError   StatusCode = StatusCode(websocket.StatusInternalError)   // 1011
)

// NoStatus is what CloseStatus returns for an error that is not a close.
const NoStatus StatusCode = -1

// ErrCannotHijack is Accept's refusal BEFORE the library touches the
// response: the writer chain does not reach an http.Hijacker (a test
// recorder, the admin loopback), so the caller answers 501
// streaming_unsupported with a JSON body rather than letting the library
// write its own plain-text 501 (§30.6: "a refusal is a refusal, never a
// fallthrough").
var ErrCannotHijack = errors.New("wsmock: response writer cannot be hijacked")

// Conn is one accepted (or dialled) WebSocket connection.
type Conn struct {
	c *websocket.Conn
}

// AcceptOptions is what the mock plane decides per handshake.
type AcceptOptions struct {
	// MaxFrame is the inbound read limit in bytes (MOCKER_STREAM_MAX_FRAME):
	// a frame over it makes the library close with 1009 and Read return
	// that close.
	MaxFrame int64
}

// CanHijack reports whether w reaches an http.Hijacker through the same
// Unwrap walk the library and http.ResponseController use. Every wrapper on
// the mock plane's path implements Unwrap (httpx.StatusRecorder,
// mockplane's trafficWriter and headWriter), so on a real listener this is
// true; on httptest.ResponseRecorder and the admin loopback it is false.
func CanHijack(w http.ResponseWriter) bool {
	for {
		switch t := w.(type) {
		case http.Hijacker:
			return true
		case interface{ Unwrap() http.ResponseWriter }:
			w = t.Unwrap()
		default:
			return false
		}
	}
}

// Accept upgrades r on w. The library's own origin check is DISABLED here
// on purpose: InsecureSkipVerify is its name for skipping the same-origin
// check (nothing to do with TLS — this process terminates none), and the
// mock plane's contract is the opposite of same-origin (DESIGN §30.12): any
// page may connect unless MOCKER_STREAM_ORIGINS narrows it, and that check
// is the mock plane's own, run BEFORE this call (D6) so that a future
// library version tightening its default fails loudly rather than silently
// re-enabling a check this plane declines. Compression is off (no
// permessage-deflate) and no subprotocol is negotiated (D12).
//
// The library writes its own HTTP error on a malformed handshake (400, 426)
// before returning an error; on ErrCannotHijack nothing has been written.
func Accept(w http.ResponseWriter, r *http.Request, opts AcceptOptions) (*Conn, error) {
	if !CanHijack(w) {
		return nil, ErrCannotHijack
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // the origin check is D6's, not the library's — see the doc comment
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return nil, err
	}
	if opts.MaxFrame > 0 {
		c.SetReadLimit(opts.MaxFrame)
	}
	return &Conn{c: c}, nil
}

// DialOptions is what a test client needs: the Host header (the mock plane
// routes by it) and any extra headers (Origin).
type DialOptions struct {
	Host   string
	Header http.Header
}

// Dial is the client half, for tests in this and other packages — the one
// way a test reaches a WebSocket client without importing the library
// (D1). status is the handshake's HTTP status: 101 on success, otherwise
// the refusal the mock plane wrote. The response itself stays here: on a
// 101 the library hands back a response with a nil body (the socket is the
// connection now — the first version of this package's own test did a
// well-meaning `defer resp.Body.Close()` and dereferenced nil), and on a
// refusal the body is drained and closed before the status is returned, so
// no caller ever holds an *http.Response from a WebSocket dial.
func Dial(ctx context.Context, url string, opts DialOptions) (*Conn, int, error) {
	c, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		Host:            opts.Host,
		HTTPHeader:      opts.Header,
		CompressionMode: websocket.CompressionDisabled,
	})
	status := 0
	if resp != nil {
		status = resp.StatusCode
		if resp.Body != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}
	if err != nil {
		return nil, status, err
	}
	return &Conn{c: c}, status, nil
}

// Read returns the next data frame. It blocks until a frame, ctx's end, or
// the connection's end; a peer close comes back as an error whose
// CloseStatus is the peer's code. The library reads control frames (ping,
// pong, close) inside this call, which is why Ping needs a concurrent
// reader (D8).
func (c *Conn) Read(ctx context.Context) (MessageType, []byte, error) {
	typ, p, err := c.c.Read(ctx)
	return MessageType(typ), p, err
}

// Write puts one data frame on the wire under ctx (the caller passes a
// context bounded by the frame timeout, D7).
func (c *Conn) Write(ctx context.Context, typ MessageType, p []byte) error {
	return c.c.Write(ctx, websocket.MessageType(typ), p)
}

// Ping sends a ping and waits for the pong under ctx. A concurrent Read
// must be running for the pong to be seen.
func (c *Conn) Ping(ctx context.Context) error { return c.c.Ping(ctx) }

// Close performs the closing handshake with code and reason (reason is cut
// to 123 bytes by the caller's own validation). It waits for the peer's
// close frame under the library's own short deadline, then closes the
// socket; the socket is closed even when the handshake fails.
func (c *Conn) Close(code StatusCode, reason string) error {
	return c.c.Close(websocket.StatusCode(code), reason)
}

// CloseNow closes the socket without a closing handshake — what a loop
// does after a write that already failed, or on shutdown when the peer
// cannot be waited for.
func (c *Conn) CloseNow() error { return c.c.CloseNow() }

// CloseStatus extracts the close code from an error returned by Read,
// Write or Ping, or NoStatus when the error is not a close.
func CloseStatus(err error) StatusCode {
	s := websocket.CloseStatus(err)
	if s == -1 {
		// The read limit is the one close the library performs WITHOUT
		// returning a CloseError to the reader: read.go's limitReader
		// closes the connection with 1009 and returns a plain
		// "read limited at N bytes" error (v1.8.15). The peer sees 1009;
		// this side must record the same code, so the error is recognised
		// by its text here — the one place the library's wording is
		// depended on, inside the seam that exists for exactly that.
		if err != nil && strings.Contains(err.Error(), "read limited at") {
			return StatusMessageTooBig
		}
		return NoStatus
	}
	return StatusCode(s)
}

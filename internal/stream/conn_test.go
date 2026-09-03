package stream

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// maxTestFrameBuf sizes the scanner's token buffer for the LARGEST legal
// frame (D21): 200 rows (MaxFrameRows) at up to MOCKER_TRAFFIC_MAX_BODY's
// default (8 KiB) each, request and response body both — the default 64 KiB
// bufio.Scanner token limit fails on a frame the server is entitled to send,
// so a test harness that never exercises this proves nothing about the frame
// that actually matters.
const maxTestFrameBuf = 8 * 1024 * 1024

// splitSSEFrames is a bufio.SplitFunc that yields one token per SSE frame —
// everything up to (not including) the blank line that terminates it.
func splitSSEFrames(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if i := bytes.Index(data, []byte("\n\n")); i >= 0 {
		return i + 2, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// sseFrameScanner wraps r in a bufio.Scanner sized and split the way D21
// requires — real socket, real client, one frame per Scan.
func sseFrameScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxTestFrameBuf)
	sc.Split(splitSSEFrames)
	return sc
}

// pageSource feeds a test's [ReadFunc] a queue of canned pages, FIFO,
// falling back to an empty page once the queue is drained — the shape
// Repo.Since gives a poller that has caught up.
type pageSource struct {
	mu    sync.Mutex
	pages []struct {
		data   []byte
		lastID int64
		n      int
		err    error
	}
}

func (p *pageSource) push(data []byte, lastID int64, n int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pages = append(p.pages, struct {
		data   []byte
		lastID int64
		n      int
		err    error
	}{data, lastID, n, err})
}

func (p *pageSource) Read(_ context.Context, since int64) ([]byte, int64, int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.pages) == 0 {
		return []byte(`{"rows":[],"lastId":0,"dropped":0}`), since, 0, nil
	}
	pg := p.pages[0]
	p.pages = p.pages[1:]
	return pg.data, pg.lastID, pg.n, pg.err
}

// TestConn_unsupportedResponseWriter is D21's ONE test built on
// httptest.ResponseRecorder rather than a real socket: a Recorder implements
// http.Flusher, so it is Flush that would wrongly pass here — it is
// SetWriteDeadline it fails, which is exactly D9's refusal path and exactly
// why every OTHER test in this package runs over httptest.Server instead.
func TestConn_unsupportedResponseWriter(t *testing.T) {
	reg := NewRegistry()
	defer reg.Close()

	c, err := reg.Open(context.Background(), 1)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	rec := httptest.NewRecorder()
	serveErr := c.Serve(rec, ServeConfig{
		Read: emptyRead, Ping: time.Hour, FrameTimeout: time.Second, SessionRecheck: time.Hour,
	})
	if !errors.Is(serveErr, ErrUnsupported) {
		t.Fatalf("Serve = %v, want ErrUnsupported", serveErr)
	}
	if got := reg.Stats().RefusedUnsupported; got != 1 {
		t.Fatalf("RefusedUnsupported = %d, want 1", got)
	}
	if got := reg.Stats().Open; got != 0 {
		t.Fatalf("Open = %d, want 0 — a refused connection must not count as live", got)
	}
}

// testStream is one live client side of a stream opened by
// [openTestStream]: the response (its Body is the SSE byte stream) and a
// channel closed once the server's own Serve call has RETURNED — the join
// point A21 requires a lifetime test to wait on, and the one thing a client
// observing EOF cannot prove on its own.
type testStream struct {
	resp *http.Response
	done <-chan struct{}
}

// openTestStream starts an httptest.Server whose one handler opens a
// connection against reg and serves it with cfg (cfg.Read/Recheck are
// overridden by the caller as needed) and returns the live client side.
// The body is closed by t.Cleanup, after the server is; a test that wants
// the close to happen EARLIER (to simulate the peer going away) calls
// resp.Body.Close itself, which is idempotent.
func openTestStream(t *testing.T, reg *Registry, cfg ServeConfig) *testStream {
	t.Helper()
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		c, err := reg.Open(r.Context(), 1)
		if err != nil {
			t.Errorf("open: %v", err)
			return
		}
		_ = c.Serve(w, cfg)
	}))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return &testStream{resp: resp, done: done}
}

func TestServe_writesTheHandshakeHeaders(t *testing.T) {
	reg := NewRegistry()
	defer reg.Close()

	st := openTestStream(t, reg, ServeConfig{
		Read: emptyRead, Ping: time.Hour, FrameTimeout: time.Second, SessionRecheck: time.Hour,
	})
	resp := st.resp

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if xa := resp.Header.Get("X-Accel-Buffering"); xa != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", xa)
	}
}

func TestServe_deliversAFrameOnNudgeAndAdvancesTheCursor(t *testing.T) {
	reg := NewRegistry()
	defer reg.Close()

	src := &pageSource{}
	src.push([]byte(`{"rows":[{"id":1}],"lastId":1,"dropped":0}`), 1, 1, nil)

	st := openTestStream(t, reg, ServeConfig{
		Read: src.Read, Ping: time.Hour, FrameTimeout: 2 * time.Second, SessionRecheck: time.Hour,
	})
	sc := sseFrameScanner(st.resp.Body)

	waitForOpen(t, reg, 1)
	reg.Notify([]int64{1})

	if !sc.Scan() {
		t.Fatalf("no frame arrived: %v", sc.Err())
	}
	frame := sc.Text()
	if !strings.Contains(frame, "id: 1") {
		t.Errorf("frame missing id line: %q", frame)
	}
	if !strings.Contains(frame, "event: traffic") {
		t.Errorf("frame missing event line: %q", frame)
	}
	if !strings.Contains(frame, `data: {"rows":[{"id":1}],"lastId":1,"dropped":0}`) {
		t.Errorf("frame missing data line: %q", frame)
	}
}

func TestServe_drainsConsecutivePagesUntilCaughtUpOnOneNudge(t *testing.T) {
	reg := NewRegistry()
	defer reg.Close()

	// Two FULL pages (n == MaxFrameRows) followed by a short one: D5's inner
	// loop must read all three off a SINGLE nudge, because a full page only
	// proves "more may be waiting", never "caught up".
	src := &pageSource{}
	bigRow := `{"id":%d,"body":"` + strings.Repeat("x", 8*1024) + `"}`
	page := func(from, n int) []byte {
		var b strings.Builder
		b.WriteByte('[')
		for i := range n {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, bigRow, from+i)
		}
		b.WriteByte(']')
		return []byte(`{"rows":` + b.String() + `,"lastId":` + fmt.Sprint(from+n-1) + `,"dropped":0}`)
	}
	src.push(page(1, MaxFrameRows), int64(MaxFrameRows), MaxFrameRows, nil)
	src.push(page(MaxFrameRows+1, MaxFrameRows), int64(2*MaxFrameRows), MaxFrameRows, nil)
	src.push(page(2*MaxFrameRows+1, 3), int64(2*MaxFrameRows+3), 3, nil)

	st := openTestStream(t, reg, ServeConfig{
		Read: src.Read, Ping: time.Hour, FrameTimeout: 5 * time.Second, SessionRecheck: time.Hour,
	})
	sc := sseFrameScanner(st.resp.Body)

	waitForOpen(t, reg, 1)
	reg.Notify([]int64{1}) // ONE nudge for all three pages — this is the whole point of the test

	var ids []string
	for range 3 {
		if !sc.Scan() {
			t.Fatalf("only got %d of 3 frames off one nudge: %v", len(ids), sc.Err())
		}
		frame := sc.Text()
		for _, line := range strings.Split(frame, "\n") {
			if strings.HasPrefix(line, "id: ") {
				ids = append(ids, strings.TrimPrefix(line, "id: "))
			}
		}
	}
	want := []string{fmt.Sprint(MaxFrameRows), fmt.Sprint(2 * MaxFrameRows), fmt.Sprint(2*MaxFrameRows + 3)}
	for i, w := range want {
		if i >= len(ids) || ids[i] != w {
			t.Fatalf("frame ids = %v, want %v", ids, want)
		}
	}
}

func TestServe_pingsOnTheConfiguredInterval(t *testing.T) {
	reg := NewRegistry()
	defer reg.Close()

	st := openTestStream(t, reg, ServeConfig{
		Read: emptyRead, Ping: 40 * time.Millisecond, FrameTimeout: time.Second, SessionRecheck: time.Hour,
	})
	sc := sseFrameScanner(st.resp.Body)

	pings := 0
	deadline := time.Now().Add(2 * time.Second)
	for pings < 3 && time.Now().Before(deadline) {
		if !sc.Scan() {
			break
		}
		if strings.HasPrefix(sc.Text(), ":") {
			pings++
		}
	}
	if pings < 3 {
		t.Fatalf("got %d ping comment frames before the deadline, want at least 3", pings)
	}
}

func TestServe_recheckFailureClosesTheConnection(t *testing.T) {
	reg := NewRegistry()
	defer reg.Close()

	var calls int
	recheck := func(context.Context) error {
		calls++
		if calls >= 2 {
			return errors.New("session gone")
		}
		return nil
	}

	st := openTestStream(t, reg, ServeConfig{
		Read: emptyRead, Recheck: recheck,
		Ping: time.Hour, FrameTimeout: time.Second, SessionRecheck: 30 * time.Millisecond,
	})

	select {
	case <-st.done:
	case <-time.After(2 * time.Second):
		t.Fatal("connection never closed after RecheckFunc refused it")
	}
	_, _ = io.Copy(io.Discard, st.resp.Body) // let the client see the close too
	if calls < 2 {
		t.Fatalf("RecheckFunc called %d times, want at least 2", calls)
	}
}

// TestServe_lifetimeExpiry is A21's own clause: maxStreamLifetime is a
// package var specifically so this test can shorten it, and it must actually
// discriminate — a connection that ignored the shortened value would hang
// this test until Go's own test timeout, not merely fail an assertion.
func TestServe_lifetimeExpiry(t *testing.T) {
	// Not run in parallel with the package's other tests (no t.Parallel()
	// here or anywhere else in this package): maxStreamLifetime is read by
	// any goroutine with a live connection, and a concurrent test opening one
	// while this test's shortened value is in effect would race it under
	// -race, which the package's own subject — goroutines outliving
	// requests — is exactly the wrong place to have as a flake instead of a
	// hard failure.
	original := maxStreamLifetime
	maxStreamLifetime = 150 * time.Millisecond

	reg := NewRegistry()
	defer reg.Close()

	start := time.Now()
	st := openTestStream(t, reg, ServeConfig{
		Read: emptyRead, Ping: time.Hour, FrameTimeout: time.Second, SessionRecheck: time.Hour,
	})
	_, _ = io.Copy(io.Discard, st.resp.Body) // blocks until the server ends the response

	elapsed := time.Since(start)

	// Join the connection's own goroutine BEFORE restoring the var — A21
	// requires the restore to happen only once Serve has actually returned,
	// not merely once the client observed the socket close.
	select {
	case <-st.done:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve's own goroutine never returned")
	}
	t.Cleanup(func() { maxStreamLifetime = original })

	if elapsed > 2*time.Second {
		t.Fatalf("connection outlived the shortened lifetime (150ms): took %v", elapsed)
	}
}

// TestServe_stalledPeerIsCutByTheFrameDeadline is D12's per-frame deadline,
// the constant DESIGN §30.11 calls the most important one of the design
// "because forgetting it produces no test failure — only a writer parked
// forever on a stalled peer". The client opens the stream and never reads;
// the server is nudged with full 1 MiB pages until the socket's buffers are
// full and a write blocks; the write must fail within the deadline and
// Serve must return ErrPeerGone rather than sit on the blocked write.
func TestServe_stalledPeerIsCutByTheFrameDeadline(t *testing.T) {
	reg := NewRegistry()
	defer reg.Close()

	src := &pageSource{}
	big := []byte(`{"rows":[{"body":"` + strings.Repeat("y", 1024*1024) + `"}],"lastId":1,"dropped":0}`)
	// Every page is reported FULL so one nudge drains page after page —
	// the busy-writer shape, not the idle one — until the peer's buffers
	// stop absorbing them.
	for i := range 64 {
		src.push(big, int64(i+1), MaxFrameRows, nil)
	}

	var serveErr error
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		c, err := reg.Open(r.Context(), 1)
		if err != nil {
			t.Errorf("open: %v", err)
			return
		}
		serveErr = c.Serve(w, ServeConfig{
			Read: src.Read, Ping: time.Hour, FrameTimeout: 300 * time.Millisecond, SessionRecheck: time.Hour,
		})
	}))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	// Deliberately: no read of resp.Body from here on.

	waitForOpen(t, reg, 1)
	start := time.Now()
	reg.Notify([]int64{1})

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Serve never returned on a peer that stopped reading — the per-frame deadline is not applied")
	}
	if !errors.Is(serveErr, ErrPeerGone) {
		t.Fatalf("Serve = %v, want ErrPeerGone", serveErr)
	}
	// Buffers on loopback absorb a few MiB before the first write blocks;
	// what is bounded is the time from THAT write to the disconnect, which
	// the 5 s below comfortably covers at a 300 ms deadline while a parked
	// writer would still be parked at 10 s.
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("stalled peer cut after %v, want well inside the frame deadline's order of magnitude", elapsed)
	}
	if got := reg.Stats().Open; got != 0 {
		t.Fatalf("Open after the cut = %d, want 0", got)
	}
}

// TestServe_readRefusalClosesTheConnection: a ReadFunc error wrapping
// ErrRefused closes the connection and Serve returns it — the seam
// internal/admin uses to re-check the workspace's identity on every read
// (D11) rather than only on the timer. Any OTHER read error keeps the
// connection open and waits for the next nudge (D5), which the second half
// pins.
func TestServe_readRefusalClosesTheConnection(t *testing.T) {
	reg := NewRegistry()
	defer reg.Close()

	var serveErr error
	done := make(chan struct{})
	var mu sync.Mutex
	refuse := false
	read := func(_ context.Context, since int64) ([]byte, int64, int, error) {
		mu.Lock()
		defer mu.Unlock()
		if refuse {
			return nil, 0, 0, fmt.Errorf("%w: workspace replaced", ErrRefused)
		}
		return nil, 0, 0, errors.New("database locked")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		c, err := reg.Open(r.Context(), 1)
		if err != nil {
			t.Errorf("open: %v", err)
			return
		}
		serveErr = c.Serve(w, ServeConfig{
			Read: read, Ping: time.Hour, FrameTimeout: time.Second, SessionRecheck: time.Hour,
		})
	}))
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	waitForOpen(t, reg, 1)

	// An ordinary read failure: nudged, the read fails, the connection
	// stays open.
	reg.Notify([]int64{1})
	select {
	case <-done:
		t.Fatalf("Serve returned (%v) on a plain read error — D5 says a failed read keeps the connection", serveErr)
	case <-time.After(150 * time.Millisecond):
	}
	if got := reg.Stats().Open; got != 1 {
		t.Fatalf("Open after a failed read = %d, want 1", got)
	}

	mu.Lock()
	refuse = true
	mu.Unlock()
	reg.Notify([]int64{1})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return on a read wrapping ErrRefused")
	}
	if !errors.Is(serveErr, ErrRefused) {
		t.Fatalf("Serve = %v, want ErrRefused", serveErr)
	}
}

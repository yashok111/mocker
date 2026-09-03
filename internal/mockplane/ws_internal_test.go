package mockplane

// ws_internal_test.go is the white-box half of P6d's A5 (decisions.md
// mocker-p6d-websocket D7): the send budget's drop-and-count and the gap
// marker, observed with a socket double whose Write BLOCKS — a slow peer
// cannot be produced deterministically through kernel buffers, and the
// clause says so — and a registry shutdown during that blocked write, which
// must return the loop within one frame timeout and join the reader.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/stream"
	"github.com/yashok111/mocker/internal/wsmock"
)

// fakeSocket feeds Read from a channel and lets a test hold Write.
type fakeSocket struct {
	in       chan []byte
	release  chan struct{} // closed to let a blocked Write proceed
	blocked  chan struct{} // closed once the first Write is parked
	block    bool          // when true the FIRST Write blocks until release
	mu       sync.Mutex
	written  []string
	read     int // frames handed to Read so far
	closed   chan struct{}
	blockedO sync.Once
}

func newFakeSocket(block bool) *fakeSocket {
	return &fakeSocket{in: make(chan []byte, 1000), release: make(chan struct{}), blocked: make(chan struct{}), block: block, closed: make(chan struct{})}
}

func (f *fakeSocket) Read(ctx context.Context) (wsmock.MessageType, []byte, error) {
	select {
	case p := <-f.in:
		f.mu.Lock()
		f.read++
		f.mu.Unlock()
		return wsmock.Text, p, nil
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	case <-f.closed:
		return 0, nil, errors.New("closed")
	}
}

func (f *fakeSocket) reads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.read
}

func (f *fakeSocket) Write(ctx context.Context, _ wsmock.MessageType, p []byte) error {
	f.mu.Lock()
	first := len(f.written) == 0
	f.mu.Unlock()
	if f.block && first {
		f.blockedO.Do(func() { close(f.blocked) })
		select {
		case <-f.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	f.written = append(f.written, string(p))
	f.mu.Unlock()
	return nil
}

func (f *fakeSocket) Ping(context.Context) error { return nil }
func (f *fakeSocket) Close(wsmock.StatusCode, string) error {
	select {
	case <-f.closed:
	default:
		close(f.closed)
	}
	return nil
}
func (f *fakeSocket) CloseNow() error { return f.Close(0, "") }

func (f *fakeSocket) all() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.written...)
}

func newTestWSLoop(t *testing.T, sock wsSocket, budget int64, def *customep.Stream) (*wsLoop, *stream.Conn, *int) {
	t.Helper()
	reg := stream.NewWorkspaceRegistry(10)
	t.Cleanup(reg.Close)
	conn, err := reg.Open(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Release)
	opts := StreamOptions{Ping: time.Hour, FrameTimeout: 500 * time.Millisecond, Lifetime: time.Hour, MaxFrame: 1 << 20, SendBudget: budget}
	sl := newStreamLoop(def, nil, opts)
	dropped := 0
	sl.hooks = streamHooks{onFrame: func([]byte) {}, onPushed: func() {}, onSkip: func() {}, onErr: func(error) {}}
	wl := &wsLoop{
		streamLoop: sl,
		sock:       sock,
		conn:       conn,
		handshake:  overrides.Input{},
		queue:      newReplyQueue(budget),
		onFrameIn:  func(wsmock.MessageType, []byte) { conn.RecordFrameIn() },
		onDropped:  func(n int) { dropped += n },
	}
	return wl, conn, &dropped
}

func TestWSLoop_budgetDropsAndCountsThenMarksTheGap(t *testing.T) {
	sock := newFakeSocket(true)
	wl, conn, dropped := newTestWSLoop(t, sock, 1000, &customep.Stream{Echo: true})

	done := make(chan wsmock.StatusCode, 1)
	go func() { done <- wl.run(conn.Context()) }()

	// 50 echo frames of 100 bytes: the first one is taken by the loop and
	// parks in Write (observed through sock.blocked, never a sleep); the
	// queue then holds at most 10 more under the 1000-byte budget and
	// drops the rest.
	frame := []byte(strings.Repeat("x", 100))
	sock.in <- frame
	select {
	case <-sock.blocked:
	case <-time.After(3 * time.Second):
		t.Fatal("the loop never reached the blocked Write")
	}
	for range 49 {
		sock.in <- frame
	}
	deadline := time.Now().Add(3 * time.Second)
	for sock.reads() < 50 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if sock.reads() < 50 {
		t.Fatalf("the reader took %d of 50 frames while the writer was blocked — it must never block on the queue", sock.reads())
	}
	close(sock.release)
	// Let the loop drain what the queue holds, then end the connection.
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(sock.all()) >= 2 && !wl.queue.pending() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	conn.CloseByAdmin()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after CloseByAdmin")
	}

	out := sock.all()
	var gaps, echoes int
	for i, w := range out {
		switch {
		case strings.HasPrefix(w, `{"$gap":`):
			gaps++
			if i == 0 {
				t.Fatalf("the gap marker came before the frame the loop was blocked on: %v", out[:3])
			}
		case w == string(frame):
			echoes++
		default:
			t.Fatalf("unexpected write %q", w)
		}
	}
	if *dropped == 0 {
		t.Fatalf("no reply was dropped under a 1000-byte budget with 50×100 bytes queued while the writer blocked; writes=%d", len(out))
	}
	if gaps != 1 {
		t.Fatalf("gap markers = %d, want exactly one after one blocked stretch; writes: %v", gaps, out)
	}
	if echoes+*dropped != 50 {
		t.Fatalf("echoes %d + dropped %d != 50", echoes, *dropped)
	}
	if !strings.Contains(out[1], `{"$gap":`) {
		t.Fatalf("the marker must be the first write after the blocked one, got %q", out[1])
	}
}

func TestWSLoop_shutdownDuringABlockedWriteReturnsWithinAFrameTimeout(t *testing.T) {
	sock := newFakeSocket(true) // the first Write blocks until release, which never comes
	wl, conn, _ := newTestWSLoop(t, sock, 1<<20, &customep.Stream{Echo: true})
	done := make(chan wsmock.StatusCode, 1)
	go func() { done <- wl.run(conn.Context()) }()
	sock.in <- []byte(`{"a":1}`)
	time.Sleep(50 * time.Millisecond) // the loop is now inside the blocked Write
	t0 := time.Now()
	conn.CloseByAdmin() // what Registry.Close does per connection
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("run did not return: a blocked write must be aborted by the connection context")
	}
	if took := time.Since(t0); took > 1500*time.Millisecond {
		t.Fatalf("run took %v to return after the cancel, want within one frame timeout (500 ms) plus slack", took)
	}
}

package stream

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// emptyRead is a [ReadFunc] with nothing to report — the shape a workspace
// with no fresh rows gets on an ordinary poll.
func emptyRead(_ context.Context, since int64) ([]byte, int64, int, error) {
	return []byte(`{"rows":[],"lastId":0,"dropped":0}`), since, 0, nil
}

// waitForOpen polls reg.Stats().Open until it reaches n or the deadline
// passes — used instead of a fixed sleep because the handler goroutine
// registers its connection on its own schedule, not the test's.
func waitForOpen(t *testing.T, reg *Registry, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if reg.Stats().Open == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Stats().Open never reached %d (stuck at %d)", n, reg.Stats().Open)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRegistry_open_refusesOverCap(t *testing.T) {
	reg := NewRegistry()
	defer reg.Close()

	opened := make([]*Conn, 0, maxStreamConns)
	for i := range maxStreamConns {
		c, err := reg.Open(context.Background(), int64(i))
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		opened = append(opened, c)
	}

	if _, err := reg.Open(context.Background(), 999); !errors.Is(err, ErrCapExceeded) {
		t.Fatalf("65th open: got %v, want ErrCapExceeded", err)
	}
	if got := reg.Stats().RefusedCap; got != 1 {
		t.Fatalf("RefusedCap = %d, want 1", got)
	}
	if got := reg.Stats().Open; got != maxStreamConns {
		t.Fatalf("Open = %d, want %d", got, maxStreamConns)
	}

	// Open never touched a ResponseWriter, so the connections it admitted
	// are never Served — deregister them directly, exactly what a caller
	// that decided against serving one after all would have to do.
	for _, c := range opened {
		reg.deregister(c)
	}
}

func TestRegistry_open_refusesOnceClosed(t *testing.T) {
	reg := NewRegistry()
	reg.Close()

	if _, err := reg.Open(context.Background(), 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("open after Close: got %v, want ErrClosed", err)
	}
}

func TestRegistry_close_isIdempotent(t *testing.T) {
	reg := NewRegistry()
	reg.Close()
	reg.Close() // must not panic or block a second time
}

func TestRegistry_notify_coalescesOnAFullSlot(t *testing.T) {
	reg := NewRegistry()
	defer reg.Close()

	c, err := reg.Open(context.Background(), 7)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reg.deregister(c)

	// First Notify fills the one-slot channel; nothing drains it, so the
	// second Notify for the same workspace must be dropped and counted —
	// D5's own definition of "drop-and-count".
	reg.Notify([]int64{7})
	reg.Notify([]int64{7})

	if got := reg.Stats().CoalescedNudges; got != 1 {
		t.Fatalf("CoalescedNudges = %d, want 1", got)
	}
	select {
	case <-c.nudge:
	default:
		t.Fatal("first Notify did not fill the slot")
	}
}

func TestRegistry_notify_ignoresOtherWorkspaces(t *testing.T) {
	reg := NewRegistry()
	defer reg.Close()

	c, err := reg.Open(context.Background(), 7)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reg.deregister(c)

	reg.Notify([]int64{8})

	select {
	case <-c.nudge:
		t.Fatal("connection scoped to workspace 7 was nudged by a batch touching only 8")
	default:
	}
}

func TestRegistry_close_stopsLiveConnectionsAndWaitsForThem(t *testing.T) {
	reg := NewRegistry()

	st := openTestStream(t, reg, ServeConfig{
		Read: emptyRead, Ping: time.Hour, FrameTimeout: time.Second, SessionRecheck: time.Hour,
	})

	waitForOpen(t, reg, 1)

	closed := make(chan struct{})
	go func() { reg.Close(); close(closed) }()

	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return — a live connection was not stopped")
	}

	select {
	case <-st.done:
	case <-time.After(time.Second):
		t.Fatal("Close returned before the connection's own Serve call did — the wait step did not join it")
	}

	if got := reg.Stats().Open; got != 0 {
		t.Fatalf("Open after Close = %d, want 0", got)
	}

	// The client's own read now drains to EOF rather than hanging — proof
	// the socket was actually released, not merely uncounted.
	drained := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, st.resp.Body)
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("client body never reached EOF after Close")
	}
}

// TestWorkspaceRegistry_capIsPerWorkspace is P6b D9: NewWorkspaceRegistry
// counts one workspace's connections against the cap and leaves another
// workspace's unaffected; a zero cap refuses every Open (§30.11).
func TestWorkspaceRegistry_capIsPerWorkspace(t *testing.T) {
	reg := NewWorkspaceRegistry(2)
	defer reg.Close()

	a1, err := reg.Open(context.Background(), 1)
	if err != nil {
		t.Fatalf("open a1: %v", err)
	}
	a2, err := reg.Open(context.Background(), 1)
	if err != nil {
		t.Fatalf("open a2: %v", err)
	}
	if _, err := reg.Open(context.Background(), 1); !errors.Is(err, ErrCapExceeded) {
		t.Fatalf("third open on workspace 1: %v, want ErrCapExceeded", err)
	}
	b1, err := reg.Open(context.Background(), 2)
	if err != nil {
		t.Fatalf("open on workspace 2 with workspace 1 at its cap: %v — the cap must be per workspace", err)
	}
	st := reg.Stats()
	if st.Open != 3 || st.Cap != 2 || st.RefusedCap != 1 {
		t.Fatalf("stats = %+v, want open 3, cap 2, refusedCap 1", st)
	}
	for _, c := range []*Conn{a1, a2, b1} {
		reg.deregister(c)
	}

	zero := NewWorkspaceRegistry(0)
	defer zero.Close()
	if _, err := zero.Open(context.Background(), 1); !errors.Is(err, ErrCapExceeded) {
		t.Fatalf("open on a zero-cap registry: %v, want ErrCapExceeded", err)
	}
}

package stream

import (
	"context"
	"errors"
	"testing"
	"time"
)

// push_test.go is P6c's package-level acceptance (decisions.md
// mocker-p6c-live-conns A4): the identity, the inbox bound and the three
// ways a Push ends, observed on a Conn that was Opened and never served —
// the one place inbox_full and push_timeout can be produced without a
// peer that never drains.

func openUnserved(t *testing.T, reg *Registry, ws int64) *Conn {
	t.Helper()
	c, err := reg.Open(t.Context(), ws)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(c.Release)
	return c
}

func TestOpen_mintsMonotonicIDsPerRegistry(t *testing.T) {
	reg := NewWorkspaceRegistry(10)
	t.Cleanup(reg.Close)
	a := openUnserved(t, reg, 1)
	b := openUnserved(t, reg, 2)
	a.Release()
	c := openUnserved(t, reg, 1)
	if a.ID() != 1 || b.ID() != 2 || c.ID() != 3 {
		t.Fatalf("ids = %d, %d, %d; want 1, 2, 3 — a released id is never reissued", a.ID(), b.ID(), c.ID())
	}
	other := NewWorkspaceRegistry(10)
	t.Cleanup(other.Close)
	if d := openUnserved(t, other, 1); d.ID() != 1 {
		t.Fatalf("second registry's first id = %d, want 1 (the counter is per registry)", d.ID())
	}
}

func TestSnapshotAndLookup_areWorkspaceScoped(t *testing.T) {
	reg := NewWorkspaceRegistry(10)
	t.Cleanup(reg.Close)
	a := openUnserved(t, reg, 1)
	a.SetInfo(Info{EndpointID: 42, Path: "/ticks", Kind: "sse", Peer: "10.0.0.1"})
	b := openUnserved(t, reg, 2)
	a.RecordFrame()
	a.RecordFrame()
	a.RecordPushed()
	a.RecordSkipped()

	rows := reg.Snapshot(1)
	if len(rows) != 1 {
		t.Fatalf("Snapshot(1) = %d rows, want 1 (workspace 2's connection must not appear)", len(rows))
	}
	got := rows[0]
	if got.ID != a.ID() || got.EndpointID != 42 || got.Path != "/ticks" || got.Kind != "sse" || got.RemoteAddr != "10.0.0.1" {
		t.Fatalf("row = %+v, want the info SetInfo recorded", got)
	}
	if got.Frames != 2 || got.Pushed != 1 || got.Skipped != 1 {
		t.Fatalf("counters = frames %d pushed %d skipped %d, want 2/1/1", got.Frames, got.Pushed, got.Skipped)
	}
	if got.OpenedAt.IsZero() || time.Since(got.OpenedAt) > time.Minute {
		t.Fatalf("openedAt = %v, want the Open instant", got.OpenedAt)
	}
	if reg.Lookup(1, a.ID()) != a {
		t.Fatalf("Lookup(1, %d) did not find the connection", a.ID())
	}
	if reg.Lookup(2, a.ID()) != nil {
		t.Fatalf("Lookup(2, %d) found workspace 1's connection through workspace 2", a.ID())
	}
	if reg.Lookup(1, 999) != nil {
		t.Fatalf("Lookup(1, 999) found a connection that was never issued")
	}
	b.Release()
	if reg.Lookup(2, b.ID()) != nil {
		t.Fatalf("Lookup found a released connection")
	}
	if reg.Cap() != 10 {
		t.Fatalf("Cap = %d, want 10", reg.Cap())
	}
}

func TestPush_boundsTheInboxAndTimesOut(t *testing.T) {
	reg := NewWorkspaceRegistry(10)
	t.Cleanup(reg.Close)
	c := openUnserved(t, reg, 1)

	expired, cancel := context.WithCancel(context.Background())
	cancel()
	for i := range inboxDepth {
		if _, err := c.Push(expired, "e", []byte(`1`)); !errors.Is(err, ErrPushTimeout) {
			t.Fatalf("push %d = %v, want ErrPushTimeout (queued, nobody serving, caller's ctx expired)", i+1, err)
		}
	}
	if _, err := c.Push(expired, "e", []byte(`1`)); !errors.Is(err, ErrInboxFull) {
		t.Fatalf("push %d = %v, want ErrInboxFull", inboxDepth+1, err)
	}
	if got := len(c.Inbox()); got != inboxDepth {
		t.Fatalf("inbox holds %d, want %d — a timed-out push stays queued", got, inboxDepth)
	}
}

func TestPush_returnsConnClosedWhenTheConnectionEnds(t *testing.T) {
	reg := NewWorkspaceRegistry(10)
	t.Cleanup(reg.Close)
	c := openUnserved(t, reg, 1)

	done := make(chan error, 1)
	go func() {
		_, err := c.Push(context.Background(), "", []byte(`{}`))
		done <- err
	}()
	// The pusher is parked on the reply; an operator's close must release it.
	time.Sleep(20 * time.Millisecond)
	if reg.CloseByAdmin(2, c.ID()) {
		t.Fatal("Registry.CloseByAdmin through another workspace must report false")
	}
	if !reg.CloseByAdmin(1, c.ID()) {
		t.Fatal("the first CloseByAdmin must report true")
	}
	if reg.CloseByAdmin(1, c.ID()) || c.CloseByAdmin() {
		t.Fatal("a second CloseByAdmin must report false (the loser of a race answers 404)")
	}
	if reg.Lookup(1, c.ID()) != nil || len(reg.Snapshot(1)) != 0 {
		t.Fatal("a closing connection must be neither found nor listed before it deregisters")
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrConnClosed) {
			t.Fatalf("push after close = %v, want ErrConnClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a pusher stayed parked after the connection was closed")
	}
	if !c.ClosedByAdmin() {
		t.Fatal("ClosedByAdmin must read true after CloseByAdmin")
	}
}

func TestPush_deliveredFramePrefersTheReplyOverACancelledContext(t *testing.T) {
	reg := NewWorkspaceRegistry(10)
	t.Cleanup(reg.Close)
	c := openUnserved(t, reg, 1)

	// Simulate the loop: take the request, reply with an ordinal, THEN end
	// the connection — a pusher seeing both must report the delivery.
	go func() {
		req := <-c.Inbox()
		req.Reply(7, nil)
		c.CloseByAdmin()
	}()
	id, err := c.Push(context.Background(), "x", []byte(`1`))
	if err != nil || id != 7 {
		t.Fatalf("push = (%d, %v), want (7, nil)", id, err)
	}
}

func TestPush_adminFeedConnectionsHaveNoInbox(t *testing.T) {
	reg := NewRegistry()
	t.Cleanup(reg.Close)
	c := openUnserved(t, reg, 1)
	if c.Inbox() != nil {
		t.Fatal("an admin-feed connection must have no inbox: nothing drains it")
	}
	if _, err := c.Push(context.Background(), "", nil); !errors.Is(err, ErrConnClosed) {
		t.Fatalf("push into an admin-feed connection = %v, want ErrConnClosed", err)
	}
}

func TestRelease_cancelsTheConnectionContext(t *testing.T) {
	reg := NewWorkspaceRegistry(10)
	t.Cleanup(reg.Close)
	c := openUnserved(t, reg, 1)
	c.Release()
	select {
	case <-c.Context().Done():
	default:
		t.Fatal("deregister must cancel the connection's context so no pusher stays parked behind a returned loop")
	}
}

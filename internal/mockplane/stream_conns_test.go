package mockplane_test

// stream_conns_test.go is P6c's serving half observed over a REAL socket
// (decisions.md mocker-p6c-live-conns A5): a pushed frame arrives between
// tick frames under the next ordinal, written by the loop; an operator's
// close ends the handler and the recorded row says so; a self-closed
// timeline's row does not; a push into a connection that already ended is
// refused. Same fixture as stream_test.go.

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/stream"
)

func tickDef(intervalMs int) *customep.Stream {
	return &customep.Stream{Tick: &customep.Tick{IntervalMs: intervalMs, Event: "tick",
		Schema: jsonx.RawMessage(`{"type":"object","properties":{"v":{"type":"integer"}},"required":["v"]}`)}}
}

// waitListed polls the registry until workspace 1 lists exactly n
// connections, and returns the snapshot.
func waitListed(t *testing.T, reg *stream.Registry, n int) []stream.Snapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		rows := reg.Snapshot(1)
		if len(rows) == n {
			return rows
		}
		if time.Now().After(deadline) {
			t.Fatalf("workspace 1 lists %d connection(s), want %d", len(rows), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitRows(t *testing.T, sink *streamSink, n int) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		evs := sink.all()
		if len(evs) >= n {
			notes := make([]string, 0, len(evs))
			for _, ev := range evs {
				notes = append(notes, ev.Notes)
			}
			return notes
		}
		if time.Now().After(deadline) {
			t.Fatalf("recorded %d row(s), want %d", len(evs), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStream_pushedFrameIsWrittenByTheLoopUnderTheNextOrdinal(t *testing.T) {
	f := newStreamFixture(t, fastOpts(), 10, sseRow(1, "/ticks", tickDef(150)))
	resp := f.open(t, "alex.mock.local", "/ticks")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	rows := waitListed(t, f.reg, 1)
	row := rows[0]
	if row.EndpointID != 1 || row.Path != "/ticks" || row.Kind != customep.KindSSE || row.RemoteAddr == "" || row.OpenedAt.IsZero() {
		t.Fatalf("listed row = %+v, want endpoint 1 /ticks sse with a peer and an open time", row)
	}
	conn := f.reg.Lookup(1, row.ID)
	if conn == nil {
		t.Fatalf("Lookup(1, %d) = nil for a listed connection", row.ID)
	}

	// Let a couple of ticks go out first, so the push lands mid-stream.
	time.Sleep(350 * time.Millisecond)
	id, err := conn.Push(context.Background(), "op", []byte(`{"x":1}`))
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if id < 2 {
		t.Fatalf("pushed frame id = %d, want one past at least two ticks", id)
	}

	frames := readFrames(t, resp.Body, int(id)+1, 5*time.Second)
	var seen bool
	for i, fr := range frames {
		if fr.id != strconv.Itoa(i+1) {
			t.Fatalf("frame %d carries id %q — the pushed frame must take the NEXT ordinal, not a parallel one; frames: %+v", i, fr.id, frames)
		}
		if fr.id == strconv.FormatInt(id, 10) {
			seen = true
			if fr.event != "op" || fr.data != `{"x":1}` {
				t.Fatalf("frame %d = %+v, want event op / data {\"x\":1}", id, fr)
			}
		} else if fr.event != "tick" {
			t.Fatalf("frame %s has event %q, want tick", fr.id, fr.event)
		}
	}
	if !seen {
		t.Fatalf("the pushed frame (id %d) never arrived; frames: %+v", id, frames)
	}
	after := f.reg.Snapshot(1)
	if len(after) != 1 || after[0].Pushed != 1 || after[0].Frames < id {
		t.Fatalf("after the push the listing = %+v, want pushed 1 and frames >= %d", after, id)
	}

	// An operator's close ends the handler; the row carries both tokens.
	if !conn.CloseByAdmin() {
		t.Fatal("first CloseByAdmin must report true")
	}
	waitListed(t, f.reg, 0)
	notes := waitRows(t, f.sink, 1)
	n := notes[0]
	if !strings.Contains(n, "stream:sse") || !strings.Contains(n, "pushed:1") || !strings.Contains(n, "closed:admin") {
		t.Fatalf("notes = %q, want stream:sse, pushed:1 and closed:admin", n)
	}
	if _, err := conn.Push(context.Background(), "", []byte(`1`)); !errors.Is(err, stream.ErrConnClosed) {
		t.Fatalf("push after close = %v, want ErrConnClosed", err)
	}
}

func TestStream_selfClosedTimelineRowCarriesNeitherToken(t *testing.T) {
	def := timeline(customep.Frame{DelayMs: 0, Data: jsonx.RawMessage(`1`)})
	def.CloseWhenDone = ptrBool(true)
	f := newStreamFixture(t, fastOpts(), 10, sseRow(1, "/once", def))
	resp := f.open(t, "alex.mock.local", "/once")
	frames := readFrames(t, resp.Body, 0, 5*time.Second)
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	notes := waitRows(t, f.sink, 1)
	if strings.Contains(notes[0], "pushed:") || strings.Contains(notes[0], "closed:admin") {
		t.Fatalf("notes = %q must carry neither pushed: nor closed:admin for a self-closed connection", notes[0])
	}
	waitListed(t, f.reg, 0)
}

func ptrBool(b bool) *bool { return &b }

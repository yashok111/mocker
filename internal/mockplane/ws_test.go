package mockplane_test

// ws_test.go is P6d's serving half observed over a REAL socket (decisions.md
// mocker-p6d-websocket A5): the fixture's handler is mounted UNDER
// httpx.RequestLog, so the upgrade walks the same StatusRecorder →
// trafficWriter chain production has, and every client is wsmock.Dial —
// no test in this package imports the library.

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/mockplane"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/stream"
	"github.com/yashok111/mocker/internal/traffic"
	"github.com/yashok111/mocker/internal/wsmock"
)

func wsRow(id int64, path string, def *customep.Stream) *customep.Row {
	return &customep.Row{
		ID: id, WorkspaceID: 1, Method: http.MethodGet, Path: path,
		CanonicalPath: router.CanonicalPath(path), SourceOrder: id, OverrideOn: true,
		ActiveStatus: 200, Kind: customep.KindWS, Stream: def,
	}
}

func wsOpts() mockplane.StreamOptions {
	o := fastOpts()
	o.MaxFrame = 64 << 10
	o.SendBudget = 256 << 10
	return o
}

// newWSFixture is newStreamFixture with the production middleware chain
// in front of the plane (A5: "mounted under httpx.RequestLog").
func newWSFixture(t *testing.T, opts mockplane.StreamOptions, rows ...*customep.Row) *streamFixture {
	t.Helper()
	ws := sseWorkspace(1, "alex")
	p := newPlane(ws, sseWorkspace(2, "bob"))
	p.SetCustomEndpoints(&fakeCustomSource{rows: map[int64][]*customep.Row{1: rows, 2: rows}})
	reg := stream.NewWorkspaceRegistry(10)
	p.SetStreams(reg, opts)
	sink := &streamSink{}
	p.SetTraffic(sink)
	state := livestate.NewStore(0, nil)
	p.SetLiveState(state)
	handler := httpx.RequestLog(slog.New(slog.DiscardHandler))(p)
	live := httptest.NewUnstartedServer(handler)
	live.Config.WriteTimeout = 300 * time.Millisecond
	live.Start()
	t.Cleanup(func() {
		reg.Close()
		live.Close()
	})
	return &streamFixture{plane: p, reg: reg, sink: sink, live: live, state: state}
}

// dial returns the connection (nil on a refused upgrade) and the handshake
// status, as wsmock.Dial reports them.
func (f *streamFixture) dial(t *testing.T, path string, header http.Header) (*wsmock.Conn, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	c, status, err := wsmock.Dial(ctx, "ws"+strings.TrimPrefix(f.live.URL, "http")+path, wsmock.DialOptions{Host: "alex.mock.local", Header: header})
	if err != nil && status == 0 {
		t.Fatalf("dial %s: %v", path, err)
	}
	if c != nil {
		t.Cleanup(func() { _ = c.CloseNow() })
	}
	return c, status
}

func mustRead(t *testing.T, c *wsmock.Conn) (wsmock.MessageType, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	typ, p, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return typ, string(p)
}

func mustWrite(t *testing.T, c *wsmock.Conn, typ wsmock.MessageType, s string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, typ, []byte(s)); err != nil {
		t.Fatalf("write %q: %v", s, err)
	}
}

func readClose(t *testing.T, c *wsmock.Conn) wsmock.StatusCode {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := c.Read(ctx)
	if err == nil {
		t.Fatal("read: want the close, got a frame")
	}
	return wsmock.CloseStatus(err)
}

func waitEvent(t *testing.T, sink *streamSink, path string) traffic.Event {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		for _, ev := range sink.all() {
			if ev.Path == path {
				return ev
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no traffic row for %s within 3 s: %+v", path, sink.all())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func pingRule() customep.Rule {
	return customep.Rule{
		When: []overrides.Condition{{In: "body", Name: "op", Op: "equals", Value: "ping"}},
		Data: jsonx.RawMessage(`{"op": "pong"}`),
	}
}

func TestWS_echoAndReactiveInSendOrder(t *testing.T) {
	def := &customep.Stream{Echo: true, Reactive: []customep.Rule{pingRule()}}
	f := newWSFixture(t, wsOpts(), wsRow(1, "/chat", def))
	c, status := f.dial(t, "/chat", nil)
	if status != http.StatusSwitchingProtocols {
		t.Fatalf("handshake = %d, want 101", status)
	}
	mustWrite(t, c, wsmock.Text, `{"op":"ping"}`)
	mustWrite(t, c, wsmock.Text, `{"x":1}`)
	mustWrite(t, c, wsmock.Text, `hello`)
	mustWrite(t, c, wsmock.Binary, "\x01\x02")
	want := []string{`{"op":"pong"}`, `{"x":1}`, `hello`, "\x01\x02"}
	for i, w := range want {
		typ, got := mustRead(t, c)
		if got != w {
			t.Fatalf("frame %d = %q, want %q (echo must not double-send on a match; a non-JSON frame must not match)", i, got, w)
		}
		if i == 3 && typ != wsmock.Binary {
			t.Fatalf("frame 3 opcode = %v, want binary mirrored", typ)
		}
	}
	_ = c.Close(wsmock.StatusNormalClosure, "bye")
	ev := waitEvent(t, f.sink, "/chat")
	for _, tok := range []string{"stream:ws", "frames:4", "frames_in:4", "close:1000"} {
		if !strings.Contains(ev.Notes, tok) {
			t.Fatalf("notes = %q, want %s", ev.Notes, tok)
		}
	}
	rows := f.reg.Snapshot(1)
	if len(rows) != 0 {
		t.Fatalf("after the close the listing still holds %+v", rows)
	}
}

func TestWS_ruleClosesWithItsCodeAndTheRowCarriesTheInbound(t *testing.T) {
	def := &customep.Stream{Reactive: []customep.Rule{{
		When:  []overrides.Condition{{In: "body", Name: "op", Op: "equals", Value: "bye"}},
		Data:  jsonx.RawMessage(`{"ok":true}`),
		Close: &customep.RuleClose{Code: 4001, Reason: "bye"},
	}}}
	opts := wsOpts()
	opts.TrafficFrames = mockplane.TrafficFramesFirst
	f := newWSFixture(t, opts, wsRow(1, "/kick", def))
	c, _ := f.dial(t, "/kick", nil)
	mustWrite(t, c, wsmock.Text, `{"op":"bye","token":"s3cr3t"}`)
	if _, got := mustRead(t, c); got != `{"ok":true}` {
		t.Fatalf("data before close = %q", got)
	}
	if code := readClose(t, c); code != 4001 {
		t.Fatalf("close code = %d, want 4001", code)
	}
	ev := waitEvent(t, f.sink, "/kick")
	for _, tok := range []string{"stream:ws", "frames:1", "frames_in:1", "close:4001"} {
		if !strings.Contains(ev.Notes, tok) {
			t.Fatalf("notes = %q, want %s", ev.Notes, tok)
		}
	}
	wantBody, _ := traffic.RedactJSONBody([]byte(`{"op":"bye","token":"s3cr3t"}`))
	if string(ev.ReqBody) != string(wantBody) || strings.Contains(string(ev.ReqBody), "s3cr3t") {
		t.Fatalf("reqBody = %q, want the first inbound frame redacted by field name (%q)", ev.ReqBody, wantBody)
	}
	if string(ev.RespBody) != `{"ok":true}` {
		t.Fatalf("respBody = %q, want the first outbound frame", ev.RespBody)
	}
}

func TestWS_timelineThenClose1000(t *testing.T) {
	def := timeline(
		customep.Frame{DelayMs: 0, Data: jsonx.RawMessage(`1`)},
		customep.Frame{DelayMs: 300, Data: jsonx.RawMessage(`2`)},
		customep.Frame{DelayMs: 300, Data: jsonx.RawMessage(`3`)},
	)
	f := newWSFixture(t, wsOpts(), wsRow(1, "/tl", def))
	c, _ := f.dial(t, "/tl", nil)
	t0 := time.Now()
	for i, w := range []string{"1", "2", "3"} {
		if _, got := mustRead(t, c); got != w {
			t.Fatalf("frame %d = %q, want %q", i, got, w)
		}
		if i == 1 && time.Since(t0) < 250*time.Millisecond {
			t.Fatalf("frame 2 arrived after %v, want >= 250 ms (frames must not burst)", time.Since(t0))
		}
	}
	if code := readClose(t, c); code != wsmock.StatusNormalClosure {
		t.Fatalf("close code = %d, want 1000", code)
	}
	if ev := waitEvent(t, f.sink, "/tl"); !strings.Contains(ev.Notes, "close:1000") || !strings.Contains(ev.Notes, "frames:3") {
		t.Fatalf("notes = %q", ev.Notes)
	}
}

func TestWS_maxFrameCloses1009(t *testing.T) {
	opts := wsOpts()
	opts.MaxFrame = 1024
	f := newWSFixture(t, opts, wsRow(1, "/echo", &customep.Stream{Echo: true}))
	c, _ := f.dial(t, "/echo", nil)
	mustWrite(t, c, wsmock.Text, strings.Repeat("x", 2048))
	if code := readClose(t, c); code != wsmock.StatusMessageTooBig {
		t.Fatalf("close code = %d, want 1009", code)
	}
	if ev := waitEvent(t, f.sink, "/echo"); !strings.Contains(ev.Notes, "close:1009") {
		t.Fatalf("notes = %q, want close:1009", ev.Notes)
	}
}

func TestWS_originRefusedBeforeTheUpgrade(t *testing.T) {
	opts := wsOpts()
	opts.Origins = []string{"https://allowed.example"}
	f := newWSFixture(t, opts, wsRow(1, "/echo", &customep.Stream{Echo: true}))

	h := http.Header{}
	h.Set("Origin", "https://evil.example")
	c, status := f.dial(t, "/echo", h)
	if c != nil || status != http.StatusForbidden {
		t.Fatalf("evil origin: conn=%v status=%d, want 403 and no upgrade", c != nil, status)
	}
	if got := f.reg.Stats().Open; got != 0 {
		t.Fatalf("a refused origin took a cap slot: open = %d", got)
	}
	h.Set("Origin", "https://Allowed.Example")
	if c, status := f.dial(t, "/echo", h); c == nil || status != http.StatusSwitchingProtocols {
		t.Fatalf("allowed origin: want 101, got %d", status)
	}
	if c, status := f.dial(t, "/echo", nil); c == nil || status != http.StatusSwitchingProtocols {
		t.Fatalf("no Origin header: want 101, got %d", status)
	}
}

func TestWS_peerCloseWakesTheLoopAndWritesTheRow(t *testing.T) {
	f := newWSFixture(t, wsOpts(), wsRow(1, "/idle", &customep.Stream{Echo: true}))
	c, _ := f.dial(t, "/idle", nil)
	waitListedN(t, f.reg, 1)
	_ = c.Close(4100, "client leaves")
	ev := waitEvent(t, f.sink, "/idle") // within 3 s, not the lifetime
	if !strings.Contains(ev.Notes, "close:4100") {
		t.Fatalf("notes = %q, want the peer's close code mirrored", ev.Notes)
	}
	waitListedN(t, f.reg, 0)
}

func TestWS_adminPushAndCloseThroughTheRegistry(t *testing.T) {
	f := newWSFixture(t, wsOpts(), wsRow(1, "/ops", &customep.Stream{Echo: true}))
	c, _ := f.dial(t, "/ops", nil)
	rows := waitListedN(t, f.reg, 1)
	if rows[0].Kind != customep.KindWS {
		t.Fatalf("listed kind = %q, want ws", rows[0].Kind)
	}
	mustWrite(t, c, wsmock.Text, `{"a":1}`)
	if _, got := mustRead(t, c); got != `{"a":1}` {
		t.Fatalf("echo = %q", got)
	}
	conn := f.reg.Lookup(1, rows[0].ID)
	id, err := conn.Push(context.Background(), "", []byte(`{"srv":1}`))
	if err != nil || id != 2 {
		t.Fatalf("push = (%d, %v), want ordinal 2 after one echo", id, err)
	}
	if _, got := mustRead(t, c); got != `{"srv":1}` {
		t.Fatalf("pushed frame = %q", got)
	}
	after := f.reg.Snapshot(1)
	if after[0].FramesIn != 1 || after[0].Pushed != 1 || after[0].Frames != 2 {
		t.Fatalf("counters = %+v, want framesIn 1 pushed 1 frames 2", after[0])
	}
	if !f.reg.CloseByAdmin(1, rows[0].ID) {
		t.Fatal("CloseByAdmin must report true on a live connection")
	}
	if code := readClose(t, c); code != wsmock.StatusGoingAway {
		t.Fatalf("close code = %d, want 1001", code)
	}
	ev := waitEvent(t, f.sink, "/ops")
	for _, tok := range []string{"closed:admin", "close:1001", "pushed:1", "frames_in:1"} {
		if !strings.Contains(ev.Notes, tok) {
			t.Fatalf("notes = %q, want %s", ev.Notes, tok)
		}
	}
}

func TestWS_registryCloseEndsALiveConnectionAndJoinsTheReader(t *testing.T) {
	f := newWSFixture(t, wsOpts(), wsRow(1, "/idle", &customep.Stream{Echo: true}))
	c, _ := f.dial(t, "/idle", nil)
	waitListedN(t, f.reg, 1)
	done := make(chan wsmock.StatusCode, 1)
	go func() { done <- readClose(t, c) }()
	f.reg.Close() // returns only once the handler — and its reader — returned
	select {
	case code := <-done:
		if code != wsmock.StatusGoingAway {
			t.Fatalf("close code = %d, want 1001", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the client never saw the close after Registry.Close")
	}
	if ev := waitEvent(t, f.sink, "/idle"); !strings.Contains(ev.Notes, "close:1001") {
		t.Fatalf("notes = %q", ev.Notes)
	}
}

func TestWS_forcedStatusAbortsTheHandshake(t *testing.T) {
	f := newWSFixture(t, wsOpts(), wsRow(1, "/echo", &customep.Stream{Echo: true}))
	if err := f.state.Set(1, livestate.Directive{Target: livestate.Target{Method: "GET", Path: "/echo"}, Action: livestate.ActionStatus, Status: 503}); err != nil {
		t.Fatalf("set directive: %v", err)
	}
	c, status := f.dial(t, "/echo", nil)
	if c != nil || status != http.StatusServiceUnavailable {
		t.Fatalf("forced 503: conn=%v status=%d, want 503 and no upgrade", c != nil, status)
	}
}

func waitListedN(t *testing.T, reg *stream.Registry, n int) []stream.Snapshot {
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

// A14: under "all" a WebSocket row carries every outbound frame and every
// inbound JSON object, one per line, each inbound one redacted.
func TestWS_trafficFramesAll_recordsBothDirectionsAsNDJSON(t *testing.T) {
	def := &customep.Stream{Echo: true}
	opts := wsOpts()
	opts.TrafficFrames = mockplane.TrafficFramesAll
	opts.TrafficMaxFrames = 10
	opts.TrafficMaxBytes = 64 << 10
	f := newWSFixture(t, opts, wsRow(1, "/echo-all", def))
	c, _ := f.dial(t, "/echo-all", nil)
	mustWrite(t, c, wsmock.Text, `{"a":1,"token":"s3cr3t"}`)
	if _, got := mustRead(t, c); got != `{"a":1,"token":"s3cr3t"}` {
		t.Fatalf("echo #1 = %q", got)
	}
	mustWrite(t, c, wsmock.Text, `plain`)
	if _, got := mustRead(t, c); got != `plain` {
		t.Fatalf("echo #2 = %q", got)
	}
	mustWrite(t, c, wsmock.Text, `{"b":2}`)
	if _, got := mustRead(t, c); got != `{"b":2}` {
		t.Fatalf("echo #3 = %q", got)
	}
	_ = c.Close(wsmock.StatusNormalClosure, "bye")
	ev := waitEvent(t, f.sink, "/echo-all")
	for _, tok := range []string{"stream:ws", "frames:3", "frames_in:3", "frames_recorded:3", "frames_in_recorded:2"} {
		if !strings.Contains(ev.Notes, tok) {
			t.Errorf("notes = %q, want %s", ev.Notes, tok)
		}
	}
	if strings.Contains(ev.Notes, "first_in:") {
		t.Errorf("notes = %q: the first inbound frame was JSON, so no first_in note", ev.Notes)
	}
	out := strings.Split(string(ev.RespBody), "\n")
	if len(out) != 3 || out[1] != "plain" || out[2] != `{"b":2}` {
		t.Errorf("respBody lines = %q, want the three echoes one per line", out)
	}
	in := strings.Split(string(ev.ReqBody), "\n")
	if len(in) != 2 || strings.Contains(string(ev.ReqBody), "s3cr3t") || in[1] != `{"b":2}` || ev.ReqContentType != "application/x-ndjson" {
		t.Errorf("reqBody = %q (%s), want two redacted JSON objects as NDJSON", ev.ReqBody, ev.ReqContentType)
	}
	if ev.Truncated {
		t.Errorf("truncated = true under a budget nothing reached")
	}
}

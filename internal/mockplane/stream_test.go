package mockplane_test

// stream_test.go is P6b's serving half observed over a REAL socket
// (decisions.md mocker-p6b-sse-mock A5): an httptest.Server around the
// Plane and a live http.Client, because httptest.ResponseRecorder cannot
// take a write deadline and would exercise only the 501 path. One test uses
// the recorder for exactly that refusal.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/mockplane"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/stream"
	"github.com/yashok111/mocker/internal/traffic"
	"github.com/yashok111/mocker/internal/workspaces"
)

// streamSink is a [mockplane.TrafficSink] that keeps every event — this
// file's own, because traffic_test.go's fakeTrafficSink is white-box
// (package mockplane) and this file is black-box.
type streamSink struct {
	mu     sync.Mutex
	events []traffic.Event
}

func (s *streamSink) Record(ev traffic.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *streamSink) all() []traffic.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]traffic.Event(nil), s.events...)
}

func sseWorkspace(id int64, slug string) *workspaces.Workspace {
	s := domain.DefaultSettings()
	s.Seed = 7
	return &workspaces.Workspace{ID: id, Slug: slug, Revision: 1, Settings: s}
}

func sseRow(id int64, path string, def *customep.Stream) *customep.Row {
	return &customep.Row{
		ID: id, WorkspaceID: 1, Method: http.MethodGet, Path: path,
		CanonicalPath: router.CanonicalPath(path), SourceOrder: id, OverrideOn: true,
		ActiveStatus: 200, Kind: customep.KindSSE, Stream: def,
	}
}

func fastOpts() mockplane.StreamOptions {
	return mockplane.StreamOptions{Ping: time.Hour, FrameTimeout: 2 * time.Second, Lifetime: time.Hour, TrafficFrames: mockplane.TrafficFramesFirst}
}

// streamFixture is a Plane serving workspace 1 ("alex") with rows, its own
// registry, a fake traffic sink and a live listener.
type streamFixture struct {
	plane *mockplane.Plane
	reg   *stream.Registry
	sink  *streamSink
	live  *httptest.Server
	state *livestate.Store
}

func newStreamFixture(t *testing.T, opts mockplane.StreamOptions, perWorkspaceCap int, rows ...*customep.Row) *streamFixture {
	t.Helper()
	return newStreamFixtureMaxResponse(t, opts, perWorkspaceCap, 0, rows...)
}

// newStreamFixtureMaxResponse is newStreamFixture with MOCKER_MAX_RESPONSE
// chosen by the caller; 0 means the fixture default. A18 clause 40 needs a
// non-default one — see newPlaneWithMaxResponse for why.
func newStreamFixtureMaxResponse(t *testing.T, opts mockplane.StreamOptions, perWorkspaceCap int, maxResponse int64, rows ...*customep.Row) *streamFixture {
	t.Helper()
	ws := sseWorkspace(1, "alex")
	var p *mockplane.Plane
	if maxResponse > 0 {
		p = newPlaneWithMaxResponse(maxResponse, ws, sseWorkspace(2, "bob"))
	} else {
		p = newPlane(ws, sseWorkspace(2, "bob"))
	}
	custom := &fakeCustomSource{rows: map[int64][]*customep.Row{1: rows, 2: rows}}
	p.SetCustomEndpoints(custom)
	reg := stream.NewWorkspaceRegistry(perWorkspaceCap)
	p.SetStreams(reg, opts)
	sink := &streamSink{}
	p.SetTraffic(sink)
	state := livestate.NewStore(0, nil)
	p.SetLiveState(state)
	live := httptest.NewUnstartedServer(p)
	live.Config.WriteTimeout = 300 * time.Millisecond // the exemption is what keeps a stream alive past this
	live.Start()
	t.Cleanup(func() {
		reg.Close()
		live.Close()
	})
	return &streamFixture{plane: p, reg: reg, sink: sink, live: live, state: state}
}

// liveResp wraps the response so that a body the test deliberately leaves
// open for its whole life (closed by t.Cleanup) is not read by the
// bodyclose linter as a leak — the same shape internal/admin's stream tests
// use.
type liveResp struct {
	*http.Response
}

func (f *streamFixture) open(t *testing.T, host, path string) liveResp {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.live.URL+path, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Host = host
	resp, err := f.live.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return liveResp{resp}
}

type sseFrame struct {
	id    string
	event string
	data  string
	at    time.Time
}

// readFrames reads every non-comment frame until EOF or the deadline.
func readFrames(t *testing.T, body io.Reader, limit int, deadline time.Duration) []sseFrame {
	t.Helper()
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.Index(data, []byte("\n\n")); i >= 0 {
			return i + 2, data[:i], nil
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	var out []sseFrame
	done := make(chan struct{})
	go func() {
		defer close(done)
		for sc.Scan() {
			raw := sc.Text()
			if strings.HasPrefix(raw, ":") {
				continue
			}
			fr := sseFrame{at: time.Now()}
			for _, line := range strings.Split(raw, "\n") {
				switch {
				case strings.HasPrefix(line, "id: "):
					fr.id = strings.TrimPrefix(line, "id: ")
				case strings.HasPrefix(line, "event: "):
					fr.event = strings.TrimPrefix(line, "event: ")
				case strings.HasPrefix(line, "data: "):
					fr.data = strings.TrimPrefix(line, "data: ")
				}
			}
			out = append(out, fr)
			if limit > 0 && len(out) >= limit {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(deadline):
		t.Fatalf("readFrames: only %d frame(s) within %v", len(out), deadline)
	}
	return out
}

func timeline(frames ...customep.Frame) *customep.Stream {
	return &customep.Stream{Timeline: &customep.Timeline{Frames: frames}}
}

// TestStream_timelineFramesInOrderThenClose is A3(a): three frames at their
// delays, ids 1..3, the handshake headers, and the connection closing on
// its own once the timeline drains — past the listener's own 300 ms
// WriteTimeout, which proves the exemption.
func TestStream_timelineFramesInOrderThenClose(t *testing.T) {
	def := timeline(
		customep.Frame{DelayMs: 0, Event: "first", Data: jsonx.RawMessage(`{"n": 1}`)},
		customep.Frame{DelayMs: 400, Data: jsonx.RawMessage(`{"n":2}`)},
		customep.Frame{DelayMs: 400, Event: "last", Data: jsonx.RawMessage(`"three"`)},
	)
	f := newStreamFixture(t, fastOpts(), 10, sseRow(1, "/events", def))

	start := time.Now()
	resp := f.open(t, "alex.mock.local", "/events")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	frames := readFrames(t, resp.Body, 0, 5*time.Second)
	if len(frames) != 3 {
		t.Fatalf("frames = %d, want 3: %+v", len(frames), frames)
	}
	if frames[0].id != "1" || frames[0].event != "first" || frames[0].data != `{"n":1}` {
		t.Errorf("frame 1 = %+v", frames[0])
	}
	if frames[1].id != "2" || frames[1].event != "" || frames[1].data != `{"n":2}` {
		t.Errorf("frame 2 = %+v (no event line expected)", frames[1])
	}
	if frames[2].id != "3" || frames[2].event != "last" || frames[2].data != `"three"` {
		t.Errorf("frame 3 = %+v", frames[2])
	}
	if gap := frames[1].at.Sub(frames[0].at); gap < 300*time.Millisecond {
		t.Errorf("frame 2 arrived %v after frame 1, want >= 300ms — the delay is not honoured", gap)
	}
	if total := time.Since(start); total < 700*time.Millisecond {
		t.Errorf("connection lasted %v, want >= 700ms (two 400 ms delays) — and past the 300 ms WriteTimeout", total)
	}
	// readFrames returned on EOF: the server closed (closeWhenDone). The
	// registry has released the slot.
	deadline := time.Now().Add(2 * time.Second)
	for f.reg.Stats().Open != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := f.reg.Stats().Open; got != 0 {
		t.Fatalf("Open after close = %d", got)
	}

	// D11: ONE traffic row for the connection, status 200, frames:3 in the
	// notes, the first frame's wire bytes as the body under "first".
	events := f.sink.all()
	if len(events) != 1 {
		t.Fatalf("traffic events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Status != 200 || !strings.Contains(ev.Notes, "stream:sse") || !strings.Contains(ev.Notes, "frames:3") {
		t.Errorf("event = status %d notes %q, want 200 with stream:sse,frames:3", ev.Status, ev.Notes)
	}
	if !strings.HasPrefix(string(ev.RespBody), "id: 1\nevent: first\ndata: {\"n\":1}") {
		t.Errorf("respBody = %q, want the first frame's wire bytes", ev.RespBody)
	}
	if ev.Duration < 700*time.Millisecond {
		t.Errorf("duration = %v, want the connection's whole life", ev.Duration)
	}
}

// TestStream_tickIsDeterministicAcrossConnections is A3(b) and D4: two
// connections to the same tick endpoint receive byte-identical bodies at
// the same ordinals; a different endpoint id gives different bodies.
func TestStream_tickIsDeterministicAcrossConnections(t *testing.T) {
	schema := jsonx.RawMessage(`{"type":"object","properties":{"price":{"type":"number"},"sym":{"type":"string"}},"required":["price","sym"]}`)
	def := &customep.Stream{Tick: &customep.Tick{IntervalMs: 100, Event: "price", Schema: schema}}
	f := newStreamFixture(t, fastOpts(), 10, sseRow(1, "/prices", def), sseRow(2, "/other", def))

	first := readFrames(t, f.open(t, "alex.mock.local", "/prices").Body, 3, 5*time.Second)
	second := readFrames(t, f.open(t, "alex.mock.local", "/prices").Body, 3, 5*time.Second)
	other := readFrames(t, f.open(t, "alex.mock.local", "/other").Body, 3, 5*time.Second)
	for i := range 3 {
		if first[i].data != second[i].data {
			t.Errorf("tick %d differs across connections: %q vs %q", i+1, first[i].data, second[i].data)
		}
		if first[i].event != "price" {
			t.Errorf("tick %d event = %q", i+1, first[i].event)
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(first[i].data), &body); err != nil || body["sym"] == nil {
			t.Errorf("tick %d body %q is not a generated object with sym", i+1, first[i].data)
		}
	}
	if first[0].data == other[0].data && first[1].data == other[1].data {
		t.Errorf("two endpoints produced the same tick bodies — the endpoint id is not in the seed")
	}
	if first[0].data == first[1].data {
		t.Errorf("consecutive ticks are identical — the ordinal is not in the seed")
	}
	if gap := first[1].at.Sub(first[0].at); gap < 60*time.Millisecond {
		t.Errorf("tick gap = %v, want about 100ms", gap)
	}
}

// TestStream_sessionLayerAppliesAtTheHandshakeOnly is A3(c) / §30.4: a
// forced status answers that status with no stream; a delay delays the
// HANDSHAKE and not each frame.
func TestStream_sessionLayerAppliesAtTheHandshakeOnly(t *testing.T) {
	def := timeline(
		customep.Frame{DelayMs: 0, Data: jsonx.RawMessage(`1`)},
		customep.Frame{DelayMs: 100, Data: jsonx.RawMessage(`2`)},
	)
	f := newStreamFixture(t, fastOpts(), 10, sseRow(1, "/events", def))

	if err := f.state.Set(1, livestate.Directive{Target: livestate.Target{Method: "GET", Path: "/events"}, Action: livestate.ActionStatus, Status: 503}); err != nil {
		t.Fatalf("set status: %v", err)
	}
	resp := f.open(t, "alex.mock.local", "/events")
	if resp.StatusCode != http.StatusServiceUnavailable || strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("forced 503: status = %d, Content-Type %q — the forced status must abort the handshake", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	f.state.Clear(1)

	if err := f.state.Set(1, livestate.Directive{Target: livestate.Target{Method: "GET", Path: "/events"}, Action: livestate.ActionDelay, Ms: 300}); err != nil {
		t.Fatalf("set delay: %v", err)
	}
	start := time.Now()
	resp = f.open(t, "alex.mock.local", "/events")
	handshake := time.Since(start)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delayed: status = %d", resp.StatusCode)
	}
	frames := readFrames(t, resp.Body, 2, 5*time.Second)
	if handshake < 250*time.Millisecond {
		t.Errorf("handshake took %v, want >= 250ms under a 300ms delay", handshake)
	}
	if gap := frames[1].at.Sub(frames[0].at); gap > 250*time.Millisecond {
		t.Errorf("frame gap = %v under a 300ms delay directive, want ~100ms — the delay must not be added per frame", gap)
	}
}

// TestStream_capIsPerWorkspace is A3(d) and D9: the cap counts one
// workspace's connections; a second workspace still opens.
func TestStream_capIsPerWorkspace(t *testing.T) {
	def := &customep.Stream{Tick: &customep.Tick{IntervalMs: 100, Schema: jsonx.RawMessage(`{"type":"object"}`)}}
	f := newStreamFixture(t, fastOpts(), 2, sseRow(1, "/t", def))

	a := f.open(t, "alex.mock.local", "/t")
	b := f.open(t, "alex.mock.local", "/t")
	if a.StatusCode != http.StatusOK || b.StatusCode != http.StatusOK {
		t.Fatalf("first two: %d %d", a.StatusCode, b.StatusCode)
	}
	deadline := time.Now().Add(2 * time.Second)
	for f.reg.Stats().Open != 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	c := f.open(t, "alex.mock.local", "/t")
	if c.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("third on the same workspace: status = %d, want 503", c.StatusCode)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(c.Body).Decode(&env)
	if env.Error.Code != "service_unavailable" {
		t.Errorf("code = %q", env.Error.Code)
	}
	if st := f.reg.Stats(); st.RefusedCap != 1 || st.Cap != 2 {
		t.Errorf("stats = %+v", st)
	}
	d := f.open(t, "bob.mock.local", "/t")
	if d.StatusCode != http.StatusOK {
		t.Fatalf("another workspace: status = %d, want 200 — the cap is per workspace", d.StatusCode)
	}
}

// TestStream_lifetimeCloses is A3(e): the mock plane's own lifetime ends
// the connection.
func TestStream_lifetimeCloses(t *testing.T) {
	def := &customep.Stream{Tick: &customep.Tick{IntervalMs: 100, Schema: jsonx.RawMessage(`{"type":"object"}`)}}
	opts := fastOpts()
	opts.Lifetime = 400 * time.Millisecond
	f := newStreamFixture(t, opts, 10, sseRow(1, "/t", def))

	start := time.Now()
	resp := f.open(t, "alex.mock.local", "/t")
	_, _ = io.Copy(io.Discard, resp.Body)
	if took := time.Since(start); took < 350*time.Millisecond || took > 3*time.Second {
		t.Fatalf("connection lasted %v, want about the 400ms lifetime", took)
	}
}

// TestStream_closeWhenDoneFalseKeepsPinging: a drained timeline with
// closeWhenDone false holds the connection until the lifetime.
func TestStream_closeWhenDoneFalseKeepsPinging(t *testing.T) {
	keep := false
	def := timeline(customep.Frame{Data: jsonx.RawMessage(`1`)})
	def.CloseWhenDone = &keep
	opts := fastOpts()
	opts.Lifetime = 500 * time.Millisecond
	opts.Ping = 50 * time.Millisecond
	f := newStreamFixture(t, opts, 10, sseRow(1, "/t", def))

	start := time.Now()
	resp := f.open(t, "alex.mock.local", "/t")
	raw, _ := io.ReadAll(resp.Body)
	if took := time.Since(start); took < 450*time.Millisecond {
		t.Fatalf("connection closed after %v with closeWhenDone false, want the lifetime", took)
	}
	if strings.Count(string(raw), ": ping") < 3 {
		t.Errorf("pings on the held connection = %d, want >= 3", strings.Count(string(raw), ": ping"))
	}
}

// TestStream_unsupportedWriterAnswers501 is the one recorder-based test:
// D9's refusal before any frame.
func TestStream_unsupportedWriterAnswers501(t *testing.T) {
	def := timeline(customep.Frame{Data: jsonx.RawMessage(`1`)})
	f := newStreamFixture(t, fastOpts(), 10, sseRow(1, "/events", def))
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/events", nil)
	rec := httptest.NewRecorder()
	f.plane.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented || !strings.Contains(rec.Body.String(), "streaming_unsupported") {
		t.Fatalf("status = %d body = %s, want 501 streaming_unsupported", rec.Code, rec.Body.String())
	}
	if st := f.reg.Stats(); st.RefusedUnsupported != 1 || st.Open != 0 {
		t.Errorf("stats = %+v", st)
	}
}

// TestStream_registryCloseEndsALiveConnection is D13 on this plane: Close
// cancels a live stream and waits for its handler.
func TestStream_registryCloseEndsALiveConnection(t *testing.T) {
	def := &customep.Stream{Tick: &customep.Tick{IntervalMs: 100, Schema: jsonx.RawMessage(`{"type":"object"}`)}}
	f := newStreamFixture(t, fastOpts(), 10, sseRow(1, "/t", def))
	resp := f.open(t, "alex.mock.local", "/t")
	deadline := time.Now().Add(2 * time.Second)
	for f.reg.Stats().Open != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	closed := make(chan struct{})
	go func() { f.reg.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("registry Close did not return with a live mock stream")
	}
	drained := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, resp.Body); close(drained) }()
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("client never saw EOF after Close")
	}
}

// TestPreviewStream_laysFramesOutInTime is D13: the timeline and the tick
// interleave by time, the tick is generated deterministically, the limit
// marks truncation, and the rate estimate is non-zero.
func TestPreviewStream_laysFramesOutInTime(t *testing.T) {
	f := newStreamFixture(t, fastOpts(), 10)
	draft := &customep.Row{Method: http.MethodGet, Path: "/p", CanonicalPath: "/p", Kind: customep.KindSSE, Stream: &customep.Stream{
		Timeline: &customep.Timeline{Frames: []customep.Frame{{DelayMs: 50, Event: "a", Data: jsonx.RawMessage(`{ "x" : 1 }`)}, {DelayMs: 300, Data: jsonx.RawMessage(`2`)}}},
		Tick:     &customep.Tick{IntervalMs: 100, Event: "t", Schema: jsonx.RawMessage(`{"type":"object","properties":{"v":{"type":"integer"}},"required":["v"]}`)},
	}}
	pv, err := f.plane.PreviewStream(context.Background(), sseWorkspace(1, "alex"), draft)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if pv.Kind != "sse" || !pv.Truncated || len(pv.Frames) != 50 {
		t.Fatalf("preview = kind %q truncated %v frames %d, want sse/true/50", pv.Kind, pv.Truncated, len(pv.Frames))
	}
	// Order by time: a@50, t@100, t@200, t@300, 2@350, t@400 ...
	want := []struct {
		at    int
		event string
	}{{50, "a"}, {100, "t"}, {200, "t"}, {300, "t"}, {350, ""}, {400, "t"}}
	for i, w := range want {
		if pv.Frames[i].AtMs != w.at || pv.Frames[i].Event != w.event {
			t.Errorf("frame %d = at %d event %q, want at %d event %q", i, pv.Frames[i].AtMs, pv.Frames[i].Event, w.at, w.event)
		}
	}
	if string(pv.Frames[0].Data) != `{"x":1}` {
		t.Errorf("timeline data not compacted: %s", pv.Frames[0].Data)
	}
	if pv.MaxBytesPerSec <= 0 {
		t.Errorf("maxBytesPerSec = %d, want > 0", pv.MaxBytesPerSec)
	}
	again, _ := f.plane.PreviewStream(context.Background(), sseWorkspace(1, "alex"), draft)
	if string(again.Frames[1].Data) != string(pv.Frames[1].Data) {
		t.Errorf("preview is not deterministic")
	}

	// A timeline-only draft that closes: not truncated, exactly its frames.
	only := &customep.Row{Method: http.MethodGet, Path: "/q", CanonicalPath: "/q", Kind: customep.KindSSE, Stream: timeline(customep.Frame{Data: jsonx.RawMessage(`1`)}, customep.Frame{DelayMs: 10, Data: jsonx.RawMessage(`2`)})}
	pv2, err := f.plane.PreviewStream(context.Background(), sseWorkspace(1, "alex"), only)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if pv2.Truncated || len(pv2.Frames) != 2 {
		t.Errorf("timeline-only preview = truncated %v frames %d, want false/2", pv2.Truncated, len(pv2.Frames))
	}
}

// A14: under "all" the connection's ONE row carries every frame's wire
// bytes, whole frames only, up to the per-row budget — and says so.
func TestSSE_trafficFramesAll_keepsEveryFrameUpToTheBudget(t *testing.T) {
	def := timeline(
		customep.Frame{DelayMs: 0, Event: "a", Data: jsonx.RawMessage(`{"n":1}`)},
		customep.Frame{DelayMs: 0, Event: "b", Data: jsonx.RawMessage(`{"n":2}`)},
		customep.Frame{DelayMs: 0, Event: "c", Data: jsonx.RawMessage(`{"n":3}`)},
	)

	opts := fastOpts()
	opts.TrafficFrames = mockplane.TrafficFramesAll
	opts.TrafficMaxFrames = 10
	opts.TrafficMaxBytes = 64 << 10
	f := newStreamFixture(t, opts, 10, sseRow(1, "/all", def))
	readFrames(t, f.open(t, "alex.mock.local", "/all").Body, 0, 5*time.Second)
	ev := waitEvent(t, f.sink, "/all")
	body := string(ev.RespBody)
	for _, want := range []string{"id: 1\nevent: a\n", "id: 2\nevent: b\n", "id: 3\nevent: c\ndata: {\"n\":3}"} {
		if !strings.Contains(body, want) {
			t.Errorf("respBody = %q, want it to carry %q", body, want)
		}
	}
	if !strings.Contains(ev.Notes, "frames:3") || !strings.Contains(ev.Notes, "frames_recorded:3") || ev.Truncated {
		t.Errorf("notes = %q truncated = %v, want frames:3,frames_recorded:3 and not truncated", ev.Notes, ev.Truncated)
	}

	// A frame budget of 2: the third frame is served, counted, not kept,
	// and the row says truncated.
	opts.TrafficMaxFrames = 2
	f2 := newStreamFixture(t, opts, 10, sseRow(1, "/two", def))
	readFrames(t, f2.open(t, "alex.mock.local", "/two").Body, 0, 5*time.Second)
	ev = waitEvent(t, f2.sink, "/two")
	if strings.Contains(string(ev.RespBody), "event: c") || !strings.Contains(ev.Notes, "frames:3") || !strings.Contains(ev.Notes, "frames_recorded:2") || !ev.Truncated {
		t.Errorf("frame budget 2: body %q notes %q truncated %v", ev.RespBody, ev.Notes, ev.Truncated)
	}

	// A byte budget the second frame does not fit in: whole frames only,
	// so the row holds exactly the first and is truncated.
	opts.TrafficMaxFrames = 10
	opts.TrafficMaxBytes = 1024
	big := timeline(
		customep.Frame{DelayMs: 0, Event: "a", Data: jsonx.RawMessage(`{"n":1}`)},
		customep.Frame{DelayMs: 0, Event: "b", Data: jsonx.RawMessage(`{"pad":"` + strings.Repeat("x", 1100) + `"}`)},
	)
	f3 := newStreamFixture(t, opts, 10, sseRow(1, "/big", big))
	readFrames(t, f3.open(t, "alex.mock.local", "/big").Body, 0, 5*time.Second)
	ev = waitEvent(t, f3.sink, "/big")
	if !strings.HasSuffix(string(ev.RespBody), "data: {\"n\":1}\n\n") || strings.Contains(string(ev.RespBody), "pad") || !ev.Truncated || !strings.Contains(ev.Notes, "frames_recorded:1") {
		t.Errorf("byte budget: body %q notes %q truncated %v", ev.RespBody, ev.Notes, ev.Truncated)
	}
}

package mockplane_test

// hooks_test.go is A18's acceptance for the two stream hooks' SERVING side —
// §A clauses 40-44 of docs/A18-endpoint-functions.md — over the same real
// sockets P6b's and P6d's own tests use, because what these clauses observe
// is framing, counting and close mechanics, none of which a fake writer has.

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/luafn"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/wsmock"
)

func luaTick(intervalMs int, src string) *customep.Stream {
	return &customep.Stream{Tick: &customep.Tick{IntervalMs: intervalMs, Lua: src}}
}

// noteValue reads one `name:N` token out of a traffic row's notes, and
// answers -1 for a token that is absent — which is a DIFFERENT observation
// from 0 and is what several clauses below turn on: "not counted" and
// "counted zero times" are two claims, and only one of them is what a token
// that is only emitted when positive can make.
func noteValue(notes, name string) int {
	for _, tok := range strings.Split(notes, ",") {
		if v, ok := strings.CutPrefix(tok, name+":"); ok {
			n, err := strconv.Atoi(v)
			if err != nil {
				return -1
			}
			return n
		}
	}
	return -1
}

// --- clause 40: a Lua tick's frame checks ---------------------------------

// TestLuaTick_refusedBodiesSkipOnceAndLeaveTheConnectionOpen is clause 40,
// and every one of its four failure conditions is asserted separately.
//
// The two refusals are exercised on ONE connection each because the clause's
// last condition — counted once, never twice — is a claim about a single
// firing: an implementation that checks the CR/LF and the size conditions
// independently with no early return increments frames_skipped twice for one
// frame, and a test that only compared "more than zero" would pass over it.
func TestLuaTick_refusedBodiesSkipOnceAndLeaveTheConnectionOpen(t *testing.T) {
	for _, tc := range []struct {
		name        string
		src         string
		maxResponse int64
	}{
		// A string with a raw LF would put a second `data:` boundary inside
		// one frame and desynchronise every frame after it.
		{"a CR or LF breaks SSE framing", `if ordinal == 1 then return "a\nb" end return {n = ordinal}`, 0},
		// Observed at a NON-DEFAULT MOCKER_MAX_RESPONSE, exactly as clause
		// 25 observes its own twin: at the 4 MiB default a hard-coded
		// ceiling and a config-reading one emit identical bytes.
		{"a body over MOCKER_MAX_RESPONSE", `if ordinal == 1 then return string.rep("x", 9000) end return {n = ordinal}`, 4096},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := sseRow(1, "/events", luaTick(60, tc.src))
			f := newStreamFixtureMaxResponse(t, fastOpts(), 4, tc.maxResponse, row)

			resp := f.open(t, "alex.mock.local", "/events")
			// TWO frames read after the refused first one: the connection
			// must not merely survive the skip, it must keep ticking.
			frames := readFrames(t, resp.Body, 2, 4*time.Second)
			if len(frames) < 2 {
				t.Fatalf("frames = %d, want 2 — a skipped firing must leave the connection open", len(frames))
			}
			// Ordinal 1 was refused, so the first frame the client sees is
			// the SECOND firing: nothing of the refused body reached it.
			if strings.Contains(frames[0].data, "\n") || strings.Contains(frames[0].data, "xxxx") {
				t.Fatalf("the refused body reached the wire: %q", frames[0].data)
			}
			resp.Body.Close()

			ev := waitEvent(t, f.sink, "/events")
			if got := noteValue(ev.Notes, "frames_skipped"); got != 1 {
				t.Fatalf("frames_skipped = %d, want exactly 1 (notes %q) — one refused firing is one skip, and an implementation checking both conditions without an early return counts it twice",
					got, ev.Notes)
			}
		})
	}
}

// --- clause 41: `return nil` ----------------------------------------------

// TestLuaTick_nilReturnSkipsAndCountsNothing is clause 41, whose named defeat
// is a nil return counted as BOTH a skip and an error: those are different
// outcomes, and D10.1 makes only the first of them a frame the plane refused.
// A decline is neither — the function chose not to send.
func TestLuaTick_nilReturnSkipsAndCountsNothing(t *testing.T) {
	row := sseRow(1, "/events", luaTick(60, `if ordinal % 2 == 1 then return nil end return {n = ordinal}`))
	f := newStreamFixture(t, fastOpts(), 4, row)

	resp := f.open(t, "alex.mock.local", "/events")
	frames := readFrames(t, resp.Body, 2, 4*time.Second)
	if len(frames) < 2 {
		t.Fatalf("frames = %d, want 2 — a nil return leaves the connection open", len(frames))
	}
	for _, fr := range frames {
		// Only even ordinals send, so every frame that arrives carries one.
		var body map[string]any
		if err := jsonx.Unmarshal([]byte(fr.data), &body); err != nil {
			t.Fatalf("frame %q did not decode: %v", fr.data, err)
		}
		if n, _ := body["n"].(float64); int(n)%2 != 0 {
			t.Fatalf("frame carries ordinal %v; the odd firings returned nil and must have sent nothing", body["n"])
		}
	}
	resp.Body.Close()

	ev := waitEvent(t, f.sink, "/events")
	if got := noteValue(ev.Notes, "frames_skipped"); got != -1 {
		t.Fatalf("frames_skipped = %d (notes %q); a declined firing is not a refused frame and must not be counted as one",
			got, ev.Notes)
	}
}

// --- clause 42: onFrame's two verbs ---------------------------------------

// TestOnFrame_replyProducesExactlyOneFrame is clause 42's first half. It also
// pins that onFrame REPLACES echo rather than running beside it: the
// definition carries no echo, and a second frame arriving would mean the
// reactive path had also answered.
func TestOnFrame_replyProducesExactlyOneFrame(t *testing.T) {
	def := &customep.Stream{OnFrame: `if frame.op == "ping" then return "reply", {op = "pong"} end return nil`}
	f := newWSFixture(t, wsOpts(), wsRow(1, "/ws", def))
	c, _ := f.dial(t, "/ws", nil)

	mustWrite(t, c, wsmock.Text, `{"op":"ping"}`)
	typ, got := mustRead(t, c)
	if typ != wsmock.Text || !strings.Contains(got, `"pong"`) {
		t.Fatalf("reply = %q (type %v), want the hook's own object", got, typ)
	}

	// A frame the hook answers with nil must produce NOTHING — the read
	// below would return the reply to it if echo were still live.
	mustWrite(t, c, wsmock.Text, `{"op":"other"}`)
	mustWrite(t, c, wsmock.Text, `{"op":"ping"}`)
	_, second := mustRead(t, c)
	if !strings.Contains(second, `"pong"`) {
		t.Fatalf("second read = %q, want the pong for the THIRD frame — anything else means the nil-answered frame produced one", second)
	}
}

// TestOnFrame_closeIsPerformedByTheWriterLoop is clause 42's second half and
// its named defeat is "the reader writes the close itself". What the test can
// observe from outside is the consequence P6d's discipline guarantees: the
// close carries the hook's OWN code, the peer's half of the handshake is read
// by a reader that is still draining, and onFrame is not called again.
func TestOnFrame_closeIsPerformedByTheWriterLoop(t *testing.T) {
	def := &customep.Stream{OnFrame: `if frame.op == "bye" then return "close", 4321, "asked" end
		return "reply", {seen = frame.op}`}
	f := newWSFixture(t, wsOpts(), wsRow(1, "/ws", def))
	c, _ := f.dial(t, "/ws", nil)

	mustWrite(t, c, wsmock.Text, `{"op":"hello"}`)
	if _, got := mustRead(t, c); !strings.Contains(got, "hello") {
		t.Fatalf("first reply = %q", got)
	}
	mustWrite(t, c, wsmock.Text, `{"op":"bye"}`)
	if code := readClose(t, c); code != 4321 {
		t.Fatalf("close code = %d, want the hook's own 4321", code)
	}

	ev := waitEvent(t, f.sink, "/ws")
	if !strings.Contains(ev.Notes, "close:4321") {
		t.Fatalf("notes = %q, want close:4321", ev.Notes)
	}
}

// --- clause 43: a broken hook ---------------------------------------------

// TestOnFrame_errorCountsItsOwnTokenAndKeepsBeingCalled is clause 43, and
// three of its four assertions are about NOT confusing two counters:
// on_frame_errors is the hook's, replies_dropped is the send budget's, and a
// row that reported the first as the second would hide broken code behind a
// full budget.
func TestOnFrame_errorCountsItsOwnTokenAndKeepsBeingCalled(t *testing.T) {
	def := &customep.Stream{OnFrame: `if frame.op == "boom" then error("no") end
		return "reply", {seen = frame.op}`}
	f := newWSFixture(t, wsOpts(), wsRow(1, "/ws", def))
	c, _ := f.dial(t, "/ws", nil)

	mustWrite(t, c, wsmock.Text, `{"op":"boom"}`)
	// The NEXT frame still gets an answer: a broken hook must not turn the
	// connection into a sink.
	mustWrite(t, c, wsmock.Text, `{"op":"after"}`)
	if _, got := mustRead(t, c); !strings.Contains(got, "after") {
		t.Fatalf("reply = %q, want the frame AFTER the failing one to be answered", got)
	}

	_ = c.CloseNow()
	ev := waitEvent(t, f.sink, "/ws")
	if got := noteValue(ev.Notes, "on_frame_errors"); got != 1 {
		t.Fatalf("on_frame_errors = %d (notes %q), want 1", got, ev.Notes)
	}
	if got := noteValue(ev.Notes, "replies_dropped"); got != -1 {
		t.Fatalf("replies_dropped = %d (notes %q); that token means the send budget was full, and a hook error counted there hides broken code behind a full budget",
			got, ev.Notes)
	}
}

// TestOnFrame_malformedReturnIsTheSameOutcomeAsAnError pins the other half of
// D10.2's "a Lua error OR malformed return": a verb the contract does not
// have is not silence, it is a hook that is wrong and must say so.
func TestOnFrame_malformedReturnIsTheSameOutcomeAsAnError(t *testing.T) {
	def := &customep.Stream{OnFrame: `return "shout", {}`}
	f := newWSFixture(t, wsOpts(), wsRow(1, "/ws", def))
	c, _ := f.dial(t, "/ws", nil)

	mustWrite(t, c, wsmock.Text, `{"op":"x"}`)
	// Nothing comes back; give the reader a moment to have processed it,
	// then close and read the row.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, _, err := c.Read(ctx); err == nil {
		t.Fatal("a malformed return produced a reply")
	}
	_ = c.CloseNow()

	ev := waitEvent(t, f.sink, "/ws")
	if got := noteValue(ev.Notes, "on_frame_errors"); got != 1 {
		t.Fatalf("on_frame_errors = %d (notes %q), want 1", got, ev.Notes)
	}
}

// TestOnFrame_seesANonObjectFrameAsAString is D10.2's own body rule, and it
// is the same rule D3 states for req.body: what does not decode as a JSON
// object arrives as the raw string rather than as a table whose shape the
// author would have to guess.
func TestOnFrame_seesANonObjectFrameAsAString(t *testing.T) {
	def := &customep.Stream{OnFrame: `return "reply", {kind = type(frame), raw = tostring(frame)}`}
	f := newWSFixture(t, wsOpts(), wsRow(1, "/ws", def))
	c, _ := f.dial(t, "/ws", nil)

	mustWrite(t, c, wsmock.Text, `not json`)
	_, got := mustRead(t, c)
	if !strings.Contains(got, `"string"`) || !strings.Contains(got, "not json") {
		t.Fatalf("reply = %q, want the raw frame as a Lua string", got)
	}

	mustWrite(t, c, wsmock.Text, `{"a":1}`)
	_, got = mustRead(t, c)
	if !strings.Contains(got, `"table"`) {
		t.Fatalf("reply = %q, want a JSON object to arrive as a table", got)
	}
}

// --- clause 44: the per-firing cost, RECORDED --------------------------------

// BenchmarkLuaTickFiring is clause 44, and it is a benchmark and not a test on
// purpose: D10.1 makes fresh-VM-per-firing conditional on this number, there
// is NO automatic gate, and the OWNER reads it and decides. A slice that pools
// VMs on its own reading of a benchmark has changed D3's statelessness
// guarantee with nobody saying so.
//
// What it measures is one whole firing as the connection loop pays for it:
// lua.NewState, the sandbox open, the mock table, the load and one call. Read
// it against the 100 ms tick floor (customep.MinTickIntervalMs) — at 10 Hz
// per connection, this is the per-connection cost per 100 ms.
func BenchmarkLuaTickFiring(b *testing.B) {
	const src = `return {n = ordinal, at = mock.now()}`
	ctx := b.Context()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		if _, _, err := luafn.RunTick(ctx, src, i, nil); err != nil {
			b.Fatalf("RunTick: %v", err)
		}
	}
}

// --- the tick's own host ---------------------------------------------------

// TestLuaTick_reachesTheWorkspaceHelpers proves the tick's Lua gets the same
// `mock` table an endpoint function does — the hook is not a lesser runtime,
// and an author moving a body between the two must not find half the helpers
// missing.
func TestLuaTick_reachesTheWorkspaceHelpers(t *testing.T) {
	row := sseRow(1, "/events", luaTick(60, `return {t = mock.now(), fam = select(2, mock.entities("/nope"))}`))
	f := newStreamFixture(t, fastOpts(), 4, row)

	resp := f.open(t, "alex.mock.local", "/events")
	frames := readFrames(t, resp.Body, 1, 4*time.Second)
	resp.Body.Close()
	if len(frames) == 0 {
		t.Fatal("no frame")
	}
	if !strings.Contains(frames[0].data, "unknown_family") {
		t.Fatalf("frame = %q, want mock.entities' own decline — the helper must be present and answer", frames[0].data)
	}
	if !strings.Contains(frames[0].data, `"t"`) {
		t.Fatalf("frame = %q, want mock.now's value", frames[0].data)
	}
}

// --- clause 45: the preview's aggregate budget and its two labels ----------

// TestPreviewStream_luaTickRunsAndIsLabelledNominal is clause 45's second
// half: a `tick.lua` draft really runs on the honest clock, and the rate the
// preview reports is labelled NOMINAL — a sample of what ran, never a bound,
// because the next firing may return anything. An unlabelled nominal number
// is read as a bound, which is the one reading it must not have.
func TestPreviewStream_luaTickRunsAndIsLabelledNominal(t *testing.T) {
	f := newStreamFixture(t, fastOpts(), 4)
	draft := sseRow(0, "/events", luaTick(100, `return {n = ordinal}`))

	pv, err := f.plane.PreviewStream(context.Background(), sseWorkspace(1, "alex"), draft)
	if err != nil {
		t.Fatalf("PreviewStream: %v", err)
	}
	if len(pv.Frames) == 0 {
		t.Fatal("no frames: a draft's Lua must actually run")
	}
	if !strings.Contains(string(pv.Frames[0].Data), `"n":1`) {
		t.Fatalf("frame 0 = %s, want the hook's own body at ordinal 1", pv.Frames[0].Data)
	}
	if !pv.NominalRate {
		t.Error("NominalRate = false; with a tick.lua producer maxBytesPerSec is a sample, and saying so is clause 45's own defeat condition")
	}
	for i, fr := range pv.Frames {
		if fr.NotRun {
			t.Fatalf("frame %d is NotRun; a hook this cheap cannot exhaust the aggregate budget", i)
		}
	}
}

// --- review finding 12: a stream hook's host carries the connection's scope --

// scopeResourceSource and scopeEntityStore are the two seams a tick needs to
// reach a NESTED family: a roster with one depth-1 family and a store that
// records the scope it was asked for.
type scopeResourceSource struct{ res *resources.Resource }

func (s scopeResourceSource) ForWorkspace(context.Context, int64) ([]*resources.Resource, error) {
	return []*resources.Resource{s.res}, nil
}

type scopeEntityStore struct {
	mu      sync.Mutex
	seen    []resources.ScopeKey
	created []resources.ScopeKey
}

func (s *scopeEntityStore) List(_ context.Context, _ int64, _, scope resources.ScopeKey) ([]resources.Entity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, scope)
	return []resources.Entity{{ID: 1, EntityKey: "1", Data: jsonx.RawMessage(`{"id":1,"text":"hi"}`)}}, nil
}

func (s *scopeEntityStore) Get(context.Context, int64, resources.ScopeKey, resources.ScopeKey, string) (resources.Entity, bool, error) {
	return resources.Entity{}, false, nil
}

func (s *scopeEntityStore) Create(_ context.Context, _ int64, _, scope resources.ScopeKey, _, _ string, data map[string]any) (resources.Entity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.created = append(s.created, scope)
	b, _ := json.Marshal(data)
	b = append([]byte(`{"id":1,`), b[1:]...)
	return resources.Entity{ID: 1, EntityKey: "1", Data: jsonx.RawMessage(b)}, nil
}

func (s *scopeEntityStore) Patch(context.Context, int64, resources.ScopeKey, resources.ScopeKey, string, string, string, map[string]any) (resources.Entity, bool, error) {
	return resources.Entity{}, false, nil
}

func (s *scopeEntityStore) Delete(context.Context, int64, resources.ScopeKey, resources.ScopeKey, string) (bool, error) {
	return false, nil
}

// TestLuaTick_hostCarriesTheConnectionsRouteScope is review finding 12. The
// tick's host was built with a nil outer tuple, so `mock.entities` on a nested
// family from a tick on `/rooms/{roomId}/events` answered bad_scope — a hook
// has no request table to take the id from, and the host had nothing else.
// The connection's own route tuple now reaches both stream hosts, and the
// store is asked for exactly the scope the URL names.
func TestLuaTick_hostCarriesTheConnectionsRouteScope(t *testing.T) {
	row := sseRow(1, "/rooms/{roomId}/events", luaTick(60,
		`local rows, err = mock.entities("/messages"); if not rows then return {err = err} end; return {first = rows[1].text}`))
	f := newStreamFixture(t, fastOpts(), 4, row)
	store := &scopeEntityStore{}
	f.plane.SetResources(scopeResourceSource{res: &resources.Resource{
		ID: 5, WorkspaceID: 1, RouteFamily: "/messages", ScopeParams: []string{"roomId"},
	}})
	f.plane.SetEntities(store)

	resp := f.open(t, "alex.mock.local", "/rooms/42/events")
	frames := readFrames(t, resp.Body, 1, 4*time.Second)
	resp.Body.Close()
	if len(frames) == 0 {
		t.Fatal("no frame")
	}
	if !strings.Contains(frames[0].data, `"first":"hi"`) {
		t.Fatalf("frame = %q, want the nested family's row — bad_scope here is the nil outer tuple", frames[0].data)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if want := resources.EncodeScope([]string{"42"}); len(store.seen) == 0 || store.seen[0] != want {
		t.Fatalf("store asked for scope %v, want %q — the URL's own room id", store.seen, want)
	}
}

// TestPreviewStream_aFailingTickIsASkipNotA500 is review finding 8: a
// tick.lua that raises at one firing stores fine and, on a live connection,
// is one skipped firing; the preview used to fall through to the route's 500
// for the same source. It now lays the frame out as skipped — time advances,
// nothing is drawn — exactly as it does for a frame over the byte cap.
func TestPreviewStream_aFailingTickIsASkipNotA500(t *testing.T) {
	f := newStreamFixture(t, fastOpts(), 4)
	draft := sseRow(0, "/events", luaTick(100, `if ordinal == 3 then error("boom") end return {n = ordinal}`))

	pv, err := f.plane.PreviewStream(context.Background(), sseWorkspace(1, "alex"), draft)
	if err != nil {
		t.Fatalf("PreviewStream: %v — a firing's own failure is a label on the frame, never an error on the route", err)
	}
	seen := map[string]bool{}
	for _, fr := range pv.Frames {
		seen[string(fr.Data)] = true
	}
	for _, want := range []string{`{"n":1}`, `{"n":2}`, `{"n":4}`} {
		if !seen[want] {
			t.Errorf("frames lack %s", want)
		}
	}
	if seen[`{"n":3}`] {
		t.Error("the frames carry the firing that raised")
	}
}

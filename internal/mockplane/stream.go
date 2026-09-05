// stream.go is P6b's serving half (decisions.md mocker-p6b-sse-mock D4, D7,
// D9, D11, D13; DESIGN §30.2–§30.5, §30.8): a custom endpoint of kind "sse"
// answered as a Server-Sent Events stream on the mock plane, with the two
// server-driven behaviours — a scripted timeline and a generated tick — and
// nothing that reads an inbound frame (P6d's).
//
// The seam with internal/stream is the same one the admin feed uses: the
// registry admits or refuses the connection (per-workspace cap here, D9),
// Conn.Handshake runs D9's support check and writes the SSE headers, and a
// Writer puts each frame on the wire under the per-frame deadline. What is
// THIS file's is the loop — one select on the handler's own goroutine
// (§30.8: a timeline and a tick start nothing), the timeline's next-frame
// timer, the tick's ticker, the ping, the lifetime, and the request context
// — and the tick's body, which is internal/gen over the definition's inline
// schema, seeded by the workspace seed, the endpoint id and the tick
// ordinal (D4).
//
// The session layer never reaches this file: serveCustom (custom.go) has
// already applied the live effect, parked on a pause, slept the handshake
// delay and resolved a forced status before branching here, and it
// branches ONLY when no status was forced — a forced 503 answers 503 with
// no stream (§30.4), through the ordinary path.
package mockplane

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/luafn"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/stream"
	"github.com/yashok111/mocker/internal/workspaces"
)

// StreamOptions is what the mock plane's stream loop reads from
// configuration, converted to durations by cmd/mocker/main.go (D8): Ping
// and FrameTimeout are MOCKER_STREAM_PING / MOCKER_STREAM_FRAME_TIMEOUT,
// shared with the admin feed; Lifetime is MOCKER_STREAM_MAX_LIFETIME, the
// mock plane's own; TrafficFrames is MOCKER_STREAM_TRAFFIC_FRAMES ("off" |
// "first", D11).
type StreamOptions struct {
	Ping          time.Duration
	FrameTimeout  time.Duration
	Lifetime      time.Duration
	TrafficFrames string
	// TrafficMaxFrames and TrafficMaxBytes (A14) bound what ONE
	// connection's traffic row keeps each way under TrafficFramesAll.
	TrafficMaxFrames int
	TrafficMaxBytes  int64

	// P6d's three (decisions.md mocker-p6d-websocket D5), WebSocket's:
	// MOCKER_STREAM_MAX_FRAME (the inbound read limit),
	// MOCKER_STREAM_SEND_BUDGET (the reply queue's byte bound) and
	// MOCKER_STREAM_ORIGINS (already normalised to scheme://host by
	// internal/config; empty = any).
	MaxFrame   int64
	SendBudget int64
	Origins    []string
}

// TrafficFrames values (D11).
const (
	TrafficFramesOff   = "off"
	TrafficFramesFirst = "first"
	// TrafficFramesAll (A14): every frame each way, whole frames only, up
	// to TrafficMaxFrames and TrafficMaxBytes per row; the row is marked
	// truncated when either cap stops the log.
	TrafficFramesAll = "all"
)

// codeStreamingUnsupported is D9's refusal on this plane, the same code the
// admin feed answers (internal/admin/stream_handlers.go): the response
// writer cannot take a deadline or flush, and a buffered fallthrough that
// looks like a stream is the worst available outcome (§30.6).
const codeStreamingUnsupported = "streaming_unsupported"

// previewFrameLimit is how many frames PreviewStream lays out (D13).
const previewFrameLimit = 50

// SetStreams wires the mock plane's own connection registry — a
// per-workspace-capped [stream.Registry], a SEPARATE instance from the
// admin feed's so an unauthenticated plane cannot exhaust the authenticated
// one (D9) — and the loop's timings. Same startup-only contract as every
// other setter on this type; a Plane whose SetStreams was never called
// answers 503 service_unavailable on every stream handshake rather than
// serving one it cannot cap.
func (p *Plane) SetStreams(reg *stream.Registry, opts StreamOptions) {
	p.streams = reg
	p.streamOpts = opts
}

// serveStream is D7's branch: everything before it (route_off, the live
// effect, the pause, the delay, the status) already ran in serveCustom.
// outer is the connection's own route tuple (routeOuterValues on the matched
// custom row), handed to the tick's Lua host so `mock.entities` on a nested
// family resolves its scope exactly as the request/response branch does. It
// was nil until the A18 review: a tick on `/rooms/{id}/events` could not read
// `/rooms/{id}/messages` at all, since a hook has no request table to take
// the id from and the host answered bad_scope. Review finding 12.
func (p *Plane) serveStream(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, rt *runtime, row *customep.Row, base resources.ScopeKey, outer []string) {
	if p.streams == nil {
		httpx.Err(w, http.StatusServiceUnavailable, "service_unavailable", "no stream registry is wired in this deployment")
		return
	}
	def := row.Stream
	if def == nil {
		// Unreachable through the write path (validateKind pairs the kind
		// with a document) and the column's CHECK; answered rather than
		// dereferenced anyway.
		p.log.Error("sse endpoint row carries no stream document", "workspace", ws.Slug, "endpoint", row.ID)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "internal error")
		return
	}

	conn, err := p.streams.Open(r.Context(), ws.ID)
	if err != nil {
		switch {
		case errors.Is(err, stream.ErrCapExceeded):
			// Refused BEFORE the upgrade with a status that says the
			// resource is unavailable, never one implying a retry-after
			// this plane does not implement (§30.12).
			httpx.Err(w, http.StatusServiceUnavailable, "service_unavailable", "this workspace's stream connection cap is reached (MOCKER_STREAM_MAX_CONNS)")
		case errors.Is(err, stream.ErrClosed):
			httpx.Err(w, http.StatusServiceUnavailable, "service_unavailable", "the server is shutting down")
		default:
			p.log.Error("stream: open", "workspace", ws.Slug, "endpoint", row.ID, "err", err)
			httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to open the stream")
		}
		return
	}
	defer conn.Release()
	// P6c (decisions.md mocker-p6c-live-conns D2): what a listing shows for
	// this connection, recorded between Open and Handshake so the row is
	// complete from the moment it is admitted. The peer is the same string
	// the traffic row records (httpx.ResolvePeer), so the two agree.
	conn.SetInfo(stream.Info{
		EndpointID: row.ID,
		Path:       row.Path,
		Kind:       row.Kind,
		Peer:       httpx.ResolvePeer(r, p.cfg.TrustProxy).String(),
	})

	wr, err := conn.Handshake(w, p.streamOpts.FrameTimeout)
	if err != nil {
		if errors.Is(err, stream.ErrUnsupported) {
			httpx.Err(w, http.StatusNotImplemented, codeStreamingUnsupported, "this response writer cannot stream (no write deadline or flush)")
			return
		}
		p.log.Error("stream: handshake", "workspace", ws.Slug, "endpoint", row.ID, "err", err)
		return
	}
	markStream(r, customep.KindSSE)

	// §30.5: copy out what the loop needs and hold no runtime reference —
	// the row pointer and the decoded definition are what "runs to
	// completion on what it opened with" costs, never the megabytes of a
	// compiled spec. The tick's generator is built here, once per
	// connection, over the workspace settings the runtime was built with.
	loop := newStreamLoop(def, p.tickSource(rt, row, p.newLuaHost(rt, ws, base, outer, genRequestFor(row.Method, row.CanonicalPath, nil))), p.streamOpts)
	// The first frame is copied for the traffic row ONLY under "first",
	// and only up to MOCKER_TRAFFIC_MAX_BODY — the recorder would cut it
	// there anyway, and a 4 MiB frame held per connection under "off" is
	// memory the row never uses (second-reader finding, triaged as real).
	// A14: the frame log — nil, one frame, or the per-row budget — is the
	// plane's mode; SSE wire frames end in a blank line, so no separator.
	attachStreamLogs(r, p.newFrameLog(nil), nil)
	loop.hooks = streamHooks{
		onFrame: func(frame []byte) {
			noteStreamFrame(r, frame)
			conn.RecordFrame()
		},
		onPushed: func() {
			noteStreamPushed(r)
			conn.RecordPushed()
		},
		onSkip: func() {
			noteStreamSkipped(r)
			conn.RecordSkipped()
		},
		onErr: func(err error) {
			p.log.Debug("stream: tick body", "workspace", ws.Slug, "endpoint", row.ID, "err", err)
		},
	}
	// The request's own context is what ends the loop when the client
	// leaves; the registry's cancel (conn.Context, a child of it) is what
	// ends it on shutdown — passed as a channel, so the loop watches both.
	// The inbox (P6c D4) is the operator's pushes, drained as one more
	// case of the same select and written by this same goroutine.
	loop.run(r.Context(), conn.Context().Done(), conn.Inbox(), wr)
	if conn.ClosedByAdmin() {
		// P6c D5/D7: the loop exited through the cancelled case because
		// an operator closed the connection; the row says so.
		markStreamClosedByAdmin(r)
	}
}

// streamHooks is how the traffic record (D11) and the registry's live
// counters (P6c D2) learn what a loop did, without the loop knowing about
// either. onPushed fires AFTER onFrame for a pushed frame: the frame is
// counted as a frame first, then as a pushed one.
type streamHooks struct {
	onFrame  func([]byte)
	onPushed func()
	onSkip   func()
	onErr    func(error)
}

// tickSource returns the generator for one endpoint's tick frames, or nil
// when the definition has no tick. The seed is the workspace's; the
// endpoint id and the tick ordinal enter through PathParams-shaped input
// (D4) so that internal/gen's own SeedList folds them the way it folds a
// detail route's id — deterministic across connections, distinct across
// endpoints and across ticks, and no new field on gen.Request.
func (p *Plane) tickSource(rt *runtime, row *customep.Row, host luafn.Host) func(ctx context.Context, n int) ([]byte, error) {
	if row.Stream == nil || row.Stream.Tick == nil {
		return nil
	}
	if row.Stream.Tick.Lua != "" {
		// A18 D10.1. Exclusive with Schema at write time (D8b(2)), so this
		// branch and the schema one below can never both be right for one
		// document — the check is here rather than a fallthrough because a
		// row that somehow carried both (a hand-run UPDATE) must serve ONE
		// producer, and the operator's Lua is the more explicit statement.
		return newLuaTickGenerator(row.Stream.Tick.Lua, host, p.cfg.MaxResponse)
	}
	var schema map[string]any
	if err := jsonx.Unmarshal(row.Stream.Tick.Schema, &schema); err != nil || schema == nil {
		// Refused at write time (ValidateStream decodes the same bytes and
		// refuses null); a row that reaches here undecodable serves empty
		// ticks and says so in the log rather than failing the whole
		// connection — and never hands a nil schema to the generator.
		if err == nil {
			err = errors.New("stream: tick schema is null")
		}
		return func(context.Context, int) ([]byte, error) { return nil, err }
	}
	g := gen.New(nil, gen.Options{
		Seed:     rt.settings.Seed,
		ListSize: rt.settings.ListSize,
		NullRate: rt.settings.NullRate,
		MaxBytes: p.cfg.MaxResponse,
		Identity: rt.settings.Identity,
		Auth:     rt.settings.Auth,
	})
	return newTickGenerator(g, schema, row.CanonicalPath, row.ID, p.cfg.MaxResponse)
}

// newTickGenerator builds the per-tick body function over one generator
// and one decoded schema. Exposed as a function of its inputs (not a Plane
// method) so PreviewStream can build one over a DRAFT row with id 0.
func newTickGenerator(g *gen.Generator, schema map[string]any, canonicalPath string, endpointID int64, maxBytes int64) func(ctx context.Context, n int) ([]byte, error) {
	variant := gen.ResponseVariant{SchemaPtr: "#/stream/tick/schema", HTTPStatus: http.StatusOK, MediaType: "application/json"}
	// The context is accepted and unused: internal/gen walks a decoded schema
	// with no store read and no network, so there is nothing here for a
	// deadline to cut. It is in the signature because the SIBLING producer
	// (newLuaTickGenerator, below) genuinely needs one, and two closure types
	// for one ticker case would be a branch in the loop rather than in the
	// factory that already knows which producer it built.
	return func(_ context.Context, n int) ([]byte, error) {
		req := gen.Request{
			Method:        http.MethodGet,
			CanonicalPath: canonicalPath,
			PathParams: map[string]string{
				"__endpoint": strconv.FormatInt(endpointID, 10),
				"__tick":     strconv.Itoa(n),
			},
			Status:        http.StatusOK,
			PatchedSchema: schema,
		}
		body, err := g.Body(variant, req)
		if err != nil {
			return nil, err
		}
		if maxBytes > 0 && int64(len(body)) > maxBytes {
			return nil, errFrameTooLarge
		}
		return body, nil
	}
}

// previewLuaBudget is D10.1's aggregate ceiling on a stream preview's Lua:
// the whole lay-out shares it, so a hook that takes the full per-call timeout
// cannot multiply by the fifty frames previewFrameLimit allows. Past it the
// remaining frames keep their place on the axis and are labelled NotRun.
//
// A package var and not a const for exactly the reason maxStreamLifetime and
// luafn.Timeout are: this package's own test shortens it, because the
// alternative is a ten-second sleep in the suite to prove a ceiling exists —
// and a ceiling nothing observes is a number, not a guard. Nothing outside a
// test writes it.
var previewLuaBudget = 10 * time.Second

// errFrameTooLarge is a tick body over MOCKER_MAX_RESPONSE (D4): skipped
// and counted, never written.
var errFrameTooLarge = errors.New("stream: generated frame exceeds MOCKER_MAX_RESPONSE")

// errFrameBreaksFraming is A18 D10.1's own refusal, and it exists because a
// Lua tick may return a STRING: a generated body is JSON and carries no raw
// CR or LF by construction, while `return "a\nb"` would put a second `data:`
// boundary inside one frame and desynchronise every frame after it on that
// connection. Skipped and counted exactly like an oversize body — the two are
// twins, and clause 40 pins that they are counted ONCE between them.
var errFrameBreaksFraming = errors.New("stream: a tick body must not contain a CR or LF")

// errTickDeclined is the `return nil` of D10.1: not an error condition, and
// carried as one only so the single closure type can express "no frame this
// firing". [streamLoop.writeTick] tells it apart from a real error before it
// counts anything, which is acceptance clause 41: a nil return counted as
// both a skip and an error is two outcomes reported for one.
var errTickDeclined = errors.New("stream: the tick declined this firing")

// newLuaTickGenerator is D10.1's producer: the same per-firing signature the
// generated one has, so the loop cannot tell them apart, with the frame
// checks the generated body passes by construction applied here explicitly.
//
// The ordinal is the SAME number the generated body is seeded by, which is
// what makes the two producers substitutable for an author: "frame 7" means
// frame 7 either way. What does NOT carry over is P6b's guarantee that frame
// 7 is byte-identical on every connection — D4 put functions out of the
// determinism guarantee and Tick.Lua's own field comment says so.
func newLuaTickGenerator(source string, host luafn.Host, maxBytes int64) func(ctx context.Context, n int) ([]byte, error) {
	return func(ctx context.Context, n int) ([]byte, error) {
		body, send, err := luafn.RunTick(ctx, source, n, host)
		if err != nil {
			return nil, err
		}
		if !send {
			return nil, errTickDeclined
		}
		if bytes.ContainsAny(body, "\r\n") {
			return nil, errFrameBreaksFraming
		}
		if maxBytes > 0 && int64(len(body)) > maxBytes {
			return nil, errFrameTooLarge
		}
		return body, nil
	}
}

// streamLoop is the state one connection carries: the definition, where
// the timeline stands, the tick ordinal, the frame ordinal for `id:`.
type streamLoop struct {
	def   *customep.Stream
	tick  func(ctx context.Context, n int) ([]byte, error)
	opts  StreamOptions
	hooks streamHooks
	next  int   // index of the next timeline frame to write
	tickN int   // ticks written so far
	id    int64 // frames written so far == the next frame's id - 1

	timeline  *time.Timer
	timelineC <-chan time.Time

	// stopped reports whether the request context or the registry's
	// cancel is already done — checked right before every write, because
	// a select with several ready cases picks one at random and could
	// otherwise write a frame after the client left or shutdown began
	// (second-reader finding).
	stopped func() bool
}

func newStreamLoop(def *customep.Stream, tick func(ctx context.Context, n int) ([]byte, error), opts StreamOptions) *streamLoop {
	return &streamLoop{def: def, tick: tick, opts: opts}
}

// timelineDone reports whether the timeline (if any) has nothing left to
// write and does not loop.
func (l *streamLoop) timelineDone() bool {
	tl := l.def.Timeline
	return tl == nil || (l.next >= len(tl.Frames) && !tl.Loop)
}

// armTimeline re-arms the timeline's timer for the next frame with that
// frame's own delay; a nil channel (no timeline, or drained) never fires.
func (l *streamLoop) armTimeline() {
	if l.def.Timeline == nil || l.next >= len(l.def.Timeline.Frames) {
		l.timelineC = nil
		return
	}
	d := time.Duration(l.def.Timeline.Frames[l.next].DelayMs) * time.Millisecond
	if l.timeline == nil {
		l.timeline = time.NewTimer(d)
	} else {
		l.timeline.Reset(d)
	}
	l.timelineC = l.timeline.C
}

// writeTimelineFrame writes the next scripted frame and advances (looping
// if the definition says so). false means the connection is over — a
// write failure, or the timeline drained with nothing left to run.
func (l *streamLoop) writeTimelineFrame(wr *stream.Writer, tickActive bool) bool {
	if l.stopped() {
		return false
	}
	fr := l.def.Timeline.Frames[l.next]
	l.id++
	payload := stream.EncodeFrame(l.id, fr.Event, compactJSON(fr.Data))
	if err := wr.Write(payload); err != nil {
		return false
	}
	l.hooks.onFrame(payload)
	l.next++
	if l.next >= len(l.def.Timeline.Frames) && l.def.Timeline.Loop {
		l.next = 0
	}
	// A timeline-only definition with closeWhenDone closes once drained (a
	// definition with no timeline and no tick is refused at write time, so
	// "nothing to do" is not a state this loop can start in).
	if l.timelineDone() && !tickActive && l.def.ClosesWhenDone() {
		return false
	}
	l.armTimeline()
	return true
}

// writeTick generates and writes one tick frame; a body the generator
// refuses is skipped and counted, never written. false means the peer is
// gone.
func (l *streamLoop) writeTick(ctx context.Context, wr *stream.Writer) bool {
	if l.stopped() {
		return false
	}
	l.tickN++
	body, err := l.tick(ctx, l.tickN)
	switch {
	case errors.Is(err, errTickDeclined):
		// A18 D10.1 / clause 41: a Lua tick that returned nil chose not to
		// send. The connection stays open and NOTHING is counted — not a
		// skip, which means "a frame the plane refused", and not an error.
		return true
	case err != nil:
		l.hooks.onSkip()
		l.hooks.onErr(err)
		return true
	}
	l.id++
	payload := stream.EncodeFrame(l.id, l.def.Tick.Event, body)
	if err := wr.Write(payload); err != nil {
		return false
	}
	l.hooks.onFrame(payload)
	return true
}

// writePushed writes one operator-pushed frame (P6c D4) under the next
// ordinal and replies to the pusher with it. false means the connection is
// over: the pusher is told ErrConnClosed (the stop was already decided) or
// the write's own error (the peer went away under this very frame).
func (l *streamLoop) writePushed(wr *stream.Writer, req stream.PushRequest) bool {
	if l.stopped() {
		req.Reply(0, stream.ErrConnClosed)
		return false
	}
	l.id++
	payload := stream.EncodeFrame(l.id, req.Event, req.Data)
	if err := wr.Write(payload); err != nil {
		req.Reply(0, err)
		return false
	}
	l.hooks.onFrame(payload)
	l.hooks.onPushed()
	req.Reply(l.id, nil)
	return true
}

// run is the loop of D7, on the caller's goroutine: the request context
// (the client leaving), the registry's cancel (shutdown, or P6c's close by
// an operator), the lifetime, the ping, the timeline's next frame, the
// tick and — P6c D4 — the inbox of pushed frames, in one select. A nil
// inbox (an admin-feed connection, or a test that has none) never fires.
func (l *streamLoop) run(ctx context.Context, cancelled <-chan struct{}, inbox <-chan stream.PushRequest, wr *stream.Writer) {
	l.stopped = func() bool {
		if ctx.Err() != nil {
			return true
		}
		select {
		case <-cancelled:
			return true
		default:
			return false
		}
	}
	lifetime := time.NewTimer(l.opts.Lifetime)
	defer lifetime.Stop()
	ping := time.NewTicker(l.opts.Ping)
	defer ping.Stop()

	l.armTimeline()
	defer func() {
		if l.timeline != nil {
			l.timeline.Stop()
		}
	}()

	var tickC <-chan time.Time
	tickActive := l.def.Tick != nil && l.tick != nil
	if tickActive {
		t := time.NewTicker(time.Duration(l.def.Tick.IntervalMs) * time.Millisecond)
		defer t.Stop()
		tickC = t.C
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-cancelled:
			return
		case <-lifetime.C:
			return
		case <-ping.C:
			if l.stopped() {
				return
			}
			if err := wr.Write(stream.PingFrame()); err != nil {
				return
			}
		case <-l.timelineC:
			if !l.writeTimelineFrame(wr, tickActive) {
				return
			}
		case <-tickC:
			if !l.writeTick(ctx, wr) {
				return
			}
		case req := <-inbox:
			if !l.writePushed(wr, req) {
				return
			}
		}
	}
}

// compactJSON renders a stored frame payload on one line: a `data:` line
// cannot carry a line break, and an operator's PUT may have sent the
// document indented. Invalid bytes cannot reach here (ValidateStream
// checks every frame), so the fallback is the bytes as stored.
func compactJSON(raw jsonx.RawMessage) []byte {
	var v any
	if err := jsonx.Unmarshal(raw, &v); err != nil {
		return raw
	}
	b, err := jsonx.Marshal(v)
	if err != nil {
		return raw
	}
	return b
}

// PreviewStream is D13: the first frames a stream DRAFT would send, laid
// out on one time axis, with no connection, no registry slot and no write.
// The draft has already passed customep's write-time validation on the
// admin side; the tick's bodies are generated with endpoint id 0, so a
// draft's preview and a stored row's serving agree in shape and differ in
// body — the same document seeded by a different id.
func (p *Plane) PreviewStream(ctx context.Context, ws *workspaces.Workspace, draft *customep.Row) (domain.StreamPreview, error) {
	rt, err := p.buildRuntime(ctx, ws, nil)
	if err != nil {
		return domain.StreamPreview{}, err
	}
	var tick func(ctx context.Context, n int) ([]byte, error)
	nominal := false
	tickCtx := ctx
	if draft.Stream != nil && draft.Stream.Tick != nil {
		switch {
		case draft.Stream.Tick.Lua != "":
			// A18 D10.1: a draft's Lua really runs, on the honest clock,
			// with a nil host — the same refusal Preview makes for live
			// state, the ref resolver and the asset lookup, and for the same
			// reason: a draft must not read real entity rows. The AGGREGATE
			// budget is here rather than inside the cursor's own calls
			// because fifty firings at the per-call timeout is a hundred
			// seconds, and a preview route that can block for that long is
			// a denial of service an authenticated operator does not need
			// to be able to cause by accident.
			var cancel context.CancelFunc
			tickCtx, cancel = context.WithTimeout(ctx, previewLuaBudget)
			defer cancel()
			tick = newLuaTickGenerator(draft.Stream.Tick.Lua, nil, p.cfg.MaxResponse)
			nominal = true
		default:
			var schema map[string]any
			if err := jsonx.Unmarshal(draft.Stream.Tick.Schema, &schema); err != nil {
				return domain.StreamPreview{}, err
			}
			g := gen.New(nil, gen.Options{
				Seed:     rt.settings.Seed,
				ListSize: rt.settings.ListSize,
				NullRate: rt.settings.NullRate,
				MaxBytes: p.cfg.MaxResponse,
				Identity: rt.settings.Identity,
				Auth:     rt.settings.Auth,
			})
			tick = newTickGenerator(g, schema, draft.CanonicalPath, 0, p.cfg.MaxResponse)
		}
	}
	out, err := expandStream(tickCtx, draft.Kind, draft.Stream, tick, previewFrameLimit)
	if err != nil {
		return domain.StreamPreview{}, err
	}
	out.NominalRate = nominal
	return out, nil
}

// expandStream lays the timeline and the tick out on one time axis, up to
// limit frames, in the order a connection would write them (ties: the
// timeline first, as the loop's select would usually pick the timer armed
// first). Pure apart from the tick function.
func expandStream(ctx context.Context, kind string, def *customep.Stream, tick func(ctx context.Context, n int) ([]byte, error), limit int) (domain.StreamPreview, error) {
	out := domain.StreamPreview{Kind: kind, Frames: []domain.StreamPreviewFrame{}}
	if def == nil {
		return out, nil
	}
	// P6d (D12): the inbound half is reported, not laid out.
	out.Rules, out.Echo = len(def.Reactive), def.Echo
	tl := &timelineCursor{def: def.Timeline}
	tk := &tickCursor{def: def.Tick, gen: tick, ctx: ctx}
	// Bounded in STEPS, not only in frames laid out: a tick whose every body
	// exceeds the frame cap lays out nothing and would otherwise never
	// reach the frame limit (second-reader finding, triaged as real).
	for steps := 0; len(out.Frames) < limit && steps < limit*4; steps++ {
		switch {
		case tl.active() && (!tk.active() || tl.at <= tk.at):
			out.Frames = append(out.Frames, tl.next())
		case tk.active():
			fr, skipped, err := tk.next()
			if err != nil {
				return domain.StreamPreview{}, err
			}
			if !skipped {
				out.Frames = append(out.Frames, fr)
			}
		default:
			out.Truncated = false
			out.MaxBytesPerSec = max(tl.rate(), tk.rate())
			return out, nil
		}
	}
	out.Truncated = tl.active() || tk.active()
	out.MaxBytesPerSec = max(tl.rate(), tk.rate())
	return out, nil
}

// timelineCursor walks a timeline for expandStream, keeping the bytes and
// the duration it has laid out for the rate estimate.
type timelineCursor struct {
	def   *customep.Timeline
	idx   int
	at    int
	bytes int64
	ms    int64
	done  bool
	begun bool
}

func (c *timelineCursor) active() bool {
	if c.def == nil || len(c.def.Frames) == 0 {
		return false
	}
	if !c.begun {
		c.begun = true
		c.at = c.def.Frames[0].DelayMs
	}
	return !c.done
}

func (c *timelineCursor) next() domain.StreamPreviewFrame {
	fr := c.def.Frames[c.idx]
	data := compactJSON(fr.Data)
	out := domain.StreamPreviewFrame{AtMs: c.at, Event: fr.Event, Data: data}
	c.bytes += int64(len(data))
	c.ms += int64(fr.DelayMs)
	c.idx++
	if c.idx >= len(c.def.Frames) {
		if c.def.Loop {
			c.idx = 0
		} else {
			c.done = true
		}
	}
	if !c.done {
		c.at += c.def.Frames[c.idx].DelayMs
	}
	return out
}

// rate is the timeline's bytes over its own duration, at least one second
// — a burst of zero-delay frames is bounded by what was written, not by a
// division by zero.
func (c *timelineCursor) rate() int64 {
	ms := c.ms
	if ms < 1000 {
		ms = 1000
	}
	return c.bytes * 1000 / ms
}

// tickCursor walks a tick for expandStream.
type tickCursor struct {
	def *customep.Tick
	gen func(ctx context.Context, n int) ([]byte, error)
	// ctx carries the AGGREGATE Lua budget of D10.1 (previewLuaBudget), and
	// it is the cursor's rather than a parameter of next() because the
	// budget is a property of the WHOLE lay-out: fifty firings share ten
	// seconds, they do not each get them.
	ctx   context.Context
	n     int
	at    int
	first int64
	begun bool
}

func (c *tickCursor) active() bool {
	if c.def == nil || c.gen == nil {
		return false
	}
	if !c.begun {
		c.begun = true
		c.at = c.def.IntervalMs
	}
	return true
}

// next generates the next tick; skipped is true for a body the connection
// would skip too (over MOCKER_MAX_RESPONSE), which advances time and lays
// out nothing.
func (c *tickCursor) next() (domain.StreamPreviewFrame, bool, error) {
	c.n++
	at := c.at
	c.at += c.def.IntervalMs
	body, err := c.gen(c.ctx, c.n)
	if err != nil {
		switch {
		case errors.Is(err, errFrameTooLarge), errors.Is(err, errFrameBreaksFraming):
			// The connection would skip these too, and the preview says so
			// the same way: time advances, nothing is laid out.
			return domain.StreamPreviewFrame{}, true, nil
		case errors.Is(err, errTickDeclined):
			// A18 D10.1: `return nil` is a firing that sends nothing. Same
			// shape as a skip on the time axis, and it is not an error.
			return domain.StreamPreviewFrame{}, true, nil
		case errors.Is(err, luafn.ErrFailed):
			// A tick.lua that raised or returned a refused shape. On a LIVE
			// connection writeTick counts this as one skipped firing and
			// keeps the connection open, and the preview says the same
			// thing the same way — time advances, nothing is laid out. It
			// used to fall through to the route's 500, so a draft that
			// STORED fine could not be previewed. Review finding 8.
			return domain.StreamPreviewFrame{}, true, nil
		case errors.Is(err, luafn.ErrTimeout), errors.Is(err, luafn.ErrCanceled):
			// The AGGREGATE budget ran out (or the admin request went away).
			// The frame keeps its place and says it was not run — clause 45's
			// own label, and the reason the preview does not simply stop: a
			// shorter list reads as "the stream ends here", which is a
			// different and wrong statement about the definition.
			return domain.StreamPreviewFrame{AtMs: at, Event: c.def.Event, NotRun: true}, false, nil
		}
		return domain.StreamPreviewFrame{}, false, err
	}
	if c.first == 0 {
		c.first = int64(len(body))
	}
	return domain.StreamPreviewFrame{AtMs: at, Event: c.def.Event, Data: body}, false, nil
}

// rate is the tick's first body over its interval.
func (c *tickCursor) rate() int64 {
	if c.def == nil || c.gen == nil || c.def.IntervalMs <= 0 {
		return 0
	}
	return c.first * 1000 / int64(c.def.IntervalMs)
}

// traffic.go wires internal/traffic's recorder into the mock plane: every
// request that reaches Step 5 of serveResolved (routes.go) — matched to a
// spec operation, matched to a custom endpoint, or matched to nothing at
// all — becomes exactly one [traffic.Event], at almost no cost on the hot
// path DESIGN §18 protects: a write-through tee that never buffers more
// than MOCKER_TRAFFIC_MAX_BODY bytes of the response, and a Record call
// that [traffic.Recorder] itself guarantees never blocks.
//
// WHAT IS RECORDED and WHAT IS NOT is fixed by serveResolved's own call
// site (plane.go), not by this file: the reserved prefix (health,
// {prefix}/state) and a CORS preflight never reach Step 5 at all, so
// neither ever produces an event — recording POST {prefix}/state would let
// the traffic screen echo live-state directives straight back to whoever is
// watching it.
package mockplane

import (
	"bytes"
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yashok111/mocker/internal/authpreset"
	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/traffic"
	"github.com/yashok111/mocker/internal/workspaces"
	"github.com/yashok111/mocker/internal/wsmock"
)

// TrafficSink is *traffic.Recorder as the plane needs it: fire and forget —
// [traffic.Recorder.Record] never blocks and never returns an error, so
// this is the smallest interface a test can satisfy with an in-memory fake
// and no database at all, exactly like [OverrideSource] and
// [LiveStateSource] before it.
type TrafficSink interface {
	Record(ev traffic.Event)
}

// SetTraffic wires src as the Plane's [TrafficSink]. Call it once, during
// startup, right after [New] and before the Plane serves its first
// request — the identical startup-only calling contract [Plane.SetOverrides]
// and [Plane.SetLiveState] already establish (see SetOverrides' own doc
// comment for why there is no lock: p.traffic is written once and only ever
// read afterward, and calling this concurrently with ServeHTTP/ServeSlug is
// a data race go test -race correctly catches).
//
// A Plane whose SetTraffic is never called records nothing at all and costs
// nothing extra doing it — captureTraffic's own nil check skips both the
// tee and the match-context allocation entirely — so every mockplane test
// that predates this file, and every one in this package that never calls
// this method, keeps passing unmodified: the same nil-source contract every
// other P1c2 source already follows (HARD RULE 6).
func (p *Plane) SetTraffic(src TrafficSink) {
	p.traffic = src
}

// trafficMatchCtxKey/trafficMatch is the "small mutable capture struct" this
// stage's own task deliberately asks for instead of a new parameter on
// serveRoute/serveGenerated: those two functions belong to routes.go and
// respond.go, and threading a value only this file's Record call needs
// through both of their signatures would touch two functions another agent
// owns for a value written exactly once per request. Riding it on the SAME
// context [attachCapturedBody] already uses keeps every consumer of "what
// did this request carry" in one place.
type trafficMatchCtxKey struct{}

type trafficMatch struct {
	kind string // "operation" | "custom" | "none"
	id   int64

	// pauseRefused is set by markPauseRefused when a pause directive matched
	// this request but [livestate.Effect.Refused] came back true — the
	// workspace already held MaxPausedPerWorkspace parked requests (rule 7).
	// The request is served normally either way; this is the only place an
	// operator can see that it happened at all.
	pauseRefused bool

	// stream is set by markStream (stream.go) once a stream handshake
	// succeeded: the kind ("sse"). streamFrames/streamSkipped count what
	// the loop wrote and what it skipped (a generated frame over
	// MOCKER_MAX_RESPONSE); streamFirst holds the FIRST frame's wire bytes
	// for MOCKER_STREAM_TRAFFIC_FRAMES=first (P6b D11). captureTraffic reads
	// all four when the handler returns — one row per connection, its
	// duration the connection's whole life.
	stream        string
	streamFrames  int
	streamSkipped int
	// streamOut and streamIn (A14) are the frame logs the row's bodies come
	// from — nil under "off", one frame under "first", up to the per-row
	// budget under "all"; see frameLog. streamIn is WebSocket's only.
	streamOut *frameLog
	streamIn  *frameLog
	// P6c (decisions.md mocker-p6c-live-conns D7): streamPushed counts the
	// frames an operator pushed into this connection (each is ALSO one of
	// streamFrames — it carried an ordinal like every other);
	// streamClosedByAdmin is set when an operator's close ended it, so the
	// row can say so instead of looking like a client that left.
	streamPushed        int
	streamClosedByAdmin bool
	// P6d (decisions.md mocker-p6d-websocket D10): the inbound half a
	// WebSocket connection has and an SSE connection never does — inbound
	// frames counted, replies dropped under the send budget, the close
	// code the connection ended with (-1 until it ends), and the FIRST
	// inbound frame: kept only under `first`, only when it is a text frame
	// carrying a JSON object, already redacted by field name; otherwise
	// streamFirstInKind says what it was ("binary" or "text") and nothing
	// is kept.
	streamFramesIn    int
	streamDropped     int
	streamCloseCode   int
	streamFirstInKind string
	streamFirstInSeen bool

	// refUnresolved is set by markRefUnresolved (ref.go) when at least one
	// "generate"-policy "ref" recipe declined while assembling this
	// response (P3c D7) — the response still carries a plausible generated
	// value in its place, and this is the only place an operator can see
	// that the value is NOT a real resource row.
	refUnresolved bool

	// assetMissing is set by markAssetMissing (asset.go) when a pinned
	// variant's bodyRef named an asset this request could not serve (A6
	// D10): the response carried the variant's status and an EMPTY body,
	// and this note is the only place an operator sees why.
	assetMissing bool

	// function is A18's note (D7), and it is a STRING rather than a bool
	// because the branch has four outcomes an operator has to tell apart —
	// it ran, it timed out, it failed, it returned too much — and they are
	// mutually exclusive by construction: markFunctionNote is called exactly
	// once per request, on the one path that decided the answer. A bool per
	// outcome would let two of them be set at once, which no code path can
	// produce and every reader would then have to rank.
	function string
}

// notePauseRefused and noteRefUnresolved are [traffic.Event.Notes] tokens
// captureTraffic joins together (never overwrites) below. Before P3c
// pauseRefused was the mock plane's only note, so the assignment here used
// to be a bare `ev.Notes = notePauseRefused`; ref_unresolved is the second,
// and [traffic.Row.HasNote]'s whole-comma-entry match requires both to
// appear as complete, comma-separated tokens rather than one silently
// overwriting the other on a request that is both.
const (
	notePauseRefused  = "pause_refused"
	noteRefUnresolved = "ref_unresolved"
	noteAssetMissing  = "asset_missing" // A6 D10, the third; joined like the two before it

	// A18 (D6, D7): the endpoint-function branch's four outcomes. They are
	// values of ONE field rather than four flags (see trafficMatch.function),
	// and they join the list above like every other token — a function-served
	// request can also be pause_refused, and HasNote's whole-entry match needs
	// each as a complete token.
	noteFunction         = "function"
	noteFunctionTimeout  = "function_timeout"
	noteFunctionFailed   = "function_failed"
	noteFunctionTooLarge = "function_too_large"
)

// attachTrafficMatch stores a fresh, "none"-initialized [trafficMatch] on
// r's context and returns both the request to pass downstream and the
// struct itself, so captureTraffic can read back whatever serveRoute
// mutated once the whole chain below it has returned.
func attachTrafficMatch(r *http.Request) (*http.Request, *trafficMatch) {
	tm := &trafficMatch{kind: "none"}
	return r.WithContext(context.WithValue(r.Context(), trafficMatchCtxKey{}, tm)), tm
}

// markTrafficMatch records which route matched — called from routes.go's
// serveRoute the instant [router.Table.Match] succeeds, BEFORE serveGenerated
// runs any override logic (route_off, live-state, when[]): routing decided
// WHAT matched, and that decision is unaffected by whatever the override
// layer later does to the response. A route_off row still answers the
// disabled-route 404 (respond.go), but it still recorded "operation" here,
// with the real OpRowID — DESIGN's "create endpoint from observed traffic"
// flow is about routes that were never declared at all, never about ones an
// operator merely switched off, so conflating the two would hide a real
// operation match behind a "none" a reviewer would read as "unknown route".
//
// A no-op when no [TrafficSink] is wired: [attachTrafficMatch] never ran, so
// the context holds nothing to find, and routes.go can call this
// unconditionally without its own nil check.
func markTrafficMatch(r *http.Request, route *router.Route) {
	tm, ok := r.Context().Value(trafficMatchCtxKey{}).(*trafficMatch)
	if !ok {
		return
	}
	if route.Custom {
		tm.kind, tm.id = "custom", route.CustomRowID
	} else {
		tm.kind, tm.id = "operation", route.OpRowID
	}
}

// markPauseRefused records that this request's pause directive matched but
// found no free park slot ([livestate.Effect.Refused], rule 7) — called from
// respond.go/custom.go's shared resolvePause, the instant Apply reports it,
// before anything else about the request is decided.
//
// A no-op when no [TrafficSink] is wired, mirroring markTrafficMatch's own
// contract: [attachTrafficMatch] never ran, so the context holds nothing to
// find, and resolvePause can call this unconditionally without its own nil
// check.
func markPauseRefused(r *http.Request) {
	tm, ok := r.Context().Value(trafficMatchCtxKey{}).(*trafficMatch)
	if !ok {
		return
	}
	tm.pauseRefused = true
}

// markRefUnresolved records that at least one "generate"-policy "ref"
// recipe declined while this response was being assembled (P3c D7) —
// called from the resolver's own closure (ref.go), the instant it declines
// on that policy, once per response no matter how many refs inside it
// declined.
//
// A no-op when no [TrafficSink] is wired, the same contract markTrafficMatch
// and markPauseRefused already establish: [attachTrafficMatch] never ran,
// so the context holds nothing to find.
// markFunctionNote records how the endpoint-function branch ended (A18 D7) —
// called from function.go on each of its four terminal paths, and on none
// other: a client that disconnected mid-function is deliberately unclassified,
// because it is not a server error and an operator chasing it would be chasing
// a closed connection (D6).
//
// A no-op when no [TrafficSink] is wired, the same contract markTrafficMatch,
// markPauseRefused and markRefUnresolved already establish.
func markFunctionNote(r *http.Request, note string) {
	tm, ok := r.Context().Value(trafficMatchCtxKey{}).(*trafficMatch)
	if !ok {
		return
	}
	tm.function = note
}

// markStream records that a stream handshake succeeded on this request.
func markStream(r *http.Request, kind string) {
	tm, ok := r.Context().Value(trafficMatchCtxKey{}).(*trafficMatch)
	if !ok {
		return
	}
	tm.stream = kind
}

// noteStreamFrame counts one written frame and, when keepFirst, keeps the
// first one's bytes up to capBytes (MOCKER_TRAFFIC_MAX_BODY).
func noteStreamFrame(r *http.Request, frame []byte) {
	tm, ok := r.Context().Value(trafficMatchCtxKey{}).(*trafficMatch)
	if !ok {
		return
	}
	tm.streamFrames++
	tm.streamOut.add(frame)
}

// attachStreamLogs (A14) hands a connection its two frame logs, built by
// the plane from MOCKER_STREAM_TRAFFIC_FRAMES and its budget; called right
// after markStream, before the first frame either way.
func attachStreamLogs(r *http.Request, out, in *frameLog) {
	tm, ok := r.Context().Value(trafficMatchCtxKey{}).(*trafficMatch)
	if !ok {
		return
	}
	tm.streamOut, tm.streamIn = out, in
}

// frameLog (A14) is what a stream connection's traffic row keeps of its
// frames in ONE direction. Three modes, one type:
//
//   - "off": nil — every method is a no-op on a nil receiver;
//   - "first": one frame, CUT at MOCKER_TRAFFIC_MAX_BODY (the pre-A14
//     behaviour, byte for byte: a later frame is neither kept nor counted
//     as truncation — "first" promised one frame and delivered it);
//   - "all": whole frames only, up to maxFrames and maxBytes, and the
//     first frame that does not fit — by count or by size — marks the log
//     truncated and closes it. A frame is never cut in "all": half a JSON
//     object in an NDJSON body is worse than a truncated flag.
//
// This is §30.13's "own retention budget" as built: the connection stays
// ONE row (so frames cannot evict ordinary traffic rows — retention is
// per row, and the row count did not change), and the two caps bound what
// that one row can hold, so a ten-frames-a-second socket held for its
// whole lifetime cannot grow a row past MOCKER_STREAM_TRAFFIC_MAX_BYTES.
type frameLog struct {
	buf       []byte
	frames    int
	truncated bool
	maxFrames int
	maxBytes  int64
	firstOnly bool
	sep       []byte
}

// newFrameLog builds the log the plane's mode calls for; sep is written
// between frames (empty for SSE, whose wire frames end in a blank line).
func (p *Plane) newFrameLog(sep []byte) *frameLog {
	switch p.streamOpts.TrafficFrames {
	case TrafficFramesFirst:
		return &frameLog{maxFrames: 1, maxBytes: p.cfg.TrafficMaxBody, firstOnly: true, sep: sep}
	case TrafficFramesAll:
		return &frameLog{maxFrames: p.streamOpts.TrafficMaxFrames, maxBytes: p.streamOpts.TrafficMaxBytes, sep: sep}
	}
	return nil
}

func (l *frameLog) add(frame []byte) {
	if l == nil {
		return
	}
	if l.frames >= l.maxFrames {
		if !l.firstOnly {
			l.truncated = true
		}
		return
	}
	if l.firstOnly {
		take := frame
		if l.maxBytes > 0 && int64(len(take)) > l.maxBytes {
			take = take[:l.maxBytes]
		}
		l.buf = append([]byte(nil), take...)
		l.frames = 1
		return
	}
	need := len(frame)
	if l.frames > 0 {
		need += len(l.sep)
	}
	if l.maxBytes > 0 && int64(len(l.buf)+need) > l.maxBytes {
		l.truncated = true
		l.maxFrames = l.frames // closed: nothing after a refused frame is kept either
		return
	}
	if l.frames > 0 {
		l.buf = append(l.buf, l.sep...)
	}
	l.buf = append(l.buf, frame...)
	l.frames++
}

// bytes is the row's body: nil when nothing was kept.
func (l *frameLog) bytes() []byte {
	if l == nil || l.frames == 0 {
		return nil
	}
	return l.buf
}

func (l *frameLog) isTruncated() bool { return l != nil && l.truncated }

func (l *frameLog) kept() int {
	if l == nil {
		return 0
	}
	return l.frames
}

// noteStreamSkipped counts one frame the loop could not write (D4).
func noteStreamSkipped(r *http.Request) {
	tm, ok := r.Context().Value(trafficMatchCtxKey{}).(*trafficMatch)
	if !ok {
		return
	}
	tm.streamSkipped++
}

// noteStreamPushed counts one frame an operator pushed and the loop wrote
// (P6c D4, D7). The frame itself was already counted by noteStreamFrame.
func noteStreamPushed(r *http.Request) {
	tm, ok := r.Context().Value(trafficMatchCtxKey{}).(*trafficMatch)
	if !ok {
		return
	}
	tm.streamPushed++
}

// wsTrafficNotes is P6d's half of the notes (decisions.md
// mocker-p6d-websocket D10), split out of captureTraffic so that function
// stays under the cyclomatic ceiling: the inbound count, the replies the
// send budget dropped (when any), the close code the connection ended with,
// what the first inbound frame was when it was kept as nothing, and — under
// `first` — the first inbound JSON frame, already redacted, as the row's
// request body.
func (p *Plane) wsTrafficNotes(tm *trafficMatch, ev *traffic.Event, notes []string) []string {
	notes = append(notes, "frames_in:"+strconv.Itoa(tm.streamFramesIn))
	if tm.streamDropped > 0 {
		notes = append(notes, "replies_dropped:"+strconv.Itoa(tm.streamDropped))
	}
	notes = append(notes, wsCloseNote(tm.streamCloseCode))
	if tm.streamFirstInSeen && tm.streamFirstInKind != "json" {
		notes = append(notes, "first_in:"+tm.streamFirstInKind)
	}
	if body := tm.streamIn.bytes(); body != nil {
		ev.ReqBody = body
		// One object under "first"; one object per line under "all"
		// (NDJSON). Either way redaction already ran per frame.
		ev.ReqContentType = "application/json"
		if p.streamOpts.TrafficFrames == TrafficFramesAll {
			ev.ReqContentType = "application/x-ndjson"
		}
	}
	if p.streamOpts.TrafficFrames == TrafficFramesAll {
		notes = append(notes, "frames_in_recorded:"+strconv.Itoa(tm.streamIn.kept()))
	}
	return notes
}

// noteStreamFrameIn counts one inbound WebSocket frame and, when keepFirst
// and it is the first, keeps it under D10's rule: a text frame whose
// payload is a JSON object is run through traffic.RedactJSONBody — there is
// no content type to dispatch on, so the frame's own opcode and shape
// decide — and cut to capBytes; anything else keeps nothing and records its
// kind for the notes.
func noteStreamFrameIn(r *http.Request, typ wsmock.MessageType, payload []byte) {
	tm, ok := r.Context().Value(trafficMatchCtxKey{}).(*trafficMatch)
	if !ok {
		return
	}
	tm.streamFramesIn++
	if tm.streamIn == nil {
		return
	}
	// Only a TEXT frame holding a JSON object can be stored at all: it is
	// the one shape the field-name redaction handles (§30.13's first
	// collision, dispatched on opcode and shape because a frame has no
	// content type). The FIRST frame's kind is noted whatever it was, so a
	// binary or plain-text opener still shows in the notes; under "first"
	// only that first frame is ever a candidate.
	kind := "json"
	var trimmed []byte
	if typ != wsmock.Text {
		kind = "binary"
	} else {
		trimmed = bytes.TrimSpace(payload)
		if len(trimmed) == 0 || trimmed[0] != '{' || !jsonx.Valid(trimmed) {
			kind = "text"
		}
	}
	if !tm.streamFirstInSeen {
		tm.streamFirstInSeen = true
		tm.streamFirstInKind = kind
	}
	if kind != "json" || (tm.streamIn.firstOnly && tm.streamFramesIn > 1) {
		return
	}
	redacted, _ := traffic.RedactJSONBody(trimmed)
	tm.streamIn.add(redacted)
}

// noteStreamRepliesDropped adds n replies the send budget dropped (D7).
func noteStreamRepliesDropped(r *http.Request, n int) {
	tm, ok := r.Context().Value(trafficMatchCtxKey{}).(*trafficMatch)
	if !ok {
		return
	}
	tm.streamDropped += n
}

// noteStreamClose records the close code a WebSocket connection ended with
// (D8, D10) — the one the server sent, or the peer's when it closed first.
func noteStreamClose(r *http.Request, code int) {
	tm, ok := r.Context().Value(trafficMatchCtxKey{}).(*trafficMatch)
	if !ok {
		return
	}
	tm.streamCloseCode = code
}

// markStreamClosedByAdmin records that an operator's close, not the client,
// the lifetime or shutdown, ended this connection (P6c D5, D7).
func markStreamClosedByAdmin(r *http.Request) {
	tm, ok := r.Context().Value(trafficMatchCtxKey{}).(*trafficMatch)
	if !ok {
		return
	}
	tm.streamClosedByAdmin = true
}

func markRefUnresolved(r *http.Request) {
	tm, ok := r.Context().Value(trafficMatchCtxKey{}).(*trafficMatch)
	if !ok {
		return
	}
	tm.refUnresolved = true
}

// captureTraffic is Step 5's traffic-recording wrapper around
// [Plane.serveRoute]: with no sink wired it is a direct passthrough — the
// exact behavior serveResolved had before this file existed — and with one
// wired it tees the response into a capped in-memory copy, times the whole
// call, and records one [traffic.Event] once serveRoute returns.
//
// segments is passed rather than recomputed from r: serveResolved already
// has it (NormalizeSegments is not idempotent-cheap enough to redo per
// request just to save one parameter), and it is also exactly the value
// [traffic.Event.Path] and the auth-path check below both need.
func (p *Plane) captureTraffic(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, segments []string) {
	if p.traffic == nil {
		p.serveRoute(w, r, ws, segments)
		return
	}

	r, tm := attachTrafficMatch(r)
	isHead := r.Method == http.MethodHead
	tw := newTrafficWriter(w, p.cfg.TrafficMaxBody, isHead)

	start := time.Now()
	p.serveRoute(tw, r, ws, segments)
	duration := time.Since(start)

	path := joinForDisplay(segments)
	suppress := authpreset.IsAuthPath(authCheckPath(segments, ws.Settings.BasePath))

	ev := traffic.Event{
		WorkspaceID:    ws.ID,
		TS:             start,
		Method:         r.Method,
		Path:           path,
		MatchedKind:    tm.kind,
		MatchedID:      tm.id,
		Status:         tw.Status,
		Duration:       duration,
		ReqHeaders:     r.Header,
		SuppressBodies: suppress,
		Truncated:      tw.truncated,
		// Round-1 review finding 5: the SAME Content-Type header each body
		// arrived/left under, so Recorder.prepare can redact form and text
		// bodies by field name too, not JSON alone. tw.Header() is read
		// AFTER serveRoute has returned — safe: the header map itself is not
		// cleared once written, only further writes to it stop reaching the
		// wire, which this file never attempts.
		ReqContentType:  r.Header.Get("Content-Type"),
		RespContentType: tw.Header().Get("Content-Type"),
	}
	if cb := capturedBodyFromContext(r); cb != nil {
		ev.ReqBody = cb.bytes
		if cb.truncated {
			ev.Truncated = true
		}
	}
	if !isHead {
		ev.RespBody = tw.captured
	}
	// P3c: notes JOIN rather than one overwriting the other — a request can
	// be both paused-refused and ref-unresolved, and traffic.Row.HasNote's
	// whole-comma-entry match needs each as its own complete token.
	var notes []string
	if tm.pauseRefused {
		notes = append(notes, notePauseRefused)
	}
	if tm.refUnresolved {
		notes = append(notes, noteRefUnresolved)
	}
	if tm.assetMissing {
		notes = append(notes, noteAssetMissing)
	}
	if tm.function != "" {
		notes = append(notes, tm.function)
	}
	if tm.stream != "" {
		// P6b (D11): one row per connection. The writer's own capture is
		// the first MOCKER_TRAFFIC_MAX_BODY bytes of whatever the loop
		// wrote — pings included — and is NOT what the record carries:
		// "off" stores no body at all, "first" stores exactly the first
		// frame. Truncated is cleared for the same reason: a stream longer
		// than the capture is the ordinary case, not a cut body.
		notes = append(notes, "stream:"+tm.stream, "frames:"+strconv.Itoa(tm.streamFrames))
		if tm.streamSkipped > 0 {
			notes = append(notes, "frames_skipped:"+strconv.Itoa(tm.streamSkipped))
		}
		// P6c (D7): both conditional, like frames_skipped — a row for a
		// connection nobody pushed into or closed carries neither token.
		if tm.streamPushed > 0 {
			notes = append(notes, "pushed:"+strconv.Itoa(tm.streamPushed))
		}
		if tm.streamClosedByAdmin {
			notes = append(notes, "closed:admin")
		}
		// P6d (D10): the inbound half, on a WebSocket connection only.
		if tm.stream == customep.KindWS {
			notes = p.wsTrafficNotes(tm, &ev, notes)
		}
		ev.RespBody = tm.streamOut.bytes()
		// A14: truncated means a frame log hit its budget (never under
		// "first", whose one cut frame is the whole promise) — or, as
		// before, a captured request body that was cut.
		ev.Truncated = tm.streamOut.isTruncated() || tm.streamIn.isTruncated()
		if cb := capturedBodyFromContext(r); cb != nil && cb.truncated {
			ev.Truncated = true
		}
		if p.streamOpts.TrafficFrames == TrafficFramesAll {
			notes = append(notes, "frames_recorded:"+strconv.Itoa(tm.streamOut.kept()))
		}
	}
	if len(notes) > 0 {
		ev.Notes = strings.Join(notes, ",")
	}

	peer := httpx.ResolvePeer(r, p.cfg.TrustProxy)
	ev.PeerIP = peer.String()
	if peer.Trusted {
		// ResolvePeer only ever sets Trusted alongside a successfully parsed
		// Forwarded address (httpx/peer.go), so this is always valid here —
		// no separate IsValid guard needed.
		ev.FwdIP = peer.Forwarded.String()
	}

	p.traffic.Record(ev)
}

// authCheckPath renders the request's path RELATIVE to ws's own base path —
// the same shape operations.path stores, and the shape [authpreset.IsAuthPath]
// was built to match against — so a workspace whose base path itself
// contains a trigger segment (basePath "/api/auth") does not suppress every
// body in the whole workspace. That is a deliberate reading of DESIGN §15,
// not an accident: without stripping the base path first, EVERY request in
// such a workspace would carry a "auth" segment and every body would be
// suppressed, which is not what an operator setting that base path meant.
//
// segments is compared element-by-element against basePath's own segments,
// never against a rejoined string — [cutReservedPrefix]'s own doc comment
// spells out exactly why a string-prefix check on decoded, rejoined path
// segments is unsafe.
//
// The comparison is now SEGMENT-WISE with a wildcard, not plain equality
// (D12): basePath may carry a {param} segment (P3h), and a real request's
// segments never equal basePath's own literal text at that position —
// "/orgs/7/quizzes" against basePath "/orgs/{orgId}" is exactly this case.
// A base-parameter segment matches ANY single segment there; every other
// basePath segment must still match literally. Parameter positions come
// from [router.BaseParamIndexes], the ONE owner of "which segments of a
// base path are parameters" (D7.1, D12) — this function reads it rather
// than scanning basePath's braces itself, the same rule
// [resourceBranch]'s own base-scope read and stripBasePath
// (internal/admin/from_traffic.go) both hold to.
func authCheckPath(segments []string, basePath string) string {
	baseSegs := NormalizeSegments(basePath)
	if len(segments) >= len(baseSegs) && baseSegmentsMatch(segments, baseSegs, basePath) {
		return joinForDisplay(segments[len(baseSegs):])
	}
	return joinForDisplay(segments)
}

// baseSegmentsMatch reports whether segments' own leading len(baseSegs)
// elements match basePath's segments position by position, letting a base
// PARAMETER position match any single segment (D12). basePath's malformed
// shape (an unbalanced brace) can never reach here in a running workspace —
// [domain.ValidateBasePath] refuses it at write time — but a database
// written before that validator existed is read positionally regardless
// (D4.4: the validator guards, it is not the mechanism), so an invalid
// shape here degrades to plain equality rather than panicking or matching
// everything.
func baseSegmentsMatch(segments, baseSegs []string, basePath string) bool {
	idx, _, valid := router.BaseParamIndexes(basePath)
	if !valid {
		return slices.Equal(segments[:len(baseSegs)], baseSegs)
	}
	isParam := make(map[int]bool, len(idx))
	for _, i := range idx {
		isParam[i] = true
	}
	for i, seg := range baseSegs {
		if isParam[i] {
			continue
		}
		if segments[i] != seg {
			return false
		}
	}
	return true
}

// trafficWriter is the write-through TEE this stage's task describes: it
// forwards every Write to the real writer immediately, through the embedded
// [httpx.StatusRecorder] (which gives it the eventual status code and the
// REAL byte count for free — including the implicit 200 net/http writes
// when a handler never calls WriteHeader), and separately copies at most
// capBytes of what was actually written aside, for
// [traffic.Event.RespBody]. It is NOT a buffer: cfg.MaxResponse is 4 MB and
// holding that much per in-flight request is exactly the hot-path cost
// DESIGN §18 exists to remove — this keeps at most capBytes (MOCKER_TRAFFIC_
// MAX_BODY) regardless of how large the real response is.
//
// plane.go's own headWriter already sits BETWEEN this and the real
// http.ResponseWriter for a HEAD request, discarding every byte it is asked
// to write while still reporting success (Write returns len(b), nil without
// touching the connection) — so blindly copying Write's own argument here
// would record a response body that was never actually sent to the client
// (DESIGN §8: HEAD answers with no body). isHead skips the copy entirely for
// exactly that reason; the status this writer records is unaffected, since
// headWriter forwards WriteHeader untouched.
//
// Unwrap is promoted from the embedded *httpx.StatusRecorder without this
// type needing to redeclare it — http.ResponseController can still reach
// the real writer for flushing and deadlines straight through both layers.
// This type deliberately never exposes ReadFrom either, for the same reason
// it never embeds anything that has one: StatusRecorder has none, so
// io.Copy against a trafficWriter always goes through Write, never bypasses
// the cap.
type trafficWriter struct {
	*httpx.StatusRecorder
	capBytes  int64
	isHead    bool
	captured  []byte
	truncated bool
}

func newTrafficWriter(w http.ResponseWriter, capBytes int64, isHead bool) *trafficWriter {
	if capBytes < 0 {
		capBytes = 0
	}
	return &trafficWriter{
		StatusRecorder: &httpx.StatusRecorder{ResponseWriter: w, Status: http.StatusOK},
		capBytes:       capBytes,
		isHead:         isHead,
	}
}

// Write forwards to the real writer FIRST — through the embedded
// StatusRecorder, which also updates Status/Bytes on the way — and only
// THEN copies at most what remains of capBytes aside. Order matters: a tee
// that inspected or truncated b before forwarding it would risk answering
// the client something other than what it actually asked for, and this
// struct exists to add a capped side copy, never to change what reaches the
// wire.
func (t *trafficWriter) Write(b []byte) (int, error) {
	n, err := t.StatusRecorder.Write(b)
	if !t.isHead {
		if room := t.capBytes - int64(len(t.captured)); room > 0 {
			take := b
			if int64(len(take)) > room {
				take = take[:room]
			}
			t.captured = append(t.captured, take...)
		}
		if int64(t.Bytes) > t.capBytes {
			t.truncated = true
		}
	}
	return n, err
}

// reqbody.go is P1c2's one-read capture of a matched request's body:
// everything else this slice adds that needs to see the client's own
// bytes — when[]'s own in:"body" conditions, resolved from here via
// [overridesInputFor], and the traffic recorder a later stage of this same
// slice wires in — reads the SAME buffer this file fills once per request,
// never a second read of r.Body. [Plane.serveResolved] (plane.go) calls
// [captureRequestBody] exactly once, right before Step 5 (route matching),
// and attaches the result to r's context via [attachCapturedBody] so every
// consumer downstream — respond.go today, the traffic stage tomorrow —
// reads it back with [capturedBodyFromContext] instead of trying r.Body a
// second time, which the restored reader below does not make free: a
// second full read means either buffering the body twice or draining the
// live connection stream twice, and neither is "one-read" any more.
package mockplane

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/overrides"
)

// minRequestBodyCap is the floor [requestBodyCap] enforces regardless of how
// low MOCKER_TRAFFIC_MAX_BODY is dialed: an operator who shrinks it purely
// to bound what traffic PERSISTS to SQLite must not also starve the
// in-memory when[] evaluator of bytes it needs to match a real request body
// — the digest's own "cap = max(cfg.TrafficMaxBody, 64 KiB)".
const minRequestBodyCap = 64 << 10 // 64 KiB

// requestBodyReadDeadline bounds how long [captureRequestBody]'s read below
// may block waiting for bytes that never arrive.
//
// Round-1 review finding 1: ReadHeaderTimeout (cmd/mocker/main.go) bounds
// the HEADERS, but nothing bounded the BODY — a client that finished its
// headers and then simply stopped sending held this read open for as long
// as it kept the TCP connection alive, with no read deadline anywhere in
// the stack to time it out (and, before this same finding's other half was
// fixed below, a capBytes-sized buffer sat allocated for the entire hold).
// DESIGN §18 forbids RATE-limiting the mock plane; a ceiling on how long
// the server WAITS for bytes that never come is not a rate limit — it does
// not touch how fast a legitimate client may send, only how long a
// deliberately silent one gets to hold a connection open.
//
// A var, not a const, purely so a test can shrink it instead of waiting out
// 30 real seconds — the same test seam internal/traffic/recorder.go's
// writeBatchFn uses for an analogous reason.
var requestBodyReadDeadline = 30 * time.Second

// requestBodyCap resolves the cap [captureRequestBody] reads up to, from the
// live config value on every call — never cached, so a config reload is
// visible on the next request exactly like every other cfg field this
// package reads directly off *config.Config.
func requestBodyCap(trafficMaxBody int64) int64 {
	if trafficMaxBody > minRequestBodyCap {
		return trafficMaxBody
	}
	return minRequestBodyCap
}

// capturedBody is what one request's body capture leaves behind: at most
// capBytes bytes of the raw wire bytes (never more — a multi-megabyte
// upload never sits fully in memory just because a workspace happens to
// have a when[] condition somewhere), whether the real body ran past that
// cap, and the JSON decode DESIGN §8's tolerant request-body parsing
// produced, if any.
//
// The zero value — every field at its zero — is exactly "no body was ever
// captured for this request": [overridesInputFor] and every future reader
// of this struct treat it identically to "the client sent nothing".
type capturedBody struct {
	bytes     []byte // up to cap bytes, verbatim; nil when there was nothing to keep
	truncated bool   // true when the real body ran past the cap
	parsed    any    // the decoded JSON value when DESIGN §8's tolerant parse succeeded; nil otherwise
	parseOK   bool   // parsed holds a real decode, never standing in for "wasn't attempted"
}

// captureRequestBody reads r's body ONCE, restoring r.Body afterward to a
// stream that reads byte-for-byte what the client actually sent — every
// consumer downstream of Step 5 (gen.Generator today; nothing else yet)
// sees the real thing regardless of how small capBytes is.
//
// capBytes bounds how many bytes are KEPT for matching/recording; the read
// itself goes one byte past capBytes so a completely filled read buffer
// (len(consumed) == capBytes+1) is exactly how overflow is detected — never
// by comparing against Content-Length, which a client can simply lie about
// (DESIGN's own threat model already treats every request as
// attacker-controlled, and a client that WANTS its body ignored by a when[]
// condition only has to under-report its own length).
//
// GET/HEAD/DELETE requests are never read at all: DESIGN's own request
// shapes never carry a body on those methods, and every consumer of a
// capturedBody already treats "no body" as the right answer for them, so
// paying for a read whose result nothing needs buys nothing. A multipart
// body is likewise never read — DESIGN §8 says so explicitly ("multipart не
// трогаем"): a file upload is exactly the shape this cap exists to keep OUT
// of memory, not the shape when[]'s single top-level-field body predicate
// was ever meant to reach into.
//
// A non-nil error is a genuine I/O failure on the read — most notably
// httpx.MaxBody's http.MaxBytesReader tripping MOCKER_MAX_BODY, or
// [requestBodyReadDeadline] expiring on a client that stopped sending —
// since this is the first thing on the mock plane that ever actually reads
// r.Body far enough to hit either. It is returned rather than folded into
// "no body" so the caller (serveResolved) decides what that means, instead
// of this function silently deciding a real failure and an absent body are
// the same thing.
//
// w is used for exactly one thing: arming [requestBodyReadDeadline] via
// http.ResponseController before the read below, the same Unwrap contract
// [headWriter] (plane.go) already establishes in this package. A
// ResponseWriter that does not support a read deadline (httptest.NewRecorder
// in this file's own unit tests; some future wrapper) simply reads without
// one, exactly like every call site before this fix — arming the deadline is
// a defence added on top of existing behaviour, never a new way for this
// function to fail a request that would otherwise have succeeded.
func captureRequestBody(w http.ResponseWriter, r *http.Request, capBytes int64) (*capturedBody, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return &capturedBody{}, nil
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodDelete:
		return &capturedBody{}, nil
	}
	if isMultipart(r.Header.Get("Content-Type")) {
		return &capturedBody{}, nil
	}

	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(requestBodyReadDeadline))

	// io.ReadAll over a bounded io.LimitReader, not a pre-sized
	// make([]byte, capBytes+1): round-1 review finding 6 measured the old
	// make-then-io.ReadFull shape allocating the FULL cap for every
	// qualifying request regardless of the real body size (79 KiB for a
	// 1-byte POST at the 64 KiB floor, scaling linearly with whatever an
	// operator dials MOCKER_TRAFFIC_MAX_BODY to) — and that whole array rode
	// the traffic recorder's queue afterward with nothing copying a smaller
	// slice back out of it (see the copy below). io.ReadAll grows its own
	// buffer from a small start as bytes actually arrive, so a tiny or slow
	// body costs a small allocation; the LimitReader still caps the READ at
	// capBytes+1, so the overflow contract is unchanged: reading exactly
	// capBytes+1 bytes is still exactly how overflow is detected, never by
	// comparing against Content-Length.
	consumed, err := io.ReadAll(io.LimitReader(r.Body, capBytes+1))
	if err != nil {
		return nil, err
	}

	kept := consumed
	truncated := int64(len(consumed)) > capBytes
	if truncated {
		kept = consumed[:capBytes]
	}

	// Restore r.Body over EVERYTHING actually consumed (consumed, not
	// kept — the cap only bounds what THIS capture holds onto, never what a
	// downstream reader gets back) followed by whatever remains of the live
	// stream. Close must still close the ORIGINAL body: a plain
	// io.NopCloser here would silently stop propagating Close to the
	// connection this body reads from.
	r.Body = &restoredBody{
		Reader: io.MultiReader(bytes.NewReader(consumed), r.Body),
		closer: r.Body,
	}

	// Copy kept into its own right-sized slice rather than handing back a
	// sub-slice of consumed: consumed's backing array can carry spare
	// capacity left over from io.ReadAll's own growth, and cb.bytes is what
	// rides into the traffic recorder's queue (traffic.go) and stays live
	// there until the next flush — round-1 review finding 6's second half.
	// A right-sized copy means the queue only ever pins exactly what was
	// kept, never whatever slack io.ReadAll happened to allocate along the
	// way to reading it.
	// make+copy, not append(nil, kept...): append's own growth policy can
	// round a small slice's capacity up past its length (size-class
	// rounding), which would silently defeat the very "no oversized backing
	// array" guarantee this copy exists for. make([]byte, n) is what
	// actually guarantees cap == len == n.
	ownedKept := make([]byte, len(kept))
	copy(ownedKept, kept)
	cb := &capturedBody{bytes: ownedKept, truncated: truncated}
	if !truncated {
		if v, ok := tryParseBody(r.Header.Get("Content-Type"), cb.bytes); ok {
			cb.parsed, cb.parseOK = v, true
		}
	}
	return cb, nil
}

// restoredBody is the io.ReadCloser [captureRequestBody] leaves in r.Body:
// Read replays the bytes already consumed, then falls through to whatever
// is still unread on the live connection; Close is forwarded to the
// ORIGINAL body, never a no-op, so the standard library's own
// connection-draining and keep-alive bookkeeping happens exactly as it
// would have with no capture in front of it at all.
type restoredBody struct {
	io.Reader
	closer io.Closer
}

func (b *restoredBody) Close() error { return b.closer.Close() }

// isMultipart reports whether contentType names a multipart body — DESIGN
// §8's one content type this capture never reads at all; see
// [captureRequestBody]'s own doc comment for why.
func isMultipart(contentType string) bool {
	typ, _, ok := splitMediaType(contentType)
	return ok && typ == "multipart"
}

// tryParseBody implements DESIGN §8's own request-body parsing table:
// application/json, text/plain, and a request with NO Content-Type at all
// (real clients routinely send a bare JSON body with no type — DESIGN's own
// wording) are all tried as JSON; anything else is left as bytes only,
// never handed to encoding/json — an XML or form-urlencoded body that
// happens to also parse as JSON is not what an operator's when[] condition
// is asking about. multipart is filtered out by [captureRequestBody] before
// body ever reaches here.
//
// A decode failure is never surfaced as an error: DESIGN §8 is explicit
// that the mock plane itself must never answer 400 over a body it could not
// parse. [capturedBody.parseOK] false is this file's entire answer to that
// failure, decided once here rather than re-decided by every when[]
// condition that might otherwise attempt its own tolerant parse.
func tryParseBody(contentType string, body []byte) (any, bool) {
	if contentType != "" {
		typ, sub, ok := splitMediaType(contentType)
		if !ok {
			return nil, false
		}
		switch typ + "/" + sub {
		case "application/json", "text/plain":
		default:
			return nil, false
		}
	}
	var v any
	if err := jsonx.Unmarshal(body, &v); err != nil {
		return nil, false
	}
	return v, true
}

// capturedBodyCtxKey is the context key [attachCapturedBody]/
// [capturedBodyFromContext] share — an unexported, zero-size type so
// nothing outside this file can collide with it or read it by accident (the
// standard context-key-type idiom).
type capturedBodyCtxKey struct{}

// attachCapturedBody returns a copy of r carrying cb. The one production
// caller is plane.go's serveResolved, once, at Step 5, before any
// downstream code (serveGenerated's when[] evaluation; a later phase's
// traffic recorder) might want to read it back.
func attachCapturedBody(r *http.Request, cb *capturedBody) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), capturedBodyCtxKey{}, cb))
}

// capturedBodyFromContext reads back what [attachCapturedBody] stored. A nil
// result — Step 5 was never reached for this request (the reserved prefix
// never attaches one; a test that calls serveGenerated directly without
// going through serveResolved) — is treated by [overridesInputFor] exactly
// like a capturedBody that read nothing: Input.BodyOK false.
func capturedBodyFromContext(r *http.Request) *capturedBody {
	cb, _ := r.Context().Value(capturedBodyCtxKey{}).(*capturedBody)
	return cb
}

// overridesInputFor builds the [overrides.Input] SelectWhen (and, in a
// future slice, any other when[]-driven feature) evaluates candidate
// variants against. Query and header are read fresh — cheap; net/http has
// already parsed both — while Body/BodyOK come from the SAME capturedBody
// plane.go attached at Step 5, never a second read of r.Body.
func overridesInputFor(r *http.Request) overrides.Input {
	in := overrides.Input{Query: r.URL.Query(), Header: r.Header}
	if cb := capturedBodyFromContext(r); cb != nil {
		in.Body, in.BodyOK = cb.parsed, cb.parseOK
	}
	return in
}

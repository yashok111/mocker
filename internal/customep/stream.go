package customep

// stream.go is P6b's half of this package (decisions.md mocker-p6b-sse-mock
// D3, D5, D6; DESIGN §30.2, §30.3): the stream document a custom endpoint of
// kind "sse" carries, and the ONE validation both writers — the admin PUT/POST
// handlers and the MCP create/update tools — run through, because both reach
// Repo.Create/UpdateExpecting and therefore normalizeAndValidate. A limit
// stated here is refused by name at write time on both writers and never
// clamped (§30.11), the same discipline the base-path validators keep.

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/luafn"
	"github.com/yashok111/mocker/internal/overrides"
)

// Kind values of custom_endpoints.kind (0005_custom_endpoints_stream.sql).
// KindWS is named so that a caller can be refused BY NAME until P6d rather
// than with "unknown kind".
const (
	KindHTTP = "http"
	KindSSE  = "sse"
	KindWS   = "ws"
)

// The non-configurable constants of §30.11, each with its reason at its
// site rather than in a table.
const (
	// MinTickIntervalMs is the floor under a tick: ten generated bodies a
	// second per connection at roughly a millisecond per body (this
	// project's own measurement) is the amplifier §30.12 names, bounded.
	MinTickIntervalMs = 100
	// MaxTimelineFrames caps a timeline's length, validated where the row
	// is written and not where it is served.
	MaxTimelineFrames = 500
	// MaxFrameDelayMs is the same ceiling livestate.maxDelayMs gives a
	// delay directive: one frame may wait at most as long as one request.
	MaxFrameDelayMs = 30_000
	// MaxEventNameBytes bounds the SSE `event:` line; it cannot carry a
	// line break in any case (validateEventName).
	MaxEventNameBytes = 64
	// DefaultMaxFrameBytes is what a frame's payload is checked against
	// when the caller passes no limit (ReplaceAllTx, a restore): the same
	// number MOCKER_MAX_RESPONSE defaults to, so a snapshot taken under
	// the default cannot be refused by the default.
	DefaultMaxFrameBytes = 4 << 20
)

// Stream is the decoded custom_endpoints.stream document. At least one of
// Timeline/Tick is required (a stream that sends nothing is a mistake).
type Stream struct {
	Timeline *Timeline `json:"timeline,omitempty"`
	Tick     *Tick     `json:"tick,omitempty"`
	// CloseWhenDone nil reads as true: the connection closes when the
	// timeline has drained (and does not loop) and there is no tick. A
	// looping timeline, a tick, or an explicit false keeps it open,
	// pinging, until the lifetime or the client leaves (D3).
	CloseWhenDone *bool `json:"closeWhenDone,omitempty"`

	// Reactive and Echo are P6d's two inbound behaviours (decisions.md
	// mocker-p6d-websocket D2–D4), legal on kind ws only: an inbound text
	// frame whose payload is a JSON object is matched against Reactive in
	// order and the first match answers (its Data as one text frame, its
	// Close as the closing handshake, or both); an unmatched frame comes
	// back as-is when Echo is true and is consumed when it is false.
	Reactive []Rule `json:"reactive,omitempty"`
	Echo     bool   `json:"echo,omitempty"`

	// OnFrame is A18's second hook (D10.2), legal on kind ws only and
	// mutually exclusive with BOTH Reactive and Echo: present, it REPLACES
	// them entirely rather than layering over them, because two producers
	// for one inbound frame is exactly the precedence question D5 refuses to
	// document for a variant.
	//
	// The source is a Lua function body over one argument, `frame`, with the
	// verb-first return convention D10.2 fixes: nil, ("reply", data) or
	// ("close", code, reason?). It is compiled at write time like every other
	// Lua in this tree — this plane always answers, and a deferred parse is a
	// 500 nobody asked for (D8).
	OnFrame string `json:"onFrame,omitempty"`
}

// Rule is one reactive rule: when[] over the frame (body) and the
// handshake (query, header), and what to do on a match.
type Rule struct {
	When  []overrides.Condition `json:"when"`
	Data  jsonx.RawMessage      `json:"data,omitempty"`
	Close *RuleClose            `json:"close,omitempty"`
}

// RuleClose closes the connection after the rule's data (if any): Code is
// 1000 or 4000..4999, Reason at most MaxCloseReasonBytes (D3).
type RuleClose struct {
	Code   int    `json:"code"`
	Reason string `json:"reason,omitempty"`
}

// P6d's two constants, refused by name at write time like P6b's (D2, D3).
const (
	// MaxReactiveRules caps stream.reactive.
	MaxReactiveRules = 100
	// MaxCloseReasonBytes is a close frame's 125-byte payload minus the
	// two bytes of the status code.
	MaxCloseReasonBytes = 123
)

// Timeline is the scripted behaviour: frames in order, each waiting DelayMs
// before it is written, optionally looping from the first once the last is
// out.
type Timeline struct {
	Frames []Frame `json:"frames"`
	Loop   bool    `json:"loop,omitempty"`
}

// Frame is one scripted frame. Data is any JSON value, written compact on
// one `data:` line.
type Frame struct {
	DelayMs int              `json:"delayMs"`
	Event   string           `json:"event,omitempty"`
	Data    jsonx.RawMessage `json:"data"`
}

// Tick is the generated behaviour: every IntervalMs a body from
// internal/gen over Schema, deterministic from the workspace seed, the
// endpoint id and the tick ordinal (D4). Schema is an inline JSON Schema
// object; a `$ref` anywhere in it is refused at write time because there is
// no document to resolve it against.
type Tick struct {
	IntervalMs int              `json:"intervalMs"`
	Event      string           `json:"event,omitempty"`
	Schema     jsonx.RawMessage `json:"schema"`
	// Lua is A18's first hook (D10.1): the tick's producer, exclusive with
	// Schema by name. Per firing the runner calls it with the ordinal — the
	// same number the generated body is seeded by — and takes `return data`.
	//
	// CONSEQUENCE, stated where the field is: P6b's guarantee "the same body
	// at the same ordinal on every connection" does NOT hold for a Lua tick.
	// D4 put functions out of the determinism guarantee and this is where
	// that reaches a stream.
	Lua string `json:"lua,omitempty"`
}

// ClosesWhenDone reads CloseWhenDone with its nil-is-true default.
func (s *Stream) ClosesWhenDone() bool {
	return s.CloseWhenDone == nil || *s.CloseWhenDone
}

// ValidateStream checks a decoded stream document against D3/D5: at least
// one behaviour, frame count and delays within bounds, the tick interval at
// or above the floor, event names that fit an SSE line, every timeline
// payload at most maxFrameBytes, and a tick schema that is a JSON object
// carrying no `$ref`. Every refusal wraps ErrInvalidRow and names the field
// and the bound.
func ValidateStream(s *Stream, maxFrameBytes int64) error {
	return ValidateStreamFor(KindSSE, s, maxFrameBytes)
}

// ValidateStreamFor is ValidateStream with the row's kind, which decides
// the inbound half (P6d, decisions.md mocker-p6d-websocket D2): reactive
// rules and echo are legal only on kind ws — an SSE connection carries no
// inbound frame, so a rule on it would quietly never fire, the shape §30.3
// already refuses for when[]'s vocabulary — and a ws row needs at least one
// behaviour of the four, where an sse row needs one of two.
func ValidateStreamFor(kind string, s *Stream, maxFrameBytes int64) error {
	if s == nil {
		return fmt.Errorf("%w: stream is required for kind %q", ErrInvalidRow, kind)
	}
	if maxFrameBytes <= 0 {
		maxFrameBytes = DefaultMaxFrameBytes
	}
	if err := validateInbound(kind, s); err != nil {
		return err
	}
	if s.Timeline != nil {
		if err := validateTimeline(s.Timeline, maxFrameBytes); err != nil {
			return err
		}
	}
	if s.Tick != nil {
		if err := validateTick(s.Tick); err != nil {
			return err
		}
	}
	return validateReactive(s.Reactive, maxFrameBytes)
}

// validateInbound is the kind-gated half of ValidateStreamFor: which inbound
// producer a document may carry, and whether it carries any behaviour at all.
// Split out at A18 because adding the third producer (onFrame) took the
// parent over the cyclomatic bound — and the split is where it is because
// this block is the one part of that function whose every branch turns on
// the same question, "does this kind have an inbound half".
//
// A18 D8b(3): onFrame had no site at all, so both its KIND check and its two
// conflicts needed a place AND an order. The kind check goes first and BESIDE
// the existing Reactive/Echo refusals rather than after them, so a document
// that is wrong about the kind is told that before it is told anything about
// which inbound producer it may have — the same reason the ws arm refuses the
// conflicts only once the kind is known to admit an inbound half at all.
func validateInbound(kind string, s *Stream) error {
	if kind != KindWS {
		if len(s.Reactive) > 0 {
			return fmt.Errorf("%w: stream.reactive has no meaning on kind %q: an SSE connection carries no inbound frame", ErrInvalidRow, kind)
		}
		if s.Echo {
			return fmt.Errorf("%w: stream.echo has no meaning on kind %q: an SSE connection carries no inbound frame", ErrInvalidRow, kind)
		}
		if s.OnFrame != "" {
			return fmt.Errorf("%w: stream.onFrame has no meaning on kind %q: an SSE connection carries no inbound frame", ErrInvalidRow, kind)
		}
		if s.Timeline == nil && s.Tick == nil {
			return fmt.Errorf("%w: stream needs a timeline or a tick — a stream that sends nothing is a mistake", ErrInvalidRow)
		}
		return nil
	}
	if s.OnFrame != "" {
		if len(s.Reactive) > 0 {
			return fmt.Errorf("%w: stream takes onFrame or reactive, not both — onFrame replaces them entirely", ErrInvalidRow)
		}
		if s.Echo {
			return fmt.Errorf("%w: stream takes onFrame or echo, not both — onFrame replaces them entirely", ErrInvalidRow)
		}
		if err := luafn.Validate(s.OnFrame); err != nil {
			return fmt.Errorf("%w: stream.onFrame does not compile: %w", ErrInvalidRow, err)
		}
	}
	if s.Timeline == nil && s.Tick == nil && len(s.Reactive) == 0 && !s.Echo && s.OnFrame == "" {
		return fmt.Errorf("%w: stream needs a timeline, a tick, a reactive rule, echo or onFrame — a stream that neither sends nor answers is a mistake", ErrInvalidRow)
	}
	return nil
}

// validateReactive is D2/D3's rule check: the count, the conditions
// through overrides.ValidateConditions (the one predicate language), at
// least one of data/close, data as a timeline frame's data is checked, and
// a close code in the application range with a reason that fits a control
// frame.
func validateReactive(rules []Rule, maxFrameBytes int64) error {
	if len(rules) > MaxReactiveRules {
		return fmt.Errorf("%w: stream.reactive holds %d rules, the cap is %d", ErrInvalidRow, len(rules), MaxReactiveRules)
	}
	for i, r := range rules {
		if len(r.When) == 0 {
			return fmt.Errorf("%w: stream.reactive[%d].when must hold at least one condition", ErrInvalidRow, i)
		}
		if err := overrides.ValidateConditions(r.When); err != nil {
			return fmt.Errorf("%w: stream.reactive[%d].when: %w", ErrInvalidRow, i, err)
		}
		if len(r.Data) == 0 && r.Close == nil {
			return fmt.Errorf("%w: stream.reactive[%d] needs data, close or both — a rule that does nothing is a mistake", ErrInvalidRow, i)
		}
		if len(r.Data) > 0 {
			if !jsonx.Valid(r.Data) {
				return fmt.Errorf("%w: stream.reactive[%d].data is not valid JSON", ErrInvalidRow, i)
			}
			if int64(len(r.Data)) > maxFrameBytes {
				return fmt.Errorf("%w: stream.reactive[%d].data is %d bytes, the frame cap is %d (MOCKER_MAX_RESPONSE)", ErrInvalidRow, i, len(r.Data), maxFrameBytes)
			}
		}
		if r.Close != nil {
			c := r.Close.Code
			if c != 1000 && (c < 4000 || c > 4999) {
				return fmt.Errorf("%w: stream.reactive[%d].close.code %d is not 1000 or in 4000..4999 (the application range; 1xxx and 3xxx are reserved)", ErrInvalidRow, i, c)
			}
			if len(r.Close.Reason) > MaxCloseReasonBytes {
				return fmt.Errorf("%w: stream.reactive[%d].close.reason is %d bytes, the cap is %d (a close frame's payload minus the code)", ErrInvalidRow, i, len(r.Close.Reason), MaxCloseReasonBytes)
			}
		}
	}
	return nil
}

func validateTimeline(tl *Timeline, maxFrameBytes int64) error {
	n := len(tl.Frames)
	if n == 0 {
		return fmt.Errorf("%w: stream.timeline.frames must hold at least one frame", ErrInvalidRow)
	}
	if n > MaxTimelineFrames {
		return fmt.Errorf("%w: stream.timeline.frames holds %d frames, the cap is %d", ErrInvalidRow, n, MaxTimelineFrames)
	}
	for i, f := range tl.Frames {
		if f.DelayMs < 0 || f.DelayMs > MaxFrameDelayMs {
			return fmt.Errorf("%w: stream.timeline.frames[%d].delayMs %d is not in [0,%d]", ErrInvalidRow, i, f.DelayMs, MaxFrameDelayMs)
		}
		if err := validateEventName(fmt.Sprintf("stream.timeline.frames[%d].event", i), f.Event); err != nil {
			return err
		}
		if len(f.Data) == 0 {
			return fmt.Errorf("%w: stream.timeline.frames[%d].data is required (use null for an empty frame)", ErrInvalidRow, i)
		}
		if !jsonx.Valid(f.Data) {
			return fmt.Errorf("%w: stream.timeline.frames[%d].data is not valid JSON", ErrInvalidRow, i)
		}
		if int64(len(f.Data)) > maxFrameBytes {
			return fmt.Errorf("%w: stream.timeline.frames[%d].data is %d bytes, the frame cap is %d (MOCKER_MAX_RESPONSE)", ErrInvalidRow, i, len(f.Data), maxFrameBytes)
		}
	}
	return nil
}

func validateTick(t *Tick) error {
	if t.IntervalMs < MinTickIntervalMs {
		return fmt.Errorf("%w: stream.tick.intervalMs %d is below the floor of %d ms", ErrInvalidRow, t.IntervalMs, MinTickIntervalMs)
	}
	if err := validateEventName("stream.tick.event", t.Event); err != nil {
		return err
	}

	// A18 D8b(2): the ORDER is load-bearing and is why this decision was
	// written down separately from D10. The `schema is required` refusal
	// below is UNCONDITIONAL on the unchanged code, so a Lua-only tick was
	// refused as schema-missing and a lua+schema tick never reached its own
	// refusal at all. Exclusivity first, then require and validate `schema`
	// only when `lua` is absent. The two checks above this block are
	// unaffected and keep their place — they are about the interval and the
	// event name, neither of which either producer changes.
	if t.Lua != "" {
		if len(t.Schema) > 0 {
			return fmt.Errorf("%w: stream.tick takes lua or schema, not both — one producer per tick", ErrInvalidRow)
		}
		if err := luafn.Validate(t.Lua); err != nil {
			return fmt.Errorf("%w: stream.tick.lua does not compile: %w", ErrInvalidRow, err)
		}
		return nil
	}

	if len(t.Schema) == 0 {
		return fmt.Errorf("%w: stream.tick.schema is required", ErrInvalidRow)
	}
	var schema map[string]any
	if err := jsonx.Unmarshal(t.Schema, &schema); err != nil {
		return fmt.Errorf("%w: stream.tick.schema must be a JSON Schema object: %w", ErrInvalidRow, err)
	}
	if schema == nil {
		// A JSON null decodes into a nil map without an error, and a nil
		// PatchedSchema would send internal/gen to a resolver this stream
		// does not have (second-reader finding, triaged as real).
		return fmt.Errorf("%w: stream.tick.schema must be a JSON Schema object, got null", ErrInvalidRow)
	}
	if containsRef(schema) {
		return fmt.Errorf("%w: stream.tick.schema carries a $ref, and an inline schema has no document to resolve it against", ErrInvalidRow)
	}
	return nil
}

// validateEventName refuses an `event:` value an SSE line cannot carry.
func validateEventName(field, name string) error {
	if name == "" {
		return nil
	}
	if len(name) > MaxEventNameBytes {
		return fmt.Errorf("%w: %s is %d bytes, the cap is %d", ErrInvalidRow, field, len(name), MaxEventNameBytes)
	}
	if strings.ContainsAny(name, "\r\n\x00") {
		return fmt.Errorf("%w: %s must not contain a line break or NUL", ErrInvalidRow, field)
	}
	return nil
}

// containsRef walks a decoded schema for a "$ref" key at any depth.
func containsRef(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if k == "$ref" {
				return true
			}
			if containsRef(child) {
				return true
			}
		}
	case []any:
		for _, child := range t {
			if containsRef(child) {
				return true
			}
		}
	}
	return false
}

// ValidateDraft runs the SAME normalisation and validation Repo.Create and
// Repo.UpdateExpecting run, on a Row nothing will write — the preview route's
// door into the one owner of "what is a legal endpoint". It mutates row the
// way the write path would (method upper-cased, kind defaulted, an empty
// responses map). maxFrameBytes <= 0 means DefaultMaxFrameBytes.
func ValidateDraft(row *Row, maxFrameBytes int64) error {
	if row == nil {
		return fmt.Errorf("%w: nil row", ErrInvalidRow)
	}
	return normalizeAndValidate(row, maxFrameBytes)
}

// refuseFunctionOnStream is A18 D8b(1), and the ORDER is the whole of it.
//
// Both stream arms below refuse a non-empty Responses map with "kind %q takes
// no responses", and a variant's `function` lives INSIDE that map — so on the
// unchanged code a stream row carrying a function answered the generic
// refusal and D5's own `function_on_stream` could never be the answer. The
// acceptance clause that observes it would have gone red against an
// implementation nobody could call wrong, which is the false-failure
// direction §D of the gate checklist names as the worse one.
//
// It is one function and not two inlined checks because the sse and ws arms
// have drifted before: a refusal stated twice is a refusal that can disagree
// with itself.
func refuseFunctionOnStream(row *Row, kind string) error {
	for status, v := range row.Responses {
		if v.Function != "" {
			return fmt.Errorf("%w: kind %q takes no function (responses[%s]): a stream is not a request/response, and its Lua goes in stream.tick.lua or stream.onFrame instead",
				ErrInvalidRow, kind, status)
		}
	}
	return nil
}

// validateKind is the D6 half of normalizeAndValidate: the row's kind
// decides which of its ordinary fields may carry a value.
func validateKind(row *Row, maxFrameBytes int64) error {
	switch row.Kind {
	case "":
		row.Kind = KindHTTP
		fallthrough
	case KindHTTP:
		if row.Stream != nil {
			return fmt.Errorf("%w: stream is only allowed with kind %q", ErrInvalidRow, KindSSE)
		}
		return nil
	case KindSSE:
		if row.Method != http.MethodGet {
			return fmt.Errorf("%w: kind %q requires method GET (an SSE handshake is a GET), got %s", ErrInvalidRow, KindSSE, row.Method)
		}
		// BEFORE the response-map refusal below, never after it (D8b(1)).
		if err := refuseFunctionOnStream(row, KindSSE); err != nil {
			return err
		}
		if len(row.Responses) != 0 {
			return fmt.Errorf("%w: kind %q takes no responses — a response map on a stream would quietly never fire", ErrInvalidRow, KindSSE)
		}
		if row.ActiveStatus != defaultActiveStatus {
			return fmt.Errorf("%w: kind %q requires activeStatus 200 (the handshake's own status), got %d", ErrInvalidRow, KindSSE, row.ActiveStatus)
		}
		return ValidateStreamFor(KindSSE, row.Stream, maxFrameBytes)
	case KindWS:
		// P6d (decisions.md mocker-p6d-websocket D2): a ws row is STRICT
		// exactly as an sse row is — a handshake is a GET, a response map
		// on a stream would quietly never fire, the handshake's status is
		// 101 and activeStatus stays the default.
		if row.Method != http.MethodGet {
			return fmt.Errorf("%w: kind %q requires method GET (a WebSocket handshake is a GET), got %s", ErrInvalidRow, KindWS, row.Method)
		}
		// BEFORE the response-map refusal below, never after it (D8b(1)).
		if err := refuseFunctionOnStream(row, KindWS); err != nil {
			return err
		}
		if len(row.Responses) != 0 {
			return fmt.Errorf("%w: kind %q takes no responses — a response map on a stream would quietly never fire", ErrInvalidRow, KindWS)
		}
		if row.ActiveStatus != defaultActiveStatus {
			return fmt.Errorf("%w: kind %q requires activeStatus 200 (the default; the handshake answers 101 on its own), got %d", ErrInvalidRow, KindWS, row.ActiveStatus)
		}
		return ValidateStreamFor(KindWS, row.Stream, maxFrameBytes)
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidRow, row.Kind)
	}
}

// ValidatePushFrame is P6c's door into the same frame rules a timeline
// frame passes at write time (decisions.md mocker-p6c-live-conns D6): the
// event name that fits an SSE line, a payload that is present, valid JSON
// and at most maxFrameBytes. The field names in the messages are the push
// body's own (`event`, `data`) rather than a timeline index; the wording
// after the field is identical, because it is the same check. maxFrameBytes
// <= 0 means DefaultMaxFrameBytes.
func ValidatePushFrame(event string, data jsonx.RawMessage, maxFrameBytes int64) error {
	if maxFrameBytes <= 0 {
		maxFrameBytes = DefaultMaxFrameBytes
	}
	if err := validateEventName("event", event); err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("%w: data is required (use null for an empty frame)", ErrInvalidRow)
	}
	if !jsonx.Valid(data) {
		return fmt.Errorf("%w: data is not valid JSON", ErrInvalidRow)
	}
	if int64(len(data)) > maxFrameBytes {
		return fmt.Errorf("%w: data is %d bytes, the frame cap is %d (MOCKER_MAX_RESPONSE)", ErrInvalidRow, len(data), maxFrameBytes)
	}
	return nil
}

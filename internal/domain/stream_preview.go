package domain

// StreamPreview is Plane.PreviewStream's whole answer (P6b, decisions.md
// mocker-p6b-sse-mock D13): the first frames a client would receive from a
// stream draft, laid out on one time axis, plus the two numbers an author
// needs before saving — whether the preview was cut short and the
// worst-case outbound rate §30.12 wants shown. Like PreviewResult it holds
// primitives and byte slices only: the admin plane re-tags it for the wire.
type StreamPreview struct {
	Kind   string
	Frames []StreamPreviewFrame
	// Truncated is true when the stream would send more frames than the
	// preview carries — a looping timeline or a tick always does.
	Truncated bool
	// MaxBytesPerSec is an ESTIMATE of the connection's peak outbound rate:
	// the larger of the timeline's own bytes over its own duration (at
	// least one second) and the tick's first body over its interval. It is
	// what the amplifier of §30.12 amounts to for this definition.
	MaxBytesPerSec int64
	// Rules and Echo are P6d's (decisions.md mocker-p6d-websocket D12): a
	// ws draft's inbound behaviours have no time axis to be laid out on, so
	// the preview reports their count and the echo flag beside the frames.
	Rules int
	Echo  bool
	// NominalRate is A18 D10.1's label: with a `tick.lua` producer,
	// MaxBytesPerSec is a SAMPLE of what actually ran, not a bound — the
	// next firing may return anything the function feels like, where a
	// schema-generated body is bounded by the schema. An unlabelled nominal
	// number is read as a bound, which is the one reading it must not have.
	NominalRate bool
}

// StreamPreviewFrame is one frame of a StreamPreview. AtMs is the offset
// from the handshake at which the frame would be written; Data is the
// compact JSON payload exactly as the `data:` line would carry it.
type StreamPreviewFrame struct {
	AtMs  int
	Event string
	Data  []byte
	// NotRun is A18 D10.1's own label: the frame's PLACE on the time axis
	// is real, its body was never produced because the preview's aggregate
	// Lua budget ran out. Fifty firings at the two-second per-call timeout
	// is a hundred seconds, which is not a preview, so the budget is real
	// and what it costs is one honest word per frame rather than a shorter
	// list that silently claims the stream ends there.
	NotRun bool
}

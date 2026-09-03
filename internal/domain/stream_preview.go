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
}

// StreamPreviewFrame is one frame of a StreamPreview. AtMs is the offset
// from the handshake at which the frame would be written; Data is the
// compact JSON payload exactly as the `data:` line would carry it.
type StreamPreviewFrame struct {
	AtMs  int
	Event string
	Data  []byte
}

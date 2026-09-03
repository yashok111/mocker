package stream

import "strconv"

// eventName is D6's one event name: every frame this package writes is
// "event: traffic", never a second name for a differently-shaped payload —
// the poll view's own "dropped" field rides inside the same frame instead.
const eventName = "traffic"

// encodeFrame builds D6's wire shape: `id: <lastID>`, `event: traffic`,
// `data: <data>`, a blank line to terminate it. data is the caller's own
// JSON — internal/admin's trafficPollView, already marshalled — passed
// through verbatim: this package does not know that shape and does not need
// to, only that it fits on one SSE data line, which jsonx.Marshal's compact
// (no embedded newline) output always does.
func encodeFrame(lastID int64, data []byte) []byte {
	return EncodeFrame(lastID, eventName, data)
}

// EncodeFrame builds one SSE frame: `id: <id>`, `event: <event>` when event
// is non-empty (an empty event name means the browser's default "message"
// event, so the line is omitted rather than written empty), `data: <data>`
// and the blank line. data must contain no line break — a compact JSON
// encoding never does — because the SSE parser would read the remainder
// as a new field. The mock plane's stream loop (P6b) is the second caller,
// with the operator's own event names.
func EncodeFrame(id int64, event string, data []byte) []byte {
	// One allocation sized for the common case (id line + event line + the
	// "data: "/"\n\n" framing + the payload itself) rather than growing a
	// buffer through repeated appends on the hot path a busy workspace
	// exercises every recorder tick.
	const fixedOverhead = len("id: \nevent: \ndata: \n\n")
	buf := make([]byte, 0, fixedOverhead+20+len(event)+len(data)) // +20: room for a 64-bit id
	buf = append(buf, "id: "...)
	buf = strconv.AppendInt(buf, id, 10)
	if event != "" {
		buf = append(buf, "\nevent: "...)
		buf = append(buf, event...)
	}
	buf = append(buf, "\ndata: "...)
	buf = append(buf, data...)
	buf = append(buf, "\n\n"...)
	return buf
}

// pingFrame is the SSE comment frame D12's MOCKER_STREAM_PING interval
// sends: a line beginning with ":" is a comment per the SSE spec, ignored by
// EventSource's own parser, so it carries no id and fires no "message" event
// — its only job is keeping an idle connection alive through a proxy and
// giving a stalled peer's write deadline (D9/D12) something to trip on.
var pingFrame = []byte(": ping\n\n")

// PingFrame is pingFrame for the mock plane's own loop.
func PingFrame() []byte { return pingFrame }

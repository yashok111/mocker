// Package traffic records what the mock plane answered, cheaply enough that
// an e2e run does not notice (DESIGN §18). It owns three things and nothing
// else: redacting a request/response pair before it is ever buffered
// (DESIGN §15), batching inserts into as few write transactions as it can,
// and answering the admin API's read queries against the traffic table.
//
// This package is a LEAF with respect to the rest of mocker's request path:
// it imports internal/store, net/http and the stdlib ONLY — never
// internal/mockplane, internal/authpreset or internal/workspaces. The caller
// decides WHAT to record (which operation matched, whether this is an auth
// path whose bodies must never be stored); this package decides HOW to
// redact, batch and store it. Keeping that boundary one-directional is what
// lets the mock plane and the recipes/authpreset stack change shape without
// ever touching this package.
package traffic

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Event is one request as the plane observed it. Bodies arrive already cut to
// whatever the plane captured (its own, larger cap); [Recorder.Record] cuts
// them again to Options.MaxBody and redacts them BEFORE anything is
// buffered (DESIGN §15) — never after, and never in the writer goroutine,
// because "before the buffer" is the property that keeps a secret from ever
// sitting in the queue in the clear, even for the width of one flush cycle.
type Event struct {
	WorkspaceID int64
	// TS is the moment the plane observed the request. It is stored in the
	// traffic.ts column as EPOCH SECONDS (int64 Unix time) — the same unit
	// every other timestamp column in this schema uses (workspaces.updated_at,
	// checkpoints.created_at, both written via unixepoch()/time.Unix()), so a
	// reader who already knows this database's timestamp convention does not
	// have to relearn it for one table. Insert, [Repo.Since] and [Repo.Rate1m]
	// all read/write this column via .Unix() / time.Unix(sec, 0) — never
	// UnixMilli.
	TS     time.Time
	Method string
	Path   string // as requested, query stripped, WITH the workspace base path

	PeerIP string // httpx.Peer's immediate peer, always recorded (§15)
	FwdIP  string // the forwarded client, empty unless MOCKER_TRUST_PROXY says otherwise

	MatchedKind string // "operation" | "custom" | "none"
	MatchedID   int64  // operations.id, custom_endpoints.id, or 0 for "none"

	Status   int
	Duration time.Duration

	ReqHeaders http.Header
	ReqBody    []byte
	RespBody   []byte

	// ReqContentType/RespContentType are the SAME Content-Type header the
	// caller already has (the request's own; the response's, once
	// serveRoute has finished writing) — read here rather than pulled back
	// out of ReqHeaders/a header map [Recorder] does not otherwise keep, so
	// [Recorder.prepare] can pick the right [RedactBody] branch (round-1
	// review finding 5: JSON/form/text all redact by field name, not JSON
	// alone) without this leaf package reaching back into the request/
	// response objects that produced ev in the first place. Neither is
	// stored: the frozen traffic table (HARD RULE 3) has no column for
	// either, and both are consumed once, by prepare, before qe is built.
	ReqContentType  string
	RespContentType string

	// SuppressBodies is set by the caller for an auth path: neither body is
	// stored at all, not even redacted — a real password and the token it
	// mints must never sit in this table for anyone who knows the shared
	// admin password to read (§15).
	SuppressBodies bool
	// Truncated records that the CALLER already cut one of the bodies before
	// this Event was built (the plane's own, larger cap). It does not say
	// WHICH body, so it folds into the stored [Row.Truncated] column (that
	// column is "at least one body was cut", exactly what this flag means)
	// but it does not by itself set NoteTruncatedReq or NoteTruncatedRsp —
	// only Record's own MaxBody cut can attribute a cut to one side, and
	// those two tokens exist so a reader can tell which.
	Truncated bool
	// Notes is the caller's free text, appended AFTER the recorder's own
	// pinned tokens (NoteRedacted, NoteSuppressed, ...) — see the Notes
	// doc comment on Row for why the two must never be merged into one
	// unstructured string a caller could accidentally shadow.
	Notes string
}

// Options configures a [Recorder]. Every field's zero (or negative, for the
// numeric ones) value falls back to the matching Default* constant, so a
// caller that only cares about overriding one knob can leave the rest at
// Options{}.
type Options struct {
	MaxBody    int64         // cfg.TrafficMaxBody; <=0 -> DefaultMaxBody
	Retention  int           // cfg.TrafficRetention; <=0 -> DefaultRetention
	Queue      int           // <=0 -> DefaultQueue
	Batch      int           // <=0 -> DefaultBatch
	FlushEvery time.Duration // <=0 -> DefaultFlushEvery
}

// The defaults, pinned rather than left to taste: DefaultFlushEvery in
// particular must stay at or under one second because scripts/smoke.sh polls
// for traffic from a shell, where Flush is not reachable, and its bounded
// retry ceiling is written against this number.
const (
	DefaultQueue      = 1024
	DefaultBatch      = 64
	DefaultFlushEvery = 500 * time.Millisecond
	DefaultMaxBody    = 8 * 1024 // 8 KiB
	DefaultRetention  = 1000
)

// Row is one stored request, as the admin API hands it out.
type Row struct {
	ID          int64             `json:"id"`
	TS          time.Time         `json:"ts"`
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	PeerIP      string            `json:"peerIp,omitempty"`
	FwdIP       string            `json:"fwdIp,omitempty"`
	MatchedKind string            `json:"matchedKind"`
	MatchedID   *int64            `json:"matchedId,omitempty"`
	Status      int               `json:"status"`
	DurationMS  float64           `json:"durationMs"`
	ReqHeaders  map[string]string `json:"reqHeaders,omitempty"`
	ReqBody     string            `json:"reqBody,omitempty"`
	RespBody    string            `json:"respBody,omitempty"`
	Notes       string            `json:"notes,omitempty"`
	Truncated   bool              `json:"truncated"`
}

// truncated is ONE column for TWO bodies (migrations/0001_init.sql, frozen —
// HARD RULE 3). It means "at least one body was cut", and Notes says which.
//
// NOTES IS A PINNED TOKEN SET, not free text, because a reader one package
// away has to act on it: the admin to-override conversion must REFUSE a row
// whose body was cut or redacted, and the frozen schema has no column for
// either. Notes is a comma-separated list composed EXCLUSIVELY by the
// recorder — the recorder's own tokens first, then any caller free text from
// Event.Notes appended after, never merged into the recorder's tokens. A
// caller cannot forge or suppress a token by choosing its own Notes string.
const (
	NoteRedacted     = "redacted" // a secret was replaced in at least one body
	NoteTruncatedReq = "truncated:req"
	NoteTruncatedRsp = "truncated:resp"
	NoteTruncatedHdr = "truncated:headers" // the header set exceeded the body cap and was dropped
	NoteSuppressed   = "suppressed"        // an auth path: no body stored at all
	// NoteDroppedPrefix is followed by a decimal count, e.g. "dropped:12".
	NoteDroppedPrefix = "dropped:"
)

// HasNote reports whether token appears in r.Notes as a whole comma-separated
// entry — never a substring scan, since NoteTruncatedReq ("truncated:req")
// is itself a prefix of nothing else here but a substring check would still
// be the wrong tool: a caller's free-text Notes could otherwise forge a
// token's presence by embedding it inside an unrelated sentence.
func (r Row) HasNote(token string) bool {
	for _, t := range strings.Split(r.Notes, ",") {
		if t == token {
			return true
		}
	}
	return false
}

// Redacted reports whether this row's stored bodies were altered (a secret
// replaced) or withheld entirely (an auth path). The admin to-override
// conversion refuses any row for which this is true — a body that either
// carries [RedactedValue] in place of the real secret or was never stored at
// all cannot become a pinned response.
func (r Row) Redacted() bool { return r.HasNote(NoteRedacted) || r.HasNote(NoteSuppressed) }

// DroppedBefore reports how many events were dropped (queue full) just
// before this row was recorded, or 0 when there is no dropped: token.
func (r Row) DroppedBefore() int {
	for _, t := range strings.Split(r.Notes, ",") {
		if n, ok := strings.CutPrefix(t, NoteDroppedPrefix); ok {
			if v, err := strconv.Atoi(n); err == nil && v >= 0 {
				return v
			}
		}
	}
	return 0
}

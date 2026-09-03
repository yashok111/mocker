package checkpoints

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
)

// maxSnapshotBytes is C18's ONE ceiling on the DECODED document, applied at
// BOTH ends: a capture whose encoded snapshot exceeds it is refused before
// the row is inserted, and a stored blob whose decompressed size exceeds it
// is refused before [bundle.Decode] sees anything.
//
// 8 MiB is chosen against the real shape of a snapshot rather than derived:
// a workspace's overrides are bounded by its spec's operation count (the
// customer's real spec has 130) and each pinned body is already capped at
// overrides' own maxPinnedBodyBytes = 4 << 20, but NOTHING caps a
// workspace's TOTAL — so the number is deliberately generous. The write
// half is what stops the ceiling from being a trap: every existing cap in
// this tree (maxSettingsJSON 64 KiB, maxPinnedBodyBytes, MOCKER_MAX_BODY)
// is write-side and none bounds a workspace's total, so a read-only ceiling
// could make a checkpoint THIS VERY BUILD wrote permanently unreadable.
//
// It is a var and not a const so a test can LOWER it: §G obs 20 has to
// materialise a document that exceeds the ceiling, and building an 8 MiB
// fixture under -race in a memory-capped scope is how this box's OOM killer
// gets invoked (it has twice wiped the whole user slice). A var written by
// a test is a data race unless that test is serial, and every bar here runs
// -race — so NO test in this package calls t.Parallel; see main_test.go.
var maxSnapshotBytes = 8 << 20

// gzipMagic is the two-byte header every gzip stream starts with. Named
// here because §G obs 20 asserts a stored config_snap begins with it — the
// check that distinguishes "gzipped" from "raw JSON in a BLOB column".
var gzipMagic = []byte{0x1f, 0x8b}

// maxDataProbeBytes bounds what [captureEntitiesTx]'s four-term probe (D5.2
// of the P3d decision document) is willing to let through to an actual
// entity read and encode. It is a package var, beside [maxSnapshotBytes]
// and for the identical reason: a test lowers it rather than materialising
// a multi-megabyte fixture under -race in a memory-capped scope.
//
// It is 6 MiB — three quarters of [maxSnapshotBytes]'s 8 MiB ceiling — and
// the two bound DIFFERENT things: maxDataProbeBytes bounds what is
// ALLOCATED (an estimate, computed in SQL before a single entity row is
// read), maxSnapshotBytes bounds what is STORED (the actual encoded
// document, checked by [compressSnapshot] after the fact). The quarter of
// headroom is deliberate: the probe's estimate is generous in the SAFE
// direction (it may overcount, never undercount), so a document that
// passes the probe and then fails the write-side ceiling is an ordinary,
// expected degrade rather than a bug in the estimate.
var maxDataProbeBytes = 6 << 20

// The three envelope constants [captureEntitiesTx]'s probe adds to the
// measured payload before comparing against [maxDataProbeBytes]. Named
// HERE, beside the budget they feed, rather than left for a fleet to
// invent: D5.2 measured each against the real encoded shape of
// [bundle.EntityRow] and [bundle.FamilyEntry] and picked deliberately
// generous numbers, and acceptance property 7 needs a fixture landing
// inside the exact band these three numbers define — a test that moved the
// budget without moving these would silently widen or narrow that band.
const (
	// dataProbeRowEnvelopeBytes estimates one EntityRow's JSON overhead
	// beyond its own scopeKey/entityKey/data bytes: five keys, their
	// quoting, two integer timestamps and the separators.
	dataProbeRowEnvelopeBytes = 96
	// dataProbeFamilyEnvelopeBytes estimates one FamilyEntry's own JSON
	// overhead beyond its routeFamily bytes (measured separately, since
	// route_family has no length bound in the schema) and its rows.
	dataProbeFamilyEnvelopeBytes = 64
	// dataProbeDocumentEnvelopeBytes estimates the DataBundle envelope
	// itself: the mockerData field and the families array's own brackets.
	dataProbeDocumentEnvelopeBytes = 64
)

// capturePreWriteHook is a test-only seam, modelled on
// internal/resources' confirmPreWriteHook and resetPreWriteHook: called
// once, right after the pre-transaction [Repo.captureSnapshot] returns and
// right before the caller's [store.DB.Write] opens — the exact window D5.1
// of the P3d decision document exists to close. A test sets this to create
// an entity row in that window and restores it to
// capturePreWriteHookNoop afterward, proving the checkpoint's data half —
// read INSIDE the write transaction by [captureEntitiesTx], never on the
// reader pool — actually observes a row created after the config half was
// already read. Production never touches it.
var capturePreWriteHook = capturePreWriteHookNoop

func capturePreWriteHookNoop() {}

// compressSnapshot is C18's write end: refuse over the ceiling FIRST, then
// gzip. The refusal happens here, in the pure codec, rather than at the
// call site, so it is impossible to reach the INSERT with an oversized
// document by adding a second caller later.
//
// Since P3d there ARE two callers, and they take OPPOSITE policies on the
// [ErrSnapshotTooLarge] this function returns — a fact this comment must
// state because the function itself cannot enforce it. [Repo.captureSnapshot]
// (config_snap) PROPAGATES the error: a workspace whose configuration
// exceeds the ceiling loses checkpointing entirely, which is the honest
// cost the ceiling comment above already accepts. [captureEntitiesTx]
// (data_snap) SWALLOWS it and degrades instead: entity rows are created by
// an unauthenticated POST X on the mock plane, so propagating here would
// let any anonymous caller permanently break a workspace's checkpointing —
// the exact denial of service the ceiling's own honest-cost paragraph
// warns against, arriving from a new, unauthenticated direction. A reader
// extending this codec must choose deliberately, not copy the nearer
// caller.
func compressSnapshot(doc []byte) ([]byte, error) {
	if len(doc) > maxSnapshotBytes {
		return nil, fmt.Errorf("%w: snapshot is %d bytes (max %d)", ErrSnapshotTooLarge, len(doc), maxSnapshotBytes)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(doc); err != nil {
		return nil, fmt.Errorf("compress snapshot: %w", err)
	}
	// Close flushes the trailer; a deferred close would drop that error and
	// store a truncated stream that only fails on the way back out.
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("compress snapshot: %w", err)
	}
	return buf.Bytes(), nil
}

// decompressSnapshot is C18's read end: gunzip at most maxSnapshotBytes+1
// bytes and refuse when what came out exceeds maxSnapshotBytes, BEFORE
// anything is handed to [bundle.Decode].
//
// The extra byte is not a rounding detail. A bare io.LimitReader at exactly
// the limit TRUNCATES silently, and a truncation that lands on trailing
// whitespace still parses as valid JSON — so the document would decode,
// short, with no error anywhere. Reading one byte past the ceiling is what
// makes the overflow visible at all; §G obs 20 drives exactly that case,
// with a document that exceeds the ceiling by one whitespace byte.
func decompressSnapshot(blob []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCorruptSnapshot, err)
	}
	defer func() { _ = zr.Close() }()

	doc, err := io.ReadAll(io.LimitReader(zr, int64(maxSnapshotBytes)+1))
	if err != nil {
		// A stream whose CRC or length trailer disagrees with its payload
		// fails HERE, on the read that reaches the end of it — gzip.Reader
		// verifies the trailer during Read, not in Close.
		return nil, fmt.Errorf("%w: %w", ErrCorruptSnapshot, err)
	}
	if len(doc) > maxSnapshotBytes {
		return nil, fmt.Errorf("%w: stored snapshot decompresses to more than %d bytes", ErrSnapshotTooLarge, maxSnapshotBytes)
	}
	return doc, nil
}

// endpointEntryFromRow converts one custom_endpoints row into the bundle's
// wire shape. It is [bundle.NewOverrideEntry]'s opposite number for the
// other table, and lives HERE rather than in internal/bundle because that
// package deliberately imports no database code (C3): building this from a
// *customep.Row would drag internal/customep — and through it
// internal/router and internal/store — into a package whose whole point is
// that it cannot open a connection.
//
// Six things are NOT carried, and each is a decision rather than an
// omission: ID, WorkspaceID, CreatedAt and UpdatedAt are DB bookkeeping
// that means nothing once a row is lifted into a portable snapshot;
// CanonicalPath is derived from Path by router.CanonicalPath — the exact
// authority the UNIQUE (workspace_id, method, canonical_path) index depends
// on, so a snapshot carrying its own copy would invent a SECOND one; and
// ResourceID is P3's and is not even a customep.Row field, only a column.
func endpointEntryFromRow(row *customep.Row) bundle.EndpointEntry {
	return bundle.EndpointEntry{
		Method:        row.Method,
		Path:          row.Path,
		OverrideOn:    row.OverrideOn,
		RouteOff:      row.RouteOff,
		ActiveStatus:  row.ActiveStatus,
		Responses:     row.Responses,
		ReqSchema:     row.ReqSchema,
		ListSize:      row.ListSize,
		DelayMs:       row.DelayMs,
		FailDirective: row.FailDirective,
		ValidateReq:   row.ValidateReq,
		// SourceOrder is carried, unlike every other bookkeeping field
		// above, because a restore must write it back VERBATIM: reassigning
		// it the way customep.Repo.Create does (max(source_order)+1) would
		// silently reorder every custom endpoint relative to the others the
		// moment more than one comes back through the same snapshot.
		SourceOrder: row.SourceOrder,
		// P6b (D12): kind always, stream null for an http row — the v4
		// half of this entry.
		Kind:   kindOrHTTP(row.Kind),
		Stream: streamJSON(row.Stream),
		// P7a (D9): the operation document, the v5 half of this entry.
		Operation: operationJSON(row.Operation),
	}
}

// operationFromJSON is operationJSON's inverse: an absent or null
// document is a nil Operation (every v4 snapshot).
func operationFromJSON(raw jsonx.RawMessage) (*customep.Operation, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil //nolint:nilnil // "the snapshot declares no operation" is the answer, and the caller stores it as a NULL column
	}
	var op customep.Operation
	if err := jsonx.Unmarshal(raw, &op); err != nil {
		return nil, err
	}
	return &op, nil
}

// operationJSON encodes a row's operation document for the snapshot, an
// EMPTY RawMessage for a row that declares none — never the literal
// "null", so a v4 document and a v5 document over the same row are the
// same bytes and `omitempty` can drop the key.
func operationJSON(op *customep.Operation) jsonx.RawMessage {
	if op == nil {
		return nil
	}
	b, err := jsonx.Marshal(op)
	if err != nil {
		// customep.Operation is a struct of strings, bools and raw JSON
		// the write path already validated; a marshal failure here is a
		// broken build, and dropping the field silently would lose an
		// operator's work on a restore.
		panic("checkpoints: marshal endpoint operation: " + err.Error())
	}
	return b
}

// kindOrHTTP applies custom_endpoints.kind's own DEFAULT to a Row that was
// built without one, so a snapshot never carries an empty kind.
func kindOrHTTP(kind string) string {
	if kind == "" {
		return customep.KindHTTP
	}
	return kind
}

// streamJSON encodes a stream document for the snapshot; nil marshals as
// the JSON null EndpointEntry.Stream's own comment requires. A marshal
// error is impossible for a *customep.Stream (plain fields and
// RawMessages) and is reported as null rather than aborting a capture —
// the row's kind still says what it was.
func streamJSON(s *customep.Stream) jsonx.RawMessage {
	if s == nil {
		return nil
	}
	b, err := jsonx.Marshal(s)
	if err != nil {
		return nil
	}
	return b
}

// streamFromJSON is streamJSON's inverse for a restore; a null or empty
// value is nil, and a document that does not decode is reported as an
// error because a stream row without its document cannot be served.
func streamFromJSON(raw jsonx.RawMessage) (*customep.Stream, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var st customep.Stream
	if err := jsonx.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// overrideRowsFromBundle rebuilds op_overrides rows out of a decoded
// snapshot. C13's fourth accepted duplication: its unexported twin is
// mockplane/scenario.go's rowFromEntry (:237-250), whose own doc (:215-219)
// says it lives there "rather than in internal/bundle because it is this
// package's need" — the same reasoning, a second time, for a package that
// needs the inverse direction for a different table write.
//
// The DB-only fields (ID, WorkspaceID, OperationID, UpdatedAt) stay at
// their zero values on purpose. [overrides.ReplaceAllTx] sets WorkspaceID
// itself (C4 duty 2) and stamps UpdatedAt; ID is resolved by the ON
// CONFLICT (workspace_id, method, path) target, which is exactly what keeps
// a row present on both sides of a rollback on the id the UI is already
// holding (C1); and OperationID is a cache column no production code
// writes.
func overrideRowsFromBundle(b bundle.Bundle) []*overrides.Row {
	rows := make([]*overrides.Row, 0, len(b.Overrides))
	for _, e := range b.Overrides {
		rows = append(rows, &overrides.Row{
			Method:        e.Method,
			Path:          e.Path,
			OverrideOn:    e.OverrideOn,
			RouteOff:      e.RouteOff,
			ActiveStatus:  e.ActiveStatus,
			Responses:     e.Responses,
			ListSize:      e.ListSize,
			DelayMs:       e.DelayMs,
			FailDirective: e.FailDirective,
			ValidateReq:   e.ValidateReq,
		})
	}
	return rows
}

// endpointRowsFromBundle rebuilds custom_endpoints rows out of a decoded
// snapshot — [endpointEntryFromRow]'s inverse.
//
// CanonicalPath is deliberately left EMPTY: [customep.ReplaceAllTx] derives
// it from the normalized Path via router.CanonicalPath inside the write
// loop (C4 duty 6), and that is the only place in the tree that computes it
// for a restore. Filling it in here would be the second authority C3 exists
// to prevent — and a wrong one, since the Path is not upper-cased or
// validated yet at this point.
func endpointRowsFromBundle(b bundle.Bundle) ([]*customep.Row, error) {
	return EndpointRowsFromBundle(b)
}

// EndpointRowsFromBundle is endpointRowsFromBundle for the admin plane's
// D6 check (decisions.md mocker-p7-api-design): an import and a rollback
// must refuse, BEFORE their transaction, a document whose endpoint rows
// carry a `$ref` the spec the workspace will hold afterwards cannot
// resolve — and the only decoder of those rows is this one. Exported as a
// separate name so the private one keeps its restore-only comment above
// and a reader of the apply path is not sent looking for a second caller.
func EndpointRowsFromBundle(b bundle.Bundle) ([]*customep.Row, error) {
	rows := make([]*customep.Row, 0, len(b.Endpoints))
	for _, e := range b.Endpoints {
		st, err := streamFromJSON(e.Stream)
		if err != nil {
			return nil, fmt.Errorf("endpoint %s %s: decode stream: %w", e.Method, e.Path, err)
		}
		// P7a (D9): absent on every v4 document, which is what a nil
		// Operation on the row means — the column stays NULL.
		op, oerr := operationFromJSON(e.Operation)
		if oerr != nil {
			return nil, fmt.Errorf("endpoint %s %s: decode operation: %w", e.Method, e.Path, oerr)
		}
		rows = append(rows, &customep.Row{
			Method:        e.Method,
			Path:          e.Path,
			SourceOrder:   e.SourceOrder,
			OverrideOn:    e.OverrideOn,
			RouteOff:      e.RouteOff,
			ActiveStatus:  e.ActiveStatus,
			Responses:     e.Responses,
			ReqSchema:     e.ReqSchema,
			ListSize:      e.ListSize,
			DelayMs:       e.DelayMs,
			FailDirective: e.FailDirective,
			ValidateReq:   e.ValidateReq,
			Kind:          e.Kind,
			Stream:        st,
			Operation:     op,
		})
	}
	return rows, nil
}

// Package customep owns the custom_endpoints table (DESIGN §8, §13): routes
// an operator defines from nothing rather than importing from a spec. It is
// the storage half only — matching a request against a custom route and
// serving its response is internal/mockplane's job (P2), and the admin API
// that decides whether a custom endpoint conflicts with an op_overrides row
// on the same (method, path) lives in internal/admin, the one layer that
// holds both repos.
//
// This package imports internal/store (the table itself), internal/router
// (CanonicalPath — the same computation the spec indexer uses, so a custom
// route and a spec operation can never disagree about what a canonical path
// is) and internal/overrides for the Variant type: responses[status] here is
// the IDENTICAL wire shape op_overrides.responses already defines, decoded
// and validated through overrides.ValidateResponses/ValidateVariant rather
// than a second implementation of the same JSON. The dependency is
// deliberately one-way — internal/overrides must never import this package,
// or the two would form a cycle.
package customep

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/overrides"
)

// Row is one custom_endpoints row, already decoded.
type Row struct {
	ID          int64
	WorkspaceID int64
	Method      string // upper case
	Path        string // RELATIVE, leading slash, no base path
	// CanonicalPath is router.CanonicalPath(Path), computed once by
	// [Repo.Create] and stored — never recomputed at match time, since it is
	// also the conflict key DESIGN §8 checks a new custom endpoint against.
	CanonicalPath string
	// SourceOrder is assigned by [Repo.Create] as max(source_order)+1 for the
	// workspace: SQLite row order is not stable across scans, and it is the
	// final tie-break router.compareRoutes uses (DESIGN §8 rule 4).
	SourceOrder int64

	OverrideOn bool // false: the row exists but is left out of the route table entirely
	RouteOff   bool // true: the route answers 404 (DESIGN §8), same meaning as op_overrides

	// ActiveStatus has no spec document to fall back on the way
	// op_overrides.ActiveStatus does (nil there means "keep the document's
	// own choice") — a custom endpoint has no document, so the column is
	// NOT NULL DEFAULT 200 and this field is a plain int, never a pointer.
	// A zero value here (an unset field on a freshly built Row) is defaulted
	// to 200 by [Repo.Create], not written through as the literal status 0.
	ActiveStatus int

	Responses map[string]overrides.Variant // key: the status as a decimal string, e.g. "200"

	// ReqSchema is PRESERVED ONLY — request validation is P2. It is copied
	// byte-for-byte between the column and this field, never re-encoded
	// through encoding/json, exactly like FailDirective below.
	ReqSchema jsonx.RawMessage

	ListSize *overrides.ListSize
	DelayMs  *int

	// FailDirective is PRESERVED ONLY, same as op_overrides.FailDirective —
	// this slice's evaluator does not interpret it.
	FailDirective jsonx.RawMessage
	ValidateReq   *bool

	// Kind is custom_endpoints.kind (P6b, 0005): KindHTTP for every row
	// that existed before the column, KindSSE for a stream. "" on a Row a
	// caller built is normalised to KindHTTP by normalizeAndValidate, so
	// no caller that never heard of streams has to say so.
	Kind string
	// Stream is the decoded stream document — non-nil exactly when Kind is
	// KindSSE, the coupling the column's CHECK constraint states in SQL and
	// validateKind states in Go. Unlike ReqSchema and FailDirective it is
	// INTERPRETED (served), so it is decoded, validated and re-encoded
	// through jsonx rather than preserved byte for byte.
	Stream *Stream

	// Operation is P7a's (DESIGN §34.3): the OpenAPI operation fields the
	// export writes for this row — nil for a row that never declared any.
	// See operation.go.
	Operation *Operation

	CreatedAt time.Time
	UpdatedAt time.Time

	// EditVersion is the per-row compare-and-swap token (A3, D4): the value a
	// caller must echo back on PUT .../endpoints/{eid} to prove it read this
	// exact row. Freshly allocated from the workspace's edit_seq on every
	// write that reaches this row (store.AllocateEditVersion), never
	// incremented in place -- see repo.go's UpdateExpecting/Create/
	// ReplaceAllTx for why an in-place +1 would be wrong once a row can be
	// deleted and recreated.
	EditVersion int64
}

// ErrNotFound is returned when a lookup or delete finds no matching row.
var ErrNotFound = errors.New("customep: endpoint not found")

// ErrConflict is returned by [Repo.Create] when a row already exists for the
// same (workspace, method, canonical_path) — the UNIQUE index DESIGN §8
// treats as a genuine conflict between two custom endpoints, as opposed to a
// custom endpoint canonically equal to a spec operation, which is the
// documented override and not an error at all.
var ErrConflict = errors.New("customep: an endpoint already exists for this method and canonical path")

// ErrInvalidRow wraps every reason a Row is rejected as structurally broken:
// an unknown method, a path that is not relative-with-leading-slash or that
// carries a query/fragment/"//", a responses key that is not a 3-digit
// status, or a Variant that fails overrides.ValidateVariant.
var ErrInvalidRow = errors.New("customep: invalid endpoint")

// ErrWorkspaceNotFound is returned by Create/Delete when the target
// workspace does not exist.
var ErrWorkspaceNotFound = errors.New("customep: workspace not found")

// defaultActiveStatus is what Row.ActiveStatus becomes when a caller leaves
// it at zero: 0 is never a valid HTTP status and the column's own DEFAULT is
// 200, so an unset field defaults to that rather than being written through
// literally.
const defaultActiveStatus = 200

// validMethods mirrors internal/specs/index.go's httpMethods (upper-cased):
// this package cannot import internal/specs (a leaf reaching up into an
// indexer would be backwards), so the same fixed list is written out again
// here rather than accepted as any non-empty string.
var validMethods = map[string]bool{
	"GET":     true,
	"PUT":     true,
	"POST":    true,
	"DELETE":  true,
	"OPTIONS": true,
	"HEAD":    true,
	"PATCH":   true,
	"TRACE":   true,
}

// normalizeAndValidate upper-cases Method, checks Path's shape, defaults a
// nil Responses to an empty map and a zero ActiveStatus to 200, and then
// validates everything through overrides.ValidateResponses — the SAME gate
// op_overrides writes (and reads) through, so a malformed row is rejected
// identically regardless of which table it is headed for. It does NOT touch
// CanonicalPath or SourceOrder: those are computed by [Repo.Create], inside
// the write transaction, from the now-normalized Path and workspace state.
// maxFrameBytes is the per-frame payload cap (MOCKER_MAX_RESPONSE, handed
// to the Repo by its constructor's caller); <= 0 means DefaultMaxFrameBytes.
func normalizeAndValidate(row *Row, maxFrameBytes int64) error {
	row.Method = upperASCII(row.Method)
	if !validMethods[row.Method] {
		return fmt.Errorf("%w: method %q is not a known HTTP method", ErrInvalidRow, row.Method)
	}
	if err := validatePath(row.Path); err != nil {
		return err
	}
	if row.Responses == nil {
		row.Responses = map[string]overrides.Variant{}
	}
	if err := overrides.ValidateResponses(row.Responses); err != nil {
		// overrides.ValidateResponses/ValidateVariant wrap overrides.ErrInvalidRow,
		// not this package's own sentinel — re-wrapped here so a caller doing
		// errors.Is(err, customep.ErrInvalidRow) sees every rejection this
		// package's Create can produce, regardless of which validation layer
		// caught it.
		return fmt.Errorf("%w: %w", ErrInvalidRow, err)
	}
	if row.ActiveStatus == 0 {
		row.ActiveStatus = defaultActiveStatus
	}
	if !overrides.ValidHTTPStatus(row.ActiveStatus) {
		// See overrides.ValidHTTPStatus: a stored status net/http refuses
		// to write is a panic per request on the mock plane.
		return fmt.Errorf("%w: status %d is outside 100..599", ErrInvalidRow, row.ActiveStatus)
	}
	if row.ListSize != nil {
		if row.ListSize.Min < 0 || row.ListSize.Max < row.ListSize.Min {
			return fmt.Errorf("%w: listSize has an inverted or negative range [%d,%d]",
				ErrInvalidRow, row.ListSize.Min, row.ListSize.Max)
		}
	}
	if row.DelayMs != nil && *row.DelayMs < 0 {
		return fmt.Errorf("%w: delayMs must not be negative, got %d", ErrInvalidRow, *row.DelayMs)
	}
	// P7a (D3): reqSchema stops being preserved-only — it is exported as
	// the operation's requestBody, so it must at least be a schema
	// object under the cap; its $refs are the admin plane's ValidateRefs.
	if err := overrides.ValidateSchemaShape(row.ReqSchema); err != nil {
		return fmt.Errorf("%w: reqSchema: %w", ErrInvalidRow, err)
	}
	if err := validateOperation(row); err != nil {
		return err
	}
	return validateKind(row, maxFrameBytes)
}

// validatePath enforces the same "relative, leading slash" shape
// overrides.Row.Path documents, plus the additional checks a custom
// endpoint's path needs because — unlike an op_overrides row, whose Path
// always came from a real operations row to begin with — nothing upstream
// of this package has already validated it: no query, no fragment, no "//"
// (which would desync from router.CanonicalPath's segment splitting).
func validatePath(p string) error {
	if p == "" || p[0] != '/' {
		return fmt.Errorf("%w: path %q must start with \"/\"", ErrInvalidRow, p)
	}
	if strings.ContainsAny(p, "?#") {
		return fmt.Errorf("%w: path %q must not carry a query or fragment", ErrInvalidRow, p)
	}
	if strings.Contains(p, "//") {
		return fmt.Errorf("%w: path %q must not contain \"//\"", ErrInvalidRow, p)
	}
	return nil
}

// upperASCII is overrides.upperASCII's identical unexported twin: upper-cases
// only what an HTTP method ever contains. Duplicated rather than imported
// because it is ten lines and importing it would mean reaching into
// internal/overrides for a helper that carries no shared state — the actual
// shared logic (ValidateVariant/ValidateResponses) IS imported, see the
// package doc comment for why this one line of duplication is not that.
func upperASCII(s string) string {
	b := []byte(s)
	changed := false
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - ('a' - 'A')
			changed = true
		}
	}
	if !changed {
		return s
	}
	return string(b)
}

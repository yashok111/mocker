// Reads: every query over an imported spec and its index, plus the row
// scanners. Split out of repo.go 2026-09-03; the text is unchanged.
package specs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/router"
)

// ByID looks up a spec by primary key.
func (r *Repo) ByID(ctx context.Context, id int64) (*Spec, error) {
	s, err := scanSpec(r.db.R.QueryRowContext(ctx, selectSpec+" WHERE id = ?", id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan spec %d: %w", id, err)
	}
	return s, nil
}

// ByHash looks up a spec by the sha256 of its bytes as uploaded — the
// `hash` [Import] deduplicates on and the value a bundle's `spec.hash`
// carries (P4b, 2026-09-02): an imported workspace binds to the spec of
// the SAME BYTES, never to one that merely shares a name, because a name is
// not unique and two renderings of one API are two specs here already.
func (r *Repo) ByHash(ctx context.Context, hash string) (*Spec, error) {
	s, err := scanSpec(r.db.R.QueryRowContext(ctx, selectSpec+" WHERE hash = ?", hash))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan spec by hash %q: %w", hash, err)
	}
	return s, nil
}

// Raw returns specID's stored bytes AS UPLOADED — JSON or YAML, whichever
// the operator sent — which is what an export inlines (P4b): [Import] hashes
// those bytes, so only those bytes re-import to the same hash on another
// installation. Normalized would be prettier and would be a different spec.
func (r *Repo) Raw(ctx context.Context, specID int64) ([]byte, error) {
	var raw []byte
	err := r.db.R.QueryRowContext(ctx, "SELECT raw FROM specs WHERE id = ?", specID).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("load spec %d raw: %w", specID, err)
	}
	return raw, nil
}

// Normalized returns specID's stored normalized document bytes — the same
// bytes [openapi.Load] produced at import time (DESIGN §7) and [Import]
// wrote into specs.normalized alongside the raw upload. P1b's mock-plane
// runtime builds over this, never over raw: re-running Load on it is then a
// pure re-parse of already-dialect-normalized JSON, not a second pass of
// dialect normalization.
func (r *Repo) Normalized(ctx context.Context, specID int64) ([]byte, error) {
	var normalized []byte
	err := r.db.R.QueryRowContext(ctx, "SELECT normalized FROM specs WHERE id = ?", specID).Scan(&normalized)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("load normalized document for spec %d: %w", specID, err)
	}
	return normalized, nil
}

// List returns every spec, ordered by id ascending (deterministic, oldest
// first — the same convention [workspaces.Repo.List] uses).
func (r *Repo) List(ctx context.Context) ([]*Spec, error) {
	rows, err := r.db.R.QueryContext(ctx, selectSpec+" ORDER BY id ASC")
	if err != nil {
		return nil, fmt.Errorf("list specs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*Spec
	for rows.Next() {
		s, err := scanSpec(rows)
		if err != nil {
			return nil, fmt.Errorf("scan spec row: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate specs: %w", err)
	}
	return out, nil
}

// Report answers specID's [openapi.Report], computing it at most once per
// spec id and memoizing the result in r.reportCache thereafter.
//
// It is NOT reading a stored column — there is no column for it, and HARD
// RULE 3 forbids adding one — the FIRST call for a given id reloads the
// spec's raw bytes, runs [openapi.Load] and [Index] over them again, and
// reconciles the result against the operations table (see computeReport's
// doc comment for the full derivation). Caching that result is safe because
// a spec row is immutable after Import: nothing in this package ever
// updates specs.raw or re-runs ReplaceOperations for an id once Import's
// transaction commits, so "what Report would compute" cannot change out
// from under a cached entry while that id stays alive.
//
// It CAN change when an id is reused, though: specs.id is a plain
// `INTEGER PRIMARY KEY` with no AUTOINCREMENT (see 0001_init.sql), so
// SQLite is free to hand a brand-new spec the same id a just-deleted spec
// used to have. [Repo.Delete] evicts the cache entry for exactly this
// reason — see its doc comment — so a reused id is always a fresh miss here
// rather than a stale hit.
//
// Before this cache existed, every call — however small its own response —
// re-decoded the full raw document, re-walked normalizeDialect, re-ran
// Index, and rescanned every operations row for specID from scratch: for a
// large imported document, seconds of CPU and hundreds of MB of heap per
// call, with no rate limit ahead of it (finding 3, P1a round-1 review). The
// returned *openapi.Report is always a fresh copy (see cloneReport): no
// caller can mutate the cached entry through the pointer it gets back.
func (r *Repo) Report(ctx context.Context, specID int64) (*openapi.Report, error) {
	r.reportMu.Lock()
	cached, ok := r.reportCache[specID]
	r.reportMu.Unlock()
	if ok {
		return cloneReport(cached), nil
	}

	report, err := r.computeReport(ctx, specID)
	if err != nil {
		return nil, err
	}

	r.reportMu.Lock()
	r.reportCache[specID] = report
	r.reportMu.Unlock()
	return cloneReport(report), nil
}

// computeReport is Report's actual derivation, run on a cache miss. It
// reloads the spec's raw bytes, runs [openapi.Load] over them again, and
// then runs [Index] over the same document and a fresh [openapi.Resolver].
// Index is the only place that ever discovers a $ref-resolution problem
// (budget, cycle, missing pointer) inside a response, and nothing persists
// those warnings anywhere else — without this call they would exist nowhere
// once Import returns.
//
// Index's own Operations/Degraded counters describe the freshly re-parsed
// document, which is exactly what normal Import stores. But nothing stops a
// caller from writing to the operations table directly through
// [Repo.ReplaceOperations] without a matching document (tests do exactly
// that), so Operations and Degraded are re-derived a second time from what
// is ACTUALLY stored — a fresh operations-table scan — after keeping only
// Index's warnings. This makes every cache-miss a real recomputation, not a
// re-derivation from some other cache: the report always reflects both what
// is really stored (Operations, Degraded, per-row parse_error) and what a
// live re-index of the raw bytes finds (ref-resolution warnings), instead of
// risking a second, driftable copy of either.
func (r *Repo) computeReport(ctx context.Context, specID int64) (*openapi.Report, error) {
	var raw []byte
	err := r.db.R.QueryRowContext(ctx, "SELECT raw FROM specs WHERE id = ?", specID).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("load spec %d raw: %w", specID, err)
	}

	doc, report, err := openapi.Load(raw)
	if err != nil {
		// The document was accepted once already, at import time; a failure
		// here means the stored bytes themselves are corrupted, which is a
		// real error and not something to paper over as a warning.
		return nil, fmt.Errorf("re-derive report for spec %d: %w", specID, err)
	}

	resolver := openapi.NewResolver(doc, openapi.DefaultRefBudget)
	Index(doc, resolver, report)
	// Discard Index's own Operations/Degraded — see doc comment above — and
	// re-derive both from the operations table below, the ground truth of
	// what is actually stored for this spec.
	report.Operations = 0
	report.Degraded = 0

	rows, err := r.db.R.QueryContext(ctx, `
		SELECT pointer, parse_error FROM operations
		WHERE spec_id = ? ORDER BY source_order ASC`, specID)
	if err != nil {
		return nil, fmt.Errorf("load operations for spec %d: %w", specID, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			pointer  string
			parseErr *string
		)
		if err := rows.Scan(&pointer, &parseErr); err != nil {
			return nil, fmt.Errorf("scan operation row: %w", err)
		}
		report.Operations++
		if parseErr != nil {
			report.Degraded++
			report.Add(pointer, "operation-parse-error", *parseErr)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operations for spec %d: %w", specID, err)
	}
	return report, nil
}

// cloneReport returns a copy of rep safe for a caller to hold and even
// mutate (e.g. via Report.Add) without corrupting r.reportCache's own copy
// — the slice header alone would alias the same backing array otherwise.
func cloneReport(rep *openapi.Report) *openapi.Report {
	if rep == nil {
		return nil
	}
	out := *rep
	out.Warnings = slices.Clone(rep.Warnings)
	return &out
}

// Operations pages specID's operations, ordered by source_order (the
// document's own order, DESIGN §7 step 5). limit <= 0 means all rows: the
// SQL itself does the paging via LIMIT/OFFSET, so a caller asking for "all"
// never has to load every row just to slice it back down, and a caller
// asking for a page never pays for rows it will not use.
func (r *Repo) Operations(ctx context.Context, specID int64, limit, offset int) ([]*Operation, error) {
	query := `
		SELECT id, spec_id, method, path, canonical_path, operation_id, summary, tag,
		       source_order, pointer, parse_error
		FROM operations
		WHERE spec_id = ?
		ORDER BY source_order ASC`
	args := []any{specID}
	if limit > 0 {
		query += " LIMIT ? OFFSET ?"
		args = append(args, limit, offset)
	}

	rows, err := r.db.R.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list operations for spec %d: %w", specID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []*Operation
	for rows.Next() {
		op, err := scanOperation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan operation row: %w", err)
		}
		out = append(out, op)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operations for spec %d: %w", specID, err)
	}
	return out, nil
}

// OperationByID returns one operations row by its primary key. The
// traffic-to-override conversion needs the operation's TEMPLATE path, which
// is the only key the mock plane's override lookup will ever produce: a
// traffic row holds the concrete path a client requested, and stripping the
// workspace's base path off that never reconstructs "/widgets/{widgetId}"
// from "/widgets/7". Resolving through this method — id -> Operation.Path —
// is the only correct route from a traffic.MatchedID to that key.
func (r *Repo) OperationByID(ctx context.Context, id int64) (*Operation, error) {
	op, err := scanOperation(r.db.R.QueryRowContext(ctx, `
		SELECT id, spec_id, method, path, canonical_path, operation_id, summary, tag,
		       source_order, pointer, parse_error
		FROM operations
		WHERE id = ?`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan operation %d: %w", id, err)
	}
	return op, nil
}

// Responses returns every response variant for operationID, ordered by
// insertion (row id) — operation_responses carries no source_order of its
// own, and insertion order already reflects the spec's own response-map
// order (DESIGN §7 step 5).
func (r *Repo) Responses(ctx context.Context, operationID int64) ([]*Response, error) {
	rows, err := r.db.R.QueryContext(ctx, `
		SELECT id, operation_id, selector, http_status, is_default, media_type, status_origin, schema_ptr
		FROM operation_responses
		WHERE operation_id = ?
		ORDER BY id ASC`, operationID)
	if err != nil {
		return nil, fmt.Errorf("list responses for operation %d: %w", operationID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []*Response
	for rows.Next() {
		resp, err := scanResponse(rows)
		if err != nil {
			return nil, fmt.Errorf("scan response row: %w", err)
		}
		out = append(out, resp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate responses for operation %d: %w", operationID, err)
	}
	return out, nil
}

// Routes builds the []router.Route the runtime route table compiles specID's
// operations into. Every field router.Build reads is filled here: OpRowID is
// the operations row id, OperationLabel is operation_id or "" when it was
// NULL (never a lookup key, per [router.Route]'s own doc comment), Method,
// Path and CanonicalPath come straight from their columns, Custom is always
// false (no custom endpoints exist until P2), and SourceOrder carries the
// document order forward since SQLite row order is not itself guaranteed
// stable across scans.
func (r *Repo) Routes(ctx context.Context, specID int64) ([]router.Route, error) {
	rows, err := r.db.R.QueryContext(ctx, `
		SELECT id, method, path, canonical_path, operation_id, source_order
		FROM operations
		WHERE spec_id = ?
		ORDER BY source_order ASC`, specID)
	if err != nil {
		return nil, fmt.Errorf("list routes for spec %d: %w", specID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []router.Route
	for rows.Next() {
		var (
			id, sourceOrder int64
			method, path    string
			canonical       string
			operationID     *string
		)
		if err := rows.Scan(&id, &method, &path, &canonical, &operationID, &sourceOrder); err != nil {
			return nil, fmt.Errorf("scan route row: %w", err)
		}
		label := ""
		if operationID != nil {
			label = *operationID
		}
		out = append(out, router.Route{
			OpRowID:        id,
			OperationLabel: label,
			Method:         method,
			Path:           path,
			CanonicalPath:  canonical,
			Custom:         false,
			SourceOrder:    sourceOrder,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate routes for spec %d: %w", specID, err)
	}
	return out, nil
}

// Variants returns every response variant for specID's operations, keyed by
// operations.id — router.Route.OpRowID, the same key the runtime's route
// table already uses, so a caller (mockplane's runtime builder) can look a
// match's variants up by the one id it already has, with no second join at
// request time.
//
// This is ONE query joining operation_responses to operations for the whole
// spec, deliberately not one query per operation: the runtime is built on a
// cold cache while a real request is waiting on it (routes.go's
// singleflight), so an N+1 here would turn a single slow request into the
// thing that also holds up every other request racing the same cold build.
//
// Degraded mirrors operations.parse_error IS NOT NULL (DESIGN §7): a
// degraded operation's variants are still returned as data — deciding what
// to do about Degraded (answer an empty 200 instead of generating a body)
// is the generator's job, not this query's.
func (r *Repo) Variants(ctx context.Context, specID int64) (map[int64][]gen.ResponseVariant, error) {
	rows, err := r.db.R.QueryContext(ctx, `
		SELECT o.id, r.selector, r.http_status, r.is_default, r.media_type, r.schema_ptr,
		       o.pointer, o.parse_error
		FROM operation_responses r
		JOIN operations o ON o.id = r.operation_id
		WHERE o.spec_id = ?
		ORDER BY o.source_order ASC, r.id ASC`, specID)
	if err != nil {
		return nil, fmt.Errorf("list response variants for spec %d: %w", specID, err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[int64][]gen.ResponseVariant)
	for rows.Next() {
		var (
			opRowID                        int64
			selector, pointer              string
			httpStatus                     int
			isDefault                      int
			mediaType, schemaPtr, parseErr sql.NullString
		)
		if err := rows.Scan(&opRowID, &selector, &httpStatus, &isDefault, &mediaType, &schemaPtr, &pointer, &parseErr); err != nil {
			return nil, fmt.Errorf("scan response variant row: %w", err)
		}
		out[opRowID] = append(out[opRowID], gen.ResponseVariant{
			OpRowID:    opRowID,
			Selector:   selector,
			HTTPStatus: httpStatus,
			IsDefault:  isDefault != 0,
			MediaType:  mediaType.String,
			SchemaPtr:  schemaPtr.String,
			OpPointer:  pointer,
			Degraded:   parseErr.Valid,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate response variants for spec %d: %w", specID, err)
	}
	return out, nil
}

// AttachedWorkspaces returns the slugs of every workspace whose
// workspaces.spec_id points at specID, ordered for deterministic output.
// This queries the workspaces table directly by SQL rather than through
// [workspaces.Repo] — this package imports neither that package nor its
// types, matching the digest's rule that workspaces must not learn about
// specs (and, symmetrically, specs stays a peer of workspaces, not a
// dependent of it).
func (r *Repo) AttachedWorkspaces(ctx context.Context, specID int64) ([]string, error) {
	return attachedWorkspacesTx(ctx, r.db.R, specID)
}

// dbQuerier is satisfied by both *sql.DB (well, the pools' QueryContext) and
// *sql.Tx, so attachedWorkspacesTx can run through either the reader pool
// (AttachedWorkspaces) or a write transaction (Delete's own check).
type dbQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func attachedWorkspacesTx(ctx context.Context, q dbQuerier, specID int64) ([]string, error) {
	rows, err := q.QueryContext(ctx, "SELECT slug FROM workspaces WHERE spec_id = ? ORDER BY slug ASC", specID)
	if err != nil {
		return nil, fmt.Errorf("list attached workspaces for spec %d: %w", specID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, fmt.Errorf("scan workspace slug: %w", err)
		}
		out = append(out, slug)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate attached workspaces for spec %d: %w", specID, err)
	}
	return out, nil
}

// selectSpec is the shared column list/order for scanSpec. It never selects
// raw or normalized: those are multi-KB blobs no caller in this package
// needs alongside a Spec, and Report/Import read them directly, by name,
// exactly where they are used.
const selectSpec = `
	SELECT id, name, version, format, source, source_ref, base_path, hash, created_at, created_by
	FROM specs`

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so scan logic is
// written once (the same pattern [workspaces.Repo] uses).
type rowScanner interface {
	Scan(dest ...any) error
}

func scanSpec(row rowScanner) (*Spec, error) {
	var (
		s         Spec
		version   sql.NullString
		sourceRef sql.NullString
		createdAt int64
		createdBy sql.NullInt64
	)
	if err := row.Scan(
		&s.ID, &s.Name, &version, &s.Format, &s.Source, &sourceRef,
		&s.BasePath, &s.Hash, &createdAt, &createdBy,
	); err != nil {
		return nil, err
	}
	s.Version = version.String
	s.SourceRef = sourceRef.String
	s.CreatedAt = time.Unix(createdAt, 0).UTC()
	if createdBy.Valid {
		id := createdBy.Int64
		s.CreatedBy = &id
	}
	return &s, nil
}

func scanOperation(row rowScanner) (*Operation, error) {
	var (
		op                                  Operation
		operationID, summary, tag, parseErr sql.NullString
	)
	if err := row.Scan(
		&op.ID, &op.SpecID, &op.Method, &op.Path, &op.CanonicalPath,
		&operationID, &summary, &tag, &op.SourceOrder, &op.Pointer, &parseErr,
	); err != nil {
		return nil, err
	}
	if operationID.Valid {
		v := operationID.String
		op.OperationID = &v
	}
	if summary.Valid {
		v := summary.String
		op.Summary = &v
	}
	if tag.Valid {
		v := tag.String
		op.Tag = &v
	}
	if parseErr.Valid {
		v := parseErr.String
		op.ParseError = &v
	}
	return &op, nil
}

func scanResponse(row rowScanner) (*Response, error) {
	var (
		resp                 Response
		isDefault            int
		mediaType, schemaPtr sql.NullString
	)
	if err := row.Scan(
		&resp.ID, &resp.OperationID, &resp.Selector, &resp.HTTPStatus, &isDefault,
		&mediaType, &resp.StatusOrigin, &schemaPtr,
	); err != nil {
		return nil, err
	}
	resp.IsDefault = isDefault != 0
	if mediaType.Valid {
		v := mediaType.String
		resp.MediaType = &v
	}
	if schemaPtr.Valid {
		v := schemaPtr.String
		resp.SchemaPtr = &v
	}
	return &resp, nil
}

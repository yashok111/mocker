// Writes: import (with its operation index), delete, and the batched index
// inserts. Split out of repo.go 2026-09-03; the text is unchanged.
package specs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yashok111/mocker/internal/openapi"
)

// ImportInput is what a caller supplies to [Repo.Import]. Everything else
// (id, hash, format, base path, timestamps) is derived from Document.
type ImportInput struct {
	// Name is the spec's display name. Empty falls back to the document's
	// own info.title, so an admin upload with no name field still gets a
	// usable label instead of a blank row on the "Specs" screen.
	Name      string
	Source    string // upload | bundle | url
	SourceRef string
	Document  []byte
	CreatedBy *int64
}

// ImportResult is what [Repo.Import] returns on success — including the
// ErrDuplicate case, where Spec is the PRE-EXISTING row and nothing new was
// written.
type ImportResult struct {
	Spec *Spec
	// Report is freshly computed by the same [openapi.Load] call Import used
	// to validate Document, never stored (see [Repo.Report]'s doc comment).
	Report *openapi.Report
	// Operations is always empty in P1a: the operation indexer that turns a
	// *openapi.Document into real rows is a later phase (see
	// [Repo.ReplaceOperations]).
	Operations []*Operation
}

// maxIndexedOperations is the hard ceiling ErrTooManyOperations enforces.
// 5000 is comfortably above any real single-service OpenAPI document (the
// P1a acceptance document has 130; well-known large public APIs run in the
// hundreds to low thousands) while still rejecting a pathological "paths"
// object orders of magnitude sooner than letting it reach the insert loop.
const maxIndexedOperations = 5000

// insertBatchSize bounds how many rows one multi-row INSERT statement
// carries in ReplaceOperations. Batching turns tens or hundreds of
// thousands of individual ExecContext calls — each paying its own
// parse/plan/execute overhead through database/sql and the SQLite engine —
// into a bounded number of multi-row statements, which is what keeps a
// large import from holding store.DB.W's one write connection for an
// extended stretch (the other half of finding 4; maxIndexedOperations
// bounds the total, this bounds the per-statement cost). 200 operation rows
// is 2000 bound parameters (10 columns), comfortably inside SQLite's
// per-statement ceiling with room to spare.
const insertBatchSize = 200

// Import validates, deduplicates and stores Document as a new spec.
//
// Size is checked BEFORE anything else — a document over cfg.MaxBody is
// refused with ErrTooLarge without ever being parsed, let alone hashed or
// hitting the database. Document is then loaded with [openapi.Load] outside
// any transaction, since parsing is pure CPU: holding the single writer
// connection (DESIGN: store.DB.W has exactly one) for the duration of a
// parse would serialize it against every other admin write in the system
// for no reason.
//
// [Index] then runs over the loaded document, also outside any transaction
// for the same CPU-bound reason, and its operation count is checked against
// maxIndexedOperations before Import ever opens a transaction: a document
// that indexes into a pathological number of operations is refused with
// ErrTooManyOperations up front, rather than discovered 33 seconds into a
// write transaction that has been blocking every other write in the process
// the whole time (finding 4, P1a round-1 review).
//
// Only once that check passes does Import open the one write transaction
// that checks the raw hash for a duplicate and, if none is found, inserts
// the spec row and calls ReplaceOperations with the real, already-computed
// operation and response rows — which itself inserts them in bounded,
// multi-row batches rather than one row per statement, for the same reason.
// Because store.DB.W is a single connection, that transaction is strictly
// serialized against every concurrent Import, so two callers racing to
// import the same bytes can never both see "no duplicate" and both insert.
// PreparedImport contains a validated runtime projection. It is immutable after
// preparation and may be inserted in a caller-owned transaction.
type PreparedImport struct {
	in                   ImportInput
	doc                  *openapi.Document
	Report               *openapi.Report
	Operations           []*Operation
	responses            map[int][]*Response
	suggestions          []Suggestion
	hash, name, basePath string
}

func (r *Repo) PrepareImport(in ImportInput) (*PreparedImport, error) {
	if int64(len(in.Document)) > r.cfg.MaxBody {
		return nil, ErrTooLarge
	}
	doc, report, err := openapi.Load(in.Document)
	if err != nil {
		return nil, err
	}
	resolver := openapi.NewResolver(doc, openapi.DefaultRefBudget)
	ops, responses := Index(doc, resolver, report)
	if len(ops) > maxIndexedOperations {
		return nil, ErrTooManyOperations
	}
	sum := sha256.Sum256(in.Document)
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = doc.Title()
	}
	basePath, _ := doc.BasePath()
	return &PreparedImport{in: in, doc: doc, Report: report, Operations: ops, responses: responses,
		suggestions: deriveSuggestions(resolver, ops, responses), hash: hex.EncodeToString(sum[:]), name: name, basePath: basePath}, nil
}

// ImportTx stores the prepared document and its complete index atomically with
// the caller's own changes. Duplicate content returns the existing immutable spec.
func (r *Repo) ImportTx(ctx context.Context, tx *sql.Tx, p *PreparedImport) (*ImportResult, error) {
	existing, err := scanSpec(tx.QueryRowContext(ctx, selectSpec+" WHERE hash = ?", p.hash))
	if err == nil {
		return &ImportResult{Spec: existing, Report: p.Report}, ErrDuplicate
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO specs
 (name,version,format,source,source_ref,base_path,hash,raw,normalized,created_at,created_by)
 VALUES (?,?,?,?,?,?,?,?,?,?,?)`, p.name, p.doc.Version(), string(p.doc.Format()), p.in.Source, p.in.SourceRef, p.basePath, p.hash, p.doc.Raw(), p.doc.Normalized(), now.Unix(), p.in.CreatedBy)
	if err != nil {
		return nil, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err = r.ReplaceOperations(ctx, tx, id, p.Operations, p.responses); err != nil {
		return nil, err
	}
	if err = insertSuggestionsTx(ctx, tx, id, p.suggestions); err != nil {
		return nil, err
	}
	return &ImportResult{Spec: &Spec{ID: id, Name: p.name, Version: p.doc.Version(), Format: string(p.doc.Format()), Source: p.in.Source, SourceRef: p.in.SourceRef, BasePath: p.basePath, Hash: p.hash, CreatedAt: now, CreatedBy: p.in.CreatedBy}, Report: p.Report}, nil
}

func (r *Repo) Import(ctx context.Context, in ImportInput) (*ImportResult, error) {
	prepared, err := r.PrepareImport(in)
	if err != nil {
		return nil, err
	}
	var result *ImportResult
	err = r.db.Write(ctx, func(tx *sql.Tx) error { var err error; result, err = r.ImportTx(ctx, tx, prepared); return err })
	return result, err
}

// Delete removes specID's spec row, cascading its operations and their
// responses (both declared ON DELETE CASCADE). It refuses to delete a spec
// any workspace still references: workspaces.spec_id REFERENCES specs(id)
// with NO ON DELETE clause, and foreign_keys is ON, so deleting an attached
// spec would otherwise surface as a raw constraint-violation error instead
// of the typed ErrAttached a caller can act on.
//
// The attachment check and the delete run inside the SAME write
// transaction — not a pre-check via the reader pool followed by a separate
// write — so there is no window between "checked unattached" and "deleted"
// for a concurrent workspace update to attach the spec in. Because
// store.DB.W is a single connection, every other write in the system
// (including a workspace's own spec_id change) is already serialized behind
// whichever write transaction is open, so this closes the race for free.
//
// On success it also evicts id from r.reportCache. This is required for
// correctness, not just hygiene: specs.id has no AUTOINCREMENT (0001_init.sql),
// so SQLite can hand a future Import the very id just freed here, and that
// new spec must never answer [Repo.Report] with the deleted one's cached
// result.
func (r *Repo) Delete(ctx context.Context, id int64) error {
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		attached, err := attachedWorkspacesTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if len(attached) > 0 {
			return ErrAttached
		}

		res, err := tx.ExecContext(ctx, "DELETE FROM specs WHERE id = ?", id)
		if err != nil {
			return fmt.Errorf("delete spec %d: %w", id, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("delete spec %d: %w", id, err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err == nil {
		r.reportMu.Lock()
		delete(r.reportCache, id)
		r.reportMu.Unlock()
	}
	return err
}

// ReplaceOperations replaces every operations row for specID — and, via
// each row's own ON DELETE CASCADE, every operation_responses row hanging
// off them — inside the caller's transaction. It is idempotent by
// construction: it deletes specID's existing operations first, so calling
// it twice with the same (ops, resp) leaves exactly one set of rows, never
// a duplicate.
//
// resp is keyed by the INDEX INTO ops, not a row id and not source_order:
// an operation's row id does not exist until its INSERT returns, so there
// is no other value the caller could have keyed responses by before calling
// this.
//
// It takes the caller's own *sql.Tx rather than opening one itself, because
// Import needs the spec row and its operations to land in exactly one
// transaction (DESIGN §7: raw+normalized+operations are never half-written)
// — ReplaceOperations is the piece of that transaction that owns the
// operations/operation_responses tables.
//
// Rows are inserted insertBatchSize at a time via multi-row INSERT
// statements, not one row per ExecContext call: on a large operation set,
// a single-row-at-a-time loop held store.DB.W's one write connection — and
// therefore every other write in the process — for tens of seconds (finding
// 4, P1a round-1 review). Each operations batch uses RETURNING id to read
// back the freshly assigned ids in the SAME order as the batch's own rows —
// a multi-row VALUES INSERT assigns rowids sequentially in VALUES-clause
// order, and RETURNING echoes rows back in that same order (confirmed
// directly against modernc.org/sqlite; see TestRepo_ReplaceOperations_batchedInsertOrder) —
// which is what lets each batch's responses be inserted immediately after,
// correctly linked to their operation_id, without a second lookup query.
// This is safe specifically because the whole call runs on store.DB.W's
// single writer connection inside one transaction — no concurrent statement
// can ever interleave and disturb that order.
func (r *Repo) ReplaceOperations(ctx context.Context, tx *sql.Tx, specID int64, ops []*Operation, resp map[int][]*Response) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM operations WHERE spec_id = ?", specID); err != nil {
		return fmt.Errorf("clear operations for spec %d: %w", specID, err)
	}

	for start := 0; start < len(ops); start += insertBatchSize {
		end := min(start+insertBatchSize, len(ops))
		batch := ops[start:end]

		opIDs, err := insertOperationsBatch(ctx, tx, specID, batch)
		if err != nil {
			return fmt.Errorf("insert operations %d-%d for spec %d: %w", start, end-1, specID, err)
		}

		var rows []responseRow
		for j := range batch {
			for _, rr := range resp[start+j] {
				rows = append(rows, responseRow{opID: opIDs[j], r: rr})
			}
		}
		if err := insertResponsesBatch(ctx, tx, rows); err != nil {
			return fmt.Errorf("insert responses for spec %d: %w", specID, err)
		}
	}
	return nil
}

// insertOperationsBatch inserts batch as one multi-row INSERT ... RETURNING
// statement and returns each row's freshly assigned id, in the SAME order
// as batch (see [Repo.ReplaceOperations]'s doc comment for why that
// ordering can be relied on here).
func insertOperationsBatch(ctx context.Context, tx *sql.Tx, specID int64, batch []*Operation) ([]int64, error) {
	var q strings.Builder
	q.WriteString(`INSERT INTO operations
		(spec_id, method, path, canonical_path, operation_id, summary, tag, source_order, pointer, parse_error)
		VALUES `)
	args := make([]any, 0, len(batch)*10)
	for i, op := range batch {
		if i > 0 {
			q.WriteByte(',')
		}
		q.WriteString("(?,?,?,?,?,?,?,?,?,?)")
		args = append(args, specID, op.Method, op.Path, op.CanonicalPath, op.OperationID, op.Summary, op.Tag,
			op.SourceOrder, op.Pointer, op.ParseError)
	}
	q.WriteString(" RETURNING id")

	rows, err := tx.QueryContext(ctx, q.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("insert operations batch: %w", err)
	}
	defer func() { _ = rows.Close() }()

	ids := make([]int64, 0, len(batch))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan inserted operation id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate inserted operation ids: %w", err)
	}
	if len(ids) != len(batch) {
		return nil, fmt.Errorf("insert operations batch: got %d ids back for %d rows", len(ids), len(batch))
	}
	return ids, nil
}

// responseRow pairs one Response with the id of the operation row it
// belongs to. Responses can only be inserted once that id is known, which
// means once their operation's own batch has already been inserted (see
// [Repo.ReplaceOperations]).
type responseRow struct {
	opID int64
	r    *Response
}

// insertResponsesBatch inserts rows as one or more multi-row INSERT
// statements, insertBatchSize rows per statement, for the same reason
// [insertOperationsBatch] does. No id needs to come back here: nothing
// downstream keys off a response row's own id within one Import.
func insertResponsesBatch(ctx context.Context, tx *sql.Tx, rows []responseRow) error {
	for start := 0; start < len(rows); start += insertBatchSize {
		end := min(start+insertBatchSize, len(rows))
		chunk := rows[start:end]

		var q strings.Builder
		q.WriteString(`INSERT INTO operation_responses
			(operation_id, selector, http_status, is_default, media_type, status_origin, schema_ptr)
			VALUES `)
		args := make([]any, 0, len(chunk)*7)
		for i, row := range chunk {
			if i > 0 {
				q.WriteByte(',')
			}
			q.WriteString("(?,?,?,?,?,?,?)")
			args = append(args, row.opID, row.r.Selector, row.r.HTTPStatus, boolToInt(row.r.IsDefault),
				row.r.MediaType, row.r.StatusOrigin, row.r.SchemaPtr)
		}
		if _, err := tx.ExecContext(ctx, q.String(), args...); err != nil {
			return fmt.Errorf("insert responses batch %d-%d: %w", start, end-1, err)
		}
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Resource suggestions: derived once at import, lazily backfilled, and since
// P3f re-derived into a new generation. Split out of repo.go 2026-09-03; the
// text is unchanged.
package specs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/openapi"
)

// ResourceSuggestion is one non-sentinel resource_suggestions row, read
// back from the table exactly as stored — the shape both [Repo.Import] and
// [Repo.EnsureSuggestions]'s lazy backfill write, and the shape a caller
// (the two later admin routes decisions.md §D3 names) renders on the
// confirm screen. It carries SpecID and Confidence, unlike [Suggestion]:
// those are the caller's own bookkeeping at DERIVATION time (there is
// nothing to compute them from yet), but once a row exists they are real,
// stored facts worth reading back rather than re-assuming.
type ResourceSuggestion struct {
	SpecID       int64
	RouteFamily  string
	Name         string
	IDField      string
	EntitySchema string
	Wrapper      Wrapper
	Confidence   float64
}

// suggestionSentinelFamily is the sentinel row's route_family (and name,
// id_field, entity_schema — all four NOT NULL columns with no default)
// (R8, decisions.md §D3): "zero suggestions" and "never derived" must be
// distinguishable, and ON CONFLICT DO NOTHING over an empty set cannot
// tell them apart, so a spec that derives nothing gets this row instead of
// no row at all. Both read routes exclude route_family = "" so the
// sentinel never renders as a suggestion — enforced here too, in
// [scanResourceSuggestion], so a caller of [Repo.EnsureSuggestions] can
// never see it either.
const suggestionSentinelFamily = ""

// insertSuggestionsTx writes suggs — possibly empty — into
// resource_suggestions for specID at gen 1, inside tx: the same
// transaction that wrote specID's operations rows (Import), or the
// backfill's own transaction (EnsureSuggestions). It is
// [insertSuggestionsGenTx] fixed at generation 1 — the only generation
// either of those two callers ever writes (decisions.md §D3.1: "generation
// 1 is what Import writes and what the lazy backfill writes... every
// generation above 1 is written by [Repo.Rederive] and by nothing else").
func insertSuggestionsTx(ctx context.Context, tx *sql.Tx, specID int64, suggs []Suggestion) error {
	return insertSuggestionsGenTx(ctx, tx, specID, 1, suggs)
}

// insertSuggestionsGenTx writes suggs — possibly empty — into
// resource_suggestions for specID at gen, inside tx. An empty suggs writes
// the sentinel row instead of nothing at all, for the reason
// [suggestionSentinelFamily] documents. gen is the caller's own
// bookkeeping: [insertSuggestionsTx] fixes it at 1, and [Repo.Rederive] is
// the one caller that passes anything else.
func insertSuggestionsGenTx(ctx context.Context, tx *sql.Tx, specID int64, gen int, suggs []Suggestion) error {
	if len(suggs) == 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO resource_suggestions
				(spec_id, gen, route_family, name, id_field, entity_schema, wrapper, confidence)
			VALUES (?, ?, ?, '', '', '', NULL, 1.0)`,
			specID, gen, suggestionSentinelFamily); err != nil {
			return fmt.Errorf("insert resource_suggestions sentinel for spec %d gen %d: %w", specID, gen, err)
		}
		return nil
	}

	for _, s := range suggs {
		wrapperJSON, err := jsonx.Marshal(s.Wrapper)
		if err != nil {
			return fmt.Errorf("marshal wrapper for %q: %w", s.RouteFamily, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO resource_suggestions
				(spec_id, gen, route_family, name, id_field, entity_schema, wrapper, confidence)
			VALUES (?, ?, ?, ?, ?, ?, ?, 1.0)`,
			specID, gen, s.RouteFamily, s.Name, s.IDField, s.EntitySchema, string(wrapperJSON)); err != nil {
			return fmt.Errorf("insert resource_suggestions %q for spec %d gen %d: %w", s.RouteFamily, specID, gen, err)
		}
	}
	return nil
}

// rowQuerier is the common surface of *sql.DB (the reader pool) and *sql.Tx
// that [suggestionsExist] needs — one implementation for both the
// unguarded fast-path read ([Repo.EnsureSuggestions]'s first check) and the
// guarded re-check taken inside the backfill's own write transaction.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// suggestionsExist reports whether specID has ANY resource_suggestions row
// at all — sentinel included, since the sentinel's whole purpose is to make
// "derived, found nothing" indistinguishable from "not yet derived" cost
// exactly one such row, not zero.
func suggestionsExist(ctx context.Context, q rowQuerier, specID int64) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, "SELECT 1 FROM resource_suggestions WHERE spec_id = ? LIMIT 1", specID).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("check resource_suggestions for spec %d: %w", specID, err)
	}
	return true, nil
}

// newSpecResolver builds an [openapi.Resolver] — a package-level
// indirection over [openapi.NewResolver] whose sole purpose is to be
// swapped out in a test, so [Repo.EnsureSuggestions]'s "the backfill runs
// ONCE" claim (decisions.md §D13 clause 9) can be asserted by an
// INSTRUMENTED CALL COUNT rather than a row count — a re-derivation that
// `ON CONFLICT DO NOTHING` swallowed would leave the same row count as no
// re-derivation at all, so only counting the (expensive, resolver-shaped)
// work itself can tell the two apart.
var newSpecResolver = openapi.NewResolver

// EnsureSuggestions is P3a's lazy backfill (R8, decisions.md §D3): the
// first call for a spec with NO resource_suggestions row at all — a spec
// imported BEFORE this slice existed, since every import since writes one
// (a real suggestion, or the sentinel) in the same transaction as its
// operations — derives, inserts and writes that record; every later call
// is a pure read and never rebuilds doc/resolver/[Index] again. The fires
// on BOTH read routes decisions.md §D3 asks for: every spec in an existing
// database predates this slice, so a caller that only ever queries one of
// the two routes must still see this run.
//
// Returns specID's derived suggestions, sentinel excluded, freshly read
// back from the table either way — a caller never needs to know whether
// this particular call derived or merely read.
func (r *Repo) EnsureSuggestions(ctx context.Context, specID int64) ([]*ResourceSuggestion, error) {
	exists, err := suggestionsExist(ctx, r.db.R, specID)
	if err != nil {
		return nil, err
	}
	if !exists {
		if err := r.backfillSuggestions(ctx, specID); err != nil {
			return nil, err
		}
	}
	return r.listSuggestions(ctx, specID)
}

// backfillSuggestions rebuilds specID's derivation inputs from scratch —
// Normalized -> [openapi.Load] -> [newSpecResolver] -> [Index], all pure —
// and runs the SAME [deriveSuggestions] Import itself calls: one
// signature, never a second, DB-shaped implementation (R3, decisions.md
// §D3). The pure rebuild happens OUTSIDE any transaction, exactly like
// Import's own derivation, before a short write transaction that
// re-checks for a concurrent backfill (two requests racing the unguarded
// read in [Repo.EnsureSuggestions] could otherwise both reach here) and
// inserts only if still nothing exists.
func (r *Repo) backfillSuggestions(ctx context.Context, specID int64) error {
	normalized, err := r.Normalized(ctx, specID)
	if err != nil {
		return fmt.Errorf("backfill resource suggestions for spec %d: %w", specID, err)
	}
	doc, report, err := openapi.Load(normalized)
	if err != nil {
		// The document was accepted once already, at import time (see
		// [Repo.computeReport]'s identical reasoning): a failure here means
		// the stored bytes are corrupted, a real error, not a warning.
		return fmt.Errorf("backfill: re-load normalized document for spec %d: %w", specID, err)
	}
	resolver := newSpecResolver(doc, openapi.DefaultRefBudget)
	ops, resp := Index(doc, resolver, report)
	suggestions := deriveSuggestions(resolver, ops, resp)

	return r.db.Write(ctx, func(tx *sql.Tx) error {
		exists, err := suggestionsExist(ctx, tx, specID)
		if err != nil {
			return err
		}
		if exists {
			// A concurrent backfill (or a concurrent Import racing an
			// impossible hash collision) already wrote a record: nothing
			// left to do, and inserting again would violate
			// UNIQUE (spec_id, gen, route_family) for anything but the
			// fully-empty case.
			return nil
		}
		return insertSuggestionsTx(ctx, tx, specID, suggestions)
	})
}

// listSuggestions reads specID's stored, non-sentinel resource_suggestions
// rows back, ordered by route_family for a deterministic, reproducible
// screen.
//
// The WHERE clause carries the ONE predicate decisions.md §D3.2/§D13.2
// gives the read side: "gen = (SELECT MAX(gen) ...)" restricts every read
// to the newest generation for specID. This is the single place that
// predicate is written — [Repo.EnsureSuggestions] (and, through it,
// [internal/resources]' findSuggestion) reads through this method and
// inherits it with no code change of its own; a second, hand-written
// MAX(gen) anywhere else would be exactly the divergent copy §D13.3
// forbids. [suggestionsExist] asks a different question ("has this spec
// EVER been derived") and keeps no predicate at all (§D4.5).
func (r *Repo) listSuggestions(ctx context.Context, specID int64) ([]*ResourceSuggestion, error) {
	rows, err := r.db.R.QueryContext(ctx, `
		SELECT spec_id, route_family, name, id_field, entity_schema, wrapper, confidence
		FROM resource_suggestions
		WHERE spec_id = ? AND route_family != ?
		  AND gen = (SELECT MAX(gen) FROM resource_suggestions WHERE spec_id = ?)
		ORDER BY route_family ASC`, specID, suggestionSentinelFamily, specID)
	if err != nil {
		return nil, fmt.Errorf("list resource suggestions for spec %d: %w", specID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []*ResourceSuggestion
	for rows.Next() {
		s, err := scanResourceSuggestion(rows)
		if err != nil {
			return nil, fmt.Errorf("scan resource suggestion row: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate resource suggestions for spec %d: %w", specID, err)
	}
	return out, nil
}

// scanner is the common surface of *sql.Row and *sql.Rows [scanResourceSuggestion] needs.
type scanner interface {
	Scan(dest ...any) error
}

// scanResourceSuggestion scans one resource_suggestions row and decodes its
// wrapper JSON back into a [Wrapper]. wrapper is NOT NULL for any row this
// function is ever asked to scan (listSuggestions' own WHERE excludes the
// one route_family = "" row that could carry a NULL wrapper) — a NULL here
// would mean the sentinel leaked past that filter, which is a bug in this
// package to fail loudly on, not a case to paper over as an empty Wrapper.
func scanResourceSuggestion(row scanner) (*ResourceSuggestion, error) {
	var (
		s           ResourceSuggestion
		wrapperJSON string
	)
	if err := row.Scan(&s.SpecID, &s.RouteFamily, &s.Name, &s.IDField, &s.EntitySchema, &wrapperJSON, &s.Confidence); err != nil {
		return nil, err
	}
	if err := jsonx.Unmarshal([]byte(wrapperJSON), &s.Wrapper); err != nil {
		return nil, fmt.Errorf("decode wrapper for %q: %w", s.RouteFamily, err)
	}
	return &s, nil
}

// RederiveResult is what [Repo.Rederive] answers (decisions.md §D4.2,
// §D13.1).
type RederiveResult struct {
	// Changed reports whether a new generation was written.
	Changed bool
	// Generation is the generation that is newest for the spec after the
	// call. On Changed == false this is the generation that already was
	// newest — never zero and never absent, so a caller reads one shape
	// either way.
	Generation int
	// Added/Removed are route_family values that entered/left the newest
	// generation across the call, sentinel excluded and computed over two
	// GENERATIONS never over what any workspace confirmed (decisions.md
	// §D4.2, §D7.2).
	Added   []string
	Removed []string
}

// genRow is one resource_suggestions row's comparable shape —
// every column [deriveSuggestions] can vary, minus spec_id and gen, which
// are the axes [Repo.Rederive] compares ACROSS rather than columns to
// diff. It is a plain comparable struct (sql.NullString itself is
// comparable) precisely so two snapshots can be compared with
// [maps.Equal] rather than a field-by-field reflection walk (decisions.md
// §D4.3: "the comparison is over the WHOLE row tuple").
type genRow struct {
	RouteFamily  string
	Name         string
	IDField      string
	EntitySchema string
	Wrapper      sql.NullString // NULL only for the sentinel row
	Confidence   float64
}

// snapshotFromSuggestions builds the comparable row set [deriveSuggestions]
// would have [insertSuggestionsGenTx] write for suggs, WITHOUT touching the
// database — the same "what would this write" shape the insert path
// itself uses, kept as a pure function so [Repo.Rederive] can compare
// against it before deciding whether to write anything at all. An empty
// suggs yields the single sentinel row, mirroring
// [insertSuggestionsGenTx]'s own empty-suggs branch (decisions.md §D4.3's
// "the sentinel row is inside the comparison").
func snapshotFromSuggestions(suggs []Suggestion) ([]genRow, error) {
	if len(suggs) == 0 {
		return []genRow{{RouteFamily: suggestionSentinelFamily, Confidence: 1.0}}, nil
	}
	out := make([]genRow, 0, len(suggs))
	for _, s := range suggs {
		wrapperJSON, err := jsonx.Marshal(s.Wrapper)
		if err != nil {
			return nil, fmt.Errorf("marshal wrapper for %q: %w", s.RouteFamily, err)
		}
		out = append(out, genRow{
			RouteFamily:  s.RouteFamily,
			Name:         s.Name,
			IDField:      s.IDField,
			EntitySchema: s.EntitySchema,
			Wrapper:      sql.NullString{String: string(wrapperJSON), Valid: true},
			Confidence:   1.0,
		})
	}
	return out, nil
}

// snapshotMap keys rows by route_family — safe because
// UNIQUE (spec_id, gen, route_family) already guarantees at most one row
// per family within one generation, so no two elements of rows can ever
// collide on this key.
func snapshotMap(rows []genRow) map[string]genRow {
	m := make(map[string]genRow, len(rows))
	for _, row := range rows {
		m[row.RouteFamily] = row
	}
	return m
}

// diffFamilies computes decisions.md §D4.2's added/removed lists: families
// present in cur and absent from prev (added), and vice versa (removed),
// sentinel excluded from both and each sorted for a deterministic response.
// prev may be nil — [Repo.Rederive]'s §D4.5 branch (a spec with no
// generation at all) has no previous generation to diff against, and a nil
// map reads as "no family was present" without a special case here.
func diffFamilies(prev, cur map[string]genRow) (added, removed []string) {
	for family := range cur {
		if family == suggestionSentinelFamily {
			continue
		}
		if _, ok := prev[family]; !ok {
			added = append(added, family)
		}
	}
	for family := range prev {
		if family == suggestionSentinelFamily {
			continue
		}
		if _, ok := cur[family]; !ok {
			removed = append(removed, family)
		}
	}
	slices.Sort(added)
	slices.Sort(removed)
	return added, removed
}

// genQuerier is the surface [newestGenerationSnapshot] needs from either
// the reader pool ([Repo.Rederive]'s pre-read) or the write transaction
// itself ([Repo.Rederive]'s in-transaction re-read) — the same
// read-through-either-pool shape [rowQuerier]/[dbQuerier] already split
// out individually; this embeds both because this one function needs both
// methods together.
type genQuerier interface {
	rowQuerier
	dbQuerier
}

// newestGenerationSnapshot reads specID's newest resource_suggestions
// generation over q — the reader pool for the pre-read taken before
// derivation, or the write transaction for the authoritative re-read
// [Repo.Rederive]'s fence compares against (decisions.md §D4.4). gen is 0
// with a nil snapshot when specID has no resource_suggestions row at all —
// the state [suggestionsExist] would answer false for, and the case
// decisions.md §D4.5 handles by always writing generation 1 unconditionally.
func newestGenerationSnapshot(ctx context.Context, q genQuerier, specID int64) (gen int, rows []genRow, err error) {
	var genNS sql.NullInt64
	if err := q.QueryRowContext(ctx, "SELECT MAX(gen) FROM resource_suggestions WHERE spec_id = ?", specID).Scan(&genNS); err != nil {
		return 0, nil, fmt.Errorf("read newest suggestion generation for spec %d: %w", specID, err)
	}
	if !genNS.Valid {
		return 0, nil, nil
	}
	gen = int(genNS.Int64)

	rset, err := q.QueryContext(ctx, `
		SELECT route_family, name, id_field, entity_schema, wrapper, confidence
		FROM resource_suggestions
		WHERE spec_id = ? AND gen = ?`, specID, gen)
	if err != nil {
		return 0, nil, fmt.Errorf("read suggestion generation %d for spec %d: %w", gen, specID, err)
	}
	defer func() { _ = rset.Close() }()

	for rset.Next() {
		var s genRow
		if err := rset.Scan(&s.RouteFamily, &s.Name, &s.IDField, &s.EntitySchema, &s.Wrapper, &s.Confidence); err != nil {
			return 0, nil, fmt.Errorf("scan suggestion generation row: %w", err)
		}
		rows = append(rows, s)
	}
	if err := rset.Err(); err != nil {
		return 0, nil, fmt.Errorf("iterate suggestion generation rows: %w", err)
	}
	return gen, rows, nil
}

// pruneGenerationsTx deletes every generation of specID older than the
// newest retention-many, inside tx — the same transaction as the insert
// that just wrote newestGen (decisions.md §D5, §D4.4). retention <= 0
// means keep every generation, the same "0 means do less" shape
// MOCKER_CHECKPOINT_RETENTION already carries; a positive retention keeps
// generations [newestGen-retention+1, newestGen] and deletes anything
// below that floor. This must be called ONLY on a call that wrote
// newestGen — decisions.md §D5, §P21: a changed:false call has nothing new
// to justify pruning history for.
func pruneGenerationsTx(ctx context.Context, tx *sql.Tx, specID int64, newestGen, retention int) error {
	if retention <= 0 {
		return nil
	}
	floor := newestGen - retention + 1
	if floor <= 1 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM resource_suggestions WHERE spec_id = ? AND gen < ?", specID, floor); err != nil {
		return fmt.Errorf("prune suggestion generations below %d for spec %d: %w", floor, specID, err)
	}
	return nil
}

// rederivePreWriteHook is a test-only seam, the same shape
// internal/resources' own confirmPreWriteHook keeps: called once, right
// after [Repo.Rederive]'s pre-read of the newest generation returns and
// right before its write transaction opens — the exact window
// [ErrStaleGeneration]'s fence exists to close. A test sets this to write a
// generation for the same spec from another connection in that window and
// restores it to rederivePreWriteHookNoop afterward, proving Rederive
// ITSELF returns ErrStaleGeneration through its own real re-read — never
// the re-read exercised in isolation, which cannot tell apart a Rederive
// that still performs it from one that silently stopped. Production never
// touches it.
var rederivePreWriteHook = rederivePreWriteHookNoop

func rederivePreWriteHookNoop() {}

// Rederive re-runs family derivation over specID's stored NORMALIZED
// document — the same bytes [Repo.backfillSuggestions] re-loads, never the
// raw upload, and never a document that can have moved under specID
// (decisions.md §D1: Import mints a new spec_id for any byte-different
// document) — and writes the result as a new generation, if and only if it
// differs from the current newest one (§D4.3, §R3: a no-op rederive writes
// no row and answers Changed: false).
//
// Derivation runs OUTSIDE the write transaction, exactly like [Repo.Import]
// and [Repo.backfillSuggestions]'s own derivation (§D4.4): loading and
// walking a document under store.DB.W's single writer connection would
// hold it for the whole parse. The newest generation is read once, over the
// reader pool, AFTER derivation and immediately before the write; it is
// re-read a second time INSIDE the write transaction, and a difference
// between the two answers [ErrStaleGeneration] and writes nothing (§D4.4,
// §D13.1) — the window [rederivePreWriteHook] exists to let a test open on
// purpose.
//
// The pre-read sits AFTER derivation rather than before it, and that is the
// order the stale check wants: the window it guards is the span between the
// two reads, so taking the first one late makes that window as small as the
// work allows. Reading before the parse would put the whole document walk
// inside it and refuse concurrent rederives that never actually raced. No
// test can tell the two orders apart — [rederivePreWriteHook] fires after
// the pre-read wherever the pre-read sits — so this comment is the only
// thing that records which one ships.
//
// A spec with no resource_suggestions row at all (§D4.5) is a case the
// comparison itself cannot express — there is no previous generation to
// diff derived against — so it is handled unconditionally: generation 1 is
// written and Changed is always true, exactly what the lazy backfill would
// have written, arriving through this verb instead.
func (r *Repo) Rederive(ctx context.Context, specID int64) (RederiveResult, error) {
	normalized, err := r.Normalized(ctx, specID)
	if err != nil {
		return RederiveResult{}, fmt.Errorf("rederive spec %d: %w", specID, err)
	}
	doc, report, err := openapi.Load(normalized)
	if err != nil {
		// The document was accepted once already, at import time (see
		// [Repo.backfillSuggestions]'s identical reasoning): a failure
		// here means the stored bytes are corrupted, a real error.
		return RederiveResult{}, fmt.Errorf("rederive: re-load normalized document for spec %d: %w", specID, err)
	}
	resolver := newSpecResolver(doc, openapi.DefaultRefBudget)
	ops, resp := Index(doc, resolver, report)
	derived := deriveSuggestions(resolver, ops, resp)

	newRows, err := snapshotFromSuggestions(derived)
	if err != nil {
		return RederiveResult{}, fmt.Errorf("rederive spec %d: %w", specID, err)
	}
	newMap := snapshotMap(newRows)

	prevGen, _, err := newestGenerationSnapshot(ctx, r.db.R, specID)
	if err != nil {
		return RederiveResult{}, fmt.Errorf("rederive spec %d: %w", specID, err)
	}

	rederivePreWriteHook()

	var result RederiveResult
	writeErr := r.db.Write(ctx, func(tx *sql.Tx) error {
		curGen, curRows, err := newestGenerationSnapshot(ctx, tx, specID)
		if err != nil {
			return err
		}
		if curGen != prevGen {
			return ErrStaleGeneration
		}

		if curGen == 0 {
			// §D4.5: nothing to compare against yet.
			if err := insertSuggestionsGenTx(ctx, tx, specID, 1, derived); err != nil {
				return err
			}
			added, removed := diffFamilies(nil, newMap)
			result = RederiveResult{Changed: true, Generation: 1, Added: added, Removed: removed}
			return pruneGenerationsTx(ctx, tx, specID, 1, r.cfg.SuggestionRetention)
		}

		curMap := snapshotMap(curRows)
		if maps.Equal(curMap, newMap) {
			result = RederiveResult{Changed: false, Generation: curGen}
			return nil
		}

		newGen := curGen + 1
		if err := insertSuggestionsGenTx(ctx, tx, specID, newGen, derived); err != nil {
			return err
		}
		added, removed := diffFamilies(curMap, newMap)
		result = RederiveResult{Changed: true, Generation: newGen, Added: added, Removed: removed}
		return pruneGenerationsTx(ctx, tx, specID, newGen, r.cfg.SuggestionRetention)
	})
	if writeErr != nil {
		return RederiveResult{}, writeErr
	}
	return result, nil
}

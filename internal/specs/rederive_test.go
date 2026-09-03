// Tests for [Repo.Rederive] (decisions.md §D4, §D9, §D13). This file is
// `package specs`, not `package specs_test` — it reaches for
// [insertSuggestionsGenTx], [newestGenerationSnapshot], [listSuggestions]
// and [rederivePreWriteHook] directly, the same white-box access
// derive_test.go already needs for [newSpecResolver].
//
// The fixture rule at the head of decisions.md §D9: the tree holds ONE
// version of [deriveSuggestions], so "the old generation did not know
// about this family" is never reproducible by running it twice. Every
// property below that needs a stale-looking old generation gets one by
// seeding it with a direct write ([reseedOnlyGeneration]/[addGeneration]),
// never by making derivation itself configurable.
package specs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testspec"
)

// rederiveEmptyDoc declares no paths at all, so [deriveSuggestions] over it
// derives nothing and [insertSuggestionsGenTx] writes only the sentinel
// row — the fixture P25 needs ("a spec that derives nothing").
const rederiveEmptyDoc = `{
  "openapi": "3.0.3",
  "info": { "title": "Empty API", "version": "1.0.0" },
  "paths": {}
}`

// newRederiveTestRepo mirrors [newDeriveTestRepo] but takes retention
// explicitly — every property in this file that touches
// [pruneGenerationsTx] needs a specific, known
// config.Config.SuggestionRetention rather than the zero value.
func newRederiveTestRepo(t *testing.T, retention int) (*Repo, *store.DB) {
	t.Helper()
	db := newDeriveTestDB(t)
	cfg := &config.Config{
		BaseDomain:          "mock.local",
		AdminHost:           "mocker.local",
		Routing:             config.RoutingHost,
		ReservedPrefix:      "/__mocker",
		AuthMode:            config.AuthShared,
		DataDir:             t.TempDir(),
		MaxBody:             10 << 20,
		MaxResponse:         4 << 20,
		RuntimeCache:        32,
		SuggestionRetention: retention,
		Dev:                 true,
	}
	return NewRepo(db, cfg), db
}

// deriveCurrent runs the exact pipeline [Repo.Import] and
// [Repo.backfillSuggestions] both run — Load -> Index -> deriveSuggestions
// — over doc, with no database involved. Tests use it to know what "the
// current rules" derive without hand-maintaining a second copy of the
// expected set.
func deriveCurrent(t *testing.T, doc []byte) []Suggestion {
	t.Helper()
	d, report, err := openapi.Load(doc)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	res := openapi.NewResolver(d, openapi.DefaultRefBudget)
	ops, resp := Index(d, res, report)
	return deriveSuggestions(res, ops, resp)
}

// reseedOnlyGeneration replaces EVERY resource_suggestions row of specID
// with exactly suggs at gen, direct SQL — the fixture rule this file's own
// package doc comment states. Used where a test needs the table to hold
// ONLY the seeded generation.
func reseedOnlyGeneration(t *testing.T, db *store.DB, specID int64, gen int, suggs []Suggestion) {
	t.Helper()
	err := db.Write(t.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), "DELETE FROM resource_suggestions WHERE spec_id = ?", specID); err != nil {
			return err
		}
		return insertSuggestionsGenTx(t.Context(), tx, specID, gen, suggs)
	})
	if err != nil {
		t.Fatalf("reseed generation %d for spec %d: %v", gen, specID, err)
	}
}

// addGeneration writes suggs at gen for specID WITHOUT touching any other
// generation already stored — the shape P9/P10/P21 need ("seed three
// generations by direct INSERT").
func addGeneration(t *testing.T, db *store.DB, specID int64, gen int, suggs []Suggestion) {
	t.Helper()
	err := db.Write(t.Context(), func(tx *sql.Tx) error {
		return insertSuggestionsGenTx(t.Context(), tx, specID, gen, suggs)
	})
	if err != nil {
		t.Fatalf("add generation %d for spec %d: %v", gen, specID, err)
	}
}

// clearSuggestions deletes every resource_suggestions row of specID,
// direct SQL, ahead of a fixture that reseeds the table from scratch.
func clearSuggestions(t *testing.T, db *store.DB, specID int64) {
	t.Helper()
	if _, err := db.W.ExecContext(t.Context(), "DELETE FROM resource_suggestions WHERE spec_id = ?", specID); err != nil {
		t.Fatalf("clear resource_suggestions for spec %d: %v", specID, err)
	}
}

// distinctGens reads SELECT DISTINCT gen ... ORDER BY gen for specID — the
// exact query several properties in this file and in derive_test.go's own
// P23 extension assert against.
func distinctGens(t *testing.T, db *store.DB, specID int64) []int {
	t.Helper()
	rows, err := db.R.QueryContext(t.Context(), "SELECT DISTINCT gen FROM resource_suggestions WHERE spec_id = ? ORDER BY gen ASC", specID)
	if err != nil {
		t.Fatalf("distinct gens for spec %d: %v", specID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []int
	for rows.Next() {
		var g int
		if err := rows.Scan(&g); err != nil {
			t.Fatalf("scan gen: %v", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate gens: %v", err)
	}
	return out
}

// dumpGeneration reads every column of specID's rows at exactly gen, each
// row formatted as one comparable string, ordered by route_family — used
// to assert an OLDER generation's rows are byte-identical across a call
// that must not have touched them (P2's own clause).
func dumpGeneration(t *testing.T, db *store.DB, specID int64, gen int) []string {
	t.Helper()
	rows, err := db.R.QueryContext(t.Context(), `
		SELECT route_family, name, id_field, entity_schema, wrapper, confidence
		FROM resource_suggestions WHERE spec_id = ? AND gen = ? ORDER BY route_family ASC`, specID, gen)
	if err != nil {
		t.Fatalf("dump generation %d for spec %d: %v", gen, specID, err)
	}
	defer func() { _ = rows.Close() }()
	return scanSuggestionDump(t, rows)
}

// dumpAllGenerations is [dumpGeneration] over EVERY generation of specID
// at once, gen carried in each row's own formatted string — used where a
// property must show a WHOLE spec's history stayed byte-identical (P9's
// "the other spec"), not just one generation of it.
func dumpAllGenerations(t *testing.T, db *store.DB, specID int64) []string {
	t.Helper()
	rows, err := db.R.QueryContext(t.Context(), `
		SELECT gen, route_family, name, id_field, entity_schema, wrapper, confidence
		FROM resource_suggestions WHERE spec_id = ? ORDER BY gen ASC, route_family ASC`, specID)
	if err != nil {
		t.Fatalf("dump all generations for spec %d: %v", specID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var (
			gen                           int
			family, name, idField, schema string
			wrapper                       sql.NullString
			confidence                    float64
		)
		if err := rows.Scan(&gen, &family, &name, &idField, &schema, &wrapper, &confidence); err != nil {
			t.Fatalf("scan resource_suggestions row: %v", err)
		}
		out = append(out, fmt.Sprintf("%d|%s|%s|%s|%s|%v|%s|%g", gen, family, name, idField, schema, wrapper.Valid, wrapper.String, confidence))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate resource_suggestions for spec %d: %v", specID, err)
	}
	return out
}

// scanSuggestionDump is [dumpGeneration]'s row-scanning half, split out so
// a future caller that already has a *sql.Rows over the same six columns
// (none exists yet) does not have to duplicate it.
func scanSuggestionDump(t *testing.T, rows *sql.Rows) []string {
	t.Helper()
	var out []string
	for rows.Next() {
		var (
			family, name, idField, schema string
			wrapper                       sql.NullString
			confidence                    float64
		)
		if err := rows.Scan(&family, &name, &idField, &schema, &wrapper, &confidence); err != nil {
			t.Fatalf("scan resource_suggestions row: %v", err)
		}
		out = append(out, fmt.Sprintf("%s|%s|%s|%s|%v|%s|%g", family, name, idField, schema, wrapper.Valid, wrapper.String, confidence))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate resource_suggestions rows: %v", err)
	}
	return out
}

// familiesInGeneration reads only route_family for specID at gen, raw
// (sentinel included, no exclusion) — used where a test seeded the row set
// itself and wants to assert exactly what landed there.
func familiesInGeneration(t *testing.T, db *store.DB, specID int64, gen int) []string {
	t.Helper()
	rows, err := db.R.QueryContext(t.Context(),
		"SELECT route_family FROM resource_suggestions WHERE spec_id = ? AND gen = ? ORDER BY route_family ASC", specID, gen)
	if err != nil {
		t.Fatalf("families in generation %d for spec %d: %v", gen, specID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var family string
		if err := rows.Scan(&family); err != nil {
			t.Fatalf("scan route_family: %v", err)
		}
		out = append(out, family)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate route_family rows: %v", err)
	}
	return out
}

// countWithParentFamily is P18's own assertion in query form: how many of
// specID's resource_suggestions rows, across EVERY generation, carry a
// non-NULL parent_family.
func countWithParentFamily(t *testing.T, db *store.DB, specID int64) int {
	t.Helper()
	var n int
	if err := db.R.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM resource_suggestions WHERE spec_id = ? AND parent_family IS NOT NULL", specID).Scan(&n); err != nil {
		t.Fatalf("count parent_family rows for spec %d: %v", specID, err)
	}
	return n
}

// rawSuggestionRow is [Suggestion] plus Confidence — a Go value Suggestion
// itself never carries (decisions.md §D3: confidence is always the
// constant 1.0 at derivation time), needed only by [TestRederive_P5] to
// seed a generation whose confidence differs from what production ever
// writes.
type rawSuggestionRow struct {
	RouteFamily, Name, IDField, EntitySchema, WrapperJSON string
	Confidence                                            float64
}

// toRawRows converts suggs — [deriveCurrent]'s own output — into
// [rawSuggestionRow]s at the production confidence (1.0), the same JSON
// [insertSuggestionsGenTx] itself would marshal.
func toRawRows(t *testing.T, suggs []Suggestion) []rawSuggestionRow {
	t.Helper()
	out := make([]rawSuggestionRow, 0, len(suggs))
	for _, s := range suggs {
		wrapperJSON, err := jsonx.Marshal(s.Wrapper)
		if err != nil {
			t.Fatalf("marshal wrapper for %q: %v", s.RouteFamily, err)
		}
		out = append(out, rawSuggestionRow{
			RouteFamily: s.RouteFamily, Name: s.Name, IDField: s.IDField, EntitySchema: s.EntitySchema,
			WrapperJSON: string(wrapperJSON), Confidence: 1.0,
		})
	}
	return out
}

// seedGen1Raw replaces every resource_suggestions row of specID with rows,
// verbatim, at generation 1 — direct SQL, bypassing [insertSuggestionsGenTx]
// entirely, because that helper always writes confidence 1.0 and
// [TestRederive_P5] needs one case that does not.
func seedGen1Raw(t *testing.T, db *store.DB, specID int64, rows []rawSuggestionRow) {
	t.Helper()
	err := db.Write(t.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), "DELETE FROM resource_suggestions WHERE spec_id = ?", specID); err != nil {
			return err
		}
		for _, row := range rows {
			if _, err := tx.ExecContext(t.Context(), `
				INSERT INTO resource_suggestions (spec_id, gen, route_family, name, id_field, entity_schema, wrapper, confidence)
				VALUES (?, 1, ?, ?, ?, ?, ?, ?)`,
				specID, row.RouteFamily, row.Name, row.IDField, row.EntitySchema, row.WrapperJSON, row.Confidence); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed raw generation 1 for spec %d: %v", specID, err)
	}
}

// docWithSuffix returns doc with an irrelevant trailing field spliced in
// right before its closing brace, changing its sha256 (so [Repo.Import]
// mints a genuinely new spec_id) without changing what it declares.
func docWithSuffix(doc []byte, field string) []byte {
	return append(slices.Clone(doc[:len(doc)-1]), []byte(","+field+"}")...)
}

// familyByName picks the one [Suggestion] named family out of suggs, or
// fails the test — several properties below need to isolate exactly one
// derived family out of [deriveCurrent]'s output.
func familyByName(t *testing.T, suggs []Suggestion, family string) Suggestion {
	t.Helper()
	for _, s := range suggs {
		if s.RouteFamily == family {
			return s
		}
	}
	t.Fatalf("no suggestion named %q in %v", family, suggs)
	return Suggestion{}
}

// --- P1 -----------------------------------------------------------------

// TestRederive_P1_noopWritesNothing is decisions.md §D9 P1: a rederive over
// an unchanged derivation writes nothing. *Fails if a second rederive over
// an untouched tree mints a generation, or if Changed is true while no row
// was written.*
func TestRederive_P1_noopWritesNothing(t *testing.T) {
	r, db := newRederiveTestRepo(t, 0)
	imp, err := r.Import(t.Context(), ImportInput{Name: "p1", Source: "upload", Document: testspec.DerivationDoc()})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	specID := imp.Spec.ID

	if _, err := r.Rederive(t.Context(), specID); err != nil {
		t.Fatalf("first rederive: %v", err)
	}
	result, err := r.Rederive(t.Context(), specID)
	if err != nil {
		t.Fatalf("second rederive: %v", err)
	}
	if result.Changed {
		t.Errorf("second rederive Changed = true, want false")
	}
	if result.Generation != 1 {
		t.Errorf("second rederive Generation = %d, want 1", result.Generation)
	}
	if got := distinctGens(t, db, specID); !slices.Equal(got, []int{1}) {
		t.Errorf("DISTINCT gen = %v, want [1]", got)
	}
}

// --- P2 -----------------------------------------------------------------

// TestRederive_P2_mintsFreshDerivedNotCopyOfPrevious is decisions.md §D9
// P2: a rederive mints the DERIVER's output, not a copy of the previous
// generation, and the previous generation's own rows stay byte-identical
// across the call. *Fails if a family the current rules derive is absent
// from the new generation, or if any row of an EARLIER generation differs
// across the call.*
func TestRederive_P2_mintsFreshDerivedNotCopyOfPrevious(t *testing.T) {
	r, db := newRederiveTestRepo(t, 0)
	imp, err := r.Import(t.Context(), ImportInput{Name: "p2", Source: "upload", Document: testspec.NestedDerivationDoc()})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	specID := imp.Spec.ID

	full := deriveCurrent(t, testspec.NestedDerivationDoc())
	narrow := []Suggestion{familyByName(t, full, testspec.FamilyOrgs)}
	reseedOnlyGeneration(t, db, specID, 1, narrow)

	before := dumpGeneration(t, db, specID, 1)

	result, err := r.Rederive(t.Context(), specID)
	if err != nil {
		t.Fatalf("rederive: %v", err)
	}
	if !result.Changed || result.Generation != 2 {
		t.Fatalf("rederive = %+v, want Changed:true Generation:2", result)
	}

	list, err := r.listSuggestions(t.Context(), specID)
	if err != nil {
		t.Fatalf("listSuggestions: %v", err)
	}
	var haveDepth1 bool
	for _, s := range list {
		if s.RouteFamily == testspec.FamilyOrgDepartments {
			haveDepth1 = true
		}
	}
	if !haveDepth1 {
		t.Errorf("listing after rederive does not name %q", testspec.FamilyOrgDepartments)
	}

	after := dumpGeneration(t, db, specID, 1)
	if !slices.Equal(before, after) {
		t.Errorf("generation 1's rows changed across the call:\nbefore %v\nafter  %v", before, after)
	}
}

// --- P3 -----------------------------------------------------------------

// TestRederive_P3_listingReturnsOnlyNewestGeneration is decisions.md §D9
// P3: the listing returns only the newest generation. *Fails if a
// route_family present only in an older generation is returned by the
// listing.*
func TestRederive_P3_listingReturnsOnlyNewestGeneration(t *testing.T) {
	r, db := newRederiveTestRepo(t, 0)
	imp, err := r.Import(t.Context(), ImportInput{Name: "p3", Source: "upload", Document: testspec.DerivationDoc()})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	specID := imp.Spec.ID

	current := deriveCurrent(t, testspec.DerivationDoc())
	const droppedFamily = "/not-really-derivable"
	wide := append(slices.Clone(current), Suggestion{
		RouteFamily: droppedFamily, Name: "stale", IDField: "id", EntitySchema: "#/components/schemas/Stale",
	})
	reseedOnlyGeneration(t, db, specID, 1, wide)

	result, err := r.Rederive(t.Context(), specID)
	if err != nil {
		t.Fatalf("rederive: %v", err)
	}
	if !result.Changed {
		t.Fatalf("rederive Changed = false, want true")
	}

	list, err := r.listSuggestions(t.Context(), specID)
	if err != nil {
		t.Fatalf("listSuggestions: %v", err)
	}
	names := make(map[string]bool, len(list))
	for _, s := range list {
		names[s.RouteFamily] = true
	}
	if names[droppedFamily] {
		t.Errorf("listing still names %q, which only the older generation carried", droppedFamily)
	}
	for _, s := range current {
		if !names[s.RouteFamily] {
			t.Errorf("listing missing current family %q", s.RouteFamily)
		}
	}
}

// --- P5 -----------------------------------------------------------------

// TestRederive_P5_wholeRowTupleCompared is decisions.md §D9 P5: the no-op
// comparison reads the whole row, not the family name — once per non-key
// column (name, id_field, entity_schema, wrapper, confidence), a
// generation 1 holding the SAME families as the current derivation with
// that ONE column differing must still be judged changed. *Fails if a
// difference in any single non-key column is judged identical to the
// current derivation.*
func TestRederive_P5_wholeRowTupleCompared(t *testing.T) {
	current := deriveCurrent(t, testspec.DerivationDoc())

	cases := []struct {
		name   string
		mutate func(rawSuggestionRow) rawSuggestionRow
	}{
		{"name", func(row rawSuggestionRow) rawSuggestionRow { row.Name += "-stale"; return row }},
		{"id_field", func(row rawSuggestionRow) rawSuggestionRow { row.IDField += "Stale"; return row }},
		{"entity_schema", func(row rawSuggestionRow) rawSuggestionRow { row.EntitySchema += "/stale"; return row }},
		{"wrapper", func(row rawSuggestionRow) rawSuggestionRow {
			row.WrapperJSON = `{"arrayKey":"stale","countKey":null,"countType":"","idType":"integer"}`
			return row
		}},
		{"confidence", func(row rawSuggestionRow) rawSuggestionRow { row.Confidence = 0.5; return row }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, db := newRederiveTestRepo(t, 0)
			imp, err := r.Import(t.Context(), ImportInput{Name: "p5-" + tc.name, Source: "upload", Document: testspec.DerivationDoc()})
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			specID := imp.Spec.ID

			rows := toRawRows(t, current)
			for i, row := range rows {
				if row.RouteFamily == testspec.FamilyWidgets {
					rows[i] = tc.mutate(row)
				}
			}
			seedGen1Raw(t, db, specID, rows)

			result, err := r.Rederive(t.Context(), specID)
			if err != nil {
				t.Fatalf("rederive: %v", err)
			}
			if !result.Changed {
				t.Errorf("Changed = false, want true")
			}
			if result.Generation != 2 {
				t.Errorf("Generation = %d, want 2", result.Generation)
			}
		})
	}
}

// --- P9 / P10 / P21 -------------------------------------------------------

// TestRederive_P9_retentionKeepsNewestPerSpec is decisions.md §D9 P9:
// retention keeps N generations, prunes the OLDEST, and prunes only the
// spec it was called for. *Fails if more than the limit survives for the
// rederived spec, if a surviving generation is older than a pruned one, or
// if any row of another spec's generations changes.*
func TestRederive_P9_retentionKeepsNewestPerSpec(t *testing.T) {
	r, db := newRederiveTestRepo(t, 2)
	impA, err := r.Import(t.Context(), ImportInput{Name: "specA", Source: "upload", Document: testspec.DerivationDoc()})
	if err != nil {
		t.Fatalf("import A: %v", err)
	}
	impB, err := r.Import(t.Context(), ImportInput{Name: "specB", Source: "upload", Document: docWithSuffix(testspec.DerivationDoc(), `"x-specb":true`)})
	if err != nil {
		t.Fatalf("import B: %v", err)
	}

	current := deriveCurrent(t, testspec.DerivationDoc())
	widgetsOnly := []Suggestion{familyByName(t, current, testspec.FamilyWidgets)}

	for _, specID := range []int64{impA.Spec.ID, impB.Spec.ID} {
		clearSuggestions(t, db, specID)
		addGeneration(t, db, specID, 1, current)
		addGeneration(t, db, specID, 2, current)
		addGeneration(t, db, specID, 3, widgetsOnly)
	}

	beforeB := dumpAllGenerations(t, db, impB.Spec.ID)

	result, err := r.Rederive(t.Context(), impA.Spec.ID)
	if err != nil {
		t.Fatalf("rederive A: %v", err)
	}
	if !result.Changed || result.Generation != 4 {
		t.Fatalf("rederive A = %+v, want Changed:true Generation:4", result)
	}

	if gensA := distinctGens(t, db, impA.Spec.ID); !slices.Equal(gensA, []int{3, 4}) {
		t.Errorf("spec A gens after rederive = %v, want [3 4]", gensA)
	}

	afterB := dumpAllGenerations(t, db, impB.Spec.ID)
	if !slices.Equal(beforeB, afterB) {
		t.Errorf("spec B's generations changed across a rederive of a different spec:\nbefore %v\nafter  %v", beforeB, afterB)
	}
}

// TestRederive_P10_zeroRetentionPrunesNothing is decisions.md §D9 P10: a
// retention of 0 prunes nothing. *Fails if a zero retention deletes a
// generation.*
func TestRederive_P10_zeroRetentionPrunesNothing(t *testing.T) {
	r, db := newRederiveTestRepo(t, 0)
	imp, err := r.Import(t.Context(), ImportInput{Name: "p10", Source: "upload", Document: testspec.DerivationDoc()})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	specID := imp.Spec.ID

	current := deriveCurrent(t, testspec.DerivationDoc())
	widgetsOnly := []Suggestion{familyByName(t, current, testspec.FamilyWidgets)}

	clearSuggestions(t, db, specID)
	addGeneration(t, db, specID, 1, current)
	addGeneration(t, db, specID, 2, current)
	addGeneration(t, db, specID, 3, widgetsOnly)

	result, err := r.Rederive(t.Context(), specID)
	if err != nil {
		t.Fatalf("rederive: %v", err)
	}
	if !result.Changed || result.Generation != 4 {
		t.Fatalf("rederive = %+v, want Changed:true Generation:4", result)
	}

	if gens := distinctGens(t, db, specID); !slices.Equal(gens, []int{1, 2, 3, 4}) {
		t.Errorf("gens after zero-retention rederive = %v, want [1 2 3 4] — all three original generations must survive", gens)
	}
}

// TestRederive_P21_pruneOnlyOnWrite is decisions.md §D9 P21: the prune runs
// only on a call that WROTE a generation. Three generations are SEEDED by
// direct INSERT under a limit of 2, so the table already sits above the
// limit with no verb having run; a rederive whose derivation equals the
// newest generation must leave every row exactly as it was. *Fails if a
// call answering changed:false deletes a row.*
func TestRederive_P21_pruneOnlyOnWrite(t *testing.T) {
	r, db := newRederiveTestRepo(t, 2)
	imp, err := r.Import(t.Context(), ImportInput{Name: "p21", Source: "upload", Document: testspec.DerivationDoc()})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	specID := imp.Spec.ID

	current := deriveCurrent(t, testspec.DerivationDoc())
	clearSuggestions(t, db, specID)
	addGeneration(t, db, specID, 1, current)
	addGeneration(t, db, specID, 2, current)
	addGeneration(t, db, specID, 3, current) // gen 3 == current: the next rederive is a no-op

	before := dumpAllGenerations(t, db, specID)

	result, err := r.Rederive(t.Context(), specID)
	if err != nil {
		t.Fatalf("rederive: %v", err)
	}
	if result.Changed {
		t.Errorf("Changed = true, want false")
	}
	if result.Generation != 3 {
		t.Errorf("Generation = %d, want 3", result.Generation)
	}

	after := dumpAllGenerations(t, db, specID)
	if !slices.Equal(before, after) {
		t.Errorf("a changed:false call altered stored rows:\nbefore %v\nafter  %v", before, after)
	}
	if gens := distinctGens(t, db, specID); !slices.Equal(gens, []int{1, 2, 3}) {
		t.Errorf("gens after a no-op call = %v, want [1 2 3] — retention must not fire", gens)
	}
}

// --- P11 ------------------------------------------------------------------

// TestRederive_P11_staleGenerationRefused is decisions.md §D9 P11: a stale
// observed generation is refused on the path a request takes. A hook fires
// between Rederive's pre-read and its write transaction and writes a
// generation from another connection in that window. *Fails if a
// generation written between the pre-read and the transaction does not
// produce the refusal, or if a refused call leaves a row behind.*
func TestRederive_P11_staleGenerationRefused(t *testing.T) {
	r, db := newRederiveTestRepo(t, 0)
	imp, err := r.Import(t.Context(), ImportInput{Name: "p11", Source: "upload", Document: testspec.DerivationDoc()})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	specID := imp.Spec.ID

	interloper := []Suggestion{{RouteFamily: "/interloper", Name: "interloper", IDField: "id", EntitySchema: "#/x"}}

	orig := rederivePreWriteHook
	t.Cleanup(func() { rederivePreWriteHook = orig })
	rederivePreWriteHook = func() {
		if err := db.Write(context.Background(), func(tx *sql.Tx) error {
			return insertSuggestionsGenTx(context.Background(), tx, specID, 2, interloper)
		}); err != nil {
			t.Fatalf("interloper write: %v", err)
		}
	}

	_, err = r.Rederive(t.Context(), specID)
	if !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("Rederive error = %v, want ErrStaleGeneration", err)
	}

	if gens := distinctGens(t, db, specID); !slices.Equal(gens, []int{1, 2}) {
		t.Fatalf("gens after refused call = %v, want [1 2] (import's own plus the interloper's)", gens)
	}
	if families := familiesInGeneration(t, db, specID, 2); !slices.Equal(families, []string{"/interloper"}) {
		t.Errorf("generation 2 families = %v, want exactly the interloper's own", families)
	}
}

// --- P18 --------------------------------------------------------------

// TestRederive_P18_parentFamilyStaysNull is decisions.md §D9 P18:
// resource_suggestions.parent_family stays NULL, across EVERY generation,
// even for a rederive that mints a new one over a nested family. *Fails if
// any row of any generation carries a non-NULL parent_family.*
func TestRederive_P18_parentFamilyStaysNull(t *testing.T) {
	r, db := newRederiveTestRepo(t, 0)
	imp, err := r.Import(t.Context(), ImportInput{Name: "p18", Source: "upload", Document: testspec.NestedDerivationDoc()})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	specID := imp.Spec.ID

	full := deriveCurrent(t, testspec.NestedDerivationDoc())
	narrow := []Suggestion{familyByName(t, full, testspec.FamilyOrgs)}
	reseedOnlyGeneration(t, db, specID, 1, narrow)

	result, err := r.Rederive(t.Context(), specID)
	if err != nil {
		t.Fatalf("rederive: %v", err)
	}
	if !result.Changed || result.Generation != 2 {
		t.Fatalf("rederive = %+v, want Changed:true Generation:2", result)
	}

	if n := countWithParentFamily(t, db, specID); n != 0 {
		t.Errorf("parent_family populated on %d rows across generations, want 0", n)
	}
}

// --- P20 --------------------------------------------------------------

// TestRederive_P20_noRowsAtAllWritesGeneration1 is decisions.md §D9 P20: a
// rederive over a spec with no suggestion rows at all writes generation 1.
// *Fails if the generation is not 1, or if the call reports no change over
// a spec that had no rows.*
func TestRederive_P20_noRowsAtAllWritesGeneration1(t *testing.T) {
	r, db := newRederiveTestRepo(t, 0)
	imp, err := r.Import(t.Context(), ImportInput{Name: "p20", Source: "upload", Document: testspec.DerivationDoc()})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	specID := imp.Spec.ID
	clearSuggestions(t, db, specID)

	result, err := r.Rederive(t.Context(), specID)
	if err != nil {
		t.Fatalf("rederive: %v", err)
	}
	if !result.Changed || result.Generation != 1 {
		t.Fatalf("rederive = %+v, want Changed:true Generation:1", result)
	}

	current := deriveCurrent(t, testspec.DerivationDoc())
	wantFamilies := make([]string, len(current))
	for i, s := range current {
		wantFamilies[i] = s.RouteFamily
	}
	slices.Sort(wantFamilies)

	gotAdded := slices.Clone(result.Added)
	slices.Sort(gotAdded)
	if !slices.Equal(gotAdded, wantFamilies) {
		t.Errorf("Added = %v, want %v", gotAdded, wantFamilies)
	}
	if len(result.Removed) != 0 {
		t.Errorf("Removed = %v, want empty", result.Removed)
	}

	list, err := r.listSuggestions(t.Context(), specID)
	if err != nil {
		t.Fatalf("listSuggestions: %v", err)
	}
	gotList := make([]string, len(list))
	for i, s := range list {
		gotList[i] = s.RouteFamily
	}
	slices.Sort(gotList)
	if !slices.Equal(gotList, wantFamilies) {
		t.Errorf("listing after rederive = %v, want %v", gotList, wantFamilies)
	}
}

// --- P25 --------------------------------------------------------------

// TestRederive_P25_derivesNothingTwiceReportsNoChange is decisions.md §D9
// P25: a spec that derives nothing, rederived, reports no change. *Fails
// if a spec that derives nothing reports a change on a second rederive, or
// if a second generation appears.*
func TestRederive_P25_derivesNothingTwiceReportsNoChange(t *testing.T) {
	r, db := newRederiveTestRepo(t, 0)
	imp, err := r.Import(t.Context(), ImportInput{Name: "p25", Source: "upload", Document: []byte(rederiveEmptyDoc)})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	specID := imp.Spec.ID

	if _, err := r.Rederive(t.Context(), specID); err != nil {
		t.Fatalf("first rederive: %v", err)
	}
	result, err := r.Rederive(t.Context(), specID)
	if err != nil {
		t.Fatalf("second rederive: %v", err)
	}
	if result.Changed {
		t.Errorf("Changed = true, want false")
	}
	if len(result.Added) != 0 || len(result.Removed) != 0 {
		t.Errorf("Added/Removed = %v/%v, want both empty", result.Added, result.Removed)
	}
	if gens := distinctGens(t, db, specID); !slices.Equal(gens, []int{1}) {
		t.Errorf("gens = %v, want [1]", gens)
	}
}

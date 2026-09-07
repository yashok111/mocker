// Tests for internal/checkpoints.
//
// This file is `package checkpoints`, not `package checkpoints_test`, for
// one reason: C18's ceiling is a package-level var and §G obs 20 has to
// lower it. That also fixes the whole PACKAGE's concurrency rule — NOT ONE
// TEST across any of this package's _test.go files calls t.Parallel. Every
// bar runs -race, and a package-level var written by one test while a
// parallel sibling reads it (which every test touching Create or Get does)
// is a detected data race, in the one package whose tests may not be
// weakened to make a bar green. Adding t.Parallel to any test in this
// package re-opens exactly that.
//
// helpers_test.go holds the shared harness every other repo_*_test.go file
// in this package draws on: insertSpec/insertUser/insertWorkspace (raw
// fixture rows), the `fixture` type and its methods (the one workspace this
// package's own tests exercise end to end), the resource/entity fixture
// helpers (`resourceFixture`, insertResource/insertEntities/…), and small
// comparison helpers (mapsEqual, scopedMapsEqual, settingsWith). Originally
// one 3459-line repo_test.go, split by concern — mechanical moves only, no
// assertion changed — once that size made the file hard to navigate.
package checkpoints

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testkit"
)

func insertSpec(t *testing.T, db *store.DB, name, hash string) int64 {
	t.Helper()
	res, err := db.W.ExecContext(t.Context(), `
		INSERT INTO specs (name, format, source, hash, raw, normalized, created_at)
		VALUES (?, 'oas31', 'upload', ?, '{}', '{}', unixepoch())`, name, hash)
	if err != nil {
		t.Fatalf("insert spec %q: %v", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("spec id: %v", err)
	}
	return id
}

func insertUser(t *testing.T, db *store.DB, name string) int64 {
	t.Helper()
	res, err := db.W.ExecContext(t.Context(),
		"INSERT INTO users (name, created_at) VALUES (?, unixepoch())", name)
	if err != nil {
		t.Fatalf("insert user %q: %v", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("user id: %v", err)
	}
	return id
}

func insertWorkspace(t *testing.T, db *store.DB, slug string, specID *int64, settings domain.Settings) int64 {
	t.Helper()
	settingsJSON, err := settings.MarshalJSONStable()
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	res, err := db.W.ExecContext(t.Context(), `
		INSERT INTO workspaces (slug, name, spec_id, revision, settings, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, unixepoch(), unixepoch())`, slug, slug, specID, string(settingsJSON))
	if err != nil {
		t.Fatalf("insert workspace %q: %v", slug, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	return id
}

type fixture struct {
	db     *store.DB
	repo   *Repo
	ovr    *overrides.Repo
	cep    *customep.Repo
	wsID   int64
	userID int64
}

func newFixture(t *testing.T, retention int) *fixture {
	t.Helper()
	db := testkit.NewDB(t)
	specID := insertSpec(t, db, "fixture", "hash-1")
	settings := domain.DefaultSettings()
	settings.ListSize = 5
	settings.Normalize()
	wsID := insertWorkspace(t, db, "ws-a", &specID, settings)
	ovr := overrides.NewRepo(db)
	cep := customep.NewRepo(db)
	return &fixture{
		db:     db,
		repo:   NewRepo(db, ovr, cep, retention),
		ovr:    ovr,
		cep:    cep,
		wsID:   wsID,
		userID: insertUser(t, db, "operator"),
	}
}

// pinStatus writes an op_overrides row pinning GET /thing to status, the
// same shape the admin "pin a status" button produces.
func (f *fixture) pinStatus(t *testing.T, status int) {
	t.Helper()
	_, _, err := f.ovr.Put(t.Context(), f.wsID, overrides.OpKey("GET", "/thing"), func(row *overrides.Row) error {
		s := status
		row.OverrideOn = true
		row.ActiveStatus = &s
		return nil
	})
	if err != nil {
		t.Fatalf("pin status %d: %v", status, err)
	}
}

// pinnedStatus reads back what pinStatus wrote, or nil when the row is gone.
func (f *fixture) pinnedStatus(t *testing.T) *int {
	t.Helper()
	row, err := f.ovr.Get(t.Context(), f.wsID, overrides.OpKey("GET", "/thing"))
	if errors.Is(err, overrides.ErrNotFound) {
		return nil
	}
	if err != nil {
		t.Fatalf("read override: %v", err)
	}
	return row.ActiveStatus
}

func (f *fixture) createEndpoint(t *testing.T, workspaceID int64, method, path string) *customep.Row {
	t.Helper()
	row, err := f.cep.Create(t.Context(), workspaceID, &customep.Row{Method: method, Path: path, ActiveStatus: 200})
	if err != nil {
		t.Fatalf("create endpoint %s %s: %v", method, path, err)
	}
	return row
}

func (f *fixture) endpoints(t *testing.T) []*customep.Row {
	t.Helper()
	rows, err := f.cep.ForWorkspace(t.Context(), f.wsID)
	if err != nil {
		t.Fatalf("list endpoints: %v", err)
	}
	return rows
}

func (f *fixture) revision(t *testing.T) int64 {
	t.Helper()
	var rev int64
	if err := f.db.R.QueryRowContext(t.Context(),
		"SELECT revision FROM workspaces WHERE id = ?", f.wsID).Scan(&rev); err != nil {
		t.Fatalf("read revision: %v", err)
	}
	return rev
}

func (f *fixture) settings(t *testing.T) domain.Settings {
	t.Helper()
	var raw string
	if err := f.db.R.QueryRowContext(t.Context(),
		"SELECT settings FROM workspaces WHERE id = ?", f.wsID).Scan(&raw); err != nil {
		t.Fatalf("read settings: %v", err)
	}
	s, err := domain.ParseSettings([]byte(raw))
	if err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	return s
}

// editVersion reads the workspaces row's edit_version — A3's per-row
// compare-and-swap token — directly off the column, standing in for the
// PATCH handler this package must not import (C10's same rule D9 restates
// for A3: internal/workspaces is not in this slice's file list).
func (f *fixture) editVersion(t *testing.T) int64 {
	t.Helper()
	var v int64
	if err := f.db.R.QueryRowContext(t.Context(),
		"SELECT edit_version FROM workspaces WHERE id = ?", f.wsID).Scan(&v); err != nil {
		t.Fatalf("read edit_version: %v", err)
	}
	return v
}

func (f *fixture) list(t *testing.T) []Summary {
	t.Helper()
	out, err := f.repo.List(t.Context(), f.wsID)
	if err != nil {
		t.Fatalf("list checkpoints: %v", err)
	}
	return out
}

// machineMade returns the ids of the workspace's machine-made checkpoints,
// oldest first — the population C7's retention rule operates on.
func (f *fixture) machineMade(t *testing.T) []int64 {
	t.Helper()
	var ids []int64
	for _, s := range f.list(t) {
		if s.Kind != KindManual {
			ids = append(ids, s.ID)
		}
	}
	// List is newest-first; reverse so assertions read chronologically.
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i]
	}
	return ids
}

func (f *fixture) storedBlob(t *testing.T, checkpointID int64) []byte {
	t.Helper()
	var blob []byte
	if err := f.db.R.QueryRowContext(t.Context(),
		"SELECT config_snap FROM checkpoints WHERE id = ?", checkpointID).Scan(&blob); err != nil {
		t.Fatalf("read config_snap %d: %v", checkpointID, err)
	}
	return blob
}

func (f *fixture) create(t *testing.T, label string) *Summary {
	t.Helper()
	s, err := f.repo.Create(t.Context(), f.wsID, label, f.userID)
	if err != nil {
		t.Fatalf("create checkpoint %q: %v", label, err)
	}
	return s
}

// auto calls Repo.Auto with the fixture's user, failing the test only on an
// ERROR — a nil *Summary is (nil, nil), Auto's ordinary way of reporting
// window suppression (SIG-AUTO), and callers that expect a write assert on
// it themselves rather than have this helper hide it.
func (f *fixture) auto(t *testing.T, label string, window int) *Summary {
	t.Helper()
	s, err := f.repo.Auto(t.Context(), f.wsID, label, f.userID, window)
	if err != nil {
		t.Fatalf("auto checkpoint %q: %v", label, err)
	}
	return s
}

// ageAutoCheckpoint pushes checkpointID's stored created_at back by
// seconds, direct SQL rather than a real sleep — the same technique
// insertWorkspace and friends already use to build rows a test can put in a
// known state without waiting on the wall clock.
func (f *fixture) ageAutoCheckpoint(t *testing.T, checkpointID int64, seconds int64) {
	t.Helper()
	if _, err := f.db.W.ExecContext(t.Context(),
		"UPDATE checkpoints SET created_at = created_at - ? WHERE id = ?", seconds, checkpointID); err != nil {
		t.Fatalf("age checkpoint %d: %v", checkpointID, err)
	}
}

// rollback calls Repo.Rollback with restoreData:false — every pre-P3d
// caller in this file reaches the repo through this helper, and D10's own
// table is explicit that it stays on the false path after the ripple: two
// guards (TestRestore_neverTouchesAnEntityRow and
// TestRollback_restoresAResourceDeclinedAwaySinceTheSnapshot) assert
// against exactly this path and must keep passing UNEDITED.
func (f *fixture) rollback(t *testing.T, checkpointID int64) Outcome {
	t.Helper()
	out, err := f.repo.Rollback(t.Context(), f.wsID, checkpointID, f.userID, false, "")
	if err != nil {
		t.Fatalf("rollback to %d: %v", checkpointID, err)
	}
	return out
}

// rollbackData is rollback's restoreData:true sibling, for the P3d fixtures
// that exercise the entity-restore path. confirmSlug is always the
// fixture's own slug ("ws-a", set by newFixture) — the happy path; the
// refusal fixtures for a missing or mismatched slug call f.repo.Rollback
// directly, the way the pre-P3d atomicity tests already call it for other
// refusals.
func (f *fixture) rollbackData(t *testing.T, checkpointID int64) Outcome {
	t.Helper()
	out, err := f.repo.Rollback(t.Context(), f.wsID, checkpointID, f.userID, true, f.slug(t))
	if err != nil {
		t.Fatalf("rollback (restoreData:true) to %d: %v", checkpointID, err)
	}
	return out
}

// slug reads the fixture's own workspace slug — D7's confirmSlug argument
// compares against exactly this column, never against what a caller names.
func (f *fixture) slug(t *testing.T) string {
	t.Helper()
	var slug string
	if err := f.db.R.QueryRowContext(t.Context(),
		"SELECT slug FROM workspaces WHERE id = ?", f.wsID).Scan(&slug); err != nil {
		t.Fatalf("read slug: %v", err)
	}
	return slug
}

// decodedData reads a checkpoint's data_snap and decodes it, failing the
// test outright if the column is NULL — every fixture that reaches for this
// helper expects a capture that actually succeeded (D5: always-capture), so
// a NULL here is itself the failure being reported, not a case to branch
// on.
func (f *fixture) decodedData(t *testing.T, checkpointID int64) bundle.DataBundle {
	t.Helper()
	var dataSnap []byte
	if err := f.db.R.QueryRowContext(t.Context(),
		"SELECT data_snap FROM checkpoints WHERE id = ?", checkpointID).Scan(&dataSnap); err != nil {
		t.Fatalf("read data_snap of checkpoint %d: %v", checkpointID, err)
	}
	if dataSnap == nil {
		t.Fatalf("data_snap of checkpoint %d is NULL, want a document", checkpointID)
	}
	doc, err := decompressSnapshot(dataSnap)
	if err != nil {
		t.Fatalf("decompress data_snap of checkpoint %d: %v", checkpointID, err)
	}
	data, err := bundle.DecodeData(doc)
	if err != nil {
		t.Fatalf("decode data_snap of checkpoint %d: %v", checkpointID, err)
	}
	return data
}

func (f *fixture) reset(t *testing.T) Outcome {
	t.Helper()
	out, err := f.repo.Reset(t.Context(), f.wsID, f.userID)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	return out
}

// writeSettings edits workspaces.settings directly, standing in for the
// admin PATCH this package must not import (internal/workspaces is not in
// this slice's file list — C10).
func (f *fixture) writeSettings(t *testing.T, s domain.Settings) {
	t.Helper()
	raw, err := s.MarshalJSONStable()
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	if _, err := f.db.W.ExecContext(t.Context(),
		"UPDATE workspaces SET settings = ?, revision = revision + 1, updated_at = unixepoch() WHERE id = ?",
		string(raw), f.wsID); err != nil {
		t.Fatalf("write settings: %v", err)
	}
}

// insertCrafted stores a bundle this package would never itself produce, so
// a test can drive the restore path with a snapshot built for it.
func (f *fixture) insertCrafted(t *testing.T, b bundle.Bundle) int64 {
	t.Helper()
	doc, err := bundle.Encode(b)
	if err != nil {
		t.Fatalf("encode crafted bundle: %v", err)
	}
	blob, err := compressSnapshot(doc)
	if err != nil {
		t.Fatalf("compress crafted bundle: %v", err)
	}
	return f.insertBlob(t, blob)
}

func (f *fixture) insertBlob(t *testing.T, blob []byte) int64 {
	t.Helper()
	res, err := f.db.W.ExecContext(t.Context(), `
		INSERT INTO checkpoints (workspace_id, kind, label, config_snap, created_at, created_by)
		VALUES (?, ?, 'подготовленный слепок', ?, unixepoch(), ?)`,
		f.wsID, KindManual, blob, f.userID)
	if err != nil {
		t.Fatalf("insert crafted checkpoint: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("crafted checkpoint id: %v", err)
	}
	return id
}

// rawGzip compresses without going through compressSnapshot, whose own
// ceiling check would refuse the oversized fixture before it could be
// stored.
func rawGzip(t *testing.T, doc []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(doc); err != nil {
		t.Fatalf("gzip: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// resourceFixture is the shape of one `resources` row a test wants in the
// tree. The rows go in by direct INSERT rather than through
// resources.Repo.Confirm, and that is the same choice
// TestCreate_dataSnapStaysNullWithEntities already states three hundred
// lines up: what these clauses are about is the CHECKPOINT codec's
// behaviour in the presence of these rows, not how they got there, and
// reaching for the other package would put a test-only import of
// internal/resources on a package that depends on it nowhere in production.
// It is also the only way to build a fixture whose snapshot values DIFFER
// from the live row's, which one of the clauses below is entirely about.
type resourceFixture struct {
	family    string
	name      string
	idField   string
	wrapper   string // JSON text; "" stores SQL NULL
	writeForm *string
	seq       int64
	seedCount int64
}

// insertResource writes one `resources` row and returns its id. It writes
// NO decision row: the two tables are written together by a confirm, but a
// test needs to put them out of step on purpose.
func (f *fixture) insertResource(t *testing.T, r resourceFixture) int64 {
	t.Helper()
	var wrapper any
	if r.wrapper != "" {
		wrapper = r.wrapper
	}
	res, err := f.db.W.ExecContext(t.Context(), `
		INSERT INTO resources (workspace_id, route_family, name, id_field, id_strategy, scope_params,
			entity_schema, wrapper, filter_map, write_form, seq, seed_count)
		VALUES (?, ?, ?, ?, 'seq', '[]', '#/components/schemas/Widget', ?, '{}', ?, ?, ?)`,
		f.wsID, r.family, r.name, r.idField, wrapper, r.writeForm, r.seq, r.seedCount)
	if err != nil {
		t.Fatalf("insert resource %q: %v", r.family, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("resource id for %q: %v", r.family, err)
	}
	return id
}

// readResource reads back the columns the restore is responsible for, or
// nil when the family has no row at all.
func (f *fixture) readResource(t *testing.T, family string) *resourceFixture {
	t.Helper()
	var (
		r                  resourceFixture
		wrapper, writeForm sql.NullString
	)
	r.family = family
	err := f.db.R.QueryRowContext(t.Context(), `
		SELECT name, id_field, wrapper, write_form, seq, seed_count
		FROM resources WHERE workspace_id = ? AND route_family = ?`, f.wsID, family,
	).Scan(&r.name, &r.idField, &wrapper, &writeForm, &r.seq, &r.seedCount)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		t.Fatalf("read resource %q: %v", family, err)
	}
	if wrapper.Valid {
		r.wrapper = wrapper.String
	}
	if writeForm.Valid {
		v := writeForm.String
		r.writeForm = &v
	}
	return &r
}

// writeDecision writes (or overwrites) one resource_decisions row.
func (f *fixture) writeDecision(t *testing.T, family, state string) {
	t.Helper()
	if _, err := f.db.W.ExecContext(t.Context(), `
		INSERT INTO resource_decisions (workspace_id, route_family, state) VALUES (?, ?, ?)
		ON CONFLICT (workspace_id, route_family) DO UPDATE SET state = excluded.state`,
		f.wsID, family, state); err != nil {
		t.Fatalf("write decision %q=%q: %v", family, state, err)
	}
}

// decisionState returns the stored state of one family, or "" when the row
// is absent.
func (f *fixture) decisionState(t *testing.T, family string) string {
	t.Helper()
	var state string
	err := f.db.R.QueryRowContext(t.Context(),
		"SELECT state FROM resource_decisions WHERE workspace_id = ? AND route_family = ?", f.wsID, family).Scan(&state)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ""
	case err != nil:
		t.Fatalf("read decision %q: %v", family, err)
	}
	return state
}

// insertEntities writes n entity rows for resourceID, keyed 1..n — the
// shape internal/resources' own population writes, minus that package.
func (f *fixture) insertEntities(t *testing.T, resourceID int64, n int) {
	t.Helper()
	f.insertEntityRange(t, resourceID, 1, n)
}

// insertEntityRange is insertEntities' half-open sibling, for the one test
// that adds rows to a family that already has some: entity_key is UNIQUE
// per (resource, scope), so a second run starting at 1 fails the constraint
// rather than extending the set.
func (f *fixture) insertEntityRange(t *testing.T, resourceID int64, from, to int) {
	t.Helper()
	now := time.Now().UnixMilli()
	for i := from; i <= to; i++ {
		if _, err := f.db.W.ExecContext(t.Context(), `
			INSERT INTO entities (resource_id, entity_key, data, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?)`,
			resourceID, fmt.Sprint(i), fmt.Sprintf(`{"id":%d,"name":"row %d"}`, i, i), now, now); err != nil {
			t.Fatalf("insert entity %d of resource %d: %v", i, resourceID, err)
		}
	}
}

// entityData returns one resource's stored rows as entity_key -> data,
// which is what "every entity row is still standing" is asserted over: a
// bare count would pass over a restore that deleted rows and wrote fresh
// ones back.
func (f *fixture) entityData(t *testing.T, resourceID int64) map[string]string {
	t.Helper()
	rows, err := f.db.R.QueryContext(t.Context(),
		"SELECT entity_key, data FROM entities WHERE resource_id = ?", resourceID)
	if err != nil {
		t.Fatalf("read entities of resource %d: %v", resourceID, err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var key, data string
		if err := rows.Scan(&key, &data); err != nil {
			t.Fatalf("scan entity of resource %d: %v", resourceID, err)
		}
		out[key] = data
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read entities of resource %d: %v", resourceID, err)
	}
	return out
}

// insertScopedEntity writes one entity row under an explicit scope_key —
// insertEntityRange's sibling for the P3e fixtures, which need two DIFFERENT
// scopes of the same family rather than the flat "" every other fixture in
// this file writes. parent_entity_id is never passed: D9 keeps that column
// NULL on every row this build writes, and a fixture that set it would be
// testing a build that does not exist.
func (f *fixture) insertScopedEntity(t *testing.T, resourceID int64, scopeKey, entityKey string) {
	t.Helper()
	now := time.Now().UnixMilli()
	if _, err := f.db.W.ExecContext(t.Context(), `
		INSERT INTO entities (resource_id, scope_key, entity_key, data, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		resourceID, scopeKey, entityKey, fmt.Sprintf(`{"id":%s,"scope":%q}`, entityKey, scopeKey), now, now); err != nil {
		t.Fatalf("insert scoped entity (scope=%q, key=%q) of resource %d: %v", scopeKey, entityKey, resourceID, err)
	}
}

// scopedEntityData is entityData's scope-aware sibling: for a nested family
// entity_key alone is not unique (UNIQUE is (resource_id, scope_key,
// entity_key)), so a comparison keyed by entity_key alone would silently
// collapse two different scopes' rows onto one map key.
func (f *fixture) scopedEntityData(t *testing.T, resourceID int64) map[[2]string]string {
	t.Helper()
	rows, err := f.db.R.QueryContext(t.Context(),
		"SELECT scope_key, entity_key, data FROM entities WHERE resource_id = ?", resourceID)
	if err != nil {
		t.Fatalf("read scoped entities of resource %d: %v", resourceID, err)
	}
	defer func() { _ = rows.Close() }()
	out := map[[2]string]string{}
	for rows.Next() {
		var scope, key, data string
		if err := rows.Scan(&scope, &key, &data); err != nil {
			t.Fatalf("scan scoped entity of resource %d: %v", resourceID, err)
		}
		out[[2]string{scope, key}] = data
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read scoped entities of resource %d: %v", resourceID, err)
	}
	return out
}

// insertBaseScopedEntity is insertScopedEntity's P3h sibling: it also binds
// base_scope_key, for the fixtures that need two DIFFERENT base scopes of
// the same family rather than the "" every pre-P3h fixture in this file
// writes. parent_entity_id is never passed, for the identical reason
// insertScopedEntity's own doc comment gives (D9).
func (f *fixture) insertBaseScopedEntity(t *testing.T, resourceID int64, baseScopeKey, scopeKey, entityKey string) {
	t.Helper()
	now := time.Now().UnixMilli()
	if _, err := f.db.W.ExecContext(t.Context(), `
		INSERT INTO entities (resource_id, base_scope_key, scope_key, entity_key, data, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		resourceID, baseScopeKey, scopeKey, entityKey,
		fmt.Sprintf(`{"id":%s,"base":%q,"scope":%q}`, entityKey, baseScopeKey, scopeKey), now, now); err != nil {
		t.Fatalf("insert base-scoped entity (base=%q, scope=%q, key=%q) of resource %d: %v",
			baseScopeKey, scopeKey, entityKey, resourceID, err)
	}
}

// baseScopedEntityData is scopedEntityData's P3h sibling: for a base-scoped
// family (base_scope_key, scope_key, entity_key) together identify a row,
// so a comparison keyed by (scope_key, entity_key) alone would silently
// collapse two different base scopes' rows onto one map key — exactly the
// collapse P1/P12's own subject is about.
func (f *fixture) baseScopedEntityData(t *testing.T, resourceID int64) map[[3]string]string {
	t.Helper()
	rows, err := f.db.R.QueryContext(t.Context(),
		"SELECT base_scope_key, scope_key, entity_key, data FROM entities WHERE resource_id = ?", resourceID)
	if err != nil {
		t.Fatalf("read base-scoped entities of resource %d: %v", resourceID, err)
	}
	defer func() { _ = rows.Close() }()
	out := map[[3]string]string{}
	for rows.Next() {
		var base, scope, key, data string
		if err := rows.Scan(&base, &scope, &key, &data); err != nil {
			t.Fatalf("scan base-scoped entity of resource %d: %v", resourceID, err)
		}
		out[[3]string{base, scope, key}] = data
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read base-scoped entities of resource %d: %v", resourceID, err)
	}
	return out
}

// entityParentIsAlwaysNull reports whether every entities row of resourceID
// has a NULL parent_entity_id — D9's own claim made an assertion rather than
// a comment: nested families are served through scope_key alone, and this
// slice writes no row that disagrees.
func (f *fixture) entityParentIsAlwaysNull(t *testing.T, resourceID int64) bool {
	t.Helper()
	var nonNull int
	if err := f.db.R.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM entities WHERE resource_id = ? AND parent_entity_id IS NOT NULL", resourceID,
	).Scan(&nonNull); err != nil {
		t.Fatalf("count non-null parent_entity_id of resource %d: %v", resourceID, err)
	}
	return nonNull == 0
}

// wrapperJSON is the four-key specs.Wrapper document a confirmed family
// stores, written with its keys ALREADY sorted: Encode canonicalises this
// column on the way into the snapshot, so a fixture in another order would
// make the round-trip assertions compare re-sorted bytes against the
// original and fail for a reason none of these clauses is about.
const wrapperJSON = `{"arrayKey":"items","countKey":"total","countType":"integer","idType":"integer"}`

// mapsEqual compares two entity_key -> data maps. maps.Equal would do, but
// this file's assertions read better with the failure message right at the
// call site, and the helper keeps the four call sites identical.
func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// scopedMapsEqual is mapsEqual's sibling over the (scopeKey, entityKey) key
// pair scopedEntityData reads — the P3e nested-family fixtures' own
// equivalent of the four unscoped comparisons mapsEqual already serves.
func scopedMapsEqual(a, b map[[2]string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// insertCraftedWithData is [fixture.insertCrafted]'s sibling for the
// fixtures that also need a VALID data_snap — property 5's own crafted
// document, which this package's own capture would never produce (it
// carries a row this fixture's live table does not, on purpose) but which
// must still satisfy [bundle.ValidateData] to reach the write loop at all.
func (f *fixture) insertCraftedWithData(t *testing.T, b bundle.Bundle, d bundle.DataBundle) int64 {
	t.Helper()
	doc, err := bundle.Encode(b)
	if err != nil {
		t.Fatalf("encode crafted bundle: %v", err)
	}
	configBlob, err := compressSnapshot(doc)
	if err != nil {
		t.Fatalf("compress crafted bundle: %v", err)
	}
	dataDoc, err := bundle.EncodeData(d)
	if err != nil {
		t.Fatalf("encode crafted data document: %v", err)
	}
	dataBlob, err := compressSnapshot(dataDoc)
	if err != nil {
		t.Fatalf("compress crafted data document: %v", err)
	}
	res, err := f.db.W.ExecContext(t.Context(), `
		INSERT INTO checkpoints (workspace_id, kind, label, config_snap, data_snap, created_at, created_by)
		VALUES (?, ?, 'подготовленный слепок с данными', ?, ?, unixepoch(), ?)`,
		f.wsID, KindManual, configBlob, dataBlob, f.userID)
	if err != nil {
		t.Fatalf("insert crafted checkpoint: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("crafted checkpoint id: %v", err)
	}
	return id
}

// settingsWith returns a copy of s with edit applied — a small helper so
// this file's base-path fixtures state only the two fields they change
// rather than reconstructing every domain.Settings field by hand.
func settingsWith(s domain.Settings, edit func(*domain.Settings)) domain.Settings {
	edit(&s)
	return s
}

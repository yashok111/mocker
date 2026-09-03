// Tests for internal/checkpoints.
//
// This file is `package checkpoints`, not `package checkpoints_test`, for
// one reason: C18's ceiling is a package-level var and §G obs 20 has to
// lower it. That also fixes the whole file's concurrency rule — NOT ONE
// TEST HERE CALLS t.Parallel. Every bar runs -race, and a package-level var
// written by one test while a parallel sibling reads it (which every test
// touching Create or Get does) is a detected data race, in the one package
// whose tests may not be weakened to make a bar green. Adding t.Parallel to
// any test below re-opens exactly that.
package checkpoints

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/store"
)

// --- harness ------------------------------------------------------------------

// newTestDB opens a fresh, migrated SQLite file under t.TempDir(),
// mirroring internal/scenarios/repo_test.go's harness.
func newTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.Context(), t.TempDir()+"/mocker.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

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
	db := newTestDB(t)
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

// --- create, list, get ---------------------------------------------------------

// TestCreate_manualDoesNotBumpRevision is C12: a history entry changes
// nothing that is served, and a bump costs a full runtime rebuild including
// a spec re-parse. An implementer copying customep.Repo.Create's
// always-bump pattern fails here.
//
// Its data_snap assertion is INVERTED by P3d's D5 (always-capture): a
// checkpoint over a workspace with no confirmed family at all still stores
// a document, one with an EMPTY families array — D5.2's own "a workspace
// with no confirmed family stores a non-NULL document" — never NULL. NULL
// is now reserved for a checkpoint written before P3d, or one whose capture
// degraded (D5.2's two bands, covered by their own tests below).
func TestCreate_manualDoesNotBumpRevision(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	before := f.revision(t)

	s := f.create(t, "  первая точка  ")

	if got := f.revision(t); got != before {
		t.Fatalf("revision moved on a manual checkpoint: %d -> %d", before, got)
	}
	if s.Kind != KindManual {
		t.Fatalf("kind = %q, want %q", s.Kind, KindManual)
	}
	if s.Label != "первая точка" {
		t.Fatalf("label = %q, want the trimmed original", s.Label)
	}
	if s.CreatedBy == nil || *s.CreatedBy != f.userID {
		t.Fatalf("createdBy = %v, want %d (C15)", s.CreatedBy, f.userID)
	}

	var createdBy sql.NullInt64
	if err := f.db.R.QueryRowContext(t.Context(),
		"SELECT created_by FROM checkpoints WHERE id = ?", s.ID).Scan(&createdBy); err != nil {
		t.Fatalf("read the stored row: %v", err)
	}
	if !createdBy.Valid || createdBy.Int64 != f.userID {
		t.Fatalf("stored created_by = %v, want %d", createdBy, f.userID)
	}

	if !s.HasData {
		t.Fatal("Summary.HasData = false, want true — a zero-family workspace still stores a document (D5.2)")
	}
	data := f.decodedData(t, s.ID)
	if len(data.Families) != 0 {
		t.Fatalf("families = %+v, want empty — this fixture confirms no resource", data.Families)
	}
}

// TestCreate_dataSnapCarriesEntitiesAndStaysOutOfConfigSnap is D13 clause
// 26's descendant under P3d. Before this slice data_snap stayed NULL
// unconditionally (P3b's own carve-out); D5's always-capture inverts that
// half, and this test is the guard for the inversion. What survives from
// clause 26 is the other half, unweakened: entity bytes must never leak
// into config_snap, which stays resources' CONFIGURATION only — that half
// is D3's refusal, not this test's own subject, but the assertion stays
// here because the two columns are read from the very same row.
//
// The rows go in by INSERT rather than through resources.Repo.Confirm on
// purpose. What the clause is about is the CHECKPOINT codec's behaviour in
// the presence of entity rows, not how they got there, and reaching for the
// other package would put a test-only import of internal/resources on a
// package that does not depend on it in production.
func TestCreate_dataSnapCarriesEntitiesAndStaysOutOfConfigSnap(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)

	now := time.Now().UnixMilli()
	res, err := f.db.W.ExecContext(t.Context(),
		`INSERT INTO resources (workspace_id, route_family, name, id_field, entity_schema, wrapper, seq, seed_count)
		 VALUES (?, '/widgets', 'widgets', 'id', '#/components/schemas/Widget', ?, 2, 2)`,
		f.wsID, `{"arrayKey":null,"countKey":null,"countType":"","idType":"integer"}`)
	if err != nil {
		t.Fatalf("insert the resources row: %v", err)
	}
	resourceID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("resources row id: %v", err)
	}
	// A confirm always writes its decision row alongside the resource; this
	// fixture builds the resources row by hand (the comment above explains
	// why) and must build the decision row the same way, or the capture's
	// join finds no confirmed family and this whole test asserts nothing.
	if _, err := f.db.W.ExecContext(t.Context(),
		`INSERT INTO resource_decisions (workspace_id, route_family, state) VALUES (?, '/widgets', 'confirmed')`,
		f.wsID); err != nil {
		t.Fatalf("write the decision row: %v", err)
	}
	for i := 1; i <= 2; i++ {
		if _, err := f.db.W.ExecContext(t.Context(),
			`INSERT INTO entities (resource_id, entity_key, data, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			resourceID, fmt.Sprint(i), fmt.Sprintf(`{"id":%d,"name":"widget %d"}`, i, i), now, now); err != nil {
			t.Fatalf("insert entity %d: %v", i, err)
		}
	}

	s := f.create(t, "with entities in the tree")

	var configSnap []byte
	if err := f.db.R.QueryRowContext(t.Context(),
		"SELECT config_snap FROM checkpoints WHERE id = ?", s.ID).Scan(&configSnap); err != nil {
		t.Fatalf("read the stored row: %v", err)
	}

	if !s.HasData {
		t.Fatal("Summary.HasData = false over a checkpoint carrying entity rows")
	}
	data := f.decodedData(t, s.ID)
	if len(data.Families) != 1 || data.Families[0].RouteFamily != "/widgets" {
		t.Fatalf("data_snap families = %+v, want one entry for /widgets", data.Families)
	}
	if len(data.Families[0].Rows) != 2 {
		t.Fatalf("data_snap rows for /widgets = %d, want 2", len(data.Families[0].Rows))
	}

	// And the entity DATA must not have leaked into the config snapshot
	// either. Half of the reason this once gave is gone: since P3b the
	// bundle accepts — and this package's capture fills — a non-empty
	// `resources`, which is CONFIGURATION. What still holds is the
	// `entities` refusal (D3): a codec that started carrying entity rows in
	// config_snap would show up in this assertion, never in data_snap.
	if len(configSnap) == 0 {
		t.Fatalf("config_snap is empty; expected the gzipped bundle")
	}
	if bytes.Contains(configSnap, []byte("widget 1")) {
		t.Fatalf("config_snap carries entity data verbatim")
	}
}

// TestCreate_rejectsEmptyAndOverlongLabel is C14's cap, counted in RUNES.
// The over-long case is Cyrillic on purpose: with ASCII a byte cap and a
// rune cap agree, so an implementation using len() would pass.
func TestCreate_rejectsEmptyAndOverlongLabel(t *testing.T) {
	f := newFixture(t, 20)

	for _, label := range []string{"", "   ", "\t\n"} {
		if _, err := f.repo.Create(t.Context(), f.wsID, label, f.userID); !errors.Is(err, ErrInvalidLabel) {
			t.Fatalf("Create(%q) error = %v, want ErrInvalidLabel", label, err)
		}
	}

	ok := strings.Repeat("я", maxLabelRunes)
	if _, err := f.repo.Create(t.Context(), f.wsID, ok, f.userID); err != nil {
		t.Fatalf("Create with a %d-rune Cyrillic label: %v", maxLabelRunes, err)
	}
	tooLong := strings.Repeat("я", maxLabelRunes+1)
	if _, err := f.repo.Create(t.Context(), f.wsID, tooLong, f.userID); !errors.Is(err, ErrInvalidLabel) {
		t.Fatalf("Create with a %d-rune label error = %v, want ErrInvalidLabel", maxLabelRunes+1, err)
	}
}

// TestListAndGet_scopeToTheWorkspace covers §C's list shape (no snapshot on
// the wire) and the by-construction 404 for another workspace's id.
func TestListAndGet_scopeToTheWorkspace(t *testing.T) {
	f := newFixture(t, 20)
	other := insertWorkspace(t, f.db, "ws-b", nil, domain.DefaultSettings())

	first := f.create(t, "первая")
	second := f.create(t, "вторая")

	got := f.list(t)
	if len(got) != 2 {
		t.Fatalf("List returned %d rows, want 2", len(got))
	}
	if got[0].ID != second.ID || got[1].ID != first.ID {
		t.Fatalf("List order = %d,%d, want newest first (%d,%d)", got[0].ID, got[1].ID, second.ID, first.ID)
	}

	if _, err := f.repo.Get(t.Context(), other, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get across workspaces error = %v, want ErrNotFound", err)
	}
	if _, err := f.repo.List(t.Context(), other); err != nil {
		t.Fatalf("List for a workspace with no checkpoints: %v", err)
	}
	if _, err := f.repo.Create(t.Context(), 9999, "нет воркспейса", f.userID); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("Create against a missing workspace = %v, want ErrWorkspaceNotFound", err)
	}
}

// --- rollback -------------------------------------------------------------------

// TestRollback_restoresTheWholeLayerAndBumpsExactlyOnce covers the three
// halves of a restore in one pass — settings wholesale (C10), overrides and
// custom endpoints (C1) — plus DESIGN §12:774-776's max+1 rule. A double
// bump (one in ReplaceAllTx and one here) fails the revision assertion.
func TestRollback_restoresTheWholeLayerAndBumpsExactlyOnce(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	kept := f.createEndpoint(t, f.wsID, "GET", "/kept")
	// A row on a SECOND workspace with a HIGHER rowid: without it a
	// workspace-scoped delete-and-reinsert would hand /kept its own freed
	// id back (custom_endpoints.id has no AUTOINCREMENT, 0001_init.sql:191)
	// and the id-stability assertion below would pass vacuously — §G obs 6
	// spells this out.
	other := insertWorkspace(t, f.db, "ws-b", nil, domain.DefaultSettings())
	f.createEndpoint(t, other, "GET", "/elsewhere")

	point := f.create(t, "точка")

	// Move everything the restore has to put back.
	f.pinStatus(t, 402)
	f.createEndpoint(t, f.wsID, "GET", "/added-later")
	if _, _, err := f.ovr.Put(t.Context(), f.wsID, overrides.OpKey("GET", "/other"), func(row *overrides.Row) error {
		row.OverrideOn = true
		return nil
	}); err != nil {
		t.Fatalf("add a second override: %v", err)
	}

	// Read the revision immediately before the call: the rule is max+1 over
	// whatever the workspace stands at NOW, never a number the snapshot
	// carries (DESIGN §12:774-776 — numbers are never reused).
	revisionBefore := f.revision(t)
	out := f.rollback(t, point.ID)

	if out.Revision != revisionBefore+1 {
		t.Fatalf("revision = %d, want exactly one past %d (max+1, never a reused number)", out.Revision, revisionBefore)
	}
	if got := f.revision(t); got != out.Revision {
		t.Fatalf("stored revision = %d, returned %d", got, out.Revision)
	}
	if !out.Changed {
		t.Fatal("Rollback reported Changed=false")
	}
	if out.ScenarioActive {
		t.Fatal("ScenarioActive=true with no scenario on the workspace")
	}

	if s := f.pinnedStatus(t); s == nil || *s != 418 {
		t.Fatalf("pinned status = %v, want the snapshot's 418", s)
	}
	if _, err := f.ovr.Get(t.Context(), f.wsID, overrides.OpKey("GET", "/other")); !errors.Is(err, overrides.ErrNotFound) {
		t.Fatalf("an override absent from the snapshot survived the restore: %v", err)
	}

	rows := f.endpoints(t)
	if len(rows) != 1 {
		t.Fatalf("custom endpoints after rollback = %d, want 1", len(rows))
	}
	if rows[0].Path != "/kept" {
		t.Fatalf("surviving endpoint = %q, want /kept", rows[0].Path)
	}
	// C1's whole reason for upsert-over-truncate: the UI holds this id.
	if rows[0].ID != kept.ID {
		t.Fatalf("endpoint id changed across the rollback: %d -> %d (truncate-and-reinsert)", kept.ID, rows[0].ID)
	}
}

// TestRollback_restoresSettingsWholesaleNotMerged is C10's half that a
// merge would pass: notFoundBody is the one TOP-LEVEL settings field absent
// from the JSON when unset (domain/settings.go:43, omitempty), so it is the
// only one whose snapshot value can differ from the live value by being
// ABSENT. listSize alone is restored identically by a merge and a replace.
func TestRollback_restoresSettingsWholesaleNotMerged(t *testing.T) {
	f := newFixture(t, 20)
	point := f.create(t, "до правки настроек")
	beforeKey := f.settings(t).Auth.SigningKey

	live := f.settings(t)
	live.ListSize = 42
	live.Auth.SigningKey = "0123456789abcdef"
	live.NotFoundBody = []byte(`{"marker":true}`)
	f.writeSettings(t, live)

	f.rollback(t, point.ID)

	got := f.settings(t)
	if got.ListSize != 5 {
		t.Fatalf("listSize = %d, want the snapshot's 5", got.ListSize)
	}
	if got.Auth.SigningKey != beforeKey {
		t.Fatalf("auth.signingKey = %q, want the snapshot's %q", got.Auth.SigningKey, beforeKey)
	}
	if len(got.NotFoundBody) != 0 {
		t.Fatalf("notFoundBody = %s, want it gone — a MERGE keeps it, a wholesale replace does not", got.NotFoundBody)
	}
}

// TestRollback_restoresBasePathValuesBesideBasePath is P3h's P12b: a
// wholesale settings restore carries basePathValues alongside basePath, its
// own property distinct from P12 (D9) because writeSettingsTx and
// restoreEntitiesTx are different functions over different tables — a
// mutation of one cannot reach the other, so P12's own mutation
// (restoreEntitiesTx dropping BaseScopeKey from the INSERT) cannot redden
// this test, and this one's own mutation (writeSettingsTx dropping
// basePathValues from the restore) cannot redden P12's. Restoring the
// prefix without the declared values it takes would leave a workspace whose
// every resource-served request refuses with base_scope_undeclared — the
// consequence the checkpoint's own comment names.
func TestRollback_restoresBasePathValuesBesideBasePath(t *testing.T) {
	f := newFixture(t, 20)
	f.writeSettings(t, settingsWith(f.settings(t), func(s *domain.Settings) {
		s.BasePath = "/orgs/{orgId}"
		s.BasePathValues = []string{"7", "8"}
	}))
	point := f.create(t, "до правки basePathValues")

	// A settings edit since the checkpoint drops the declared set and
	// relocates the prefix — the fixture a wholesale restore is supposed to
	// overwrite, mirroring TestRollback_restoresSettingsWholesaleNotMerged's
	// own "edit since the checkpoint" step.
	f.writeSettings(t, settingsWith(f.settings(t), func(s *domain.Settings) {
		s.BasePath = "/tenants/{tenantId}"
		s.BasePathValues = []string{"9"}
	}))

	f.rollback(t, point.ID)

	got := f.settings(t)
	if got.BasePath != "/orgs/{orgId}" {
		t.Fatalf("basePath = %q, want the checkpoint's %q", got.BasePath, "/orgs/{orgId}")
	}
	want := []string{"7", "8"}
	if len(got.BasePathValues) != len(want) || got.BasePathValues[0] != want[0] || got.BasePathValues[1] != want[1] {
		t.Fatalf("basePathValues = %v, want the checkpoint's %v (it must travel WITH basePath, not be dropped by a wholesale restore)", got.BasePathValues, want)
	}
}

// TestRollback_allocatesAFreshEditVersionForSettings is D9's rule stated by
// name for writeSettingsTx: it writes `settings`, a field
// PATCH /api/workspaces/{id} guards, so it must ALLOCATE from
// workspaces.edit_seq rather than leave a pre-rollback token matching the
// just-restored row. A PATCH caller who read edit_version before the
// rollback and would otherwise pass A3's compare-and-swap check against the
// restored row — silently overwriting what the rollback just put back — is
// exactly the failure this allocation exists to close.
func TestRollback_allocatesAFreshEditVersionForSettings(t *testing.T) {
	f := newFixture(t, 20)
	point := f.create(t, "до правки настроек")

	preRollbackToken := f.editVersion(t)

	live := f.settings(t)
	live.ListSize = 42
	f.writeSettings(t, live)

	f.rollback(t, point.ID)

	got := f.editVersion(t)
	if got == preRollbackToken {
		t.Fatalf("edit_version = %d unchanged across rollback, want a fresh value (the rollback writes settings, a guarded field)", got)
	}
	if got <= preRollbackToken {
		t.Fatalf("edit_version = %d, want strictly greater than the pre-rollback token %d (the allocator never hands out a value it has handed out before)", got, preRollbackToken)
	}
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

// TestRollback_protectsWhatItDestroys is §G obs 5's unit half: the
// pre-destructive checkpoint holds the state being DESTROYED, not the one
// being restored. An implementation that snapshots AFTER the apply stores
// 418 here and the second rollback becomes a no-op.
func TestRollback_protectsWhatItDestroys(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	point := f.create(t, "418")

	f.pinStatus(t, 402)
	f.rollback(t, point.ID)

	if s := f.pinnedStatus(t); s == nil || *s != 418 {
		t.Fatalf("after the first rollback the status is %v, want 418", s)
	}

	machine := f.machineMade(t)
	if len(machine) != 1 {
		t.Fatalf("machine-made checkpoints = %d, want 1", len(machine))
	}
	protective, err := f.repo.Get(t.Context(), f.wsID, machine[0])
	if err != nil {
		t.Fatalf("get the pre-destructive checkpoint: %v", err)
	}
	if protective.Kind != KindPreDestructive {
		t.Fatalf("kind = %q, want %q", protective.Kind, KindPreDestructive)
	}
	if want := rollbackLabel(point.ID); protective.Label != want {
		t.Fatalf("label = %q, want %q (C14, Russian and server-generated)", protective.Label, want)
	}
	if len(protective.Bundle.Overrides) != 1 || protective.Bundle.Overrides[0].ActiveStatus == nil ||
		*protective.Bundle.Overrides[0].ActiveStatus != 402 {
		t.Fatalf("the protective snapshot does not hold the DESTROYED state (402): %+v", protective.Bundle.Overrides)
	}

	// And it is usable: rolling back to it returns the state the first
	// rollback threw away.
	f.rollback(t, machine[0])
	if s := f.pinnedStatus(t); s == nil || *s != 402 {
		t.Fatalf("rolling back to the protective checkpoint gave %v, want 402", s)
	}
}

// TestRollback_reportsAnActiveScenario is C8: the operation is ALLOWED
// while a scenario is active, and the flag §C's body carries says so, so
// the screen can warn that part of the restored layer is masked. A 409 here
// would refuse a demonstrably visible operation.
func TestRollback_reportsAnActiveScenario(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	point := f.create(t, "точка")
	f.pinStatus(t, 402)

	res, err := f.db.W.ExecContext(t.Context(),
		"INSERT INTO scenarios (workspace_id, name, snapshot, created_at) VALUES (?, 'S', X'7B7D', unixepoch())", f.wsID)
	if err != nil {
		t.Fatalf("insert scenario: %v", err)
	}
	scenarioID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("scenario id: %v", err)
	}
	if _, err := f.db.W.ExecContext(t.Context(),
		"UPDATE workspaces SET scenario_id = ?, revision = revision + 1 WHERE id = ?", scenarioID, f.wsID); err != nil {
		t.Fatalf("activate scenario: %v", err)
	}

	out := f.rollback(t, point.ID)
	if !out.ScenarioActive {
		t.Fatal("ScenarioActive=false while a scenario is active")
	}
	reset := f.reset(t)
	if !reset.ScenarioActive {
		t.Fatal("Reset's ScenarioActive=false while a scenario is active")
	}

	var active sql.NullInt64
	if err := f.db.R.QueryRowContext(t.Context(),
		"SELECT scenario_id FROM workspaces WHERE id = ?", f.wsID).Scan(&active); err != nil {
		t.Fatalf("read scenario_id: %v", err)
	}
	if !active.Valid || active.Int64 != scenarioID {
		t.Fatalf("the scenario was deactivated by the restore: %v", active)
	}
}

// --- reset ----------------------------------------------------------------------

// TestReset_deletesBothKindsOfEditAndKeepsSettings is C9: «сбросить всё к
// спеке» reaches custom endpoints too — a custom endpoint is not in the
// spec — while settings survive, because they are the workspace's identity
// and resetting basePath would move every route.
func TestReset_deletesBothKindsOfEditAndKeepsSettings(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	f.createEndpoint(t, f.wsID, "GET", "/custom")
	before := f.revision(t)
	beforeSettings := f.settings(t)

	out := f.reset(t)

	if !out.Changed {
		t.Fatal("Changed=false for a reset that deleted two rows")
	}
	if out.Revision != before+1 {
		t.Fatalf("revision = %d, want %d", out.Revision, before+1)
	}
	if s := f.pinnedStatus(t); s != nil {
		t.Fatalf("the override survived the reset: %v", s)
	}
	if rows := f.endpoints(t); len(rows) != 0 {
		t.Fatalf("custom endpoints survived the reset: %d", len(rows))
	}
	if got := f.settings(t); got.ListSize != beforeSettings.ListSize || got.Auth.SigningKey != beforeSettings.Auth.SigningKey {
		t.Fatalf("settings changed on a reset: %+v", got)
	}

	machine := f.machineMade(t)
	if len(machine) != 1 {
		t.Fatalf("machine-made checkpoints = %d, want the one protecting the reset", len(machine))
	}
	protective, err := f.repo.Get(t.Context(), f.wsID, machine[0])
	if err != nil {
		t.Fatalf("get the protective checkpoint: %v", err)
	}
	if protective.Label != resetLabel {
		t.Fatalf("label = %q, want %q", protective.Label, resetLabel)
	}
	if len(protective.Bundle.Overrides) != 1 || len(protective.Bundle.Endpoints) != 1 {
		t.Fatalf("the protective checkpoint captured only one table: %d overrides, %d endpoints",
			len(protective.Bundle.Overrides), len(protective.Bundle.Endpoints))
	}

	// And the reset is undoable — the point of taking the checkpoint first.
	f.rollback(t, machine[0])
	if s := f.pinnedStatus(t); s == nil || *s != 418 {
		t.Fatalf("the override did not come back: %v", s)
	}
	if rows := f.endpoints(t); len(rows) != 1 {
		t.Fatalf("the custom endpoint did not come back: %d rows", len(rows))
	}
}

// TestReset_noOpWritesNothing is C9: a reset that would delete nothing
// writes no checkpoint, bumps no revision and succeeds. The decision comes
// from the pre-transaction read, never from a count returned by the apply
// (ReplaceAllTx deliberately returns none), and Changed is the only carrier
// the handler's `changed` field has.
func TestReset_noOpWritesNothing(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	f.reset(t)

	revisionAfterFirst := f.revision(t)
	checkpointsAfterFirst := len(f.list(t))

	out := f.reset(t)

	if out.Changed {
		t.Fatal("Changed=true for a reset with nothing to delete (C9)")
	}
	if out.Revision != revisionAfterFirst {
		t.Fatalf("revision moved on a no-op reset: %d -> %d", revisionAfterFirst, out.Revision)
	}
	if got := f.revision(t); got != revisionAfterFirst {
		t.Fatalf("stored revision moved on a no-op reset: %d -> %d", revisionAfterFirst, got)
	}
	if got := len(f.list(t)); got != checkpointsAfterFirst {
		t.Fatalf("a no-op reset wrote a checkpoint: %d -> %d", checkpointsAfterFirst, got)
	}
}

// --- retention (C7) --------------------------------------------------------------

// TestRetention_prunesMachineMadeAndSparesTheNamedOnes is C7's first case
// and DESIGN §12:773's «именованные не удаляются».
func TestRetention_prunesMachineMadeAndSparesTheNamedOnes(t *testing.T) {
	f := newFixture(t, 3)
	manual := f.create(t, "названная точка")

	for i := range 5 {
		f.pinStatus(t, 400+i)
		f.reset(t)
	}

	machine := f.machineMade(t)
	if len(machine) != 3 {
		t.Fatalf("machine-made checkpoints = %d, want the retention of 3", len(machine))
	}
	// The survivors must be the NEWEST three. machineMade returns them
	// oldest first, so every id is greater than the one before it, and the
	// FIRST of the five inserts (the smallest machine-made id there ever
	// was) must not be among them.
	for i := 1; i < len(machine); i++ {
		if machine[i] <= machine[i-1] {
			t.Fatalf("survivors are not in id order: %v", machine)
		}
	}
	if machine[0] <= manual.ID {
		t.Fatalf("the oldest machine-made checkpoint survived: %v", machine)
	}
	var found bool
	for _, s := range f.list(t) {
		if s.ID == manual.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("the manual checkpoint %d was pruned", manual.ID)
	}
}

// TestRetention_zeroPrunesNothing is §G obs 19, and it exists because
// smoke.sh runs at 3 and cannot see this. Zero means PRUNE NOTHING — a
// deliberate departure from traffic.NewRecorder's
// `if rec.retention <= 0 { rec.retention = DefaultRetention }`
// (recorder.go:98-100). An implementation that copies that default
// substitution keeps only the newest 1000 (or, read as "prune everything",
// none at all) and fails here.
func TestRetention_zeroPrunesNothing(t *testing.T) {
	f := newFixture(t, 0)

	const destructiveResets = 6
	for i := range destructiveResets {
		f.pinStatus(t, 400+i)
		f.reset(t)
	}

	machine := f.machineMade(t)
	if len(machine) != destructiveResets {
		t.Fatalf("machine-made checkpoints at retention 0 = %d, want all %d", len(machine), destructiveResets)
	}
}

// TestRetention_rollbackNeverPrunesItsOwnTarget is C7's third case: a
// MACHINE-MADE target is spared even when it falls outside the newest N,
// leaving N+1 rows for the duration of that one rollback — the alternative
// deletes the row the user just returned to, in the transaction that
// applies it. The overflow then corrects itself at the next machine-made
// insert whose target is not that row, and a MANUAL target gets no
// exemption at all.
func TestRetention_rollbackNeverPrunesItsOwnTarget(t *testing.T) {
	const retention = 2
	f := newFixture(t, retention)

	f.pinStatus(t, 401)
	manual := f.create(t, "M")

	f.pinStatus(t, 402)
	f.rollback(t, manual.ID) // writes O, holding 402
	f.pinStatus(t, 403)
	f.rollback(t, manual.ID) // writes P, holding 403

	machine := f.machineMade(t)
	if len(machine) != retention {
		t.Fatalf("machine-made population = %v, want exactly %d before the interesting rollback", machine, retention)
	}
	oldest := machine[0]

	// Roll back to the OLDEST machine-made row: it is the target, so it
	// survives its own prune and the population is N+1.
	f.rollback(t, oldest)

	after := f.machineMade(t)
	if len(after) != retention+1 {
		t.Fatalf("machine-made population = %v, want %d (newest %d plus the spared target)", after, retention+1, retention)
	}
	if after[0] != oldest {
		t.Fatalf("the rollback pruned the checkpoint it was restoring FROM: %v does not start with %d", after, oldest)
	}
	if s := f.pinnedStatus(t); s == nil || *s != 402 {
		t.Fatalf("state after rolling back to the oldest machine-made row = %v, want 402", s)
	}

	// A MANUAL target grants no exemption, so the overflow corrects itself.
	f.pinStatus(t, 405)
	f.rollback(t, manual.ID)
	corrected := f.machineMade(t)
	if len(corrected) != retention {
		t.Fatalf("machine-made population = %v, want back down to %d", corrected, retention)
	}
	for _, id := range corrected {
		if id == oldest {
			t.Fatalf("the spared target %d survived a rollback whose target was manual: %v", oldest, corrected)
		}
	}
}

// --- atomicity (§G obs 15) --------------------------------------------------------

// TestRollback_isAtomicWhenTheApplyFailsMidway is §G obs 15. The injection
// point is the one that matters: the SECOND endpoint of the snapshot, a row
// whose shape passes bundle.Validate (non-empty method, leading-slash path)
// and fails customep's own validatePath, which rejects "?" because it would
// desync from router.CanonicalPath's segment splitting. C4 duty 4 validates
// per row INSIDE the write loop, so the first endpoint really reaches the
// table before the second one fails — which is what makes "atomic" and
// "already half-applied" distinguishable at all. Injecting immediately
// after the checkpoint insert would NOT fail a split implementation.
func TestRollback_isAtomicWhenTheApplyFailsMidway(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	liveEndpoint := f.createEndpoint(t, f.wsID, "GET", "/live")

	beforeRevision := f.revision(t)
	beforeSettings := f.settings(t)

	// A snapshot that differs from the live workspace in every table, so
	// "nothing committed" is observable in all three.
	snapSettings := beforeSettings
	snapSettings.ListSize = 77
	b := bundle.New("ws-a", snapSettings, bundle.SpecRef{Name: "fixture", Hash: "hash-1"}, nil)
	b.Endpoints = []bundle.EndpointEntry{
		{Method: "GET", Path: "/aaa", OverrideOn: true, ActiveStatus: 200, Responses: map[string]overrides.Variant{}},
		{Method: "GET", Path: "/bbb?x", OverrideOn: true, ActiveStatus: 200, Responses: map[string]overrides.Variant{}},
	}
	if err := bundle.Validate(b); err != nil {
		t.Fatalf("the fixture must pass bundle.Validate to reach the write loop at all: %v", err)
	}
	bad := f.insertCrafted(t, b)
	// Counted AFTER the crafted row is stored: what must not appear is the
	// PRE-DESTRUCTIVE checkpoint this rollback would write.
	beforeCheckpoints := len(f.list(t))

	_, err := f.repo.Rollback(t.Context(), f.wsID, bad, f.userID, false, "")
	if err == nil {
		t.Fatal("rollback over a snapshot whose second endpoint is invalid succeeded")
	}
	if !errors.Is(err, customep.ErrInvalidRow) {
		t.Fatalf("rollback error = %v, want customep.ErrInvalidRow from the write loop's second row", err)
	}

	if got := f.revision(t); got != beforeRevision {
		t.Fatalf("revision moved despite the failed apply: %d -> %d", beforeRevision, got)
	}
	if got := f.settings(t); got.ListSize != beforeSettings.ListSize {
		t.Fatalf("settings committed despite the failed apply: listSize %d", got.ListSize)
	}
	if got := len(f.list(t)); got != beforeCheckpoints {
		t.Fatalf("a checkpoint committed despite the failed apply: %d -> %d", beforeCheckpoints, got)
	}
	if s := f.pinnedStatus(t); s == nil || *s != 418 {
		t.Fatalf("op_overrides was modified despite the failed apply: %v", s)
	}
	rows := f.endpoints(t)
	if len(rows) != 1 || rows[0].ID != liveEndpoint.ID || rows[0].Path != "/live" {
		t.Fatalf("custom_endpoints was modified despite the failed apply: %+v", rows)
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

// --- the gzip container, bounded at both ends (§G obs 20) --------------------------

// TestSnapshot_isGzippedAndRoundTrips is obs 20's first two checks: the
// stored column is a gzip stream (not raw JSON in a BLOB), and a
// write-then-read returns the same document.
func TestSnapshot_isGzippedAndRoundTrips(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	f.createEndpoint(t, f.wsID, "POST", "/custom")

	point := f.create(t, "точка")

	blob := f.storedBlob(t, point.ID)
	if !bytes.HasPrefix(blob, gzipMagic) {
		t.Fatalf("config_snap does not start with the gzip magic bytes: %x", blob[:min(4, len(blob))])
	}

	got, err := f.repo.Get(t.Context(), f.wsID, point.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	roundTripped, err := bundle.Encode(got.Bundle)
	if err != nil {
		t.Fatalf("re-encode: %v", err)
	}
	stored, err := decompressSnapshot(blob)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !bytes.Equal(roundTripped, stored) {
		t.Fatalf("round trip changed the document:\n stored: %s\n  round: %s", stored, roundTripped)
	}
	if len(got.Bundle.Endpoints) != 1 || got.Bundle.Endpoints[0].Path != "/custom" {
		t.Fatalf("the endpoint did not survive the round trip: %+v", got.Bundle.Endpoints)
	}
}

// TestSnapshot_refusesABlobThatIsNotGzip is obs 20's third check.
func TestSnapshot_refusesABlobThatIsNotGzip(t *testing.T) {
	f := newFixture(t, 20)
	point := f.create(t, "точка")

	if _, err := f.db.W.ExecContext(t.Context(),
		"UPDATE checkpoints SET config_snap = ? WHERE id = ?", []byte(`{"mockerBundle":4}`), point.ID); err != nil {
		t.Fatalf("overwrite config_snap: %v", err)
	}
	if _, err := f.repo.Get(t.Context(), f.wsID, point.ID); !errors.Is(err, ErrCorruptSnapshot) {
		t.Fatalf("Get over raw JSON = %v, want ErrCorruptSnapshot", err)
	}
}

// TestSnapshot_ceilingRefusesOneWhitespaceByteOver is obs 20's fourth
// check, and the reason decompressSnapshot reads maxSnapshotBytes+1: a bare
// LimitReader at exactly the limit truncates silently, and a truncation
// landing on trailing whitespace still parses as valid JSON. The overflow
// here is ONE SPACE — the document is still valid JSON, and it is still
// refused.
//
// It lowers maxSnapshotBytes and restores it. C18 forbids an 8 MiB fixture:
// building one under -race in a memory-capped scope is how this box's OOM
// killer gets invoked. NO t.Parallel here or anywhere in this package.
func TestSnapshot_ceilingRefusesOneWhitespaceByteOver(t *testing.T) {
	f := newFixture(t, 20)
	point := f.create(t, "точка")
	doc, err := decompressSnapshot(f.storedBlob(t, point.ID))
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}

	restore := maxSnapshotBytes
	t.Cleanup(func() { maxSnapshotBytes = restore })
	maxSnapshotBytes = len(doc)

	// Exactly at the ceiling: accepted.
	if _, err := f.repo.Get(t.Context(), f.wsID, point.ID); err != nil {
		t.Fatalf("a document of exactly maxSnapshotBytes was refused: %v", err)
	}

	// One whitespace byte over: refused, before bundle.Decode sees it.
	over := f.insertBlob(t, rawGzip(t, append(append([]byte(nil), doc...), ' ')))
	if _, err := f.repo.Get(t.Context(), f.wsID, over); !errors.Is(err, ErrSnapshotTooLarge) {
		t.Fatalf("Get over a document one byte past the ceiling = %v, want ErrSnapshotTooLarge", err)
	}
}

// TestSnapshot_ceilingRefusesAnOversizedCapture is obs 20's fifth check and
// C18's write half — the one that stops the ceiling from being a trap.
// Every existing cap in this tree is write-side and none bounds a
// workspace's TOTAL, so a read-only ceiling could make a checkpoint this
// very build wrote permanently unreadable. The refusal happens BEFORE any
// write: no checkpoint row, no revision move.
func TestSnapshot_ceilingRefusesAnOversizedCapture(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	point := f.create(t, "точка")
	doc, err := decompressSnapshot(f.storedBlob(t, point.ID))
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}

	restore := maxSnapshotBytes
	t.Cleanup(func() { maxSnapshotBytes = restore })
	maxSnapshotBytes = len(doc) - 1

	beforeRevision := f.revision(t)
	beforeCheckpoints := len(f.list(t))

	if _, err := f.repo.Create(t.Context(), f.wsID, "слишком большой", f.userID); !errors.Is(err, ErrSnapshotTooLarge) {
		t.Fatalf("Create over the ceiling = %v, want ErrSnapshotTooLarge", err)
	}
	// Rollback and Reset capture a pre-destructive checkpoint first, so both
	// are refused too — C18's stated consequence, not a surprise.
	if _, err := f.repo.Rollback(t.Context(), f.wsID, point.ID, f.userID, false, ""); !errors.Is(err, ErrSnapshotTooLarge) {
		t.Fatalf("Rollback over the ceiling = %v, want ErrSnapshotTooLarge", err)
	}
	if _, err := f.repo.Reset(t.Context(), f.wsID, f.userID); !errors.Is(err, ErrSnapshotTooLarge) {
		t.Fatalf("Reset over the ceiling = %v, want ErrSnapshotTooLarge", err)
	}

	if got := len(f.list(t)); got != beforeCheckpoints {
		t.Fatalf("a refused capture still wrote a checkpoint: %d -> %d", beforeCheckpoints, got)
	}
	if got := f.revision(t); got != beforeRevision {
		t.Fatalf("a refused capture still moved the revision: %d -> %d", beforeRevision, got)
	}
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

// --- the fence (C5) ----------------------------------------------------------------

// TestFenceTx_comparesAllThreeIdentityColumns is C5 step 4. revision ALONE
// cannot prove workspace identity — workspaces.id has no AUTOINCREMENT, so
// a deleted workspace's id can be reused with revision back at 1, reachable
// precisely because a manual checkpoint does not bump (C12) — and
// created_at is second-resolution, which is why slug is compared beside it.
func TestFenceTx_comparesAllThreeIdentityColumns(t *testing.T) {
	f := newFixture(t, 20)
	before, err := f.repo.readWorkspaceCore(t.Context(), f.wsID)
	if err != nil {
		t.Fatalf("read core: %v", err)
	}

	cases := map[string]workspaceCore{
		"revision":  {revision: before.revision + 1, createdAt: before.createdAt, slug: before.slug},
		"createdAt": {revision: before.revision, createdAt: before.createdAt + 1, slug: before.slug},
		"slug":      {revision: before.revision, createdAt: before.createdAt, slug: before.slug + "-other"},
	}
	for name, moved := range cases {
		t.Run(name, func(t *testing.T) {
			tx, err := f.db.W.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := fenceTx(t.Context(), tx, f.wsID, moved); !errors.Is(err, errFenceMoved) {
				t.Fatalf("fenceTx with a moved %s = %v, want errFenceMoved", name, err)
			}
		})
	}

	tx, err := f.db.W.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := fenceTx(t.Context(), tx, f.wsID, before); err != nil {
		t.Fatalf("fenceTx over an unchanged workspace: %v", err)
	}
}

// TestRetrying_isBoundedAndAnswersConcurrentEdit is C5 step 7: three
// attempts, then a 409's error. Driven at the loop rather than through a
// racing goroutine, which is a flaky test that proves less — the same
// judgement scenarios/repo.go makes about its own fence seam.
func TestRetrying_isBoundedAndAnswersConcurrentEdit(t *testing.T) {
	attempts := 0
	err := retrying(func() error {
		attempts++
		return errFenceMoved
	})
	if !errors.Is(err, ErrConcurrentEdit) {
		t.Fatalf("exhausted retry = %v, want ErrConcurrentEdit", err)
	}
	if attempts != maxAttempts {
		t.Fatalf("attempts = %d, want %d", attempts, maxAttempts)
	}

	attempts = 0
	if err := retrying(func() error {
		attempts++
		if attempts < maxAttempts {
			return errFenceMoved
		}
		return nil
	}); err != nil {
		t.Fatalf("a retry that eventually succeeds returned %v", err)
	}

	sentinel := errors.New("not a fence")
	attempts = 0
	if err := retrying(func() error {
		attempts++
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("a non-fence error was swallowed: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("a non-fence error was retried %d times", attempts)
	}
}

// --- Auto (P2d's debounce trigger, SIG-AUTO) --------------------------------------

// TestAuto_writesAKindAutoRowCarryingItsLabel is the plain case: an empty
// history has nothing to debounce against, so the outer probe never
// suppresses the very first call.
func TestAuto_writesAKindAutoRowCarryingItsLabel(t *testing.T) {
	f := newFixture(t, 20)

	s := f.auto(t, "перед массовой правкой", 60)
	if s == nil {
		t.Fatal("Auto on an empty history returned (nil, nil), want a write")
	}
	if s.Kind != KindAuto {
		t.Fatalf("kind = %q, want %q", s.Kind, KindAuto)
	}
	if s.Label != "перед массовой правкой" {
		t.Fatalf("label = %q, want the label Auto was given", s.Label)
	}
	if s.CreatedBy == nil || *s.CreatedBy != f.userID {
		t.Fatalf("createdBy = %v, want %d (C15)", s.CreatedBy, f.userID)
	}

	list := f.list(t)
	if len(list) != 1 || list[0].ID != s.ID || list[0].Kind != KindAuto {
		t.Fatalf("stored history = %+v, want exactly the one auto row Auto returned", list)
	}
}

// TestAuto_suppressesASecondCallInsideTheWindow is SIG-AUTO's central
// promise: a second call before the window elapses returns (nil, nil) — an
// ordinary return, not an error — and writes NOTHING, so the stored row
// still carries the FIRST call's label.
func TestAuto_suppressesASecondCallInsideTheWindow(t *testing.T) {
	f := newFixture(t, 20)
	const window = 3600 // far longer than this test can possibly take

	first := f.auto(t, "первая", window)
	if first == nil {
		t.Fatal("first Auto returned (nil, nil), want a write")
	}

	second, err := f.repo.Auto(t.Context(), f.wsID, "вторая", f.userID, window)
	if err != nil {
		t.Fatalf("suppressed Auto returned an error, want a plain (nil, nil): %v", err)
	}
	if second != nil {
		t.Fatalf("second Auto inside the window = %+v, want (nil, nil)", second)
	}

	list := f.list(t)
	if len(list) != 1 {
		t.Fatalf("history after a suppressed Auto = %d rows, want exactly 1 (nothing written)", len(list))
	}
	if list[0].Label != "первая" {
		t.Fatalf("stored label = %q, want %q — the suppressed call must not have written its own label", list[0].Label, "первая")
	}
}

// TestAuto_boundaryAtExactlyTheWindowWrites is the >= in SIG-AUTO's
// "now - max >= window": a sleep of EXACTLY the window must re-arm. Rather
// than a real sleep, the first row's created_at is pushed back by exactly
// `window` seconds through direct SQL — the same technique
// [fixture.ageAutoCheckpoint] uses elsewhere in this file — so the second
// call's elapsed time is deterministically at (or, in the rare case a wall
// clock second ticks between the two statements, past) the boundary. An
// implementation using `>` instead of `>=` fails this the common case: it
// suppresses at elapsed == window instead of re-arming.
func TestAuto_boundaryAtExactlyTheWindowWrites(t *testing.T) {
	f := newFixture(t, 20)
	const window = 5

	first := f.auto(t, "первая", window)
	if first == nil {
		t.Fatal("first Auto returned (nil, nil), want a write")
	}
	f.ageAutoCheckpoint(t, first.ID, window)

	second, err := f.repo.Auto(t.Context(), f.wsID, "вторая", f.userID, window)
	if err != nil {
		t.Fatalf("Auto exactly at the window: %v", err)
	}
	if second == nil {
		t.Fatal("Auto exactly at the window returned (nil, nil), want a write: the comparison must be >=, not >")
	}
}

// TestAuto_prunesLikeCreate is C7's retention rule reached through Auto
// instead of Create: [pruneRetentionTx]'s filter is `kind <> 'manual'`
// rather than an enumeration of the machine-made kinds, so auto rows join
// the pruned population for free (checkpoints.go's Kind doc comment) — this
// is the test that would fail if [Repo.Auto] wrote through
// [insertCheckpointTx] alone, skipping the prune SIG-AUTO forbids skipping.
func TestAuto_prunesLikeCreate(t *testing.T) {
	const retention = 2
	const window = 1
	f := newFixture(t, retention)

	const writes = 5
	for i := 0; i < writes; i++ {
		s := f.auto(t, "дебаунс", window)
		if s == nil {
			t.Fatalf("call %d suppressed unexpectedly", i)
		}
		// Age the row well past the window so the NEXT call is not
		// suppressed by the very row this call just wrote — this test is
		// about retention, not the window, and a suppressed call would
		// never reach pruneRetentionTx at all.
		f.ageAutoCheckpoint(t, s.ID, window+1)
	}

	machine := f.machineMade(t)
	if len(machine) != retention {
		t.Fatalf("auto checkpoints after %d writes = %d, want the retention of %d", writes, len(machine), retention)
	}
}

// TestAuto_rejectsEmptyLabelLikeCreate is SIG-AUTO's "reuses Create's whole
// sequence": validateLabel runs first, through the SAME [ErrInvalidLabel]
// path [TestCreate_rejectsEmptyAndOverlongLabel] exercises, and a rejected
// label writes nothing at all — not even the outer window probe's read
// matters, because there is no row to write.
func TestAuto_rejectsEmptyLabelLikeCreate(t *testing.T) {
	f := newFixture(t, 20)

	for _, label := range []string{"", "   ", "\t\n"} {
		if _, err := f.repo.Auto(t.Context(), f.wsID, label, f.userID, 60); !errors.Is(err, ErrInvalidLabel) {
			t.Fatalf("Auto(%q) error = %v, want ErrInvalidLabel", label, err)
		}
	}
	if got := len(f.list(t)); got != 0 {
		t.Fatalf("a rejected label still wrote %d rows", got)
	}
}

// --- Delete (P2d, SIG-DELCP) -------------------------------------------------------

// TestDelete_removesScopedToWorkspaceAndAnswersNotFoundTwice is SIG-DELCP
// whole: scoped to the workspace (a checkpoint id valid in a DIFFERENT
// workspace is untouched and unreachable, exactly the by-construction
// ErrNotFound [Get]'s doc comment states), no revision bump, and a
// zero-row delete answers ErrNotFound — including calling Delete AGAIN on
// an already-deleted id, so the 404 is not a one-shot artefact of the row
// having just been touched.
func TestDelete_removesScopedToWorkspaceAndAnswersNotFoundTwice(t *testing.T) {
	f := newFixture(t, 20)
	target := f.create(t, "к удалению")
	before := f.revision(t)

	otherSpecID := insertSpec(t, f.db, "other", "hash-2")
	otherSettings := domain.DefaultSettings()
	otherSettings.Normalize()
	otherWsID := insertWorkspace(t, f.db, "ws-b", &otherSpecID, otherSettings)

	if err := f.repo.Delete(t.Context(), otherWsID, target.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete(%d) from a different workspace = %v, want ErrNotFound", target.ID, err)
	}
	if got := len(f.list(t)); got != 1 {
		t.Fatalf("a checkpoint belonging to another workspace was deleted: history has %d rows, want 1", got)
	}

	if err := f.repo.Delete(t.Context(), f.wsID, target.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := len(f.list(t)); got != 0 {
		t.Fatalf("Delete left %d rows behind, want 0", got)
	}
	if got := f.revision(t); got != before {
		t.Fatalf("revision moved on Delete: %d -> %d", before, got)
	}

	if err := f.repo.Delete(t.Context(), f.wsID, target.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("first Delete of an already-deleted checkpoint = %v, want ErrNotFound", err)
	}
	if err := f.repo.Delete(t.Context(), f.wsID, target.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete of the same checkpoint = %v, want ErrNotFound", err)
	}
}

// --- the resource layer inside a checkpoint (P3b) -------------------------------

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

// TestCreate_configSnapCarriesResourcesAndDecisions is D10 clause 21: a
// checkpoint's config_snap carries the `resources` rows AND the
// `resource_decisions` rows. Under P3d it ALSO carries the workspace's
// entity rows — but in data_snap, a separate column and a separate
// document (D5's always-capture); config_snap's own Bundle.Entities stays
// JSON null regardless (D3's refusal, unweakened).
//
// The two config tables travel together or not at all. A snapshot carrying
// one without the other restores a workspace whose decision row says
// `declined` beside a live `resources` row — a state the confirm path
// answers `already_confirmed` for while the screen renders it as declined —
// which is why the fixture declares a DECLINED family with no resource row
// alongside the confirmed one: the decisions array is the only place that
// family exists at all.
//
// Verified by MUTATION: deleting the resource_decisions read from
// captureSnapshot reds this test and
// TestRollback_restoresAResourceDeclinedAwaySinceTheSnapshot with it.
func TestCreate_configSnapCarriesResourcesAndDecisions(t *testing.T) {
	f := newFixture(t, 20)
	bare := "bare"
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id",
		wrapper: wrapperJSON, writeForm: &bare, seq: 3, seedCount: 3,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.writeDecision(t, "/gadgets", "declined")
	f.insertEntities(t, resourceID, 3)

	point := f.create(t, "с ресурсами")

	stored, err := f.repo.Get(t.Context(), f.wsID, point.ID)
	if err != nil {
		t.Fatalf("get checkpoint: %v", err)
	}

	if len(stored.Bundle.Resources) != 1 {
		t.Fatalf("config_snap carries %d resources, want 1: %+v", len(stored.Bundle.Resources), stored.Bundle.Resources)
	}
	got := stored.Bundle.Resources[0]
	if got.RouteFamily != "/widgets" || got.Name != "widgets" || got.IDField != "id" || got.IDStrategy != "seq" {
		t.Fatalf("resource entry identity = %+v", got)
	}
	if got.Seq != 3 || got.SeedCount != 3 {
		t.Fatalf("resource entry counters = seq %d, seedCount %d, want 3 and 3", got.Seq, got.SeedCount)
	}
	if got.WriteForm == nil || *got.WriteForm != "bare" {
		t.Fatalf("resource entry writeForm = %v, want \"bare\"", got.WriteForm)
	}
	if string(got.Wrapper) != wrapperJSON {
		t.Fatalf("resource entry wrapper = %s, want %s", got.Wrapper, wrapperJSON)
	}
	if string(got.FilterMap) != "{}" {
		t.Fatalf("resource entry filterMap = %s, want {}", got.FilterMap)
	}
	if got.ParentFamily != nil {
		t.Fatalf("resource entry parentFamily = %v, want null in this build", *got.ParentFamily)
	}

	wantDecisions := map[string]string{"/gadgets": "declined", "/widgets": "confirmed"}
	gotDecisions := map[string]string{}
	for _, d := range stored.Bundle.Decisions {
		gotDecisions[d.RouteFamily] = d.State
	}
	if len(gotDecisions) != len(wantDecisions) {
		t.Fatalf("config_snap carries decisions %+v, want %+v", gotDecisions, wantDecisions)
	}
	for family, state := range wantDecisions {
		if gotDecisions[family] != state {
			t.Fatalf("decision for %q = %q, want %q", family, gotDecisions[family], state)
		}
	}

	// The other half of the clause, inverted by P3d: entity DATA is now
	// captured, but into data_snap ONLY — config_snap's own Bundle.Entities
	// stays JSON null regardless (D3's refusal is unweakened).
	data := f.decodedData(t, point.ID)
	if len(data.Families) != 1 || data.Families[0].RouteFamily != "/widgets" {
		t.Fatalf("data_snap families = %+v, want one entry for /widgets", data.Families)
	}
	if len(data.Families[0].Rows) != 3 {
		t.Fatalf("data_snap rows for /widgets = %d, want 3", len(data.Families[0].Rows))
	}
	if !isJSONNullBytes(stored.Bundle.Entities) {
		t.Fatalf("config_snap carries entities = %s, want null", stored.Bundle.Entities)
	}
}

// TestRestore_neverTouchesAnEntityRow is D10 clause 22, and it is the
// whole reason P3b exists. entities.resource_id is ON DELETE CASCADE and
// the restore beside this one is DELETE-then-UPSERT by natural key, so a
// resource half written in that same shape destroys every entity row of
// every family the snapshot names — silently, in a transaction the operator
// asked to restore CONFIGURATION.
//
// The fixture holds two families, one the target snapshot names and one it
// does not, both carrying rows: a restore that deleted only what it was
// about to write back would pass over the second, and one that deleted the
// workspace's resources wholesale fails on both.
//
// BOTH verbs are driven here, because the clause is about both and because
// the same injection into resetTx must red this test as well as
// TestReset_leavesResourcesAndEntitiesIntact below.
//
// Verified by MUTATION: `DELETE FROM resources WHERE workspace_id = ?`
// injected into rollbackTx AFTER the three resource statements reds this
// test, and co-reds the three clauses below it — at that position both
// UPSERTs have already run, so every row of the workspace is simply gone
// with its entities by cascade.
func TestRestore_neverTouchesAnEntityRow(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	namedID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 2, seedCount: 2,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, namedID, 2)

	point := f.create(t, "до второго ресурса")

	// Confirmed AFTER the checkpoint: the snapshot names it nowhere.
	unnamedID := f.insertResource(t, resourceFixture{
		family: "/quizzes", name: "quizzes", idField: "id", wrapper: wrapperJSON, seq: 3, seedCount: 3,
	})
	f.writeDecision(t, "/quizzes", "confirmed")
	f.insertEntities(t, unnamedID, 3)

	namedBefore := f.entityData(t, namedID)
	unnamedBefore := f.entityData(t, unnamedID)

	f.pinStatus(t, 402)
	f.rollback(t, point.ID)

	// The configuration half really did roll back — otherwise this test
	// would be green over a restore that does nothing at all.
	if s := f.pinnedStatus(t); s == nil || *s != 418 {
		t.Fatalf("the override did not roll back: %v", s)
	}
	if got := f.entityData(t, namedID); !mapsEqual(got, namedBefore) {
		t.Fatalf("the named family's entities changed: %v -> %v", namedBefore, got)
	}
	if got := f.entityData(t, unnamedID); !mapsEqual(got, unnamedBefore) {
		t.Fatalf("the unnamed family's entities changed: %v -> %v", unnamedBefore, got)
	}
	// And the family confirmed AFTER the checkpoint survives as a row, not
	// only as data: R16's UPSERT-only rule leaves rows the snapshot does
	// not name standing.
	if f.readResource(t, "/quizzes") == nil {
		t.Fatal("the family confirmed after the checkpoint lost its resources row")
	}

	// The other verb, same rule. Reset deletes the workspace layer down to
	// the spec and has no resource statements at all — R16 covers it by
	// what resetTx does NOT contain.
	f.pinStatus(t, 409)
	f.reset(t)
	if s := f.pinnedStatus(t); s != nil {
		t.Fatalf("the reset did not delete the override; the fixture is not the state it claims: %v", s)
	}
	if got := f.entityData(t, namedID); !mapsEqual(got, namedBefore) {
		t.Fatalf("the reset changed the named family's entities: %v -> %v", namedBefore, got)
	}
	if got := f.entityData(t, unnamedID); !mapsEqual(got, unnamedBefore) {
		t.Fatalf("the reset changed the unnamed family's entities: %v -> %v", unnamedBefore, got)
	}
}

// TestRollback_restoresAResourceDeclinedAwaySinceTheSnapshot is D10 clause
// 23: UPSERT-ONLY means never DELETE, not "never write resources at all".
// A family the snapshot names and the workspace has since declined comes
// BACK — its row and its decision together.
//
// The snapshot is taken by Repo.Create rather than crafted by hand on
// purpose: clause 21's mutation (deleting the resource_decisions read from
// captureSnapshot) co-reds this test only if the capture is the real one —
// insertCrafted is immune to a capture-side edit.
//
// It also documents the divergence R16 accepts rather than hides: the row
// comes back CONFIGURED and EMPTY. The decline's own cascade already
// destroyed its entities and no snapshot in this build holds them.
//
// Verified by MUTATION: breaking the restore's `resources` UPSERT reds this
// test and TestRollback_restoresASnapshotsColumnsOverALiveRow with it.
func TestRollback_restoresAResourceDeclinedAwaySinceTheSnapshot(t *testing.T) {
	f := newFixture(t, 20)
	bare := "bare"
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id",
		wrapper: wrapperJSON, writeForm: &bare, seq: 4, seedCount: 4,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 4)

	point := f.create(t, "до отказа")

	// The decline, as internal/resources performs it: the decision flips
	// and the row goes, taking its entities with it by cascade.
	if _, err := f.db.W.ExecContext(t.Context(), "DELETE FROM resources WHERE id = ?", resourceID); err != nil {
		t.Fatalf("decline the family: %v", err)
	}
	f.writeDecision(t, "/widgets", "declined")
	if got := len(f.entityData(t, resourceID)); got != 0 {
		t.Fatalf("the decline left %d entity rows behind; the fixture is not the state it claims", got)
	}

	f.rollback(t, point.ID)

	restored := f.readResource(t, "/widgets")
	if restored == nil {
		t.Fatal("the rollback did not restore the resources row the snapshot names")
	}
	if restored.name != "widgets" || restored.idField != "id" || restored.seedCount != 4 {
		t.Fatalf("restored row = %+v", restored)
	}
	if restored.writeForm == nil || *restored.writeForm != "bare" {
		t.Fatalf("restored writeForm = %v, want \"bare\"", restored.writeForm)
	}
	if got := f.decisionState(t, "/widgets"); got != "confirmed" {
		t.Fatalf("decision after the rollback = %q, want \"confirmed\"", got)
	}
	// R16's accepted divergence, asserted so it is a decision and not a
	// surprise: the family is back, and it is empty.
	var entityCount int
	if err := f.db.R.QueryRowContext(t.Context(), `
		SELECT COUNT(*) FROM entities WHERE resource_id IN
			(SELECT id FROM resources WHERE workspace_id = ?)`, f.wsID).Scan(&entityCount); err != nil {
		t.Fatalf("count entities: %v", err)
	}
	if entityCount != 0 {
		t.Fatalf("entity rows = %d, want 0: a rollback cannot bring back what a decline destroyed", entityCount)
	}
}

// TestRollback_restoresASnapshotsColumnsOverALiveRow is D10 clause 23a: the
// UPSERT writes the snapshot's COLUMNS over a live row, not merely its key.
//
// write_form is the sharp one and the reason a clause observes columns at
// all: NULL means the family's POST shape was not recognised, and the mock
// plane answers a GENERATED 201 over a write that stores nothing — a
// silent lost write, not a 500. A restore that wrote the key and left the
// shape would leave that state undetectable by every other clause here.
func TestRollback_restoresASnapshotsColumnsOverALiveRow(t *testing.T) {
	f := newFixture(t, 20)
	f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id",
		wrapper: wrapperJSON, writeForm: nil, seq: 2, seedCount: 2,
	})
	f.writeDecision(t, "/widgets", "confirmed")

	point := f.create(t, "снимок с write_form NULL")

	const liveWrapper = `{"arrayKey":"data","countKey":null,"countType":"","idType":"string"}`
	if _, err := f.db.W.ExecContext(t.Context(), `
		UPDATE resources SET write_form = 'bare', wrapper = ?, id_field = 'uuid'
		WHERE workspace_id = ? AND route_family = '/widgets'`, liveWrapper, f.wsID); err != nil {
		t.Fatalf("move the live row away from the snapshot: %v", err)
	}

	f.rollback(t, point.ID)

	got := f.readResource(t, "/widgets")
	if got == nil {
		t.Fatal("the resources row disappeared across the rollback")
	}
	if got.writeForm != nil {
		t.Fatalf("write_form = %q, want the snapshot's NULL", *got.writeForm)
	}
	if got.wrapper != wrapperJSON {
		t.Fatalf("wrapper = %s, want the snapshot's %s", got.wrapper, wrapperJSON)
	}
	if got.idField != "id" {
		t.Fatalf("id_field = %q, want the snapshot's \"id\"", got.idField)
	}
}

// TestRollback_doesNotDeclineAFamilyConfirmedSinceTheSnapshot is D10 clause
// 24: a snapshot's `declined` decision is NOT applied to a family that has
// a `resources` row right now.
//
// Applying it would recreate the exact state carrying the two tables
// together exists to prevent — `declined` written over a live row R16
// forbids deleting, after which the screen renders the family as declined
// while the confirm path answers `already_confirmed` and the mock plane
// goes on serving it.
func TestRollback_doesNotDeclineAFamilyConfirmedSinceTheSnapshot(t *testing.T) {
	f := newFixture(t, 20)
	f.writeDecision(t, "/widgets", "declined")

	point := f.create(t, "снимок с отказом")

	// Confirmed since: the row and its rows exist now, and the snapshot
	// names the family only in its decisions array.
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 5, seedCount: 5,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 5)
	before := f.entityData(t, resourceID)

	f.rollback(t, point.ID)

	if got := f.decisionState(t, "/widgets"); got != "confirmed" {
		t.Fatalf("decision after the rollback = %q, want \"confirmed\": the snapshot's decline must not land on a live row", got)
	}
	if f.readResource(t, "/widgets") == nil {
		t.Fatal("the live resources row disappeared across the rollback")
	}
	if got := f.entityData(t, resourceID); !mapsEqual(got, before) {
		t.Fatalf("entities changed: %v -> %v", before, got)
	}
}

// TestRollback_neverRewindsTheEntityCounter is D10 clause 25 and R18.
//
// `seq` is restored as max(current, snapshot), computed IN SQL inside the
// transaction. Writing the snapshot's value verbatim sets the counter BELOW
// a live entity key, and the next POST X then mints an id that already
// exists — which does NOT answer 500: the insert violates the UNIQUE, the
// mock plane logs it, declines the takeover and serves a GENERATED 201, so
// the caller is told the write succeeded and no row exists. The counter is
// read off the column rather than driven through a POST for the reason the
// clause gives: that needs internal/mockplane and internal/resources, and
// this package imports neither.
func TestRollback_neverRewindsTheEntityCounter(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 2, seedCount: 2,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 2)

	point := f.create(t, "снимок при seq=2")

	// Anonymous clients wrote three more rows through the mock plane: seq
	// moved, and nothing bumped revision, which is exactly why fenceTx
	// cannot see this and a max taken in Go over the capture would not
	// either.
	if _, err := f.db.W.ExecContext(t.Context(),
		"UPDATE resources SET seq = 5 WHERE id = ?", resourceID); err != nil {
		t.Fatalf("move seq: %v", err)
	}
	f.insertEntityRange(t, resourceID, 3, 5)

	f.rollback(t, point.ID)

	got := f.readResource(t, "/widgets")
	if got == nil {
		t.Fatal("the resources row disappeared across the rollback")
	}
	if got.seq != 5 {
		t.Fatalf("seq after the rollback = %d, want 5 — the live value, never the snapshot's 2", got.seq)
	}
	// seed_count IS restored verbatim: it is configuration, not a counter.
	if got.seedCount != 2 {
		t.Fatalf("seed_count = %d, want the snapshot's 2", got.seedCount)
	}
}

// TestRollback_resourceHalfIsInsideTheOneTransaction is D10 clause 26: a
// failure injected AFTER the resources UPSERT leaves the workspace exactly
// as it was.
//
// The injection is this package's only technique, and it dictates where the
// three resource statements live: a crafted snapshot whose SECOND endpoint
// fails customep's own validatePath, inside customep.ReplaceAllTx. The
// resource statements therefore run BEFORE that call — placed after it,
// they could never be reached by an injected failure and this clause would
// be green over a resource half running in its own transaction.
func TestRollback_resourceHalfIsInsideTheOneTransaction(t *testing.T) {
	f := newFixture(t, 20)
	f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")

	settings := f.settings(t)
	b := bundle.New("ws-a", settings, bundle.SpecRef{Name: "fixture", Hash: "hash-1"}, nil)
	b.Resources = []bundle.ResourceEntry{{
		RouteFamily: "/quizzes", Name: "quizzes", IDField: "id", IDStrategy: "seq",
		ScopeParams: []string{}, EntitySchema: "#/components/schemas/Quiz",
		Wrapper: jsonx.RawMessage(wrapperJSON), FilterMap: jsonx.RawMessage(`{}`),
		Seq: 9, SeedCount: 9,
	}}
	b.Decisions = []bundle.DecisionEntry{{RouteFamily: "/quizzes", State: "confirmed"}}
	b.Endpoints = []bundle.EndpointEntry{
		{Method: "GET", Path: "/aaa", OverrideOn: true, ActiveStatus: 200, Responses: map[string]overrides.Variant{}},
		{Method: "GET", Path: "/bbb?x", OverrideOn: true, ActiveStatus: 200, Responses: map[string]overrides.Variant{}},
	}
	if err := bundle.Validate(b); err != nil {
		t.Fatalf("the fixture must pass bundle.Validate to reach the write loop at all: %v", err)
	}
	bad := f.insertCrafted(t, b)

	_, err := f.repo.Rollback(t.Context(), f.wsID, bad, f.userID, false, "")
	if !errors.Is(err, customep.ErrInvalidRow) {
		t.Fatalf("rollback error = %v, want customep.ErrInvalidRow from the write loop's second row", err)
	}

	if f.readResource(t, "/quizzes") != nil {
		t.Fatal("the resources UPSERT committed despite the failed apply: the resource half runs in its own transaction")
	}
	if got := f.decisionState(t, "/quizzes"); got != "" {
		t.Fatalf("a resource decision committed despite the failed apply: %q", got)
	}
	if f.readResource(t, "/widgets") == nil {
		t.Fatal("the pre-existing resources row disappeared")
	}
}

// TestReset_leavesResourcesAndEntitiesIntact is D10 clause 27: «сбросить
// всё к спеке» resets the workspace LAYER — op_overrides and
// custom_endpoints — and touches neither the resource roster nor a single
// entity row.
//
// resetTx is the third ReplaceAllTx call site in this package and therefore
// the natural place to add a fourth for a third table; R16's rule covers
// Reset exactly as it covers Rollback, and for the identical reason — that
// fourth call would cascade every entity row away.
func TestReset_leavesResourcesAndEntitiesIntact(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	f.createEndpoint(t, f.wsID, "GET", "/live")
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 3, seedCount: 3,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 3)
	before := f.entityData(t, resourceID)

	out := f.reset(t)
	if !out.Changed {
		t.Fatal("the reset reported no change; the fixture is not the state it claims")
	}
	if s := f.pinnedStatus(t); s != nil {
		t.Fatalf("the reset did not delete the override: %v", s)
	}

	if f.readResource(t, "/widgets") == nil {
		t.Fatal("the reset deleted the resources row")
	}
	if got := f.decisionState(t, "/widgets"); got != "confirmed" {
		t.Fatalf("decision after the reset = %q, want \"confirmed\"", got)
	}
	if got := f.entityData(t, resourceID); !mapsEqual(got, before) {
		t.Fatalf("the reset changed entity rows: %v -> %v", before, got)
	}
}

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

// --- P3d: the entity-data half of a checkpoint (D5-D7, acceptance properties 1,3,4,5,7) ---

// TestCapture_allFourCallSitesWriteData is property 1's own enumeration
// taken literally: Create, Auto, Rollback (its own pre-destructive row) and
// Reset (its own pre-destructive row) all write a non-NULL data_snap over a
// workspace holding a confirmed family — D5's always-capture reaches all
// four, not only the two ("Create", "Rollback") an implementer reaching for
// the obvious pair would remember.
func TestCapture_allFourCallSitesWriteData(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	f.createEndpoint(t, f.wsID, "GET", "/live")
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 1)

	manual := f.create(t, "manual")
	if !manual.HasData {
		t.Fatal("Create: HasData = false")
	}

	auto := f.auto(t, "auto", 60)
	if auto == nil || !auto.HasData {
		t.Fatalf("Auto: %+v, want a row with HasData=true", auto)
	}

	beforeMachine := len(f.machineMade(t))
	f.rollback(t, manual.ID) // restoreData:false — still writes a pre-destructive row WITH data
	machine := f.machineMade(t)
	if len(machine) != beforeMachine+1 {
		t.Fatalf("machine-made count after Rollback = %d, want %d", len(machine), beforeMachine+1)
	}
	pd, err := f.repo.Get(t.Context(), f.wsID, machine[len(machine)-1])
	if err != nil {
		t.Fatalf("get Rollback's pre-destructive checkpoint: %v", err)
	}
	if !pd.HasData {
		t.Fatal("Rollback's own pre-destructive checkpoint: HasData = false")
	}

	resetOut := f.reset(t)
	if !resetOut.Changed {
		t.Fatal("Reset reported no change; the fixture is not the state it claims")
	}
	resetMachine := f.machineMade(t)
	rd, err := f.repo.Get(t.Context(), f.wsID, resetMachine[len(resetMachine)-1])
	if err != nil {
		t.Fatalf("get Reset's pre-destructive checkpoint: %v", err)
	}
	if !rd.HasData {
		t.Fatal("Reset's own pre-destructive checkpoint: HasData = false")
	}
}

// TestCapture_scopesToTheOwningWorkspace is D5.2's own load-bearing clause,
// observed directly: a SECOND workspace confirming the SAME route_family
// with its OWN rows contributes nothing to this workspace's data_snap. The
// hazard is not a leak into a blob — because D6 step 1 resolves a restore
// by (workspace_id, route_family), a foreign family sharing a route path
// would get the OTHER workspace's rows WRITTEN INTO IT on restore.
func TestCapture_scopesToTheOwningWorkspace(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 1)

	otherSettings := domain.DefaultSettings()
	otherSettings.Normalize()
	otherWsID := insertWorkspace(t, f.db, "ws-b", nil, otherSettings)
	otherRes, err := f.db.W.ExecContext(t.Context(), `
		INSERT INTO resources (workspace_id, route_family, name, id_field, id_strategy, scope_params,
			entity_schema, wrapper, filter_map, seq, seed_count)
		VALUES (?, '/widgets', 'widgets', 'id', 'seq', '[]', '#/components/schemas/Widget', ?, '{}', 1, 1)`,
		otherWsID, wrapperJSON)
	if err != nil {
		t.Fatalf("insert the other workspace's resource: %v", err)
	}
	otherResourceID, err := otherRes.LastInsertId()
	if err != nil {
		t.Fatalf("other resource id: %v", err)
	}
	if _, err := f.db.W.ExecContext(t.Context(),
		`INSERT INTO resource_decisions (workspace_id, route_family, state) VALUES (?, '/widgets', 'confirmed')`,
		otherWsID); err != nil {
		t.Fatalf("write the other workspace's decision: %v", err)
	}
	now := time.Now().UnixMilli()
	if _, err := f.db.W.ExecContext(t.Context(),
		`INSERT INTO entities (resource_id, entity_key, data, created_at, updated_at) VALUES (?, '1', ?, ?, ?)`,
		otherResourceID, `{"id":1,"name":"the other workspace's own row"}`, now, now); err != nil {
		t.Fatalf("insert the other workspace's entity: %v", err)
	}

	point := f.create(t, "капчер, две мастерские")

	data := f.decodedData(t, point.ID)
	if len(data.Families) != 1 {
		t.Fatalf("families = %+v, want exactly one (this workspace's own)", data.Families)
	}
	if len(data.Families[0].Rows) != 1 {
		t.Fatalf("rows for /widgets = %d, want 1 — the other workspace's row leaked in", len(data.Families[0].Rows))
	}
	if strings.Contains(string(data.Families[0].Rows[0].Data), "other workspace") {
		t.Fatalf("data_snap carries the other workspace's row: %s", data.Families[0].Rows[0].Data)
	}
}

// TestCapture_seesARowCreatedInTheWindowBeforeDbWrite is D5.1's whole
// argument, observed directly: [captureEntitiesTx] runs INSIDE the write
// transaction, never on the reader pool, so a row created in the window
// between [Repo.captureSnapshot] (config, pre-transaction) and db.Write
// (entities, inside it) still lands in the checkpoint's data_snap.
// [capturePreWriteHook] is the seam, fired exactly once per capture call,
// right where D5.1 says the hazard lives — the whole reason a counter-based
// fence was rejected (D5.1) is that this window cannot be closed any other
// way.
func TestCapture_seesARowCreatedInTheWindowBeforeDbWrite(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 1)

	restore := capturePreWriteHook
	t.Cleanup(func() { capturePreWriteHook = restore })
	capturePreWriteHook = func() {
		// Simulates an anonymous POST X landing exactly in D5.1's window.
		f.insertEntityRange(t, resourceID, 2, 2)
	}

	point := f.create(t, "капчер в окне")

	data := f.decodedData(t, point.ID)
	if len(data.Families) != 1 {
		t.Fatalf("families = %+v, want one", data.Families)
	}
	if len(data.Families[0].Rows) != 2 {
		t.Fatalf("rows for /widgets = %d, want 2 — the row created in the capture window is missing", len(data.Families[0].Rows))
	}
}

// TestCapture_carriesAConfirmedFamilyWithNoRowsAsEmptyNotAbsent is D5.2's
// own promise: a confirmed family holding zero entity rows is present in
// the document with an EMPTY relation, never dropped — the reason
// [captureEntitiesTx]'s own read is a LEFT JOIN, not an INNER one.
func TestCapture_carriesAConfirmedFamilyWithNoRowsAsEmptyNotAbsent(t *testing.T) {
	f := newFixture(t, 20)
	f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 0, seedCount: 0,
	})
	f.writeDecision(t, "/widgets", "confirmed")

	point := f.create(t, "пустое семейство")

	data := f.decodedData(t, point.ID)
	if len(data.Families) != 1 || data.Families[0].RouteFamily != "/widgets" {
		t.Fatalf("families = %+v, want one entry for /widgets", data.Families)
	}
	if len(data.Families[0].Rows) != 0 {
		t.Fatalf("rows = %+v, want an empty slice, not dropped", data.Families[0].Rows)
	}
}

// TestCapture_carriesANonContiguousKeySetInCanonicalOrder is
// [bundle.EntityRow]'s own reminder made concrete: a captured key set need
// not be 1..N (Confirm and reseed assign positionally; EntityStore.Create
// allocates from the counter), and the canonical byte order sorts rows by
// DECIMAL value, not lexically — "2" before "10".
func TestCapture_carriesANonContiguousKeySetInCanonicalOrder(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 10, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	now := time.Now().UnixMilli()
	for _, key := range []string{"10", "2", "7"} {
		if _, err := f.db.W.ExecContext(t.Context(),
			`INSERT INTO entities (resource_id, entity_key, data, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			resourceID, key, fmt.Sprintf(`{"id":%s}`, key), now, now); err != nil {
			t.Fatalf("insert entity %s: %v", key, err)
		}
	}

	point := f.create(t, "непрерывные ключи")

	data := f.decodedData(t, point.ID)
	if len(data.Families) != 1 || len(data.Families[0].Rows) != 3 {
		t.Fatalf("data_snap = %+v, want one family with 3 rows", data.Families)
	}
	gotKeys := make([]string, 0, len(data.Families[0].Rows))
	for _, r := range data.Families[0].Rows {
		gotKeys = append(gotKeys, r.EntityKey)
	}
	wantKeys := []string{"2", "7", "10"}
	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("entityKey order = %v, want %v (decimal, not lexical)", gotKeys, wantKeys)
	}
}

// TestRollback_dataRestoreOverwritesARowCreatedAfterTheCheckpoint is
// property 3's declared "FIRST fixture", run under restoreData:true: D6
// step 3 (DELETE) and step 4 (INSERT verbatim) win over anything an
// anonymous POST X wrote to the family after the checkpoint.
func TestRollback_dataRestoreOverwritesARowCreatedAfterTheCheckpoint(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 1)

	point := f.create(t, "до новой строки")
	f.insertEntityRange(t, resourceID, 2, 2)

	f.rollbackData(t, point.ID)

	got := f.entityData(t, resourceID)
	want := map[string]string{"1": `{"id":1,"name":"row 1"}`}
	if !mapsEqual(got, want) {
		t.Fatalf("entities after restoreData:true = %v, want exactly the checkpoint's %v (the post-checkpoint row must be gone)", got, want)
	}
}

// TestRollback_falsePathLeavesAPostCheckpointRowUntouched is property 3's
// SAME fixture as the test above, run with restoreData:false — the false
// path's own new guard (D6), and deliberately NOT
// [TestRestore_neverTouchesAnEntityRow]: that test's rows all PREDATE their
// checkpoint, so an unconditional restore re-inserting them verbatim is
// invisible to its map[entity_key]data comparison. This fixture's row
// POSTDATES the checkpoint, which only a false path that never calls
// [restoreEntitiesTx] at all can leave standing.
func TestRollback_falsePathLeavesAPostCheckpointRowUntouched(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 1)

	point := f.create(t, "до новой строки")
	f.insertEntityRange(t, resourceID, 2, 2)
	before := f.entityData(t, resourceID)

	f.rollback(t, point.ID) // restoreData:false/absent

	got := f.entityData(t, resourceID)
	if !mapsEqual(got, before) {
		t.Fatalf("entities after restoreData:false = %v, want unchanged %v", got, before)
	}
}

// TestRollback_dataRestorePreservesTheCapturedKeySet is property 3's
// non-contiguous-key fixture: a restore replays STORED keys, never
// re-derived positionally the way reseed does.
func TestRollback_dataRestorePreservesTheCapturedKeySet(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 10, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	now := time.Now().UnixMilli()
	for _, key := range []string{"2", "7", "10"} {
		if _, err := f.db.W.ExecContext(t.Context(),
			`INSERT INTO entities (resource_id, entity_key, data, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			resourceID, key, fmt.Sprintf(`{"id":%s}`, key), now, now); err != nil {
			t.Fatalf("insert entity %s: %v", key, err)
		}
	}
	point := f.create(t, "непрерывный набор")

	// Replace with a DIFFERENT, contiguous set, so a restore that
	// re-derives keys positionally is indistinguishable from one that does
	// not unless the captured keys survive verbatim.
	if _, err := f.db.W.ExecContext(t.Context(), "DELETE FROM entities WHERE resource_id = ?", resourceID); err != nil {
		t.Fatalf("clear entities: %v", err)
	}
	f.insertEntities(t, resourceID, 2)

	f.rollbackData(t, point.ID)

	got := f.entityData(t, resourceID)
	gotKeys := make([]string, 0, len(got))
	for k := range got {
		gotKeys = append(gotKeys, k)
	}
	// Sorted as DECIMAL INTEGERS, not lexically — "2" < "7" < "10" is not
	// the string order, and this assertion is about which KEYS survived,
	// not about string collation.
	slices.SortFunc(gotKeys, func(a, b string) int {
		an, _ := strconv.Atoi(a)
		bn, _ := strconv.Atoi(b)
		return an - bn
	})
	wantKeys := []string{"2", "7", "10"}
	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("restored keys = %v, want the captured, non-contiguous %v", gotKeys, wantKeys)
	}
}

// TestRollback_dataRestoreEmptiesAFamilyPopulatedSinceTheCheckpoint is
// property 3's "carried family POPULATED at rollback time whose relation in
// the DOCUMENT is empty" fixture: the document's own empty relation wins,
// exactly as a non-empty one would.
func TestRollback_dataRestoreEmptiesAFamilyPopulatedSinceTheCheckpoint(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 0, seedCount: 0,
	})
	f.writeDecision(t, "/widgets", "confirmed")

	point := f.create(t, "пусто на слепке")

	// Populated by an anonymous POST X after the (empty) checkpoint.
	f.insertEntities(t, resourceID, 3)

	f.rollbackData(t, point.ID)

	if got := f.entityData(t, resourceID); len(got) != 0 {
		t.Fatalf("entities after restoreData:true over an empty-relation checkpoint = %v, want none", got)
	}
}

// TestRollback_dataRestoresMultipleCarriedFamilies is property 3's
// "more than one carried family" fixture.
func TestRollback_dataRestoresMultipleCarriedFamilies(t *testing.T) {
	f := newFixture(t, 20)
	widgetsID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 2, seedCount: 2,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, widgetsID, 2)
	quizzesID := f.insertResource(t, resourceFixture{
		family: "/quizzes", name: "quizzes", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/quizzes", "confirmed")
	f.insertEntities(t, quizzesID, 1)

	point := f.create(t, "два семейства")

	f.insertEntityRange(t, widgetsID, 3, 3)
	if _, err := f.db.W.ExecContext(t.Context(), "DELETE FROM entities WHERE resource_id = ?", quizzesID); err != nil {
		t.Fatalf("clear quizzes: %v", err)
	}

	f.rollbackData(t, point.ID)

	if got := f.entityData(t, widgetsID); len(got) != 2 {
		t.Fatalf("/widgets after restore = %v, want the checkpoint's 2 rows", got)
	}
	if got := f.entityData(t, quizzesID); len(got) != 1 {
		t.Fatalf("/quizzes after restore = %v, want the checkpoint's 1 row", got)
	}
}

// TestRollback_dataLeavesALiveFamilyAbsentFromTheDocumentUntouched is
// property 3's own "a live family absent from the document is untouched
// under restoreData:true" subject — the restoreData:true sibling of
// [TestRestore_neverTouchesAnEntityRow]'s restoreData:false coverage of the
// identical shape (a family confirmed AFTER the checkpoint, so neither
// config_snap nor data_snap names it at all: restoreEntitiesTx's step 2,
// D6, treats the resolve-by-route_family miss as SIG-AUTO, not an error).
//
// Verified by MUTATION: making restoreEntitiesTx additionally DELETE every
// live confirmed family the document does not carry (a plausible wrong
// implementation that strands a post-checkpoint confirm) reds only this
// test in the package.
func TestRollback_dataLeavesALiveFamilyAbsentFromTheDocumentUntouched(t *testing.T) {
	f := newFixture(t, 20)
	widgetsID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 2, seedCount: 2,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, widgetsID, 2)

	point := f.create(t, "до второго ресурса")

	// Confirmed AFTER the checkpoint: absent from both halves of the
	// document, exactly TestRestore_neverTouchesAnEntityRow's own setup for
	// the restoreData:false path.
	quizzesID := f.insertResource(t, resourceFixture{
		family: "/quizzes", name: "quizzes", idField: "id", wrapper: wrapperJSON, seq: 3, seedCount: 3,
	})
	f.writeDecision(t, "/quizzes", "confirmed")
	f.insertEntities(t, quizzesID, 3)

	before := f.entityData(t, quizzesID)

	f.rollbackData(t, point.ID)

	if got := f.entityData(t, quizzesID); !mapsEqual(got, before) {
		t.Fatalf("a live family absent from the document changed under restoreData:true: %v -> %v", before, got)
	}
	if f.readResource(t, "/quizzes") == nil {
		t.Fatal("the family confirmed after the checkpoint lost its resources row under restoreData:true")
	}
	// The carried family really did restore under restoreData:true —
	// otherwise this test would be green over a restore that touches
	// nothing at all.
	if got := f.entityData(t, widgetsID); len(got) != 2 {
		t.Fatalf("/widgets after restore = %v, want the checkpoint's 2 rows", got)
	}
}

// TestRollback_dataRestoreBringsBackRowsOfAFamilyDeclinedSince is property
// 3's "family declined after the checkpoint, which comes back confirmed AND
// populated" fixture — the sibling, under restoreData:true, of
// [TestRollback_restoresAResourceDeclinedAwaySinceTheSnapshot]'s
// restoreData:false result (entityCount == 0): this is what P3b's own
// "a rollback cannot bring back what a decline destroyed" reads the other
// way round, since P3d's data half now can.
func TestRollback_dataRestoreBringsBackRowsOfAFamilyDeclinedSince(t *testing.T) {
	f := newFixture(t, 20)
	bare := "bare"
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id",
		wrapper: wrapperJSON, writeForm: &bare, seq: 4, seedCount: 4,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 4)

	point := f.create(t, "до отказа")

	if _, err := f.db.W.ExecContext(t.Context(), "DELETE FROM resources WHERE id = ?", resourceID); err != nil {
		t.Fatalf("decline the family: %v", err)
	}
	f.writeDecision(t, "/widgets", "declined")

	f.rollbackData(t, point.ID)

	restored := f.readResource(t, "/widgets")
	if restored == nil {
		t.Fatal("restoreData:true did not restore the resources row")
	}
	var entityCount int
	if err := f.db.R.QueryRowContext(t.Context(), `
		SELECT COUNT(*) FROM entities WHERE resource_id IN
			(SELECT id FROM resources WHERE workspace_id = ?)`, f.wsID).Scan(&entityCount); err != nil {
		t.Fatalf("count entities: %v", err)
	}
	if entityCount != 4 {
		t.Fatalf("entity rows after restoreData:true = %d, want the checkpoint's 4", entityCount)
	}
}

// TestRollback_dataRestoreResolvesByFamilyAcrossADeclineAndReconfirm is
// property 3's last fixture: a decline-and-reconfirm whose id is FORCED to
// differ from the declined row's — D4's whole reason a document addresses a
// family by route_family, never by resources.id, which is not stable across
// exactly this sequence.
func TestRollback_dataRestoreResolvesByFamilyAcrossADeclineAndReconfirm(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 2, seedCount: 2,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 2)

	point := f.create(t, "до отказа и переподтверждения")

	if _, err := f.db.W.ExecContext(t.Context(), "DELETE FROM resources WHERE id = ?", resourceID); err != nil {
		t.Fatalf("decline: %v", err)
	}
	f.writeDecision(t, "/widgets", "declined")

	// A decoy row FORCES the reconfirm below to mint a different id than
	// the declined one — SQLite is free to reuse a deleted rowid with no
	// AUTOINCREMENT on the column when the table would otherwise be empty,
	// and this fixture must not rely on that NOT happening.
	f.insertResource(t, resourceFixture{
		family: "/decoy", name: "decoy", idField: "id", wrapper: wrapperJSON, seq: 0, seedCount: 0,
	})

	reconfirmedID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	if reconfirmedID == resourceID {
		t.Fatalf("reconfirmed id = %d, same as the declined row's %d; the fixture failed to force the gap", reconfirmedID, resourceID)
	}
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, reconfirmedID, 1)

	f.rollbackData(t, point.ID)

	got := f.entityData(t, reconfirmedID)
	want := map[string]string{"1": `{"id":1,"name":"row 1"}`, "2": `{"id":2,"name":"row 2"}`}
	if !mapsEqual(got, want) {
		t.Fatalf("entities after restore across a decline/reconfirm = %v, want the checkpoint's %v", got, want)
	}
}

// TestRollback_dataRestoreRaisesSeqOverATornCheckpoint is property 4's own
// scenario and D5.1a's own math: config_snap's `resources.seq` (T1,
// pre-transaction) and data_snap's rows (T2, inside it) are captured at TWO
// instants, so a row minted in the window between them is in the rows but
// not reflected by the captured seq — a TORN checkpoint. Without
// [restoreEntitiesTx]'s own MAX-over-restored-keys term, the pre-existing
// `MAX(excluded.seq, resources.seq)` would leave seq at the checkpoint's
// stale value while the extra key sits in the table, and the next
// allocation would collide on it.
func TestRollback_dataRestoreRaisesSeqOverATornCheckpoint(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 1)

	restore := capturePreWriteHook
	t.Cleanup(func() { capturePreWriteHook = restore })
	capturePreWriteHook = func() {
		// An anonymous POST X landing in D5.1a's window: after
		// config_snap's seq was already read (T1), before data_snap's rows
		// are read inside the transaction (T2).
		if _, err := f.db.W.ExecContext(t.Context(),
			"UPDATE resources SET seq = 2 WHERE id = ?", resourceID); err != nil {
			t.Fatalf("bump seq in the capture window: %v", err)
		}
		f.insertEntityRange(t, resourceID, 2, 2)
	}
	point := f.create(t, "рваный слепок")
	capturePreWriteHook = restore // do not let the tear leak into the rollback below

	// Confirm the tear: config_snap's own captured seq is the STALE 1.
	stored, err := f.repo.Get(t.Context(), f.wsID, point.ID)
	if err != nil {
		t.Fatalf("get checkpoint: %v", err)
	}
	if len(stored.Bundle.Resources) != 1 || stored.Bundle.Resources[0].Seq != 1 {
		t.Fatalf("captured config seq = %+v, want 1 (the tear this fixture is built to produce)", stored.Bundle.Resources)
	}
	data := f.decodedData(t, point.ID)
	if len(data.Families) != 1 || len(data.Families[0].Rows) != 2 {
		t.Fatalf("captured rows = %+v, want 2 (key \"2\" landed inside the transaction)", data.Families)
	}

	// Roll the LIVE seq back down, as a fresh reseed might, so the
	// restore's own MAX-over-restored-keys term has something to raise.
	if _, err := f.db.W.ExecContext(t.Context(),
		"UPDATE resources SET seq = 1 WHERE id = ?", resourceID); err != nil {
		t.Fatalf("reset live seq: %v", err)
	}

	f.rollbackData(t, point.ID)

	got := f.readResource(t, "/widgets")
	if got == nil {
		t.Fatal("resources row disappeared")
	}
	if got.seq < 2 {
		t.Fatalf("seq after restore = %d, want at least 2 (the largest restored key) — a torn checkpoint's stale captured seq must not win", got.seq)
	}
}

// TestRollback_refusesAHandBuiltDataDocumentOutsideValidateDatasDomain is
// property 4's other half: [bundle.ValidateData] runs once, on the WHOLE
// document, before anything is written — observed with a fixture no capture
// this build produces could ever create (a non-decimal entityKey), because
// every document this package's own capture writes already satisfies the
// domain by construction (D6).
func TestRollback_refusesAHandBuiltDataDocumentOutsideValidateDatasDomain(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 1)

	// A VALID data document to encode from — EncodeData itself calls
	// ValidateData and would refuse the corrupt shape directly, so the
	// corruption below is applied to the already-encoded BYTES, after the
	// one call this fixture is allowed to make to a validating encoder.
	doc, err := bundle.EncodeData(bundle.DataBundle{
		MockerData: bundle.DataVersion,
		Families: []bundle.FamilyEntry{{
			RouteFamily: "/widgets",
			Rows: []bundle.EntityRow{
				{ScopeKey: "", EntityKey: "1", Data: jsonx.RawMessage(`{"id":1}`), CreatedAt: 1, UpdatedAt: 1},
			},
		}},
	})
	if err != nil {
		t.Fatalf("encode a valid data document to craft from: %v", err)
	}
	corrupted := bytes.Replace(doc, []byte(`"entityKey":"1"`), []byte(`"entityKey":"abc"`), 1)
	if bytes.Equal(corrupted, doc) {
		t.Fatal("the entityKey replacement did not match; fixture is stale")
	}
	dataBlob, err := compressSnapshot(corrupted)
	if err != nil {
		t.Fatalf("compress the crafted data document: %v", err)
	}

	settings := f.settings(t)
	b := bundle.New("ws-a", settings, bundle.SpecRef{Name: "fixture", Hash: "hash-1"}, nil)
	configDoc, err := bundle.Encode(b)
	if err != nil {
		t.Fatalf("encode config bundle: %v", err)
	}
	configBlob, err := compressSnapshot(configDoc)
	if err != nil {
		t.Fatalf("compress config bundle: %v", err)
	}
	res, err := f.db.W.ExecContext(t.Context(), `
		INSERT INTO checkpoints (workspace_id, kind, label, config_snap, data_snap, created_at, created_by)
		VALUES (?, ?, 'подготовленный со сломанным data_snap', ?, ?, unixepoch(), ?)`,
		f.wsID, KindManual, configBlob, dataBlob, f.userID)
	if err != nil {
		t.Fatalf("insert crafted checkpoint: %v", err)
	}
	bad, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("crafted checkpoint id: %v", err)
	}

	beforeRevision := f.revision(t)
	beforeCheckpoints := len(f.list(t))
	before := f.entityData(t, resourceID)

	_, err = f.repo.Rollback(t.Context(), f.wsID, bad, f.userID, true, f.slug(t))
	if !errors.Is(err, ErrCorruptSnapshot) {
		t.Fatalf("rollback over a hand-built out-of-domain data document = %v, want ErrCorruptSnapshot", err)
	}

	if got := f.revision(t); got != beforeRevision {
		t.Fatalf("revision moved despite the refusal: %d -> %d", beforeRevision, got)
	}
	if got := len(f.list(t)); got != beforeCheckpoints {
		t.Fatalf("a checkpoint committed despite the refusal: %d -> %d", beforeCheckpoints, got)
	}
	if got := f.entityData(t, resourceID); !mapsEqual(got, before) {
		t.Fatalf("entities changed despite the refusal: %v -> %v", before, got)
	}
	if s := f.pinnedStatus(t); s == nil || *s != 418 {
		t.Fatalf("op_overrides changed despite the refusal: %v", s)
	}
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

// TestRollback_dataRestoreIsInsideTheOneTransaction is property 5: a
// restoreData:true rollback's entity write SHARES rollbackTx's single
// transaction. Injected the same way
// [TestRollback_isAtomicWhenTheApplyFailsMidway] does — a crafted snapshot
// whose second endpoint fails customep's own validatePath, inside
// customep.ReplaceAllTx, which [restoreEntitiesTx] runs BEFORE (D6) — so a
// failure there must leave the entity rows exactly as they were, proving
// the write shares the transaction rather than having already committed on
// its own.
func TestRollback_dataRestoreIsInsideTheOneTransaction(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 1)

	// Move the live row away from what the crafted checkpoint's data_snap
	// carries, so "unchanged after the injected failure" is not true
	// vacuously — if the restore ran, this value would become the
	// checkpoint's own.
	if _, err := f.db.W.ExecContext(t.Context(),
		"UPDATE entities SET data = ? WHERE resource_id = ? AND entity_key = '1'",
		`{"id":1,"name":"changed after the checkpoint"}`, resourceID); err != nil {
		t.Fatalf("mutate the live entity: %v", err)
	}
	before := f.entityData(t, resourceID)

	settings := f.settings(t)
	b := bundle.New("ws-a", settings, bundle.SpecRef{Name: "fixture", Hash: "hash-1"}, nil)
	b.Resources = []bundle.ResourceEntry{{
		RouteFamily: "/widgets", Name: "widgets", IDField: "id", IDStrategy: "seq",
		ScopeParams: []string{}, EntitySchema: "#/components/schemas/Widget",
		Wrapper: jsonx.RawMessage(wrapperJSON), FilterMap: jsonx.RawMessage(`{}`),
		Seq: 1, SeedCount: 1,
	}}
	b.Decisions = []bundle.DecisionEntry{{RouteFamily: "/widgets", State: "confirmed"}}
	b.Endpoints = []bundle.EndpointEntry{
		{Method: "GET", Path: "/aaa", OverrideOn: true, ActiveStatus: 200, Responses: map[string]overrides.Variant{}},
		{Method: "GET", Path: "/bbb?x", OverrideOn: true, ActiveStatus: 200, Responses: map[string]overrides.Variant{}},
	}
	if err := bundle.Validate(b); err != nil {
		t.Fatalf("the fixture must pass bundle.Validate to reach the write loop at all: %v", err)
	}
	d := bundle.DataBundle{
		MockerData: bundle.DataVersion,
		Families: []bundle.FamilyEntry{{
			RouteFamily: "/widgets",
			Rows: []bundle.EntityRow{
				{ScopeKey: "", EntityKey: "1", Data: jsonx.RawMessage(`{"id":1,"name":"from the checkpoint"}`), CreatedAt: 1, UpdatedAt: 1},
			},
		}},
	}
	bad := f.insertCraftedWithData(t, b, d)

	_, err := f.repo.Rollback(t.Context(), f.wsID, bad, f.userID, true, f.slug(t))
	if !errors.Is(err, customep.ErrInvalidRow) {
		t.Fatalf("rollback error = %v, want customep.ErrInvalidRow from the write loop's second row", err)
	}

	got := f.entityData(t, resourceID)
	if !mapsEqual(got, before) {
		t.Fatalf("entities after the injected failure = %v, want unchanged %v (the restore must have shared the failed transaction)", got, before)
	}
}

// TestCapture_degradesWhenTheProbeRefuses is property 7's first band: over
// [maxDataProbeBytes], [captureEntitiesTx] reads NO entity row at all and
// the checkpoint is still written, with data_snap NULL — never an error,
// because entity rows are minted by an unauthenticated POST X (D5.2).
func TestCapture_degradesWhenTheProbeRefuses(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 1)

	restoreProbe := maxDataProbeBytes
	t.Cleanup(func() { maxDataProbeBytes = restoreProbe })
	maxDataProbeBytes = 1 // any real population is over this

	s := f.create(t, "проба выше бюджета")
	if s.HasData {
		t.Fatal("HasData = true over the probe's own ceiling, want a degrade (data_snap NULL)")
	}
	var dataSnap []byte
	if err := f.db.R.QueryRowContext(t.Context(),
		"SELECT data_snap FROM checkpoints WHERE id = ?", s.ID).Scan(&dataSnap); err != nil {
		t.Fatalf("read the stored row: %v", err)
	}
	if dataSnap != nil {
		t.Fatal("data_snap is non-NULL despite the probe refusing")
	}
}

// TestCapture_degradesWhenCompressSnapshotRefusesThePassingProbe is
// property 7's second band: the probe (over [maxDataProbeBytes], a
// generous ESTIMATE) can pass while the actual encoded document still
// exceeds [maxSnapshotBytes] — [compressSnapshot]'s write-side ceiling. The
// swallow this test observes is the deliberate INVERSION of
// [compressSnapshot]'s own config-side policy: [Repo.captureSnapshot]
// propagates the identical error, [captureEntitiesTx] degrades instead
// (D5.2).
func TestCapture_degradesWhenCompressSnapshotRefusesThePassingProbe(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 20)

	// Measure both documents at the default ceilings, to place
	// maxSnapshotBytes strictly between them: above config_snap's own size
	// (which must still succeed) and below data_snap's (which must not).
	probe := f.create(t, "измерение")
	configDoc, err := decompressSnapshot(f.storedBlob(t, probe.ID))
	if err != nil {
		t.Fatalf("decompress config_snap: %v", err)
	}
	var rawDataSnap []byte
	if err := f.db.R.QueryRowContext(t.Context(),
		"SELECT data_snap FROM checkpoints WHERE id = ?", probe.ID).Scan(&rawDataSnap); err != nil {
		t.Fatalf("read data_snap: %v", err)
	}
	dataDoc, err := decompressSnapshot(rawDataSnap)
	if err != nil {
		t.Fatalf("decompress data_snap: %v", err)
	}
	if len(dataDoc) <= len(configDoc) {
		t.Fatalf("data document (%d bytes) is not larger than config (%d) — fixture needs more entity rows", len(dataDoc), len(configDoc))
	}

	restoreCeiling := maxSnapshotBytes
	t.Cleanup(func() { maxSnapshotBytes = restoreCeiling })
	maxSnapshotBytes = len(configDoc) + 1 // config still fits; data does not

	s := f.create(t, "проба между границами")
	if s.HasData {
		t.Fatal("HasData = true despite compressSnapshot refusing the entity document")
	}
	var dataSnap []byte
	if err := f.db.R.QueryRowContext(t.Context(),
		"SELECT data_snap FROM checkpoints WHERE id = ?", s.ID).Scan(&dataSnap); err != nil {
		t.Fatalf("read the stored row: %v", err)
	}
	if dataSnap != nil {
		t.Fatal("data_snap is non-NULL despite compressSnapshot refusing")
	}
	var configSnap []byte
	if err := f.db.R.QueryRowContext(t.Context(),
		"SELECT config_snap FROM checkpoints WHERE id = ?", s.ID).Scan(&configSnap); err != nil {
		t.Fatalf("read the stored row: %v", err)
	}
	if configSnap == nil {
		t.Fatal("config_snap is NULL — Create failed entirely instead of degrading only the data half")
	}
}

// TestRollback_dataRestoreOfANestedFamilyKeepsRowsUnderTheirOwnScopes is D10
// clause 33 (D10.3): a nested family's two-scope capture and restoreData:true
// rollback comes back scoped, and parent_entity_id stays NULL throughout —
// the shape D9 makes true of every row this build writes, not merely the
// unscoped ones every other fixture in this file exercises.
//
// This slice adds ZERO production lines to internal/checkpoints or
// internal/bundle beyond the comment rewrites D16 names (D10.1): the codec
// already carries ScopeKey, ValidateData rule 4 already keys on the
// compound (scopeKey, entityKey), canonicalizeData already sorts by it, and
// restoreEntitiesTx's INSERT already binds scope_key from the row. This
// test is the proof that those already-shipped mechanisms in fact do the
// job a nested family needs, not a change that makes them do it.
func TestRollback_dataRestoreOfANestedFamilyKeepsRowsUnderTheirOwnScopes(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/orgs/{}/users", name: "users", idField: "id", wrapper: wrapperJSON, seq: 4, seedCount: 4,
	})
	f.writeDecision(t, "/orgs/{}/users", "confirmed")
	f.insertScopedEntity(t, resourceID, "7", "1")
	f.insertScopedEntity(t, resourceID, "7", "2")
	f.insertScopedEntity(t, resourceID, "8", "1")
	f.insertScopedEntity(t, resourceID, "8", "2")

	point := f.create(t, "снимок вложенной семьи")
	before := f.scopedEntityData(t, resourceID)

	// An anonymous client wrote into BOTH scopes since the checkpoint —
	// the fixture a restore is supposed to overwrite, exactly as
	// TestRollback_dataRestoreOverwritesARowCreatedAfterTheCheckpoint does
	// for the unscoped case, now over two scopes at once.
	f.insertScopedEntity(t, resourceID, "7", "3")
	f.insertScopedEntity(t, resourceID, "8", "3")

	f.rollbackData(t, point.ID)

	got := f.scopedEntityData(t, resourceID)
	if !scopedMapsEqual(got, before) {
		t.Fatalf("scoped entities after restoreData:true = %v, want exactly the checkpoint's %v (both scopes restored, the post-checkpoint rows gone)", got, before)
	}
	// Both scopes must be represented — a bug that dropped one scope's
	// rows entirely, rather than merely failing to overwrite them, would
	// still pass a bare length check against the pre-checkpoint set if it
	// happened to drop and keep the same total count.
	sawScopes := map[string]bool{}
	for k := range got {
		sawScopes[k[0]] = true
	}
	if !sawScopes["7"] || !sawScopes["8"] {
		t.Fatalf("scoped entities after restore cover scopes %v, want both \"7\" and \"8\"", sawScopes)
	}
	if !f.entityParentIsAlwaysNull(t, resourceID) {
		t.Fatal("a restored nested family has a non-NULL parent_entity_id — D9 says every row this build writes keeps it NULL")
	}

	data := f.decodedData(t, point.ID)
	if len(data.Families) != 1 || len(data.Families[0].Rows) != 4 {
		t.Fatalf("captured data = %+v, want one family with 4 rows across two scopes", data.Families)
	}
	scopesInDoc := map[string]int{}
	for _, r := range data.Families[0].Rows {
		scopesInDoc[r.ScopeKey]++
	}
	if scopesInDoc["7"] != 2 || scopesInDoc["8"] != 2 {
		t.Fatalf("data_snap scope distribution = %v, want 2 rows under each of \"7\" and \"8\"", scopesInDoc)
	}
}

// TestRollback_neverRewindsTheEntityCounterOfANestedFamily is
// TestRollback_neverRewindsTheEntityCounter's nested sibling: D5.5's counter
// is family-wide, not per-scope (`resources.seq` has no scope_key column of
// its own), so the existing MAX rule has to keep holding across a family
// whose rows span more than one scope — this is the fixture that proves it
// still does, rather than assuming a flat-scope fixture generalises.
func TestRollback_neverRewindsTheEntityCounterOfANestedFamily(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/orgs/{}/users", name: "users", idField: "id", wrapper: wrapperJSON, seq: 2, seedCount: 2,
	})
	f.writeDecision(t, "/orgs/{}/users", "confirmed")
	// Two scopes share the family-wide counter: key "1" under scope "7"
	// and key "2" under scope "8" are the two rows seq=2 already accounts
	// for — a per-scope counter would instead start each scope at 1, which
	// is exactly the assumption this test exists to rule out.
	f.insertScopedEntity(t, resourceID, "7", "1")
	f.insertScopedEntity(t, resourceID, "8", "2")

	point := f.create(t, "снимок при seq=2, две области")

	// Anonymous clients wrote three more rows spread across both scopes
	// after the checkpoint — seq moved to 5 exactly as in the flat-scope
	// test, and a restore must raise it to that live value regardless of
	// which scope each new row landed in.
	if _, err := f.db.W.ExecContext(t.Context(),
		"UPDATE resources SET seq = 5 WHERE id = ?", resourceID); err != nil {
		t.Fatalf("move seq: %v", err)
	}
	f.insertScopedEntity(t, resourceID, "7", "3")
	f.insertScopedEntity(t, resourceID, "8", "4")
	f.insertScopedEntity(t, resourceID, "7", "5")

	f.rollbackData(t, point.ID)

	got := f.readResource(t, "/orgs/{}/users")
	if got == nil {
		t.Fatal("the resources row disappeared across the rollback")
	}
	if got.seq != 5 {
		t.Fatalf("seq after the rollback = %d, want 5 — the live value across BOTH scopes, never the snapshot's 2", got.seq)
	}
	if got.seedCount != 2 {
		t.Fatalf("seed_count = %d, want the snapshot's 2", got.seedCount)
	}
}

// TestRollback_dataRestoreOfADepthTwoFamilyKeepsScopesByteIdentical is P3g's
// D10/P14: this package's capture and restore change no production line for
// nesting beyond one level — captureEntitiesTx reads scope_key exactly as
// stored and restoreEntitiesTx writes it back VERBATIM, neither one ever
// interpreting it as a chain of ancestor values (see captureEntitiesTx's and
// restoreEntitiesTx's own doc comments). "No change" and "not covered" are
// indistinguishable from the outside, so this is the test that makes the
// claim provable: a depth-2 scope_key — two ancestor values joined the same
// way resources.EncodeScope joins them ("orgID/teamID", each already
// path-escaped) — must round-trip through a capture and a restore with its
// (scope_key, entity_key) pair byte-identical, exactly as
// TestRollback_dataRestoreOfANestedFamilyKeepsRowsUnderTheirOwnScopes proved
// for one ancestor value. This package has no notion of "family depth" at
// all — a two-segment scope_key is opaque to it in exactly the way a
// one-segment one is — which is the whole point: nothing here needs to
// change for the property to hold, and nothing here does.
func TestRollback_dataRestoreOfADepthTwoFamilyKeepsScopesByteIdentical(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/orgs/{}/teams/{}/users", name: "users", idField: "id", wrapper: wrapperJSON, seq: 4, seedCount: 4,
	})
	f.writeDecision(t, "/orgs/{}/teams/{}/users", "confirmed")
	// Two root scopes, two team scopes under the first root — four
	// depth-2 scope keys, deliberately including two DIFFERENT roots so a
	// restore that flattened to the innermost segment alone would collide
	// two of them into one.
	f.insertScopedEntity(t, resourceID, "7/3", "1")
	f.insertScopedEntity(t, resourceID, "7/4", "1")
	f.insertScopedEntity(t, resourceID, "8/3", "1")
	f.insertScopedEntity(t, resourceID, "8/4", "1")

	point := f.create(t, "снимок семьи глубины 2")
	before := f.scopedEntityData(t, resourceID)

	// Mutate every row's data after the checkpoint — the fixture a
	// restore is supposed to overwrite, mirroring
	// TestRollback_dataRestoreOfANestedFamilyKeepsRowsUnderTheirOwnScopes's
	// own "write since the checkpoint" step, done here by an UPDATE rather
	// than a fresh INSERT so the row COUNT cannot by itself prove the
	// restore did anything — only the scope_key/entity_key pair and the
	// data behind it can.
	if _, err := f.db.W.ExecContext(t.Context(),
		"UPDATE entities SET data = '{\"mutated\":true}' WHERE resource_id = ?", resourceID); err != nil {
		t.Fatalf("mutate rows before restore: %v", err)
	}

	f.rollbackData(t, point.ID)

	got := f.scopedEntityData(t, resourceID)
	if !scopedMapsEqual(got, before) {
		t.Fatalf("scoped entities after restoreData:true = %v, want exactly the checkpoint's %v (every depth-2 scope_key restored byte-identical, the post-checkpoint mutation gone)", got, before)
	}
	wantScopes := map[string]bool{"7/3": true, "7/4": true, "8/3": true, "8/4": true}
	sawScopes := map[string]bool{}
	for k := range got {
		sawScopes[k[0]] = true
	}
	if len(sawScopes) != len(wantScopes) {
		t.Fatalf("scoped entities after restore cover scopes %v, want exactly %v", sawScopes, wantScopes)
	}
	for scope := range wantScopes {
		if !sawScopes[scope] {
			t.Fatalf("scoped entities after restore = %v, missing depth-2 scope %q", sawScopes, scope)
		}
	}
	if !f.entityParentIsAlwaysNull(t, resourceID) {
		t.Fatal("a restored depth-2 family has a non-NULL parent_entity_id — D9 (re-decided at every depth by P3g) says every row this build writes keeps it NULL")
	}

	// The document itself must carry the same four scope keys — the
	// property is about capture as much as restore, and a capture that
	// silently truncated a multi-segment scope_key would still pass the
	// restore-side assertions above if restoreEntitiesTx wrote the
	// truncated value back just as verbatim.
	data := f.decodedData(t, point.ID)
	if len(data.Families) != 1 || len(data.Families[0].Rows) != 4 {
		t.Fatalf("captured data = %+v, want one family with 4 rows across four depth-2 scopes", data.Families)
	}
	scopesInDoc := map[string]bool{}
	for _, r := range data.Families[0].Rows {
		scopesInDoc[r.ScopeKey] = true
	}
	for scope := range wantScopes {
		if !scopesInDoc[scope] {
			t.Fatalf("data_snap scope keys = %v, missing depth-2 scope %q", scopesInDoc, scope)
		}
	}
}

// TestRollback_dataRestoreKeepsRowsUnderTheirOwnBaseScope is P3h's P12: a
// checkpoint captures the base scope alongside scope_key and restores it
// verbatim, keeping two base scopes' rows apart the same way
// TestRollback_dataRestoreOfANestedFamilyKeepsRowsUnderTheirOwnScopes proves
// for two ORDINARY scopes — base_scope_key and scope_key are independent
// columns of the same natural key, and this is the property's own proof for
// the one that slice left uncovered. Mutation this test reddens for: drop
// BaseScopeKey from restoreEntitiesTx's INSERT (D9's own named mutation).
func TestRollback_dataRestoreKeepsRowsUnderTheirOwnBaseScope(t *testing.T) {
	f := newFixture(t, 20)
	f.writeSettings(t, settingsWith(f.settings(t), func(s *domain.Settings) {
		s.BasePath = "/orgs/{orgId}"
		s.BasePathValues = []string{"7", "8"}
	}))
	resourceID := f.insertResource(t, resourceFixture{
		family: "/quizzes", name: "quizzes", idField: "id", wrapper: wrapperJSON, seq: 4, seedCount: 4,
	})
	f.writeDecision(t, "/quizzes", "confirmed")
	// entity_key is family-wide (P3g's rule), so it cannot repeat across
	// rows of one family regardless of which base scope each sits in — the
	// same fact TestValidateData_baseScopeKeyDoesNotWidenUniqueness rests
	// on at the codec layer. Two base scopes, one flat scope_key each.
	f.insertBaseScopedEntity(t, resourceID, "7", "", "1")
	f.insertBaseScopedEntity(t, resourceID, "7", "", "2")
	f.insertBaseScopedEntity(t, resourceID, "8", "", "3")
	f.insertBaseScopedEntity(t, resourceID, "8", "", "4")

	point := f.create(t, "снимок с базовыми областями")
	before := f.baseScopedEntityData(t, resourceID)

	// An anonymous client wrote into both base scopes since the checkpoint
	// — the fixture a restore is supposed to overwrite, mirroring
	// TestRollback_dataRestoreOfANestedFamilyKeepsRowsUnderTheirOwnScopes's
	// own step for ordinary scopes.
	f.insertBaseScopedEntity(t, resourceID, "7", "", "5")
	f.insertBaseScopedEntity(t, resourceID, "8", "", "6")

	f.rollbackData(t, point.ID)

	got := f.baseScopedEntityData(t, resourceID)
	if len(got) != len(before) {
		t.Fatalf("base-scoped entities after restoreData:true = %d rows, want exactly the checkpoint's %d", len(got), len(before))
	}
	for k, v := range before {
		if got[k] != v {
			t.Fatalf("base-scoped entities after restoreData:true = %v, want exactly the checkpoint's %v (base scope %q not restored byte-identical)", got, before, k[0])
		}
	}
	// Both base scopes must be represented — a bug that dropped one base
	// scope's rows entirely, rather than merely failing to overwrite them,
	// would still pass a bare length check against the pre-checkpoint set
	// if it happened to drop and keep the same total count.
	sawBases := map[string]bool{}
	for k := range got {
		sawBases[k[0]] = true
	}
	if !sawBases["7"] || !sawBases["8"] {
		t.Fatalf("base-scoped entities after restore cover base scopes %v, want both \"7\" and \"8\"", sawBases)
	}

	data := f.decodedData(t, point.ID)
	if len(data.Families) != 1 || len(data.Families[0].Rows) != 4 {
		t.Fatalf("captured data = %+v, want one family with 4 rows across two base scopes", data.Families)
	}
	basesInDoc := map[string]int{}
	for _, r := range data.Families[0].Rows {
		basesInDoc[r.BaseScopeKey]++
	}
	if basesInDoc["7"] != 2 || basesInDoc["8"] != 2 {
		t.Fatalf("data_snap base scope distribution = %v, want 2 rows under each of \"7\" and \"8\"", basesInDoc)
	}
}

// TestRollback_dataRestoreOfAV1DocumentLandsEveryRowInTheEmptyBaseScope is
// P3h's other half of P12: a checkpoint whose data_snap predates this slice
// — mockerData: 1, no baseScopeKey on any row — still restores, and every
// row lands at base_scope_key = "". Built with bundle.EncodeData over a
// bundle.DataBundle{MockerData: 1, ...} rather than via captureEntitiesTx
// (which, in this build, only ever writes DataVersion 2): a hand-built v1
// document is byte-identical to what EncodeData produces for MockerData: 1
// with every row's BaseScopeKey left at its zero value, because the field
// is `omitempty` — the same reasoning bundle.data_test.go's own hand-built
// literal test uses at the codec layer, done here through the real restore
// path instead. Mutation this test reddens for: refusing a DataVersion < 2
// document at either DecodeData or ValidateData (D9's other named case).
func TestRollback_dataRestoreOfAV1DocumentLandsEveryRowInTheEmptyBaseScope(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 1)

	settings := f.settings(t)
	b := bundle.New("ws-a", settings, bundle.SpecRef{Name: "fixture", Hash: "hash-1"}, nil)
	b.Resources = []bundle.ResourceEntry{{
		RouteFamily: "/widgets", Name: "widgets", IDField: "id", IDStrategy: "seq",
		ScopeParams: []string{}, EntitySchema: "#/components/schemas/Widget",
		Wrapper: jsonx.RawMessage(wrapperJSON), FilterMap: jsonx.RawMessage(`{}`),
		Seq: 1, SeedCount: 1,
	}}
	b.Decisions = []bundle.DecisionEntry{{RouteFamily: "/widgets", State: "confirmed"}}
	b.Endpoints = []bundle.EndpointEntry{}

	v1Data := bundle.DataBundle{
		MockerData: 1, // a pre-P3h document: no baseScopeKey field existed at all
		Families: []bundle.FamilyEntry{
			{RouteFamily: "/widgets", Rows: []bundle.EntityRow{
				{ScopeKey: "", EntityKey: "1", Data: jsonx.RawMessage(`{"id":1,"name":"from a v1 snapshot"}`),
					CreatedAt: 1756370000, UpdatedAt: 1756370000},
			}},
		},
	}
	point := f.insertCraftedWithData(t, b, v1Data)

	f.rollbackData(t, point)

	got := f.baseScopedEntityData(t, resourceID)
	want := map[[3]string]string{
		{"", "", "1"}: `{"id":1,"name":"from a v1 snapshot"}`,
	}
	if len(got) != 1 {
		t.Fatalf("base-scoped entities after restoring a v1 document = %v, want exactly one row", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("row after restoring a v1 document = %v, want %v at (baseScope=%q, scope=%q, key=%q) — every v1 row must land in the empty base scope",
				got, v, k[0], k[1], k[2])
		}
	}
}

// TestRollback_dataRestoreOfANestedFamilyKeepsBaseAndOrdinaryScopesIndependent
// combines P12's own subject with the nested-family fixture
// TestRollback_dataRestoreOfANestedFamilyKeepsRowsUnderTheirOwnScopes already
// pins for ordinary scope_key: a row's base scope and its (ancestor-tuple)
// scope are two independent columns of the natural key, and a restore that
// conflated them — writing one into the other's column, or dropping either
// — must not pass this test's byte-identical comparison across FOUR
// combinations of base scope and ordinary scope.
func TestRollback_dataRestoreOfANestedFamilyKeepsBaseAndOrdinaryScopesIndependent(t *testing.T) {
	f := newFixture(t, 20)
	f.writeSettings(t, settingsWith(f.settings(t), func(s *domain.Settings) {
		s.BasePath = "/tenants/{tenantId}/orgs/{}/users"
		s.BasePathValues = []string{"7", "8"}
	}))
	resourceID := f.insertResource(t, resourceFixture{
		family: "/orgs/{}/users", name: "users", idField: "id", wrapper: wrapperJSON, seq: 4, seedCount: 4,
	})
	f.writeDecision(t, "/orgs/{}/users", "confirmed")
	f.insertBaseScopedEntity(t, resourceID, "7", "100", "1")
	f.insertBaseScopedEntity(t, resourceID, "7", "200", "2")
	f.insertBaseScopedEntity(t, resourceID, "8", "100", "3")
	f.insertBaseScopedEntity(t, resourceID, "8", "200", "4")

	point := f.create(t, "снимок вложенной семьи с базовыми областями")
	before := f.baseScopedEntityData(t, resourceID)

	if _, err := f.db.W.ExecContext(t.Context(),
		"UPDATE entities SET data = '{\"mutated\":true}' WHERE resource_id = ?", resourceID); err != nil {
		t.Fatalf("mutate rows before restore: %v", err)
	}

	f.rollbackData(t, point.ID)

	got := f.baseScopedEntityData(t, resourceID)
	if len(got) != len(before) {
		t.Fatalf("base-scoped entities after restore = %d rows, want exactly the checkpoint's %d", len(got), len(before))
	}
	for k, v := range before {
		if got[k] != v {
			t.Fatalf("base-scoped entities after restore = %v, want exactly the checkpoint's %v (base=%q scope=%q not restored byte-identical)", got, before, k[0], k[1])
		}
	}
}

// settingsWith returns a copy of s with edit applied — a small helper so
// this file's base-path fixtures state only the two fields they change
// rather than reconstructing every domain.Settings field by hand.
func settingsWith(s domain.Settings, edit func(*domain.Settings)) domain.Settings {
	edit(&s)
	return s
}

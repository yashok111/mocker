package checkpoints

import (
	"errors"
	"testing"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/overrides"
)

// P4b's repository tests: what a fork copies that an export never carries
// (assets, scenarios, the active scenario pointer), what neither touches
// on the source, and the export/import round trip at the codec level. The
// handler tests in internal/admin cover the same over HTTP with a confirmed
// family; here the fixture has no resources at all, which is exactly the
// case an INSERT … SELECT over a JOIN on resources must survive.

func insertAsset(t *testing.T, f *fixture, wsID int64, name string) {
	t.Helper()
	if _, err := f.db.W.ExecContext(t.Context(), `
		INSERT INTO assets (workspace_id, name, media_type, size_bytes, sha256, data, created_at, updated_at)
		VALUES (?, ?, 'image/png', 3, 'abc', X'010203', unixepoch(), unixepoch())`, wsID, name); err != nil {
		t.Fatalf("insert asset: %v", err)
	}
}

func insertScenario(t *testing.T, f *fixture, wsID int64, name string) int64 {
	t.Helper()
	res, err := f.db.W.ExecContext(t.Context(), `
		INSERT INTO scenarios (workspace_id, name, snapshot, created_at, edit_version)
		VALUES (?, ?, '{}', unixepoch(), 1)`, wsID, name)
	if err != nil {
		t.Fatalf("insert scenario: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func countRows(t *testing.T, f *fixture, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.R.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func TestFork_copiesAssetsScenariosAndTheActivePointer_sourceUntouched(t *testing.T) {
	f := newFixture(t, 10)
	f.pinStatus(t, 503)
	f.createEndpoint(t, f.wsID, "GET", "/extra")
	insertAsset(t, f, f.wsID, "logo.png")
	insertScenario(t, f, f.wsID, "empty")
	active := insertScenario(t, f, f.wsID, "full")
	if _, err := f.db.W.ExecContext(t.Context(), "UPDATE workspaces SET scenario_id = ? WHERE id = ?", active, f.wsID); err != nil {
		t.Fatal(err)
	}
	srcBefore, err := f.repo.readWorkspaceCore(t.Context(), f.wsID)
	if err != nil {
		t.Fatal(err)
	}

	ws, err := f.repo.Fork(t.Context(), ForkInput{
		SourceID: f.wsID, Name: "copy", IncludeData: true, CreatedBy: f.userID, Label: "копия",
	})
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if ws.ID == f.wsID || ws.ForkedFrom == nil || *ws.ForkedFrom != f.wsID || ws.Slug == "ws-a" {
		t.Errorf("fork = id %d slug %q forkedFrom %v; want a new id and slug pointing at %d", ws.ID, ws.Slug, ws.ForkedFrom, f.wsID)
	}

	// The layer: the pinned override and the custom endpoint.
	row, err := f.ovr.Get(t.Context(), ws.ID, overrides.OpKey("GET", "/thing"))
	if err != nil || row.ActiveStatus == nil || *row.ActiveStatus != 503 {
		t.Errorf("fork override = %+v (err %v); want the pinned 503", row, err)
	}
	if n := countRows(t, f, "SELECT COUNT(*) FROM custom_endpoints WHERE workspace_id = ?", ws.ID); n != 1 {
		t.Errorf("fork custom_endpoints = %d, want 1", n)
	}
	// What an export never carries.
	if n := countRows(t, f, "SELECT COUNT(*) FROM assets WHERE workspace_id = ? AND name = 'logo.png' AND sha256 = 'abc'", ws.ID); n != 1 {
		t.Errorf("fork assets = %d, want the copied logo.png", n)
	}
	if n := countRows(t, f, "SELECT COUNT(*) FROM scenarios WHERE workspace_id = ?", ws.ID); n != 2 {
		t.Errorf("fork scenarios = %d, want 2", n)
	}
	if ws.ScenarioID == nil || *ws.ScenarioID == active {
		t.Fatalf("fork scenario_id = %v; want the COPY of the active scenario, not the source's row %d", ws.ScenarioID, active)
	}
	var name string
	if err := f.db.R.QueryRowContext(t.Context(), "SELECT name FROM scenarios WHERE id = ? AND workspace_id = ?", *ws.ScenarioID, ws.ID).Scan(&name); err != nil || name != "full" {
		t.Errorf("fork's active scenario = %q (err %v), want \"full\" in the fork's own rows", name, err)
	}
	// The copy's edit_version tokens come from ITS sequence, distinct.
	if n := countRows(t, f, "SELECT COUNT(DISTINCT edit_version) FROM scenarios WHERE workspace_id = ?", ws.ID); n != 2 {
		t.Errorf("fork scenarios share an edit_version; want two distinct tokens")
	}
	// A baseline checkpoint, and only one.
	if n := countRows(t, f, "SELECT COUNT(*) FROM checkpoints WHERE workspace_id = ? AND kind = 'manual' AND label = 'копия'", ws.ID); n != 1 {
		t.Errorf("fork checkpoints = %d, want the one baseline", n)
	}

	// The source: no revision bump, no checkpoint, its own scenario pointer.
	srcAfter, err := f.repo.readWorkspaceCore(t.Context(), f.wsID)
	if err != nil {
		t.Fatal(err)
	}
	if srcAfter.revision != srcBefore.revision {
		t.Errorf("source revision %d -> %d; a fork must not write the source", srcBefore.revision, srcAfter.revision)
	}
	if n := countRows(t, f, "SELECT COUNT(*) FROM checkpoints WHERE workspace_id = ?", f.wsID); n != 0 {
		t.Errorf("source gained %d checkpoints on a fork", n)
	}
	var srcScenario int64
	if err := f.db.R.QueryRowContext(t.Context(), "SELECT scenario_id FROM workspaces WHERE id = ?", f.wsID).Scan(&srcScenario); err != nil || srcScenario != active {
		t.Errorf("source scenario_id = %d (err %v), want %d", srcScenario, err, active)
	}
}

func TestFork_refusesASlugThatIsTakenWithoutWritingAnything(t *testing.T) {
	f := newFixture(t, 10)
	insertAsset(t, f, f.wsID, "logo.png")
	_, err := f.repo.Fork(t.Context(), ForkInput{SourceID: f.wsID, Name: "copy", Slug: "ws-a", CreatedBy: f.userID, Label: "x"})
	if err == nil {
		t.Fatal("Fork onto a taken slug succeeded")
	}
	if n := countRows(t, f, "SELECT COUNT(*) FROM workspaces"); n != 1 {
		t.Errorf("workspaces = %d after a refused fork, want 1", n)
	}
	if n := countRows(t, f, "SELECT COUNT(*) FROM assets"); n != 1 {
		t.Errorf("assets = %d after a refused fork, want the source's 1", n)
	}
}

func TestExportImport_roundTripsTheLayerAndRefusesAnUnresolvedSpec(t *testing.T) {
	f := newFixture(t, 10)
	f.pinStatus(t, 418)
	f.createEndpoint(t, f.wsID, "POST", "/extra")

	doc, err := f.repo.Export(t.Context(), f.wsID, true)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if doc.MockerBundle != bundle.CurrentVersion || len(doc.Overrides) != 1 || len(doc.Endpoints) != 1 || doc.Data != nil {
		t.Fatalf("export = version %d, overrides %d, endpoints %d, data %v; want v%d/1/1/nil (no confirmed family)",
			doc.MockerBundle, len(doc.Overrides), len(doc.Endpoints), doc.Data, bundle.CurrentVersion)
	}
	if doc.Spec.Hash != "hash-1" || doc.Spec.Name != "fixture" {
		t.Errorf("export spec = %+v, want the fixture's hash and name", doc.Spec)
	}
	// Encode and decode as the wire would, so the import reads what a file
	// holds rather than the struct the export returned.
	raw, err := bundle.EncodeExport(doc)
	if err != nil {
		t.Fatal(err)
	}
	back, err := bundle.DecodeExport(raw)
	if err != nil {
		t.Fatal(err)
	}

	// The document names a spec; the caller resolved none — refused, no row.
	if _, err := f.repo.Import(t.Context(), ImportInput{Name: "no spec", Document: back, CreatedBy: f.userID, Label: "импорт"}); !errors.Is(err, ErrSpecMissing) {
		t.Errorf("Import with an unresolved spec: err = %v, want ErrSpecMissing", err)
	}
	if n := countRows(t, f, "SELECT COUNT(*) FROM workspaces"); n != 1 {
		t.Errorf("workspaces = %d after the refusal, want 1", n)
	}

	var specID int64
	if err := f.db.R.QueryRowContext(t.Context(), "SELECT id FROM specs WHERE hash = 'hash-1'").Scan(&specID); err != nil {
		t.Fatal(err)
	}
	out, err := f.repo.Import(t.Context(), ImportInput{
		Name: "imported", Slug: "imported", SpecID: &specID, Document: back, CreatedBy: f.userID, Label: "импорт",
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if out.Workspace.Slug != "imported" || out.Workspace.SpecID == nil || *out.Workspace.SpecID != specID || out.EntitiesRestored != 0 {
		t.Errorf("import outcome = %+v", out)
	}
	row, err := f.ovr.Get(t.Context(), out.Workspace.ID, overrides.OpKey("GET", "/thing"))
	if err != nil || row.ActiveStatus == nil || *row.ActiveStatus != 418 {
		t.Errorf("imported override = %+v (err %v); want the pinned 418", row, err)
	}
	if n := countRows(t, f, "SELECT COUNT(*) FROM custom_endpoints WHERE workspace_id = ? AND method = 'POST'", out.Workspace.ID); n != 1 {
		t.Errorf("imported custom_endpoints = %d, want 1", n)
	}
	if n := countRows(t, f, "SELECT COUNT(*) FROM checkpoints WHERE workspace_id = ? AND label = 'импорт'", out.Workspace.ID); n != 1 {
		t.Errorf("imported baseline checkpoints = %d, want 1", n)
	}
	// The import's export is the source's export, name aside.
	again, err := f.repo.Export(t.Context(), out.Workspace.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	again.Workspace.Name = doc.Workspace.Name
	a, _ := bundle.EncodeExport(again)
	b, _ := bundle.EncodeExport(bundle.Export{Bundle: doc.Bundle})
	if string(a) != string(b) {
		t.Errorf("re-export differs from the source's export:\n%s\n%s", a, b)
	}
}

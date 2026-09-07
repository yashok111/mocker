// repo_create_test.go: creating, listing, getting and deleting a
// checkpoint — the CRUD lifecycle, split out of the former repo_test.go
// (package checkpoints, not checkpoints_test — see helpers_test.go's own
// comment for why, and why no test in this package calls t.Parallel).
package checkpoints

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/domain"
)

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

package workspaces_test

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/workspaces"
)

// newTestDB opens a fresh, migrated SQLite file under t.TempDir() and closes
// it on cleanup.
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

// insertUser writes a minimal users row directly. The workspaces package
// owns no user logic, but owner_id is a foreign key and foreign_keys=ON, so
// a non-nil OwnerID needs a real row to point at.
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

func TestRepo_Create(t *testing.T) {
	tests := []struct {
		name     string
		in       workspaces.CreateInput
		wantSlug string
		wantErr  error
	}{
		{
			name:     "explicit slug is used as-is",
			in:       workspaces.CreateInput{Name: "Alex", Slug: "alex"},
			wantSlug: "alex",
		},
		{
			name:     "slug derived from a cyrillic name is transliterated",
			in:       workspaces.CreateInput{Name: "Александр"},
			wantSlug: "aleksandr",
		},
		{
			name:    "reserved explicit slug is rejected",
			in:      workspaces.CreateInput{Name: "Admin", Slug: "admin"},
			wantErr: workspaces.ErrSlugInvalid,
		},
		{
			name:    "malformed explicit slug is rejected",
			in:      workspaces.CreateInput{Name: "Bad", Slug: "-bad"},
			wantErr: workspaces.ErrSlugInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := newTestDB(t)
			repo := workspaces.NewRepo(db)

			ws, err := repo.Create(t.Context(), tt.in)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Create() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create() unexpected error: %v", err)
			}
			if ws.Slug != tt.wantSlug {
				t.Errorf("Slug = %q, want %q", ws.Slug, tt.wantSlug)
			}
			if ws.ID == 0 {
				t.Error("ID is zero, want assigned id")
			}
			if ws.Revision != 1 {
				t.Errorf("Revision = %d, want 1", ws.Revision)
			}
			// A fresh workspace gets domain.DefaultSettings(), which mints its
			// own random signing key — that's the field most likely to be
			// silently left zero if Create ever forgot to fill settings in.
			if ws.Settings.Auth.SigningKey == "" {
				t.Error("Settings.Auth.SigningKey is empty, want a generated key")
			}
			if ws.Settings.ListSize != domain.DefaultSettings().ListSize {
				t.Errorf("Settings.ListSize = %d, want default %d", ws.Settings.ListSize, domain.DefaultSettings().ListSize)
			}

			// Persisted, not just returned.
			reloaded, err := repo.BySlug(t.Context(), ws.Slug)
			if err != nil {
				t.Fatalf("BySlug() after create: %v", err)
			}
			if reloaded.ID != ws.ID {
				t.Errorf("reloaded ID = %d, want %d", reloaded.ID, ws.ID)
			}
		})
	}
}

func TestRepo_Create_duplicateSlug(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := workspaces.NewRepo(db)

	if _, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "Alex", Slug: "alex"}); err != nil {
		t.Fatalf("first Create(): %v", err)
	}

	_, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "Alex Two", Slug: "alex"})
	if !errors.Is(err, workspaces.ErrSlugTaken) {
		t.Fatalf("second Create() error = %v, want ErrSlugTaken", err)
	}
}

// TestRepo_Create_settingsTooLarge is round-1 review finding 3's regression:
// an oversized settings blob must be rejected before it is ever persisted,
// since the mock plane re-parses settings on every unauthenticated request
// to the workspace.
func TestRepo_Create_settingsTooLarge(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := workspaces.NewRepo(db)

	big := domain.DefaultSettings()
	big.NotFoundBody = json.RawMessage(`"` + strings.Repeat("x", 70000) + `"`)

	_, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "Alex", Settings: &big})
	if !errors.Is(err, workspaces.ErrSettingsTooLarge) {
		t.Fatalf("Create() error = %v, want ErrSettingsTooLarge", err)
	}
}

// TestRepo_Create_concurrentSlugDerivation exercises the atomic
// probe-then-insert: several goroutines derive a slug from the same name at
// once, and every one of them must land a distinct slug rather than erroring
// or silently colliding.
func TestRepo_Create_concurrentSlugDerivation(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := workspaces.NewRepo(db)

	const n = 6
	var wg sync.WaitGroup
	slugs := make([]string, n)
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ws, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "Alex"})
			errs[i] = err
			if ws != nil {
				slugs[i] = ws.Slug
			}
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool, n)
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("Create() [%d]: %v", i, errs[i])
		}
		if seen[slugs[i]] {
			t.Fatalf("slug %q assigned to more than one create", slugs[i])
		}
		seen[slugs[i]] = true
	}
	if len(seen) != n {
		t.Fatalf("got %d distinct slugs, want %d", len(seen), n)
	}
}

func TestRepo_ByID(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := workspaces.NewRepo(db)

	created, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "Alex", Slug: "alex"})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}

	t.Run("found", func(t *testing.T) {
		got, err := repo.ByID(t.Context(), created.ID)
		if err != nil {
			t.Fatalf("ByID(): %v", err)
		}
		if got.Slug != "alex" {
			t.Errorf("Slug = %q, want alex", got.Slug)
		}
	})

	t.Run("missing", func(t *testing.T) {
		_, err := repo.ByID(t.Context(), created.ID+1000)
		if !errors.Is(err, workspaces.ErrNotFound) {
			t.Fatalf("ByID() error = %v, want ErrNotFound", err)
		}
	})
}

func TestRepo_BySlug(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := workspaces.NewRepo(db)

	if _, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "Alex", Slug: "alex"}); err != nil {
		t.Fatalf("Create(): %v", err)
	}

	t.Run("found", func(t *testing.T) {
		got, err := repo.BySlug(t.Context(), "alex")
		if err != nil {
			t.Fatalf("BySlug(): %v", err)
		}
		if got.Name != "Alex" {
			t.Errorf("Name = %q, want Alex", got.Name)
		}
	})

	t.Run("missing", func(t *testing.T) {
		_, err := repo.BySlug(t.Context(), "nope")
		if !errors.Is(err, workspaces.ErrNotFound) {
			t.Fatalf("BySlug() error = %v, want ErrNotFound", err)
		}
	})
}

func TestRepo_Update(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := workspaces.NewRepo(db)

	created, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "Alex", Slug: "alex"})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}

	envelope := "response"
	updated, err := repo.Update(t.Context(), created.ID, func(ws *workspaces.Workspace) error {
		ws.Name = "Alex Renamed"
		ws.Settings.ListSize = 42
		ws.Settings.Envelope = &envelope
		return nil
	})
	if err != nil {
		t.Fatalf("Update(): %v", err)
	}
	if updated.Revision != created.Revision+1 {
		t.Errorf("Revision = %d, want %d", updated.Revision, created.Revision+1)
	}
	if updated.Name != "Alex Renamed" {
		t.Errorf("Name = %q, want Alex Renamed", updated.Name)
	}
	if updated.Settings.ListSize != 42 {
		t.Errorf("Settings.ListSize = %d, want 42", updated.Settings.ListSize)
	}

	// Persisted: a fresh read sees the same revision and settings, and a
	// second Update bumps revision by exactly one more, not by however many
	// fields mutate() touched.
	reloaded, err := repo.ByID(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("ByID() after update: %v", err)
	}
	if reloaded.Revision != updated.Revision {
		t.Errorf("reloaded Revision = %d, want %d", reloaded.Revision, updated.Revision)
	}
	if reloaded.Settings.ListSize != 42 {
		t.Errorf("reloaded Settings.ListSize = %d, want 42", reloaded.Settings.ListSize)
	}
	if reloaded.Settings.Envelope == nil || *reloaded.Settings.Envelope != "response" {
		t.Errorf("reloaded Settings.Envelope = %v, want \"response\"", reloaded.Settings.Envelope)
	}

	again, err := repo.Update(t.Context(), created.ID, func(ws *workspaces.Workspace) error {
		ws.Settings.ListSize = 43
		ws.Settings.NullRate = 0.5 // touching a second field must not bump revision twice
		return nil
	})
	if err != nil {
		t.Fatalf("second Update(): %v", err)
	}
	if again.Revision != updated.Revision+1 {
		t.Errorf("Revision after second update = %d, want %d", again.Revision, updated.Revision+1)
	}

	t.Run("mutate error leaves the row untouched", func(t *testing.T) {
		boom := errors.New("boom")
		before, err := repo.ByID(t.Context(), created.ID)
		if err != nil {
			t.Fatalf("ByID(): %v", err)
		}

		_, err = repo.Update(t.Context(), created.ID, func(ws *workspaces.Workspace) error {
			ws.Name = "should not stick"
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("Update() error = %v, want wrapped %v", err, boom)
		}

		after, err := repo.ByID(t.Context(), created.ID)
		if err != nil {
			t.Fatalf("ByID(): %v", err)
		}
		if after.Revision != before.Revision {
			t.Errorf("Revision = %d, want unchanged %d", after.Revision, before.Revision)
		}
		if after.Name != before.Name {
			t.Errorf("Name = %q, want unchanged %q", after.Name, before.Name)
		}
	})

	t.Run("missing workspace", func(t *testing.T) {
		_, err := repo.Update(t.Context(), created.ID+1000, func(ws *workspaces.Workspace) error {
			return nil
		})
		if !errors.Is(err, workspaces.ErrNotFound) {
			t.Fatalf("Update() error = %v, want ErrNotFound", err)
		}
	})

	// Round-1 review finding 3: an oversized settings blob must be rejected,
	// and rejecting it must not leave a partial write behind.
	t.Run("settings too large", func(t *testing.T) {
		before, err := repo.ByID(t.Context(), created.ID)
		if err != nil {
			t.Fatalf("ByID(): %v", err)
		}

		_, err = repo.Update(t.Context(), created.ID, func(ws *workspaces.Workspace) error {
			ws.Settings.NotFoundBody = json.RawMessage(`"` + strings.Repeat("x", 70000) + `"`)
			return nil
		})
		if !errors.Is(err, workspaces.ErrSettingsTooLarge) {
			t.Fatalf("Update() error = %v, want ErrSettingsTooLarge", err)
		}

		after, err := repo.ByID(t.Context(), created.ID)
		if err != nil {
			t.Fatalf("ByID() after rejected update: %v", err)
		}
		if after.Revision != before.Revision {
			t.Errorf("Revision = %d, want unchanged %d", after.Revision, before.Revision)
		}
	})
}

// TestRepo_Create_bootstrapsEditVersion pins D4's bootstrap rule: creation
// INSERTs edit_seq = 1 AND edit_version = 1 in the same statement, since the
// allocator's UPDATE has no row to update until the INSERT has run.
func TestRepo_Create_bootstrapsEditVersion(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := workspaces.NewRepo(db)

	created, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "Alex", Slug: "alex"})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if created.EditVersion != 1 {
		t.Errorf("EditVersion = %d, want 1", created.EditVersion)
	}
	if created.EditSeq != 1 {
		t.Errorf("EditSeq = %d, want 1", created.EditSeq)
	}

	reloaded, err := repo.ByID(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("ByID(): %v", err)
	}
	if reloaded.EditVersion != 1 {
		t.Errorf("reloaded EditVersion = %d, want 1", reloaded.EditVersion)
	}
}

// TestRepo_UpdateExpecting exercises D7's five-case table against the
// workspaces table's own CAS sibling.
func TestRepo_UpdateExpecting(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := workspaces.NewRepo(db)

	noop := func(ws *workspaces.Workspace) error { return nil }

	t.Run("no expectation: no check, behaves like Update", func(t *testing.T) {
		created, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "A", Slug: "a-noexpect"})
		if err != nil {
			t.Fatalf("Create(): %v", err)
		}

		updated, err := repo.UpdateExpecting(t.Context(), created.ID, nil, func(ws *workspaces.Workspace) error {
			ws.Name = "A renamed"
			return nil
		})
		if err != nil {
			t.Fatalf("UpdateExpecting(nil): %v", err)
		}
		if updated.Name != "A renamed" {
			t.Errorf("Name = %q, want %q", updated.Name, "A renamed")
		}
		// A write always allocates a fresh edit_version when it proceeds,
		// whether or not the caller sent an expectation (D9's criterion is
		// about the fields written, not about whether a check happened).
		if updated.EditVersion == created.EditVersion {
			t.Errorf("EditVersion = %d, want a fresh value distinct from %d", updated.EditVersion, created.EditVersion)
		}
	})

	t.Run("expected 0 against a live row: conflict, never ignored", func(t *testing.T) {
		created, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "B", Slug: "b-zero"})
		if err != nil {
			t.Fatalf("Create(): %v", err)
		}

		zero := int64(0)
		_, err = repo.UpdateExpecting(t.Context(), created.ID, &zero, noop)
		var conflict *store.EditConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("UpdateExpecting(expect=0) error = %v, want *store.EditConflictError", err)
		}
		if conflict.Gone {
			t.Errorf("Gone = true, want false: the row exists")
		}
		current, ok := conflict.Current.(workspaces.Workspace)
		if !ok {
			t.Fatalf("Current = %T, want workspaces.Workspace", conflict.Current)
		}
		if current.EditVersion != created.EditVersion {
			t.Errorf("Current.EditVersion = %d, want %d", current.EditVersion, created.EditVersion)
		}

		// Refused, so the row must be untouched.
		reloaded, err := repo.ByID(t.Context(), created.ID)
		if err != nil {
			t.Fatalf("ByID(): %v", err)
		}
		if reloaded.Name != "B" {
			t.Errorf("Name = %q, want unchanged %q", reloaded.Name, "B")
		}
	})

	t.Run("expected current version: proceeds, allocates a fresh version (never +1 in place)", func(t *testing.T) {
		created, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "C", Slug: "c-match"})
		if err != nil {
			t.Fatalf("Create(): %v", err)
		}

		expect := created.EditVersion
		updated, err := repo.UpdateExpecting(t.Context(), created.ID, &expect, func(ws *workspaces.Workspace) error {
			ws.Name = "C renamed"
			return nil
		})
		if err != nil {
			t.Fatalf("UpdateExpecting(): %v", err)
		}
		if updated.EditVersion <= expect {
			t.Errorf("EditVersion = %d, want strictly greater than expected %d", updated.EditVersion, expect)
		}
		if updated.EditVersion == expect+1 {
			// Not itself wrong in isolation, but this repo's allocator is
			// shared with every other write in the workspace, so asserting
			// exact +1 here would over-specify; the real property (allocated,
			// not reused) is covered by the reuse-after-delete case below.
			t.Logf("EditVersion happens to equal expect+1 = %d; that is incidental, not guaranteed", updated.EditVersion)
		}
	})

	t.Run("expected stale version against live row: conflict carries current row", func(t *testing.T) {
		created, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "D", Slug: "d-stale"})
		if err != nil {
			t.Fatalf("Create(): %v", err)
		}
		staleExpect := created.EditVersion

		// First writer succeeds and moves the row to a new version.
		first, err := repo.UpdateExpecting(t.Context(), created.ID, &staleExpect, func(ws *workspaces.Workspace) error {
			ws.Name = "D first"
			return nil
		})
		if err != nil {
			t.Fatalf("first UpdateExpecting(): %v", err)
		}

		// Second writer, still holding the pre-first-write token, is refused
		// rather than silently overwriting the first writer's edit.
		_, err = repo.UpdateExpecting(t.Context(), created.ID, &staleExpect, func(ws *workspaces.Workspace) error {
			ws.Name = "D second (must not stick)"
			return nil
		})
		var conflict *store.EditConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("second UpdateExpecting() error = %v, want *store.EditConflictError", err)
		}
		if conflict.Gone {
			t.Errorf("Gone = true, want false: the row exists")
		}
		current, ok := conflict.Current.(workspaces.Workspace)
		if !ok {
			t.Fatalf("Current = %T, want workspaces.Workspace", conflict.Current)
		}
		if current.EditVersion != first.EditVersion {
			t.Errorf("Current.EditVersion = %d, want %d (the winner's version)", current.EditVersion, first.EditVersion)
		}

		reloaded, err := repo.ByID(t.Context(), created.ID)
		if err != nil {
			t.Fatalf("ByID(): %v", err)
		}
		if reloaded.Name != "D first" {
			t.Errorf("Name = %q, want the first writer's edit to have stuck: %q", reloaded.Name, "D first")
		}
	})

	t.Run("expected version against a deleted row: ErrEditConflict, not ErrNotFound", func(t *testing.T) {
		created, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "E", Slug: "e-deleted"})
		if err != nil {
			t.Fatalf("Create(): %v", err)
		}
		expect := created.EditVersion

		if err := repo.Delete(t.Context(), created.ID); err != nil {
			t.Fatalf("Delete(): %v", err)
		}

		_, err = repo.UpdateExpecting(t.Context(), created.ID, &expect, noop)
		var conflict *store.EditConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("UpdateExpecting() after delete, error = %v, want *store.EditConflictError", err)
		}
		if !conflict.Gone {
			t.Errorf("Gone = false, want true: the target row was deleted")
		}
		if conflict.Current != nil {
			t.Errorf("Current = %v, want nil when Gone", conflict.Current)
		}
	})

	t.Run("no expectation against a deleted row: still ErrNotFound, unchanged from Update", func(t *testing.T) {
		created, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "F", Slug: "f-deleted-noexpect"})
		if err != nil {
			t.Fatalf("Create(): %v", err)
		}
		if err := repo.Delete(t.Context(), created.ID); err != nil {
			t.Fatalf("Delete(): %v", err)
		}

		_, err = repo.UpdateExpecting(t.Context(), created.ID, nil, noop)
		if !errors.Is(err, workspaces.ErrNotFound) {
			t.Fatalf("UpdateExpecting(nil) after delete, error = %v, want ErrNotFound", err)
		}
		var conflict *store.EditConflictError
		if errors.As(err, &conflict) {
			t.Fatalf("UpdateExpecting(nil) after delete returned an EditConflictError; want plain ErrNotFound since no expectation was sent")
		}
	})
}

func TestRepo_Delete(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := workspaces.NewRepo(db)

	created, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "Alex", Slug: "alex"})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}

	if err := repo.Delete(t.Context(), created.ID); err != nil {
		t.Fatalf("Delete(): %v", err)
	}

	if _, err := repo.ByID(t.Context(), created.ID); !errors.Is(err, workspaces.ErrNotFound) {
		t.Fatalf("ByID() after delete: error = %v, want ErrNotFound", err)
	}

	if err := repo.Delete(t.Context(), created.ID); !errors.Is(err, workspaces.ErrNotFound) {
		t.Fatalf("Delete() of already-deleted row: error = %v, want ErrNotFound", err)
	}
}

func TestRepo_List(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := workspaces.NewRepo(db)

	owner1 := insertUser(t, db, "owner-one")
	owner2 := insertUser(t, db, "owner-two")

	ws1, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "First", Slug: "first", OwnerID: &owner1})
	if err != nil {
		t.Fatalf("Create(first): %v", err)
	}
	ws2, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "Second", Slug: "second", OwnerID: &owner2})
	if err != nil {
		t.Fatalf("Create(second): %v", err)
	}
	ws3, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "Third", Slug: "third", OwnerID: &owner1})
	if err != nil {
		t.Fatalf("Create(third): %v", err)
	}

	t.Run("unfiltered lists everyone in id order", func(t *testing.T) {
		got, err := repo.List(t.Context(), nil)
		if err != nil {
			t.Fatalf("List(): %v", err)
		}
		wantIDs := []int64{ws1.ID, ws2.ID, ws3.ID}
		if len(got) != len(wantIDs) {
			t.Fatalf("List() returned %d workspaces, want %d", len(got), len(wantIDs))
		}
		for i, id := range wantIDs {
			if got[i].ID != id {
				t.Errorf("List()[%d].ID = %d, want %d", i, got[i].ID, id)
			}
		}
	})

	t.Run("filtered by owner", func(t *testing.T) {
		got, err := repo.List(t.Context(), &owner1)
		if err != nil {
			t.Fatalf("List(owner1): %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("List(owner1) returned %d workspaces, want 2", len(got))
		}
		if got[0].ID != ws1.ID || got[1].ID != ws3.ID {
			t.Errorf("List(owner1) = [%d, %d], want [%d, %d]", got[0].ID, got[1].ID, ws1.ID, ws3.ID)
		}
	})

	t.Run("filtered by owner with nothing owned", func(t *testing.T) {
		none := int64(999999)
		got, err := repo.List(t.Context(), &none)
		if err != nil {
			t.Fatalf("List(none): %v", err)
		}
		if len(got) != 0 {
			t.Errorf("List(none) returned %d workspaces, want 0", len(got))
		}
	})
}

// insertSpec writes a minimal specs row directly. workspaces.EnsureDefault
// takes a specID and stores it as a foreign key (foreign_keys=ON), so a
// test exercising it needs a real row to point at — mirrors insertUser's own
// reasoning above.
func insertSpec(t *testing.T, db *store.DB, name string) int64 {
	t.Helper()
	res, err := db.W.ExecContext(t.Context(), `
		INSERT INTO specs (name, format, source, hash, raw, normalized, created_at)
		VALUES (?, 'oas31', 'upload', ?, x'7b7d', x'7b7d', unixepoch())`,
		name, name+"-hash")
	if err != nil {
		t.Fatalf("insert spec %q: %v", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("spec id: %v", err)
	}
	return id
}

// TestRepo_EnsureDefault covers DESIGN §14 screen 2's auto-create: a
// zero-workspace owner gets exactly one workspace against the given spec,
// an owner who already has one is left alone, and the picked slug comes
// back on the Workspace so the caller can show it.
func TestRepo_EnsureDefault(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := workspaces.NewRepo(db)
	specID := insertSpec(t, db, "Widgets API")

	t.Run("creates one for a zero-workspace owner", func(t *testing.T) {
		t.Parallel()
		owner := insertUser(t, db, "alex-ensure-new")

		ws, err := repo.EnsureDefault(t.Context(), owner, "Alex Ensure New", specID, nil)
		if err != nil {
			t.Fatalf("EnsureDefault(): %v", err)
		}
		if ws == nil {
			t.Fatal("EnsureDefault() = nil, want a created workspace")
		}
		if ws.Slug == "" {
			t.Error("EnsureDefault() workspace has an empty slug — the picked slug must be shown, DESIGN §14")
		}
		if ws.SpecID == nil || *ws.SpecID != specID {
			t.Errorf("EnsureDefault() SpecID = %v, want %d", ws.SpecID, specID)
		}
		if ws.OwnerID == nil || *ws.OwnerID != owner {
			t.Errorf("EnsureDefault() OwnerID = %v, want %d", ws.OwnerID, owner)
		}

		got, err := repo.List(t.Context(), &owner)
		if err != nil {
			t.Fatalf("List(): %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("owner has %d workspaces after EnsureDefault, want 1", len(got))
		}
	})

	t.Run("leaves an owner with an existing workspace alone", func(t *testing.T) {
		t.Parallel()
		owner := insertUser(t, db, "alex-ensure-existing")
		manual, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "Manual", OwnerID: &owner})
		if err != nil {
			t.Fatalf("Create(): %v", err)
		}

		ws, err := repo.EnsureDefault(t.Context(), owner, "Alex Ensure Existing", specID, nil)
		if err != nil {
			t.Fatalf("EnsureDefault(): %v", err)
		}
		if ws != nil {
			t.Fatalf("EnsureDefault() = %+v, want nil (owner already had a workspace)", ws)
		}

		got, err := repo.List(t.Context(), &owner)
		if err != nil {
			t.Fatalf("List(): %v", err)
		}
		if len(got) != 1 || got[0].ID != manual.ID {
			t.Fatalf("owner's workspaces changed: got %d rows, want exactly the 1 manual one", len(got))
		}
	})
}

// TestRepo_EnsureDefault_concurrentFirstLogin is the race DESIGN §14 screen 2
// calls out by name: two logins for the same brand-new user racing must not
// create two workspaces (nor half of one). It works for the same reason
// TestRepo_Create_concurrentSlugDerivation's slug race does — store.DB.W is
// a single connection, so store.DB.Write serializes the "does the owner
// already own one" check against the insert process-wide — but this test
// asserts the OUTCOME that reasoning predicts (exactly one row, not zero
// races on distinct slugs) rather than re-deriving the mechanism.
func TestRepo_EnsureDefault_concurrentFirstLogin(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := workspaces.NewRepo(db)
	specID := insertSpec(t, db, "Widgets API Concurrent")
	owner := insertUser(t, db, "alex-ensure-race")

	const n = 6
	var wg sync.WaitGroup
	results := make([]*workspaces.Workspace, n)
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ws, err := repo.EnsureDefault(t.Context(), owner, "Alex Ensure Race", specID, nil)
			results[i] = ws
			errs[i] = err
		}(i)
	}
	wg.Wait()

	created := 0
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("EnsureDefault() [%d]: %v", i, errs[i])
		}
		if results[i] != nil {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("%d of %d concurrent EnsureDefault calls created a workspace, want exactly 1", created, n)
	}

	got, err := repo.List(t.Context(), &owner)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("owner ended up with %d workspaces, want 1", len(got))
	}
}

func TestRepo_SlugTaken(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := workspaces.NewRepo(db)

	if _, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "Alex", Slug: "alex"}); err != nil {
		t.Fatalf("Create(): %v", err)
	}

	tests := []struct {
		name string
		slug string
		want bool
	}{
		{name: "taken", slug: "alex", want: true},
		{name: "free", slug: "someone-else", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := repo.SlugTaken(t.Context(), tt.slug)
			if err != nil {
				t.Fatalf("SlugTaken(%q): %v", tt.slug, err)
			}
			if got != tt.want {
				t.Errorf("SlugTaken(%q) = %v, want %v", tt.slug, got, tt.want)
			}
		})
	}
}

// TestRepo_Delete_forkSourceDetachesItsCopies pins the 2026-09-03 audit
// finding: forked_from REFERENCES workspaces(id) with no ON DELETE clause
// and foreign_keys=ON, so deleting a fork's source used to be REFUSED by
// the schema ("FOREIGN KEY constraint failed" → a 500), not left dangling
// as P4b's carve-out describes. Delete now detaches the copies first.
func TestRepo_Delete_forkSourceDetachesItsCopies(t *testing.T) {
	db := newTestDB(t)
	repo := workspaces.NewRepo(db)

	src, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "Source"})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	srcID := src.ID
	copyWS, err := repo.Create(t.Context(), workspaces.CreateInput{Name: "Copy", ForkedFrom: &srcID})
	if err != nil {
		t.Fatalf("create copy: %v", err)
	}
	if copyWS.ForkedFrom == nil || *copyWS.ForkedFrom != srcID {
		t.Fatalf("copy.ForkedFrom = %v, want %d", copyWS.ForkedFrom, srcID)
	}

	if err := repo.Delete(t.Context(), srcID); err != nil {
		t.Fatalf("delete the fork source: %v", err)
	}
	after, err := repo.ByID(t.Context(), copyWS.ID)
	if err != nil {
		t.Fatalf("copy after the source's delete: %v", err)
	}
	if after.ForkedFrom != nil {
		t.Fatalf("copy.ForkedFrom = %d after the source's delete, want nil", *after.ForkedFrom)
	}
}

package assets_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/assets"
	"github.com/yashok111/mocker/internal/store"
)

// newTestDB opens a fresh, migrated SQLite file under t.TempDir(), the
// harness every repository test in this tree uses.
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

// newWorkspace inserts one workspace row and returns its id, the same raw
// INSERT the scenarios and customep tests use so this package does not
// import internal/workspaces for a fixture.
func newWorkspace(t *testing.T, db *store.DB, slug string) int64 {
	t.Helper()
	now := time.Now().Unix()
	res, err := db.W.ExecContext(t.Context(),
		`INSERT INTO workspaces (slug, name, spec_id, revision, settings, created_at, updated_at)
		 VALUES (?, ?, NULL, 1, '{}', ?, ?)`, slug, slug, now, now)
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func revisionOf(t *testing.T, db *store.DB, id int64) int64 {
	t.Helper()
	var rev int64
	if err := db.R.QueryRowContext(t.Context(), "SELECT revision FROM workspaces WHERE id = ?", id).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	return rev
}

func TestValidName(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"avatar-1.jpg", "a.b.c", "_", "A", strings.Repeat("x", 128)} {
		if !assets.ValidName(ok) {
			t.Errorf("ValidName(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", ".", "..", "a/b", "a b", "a%20b", strings.Repeat("x", 129), "ü.png", "a\n"} {
		if assets.ValidName(bad) {
			t.Errorf("ValidName(%q) = true, want false", bad)
		}
	}
	if name, ok := assets.NameFromBodyRef("asset:avatar.jpg"); !ok || name != "avatar.jpg" {
		t.Errorf("NameFromBodyRef = %q,%v", name, ok)
	}
	for _, bad := range []string{"avatar.jpg", "asset:", "asset:a/b", "file:avatar.jpg", ""} {
		if _, ok := assets.NameFromBodyRef(bad); ok {
			t.Errorf("NameFromBodyRef(%q) accepted", bad)
		}
	}
}

// TestPut_createThenReplace is A1: created true then false, the bytes and
// the type replaced, created_at kept, revision bumped once per write inside
// the write's own transaction (no deadlock — the call returns).
func TestPut_createThenReplace(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ws := newWorkspace(t, db, "alex")
	repo := assets.NewRepo(db, 1<<20, 4<<20)
	ctx := t.Context()

	m1, created, err := repo.Put(ctx, ws, "avatar.jpg", "image/jpeg", []byte("JPEG-ONE"))
	if err != nil || !created {
		t.Fatalf("first Put: created=%v err=%v", created, err)
	}
	if m1.SizeBytes != 8 || m1.MediaType != "image/jpeg" || len(m1.SHA256) != 64 {
		t.Fatalf("meta = %+v", m1)
	}
	if got := revisionOf(t, db, ws); got != 2 {
		t.Fatalf("revision after create = %d, want 2", got)
	}

	m2, created, err := repo.Put(ctx, ws, "avatar.jpg", "image/webp", []byte("WEBP-TWO!"))
	if err != nil || created {
		t.Fatalf("second Put: created=%v err=%v", created, err)
	}
	if m2.SizeBytes != 9 || m2.MediaType != "image/webp" || m2.SHA256 == m1.SHA256 {
		t.Fatalf("replaced meta = %+v", m2)
	}
	if !m2.CreatedAt.Equal(m1.CreatedAt) {
		t.Fatalf("created_at moved on replace: %v → %v", m1.CreatedAt, m2.CreatedAt)
	}
	if got := revisionOf(t, db, ws); got != 3 {
		t.Fatalf("revision after replace = %d, want 3", got)
	}

	meta, data, err := repo.Get(ctx, ws, "avatar.jpg")
	if err != nil || !bytes.Equal(data, []byte("WEBP-TWO!")) || meta.MediaType != "image/webp" {
		t.Fatalf("Get = %+v %q %v", meta, data, err)
	}
	list, err := repo.List(ctx, ws)
	if err != nil || len(list) != 1 || list[0].Name != "avatar.jpg" {
		t.Fatalf("List = %+v %v", list, err)
	}
	total, err := repo.TotalBytes(ctx, ws)
	if err != nil || total != 9 {
		t.Fatalf("TotalBytes = %d %v", total, err)
	}
}

func TestPut_refusals(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ws := newWorkspace(t, db, "alex")
	repo := assets.NewRepo(db, 16, 40)
	ctx := t.Context()

	if _, _, err := repo.Put(ctx, ws, "a/b", "image/png", []byte("x")); !errors.Is(err, assets.ErrInvalidName) {
		t.Errorf("bad name: %v", err)
	}
	if _, _, err := repo.Put(ctx, ws, "big.bin", "image/png", make([]byte, 17)); !errors.Is(err, assets.ErrTooLarge) {
		t.Errorf("too large: %v", err)
	}
	if _, _, err := repo.Put(ctx, 999, "a.png", "image/png", []byte("x")); !errors.Is(err, assets.ErrWorkspaceNotFound) {
		t.Errorf("missing workspace: %v", err)
	}
	if got := revisionOf(t, db, ws); got != 1 {
		t.Fatalf("a refused Put bumped revision to %d", got)
	}
}

// TestPut_quotaExcludesTheReplacedRow is A1's replace-near-the-ceiling
// clause: 16 + 16 + 8 = 40 fills the quota exactly; replacing one 16-byte
// row with another 16-byte body must succeed (the old row is excluded from
// the sum), and a 9-byte addition must be refused.
func TestPut_quotaExcludesTheReplacedRow(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ws := newWorkspace(t, db, "alex")
	repo := assets.NewRepo(db, 16, 40)
	ctx := t.Context()

	for _, f := range []struct {
		name string
		n    int
	}{{"a", 16}, {"b", 16}, {"c", 8}} {
		if _, _, err := repo.Put(ctx, ws, f.name, "application/octet-stream", make([]byte, f.n)); err != nil {
			t.Fatalf("seed %s: %v", f.name, err)
		}
	}
	if _, _, err := repo.Put(ctx, ws, "a", "application/octet-stream", bytes.Repeat([]byte("y"), 16)); err != nil {
		t.Fatalf("replace at the ceiling: %v", err)
	}
	if _, _, err := repo.Put(ctx, ws, "d", "application/octet-stream", make([]byte, 9)); !errors.Is(err, assets.ErrQuota) {
		t.Fatalf("over quota: %v", err)
	}
	if _, _, err := repo.Put(ctx, ws, "c", "application/octet-stream", make([]byte, 9)); !errors.Is(err, assets.ErrQuota) {
		t.Fatalf("replace that grows over quota: %v", err)
	}
}

// TestPut_concurrentQuotaIsAtomic: two Puts that each fit alone (16 each
// under a 24-byte quota) and not together leave exactly one row.
func TestPut_concurrentQuotaIsAtomic(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ws := newWorkspace(t, db, "alex")
	repo := assets.NewRepo(db, 16, 24)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, name := range []string{"one", "two"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[i] = repo.Put(context.Background(), ws, name, "application/octet-stream", make([]byte, 16))
		}()
	}
	wg.Wait()
	okN := 0
	for _, err := range errs {
		switch {
		case err == nil:
			okN++
		case errors.Is(err, assets.ErrQuota):
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	list, err := repo.List(t.Context(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if okN != 1 || len(list) != 1 {
		t.Fatalf("ok=%d rows=%d, want exactly one", okN, len(list))
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ws := newWorkspace(t, db, "alex")
	repo := assets.NewRepo(db, 1<<20, 4<<20)
	ctx := t.Context()
	if _, _, err := repo.Put(ctx, ws, "a.png", "image/png", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, ws, "a.png", "wrong"); !errors.Is(err, assets.ErrConfirmSlug) {
		t.Fatalf("wrong slug: %v", err)
	}
	if err := repo.Delete(ctx, ws, "nope.png", "wrong"); !errors.Is(err, assets.ErrConfirmSlug) {
		t.Fatalf("wrong slug on a missing name must refuse the same way, got %v", err)
	}
	if err := repo.Delete(ctx, ws, "a.png", "alex"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := repo.Delete(ctx, ws, "a.png", "alex"); !errors.Is(err, assets.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := repo.Meta(ctx, ws, "a.png"); !errors.Is(err, assets.ErrNotFound) {
		t.Fatalf("Meta after delete: %v", err)
	}
	if got := revisionOf(t, db, ws); got != 3 {
		t.Fatalf("revision = %d, want 3 (one put, one delete)", got)
	}
}

package luastate

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testkit"
	"github.com/yashok111/mocker/internal/testleak"
)

// The leak guard every package with tests carries; see CLAUDE.md's goleak
// paragraph.
func TestMain(m *testing.M) { testleak.VerifyTestMain(m) }

// insertWorkspace writes a minimal workspaces row directly, exactly as
// internal/customep/repo_test.go does — this package owns no workspace
// logic and must not import internal/workspaces, and the FK the table
// carries makes a real row the honest fixture.
func insertWorkspace(t *testing.T, db *store.DB, id int64) {
	t.Helper()
	if _, err := db.W.ExecContext(t.Context(), `
		INSERT INTO workspaces (id, slug, name, revision, settings, created_at, updated_at)
		VALUES (?, ?, ?, 1, '{}', unixepoch(), unixepoch())`,
		id, "ws"+strings.Repeat("x", int(id)), "ws"+string(rune('A'+id)),
	); err != nil {
		t.Fatalf("insert workspace %d: %v", id, err)
	}
}

func TestSetGetDelete_roundTrip(t *testing.T) {
	db := testkit.NewDB(t)
	insertWorkspace(t, db, 1)
	r := NewRepo(db)
	ctx := t.Context()

	if err := r.Set(ctx, 1, "session/abc", jsonx.RawMessage(`{"group":"g1"}`)); err != nil {
		t.Fatal(err)
	}
	raw, found, err := r.Get(ctx, 1, "session/abc")
	if err != nil || !found {
		t.Fatalf("get = %q, %v, %v", raw, found, err)
	}
	// Byte for byte: a value round-trips exactly as stored, never re-encoded.
	if string(raw) != `{"group":"g1"}` {
		t.Fatalf("value = %s, want the stored bytes", raw)
	}

	// A miss is found:false, never an error — the cold start of a session is
	// the ordinary first call, not a failure.
	if _, found, err := r.Get(ctx, 1, "absent"); err != nil || found {
		t.Fatalf("absent get = %v, %v, want false, nil", found, err)
	}

	deleted, err := r.Delete(ctx, 1, "session/abc")
	if err != nil || !deleted {
		t.Fatalf("delete = %v, %v", deleted, err)
	}
	if deleted, err := r.Delete(ctx, 1, "session/abc"); err != nil || deleted {
		t.Fatalf("second delete = %v, %v, want false, nil", deleted, err)
	}
}

func TestSet_scopesByWorkspace(t *testing.T) {
	db := testkit.NewDB(t)
	insertWorkspace(t, db, 1)
	insertWorkspace(t, db, 2)
	r := NewRepo(db)
	ctx := t.Context()

	if err := r.Set(ctx, 1, "k", jsonx.RawMessage(`1`)); err != nil {
		t.Fatal(err)
	}
	if err := r.Set(ctx, 2, "k", jsonx.RawMessage(`2`)); err != nil {
		t.Fatal(err)
	}
	raw, found, err := r.Get(ctx, 1, "k")
	if err != nil || !found || string(raw) != `1` {
		t.Fatalf("workspace 1 sees %q, %v, %v", raw, found, err)
	}
}

func TestSet_overwritesUnderTheSameKey(t *testing.T) {
	db := testkit.NewDB(t)
	insertWorkspace(t, db, 1)
	r := NewRepo(db)
	ctx := t.Context()

	for _, v := range []string{`"a"`, `"b"`} {
		if err := r.Set(ctx, 1, "k", jsonx.RawMessage(v)); err != nil {
			t.Fatal(err)
		}
	}
	raw, _, err := r.Get(ctx, 1, "k")
	if err != nil || string(raw) != `"b"` {
		t.Fatalf("value = %s, %v — a session reassigns, last write wins", raw, err)
	}
}

func TestSet_caps(t *testing.T) {
	db := testkit.NewDB(t)
	insertWorkspace(t, db, 1)
	insertWorkspace(t, db, 2)
	r := NewRepo(db)
	ctx := t.Context()

	if err := r.Set(ctx, 1, "", jsonx.RawMessage(`1`)); !errors.Is(err, ErrBadKey) {
		t.Fatalf("empty key = %v, want ErrBadKey", err)
	}
	if err := r.Set(ctx, 1, strings.Repeat("k", MaxKeyBytes+1), jsonx.RawMessage(`1`)); !errors.Is(err, ErrBadKey) {
		t.Fatalf("long key = %v, want ErrBadKey", err)
	}
	if err := r.Set(ctx, 1, "big", jsonx.RawMessage(`"`+strings.Repeat("x", MaxValueBytes)+`"`)); !errors.Is(err, ErrValueTooBig) {
		t.Fatalf("big value = %v, want ErrValueTooBig", err)
	}

	// The count cap gates NEW keys only: the same key rewritten a thousand
	// times never trips it, the thousand-and-first distinct key does.
	for i := 0; i < MaxKeysPerWorkspace; i++ {
		if err := r.Set(ctx, 1, fmt.Sprintf("k%04d", i), jsonx.RawMessage(`1`)); err != nil {
			t.Fatalf("key %d: %v", i, err)
		}
	}
	if err := r.Set(ctx, 1, "one-too-many", jsonx.RawMessage(`1`)); !errors.Is(err, ErrTooManyKeys) {
		t.Fatalf("1001st key = %v, want ErrTooManyKeys", err)
	}
	if err := r.Set(ctx, 1, "k0000", jsonx.RawMessage(`2`)); err != nil {
		t.Fatalf("rewrite under a full cap = %v, want nil", err)
	}

	// And the cap is per workspace, never global: workspace 2 starts fresh.
	if err := r.Set(ctx, 2, "fresh", jsonx.RawMessage(`1`)); err != nil {
		t.Fatalf("other workspace: %v", err)
	}
}

func TestWipeTx_wipesOnlyItsWorkspaceInsideACallerTransaction(t *testing.T) {
	db := testkit.NewDB(t)
	insertWorkspace(t, db, 1)
	insertWorkspace(t, db, 2)
	r := NewRepo(db)
	ctx := t.Context()

	// Two keys on workspace 1, one on workspace 2: the wipe answers for its
	// workspace only.
	for _, k := range []struct {
		ws  int64
		key string
	}{{1, "a"}, {1, "b"}, {2, "a"}} {
		if err := r.Set(ctx, k.ws, k.key, jsonx.RawMessage(`1`)); err != nil {
			t.Fatal(err)
		}
	}
	var wiped int64
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		wiped, err = WipeTx(ctx, tx, 1)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if wiped != 2 {
		t.Fatalf("wiped = %d, want 2 — the reset's no-op detection reads this count", wiped)
	}
	if _, found, _ := r.Get(ctx, 1, "a"); found {
		t.Fatal("workspace 1 state survived the wipe")
	}
	if _, found, _ := r.Get(ctx, 2, "a"); !found {
		t.Fatal("workspace 2 state lost to workspace 1's wipe")
	}
}

func TestWorkspaceDeleteCascades_theFK(t *testing.T) {
	db := testkit.NewDB(t)
	insertWorkspace(t, db, 1)
	r := NewRepo(db)
	ctx := t.Context()

	if err := r.Set(ctx, 1, "k", jsonx.RawMessage(`1`)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.ExecContext(ctx, "DELETE FROM workspaces WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := r.Get(ctx, 1, "k"); found {
		t.Fatal("state outlived its workspace — the FK cascade is the delete story")
	}
}

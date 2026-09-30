package resources

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testkit"
)

func computedPatchFixture(t *testing.T) (*Repo, *store.DB, int64, Entity) {
	t.Helper()
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	workspace := insertWorkspace(t, db, "computed", nil, domain.Settings{})
	resource := insertResourceRow(t, db, workspace, "/orders", "id", "integer")
	repo := newTestRepo(t, db, 4<<20, 64<<10)
	_, _, err := repo.Set(t.Context(), resource, "tenant", "parent", "42", "id", "integer", map[string]any{
		"status": "created", "large": jsonx.Number("9007199254740993"),
		"nested": map[string]any{"fraction": jsonx.Number("0.10000000000000001"), "huge": jsonx.Number("1e999"), "zero": jsonx.Number("-0")},
	})
	if err != nil {
		t.Fatal(err)
	}
	row, found, err := repo.Get(t.Context(), resource, "tenant", "parent", "42")
	if err != nil || !found {
		t.Fatalf("seed read: found=%v err=%v", found, err)
	}
	return repo, db, resource, row
}

func TestPatchComputedUsesLatestRowAndPinsIdentity(t *testing.T) {
	t.Parallel()
	repo, db, resource, before := computedPatchFixture(t)
	conflict := errors.New("transition unavailable")
	choose := func(current map[string]any) (map[string]any, error) {
		if current["status"] != "created" {
			return nil, conflict
		}
		if current["large"] != jsonx.Number("9007199254740993") {
			return nil, errors.New("numbers were rounded")
		}
		return map[string]any{"status": "paid", "id": 999}, nil
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, found, err := repo.PatchComputed(t.Context(), resource, "tenant", "parent", "42", "id", "integer", choose)
			if err == nil && !found {
				err = errors.New("existing entity disappeared")
			}
			results <- err
		})
	}
	wg.Wait()
	close(results)
	var successes, conflicts int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, conflict):
			conflicts++
		default:
			t.Fatalf("unexpected result: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("results success=%d conflict=%d; want 1 each", successes, conflicts)
	}
	row, found, err := repo.Get(t.Context(), resource, "tenant", "parent", "42")
	if err != nil || !found || row.ID != before.ID || !row.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("identity changed: %+v found=%v err=%v", row, found, err)
	}
	for _, exact := range []string{`"id":42`, `"status":"paid"`, `"large":9007199254740993`, `"fraction":0.10000000000000001`, `"huge":1e999`, `"zero":-0`} {
		if !strings.Contains(string(row.Data), exact) {
			t.Fatalf("lost %s: %s", exact, row.Data)
		}
	}
	if count := entityCount(t, db, resource); count != 1 {
		t.Fatalf("entity count=%d", count)
	}
	var seq int
	if err := db.R.QueryRowContext(t.Context(), "SELECT seq FROM resources WHERE id=?", resource).Scan(&seq); err != nil || seq != 42 {
		t.Fatalf("sequence=%d err=%v", seq, err)
	}
}

func TestPatchComputedFailureRollsBack(t *testing.T) {
	t.Parallel()
	callbackErr := errors.New("guard failed")
	for _, name := range []string{"callback", "cancelled", "entity cap", "family cap", "marshal"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repo, db, resource, before := computedPatchFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := callbackErr
			choose := func(map[string]any) (map[string]any, error) { return nil, callbackErr }
			switch name {
			case "cancelled":
				want = context.Canceled
				choose = func(map[string]any) (map[string]any, error) { cancel(); return map[string]any{"status": "paid"}, nil }
			case "entity cap":
				want = ErrEntityLimit
				choose = func(map[string]any) (map[string]any, error) {
					return map[string]any{"oversize": strings.Repeat("x", 4<<20)}, nil
				}
			case "family cap":
				want = ErrEntityLimit
				repo.maxResponseBytes = 80
				choose = func(map[string]any) (map[string]any, error) { return map[string]any{"status": "paid"}, nil }
			case "marshal":
				want = nil
				choose = func(map[string]any) (map[string]any, error) { return map[string]any{"invalid": make(chan int)}, nil }
			}
			_, found, err := repo.PatchComputed(ctx, resource, "tenant", "parent", "42", "id", "integer", choose)
			if err == nil || found || (want != nil && !errors.Is(err, want)) {
				t.Fatalf("found=%v err=%v; want %v", found, err, want)
			}
			after, found, err := repo.Get(t.Context(), resource, "tenant", "parent", "42")
			if err != nil || !found || string(after.Data) != string(before.Data) || !after.UpdatedAt.Equal(before.UpdatedAt) || entityCount(t, db, resource) != 1 {
				t.Fatalf("failure mutated row: %+v found=%v err=%v", after, found, err)
			}
		})
	}
}

func TestPatchComputedMissingRowNeverCallsSelectorOrInserts(t *testing.T) {
	t.Parallel()
	repo, db, resource, _ := computedPatchFixture(t)
	for _, address := range []struct {
		base, scope ScopeKey
		key         string
	}{
		{"tenant", "parent", "43"}, {"other", "parent", "42"}, {"tenant", "other", "42"},
	} {
		_, found, err := repo.PatchComputed(t.Context(), resource, address.base, address.scope, address.key, "id", "integer", func(map[string]any) (map[string]any, error) {
			t.Fatal("selector called for missing row")
			return nil, nil
		})
		if err != nil || found {
			t.Fatalf("missing row found=%v err=%v", found, err)
		}
	}
	if count := entityCount(t, db, resource); count != 1 {
		t.Fatalf("inserted missing entity: count=%d", count)
	}
	deleted, err := repo.Delete(t.Context(), resource, "tenant", "parent", "42")
	if err != nil || !deleted {
		t.Fatalf("delete: %v %v", deleted, err)
	}
	_, found, err := repo.PatchComputed(t.Context(), resource, "tenant", "parent", "42", "id", "integer", func(map[string]any) (map[string]any, error) {
		t.Fatal("selector called for deleted row")
		return nil, nil
	})
	if err != nil || found || entityCount(t, db, resource) != 0 {
		t.Fatalf("resurrected deleted row: found=%v err=%v", found, err)
	}
}

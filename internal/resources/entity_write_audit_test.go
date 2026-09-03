package resources

import (
	"errors"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
)

// TestSet_sameKeyInAnotherBaseScopeIs409NotA500 pins the 2026-09-03 audit
// finding: entities' UNIQUE is (resource_id, scope_key, entity_key) —
// 0003 left base_scope_key out of it because one counter per family could
// never mint a key twice — and A11's operator-chosen key broke that
// premise. The driver's constraint error used to surface as a 500; it is
// a typed conflict now.
func TestSet_sameKeyInAnotherBaseScopeIs409NotA500(t *testing.T) {
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	specID := importSpecDoc(t, db, []byte(nestedFixtureDoc))
	wsID := insertWorkspace(t, db, "acme", &specID, domain.Settings{
		Seed: 1, ListSize: 2, BasePath: "/tenants/{tenantId}", BasePathValues: []string{"7", "8"},
	})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	org, err := repo.Confirm(t.Context(), wsID, familyOrgs)
	if err != nil {
		t.Fatalf("confirm %q: %v", familyOrgs, err)
	}
	under7, err := repo.List(t.Context(), org.ID, ScopeKey("7"), "")
	if err != nil || len(under7) == 0 {
		t.Fatalf("List under base 7: %v (%d rows)", err, len(under7))
	}
	taken := under7[0].EntityKey

	_, _, err = repo.Set(t.Context(), org.ID, ScopeKey("8"), "", taken, org.IDField, org.Wrapper.IDType, map[string]any{"name": "x"})
	if !errors.Is(err, ErrEntityKeyConflict) {
		t.Fatalf("Set(base 8, key %q held by base 7) = %v, want ErrEntityKeyConflict", taken, err)
	}

	// Replacing the row in ITS OWN base scope is still an ordinary replace.
	_, created, err := repo.Set(t.Context(), org.ID, ScopeKey("7"), "", taken, org.IDField, org.Wrapper.IDType, map[string]any{"name": "y"})
	if err != nil || created {
		t.Fatalf("Set(base 7, key %q) = created %v, err %v; want a replace", taken, created, err)
	}
}

// TestSet_keyMustBeCanonicalForTheIDType pins the second A11 finding of the
// same audit: Set derives data[idField] from the key through
// gen.CoerceIDValue, which HASHES an unparsable key into a plausible id
// instead of failing — so "abc" on an integer family stored a row whose key
// and id disagreed. The key must round-trip through the id type unchanged.
func TestSet_keyMustBeCanonicalForTheIDType(t *testing.T) {
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	specID := importSpecDoc(t, db, []byte(nestedFixtureDoc))
	wsID := insertWorkspace(t, db, "acme", &specID, domain.Settings{Seed: 1, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	org, err := repo.Confirm(t.Context(), wsID, familyOrgs)
	if err != nil {
		t.Fatalf("confirm %q: %v", familyOrgs, err)
	}
	for _, key := range []string{"abc", "007", "-0", "1e3"} {
		_, _, err := repo.Set(t.Context(), org.ID, "", "", key, org.IDField, "integer", map[string]any{"name": "x"})
		if !errors.Is(err, ErrEntityKeyNotCanonical) {
			t.Errorf("Set(key %q, integer) = %v, want ErrEntityKeyNotCanonical", key, err)
		}
	}
	if _, _, err := repo.Set(t.Context(), org.ID, "", "", "42", org.IDField, "integer", map[string]any{"name": "x"}); err != nil {
		t.Fatalf("Set(key \"42\", integer) = %v, want ok", err)
	}
	if _, _, err := repo.Set(t.Context(), org.ID, "", "", "abc", org.IDField, "string", map[string]any{"name": "x"}); err != nil {
		t.Fatalf("Set(key \"abc\", string) = %v, want ok — a string family takes any segment", err)
	}
}

func TestCanonicalEntityKey(t *testing.T) {
	cases := []struct {
		key, idType string
		want        bool
	}{
		{"1", "integer", true}, {"01", "integer", false}, {"+1", "integer", false}, {"x", "integer", false},
		{"1.5", "number", true}, {"1.50", "number", false}, {"1e3", "number", false}, {"1000", "number", true},
		{"true", "boolean", true}, {"1", "boolean", false},
		{"anything.at-all~", "string", true}, {"x", "", true},
	}
	for _, c := range cases {
		if got := CanonicalEntityKey(c.key, c.idType); got != c.want {
			t.Errorf("CanonicalEntityKey(%q, %q) = %v, want %v", c.key, c.idType, got, c.want)
		}
	}
}

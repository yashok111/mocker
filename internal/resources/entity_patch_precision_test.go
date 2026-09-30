package resources

import (
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/testkit"
)

func TestPatchPreservesUntouchedJSONNumbers(t *testing.T) {
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	specID := importSpecDoc(t, db, []byte(nestedFixtureDoc))
	wsID := insertWorkspace(t, db, "precision", &specID, domain.Settings{Seed: 1, ListSize: 1})
	repo := newTestRepo(t, db, 4<<20, 64<<10)
	res, err := repo.Confirm(t.Context(), wsID, familyOrgs)
	if err != nil {
		t.Fatal(err)
	}
	data := map[string]any{"large": jsonx.Number("9007199254740993"), "nested": map[string]any{
		"fraction": jsonx.Number("0.10000000000000001"), "huge": jsonx.Number("1e999"), "zero": jsonx.Number("-0"),
	}}
	if _, _, err := repo.Set(t.Context(), res.ID, "", "", "42", res.IDField, res.Wrapper.IDType, data); err != nil {
		t.Fatal(err)
	}
	row, found, err := repo.Patch(t.Context(), res.ID, "", "", "42", res.IDField, res.Wrapper.IDType, map[string]any{"name": "updated"})
	if err != nil || !found {
		t.Fatalf("Patch = found %v, err %v", found, err)
	}
	for _, exact := range []string{`"large":9007199254740993`, `"fraction":0.10000000000000001`, `"huge":1e999`, `"zero":-0`, `"name":"updated"`} {
		if !strings.Contains(string(row.Data), exact) {
			t.Fatalf("Patch lost %s: %s", exact, row.Data)
		}
	}
}

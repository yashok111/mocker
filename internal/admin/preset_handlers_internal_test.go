package admin

// This file is `package admin`, not `package admin_test`, for the same
// reason from_traffic_internal_test.go gives: mergeAuthPresetBindings,
// validateAuthPresetBinding and buildAuthPresetExpect are unexported pure
// functions handleApplyAuthPreset delegates to (preset_handlers.go), and the
// property each one owns — how a set of bindings folds into rows, which
// wording a bad binding earns, which opKey a missing editVersions entry
// names — is reachable through HTTP but not cheaply isolated there. Testing
// them directly needs no HTTP and no DB.

import (
	"net/http"
	"testing"

	"github.com/yashok111/mocker/internal/authpreset"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
)

// TestMergeAuthPresetBindings_freshRow covers the closure's first arm: an
// opKey with no row in current gets a freshly built one (OverrideOn true,
// Method/Path copied from the binding, an empty Responses map to write
// into) rather than a nil-map panic.
func TestMergeAuthPresetBindings_freshRow(t *testing.T) {
	t.Parallel()
	bindings := []authpreset.Binding{
		{Method: "POST", Path: "/auth/login", Status: 200, DataPath: "token", Recipe: recipes.Recipe{Kind: recipes.KindJWT}},
	}

	rows, err := mergeAuthPresetBindings(bindings, map[string]*overrides.Row{})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Method != http.MethodPost || row.Path != "/auth/login" {
		t.Errorf("Method/Path = %s %s, want POST /auth/login", row.Method, row.Path)
	}
	if !row.OverrideOn {
		t.Error("OverrideOn = false, want true on a freshly built row")
	}
	variant, ok := row.Responses["200"]
	if !ok {
		t.Fatalf("Responses[200] missing, got %+v", row.Responses)
	}
	if variant.Recipes["token"].Kind != recipes.KindJWT {
		t.Errorf("Recipes[token].Kind = %q, want jwt", variant.Recipes["token"].Kind)
	}
}

// TestMergeAuthPresetBindings_reusesExistingRow covers the closure's second
// arm: an opKey ALREADY present in current is reused as-is rather than
// rebuilt, so fields the merge never touches (DelayMs, ActiveStatus, an
// unrelated status's Variant) survive the apply untouched — the whole point
// of D8's "merge into, never replace wholesale" (see
// TestHandler_authPresetApply_PreservesExistingOverrideFields's HTTP-level
// coverage of the same property).
func TestMergeAuthPresetBindings_reusesExistingRow(t *testing.T) {
	t.Parallel()
	delay := 250
	activeStatus := 503
	existing := &overrides.Row{
		Method:       "POST",
		Path:         "/auth/login",
		OverrideOn:   true,
		DelayMs:      &delay,
		ActiveStatus: &activeStatus,
		Responses: map[string]overrides.Variant{
			"503": {Mode: "generated"},
		},
	}
	current := map[string]*overrides.Row{overrides.OpKey("POST", "/auth/login"): existing}
	bindings := []authpreset.Binding{
		{Method: "POST", Path: "/auth/login", Status: 200, DataPath: "token", Recipe: recipes.Recipe{Kind: recipes.KindJWT}},
	}

	rows, err := mergeAuthPresetBindings(bindings, current)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(rows) != 1 || rows[0] != existing {
		t.Fatalf("rows = %+v, want the SAME *Row pointer as current's entry (reused, not rebuilt)", rows)
	}
	if rows[0].DelayMs == nil || *rows[0].DelayMs != 250 {
		t.Errorf("DelayMs = %v, want 250 (preserved from the existing row)", rows[0].DelayMs)
	}
	if rows[0].ActiveStatus == nil || *rows[0].ActiveStatus != 503 {
		t.Errorf("ActiveStatus = %v, want 503 (preserved from the existing row)", rows[0].ActiveStatus)
	}
	if _, ok := rows[0].Responses["503"]; !ok {
		t.Errorf("Responses[503] from the existing row is gone, got %+v", rows[0].Responses)
	}
	if _, ok := rows[0].Responses["200"]; !ok {
		t.Errorf("Responses[200] (the applied binding's own status) is missing, got %+v", rows[0].Responses)
	}
}

// TestMergeAuthPresetBindings_secondBindingSameOpKeyReusesBuiltRow covers
// the closure's "already in rowsByKey" arm: two bindings on the SAME opKey
// within one request must collapse into the one row PutMany would write,
// not fetch current[key] twice and silently build two competing rows for
// it.
func TestMergeAuthPresetBindings_secondBindingSameOpKeyReusesBuiltRow(t *testing.T) {
	t.Parallel()
	bindings := []authpreset.Binding{
		{Method: "POST", Path: "/auth/login", Status: 200, DataPath: "token", Recipe: recipes.Recipe{Kind: recipes.KindJWT}},
		{Method: "POST", Path: "/auth/login", Status: 200, DataPath: "user.id", Recipe: recipes.Recipe{Kind: recipes.KindIdentity, Field: "id"}},
	}

	rows, err := mergeAuthPresetBindings(bindings, map[string]*overrides.Row{})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1 (both bindings share one opKey)", len(rows))
	}
	recipesGot := rows[0].Responses["200"].Recipes
	if len(recipesGot) != 2 {
		t.Fatalf("Recipes = %+v, want 2 entries (token, user.id)", recipesGot)
	}
	if recipesGot["token"].Kind != recipes.KindJWT {
		t.Errorf("Recipes[token].Kind = %q, want jwt", recipesGot["token"].Kind)
	}
	if recipesGot["user.id"].Kind != recipes.KindIdentity {
		t.Errorf("Recipes[user.id].Kind = %q, want identity", recipesGot["user.id"].Kind)
	}
}

// TestMergeAuthPresetBindings_initializesNilRecipesMap covers the closure's
// "variant.Recipes == nil" arm: a status with no Variant yet reads back a
// zero overrides.Variant, whose Recipes map is nil — writing into a nil map
// panics, so the closure must allocate one before the first write.
func TestMergeAuthPresetBindings_initializesNilRecipesMap(t *testing.T) {
	t.Parallel()
	// The existing row has a "503" Variant but no "200" one at all, so
	// row.Responses["200"] reads back the zero Variant (Recipes == nil).
	existing := &overrides.Row{
		Method: "GET", Path: "/widgets", OverrideOn: true,
		Responses: map[string]overrides.Variant{"503": {Mode: "generated"}},
	}
	current := map[string]*overrides.Row{overrides.OpKey("GET", "/widgets"): existing}
	bindings := []authpreset.Binding{
		{Method: "GET", Path: "/widgets", Status: 200, DataPath: "id", Recipe: recipes.Recipe{Kind: recipes.KindNull}},
	}

	rows, err := mergeAuthPresetBindings(bindings, current)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	variant := rows[0].Responses["200"]
	if variant.Recipes == nil {
		t.Fatal("Recipes is nil, want it allocated on first write to a fresh status")
	}
	if variant.Recipes["id"].Kind != recipes.KindNull {
		t.Errorf("Recipes[id].Kind = %q, want null", variant.Recipes["id"].Kind)
	}
}

// TestMergeAuthPresetBindings_mergesWithoutOverwritingSiblingRecipes covers
// the closure's "variant.Recipes != nil" arm: a status that already carries
// a recipe on a DIFFERENT dataPath must keep it — the merge adds the new
// binding's recipe alongside, never replacing the map wholesale.
func TestMergeAuthPresetBindings_mergesWithoutOverwritingSiblingRecipes(t *testing.T) {
	t.Parallel()
	existing := &overrides.Row{
		Method: "POST", Path: "/auth/login", OverrideOn: true,
		Responses: map[string]overrides.Variant{
			"200": {Recipes: map[string]recipes.Recipe{"token": {Kind: recipes.KindJWT}}},
		},
	}
	current := map[string]*overrides.Row{overrides.OpKey("POST", "/auth/login"): existing}
	bindings := []authpreset.Binding{
		{Method: "POST", Path: "/auth/login", Status: 200, DataPath: "user.id", Recipe: recipes.Recipe{Kind: recipes.KindIdentity, Field: "id"}},
	}

	rows, err := mergeAuthPresetBindings(bindings, current)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	recipesGot := rows[0].Responses["200"].Recipes
	if _, ok := recipesGot["token"]; !ok {
		t.Error("Recipes[token] from the existing row is gone, want it preserved")
	}
	if _, ok := recipesGot["user.id"]; !ok {
		t.Error("Recipes[user.id] from the new binding is missing")
	}
}

// TestMergeAuthPresetBindings_sortsRowsByOpKey pins the deterministic
// output order the merge's own doc comment promises: rows come back sorted
// by opKey, not in map-iteration order.
func TestMergeAuthPresetBindings_sortsRowsByOpKey(t *testing.T) {
	t.Parallel()
	bindings := []authpreset.Binding{
		{Method: "POST", Path: "/widgets", Status: 200, DataPath: "id", Recipe: recipes.Recipe{Kind: recipes.KindNull}},
		{Method: "GET", Path: "/auth/login", Status: 200, DataPath: "id", Recipe: recipes.Recipe{Kind: recipes.KindNull}},
	}

	rows, err := mergeAuthPresetBindings(bindings, map[string]*overrides.Row{})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
	firstKey := overrides.OpKey(rows[0].Method, rows[0].Path)
	secondKey := overrides.OpKey(rows[1].Method, rows[1].Path)
	if firstKey >= secondKey {
		t.Errorf("rows[0] opKey %q is not before rows[1] opKey %q, want sorted order", firstKey, secondKey)
	}
}

// TestValidateAuthPresetBinding covers each refusal reason plus the
// well-formed case, pinning the exact wording preserved from when these
// checks lived inline in handleApplyAuthPreset.
func TestValidateAuthPresetBinding(t *testing.T) {
	t.Parallel()
	valid := recipes.Recipe{Kind: recipes.KindNull}

	cases := []struct {
		name    string
		binding authpreset.Binding
		wantMsg string
	}{
		{
			name:    "empty method",
			binding: authpreset.Binding{Method: "", Path: "/auth/login", DataPath: "token", Recipe: valid},
			wantMsg: "binding 0: method/path must name an operation",
		},
		{
			name:    "empty path",
			binding: authpreset.Binding{Method: "POST", Path: "", DataPath: "token", Recipe: valid},
			wantMsg: "binding 0: method/path must name an operation",
		},
		{
			name:    "path without a leading slash",
			binding: authpreset.Binding{Method: "POST", Path: "auth/login", DataPath: "token", Recipe: valid},
			wantMsg: "binding 0: method/path must name an operation",
		},
		{
			name:    "empty dataPath",
			binding: authpreset.Binding{Method: "POST", Path: "/auth/login", DataPath: "", Recipe: valid},
			wantMsg: "binding 0 (POST /auth/login): dataPath is required",
		},
		{
			name:    "invalid recipe",
			binding: authpreset.Binding{Method: "POST", Path: "/auth/login", DataPath: "token", Recipe: recipes.Recipe{Kind: "not-a-real-kind"}},
			wantMsg: `binding 0 (POST /auth/login token): recipes: invalid recipe: unknown kind "not-a-real-kind"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			msg, ok := validateAuthPresetBinding(0, tc.binding)
			if ok {
				t.Fatal("ok = true, want false")
			}
			if msg != tc.wantMsg {
				t.Errorf("msg = %q, want %q", msg, tc.wantMsg)
			}
		})
	}

	t.Run("well-formed binding", func(t *testing.T) {
		t.Parallel()
		msg, ok := validateAuthPresetBinding(0, authpreset.Binding{Method: "POST", Path: "/auth/login", DataPath: "token", Recipe: valid})
		if !ok {
			t.Errorf("ok = false (msg = %q), want true", msg)
		}
	})
}

// TestBuildAuthPresetExpect covers both halves of D5's rule: a fully
// covered editVersions builds one expect entry per DISTINCT opKey the
// bindings resolve to (two bindings on the same operation still collapse to
// one), and a partially covered one names the first (sorted) opKey missing
// its entry rather than refusing over an unrelated one.
func TestBuildAuthPresetExpect(t *testing.T) {
	t.Parallel()

	t.Run("covered", func(t *testing.T) {
		t.Parallel()
		bindings := []authpreset.Binding{
			{Method: "POST", Path: "/auth/login", DataPath: "token"},
			{Method: "POST", Path: "/auth/login", DataPath: "user.id"}, // same opKey as above
		}
		loginKey := overrides.OpKey("POST", "/auth/login")
		editVersions := map[string]int64{loginKey: 7}

		expect, missingKey, ok := buildAuthPresetExpect(bindings, editVersions)
		if !ok {
			t.Fatalf("ok = false, missingKey = %q, want true", missingKey)
		}
		if len(expect) != 1 {
			t.Fatalf("expect = %+v, want exactly 1 entry (both bindings share one opKey)", expect)
		}
		if expect[loginKey] != 7 {
			t.Errorf("expect[%q] = %d, want 7", loginKey, expect[loginKey])
		}
	})

	t.Run("missing entry", func(t *testing.T) {
		t.Parallel()
		bindings := []authpreset.Binding{
			{Method: "POST", Path: "/auth/login", DataPath: "token"},
		}
		loginKey := overrides.OpKey("POST", "/auth/login")

		expect, missingKey, ok := buildAuthPresetExpect(bindings, map[string]int64{})
		if ok {
			t.Fatalf("ok = true, expect = %+v, want false (editVersions has no entry for the touched opKey)", expect)
		}
		if missingKey != loginKey {
			t.Errorf("missingKey = %q, want %q", missingKey, loginKey)
		}
	})

	t.Run("extra unrelated entry is ignored, never checked", func(t *testing.T) {
		t.Parallel()
		bindings := []authpreset.Binding{
			{Method: "POST", Path: "/auth/login", DataPath: "token"},
		}
		loginKey := overrides.OpKey("POST", "/auth/login")
		widgetsKey := overrides.OpKey("GET", "/widgets")
		// widgetsKey carries no value at all (a stale/bogus zero entry would
		// pass just as easily) — the point is that buildAuthPresetExpect
		// never even looks at it, only at opKeys the bindings actually touch.
		editVersions := map[string]int64{loginKey: 3, widgetsKey: 999}

		expect, _, ok := buildAuthPresetExpect(bindings, editVersions)
		if !ok {
			t.Fatal("ok = false, want true")
		}
		if _, present := expect[widgetsKey]; present {
			t.Errorf("expect = %+v, want no entry for the untouched opKey %q", expect, widgetsKey)
		}
	})
}

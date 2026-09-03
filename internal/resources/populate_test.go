package resources

import (
	"bytes"
	"reflect"
	"strconv"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/specs"
)

// decodeNumberAware round-trips b through jsonx with UseNumber, the same
// comparison D13 clause 5 asks for ("decoded with UseNumber on both sides
// and compared value by value, never byte for byte").
func decodeNumberAware(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	dec := jsonx.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

// --- D13 clause 5: confirming changes nothing visible, narrowly -----------

// TestConfirm_IdentityForIDField_id proves R32's narrow identity claim: for
// familyWidgets (id_field "id", no override row), the DETAIL body Confirm
// stores for entity i is the SAME JSON DOCUMENT gen.Body itself produces
// for GET /widgets/{i} through the identical Request/variant shape — the
// generator is not being fed anything Confirm invented.
func TestConfirm_IdentityForIDField_id(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 42, ListSize: 3})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	entities, err := repo.List(t.Context(), res.ID, "", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entities) != 3 {
		t.Fatalf("entity count = %d, want 3", len(entities))
	}

	// Rebuild the exact same generator/variant/request shape independently
	// (never reusing Confirm's own internals) and compare.
	sr := specs.NewRepo(db, testSpecConfig(t))
	generator, _, err := (&Repo{specs: sr}).buildGenerator(t.Context(), specID, domain.Settings{Seed: 42, ListSize: 3})
	if err != nil {
		t.Fatalf("buildGenerator: %v", err)
	}
	variants, err := sr.Variants(t.Context(), specID)
	if err != nil {
		t.Fatalf("Variants: %v", err)
	}
	routes, err := sr.Routes(t.Context(), specID)
	if err != nil {
		t.Fatalf("Routes: %v", err)
	}
	detailRoute, idParam, _, err := locateFamilyOperations(routes, familyWidgets)
	if err != nil {
		t.Fatalf("locateFamilyOperations: %v", err)
	}
	detailVariant, ok := defaultVariant200(variants[detailRoute.OpRowID])
	if !ok {
		t.Fatalf("no default 200 variant for detail route")
	}

	for i, e := range entities {
		id := i + 1
		want, err := generator.Body(detailVariant, gen.Request{
			Method: "GET", CanonicalPath: familyWidgets + "/{}",
			PathParams: map[string]string{idParam: strconv.Itoa(id)},
			ListFamily: familyWidgets, IDParam: idParam, Status: 200,
		})
		if err != nil {
			t.Fatalf("independent gen.Body(%d): %v", id, err)
		}
		wantMap := decodeNumberAware(t, want)
		gotMap := decodeNumberAware(t, e.Data)
		// id_field is "id" here, so no reconciliation is needed: the two
		// documents must already agree on id, value for value.
		if !reflect.DeepEqual(wantMap, gotMap) {
			t.Fatalf("entity %d: stored = %v, independently generated = %v", id, gotMap, wantMap)
		}
	}
}

// --- D13 clause 6: a non-"id" id_field works anyway ------------------------

func TestConfirm_NonIDFieldWorks(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 3})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyUsers)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if res.IDField != "userId" {
		t.Fatalf("IDField = %q, want userId", res.IDField)
	}

	// The row lives under entity_key "3" — the ALLOCATED seq's decimal
	// string, never re-derived from data["userId"] (see [Entity.EntityKey]'s
	// doc comment). The wire SHAPE of data["userId"] itself (a JSON number,
	// since /users declares it integer) is asserted by the sibling test
	// TestConfirm_NonIDFieldWorks_IntegerShape.
	if _, ok, err := repo.Get(t.Context(), res.ID, "", "", "3"); err != nil || !ok {
		t.Fatalf("Get(3) = ok=%v err=%v, want found", ok, err)
	}
}

// TestConfirm_NonIDFieldWorks_IntegerShape re-checks the same family but
// asserts the WIRE shape (a JSON number, since /users' userId is declared
// integer) rather than assuming a string, which the previous test's loose
// type assertion would silently pass either way.
func TestConfirm_NonIDFieldWorks_IntegerShape(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 3})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyUsers)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	got, ok, err := repo.Get(t.Context(), res.ID, "", "", "3")
	if err != nil || !ok {
		t.Fatalf("Get(3) = ok=%v err=%v, want found", ok, err)
	}
	data := decodeNumberAware(t, got.Data)
	num, ok := data["userId"].(jsonx.Number)
	if !ok {
		t.Fatalf("data[userId] = %T(%v), want a JSON number (userId is declared integer)", data["userId"], data["userId"])
	}
	if num.String() != "3" {
		t.Fatalf("data[userId] = %s, want 3", num.String())
	}
}

// --- D13 clause 7: determinism across workspaces AND across families ------

func TestConfirm_DeterminismAcrossWorkspaces(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	ws1 := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 9, ListSize: 4})
	ws2 := insertWorkspace(t, db, "bravo", &specID, domain.Settings{Seed: 9, ListSize: 4})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res1, err := repo.Confirm(t.Context(), ws1, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm ws1: %v", err)
	}
	res2, err := repo.Confirm(t.Context(), ws2, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm ws2: %v", err)
	}

	e1, err := repo.List(t.Context(), res1.ID, "", "")
	if err != nil {
		t.Fatalf("List ws1: %v", err)
	}
	e2, err := repo.List(t.Context(), res2.ID, "", "")
	if err != nil {
		t.Fatalf("List ws2: %v", err)
	}
	if len(e1) != len(e2) {
		t.Fatalf("entity counts differ: %d vs %d", len(e1), len(e2))
	}
	for i := range e1 {
		if string(e1[i].Data) != string(e2[i].Data) {
			t.Fatalf("entity %d differs across two workspaces with the SAME spec and seed: %s vs %s", i, e1[i].Data, e2[i].Data)
		}
	}
}

func TestConfirm_DeterminismAcrossFamiliesDiffers(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 9, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	widgets, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm widgets: %v", err)
	}
	users, err := repo.Confirm(t.Context(), wsID, familyUsers)
	if err != nil {
		t.Fatalf("Confirm users: %v", err)
	}

	we, err := repo.List(t.Context(), widgets.ID, "", "")
	if err != nil {
		t.Fatalf("List widgets: %v", err)
	}
	ue, err := repo.List(t.Context(), users.ID, "", "")
	if err != nil {
		t.Fatalf("List users: %v", err)
	}
	if len(we) == 0 || len(ue) == 0 {
		t.Fatalf("expected non-empty entity sets for both families")
	}
	if string(we[0].Data) == string(ue[0].Data) {
		t.Fatalf("two DIFFERENT families in one workspace produced identical bodies: %s", we[0].Data)
	}
}

// --- write_form (R12) -------------------------------------------------

func TestComputeWriteForm(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	sr := specs.NewRepo(db, testSpecConfig(t))

	normalized, err := sr.Normalized(t.Context(), specID)
	if err != nil {
		t.Fatalf("Normalized: %v", err)
	}
	doc, _, err := openapi.Load(normalized)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	resolver := openapi.NewResolver(doc, openapi.DefaultRefBudget)

	routes, err := sr.Routes(t.Context(), specID)
	if err != nil {
		t.Fatalf("Routes: %v", err)
	}
	variants, err := sr.Variants(t.Context(), specID)
	if err != nil {
		t.Fatalf("Variants: %v", err)
	}
	suggestions, err := sr.EnsureSuggestions(t.Context(), specID)
	if err != nil {
		t.Fatalf("EnsureSuggestions: %v", err)
	}
	var widgetsEntitySchema, usersEntitySchema string
	for _, s := range suggestions {
		switch s.RouteFamily {
		case familyWidgets:
			widgetsEntitySchema = s.EntitySchema
		case familyUsers:
			usersEntitySchema = s.EntitySchema
		}
	}
	if widgetsEntitySchema == "" || usersEntitySchema == "" {
		t.Fatalf("expected both families to derive a suggestion")
	}

	t.Run("bare — request body equals the item schema", func(t *testing.T) {
		t.Parallel()
		_, _, post, err := locateFamilyOperations(routes, familyWidgets)
		if err != nil {
			t.Fatalf("locateFamilyOperations: %v", err)
		}
		if post == nil {
			t.Fatalf("expected /widgets to declare a POST route")
		}
		wf := computeWriteForm(resolver, variants[post.OpRowID], widgetsEntitySchema)
		if wf == nil || *wf != "bare" {
			t.Fatalf("write_form = %v, want \"bare\"", wf)
		}
	})

	t.Run("nil — no POST route at all", func(t *testing.T) {
		t.Parallel()
		_, _, post, err := locateFamilyOperations(routes, familyUsers)
		if err != nil {
			t.Fatalf("locateFamilyOperations: %v", err)
		}
		if post != nil {
			t.Fatalf("expected /users to declare no POST route")
		}
	})

	t.Run("nil — empty postVariants", func(t *testing.T) {
		t.Parallel()
		if wf := computeWriteForm(resolver, nil, widgetsEntitySchema); wf != nil {
			t.Fatalf("write_form = %v, want nil for an empty variant slice", wf)
		}
	})
}

// --- forceID (R35) ----------------------------------------------------

func TestForceID(t *testing.T) {
	t.Parallel()

	t.Run("overwrites the named field", func(t *testing.T) {
		t.Parallel()
		out, err := forceID([]byte(`{"id":999,"name":"x"}`), "id", "integer", 7)
		if err != nil {
			t.Fatalf("forceID: %v", err)
		}
		var m map[string]any
		if err := jsonx.Unmarshal(out, &m); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if m["id"] != float64(7) {
			t.Fatalf("id = %v, want 7", m["id"])
		}
	})

	t.Run("large integer survives the round trip", func(t *testing.T) {
		t.Parallel()
		// Not the id field itself (that is always the small seq), but a
		// SIBLING field above 2^53 — proves UseNumber is actually in
		// effect, not just that small ids happen to work (D13 clause 38's
		// underlying mechanism).
		out, err := forceID([]byte(`{"id":1,"big":9007199254740993}`), "id", "integer", 1)
		if err != nil {
			t.Fatalf("forceID: %v", err)
		}
		if !jsonx.Valid(out) {
			t.Fatalf("output is not valid JSON: %s", out)
		}
		var m map[string]any
		dec := jsonx.NewDecoder(bytes.NewReader(out))
		dec.UseNumber()
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got := m["big"].(jsonx.Number).String(); got != "9007199254740993" {
			t.Fatalf("big = %s, want 9007199254740993 (float64 would round this)", got)
		}
	})

	t.Run("nil body aborts", func(t *testing.T) {
		t.Parallel()
		if _, err := forceID(nil, "id", "integer", 1); err == nil {
			t.Fatalf("forceID(nil) = nil error, want an error (nothing to decode)")
		}
	})

	t.Run("non-object body aborts", func(t *testing.T) {
		t.Parallel()
		if _, err := forceID([]byte(`[1,2,3]`), "id", "integer", 1); err == nil {
			t.Fatalf("forceID(array) = nil error, want an error")
		}
	})

	t.Run("null body aborts", func(t *testing.T) {
		t.Parallel()
		if _, err := forceID([]byte(`null`), "id", "integer", 1); err == nil {
			t.Fatalf("forceID(null) = nil error, want an error")
		}
	})
}

// --- defaultVariant200 -------------------------------------------------

func TestDefaultVariant200(t *testing.T) {
	t.Parallel()
	t.Run("picks the IsDefault 200 row", func(t *testing.T) {
		t.Parallel()
		v, ok := defaultVariant200([]gen.ResponseVariant{
			{Selector: "default", HTTPStatus: 404, IsDefault: false},
			{Selector: "200", HTTPStatus: 200, IsDefault: true},
		})
		if !ok || v.HTTPStatus != 200 {
			t.Fatalf("defaultVariant200 = %v, %v, want the 200 row", v, ok)
		}
	})
	t.Run("no default row at all", func(t *testing.T) {
		t.Parallel()
		if _, ok := defaultVariant200(nil); ok {
			t.Fatalf("defaultVariant200(nil) = ok, want not found")
		}
	})
	t.Run("default row is not 200", func(t *testing.T) {
		t.Parallel()
		if _, ok := defaultVariant200([]gen.ResponseVariant{{IsDefault: true, HTTPStatus: 201}}); ok {
			t.Fatalf("defaultVariant200 with a 201 default = ok, want not found (population never runs off a variant nothing serves)")
		}
	})
}

// --- locateFamilyOperations ---------------------------------------------

func TestLocateFamilyOperations(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	sr := specs.NewRepo(db, testSpecConfig(t))
	routes, err := sr.Routes(t.Context(), specID)
	if err != nil {
		t.Fatalf("Routes: %v", err)
	}

	detail, idParam, post, err := locateFamilyOperations(routes, familyWidgets)
	if err != nil {
		t.Fatalf("locateFamilyOperations(widgets): %v", err)
	}
	if idParam != "id" {
		t.Fatalf("idParam = %q, want id", idParam)
	}
	if detail.CanonicalPath != familyWidgets+"/{}" {
		t.Fatalf("detail.CanonicalPath = %q", detail.CanonicalPath)
	}
	if post == nil || post.CanonicalPath != familyWidgets {
		t.Fatalf("post = %v, want the /widgets POST route", post)
	}

	_, idParam2, post2, err := locateFamilyOperations(routes, familyUsers)
	if err != nil {
		t.Fatalf("locateFamilyOperations(users): %v", err)
	}
	if idParam2 != "userId" {
		t.Fatalf("idParam = %q, want userId", idParam2)
	}
	if post2 != nil {
		t.Fatalf("post = %v, want nil (users declares no POST)", post2)
	}

	if _, _, _, err := locateFamilyOperations(routes, "/nonexistent"); err == nil {
		t.Fatalf("locateFamilyOperations(unknown family) = nil error, want ErrPopulationFailed")
	}
}

// --- router sanity used by locateFamilyOperations -----------------------

func TestLocateFamilyOperations_UsesRouterFamilyLogic(t *testing.T) {
	t.Parallel()
	// A route whose CanonicalPath ends in "/{}" but the prefix does not
	// name a GET collection route at all must not be picked as a detail
	// route for that "family" — router.ListFamily is what enforces this,
	// and this test proves locateFamilyOperations really goes through it
	// rather than a naive suffix match.
	routes := []router.Route{
		{Method: "GET", Path: "/orphans/{id}", CanonicalPath: "/orphans/{}"},
	}
	if _, _, _, err := locateFamilyOperations(routes, "/orphans"); err == nil {
		t.Fatalf("locateFamilyOperations matched a detail route with no sibling collection GET")
	}
}

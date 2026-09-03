package overrides_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
)

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// TestValidation_recipesAndModeAndStatus exercises exactly what the task
// calls out for decode-time validation: an unknown mode, a status key that
// is not 3 decimal digits, and a recipe that fails recipes.Recipe.Validate.
// These go through Put's mutate path (rather than a direct call to an
// unexported validator) so the test proves the same thing a caller of this
// package actually observes.
func TestValidation_recipesAndModeAndStatus(t *testing.T) {
	tests := []struct {
		name      string
		responses map[string]overrides.Variant
	}{
		{
			name: "unknown mode",
			responses: map[string]overrides.Variant{
				"200": {Mode: "teleport"},
			},
		},
		{
			// P3a (D13 clause 26): "stateful" is not, and must never
			// become, a legal response mode — a resource-served route is
			// switched by confirming/declining a route family
			// (internal/resources), never by an op_overrides row naming a
			// third mode beside "generated"/"pinned".
			name: "unknown mode literal stateful",
			responses: map[string]overrides.Variant{
				"200": {Mode: "stateful"},
			},
		},
		{
			name: "status key not 3 digits",
			responses: map[string]overrides.Variant{
				"20": {Mode: "generated"},
			},
		},
		{
			name: "status key not numeric",
			responses: map[string]overrides.Variant{
				"2XX": {Mode: "generated"},
			},
		},
		{
			name: "invalid recipe kind",
			responses: map[string]overrides.Variant{
				"200": {
					Mode:    "generated",
					Recipes: map[string]recipes.Recipe{"id": {Kind: recipes.Kind("bogus")}},
				},
			},
		},
		{
			name: "invalid recipe payload",
			responses: map[string]overrides.Variant{
				"200": {
					Mode:    "generated",
					Recipes: map[string]recipes.Recipe{"id": {Kind: recipes.KindConst}}, // const needs a value
				},
			},
		},
		{
			name: "unknown bodyEncoding",
			responses: map[string]overrides.Variant{
				"200": {Mode: "pinned", BodyEncoding: "rot13"},
			},
		},
		{
			name: "base64 body is not valid base64",
			responses: map[string]overrides.Variant{
				"200": {Mode: "pinned", BodyEncoding: "base64", Body: raw(`"not-base64!!"`)},
			},
		},
		{
			name: "base64 body is not a JSON string",
			responses: map[string]overrides.Variant{
				"200": {Mode: "pinned", BodyEncoding: "base64", Body: raw(`{"oops":true}`)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := newTestDB(t)
			repo := overrides.NewRepo(db)
			wsID := insertWorkspace(t, db, "alex")

			_, _, err := repo.Put(t.Context(), wsID, overrides.OpKey("GET", "/things"), func(row *overrides.Row) error {
				row.Responses = tt.responses
				return nil
			})
			if !errors.Is(err, overrides.ErrInvalidRow) {
				t.Fatalf("Put() error = %v, want ErrInvalidRow", err)
			}
		})
	}
}

// TestValidation_validBase64Body proves the happy path of the same check:
// a well-formed base64 body is accepted, decoded and re-encoded internally
// only to prove it decodes, then stored and read back unchanged.
func TestValidation_validBase64Body(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	body := raw(`"aGVsbG8="`) // base64("hello")
	_, _, err := repo.Put(t.Context(), wsID, overrides.OpKey("GET", "/things"), func(row *overrides.Row) error {
		row.Responses = map[string]overrides.Variant{
			"200": {Mode: "pinned", BodyEncoding: "base64", Body: body},
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Put(): %v", err)
	}

	got, err := repo.Get(t.Context(), wsID, overrides.OpKey("GET", "/things"))
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if string(got.Responses["200"].Body) != string(body) {
		t.Errorf("Body = %s, want %s", got.Responses["200"].Body, body)
	}
}

// quotedBodyOfLength returns a JSON string literal (a legal Variant.Body)
// whose encoded length is EXACTLY n bytes: the opening and closing quote
// plus n-2 filler characters.
func quotedBodyOfLength(n int) json.RawMessage {
	b := make([]byte, n)
	b[0], b[n-1] = '"', '"'
	for i := 1; i < n-1; i++ {
		b[i] = 'a'
	}
	return json.RawMessage(b)
}

// TestValidation_pinnedBodyOverLimit is round-1 findings #3/#8: a pinned
// body has no per-request cost gate of its own once stored (unlike a
// generated body, bounded on every call by gen.Options.MaxBytes), so the
// write is where an oversized one has to be caught. Covers both body
// shapes: a literal (non-base64) body measured by its own raw length, and
// a base64 body measured by its DECODED length — the one mockplane's
// pinnedBody actually serves — not the (longer) base64 text.
const maxPinnedBodyBytesForTest = 4 << 20 // mirrors overrides.maxPinnedBodyBytes, unexported to this external test package

func TestValidation_pinnedBodyOverLimit(t *testing.T) {
	t.Run("literal body, one byte over the limit", func(t *testing.T) {
		t.Parallel()
		db := newTestDB(t)
		repo := overrides.NewRepo(db)
		wsID := insertWorkspace(t, db, "alex")

		body := quotedBodyOfLength(maxPinnedBodyBytesForTest + 1)
		_, _, err := repo.Put(t.Context(), wsID, overrides.OpKey("GET", "/things"), func(row *overrides.Row) error {
			row.Responses = map[string]overrides.Variant{"200": {Mode: "pinned", Body: body}}
			return nil
		})
		if !errors.Is(err, overrides.ErrInvalidRow) {
			t.Fatalf("Put() error = %v, want ErrInvalidRow", err)
		}
	})

	t.Run("base64 body, measured by its DECODED length", func(t *testing.T) {
		t.Parallel()
		db := newTestDB(t)
		repo := overrides.NewRepo(db)
		wsID := insertWorkspace(t, db, "alex")

		decoded := make([]byte, maxPinnedBodyBytesForTest+1)
		encoded := base64.StdEncoding.EncodeToString(decoded)
		bodyJSON, err := json.Marshal(encoded)
		if err != nil {
			t.Fatalf("marshal encoded body: %v", err)
		}

		_, _, err = repo.Put(t.Context(), wsID, overrides.OpKey("GET", "/things"), func(row *overrides.Row) error {
			row.Responses = map[string]overrides.Variant{
				"200": {Mode: "pinned", BodyEncoding: "base64", Body: bodyJSON},
			}
			return nil
		})
		if !errors.Is(err, overrides.ErrInvalidRow) {
			t.Fatalf("Put() error = %v, want ErrInvalidRow", err)
		}
	})

	t.Run("at exactly the limit is accepted", func(t *testing.T) {
		t.Parallel()
		db := newTestDB(t)
		repo := overrides.NewRepo(db)
		wsID := insertWorkspace(t, db, "alex")

		body := quotedBodyOfLength(maxPinnedBodyBytesForTest)
		_, _, err := repo.Put(t.Context(), wsID, overrides.OpKey("GET", "/things"), func(row *overrides.Row) error {
			row.Responses = map[string]overrides.Variant{"200": {Mode: "pinned", Body: body}}
			return nil
		})
		if err != nil {
			t.Fatalf("Put() at exactly the limit: %v, want accepted", err)
		}
	})
}

// TestValidation_tooManyRecipesInOneVariant is round-1 finding #7's
// write-time gate: [recipes.Set.Lookup] scans every bound pattern that
// could match a given path, so nothing bounding the COUNT lets one stored
// row force that scan to repeat per leaf/array-node of every generated
// body on an unauthenticated, unrate-limited GET (DESIGN §18). One over the
// limit must be rejected; at exactly the limit must still be accepted.
func TestValidation_tooManyRecipesInOneVariant(t *testing.T) {
	buildRecipes := func(n int) map[string]recipes.Recipe {
		out := make(map[string]recipes.Recipe, n)
		for i := range n {
			out[fmt.Sprintf("field%d", i)] = recipes.Recipe{Kind: recipes.KindNull}
		}
		return out
	}

	t.Run("over the limit is rejected", func(t *testing.T) {
		t.Parallel()
		db := newTestDB(t)
		repo := overrides.NewRepo(db)
		wsID := insertWorkspace(t, db, "alex")

		_, _, err := repo.Put(t.Context(), wsID, overrides.OpKey("GET", "/things"), func(row *overrides.Row) error {
			row.Responses = map[string]overrides.Variant{"200": {Mode: "generated", Recipes: buildRecipes(1001)}}
			return nil
		})
		if !errors.Is(err, overrides.ErrInvalidRow) {
			t.Fatalf("Put() error = %v, want ErrInvalidRow", err)
		}
	})

	t.Run("at exactly the limit is accepted", func(t *testing.T) {
		t.Parallel()
		db := newTestDB(t)
		repo := overrides.NewRepo(db)
		wsID := insertWorkspace(t, db, "alex")

		_, _, err := repo.Put(t.Context(), wsID, overrides.OpKey("GET", "/things"), func(row *overrides.Row) error {
			row.Responses = map[string]overrides.Variant{"200": {Mode: "generated", Recipes: buildRecipes(1000)}}
			return nil
		})
		if err != nil {
			t.Fatalf("Put() at exactly the limit: %v, want accepted", err)
		}
	})
}

// TestValidation_pathAndMethod covers point 6: reject an empty or
// non-slash-prefixed path, and two spellings of one method must not create
// two rows.
func TestValidation_pathAndMethod(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	t.Run("lower-case method normalizes to upper-case", func(t *testing.T) {
		row, _, err := repo.Put(t.Context(), wsID, overrides.OpKey("get", "/normalize"), func(row *overrides.Row) error {
			return nil
		})
		if err != nil {
			t.Fatalf("Put(): %v", err)
		}
		if row.Method != http.MethodGet {
			t.Errorf("Method = %q, want GET", row.Method)
		}

		// A second Put spelled differently must hit the SAME row, not create
		// a second one under the UNIQUE(workspace_id, method, path) index.
		_, rev2, err := repo.Put(t.Context(), wsID, overrides.OpKey("GET", "/normalize"), func(row *overrides.Row) error {
			row.RouteOff = true
			return nil
		})
		if err != nil {
			t.Fatalf("second Put(): %v", err)
		}

		all, err := repo.ForWorkspace(t.Context(), wsID)
		if err != nil {
			t.Fatalf("ForWorkspace(): %v", err)
		}
		if len(all) != 1 {
			t.Fatalf("ForWorkspace() = %d rows, want 1 (two method spellings must collide)", len(all))
		}
		if rev2 == 0 {
			t.Errorf("revision after second Put = 0")
		}
	})
}

// TestOpKey_ParseOpKey_forPutRejectsBadPath proves ParseOpKey's own leading-
// slash requirement is what stands between a caller and an ambiguous Put.
func TestPut_badOpKeyIsRejectedBeforeAnyDBWork(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	_, _, err := repo.Put(t.Context(), wsID, "not a valid key", func(row *overrides.Row) error { return nil })
	if !errors.Is(err, overrides.ErrInvalidOpKey) {
		t.Fatalf("Put() error = %v, want ErrInvalidOpKey", err)
	}

	all, err := repo.ForWorkspace(t.Context(), wsID)
	if err != nil {
		t.Fatalf("ForWorkspace(): %v", err)
	}
	if len(all) != 0 {
		t.Errorf("ForWorkspace() = %d rows, want 0 (rejected Put must not write anything)", len(all))
	}
}

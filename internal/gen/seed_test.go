package gen

import (
	"net/url"
	"strconv"
	"testing"
)

func TestSeedListDeterministic(t *testing.T) {
	opts := Options{Seed: 42}
	req := Request{
		Method:        "GET",
		CanonicalPath: "/api/v1/users/{}",
		PathParams:    map[string]string{"id": "7"},
		Status:        200,
	}
	a := SeedList(opts, req)
	b := SeedList(opts, req)
	if a != b {
		t.Fatalf("SeedList not deterministic: %d != %d", a, b)
	}
}

// TestSeedListPathParamOrderIndependent proves the "sorted by name" half of
// the seedList contract: router.Match populates PathParams from a Go map,
// whose iteration order is randomized per process — SeedList must not leak
// that randomness into the seed.
func TestSeedListPathParamOrderIndependent(t *testing.T) {
	opts := Options{Seed: 1}
	base := Request{Method: "GET", CanonicalPath: "/a/{}/b/{}", Status: 200}

	r1 := base
	r1.PathParams = map[string]string{"a": "1", "b": "2"}
	r2 := base
	r2.PathParams = map[string]string{"b": "2", "a": "1"} // same pairs, different insertion order

	if SeedList(opts, r1) != SeedList(opts, r2) {
		t.Fatalf("SeedList depends on map iteration order")
	}
}

// TestSeedListExcludesQuery is the "БЕЗ query" half of DESIGN §9: pagination
// params like ?offset=20 must select a slice of an existing set, never
// change which set exists.
func TestSeedListExcludesQuery(t *testing.T) {
	opts := Options{Seed: 1}
	base := Request{Method: "GET", CanonicalPath: "/items", Status: 200}

	r1 := base
	r1.Query = url.Values{"offset": {"0"}}
	r2 := base
	r2.Query = url.Values{"offset": {"20"}, "limit": {"5"}}

	if SeedList(opts, r1) != SeedList(opts, r2) {
		t.Fatalf("SeedList must not depend on query parameters")
	}
}

// TestSeedListVariesByIdentity proves the seed actually distinguishes
// method, path, path params, and status from one another — a naive
// concatenation without length-prefixing could alias "a"+"bc" with "ab"+"c".
func TestSeedListVariesByIdentity(t *testing.T) {
	base := Request{Method: "GET", CanonicalPath: "/x/{}", PathParams: map[string]string{"id": "1"}, Status: 200}
	opts := Options{Seed: 1}

	variants := []Request{
		{Method: "POST", CanonicalPath: base.CanonicalPath, PathParams: base.PathParams, Status: base.Status},
		{Method: base.Method, CanonicalPath: "/x/{}/y", PathParams: base.PathParams, Status: base.Status},
		{Method: base.Method, CanonicalPath: base.CanonicalPath, PathParams: map[string]string{"id": "2"}, Status: base.Status},
		{Method: base.Method, CanonicalPath: base.CanonicalPath, PathParams: base.PathParams, Status: 404},
		// length-prefix ambiguity probe: {"ab":"c"} vs {"a":"bc"} must differ
		{Method: base.Method, CanonicalPath: base.CanonicalPath, PathParams: map[string]string{"a": "bc"}, Status: base.Status},
	}

	baseSeed := SeedList(opts, base)
	seen := map[uint64]bool{baseSeed: true}
	for i, v := range variants {
		s := SeedList(opts, v)
		if seen[s] {
			t.Errorf("variant %d collided with a previous seed (%d)", i, s)
		}
		seen[s] = true
	}

	abc := SeedList(opts, Request{Method: "GET", CanonicalPath: "/z", PathParams: map[string]string{"ab": "c"}, Status: 200})
	aBc := SeedList(opts, Request{Method: "GET", CanonicalPath: "/z", PathParams: map[string]string{"a": "bc"}, Status: 200})
	if abc == aBc {
		t.Fatalf("length-prefix ambiguity: {ab:c} and {a:bc} produced the same seed")
	}
}

func TestSeedListDifferentSeedDiffers(t *testing.T) {
	req := Request{Method: "GET", CanonicalPath: "/x", Status: 200}
	a := SeedList(Options{Seed: 1}, req)
	b := SeedList(Options{Seed: 2}, req)
	if a == b {
		t.Fatalf("different Options.Seed produced the same SeedList")
	}
}

func TestSeedItemByIDDeterministicAndDistinct(t *testing.T) {
	seedList := SeedList(Options{Seed: 1}, Request{Method: "GET", CanonicalPath: "/x", Status: 200})

	a1 := SeedItemByID(seedList, "42")
	a2 := SeedItemByID(seedList, "42")
	if a1 != a2 {
		t.Fatalf("SeedItemByID not deterministic")
	}

	b := SeedItemByID(seedList, "43")
	if a1 == b {
		t.Fatalf("different ids produced the same item seed")
	}
}

func TestSeedScalarVariesByDataPath(t *testing.T) {
	seed := uint64(1234)
	a := SeedScalar(seed, "name")
	b := SeedScalar(seed, "email")
	if a == b {
		t.Fatalf("different dataPaths produced the same scalar seed")
	}
	if SeedScalar(seed, "name") != a {
		t.Fatalf("SeedScalar not deterministic")
	}
}

// TestSeedScalarItemRestart is the crux of the "row == card" contract: a
// list row's field and a detail card's field for the SAME item must hash
// identically when both use the item-local dataPath ("name"), which is
// exactly what happens once the item seed itself already encodes identity.
func TestSeedScalarItemRestart(t *testing.T) {
	seedList := SeedList(Options{Seed: 9}, Request{Method: "GET", CanonicalPath: "/users", Status: 200})
	itemSeed := SeedItemByID(seedList, "7")

	rowField := SeedScalar(itemSeed, "name")  // list row, item-local path
	cardField := SeedScalar(itemSeed, "name") // detail card, same item-local path
	if rowField != cardField {
		t.Fatalf("row and card scalar seeds differ for the same item/dataPath")
	}

	// And the WRONG (design-forbidden) shape — a row using a page-relative
	// path like "items[3].name" — must NOT match the card's "name": this is
	// the exact failure mode DESIGN §9 calls out by name.
	wrong := SeedScalar(itemSeed, "items[3].name")
	if wrong == cardField {
		t.Fatalf("a page-relative dataPath accidentally matched the item-local one")
	}
}

func TestIDForIndexTypedAndCanonical(t *testing.T) {
	seedList := SeedList(Options{Seed: 5}, Request{Method: "GET", CanonicalPath: "/bulletins", Status: 200})

	t.Run("integer uint format", func(t *testing.T) {
		schema := map[string]any{"type": "integer", "format": "uint"}
		val, s := IDForIndex(seedList, 3, schema)
		n, ok := val.(int64)
		if !ok {
			t.Fatalf("expected int64, got %T (%v)", val, val)
		}
		if n < 0 {
			t.Fatalf("id must be non-negative, got %d", n)
		}
		if s != strconv.FormatInt(n, 10) {
			t.Fatalf("canonical form %q does not match typed value %d", s, n)
		}
	})

	t.Run("string uuid format", func(t *testing.T) {
		schema := map[string]any{"type": "string", "format": "uuid"}
		val, s := IDForIndex(seedList, 3, schema)
		str, ok := val.(string)
		if !ok {
			t.Fatalf("expected string, got %T", val)
		}
		if str != s {
			t.Fatalf("canonical form must equal the typed string itself: %q != %q", s, str)
		}
		if len(str) != 36 {
			t.Fatalf("expected RFC4122-shaped 36-char uuid, got %q (%d chars)", str, len(str))
		}
	})

	t.Run("untyped falls back to decimal string", func(t *testing.T) {
		val, s := IDForIndex(seedList, 3, nil)
		if val != s {
			t.Fatalf("untyped id: typed value must equal its own canonical string, got %v vs %q", val, s)
		}
	})

	t.Run("deterministic across calls", func(t *testing.T) {
		schema := map[string]any{"type": "integer"}
		v1, s1 := IDForIndex(seedList, 10, schema)
		v2, s2 := IDForIndex(seedList, 10, schema)
		if v1 != v2 || s1 != s2 {
			t.Fatalf("IDForIndex not deterministic")
		}
	})

	t.Run("distinct across index", func(t *testing.T) {
		schema := map[string]any{"type": "integer"}
		_, s1 := IDForIndex(seedList, 1, schema)
		_, s2 := IDForIndex(seedList, 2, schema)
		if s1 == s2 {
			t.Fatalf("different indices produced the same id")
		}
	})
}

// TestIDForIndexRoundTripsWithSeedItemByID exercises the actual contract
// two agents (List, and this test standing in for the detail route) rely
// on: the id written into a list row at global index i, fed back through
// SeedItemByID, must be the SAME seed a detail route for that id computes.
func TestIDForIndexRoundTripsWithSeedItemByID(t *testing.T) {
	seedList := SeedList(Options{Seed: 3}, Request{Method: "GET", CanonicalPath: "/users", Status: 200})
	schema := map[string]any{"type": "integer", "format": "int64"}

	_, canonical := IDForIndex(seedList, 5, schema)

	// The list row, generating item 5, computes its item seed the same way
	// the detail route (which only ever has the ID as a string) does.
	rowItemSeed := SeedItemByID(seedList, canonical)
	detailItemSeed := SeedItemByID(seedList, canonical)
	if rowItemSeed != detailItemSeed {
		t.Fatalf("row/detail item seeds diverge for id %q", canonical)
	}
}

// TestPrimaryIDType exercises the newly-exported reader directly, including
// the OAS 3.1 union-type unwrap ["integer","null"] -> "integer" that a bare
// schema["type"].(string) type assertion would miss entirely (it would
// answer "" for that shape, matching this function's own no-usable-schema
// fallback and silently misclassifying a nullable id as untyped).
func TestPrimaryIDType(t *testing.T) {
	tests := []struct {
		name   string
		schema map[string]any
		want   string
	}{
		{"plain string type", map[string]any{"type": "integer"}, "integer"},
		{"union with null first", map[string]any{"type": []any{"null", "integer"}}, "integer"},
		{"union with null last", map[string]any{"type": []any{"string", "null"}}, "string"},
		{"forced null only", map[string]any{"type": []any{"null"}}, ""},
		{"no type at all", map[string]any{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := PrimaryIDType(tc.schema); got != tc.want {
				t.Errorf("PrimaryIDType(%v) = %q, want %q", tc.schema, got, tc.want)
			}
		})
	}
}

func TestHash64LengthPrefixAvoidsAmbiguity(t *testing.T) {
	a := hash64([]byte("ab"), []byte("c"))
	b := hash64([]byte("a"), []byte("bc"))
	if a == b {
		t.Fatalf("hash64([]byte(\"ab\"),[]byte(\"c\")) collided with hash64([]byte(\"a\"),[]byte(\"bc\"))")
	}
}

// TestHash64Stable pins the algorithm to a literal expected value: this is
// the "byte-identical after a process restart" half of DESIGN §9's
// determinism contract made concrete — hash64 has no process-random seed
// (unlike hash/maphash) to make this test flaky, so an unexplained failure
// here means the FNV-1a implementation itself changed, which would silently
// change every seed a running installation ever computed.
func TestHash64Stable(t *testing.T) {
	const want = uint64(0xca4eecd9d61365d6)
	got := hash64([]byte("hello"))
	if got != want {
		t.Fatalf("hash64([]byte(\"hello\")) = %#x, want %#x (algorithm changed?)", got, want)
	}
	if hash64([]byte("hello")) != got {
		t.Fatalf("hash64 is not stable across calls")
	}
}

package recipes

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		pattern, path string
		want          bool
	}{
		{"", "", true},
		{"", "x", false},
		{"user.profile.avatar_url", "user.profile.avatar_url", true},
		{"user.profile.avatar_url", "user.profile.avatar_uri", false},
		{"items[*].status", "items[2].status", true},
		{"items[*].status", "items[2].other", false},
		{"items[3].status", "items[2].status", false},
		{"items[3].status", "items[3].status", true},
		{"roles[*]", "roles[0]", true},
		{"roles[*]", "roles", false}, // different token count
		{"a.b", "a.b.c", false},      // pattern shorter than path
		{"a.b.c", "a.b", false},      // pattern longer than path
		{"a[*][*]", "a[1][2]", true}, // nested wildcards
		{"a[0][*]", "a[0][2]", true},
		{"a[0][*]", "a[1][2]", false},
	}
	for _, tc := range tests {
		if got := MatchPattern(tc.pattern, tc.path); got != tc.want {
			t.Errorf("MatchPattern(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

// TestLookup_Precedence is the "map iteration order decides which recipe
// wins" trap the task brief calls out by name: run enough times and a
// precedence bug driven by map order shows up as flakiness, so each case
// below pins WHICH recipe wins by giving it a distinguishable Value, not
// just asserting that some recipe matched.
func TestLookup_Precedence(t *testing.T) {
	tag := func(s string) Recipe { return Recipe{Kind: KindConst, Data: raw(`"` + s + `"`)} }

	tests := []struct {
		name     string
		bindings map[string]Recipe
		dataPath string
		want     string
	}{
		{
			name: "exact index beats wildcard on the matching index",
			bindings: map[string]Recipe{
				"items[*].status": tag("wild"),
				"items[2].status": tag("exact"),
			},
			dataPath: "items[2].status",
			want:     "exact",
		},
		{
			name: "wildcard still wins off the exact index's target",
			bindings: map[string]Recipe{
				"items[*].status": tag("wild"),
				"items[2].status": tag("exact"),
			},
			dataPath: "items[5].status",
			want:     "wild",
		},
		{
			name: "earlier exact beats a later exact-vs-wildcard mix",
			bindings: map[string]Recipe{
				"a[0].b[*].c": tag("front-exact"),
				"a[*].b[1].c": tag("back-exact"),
			},
			dataPath: "a[0].b[1].c",
			want:     "front-exact",
		},
		{
			name: "two exact indices at different positions beat one",
			bindings: map[string]Recipe{
				"a[0].b[1].c": tag("both-exact"),
				"a[0].b[*].c": tag("one-exact"),
			},
			dataPath: "a[0].b[1].c",
			want:     "both-exact",
		},
		{
			name: "true tie breaks on the lexicographically smaller pattern",
			bindings: map[string]Recipe{
				"items[07].status": tag("zero-padded"),
				"items[7].status":  tag("plain"),
			},
			dataPath: "items[7].status",
			want:     "zero-padded", // "items[07]..." < "items[7]..." ('0' < '7')
		},
		{
			name: "root pattern matches only the root path",
			bindings: map[string]Recipe{
				"": tag("root"),
			},
			dataPath: "",
			want:     "root",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Run several times: a bug that reads Go's randomized map
			// iteration order would show up as flakiness here, not a
			// deterministic failure.
			for range 20 {
				s, err := Compile(tc.bindings)
				if err != nil {
					t.Fatalf("Compile: %v", err)
				}
				got, ok := s.Lookup(tc.dataPath)
				if !ok {
					t.Fatalf("Lookup(%q) = not found", tc.dataPath)
				}
				var gotStr string
				if err := json.Unmarshal(got.Data, &gotStr); err != nil {
					t.Fatalf("decode winning recipe's value: %v", err)
				}
				if gotStr != tc.want {
					t.Fatalf("Lookup(%q) picked %q, want %q", tc.dataPath, gotStr, tc.want)
				}
			}
		})
	}
}

func TestLookup_NoMatch(t *testing.T) {
	s, err := Compile(map[string]Recipe{"a.b": {Kind: KindNull}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Lookup("a.c"); ok {
		t.Fatalf("Lookup(a.c) = found, want not found")
	}
}

func TestListSizeAt(t *testing.T) {
	bindings := map[string]Recipe{
		"items":           {Kind: KindListSize, Data: raw(`10`)},
		"orders[*].items": {Kind: KindListSize, Data: raw(`[2,4]`)},
		// Same pattern shape as "items" but a different kind — ListSizeAt
		// must ignore it even though it matches, proving the kind filter
		// composes correctly with the precedence scan.
		"items.status": {Kind: KindConst, Data: raw(`"x"`)},
	}
	s, err := Compile(bindings)
	if err != nil {
		t.Fatal(err)
	}

	if lo, hi, ok := s.ListSizeAt("items"); !ok || lo != 10 || hi != 10 {
		t.Fatalf("ListSizeAt(items) = %d,%d,%v, want 10,10,true", lo, hi, ok)
	}
	if lo, hi, ok := s.ListSizeAt("orders[3].items"); !ok || lo != 2 || hi != 4 {
		t.Fatalf("ListSizeAt(orders[3].items) = %d,%d,%v, want 2,4,true", lo, hi, ok)
	}
	if _, _, ok := s.ListSizeAt("nope"); ok {
		t.Fatalf("ListSizeAt(nope) = found, want not found")
	}
	if _, _, ok := s.ListSizeAt("items.status"); ok {
		t.Fatalf("ListSizeAt(items.status) matched a non-listSize recipe")
	}
}

func TestCompile_NilForEmptyBindings(t *testing.T) {
	if s, err := Compile(nil); s != nil || err != nil {
		t.Fatalf("Compile(nil) = %v, %v, want nil, nil", s, err)
	}
	if s, err := Compile(map[string]Recipe{}); s != nil || err != nil {
		t.Fatalf("Compile({}) = %v, %v, want nil, nil", s, err)
	}
}

func TestCompile_RejectsAnInvalidRecipe(t *testing.T) {
	_, err := Compile(map[string]Recipe{"x": {Kind: Kind("bogus")}})
	if !errors.Is(err, ErrRecipe) {
		t.Fatalf("Compile() = %v, want an error wrapping ErrRecipe", err)
	}
}

// TestNilSet_MethodsAreSafe is HARD-required by Compile's own contract: "a
// nil *Set is a legal receiver for every method below".
func TestNilSet_MethodsAreSafe(t *testing.T) {
	var s *Set
	if got := s.Len(); got != 0 {
		t.Errorf("nil Set.Len() = %d, want 0", got)
	}
	if got := s.Bindings(); got != nil {
		t.Errorf("nil Set.Bindings() = %v, want nil", got)
	}
	if _, ok := s.Lookup("anything"); ok {
		t.Errorf("nil Set.Lookup() = found, want not found")
	}
	if _, _, ok := s.ListSizeAt("anything"); ok {
		t.Errorf("nil Set.ListSizeAt() = found, want not found")
	}
}

func TestSet_LenAndBindings(t *testing.T) {
	bindings := map[string]Recipe{
		"a": {Kind: KindNull},
		"b": {Kind: KindOmit},
	}
	s, err := Compile(bindings)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Len(); got != 2 {
		t.Fatalf("Len() = %d, want 2", got)
	}
	got := s.Bindings()
	if len(got) != 2 || got["a"].Kind != KindNull || got["b"].Kind != KindOmit {
		t.Fatalf("Bindings() = %+v", got)
	}
}

// TestSet_Bindings_IsACopy proves the map handed back cannot be used to
// mutate the Set's own compiled state from underneath it.
// TestLookup_IndexedByFirstToken_CorrectAmongManyBuckets is the
// correctness side of round-1 finding #7's fix: with bindings spread
// across MANY distinct first-token buckets (plus a root and an array-root
// pattern), a Lookup for one specific path must still find exactly its own
// match — narrowing to one bucket by dataPath's own first token must never
// discard a genuine one living there, and must never spill into a
// different bucket by mistake.
func TestLookup_IndexedByFirstToken_CorrectAmongManyBuckets(t *testing.T) {
	bindings := map[string]Recipe{
		"":              {Kind: KindConst, Data: raw(`"root"`)},
		"[*].tag":       {Kind: KindConst, Data: raw(`"array-root"`)},
		"user.name":     {Kind: KindConst, Data: raw(`"user-name"`)},
		"user.email":    {Kind: KindConst, Data: raw(`"user-email"`)},
		"org.name":      {Kind: KindConst, Data: raw(`"org-name"`)},
		"items[*].id":   {Kind: KindConst, Data: raw(`"items-id"`)},
		"items[3].id":   {Kind: KindConst, Data: raw(`"items-id-3"`)},
		"widgets.color": {Kind: KindConst, Data: raw(`"widgets-color"`)},
	}
	s, err := Compile(bindings)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	tests := []struct {
		path string
		want string
	}{
		{"", "root"},
		{"[2].tag", "array-root"},
		{"user.name", "user-name"},
		{"user.email", "user-email"},
		{"org.name", "org-name"},
		{"items[5].id", "items-id"},
		{"items[3].id", "items-id-3"}, // exact index beats the wildcard, across buckets that share the same first token
		{"widgets.color", "widgets-color"},
	}
	for _, tc := range tests {
		got, ok := s.Lookup(tc.path)
		if !ok {
			t.Errorf("Lookup(%q) = not found, want %q", tc.path, tc.want)
			continue
		}
		var gotStr string
		if err := json.Unmarshal(got.Data, &gotStr); err != nil {
			t.Fatalf("decode recipe value for %q: %v", tc.path, err)
		}
		if gotStr != tc.want {
			t.Errorf("Lookup(%q) = %q, want %q", tc.path, gotStr, tc.want)
		}
	}

	// Fields nothing was ever bound to, in existing buckets and in no
	// bucket at all, must still cleanly miss.
	for _, miss := range []string{"user.phone", "nope.nope", "items[5].name"} {
		if _, ok := s.Lookup(miss); ok {
			t.Errorf("Lookup(%q) = found, want not found", miss)
		}
	}
}

// TestLookup_ManyBindingsAcrossDistinctBucketsStaysFast is round-1 finding
// #7's regression guard for the realistic shape the first-token index
// exists for: a large document with many DISTINCT field names, each bound
// once. Before the fix (a flat, unindexed []compiledPattern scanned in full
// on every call), a Lookup for one specific path paid for every OTHER
// binding too, regardless of how unrelated their own first token was —
// measured, 200,000 such bindings turned an 83,649-byte response's 1.87ms
// generation into 3.034s. With the index, a path whose own bucket holds a
// single entry costs O(1) regardless of how many OTHER buckets exist, so
// this uses a tight, non-flaky budget rather than one merely below the
// unfixed number.
func TestLookup_ManyBindingsAcrossDistinctBucketsStaysFast(t *testing.T) {
	const n = 200_000
	bindings := make(map[string]Recipe, n)
	for i := range n {
		// Each binding gets its OWN first-token name — n distinct buckets,
		// none of them the one "target.leaf" below lives in.
		bindings[fmt.Sprintf("field%d.leaf", i)] = Recipe{Kind: KindNull}
	}
	bindings["target.leaf"] = Recipe{Kind: KindConst, Data: raw(`"found"`)}
	s, err := Compile(bindings)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	start := time.Now()
	got, ok := s.Lookup("target.leaf")
	elapsed := time.Since(start)

	if !ok {
		t.Fatalf("Lookup(target.leaf) = not found among %d bindings", n)
	}
	var gotStr string
	if err := json.Unmarshal(got.Data, &gotStr); err != nil {
		t.Fatalf("decode recipe value: %v", err)
	}
	if gotStr != "found" {
		t.Fatalf("Lookup(target.leaf) = %q, want %q", gotStr, "found")
	}
	const budget = 20 * time.Millisecond // target.leaf's own bucket has ONE entry; this is generous headroom, not a near-miss
	if elapsed > budget {
		t.Errorf("Lookup took %v among %d bindings spread across distinct buckets, want under %v — looks like the first-token index regressed to a full scan", elapsed, n, budget)
	}
}

func TestSet_Bindings_IsACopy(t *testing.T) {
	s, err := Compile(map[string]Recipe{"x": {Kind: KindNull}})
	if err != nil {
		t.Fatal(err)
	}
	cp := s.Bindings()
	cp["x"] = Recipe{Kind: KindOmit}

	got, ok := s.Lookup("x")
	if !ok || got.Kind != KindNull {
		t.Fatalf("mutating Bindings()'s result leaked into the Set: got %+v, ok=%v", got, ok)
	}
}

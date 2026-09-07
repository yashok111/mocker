package openapi_test

import (
	"errors"
	"slices"
	"sort"
	"testing"

	"github.com/yashok111/mocker/internal/openapi"
)

// TestWalkRefNodes_VisitsEveryObjectThroughArrays is the property all three
// former hand-written copies of this walk depended on and any one of them
// could have lost: a `$ref` nested inside an ARRAY of composition branches
// is reached, and an array is descended into without ever being handed to
// the visitor (only an object can carry a `$ref`).
func TestWalkRefNodes_VisitsEveryObjectThroughArrays(t *testing.T) {
	t.Parallel()
	doc := map[string]any{
		"allOf": []any{
			map[string]any{"$ref": "#/components/schemas/A"},
			map[string]any{"properties": map[string]any{
				"deep": map[string]any{"items": []any{map[string]any{"$ref": "#/components/schemas/B"}}},
			}},
			"a bare string in the array, not an object",
		},
	}

	var refs []string
	visited := 0
	if err := openapi.WalkRefNodes(doc, func(obj map[string]any) error {
		visited++
		if r, ok := obj["$ref"].(string); ok {
			refs = append(refs, r)
		}
		return nil
	}); err != nil {
		t.Fatalf("WalkRefNodes: %v", err)
	}

	sort.Strings(refs)
	want := []string{"#/components/schemas/A", "#/components/schemas/B"}
	if !slices.Equal(refs, want) {
		t.Fatalf("refs = %v, want %v", refs, want)
	}
	// root, the two allOf branches, "properties", "deep", and the one item
	// object — six objects, no more (the bare string is not one).
	if visited != 6 {
		t.Fatalf("visited %d objects, want 6", visited)
	}
}

// TestWalkRefNodes_FirstErrorAborts pins the early-exit contract
// customep.containsRef is built on: the visitor's error comes back
// unchanged (so a sentinel is comparable with errors.Is) and nothing after
// it is visited.
func TestWalkRefNodes_FirstErrorAborts(t *testing.T) {
	t.Parallel()
	stop := errors.New("stop")
	doc := map[string]any{"a": map[string]any{"b": map[string]any{}}}

	visited := 0
	err := openapi.WalkRefNodes(doc, func(map[string]any) error {
		visited++
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("WalkRefNodes error = %v, want the sentinel back unchanged", err)
	}
	if visited != 1 {
		t.Fatalf("visited %d objects after the first error, want 1", visited)
	}
}

// TestWalkRefNodes_VisitorMayRewriteInPlace is what mockplane's sanitizeRefs
// needs and the reason the walk descends AFTER the visitor rather than
// before: a visitor that empties a node is not then walked into the keys it
// just deleted, so no "do not descend" signal is required.
func TestWalkRefNodes_VisitorMayRewriteInPlace(t *testing.T) {
	t.Parallel()
	inner := map[string]any{"$ref": "#/nope", "properties": map[string]any{"x": map[string]any{"$ref": "#/also-nope"}}}
	doc := map[string]any{"schema": inner}

	var seen []string
	if err := openapi.WalkRefNodes(doc, func(obj map[string]any) error {
		if r, ok := obj["$ref"].(string); ok {
			seen = append(seen, r)
			for k := range obj {
				delete(obj, k)
			}
			obj["type"] = "object"
		}
		return nil
	}); err != nil {
		t.Fatalf("WalkRefNodes: %v", err)
	}

	// The nested "#/also-nope" was never reached: emptying its parent took
	// it out of the tree before the walk descended.
	if !slices.Equal(seen, []string{"#/nope"}) {
		t.Fatalf("seen = %v, want only the outer ref", seen)
	}
	if len(inner) != 1 || inner["type"] != "object" {
		t.Fatalf("in-place rewrite did not stick: %v", inner)
	}
}

// TestSelectMediaType covers the exported picker's two rules and its
// documented panic. internal/specs' own test holds the same contract
// through the forwarder it kept; this one holds it where the rule now
// lives, so deleting the forwarder later leaves the contract tested.
func TestSelectMediaType(t *testing.T) {
	t.Parallel()

	t.Run("application/json wins over a lexicographically earlier key", func(t *testing.T) {
		got := openapi.SelectMediaType(map[string]any{
			"application/hal+json": map[string]any{},
			"application/json":     map[string]any{},
			"*/*":                  map[string]any{},
		})
		if got != "application/json" {
			t.Fatalf("SelectMediaType = %q, want application/json", got)
		}
	})

	t.Run("without it the lexicographically first key wins", func(t *testing.T) {
		got := openapi.SelectMediaType(map[string]any{
			"text/plain":      map[string]any{},
			"application/xml": map[string]any{},
		})
		if got != "application/xml" {
			t.Fatalf("SelectMediaType = %q, want application/xml", got)
		}
	})

	t.Run("an empty map panics, by contract", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("SelectMediaType(empty map) did not panic; every caller is required to guard emptiness itself")
			}
		}()
		openapi.SelectMediaType(map[string]any{})
	})
}

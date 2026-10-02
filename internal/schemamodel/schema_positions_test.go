package schemamodel

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestIsSchemaPosition(t *testing.T) {
	root := map[string]any{"components": map[string]any{"schemas": map[string]any{"A": map[string]any{"properties": map[string]any{"example": true, "x-field": false, "a/b~c": map[string]any{"$ref": "#/components/schemas/A"}}, "example": map[string]any{"properties": map[string]any{"fake": map[string]any{}}}, "allOf": []any{true}, "x-hidden": map[string]any{"properties": map[string]any{"fake": true}}, "not": nil, "then": "scalar"}, "B": false}}}
	for _, tc := range []struct {
		pointer string
		want    bool
		invalid bool
	}{
		{"/components/schemas/A", true, false}, {"/components/schemas/B", true, false}, {"/components/schemas/A/properties/example", true, false}, {"/components/schemas/A/properties/x-field", true, false}, {"/components/schemas/A/properties/a~1b~0c", true, false}, {"/components/schemas/A/example", false, false},
		{"/components/schemas/A/x-hidden", false, false},
		{"/components/schemas/A/not", false, false},
		{"/components/schemas/A/then", false, false}, {"/components/schemas/A/allOf/0", true, false}, {"/components/schemas/A/allOf/00", false, true}, {"#/components/schemas/A", false, true}, {"/components/schemas/A~2", false, true},
	} {
		t.Run(tc.pointer, func(t *testing.T) {
			got, err := IsSchemaPosition(t.Context(), root, tc.pointer)
			if (err != nil) != tc.invalid || got != tc.want {
				t.Fatalf("got %v %v", got, err)
			}
		})
	}
}

// A deterministic context proves cancellation is checked inside traversal.
type entryCancelContext struct {
	context.Context
	calls int
}

func (c *entryCancelContext) Err() error {
	c.calls++
	if c.calls > 20 {
		return context.Canceled
	}
	return nil
}
func TestIsSchemaPositionCancellationAndBudgets(t *testing.T) {
	schemas := map[string]any{}
	for i := 0; i < 100; i++ {
		schemas[strconv.Itoa(i)] = true
	}
	root := map[string]any{"components": map[string]any{"schemas": schemas}}
	ctx := &entryCancelContext{Context: t.Context()}
	if _, err := IsSchemaPosition(ctx, root, "/components/schemas/0"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	for i := 100; i < maxNodes; i++ {
		schemas[strconv.Itoa(i)] = true
	}
	if _, err := IsSchemaPosition(t.Context(), root, "/components/schemas/0"); err == nil {
		t.Fatal("node budget ignored")
	}
	for i := maxNodes - 3; i < maxNodes; i++ {
		delete(schemas, strconv.Itoa(i))
	}
	if ok, err := IsSchemaPosition(t.Context(), root, "/components/schemas/0"); err != nil || !ok {
		t.Fatalf("boundary: %v %v", ok, err)
	}
	schema := map[string]any{}
	root = map[string]any{"components": map[string]any{"schemas": map[string]any{"A": schema}}}
	for range 127 {
		child := map[string]any{}
		schema["not"] = child
		schema = child
	}
	if _, err := IsSchemaPosition(t.Context(), root, "/components/schemas/A"); err == nil {
		t.Fatal("depth budget ignored")
	}
}

func TestAuthoredPointerBoundsAndLiteralPercent(t *testing.T) {
	root := map[string]any{"components": map[string]any{"schemas": map[string]any{"a%2Fb": true, "a/b": false}}}
	for _, pointer := range []string{"/" + strings.Repeat("a", 2048), strings.Repeat("/a", 65), "/components/schemas/A~", "/components/schemas/\xff"} {
		if _, _, err := ResolveAuthoredPointer(root, pointer); err == nil {
			t.Fatalf("accepted %q", pointer)
		}
	}
	value, found, err := ResolveAuthoredPointer(root, "/components/schemas/a%2Fb")
	if err != nil || !found || value != true {
		t.Fatalf("percent decoding: %v %v %v", value, found, err)
	}
}

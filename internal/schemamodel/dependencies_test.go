package schemamodel

import (
	"context"
	"errors"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestDependencyReferencesScopeAndOpaqueData(t *testing.T) {
	var root map[string]any
	if err := jsonx.Unmarshal([]byte(`{"components":{"schemas":{"A":{"$ref":"#/components/schemas/B","example":{"$ref":"#/missing"},"x-note":{"$ref":"#/missing"}},"B":{"type":"string"},"Scoped":{"$id":"https://example.test/schema","$ref":"#/components/schemas/B"},"Dynamic":{"$dynamicRef":"#/components/schemas/B"},"Broken":{"$ref":"#/missing"}}}}`), &root); err != nil {
		t.Fatal(err)
	}
	index, err := DependencyReferences(t.Context(), root, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(index.References) != 4 {
		t.Fatalf("references: %+v", index)
	}
	if !index.References[0].Supported {
		t.Fatalf("expected local ref: %+v", index)
	}
	if index.References[0].TargetPointer != "/components/schemas/B" {
		t.Fatalf("wrong target: %+v", index)
	}
	if len(index.Diagnostics) != 3 {
		t.Fatalf("diagnostics: %+v", index)
	}
	for _, ref := range index.References[1:] {
		if ref.Supported {
			t.Fatalf("unsupported ref: %+v", ref)
		}
	}
}

func TestDependencyReferencesLimitAndCancellation(t *testing.T) {
	root := map[string]any{"components": map[string]any{"schemas": map[string]any{"A": map[string]any{"$ref": "#/components/schemas/A"}, "B": map[string]any{"$ref": "#/components/schemas/A"}}}}
	index, err := DependencyReferences(t.Context(), root, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(index.References) != 1 {
		t.Fatalf("references: %+v", index)
	}
	if !index.Truncated {
		t.Fatal("expected truncation")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = DependencyReferences(ctx, root, 10)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error: %v", err)
	}
}

func TestDependencyReferencesRejectInvalidTargetsAndPointers(t *testing.T) {
	root := document(t, `{"components":{"schemas":{"A":{"$ref":"#/components/schemas/B"},"B":12,"C":{"$ref":"#/components/schemas/~2"},"~2":{}}}}`)
	index, err := DependencyReferences(t.Context(), root, 20)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, d := range index.Diagnostics {
		codes[d.Code] = true
	}
	if !codes["invalid_reference_target"] || !codes["invalid_reference_pointer"] {
		t.Fatalf("invalid dependencies accepted: %+v", index)
	}
	for _, ref := range index.References {
		if ref.Supported {
			t.Fatalf("invalid local edge: %+v", ref)
		}
	}
}

func TestDependencyReferenceKindsAndBudgetReasons(t *testing.T) {
	root := document(t, `{"components":{"schemas":{"A":{"$ref":"#/components/schemas/B","$dynamicRef":"#dynamic","discriminator":{"mapping":{"b":"B"}}},"B":{}}}}`)
	index, err := DependencyReferences(t.Context(), root, 20)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, ref := range index.References {
		kinds[ref.Kind] = true
	}
	if !kinds["ref"] || !kinds["dynamicRef"] || !kinds["discriminator"] {
		t.Fatalf("reference kinds: %+v", index)
	}
	index, err = DependencyReferencesWithinBudget(t.Context(), root, 20, 1)
	if err != nil || index.TruncatedReason != "traversal" {
		t.Fatalf("budget reason: %+v %v", index, err)
	}
	index, err = DependencyReferences(t.Context(), root, 1)
	if err != nil || index.TruncatedReason != "references" {
		t.Fatalf("sites reason: %+v %v", index, err)
	}
}

func TestDependencyReferencesDiagnoseMalformedStructuralContainers(t *testing.T) {
	for _, raw := range []string{`{"paths":[]}`, `{"components":{"schemas":[]}}`, `{"components":{"schemas":{"Bad":null}}}`, `{"paths":{"/a":{"get":{"parameters":{}}}}}`} {
		index, err := DependencyReferences(t.Context(), document(t, raw), 20)
		if err != nil {
			t.Fatal(err)
		}
		if len(index.Diagnostics) == 0 {
			t.Fatalf("malformed container silently ignored: %s", raw)
		}
	}
}

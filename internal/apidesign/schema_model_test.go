package apidesign

import (
	"fmt"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/schemamodel"
)

func TestSchemaModelPreviewAndVersionedCommands(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	ctx := t.Context()
	d, err := r.Create(ctx, CreateInput{Name: "Schema", Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	commands := []schemamodel.Command{{Kind: "create_schema", SchemaName: "User", SchemaJSON: `{"type":"object","example":{"n":9007199254740993},"x-unknown":{"value":42}}`}}
	preview, err := r.PreviewSchemaModel(ctx, d.Design.ID, SchemaModelProposal{Commands: commands})
	if err != nil || !preview.Valid {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	current, err := r.Detail(ctx, d.Design.ID)
	if err != nil || current.Design.Version != 1 || len(current.Revisions) != 1 || strings.Contains(current.Draft.Document, "User") {
		t.Fatal("preview persisted", err)
	}
	next, err := r.ApplySchemaModelCommands(ctx, d.Design.ID, 1, "mcp", commands)
	if err != nil {
		t.Fatal(err)
	}
	if next.Design.Version != 2 || !strings.Contains(next.Draft.Document, "9007199254740993") {
		t.Fatalf("save %+v", next)
	}
	if _, err := r.ApplySchemaModelCommands(ctx, d.Design.ID, 1, "mcp", commands); err == nil {
		t.Fatal("stale save accepted")
	}
	bad := `{"openapi":"3.1.0","paths":{}}`
	preview, err = r.PreviewSchemaModel(ctx, d.Design.ID, SchemaModelProposal{Document: &bad})
	if err != nil || preview.Valid || len(preview.Diagnostics) == 0 {
		t.Fatalf("invalid preview: %+v %v", preview, err)
	}
	if _, err := r.ApplySchemaModelCommands(ctx, d.Design.ID, 2, "mcp", []schemamodel.Command{{Kind: "replace_schema", SchemaName: "User", SchemaJSON: `{"$ref":"#/components/schemas/Missing"}`}}); err == nil {
		t.Fatal("invalid full contract saved")
	}
	got, err := r.SchemaModel(ctx, d.Design.ID)
	if err != nil || got.Version != 2 || len(got.Model.Schemas) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestSchemaLayoutValidatedOnRawSave(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Name: "Layout", Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.TrimSuffix(strings.TrimSpace(d.Draft.Document), "}") + `,"x-mocker-schema-layout":{"formatVersion":1,"positions":{"A":{"x":100001,"y":0}}}}`
	if _, err = r.Save(t.Context(), d.Design.ID, SaveInput{ExpectedVersion: 1, Document: raw, Source: "ui"}); err == nil {
		t.Fatal("bad layout accepted")
	}
}
func TestRawSaveDoesNotAdoptEditorSizeLimits(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Name: "Large", Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := decodeDocument(d.Draft.Document)
	if err != nil {
		t.Fatal(err)
	}
	schemas := map[string]any{}
	for i := range 201 {
		schemas[fmt.Sprintf("Schema%d", i)] = map[string]any{"type": "object"}
	}
	root["components"] = map[string]any{"schemas": schemas}
	raw, err := jsonx.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	next, err := r.Save(t.Context(), d.Design.ID, SaveInput{ExpectedVersion: 1, Document: string(raw), Source: "ui"})
	if err != nil {
		t.Fatal("ordinary save restricted by editor limits", err)
	}
	if _, err = r.SchemaModel(t.Context(), next.Design.ID); err == nil {
		t.Fatal("editor silently accepted oversized model")
	}
}
func TestSchemaRenamePreservesOpaqueUnknownKeywordReferences(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	raw := `{"openapi":"3.1.0","info":{"title":"Unknown schema keyword","version":"1"},"paths":{},"components":{"schemas":{"User":{"type":"object"},"Holder":{"type":"object","unknownKeyword":{"$ref":"#/components/schemas/User"},"properties":{"user":{"$ref":"#/components/schemas/User"}}}}}}`
	d, err := r.Create(t.Context(), CreateInput{Name: "Unknown", Document: raw, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	next, err := r.ApplySchemaModelCommands(t.Context(), d.Design.ID, 1, "ui", []schemamodel.Command{{Kind: "rename_schema", SchemaName: "User", NewName: "Person"}})
	if err != nil {
		t.Fatal(err)
	}
	root, err := decodeDocument(next.Draft.Document)
	if err != nil {
		t.Fatal(err)
	}
	holder := root["components"].(map[string]any)["schemas"].(map[string]any)["Holder"].(map[string]any)
	if holder["unknownKeyword"].(map[string]any)["$ref"] != "#/components/schemas/User" || holder["properties"].(map[string]any)["user"].(map[string]any)["$ref"] != "#/components/schemas/Person" {
		t.Fatal("wrong reference context", holder)
	}
}
func TestSchemaModelRetainsDynamicAnchorCompatibility(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	raw := `{"openapi":"3.1.0","info":{"title":"Anchors","version":"1"},"paths":{},"components":{"schemas":{"Node":{"type":"object","$dynamicAnchor":"node","properties":{"child":{"$dynamicRef":"#node"}}}}}}`
	d, err := r.Create(t.Context(), CreateInput{Name: "Anchors", Document: raw, Source: "ui"})
	if err != nil {
		t.Fatal("existing dynamic anchor support regressed", err)
	}
	next, err := r.ApplySchemaModelCommands(t.Context(), d.Design.ID, 1, "ui", []schemamodel.Command{{Kind: "move_schema", SchemaName: "Node", X: new(1.0), Y: new(2.0)}})
	if err != nil || !strings.Contains(next.Draft.Document, `"$dynamicRef": "#node"`) {
		t.Fatal("dynamic anchor lost", err)
	}
}

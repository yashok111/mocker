package apidesign

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/resourcemap"
)

const aliasedPathItems = `{"openapi":"3.1.0","info":{"title":"Aliases","version":"1"},"paths":{"/orders":{"$ref":"#/components/pathItems/Orders"},"/purchases":{"$ref":"#/components/pathItems/Orders"}},"components":{"pathItems":{"Orders":{"get":{"responses":{"200":{"description":"OK","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Order"}}}}}}}},"schemas":{"Order":{"type":"object"}}},"x-number":9007199254740993}`

func TestResourceMapReferencedOccurrencesKeepDistinctIdentities(t *testing.T) {
	r, _ := testRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Name: "Aliases", Document: aliasedPathItems, Source: "ui"})
	if err != nil {
		diagnostics, _ := r.Validate(aliasedPathItems)
		t.Fatalf("%v: %+v", err, diagnostics)
	}
	m, err := r.ResourceMap(t.Context(), d.Design.ID)
	if err != nil || len(m.Model.Operations) != 2 {
		t.Fatalf("referenced occurrences missing: %+v %v", m.Model, err)
	}
	first, second := m.Model.Operations[0], m.Model.Operations[1]
	if first.Key == "" || first.Key == second.Key || strings.Join(first.Schemas, ",") != "Order" {
		t.Fatalf("identity or dependency incorrect: %+v", m.Model.Operations)
	}
	preview, err := r.PreviewResourceMap(t.Context(), d.Design.ID, ResourceMapProposal{Commands: []resourcemap.Command{{Kind: "upsert_resource", Resource: &resourcemap.Resource{ID: "owned", Name: "Owned", OperationKeys: []string{first.Key}}}}})
	if err != nil || !preview.Valid {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	unchanged, err := r.Detail(t.Context(), d.Design.ID)
	if err != nil || unchanged.Design.Version != 1 {
		t.Fatalf("preview wrote history: %+v %v", unchanged, err)
	}
	renamed := strings.Replace(preview.Document, `"/orders"`, `"/checkout"`, 1)
	saved, err := r.Save(t.Context(), d.Design.ID, SaveInput{ExpectedVersion: 1, Document: renamed, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	m, err = r.ResourceMap(t.Context(), d.Design.ID)
	if err != nil || len(m.Model.Operations) != 2 || m.Model.Operations[0].Key != first.Key || m.Model.Operations[0].Path != "/checkout" {
		t.Fatalf("rename lost identity: %+v %v", m.Model.Operations, err)
	}
	root, err := decodeDocument(saved.Draft.Document)
	if err != nil {
		t.Fatal(err)
	}
	target := root["components"].(map[string]any)["pathItems"].(map[string]any)["Orders"].(map[string]any)["get"].(map[string]any)
	if _, exists := target[OperationKey]; exists {
		t.Fatal("normalization wrote identity into shared definition")
	}
	if !strings.Contains(saved.Draft.Document, "9007199254740993") || !strings.Contains(saved.Draft.Document, `"$ref": "#/components/pathItems/Orders"`) {
		t.Fatal("normalization lost authored data")
	}
}

func TestReferencedIdentityUnannotatedSaveAndLegacyRead(t *testing.T) {
	r, _ := testRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Name: "Aliases", Document: aliasedPathItems, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := r.Save(t.Context(), d.Design.ID, SaveInput{ExpectedVersion: 1, Document: aliasedPathItems, Source: "ui"})
	if err != nil || saved.Design.Version != 1 || saved.Draft.Document != d.Draft.Document {
		t.Fatalf("unannotated no-op lost IDs: %+v %v", saved, err)
	}
	first, err := withOperationKeys(aliasedPathItems, "", 77)
	if err != nil {
		t.Fatal(err)
	}
	second, err := withOperationKeys(aliasedPathItems, "", 77)
	if err != nil || first != second {
		t.Fatalf("legacy inherited keys unstable: %v", err)
	}
}

func TestInvalidReferencedIdentityIsRejected(t *testing.T) {
	for _, metadata := range []string{`null`, `[]`, `{"get":null}`, `{"GET":"key"}`, `{"get":" "}`, `{"get":123}`} {
		t.Run(metadata, func(t *testing.T) {
			raw := strings.Replace(aliasedPathItems, `"/orders":{`, `"/orders":{"x-mocker-canvas-operation-ids":`+metadata+`,`, 1)
			if _, err := withOperationKeys(raw, "", 0); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted malformed inherited keys: %v", err)
			}
		})
	}
	raw := strings.ReplaceAll(aliasedPathItems, `{"$ref":"#/components/pathItems/Orders"}`, `{"$ref":"#/components/pathItems/Orders","x-mocker-canvas-operation-ids":{"get":"shared"}}`)
	if _, err := withOperationKeys(raw, "", 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("accepted duplicate consumer keys: %v", err)
	}
}

func TestReferencedPathItemSiblingsAreValidated(t *testing.T) {
	r, _ := testRepo(t)
	for _, test := range []struct {
		name, sibling string
	}{
		{"missing response", `"post":{"summary":"Must validate"}`},
		{"wrong method type", `"post":false`},
		{"missing path parameter", `"get":{"responses":{"200":{"description":"OK"}}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := fmt.Sprintf(`{"openapi":"3.1.0","info":{"title":"A","version":"1"},"paths":{"/orders/{id}":{"$ref":"#/components/pathItems/Orders",%s}},"components":{"pathItems":{"Orders":{}}}}`, test.sibling)
			diagnostics, err := r.Validate(raw)
			if err != nil || len(diagnostics) == 0 {
				t.Fatalf("invalid sibling accepted: %v %v", diagnostics, err)
			}
		})
	}
}

func TestReferencedPathItemInheritsParametersWithoutDroppingSiblings(t *testing.T) {
	r, _ := testRepo(t)
	raw := `{"openapi":"3.1.0","info":{"title":"A","version":"1"},"paths":{"/orders/{id}":{"$ref":"#/components/pathItems/Orders","get":{"responses":{"200":{"description":"OK"}}},"parameters":[{"name":"locale","in":"query","schema":{"type":"string"}}]}},"components":{"pathItems":{"Orders":{"parameters":[{"name":"id","in":"path","required":true,"schema":{"$ref":"#/components/schemas/ID"}}],"delete":{"responses":{"204":{"description":"Gone"}}}}},"schemas":{"ID":{"type":"string"}}}}`
	diagnostics, err := r.Validate(raw)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("valid inherited params rejected: %+v %v", diagnostics, err)
	}
	d, err := r.Create(t.Context(), CreateInput{Name: "Siblings", Document: raw, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := r.ResourceMap(t.Context(), d.Design.ID)
	if err != nil || len(m.Model.Operations) != 2 {
		t.Fatalf("sibling operations lost: %+v %v", m, err)
	}
	for _, operation := range m.Model.Operations {
		if strings.Join(operation.Schemas, ",") != "ID" {
			t.Fatalf("inherited parameter dependency lost: %+v", operation)
		}
		if (operation.Method == "GET") != (operation.SourcePointer == "") {
			t.Fatalf("wrong navigation source: %+v", operation)
		}
	}
}

func TestReferencedOperationsApplyCommandsWithoutChangingOtherAliases(t *testing.T) {
	r, _ := testRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Name: "Aliases", Document: aliasedPathItems, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := r.ResourceMap(t.Context(), d.Design.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, second := m.Model.Operations[0], m.Model.Operations[1]
	_, err = r.ApplyResourceMapCommands(t.Context(), d.Design.ID, 1, "mcp", []resourcemap.Command{
		{Kind: "upsert_resource", Resource: &resourcemap.Resource{ID: "owner", Name: "Owner", OperationKeys: []string{}}},
		{Kind: "assign_operation", ResourceID: "owner", OperationKey: first.Key},
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err = r.ResourceMap(t.Context(), d.Design.ID)
	if err != nil || m.Version != 2 {
		t.Fatalf("referenced command save failed: %+v %v", m, err)
	}
	assignments := map[string]string{}
	for _, resource := range m.Model.Resources {
		for _, key := range resource.OperationKeys {
			assignments[key] = resource.ID
		}
	}
	if assignments[first.Key] != "owner" || assignments[second.Key] == "owner" || assignments[second.Key] == "" {
		t.Fatalf("alias assignment leaked: %+v", assignments)
	}
}

func TestOpenAPI30PathAliasGetsOwnIdentity(t *testing.T) {
	r, _ := testRepo(t)
	raw := `{"openapi":"3.0.3","info":{"title":"Alias","version":"1"},"paths":{"/a":{"get":{"x-mocker-canvas-operation-id":"literal","responses":{"200":{"description":"OK"}}}},"/b":{"$ref":"#/paths/~1a"}}}`
	d, err := r.Create(t.Context(), CreateInput{Name: "Alias", Document: raw, Source: "ui"})
	if err != nil {
		diagnostics, _ := r.Validate(raw)
		t.Fatalf("%v: %+v", err, diagnostics)
	}
	m, err := r.ResourceMap(t.Context(), d.Design.ID)
	if err != nil || len(m.Model.Operations) != 2 || m.Model.Operations[0].Key != "literal" || m.Model.Operations[1].Key == "literal" || m.Model.Operations[1].SourcePointer != "/paths/~1a/get" {
		t.Fatalf("3.0 aliases lost: %+v %v", m, err)
	}
}

func TestRepeatedOperationIDThroughAliasesRemainsInvalid(t *testing.T) {
	r, _ := testRepo(t)
	raw := strings.Replace(aliasedPathItems, `"get":{`, `"get":{"operationId":"listOrders",`, 1)
	diagnostics, err := r.Validate(raw)
	if err != nil || len(diagnostics) == 0 {
		t.Fatalf("duplicate operationId became valid: %+v %v", diagnostics, err)
	}
	if !strings.Contains(diagnostics[0].Message, "operationId") {
		t.Fatalf("unrelated rejection: %+v", diagnostics)
	}
}

func TestPathItemReferencesToOtherObjectKindsAreInvalid(t *testing.T) {
	r, _ := testRepo(t)
	for _, ref := range []string{"#/components/schemas/Order", "#"} {
		raw := `{"openapi":"3.1.0","info":{"title":"A","version":"1"},"paths":{"/orders":{"$ref":"` + ref + `"}},"components":{"schemas":{"Order":{"type":"object"}}}}`
		diagnostics, err := r.Validate(raw)
		if err != nil || len(diagnostics) == 0 || diagnostics[0].Pointer != "/paths/~1orders/$ref" {
			t.Errorf("invalid target %s not rejected at reference site: %+v %v", ref, diagnostics, err)
		}
		root, err := decodeDocument(raw)
		if err != nil {
			t.Fatal(err)
		}
		model, err := resourcemap.Project(root)
		if err != nil || len(model.Diagnostics) == 0 || model.Diagnostics[0].Code != "path_item_ref_invalid" {
			t.Errorf("invalid target %s silently omitted from map: %+v %v", ref, model, err)
		}
	}
}

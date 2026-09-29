package apidesign

import (
	"errors"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/resourcemap"
)

func TestResourceMapPreviewIsPureAndCommandsUseVersionFence(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Name: "Resources", Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.ResourceMap(t.Context(), d.Design.ID)
	if err != nil || len(got.Model.Resources) != 1 || len(got.Model.Operations) != 1 {
		t.Fatalf("map: %+v %v", got, err)
	}
	resource := got.Model.Resources[0].Resource
	resource.Name = "Purchases"
	resource.OperationKeys = []string{}
	preview, err := r.PreviewResourceMap(t.Context(), d.Design.ID, ResourceMapProposal{Commands: []resourcemap.Command{{Kind: "upsert_resource", Resource: &resource}}})
	if err != nil || !preview.Valid || !strings.Contains(preview.Document, "Purchases") {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	current, err := r.Detail(t.Context(), d.Design.ID)
	if err != nil || current.Design.Version != 1 || strings.Contains(current.Draft.Document, "Purchases") {
		t.Fatalf("preview wrote: %+v %v", current, err)
	}
	next, err := r.ApplyResourceMapCommands(t.Context(), d.Design.ID, 1, "mcp", []resourcemap.Command{{Kind: "upsert_resource", Resource: &resource}})
	if err != nil || next.Design.Version != 2 || !strings.Contains(next.Draft.Document, "9007199254740993") || !strings.Contains(next.Draft.Document, "Purchases") {
		t.Fatalf("save: %+v %v", next, err)
	}
	if _, err := r.ApplyResourceMapCommands(t.Context(), d.Design.ID, 1, "mcp", []resourcemap.Command{{Kind: "remove_resource", ResourceID: resource.ID}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale command accepted: %v", err)
	}
}

func TestResourceMapPreviewNormalizesKeysAndValidatesPresentExtension(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Name: "Resources", Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"openapi":"3.1.0","info":{"title":"Example","version":"1"},"paths":{"/orders":{"get":{"responses":{"200":{"description":"OK"}}}}},"x-unknown":{"n":9007199254740993}}`
	preview, err := r.PreviewResourceMap(t.Context(), d.Design.ID, ResourceMapProposal{Document: &raw})
	if err != nil || len(preview.Model.Operations) != 1 || preview.Model.Operations[0].Key == "" || !strings.Contains(preview.Document, "9007199254740993") {
		t.Fatalf("normalization: %+v %v", preview, err)
	}
	if _, err := r.Save(t.Context(), d.Design.ID, SaveInput{ExpectedVersion: 1, Document: raw, Source: "ui"}); err != nil {
		t.Fatal("ordinary document rejected", err)
	}
	bad := strings.TrimSuffix(raw, "}") + `,"x-mocker-resource-map":{"formatVersion":1,"resources":{},"relations":[]}}`
	if _, err := r.Save(t.Context(), d.Design.ID, SaveInput{ExpectedVersion: 2, Document: bad, Source: "ui"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("malformed map saved: %v", err)
	}
}

func TestResourceMapRawSaveRetainsRepairableDanglingReferences(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Name: "Repair", Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"openapi":"3.1.0","info":{"title":"Repair","version":"1"},"paths":{},"x-mocker-resource-map":{"formatVersion":1,"resources":[{"id":"old","name":"Old","service":"","description":"","operationKeys":["missing-operation"],"x":0,"y":0}],"relations":[{"id":"missing-relation","fromResourceId":"old","toResourceId":"removed","label":"uses"}]}}`
	if _, err := r.Save(t.Context(), d.Design.ID, SaveInput{ExpectedVersion: 1, Document: raw, Source: "ui"}); err != nil {
		t.Fatal("repairable references rejected", err)
	}
	got, err := r.ResourceMap(t.Context(), d.Design.ID)
	if err != nil {
		t.Fatal(err)
	}
	warnings := map[string]bool{}
	for _, d := range got.Model.Diagnostics {
		warnings[d.Code] = true
	}
	if !warnings["stale_operation_assignment"] || !warnings["missing_relation_endpoint"] {
		t.Fatalf("missing repair cues: %+v", got.Model.Diagnostics)
	}
}

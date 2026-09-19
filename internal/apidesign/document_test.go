package apidesign

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestDocumentsPreserveYAMLAndRejectInvalidContracts(t *testing.T) {
	r, _ := testRepo(t)
	yaml := `openapi: 3.1.0
info:
  title: Orders
  version: '1'
paths: {}
components:
  schemas:
    Node:
      type: object
      properties:
        child:
          $ref: '#/components/schemas/Node'
x-retained:
  example: 9007199254740993
`
	d, err := r.Create(t.Context(), CreateInput{Name: "YAML", Document: yaml, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Draft.Document, "9007199254740993") || !strings.Contains(d.Draft.Document, `"x-retained"`) {
		t.Fatal(d.Draft.Document)
	}
	cases := map[string]string{
		"root":             "[]",
		"version":          `{"openapi":"2.0","info":{"title":"A","version":"1"},"paths":{}}`,
		"info":             `{"openapi":"3.1.0","paths":{}}`,
		"remote":           `{"openapi":"3.1.0","info":{"title":"A","version":"1"},"paths":{},"components":{"schemas":{"A":{"$ref":"https://example.com/a.json"}}}}`,
		"duplicate":        `{"openapi":"3.1.0","info":{"title":"A","version":"1"},"paths":{"/a":{"get":{"operationId":"same","responses":{"200":{"description":"ok"}}}},"/b":{"get":{"operationId":"same","responses":{"200":{"description":"ok"}}}}}}`,
		"path missing":     `{"openapi":"3.1.0","info":{"title":"A","version":"1"},"paths":{"/a/{id}":{"get":{"responses":{"200":{"description":"ok"}}}}}}`,
		"path optional":    `{"openapi":"3.1.0","info":{"title":"A","version":"1"},"paths":{"/a/{id}":{"parameters":[{"in":"path","name":"id","required":false,"schema":{"type":"string"}}],"get":{"responses":{"200":{"description":"ok"}}}}}}`,
		"response missing": `{"openapi":"3.1.0","info":{"title":"A","version":"1"},"paths":{"/a":{"get":{}}}}`,
	}
	for name, document := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := r.prepare(document)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("expected invalid: %v", err)
			}
		})
	}
	valid := `{"openapi":"3.1.0","info":{"title":"A","version":"1"},"paths":{"/a/{id}":{"parameters":[{"$ref":"#/components/parameters/Id"}],"get":{"responses":{"200":{"description":"ok"}}}}},"components":{"parameters":{"Id":{"in":"path","name":"id","required":true,"schema":{"type":"string"}}}}}`
	if _, err = r.prepare(valid); err != nil {
		t.Fatalf("inherited referenced parameter: %v", err)
	}
}

func TestDiffRetainsExplicitNullValues(t *testing.T) {
	changes := []Change{}
	diffValue("/example", nil, "now", &changes)
	raw, err := json.Marshal(changes)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"before":null`) {
		t.Fatalf("null was omitted: %s", raw)
	}
}

func TestReferencesRespectNamedSchemasAndExampleData(t *testing.T) {
	r, _ := testRepo(t)
	for _, schema := range []string{
		`{"type":"object","properties":{"example":{"$ref":"#/missing"}}}`,
		`{"type":"object","properties":{"properties":{"$ref":"#/missing"}}}`,
		`{"type":"object","properties":{"x-field":{"$ref":"#/missing"}}}`,
	} {
		_, err := r.prepare(`{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{},"components":{"schemas":{"A":` + schema + `}}}`)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("unresolved property ref accepted: %v", err)
		}
	}
	raw := `{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{},"components":{"schemas":{"A":{"type":"object","example":{"$ref":"user data"},"default":{"$ref":"user data"},"enum":[{"$ref":"user data"}]}}}}`
	if _, err := r.prepare(raw); err != nil {
		t.Fatalf("example data interpreted as a reference: %v", err)
	}
	raw = `{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{},"components":{"examples":{"Missing":{"$ref":"#/missing"}}}}`
	if _, err := r.prepare(raw); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Example Object reference accepted: %v", err)
	}
}

func TestChangeSetsRestoreAndForeignRevisions(t *testing.T) {
	r, _ := testRepo(t)
	ctx := t.Context()
	d, err := r.Create(ctx, CreateInput{Name: "API", Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := r.Create(ctx, CreateInput{Name: "Other", Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Revision(ctx, d.Design.ID, other.Draft.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = r.Diff(ctx, d.Design.ID, other.Draft.ID, 0); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = r.Restore(ctx, d.Design.ID, other.Draft.ID, 1, "foreign", "ui"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	set, err := r.CreateChangeSet(ctx, d.Design.ID, 1, "Agent task", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	d, err = r.Save(ctx, d.Design.ID, SaveInput{ExpectedVersion: 1, Document: testDocument, Summary: "new routes", Source: "mcp", ChangeSetID: &set.ID})
	if err != nil {
		t.Fatal(err)
	}
	if d.Draft.ChangeSetID == nil || *d.Draft.ChangeSetID != set.ID {
		t.Fatal(d.Draft)
	}
	if _, err = r.CloseChangeSet(ctx, d.Design.ID, set.ID, 1); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = r.CloseChangeSet(ctx, d.Design.ID, set.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Save(ctx, d.Design.ID, SaveInput{ExpectedVersion: 2, Document: testDocument, Source: "ui", ChangeSetID: &set.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("closed set accepted: %v", err)
	}
	restored, err := r.Restore(ctx, d.Design.ID, d.Draft.ID, 2, "restore current as new revision", "ui")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Design.Version != 3 || restored.Draft.ID == d.Draft.ID || len(restored.Revisions) != 3 {
		t.Fatal(restored)
	}
}

func TestDiffEscapingAndFrozenReview(t *testing.T) {
	r, _ := testRepo(t)
	ctx := t.Context()
	d, err := r.Create(ctx, CreateInput{Name: "API", Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	first := d.Draft
	changed := strings.ReplaceAll(testDocument, "Orders", "Edited")
	changed = strings.Replace(changed, `"operationId":"orders"`, `"operationId":"orders","description":"changed"`, 1)
	d, err = r.Save(ctx, d.Design.ID, SaveInput{ExpectedVersion: 1, Document: changed, Source: "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.RequestReview(ctx, d.Design.ID, 2, "fixed candidate", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := r.Diff(ctx, d.Design.ID, review.BaseRevisionID, review.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.From.ID != first.ID || len(frozen.Changes) != 2 {
		t.Fatal(frozen)
	}
	found := false
	for _, change := range frozen.Changes {
		if change.Pointer == "/paths/~1orders/get/description" {
			found = true
			if change.Impact != "compatible" {
				t.Fatal(change)
			}
		}
	}
	if !found {
		t.Fatal(frozen.Changes)
	}
	_, err = r.Save(ctx, d.Design.ID, SaveInput{ExpectedVersion: 2, Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := r.Diff(ctx, d.Design.ID, review.BaseRevisionID, review.RevisionID)
	if err != nil || again.To.Document != frozen.To.Document {
		t.Fatalf("frozen diff moved: %v", err)
	}
	latest, err := r.Detail(ctx, d.Design.ID)
	if err != nil || latest.Reviews[0].RevisionID != review.RevisionID {
		t.Fatal(err)
	}
}

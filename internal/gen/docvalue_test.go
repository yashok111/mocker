package gen

import (
	"encoding/json"
	"sync"
	"testing"
)

// TestBody_documentExampleIsClonedNotAliased pins the 2026-09-03 audit's
// one critical finding: leafValue used to return a schema-level example
// (and const/default/enum members) BY REFERENCE, and the list walker then
// wrote the row's id INTO that map — so every row of a page was the same
// document-owned map ending with the last id, and two concurrent requests
// wrote the same map, which is a fatal "concurrent map writes" that kills
// the process, not a panic the middleware recovers.
//
// Two assertions: rows of one page carry DISTINCT ids (the aliasing would
// collapse them), and eight concurrent generations over the same resolver
// pass -race (the aliasing would trip it).
func TestBody_documentExampleIsClonedNotAliased(t *testing.T) {
	doc := baseDoc()
	item := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":   map[string]any{"type": "integer"},
			"name": map[string]any{"type": "string"},
		},
		"example": map[string]any{"name": "from-example"},
	}
	doc["components"] = map[string]any{"schemas": map[string]any{"Widget": item}}
	doc["paths"] = map[string]any{
		"/widgets": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type":  "array",
									"items": map[string]any{"$ref": "#/components/schemas/Widget"},
								},
							},
						},
					},
				},
			},
		},
		"/widgets/{id}": map[string]any{
			"get": map[string]any{
				"parameters": []any{map[string]any{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "integer"}}},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/Widget"},
							},
						},
					},
				},
			},
		},
	}
	res := buildResolver(t, doc)
	g := New(res, Options{Seed: 1})
	v := ResponseVariant{
		Selector:  "200",
		MediaType: "application/json",
		SchemaPtr: "#/paths/~1widgets/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1widgets/get",
	}
	req := Request{Method: "GET", CanonicalPath: "/widgets", Status: 200}

	b, err := g.Body(v, req)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, b)
	}
	if len(rows) < 2 {
		t.Fatalf("want a page of at least two rows, got %d: %s", len(rows), b)
	}
	seen := map[any]bool{}
	for i, row := range rows {
		if row["name"] != "from-example" {
			t.Fatalf("row %d: example did not win: %v", i, row)
		}
		if seen[row["id"]] {
			t.Fatalf("row %d repeats id %v — the rows alias one document map: %s", i, row["id"], b)
		}
		seen[row["id"]] = true
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 4 {
				if _, err := g.Body(v, req); err != nil {
					t.Errorf("concurrent Body: %v", err)
				}
			}
		}()
	}
	wg.Wait()
}

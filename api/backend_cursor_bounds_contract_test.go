package api

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
)

// TestBackendCursorBoundsMatchTheServer pins review 2026-10-06, F22 and F14:
// the contract let an events cursor be 4096 characters and left flow cursor
// and search unbounded, while decodeGraphPage refuses a cursor over 1024
// bytes and the flow query a search over 1024 bytes; and it presented limit
// and cursor as independent although the events, diagram-list and analysis
// cursors bind the page size they were issued under.
func TestBackendCursorBoundsMatchTheServer(t *testing.T) {
	for _, name := range []string{"QueryBackendEventsRequest", "QueryBackendFlowRequest"} {
		schema, err := BackendSchema(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, arm := range schema["oneOf"].([]any) {
			properties := arm.(map[string]any)["properties"].(map[string]any)
			for _, member := range []string{"cursor", "search"} {
				field, ok := properties[member].(map[string]any)
				if !ok {
					continue
				}
				if fmt.Sprint(field["maxLength"]) != "1024" {
					t.Errorf("%s arm %d %s maxLength = %v, want 1024", name, i, member, field["maxLength"])
				}
				if member == "cursor" && name == "QueryBackendEventsRequest" && !strings.Contains(fmt.Sprint(field["description"]), "limit") {
					t.Errorf("%s arm %d cursor does not say it binds limit", name, i)
				}
			}
		}
	}
	var doc struct {
		Paths map[string]map[string]jsontext.Value `json:"paths"`
	}
	if err := json.Unmarshal(backendContract, &doc); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/backend-projects/{id}/analyses", "/api/backend-projects/{id}/analyses/{aid}/results", "/api/backend-projects/{id}/diagrams", "/api/backend-projects/{id}/diagram-views"} {
		var get struct {
			Parameters []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"parameters"`
		}
		if err := json.Unmarshal(doc.Paths[path]["get"], &get); err != nil {
			t.Fatal(path, err)
		}
		found := false
		for _, p := range get.Parameters {
			if p.Name == "cursor" {
				found = true
				if !strings.Contains(p.Description, "limit") {
					t.Errorf("GET %s cursor does not say it binds limit: %q", path, p.Description)
				}
			}
		}
		if !found {
			t.Errorf("GET %s has no cursor parameter", path)
		}
	}
}

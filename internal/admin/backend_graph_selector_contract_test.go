package admin

import (
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendGraphEdgeInventoryOmitsNodeSelectors(t *testing.T) {
	server := loopbackTestServer(t, nil)
	var project backendmodel.Project
	b41Call(t, server, "POST", "/api/backend-projects", backendmodel.CreateInput{Name: "Edge inventory", IdempotencyKey: "edge-inventory"}, 201, &project)
	path := "/api/backend-projects/" + project.ID + "/graph/query"
	// This is the wire body emitted by the shared UI after selecting edges.
	body := fmt.Sprintf(`{"revisionId":%q,"recordType":"edges","kind":"","limit":100,"cursor":""}`, project.CurrentRevisionID)
	b41Call(t, server, "POST", path, body, 200, nil)
	// Node-only selectors are forbidden by presence, including empty values.
	for _, target := range []string{
		fmt.Sprintf(`"revisionId":%q`, project.CurrentRevisionID),
		fmt.Sprintf(`"changeProposal":{"proposalId":%q,"proposalRevisionId":%q}`, project.ID, project.CurrentRevisionID),
		fmt.Sprintf(`"importCandidate":{"importId":%q,"importVersion":1,"candidateHash":%q}`, project.ID, strings.Repeat("a", 64)),
	} {
		for _, field := range []string{"search", "parentId"} {
			invalid := fmt.Sprintf(`{%s,"recordType":"edges",%q:"","limit":100}`, target, field)
			var failure struct {
				Error struct {
					Details struct {
						Path string `json:"path"`
					} `json:"details"`
				} `json:"error"`
			}
			response := b41Call(t, server, "POST", path, invalid, 422, nil)
			if err := json.Unmarshal(response, &failure); err != nil {
				t.Fatal(err)
			}
			if failure.Error.Details.Path != "/"+field {
				t.Fatalf("expected selector rejection at /%s: %s", field, response)
			}
		}
	}
}

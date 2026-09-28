package mcp

import (
	"errors"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/internal/statediagram"
)

type stateDiagramReadInput struct {
	DesignID  int64  `json:"designId" jsonschema:"API design id"`
	DiagramID string `json:"diagramId" jsonschema:"stable diagram id from list_state_diagrams"`
}
type stateDiagramWriteInput struct {
	DesignID        int64                  `json:"designId"`
	DiagramID       string                 `json:"diagramId,omitempty"`
	ExpectedVersion int64                  `json:"expectedVersion" jsonschema:"current API design version; reread and reconcile a 409"`
	Diagram         *statediagram.Diagram  `json:"diagram,omitempty" jsonschema:"complete diagram for create/save; other API fields are retained"`
	Commands        []statediagram.Command `json:"commands,omitempty" jsonschema:"ordered atomic edits: upsert_state, remove_state, upsert_transition, remove_transition, settings; upserts replace the complete object"`
}
type stateDiagramEvaluateInput struct {
	DesignID      int64                 `json:"designId"`
	DiagramID     string                `json:"diagramId"`
	Diagram       *statediagram.Diagram `json:"diagram,omitempty" jsonschema:"optional unsaved diagram to evaluate instead of the saved one"`
	Document      string                `json:"document,omitempty" jsonschema:"optional proposed OpenAPI for resolving bindings; otherwise the saved draft"`
	DataJSON      string                `json:"dataJSON,omitempty" jsonschema:"initial entity JSON object text; exact large numbers retained; default {}"`
	TransitionIDs []string              `json:"transitionIds,omitempty" jsonschema:"up to 100 ordered transition ids to replay from initial state; stops at first blocked step"`
}

func stateDiagramCall(id int64, diagramID string) (apiDesignCall, error) {
	call, err := apiDesignRead(id)
	if err != nil {
		return call, err
	}
	if !statediagram.ValidID(diagramID) {
		return call, errors.New("diagramId must contain 1-80 letters, digits, hyphens or underscores")
	}
	call.params = append(call.params, diagramID)
	return call, nil
}
func addStateDiagramTools(s *sdk.Server, lb *loopback) {
	const base = "/api/designs/{id}/state-diagrams"
	addAPIDesignTool(s, lb, "list_state_diagrams", "GET "+base, "Lists lifecycle diagrams inside an API draft with the API version and revision id. Diagrams share API history, diff, export and restore.", true, func(in apiDesignIDInput) (apiDesignCall, error) { return apiDesignRead(in.DesignID) })
	addAPIDesignTool(s, lb, "get_state_diagram", "GET "+base+"/{did}", "Reads one complete lifecycle diagram, API version and revision id. Read before editing. Positions, conditions and operation bindings are shared with the UI.", true, func(in stateDiagramReadInput) (apiDesignCall, error) {
		return stateDiagramCall(in.DesignID, in.DiagramID)
	})
	for _, item := range []struct{ name, method, suffix, description string }{
		{"create_state_diagram", "POST", "", "Creates a diagram in the API draft. Supply diagram with stable id and expectedVersion. No mock behavior changes. On a lost response inspect list_state_diagrams before retrying."},
		{"save_state_diagram", "PUT", "/{did}", "Replaces one COMPLETE diagram, preserving all other API and diagram fields. diagram.id must match diagramId. Requires expectedVersion; conflicts must be reconciled."},
		{"delete_state_diagram", "DELETE", "/{did}", "Removes one diagram from the API draft, retaining revision history. Requires diagramId and expectedVersion."},
		{"apply_state_diagram_commands", "POST", "/{did}/commands", "Applies ordered atomic edits to one diagram. upsert_state/state and upsert_transition/transition replace complete objects; remove_state/id removes incident edges and clears initial selection; remove_transition/id; settings/name/initialStateId. Requires expectedVersion. Use transitions.binding to link method/path in this API. Returns updated API detail."},
	} {
		addAPIDesignTool(s, lb, item.name, item.method+" "+base+item.suffix, item.description, false, func(in stateDiagramWriteInput) (apiDesignCall, error) {
			if in.ExpectedVersion <= 0 {
				return apiDesignCall{}, errors.New("expectedVersion must be positive")
			}
			var call apiDesignCall
			var err error
			if item.suffix == "" {
				call, err = apiDesignRead(in.DesignID)
			} else {
				call, err = stateDiagramCall(in.DesignID, in.DiagramID)
			}
			call.body = struct {
				ExpectedVersion int64                  `json:"expectedVersion"`
				Diagram         *statediagram.Diagram  `json:"diagram,omitempty"`
				Commands        []statediagram.Command `json:"commands,omitempty"`
			}{in.ExpectedVersion, in.Diagram, in.Commands}
			return call, err
		})
	}
	for _, item := range []struct{ name, suffix, description string }{
		{"validate_state_diagram", "validate", "Checks a saved or proposed diagram: initial/terminal states, endpoints, reachability, API bindings and JSON guards/patches. Returns diagnostics. No save, HTTP execution or entity writes."},
		{"simulate_state_diagram", "simulate", "Replays transitionIds from the initial state against dataJSON using the shared UI evaluator. Returns stateId, exact dataJSON, ordered accepted/blocked steps and diagnostics. Conditions use JSON Pointer equality; patchJSON shallow merges on accepted steps. Stops at first rejection. No HTTP calls, saved run or entity mutations. Empty transitionIds inspects the initial simulation."},
	} {
		addAPIDesignTool(s, lb, item.name, "POST "+base+"/{did}/"+item.suffix, item.description, true, func(in stateDiagramEvaluateInput) (apiDesignCall, error) {
			call, err := stateDiagramCall(in.DesignID, in.DiagramID)
			call.body = struct {
				Diagram       *statediagram.Diagram `json:"diagram,omitempty"`
				Document      string                `json:"document,omitempty"`
				DataJSON      string                `json:"dataJSON,omitempty"`
				TransitionIDs []string              `json:"transitionIds,omitempty"`
			}{in.Diagram, in.Document, in.DataJSON, in.TransitionIDs}
			return call, err
		})
	}
}

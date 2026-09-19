package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/jsonx"
)

// The designer tools return the admin response unchanged. Its authored document,
// immutable revisions and structural diff are already the agent-facing view.
// Publication deliberately has no tool: a UI session confirms the candidate.
func addAPIDesignTools(s *sdk.Server, lb *loopback) {
	addAPIDesignTool(s, lb, "list_api_designs", "GET /api/designs",
		"Lists API design projects with draft and published mock URLs and their current versions.", true,
		func(_ struct{}) (apiDesignCall, error) { return apiDesignCall{}, nil })
	addAPIDesignTool(s, lb, "create_api_design", "POST /api/designs",
		"Creates an API design with isolated draft and published mock addresses. Supply the full OpenAPI JSON/YAML as document, OR workspaceId to capture an existing workspace without changing it. Omit both for an empty OpenAPI 3.1 document. Returns the draft and its version. Nothing is published. Not idempotent: after a lost response, check list_api_designs before creating again.", false,
		func(in createAPIDesignInput) (apiDesignCall, error) {
			if strings.TrimSpace(in.Name) == "" {
				return apiDesignCall{}, errors.New("name is required")
			}
			if in.WorkspaceID != nil && (*in.WorkspaceID <= 0 || in.Document != "") {
				return apiDesignCall{}, errors.New("workspaceId must be positive and cannot be combined with document")
			}
			return apiDesignCall{body: in}, nil
		})
	addAPIDesignTool(s, lb, "get_api_design", "GET /api/designs/{id}",
		"Reads the complete authored draft, its version, publication state, releases, review candidates, revision history and named change sets. Read this before editing; preserve all unrelated document fields. UI and MCP share this state. Managed mock workspaces cannot be edited with legacy workspace tools.", true,
		func(in apiDesignIDInput) (apiDesignCall, error) { return apiDesignRead(in.DesignID) })
	addAPIDesignTool(s, lb, "save_api_design_draft", "PUT /api/designs/{id}/draft",
		"Saves the COMPLETE authored OpenAPI document as JSON/YAML text. Read get_api_design first and send its exact expectedVersion; this is full replacement, so resend every field you want retained. summary describes this edit; optional changeSetId groups an agent task. Server validates local references and contract structure, then atomically saves history and switches only the draft mock. A 409 carries the current version: compare and resolve, never blindly retry with the newer number. Analyst publication remains separate.", false,
		func(in saveAPIDesignInput) (apiDesignCall, error) {
			call, err := apiDesignWrite(in.DesignID, in.ExpectedVersion)
			if err != nil {
				return call, err
			}
			if strings.TrimSpace(in.Document) == "" {
				return call, errors.New("document is required")
			}
			if in.ChangeSetID != nil && *in.ChangeSetID <= 0 {
				return call, errors.New("changeSetId must be positive")
			}
			call.body = struct {
				ExpectedVersion int64  `json:"expectedVersion"`
				Document        string `json:"document"`
				Summary         string `json:"summary"`
				ChangeSetID     *int64 `json:"changeSetId,omitempty"`
			}{in.ExpectedVersion, in.Document, in.Summary, in.ChangeSetID}
			return call, nil
		})
	addAPIDesignTool(s, lb, "get_api_design_revision", "GET /api/designs/{id}/revisions/{rid}",
		"Reads one immutable authored contract revision. revisionId must belong to this design. Use its document as the exact historical export, or compare it through get_api_design_diff.", true,
		func(in apiDesignRevisionInput) (apiDesignCall, error) {
			return apiDesignRead(in.DesignID, in.RevisionID)
		})
	addAPIDesignTool(s, lb, "get_api_design_diff", "GET /api/designs/{id}/diff",
		"Compares immutable authored revisions, returning canonical before/after documents and field-level changes with JSON pointers and compatibility hints. Defaults to last publication (initial import before the first publication) versus draft. Pass fromRevisionId and toRevisionId for historical comparisons. impact review means compatibility requires human analysis, not a guarantee.", true,
		func(in apiDesignDiffInput) (apiDesignCall, error) {
			call, err := apiDesignRead(in.DesignID)
			if err != nil {
				return call, err
			}
			q := url.Values{}
			for name, id := range map[string]*int64{"fromRevisionId": in.FromRevisionID, "toRevisionId": in.ToRevisionID} {
				if id == nil {
					continue
				}
				if *id <= 0 {
					return call, fmt.Errorf("%s must be positive", name)
				}
				q.Set(name, strconv.FormatInt(*id, 10))
			}
			call.query = q.Encode()
			return call, nil
		})
	addAPIDesignTool(s, lb, "validate_api_design", "POST /api/designs/{id}/validate",
		"Validates proposed OpenAPI JSON/YAML without saving it or changing either mock. Returns valid and diagnostics with JSON pointers. Use this to repair malformed schemas, unresolved local references, duplicate operationId or path parameter errors before saving.", true,
		func(in validateAPIDesignInput) (apiDesignCall, error) {
			call, err := apiDesignRead(in.DesignID)
			call.body = struct {
				Document string `json:"document"`
			}{in.Document}
			return call, err
		})
	addAPIDesignTool(s, lb, "create_api_design_change_set", "POST /api/designs/{id}/change-sets",
		"Opens a named task grouping multiple draft revisions. title explains the requested API change; expectedVersion is the current design version. Pass the returned id to save_api_design_draft.changeSetId. Opening the set does not change the document or publish it.", false,
		func(in createAPIDesignChangeSetInput) (apiDesignCall, error) {
			call, err := apiDesignWrite(in.DesignID, in.ExpectedVersion)
			call.body = struct {
				ExpectedVersion int64  `json:"expectedVersion"`
				Title           string `json:"title"`
			}{in.ExpectedVersion, in.Title}
			return call, err
		})
	addAPIDesignTool(s, lb, "close_api_design_change_set", "PUT /api/designs/{id}/change-sets/{cid}",
		"Closes an agent/analyst task without publishing it. Further saves into that change set are refused. Supply the current expectedVersion; the revision history remains available.", false,
		func(in closeAPIDesignChangeSetInput) (apiDesignCall, error) {
			call, err := apiDesignWrite(in.DesignID, in.ExpectedVersion, in.ChangeSetID)
			call.body = apiDesignVersionBody{in.ExpectedVersion}
			return call, err
		})
	addAPIDesignTool(s, lb, "request_api_design_review", "POST /api/designs/{id}/reviews",
		"Freezes the exact current draft and comparison baseline for analyst review; expectedVersion must match. Returns reviewUrl for the human. Does NOT publish: only an analyst's browser session can confirm publication. New edits supersede this candidate. Use get_api_design to observe whether the analyst published it.", false,
		func(in requestAPIDesignReviewInput) (apiDesignCall, error) {
			call, err := apiDesignWrite(in.DesignID, in.ExpectedVersion)
			call.body = struct {
				ExpectedVersion int64  `json:"expectedVersion"`
				Summary         string `json:"summary"`
			}{in.ExpectedVersion, in.Summary}
			return call, err
		})
	addAPIDesignTool(s, lb, "restore_api_design_revision", "POST /api/designs/{id}/restore",
		"Restores an old authored revision as a NEW draft, preserving all history and leaving the published mock unchanged. revisionId must belong to this design; expectedVersion protects concurrent edits. Compare the result and request human review before publication.", false,
		func(in restoreAPIDesignInput) (apiDesignCall, error) {
			call, err := apiDesignWrite(in.DesignID, in.ExpectedVersion)
			if err != nil {
				return call, err
			}
			if in.RevisionID <= 0 {
				return call, errors.New("revisionId must be positive")
			}
			call.body = struct {
				ExpectedVersion int64  `json:"expectedVersion"`
				RevisionID      int64  `json:"revisionId"`
				Summary         string `json:"summary"`
			}{in.ExpectedVersion, in.RevisionID, in.Summary}
			return call, nil
		})
}

type apiDesignCall struct {
	params []any
	query  string
	body   any
}

func apiDesignRead(ids ...int64) (apiDesignCall, error) {
	call := apiDesignCall{params: make([]any, len(ids))}
	for i, id := range ids {
		if id <= 0 {
			return call, errors.New("design and object IDs must be positive integers")
		}
		call.params[i] = id
	}
	return call, nil
}

func apiDesignWrite(designID, expectedVersion int64, objectIDs ...int64) (apiDesignCall, error) {
	if expectedVersion <= 0 {
		return apiDesignCall{}, errors.New("expectedVersion must be the positive version from get_api_design")
	}
	return apiDesignRead(append([]int64{designID}, objectIDs...)...)
}

func addAPIDesignTool[Input any](s *sdk.Server, lb *loopback, name, route, description string, readOnly bool, build func(Input) (apiDesignCall, error)) {
	sdk.AddTool(s, &sdk.Tool{Name: name, Description: description, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: readOnly, IdempotentHint: readOnly}},
		func(ctx context.Context, _ *sdk.CallToolRequest, in Input) (*sdk.CallToolResult, any, error) {
			call, err := build(in)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", name, err)
			}
			method, path := toolPath(name, route, call.params...)
			if call.query != "" {
				path += "?" + call.query
			}
			var body []byte
			if call.body != nil {
				body, err = jsonx.Marshal(call.body)
				if err != nil {
					return nil, nil, fmt.Errorf("%s: encode input: %w", name, err)
				}
			}
			status, response, err := lb.do(ctx, method, path, body)
			if err != nil {
				return nil, nil, err
			}
			if status < 200 || status >= 300 {
				return nil, nil, apiDesignToolError(status, response)
			}
			var out map[string]jsonx.RawMessage
			if err := jsonx.Unmarshal(response, &out); err != nil {
				return nil, nil, fmt.Errorf("%s: decode response: %w", name, err)
			}
			if name == "request_api_design_review" {
				var review struct {
					ID int64 `json:"id"`
				}
				if err := jsonx.Unmarshal(response, &review); err != nil || review.ID <= 0 {
					return nil, nil, errors.New("request_api_design_review: response has no valid review id")
				}
				out["reviewUrl"], err = jsonx.Marshal(fmt.Sprintf("/designs/%v?reviewId=%d", call.params[0], review.ID))
				if err != nil {
					return nil, nil, err
				}
				response, err = jsonx.Marshal(out)
				if err != nil {
					return nil, nil, err
				}
			}
			// Supply raw structured content: the SDK's generic output-schema pass
			// round-trips arbitrary numbers through float64, corrupting authored
			// examples and constraints above 2^53 in structural diffs.
			return &sdk.CallToolResult{
				StructuredContent: jsonx.RawMessage(response),
				Content:           []sdk.Content{&sdk.TextContent{Text: string(response)}},
			}, nil, nil
		})
}

func apiDesignToolError(status int, body []byte) error {
	err := toolErr(status, body)
	if status >= 400 && status < 500 {
		var envelope struct {
			Error struct {
				Details jsonx.RawMessage `json:"details"`
			} `json:"error"`
		}
		if jsonx.Unmarshal(body, &envelope) == nil && len(envelope.Error.Details) > 0 {
			return fmt.Errorf("%w; details: %s", err, envelope.Error.Details)
		}
	}
	return err
}

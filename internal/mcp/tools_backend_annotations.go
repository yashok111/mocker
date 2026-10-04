package mcp

import (
	"errors"
	"net/url"
	"strconv"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/api"
	"github.com/yashok111/mocker/internal/backendmodel"
)

func addBackendAnnotationTools(s *sdk.Server, lb *loopback) {
	schema, err := api.BackendSchema("ListBackendAnnotationsQuery")
	if err != nil {
		panic(err)
	}
	backendToolPathSchema(schema, []string{"projectId"})
	tool := &sdk.Tool{Name: "list_backend_annotations", Description: "Lists project annotations with current, historical or orphaned targets; default page size 100, maximum 500. Filters intersect and targetId requires recordType. Cursor binds metadata version and source head; restart pages on 409. Annotation edits use project commands and preserve source semantics.", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}
	addBackendTool(s, lb, tool, "GET /api/backend-projects/{id}/annotations")
}
func backendAnnotationToolCall(in backendToolInput) (designScenarioCall, error) {
	if !backendmodel.ValidID(in.ProjectID) {
		return designScenarioCall{}, errors.New("projectId must be a canonical UUID")
	}
	q := url.Values{}
	for key, value := range map[string]string{"annotationId": in.AnnotationID, "targetId": in.TargetID, "revisionId": in.RevisionID} {
		if value != "" {
			if !backendmodel.ValidID(value) {
				return designScenarioCall{}, errors.New("annotation filters require canonical UUIDs")
			}
			q.Set(key, value)
		}
	}
	if in.RecordType != "" {
		q.Set("recordType", in.RecordType)
	}
	if in.TargetID != "" && in.RecordType == "" {
		return designScenarioCall{}, errors.New("targetId requires recordType")
	}
	if in.Orphaned != nil {
		q.Set("orphaned", strconv.FormatBool(*in.Orphaned))
	}
	if in.Limit > 0 {
		q.Set("limit", strconv.Itoa(in.Limit))
	}
	if in.Cursor != "" {
		q.Set("cursor", in.Cursor)
	}
	return designScenarioCall{params: []any{in.ProjectID}, query: q.Encode()}, nil
}

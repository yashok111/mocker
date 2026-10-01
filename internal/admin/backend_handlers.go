package admin

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/guide"
	"github.com/yashok111/mocker/internal/httpx"
)

func (s *Server) backendError(w http.ResponseWriter, err error) {
	if fault, ok := errors.AsType[*backendmodel.FaultError](err); ok {
		httpx.JSON(w, fault.Status, struct {
			Error *backendmodel.FaultError `json:"error"`
		}{fault})
		return
	}
	s.log.Error("backend project", "err", err)
	httpx.JSON(w, 500, map[string]any{"error": backendmodel.FaultError{Code: "backend_internal", Message: "Unable to complete backend project operation", Retryable: false}})
}

func (s *Server) backendBody(w http.ResponseWriter, r *http.Request, out any) bool {
	return s.backendBodyLimit(w, r, out, s.cfg.MaxBody)
}

func (s *Server) backendBodyLimit(w http.ResponseWriter, r *http.Request, out any, maxBytes int64) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes))
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		s.backendError(w, &backendmodel.FaultError{Status: 413, Code: "backend_too_large", Message: "Request exceeds maxBodyBytes", Details: map[string]any{"maxBodyBytes": maxBytes}})
		return false
	}
	if err == nil {
		if trimmed := bytes.TrimSpace(body); len(trimmed) == 0 || trimmed[0] != '{' {
			err = errors.New("request must be an object")
		}
	}
	if err == nil {
		err = json.Unmarshal(body, out, json.RejectUnknownMembers(true))
	}
	if err != nil {
		s.backendError(w, &backendmodel.FaultError{Status: 400, Code: "backend_invalid", Message: "Request must be one JSON object matching the request schema"})
		return false
	}
	return true
}

func backendListInput(r *http.Request) (backendmodel.ListInput, error) {
	var input backendmodel.ListInput
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return input, &backendmodel.FaultError{Status: 400, Code: "backend_invalid", Message: "Invalid query parameters"}
	}
	for key, values := range query {
		if (key != "limit" && key != "cursor") || len(values) != 1 {
			return input, &backendmodel.FaultError{Status: 400, Code: "backend_invalid", Message: "Only one limit and cursor query parameter is allowed"}
		}
	}
	if values, ok := query["limit"]; ok {
		input.Limit, err = strconv.Atoi(values[0])
		if err != nil || input.Limit < 1 || input.Limit > backendmodel.MaxPageSize {
			return input, &backendmodel.FaultError{Status: 400, Code: "backend_invalid", Message: "limit must be between 1 and 100"}
		}
	}
	input.Cursor = query.Get("cursor")
	return input, nil
}

func (s *Server) handleListBackendProjects(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	in, err := backendListInput(r)
	if err != nil {
		s.backendError(w, err)
		return
	}
	page, err := s.backendRepo.List(r.Context(), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, page)
}

func (s *Server) handleCreateBackendProject(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendmodel.CreateInput
	if !s.backendBody(w, r, &in) {
		return
	}
	project, err := s.backendRepo.Create(r.Context(), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	w.Header().Set("Location", "/api/backend-projects/"+project.ID)
	httpx.JSON(w, 201, project)
}

func (s *Server) handleGetBackendProject(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	project, err := s.backendRepo.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, project)
}

func (s *Server) handleApplyBackendProjectCommands(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendmodel.CommandsInput
	if !s.backendBody(w, r, &in) {
		return
	}
	project, err := s.backendRepo.Apply(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, project)
}

func (s *Server) handleListBackendRevisions(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	in, err := backendListInput(r)
	if err != nil {
		s.backendError(w, err)
		return
	}
	page, err := s.backendRepo.Revisions(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, page)
}

func (s *Server) handleGetBackendRevision(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	revision, err := s.backendRepo.Revision(r.Context(), r.PathValue("id"), r.PathValue("rid"))
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, revision)
}

func (s *Server) handleGetBackendCapabilities(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	httpx.JSON(w, 200, map[string]any{
		"modelSchemaVersions":      backendmodel.SupportedModelSchemaVersions(),
		"workflowVersions":         guide.BackendWorkflows(),
		"features":                 append(backendmodel.Features(), "backend-relational-import", "backend-database-query", "backend-database-er", "backend-db-proposals", "backend-db-typed-edits"),
		"providerProfiles":         []string{backendmodel.GraphProfile, backendmodel.RelationalProfile},
		"supportedNodeKinds":       backendmodel.SupportedNodeKindsForProfile(backendmodel.RelationalProfile),
		"supportedEdgeKinds":       backendmodel.SupportedEdgeKindsForProfile(backendmodel.RelationalProfile),
		"guideSetId":               guide.CurrentGuideSetID(),
		"viewSchemaVersions":       []string{backendmodel.ProposalDocumentVersion},
		"proposalDocumentVersions": []string{backendmodel.ProposalDocumentVersion},
		"proposalCommands":         []map[string]string{{"type": "alter_column", "property": "nullable"}, {"type": "alter_constraint", "action": "create", "constraintKind": "foreign_key"}, {"type": "alter_constraint", "action": "update", "constraintKind": "foreign_key"}, {"type": "set_criteria"}},
		"importModes":              []string{"initial", "reconcile"},
		"importCommands":           []string{"upsert_node", "upsert_edge", "upsert_evidence", "remove", "map_identity", "delete_assertion"},
		"reconciliationProfile":    map[string]string{"version": "1", "profile": backendmodel.GraphProfile, "scope": "whole-repository"},
		"profileCapabilities": []map[string]any{
			{"profile": backendmodel.GraphProfile, "modelSchemaVersions": []string{backendmodel.SchemaVersion}, "nodeKinds": backendmodel.SupportedNodeKindsForProfile(backendmodel.GraphProfile), "edgeKinds": backendmodel.SupportedEdgeKindsForProfile(backendmodel.GraphProfile), "importModes": []string{"initial", "reconcile"}},
			{"profile": backendmodel.RelationalProfile, "modelSchemaVersions": []string{backendmodel.RelationalSchemaVersion}, "nodeKinds": backendmodel.SupportedNodeKindsForProfile(backendmodel.RelationalProfile), "edgeKinds": backendmodel.SupportedEdgeKindsForProfile(backendmodel.RelationalProfile), "importModes": []string{"initial", "reconcile"}},
		},
		"profileExtensions": []backendmodel.ImportProfileExtension{{FromProfile: backendmodel.GraphProfile, ToProfile: backendmodel.RelationalProfile}},
		"reconciliationProfiles": []map[string]string{
			{"version": "1", "profile": backendmodel.GraphProfile, "scope": "whole-repository"},
			{"version": "1", "profile": backendmodel.RelationalProfile, "scope": "whole-repository-combined-graph"},
		},
		"comparisonVersion": int64(1),
		"limits": map[string]any{"maxNameLength": backendmodel.MaxNameLength, "maxIdempotencyKeyLength": backendmodel.MaxKeyLength, "defaultPageSize": backendmodel.DefaultPageSize, "maxPageSize": backendmodel.MaxPageSize, "maxCommands": 1, "maxBodyBytes": s.cfg.MaxBody,
			"maxImportBatchCommands": backendmodel.MaxImportCommands, "maxImportBatchBytes": min(s.cfg.MaxBody, int64(backendmodel.MaxImportBatchBytes)),
			"maxManifestFiles": backendmodel.MaxManifestFiles, "maxSnippetBytes": backendmodel.MaxEvidenceSnippetBytes,
			"maxRevisionNodes": backendmodel.MaxRevisionNodes, "maxRevisionEdges": backendmodel.MaxRevisionEdges, "maxRevisionEvidence": backendmodel.MaxRevisionEvidence,
			"maxRevisionPayloadBytes": backendmodel.MaxRevisionBytes, "maxProjectStagingBytes": backendmodel.MaxProjectStagingBytes,
			"maxOpenImportSessions": backendmodel.MaxOpenImportSessions, "defaultGraphPageSize": backendmodel.DefaultGraphPageSize, "maxGraphPageSize": backendmodel.MaxGraphPageSize,
			"maxRelationalFacets": backendmodel.MaxRelationalFacets, "maxRelationalOrderedColumns": backendmodel.MaxRelationalOrderedColumns, "maxRelationalIndexTerms": backendmodel.MaxRelationalIndexTerms, "maxRelationalNativeBytes": backendmodel.MaxRelationalNativeBytes, "maxRelationalReferences": backendmodel.MaxRelationalReferences, "defaultDatabasePageSize": backendmodel.DefaultGraphPageSize, "maxDatabasePageSize": backendmodel.MaxGraphPageSize,
			"maxProposalCommands":                  backendmodel.MaxProposalCommands,
			"maxProposalBatchBytes":                min(s.cfg.MaxBody, int64(backendmodel.MaxProposalCommandBytes)),
			"maxProposalAuthoredCriteria":          backendmodel.MaxProposalCriteria,
			"maxProposalReasonBytes":               backendmodel.MaxProposalTextBytes,
			"maxProposalCriterionDescriptionBytes": backendmodel.MaxProposalTextBytes},
	})
}

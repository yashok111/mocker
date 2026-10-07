package admin

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/guide"
	"github.com/yashok111/mocker/internal/httpx"
)

func (s *Server) backendError(w http.ResponseWriter, err error) {
	if fault, ok := errors.AsType[*backendmodel.FaultError](err); ok {
		if fault.Code == "backend_analysis_queue_full" && fault.Status == 429 {
			w.Header().Set("Retry-After", "2")
		}
		httpx.JSON(w, fault.Status, struct {
			Error *backendmodel.FaultError `json:"error"`
		}{fault})
		return
	}
	// Review 2026-10-06, F176: a request that was cancelled or ran out of
	// time (often while queued behind the single writer, where store.Write
	// wraps it as "begin: context canceled") is not a server fault. It was
	// logged at ERROR and answered as a non-retryable 500, which polluted the
	// log and told a client that only timed out not to try again. A client
	// that disconnected never reads this answer; one that hit a deadline does.
	// The precedent is stepRepositoryError's execution_cancelled.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		s.log.Info("backend project request ended early", "err", err)
		httpx.JSON(w, 503, map[string]any{"error": backendmodel.FaultError{Code: "backend_request_cancelled", Message: "Request was cancelled or timed out before the operation finished", Retryable: true}})
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
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var in backendmodel.CommandsInput
	if !s.backendBodyLimit(w, r, &in, min(s.cfg.MaxBody, int64(backendmodel.MaxProjectCommandBytes))) {
		return
	}
	project, err := s.backendRepo.ApplyAs(r.Context(), r.PathValue("id"), in, user.Name)
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
	installation, err := s.backendRepo.InstallationID(r.Context())
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{
		"installationId":           installation,
		"observationSupport":       map[string]any{"documentVersion": "backend-observations-v1", "correlationPolicy": "backend-correlation-v1", "maxBatchRecords": 500, "maxBatchBytes": 1048576, "maxAdapterBytes": 4194304, "maxSpanLinks": 32},
		"replaySupport":            map[string]any{"packageVersion": "backend-replay-v1", "protocolVersion": "orders-replay-v1", "checkerVersion": "orders-checker-v1", "queueLimit": 20, "workers": 2, "executionSeconds": 60, "paymentScope": "mocked", "persistenceScope": "actual_fixture", "requiresExplicitConsent": true},
		"portableSupport":          map[string]any{"format": "backend-portable-v1", "sourceSchemaVersions": []string{"5", "6"}, "artifactContextVersion": "artifact-context-v3", "maxChunkBytes": 1048576, "maxChunkRecords": 500, "maxBundleBytes": 268435456, "maxArtifactMappings": 20, "importMode": "new_project", "previewPublishes": false},
		"modelSchemaVersions":      backendmodel.SupportedModelSchemaVersions(),
		"workflowVersions":         guide.BackendWorkflows(),
		"features":                 backendCapabilityFeatures(),
		"diagramSupport":           map[string]any{"documentVersion": "backend-diagram-v1", "viewDocumentVersion": "diagram-view-v1", "kinds": []string{"architecture", "interactions", "lifecycle", "business_map"}, "targets": []string{"revisionId", "changeProposal"}, "projectionPolicy": "architecture-v1", "levels": []string{"context", "containers", "components"}},
		"analysisSupport":          map[string]any{"documentVersion": "backend-analysis-context-v1", "documentVersions": []string{"backend-analysis-context-v1", "backend-analysis-context-v2"}, "inputDocumentVersions": []string{"backend-analysis-input/v1", "backend-analysis-input/v2"}, "ruleSetVersions": []string{"b42-rules/v1", "b43-rules/v1", "diagnostics-v1"}, "kinds": []string{"diff", "impact", "change_package", "conformance", "endpoint_review", "diagnostics"}, "targets": []string{"revisionId", "proposal", "changeProposal", "commandPreview"}, "observationModes": []string{"none"}, "ruleSetVersion": "b42-rules/v1", "traversalVersion": "b42-traversal/v1"},
		"providerProfiles":         []string{backendmodel.GraphProfile, backendmodel.RelationalProfile, backendmodel.RuntimeProfile, backendmodel.LineageProfile, backendmodel.EventsProfile, backendmodel.ComposedProfile},
		"supportedNodeKinds":       backendmodel.SupportedNodeKindsForProfile(backendmodel.ComposedProfile),
		"supportedEdgeKinds":       backendmodel.SupportedEdgeKindsForProfile(backendmodel.ComposedProfile),
		"guideSetId":               guide.CurrentGuideSetID(),
		"viewSchemaVersions":       []string{backendmodel.ProposalDocumentVersion, backendmodel.SavedViewDocumentVersion, "api-artifact-pins-v1", backendmodel.EditorArtifactDocumentVersion, backendmodel.ChangeProposalDocumentVersion, "import-candidate-v1", backendmodel.SavedViewV2DocumentVersion, "backend-diagram-v1", "diagram-view-v1", "artifact-context-v3", "backend-portable-v1", "backend-replay-v1", "backend-observations-v1"},
		"proposalDocumentVersions": []string{backendmodel.ProposalDocumentVersion, backendmodel.ChangeProposalDocumentVersion},
		"changeProposalCommands":   backendChangeProposalCommands(),
		"sourceScopes":             []string{"add_repository", "reconcile", "add_provider", "migrate_provider"},
		"syncPolicies":             []string{backendmodel.WholeSourcePolicy, backendmodel.IncrementalSourcePolicy},
		"readTargetSupport":        backendReadTargetSupport(),
		"proposalCommands":         []map[string]string{{"type": "alter_column", "property": "nullable"}, {"type": "alter_constraint", "action": "create", "constraintKind": "foreign_key"}, {"type": "alter_constraint", "action": "update", "constraintKind": "foreign_key"}, {"type": "set_criteria"}},
		"importModes":              []string{"initial", "reconcile", "composed"},
		"importCommands":           []string{"upsert_node", "upsert_edge", "upsert_evidence", "remove", "map_identity", "delete_assertion", "claim_identity", "resolve_assertion"},
		"reconciliationProfile":    map[string]string{"version": "1", "profile": backendmodel.GraphProfile, "scope": "whole-repository"},
		"profileCapabilities": []map[string]any{
			{"profile": backendmodel.GraphProfile, "modelSchemaVersions": []string{backendmodel.SchemaVersion}, "nodeKinds": backendmodel.SupportedNodeKindsForProfile(backendmodel.GraphProfile), "edgeKinds": backendmodel.SupportedEdgeKindsForProfile(backendmodel.GraphProfile), "importModes": []string{"initial", "reconcile"}},
			{"profile": backendmodel.RelationalProfile, "modelSchemaVersions": []string{backendmodel.RelationalSchemaVersion}, "nodeKinds": backendmodel.SupportedNodeKindsForProfile(backendmodel.RelationalProfile), "edgeKinds": backendmodel.SupportedEdgeKindsForProfile(backendmodel.RelationalProfile), "importModes": []string{"initial", "reconcile"}},
			{"profile": backendmodel.RuntimeProfile, "modelSchemaVersions": []string{backendmodel.RuntimeSchemaVersion}, "nodeKinds": backendmodel.SupportedNodeKindsForProfile(backendmodel.RuntimeProfile), "edgeKinds": backendmodel.SupportedEdgeKindsForProfile(backendmodel.RuntimeProfile), "importModes": []string{"initial", "reconcile"}},
			{"profile": backendmodel.LineageProfile, "modelSchemaVersions": []string{backendmodel.LineageSchemaVersion}, "nodeKinds": backendmodel.SupportedNodeKindsForProfile(backendmodel.LineageProfile), "edgeKinds": backendmodel.SupportedEdgeKindsForProfile(backendmodel.LineageProfile), "importModes": []string{"initial", "reconcile"}},
			{"profile": backendmodel.EventsProfile, "modelSchemaVersions": []string{backendmodel.EventsSchemaVersion}, "nodeKinds": backendmodel.SupportedNodeKindsForProfile(backendmodel.EventsProfile), "edgeKinds": backendmodel.SupportedEdgeKindsForProfile(backendmodel.EventsProfile), "importModes": []string{"initial", "reconcile"}},
			{"profile": backendmodel.ComposedProfile, "modelSchemaVersions": []string{backendmodel.ComposedSchemaVersion}, "nodeKinds": backendmodel.SupportedNodeKindsForProfile(backendmodel.ComposedProfile), "edgeKinds": backendmodel.SupportedEdgeKindsForProfile(backendmodel.ComposedProfile), "importModes": []string{"composed"}},
		},
		"profileExtensions": []backendmodel.ImportProfileExtension{{FromProfile: backendmodel.GraphProfile, ToProfile: backendmodel.RelationalProfile}, {FromProfile: backendmodel.RelationalProfile, ToProfile: backendmodel.RuntimeProfile}, {FromProfile: backendmodel.RuntimeProfile, ToProfile: backendmodel.LineageProfile}, {FromProfile: backendmodel.LineageProfile, ToProfile: backendmodel.EventsProfile}, {FromProfile: backendmodel.EventsProfile, ToProfile: backendmodel.ComposedProfile}},
		"reconciliationProfiles": []map[string]string{
			{"version": "1", "profile": backendmodel.GraphProfile, "scope": "whole-repository"},
			{"version": "1", "profile": backendmodel.RelationalProfile, "scope": "whole-repository-combined-graph"},
			{"version": "1", "profile": backendmodel.RuntimeProfile, "scope": "whole-repository-combined-graph"},
			{"version": "1", "profile": backendmodel.LineageProfile, "scope": "whole-repository-combined-graph"},
			{"version": "1", "profile": backendmodel.EventsProfile, "scope": "whole-repository-combined-graph"},
			{"version": "1", "profile": backendmodel.ComposedProfile, "scope": "provider-partition"},
		},
		"comparisonVersion": int64(1),
		"limits": map[string]any{"maxNameLength": backendmodel.MaxNameLength, "maxIdempotencyKeyLength": backendmodel.MaxKeyLength, "defaultPageSize": backendmodel.DefaultPageSize, "maxPageSize": backendmodel.MaxPageSize, "maxCommands": backendmodel.MaxProjectCommands, "maxBodyBytes": s.cfg.MaxBody,
			"maxDiagramDocumentBytes": min(s.cfg.MaxBody, int64(1<<20)), "maxDiagramElements": 1000, "maxDiagramLinks": 3000, "maxDiagrams": 1000, "maxDiagramVersions": 1000, "maxDiagramProjectBytes": 256 << 20, "maxDiagramViewBytes": 128 << 10, "maxDiagramViews": 1000, "maxDiagramViewVersions": 1000, "maxDiagramViewProjectBytes": 64 << 20,
			"maxProjectCommandBytes":    min(s.cfg.MaxBody, int64(backendmodel.MaxProjectCommandBytes)),
			"maxAnnotationBodyBytes":    min(s.cfg.MaxBody, int64(backendmodel.MaxAnnotationBodyBytes)),
			"maxAnnotationIdentities":   backendmodel.MaxAnnotationIdentities,
			"maxAnnotationTextBytes":    backendmodel.MaxAnnotationTextBytes,
			"defaultAnnotationPageSize": backendmodel.DefaultAnnotationPageSize,
			"maxAnnotationPageSize":     backendmodel.MaxAnnotationPageSize,
			"maxSourceRepositories":     backendmodel.MaxSourceRepositories,
			"maxSourceProviders":        backendmodel.MaxSourceProviders,
			"maxIncrementalSubjects":    backendmodel.MaxIncrementalSubjects,
			"maxChangeProposalCommands": backendmodel.MaxChangeProposalCommands,
			"maxAnalysisInputBytes":     min(s.cfg.MaxBody, int64(2<<20)), "maxAnalysisResultBytes": int64(32 << 20), "maxAnalysisProjectBytes": int64(256 << 20), "maxAnalysisJobs": int64(1000), "maxAnalysisWaitingJobs": int64(20), "maxAnalysisRunningJobs": int64(2), "maxAnalysisStates": int64(10000), "maxAnalysisDependencyVisits": int64(50000), "maxAnalysisDepth": int64(32), "maxAnalysisFindings": int64(10000), "maxAnalysisRecords": int64(20000), "maxAnalysisWitnessesPerObject": int64(8), "recommendedAnalysisPollIntervalMs": int64(2000),
			"maxChangeProposalCommandBytes": min(s.cfg.MaxBody, int64(backendmodel.MaxChangeProposalCommandBytes)),
			"maxChangeProposalCriteria":     backendmodel.MaxChangeProposalCriteria,
			"maxChangeProposals":            backendmodel.MaxChangeProposals,
			"maxChangeProposalRevisions":    backendmodel.MaxChangeProposalRevisions,
			"maxChangeProposalBytes":        backendmodel.MaxChangeProposalBytes,
			"maxMaterializationTargets":     5, "maxMaterializationCommands": 100, "maxMaterializationBytes": 1048576, "maxDiagramSVGBytes": 2097152,
			"maxEditorArtifactContextBytes": backendmodel.MaxEditorArtifactContextBytes, "maxEditorEventConstructionBytes": backendmodel.MaxEditorEventConstructionBytes, "maxEventMapBytes": designscenario.MaxEventMapBytes,
			"maxImportBatchCommands": backendmodel.MaxImportCommands, "maxImportBatchBytes": min(s.cfg.MaxBody, int64(backendmodel.MaxImportBatchBytes)),
			"maxManifestFiles": backendmodel.MaxManifestFiles, "maxSnippetBytes": backendmodel.MaxEvidenceSnippetBytes,
			"maxRevisionNodes": backendmodel.MaxRevisionNodes, "maxRevisionEdges": backendmodel.MaxRevisionEdges, "maxRevisionEvidence": backendmodel.MaxRevisionEvidence,
			"maxRevisionPayloadBytes": backendmodel.MaxRevisionBytes, "maxProjectStagingBytes": backendmodel.MaxProjectStagingBytes,
			"maxOpenImportSessions": backendmodel.MaxOpenImportSessions, "defaultGraphPageSize": backendmodel.DefaultGraphPageSize, "maxGraphPageSize": backendmodel.MaxGraphPageSize,
			"maxRelationalFacets": backendmodel.MaxRelationalFacets, "maxRelationalOrderedColumns": backendmodel.MaxRelationalOrderedColumns, "maxRelationalIndexTerms": backendmodel.MaxRelationalIndexTerms, "maxRelationalNativeBytes": backendmodel.MaxRelationalNativeBytes, "maxRelationalReferences": backendmodel.MaxRelationalReferences, "defaultDatabasePageSize": backendmodel.DefaultGraphPageSize, "maxDatabasePageSize": backendmodel.MaxGraphPageSize,
			"maxRuntimeNativeBytes": backendmodel.MaxRuntimeNativeBytes, "maxRuntimePorts": backendmodel.MaxRuntimePorts, "maxRuntimeExitReferences": backendmodel.MaxRuntimeExitReferences, "maxRuntimeCallCandidates": backendmodel.MaxRuntimeCallCandidates, "maxRuntimeTextBytes": backendmodel.MaxRuntimeTextBytes,
			"maxFlowCallHops": 32, "maxFlowTraversalStates": 5000, "maxFlowExaminedEdges": 20000, "maxFlowAccessPairs": 5000, "maxFlowWitnessEdges": 256, "defaultFlowPageSize": backendmodel.DefaultGraphPageSize, "maxFlowPageSize": backendmodel.MaxGraphPageSize,
			"maxLineageSources": backendmodel.MaxLineageSources, "maxLineageReferences": backendmodel.MaxLineageReferences, "maxAPIFieldsPerOperation": backendmodel.MaxAPIFieldsPerOperation,
			"defaultLineageDepth": 8, "maxLineageDepth": 32, "defaultLineagePageSize": 50, "maxLineagePageSize": 100, "maxLineageVisitedValues": 5000, "maxLineageExaminedMappings": 5000, "maxLineageReferenceIncidences": 20000,
			"maxEventFieldsPerMessage": backendmodel.MaxEventFieldsPerMessage, "maxEventHandlers": backendmodel.MaxEventHandlers,
			"maxEventsExaminedEdges": backendmodel.EventsMaxExaminedEdges, "maxEventsItems": backendmodel.EventsMaxItems, "maxEventsAuxiliaryRecords": backendmodel.EventsMaxAuxiliaryRecords, "maxEventsWitnessRecords": backendmodel.EventsMaxWitnessRecords, "defaultEventsPageSize": 50, "maxEventsPageSize": 100,
			"maxProposalCommands":                  backendmodel.MaxProposalCommands,
			"maxProposalBatchBytes":                min(s.cfg.MaxBody, int64(backendmodel.MaxProposalCommandBytes)),
			"maxProposalAuthoredCriteria":          backendmodel.MaxProposalCriteria,
			"maxProposalReasonBytes":               backendmodel.MaxProposalTextBytes,
			"maxProposalCriterionDescriptionBytes": backendmodel.MaxProposalTextBytes},
	})
}

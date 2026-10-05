package mcp

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yashok111/mocker/api"
	"github.com/yashok111/mocker/internal/backendanalysis"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/testauth"
)

// Exercises the actual SDK, importer, worker and lifecycle; no source is executed.
func TestBackendB43SDKPublicWorkflowExample(t *testing.T) {
	h := newB41SDKExamples(t)
	b43SaveTranscript(t, h)
	_, node := h.importHandler(t, "b43", "static", "package example\n\nfunc Handle() {}\n", "handler", "Handle")
	base := h.project.CurrentRevisionID
	var draft backendmodel.ChangeProposalDetail
	h.call(t, "create_backend_change_proposal", backendmodel.CreateChangeProposalInput{Name: "Structural handoff", BaseRevisionID: base, IdempotencyKey: h.key("draft")}, nil, &draft)
	criteria := []backendmodel.ChangeCriterion{{Key: "exists", Kind: "object_exists", Required: true, Description: "Pinned handler exists", RecordType: "node", ID: node, ObjectKind: "handler"}, {Key: "runtime", Kind: "runtime_check", Required: false, Description: "Observe separately", TargetIDs: []string{node}}}
	h.apply(t, &draft, []backendmodel.ChangeProposalCommand{{Type: "set_criteria", CommandID: uuid.NewV7().String(), Reason: "Acceptance contract", Criteria: criteria}})
	service := b42ExampleService(h)
	if err := service.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	stop := b42RunExampleWorker(t, service)
	exact := backendmodel.ProposalReadTarget{ProposalID: draft.Proposal.ID, ProposalRevisionID: draft.Revision.ID}
	var conformance backendanalysis.Job
	for _, kind := range []string{"change_package", "conformance"} {
		input := map[string]any{"kind": kind, "changeProposal": exact, "limits": map[string]any{}, "observationMode": "none", "idempotencyKey": h.key(kind)}
		if kind == "conformance" {
			input["resultRevisionId"] = base
			input["identityMap"] = []any{}
			input["testAttachments"] = []any{}
		}
		var job backendanalysis.Job
		receipt := h.call(t, "start_backend_analysis", input, nil, &job)
		job = b43PollExample(t, h, job.ID)
		h.replay(t, receipt)
		for _, section := range []string{"changes", "findings", "witnesses", "checks", "gaps"} {
			b42ReadExamplePages(t, h, job, section)
		}
		if kind == "conformance" {
			conformance = job
		}
	}
	manifest := b42ReadExamplePages(t, h, conformance, "checks")
	owner := map[string]any{"proposalId": draft.Proposal.ID}
	implemented := backendmodel.ApplyChangeProposalLifecycleInput{ExpectedVersion: draft.Proposal.Version, ProposalRevisionID: draft.Revision.ID, Action: "implemented", IdempotencyKey: h.key("implemented"), ResultRevisionID: base, Report: backendmodel.AnalysisReportRef{JobID: conformance.ID, ResultVersion: *conformance.ResultVersion, InputHash: conformance.AnalysisInputHash, ResultHash: manifest.SemanticResultHash}, Exceptions: []backendmodel.ChangeProposalException{{CriterionKey: "runtime", Author: "Reviewer", Reason: "Static verification only"}}}
	// Ready remains a separate impact-report gate.
	h.reject(t, "apply_backend_change_proposal_lifecycle", implemented, owner, "409")
	var impact backendanalysis.Job
	h.call(t, "start_backend_analysis", map[string]any{"kind": "impact", "fromRevisionId": base, "target": map[string]any{"changeProposal": exact}, "scope": map[string]any{}, "limits": map[string]any{}, "observationMode": "none", "idempotencyKey": h.key("impact")}, nil, &impact)
	impact = b42PollExample(t, h, impact.ID)
	readyManifest := b42ReadExamplePages(t, h, impact, "checks")
	gaps := make([]string, 0, len(readyManifest.Gaps))
	for _, gap := range readyManifest.Gaps {
		gaps = append(gaps, gap.ID)
	}
	var applied backendmodel.ChangeProposalApplyResult
	h.call(t, "apply_backend_change_proposal_lifecycle", backendmodel.ApplyChangeProposalLifecycleInput{ExpectedVersion: draft.Proposal.Version, ProposalRevisionID: draft.Revision.ID, Action: "ready", IdempotencyKey: h.key("ready"), Report: backendmodel.AnalysisReportRef{JobID: impact.ID, ResultVersion: *impact.ResultVersion, InputHash: impact.AnalysisInputHash, ResultHash: readyManifest.SemanticResultHash}, AcknowledgedGapIDs: gaps}, owner, &applied)
	implemented.ExpectedVersion = applied.Proposal.Version
	receipt := h.call(t, "apply_backend_change_proposal_lifecycle", implemented, owner, &applied)
	if applied.Proposal.Status != "implemented" || applied.Proposal.ImplementedReference == nil || applied.Proposal.ImplementedReference.BehaviorStatus != "unverified" {
		t.Fatal(applied)
	}
	for _, action := range []string{"archive", "unarchive"} {
		version := applied.Proposal.Version
		applied = backendmodel.ChangeProposalApplyResult{}
		h.call(t, "apply_backend_change_proposal_lifecycle", backendmodel.ApplyChangeProposalLifecycleInput{ExpectedVersion: version, ProposalRevisionID: draft.Revision.ID, Action: action, IdempotencyKey: h.key(action)}, owner, &applied)
		if action == "archive" && (applied.Proposal.Status != "archived" || applied.Proposal.ImplementedReference == nil) {
			t.Fatal(applied)
		}
	}
	if applied.Proposal.Status != "draft" || applied.Proposal.ReadyReference != nil || applied.Proposal.ImplementedReference != nil {
		t.Fatal(applied)
	}
	h.replay(t, receipt)
	b43ValidateTranscript(t, h)
	stop()
}

func b43PollExample(t *testing.T, h *b41SDKExamples, id string) backendanalysis.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var detail struct {
			Job   backendanalysis.Job            `json:"job"`
			Input backendanalysis.InputContextV2 `json:"input"`
		}
		h.call(t, "get_backend_analysis", nil, map[string]any{"jobId": id}, &detail)
		if detail.Input.DocumentVersion != "backend-analysis-context-v2" || detail.Input.ObservationMode != "none" {
			t.Fatal(detail)
		}
		switch detail.Job.Status {
		case "queued", "running":
			time.Sleep(5 * time.Millisecond)
		case "completed":
			return detail.Job
		default:
			raw, _ := json.Marshal(detail)
			t.Fatal(string(raw))
		}
	}
	t.Fatal("analysis polling timed out")
	return backendanalysis.Job{}
}

func TestBackendB43SDKEndpointReviewExample(t *testing.T) {
	h := newB41SDKExamples(t)
	b43SaveTranscript(t, h)
	sourceInput := b41SourceInput(h.project, "endpoint", "static", "package example\n\nfunc Cancel() {}\n", true)
	for i := range sourceInput.Inventory {
		if sourceInput.Inventory[i].Category == "endpoints" {
			sourceInput.Inventory[i].KnownCount = 1
			sourceInput.Inventory[i].Denominator = new(int64(1))
		}
	}
	session := h.begin(t, sourceInput)
	commands := b41NodeCommands(session, b41DeclaredNode{"handler", "Cancel", 3})
	commands = append(commands,
		backendmodel.ImportCommand{Op: "upsert_node", Node: &backendmodel.ImportNode{ExternalKey: "endpoint", Kind: "http_operation", Name: "Cancel", Attributes: map[string]jsontext.Value{"method": []byte(`"POST"`), "path": []byte(`"/orders/{id}/cancel"`)}, EvidenceKeys: []string{"endpoint-proof"}}},
		backendmodel.ImportCommand{Op: "upsert_edge", Edge: &backendmodel.ImportEdge{ExternalKey: "handles", Kind: "handles", FromRef: &backendmodel.ImportRecordRef{LocalKey: "endpoint"}, ToRef: &backendmodel.ImportRecordRef{LocalKey: "handler"}, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"handles-proof"}}},
		backendmodel.ImportCommand{Op: "upsert_edge", Edge: &backendmodel.ImportEdge{ExternalKey: "error", Kind: "error", FromRef: &backendmodel.ImportRecordRef{LocalKey: "step"}, ToRef: &backendmodel.ImportRecordRef{LocalKey: "step"}, Attributes: map[string]jsontext.Value{"label": []byte(`"failure"`), "outcome": []byte(`"error"`)}, EvidenceKeys: []string{"error-proof"}}},
	)

	attrs := func(raw string) map[string]jsontext.Value {
		var out map[string]jsontext.Value
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	commands = append(commands,
		backendmodel.ImportCommand{Op: "upsert_node", Node: &backendmodel.ImportNode{ExternalKey: "flow", Kind: "flow", Name: "Cancel flow", ParentRef: &backendmodel.ImportRecordRef{LocalKey: "handler"}, Attributes: attrs(`{"analysisStatus":"partial","gaps":["Only input loop declared"],"entryStepRef":{"localKey":"step"},"exitStepRefs":[],"exitStatus":"unknown"}`), EvidenceKeys: []string{"flow-proof"}}},
		backendmodel.ImportCommand{Op: "upsert_node", Node: &backendmodel.ImportNode{ExternalKey: "step", Kind: "flow_step", Name: "Input", ParentRef: &backendmodel.ImportRecordRef{LocalKey: "flow"}, Attributes: attrs(`{"analysisStatus":"complete","gaps":[],"stepKind":"input","inputs":[],"outputs":[],"transactionContext":{"status":"none","reason":"No transaction"},"nativeText":"Cancel()"}`), EvidenceKeys: []string{"step-proof"}}},
		backendmodel.ImportCommand{Op: "upsert_edge", Edge: &backendmodel.ImportEdge{ExternalKey: "contains-flow", Kind: "contains", FromRef: &backendmodel.ImportRecordRef{LocalKey: "handler"}, ToRef: &backendmodel.ImportRecordRef{LocalKey: "flow"}, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"contains-flow-proof"}}},
		backendmodel.ImportCommand{Op: "upsert_edge", Edge: &backendmodel.ImportEdge{ExternalKey: "contains-step", Kind: "contains", FromRef: &backendmodel.ImportRecordRef{LocalKey: "flow"}, ToRef: &backendmodel.ImportRecordRef{LocalKey: "step"}, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"contains-step-proof"}}},
	)
	for _, subject := range []struct{ kind, key string }{{"node", "flow"}, {"node", "step"}, {"edge", "contains-flow"}, {"edge", "contains-step"}, {"node", "endpoint"}, {"edge", "handles"}, {"edge", "error"}} {
		commands = append(commands, backendmodel.ImportCommand{Op: "upsert_evidence", Evidence: &backendmodel.ImportEvidence{ExternalKey: subject.key + "-proof", SubjectType: subject.kind, SubjectKey: subject.key, Method: "ast", Status: "explicit", Source: backendmodel.EvidenceSource{RepositoryID: session.RepositoryID, SnapshotID: session.SnapshotID, File: "source.go", ContentHash: session.Manifest.Snapshot.Files[0].ContentHash, StartLine: new(int64(3)), EndLine: new(int64(3))}}})
	}
	batch := h.batch(t, &session, commands)
	h.commit(t, &session, h.preview(t, &session))
	endpoint := b41Identity(t, batch, "node", "endpoint")
	base := h.project.CurrentRevisionID
	service := b42ExampleService(h)
	if err := service.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	b42RunExampleWorker(t, service)
	rest := b42ExampleREST(t, h)
	for _, after := range []*string{new(endpoint), nil} {
		input := backendanalysis.StartEndpointReviewInput{Kind: "endpoint_review", Limits: backendanalysis.Limits{}, ObservationMode: "none", ObservationPins: []jsontext.Value{}, IdempotencyKey: h.key("endpoint"), FromRevisionID: base, ToRevisionID: base, BeforeEndpointID: endpoint, AfterEndpointID: after}
		var job backendanalysis.Job
		raw := rest("POST", "/api/backend-projects/"+h.project.ID+"/analyses", input, 202, &job)
		again := rest("POST", "/api/backend-projects/"+h.project.ID+"/analyses", input, 202, nil)
		if !bytes.Equal(raw, again) {
			t.Fatal("REST 202 replay changed")
		}
		job = b43PollExample(t, h, job.ID)
		var page struct {
			Items      []backendanalysis.ResultRecord `json:"items"`
			NextCursor string                         `json:"nextCursor"`
		}
		h.call(t, "get_backend_analysis_results", nil, map[string]any{"jobId": job.ID, "resultVersion": *job.ResultVersion, "section": "findings", "limit": 1}, &page)
		if len(page.Items) == 0 {
			t.Fatal("missing endpoint witness")
		}
		var detail backendanalysis.EndpointItemDetail
		if err := json.Unmarshal(page.Items[0].Detail, &detail); err != nil {
			t.Fatal(err)
		}
		if detail.Type != "endpoint_item" || detail.Category != "error_branch" || detail.BehaviorStatus != "unverified" || detail.Side != detail.Witness.Side {
			t.Fatal(detail)
		}
		if after == nil && detail.Side != "before" {
			t.Fatal("removal lost before-side")
		}
		if page.NextCursor != "" {
			h.reject(t, "get_backend_analysis_results", nil, map[string]any{"jobId": job.ID, "resultVersion": *job.ResultVersion, "section": "findings", "limit": 2, "cursor": page.NextCursor}, "400")
		}
		h.reject(t, "get_backend_analysis_results", nil, map[string]any{"jobId": job.ID, "resultVersion": *job.ResultVersion + 1, "section": "findings"}, "404")
		for _, section := range []string{"changes", "findings", "witnesses", "checks", "gaps"} {
			b42ReadExamplePages(t, h, job, section)
		}
	}
	b43ValidateTranscript(t, h)
}

func b43ValidateTranscript(t *testing.T, h *b41SDKExamples) {
	t.Helper()
	names := map[string]string{"start_backend_analysis": "BackendAnalysisJob", "get_backend_analysis": "BackendAnalysisJobDetail", "get_backend_analysis_results": "BackendAnalysisResultPage", "apply_backend_change_proposal_lifecycle": "BackendChangeProposalApplyResult"}
	validators := map[string]*jsonschema.Schema{}
	for _, entry := range h.transcript {
		name := names[entry.Tool]
		if name == "" || entry.Error != "" {
			continue
		}
		validator := validators[name]
		if validator == nil {
			schema, err := api.BackendSchema(name)
			if err != nil {
				t.Fatal(err)
			}
			compiler := jsonschema.NewCompiler()
			if err := compiler.AddResource("https://mocker.invalid/b43", schema); err != nil {
				t.Fatal(err)
			}
			validator, err = compiler.Compile("https://mocker.invalid/b43")
			if err != nil {
				t.Fatal(err)
			}
			validators[name] = validator
		}
		decoder := jsonx.NewDecoder(strings.NewReader(entry.Response))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		if err := validator.Validate(value); err != nil {
			t.Fatalf("%s: %v", entry.Tool, err)
		}
	}
}

func TestBackendB43RESTAuthCSRFAndProjectIsolation(t *testing.T) {
	h := newB41SDKExamples(t)
	b43SaveTranscript(t, h)
	_, _ = h.importHandler(t, "isolation", "static", "package example\n\nfunc Handle() {}\n", "handler", "Handle")
	var draft backendmodel.ChangeProposalDetail
	h.call(t, "create_backend_change_proposal", backendmodel.CreateChangeProposalInput{Name: "Owned", BaseRevisionID: h.project.CurrentRevisionID, IdempotencyKey: h.key("draft")}, nil, &draft)
	b42ExampleService(h)
	input := map[string]any{"kind": "change_package", "changeProposal": backendmodel.ProposalReadTarget{ProposalID: draft.Proposal.ID, ProposalRevisionID: draft.Revision.ID}, "limits": map[string]any{}, "observationMode": "none", "idempotencyKey": "auth"}
	path := "/api/backend-projects/" + h.project.ID + "/analyses"
	handler := h.server.Handler()
	req := httptest.NewRequest(http.MethodPost, "http://mocker.local"+path, bytes.NewReader(b41JSON(t, input)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://mocker.local")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != 401 {
		t.Fatalf("unauthorized=%d", response.Code)
	}
	login := httptest.NewRequest(http.MethodPost, "http://mocker.local/api/auth/login", bytes.NewReader(b41JSON(t, map[string]string{"name": "B43 auth", "password": testauth.Password})))
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Origin", "http://mocker.local")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, login)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	cookies := response.Result().Cookies()
	req = httptest.NewRequest(http.MethodPost, "http://mocker.local"+path, bytes.NewReader(b41JSON(t, input)))
	req.AddCookie(cookies[0])
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://mocker.local")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != 403 {
		t.Fatalf("missing CSRF=%d", response.Code)
	}
	rest := b42ExampleREST(t, h)
	var job backendanalysis.Job
	rest("POST", path, input, 202, &job)
	var foreign backendmodel.Project
	h.call(t, "create_backend_project", backendmodel.CreateInput{Name: "Foreign", IdempotencyKey: h.key("foreign")}, nil, &foreign)
	rest("POST", "/api/backend-projects/"+foreign.ID+"/analyses", input, 404, nil)
	rest("GET", "/api/backend-projects/"+foreign.ID+"/analyses/"+job.ID, nil, 404, nil)
	h.reject(t, "get_backend_analysis", nil, map[string]any{"projectId": foreign.ID, "jobId": job.ID}, "404")
}

func b43SaveTranscript(t *testing.T, h *b41SDKExamples) {
	t.Helper()
	directory := os.Getenv("B43_SDK_ARTIFACT_DIR")
	if directory == "" {
		return
	}
	t.Cleanup(func() {
		if err := os.WriteFile(filepath.Join(directory, t.Name()+".json"), b41JSON(t, h.transcript), 0600); err != nil {
			t.Error(err)
		}
	})
}

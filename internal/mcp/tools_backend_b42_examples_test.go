package mcp

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/backendanalysis"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/testauth"
)

// The example imports inert declarations, then uses the real SDK, authenticated
// REST handlers and worker. Only process startup/shutdown uses internal methods.
// It is an executable developer example, not live-agent acceptance.
func TestBackendB42SDKPublicWorkflowExample(t *testing.T) {
	h := newB41SDKExamples(t)
	source, node := h.importHandler(t, "example", "static", "package example\n\nfunc Before() {}\n", "handler", "Before")
	base := h.project.CurrentRevisionID
	var draft backendmodel.ChangeProposalDetail
	h.call(t, "create_backend_change_proposal", backendmodel.CreateChangeProposalInput{Name: "Reviewed desired change", BaseRevisionID: base, IdempotencyKey: h.key("proposal")}, nil, &draft)
	h.apply(t, &draft, []backendmodel.ChangeProposalCommand{b41Rename(node, "Desired")})
	oldDraft := draft.Revision.ID
	oldSource := h.call(t, "get_backend_node", map[string]any{"revisionId": base, "nodeId": node}, nil, nil)
	oldDesired := h.call(t, "get_backend_node", b41FullTarget(draft), map[string]any{"nodeId": node}, nil)

	// Advance only this source partition through the public import protocol.
	next := b41SourceInput(h.project, "example", "static", "package example\n\nfunc NewSource() {}\n", true)
	next.SourceScope = &backendmodel.SourceScope{Kind: "reconcile", RepositoryID: source.RepositoryID, ProviderNamespace: "static"}
	session := h.begin(t, next)
	h.batch(t, &session, b41NodeCommands(session, b41DeclaredNode{"handler", "NewSource", 3}))
	h.commit(t, &session, h.preview(t, &session))
	newBase := h.project.CurrentRevisionID
	owner := map[string]any{"proposalId": draft.Proposal.ID}
	rebase := backendmodel.PreviewChangeProposalRebaseInput{ExpectedVersion: draft.Proposal.Version, ProposalRevisionID: oldDraft, NewBaseRevisionID: newBase, IdentityResolutions: []backendmodel.ChangeRebaseIdentityResolution{}, Resolutions: []backendmodel.ChangeRebaseResolution{}, RepairCommands: []backendmodel.ChangeProposalCommand{}}
	var candidate backendmodel.ChangeProposalRebaseCandidate
	h.call(t, "preview_backend_change_proposal_rebase", rebase, owner, &candidate)
	if len(candidate.Conflicts) != 1 || candidate.CandidateHash != nil {
		t.Fatalf("expected unresolved B/O/N name conflict: %+v", candidate)
	}
	conflict := candidate.Conflicts[0]
	for _, value := range []struct {
		actual backendmodel.ChangeRebaseValue
		want   string
	}{{conflict.Base, "Before"}, {conflict.Ours, "Desired"}, {conflict.NewSource, "NewSource"}} {
		if value.actual.Presence != "value" || !bytes.Contains(value.actual.Value, []byte(value.want)) {
			t.Fatalf("B/O/N value=%+v want %s", value.actual, value.want)
		}
	}
	rebase.Resolutions = []backendmodel.ChangeRebaseResolution{{ConflictID: conflict.ID, Choice: "keep_proposal", Reason: "The explicitly reviewed desired name must survive source advancement"}}
	h.call(t, "preview_backend_change_proposal_rebase", rebase, owner, &candidate)
	if candidate.CandidateHash == nil {
		t.Fatalf("resolved candidate: %+v", candidate)
	}
	var rebased backendmodel.ChangeProposalApplyResult
	rebaseReceipt := h.call(t, "apply_backend_change_proposal_rebase", backendmodel.ApplyChangeProposalRebaseInput{PreviewChangeProposalRebaseInput: rebase, CandidateHash: *candidate.CandidateHash, IdempotencyKey: h.key("rebase")}, owner, &rebased)
	if rebased.Revision.BaseRevisionID != newBase || rebased.Revision.ID == oldDraft || rebased.Proposal.Status != "draft" || rebased.Revision.Rebase == nil {
		t.Fatal(rebased)
	}
	draft.Proposal, draft.Revision = rebased.Proposal, rebased.Revision
	h.replay(t, oldSource)
	h.replay(t, oldDesired)

	service := b42ExampleService(h)
	// REST Start and MCP cancellation act on the same persisted job.
	rest := b42ExampleREST(t, h)
	startInput := map[string]any{"kind": "diff", "fromRevisionId": base, "target": map[string]string{"revisionId": newBase}, "scope": map[string]any{}, "limits": map[string]any{}, "observationMode": "none", "idempotencyKey": h.key("source-diff")}
	var cancelled backendanalysis.Job
	startBytes := rest("POST", "/api/backend-projects/"+h.project.ID+"/analyses", startInput, http.StatusAccepted, &cancelled)
	if cancelled.Status != "queued" || cancelled.RecommendedPollIntervalMs != 2000 {
		t.Fatal(cancelled)
	}
	cancelReceipt := h.call(t, "cancel_backend_analysis", backendanalysis.CancelInput{IdempotencyKey: h.key("cancel")}, map[string]any{"jobId": cancelled.ID}, &cancelled)
	if cancelled.Status != "cancelled" {
		t.Fatal(cancelled)
	}
	h.reject(t, "cancel_backend_analysis", backendanalysis.CancelInput{IdempotencyKey: h.key("cancel-again")}, map[string]any{"jobId": cancelled.ID}, "backend_analysis_terminal_conflict")
	if got := rest("POST", "/api/backend-projects/"+h.project.ID+"/analyses", startInput, http.StatusAccepted, nil); !bytes.Equal(got, startBytes) {
		t.Fatal("REST Start replay changed after cancellation")
	}

	// Close the database with an admitted queued job; startup must interrupt it.
	var interrupted backendanalysis.Job
	startInput["idempotencyKey"] = h.key("interrupt")
	startReceipt := h.call(t, "start_backend_analysis", startInput, nil, &interrupted)
	if err := service.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := h.db.Close(); err != nil {
		t.Fatal(err)
	}
	h.server, h.db = newResourcesTestServer(t, h.cfg)
	h.tools = newToolFixture(h.server)
	service = b42ExampleService(h)
	if err := service.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	var detail b42ExampleDetail
	h.call(t, "get_backend_analysis", nil, map[string]any{"jobId": interrupted.ID}, &detail)
	if detail.Job.Status != "interrupted" || detail.Job.ResultVersion == nil || detail.Input.FromRevisionID != base || detail.Input.Target.RevisionID != newBase {
		t.Fatalf("startup recovery: %+v", detail)
	}
	h.replay(t, startReceipt)
	h.replay(t, cancelReceipt)
	h.replay(t, rebaseReceipt)
	frozenInterrupted := h.call(t, "get_backend_analysis_results", nil, map[string]any{"jobId": interrupted.ID, "resultVersion": *detail.Job.ResultVersion, "section": "changes"}, nil)
	var retry backendanalysis.Job
	retryReceipt := h.call(t, "retry_backend_analysis", backendanalysis.RetryInput{IdempotencyKey: h.key("retry")}, map[string]any{"jobId": interrupted.ID}, &retry)
	if retry.ID == interrupted.ID || retry.AnalysisInputHash != interrupted.AnalysisInputHash || retry.Status != "queued" {
		t.Fatal(retry)
	}
	h.replay(t, retryReceipt)
	h.reject(t, "retry_backend_analysis", backendanalysis.RetryInput{IdempotencyKey: h.key("retry-running")}, map[string]any{"jobId": retry.ID}, "backend_analysis_not_terminal")
	stop := b42RunExampleWorker(t, service)
	completed := b42PollExample(t, h, retry.ID)
	if completed.ResultVersion == nil {
		t.Fatal(completed)
	}
	sourcePage := b42ReadExamplePages(t, h, completed, "changes")
	if sourcePage.RuntimeVerified || !sourcePage.Complete {
		t.Fatal(sourcePage)
	}
	h.replay(t, frozenInterrupted)
	h.call(t, "get_backend_analysis", nil, map[string]any{"jobId": interrupted.ID}, &detail)
	if detail.Job.Status != "interrupted" {
		t.Fatal("retry rewrote original job")
	}

	// Only the exact saved full-draft impact may qualify the ready association.
	var impact backendanalysis.Job
	h.call(t, "start_backend_analysis", map[string]any{"kind": "impact", "fromRevisionId": newBase, "target": map[string]any{"changeProposal": b41FullTarget(draft).ChangeProposal}, "scope": map[string]any{}, "limits": map[string]any{}, "observationMode": "none", "idempotencyKey": h.key("impact")}, nil, &impact)
	impact = b42PollExample(t, h, impact.ID)
	report := b42ReadExamplePages(t, h, impact, "changes")
	if report.RuntimeVerified || !report.Complete || len(report.ChangedIDs) == 0 || !slices.Equal(report.ChangedIDs, report.CoveredChangedIDs) {
		t.Fatalf("ineligible example report: %+v", report)
	}
	gaps := make([]string, 0, len(report.Gaps))
	for _, gap := range report.Gaps {
		gaps = append(gaps, gap.ID)
	}
	slices.Sort(gaps)
	readyInput := backendmodel.ApplyChangeProposalLifecycleInput{ExpectedVersion: draft.Proposal.Version, ProposalRevisionID: draft.Revision.ID, Action: "ready", IdempotencyKey: h.key("ready"), Report: backendmodel.AnalysisReportRef{JobID: impact.ID, ResultVersion: *impact.ResultVersion, InputHash: impact.AnalysisInputHash, ResultHash: report.SemanticResultHash}, AcknowledgedGapIDs: gaps}
	bad := readyInput
	bad.Report.ResultHash = strings.Repeat("f", 64)
	h.reject(t, "apply_backend_change_proposal_lifecycle", bad, owner, "backend_analysis_gate_conflict")
	var ready backendmodel.ChangeProposalApplyResult
	readyReceipt := h.call(t, "apply_backend_change_proposal_lifecycle", readyInput, owner, &ready)
	if ready.Proposal.Status != "ready" || ready.Revision.ID != draft.Revision.ID || ready.Proposal.ReadyReference == nil {
		t.Fatal(ready)
	}
	association := ready.Proposal.ReadyReference
	if association.Report != readyInput.Report || association.ProposalRevisionID != draft.Revision.ID || association.DraftHash != draft.Revision.SemanticHash || !slices.Equal(association.AcknowledgedGapIDs, gaps) || ready.Proposal.Version != draft.Proposal.Version+1 {
		t.Fatal("ready did not retain the exact reviewed report and draft", association)
	}
	savedDetail := h.call(t, "get_backend_analysis", nil, map[string]any{"jobId": impact.ID}, nil)
	savedPage := h.call(t, "get_backend_analysis_results", nil, map[string]any{"jobId": impact.ID, "resultVersion": *impact.ResultVersion, "section": "changes"}, nil)
	stop()
	if err := h.db.Close(); err != nil {
		t.Fatal(err)
	}
	h.server, h.db = newResourcesTestServer(t, h.cfg)
	h.tools = newToolFixture(h.server)
	service = b42ExampleService(h)
	if err := service.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, receipt := range []b41SDKReceipt{startReceipt, retryReceipt, cancelReceipt, rebaseReceipt, readyReceipt, savedDetail, savedPage, oldSource, oldDesired} {
		h.replay(t, receipt)
	}
}

// The public read context admits legacy artifact-context encodings. Keep pins
// as wire JSON instead of feeding them into the stricter writable-domain codec.
type b42ExampleDetail struct {
	Job   *backendanalysis.Job `json:"job"`
	Input struct {
		DocumentVersion string                                `json:"documentVersion"`
		FromRevisionID  string                                `json:"fromRevisionId"`
		Target          backendanalysis.AnalysisContextTarget `json:"target"`
		ObservationMode string                                `json:"observationMode"`
		BeforePins      jsontext.Value                        `json:"beforePins"`
		AfterPins       jsontext.Value                        `json:"afterPins"`
	} `json:"input"`
}

func b42ExampleService(h *b41SDKExamples) *backendanalysis.Service {
	graphs := backendmodel.NewRepo(h.db)
	jobs := backendanalysis.NewRepo(h.db)
	service := backendanalysis.NewService(jobs, graphs, backendanalysis.NewEngine(graphs, nil))
	h.server.SetBackendAnalysis(service, jobs)
	return service
}

func b42RunExampleWorker(t *testing.T, service *backendanalysis.Service) func() {
	t.Helper()
	run := make(chan error, 1)
	go func() { run <- service.Run(t.Context()) }()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := service.WaitRunning(ctx); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Error(err)
		}
		if err := <-run; err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(stop)
	return stop
}

func b42PollExample(t *testing.T, h *b41SDKExamples, id string) backendanalysis.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var detail b42ExampleDetail
		h.call(t, "get_backend_analysis", nil, map[string]any{"jobId": id}, &detail)
		if detail.Input.DocumentVersion != "backend-analysis-context-v1" || detail.Input.ObservationMode != "none" {
			t.Fatal(detail)
		}
		switch detail.Job.Status {
		case "queued", "running":
			// Tests accelerate the advertised production polling interval.
			time.Sleep(5 * time.Millisecond)
		case "completed":
			return *detail.Job
		default:
			t.Fatalf("analysis terminated unexpectedly: %+v", detail.Job)
		}
	}
	t.Fatal("analysis polling timed out")
	return backendanalysis.Job{}
}

func b42ReadExamplePages(t *testing.T, h *b41SDKExamples, job backendanalysis.Job, section string) backendanalysis.ResultManifest {
	t.Helper()
	args := map[string]any{"jobId": job.ID, "resultVersion": *job.ResultVersion, "section": section, "limit": 1}
	seen := map[string]bool{}
	var manifest backendanalysis.ResultManifest
	for {
		var page struct {
			Manifest   backendanalysis.ResultManifest `json:"manifest"`
			Items      []backendanalysis.ResultRecord `json:"items"`
			NextCursor string                         `json:"nextCursor"`
		}
		frozen := h.call(t, "get_backend_analysis_results", nil, args, &page)
		if manifest.SemanticResultHash != "" && manifest.SemanticResultHash != page.Manifest.SemanticResultHash {
			t.Fatal("frozen page changed report")
		}
		manifest = page.Manifest
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatal("duplicate result across pages")
			}
			seen[item.ID] = true
		}
		h.replay(t, frozen)
		if page.NextCursor == "" {
			break
		}
		args["cursor"] = page.NextCursor
	}
	for _, entry := range manifest.Sections {
		if entry.Section == section && entry.Count != len(seen) {
			t.Fatalf("incomplete pagination: got %d want %d", len(seen), entry.Count)
		}
	}
	if manifest.ResultVersion != *job.ResultVersion || manifest.AnalysisInputHash != job.AnalysisInputHash {
		t.Fatal("result pins mismatch")
	}
	return manifest
}

// REST uses the real session/CSRF chain, with no adapter identity bypass.
func b42ExampleREST(t *testing.T, h *b41SDKExamples) func(string, string, any, int, any) []byte {
	t.Helper()
	handler := h.server.Handler()
	request := httptest.NewRequest(http.MethodPost, "http://mocker.local/api/auth/login", bytes.NewReader(b41JSON(t, map[string]string{"name": "B42 example", "password": testauth.Password})))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://mocker.local")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("REST login: %s", response.Body.String())
	}
	var login struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || login.CSRFToken == "" {
		t.Fatal("missing REST session")
	}
	return func(method, path string, input any, status int, out any) []byte {
		t.Helper()
		var raw jsontext.Value
		if input != nil {
			raw = b41JSON(t, input)
		}
		req := httptest.NewRequest(method, "http://mocker.local"+path, bytes.NewReader(raw))
		req.AddCookie(cookies[0])
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://mocker.local")
		req.Header.Set("X-CSRF-Token", login.CSRFToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		h.transcript = append(h.transcript, b41SDKTranscript{Tool: fmt.Sprintf("REST %s %s", method, path), Request: string(raw), Response: rec.Body.String()})
		if rec.Code != status {
			t.Fatalf("REST %s %s status=%d want=%d: %s", method, path, rec.Code, status, rec.Body.String())
		}
		if out != nil {
			if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
				t.Fatal(err)
			}
		}
		return bytes.Clone(rec.Body.Bytes())
	}
}

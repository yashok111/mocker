package mcp

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/yashok111/mocker/internal/backendanalysis"
	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendAnalysisSDKRawQueryAndMutation(t *testing.T) {
	base := `{"projectId":"` + backendTestID + `","jobId":"` + backendTestID + `"`
	args := base + `,"resultVersion":9007199254740993,"section":"findings","service":"billing","kind":"column","certainty":"possible","direction":"upstream","depth":32,"limit":500,"cursor":"saved"}`
	calls := &recordingCaller{status: 200, body: []byte(`{"items":[]}`)}
	_, msg := callTool(t, calls, "get_backend_analysis_results", args)
	if msg != "" || calls.method != "GET" || !strings.Contains(calls.path, "/analyses/"+backendTestID+"/results?") {
		t.Fatalf("%s %s", msg, calls.path)
	}
	for _, part := range []string{"resultVersion=9007199254740993", "section=findings", "service=billing", "kind=column", "certainty=possible", "direction=upstream", "depth=32", "limit=500", "cursor=saved"} {
		if !strings.Contains(calls.path, part) {
			t.Fatal(calls.path)
		}
	}
	for _, v := range []string{"1.0", "1e0", "9223372036854775808", "null"} {
		calls := &recordingCaller{status: 200}
		_, msg := callTool(t, calls, "get_backend_analysis_results", base+`,"resultVersion":`+v+`,"section":"changes"}`)
		if msg == "" || calls.method != "" {
			t.Fatal(v, msg, calls.path)
		}
	}
	for _, name := range []string{"cancel_backend_analysis", "retry_backend_analysis"} {
		calls := &recordingCaller{status: 202, body: []byte(`{"id":"` + backendTestID + `"}`)}
		_, msg := callTool(t, calls, name, base+`,"idempotencyKey":"receipt"}`)
		if msg != "" || calls.method != "POST" || !strings.Contains(string(calls.sent), `"idempotencyKey":"receipt"`) {
			t.Fatal(name, msg, calls.path)
		}
	}
}
func TestBackendChangeRebaseSDKRawNumbers(t *testing.T) {
	for _, v := range []string{"9007199254740993", "9223372036854775807"} {
		args := `{"projectId":"` + backendTestID + `","proposalId":"` + backendTestID + `","expectedVersion":` + v + `,"proposalRevisionId":"` + backendTestID + `","newBaseRevisionId":"` + backendTestID + `","identityResolutions":[],"resolutions":[],"repairCommands":[]}`
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, msg := callTool(t, calls, "preview_backend_change_proposal_rebase", args)
		if msg != "" || !strings.Contains(string(calls.sent), `"expectedVersion":`+v) {
			t.Fatal(msg, string(calls.sent))
		}
		for _, bad := range []string{strings.Replace(args, v, "1.0", 1), strings.Replace(args, v, "1e0", 1), strings.Replace(args, `,"repairCommands":[]`, "", 1), strings.Replace(args, `"resolutions":[]`, `"resolutions":null`, 1), strings.Replace(args, `"expectedVersion":`+v, `"expectedVersion":`+v+`,"expectedVersion":1`, 1)} {
			calls := &recordingCaller{status: 200}
			_, msg := callTool(t, calls, "preview_backend_change_proposal_rebase", bad)
			if msg == "" || calls.method != "" {
				t.Fatal("admitted", bad)
			}
		}
	}
}

func TestBackendAnalysisRealSDKFrozenTargetsAndReady(t *testing.T) {
	cfg := resourcesTestConfig(t)
	server, db := newResourcesTestServer(t, cfg)
	graphs := backendmodel.NewRepo(db)
	jobs := backendanalysis.NewRepo(db)
	service := backendanalysis.NewService(jobs, graphs, backendanalysis.NewEngine(graphs, nil))
	server.SetBackendAnalysis(service, jobs)
	p, err := graphs.Create(t.Context(), backendmodel.CreateInput{Name: "SDK Analysis", IdempotencyKey: "project"})
	if err != nil {
		t.Fatal(err)
	}
	begin := backendmodel.BeginImportInput{ExpectedVersion: p.Version, BaseRevisionID: p.CurrentRevisionID, IdempotencyKey: "source", Profile: backendmodel.EventsProfile, Manifest: backendmodel.SourceManifest{RepositoryName: "synthetic", Provider: backendmodel.SourceProvider{Name: "synthetic", Version: "1", Namespace: "sdk", Method: "ast", Profiles: []string{backendmodel.GraphProfile, backendmodel.RelationalProfile, backendmodel.RuntimeProfile, backendmodel.LineageProfile, backendmodel.EventsProfile}, Limitations: []string{}}, Snapshot: backendmodel.SnapshotManifest{Consistency: "verified", CapturedAt: time.Now().UTC(), Files: []backendmodel.ManifestFile{{Path: "schema.sql", ContentHash: strings.Repeat("a", 64), FileType: "sql", AnalysisStatus: "analyzed"}}}}}
	for _, category := range strings.Fields("files endpoints datastores migrations producers consumers jobs contracts tests") {
		count := int64(0)
		if category == "files" || category == "datastores" {
			count = 1
		}
		begin.Inventory = append(begin.Inventory, backendmodel.InventoryItem{Category: category, Status: "complete", KnownCount: count, Denominator: new(count), DiscoverySource: "synthetic", Gaps: []string{}})
	}
	for i := range begin.Inventory {
		if begin.Inventory[i].Category == "tests" {
			begin.Inventory[i].Status = "partial"
			begin.Inventory[i].Denominator = nil
			begin.Inventory[i].Gaps = []string{"Synthetic tests inventory unknown"}
		}
	}
	session, err := graphs.BeginImport(t.Context(), p.ID, begin)
	if err != nil {
		t.Fatal(err)
	}

	commands := []backendmodel.ImportCommand{{Op: "upsert_node", Node: &backendmodel.ImportNode{ExternalKey: "db", Kind: "datastore", Name: "Synthetic", Attributes: map[string]jsontext.Value{"relational": jsontext.Value(`{"facets":{"sql":{"sourceKind":"sql","dialect":"sqlite","analysisStatus":"complete","gaps":[],"evidenceKeys":["proof"],"qualifiedName":"synthetic","nativeDefinition":null,"databaseName":"synthetic"}}}`)}, EvidenceKeys: []string{"proof"}}}, {Op: "upsert_evidence", Evidence: &backendmodel.ImportEvidence{ExternalKey: "proof", SubjectType: "node", SubjectKey: "db", Method: "sql", Status: "explicit", Source: backendmodel.EvidenceSource{RepositoryID: session.RepositoryID, SnapshotID: session.SnapshotID, File: "schema.sql", ContentHash: strings.Repeat("a", 64)}}}}
	hash, err := backendmodel.ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := graphs.PutImportBatch(t.Context(), p.ID, session.ID, "db", backendmodel.ImportBatchInput{ExpectedImportVersion: session.Version, PayloadHash: hash, Commands: commands})
	if err != nil {
		t.Fatal(err)
	}
	datastore := ""
	for _, identity := range batch.Identities {
		if identity.RecordType == "node" {
			datastore = identity.ID
		}
	}
	preview, err := graphs.PreviewImport(t.Context(), p.ID, session.ID, backendmodel.PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || preview.CandidateHash == nil {
		t.Fatalf("%+v %v", preview, err)
	}
	source, err := graphs.CommitImport(t.Context(), p.ID, session.ID, backendmodel.CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "source-commit"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := graphs.CreateChangeProposal(t.Context(), p.ID, backendmodel.CreateChangeProposalInput{Name: "Empty desired", BaseRevisionID: source.Revision.ID, IdempotencyKey: "full"})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := graphs.CreateProposal(t.Context(), p.ID, backendmodel.CreateProposalInput{Name: "Legacy", BaseRevisionID: source.Revision.ID, RepositoryID: session.RepositoryID, DatastoreID: datastore, FacetKey: "sql", IdempotencyKey: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	command := backendmodel.ChangeProposalCommand{Type: "set_criteria", CommandID: uuid.NewV7().String(), Reason: "Declared empty criteria", Criteria: []backendmodel.ChangeCriterion{}}
	candidate, err := graphs.PreviewChangeProposal(t.Context(), p.ID, d.Proposal.ID, backendmodel.PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []backendmodel.ChangeProposalCommand{command}})
	if err != nil || candidate.CandidateHash == nil {
		t.Fatalf("%+v %v", candidate, err)
	}
	exact := backendmodel.ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: d.Revision.ID}
	targets := []backendanalysis.AnalysisTarget{{RevisionID: source.Revision.ID}, {Proposal: &backendmodel.ProposalReadTarget{ProposalID: legacy.Proposal.ID, ProposalRevisionID: legacy.Revision.ID}}, {ChangeProposal: &exact}, {CommandPreview: &backendanalysis.CommandPreviewTarget{ChangeProposal: exact, ExpectedVersion: d.Proposal.Version, Commands: []backendmodel.ChangeProposalCommand{command}, CandidateHash: *candidate.CandidateHash}}}
	fixture := newToolFixture(server)
	invoke := func(name string, input any, out any) []byte {
		t.Helper()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		response, msg := fixture.Call(t, name, string(raw))
		if msg != "" {
			t.Fatal(name, msg)
		}
		if out != nil {
			if err = json.Unmarshal(response, out); err != nil {
				t.Fatal(err)
			}
		}
		return response
	}
	for i, target := range targets {
		for _, kind := range []string{"diff", "impact"} {
			args := map[string]any{"projectId": p.ID, "kind": kind, "fromRevisionId": source.Revision.ID, "target": target, "scope": map[string]any{}, "limits": map[string]any{"resultBytes": 1048576}, "observationMode": "none", "idempotencyKey": fmt.Sprintf("%d-%s", i, kind)}
			var job backendanalysis.Job
			first := invoke("start_backend_analysis", args, &job)
			if job.RecommendedPollIntervalMs != 2000 {
				t.Fatal(job)
			}
			detail := invoke("get_backend_analysis", map[string]string{"projectId": p.ID, "jobId": job.ID}, nil)
			if !strings.Contains(string(detail), "backend-analysis-context-v1") || strings.Contains(string(detail), "admission") {
				t.Fatal(string(detail))
			}
			invoke("cancel_backend_analysis", map[string]string{"projectId": p.ID, "jobId": job.ID, "idempotencyKey": "cancel-" + job.ID}, nil)
			if !bytes.Equal(first, invoke("start_backend_analysis", args, nil)) {
				t.Fatal("start receipt changed")
			}
		}
	}
	if err = service.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	run := make(chan error, 1)
	go func() { run <- service.Run(t.Context()) }()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err = service.WaitRunning(ctx); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = service.Close(context.Background())
		if err := <-run; err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(stop)
	var job backendanalysis.Job
	invoke("start_backend_analysis", map[string]any{"projectId": p.ID, "kind": "impact", "fromRevisionId": source.Revision.ID, "target": targets[2], "scope": map[string]any{}, "limits": map[string]any{}, "observationMode": "none", "idempotencyKey": "gate"}, &job)
	for job.Status == "queued" || job.Status == "running" {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
		j, err := jobs.Get(ctx, p.ID, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		job = *j
	}
	if job.Status != "completed" {
		t.Fatal(job)
	}
	page, err := jobs.Results(ctx, p.ID, job.ID, backendanalysis.ResultQuery{ResultVersion: *job.ResultVersion, Section: "changes"})
	if err != nil {
		t.Fatal(err)
	}
	report := backendmodel.AnalysisReportRef{JobID: job.ID, ResultVersion: *job.ResultVersion, InputHash: job.AnalysisInputHash, ResultHash: page.Manifest.SemanticResultHash}
	var ready backendmodel.ChangeProposalApplyResult
	readyInput := map[string]any{"projectId": p.ID, "proposalId": d.Proposal.ID, "expectedVersion": d.Proposal.Version, "proposalRevisionId": d.Revision.ID, "action": "ready", "idempotencyKey": "ready", "report": report, "acknowledgedGapIds": []string{}}
	if page.Manifest.SourceCoverageAfter.Status != "partial" || !page.Manifest.Complete || page.Manifest.RuntimeVerified || len(page.Manifest.Gaps) == 0 {
		t.Fatal(page.Manifest)
	}
	gaps := make([]string, 0, len(page.Manifest.Gaps))
	for _, gap := range page.Manifest.Gaps {
		gaps = append(gaps, gap.ID)
	}
	for _, ack := range [][]string{{}, {"unknown"}, {gaps[0], gaps[0]}} {
		readyInput["acknowledgedGapIds"] = ack
		raw, err := json.Marshal(readyInput)
		if err != nil {
			t.Fatal(err)
		}
		if _, message := fixture.Call(t, "apply_backend_change_proposal_lifecycle", string(raw)); !strings.Contains(message, "backend_change_ready_conflict") {
			t.Fatal("bad gap acknowledgement admitted", message)
		}
	}
	readyInput["acknowledgedGapIds"] = gaps
	receipt := invoke("apply_backend_change_proposal_lifecycle", readyInput, &ready)
	if ready.Proposal.Status != "ready" {
		t.Fatal(ready)
	}
	rebase := map[string]any{"projectId": p.ID, "proposalId": d.Proposal.ID, "expectedVersion": ready.Proposal.Version, "proposalRevisionId": d.Revision.ID, "newBaseRevisionId": source.Revision.ID, "identityResolutions": []any{}, "resolutions": []any{}, "repairCommands": []any{}}
	var rb backendmodel.ChangeProposalRebaseCandidate
	invoke("preview_backend_change_proposal_rebase", rebase, &rb)
	if rb.CandidateHash == nil {
		t.Fatal(rb)
	}
	rebase["candidateHash"] = *rb.CandidateHash
	rebase["idempotencyKey"] = "rebase"
	invoke("apply_backend_change_proposal_rebase", rebase, nil)
	if !bytes.Equal(receipt, invoke("apply_backend_change_proposal_lifecycle", readyInput, nil)) {
		t.Fatal("historical ready receipt changed")
	}
	savedDetail := invoke("get_backend_analysis", map[string]string{"projectId": p.ID, "jobId": job.ID}, nil)
	stop()
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, reopened := newResourcesTestServer(t, cfg)
	nextJobs := backendanalysis.NewRepo(reopened)
	restarted.SetBackendAnalysis(backendanalysis.NewService(nextJobs, backendmodel.NewRepo(reopened), backendanalysis.NewEngine(backendmodel.NewRepo(reopened), nil)), nextJobs)
	fixture = newToolFixture(restarted)
	if !bytes.Equal(savedDetail, invoke("get_backend_analysis", map[string]string{"projectId": p.ID, "jobId": job.ID}, nil)) {
		t.Fatal("restart context changed")
	}
	if !bytes.Equal(receipt, invoke("apply_backend_change_proposal_lifecycle", readyInput, nil)) {
		t.Fatal("restart ready receipt changed")
	}
}

func TestBackendAnalysisToolAnnotations(t *testing.T) {
	endpoint := newTestEndpoint(t)
	response := doMCP(t, endpoint.Handler(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, map[string]string{"Authorization": "Bearer " + testKey})
	var env struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Annotations struct {
					ReadOnly   bool `json:"readOnlyHint"`
					Idempotent bool `json:"idempotentHint"`
				} `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"start_backend_analysis": false, "list_backend_analysis": true, "get_backend_analysis": true, "cancel_backend_analysis": false, "retry_backend_analysis": false, "get_backend_analysis_results": true, "preview_backend_change_proposal_rebase": true, "apply_backend_change_proposal_rebase": false, "apply_backend_change_proposal_lifecycle": false}
	for _, tool := range env.Result.Tools {
		read, ok := want[tool.Name]
		if !ok {
			continue
		}
		if tool.Annotations.ReadOnly != read || !tool.Annotations.Idempotent {
			t.Fatal(tool)
		}
		delete(want, tool.Name)
	}
	if len(want) != 0 {
		t.Fatal("missing tools", want)
	}
}

func TestBackendAnalysisSDKOperationBodyCaps(t *testing.T) {
	calls := &recordingCaller{status: 202, body: []byte(`{"id":"` + backendTestID + `"}`)}
	fixture := newToolFixture(calls)
	args := func(size int) string {
		return `{"projectId":"` + backendTestID + `","kind":"diff","fromRevisionId":"` + backendTestID + `","target":{"revisionId":"` + backendTestID + `"},"scope":{"kind":"` + strings.Repeat("a", size) + `"},"limits":{},"observationMode":"none","idempotencyKey":"cap"}`
	}
	if _, message := fixture.Call(t, "start_backend_analysis", args(1<<20)); message != "" || calls.method != "POST" || len(calls.sent) <= 1<<20 {
		t.Fatalf("1MiB+ body refused: %s", message)
	}
	calls.method = ""
	if _, message := fixture.Call(t, "start_backend_analysis", args(2<<20)); message == "" || calls.method != "" {
		t.Fatal("2MiB+ body admitted", message)
	}
}

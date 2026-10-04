package admin

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/yashok111/mocker/internal/backendanalysis"
	"github.com/yashok111/mocker/internal/backendmodel"
)

func b42Source(t *testing.T, s *Server, p backendmodel.Project, repository, key, name string, sourceVersion ...string) backendmodel.ImportCommitResult {
	t.Helper()
	begin := transportImportFixture(p)
	begin.IdempotencyKey = key
	begin.Mode = "composed"
	begin.Profile = backendmodel.ComposedProfile
	begin.SourceScope = &backendmodel.SourceScope{Kind: "add_repository"}
	if repository != "" {
		begin.SourceScope = &backendmodel.SourceScope{Kind: "reconcile", RepositoryID: repository, ProviderNamespace: begin.Manifest.Provider.Namespace}
	}
	begin.ScopeStatus = &backendmodel.SourceScopeStatus{Status: "complete", Gaps: []string{}}
	begin.SyncPolicy = backendmodel.WholeSourcePolicy
	begin.Manifest.Provider.Profiles = []string{backendmodel.GraphProfile, backendmodel.RelationalProfile, backendmodel.RuntimeProfile, backendmodel.LineageProfile, backendmodel.EventsProfile, backendmodel.ComposedProfile}
	if len(sourceVersion) > 0 && sourceVersion[0] == "5" {
		begin.Mode = ""
		begin.Profile = backendmodel.EventsProfile
		begin.SourceScope = nil
		begin.ScopeStatus = nil
		begin.SyncPolicy = ""
		begin.Manifest.Provider.Profiles = begin.Manifest.Provider.Profiles[:5]
	}
	for i := range begin.Inventory {
		begin.Inventory[i].Status = "complete"
		begin.Inventory[i].Denominator = new(begin.Inventory[i].KnownCount)
		begin.Inventory[i].Reason = ""
	}
	session, err := s.backendRepo.BeginImport(t.Context(), p.ID, begin)
	if err != nil {
		t.Fatal(err)
	}
	commands := []backendmodel.ImportCommand{{Op: "upsert_node", Node: &backendmodel.ImportNode{ExternalKey: "system", Kind: "system", Name: name, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"proof"}}}, {Op: "upsert_evidence", Evidence: &backendmodel.ImportEvidence{ExternalKey: "proof", SubjectType: "node", SubjectKey: "system", Method: "ast", Status: "explicit", Source: backendmodel.EvidenceSource{RepositoryID: session.RepositoryID, SnapshotID: session.SnapshotID, File: "main.go", ContentHash: strings.Repeat("a", 64)}}}}
	hash, err := backendmodel.ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.backendRepo.PutImportBatch(t.Context(), p.ID, session.ID, "graph", backendmodel.ImportBatchInput{ExpectedImportVersion: session.Version, PayloadHash: hash, Commands: commands})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.backendRepo.PreviewImport(t.Context(), p.ID, session.ID, backendmodel.PreviewImportInput{ExpectedImportVersion: receipt.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || preview.CandidateHash == nil {
		t.Fatalf("%v %v", preview, err)
	}
	out, err := s.backendRepo.CommitImport(t.Context(), p.ID, session.ID, backendmodel.CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: key + "commit"})
	if err != nil {
		t.Fatal(err)
	}
	return *out
}
func b42Draft(t *testing.T, s *Server) (backendmodel.Project, *backendmodel.ChangeProposalDetail, string, string) {
	t.Helper()
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Ready", IdempotencyKey: "project"})
	if err != nil {
		t.Fatal(err)
	}
	source := b42Source(t, s, *p, "", "initial", "System")
	graph, err := s.backendRepo.ResolveEffectiveGraph(t.Context(), source.Project.ID, backendmodel.BackendReadTarget{RevisionID: source.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.backendRepo.CreateChangeProposal(t.Context(), p.ID, backendmodel.CreateChangeProposalInput{Name: "Desired", BaseRevisionID: source.Revision.ID, IdempotencyKey: "proposal"})
	if err != nil {
		t.Fatal(err)
	}
	commands := []backendmodel.ChangeProposalCommand{{Type: "rename", CommandID: uuid.NewV7().String(), RecordType: "node", ID: graph.State.Nodes[0].ID, Name: "Desired", Reason: "Desired"}}
	preview, err := s.backendRepo.PreviewChangeProposal(t.Context(), p.ID, d.Proposal.ID, backendmodel.PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: commands})
	if err != nil || preview.CandidateHash == nil {
		t.Fatalf("%v %v", preview, err)
	}
	saved, err := s.backendRepo.ApplyChangeProposal(t.Context(), p.ID, d.Proposal.ID, backendmodel.ApplyChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: commands, CandidateHash: *preview.CandidateHash, IdempotencyKey: "commands"})
	if err != nil {
		t.Fatal(err)
	}
	d, err = s.backendRepo.GetChangeProposal(t.Context(), p.ID, d.Proposal.ID, backendmodel.GetChangeProposalInput{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Revision.ID != saved.Revision.ID {
		t.Fatal("draft mismatch")
	}
	return source.Project, d, graph.State.Nodes[0].ID, graph.Source.SourceVector.Partitions[0].RepositoryID
}
func TestBackendChangeLifecycleRealAnalysisGateAndRebase(t *testing.T) {
	s := loopbackTestServer(t, nil)
	p, d, node, repository := b42Draft(t, s)
	jobs := backendanalysis.NewRepo(s.db)
	service := backendanalysis.NewService(jobs, s.backendRepo, backendanalysis.NewEngine(s.backendRepo, nil))
	s.SetBackendAnalysis(service, jobs)
	if err := service.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	run := make(chan error, 1)
	go func() { run <- service.Run(t.Context()) }()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := service.WaitRunning(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(context.Background()); err != nil {
			t.Error(err)
		}
		if err := <-run; err != nil {
			t.Error(err)
		}
	})
	base := "/api/backend-projects/" + p.ID
	path := base + "/change-proposals/" + d.Proposal.ID
	in := backendanalysis.StartInput{Kind: "impact", FromRevisionID: d.Revision.BaseRevisionID, Target: backendanalysis.AnalysisTarget{ChangeProposal: &backendmodel.ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: d.Revision.ID}}, ObservationMode: "none", IdempotencyKey: "impact"}
	var job backendanalysis.Job
	start := b41Call(t, s, "POST", base+"/analyses", in, 202, &job)
	for job.Status == "queued" || job.Status == "running" {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
		next, err := jobs.Get(ctx, p.ID, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		job = *next
	}
	if job.Status != "completed" {
		t.Fatal(job)
	}
	var page struct {
		Manifest backendanalysis.ResultManifest `json:"manifest"`
	}
	b41Call(t, s, "GET", fmt.Sprintf("%s/analyses/%s/results?resultVersion=%d&section=changes", base, job.ID, *job.ResultVersion), nil, 200, &page)
	report := backendmodel.AnalysisReportRef{JobID: job.ID, ResultVersion: *job.ResultVersion, InputHash: job.AnalysisInputHash, ResultHash: page.Manifest.SemanticResultHash}
	ready := backendmodel.ApplyChangeProposalLifecycleInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Action: "ready", IdempotencyKey: "ready", Report: report, AcknowledgedGapIDs: []string{}}
	for _, gap := range page.Manifest.Gaps {
		ready.AcknowledgedGapIDs = append(ready.AcknowledgedGapIDs, gap.ID)
	}
	bad := ready
	bad.Report.ResultHash = strings.Repeat("f", 64)
	fault := b41Call(t, s, "POST", path+"/lifecycle", bad, 409, nil)
	if !bytes.Contains(fault, []byte("backend_analysis_gate_conflict")) {
		t.Fatal(string(fault))
	}

	scoped := in
	scoped.IdempotencyKey = "scope-omitted"
	scoped.Scope.Kind = "not-selected"
	var scopedJob backendanalysis.Job
	b41Call(t, s, "POST", base+"/analyses", scoped, 202, &scopedJob)
	for scopedJob.Status == "queued" || scopedJob.Status == "running" {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
		next, err := jobs.Get(ctx, p.ID, scopedJob.ID)
		if err != nil {
			t.Fatal(err)
		}
		scopedJob = *next
	}
	scopedPage, err := jobs.Results(ctx, p.ID, scopedJob.ID, backendanalysis.ResultQuery{ResultVersion: *scopedJob.ResultVersion, Section: "changes"})
	if err != nil {
		t.Fatal(err)
	}
	if !scopedPage.Manifest.Complete || len(scopedPage.Manifest.ChangedIDs) == 0 || len(scopedPage.Manifest.CoveredChangedIDs) != 0 {
		t.Fatal(scopedPage.Manifest)
	}
	scopedReady := ready
	scopedReady.IdempotencyKey = "scope-ready"
	scopedReady.Report = backendmodel.AnalysisReportRef{JobID: scopedJob.ID, ResultVersion: *scopedJob.ResultVersion, InputHash: scopedJob.AnalysisInputHash, ResultHash: scopedPage.Manifest.SemanticResultHash}
	scopedReady.AcknowledgedGapIDs = []string{}
	for _, gap := range scopedPage.Manifest.Gaps {
		scopedReady.AcknowledgedGapIDs = append(scopedReady.AcknowledgedGapIDs, gap.ID)
	}
	b41Call(t, s, "POST", path+"/lifecycle", scopedReady, 409, nil)
	bad = ready
	bad.Action = "merge"
	b41Call(t, s, "POST", path+"/lifecycle", bad, 422, nil)
	bad = ready
	bad.AcknowledgedGapIDs = []string{"unknown-gap"}
	b41Call(t, s, "POST", path+"/lifecycle", bad, 409, nil)
	var accepted backendmodel.ChangeProposalApplyResult
	receipt := b41Call(t, s, "POST", path+"/lifecycle", ready, 200, &accepted)
	if accepted.Proposal.Status != "ready" || accepted.Proposal.ReadyReference == nil || accepted.Revision.ID != d.Revision.ID {
		t.Fatal(accepted)
	}
	b41Call(t, s, "GET", base+"/change-proposals?status=ready", nil, 200, nil)
	bad = ready
	bad.IdempotencyKey = "again"
	b41Call(t, s, "POST", path+"/lifecycle", bad, 409, nil)
	detail := b41Call(t, s, "GET", base+"/analyses/"+job.ID, nil, 200, nil)
	source := b42Source(t, s, p, repository, "advance", "New Source")
	rebase := backendmodel.PreviewChangeProposalRebaseInput{ExpectedVersion: accepted.Proposal.Version, ProposalRevisionID: d.Revision.ID, NewBaseRevisionID: source.Revision.ID, IdentityResolutions: []backendmodel.ChangeRebaseIdentityResolution{}, Resolutions: []backendmodel.ChangeRebaseResolution{}, RepairCommands: []backendmodel.ChangeProposalCommand{}}
	var candidate backendmodel.ChangeProposalRebaseCandidate
	b41Call(t, s, "POST", path+"/rebase-preview", rebase, 200, &candidate)
	if len(candidate.Conflicts) != 1 {
		t.Fatalf("missing real conflict %+v", candidate)
	}
	rebase.Resolutions = []backendmodel.ChangeRebaseResolution{{ConflictID: candidate.Conflicts[0].ID, Choice: "keep_proposal", Reason: "Keep intent"}}
	b41Call(t, s, "POST", path+"/rebase-preview", rebase, 200, &candidate)
	if candidate.CandidateHash == nil {
		t.Fatal(candidate)
	}
	apply := backendmodel.ApplyChangeProposalRebaseInput{PreviewChangeProposalRebaseInput: rebase, CandidateHash: *candidate.CandidateHash, IdempotencyKey: "rebase"}
	var rebased backendmodel.ChangeProposalApplyResult
	rebaseReceipt := b41Call(t, s, "POST", path+"/rebase", apply, 200, &rebased)
	if rebased.Proposal.Status != "draft" || rebased.Proposal.ReadyReference != nil || rebased.Revision.Rebase == nil {
		t.Fatal(rebased)
	}
	if !bytes.Equal(receipt, b41Call(t, s, "POST", path+"/lifecycle", ready, 200, nil)) {
		t.Fatal("ready replay changed after rebase")
	}
	if !bytes.Equal(rebaseReceipt, b41Call(t, s, "POST", path+"/rebase", apply, 200, nil)) {
		t.Fatal("rebase replay changed")
	}
	if !bytes.Equal(detail, b41Call(t, s, "GET", base+"/analyses/"+job.ID, nil, 200, nil)) {
		t.Fatal("saved input changed")
	}
	if !bytes.Equal(start, b41Call(t, s, "POST", base+"/analyses", in, 202, nil)) {
		t.Fatal("start replay changed")
	}
	b41Call(t, s, "GET", path+"/revisions/"+rebased.Revision.ID+"/nodes/"+node, nil, 200, nil)
}

func TestBackendChangeRebaseRESTExactIntegerCAS(t *testing.T) {
	s := loopbackTestServer(t, nil)
	p, d, _, _ := b42Draft(t, s)
	path := "/api/backend-projects/" + p.ID + "/change-proposals/" + d.Proposal.ID + "/rebase-preview"
	for _, version := range []string{"9007199254740993", "9223372036854775807"} {
		if _, err := s.db.W.ExecContext(t.Context(), `UPDATE backend_change_proposals SET version=? WHERE id=?`, version, d.Proposal.ID); err != nil {
			t.Fatal(err)
		}
		raw := `{"expectedVersion":` + version + `,"proposalRevisionId":"` + d.Revision.ID + `","newBaseRevisionId":"` + d.Revision.BaseRevisionID + `","identityResolutions":[],"resolutions":[],"repairCommands":[]}`
		var decoded backendmodel.PreviewChangeProposalRebaseInput
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(raw))
		recorder := httptest.NewRecorder()
		if !s.backendChangeProposalBody(recorder, request, &decoded) {
			t.Fatal(recorder.Body)
		}
		expected, _ := strconv.ParseInt(version, 10, 64)
		if decoded.ExpectedVersion != expected {
			t.Fatalf("parser rounded %d", decoded.ExpectedVersion)
		}
		want := 200
		field := "expectedVersion"
		if version == "9223372036854775807" {
			want = 409
			field = "currentVersion"
		}
		response := b41Call(t, s, "POST", path, raw, want, nil)
		if !strings.Contains(string(response), `"`+field+`":`+version) {
			t.Fatal("CAS rounded", string(response))
		}
		for _, bad := range []string{"1.0", "1e0", "9223372036854775808", "null"} {
			b41Call(t, s, "POST", path, strings.Replace(raw, version, bad, 1), 400, nil)
		}
	}
}

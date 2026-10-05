package backendanalysis

import (
	"encoding/json/v2"
	"testing"

	model "github.com/yashok111/mocker/internal/backendmodel"
)

func TestB43ConformanceImplementedArchiveUnarchive(t *testing.T) {
	f := newRebaseAnalysisFixture(t, "6")
	d := f.proposal()
	d = setB43Criteria(t, f, d, []model.ChangeCriterion{{Key: "exists", Kind: "object_exists", Required: true, Description: "Exact source object", RecordType: "node", ID: f.node, ObjectKind: "handler"}})
	_, readyRef := gateJob(t, f, d, "impact", false)
	gate, err := f.jobs.ReadAnalysisGate(t.Context(), f.project.ID, readyRef)
	if err != nil {
		t.Fatal(err)
	}
	ready, err := f.graphs.ApplyChangeProposalLifecycle(t.Context(), f.project.ID, d.Proposal.ID, model.ApplyChangeProposalLifecycleInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Action: "ready", IdempotencyKey: "ready", Report: readyRef, AcknowledgedGapIDs: gate.GapIDs}, f.jobs)
	if err != nil {
		t.Fatal(err)
	}
	f.importSource("implemented", "Desired", false, nil)
	job, _, _ := runB43Conformance(t, f, d, nil, nil)
	s := NewService(f.jobs, f.graphs, NewEngine(f.graphs, nil))
	claim, err := f.jobs.Claim(t.Context(), "conformance")
	if err != nil || claim == nil {
		t.Fatalf("claim %v", err)
	}
	if err = s.execute(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	completed, err := f.jobs.Get(t.Context(), f.project.ID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := f.jobs.Results(t.Context(), f.project.ID, job.ID, ResultQuery{ResultVersion: *completed.ResultVersion, Section: "checks"})
	if err != nil {
		t.Fatal(err)
	}
	ref := model.AnalysisReportRef{JobID: job.ID, ResultVersion: *completed.ResultVersion, InputHash: job.AnalysisInputHash, ResultHash: page.Manifest.SemanticResultHash}
	evidence, err := f.jobs.ReadConformanceEvidence(t.Context(), f.project.ID, ref)
	if err != nil || len(evidence.Criteria) != 1 {
		t.Fatalf("gate %+v %v", evidence, err)
	}
	input := model.ApplyChangeProposalLifecycleInput{ExpectedVersion: ready.Proposal.Version, ProposalRevisionID: d.Revision.ID, Action: "implemented", IdempotencyKey: "implemented", Report: ref, ResultRevisionID: f.project.CurrentRevisionID, Exceptions: []model.ChangeProposalException{}}
	implemented, err := f.graphs.ApplyChangeProposalLifecycle(t.Context(), f.project.ID, d.Proposal.ID, input, f.jobs)
	if err != nil {
		t.Fatal(err)
	}
	if implemented.Proposal.Status != "implemented" || implemented.Proposal.ImplementedReference == nil || implemented.Proposal.ReadyReference == nil {
		t.Fatalf("association %+v", implemented.Proposal)
	}
	archived, err := f.graphs.ApplyChangeProposalLifecycle(t.Context(), f.project.ID, d.Proposal.ID, model.ApplyChangeProposalLifecycleInput{ExpectedVersion: implemented.Proposal.Version, ProposalRevisionID: d.Revision.ID, Action: "archive", IdempotencyKey: "archive"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if archived.Proposal.ImplementedReference == nil {
		t.Fatal("archive lost implemented association")
	}
	unarchived, err := f.graphs.ApplyChangeProposalLifecycle(t.Context(), f.project.ID, d.Proposal.ID, model.ApplyChangeProposalLifecycleInput{ExpectedVersion: archived.Proposal.Version, ProposalRevisionID: d.Revision.ID, Action: "unarchive", IdempotencyKey: "unarchive"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if unarchived.Proposal.Status != "draft" || unarchived.Proposal.ReadyReference != nil || unarchived.Proposal.ImplementedReference != nil {
		t.Fatal("unarchive did not clear associations")
	}
	replay, err := f.graphs.ApplyChangeProposalLifecycle(t.Context(), f.project.ID, d.Proposal.ID, input, nil)
	if err != nil || integrationBytes(t, replay) != integrationBytes(t, implemented) {
		t.Fatalf("implemented replay moved %v", err)
	}
}

func TestB43ConformanceGateChecksSavedChunks(t *testing.T) {
	f := newRebaseAnalysisFixture(t, "6")
	d := f.proposal()
	d = setB43Criteria(t, f, d, []model.ChangeCriterion{{Key: "runtime", Kind: "runtime_check", Required: true, Description: "Runtime remains unverified", TargetIDs: []string{f.node}}})
	job, _, _ := runB43Conformance(t, f, d, nil, nil)
	service := NewService(f.jobs, f.graphs, NewEngine(f.graphs, nil))
	claim, err := f.jobs.Claim(t.Context(), "gate-integrity")
	if err != nil || claim == nil {
		t.Fatalf("claim %v", err)
	}
	if err = service.execute(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	complete, err := f.jobs.Get(t.Context(), f.project.ID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := f.jobs.Results(t.Context(), f.project.ID, job.ID, ResultQuery{ResultVersion: *complete.ResultVersion, Section: "checks"})
	if err != nil {
		t.Fatal(err)
	}
	ref := model.AnalysisReportRef{JobID: job.ID, ResultVersion: *complete.ResultVersion, InputHash: job.AnalysisInputHash, ResultHash: page.Manifest.SemanticResultHash}
	evidence, err := f.jobs.ReadConformanceEvidence(t.Context(), f.project.ID, ref)
	if err != nil || evidence.Criteria[0].Outcome != "unverified" {
		t.Fatalf("runtime evidence %+v %v", evidence, err)
	}
	// Simulate corrupted storage beyond the immutable SQL trigger; production keeps it.
	if _, err = f.db.W.ExecContext(t.Context(), `DROP TRIGGER backend_analysis_chunk_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.W.ExecContext(t.Context(), `UPDATE backend_analysis_chunks SET items_json=replace(items_json,'unverified','satisfied') WHERE job_id=? AND section='checks'`, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.jobs.ReadConformanceEvidence(t.Context(), f.project.ID, ref); err == nil {
		t.Fatal("gate trusted manifest counters without checking chunks")
	}
}

func TestB43ConformanceReaderRejectsSelfConsistentRuntimeForgery(t *testing.T) {
	f := newRebaseAnalysisFixture(t, "6")
	d := f.proposal()
	d = setB43Criteria(t, f, d, []model.ChangeCriterion{{Key: "runtime", Kind: "runtime_check", Required: true, Description: "Runtime cannot pass statically", TargetIDs: []string{f.node}}})
	job, _, report := runB43Conformance(t, f, d, nil, nil)
	for i := range report.Snapshot.Chunks {
		chunk := &report.Snapshot.Chunks[i]
		var records []ResultRecord
		if err := json.Unmarshal(chunk.ItemsJSON, &records); err != nil {
			t.Fatal(err)
		}
		for j := range records {
			if chunk.Section == "checks" {
				var row ConformanceCriterionDetail
				if err := json.Unmarshal(records[j].Detail, &row); err != nil {
					t.Fatal(err)
				}
				row.Outcome = "satisfied"
				records[j].Detail, _ = canonical(row)
			}
			if chunk.Section == "findings" {
				var summary ConformanceSummaryDetail
				if err := json.Unmarshal(records[j].Detail, &summary); err != nil {
					t.Fatal(err)
				}
				summary.RequiredSatisfied = true
				summary.UnverifiedCount = 0
				summary.SatisfiedCount = 1
				records[j].Detail, _ = canonical(summary)
			}
		}
		chunk.ItemsJSON, _ = canonical(records)
	}
	claim, err := f.jobs.Claim(t.Context(), "forged")
	if err != nil || claim == nil {
		t.Fatalf("claim %v", err)
	}
	report.Snapshot.Manifest.JobID = job.ID
	report.Snapshot.Manifest.AnalysisInputHash = job.AnalysisInputHash
	report.Snapshot.Manifest.ResultVersion = 1
	if _, err = f.jobs.Finalize(t.Context(), f.project.ID, job.ID, claim.Token, *report); err != nil {
		t.Fatal(err)
	}
	page, err := f.jobs.Results(t.Context(), f.project.ID, job.ID, ResultQuery{ResultVersion: 1, Section: "checks"})
	if err != nil {
		t.Fatal(err)
	}
	ref := model.AnalysisReportRef{JobID: job.ID, ResultVersion: 1, InputHash: job.AnalysisInputHash, ResultHash: page.Manifest.SemanticResultHash}
	if _, err = f.jobs.ReadConformanceEvidence(t.Context(), f.project.ID, ref); err == nil {
		t.Fatal("self-consistent runtime success accepted")
	}
}

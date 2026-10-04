package backendanalysis

import (
	"context"
	"testing"
	"time"
	"uuid"

	model "github.com/yashok111/mocker/internal/backendmodel"
)

func gateJob(t *testing.T, f *rebaseAnalysisFixture, d *model.ChangeProposalDetail, kind string, preview bool) (*Job, model.AnalysisReportRef) {
	t.Helper()
	target := AnalysisTarget{ChangeProposal: &model.ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: d.Revision.ID}}
	if preview {
		commands := []model.ChangeProposalCommand{integrationRename(t, f.node, "preview")}
		p, err := f.graphs.PreviewChangeProposal(t.Context(), f.project.ID, d.Proposal.ID, model.PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: commands})
		if err != nil {
			t.Fatal(err)
		}
		target = AnalysisTarget{CommandPreview: &CommandPreviewTarget{ChangeProposal: *target.ChangeProposal, ExpectedVersion: d.Proposal.Version, Commands: commands, CandidateHash: *p.CandidateHash}}
	}
	s := NewService(f.jobs, f.graphs, NewEngine(f.graphs, nil))
	if err := s.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	run := make(chan error, 1)
	go func() { run <- s.Run(t.Context()) }()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := s.WaitRunning(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
		if err := <-run; err != nil {
			t.Error(err)
		}
	})
	j, err := s.Start(ctx, f.project.ID, StartInput{Kind: kind, FromRevisionID: d.Revision.BaseRevisionID, Target: target, Limits: defaultLimits(), ObservationMode: "none", IdempotencyKey: uuid.NewV7().String()})
	if err != nil {
		t.Fatal(err)
	}
	for j.Status == "queued" || j.Status == "running" {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
		j, err = f.jobs.Get(ctx, f.project.ID, j.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if j.Status != "completed" {
		t.Fatalf("job: %+v", j)
	}
	page, err := f.jobs.Results(ctx, f.project.ID, j.ID, ResultQuery{ResultVersion: *j.ResultVersion, Section: "changes"})
	if err != nil {
		t.Fatal(err)
	}
	return j, model.AnalysisReportRef{JobID: j.ID, ResultVersion: *j.ResultVersion, InputHash: j.AnalysisInputHash, ResultHash: page.Manifest.SemanticResultHash}
}

func TestAnalysisGateExactSavedEvidence(t *testing.T) {
	f := newRebaseAnalysisFixture(t, "6")
	d := f.proposal()
	_, ref := gateJob(t, f, d, "impact", false)
	e, err := f.jobs.ReadAnalysisGate(t.Context(), f.project.ID, ref)
	if err != nil {
		t.Fatal(err)
	}
	if e.ChangeProposal.ProposalRevisionID != d.Revision.ID || e.DraftHash != d.Revision.SemanticHash || e.BaseRevisionID != d.Revision.BaseRevisionID || e.Report != ref || !e.Complete || len(e.ChangedIDs) == 0 {
		t.Fatalf("wrong evidence: %+v", e)
	}
	before := integrationBytes(t, e)
	f.importSource("advance", "advanced", false, nil)
	f.rebase(d, "keep_proposal")
	e, err = f.jobs.ReadAnalysisGate(t.Context(), f.project.ID, ref)
	if err != nil || integrationBytes(t, e) != before {
		t.Fatalf("saved evidence moved: %v", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*model.AnalysisReportRef)
	}{
		{"job", func(r *model.AnalysisReportRef) { r.JobID = uuid.NewV7().String() }},
		{"version", func(r *model.AnalysisReportRef) { r.ResultVersion++ }},
		{"input", func(r *model.AnalysisReportRef) { r.InputHash = "wrong" }},
		{"result", func(r *model.AnalysisReportRef) { r.ResultHash = "wrong" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := ref
			tc.change(&bad)
			if _, err := f.jobs.ReadAnalysisGate(t.Context(), f.project.ID, bad); err == nil {
				t.Fatal("accepted wrong pin")
			}
		})
	}
	if _, err = f.jobs.ReadAnalysisGate(t.Context(), uuid.NewV7().String(), ref); err == nil {
		t.Fatal("accepted foreign project")
	}
}
func TestAnalysisGateRejectsOtherTargets(t *testing.T) {
	for _, tc := range []struct {
		kind    string
		preview bool
	}{{"diff", false}, {"impact", true}} {
		t.Run(tc.kind+string(rune('0'+boolInt(tc.preview))), func(t *testing.T) {
			f := newRebaseAnalysisFixture(t, "6")
			d := f.proposal()
			_, ref := gateJob(t, f, d, tc.kind, tc.preview)
			if _, err := f.jobs.ReadAnalysisGate(t.Context(), f.project.ID, ref); err == nil {
				t.Fatal("accepted ineligible report")
			}
		})
	}
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func TestAnalysisGateLifecycleRebaseAndRestart(t *testing.T) {
	for _, mode := range []string{"saved", "carry", "resolution", "empty"} {
		t.Run(mode, func(t *testing.T) {
			f := newRebaseAnalysisFixture(t, "6")
			d := f.proposal()
			if mode == "empty" {
				var err error
				d, err = f.graphs.CreateChangeProposal(t.Context(), f.project.ID, model.CreateChangeProposalInput{Name: "Empty", BaseRevisionID: f.project.CurrentRevisionID, IdempotencyKey: "empty"})
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "carry" {
				f.importSource("delete", "", true, nil)
				d = f.rebase(d, "keep_proposal")
			}
			if mode == "resolution" {
				f.importSource("rename", "Source renamed", false, nil)
				d = f.rebase(d, "replace")
			}
			if mode == "carry" || mode == "resolution" {
				graph, err := f.graphs.ResolveEffectiveGraph(t.Context(), f.project.ID, model.BackendReadTarget{ChangeProposal: &model.ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: d.Revision.ID}})
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, o := range graph.Origins {
					if o.RebaseResolution != nil {
						found = true
						if o.Kind != "intent" {
							t.Fatal("resolution acquired source proof")
						}
					}
				}
				if !found {
					t.Fatal("missing rebase resolution origin")
				}
			}
			_, ref := gateJob(t, f, d, "impact", false)
			e, err := f.jobs.ReadAnalysisGate(t.Context(), f.project.ID, ref)
			if err != nil {
				t.Fatal(err)
			}
			before := integrationBytes(t, e)
			input, err := f.jobs.Input(t.Context(), f.project.ID, ref.JobID)
			if err != nil {
				t.Fatal(err)
			}
			inputBefore := integrationBytes(t, input)
			in := model.ApplyChangeProposalLifecycleInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Action: "ready", IdempotencyKey: "ready", Report: ref, AcknowledgedGapIDs: e.GapIDs}
			ready, err := f.graphs.ApplyChangeProposalLifecycle(t.Context(), f.project.ID, d.Proposal.ID, in, f.jobs)
			if err != nil {
				t.Fatal(err)
			}
			if ready.Proposal.Status != "ready" || ready.Proposal.ReadyReference == nil || ready.Revision.ID != d.Revision.ID {
				t.Fatalf("wrong ready: %+v", ready)
			}
			readyBytes := integrationBytes(t, ready)
			// A normal preview still observes the new aggregate version and memory guards.
			commands := []model.ChangeProposalCommand{integrationRename(t, f.node, "After ready")}
			preview, err := f.graphs.PreviewChangeProposal(t.Context(), f.project.ID, d.Proposal.ID, model.PreviewChangeProposalInput{ExpectedVersion: ready.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: commands})
			if err != nil {
				t.Fatal(err)
			}
			edit, err := f.graphs.ApplyChangeProposal(t.Context(), f.project.ID, d.Proposal.ID, model.ApplyChangeProposalInput{ExpectedVersion: ready.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: commands, CandidateHash: *preview.CandidateHash, IdempotencyKey: "after-ready"})
			if err != nil {
				t.Fatal(err)
			}
			if edit.Proposal.Status != "draft" || edit.Proposal.ReadyReference != nil {
				t.Fatal("edit retained current ready association")
			}
			f.reopen()
			after, err := f.jobs.ReadAnalysisGate(t.Context(), f.project.ID, ref)
			if err != nil || integrationBytes(t, after) != before {
				t.Fatal("historical report moved", err)
			}
			input, err = f.jobs.Input(t.Context(), f.project.ID, ref.JobID)
			if err != nil || integrationBytes(t, input) != inputBefore {
				t.Fatal("historical input moved", err)
			}
			replay, err := f.graphs.ApplyChangeProposalLifecycle(t.Context(), f.project.ID, d.Proposal.ID, in, nil)
			if err != nil || integrationBytes(t, replay) != readyBytes {
				t.Fatal("ready receipt moved", err)
			}
			current, err := f.graphs.GetChangeProposal(t.Context(), f.project.ID, d.Proposal.ID, model.GetChangeProposalInput{})
			if err != nil || current.Proposal.Status != "draft" || current.Revision.ID != edit.Revision.ID {
				t.Fatal("replay changed current status", err)
			}
		})
	}
}

func TestAnalysisGateRejectsNoncompletedAndUnpublished(t *testing.T) {
	for _, status := range []string{"queued", "running", "cancelled", "interrupted", "failed", "unpublished"} {
		t.Run(status, func(t *testing.T) {
			f := newRebaseAnalysisFixture(t, "6")
			d := f.proposal()
			_, j, _, report := f.start(d, d.Revision.BaseRevisionID, false)
			ref := model.AnalysisReportRef{JobID: j.ID, ResultVersion: 1, InputHash: j.AnalysisInputHash, ResultHash: report.Snapshot.Manifest.SemanticResultHash}
			switch status {
			case "running", "failed", "unpublished":
				claim, err := f.jobs.Claim(t.Context(), "gate-worker")
				if err != nil {
					t.Fatal(err)
				}
				if status == "failed" || status == "unpublished" {
					report.Snapshot.Manifest.JobID = j.ID
					report.Snapshot.Manifest.AnalysisInputHash = j.AnalysisInputHash
					report.Snapshot.Manifest.ResultVersion = 1
					if status == "failed" {
						report.Status = "failed"
						report.Snapshot.Manifest.Complete = false
					}
					terminal, err := f.jobs.Finalize(t.Context(), f.project.ID, j.ID, claim.Token, *report)
					if err != nil {
						t.Fatal(err)
					}
					page, err := f.jobs.Results(t.Context(), f.project.ID, j.ID, ResultQuery{ResultVersion: *terminal.ResultVersion, Section: "changes"})
					if err != nil {
						t.Fatal(err)
					}
					ref.ResultHash = page.Manifest.SemanticResultHash
					if status == "unpublished" {
						ref.ResultVersion = 2
					}
				}
			case "cancelled":
				if _, err := f.jobs.Cancel(t.Context(), f.project.ID, j.ID, CancelInput{IdempotencyKey: "cancel"}); err != nil {
					t.Fatal(err)
				}
			case "interrupted":
				if err := f.jobs.RecoverInterrupted(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.jobs.ReadAnalysisGate(t.Context(), f.project.ID, ref); err == nil {
				t.Fatal("accepted ineligible state")
			}
		})
	}
}

func TestAnalysisGateRejectsPublishedIntermediate(t *testing.T) {
	f := newRebaseAnalysisFixture(t, "6")
	d := f.proposal()
	_, j, _, report := f.start(d, d.Revision.BaseRevisionID, false)
	claim, err := f.jobs.Claim(t.Context(), "publish-worker")
	if err != nil {
		t.Fatal(err)
	}
	report.Snapshot.Manifest.JobID = j.ID
	report.Snapshot.Manifest.AnalysisInputHash = j.AnalysisInputHash
	report.Snapshot.Manifest.ResultVersion = 1
	if _, err = f.jobs.Publish(t.Context(), f.project.ID, j.ID, claim.Token, report.Snapshot); err != nil {
		t.Fatal(err)
	}
	first, err := f.jobs.Results(t.Context(), f.project.ID, j.ID, ResultQuery{ResultVersion: 1, Section: "changes"})
	if err != nil {
		t.Fatal(err)
	}
	report.Snapshot.Manifest.ResultVersion = 2
	// Ready is evidence-backed; an incompatible verdict itself is not a veto.
	report.Snapshot.Manifest.Verdict = "incompatible"
	if _, err = f.jobs.Finalize(t.Context(), f.project.ID, j.ID, claim.Token, *report); err != nil {
		t.Fatal(err)
	}
	old := model.AnalysisReportRef{JobID: j.ID, ResultVersion: 1, InputHash: j.AnalysisInputHash, ResultHash: first.Manifest.SemanticResultHash}
	if _, err = f.jobs.ReadAnalysisGate(t.Context(), f.project.ID, old); err == nil {
		t.Fatal("accepted exact intermediate manifest as terminal evidence")
	}
	final, err := f.jobs.Results(t.Context(), f.project.ID, j.ID, ResultQuery{ResultVersion: 2, Section: "changes"})
	if err != nil {
		t.Fatal(err)
	}
	ref := old
	ref.ResultVersion = 2
	ref.ResultHash = final.Manifest.SemanticResultHash
	e, err := f.jobs.ReadAnalysisGate(t.Context(), f.project.ID, ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.graphs.ApplyChangeProposalLifecycle(t.Context(), f.project.ID, d.Proposal.ID, model.ApplyChangeProposalLifecycleInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Action: "ready", IdempotencyKey: "ready-incompatible", Report: ref, AcknowledgedGapIDs: e.GapIDs}, f.jobs); err != nil {
		t.Fatal("incompatible verdict blocked ready", err)
	}
}

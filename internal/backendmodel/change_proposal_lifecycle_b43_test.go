package backendmodel

import (
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"
	"uuid"
)

func TestB43ArchiveUnarchiveReceiptAndAssociation(t *testing.T) {
	r, base, d := changeFixture(t)
	in := ApplyChangeProposalLifecycleInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Action: "archive", IdempotencyKey: "archive"}
	archived, err := r.ApplyChangeProposalLifecycle(t.Context(), base.Project.ID, d.Proposal.ID, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if archived.Proposal.Status != "archived" {
		t.Fatal(archived.Proposal.Status)
	}
	unarchive := ApplyChangeProposalLifecycleInput{ExpectedVersion: archived.Proposal.Version, ProposalRevisionID: d.Revision.ID, Action: "unarchive", IdempotencyKey: "unarchive"}
	draft, err := r.ApplyChangeProposalLifecycle(t.Context(), base.Project.ID, d.Proposal.ID, unarchive, nil)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Proposal.Status != "draft" || draft.Proposal.ReadyReference != nil {
		t.Fatal("unarchive did not reset")
	}
	replay, err := r.ApplyChangeProposalLifecycle(t.Context(), base.Project.ID, d.Proposal.ID, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(archived)
	b, _ := json.Marshal(replay)
	if string(a) != string(b) {
		t.Fatal("archive receipt bytes changed")
	}
	in.ExpectedVersion = draft.Proposal.Version
	in.IdempotencyKey = "invalid-unarchive"
	in.Action = "unarchive"
	if _, err = r.ApplyChangeProposalLifecycle(t.Context(), base.Project.ID, d.Proposal.ID, in, nil); err == nil {
		t.Fatal("fresh-key invalid transition accepted")
	}
}
func TestB43LifecycleClosedArms(t *testing.T) {
	const rid = "11111111-1111-4111-8111-111111111111"
	for _, action := range []string{"archive", "unarchive"} {
		raw := fmt.Sprintf(`{"expectedVersion":9223372036854775807,"proposalRevisionId":%q,"action":%q,"idempotencyKey":"key"}`, rid, action)
		var in ApplyChangeProposalLifecycleInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err = json.Unmarshal(encoded, &fields); err != nil || len(fields) != 4 {
			t.Fatalf("lifecycle fields %s %v", encoded, err)
		}
	}
}

func TestB43ArchiveAtomicFailuresAndConcurrentCAS(t *testing.T) {
	for _, mode := range []string{"event", "receipt", "quota", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			r, base, d := changeFixture(t)
			in := ApplyChangeProposalLifecycleInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Action: "archive", IdempotencyKey: "archive"}
			err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
				switch mode {
				case "event":
					_, err := tx.ExecContext(t.Context(), `CREATE TRIGGER fail_b43 BEFORE INSERT ON backend_change_proposal_events WHEN NEW.version>1 BEGIN SELECT RAISE(ABORT,'event failure'); END`)
					return err
				case "receipt":
					_, err := tx.ExecContext(t.Context(), `CREATE TRIGGER fail_b43 BEFORE INSERT ON backend_command_receipts BEGIN SELECT RAISE(ABORT,'receipt failure'); END`)
					return err
				case "overflow":
					in.ExpectedVersion = math.MaxInt64
					_, err := tx.ExecContext(t.Context(), `UPDATE backend_change_proposals SET version=? WHERE id=?`, in.ExpectedVersion, d.Proposal.ID)
					return err
				case "quota":
					for i := range MaxChangeProposalRevisions {
						rev := d.Revision
						rev.ID = uuid.NewV7().String()
						rev.ParentRevisionID = new(d.Revision.ID)
						rev.AcceptedBatchRevisionID = rev.ID
						p := d.Proposal
						p.Version = int64(i + 10)
						if err := persistChangeRevision(t.Context(), tx, p, rev, "restore", d.Revision.ID, []ChangeProposalCommand{}, nil); err != nil {
							return err
						}
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			before := readyState(t, r, base.Project.ID, d.Proposal.ID)
			if _, err = r.ApplyChangeProposalLifecycle(t.Context(), base.Project.ID, d.Proposal.ID, in, nil); err == nil {
				t.Fatal("injected failure accepted")
			}
			if readyState(t, r, base.Project.ID, d.Proposal.ID) != before {
				t.Fatal("lifecycle failure did not roll back")
			}
			if r.db.TransientBytes("backend:"+base.Project.ID) != 0 {
				t.Fatal("lease leaked")
			}
		})
	}
	t.Run("concurrent", func(t *testing.T) {
		r, base, d := changeFixture(t)
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for _, key := range []string{"first", "second"} {
			wg.Go(func() {
				_, err := r.ApplyChangeProposalLifecycle(t.Context(), base.Project.ID, d.Proposal.ID, ApplyChangeProposalLifecycleInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Action: "archive", IdempotencyKey: key}, nil)
				results <- err
			})
		}
		wg.Wait()
		close(results)
		success := 0
		for err := range results {
			if err == nil {
				success++
			}
		}
		if success != 1 {
			t.Fatalf("winners %d", success)
		}
	})
}

func TestB43LifecycleConformanceGateRoster(t *testing.T) {
	r, base, d := changeFixture(t)
	target := BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: d.Revision.ID}}
	graph, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, target)
	if err != nil {
		t.Fatal(err)
	}
	source, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{RevisionID: base.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	req := AnalysisEvidenceRequest{Targets: []BackendReadTarget{target, {RevisionID: base.Revision.ID}}, BaselineRevisionID: base.Revision.ID, ResultRevisionID: base.Revision.ID}
	footprint, err := r.AnalysisEvidenceInputFootprint(t.Context(), base.Project.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	ctx, lease, err := r.ReserveAnalysisInput(t.Context(), base.Project.ID, footprint)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	pins, err := r.ReadAnalysisEvidencePins(ctx, base.Project.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	ref := AnalysisReportRef{JobID: uuid.NewV7().String(), ResultVersion: 1, InputHash: strings.Repeat("a", 64), ResultHash: strings.Repeat("b", 64)}
	input := ApplyChangeProposalLifecycleInput{Action: "implemented", ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Report: ref, ResultRevisionID: base.Revision.ID}
	d.Revision.Criteria = []ChangeCriterion{{Key: "required", Kind: "object_exists", Required: true}, {Key: "optional", Kind: "object_exists", Required: false}}
	valid := ConformanceEvidence{ProjectID: base.Project.ID, Kind: "conformance", Status: "completed", Report: ref, ChangeProposal: *target.ChangeProposal, BaseRevisionID: base.Revision.ID, DraftHash: d.Revision.SemanticHash, ResultRevisionID: base.Revision.ID, ResultSemanticHash: source.Pins.BaseSemanticHash, BasePins: source.Pins, DraftPins: graph.Pins, ResultPins: source.Pins, BaseSource: analysisSourcePinsForGraph(source), DraftSource: analysisSourcePinsForGraph(graph), ResultSource: analysisSourcePinsForGraph(source), EvidencePins: pins, Complete: true, RuleSetVersion: "b43-rules/v1", TraversalVersion: "b42-traversal/v1", BehaviorStatus: "unverified", Criteria: []ConformanceCriterionEvidence{{Key: "required", Kind: "object_exists", Required: true, Outcome: "satisfied"}, {Key: "optional", Kind: "object_exists", Required: false, Outcome: "violated"}}}
	cases := []struct {
		name   string
		mutate func(*ConformanceEvidence)
	}{
		{"project", func(e *ConformanceEvidence) { e.ProjectID = "other" }}, {"kind", func(e *ConformanceEvidence) { e.Kind = "impact" }}, {"status", func(e *ConformanceEvidence) { e.Status = "cancelled" }}, {"hash", func(e *ConformanceEvidence) { e.Report.ResultHash = "other" }}, {"version", func(e *ConformanceEvidence) { e.Report.ResultVersion++ }}, {"draft", func(e *ConformanceEvidence) { e.DraftHash = "other" }}, {"source", func(e *ConformanceEvidence) { e.ResultSource.ContentHash = "other" }}, {"partial", func(e *ConformanceEvidence) { e.Complete = false }}, {"truncated", func(e *ConformanceEvidence) { e.TruncationReasons = []string{"limit"} }}, {"rules", func(e *ConformanceEvidence) { e.RuleSetVersion = "other" }}, {"required runtime", func(e *ConformanceEvidence) { e.Criteria[0].Outcome = "unverified" }}, {"missing", func(e *ConformanceEvidence) { e.Criteria = e.Criteria[:1] }}, {"duplicate", func(e *ConformanceEvidence) { e.Criteria[1] = e.Criteria[0] }}, {"required flag", func(e *ConformanceEvidence) { e.Criteria[0].Required = false }},
	}
	if err = validateConformanceLifecycle(base.Project.ID, d.Proposal.ID, input, &valid, &d.Revision, source, graph, source, pins); err != nil {
		t.Fatal(err)
	}
	t.Run("forged satisfied runtime", func(t *testing.T) {
		revision := d.Revision
		revision.Criteria = slices.Clone(d.Revision.Criteria)
		revision.Criteria[0].Kind = "runtime_check"
		evidence := valid
		evidence.Criteria = slices.Clone(valid.Criteria)
		evidence.Criteria[0].Kind = "runtime_check"
		if validateConformanceLifecycle(base.Project.ID, d.Proposal.ID, input, &evidence, &revision, source, graph, source, pins) == nil {
			t.Fatal("forged satisfied runtime accepted")
		}
	})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := valid
			e.Criteria = slices.Clone(valid.Criteria)
			tc.mutate(&e)
			if validateConformanceLifecycle(base.Project.ID, d.Proposal.ID, input, &e, &d.Revision, source, graph, source, pins) == nil {
				t.Fatal("invalid conformance evidence accepted")
			}
		})
	}
}

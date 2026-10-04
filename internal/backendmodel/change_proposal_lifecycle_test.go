package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"math"
	"strings"
	"sync"
	"testing"
	"uuid"
)

type readyReader func(context.Context, string, AnalysisReportRef) (*AnalysisGateEvidence, error)

func (f readyReader) ReadAnalysisGate(ctx context.Context, pid string, ref AnalysisReportRef) (*AnalysisGateEvidence, error) {
	return f(ctx, pid, ref)
}
func readyEvidence(t *testing.T, r *Repo, d *ChangeProposalDetail) (ApplyChangeProposalLifecycleInput, AnalysisGateEvidence) {
	t.Helper()
	g, err := r.ResolveEffectiveGraph(t.Context(), d.Proposal.ProjectID, BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: d.Revision.ID}})
	if err != nil {
		t.Fatal(err)
	}
	ref := AnalysisReportRef{JobID: uuid.NewV7().String(), ResultVersion: 1, InputHash: strings.Repeat("a", 64), ResultHash: strings.Repeat("b", 64)}
	in := ApplyChangeProposalLifecycleInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Action: "ready", IdempotencyKey: "ready", Report: ref, AcknowledgedGapIDs: []string{"source"}}
	e := AnalysisGateEvidence{ProjectID: d.Proposal.ProjectID, Kind: "impact", Status: "completed", Report: ref, ChangeProposal: *g.Target.ChangeProposal, FromRevisionID: d.Revision.BaseRevisionID, BaseRevisionID: g.Pins.BaseRevisionID, BaseSemanticHash: g.Pins.BaseSemanticHash, DraftHash: d.Revision.SemanticHash, EffectiveSemanticHash: g.Pins.EffectiveSemanticHash, TargetHash: g.Pins.TargetHash, SourcePins: AnalysisSourcePins{RevisionID: g.Pins.BaseRevisionID, SemanticHash: g.Pins.BaseSemanticHash, ContentHash: g.Source.SourceContentHash, SourceVectorHash: g.Pins.SourceVectorHash, SourceSnapshotIDs: g.Pins.SourceSnapshotIDs}, ArtifactPins: g.Pins.ArtifactPins, Complete: true, GapIDs: []string{"source"}, RuleSetVersion: "b42-rules/v1", TraversalVersion: "b42-traversal/v1"}
	return in, e
}
func fixedReady(e AnalysisGateEvidence) readyReader {
	return func(context.Context, string, AnalysisReportRef) (*AnalysisGateEvidence, error) { return &e, nil }
}
func readyState(t *testing.T, r *Repo, pid, id string) string {
	t.Helper()
	var value string
	err := r.db.R.QueryRowContext(t.Context(), `SELECT json_array(status,version,current_draft_revision_id,current_draft_hash,ready_reference,(SELECT group_concat(document) FROM backend_change_proposal_events WHERE proposal_id=p.id),(SELECT group_concat(document) FROM backend_change_proposal_revisions WHERE proposal_id=p.id),(SELECT group_concat(response) FROM backend_command_receipts)) FROM backend_change_proposals p WHERE project_id=? AND id=?`, pid, id).Scan(&value)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func TestChangeReadyGate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ApplyChangeProposalLifecycleInput, *AnalysisGateEvidence)
	}{
		{"valid", func(*ApplyChangeProposalLifecycleInput, *AnalysisGateEvidence) {}},
		{"project", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) { e.ProjectID = "other" }},
		{"job", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) { e.Report.JobID = "other" }},
		{"result version", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) { e.Report.ResultVersion++ }},
		{"input hash", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) { e.Report.InputHash = "other" }},
		{"result hash", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) { e.Report.ResultHash = "other" }},
		{"diff", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) { e.Kind = "diff" }},
		{"preview", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) { e.CommandPreview = true }},
		{"proposal", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) {
			e.ChangeProposal.ProposalID = "other"
		}},
		{"old equal hash draft", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) {
			e.ChangeProposal.ProposalRevisionID = uuid.NewV7().String()
		}},
		{"from", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) { e.FromRevisionID = "other" }},
		{"base", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) { e.BaseRevisionID = "other" }},
		{"base hash", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) { e.BaseSemanticHash = "other" }},
		{"draft hash", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) { e.DraftHash = "other" }},
		{"effective hash", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) { e.EffectiveSemanticHash = "other" }},
		{"target hash", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) { e.TargetHash = "other" }},
		{"source content", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) {
			e.SourcePins.ContentHash = "other"
		}},
		{"source vector", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) {
			e.SourcePins.SourceVectorHash = "other"
		}},
		{"source snapshots", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) {
			e.SourcePins.SourceSnapshotIDs = []string{"other"}
		}},
		{"artifacts", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) {
			e.ArtifactPins = append(e.ArtifactPins, ArtifactPin{})
		}},
		{"failed", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) { e.Status = "failed" }},
		{"cancelled", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) { e.Status = "cancelled" }},
		{"interrupted", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) { e.Status = "interrupted" }},
		{"partial", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) { e.Complete = false }},
		{"missing seed", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) {
			e.ChangedIDs = []ChangeRecordRef{{RecordType: "node", ID: "uncovered"}}
		}},
		{"truncation", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) {
			e.TruncationReasons = []string{"depth"}
		}},
		{"duplicate ack", func(in *ApplyChangeProposalLifecycleInput, _ *AnalysisGateEvidence) {
			in.AcknowledgedGapIDs = []string{"source", "source"}
		}},
		{"unknown ack", func(in *ApplyChangeProposalLifecycleInput, _ *AnalysisGateEvidence) {
			in.AcknowledgedGapIDs = []string{"unknown"}
		}},
		{"missing ack", func(in *ApplyChangeProposalLifecycleInput, _ *AnalysisGateEvidence) {
			in.AcknowledgedGapIDs = []string{}
		}},
		{"rule version", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) { e.RuleSetVersion = "" }},
		{"traversal version", func(_ *ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) { e.TraversalVersion = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _, d := changeFixture(t)
			in, e := readyEvidence(t, r, d)
			tc.mutate(&in, &e)
			before := readyState(t, r, d.Proposal.ProjectID, d.Proposal.ID)
			out, err := r.ApplyChangeProposalLifecycle(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, in, fixedReady(e))
			if r.db.TransientBytes("backend:"+d.Proposal.ProjectID) != 0 {
				t.Fatal("lifecycle leaked reservation")
			}
			if tc.name == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				if out.Proposal.Status != "ready" || out.Proposal.Version != d.Proposal.Version+1 || out.Revision.ID != d.Revision.ID || out.Revision.SemanticHash != d.Revision.SemanticHash {
					t.Fatalf("wrong transition: %+v", out)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid report accepted")
			}
			if readyState(t, r, d.Proposal.ProjectID, d.Proposal.ID) != before {
				t.Fatal("rejected gate mutated state")
			}
		})
	}
}
func TestChangeReadyClosedInput(t *testing.T) {
	base := `{"expectedVersion":9007199254740993,"proposalRevisionId":"draft","action":"ready","idempotencyKey":"key","report":{"jobId":"job","resultVersion":1,"inputHash":"hash","resultHash":"hash"},"acknowledgedGapIds":[]}`
	var in ApplyChangeProposalLifecycleInput
	if err := json.Unmarshal([]byte(base), &in); err != nil || in.ExpectedVersion != 9007199254740993 {
		t.Fatalf("exact integer: %v", err)
	}
	for _, raw := range []string{strings.Replace(base, "9007199254740993", "1.0", 1), strings.Replace(base, "9007199254740993", "1e0", 1), strings.Replace(base, `"acknowledgedGapIds":[]`, `"acknowledgedGapIds":null`, 1), strings.Replace(base, `"resultVersion":1`, `"resultVersion":null`, 1), strings.Replace(base, `"action":"ready"`, `"action":"ready","pass":true`, 1)} {
		if err := json.Unmarshal([]byte(raw), &in); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestChangeReadyEditResetsAndReplay(t *testing.T) {
	for _, action := range []string{"apply", "restore", "rebase"} {
		t.Run(action, func(t *testing.T) {
			r, base, d := changeFixture(t)
			pid, id := d.Proposal.ProjectID, d.Proposal.ID
			in, e := readyEvidence(t, r, d)
			ready, err := r.ApplyChangeProposalLifecycle(t.Context(), pid, id, in, fixedReady(e))
			if err != nil {
				t.Fatal(err)
			}
			raw := ready.receiptJSON
			var event string
			if err = r.db.R.QueryRowContext(t.Context(), `SELECT document FROM backend_change_proposal_events WHERE proposal_id=? AND version=?`, id, ready.Proposal.Version).Scan(&event); err != nil {
				t.Fatal(err)
			}
			got, err := r.GetChangeProposal(t.Context(), pid, id, GetChangeProposalInput{})
			if err != nil {
				t.Fatal(err)
			}
			if got.Proposal.ReadyReference == nil || got.Proposal.ReadyReference.Report != in.Report {
				t.Fatal("lost current association")
			}
			fresh := in
			fresh.IdempotencyKey = "new-ready"
			fresh.ExpectedVersion = ready.Proposal.Version
			_, err = r.ApplyChangeProposalLifecycle(t.Context(), pid, id, fresh, fixedReady(e))
			assertFault(t, err, "backend_change_status_conflict")
			err = nil
			var after *ChangeProposalApplyResult
			switch action {
			case "apply":
				next, _ := saveChange(t, r, &ChangeProposalDetail{Proposal: ready.Proposal, Revision: ready.Revision}, "edit", changeCommand(t, "set_criteria", `"criteria":[]`))
				after = &ChangeProposalApplyResult{Proposal: next.Proposal, Revision: next.Revision}
			case "restore":
				after, err = r.RestoreChangeProposal(t.Context(), pid, id, RestoreChangeProposalInput{ExpectedVersion: ready.Proposal.Version, ProposalRevisionID: d.Revision.ID, RestoreRevisionID: d.Revision.ID, IdempotencyKey: "restore"})
			case "rebase":
				rebase := PreviewChangeProposalRebaseInput{ExpectedVersion: ready.Proposal.Version, ProposalRevisionID: d.Revision.ID, NewBaseRevisionID: base.Revision.ID}
				p, pe := r.PreviewChangeProposalRebase(t.Context(), pid, id, rebase)
				if pe != nil {
					t.Fatal(pe)
				}
				after, err = r.ApplyChangeProposalRebase(t.Context(), pid, id, ApplyChangeProposalRebaseInput{PreviewChangeProposalRebaseInput: rebase, CandidateHash: *p.CandidateHash, IdempotencyKey: "rebase"})
			}
			if err != nil {
				t.Fatal(err)
			}
			if after.Proposal.Status != "draft" || after.Proposal.ReadyReference != nil || after.Revision.ID == d.Revision.ID {
				t.Fatalf("not reset: %+v", after)
			}
			before := readyState(t, r, pid, id)
			replay, err := r.ApplyChangeProposalLifecycle(t.Context(), pid, id, in, nil)
			if err != nil || replay.receiptJSON != raw {
				t.Fatalf("replay changed: %v", err)
			}
			if readyState(t, r, pid, id) != before {
				t.Fatal("replay mutated current aggregate")
			}
			got, err = r.GetChangeProposal(t.Context(), pid, id, GetChangeProposalInput{ProposalRevisionID: d.Revision.ID})
			if err != nil || got.Proposal.Status != "draft" || got.Proposal.ReadyReference != nil || got.Revision.ID != d.Revision.ID {
				t.Fatalf("historical/current conflated: %+v %v", got, err)
			}
			var afterEvent string
			if err = r.db.R.QueryRowContext(t.Context(), `SELECT document FROM backend_change_proposal_events WHERE proposal_id=? AND version=?`, id, ready.Proposal.Version).Scan(&afterEvent); err != nil || afterEvent != event {
				t.Fatal("historical event changed", err)
			}
		})
	}
}
func TestChangeReadyListPagination(t *testing.T) {
	r, base, d := changeFixture(t)
	pid := base.Project.ID
	for _, key := range []string{"two", "three"} {
		if _, err := r.CreateChangeProposal(t.Context(), pid, CreateChangeProposalInput{Name: key, BaseRevisionID: base.Revision.ID, IdempotencyKey: key}); err != nil {
			t.Fatal(err)
		}
	}
	old, err := r.ListChangeProposals(t.Context(), pid, ChangeProposalListInput{Status: "draft", Limit: 1})
	if err != nil || old.NextCursor == "" {
		t.Fatal("missing cursor", err)
	}
	in, e := readyEvidence(t, r, d)
	if _, err = r.ApplyChangeProposalLifecycle(t.Context(), pid, d.Proposal.ID, in, fixedReady(e)); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ListChangeProposals(t.Context(), pid, ChangeProposalListInput{Status: "draft", Limit: 1, Cursor: old.NextCursor}); err == nil {
		t.Fatal("accepted stale cursor")
	}
	ready, err := r.ListChangeProposals(t.Context(), pid, ChangeProposalListInput{Status: "ready", Limit: 1})
	if err != nil || len(ready.Items) != 1 || ready.Items[0].ReadyReference == nil || ready.NextCursor != "" {
		t.Fatalf("ready filter %+v %v", ready, err)
	}
	first, err := r.ListChangeProposals(t.Context(), pid, ChangeProposalListInput{Status: "draft", Limit: 1})
	if err != nil || len(first.Items) != 1 || first.Items[0].Status != "draft" || first.NextCursor == "" {
		t.Fatalf("draft filter %+v %v", first, err)
	}
	second, err := r.ListChangeProposals(t.Context(), pid, ChangeProposalListInput{Status: "draft", Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID || second.NextCursor != "" {
		t.Fatalf("second page %+v %v", second, err)
	}
}

func TestChangeReadyAtomicFailures(t *testing.T) {
	for _, mode := range []string{"event", "receipt", "quota", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			r, _, d := changeFixture(t)
			pid, id := d.Proposal.ProjectID, d.Proposal.ID
			in, e := readyEvidence(t, r, d)
			err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
				switch mode {
				case "event":
					_, err := tx.ExecContext(t.Context(), `CREATE TRIGGER fail_ready BEFORE INSERT ON backend_change_proposal_events WHEN NEW.version>1 BEGIN SELECT RAISE(ABORT,'event failure'); END`)
					return err
				case "receipt":
					_, err := tx.ExecContext(t.Context(), `CREATE TRIGGER fail_ready BEFORE INSERT ON backend_command_receipts BEGIN SELECT RAISE(ABORT,'receipt failure'); END`)
					return err
				case "overflow":
					in.ExpectedVersion = math.MaxInt64
					_, err := tx.ExecContext(t.Context(), `UPDATE backend_change_proposals SET version=? WHERE id=?`, in.ExpectedVersion, id)
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
			before := readyState(t, r, pid, id)
			if _, err = r.ApplyChangeProposalLifecycle(t.Context(), pid, id, in, fixedReady(e)); err == nil {
				t.Fatal("accepted injected failure")
			}
			if r.db.TransientBytes("backend:"+pid) != 0 {
				t.Fatal("failure leaked reservation")
			}
			if mode == "quota" {
				assertFault(t, err, "backend_change_quota_exceeded")
			}
			if readyState(t, r, pid, id) != before {
				t.Fatal("failure did not roll back transition")
			}
		})
	}
}
func TestChangeReadyConcurrentCAS(t *testing.T) {
	for _, mode := range []string{"edit", "ready", "same-key"} {
		t.Run(mode, func(t *testing.T) {
			r, _, d := changeFixture(t)
			pid, id := d.Proposal.ProjectID, d.Proposal.ID
			in, e := readyEvidence(t, r, d)
			entered, release := make(chan struct{}), make(chan struct{})
			blocked := readyReader(func(context.Context, string, AnalysisReportRef) (*AnalysisGateEvidence, error) {
				close(entered)
				<-release
				return &e, nil
			})
			done := make(chan error, 1)
			go func() { _, err := r.ApplyChangeProposalLifecycle(t.Context(), pid, id, in, blocked); done <- err }()
			<-entered
			if mode == "edit" {
				saveChange(t, r, d, "concurrent-edit", changeCommand(t, "set_criteria", `"criteria":[]`))
			} else {
				next := in
				if mode == "ready" {
					next.IdempotencyKey = "other-ready"
				}
				if _, err := r.ApplyChangeProposalLifecycle(t.Context(), pid, id, next, fixedReady(e)); err != nil {
					t.Fatal(err)
				}
			}
			before := readyState(t, r, pid, id)
			close(release)
			err := <-done
			if mode == "same-key" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				assertFault(t, err, "backend_change_version_conflict")
			}
			if readyState(t, r, pid, id) != before {
				t.Fatal("losing/replayed operation changed aggregate")
			}
		})
	}
	// Two callers may both complete preparation before either reaches the writer.
	t.Run("parallel", func(t *testing.T) {
		r, _, d := changeFixture(t)
		in, e := readyEvidence(t, r, d)
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for _, key := range []string{"a", "b"} {
			wg.Go(func() {
				next := in
				next.IdempotencyKey = key
				_, err := r.ApplyChangeProposalLifecycle(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, next, fixedReady(e))
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
			t.Fatalf("successful transitions: %d", success)
		}
	})
}

func TestChangeReadyRetainsPreparationLease(t *testing.T) {
	r, _, d := changeFixture(t)
	in, e := readyEvidence(t, r, d)
	prepared, err := r.prepareChangeReady(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, in, fixedReady(e))
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.lease.Release()
	if prepared == nil || r.db.TransientBytes("backend:"+d.Proposal.ProjectID) == 0 {
		t.Fatal("decoded ready draft lost its charge before writer/receipt serialization")
	}
}

func TestChangeReadyReleasesPreparedLease(t *testing.T) {
	r, _, d := changeFixture(t)
	in, e := readyEvidence(t, r, d)
	prepared, err := r.prepareChangeReady(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, in, fixedReady(e))
	if err != nil {
		t.Fatal(err)
	}
	prepared.lease.Release()
	if r.db.TransientBytes("backend:"+d.Proposal.ProjectID) != 0 {
		t.Fatal("prepared release leaked reservation")
	}
	e.TargetHash = "invalid"
	if _, err = r.prepareChangeReady(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, in, fixedReady(e)); err == nil {
		t.Fatal("invalid pins accepted")
	}
	if r.db.TransientBytes("backend:"+d.Proposal.ProjectID) != 0 {
		t.Fatal("failed preparation leaked reservation")
	}
}

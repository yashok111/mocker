package backendmodel

import (
	"encoding/json/v2"
	"errors"
	"log/slog"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/yashok111/mocker/internal/store"
)

func proposalPreviewInput(d *ProposalDetail, commands ...ProposalCommand) PreviewProposalInput {
	return PreviewProposalInput{ExpectedVersion: d.Proposal.Version, DraftRevisionID: d.Proposal.DraftRevisionID, Commands: commands}
}

func proposalApplyInput(t *testing.T, r *Repo, d *ProposalDetail, key string, commands ...ProposalCommand) ApplyProposalInput {
	t.Helper()
	in := proposalPreviewInput(d, commands...)
	v, err := r.PreviewProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, in)
	if err != nil || v.CandidateHash == nil {
		t.Fatalf("preview: %+v %v", v, err)
	}
	return ApplyProposalInput{ExpectedVersion: in.ExpectedVersion, DraftRevisionID: in.DraftRevisionID, Commands: commands, CandidateHash: *v.CandidateHash, IdempotencyKey: key}
}

func TestProposalPreviewNoWrites(t *testing.T) {
	t.Parallel()
	r, detail, _, ids := proposalEvaluationFixture(t, "sqlite")
	before := proposalSourceBytes(t, r)
	var count, changes int64
	if err := r.db.W.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	in := proposalPreviewInput(detail, proposalNullable(ids, "required", false))
	one, err := r.PreviewProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, in)
	if err != nil || one.CandidateHash == nil || one.CandidateGraphHash == nil || one.DraftRevisionID != detail.Revision.ID || one.BaseRevisionID != detail.Proposal.BaseRevisionID || one.ExpectedVersion != 1 {
		t.Fatalf("preview: %+v %v", one, err)
	}
	two, err := r.PreviewProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, in)
	if err != nil || *one.CandidateHash != *two.CandidateHash || *one.CandidateGraphHash != *two.CandidateGraphHash {
		t.Fatalf("unstable preview: %+v %v", two, err)
	}
	var after int64
	if err := r.db.W.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != changes {
		t.Fatalf("preview wrote: %d -> %d %v", changes, after, err)
	}
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_proposal_revisions`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("preview created revision: %d %v", count, err)
	}
	assertProposalSourceBytes(t, r, before)
	badFK := proposalFK(ids)
	badFK.ColumnPairs[0].ToColumnID = ids["column:users:email"]
	bad, err := r.PreviewProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, proposalPreviewInput(detail, badFK))
	if err != nil || bad.CandidateHash != nil || bad.CandidateGraphHash != nil || len(bad.Diagnostics) == 0 {
		t.Fatalf("invalid candidate: %+v %v", bad, err)
	}
}

func TestProposalApplyCASAndReceipt(t *testing.T) {
	t.Parallel()
	r, detail, _, ids := proposalEvaluationFixture(t, "postgresql")
	before := proposalSourceBytes(t, r)
	in := proposalApplyInput(t, r, detail, "apply", proposalNullable(ids, "required", false))
	got, err := r.ApplyProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Proposal.Version != 2 || got.Proposal.DraftRevisionID != got.Revision.ID || got.Proposal.DraftHash != got.Revision.SemanticHash || got.Revision.ParentRevisionID == nil || *got.Revision.ParentRevisionID != detail.Revision.ID || got.Revision.SemanticHash != in.CandidateHash || len(got.Criteria) != 3 {
		t.Fatalf("apply: %+v", got)
	}
	var saved string
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT response FROM backend_command_receipts WHERE scope=? AND key=?`, "proposal-apply:"+detail.Proposal.ProjectID+":"+detail.Proposal.ID, in.IdempotencyKey).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		b, err := json.Marshal(got)
		if err != nil || string(b) != saved {
			t.Fatalf("initial acknowledgement differs from durable receipt: %v", err)
		}
	}
	assertProposalSourceBytes(t, r, before)
	get, err := r.GetProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, GetProposalInput{})
	if err != nil || len(get.History) != 2 || get.LastApplyReceipt == nil || get.Revision.ID != got.Revision.ID {
		t.Fatalf("saved read: %+v %v", get, err)
	}
	stale := in
	stale.IdempotencyKey = "stale"
	_, err = r.ApplyProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, stale)
	f := assertFault(t, err, "backend_proposal_version_conflict")
	if f.CurrentVersion != 2 || f.Details["draftRevisionId"] != got.Revision.ID {
		t.Fatalf("lost current conflict pins: %+v", f)
	}
	in.Commands[0].Nullable = new(true)
	_, err = r.ApplyProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, in)
	assertFault(t, err, "backend_idempotency_conflict")
	_, err = r.PreviewProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, proposalPreviewInput(get, proposalNullable(ids, "required", false)))
	assertFault(t, err, "backend_proposal_invalid")
}

func TestProposalApplyPreparedRace(t *testing.T) {
	for _, sameKey := range []bool{false, true} {
		t.Run(map[bool]string{false: "competing", true: "same-key"}[sameKey], func(t *testing.T) {
			r, detail, _, ids := proposalEvaluationFixture(t, "sqlite")
			one := proposalApplyInput(t, r, detail, "one", proposalFK(ids))
			two := one
			if !sameKey {
				two.IdempotencyKey = "two"
			}
			results := make(chan *ProposalApplyResult, 2)
			errors := make(chan error, 2)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for _, in := range []ApplyProposalInput{one, two} {
				wg.Go(func() {
					<-start
					got, err := r.ApplyProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, in)
					if err != nil {
						errors <- err
					} else {
						results <- got
					}
				})
			}
			close(start)
			wg.Wait()
			close(results)
			close(errors)
			wantSuccess, wantFailures := 1, 1
			if sameKey {
				wantSuccess, wantFailures = 2, 0
			}
			if len(results) != wantSuccess || len(errors) != wantFailures {
				t.Fatalf("race results: %d successes / %d errors", len(results), len(errors))
			}
			var rid string
			for result := range results {
				if rid == "" {
					rid = result.Revision.ID
				}
				if rid != result.Revision.ID {
					t.Fatal("same request published multiple revisions")
				}
			}
			for err := range errors {
				assertFault(t, err, "backend_proposal_version_conflict")
			}
			var n int
			if err := r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_proposal_revisions`).Scan(&n); err != nil || n != 2 {
				t.Fatalf("race revisions: %d %v", n, err)
			}
			if err := r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_command_receipts WHERE scope LIKE 'proposal-apply:%'`).Scan(&n); err != nil || n != 1 {
				t.Fatalf("race receipts: %d %v", n, err)
			}
		})
	}
}

func TestProposalRollbackOnFault(t *testing.T) {
	t.Parallel()
	r, detail, _, ids := proposalEvaluationFixture(t, "postgresql")
	in := proposalApplyInput(t, r, detail, "fault", proposalNullable(ids, "required", false))
	before := proposalSourceBytes(t, r)
	if _, err := r.db.W.ExecContext(t.Context(), `CREATE TRIGGER fail_proposal_apply BEFORE INSERT ON backend_command_receipts WHEN NEW.scope LIKE 'proposal-apply:%' BEGIN SELECT RAISE(ABORT, 'receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ApplyProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, in); err == nil {
		t.Fatal("fault accepted")
	}
	get, err := r.GetProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, GetProposalInput{})
	if err != nil || get.Proposal.Version != 1 || get.Revision.ID != detail.Revision.ID || len(get.History) != 1 || get.LastApplyReceipt != nil {
		t.Fatalf("partial apply: %+v %v", get, err)
	}
	assertProposalSourceBytes(t, r, before)
	if _, err := r.db.W.ExecContext(t.Context(), `DROP TRIGGER fail_proposal_apply`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ApplyProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, in); err != nil {
		t.Fatal(err)
	}
}

func TestProposalApplyReplayAfterRestart(t *testing.T) {
	t.Parallel()
	r, detail, _, ids := proposalEvaluationFixture(t, "sqlite")
	one := proposalApplyInput(t, r, detail, "first", proposalFK(ids))
	first, err := r.ApplyProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, one)
	if err != nil {
		t.Fatal(err)
	}
	get, err := r.GetProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, GetProposalInput{})
	if err != nil {
		t.Fatal(err)
	}
	c := proposalFK(ids)
	c.CommandID = "second"
	c.Action = "update"
	c.Name = ""
	c.TableID = ""
	c.ConstraintID = first.Changes[0].GeneratedIDs["constraintId"]
	c.DeleteAction = "cascade"
	two := proposalApplyInput(t, r, get, "second", c)
	if _, err := r.ApplyProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, two); err != nil {
		t.Fatal(err)
	}
	var receipt string
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT response FROM backend_command_receipts WHERE scope=? AND key=?`, "proposal-apply:"+detail.Proposal.ProjectID+":"+detail.Proposal.ID, "first").Scan(&receipt); err != nil {
		t.Fatal(err)
	}
	db := r.db
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), db.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.Migrate(t.Context(), slog.Default()); err != nil {
		t.Fatal(err)
	}
	r = NewRepo(reopened)
	// Exhaustion and a used command ID must not hide an acknowledged receipt.
	if _, err := reopened.W.ExecContext(t.Context(), `UPDATE backend_proposals SET version=? WHERE id=?`, math.MaxInt64, detail.Proposal.ID); err != nil {
		t.Fatal(err)
	}
	replayed, err := r.ApplyProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, one)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(replayed)
	if err != nil || string(b) != receipt || replayed.Proposal.Version != 2 {
		t.Fatalf("replay changed: %s / %s %v", receipt, b, err)
	}
	one.CandidateHash = "0000000000000000000000000000000000000000000000000000000000000000"
	_, err = r.ApplyProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, one)
	assertFault(t, err, "backend_idempotency_conflict")
}

func TestProposalApplyHashDraftAndVersionPrecision(t *testing.T) {
	t.Parallel()
	r, detail, _, ids := proposalEvaluationFixture(t, "postgresql")
	in := proposalApplyInput(t, r, detail, "hash", proposalNullable(ids, "required", false))
	wrong := in
	wrong.CandidateHash = "0000000000000000000000000000000000000000000000000000000000000000"
	_, err := r.ApplyProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, wrong)
	assertFault(t, err, "backend_proposal_preview_conflict")
	wrong = in
	wrong.DraftRevisionID = detail.Proposal.BaseRevisionID
	_, err = r.ApplyProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, wrong)
	assertFault(t, err, "backend_proposal_preview_conflict")
	const exact int64 = 9007199254740993
	if _, err := r.db.W.ExecContext(t.Context(), `UPDATE backend_proposals SET version=? WHERE id=?`, exact, detail.Proposal.ID); err != nil {
		t.Fatal(err)
	}
	_, err = r.ApplyProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, in)
	f := assertFault(t, err, "backend_proposal_version_conflict")
	b, err := json.Marshal(f)
	if err != nil || f.CurrentVersion != exact || !strings.Contains(string(b), `"currentVersion":9007199254740993`) {
		t.Fatalf("rounded CAS: %+v %s %v", f, b, err)
	}
	get, err := r.GetProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, GetProposalInput{})
	if err != nil {
		t.Fatal(err)
	}
	valid := proposalApplyInput(t, r, get, "precise", proposalNullable(ids, "required", false))
	got, err := r.ApplyProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, valid)
	if err != nil || got.Proposal.Version != exact+1 {
		t.Fatalf("int64 increment: %+v %v", got, err)
	}
}

func TestProposalPreviewStrictWireAndExactVersion(t *testing.T) {
	valid := `{"expectedVersion":9007199254740993,"draftRevisionId":"018effb2-731a-7edb-b633-1387b184b21d","commands":[{"type":"alter_column","commandId":"c","reason":"Required","columnId":"018effb2-731a-7edb-b633-1387b184b21d","nullable":false}]}`
	var in PreviewProposalInput
	if err := json.Unmarshal([]byte(valid), &in); err != nil || in.ExpectedVersion != 9007199254740993 {
		t.Fatalf("wire version rounded: %+v %v", in, err)
	}
	for _, raw := range []string{`null`, `{}`, strings.Replace(valid, "9007199254740993", "null", 1), strings.Replace(valid, "9007199254740993", "1.5", 1), strings.Replace(valid, "9007199254740993", "9223372036854775808", 1), valid[:len(valid)-1] + `,"expectedVersion":1}`, valid[:len(valid)-1] + `,"status":"ready"}`, strings.Replace(valid, `"commands":[`, `"commands":null,"discard":[`, 1)} {
		if err := json.Unmarshal([]byte(raw), &in); err == nil {
			t.Fatalf("accepted malformed preview: %s", raw)
		}
	}
	unsupported := strings.Replace(valid, "alter_column", "delete_node", 1)
	err := json.Unmarshal([]byte(unsupported), &in)
	f, ok := errors.AsType[*FaultError](err)
	if !ok || f.Status != 422 || f.Code != "backend_unsupported_scope" {
		t.Fatalf("unsupported command lost its code: %v", err)
	}
}

func TestProposalBaseOutdatedIsolation(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	out, ids := commitRelationalFixture(t, r, "postgresql", "v1")
	detail, err := r.CreateProposal(t.Context(), out.Project.ID, proposalCreateInput(out, ids, "historical-preview"))
	if err != nil {
		t.Fatal(err)
	}
	in := proposalPreviewInput(detail, proposalNullable(ids, "require", false))
	before, err := r.PreviewProposal(t.Context(), out.Project.ID, detail.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	latest, _ := databaseCommitV2(t, r, out, ids, "postgresql")
	after, err := r.PreviewProposal(t.Context(), out.Project.ID, detail.Proposal.ID, in)
	if err != nil || *before.CandidateHash != *after.CandidateHash || *before.CandidateGraphHash != *after.CandidateGraphHash {
		t.Fatalf("source head reinterpreted proposal: %+v %v", after, err)
	}
	result, err := r.ApplyProposal(t.Context(), out.Project.ID, detail.Proposal.ID, ApplyProposalInput{ExpectedVersion: in.ExpectedVersion, DraftRevisionID: in.DraftRevisionID, Commands: in.Commands, CandidateHash: *before.CandidateHash, IdempotencyKey: "historical-save"})
	if err != nil || result.CandidateGraphHash != *before.CandidateGraphHash {
		t.Fatalf("preview/apply graph mismatch: %+v %v", result, err)
	}
	read, err := r.GetProposal(t.Context(), out.Project.ID, detail.Proposal.ID, GetProposalInput{})
	if err != nil || !read.BaseOutdated || read.CurrentSourceRevisionID != latest.Revision.ID || read.Proposal.BaseRevisionID != out.Revision.ID || read.EffectiveGraphHash != result.CandidateGraphHash {
		t.Fatalf("baseline warning: %+v %v", read, err)
	}
}

func TestProposalReadHistoricalHashAndCriteriaOnlyChange(t *testing.T) {
	t.Parallel()
	r, detail, _, ids := proposalEvaluationFixture(t, "sqlite")
	one := proposalApplyInput(t, r, detail, "one", proposalNullable(ids, "require", false))
	first, err := r.ApplyProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, one)
	if err != nil {
		t.Fatal(err)
	}
	detail, err = r.GetProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, GetProposalInput{})
	if err != nil {
		t.Fatal(err)
	}
	set := ProposalCommand{Type: "set_criteria", CommandID: "recommend", Reason: "Review rollout", Criteria: []ProposalCriterionInput{{Key: "rollout", Kind: "migration_plan", TargetIDs: []string{ids["table:orders"]}, Description: "Check rollback"}}}
	two := proposalApplyInput(t, r, detail, "two", set)
	second, err := r.ApplyProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, two)
	if err != nil || second.Revision.SemanticHash == first.Revision.SemanticHash || second.CandidateGraphHash != first.CandidateGraphHash {
		t.Fatalf("criteria changed graph: %+v %v", second, err)
	}
	old, err := r.GetProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, GetProposalInput{ProposalRevisionID: first.Revision.ID})
	if err != nil || old.Revision.ID != first.Revision.ID || old.EffectiveGraphHash != first.CandidateGraphHash || len(old.Revision.Criteria) != 3 {
		t.Fatalf("historical graph pins changed: %+v %v", old, err)
	}
	current, err := r.GetProposal(t.Context(), detail.Proposal.ProjectID, detail.Proposal.ID, GetProposalInput{})
	if err != nil || current.EffectiveGraphHash != old.EffectiveGraphHash || len(current.Revision.Criteria) != 4 || current.Revision.ID != second.Revision.ID {
		t.Fatalf("criteria read: %+v %v", current, err)
	}
}

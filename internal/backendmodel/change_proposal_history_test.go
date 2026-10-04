package backendmodel

import (
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"slices"
	"sync"
	"testing"
	"uuid"

	"github.com/yashok111/mocker/internal/store"
)

func saveChange(t *testing.T, r *Repo, d *ChangeProposalDetail, key string, commands ...ChangeProposalCommand) (*ChangeProposalDetail, ApplyChangeProposalInput) {
	t.Helper()
	in := PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: commands}
	preview, err := r.PreviewChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if preview.CandidateHash == nil {
		t.Fatalf("invalid desired graph: %+v", preview.Diagnostics)
	}
	apply := ApplyChangeProposalInput{ExpectedVersion: in.ExpectedVersion, ProposalRevisionID: in.ProposalRevisionID, Commands: commands, CandidateHash: *preview.CandidateHash, IdempotencyKey: key}
	out, err := r.ApplyChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, apply)
	if err != nil {
		t.Fatal(err)
	}
	return &ChangeProposalDetail{Proposal: out.Proposal, Revision: out.Revision}, apply
}
func changeReadSnapshot(t *testing.T, r *Repo, d *ChangeProposalDetail) *ChangeEvaluationSnapshot {
	t.Helper()
	source, err := loadComposedBase(t.Context(), r.db.R, d.Proposal.ProjectID, d.Revision.BaseRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := newChangeEvaluation(source, d.Revision, map[string]ChangeObjectIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := evaluation.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestChangeProposalCommandLedgerRestoreReplay(t *testing.T) {
	r, base, initial := changeFixture(t)
	id := uuid.NewV7().String()
	create := changeCommand(t, "create_node", fmt.Sprintf(`"id":%q,"kind":"service","name":"One","parentId":null,"attributes":{}`, id))
	d, firstInput := saveChange(t, r, initial, "first", create)
	firstReceipt, err := r.ApplyChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, firstInput)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, _ := json.Marshal(firstReceipt)
	noOp := changeCommand(t, "rename", fmt.Sprintf(`"recordType":"node","id":%q,"name":"One"`, id))
	overwritten := changeCommand(t, "rename", fmt.Sprintf(`"recordType":"node","id":%q,"name":"Two"`, id))
	final := changeCommand(t, "rename", fmt.Sprintf(`"recordType":"node","id":%q,"name":"Three"`, id))
	criteria := changeCommand(t, "set_criteria", `"criteria":[]`)
	d, _ = saveChange(t, r, d, "second", noOp, overwritten, final, criteria)
	var batchRaw string
	if err = r.db.R.QueryRowContext(t.Context(), `SELECT commands FROM backend_change_proposal_batches WHERE revision_id=?`, d.Revision.ID).Scan(&batchRaw); err != nil {
		t.Fatal(err)
	}
	var accepted []ChangeProposalCommand
	if err = json.Unmarshal([]byte(batchRaw), &accepted); err != nil || len(accepted) != 4 {
		t.Fatalf("accepted history incomplete: %s %v", batchRaw, err)
	}
	restore := RestoreChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, RestoreRevisionID: initial.Revision.ID, IdempotencyKey: "restore"}
	restored, err := r.RestoreChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, restore)
	if err != nil {
		t.Fatal(err)
	}
	path := r.db.Path()
	if err = r.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	r = NewRepo(reopened)
	d = &ChangeProposalDetail{Proposal: restored.Proposal, Revision: restored.Revision}
	for _, c := range []ChangeProposalCommand{create, noOp, overwritten, final, criteria} {
		_, err = r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{c}})
		assertFault(t, err, "backend_change_command_conflict")
	}
	create.CommandID = uuid.NewV7().String()
	_, err = r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{create}})
	assertFault(t, err, "backend_change_identity_conflict")
	replay, err := r.ApplyChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, firstInput)
	if err != nil {
		t.Fatal(err)
	}
	bytes, _ := json.Marshal(replay)
	if string(bytes) != string(firstBytes) {
		t.Fatal("original apply receipt changed after restore")
	}
	restoreReplay, err := r.RestoreChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, restore)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(restored)
	b, _ := json.Marshal(restoreReplay)
	if string(a) != string(b) {
		t.Fatal("restore receipt changed")
	}
	old, err := r.GetChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, GetChangeProposalInput{ProposalRevisionID: initial.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(changeReadSnapshot(t, r, old).Identities) != len(changeReadSnapshot(t, r, initial).Identities) {
		t.Fatal("later identity ledger leaked into historical snapshot")
	}
}

func TestChangeProposalFinalReferenceRepairAndOrigins(t *testing.T) {
	r, _, d := changeFixture(t)
	parent, child, edge := uuid.NewV7().String(), uuid.NewV7().String(), uuid.NewV7().String()
	createParent := changeCommand(t, "create_node", fmt.Sprintf(`"id":%q,"kind":"service","name":"Parent","parentId":null,"attributes":{}`, parent))
	createChild := changeCommand(t, "create_node", fmt.Sprintf(`"id":%q,"kind":"module","name":"Child","parentId":%q,"attributes":{}`, child, parent))
	createEdge := changeCommand(t, "upsert_edge", fmt.Sprintf(`"id":%q,"kind":"contains","from":%q,"to":%q,"attributes":{}`, edge, parent, child))
	d, _ = saveChange(t, r, d, "hierarchy", createChild, createEdge, createParent)
	bad := changeCommand(t, "remove_node", fmt.Sprintf(`"id":%q`, parent))
	preview, err := r.PreviewChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{bad}})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Diagnostics) == 0 || preview.SemanticHash != nil || preview.CandidateHash != nil {
		t.Fatal("dangling final references accepted")
	}
	update := changeCommand(t, "update_node", fmt.Sprintf(`"id":%q,"update":{"kind":"module","group":"parent","parentId":null}`, child))
	removeEdge := changeCommand(t, "remove_edge", fmt.Sprintf(`"id":%q`, edge))
	criterion := changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{map[string]any{"key": "removed", "kind": "object_absent", "required": true, "description": "Parent is removed", "recordType": "node", "id": parent, "objectKind": "service"}}})
	d, _ = saveChange(t, r, d, "repair", bad, update, removeEdge, criterion)
	snapshot := changeReadSnapshot(t, r, d)
	if slices.ContainsFunc(snapshot.Nodes, func(n Node) bool { return n.ID == parent }) {
		t.Fatal("removed parent remained live")
	}
	for _, origin := range snapshot.Origins {
		if origin.ID == child && origin.Origin.Kind != "intent" {
			t.Fatal("created property became source proof")
		}
	}
}

func TestChangeProposalAtomicRollbackAndConcurrentCAS(t *testing.T) {
	r, _, d := changeFixture(t)
	command := changeCommand(t, "set_criteria", `"criteria":[]`)
	commands := []ChangeProposalCommand{changeCreateNode(t, uuid.NewV7().String(), "service", nil, map[string]any{}), command}
	preview, err := r.PreviewChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: 1, ProposalRevisionID: d.Revision.ID, Commands: commands})
	if err != nil {
		t.Fatal(err)
	}
	input := ApplyChangeProposalInput{ExpectedVersion: 1, ProposalRevisionID: d.Revision.ID, Commands: commands, CandidateHash: *preview.CandidateHash, IdempotencyKey: "atomic"}
	err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `CREATE TRIGGER fail_change_event BEFORE INSERT ON backend_change_proposal_events WHEN NEW.version>1 BEGIN SELECT RAISE(ABORT,'injected event failure'); END;`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.ApplyChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, input)
	if err == nil {
		t.Fatal("injected failure not reached")
	}
	for table, want := range map[string]int{"backend_change_proposal_revisions": 1, "backend_change_proposal_batches": 1, "backend_change_proposal_commands": 0, "backend_change_proposal_events": 1, "backend_change_proposal_identities": 0} {
		var count int
		if err = r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != want {
			t.Fatalf("rollback %s=%d want%d err%v", table, count, want, err)
		}
	}
	err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `DROP TRIGGER fail_change_event`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for _, key := range []string{"winner-a", "winner-b"} {
		in := input
		in.IdempotencyKey = key
		wg.Go(func() {
			_, err := r.ApplyChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, in)
			outcomes <- err
		})
	}
	wg.Wait()
	close(outcomes)
	success := 0
	for err := range outcomes {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("CAS winners=%d", success)
	}
}

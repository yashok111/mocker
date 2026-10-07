package backendmodel

import (
	"database/sql"
	"errors"
	"testing"
	"uuid"

	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testkit"
)

func TestChangeRebaseRetainedArtifactBindingRequiresLiveTarget(t *testing.T) {
	t.Parallel()
	service, old, ids, scenario := artifactServiceFixture(t)
	base, _ := upgradeEventsArtifactFixture(t, service, old)
	r := service.repo
	d, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "rebase artifacts", BaseRevisionID: base.Revision.ID, IdempotencyKey: "proposal"})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewV7().String()
	pin := scenarioSet(base, ids, scenario).Commands[0]
	bindings := []EditorBindingInput{{Selector: EditorSelector{Kind: "participant", ParticipantID: "p"}, SourceNodeIDs: []string{id}}}
	d, _ = saveChange(t, r, d, "pin", changeCreateNode(t, id, "service", nil, map[string]any{}), changeMapCommand(t, "set_artifact_pin", map[string]any{"artifact": pin.Artifact, "revisionId": pin.RevisionID, "editorBindings": bindings}))
	remove := changeMapCommand(t, "remove_node", map[string]any{"id": id})
	input := PreviewChangeProposalRebaseInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, NewBaseRevisionID: base.Revision.ID, RepairCommands: []ChangeProposalCommand{remove}}
	preview, err := r.PreviewChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if preview.CandidateHash != nil || len(preview.Diagnostics) == 0 {
		t.Fatal("retained artifact binding accepted a removed target")
	}
	input.RepairCommands = append(input.RepairCommands, changeMapCommand(t, "remove_artifact_pin", map[string]any{"artifact": pin.Artifact}))
	repaired, err := r.PreviewChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.CandidateHash == nil {
		t.Fatalf("ordered real repairs did not fix binding: %+v", repaired)
	}
}

func TestChangeRebaseRejectsBeforeUnreservedDraftDecode(t *testing.T) {
	t.Parallel()
	r, base, d := changeFixture(t)
	// A valid JSON number outside float64 exposes generic decoding before the
	// memory gate. This corrupt sentinel lives only in this isolated test DB.
	// Store27 (48dce80, B6.3) seals proposal revision payloads; the sentinel is
	// seeded through the Store26 fixture rebuild + production migration, which
	// copies the raw bytes without decoding them.
	if err := testkit.EditLegacyBackendFixture(t.Context(), r.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), `DROP TRIGGER backend_change_revision_no_update`); err != nil {
			return err
		}
		_, err := tx.ExecContext(t.Context(), `UPDATE backend_change_proposal_revisions SET document=json_set(document,'$.decodeSentinel',json('1e1000')) WHERE id=?`, d.Revision.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var reservation *store.TransientReservation
	if err := r.db.Write(t.Context(), func(*sql.Tx) error {
		var ok bool
		reservation, ok = r.db.ReserveTransient("backend:"+base.Project.ID, MaxProjectStagingBytes, MaxProjectStagingBytes)
		if !ok {
			t.Fatal("reservation fixture failed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	defer reservation.Release()
	_, err := r.PreviewChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, PreviewChangeProposalRebaseInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, NewBaseRevisionID: base.Revision.ID})
	fault, ok := errors.AsType[*FaultError](err)
	if !ok || fault.Status != 413 {
		t.Fatalf("draft decoded before reservation rejection: %v", err)
	}
}

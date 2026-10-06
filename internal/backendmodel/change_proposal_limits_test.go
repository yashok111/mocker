package backendmodel

import (
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
	"time"
	"uuid"
)

func TestChangeProposalDraftQuotaReplayAndInputBounds(t *testing.T) {
	r, _, d := changeFixture(t)
	command := changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{}})
	saved, input := saveChange(t, r, d, "quota-receipt", command)
	original, err := r.ApplyChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	originalJSON, _ := json.Marshal(original)
	err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		p := saved.Proposal
		rev := saved.Revision
		for i := 2; i < MaxChangeProposalRevisions; i++ {
			previous := rev.ID
			rev.ID = uuid.NewV7().String()
			rev.ParentRevisionID = new(previous)
			rev.AcceptedBatchRevisionID = rev.ID
			rev.CreatedAt = time.Now().UTC()
			p.Version++
			p.CurrentDraftRevisionID = rev.ID
			p.UpdatedAt = rev.CreatedAt
			if err := persistChangeRevision(t.Context(), tx, p, rev, "restore", d.Revision.ID, []ChangeProposalCommand{}, nil); err != nil {
				return err
			}
		}
		return updateChangeAggregate(t.Context(), tx, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := r.GetChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, GetChangeProposalInput{})
	if err != nil {
		t.Fatal(err)
	}
	next := changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{}})
	preview, err := r.PreviewChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: current.Proposal.Version, ProposalRevisionID: current.Revision.ID, Commands: []ChangeProposalCommand{next}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.ApplyChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, ApplyChangeProposalInput{ExpectedVersion: current.Proposal.Version, ProposalRevisionID: current.Revision.ID, Commands: []ChangeProposalCommand{next}, CandidateHash: *preview.CandidateHash, IdempotencyKey: "quota-rejected"})
	assertFault(t, err, "backend_change_quota_exceeded")
	replay, err := r.ApplyChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	replayJSON, _ := json.Marshal(replay)
	if string(originalJSON) != string(replayJSON) {
		t.Fatal("quota prevented exact original replay")
	}
	var count int
	if err = r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_change_proposal_revisions_documents`).Scan(&count); err != nil || count != MaxChangeProposalRevisions {
		t.Fatalf("quota refusal persisted %d drafts: %v", count, err)
	}
	commands := make([]ChangeProposalCommand, MaxChangeProposalCommands+1)
	for i := range commands {
		commands[i] = changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{}})
	}
	if err = validateChangeCommands(commands); err == nil {
		t.Fatal("101 command batch admitted")
	}
	commands = commands[:100]
	for i := range commands {
		commands[i].Reason = strings.Repeat("r", 4096)
		commands[i].Criteria = []ChangeCriterion{{Key: fmt.Sprint(i), Kind: "runtime_check", Description: strings.Repeat("d", 4096), TargetIDs: []string{}}}
		commands[i].Criteria = append(commands[i].Criteria, ChangeCriterion{Key: "second", Kind: "runtime_check", Description: strings.Repeat("x", 4096), TargetIDs: []string{}})
	}
	if err = validateChangeCommands(commands); err == nil {
		t.Fatal("batch larger than1MiB admitted")
	}
}

func TestChangeProposalProjectQuotaAndVersionExhaustion(t *testing.T) {
	r, base, d := changeFixture(t)
	err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		for i := 1; i < MaxChangeProposals; i++ {
			p := d.Proposal
			p.ID = uuid.NewV7().String()
			p.CurrentDraftRevisionID = uuid.NewV7().String()
			rev := d.Revision
			rev.ID, rev.ProposalID, rev.AcceptedBatchRevisionID = p.CurrentDraftRevisionID, p.ID, p.CurrentDraftRevisionID
			stamp := p.CreatedAt.Format(time.RFC3339Nano)
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_change_proposals (`+changeProposalColumns+`) VALUES(?,?,?,?,?,?,?,?,?,NULL)`, p.ID, p.ProjectID, p.Name, p.Version, p.Status, p.CurrentDraftRevisionID, p.CurrentDraftHash, stamp, stamp); err != nil {
				return err
			}
			if err := persistChangeRevision(t.Context(), tx, p, rev, "create", "", []ChangeProposalCommand{}, nil); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Overflow", BaseRevisionID: base.Revision.ID, IdempotencyKey: "overflow"})
	assertFault(t, err, "backend_change_quota_exceeded")
	if _, err = r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Desired graph", BaseRevisionID: base.Revision.ID, IdempotencyKey: "create-change"}); err != nil {
		t.Fatal("create replay failed at project quota", err)
	}
	err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE backend_change_proposals SET version=9223372036854775807 WHERE id=?`, d.Proposal.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: 9223372036854775807, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{}})}})
	assertFault(t, err, "backend_change_version_conflict")
	if _, err = r.ListChangeProposals(t.Context(), base.Project.ID, ChangeProposalListInput{}); err != nil {
		t.Fatal("exhausted version must remain readable", err)
	}
}

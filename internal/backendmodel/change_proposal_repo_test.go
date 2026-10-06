package backendmodel

import (
	"encoding/json/v2"
	"fmt"
	"testing"
	"uuid"
)

func changeFixture(t *testing.T) (*Repo, *ImportCommitResult, *ChangeProposalDetail) {
	t.Helper()
	r, _ := testRepo(t)
	p := createProject(t, r, "change-source")
	_, base := commitSource6Fixture(t, r, p, source6Input(t, p))
	detail, err := r.CreateChangeProposal(t.Context(), p.ID, CreateChangeProposalInput{Name: "Desired graph", BaseRevisionID: base.Revision.ID, IdempotencyKey: "create-change"})
	if err != nil {
		t.Fatal(err)
	}
	return r, base, detail
}

func changeCommand(t *testing.T, typ, payload string) ChangeProposalCommand {
	t.Helper()
	var c ChangeProposalCommand
	raw := fmt.Sprintf(`{"type":%q,"commandId":%q,"reason":"Review desired behavior",%s}`, typ, uuid.NewV7().String(), payload)
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestChangeProposalCreateReplayAndSemanticCandidate(t *testing.T) {
	t.Parallel()
	r, base, d := changeFixture(t)
	if d.Proposal.Version != 1 || d.Revision.BaseRevisionID != base.Revision.ID || d.Revision.DocumentVersion != "proposal-graph-v1" {
		t.Fatalf("wrong draft: %+v", d)
	}
	in := CreateChangeProposalInput{Name: "Desired graph", BaseRevisionID: base.Revision.ID, IdempotencyKey: "create-change"}
	replay, err := r.CreateChangeProposal(t.Context(), base.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(d)
	b, _ := json.Marshal(replay)
	if string(a) != string(b) {
		t.Fatal("create receipt changed")
	}
	second, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Equivalent", BaseRevisionID: base.Revision.ID, IdempotencyKey: "second"})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewV7().String()
	c := changeCommand(t, "create_node", fmt.Sprintf(`"id":%q,"kind":"service","name":"Planned","parentId":null,"attributes":{}`, id))
	preview := func(d *ChangeProposalDetail, c ChangeProposalCommand) *ChangeProposalCandidate {
		t.Helper()
		v, err := r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{c}})
		if err != nil {
			t.Fatal(err)
		}
		if len(v.Diagnostics) > 0 || v.SemanticHash == nil || v.CandidateHash == nil {
			t.Fatalf("invalid preview: %+v", v)
		}
		return v
	}
	v1 := preview(d, c)
	c.CommandID = uuid.NewV7().String()
	c.Reason = "Different provenance"
	v2 := preview(second, c)
	if *v1.SemanticHash != *v2.SemanticHash || *v1.CandidateHash == *v2.CandidateHash {
		t.Fatal("semantic and candidate domains are conflated")
	}
	var count int
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_change_proposal_revisions_documents`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("preview persisted: %d %v", count, err)
	}
}

func TestChangeProposalRejectsMixedAndMissingCommandFields(t *testing.T) {
	id := uuid.NewV7().String()
	for _, payload := range []string{
		`"type":"remove_node","id":"` + id + `","cascade":true`,
		`"type":"create_node","id":"` + id + `","kind":"service","name":"X","attributes":{}`,
		`"type":"map_identity","target":{"kind":"intent_identity","recordType":"node","id":"` + id + `"},"newExternalKey":"x"`,
		`"type":"set_criteria","criteria":null`,
	} {
		var c ChangeProposalCommand
		raw := fmt.Sprintf(`{"commandId":%q,"reason":"x",%s}`, uuid.NewV7().String(), payload)
		if json.Unmarshal([]byte(raw), &c) == nil {
			t.Fatalf("accepted malformed %s", raw)
		}
	}
}

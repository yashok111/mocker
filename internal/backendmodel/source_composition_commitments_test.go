package backendmodel

import (
	"encoding/json/v2"
	"testing"
)

func TestSource6CandidateBindsLegacyDecisionBodiesAndBatchOrder(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*composedCandidate)
	}{
		{"reason", func(c *composedCandidate) { c.LegacyDecisions[0].Deletion.Reason = "changed reason" }},
		{"expected_id", func(c *composedCandidate) { c.LegacyDecisions[0].Deletion.ExpectedID = sourceContractOther }},
		{"batch_order", func(c *composedCandidate) {
			c.BatchCommitments[0], c.BatchCommitments[1] = c.BatchCommitments[1], c.BatchCommitments[0]
		}},
		{"request_hash", func(c *composedCandidate) { c.BatchCommitments[0].RequestHash = fixtureHash }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			graph := sourceContractHashGraph(t)
			c := &composedCandidate{Source: graph, Decisions: []SourceDecision{}, LegacyDecisions: []ImportCommand{{Op: "delete_assertion", Deletion: &ImportDeletion{RecordType: "node", ExternalKey: "key", ExpectedID: sourceContractNode, Reason: "original"}}}, BatchCommitments: []SourceBatchCommitment{{AcceptedVersion: 2, BatchID: "a", PayloadHash: fixtureHash, RequestHash: graph.Assertions[0].AssertionHash}, {AcceptedVersion: 3, BatchID: "b", PayloadHash: fixtureHash, RequestHash: graph.Assertions[0].AssertionHash}}}
			s := &ImportSession{ProjectID: sourceContractRepo, BaseRevisionID: sourceContractBase, Version: 4, Mode: "composed", Profile: ComposedProfile, SourceScope: &SourceScope{Kind: "add_repository"}, ScopeStatus: &SourceScopeStatus{Status: "complete", Gaps: []string{}}, SyncPolicy: WholeSourcePolicy}
			before, err := source6CandidateHash(s, c)
			if err != nil {
				t.Fatal(err)
			}
			semanticBefore, err := source6SemanticHash(graph)
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(c)
			after, err := source6CandidateHash(s, c)
			if err != nil || before == after {
				t.Fatalf("candidate omitted %s: %v", tc.name, err)
			}
			semanticAfter, err := source6SemanticHash(graph)
			if err != nil || semanticBefore != semanticAfter {
				t.Fatal("command metadata entered semantic domain")
			}
		})
	}
}

func TestSource6RevisionPersistsBatchCommitments(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "commitments")
	s, out := commitSource6Fixture(t, r, p, source6Input(t, p))
	var raw string
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT document FROM backend_revision_decisions_documents WHERE revision_id=?`, out.Revision.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var document struct {
		Version string                  `json:"documentVersion"`
		Batches []SourceBatchCommitment `json:"batches"`
	}
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatal(err)
	}
	if document.Version != "source-decisions-v1" || len(document.Batches) != 1 {
		t.Fatalf("durable commitments=%s", raw)
	}
	stored, err := loadSourceBatchCommitments(t.Context(), r.db.R, s.ID)
	if err != nil || len(stored) != 1 || document.Batches[0] != stored[0] {
		t.Fatalf("commitment differs from accepted receipt input: %+v %v", stored, err)
	}
}

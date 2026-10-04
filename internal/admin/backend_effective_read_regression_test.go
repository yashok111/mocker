package admin

import (
	"bytes"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendEffectiveNativeIdentityAndEdgeNamesOperationSchemas(t *testing.T) {
	s := loopbackTestServer(t, nil)
	var project backendmodel.Project
	b41Call(t, s, "POST", "/api/backend-projects", backendmodel.CreateInput{Name: "Effective schema regression", IdempotencyKey: "schema-project"}, 201, &project)
	base := "/api/backend-projects/" + project.ID
	begin := transportImportFixture(project)
	begin.Mode = "composed"
	begin.Profile = backendmodel.ComposedProfile
	begin.SourceScope = &backendmodel.SourceScope{Kind: "add_repository"}
	begin.ScopeStatus = &backendmodel.SourceScopeStatus{Status: "complete", Gaps: []string{}}
	begin.SyncPolicy = backendmodel.WholeSourcePolicy
	begin.Manifest.Provider.Profiles = []string{backendmodel.GraphProfile, backendmodel.RelationalProfile, backendmodel.RuntimeProfile, backendmodel.LineageProfile, backendmodel.EventsProfile, backendmodel.ComposedProfile}
	var session backendmodel.ImportSession
	b41Call(t, s, "POST", base+"/imports", begin, 200, &session)
	cmds := make([]backendmodel.ImportCommand, 0, 6)
	for _, raw := range []string{`{"op":"upsert_node","node":{"externalKey":"system","kind":"system","name":"System","attributes":{},"evidenceKeys":[]}}`, `{"op":"upsert_node","node":{"externalKey":"child","kind":"service","name":"Child","parentRef":{"localKey":"system"},"attributes":{},"evidenceKeys":[]}}`, `{"op":"upsert_edge","edge":{"externalKey":"edge","kind":"contains","fromRef":{"localKey":"system"},"toRef":{"localKey":"child"},"attributes":{},"evidenceKeys":[]}}`} {
		var command backendmodel.ImportCommand
		if err := json.Unmarshal([]byte(raw), &command); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, command)
	}
	for i, key := range []string{"system", "child", "edge"} {
		typ := "node"
		if cmds[i].Edge != nil {
			typ = "edge"
			cmds[i].Edge.EvidenceKeys = []string{"proof-" + key}
		} else {
			cmds[i].Node.EvidenceKeys = []string{"proof-" + key}
		}
		evidence := backendmodel.ImportCommand{Op: "upsert_evidence", Evidence: &backendmodel.ImportEvidence{ExternalKey: "proof-" + key, SubjectType: typ, SubjectKey: key, Method: "ast", Status: "explicit", Explanation: "Static source declaration", Source: backendmodel.EvidenceSource{RepositoryID: session.RepositoryID, SnapshotID: session.SnapshotID, File: "main.go", ContentHash: strings.Repeat("a", 64), StartLine: new(int64(1)), EndLine: new(int64(1))}}}
		cmds = append(cmds, evidence)
	}
	hash, err := backendmodel.ImportBatchHash(cmds)
	if err != nil {
		t.Fatal(err)
	}
	var batch backendmodel.BatchReceipt
	b41Call(t, s, "PUT", base+"/imports/"+session.ID+"/batches/records", backendmodel.ImportBatchInput{ExpectedImportVersion: session.Version, PayloadHash: hash, Commands: cmds}, 200, &batch)
	var preview backendmodel.ImportPreview
	b41Call(t, s, "POST", base+"/imports/"+session.ID+"/preview", backendmodel.PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: project.CurrentRevisionID}, 200, &preview)
	if preview.CandidateHash == nil {
		t.Fatalf("candidate %+v", preview.Diagnostics)
	}
	var committed backendmodel.ImportCommitResult
	b41Call(t, s, "POST", base+"/imports/"+session.ID+"/commit", backendmodel.CommitImportInput{ExpectedVersion: project.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "schema-commit"}, 200, &committed)
	edgeID := ""
	for _, typ := range []string{"nodes", "edges"} {
		var page backendmodel.GraphPage
		b41Call(t, s, "POST", base+"/graph/query", backendmodel.GraphQueryInput{RevisionID: committed.Revision.ID, RecordType: typ}, 200, &page)
		if len(page.Identities) == 0 {
			t.Fatalf("missing %s identity", typ)
		}
		for _, identity := range page.Identities {
			if identity.Origin.Selector.Kind != "source_identity" {
				t.Fatalf("invalid identity origin: %+v", identity)
			}
		}
		if typ == "edges" {
			edgeID = page.Edges[0].ID
		}
	}
	var draft backendmodel.ChangeProposalDetail
	b41Call(t, s, "POST", base+"/change-proposals", backendmodel.CreateChangeProposalInput{Name: "Rename edge", BaseRevisionID: committed.Revision.ID, IdempotencyKey: "schema-proposal"}, 200, &draft)
	command := backendmodel.ChangeProposalCommand{Type: "rename", CommandID: "0197aaf9-5555-7000-8000-000000000099", Reason: "Desired edge caption", RecordType: "edge", ID: edgeID, Name: "Desired edge caption"}
	input := backendmodel.PreviewChangeProposalInput{ExpectedVersion: draft.Proposal.Version, ProposalRevisionID: draft.Revision.ID, Commands: []backendmodel.ChangeProposalCommand{command}}
	var candidate backendmodel.ChangeProposalCandidate
	b41Call(t, s, "POST", base+"/change-proposals/"+draft.Proposal.ID+"/preview", input, 200, &candidate)
	if candidate.CandidateHash == nil {
		t.Fatalf("proposal %+v", candidate.Diagnostics)
	}
	var applied backendmodel.ChangeProposalApplyResult
	b41Call(t, s, "POST", base+"/change-proposals/"+draft.Proposal.ID+"/commands", backendmodel.ApplyChangeProposalInput{ExpectedVersion: input.ExpectedVersion, ProposalRevisionID: input.ProposalRevisionID, Commands: input.Commands, CandidateHash: *candidate.CandidateHash, IdempotencyKey: "schema-rename"}, 200, &applied)
	var renamed backendmodel.GraphPage
	raw := b41Call(t, s, "POST", base+"/graph/query", backendmodel.GraphQueryInput{ChangeProposal: &backendmodel.ProposalReadTarget{ProposalID: applied.Proposal.ID, ProposalRevisionID: applied.Revision.ID}, RecordType: "edges", ID: edgeID}, 200, &renamed)
	if len(renamed.EdgeNames) != 1 || renamed.EdgeNames[0].Name != command.Name || !bytes.Contains(raw, []byte(command.Name)) {
		t.Fatalf("missing declared edge name: %s", raw)
	}
	if strings.Contains(string(b41Call(t, s, "POST", base+"/graph/query", backendmodel.GraphQueryInput{RevisionID: committed.Revision.ID, RecordType: "edges"}, 200, nil)), `"edgeNames"`) {
		t.Fatal("source wire gained desired names")
	}
}

package admin

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func b41Call(t *testing.T, s *Server, method, path string, input any, want int, out any) []byte {
	t.Helper()
	var raw []byte
	var err error
	if v, ok := input.(string); ok {
		raw = []byte(v)
	} else if input != nil {
		raw, err = json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
	}
	status, data, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), method, path, raw)
	if err != nil || status != want {
		t.Fatalf("%s %s: %d %s %v; want %d", method, path, status, data, err, want)
	}
	if want == 200 {
		validateBackendImportResponse(t, method, path, data)
	}
	if out != nil {
		if err = json.Unmarshal(data, out); err != nil {
			t.Fatal(err)
		}
	}
	return data
}

func TestBackendB41ComposedCandidateAndProposalLifecycle(t *testing.T) {
	s := loopbackTestServer(t, nil)
	var project backendmodel.Project
	b41Call(t, s, "POST", "/api/backend-projects", backendmodel.CreateInput{Name: "Composed", IdempotencyKey: "create"}, 201, &project)
	base := "/api/backend-projects/" + project.ID
	begin := transportImportFixture(project)
	begin.Mode = "composed"
	begin.Profile = backendmodel.ComposedProfile
	begin.SourceScope = &backendmodel.SourceScope{Kind: "add_repository"}
	begin.ScopeStatus = &backendmodel.SourceScopeStatus{Status: "complete", Gaps: []string{}}
	begin.SyncPolicy = backendmodel.WholeSourcePolicy
	begin.Manifest.Provider.Profiles = []string{backendmodel.GraphProfile, backendmodel.RelationalProfile, backendmodel.RuntimeProfile, backendmodel.LineageProfile, backendmodel.EventsProfile, backendmodel.ComposedProfile}
	var session backendmodel.ImportSession
	beginBytes := b41Call(t, s, "POST", base+"/imports", begin, 200, &session)
	if replay := b41Call(t, s, "POST", base+"/imports", begin, 200, nil); !bytes.Equal(replay, beginBytes) {
		t.Fatal("begin replay changed")
	}
	importPath := base + "/imports/" + session.ID
	raw := `[{"op":"upsert_node","node":{"externalKey":"system","kind":"system","name":"System","attributes":{},"evidenceKeys":["proof"]}},{"op":"upsert_evidence","evidence":{"externalKey":"proof","subjectType":"node","subjectKey":"system","method":"ast","status":"explicit","source":{"repositoryId":"` + session.RepositoryID + `","snapshotId":"` + session.SnapshotID + `","file":"main.go","contentHash":"` + strings.Repeat("a", 64) + `","startLine":1,"endLine":1},"explanation":"Source declaration"}}]`
	var commands []backendmodel.ImportCommand
	if err := json.Unmarshal([]byte(raw), &commands); err != nil {
		t.Fatal(err)
	}
	hash, err := backendmodel.ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	batchIn := backendmodel.ImportBatchInput{ExpectedImportVersion: session.Version, PayloadHash: hash, Commands: commands}
	var receipt backendmodel.BatchReceipt
	batchBytes := b41Call(t, s, "PUT", importPath+"/batches/graph", batchIn, 200, &receipt)
	var preview backendmodel.ImportPreview
	b41Call(t, s, "POST", importPath+"/preview", backendmodel.PreviewImportInput{ExpectedImportVersion: receipt.AcceptedVersion, BaseRevisionID: project.CurrentRevisionID}, 200, &preview)
	if preview.CandidateHash == nil {
		t.Fatalf("preview diagnostics: %+v", preview.Diagnostics)
	}
	nodeID := ""
	for _, identity := range receipt.Identities {
		if identity.RecordType == "node" {
			nodeID = identity.ID
		}
	}
	if nodeID == "" {
		t.Fatal("node identity missing")
	}
	pins := fmt.Sprintf("?importVersion=%d&candidateHash=%s", preview.Version, *preview.CandidateHash)
	for _, suffix := range []string{"nodes/" + nodeID, "evidence", "coverage", "assertions"} {
		b41Call(t, s, "GET", importPath+"/candidate/"+suffix+pins, nil, 200, nil)
	}
	b41Call(t, s, "POST", base+"/graph/query", fmt.Sprintf(`{"importCandidate":{"importId":%q,"importVersion":%d,"candidateHash":%q},"recordType":"nodes"}`, session.ID, preview.Version, *preview.CandidateHash), 200, nil)
	for _, bad := range []string{"0", "01", "+1", "1e0", "1.0", "9223372036854775808", "null"} {
		b41Call(t, s, "GET", importPath+"/candidate/coverage?importVersion="+bad+"&candidateHash="+*preview.CandidateHash, nil, 400, nil)
	}
	b41Call(t, s, "GET", importPath+"/candidate/coverage"+pins+"&importVersion=1", nil, 400, nil)
	b41Call(t, s, "GET", importPath+"/candidate/coverage"+pins, `{}`, 400, nil)
	b41Call(t, s, "GET", importPath+"/candidate/coverage?importVersion=9223372036854775807&candidateHash="+*preview.CandidateHash, nil, 409, nil)
	for _, kind := range []string{"assertion_conflict", "claim_identity", "migration"} {
		b41Call(t, s, "GET", fmt.Sprintf("%s/changes?previewVersion=%d&recordType=%s", importPath, preview.Version, kind), nil, 200, nil)
	}
	var committed backendmodel.ImportCommitResult
	commitIn := backendmodel.CommitImportInput{ExpectedVersion: project.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "commit"}
	commitBytes := b41Call(t, s, "POST", importPath+"/commit", commitIn, 200, &committed)
	if replay := b41Call(t, s, "POST", importPath+"/commit", commitIn, 200, nil); !bytes.Equal(replay, commitBytes) {
		t.Fatal("commit replay changed")
	}
	if replay := b41Call(t, s, "PUT", importPath+"/batches/graph", batchIn, 200, nil); !bytes.Equal(replay, batchBytes) {
		t.Fatal("batch replay after commit changed")
	}
	b41Call(t, s, "GET", importPath+"/candidate/coverage"+pins, nil, 409, nil)
	sourcePath := base + "/revisions/" + committed.Revision.ID
	b41Call(t, s, "GET", base+"/revisions", nil, 200, nil)
	for _, suffix := range []string{"nodes/" + nodeID, "evidence", "coverage", "assertions?id=" + nodeID} {
		b41Call(t, s, "GET", sourcePath+"/"+suffix, nil, 200, nil)
	}
	b41ConflictLifecycle(t, s, committed, session.RepositoryID, nodeID)
	var created backendmodel.ChangeProposalDetail
	create := backendmodel.CreateChangeProposalInput{Name: "Desired", BaseRevisionID: committed.Revision.ID, IdempotencyKey: "proposal"}
	createBytes := b41Call(t, s, "POST", base+"/change-proposals", create, 200, &created)
	if replay := b41Call(t, s, "POST", base+"/change-proposals", create, 200, nil); !bytes.Equal(replay, createBytes) {
		t.Fatal("proposal create replay changed")
	}
	path := base + "/change-proposals/" + created.Proposal.ID
	b41Call(t, s, "GET", base+"/change-proposals?status=draft", nil, 200, nil)
	viewInput := backendmodel.CreateSavedViewInput{DocumentVersion: "saved-view-v2", Name: "Exact full draft", Target: backendmodel.BackendReadTarget{ChangeProposal: &backendmodel.ProposalReadTarget{ProposalID: created.Proposal.ID, ProposalRevisionID: created.Revision.ID}}, State: backendmodel.SavedViewState{Flow: &backendmodel.SavedFlowViewState{Kind: "flow", Positions: []backendmodel.SavedViewPosition{}, CollapsedGroupIDs: []string{}}}, IdempotencyKey: "saved-v2"}
	var view backendmodel.SavedView
	viewReceipt := b41Call(t, s, "POST", base+"/saved-views", viewInput, 200, &view)
	if replay := b41Call(t, s, "POST", base+"/saved-views", viewInput, 200, nil); !bytes.Equal(replay, viewReceipt) {
		t.Fatal("saved v2 replay changed")
	}
	b41Call(t, s, "GET", base+"/saved-views/"+view.ID+"?version=1", nil, 200, nil)
	b41Call(t, s, "POST", base+"/saved-views/"+view.ID+"/save", backendmodel.SaveSavedViewInput{DocumentVersion: "saved-view-v2", Name: "Saved exact draft", State: viewInput.State, ExpectedVersion: view.Version, IdempotencyKey: "save-v2"}, 200, nil)
	for _, target := range []string{`"revisionId":"` + committed.Revision.ID + `"`, `"changeProposal":{"proposalId":"` + created.Proposal.ID + `","proposalRevisionId":"` + created.Revision.ID + `"}`} {
		b41Call(t, s, "POST", base+"/flow/query", `{`+target+`,"view":"entrypoints"}`, 200, nil)
		b41Call(t, s, "POST", base+"/events/query", `{`+target+`,"view":"jobs"}`, 200, nil)
		b41Call(t, s, "POST", base+"/api-artifacts/query", `{`+target+`}`, 200, nil)
	}
	command := backendmodel.ChangeProposalCommand{Type: "rename", CommandID: "0197aaf9-5555-7000-8000-000000000099", Reason: "Desired name", RecordType: "node", ID: nodeID, Name: "Desired system"}
	in := backendmodel.PreviewChangeProposalInput{ExpectedVersion: created.Proposal.Version, ProposalRevisionID: created.Revision.ID, Commands: []backendmodel.ChangeProposalCommand{command}}
	var candidate backendmodel.ChangeProposalCandidate
	b41Call(t, s, "POST", path+"/preview", in, 200, &candidate)
	if candidate.CandidateHash == nil {
		t.Fatalf("proposal diagnostics %+v", candidate.Diagnostics)
	}
	apply := backendmodel.ApplyChangeProposalInput{ExpectedVersion: in.ExpectedVersion, ProposalRevisionID: in.ProposalRevisionID, Commands: in.Commands, CandidateHash: *candidate.CandidateHash, IdempotencyKey: "apply"}
	var saved backendmodel.ChangeProposalApplyResult
	applyBytes := b41Call(t, s, "POST", path+"/commands", apply, 200, &saved)
	if replay := b41Call(t, s, "POST", path+"/commands", apply, 200, nil); !bytes.Equal(replay, applyBytes) {
		t.Fatal("apply replay changed")
	}
	for _, rid := range []string{created.Revision.ID, saved.Revision.ID} {
		b41Call(t, s, "GET", path+"?proposalRevisionId="+rid, nil, 200, nil)
		for _, suffix := range []string{"nodes/" + nodeID, "evidence", "coverage", "assertions"} {
			b41Call(t, s, "GET", path+"/revisions/"+rid+"/"+suffix, nil, 200, nil)
		}
	}
	restore := backendmodel.RestoreChangeProposalInput{ExpectedVersion: saved.Proposal.Version, ProposalRevisionID: saved.Revision.ID, RestoreRevisionID: created.Revision.ID, IdempotencyKey: "restore"}
	var restored backendmodel.ChangeProposalApplyResult
	restoreBytes := b41Call(t, s, "POST", path+"/restore", restore, 200, &restored)
	if replay := b41Call(t, s, "POST", path+"/restore", restore, 200, nil); !bytes.Equal(replay, restoreBytes) {
		t.Fatal("restore replay changed")
	}
	in.ExpectedVersion = restored.Proposal.Version
	in.ProposalRevisionID = restored.Revision.ID
	b41Call(t, s, "POST", path+"/preview", in, http.StatusConflict, nil)
	var final backendmodel.Project
	b41Call(t, s, "GET", base, nil, 200, &final)
	if final.CurrentRevisionID != committed.Revision.ID {
		t.Fatal("proposal moved source head")
	}
}

func b41ConflictLifecycle(t *testing.T, s *Server, committed backendmodel.ImportCommitResult, repositoryID, nodeID string) {
	t.Helper()
	base := "/api/backend-projects/" + committed.Project.ID
	var assertions backendmodel.BackendAssertionsPage
	b41Call(t, s, "GET", base+"/revisions/"+committed.Revision.ID+"/assertions?id="+nodeID, nil, 200, &assertions)
	claim := assertions.Items[0].Assertion
	begin := transportImportFixture(committed.Project)
	begin.IdempotencyKey = "second-provider"
	begin.Mode = "composed"
	begin.Profile = backendmodel.ComposedProfile
	begin.SourceScope = &backendmodel.SourceScope{Kind: "add_provider", RepositoryID: repositoryID}
	begin.ScopeStatus = &backendmodel.SourceScopeStatus{Status: "complete", Gaps: []string{}}
	begin.SyncPolicy = backendmodel.WholeSourcePolicy
	begin.Manifest.Provider.Namespace = "other"
	begin.Manifest.Provider.Profiles = []string{backendmodel.GraphProfile, backendmodel.RelationalProfile, backendmodel.RuntimeProfile, backendmodel.LineageProfile, backendmodel.EventsProfile, backendmodel.ComposedProfile}
	var session backendmodel.ImportSession
	b41Call(t, s, "POST", base+"/imports", begin, 200, &session)
	path := base + "/imports/" + session.ID
	raw := `[{"op":"upsert_node","node":{"externalKey":"own-system","kind":"system","name":"Alternate","attributes":{},"evidenceKeys":["proof"]}},{"op":"upsert_evidence","evidence":{"externalKey":"proof","subjectType":"node","subjectKey":"own-system","method":"ast","status":"explicit","source":{"repositoryId":"` + session.RepositoryID + `","snapshotId":"` + session.SnapshotID + `","file":"main.go","contentHash":"` + strings.Repeat("a", 64) + `","startLine":1,"endLine":1},"explanation":"Other provider declaration"}}]`
	var commands []backendmodel.ImportCommand
	if err := json.Unmarshal([]byte(raw), &commands); err != nil {
		t.Fatal(err)
	}
	commands = append([]backendmodel.ImportCommand{{Op: "claim_identity", ClaimIdentity: &backendmodel.SourceClaimIdentity{DecisionID: "0197aaf9-5555-7000-8000-000000000071", RecordType: "node", ExternalKey: "own-system", Target: backendmodel.BaseAssertionRef{RepositoryID: claim.Owner.RepositoryID, ProviderNamespace: claim.Owner.ProviderNamespace, RecordType: "node", ExternalKey: claim.ExternalKey, ExpectedID: nodeID, AssertionHash: claim.AssertionHash}, Reason: "Same declaration", EvidenceKeys: []string{"proof"}}}}, commands...)
	hash, err := backendmodel.ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	var batch backendmodel.BatchReceipt
	b41Call(t, s, "PUT", path+"/batches/claims", backendmodel.ImportBatchInput{ExpectedImportVersion: session.Version, PayloadHash: hash, Commands: commands}, 200, &batch)
	var preview backendmodel.ImportPreview
	b41Call(t, s, "POST", path+"/preview", backendmodel.PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: committed.Revision.ID}, 200, &preview)
	if preview.State != "needs_resolution" {
		t.Fatalf("expected conflict %+v", preview)
	}
	var changes backendmodel.ImportChangesPage
	b41Call(t, s, "GET", fmt.Sprintf("%s/changes?previewVersion=%d&recordType=assertion_conflict", path, preview.Version), nil, 200, &changes)
	if len(changes.Items) != 1 || changes.Items[0].AssertionConflict == nil {
		t.Fatalf("conflicts %+v", changes)
	}
	conflict := changes.Items[0].AssertionConflict
	chosen := conflict.Contenders[0]
	resolution := backendmodel.SourceAssertionResolution{DecisionID: "0197aaf9-5555-7000-8000-000000000072", RecordType: "node", ID: nodeID, Property: conflict.Property, ConflictHash: conflict.ConflictHash, Select: backendmodel.SourceAssertionSelection{RepositoryID: chosen.Owner.RepositoryID, ProviderNamespace: chosen.Owner.ProviderNamespace, AssertionHash: chosen.AssertionHash}, Reason: "Choose exact declaration"}
	commands = []backendmodel.ImportCommand{{Op: "resolve_assertion", Resolution: &resolution}}
	hash, err = backendmodel.ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	b41Call(t, s, "PUT", path+"/batches/resolve", backendmodel.ImportBatchInput{ExpectedImportVersion: preview.Version, PayloadHash: hash, Commands: commands}, 200, &batch)
	b41Call(t, s, "POST", path+"/preview", backendmodel.PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: committed.Revision.ID}, 200, &preview)
	if preview.CandidateHash == nil {
		t.Fatalf("resolution preview %+v", preview)
	}
	pins := fmt.Sprintf("?importVersion=%d&candidateHash=%s", preview.Version, *preview.CandidateHash)
	b41Call(t, s, "GET", path+"/candidate/assertions"+pins, nil, 200, &assertions)
	if len(assertions.Items) != 2 {
		t.Fatalf("losing claim lost: %+v", assertions.Items)
	}
	b41Call(t, s, "GET", fmt.Sprintf("%s/changes?previewVersion=%d&recordType=claim_identity", path, preview.Version), nil, 200, nil)
	b41Call(t, s, "POST", path+"/abort", backendmodel.AbortImportInput{ExpectedImportVersion: preview.Version, IdempotencyKey: "abort"}, 200, nil)
	b41Call(t, s, "GET", path+"/candidate/assertions"+pins, nil, 409, nil)
}

func TestBackendB41SourceFiveKeepsLegacyWireAndRejectsAssertions(t *testing.T) {
	s := loopbackTestServer(t, nil)
	var project backendmodel.Project
	b41Call(t, s, "POST", "/api/backend-projects", backendmodel.CreateInput{Name: "Legacy five", IdempotencyKey: "create"}, 201, &project)
	base := "/api/backend-projects/" + project.ID
	begin := transportImportFixture(project)
	begin.Profile = backendmodel.EventsProfile
	begin.Manifest.Provider.Profiles = []string{backendmodel.GraphProfile, backendmodel.RelationalProfile, backendmodel.RuntimeProfile, backendmodel.LineageProfile, backendmodel.EventsProfile}
	var session backendmodel.ImportSession
	b41Call(t, s, "POST", base+"/imports", begin, 200, &session)
	var commands []backendmodel.ImportCommand
	if err := json.Unmarshal([]byte(`[{"op":"upsert_node","node":{"externalKey":"unknown","kind":"unresolved_target","name":"Unknown","attributes":{"expectedKind":"symbol","reason":"not resolved","searchScope":"repository"},"evidenceKeys":[]}}]`), &commands); err != nil {
		t.Fatal(err)
	}
	hash, err := backendmodel.ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	path := base + "/imports/" + session.ID
	var batch backendmodel.BatchReceipt
	b41Call(t, s, "PUT", path+"/batches/graph", backendmodel.ImportBatchInput{ExpectedImportVersion: session.Version, PayloadHash: hash, Commands: commands}, 200, &batch)
	var preview backendmodel.ImportPreview
	b41Call(t, s, "POST", path+"/preview", backendmodel.PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: project.CurrentRevisionID}, 200, &preview)
	if preview.CandidateHash == nil {
		t.Fatalf("preview %+v", preview)
	}
	var imported backendmodel.ImportCommitResult
	b41Call(t, s, "POST", path+"/commit", backendmodel.CommitImportInput{ExpectedVersion: project.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "commit"}, 200, &imported)
	node := batch.Identities[0].ID
	raw := b41Call(t, s, "GET", base+"/revisions/"+imported.Revision.ID+"/nodes/"+node, nil, 200, nil)
	if strings.Contains(string(raw), `"node":`) || !strings.Contains(string(raw), `"externalKey":"unknown"`) {
		t.Fatalf("legacy node wire changed: %s", raw)
	}
	b41Call(t, s, "GET", base+"/revisions/"+imported.Revision.ID+"/assertions", nil, 422, nil)
	var draft backendmodel.ChangeProposalDetail
	b41Call(t, s, "POST", base+"/change-proposals", backendmodel.CreateChangeProposalInput{Name: "Desired legacy", BaseRevisionID: imported.Revision.ID, IdempotencyKey: "proposal"}, 200, &draft)
	draftPath := base + "/change-proposals/" + draft.Proposal.ID + "/revisions/" + draft.Revision.ID
	b41Call(t, s, "GET", draftPath+"/nodes/"+node, nil, 200, nil)
	b41Call(t, s, "GET", draftPath+"/assertions", nil, 422, nil)
}

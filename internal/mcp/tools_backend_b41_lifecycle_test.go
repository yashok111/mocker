package mcp

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendB41RealSDKLifecycleAndReceiptParity(t *testing.T) {
	server, _ := newResourcesTestServer(t, resourcesTestConfig(t))
	call := func(name string, args any, out any) []byte {
		t.Helper()
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		body, message := callTool(t, server, name, string(raw))
		if message != "" {
			t.Fatalf("%s: %s", name, message)
		}
		if out != nil {
			if err = json.Unmarshal(body, out); err != nil {
				t.Fatal(err)
			}
		}
		return body
	}
	var project backendmodel.Project
	call("create_backend_project", backendmodel.CreateInput{Name: "SDK composed", IdempotencyKey: "create"}, &project)
	inventory := []backendmodel.InventoryItem{}
	for category := range strings.FieldsSeq("files endpoints datastores migrations producers consumers jobs contracts tests") {
		inventory = append(inventory, backendmodel.InventoryItem{Category: category, Status: "unsupported", DiscoverySource: "fixture", Gaps: []string{}, Reason: "outside fixture"})
	}
	begin := backendmodel.BeginImportInput{ExpectedVersion: project.Version, BaseRevisionID: project.CurrentRevisionID, IdempotencyKey: "begin", Mode: "composed", Profile: backendmodel.ComposedProfile, SourceScope: &backendmodel.SourceScope{Kind: "add_repository"}, ScopeStatus: &backendmodel.SourceScopeStatus{Status: "complete", Gaps: []string{}}, SyncPolicy: backendmodel.WholeSourcePolicy, Inventory: inventory, Manifest: backendmodel.SourceManifest{RepositoryName: "repo", Provider: backendmodel.SourceProvider{Name: "fixture", Version: "1", Namespace: "sdk", Method: "agent", Profiles: []string{backendmodel.GraphProfile, backendmodel.RelationalProfile, backendmodel.RuntimeProfile, backendmodel.LineageProfile, backendmodel.EventsProfile, backendmodel.ComposedProfile}, Limitations: []string{}}, Snapshot: backendmodel.SnapshotManifest{Consistency: "verified", CapturedAt: time.Now().UTC(), Files: []backendmodel.ManifestFile{}}}}
	withProject := func(input any, extra map[string]any) map[string]any {
		t.Helper()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]jsontext.Value
		if err = json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		out := map[string]any{"projectId": project.ID}
		for key, value := range fields {
			out[key] = value
		}
		for key, value := range extra {
			out[key] = value
		}
		return out
	}
	var session backendmodel.ImportSession
	call("begin_backend_import", withProject(begin, nil), &session)
	commands := []backendmodel.ImportCommand{{Op: "upsert_node", Node: &backendmodel.ImportNode{ExternalKey: "unknown", Kind: "unresolved_target", Name: "Unknown", Attributes: map[string]jsontext.Value{"expectedKind": jsontext.Value(`"symbol"`), "reason": jsontext.Value(`"not resolved"`), "searchScope": jsontext.Value(`"repository"`)}, EvidenceKeys: []string{}}}}
	hash, err := backendmodel.ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	var batch backendmodel.BatchReceipt
	call("put_backend_import_batch", map[string]any{"projectId": project.ID, "importId": session.ID, "batchId": "graph", "expectedImportVersion": session.Version, "payloadHash": hash, "commands": commands}, &batch)
	var preview backendmodel.ImportPreview
	call("preview_backend_import", map[string]any{"projectId": project.ID, "importId": session.ID, "expectedImportVersion": batch.AcceptedVersion, "baseRevisionId": project.CurrentRevisionID}, &preview)
	if preview.CandidateHash == nil {
		t.Fatalf("candidate %+v", preview)
	}
	pin := backendmodel.ImportCandidateReadTarget{ImportID: session.ID, ImportVersion: preview.Version, CandidateHash: *preview.CandidateHash}
	call("get_backend_assertions", map[string]any{"projectId": project.ID, "importCandidate": pin}, nil)
	var imported backendmodel.ImportCommitResult
	call("commit_backend_import", map[string]any{"projectId": project.ID, "importId": session.ID, "expectedVersion": project.Version, "expectedImportVersion": preview.Version, "candidateHash": *preview.CandidateHash, "idempotencyKey": "commit"}, &imported)
	create := backendmodel.CreateChangeProposalInput{Name: "Desired", BaseRevisionID: imported.Revision.ID, IdempotencyKey: "proposal"}
	var draft backendmodel.ChangeProposalDetail
	first := call("create_backend_change_proposal", withProject(create, nil), &draft)
	raw, _ := json.Marshal(create)
	source := httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", nil)
	status, rest, err := server.CallAsMCP(t.Context(), source, "POST", "/api/backend-projects/"+project.ID+"/change-proposals", raw)
	if err != nil || status != 200 || !bytes.Equal(bytes.TrimSpace(first), bytes.TrimSpace(rest)) {
		t.Fatalf("create parity %d %s %s %v", status, first, rest, err)
	}
	command := backendmodel.ChangeProposalCommand{Type: "set_criteria", CommandID: "0197aaf9-5555-7000-8000-000000000081", Reason: "No checks declared", Criteria: []backendmodel.ChangeCriterion{}}
	proposalInput := backendmodel.PreviewChangeProposalInput{ExpectedVersion: draft.Proposal.Version, ProposalRevisionID: draft.Revision.ID, Commands: []backendmodel.ChangeProposalCommand{command}}
	extra := map[string]any{"proposalId": draft.Proposal.ID}
	var candidate backendmodel.ChangeProposalCandidate
	call("preview_backend_change_proposal_commands", withProject(proposalInput, extra), &candidate)
	if candidate.CandidateHash == nil {
		t.Fatalf("proposal candidate %+v", candidate)
	}
	apply := backendmodel.ApplyChangeProposalInput{ExpectedVersion: proposalInput.ExpectedVersion, ProposalRevisionID: proposalInput.ProposalRevisionID, Commands: proposalInput.Commands, CandidateHash: *candidate.CandidateHash, IdempotencyKey: "apply"}
	var saved backendmodel.ChangeProposalApplyResult
	receipt := call("apply_backend_change_proposal_commands", withProject(apply, extra), &saved)
	raw, _ = json.Marshal(apply)
	status, rest, err = server.CallAsMCP(t.Context(), source, "POST", "/api/backend-projects/"+project.ID+"/change-proposals/"+draft.Proposal.ID+"/commands", raw)
	if err != nil || status != 200 || !bytes.Equal(bytes.TrimSpace(receipt), bytes.TrimSpace(rest)) {
		t.Fatalf("apply parity %d %s %s %v", status, receipt, rest, err)
	}
	target := backendmodel.ProposalReadTarget{ProposalID: draft.Proposal.ID, ProposalRevisionID: saved.Revision.ID}
	assertionBytes := call("get_backend_assertions", map[string]any{"projectId": project.ID, "changeProposal": target}, nil)
	status, rest, err = server.CallAsMCP(t.Context(), source, "GET", "/api/backend-projects/"+project.ID+"/change-proposals/"+draft.Proposal.ID+"/revisions/"+saved.Revision.ID+"/assertions", nil)
	if err != nil || status != 200 || !bytes.Equal(bytes.TrimSpace(assertionBytes), bytes.TrimSpace(rest)) {
		t.Fatalf("assertion parity %d %v", status, err)
	}
}

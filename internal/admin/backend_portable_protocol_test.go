package admin

import (
	"encoding/json/v2"
	"fmt"
	"testing"

	bm "github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/backendportable"
)

func TestPortableRESTSelectionExportImportReplay(t *testing.T) {
	s := loopbackTestServer(t, nil)
	p, proposal, _, _ := b42Draft(t, s)
	target := bm.BackendReadTarget{ChangeProposal: &bm.ProposalReadTarget{ProposalID: proposal.Proposal.ID, ProposalRevisionID: proposal.Revision.ID}}
	call := func(method, path string, value any, want int) []byte {
		t.Helper()
		var raw []byte
		var err error
		if value != nil {
			raw, err = json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
		}
		status, body, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), method, path, raw)
		if err != nil || status != want {
			t.Fatalf("%s %s: %d %s %v", method, path, status, body, err)
		}
		if status == 200 {
			validateBackendImportResponse(t, method, path, body)
		}
		return body
	}
	var selection backendportable.Selection
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	check(json.Unmarshal(call("POST", "/api/backend-projects/"+p.ID+"/portable/selection", backendportable.SelectionInput{Target: target, DiagramViews: []backendportable.SVGInput{}}, 200), &selection))
	var exported backendportable.ExportResult
	check(json.Unmarshal(call("POST", "/api/backend-projects/"+p.ID+"/portable/export", backendportable.ExportInput{Selection: selection, IdempotencyKey: "export"}, 200), &exported))
	var session backendportable.Session
	check(json.Unmarshal(call("POST", "/api/backend-projects/portable/imports", backendportable.BeginInput{Manifest: exported.Manifest, IdempotencyKey: "begin"}, 200), &session))
	for _, descriptor := range exported.Manifest.Chunks {
		var chunk struct {
			Index        int    `json:"index"`
			ManifestHash string `json:"manifestHash"`
			Body         string `json:"body"`
		}
		path := fmt.Sprintf("/api/backend-projects/portable/exports/%s/chunks/%d?manifestHash=%s", exported.Session.ID, descriptor.Index, exported.Session.ManifestHash)
		check(json.Unmarshal(call("GET", path, nil, 200), &chunk))
		check(json.Unmarshal(call("POST", "/api/backend-projects/portable/imports/"+session.ID+"/chunks", backendportable.PutInput{ExpectedVersion: session.Version, Index: descriptor.Index, Body: chunk.Body, IdempotencyKey: fmt.Sprint("put", descriptor.Index)}, 200), &session))
	}
	var preview backendportable.PreviewResult
	path := "/api/backend-projects/portable/imports/" + session.ID
	check(json.Unmarshal(call("POST", path+"/preview", backendportable.PreviewInput{ExpectedVersion: session.Version, Name: "Imported by REST", ArtifactMappings: []bm.PortableArtifactMapping{}, IdempotencyKey: "preview"}, 200), &preview))
	if _, err := s.backendRepo.Get(t.Context(), preview.ProjectID); err == nil {
		t.Fatal("preview exposed project")
	}
	input := backendportable.CommitInput{ExpectedVersion: preview.Session.Version, CandidateHash: preview.CandidateHash, IdempotencyKey: "commit"}
	first := call("POST", path+"/commit", input, 200)
	second := call("POST", path+"/commit", input, 200)
	if string(first) != string(second) {
		t.Fatal("REST commit receipt changed")
	}
	call("POST", path+"/commit", map[string]any{"expectedVersion": preview.Session.Version, "candidateHash": preview.CandidateHash, "idempotencyKey": "invalid", "unknown": true}, 422)
}

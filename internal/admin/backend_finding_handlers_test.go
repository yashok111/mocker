package admin

import (
	"encoding/json/v2"
	"github.com/yashok111/mocker/internal/backendanalysis"
	"github.com/yashok111/mocker/internal/backendmodel"
	"testing"
)

func TestBackendFindingRESTExactResult(t *testing.T) {
	s := loopbackTestServer(t, nil)
	repo := backendanalysis.NewRepo(s.db)
	engine := backendanalysis.NewEngine(s.backendRepo, nil)
	s.SetBackendAnalysis(backendanalysis.NewService(repo, s.backendRepo, engine), repo)
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Diagnostics", IdempotencyKey: "project"})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"kind":"diagnostics","target":{"revisionId":"` + p.CurrentRevisionID + `"},"scope":{},"limits":{},"observationMode":"none","idempotencyKey":"diagnostic"}`
	status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", "/api/backend-projects/"+p.ID+"/analyses", []byte(body))
	if err != nil || status != 202 {
		t.Fatal(status, string(raw), err)
	}
	var j backendanalysis.Job
	if err = json.Unmarshal(raw, &j); err != nil {
		t.Fatal(err)
	}
	claim, err := repo.Claim(t.Context(), "test")
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	terminal, err := engine.Analyze(t.Context(), &claim.Input, func(backendanalysis.PreparedSnapshot) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	terminal.Snapshot.Manifest.ResultVersion = 1
	terminal.Snapshot.Manifest.AnalysisInputHash = j.AnalysisInputHash
	if _, err = repo.Finalize(t.Context(), p.ID, j.ID, claim.Token, *terminal); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/analyses/" + j.ID, "/findings?jobId=" + j.ID + "&resultVersion=1"} {
		path = "/api/backend-projects/" + p.ID + path
		status, raw, err = s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", path, nil)
		if err != nil || status != 200 {
			t.Fatal(status, string(raw), err)
		}
		validateBackendImportResponse(t, "GET", path, raw)
	}
	status, _, _ = s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", "/api/backend-projects/"+p.ID+"/findings?jobId="+j.ID+"&resultVersion=1&latest=true", nil)
	if status != 422 {
		t.Fatal(status)
	}
}

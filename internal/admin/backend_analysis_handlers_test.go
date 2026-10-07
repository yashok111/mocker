package admin

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/backendanalysis"
	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendAnalysisRESTStoredReplayAndQueries(t *testing.T) {
	s := loopbackTestServer(t, nil)
	repo := backendanalysis.NewRepo(s.db)
	s.SetBackendAnalysis(backendanalysis.NewService(repo, s.backendRepo, backendanalysis.NewEngine(s.backendRepo, nil)), repo)
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Analysis", IdempotencyKey: "project"})
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"kind":"diff","fromRevisionId":"` + p.CurrentRevisionID + `","target":{"revisionId":"` + p.CurrentRevisionID + `"},"scope":{},"limits":{},"observationMode":"none","idempotencyKey":"start"}`
	path := "/api/backend-projects/" + p.ID + "/analyses"
	call := func(method, path, body string, want int) []byte {
		t.Helper()
		status, out, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), method, path, []byte(body))
		if err != nil || status != want {
			t.Fatalf("%s %s: %d %s %v", method, path, status, out, err)
		}
		if want == 200 {
			validateBackendImportResponse(t, method, path, out)
		}
		return out
	}
	first := call("POST", path, raw, 202)
	second := call("POST", path, raw, 202)
	if string(first) != string(second) {
		t.Fatal("replay changed")
	}
	var job backendanalysis.Job
	if err = json.Unmarshal(first, &job); err != nil {
		t.Fatal(err)
	}
	if job.RecommendedPollIntervalMs != 2000 {
		t.Fatal(job)
	}
	detail := call("GET", path+"/"+job.ID, "", 200)
	if !strings.Contains(string(detail), `"documentVersion":"backend-analysis-context-v1"`) {
		t.Fatal(string(detail))
	}
	call("GET", path, "", 200)
	call("GET", path+"?status=completed&status=queued", "", 400)
	call("GET", path+"/"+job.ID+"?latest=true", "", 400)
	call("GET", path+"/"+job.ID+"/results?section=changes", "", 400)
	call("GET", path+"/"+job.ID+"/results?section=changes&resultVersion=1", "", 409)
	cancel := call("POST", path+"/"+job.ID+"/cancel", `{"idempotencyKey":"cancel"}`, 200)
	if !strings.Contains(string(cancel), `"status":"cancelled"`) {
		t.Fatal(string(cancel))
	}
	result := call("GET", path+"/"+job.ID+"/results?section=changes&resultVersion=1", "", 200)
	if !strings.Contains(string(result), `"items":[]`) {
		t.Fatal(string(result))
	}
	retry := call("POST", path+"/"+job.ID+"/retry", `{"idempotencyKey":"retry"}`, 202)
	if string(retry) != string(call("POST", path+"/"+job.ID+"/retry", `{"idempotencyKey":"retry"}`, 202)) {
		t.Fatal("retry receipt changed")
	}
	call("POST", path+"/"+job.ID+"/cancel", `{"idempotencyKey":"new-cancel"}`, 409)
	call("GET", path+"/"+job.ID+"/results?section=changes&resultVersion=2", "", 404)
}

func TestBackendCapabilitiesAnalysisDelivered(t *testing.T) {
	s := loopbackTestServer(t, nil)
	caps := readB41Capabilities(t, s)
	for _, feature := range []string{"backend-analysis-jobs", "backend-analysis-diff", "backend-analysis-impact", "backend-change-rebase", "backend-change-ready"} {
		if !strings.Contains(strings.Join(caps.Features, ","), feature) {
			t.Errorf("missing %s", feature)
		}
	}
	if caps.Limits["maxAnalysisInputBytes"] != 2<<20 || caps.Limits["maxAnalysisWaitingJobs"] != 20 {
		t.Fatal(caps.Limits)
	}
}

func TestBackendAnalysisAdmissionLimitsAndReplay(t *testing.T) {
	s := loopbackTestServer(t, nil)
	repo := backendanalysis.NewRepo(s.db)
	s.SetBackendAnalysis(backendanalysis.NewService(repo, s.backendRepo, backendanalysis.NewEngine(s.backendRepo, nil)), repo)
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Quota", IdempotencyKey: "project"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/backend-projects/" + p.ID + "/analyses"
	raw := `{"kind":"diff","fromRevisionId":"` + p.CurrentRevisionID + `","target":{"revisionId":"` + p.CurrentRevisionID + `"},"scope":{},"limits":{"resultBytes":1048576},"observationMode":"none","idempotencyKey":"first"}`
	first := b41Call(t, s, "POST", path, strings.Repeat(" ", 1<<20)+raw, 202, nil)
	b41Call(t, s, "POST", path, strings.Repeat(" ", 2<<20)+raw, 413, nil)
	b41Call(t, s, "POST", "/api/backend-projects/"+p.ID+"/change-proposals", strings.Repeat(" ", 1<<20)+`{}`, 413, nil)
	for i := 1; i < 20; i++ {
		b41Call(t, s, "POST", path, strings.Replace(raw, `"first"`, fmt.Sprintf(`"key%d"`, i), 1), 202, nil)
	}
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(strings.Replace(raw, `"first"`, `"full"`, 1)))
	r.SetPathValue("id", p.ID)
	r = r.WithContext(withAuthContext(r.Context(), nil, &auth.User{Name: "quota"}))
	w := httptest.NewRecorder()
	s.handleStartBackendAnalysis(w, r)
	if w.Code != 429 || w.Header().Get("Retry-After") != "2" || !strings.Contains(w.Body.String(), "backend_analysis_queue_full") {
		t.Fatalf("%d %v %s", w.Code, w.Header(), w.Body)
	}
	replay := b41Call(t, s, "POST", path, raw, 202, nil)
	if string(first) != string(replay) {
		t.Fatal("quota blocked receipt")
	}
	for _, bad := range []string{strings.Replace(raw, `"target":{`, `"target":{"proposal":{},`, 1), strings.Replace(raw, `"scope":{}`, `"scope":{"depth":null}`, 1), strings.Replace(raw, `"observationMode":"none"`, `"observationMode":"pinned"`, 1), strings.Replace(raw, `"scope":{}`, `"scope":{},"scope":{}`, 1)} {
		want := 400
		if strings.Contains(bad, "pinned") {
			want = 422
		}
		b41Call(t, s, "POST", path, bad, want, nil)
	}
}

func TestBackendAnalysisRetainedQuotaAndCursorBinding(t *testing.T) {
	s := loopbackTestServer(t, nil)
	repo := backendanalysis.NewRepo(s.db)
	s.SetBackendAnalysis(backendanalysis.NewService(repo, s.backendRepo, backendanalysis.NewEngine(s.backendRepo, nil)), repo)
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Retained", IdempotencyKey: "project"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/backend-projects/" + p.ID + "/analyses"
	raw := `{"kind":"diff","fromRevisionId":"` + p.CurrentRevisionID + `","target":{"revisionId":"` + p.CurrentRevisionID + `"},"scope":{},"limits":{},"observationMode":"none","idempotencyKey":"first"}`
	first := b41Call(t, s, "POST", path, raw, 202, nil)
	for i := 1; i < 7; i++ {
		b41Call(t, s, "POST", path, strings.Replace(raw, `"first"`, fmt.Sprintf(`"key%d"`, i), 1), 202, nil)
	}
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(strings.Replace(raw, `"first"`, `"quota"`, 1)))
	r.SetPathValue("id", p.ID)
	r = r.WithContext(withAuthContext(r.Context(), nil, &auth.User{Name: "quota"}))
	w := httptest.NewRecorder()
	s.handleStartBackendAnalysis(w, r)
	if w.Code != 409 || w.Header().Get("Retry-After") != "" || !strings.Contains(w.Body.String(), "backend_analysis_byte_quota") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if string(first) != string(b41Call(t, s, "POST", path, raw, 202, nil)) {
		t.Fatal("quota blocked receipt")
	}
	var page backendanalysis.JobPage
	b41Call(t, s, "GET", path+"?limit=1", nil, 200, &page)
	if page.NextCursor == "" {
		t.Fatal(page)
	}
	b41Call(t, s, "GET", path+"?limit=1&cursor="+page.NextCursor, nil, 200, nil)
	b41Call(t, s, "GET", path+"?limit=2&cursor="+page.NextCursor, nil, 400, nil)
	// The cursor does not transfer to another project. That project must
	// exist: an unknown one is a 404 before any cursor is read (review
	// 2026-10-06, F13), which this line used to pin as 400 by reusing a
	// revision id as the project id.
	other, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Other", IdempotencyKey: "other"})
	if err != nil {
		t.Fatal(err)
	}
	b41Call(t, s, "GET", "/api/backend-projects/"+other.ID+"/analyses?limit=1&cursor="+page.NextCursor, nil, 400, nil)
	b41Call(t, s, "GET", "/api/backend-projects/"+p.CurrentRevisionID+"/analyses?limit=1&cursor="+page.NextCursor, nil, 404, nil)
	s.cfg.MaxBody = 128
	b41Call(t, s, "POST", path, raw, 413, nil)
}

func TestBackendAnalysisPublicRoutePolicies(t *testing.T) {
	want := map[string]checkpointPolicy{
		"POST /api/backend-projects/{id}/analyses":                              cpAnotherLayer,
		"GET /api/backend-projects/{id}/analyses":                               cpRead,
		"GET /api/backend-projects/{id}/analyses/{aid}":                         cpRead,
		"POST /api/backend-projects/{id}/analyses/{aid}/cancel":                 cpAnotherLayer,
		"POST /api/backend-projects/{id}/analyses/{aid}/retry":                  cpAnotherLayer,
		"GET /api/backend-projects/{id}/analyses/{aid}/results":                 cpRead,
		"POST /api/backend-projects/{id}/change-proposals/{pid}/rebase-preview": cpNeverTouchesLayer,
		"POST /api/backend-projects/{id}/change-proposals/{pid}/rebase":         cpAnotherLayer,
		"POST /api/backend-projects/{id}/change-proposals/{pid}/lifecycle":      cpAnotherLayer,
	}
	s := loopbackTestServer(t, nil)
	for _, route := range s.routes() {
		policy, ok := want[route.pattern]
		if !ok {
			continue
		}
		if route.checkpoint != policy || route.mcp != mcpAllow {
			t.Fatal(route)
		}
		w := httptest.NewRecorder()
		route.handler(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if w.Code != 401 {
			t.Fatal(route.pattern, w.Code)
		}
		delete(want, route.pattern)
	}
	if len(want) != 0 {
		t.Fatal(want)
	}
}

func TestBackendAnalysisCompletedManualSourceSchema(t *testing.T) {
	s := loopbackTestServer(t, nil)
	repo := backendanalysis.NewRepo(s.db)
	service := backendanalysis.NewService(repo, s.backendRepo, backendanalysis.NewEngine(s.backendRepo, nil))
	s.SetBackendAnalysis(service, repo)
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Manual source", IdempotencyKey: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	run := make(chan error, 1)
	go func() { run <- service.Run(t.Context()) }()
	t.Cleanup(func() {
		_ = service.Close(context.Background())
		if err := <-run; err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err = service.WaitRunning(ctx); err != nil {
		t.Fatal(err)
	}
	path := "/api/backend-projects/" + p.ID + "/analyses"
	var job backendanalysis.Job
	b41Call(t, s, "POST", path, backendanalysis.StartInput{Kind: "impact", FromRevisionID: p.CurrentRevisionID, Target: backendanalysis.AnalysisTarget{RevisionID: p.CurrentRevisionID}, ObservationMode: "none", IdempotencyKey: "manual-impact"}, 202, &job)
	for job.Status == "queued" || job.Status == "running" {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
		next, err := repo.Get(ctx, p.ID, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		job = *next
	}
	if job.Status != "completed" {
		t.Fatal(job)
	}
	b41Call(t, s, "GET", path+"/"+job.ID, nil, 200, nil)
	var page struct {
		Manifest backendanalysis.ResultManifest `json:"manifest"`
	}
	b41Call(t, s, "GET", fmt.Sprintf("%s/%s/results?resultVersion=%d&section=gaps", path, job.ID, *job.ResultVersion), nil, 200, &page)
	if !page.Manifest.Complete || page.Manifest.SourceCoverageBefore.Status != "partial" || page.Manifest.SourceCoverageAfter.Status != "partial" {
		t.Fatal(page.Manifest)
	}
}

func TestBackendAnalysisGETBodyRejected(t *testing.T) {
	s := loopbackTestServer(t, nil)
	repo := backendanalysis.NewRepo(s.db)
	s.SetBackendAnalysis(backendanalysis.NewService(repo, s.backendRepo, backendanalysis.NewEngine(s.backendRepo, nil)), repo)
	const id = "0197aaf9-5555-7000-8000-000000000001"
	for _, path := range []string{"/api/backend-projects/" + id + "/analyses", "/api/backend-projects/" + id + "/analyses/" + id, "/api/backend-projects/" + id + "/analyses/" + id + "/results?resultVersion=1&section=changes"} {
		status, body, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), http.MethodGet, path, []byte(`{}`))
		if err != nil || status != 400 {
			t.Fatalf("GET body %s: %d %s %v", path, status, body, err)
		}
	}
}

func TestBackendAnalysisSource5PinsPublicSchemas(t *testing.T) {
	s := loopbackTestServer(t, nil)
	jobs := backendanalysis.NewRepo(s.db)
	service := backendanalysis.NewService(jobs, s.backendRepo, backendanalysis.NewEngine(s.backendRepo, nil))
	s.SetBackendAnalysis(service, jobs)
	project, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{
		Name: "Source5 pins", IdempotencyKey: "source5-pins-project",
	})
	if err != nil {
		t.Fatal(err)
	}
	source := b42Source(t, s, *project, "", "source5", "System", "5")
	base := "/api/backend-projects/" + project.ID
	var draft backendmodel.ChangeProposalDetail
	b41Call(t, s, "POST", base+"/change-proposals", backendmodel.CreateChangeProposalInput{
		Name: "Source5 draft", BaseRevisionID: source.Revision.ID, IdempotencyKey: "draft",
	}, http.StatusOK, &draft)
	previewPath := base + "/change-proposals/" + draft.Proposal.ID + "/rebase-preview"
	previewInput := backendmodel.PreviewChangeProposalRebaseInput{
		ExpectedVersion: draft.Proposal.Version, ProposalRevisionID: draft.Revision.ID,
		NewBaseRevisionID:   source.Revision.ID,
		IdentityResolutions: []backendmodel.ChangeRebaseIdentityResolution{},
		Resolutions:         []backendmodel.ChangeRebaseResolution{}, RepairCommands: []backendmodel.ChangeProposalCommand{},
	}
	previewRaw := b41Call(t, s, "POST", previewPath, previewInput, http.StatusOK, nil)
	var preview struct {
		SourcePins backendmodel.AnalysisSourcePins `json:"sourcePins"`
	}
	if err := json.Unmarshal(previewRaw, &preview); err != nil {
		t.Fatal(err)
	}
	assertSource5Pins := func(pins backendmodel.AnalysisSourcePins) {
		t.Helper()
		if pins.ContentHash != "" || len(pins.SourceSnapshotIDs) == 0 {
			t.Fatalf("expected source5 empty content hash with actual snapshots: %+v", pins)
		}
	}
	assertSource5Pins(preview.SourcePins)
	var job backendanalysis.Job
	b41Call(t, s, "POST", base+"/analyses", backendanalysis.StartInput{
		Kind: "impact", FromRevisionID: source.Revision.ID,
		Target: backendanalysis.AnalysisTarget{ChangeProposal: &backendmodel.ProposalReadTarget{
			ProposalID: draft.Proposal.ID, ProposalRevisionID: draft.Revision.ID,
		}}, ObservationMode: "none", IdempotencyKey: "analysis",
	}, http.StatusAccepted, &job)
	var detail struct {
		Input struct {
			BeforeSource backendmodel.AnalysisSourcePins `json:"beforeSource"`
			AfterSource  backendmodel.AnalysisSourcePins `json:"afterSource"`
		} `json:"input"`
	}
	detailPath := base + "/analyses/" + job.ID
	detailRaw := b41Call(t, s, "GET", detailPath, nil, http.StatusOK, &detail)
	assertSource5Pins(detail.Input.BeforeSource)
	assertSource5Pins(detail.Input.AfterSource)
	validateBackendImportResponse(t, "POST", previewPath, previewRaw)
	validateBackendImportResponse(t, "GET", detailPath, detailRaw)
}

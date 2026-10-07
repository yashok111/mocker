package admin

import (
	"encoding/json/v2"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendanalysis"
	bm "github.com/yashok111/mocker/internal/backendmodel"
	ob "github.com/yashok111/mocker/internal/backendobservations"
)

type backendFailure struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func decodeBackendFailure(t *testing.T, raw []byte) backendFailure {
	t.Helper()
	var f backendFailure
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return f
}

// TestCapabilitiesAdvertiseEveryAnalysisKindAndMode pins review 2026-10-06,
// F11/F27: capabilities.analysisSupport listed six kinds and
// observationModes ["none"], so an agent negotiating by it (as the analysis
// guides tell it to) concluded measurements and pinned observed impact were
// unsupported while start_backend_analysis accepted them.
func TestCapabilitiesAdvertiseEveryAnalysisKindAndMode(t *testing.T) {
	s := loopbackTestServer(t, nil)
	status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", "/api/backend-projects/capabilities", nil)
	if err != nil || status != 200 {
		t.Fatal(status, string(raw), err)
	}
	var caps struct {
		AnalysisSupport struct {
			Kinds            []string `json:"kinds"`
			ObservationModes []string `json:"observationModes"`
		} `json:"analysisSupport"`
	}
	if err := json.Unmarshal(raw, &caps); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(caps.AnalysisSupport.Kinds, "scenario_measurement") || !slices.Contains(caps.AnalysisSupport.Kinds, "scenario_comparison") || !slices.Contains(caps.AnalysisSupport.ObservationModes, "pinned") {
		t.Errorf("analysisSupport = %+v", caps.AnalysisSupport)
	}
}

// TestProjectCapabilitiesMatchTheServer pins review 2026-10-06, F90: every
// project resource carried the original seven features while
// get_backend_capabilities advertised sixty-odd, so a client checking
// project.capabilities concluded that annotations, relational import or sync
// were unsupported.
func TestProjectCapabilitiesMatchTheServer(t *testing.T) {
	s := loopbackTestServer(t, nil)
	var p bm.Project
	b41Call(t, s, "POST", "/api/backend-projects", bm.CreateInput{Name: "Caps", IdempotencyKey: "caps"}, 201, &p)
	status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", "/api/backend-projects/capabilities", nil)
	if err != nil || status != 200 {
		t.Fatal(status, string(raw), err)
	}
	var caps struct {
		Features []string `json:"features"`
	}
	if err := json.Unmarshal(raw, &caps); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Capabilities, caps.Features) {
		t.Errorf("project capabilities %v, server features %v", p.Capabilities, caps.Features)
	}
}

// TestComposedRevisionCompareMatchesTheContract pins review 2026-10-06, F16:
// comparing two composed (schema 6) revisions emits sourceBefore/sourceAfter
// and per-item sourceClaimBefore/sourceClaimAfter, which api/openapi.json did
// not declare on two additionalProperties:false schemas, so a strict client
// rejected the real response. b41Call validates the 200 against the contract.
func TestComposedRevisionCompareMatchesTheContract(t *testing.T) {
	s := loopbackTestServer(t, nil)
	p, err := s.backendRepo.Create(t.Context(), bm.CreateInput{Name: "Compare", IdempotencyKey: "compare"})
	if err != nil {
		t.Fatal(err)
	}
	first := b42Source(t, s, *p, "", "first", "System")
	graph, err := s.backendRepo.ResolveEffectiveGraph(t.Context(), p.ID, bm.BackendReadTarget{RevisionID: first.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	second := b42Source(t, s, first.Project, graph.Source.SourceVector.Partitions[0].RepositoryID, "second", "Renamed System")
	raw := b41Call(t, s, "POST", "/api/backend-projects/"+p.ID+"/revisions/compare", bm.CompareRevisionsInput{FromRevisionID: first.Revision.ID, ToRevisionID: second.Revision.ID}, 200, nil)
	if !strings.Contains(string(raw), `"sourceBefore"`) {
		t.Fatalf("fixture did not exercise the composed members: %s", raw)
	}
}

// TestAnalysisListUnknownProjectIs404 pins review 2026-10-06, F13: the list
// never looked the project up, so a mistyped or deleted projectId answered
// 200 with an empty page, while every sibling read answers 404.
func TestAnalysisListUnknownProjectIs404(t *testing.T) {
	s := loopbackTestServer(t, nil)
	jobs := backendanalysis.NewRepo(s.db)
	s.SetBackendAnalysis(backendanalysis.NewService(jobs, s.backendRepo, backendanalysis.NewEngine(s.backendRepo, nil)), jobs)
	b41Call(t, s, "GET", "/api/backend-projects/01900000-0000-7000-8000-00000000000a/analyses", nil, 404, nil)
}

// TestBackendBodyKeepsTypedDecoderFaults pins review 2026-10-06, F173:
// backendBodyLimit and backendImportDecodedBody replaced every decode error
// with a fixed 400, discarding the FaultError a custom UnmarshalJSON returns
// on purpose. The documented 4 MiB adapter limit was unreachable as 413, and
// a changeManifest on a non-incremental begin lost its 422 and its path.
func TestBackendBodyKeepsTypedDecoderFaults(t *testing.T) {
	s := loopbackTestServer(t, nil)
	s.SetBackendObservations(&ob.Service{Repo: ob.NewRepo(s.db), Graphs: s.backendRepo})
	var p bm.Project
	b41Call(t, s, "POST", "/api/backend-projects", bm.CreateInput{Name: "Decode", IdempotencyKey: "decode"}, 201, &p)
	base := "/api/backend-projects/" + p.ID

	adapt := `{"data":"` + strings.Repeat("x", ob.AdapterBytes+1) + `"}`
	if f := decodeBackendFailure(t, b41Call(t, s, "POST", base+"/observations/adapt", adapt, 413, nil)); f.Error.Code != "backend_observation_adapter_limit" {
		t.Errorf("adapt over the limit: code %q", f.Error.Code)
	}

	valid, err := json.Marshal(transportImportFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	begin := strings.Replace(string(valid), `"expectedVersion":1`, `"changeManifest":null,"expectedVersion":1`, 1)
	f := decodeBackendFailure(t, b41Call(t, s, "POST", base+"/imports", begin, 422, nil))
	if f.Error.Code != "backend_import_invalid" || f.Error.Details["path"] != "changeManifest" {
		t.Errorf("changeManifest on a full begin: %+v", f.Error)
	}
}

// TestBackendBodyErrorsNameTheBody pins review 2026-10-06, F170 and F21: a
// body that failed its schema, or an id or hash member that is not canonical,
// was answered with backendQueryError()'s "Only allowed, one-valued query
// parameters and limits may be supplied" on routes that refuse any query.
func TestBackendBodyErrorsNameTheBody(t *testing.T) {
	s := loopbackTestServer(t, nil)
	var p bm.Project
	b41Call(t, s, "POST", "/api/backend-projects", bm.CreateInput{Name: "Body", IdempotencyKey: "body"}, 201, &p)
	base := "/api/backend-projects/" + p.ID
	for _, tc := range []struct{ name, path, body, field string }{
		{"artifact query unknown member", base + "/artifacts/query", `{"unknown":1}`, ""},
		{"change proposal unknown member", base + "/change-proposals/" + p.ID + "/preview", `{"unknown":1}`, ""},
		{"change proposal bad id", base + "/change-proposals/" + p.ID + "/preview", `{"expectedVersion":1,"proposalRevisionId":"nope","commands":[]}`, "proposalRevisionId"},
		{"change proposal bad hash", base + "/change-proposals/" + p.ID + "/commands", `{"expectedVersion":1,"proposalRevisionId":"` + p.CurrentRevisionID + `","commands":[],"candidateHash":"x","idempotencyKey":"k"}`, "candidateHash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := decodeBackendFailure(t, b41Call(t, s, "POST", tc.path, tc.body, 400, nil))
			if f.Error.Code != "backend_invalid" || strings.Contains(f.Error.Message, "query") {
				t.Errorf("error = %+v, want a body-specific backend_invalid", f.Error)
			}
			if tc.field != "" && f.Error.Details["field"] != tc.field {
				t.Errorf("details = %v, want field %q", f.Error.Details, tc.field)
			}
		})
	}
}

// TestDiagramBodyAdmissionIsOneStatus pins review 2026-10-06, F171: a
// non-object or unparsable body on a diagram route answered 400 from
// backendBodyLimit, while a schema-invalid object on the same route answers
// 422 through diagramError.
func TestDiagramBodyAdmissionIsOneStatus(t *testing.T) {
	s := loopbackTestServer(t, nil)
	var p bm.Project
	b41Call(t, s, "POST", "/api/backend-projects", bm.CreateInput{Name: "Diagram", IdempotencyKey: "diagram"}, 201, &p)
	for _, body := range []string{`[]`, `not-json`, `{"document":null,"idempotencyKey":"bad"}`} {
		f := decodeBackendFailure(t, b41Call(t, s, "POST", "/api/backend-projects/"+p.ID+"/diagrams", body, 422, nil))
		if f.Error.Code != "backend_invalid" {
			t.Errorf("%s: code %q", body, f.Error.Code)
		}
	}
}

// TestGraphQueryMalformedSelectorIsOneClass pins review 2026-10-06, F17: a
// non-canonical parentId, from or to was refused as 422
// backend_import_invalid (an import code on a read), while the same mistake in
// id is 400 backend_invalid. A selector the recordType forbids stays 422.
func TestGraphQueryMalformedSelectorIsOneClass(t *testing.T) {
	s := loopbackTestServer(t, nil)
	var p bm.Project
	b41Call(t, s, "POST", "/api/backend-projects", bm.CreateInput{Name: "Selectors", IdempotencyKey: "selectors"}, 201, &p)
	path := "/api/backend-projects/" + p.ID + "/graph/query"
	for _, tc := range []struct{ recordType, key string }{{"nodes", "id"}, {"nodes", "parentId"}, {"edges", "from"}, {"edges", "to"}} {
		body := `{"revisionId":"` + p.CurrentRevisionID + `","recordType":"` + tc.recordType + `","` + tc.key + `":"nope"}`
		f := decodeBackendFailure(t, b41Call(t, s, "POST", path, body, 400, nil))
		if f.Error.Code != "backend_invalid" || f.Error.Details["path"] != "/"+tc.key {
			t.Errorf("%s: %+v", tc.key, f.Error)
		}
	}
	body := `{"revisionId":"` + p.CurrentRevisionID + `","recordType":"edges","parentId":"nope"}`
	if f := decodeBackendFailure(t, b41Call(t, s, "POST", path, body, 422, nil)); f.Error.Details["path"] != "/parentId" {
		t.Errorf("forbidden selector: %+v", f.Error)
	}
}

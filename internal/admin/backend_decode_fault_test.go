package admin

import (
	"encoding/json/v2"
	"strings"
	"testing"

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

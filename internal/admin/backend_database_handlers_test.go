package admin

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/jsonx"
)

// A pinned schema1 read must reach the shared domain refusal, not a missing route.
func TestBackendDatabaseRouteReachesPinnedDomain(t *testing.T) {
	s := loopbackTestServer(t, nil)
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Database", IdempotencyKey: "database"})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"revisionId":"` + p.CurrentRevisionID + `","datastoreId":"` + p.ID + `","facetKey":"sql","recordType":"tables"}`
	status, data, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", "/api/backend-projects/"+p.ID+"/database/query", []byte(body))
	if err != nil || status != http.StatusNotFound || !strings.Contains(string(data), "backend_not_found") {
		t.Fatalf("missing datastore must be domain404: %d %s %v", status, data, err)
	}
	var response struct {
		Error backendmodel.FaultError `json:"error"`
	}
	if err := json.Unmarshal(data, &response); err != nil || response.Error.Code != "backend_not_found" {
		t.Fatalf("database domain error envelope: %s %v", data, err)
	}
}

func TestBackendRelationalOpenAPICommandTemplates(t *testing.T) {
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	decode := func(raw []byte) any {
		t.Helper()
		decoder := jsonx.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	compiler := jsonschema.NewCompiler()
	const uri = "https://mocker.invalid/task4-openapi.json"
	if err := compiler.AddResource(uri, decode(raw)); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(uri + "#/components/schemas/BackendImportCommand")
	if err != nil {
		t.Fatal(err)
	}
	for _, dialect := range []string{"postgresql", "sqlite"} {
		for _, version := range []string{"v1", "v2"} {
			t.Run(dialect+"/"+version, func(t *testing.T) {
				data, err := os.ReadFile("../backendmodel/testdata/relational/orders/" + dialect + "/" + version + "/commands.json")
				if err != nil {
					t.Fatal(err)
				}
				const id = "0197aaf9-5555-7000-8000-000000000001"
				data = []byte(strings.NewReplacer("@repositoryId@", id, "@snapshotId@", id, "@v1RevisionId@", id, "@v1Object:column:orders:legacy_note@", id, "@v1Object:index:orders:legacy_note_idx@", id).Replace(string(data)))
				for _, command := range decode(data).([]any) {
					if err := schema.Validate(command); err != nil {
						t.Fatalf("source-authored relational command rejected: %v", err)
					}
				}
			})
		}
	}
}

func TestBackendRelationalCapabilities(t *testing.T) {
	s := loopbackTestServer(t, nil)
	status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", "/api/backend-projects/capabilities", nil)
	if err != nil || status != 200 {
		t.Fatalf("%d %s %v", status, raw, err)
	}
	var capabilities struct {
		ModelSchemaVersions []string `json:"modelSchemaVersions"`
		ProviderProfiles    []string `json:"providerProfiles"`
		ProfileCapabilities []struct {
			Profile             string   `json:"profile"`
			ModelSchemaVersions []string `json:"modelSchemaVersions"`
		} `json:"profileCapabilities"`
		ProfileExtensions []backendmodel.ImportProfileExtension `json:"profileExtensions"`
		Features          []string                              `json:"features"`
		Limits            map[string]int64                      `json:"limits"`
	}
	if err := json.Unmarshal(raw, &capabilities); err != nil {
		t.Fatal(err)
	}
	if strings.Join(capabilities.ModelSchemaVersions, ",") != "1,2,3,4,5,6" || strings.Join(capabilities.ProviderProfiles, ",") != "foundation-graph-v1,relational-graph-v1,runtime-flow-v1,field-lineage-v1,events-service-v1,composed-source-v1" || len(capabilities.ProfileCapabilities) != 6 || len(capabilities.ProfileExtensions) != 5 {
		t.Fatalf("incomplete profile advertisement: %s", raw)
	}
	for name, want := range map[string]int64{"maxRelationalFacets": 16, "maxRelationalOrderedColumns": 64, "maxRelationalIndexTerms": 64, "maxRelationalNativeBytes": 65536, "maxRelationalReferences": 500, "defaultDatabasePageSize": 100, "maxDatabasePageSize": 500} {
		if capabilities.Limits[name] != want {
			t.Errorf("%s=%d want %d", name, capabilities.Limits[name], want)
		}
	}
	for _, feature := range []string{"backend-relational-import", "backend-database-query", "backend-database-er"} {
		if !slices.Contains(capabilities.Features, feature) {
			t.Errorf("missing %s", feature)
		}
	}
}

func TestBackendDatabaseWireSelectorPresence(t *testing.T) {
	s := loopbackTestServer(t, nil)
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Wire", IdempotencyKey: "wire"})
	if err != nil {
		t.Fatal(err)
	}
	prefix := `{"revisionId":"` + p.CurrentRevisionID + `","datastoreId":"` + p.ID + `","facetKey":"sql","recordType":`
	for _, body := range []string{
		prefix + `"relationships","search":""}`,
		prefix + `"tables","tableId":""}`,
		prefix + `"tables","limit":0}`,
		prefix + `"tables","limit":null}`,
		prefix + `"tables","unknown":true}`,
		prefix + `"tables","facetKey":"sql"}`,
		prefix + `null}`,
	} {
		status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", "/api/backend-projects/"+p.ID+"/database/query", []byte(body))
		if err != nil || status != 400 || !strings.Contains(string(raw), "backend_invalid") {
			t.Errorf("invalid selector reached domain: %s -> %d %s %v", body, status, raw, err)
		}
	}
	for _, body := range []string{prefix + `"tables"}`, prefix + `"tables","search":""}`} {
		status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", "/api/backend-projects/"+p.ID+"/database/query", []byte(body))
		if err != nil || status != 404 {
			t.Errorf("allowed omission/empty search: %d %s %v", status, raw, err)
		}
	}
}

func TestBackendBeginProfilePresence(t *testing.T) {
	for _, value := range []string{`""`, `null`, `42`, `"wrong"`} {
		err := backendImportShape(jsontext.Value(`{"profile":`+value+`}`), reflect.TypeFor[backendmodel.BeginImportInput](), "")
		if err == nil || !strings.Contains(err.Error(), "/profile") {
			t.Fatalf("profile presence lost: %s %v", value, err)
		}
	}
}

func TestBackendDatabaseRouteNeverCreatesCheckpoint(t *testing.T) {
	for _, route := range (&Server{}).routes() {
		if route.pattern == "POST /api/backend-projects/{id}/database/query" {
			if route.checkpoint != cpNeverTouchesLayer {
				t.Fatalf("database route checkpoint policy: %+v", route.checkpoint)
			}
			return
		}
	}
	t.Fatal("database route missing")
}

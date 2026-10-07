package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendSavedViewToolRoutesAndPrecision(t *testing.T) {
	const id = backendTestID
	const state = `"state":{"kind":"flow","scope":{},"filters":{"search":"","accessKind":"","reverseAccessKind":""},"selection":null,"positions":[],"collapsedGroupIds":[]}`
	for _, tc := range []struct{ name, args, method, path string }{
		{"list_backend_saved_views", `{"projectId":"` + id + `","kind":"flow","limit":100}`, "GET", "/saved-views?kind=flow&limit=100"},
		{"get_backend_saved_view", `{"projectId":"` + id + `","viewId":"` + id + `","version":9007199254740993}`, "GET", "/saved-views/" + id + "?version=9007199254740993"},
		{"create_backend_saved_view", `{"projectId":"` + id + `","name":"Flow","target":{"revisionId":"` + id + `"},` + state + `,"idempotencyKey":"create"}`, "POST", "/saved-views"},
		{"save_backend_saved_view", `{"projectId":"` + id + `","viewId":"` + id + `","name":"Flow",` + state + `,"expectedVersion":9007199254740993,"idempotencyKey":"save"}`, "POST", "/saved-views/" + id + "/save"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := &recordingCaller{status: 200, body: []byte(`{"version":9007199254740995}`)}
			out, msg := callTool(t, calls, tc.name, tc.args)
			if msg != "" {
				t.Fatal(msg)
			}
			if calls.method != tc.method || calls.path != "/api/backend-projects/"+id+tc.path {
				t.Fatal(calls.method, calls.path)
			}
			if !strings.Contains(string(out), "9007199254740995") {
				t.Fatal("rounded result")
			}
			if strings.Contains(tc.args, "expectedVersion") && !strings.Contains(string(calls.sent), "9007199254740993") {
				t.Fatal("rounded expected version", string(calls.sent))
			}
		})
	}
}

// Executes the database4 saved-view guide example through the public SDK and
// real admin/store, using independently authored inert SQL/Go fixture records.
func TestBackendSavedViewGuideDatabaseSDKExample(t *testing.T) {
	for _, dialect := range []string{"postgresql", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			server, _ := newResourcesTestServer(t, resourcesTestConfig(t))
			fixture := newToolFixture(server)
			encode := func(value any) []byte {
				t.Helper()
				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			call := func(name string, input map[string]any, out any) []byte {
				t.Helper()
				raw, message := fixture.Call(t, name, string(encode(input)))
				if message != "" {
					t.Fatalf("%s: %s", name, message)
				}
				if out != nil {
					if err := json.Unmarshal(raw, out); err != nil {
						t.Fatal(err)
					}
				}
				return raw
			}
			var project backendmodel.Project
			call("create_backend_project", map[string]any{"name": "Saved " + dialect, "idempotencyKey": "saved-project"}, &project)
			fixtureNames := []string{"schema.sql", "models.go", "migrations/001_initial.sql", "migrations/002_unsupported.sql"}
			files := make([]backendmodel.ManifestFile, 0, len(fixtureNames))
			root := filepath.Join("../backendmodel/testdata/relational/orders", dialect, "v1")
			for _, name := range fixtureNames {
				raw, err := os.ReadFile(filepath.Join(root, name))
				if err != nil {
					t.Fatal(err)
				}
				hash := sha256.Sum256(raw)
				files = append(files, backendmodel.ManifestFile{Path: dialect + "/v1/" + name, ContentHash: hex.EncodeToString(hash[:]), FileType: strings.TrimPrefix(filepath.Ext(name), "."), AnalysisStatus: "analyzed"})
			}
			inventory := []backendmodel.InventoryItem{}
			for category := range strings.FieldsSeq("files endpoints datastores migrations producers consumers jobs contracts tests") {
				count := int64(0)
				switch category {
				case "files":
					count = 4
				case "datastores":
					count = 1
				case "migrations":
					count = 2
				}
				inventory = append(inventory, backendmodel.InventoryItem{Category: category, Status: "complete", KnownCount: count, Denominator: new(count), DiscoverySource: "source fixture", Gaps: []string{}})
			}
			var session backendmodel.ImportSession
			call("begin_backend_import", map[string]any{"projectId": project.ID, "profile": backendmodel.RelationalProfile, "expectedVersion": project.Version, "baseRevisionId": project.CurrentRevisionID, "idempotencyKey": "saved-import", "manifest": backendmodel.SourceManifest{RepositoryName: "orders", Provider: backendmodel.SourceProvider{Name: "orders-fixtures", Version: "1", Namespace: "orders-fixtures", Method: "agent", Profiles: []string{backendmodel.GraphProfile, backendmodel.RelationalProfile}, Limitations: []string{}}, Snapshot: backendmodel.SnapshotManifest{Consistency: "verified", CapturedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), Files: files}}, "inventory": inventory}, &session)
			raw, err := os.ReadFile(filepath.Join(root, "commands.json"))
			if err != nil {
				t.Fatal(err)
			}
			var commands []backendmodel.ImportCommand
			if err := json.Unmarshal([]byte(strings.NewReplacer("@repositoryId@", session.RepositoryID, "@snapshotId@", session.SnapshotID).Replace(string(raw))), &commands); err != nil {
				t.Fatal(err)
			}
			hash, err := backendmodel.ImportBatchHash(commands)
			if err != nil {
				t.Fatal(err)
			}
			var receipt backendmodel.BatchReceipt
			call("put_backend_import_batch", map[string]any{"projectId": project.ID, "importId": session.ID, "batchId": "saved-fixture", "expectedImportVersion": session.Version, "payloadHash": hash, "commands": commands}, &receipt)
			ids := map[string]string{}
			for _, identity := range receipt.Identities {
				ids[identity.ExternalKey] = identity.ID
			}
			var preview backendmodel.ImportPreview
			call("preview_backend_import", map[string]any{"projectId": project.ID, "importId": session.ID, "expectedImportVersion": receipt.AcceptedVersion, "baseRevisionId": project.CurrentRevisionID}, &preview)
			if preview.CandidateHash == nil {
				t.Fatalf("fixture import not ready: %+v", preview)
			}
			var committed backendmodel.ImportCommitResult
			call("commit_backend_import", map[string]any{"projectId": project.ID, "importId": session.ID, "expectedVersion": project.Version, "expectedImportVersion": preview.Version, "candidateHash": *preview.CandidateHash, "idempotencyKey": "saved-commit"}, &committed)
			table := ids["table:orders"]
			schema := ""
			for _, command := range commands {
				if command.Node != nil && command.Node.Kind == "db_schema" {
					schema = ids[command.Node.ExternalKey]
					break
				}
			}
			state := map[string]any{"kind": "database", "scope": map[string]any{"datastoreId": ids["database:orders"], "facetKey": "sql"}, "filters": map[string]any{"search": "orders", "relationshipTableId": table}, "selection": map[string]any{"recordType": "node", "id": table}, "positions": []map[string]any{{"nodeId": table, "x": 320, "y": -20}}, "collapsedGroupIds": []string{schema}}
			create := map[string]any{"projectId": project.ID, "name": "Orders ER", "target": map[string]any{"revisionId": committed.Revision.ID}, "state": state, "idempotencyKey": "database-view-create-1"}
			var first backendmodel.SavedView
			original := call("create_backend_saved_view", create, &first)
			call("list_backend_saved_views", map[string]any{"projectId": project.ID, "kind": "database", "limit": 50}, nil)
			save := map[string]any{"projectId": project.ID, "viewId": first.ID, "name": "Orders ER arranged", "state": state, "expectedVersion": 1, "idempotencyKey": "database-view-save-2"}
			var second backendmodel.SavedView
			acknowledgement := call("save_backend_saved_view", save, &second)
			if second.Version != 2 {
				t.Fatal("save did not append version2")
			}
			// Simulate losing the acknowledgement. Retry remains exactly byte-identical.
			if replay := call("save_backend_saved_view", save, nil); !bytes.Equal(acknowledgement, replay) {
				t.Fatal("uncertain retry changed response")
			}
			if historical := call("get_backend_saved_view", map[string]any{"projectId": project.ID, "viewId": first.ID, "version": 1}, nil); !bytes.Equal(original, historical) {
				t.Fatal("version1 substituted newer presentation")
			}
			stale := map[string]any{"projectId": project.ID, "viewId": first.ID, "name": "Stale local edit", "state": state, "expectedVersion": 1, "idempotencyKey": "database-view-conflict"}
			failure, message := fixture.Call(t, "save_backend_saved_view", string(encode(stale)))
			if !strings.Contains(string(failure)+message, "backend_version_conflict") || !strings.Contains(string(failure)+message, "currentVersion") {
				t.Fatalf("old CAS did not return recovery fields: %s %s", failure, message)
			}
			call("query_backend_database", map[string]any{"projectId": project.ID, "revisionId": first.Pins.RevisionID, "datastoreId": ids["database:orders"], "facetKey": "sql", "recordType": "tables", "search": "orders"}, nil)
			call("get_backend_project", map[string]any{"projectId": project.ID}, &project)
			if project.Version != committed.Project.Version || project.CurrentRevisionID != committed.Revision.ID {
				t.Fatal("saved presentation changed source")
			}
		})
	}
}

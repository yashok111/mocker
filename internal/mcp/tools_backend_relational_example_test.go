package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yashok111/mocker/api"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/jsonx"
)

// This example imports the independently authored source templates. The fixture
// SQL and Go files are read only for manifest hashes and never executed.
func TestBackendRelationalRealSDKFixtureExample(t *testing.T) {
	for _, dialect := range []string{"postgresql", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			cfg := resourcesTestConfig(t)
			server, db := newResourcesTestServer(t, cfg)
			fixture := newToolFixture(server)
			captures := []map[string]jsontext.Value{}
			directory := os.Getenv("MOCKER_B11_TASK4_CAPTURE_DIR")
			if directory == "" {
				directory = os.Getenv("MOCKER_B12_TASK4_CAPTURE_DIR")
			}
			if directory != "" {
				t.Cleanup(func() {
					if t.Failed() {
						return
					}
					raw, err := json.Marshal(captures)
					if err != nil {
						t.Error(err)
						return
					}
					if err := os.WriteFile(filepath.Join(directory, "task-4-sdk-"+dialect+"-responses.json"), raw, 0600); err != nil {
						t.Error(err)
					}
				})
			}
			encode := func(v any) []byte {
				t.Helper()
				raw, err := json.Marshal(v)
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			responseContracts := map[string]string{"begin_backend_import": "BackendImportSession", "put_backend_import_batch": "BackendBatchReceipt", "preview_backend_import": "BackendImportPreview", "commit_backend_import": "BackendImportCommitResult", "query_backend_database": "BackendDatabasePage", "query_backend_graph": "BackendGraphPage", "get_backend_node": "BackendNode", "get_backend_evidence": "BackendEvidencePage"}
			for tool, schema := range map[string]string{"create_backend_proposal": "BackendProposalDetail", "get_backend_proposal": "BackendProposalDetail", "list_backend_proposals": "BackendProposalPage", "preview_backend_proposal_commands": "BackendProposalPreview", "apply_backend_proposal_commands": "BackendProposalApplyResult"} {
				responseContracts[tool] = schema
			}
			responseValidators := map[string]*jsonschema.Schema{}
			call := func(name string, input map[string]any, out any) []byte {
				t.Helper()
				raw, msg := fixture.Call(t, name, string(encode(input)))
				if msg != "" {
					t.Fatalf("%s: %s", name, msg)
				}
				captures = append(captures, map[string]jsontext.Value{"tool": encode(name), "arguments": encode(input), "response": jsontext.Value(raw)})
				if name := responseContracts[name]; name != "" {
					if name == "BackendNode" && input["proposal"] != nil {
						name = "BackendProposalNodeRead"
					}
					compiled := responseValidators[name]
					if compiled == nil {
						schema, err := api.BackendSchema(name)
						if err != nil {
							t.Fatal(err)
						}
						compiler := jsonschema.NewCompiler()
						if err := compiler.AddResource("https://mocker.invalid/response", schema); err != nil {
							t.Fatal(err)
						}
						compiled, err = compiler.Compile("https://mocker.invalid/response")
						if err != nil {
							t.Fatal(err)
						}
						responseValidators[name] = compiled
					}
					decoder := jsonx.NewDecoder(bytes.NewReader(raw))
					decoder.UseNumber()
					var value any
					if err := decoder.Decode(&value); err != nil {
						t.Fatal(err)
					}
					if err := compiled.Validate(value); err != nil {
						t.Fatalf("%s response contract: %v", name, err)
					}
				}
				if out != nil {
					if err := json.Unmarshal(raw, out); err != nil {
						t.Fatal(err)
					}
				}
				return raw
			}
			var p backendmodel.Project
			call("create_backend_project", map[string]any{"name": dialect, "idempotencyKey": "create"}, &p)
			root := filepath.Join("../backendmodel/testdata/relational/orders", dialect)
			begin := func(version, key string, repo string, partial bool) backendmodel.ImportSession {
				in := backendmodel.BeginImportInput{Profile: backendmodel.RelationalProfile, ExpectedVersion: p.Version, BaseRevisionID: p.CurrentRevisionID, IdempotencyKey: key}
				in.Manifest = backendmodel.SourceManifest{RepositoryName: "orders-fixture", Provider: backendmodel.SourceProvider{Name: "orders-fixtures", Version: "1", Namespace: "orders-fixtures", Method: "agent", Profiles: []string{backendmodel.GraphProfile, backendmodel.RelationalProfile}, Limitations: []string{}}, Snapshot: backendmodel.SnapshotManifest{Consistency: "verified", CapturedAt: time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC), Files: []backendmodel.ManifestFile{}}}
				paths := []string{"schema.sql", "models.go", "migrations/001_initial.sql", "migrations/002_unsupported.sql"}
				if version == "v2" {
					paths = append(paths, "migrations/003_column_change.sql")
				}
				for _, name := range paths {
					raw, err := os.ReadFile(filepath.Join(root, version, name))
					if err != nil {
						t.Fatal(err)
					}
					hash := sha256.Sum256(raw)
					in.Manifest.Snapshot.Files = append(in.Manifest.Snapshot.Files, backendmodel.ManifestFile{Path: filepath.Join(dialect, version, name), ContentHash: hex.EncodeToString(hash[:]), FileType: strings.TrimPrefix(filepath.Ext(name), "."), AnalysisStatus: "analyzed"})
				}
				for _, category := range strings.Fields("files endpoints datastores migrations producers consumers jobs contracts tests") {
					n := int64(0)
					switch category {
					case "files":
						n = int64(len(paths))
					case "datastores":
						n = 1
					case "migrations":
						n = int64(len(paths) - 2)
					}
					in.Inventory = append(in.Inventory, backendmodel.InventoryItem{Category: category, Status: "complete", KnownCount: n, Denominator: new(n), DiscoverySource: "orders-fixtures", Gaps: []string{}, Reason: ""})
				}
				if repo != "" {
					in.Mode = "reconcile"
					in.RepositoryID = new(repo)
					in.GraphScope = &backendmodel.GraphScope{Profile: backendmodel.RelationalProfile, Status: "complete", Gaps: []string{}}
				}
				if partial {
					in.GraphScope.Status = "partial"
					in.GraphScope.Gaps = []string{"Only orders SQL reobserved"}
					for i := range in.Inventory {
						if in.Inventory[i].Category == "datastores" {
							in.Inventory[i].Status = "partial"
							in.Inventory[i].KnownCount = 0
							in.Inventory[i].Denominator = nil
							in.Inventory[i].Gaps = []string{"Datastore not reobserved"}
						}
					}
				}
				var fields map[string]any
				decoder := jsonx.NewDecoder(bytes.NewReader(encode(in)))
				decoder.UseNumber()
				if err := decoder.Decode(&fields); err != nil {
					t.Fatal(err)
				}
				fields["projectId"] = p.ID
				var session backendmodel.ImportSession
				call("begin_backend_import", fields, &session)
				return session
			}
			template := func(version string, s backendmodel.ImportSession, revision string, ids map[string]string) []backendmodel.ImportCommand {
				raw, err := os.ReadFile(filepath.Join(root, version, "commands.json"))
				if err != nil {
					t.Fatal(err)
				}
				replacements := []string{"@repositoryId@", s.RepositoryID, "@snapshotId@", s.SnapshotID, "@v1RevisionId@", revision}
				for _, key := range []string{"column:orders:legacy_note", "index:orders:legacy_note_idx"} {
					replacements = append(replacements, "@v1Object:"+key+"@", ids[key])
				}
				raw = []byte(strings.NewReplacer(replacements...).Replace(string(raw)))
				var commands []backendmodel.ImportCommand
				if err := json.Unmarshal(raw, &commands); err != nil {
					t.Fatal(err)
				}
				return commands
			}
			ids := map[string]string{}
			batch := func(s *backendmodel.ImportSession, key string, commands []backendmodel.ImportCommand) {
				hash, err := backendmodel.ImportBatchHash(commands)
				if err != nil {
					t.Fatal(err)
				}
				var receipt backendmodel.BatchReceipt
				call("put_backend_import_batch", map[string]any{"projectId": p.ID, "importId": s.ID, "batchId": key, "expectedImportVersion": s.Version, "payloadHash": hash, "commands": commands}, &receipt)
				s.Version = receipt.AcceptedVersion
				for _, identity := range receipt.Identities {
					ids[identity.ExternalKey] = identity.ID
				}
			}
			commit := func(s backendmodel.ImportSession, key string) backendmodel.ImportCommitResult {
				var preview backendmodel.ImportPreview
				call("preview_backend_import", map[string]any{"projectId": p.ID, "importId": s.ID, "expectedImportVersion": s.Version, "baseRevisionId": p.CurrentRevisionID}, &preview)
				if preview.State != "ready" || preview.CandidateHash == nil {
					t.Fatalf("preview: %+v", preview)
				}
				input := map[string]any{"projectId": p.ID, "importId": s.ID, "expectedVersion": p.Version, "expectedImportVersion": preview.Version, "candidateHash": *preview.CandidateHash, "idempotencyKey": key}
				var out backendmodel.ImportCommitResult
				receipt := call("commit_backend_import", input, &out)
				// Lost response recovery must return the original acknowledged bytes.
				if replay := call("commit_backend_import", input, nil); !bytes.Equal(receipt, replay) {
					t.Fatal("commit replay changed bytes")
				}
				p = out.Project
				return out
			}
			s := begin("v1", "v1", "", false)
			original := template("v1", s, "", ids)
			for _, tc := range []struct {
				name, field, value string
				status             int
			}{
				{"scalar-union", "nullable", `{"status":"unknown","reason":"missing","value":false}`, 422},
				{"ordinal-overflow", "ordinal", `{"status":"known","value":9223372036854775808}`, 422},
				{"stored-evidence", "evidenceIds", `[]`, 422},
				{"stored-freshness", "freshness", `{"status":"current"}`, 422},
				{"stored-snapshot", "sourceSnapshotId", string(encode(s.SnapshotID)), 422},
				{"facet-quota", "", "", 413},
				{"unknown-field", "surprise", `true`, 422},
				{"native-bytes", "nativeType", string(encode(map[string]any{"status": "known", "value": strings.Repeat("雪", backendmodel.MaxRelationalNativeBytes/3+1)})), 413},
			} {
				var commands []backendmodel.ImportCommand
				if err := json.Unmarshal(encode(original), &commands); err != nil {
					t.Fatal(err)
				}
				for i := range commands {
					n := commands[i].Node
					if n == nil || n.ExternalKey != "column:orders:total" {
						continue
					}
					var facets map[string]map[string]jsontext.Value
					if err := json.Unmarshal(n.Attributes["facets"], &facets); err != nil {
						t.Fatal(err)
					}
					if tc.name == "facet-quota" {
						selected := facets["sql"]
						facets = map[string]map[string]jsontext.Value{}
						for i := range backendmodel.MaxRelationalFacets + 1 {
							facets[fmt.Sprintf("facet%d", i)] = selected
						}
					} else {
						facets["sql"][tc.field] = jsontext.Value(tc.value)
					}
					n.Attributes["facets"] = encode(facets)
				}
				hash, err := backendmodel.ImportBatchHash(commands)
				if err != nil {
					t.Fatal(err)
				}
				input := map[string]any{"projectId": p.ID, "importId": s.ID, "batchId": tc.name, "expectedImportVersion": s.Version, "payloadHash": hash, "commands": commands}
				_, msg := fixture.Call(t, "put_backend_import_batch", string(encode(input)))
				if msg == "" {
					t.Fatalf("%s malformed batch accepted by SDK", tc.name)
				}
				delete(input, "projectId")
				delete(input, "importId")
				delete(input, "batchId")
				status, raw, err := server.CallAsMCP(t.Context(), httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", nil), "PUT", "/api/backend-projects/"+p.ID+"/imports/"+s.ID+"/batches/"+tc.name, encode(input))
				if err != nil || status != tc.status {
					t.Fatalf("%s REST domain error: %d %s %v", tc.name, status, raw, err)
				}
			}
			batch(&s, "v1", original)
			first := commit(s, "v1-commit")
			proposalSDKFixtureExample(t, dialect, p, ids, call, func() {
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				server, db = newResourcesTestServer(t, cfg)
				fixture = newToolFixture(server)
			})
			for _, record := range []string{"nodes", "edges"} {
				call("query_backend_graph", map[string]any{"projectId": p.ID, "revisionId": first.Revision.ID, "recordType": record}, nil)
			}
			datastore := ids["database:orders"]
			if datastore == "" {
				t.Fatal("no datastore identity")
			}
			query := func(revision, record string) backendmodel.DatabasePage {
				input := map[string]any{"projectId": p.ID, "revisionId": revision, "datastoreId": datastore, "facetKey": "sql", "recordType": record}
				var page backendmodel.DatabasePage
				sdkRaw := call("query_backend_database", input, &page)
				delete(input, "projectId")
				status, restRaw, err := server.CallAsMCP(t.Context(), httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", nil), "POST", "/api/backend-projects/"+p.ID+"/database/query", encode(input))
				if err != nil || status != 200 || !bytes.Equal(sdkRaw, restRaw) {
					t.Fatalf("SDK/REST differ: %d %s %v", status, restRaw, err)
				}
				if page.RevisionID != revision || page.SemanticHash == "" {
					t.Fatal("revision pin lost")
				}
				return page
			}
			tables := query(first.Revision.ID, "tables")
			if len(tables.TableItems) != 4 {
				t.Fatalf("source tables: %+v", tables.TableItems)
			}
			fks := query(first.Revision.ID, "relationships")
			if len(fks.RelationshipItems) != 3 {
				t.Fatalf("source FK count: %+v", fks.RelationshipItems)
			}
			for _, fk := range fks.RelationshipItems {
				if len(fk.EvidenceIDs) == 0 {
					t.Fatal("FK without evidence")
				}
			}
			var node backendmodel.Node
			call("get_backend_node", map[string]any{"projectId": p.ID, "revisionId": first.Revision.ID, "nodeId": ids["column:orders:total"]}, &node)
			if node.FacetComparison == nil || node.FacetComparison.Status != "different" {
				t.Fatalf("SQL/ORM drift lost: %+v", node.FacetComparison)
			}
			var evidence backendmodel.EvidencePage
			call("get_backend_evidence", map[string]any{"projectId": p.ID, "revisionId": first.Revision.ID, "subjectId": node.ID}, &evidence)
			if len(evidence.Items) == 0 {
				t.Fatal("node source proof missing")
			}
			partial := begin("v1", "partial", s.RepositoryID, true)
			selected := []backendmodel.ImportCommand{}
			for _, command := range template("v1", partial, "", ids) {
				if command.Node != nil && command.Node.ExternalKey == "table:orders" {
					var facets map[string]jsontext.Value
					if err := json.Unmarshal(command.Node.Attributes["facets"], &facets); err != nil {
						t.Fatal(err)
					}
					delete(facets, "orm")
					delete(facets, "migration")
					command.Node.Attributes["facets"] = encode(facets)
					command.Node.EvidenceKeys = []string{"proof:v1:table:orders:sql"}
					selected = append(selected, command)
				}
				if command.Evidence != nil && command.Evidence.ExternalKey == "proof:v1:table:orders:sql" {
					selected = append(selected, command)
				}
			}
			batch(&partial, "partial", selected)
			partialOut := commit(partial, "partial-commit")
			if partialOut.Revision.Coverage.Status != "partial" {
				t.Fatal("partial coverage lost")
			}
			call("get_backend_node", map[string]any{"projectId": p.ID, "revisionId": p.CurrentRevisionID, "nodeId": ids["column:orders:status"]}, &node)
			if node.Freshness == nil || node.Freshness.Status != "stale" {
				t.Fatal("omitted column no longer stale")
			}
			next := begin("v2", "v2", s.RepositoryID, false)
			batch(&next, "mapping", []backendmodel.ImportCommand{{Op: "map_identity", Identity: &backendmodel.ImportIdentityMap{RecordType: "node", FromExternalKey: "column:orders:status", ToExternalKey: "column:orders:state", ExpectedID: ids["column:orders:status"], Reason: "Explicit source rename", EvidenceKeys: []string{"proof:v2:column:orders:state:sql"}}}})
			commands := template("v2", next, first.Revision.ID, ids)
			for _, key := range []string{"column:orders:legacy_note", "index:orders:legacy_note_idx"} {
				for _, record := range []string{"node", "edge"} {
					external := key
					if record == "edge" {
						external = "contains:" + key
					}
					commands = append(commands, backendmodel.ImportCommand{Op: "delete_assertion", Deletion: &backendmodel.ImportDeletion{RecordType: record, ExternalKey: external, ExpectedID: ids[external], Reason: "Reviewed source removes object"}})
				}
			}
			oldID := ids["column:orders:status"]
			batch(&next, "v2", commands)
			last := commit(next, "v2-commit")
			if ids["column:orders:state"] != oldID {
				t.Fatal("explicit rename allocated a new UUID")
			}
			query(last.Revision.ID, "tables")
			query(last.Revision.ID, "relationships")
			// The original pinned read remains byte-equal after later imports.
			again := query(first.Revision.ID, "tables")
			if !bytes.Equal(encode(tables), encode(again)) {
				t.Fatal("historical projection changed")
			}
		})
	}
}

package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/yashok111/mocker/api"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/jsonx"
)

func TestBackendFlowRealSDKSourceFixture(t *testing.T) {
	for _, dialect := range []string{"postgresql", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			server, _ := newResourcesTestServer(t, resourcesTestConfig(t))
			fixture := newToolFixture(server)
			captures := []map[string]jsontext.Value{}
			if directory := os.Getenv("MOCKER_B21_SDK_CAPTURE_DIR"); directory != "" {
				t.Cleanup(func() {
					if t.Failed() {
						return
					}
					raw, err := json.Marshal(captures)
					if err == nil {
						err = os.WriteFile(filepath.Join(directory, "runtime-sdk-"+dialect+".json"), raw, 0600)
					}
					if err != nil {
						t.Error(err)
					}
				})
			}
			encode := func(v any) []byte {
				t.Helper()
				b, e := json.Marshal(v)
				if e != nil {
					t.Fatal(e)
				}
				return b
			}
			responseValidators := map[string]*jsonschema.Schema{}
			call := func(name string, input map[string]any, out any) []byte {
				t.Helper()
				raw, msg := fixture.Call(t, name, string(encode(input)))
				if msg != "" {
					t.Fatalf("%s: %s", name, msg)
				}
				captures = append(captures, map[string]jsontext.Value{"tool": encode(name), "arguments": encode(input), "response": raw})
				if name == "query_backend_flow" {
					const schemaName = "BackendFlowPage"
					compiled := responseValidators[schemaName]
					if compiled == nil {
						schema, e := api.BackendSchema("BackendFlowPage")
						if e != nil {
							t.Fatal(e)
						}
						compiler := jsonschema.NewCompiler()
						if e = compiler.AddResource("https://mocker.invalid/flow", schema); e != nil {
							t.Fatal(e)
						}
						compiled, e = compiler.Compile("https://mocker.invalid/flow")
						if e != nil {
							t.Fatal(e)
						}
						responseValidators[schemaName] = compiled
					}
					decoder := jsonx.NewDecoder(bytes.NewReader(raw))
					decoder.UseNumber()
					var value any
					if e := decoder.Decode(&value); e != nil {
						t.Fatal(e)
					}
					if e := compiled.Validate(value); e != nil {
						t.Fatalf("flow response schema: %v", e)
					}
				}
				if out != nil {
					if e := json.Unmarshal(raw, out); e != nil {
						t.Fatal(e)
					}
				}
				return raw
			}
			var project backendmodel.Project
			call("create_backend_project", map[string]any{"name": "Cancel " + dialect, "idempotencyKey": "flow-create"}, &project)
			in := backendmodel.BeginImportInput{Profile: backendmodel.RuntimeProfile, ExpectedVersion: project.Version, BaseRevisionID: project.CurrentRevisionID, IdempotencyKey: "flow-import"}
			in.Manifest = backendmodel.SourceManifest{RepositoryName: "orders", Provider: backendmodel.SourceProvider{Name: "orders-fixtures", Version: "1", Namespace: "orders-fixtures", Method: "agent", Profiles: []string{backendmodel.GraphProfile, backendmodel.RelationalProfile, backendmodel.RuntimeProfile}, Limitations: []string{}}, Snapshot: backendmodel.SnapshotManifest{Consistency: "verified", CapturedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), Files: []backendmodel.ManifestFile{}}}
			for _, name := range []string{"schema.sql", "models.go", "migrations/001_initial.sql", "migrations/002_unsupported.sql"} {
				b, e := os.ReadFile(filepath.Join("../backendmodel/testdata/relational/orders", dialect, "v1", name))
				if e != nil {
					t.Fatal(e)
				}
				hash := sha256.Sum256(b)
				in.Manifest.Snapshot.Files = append(in.Manifest.Snapshot.Files, backendmodel.ManifestFile{Path: dialect + "/v1/" + name, ContentHash: hex.EncodeToString(hash[:]), FileType: strings.TrimPrefix(filepath.Ext(name), "."), AnalysisStatus: "analyzed"})
			}
			b, e := os.ReadFile("../backendmodel/testdata/runtime/orders/cancel.go.txt")
			if e != nil {
				t.Fatal(e)
			}
			hash := sha256.Sum256(b)
			sourceHash := hex.EncodeToString(hash[:])
			sourceLines := int64(bytes.Count(b, []byte{'\n'}))
			in.Manifest.Snapshot.Files = append(in.Manifest.Snapshot.Files, backendmodel.ManifestFile{Path: "cancel.go", ContentHash: sourceHash, FileType: "go", AnalysisStatus: "analyzed"})
			for _, category := range strings.Fields("files endpoints datastores migrations producers consumers jobs contracts tests") {
				n := int64(0)
				switch category {
				case "files":
					n = 5
				case "endpoints", "datastores":
					n = 1
				case "migrations":
					n = 2
				}
				in.Inventory = append(in.Inventory, backendmodel.InventoryItem{Category: category, Status: "complete", KnownCount: n, Denominator: new(n), DiscoverySource: "source fixture", Gaps: []string{}})
			}
			var fields map[string]any
			decoder := jsonx.NewDecoder(bytes.NewReader(encode(in)))
			decoder.UseNumber()
			if e := decoder.Decode(&fields); e != nil {
				t.Fatal(e)
			}
			fields["projectId"] = project.ID
			var session backendmodel.ImportSession
			call("begin_backend_import", fields, &session)
			template, e := os.ReadFile(filepath.Join("../backendmodel/testdata/relational/orders", dialect, "v1", "commands.json"))
			if e != nil {
				t.Fatal(e)
			}
			var commands []backendmodel.ImportCommand
			if e = json.Unmarshal([]byte(strings.NewReplacer("@repositoryId@", session.RepositoryID, "@snapshotId@", session.SnapshotID).Replace(string(template))), &commands); e != nil {
				t.Fatal(e)
			}
			attrs := func(v map[string]any) map[string]jsontext.Value {
				t.Helper()
				var out map[string]jsontext.Value
				if e := json.Unmarshal(encode(v), &out); e != nil {
					t.Fatal(e)
				}
				return out
			}
			proof := func(subject, kind string) {
				commands = append(commands, backendmodel.ImportCommand{Op: "upsert_evidence", Evidence: &backendmodel.ImportEvidence{ExternalKey: "runtime-proof:" + subject, SubjectType: kind, SubjectKey: subject, Method: "agent", Status: "explicit", Source: backendmodel.EvidenceSource{RepositoryID: session.RepositoryID, SnapshotID: session.SnapshotID, File: "cancel.go", ContentHash: sourceHash, StartLine: new(int64(1)), EndLine: new(sourceLines)}}})
			}
			node := func(key, kind, parent string, a map[string]any) {
				var p *string
				if parent != "" {
					p = new(parent)
				}
				commands = append(commands, backendmodel.ImportCommand{Op: "upsert_node", Node: &backendmodel.ImportNode{ExternalKey: key, Kind: kind, Name: key, ParentKey: p, Attributes: attrs(a), EvidenceKeys: []string{"runtime-proof:" + key}}})
				proof(key, "node")
			}
			edge := func(key, kind, from, to string, a map[string]any) {
				commands = append(commands, backendmodel.ImportCommand{Op: "upsert_edge", Edge: &backendmodel.ImportEdge{ExternalKey: key, Kind: kind, FromKey: from, ToKey: to, Attributes: attrs(a), EvidenceKeys: []string{"runtime-proof:" + key}}})
				proof(key, "edge")
			}
			complete := func() map[string]any { return map[string]any{"analysisStatus": "complete", "gaps": []string{}} }
			known := func(v string) map[string]any { return map[string]any{"status": "known", "value": v} }
			node("operation:cancel", "http_operation", "", map[string]any{"method": "POST", "path": "/orders/{id}/cancel"})
			node("handler:cancel", "handler", "", map[string]any{"language": "go"})
			a := complete()
			a["analysisStatus"] = "partial"
			a["gaps"] = []string{"HTTP decoding and BeginTx failure are outside the expanded flow"}
			a["entryStepKey"] = "step:begin"
			a["exitStepKeys"] = []string{"step:return-error", "step:return-success"}
			a["exitStatus"] = "complete"
			node("flow:cancel", "flow", "handler:cancel", a)
			edge("handles:cancel", "handles", "operation:cancel", "handler:cancel", map[string]any{})
			edge("contains:flow", "contains", "handler:cancel", "flow:cancel", map[string]any{})
			a = complete()
			a["datastoreKey"] = "database:orders"
			a["connectionScope"] = known("local tx")
			a["isolationLevel"] = map[string]any{"status": "unknown", "reason": "BeginTx nil options; deployment default unknown"}
			a["boundaryStatus"] = "complete"
			node("tx:cancel", "transaction", "flow:cancel", a)
			edge("contains:tx", "contains", "flow:cancel", "tx:cancel", map[string]any{})
			for _, step := range []struct{ key, kind string }{{"step:begin", "transaction_begin"}, {"step:query", "query"}, {"step:condition", "condition"}, {"step:commit", "transaction_commit"}, {"step:rollback", "transaction_rollback"}, {"step:return-error", "return"}, {"step:return-success", "return"}} {
				a = complete()
				a["stepKind"] = step.kind
				a["inputs"] = []any{}
				a["outputs"] = []any{}
				a["transactionContext"] = map[string]any{"status": "known", "transactionKey": "tx:cancel"}
				a["nativeText"] = map[string]string{"step:begin": "tx, err := db.BeginTx(ctx, nil)", "step:query": "err = tx.QueryRowContext(ctx, query, status, id).Scan(&status)", "step:condition": "err != nil", "step:commit": "return tx.Commit()", "step:rollback": "tx.Rollback()", "step:return-error": "return err", "step:return-success": "return tx.Commit()"}[step.key]
				if step.kind == "condition" {
					a["expression"] = known("err != nil")
				}
				node(step.key, "flow_step", "flow:cancel", a)
				edge("contains:"+step.key, "contains", "flow:cancel", step.key, map[string]any{})
			}
			native := "UPDATE orders SET status = $1 WHERE id = $2 RETURNING status"
			a = complete()
			a["dialect"] = dialect
			a["nativeDefinition"] = native
			a["parameters"] = []any{map[string]any{"key": "status", "name": "status", "nativeType": known("string")}, map[string]any{"key": "id", "name": "id", "nativeType": known("int64")}}
			a["results"] = []any{map[string]any{"key": "result-status", "name": "status", "nativeType": known("string")}}
			a["columnScope"] = "complete"
			node("query:cancel", "query", "handler:cancel", a)
			edge("contains:query", "contains", "handler:cancel", "query:cancel", map[string]any{})
			edge("next:begin-query", "next", "step:begin", "step:query", map[string]any{})
			edge("next:query-condition", "next", "step:query", "step:condition", map[string]any{})
			edge("call:query", "calls", "step:query", "query:cancel", map[string]any{})
			edge("branch:ok", "branch", "step:condition", "step:commit", map[string]any{"label": "success", "condition": known("err == nil")})
			edge("branch:error", "error", "step:condition", "step:rollback", map[string]any{"label": "query error", "outcome": "error"})
			edge("returns:commit", "returns", "step:commit", "step:return-success", map[string]any{"label": "commit result"})
			edge("returns:rollback", "returns", "step:rollback", "step:return-error", map[string]any{"label": "original error"})
			for _, boundary := range []struct{ kind, step string }{{"begins", "step:begin"}, {"commits", "step:commit"}, {"rolls_back", "step:rollback"}} {
				edge("boundary:"+boundary.kind, boundary.kind, boundary.step, "tx:cancel", map[string]any{})
			}
			for _, access := range []struct{ key, kind, mode, target string }{{"access:write-status", "writes", "update", "column:orders:status"}, {"access:read-id", "reads", "read", "column:orders:id"}, {"access:read-status", "reads", "read", "column:orders:status"}} {
				edge(access.key, access.kind, "query:cancel", access.target, map[string]any{"accessMode": access.mode, "datastoreKey": "database:orders", "facetKey": "sql", "columnScope": "listed"})
			}
			a = map[string]any{"analysisStatus": "partial", "gaps": []string{"Concrete result columns and HTTP caller not captured"}, "dialect": dialect, "nativeDefinition": "SELECT * FROM orders", "parameters": []any{}, "results": []any{}, "columnScope": "unknown"}
			node("query:all", "query", "handler:cancel", a)
			edge("contains:all", "contains", "handler:cancel", "query:all", map[string]any{})
			edge("access:possible", "reads", "query:all", "table:orders", map[string]any{"accessMode": "read", "datastoreKey": "database:orders", "facetKey": "sql", "columnScope": "unknown", "scopeReason": "Star projection columns not analyzed"})
			h, e := backendmodel.ImportBatchHash(commands)
			if e != nil {
				t.Fatal(e)
			}
			var batch backendmodel.BatchReceipt
			call("put_backend_import_batch", map[string]any{"projectId": project.ID, "importId": session.ID, "batchId": "runtime", "expectedImportVersion": session.Version, "payloadHash": h, "commands": commands}, &batch)
			ids := map[string]string{}
			for _, id := range batch.Identities {
				ids[id.ExternalKey] = id.ID
			}
			var preview backendmodel.ImportPreview
			call("preview_backend_import", map[string]any{"projectId": project.ID, "importId": session.ID, "expectedImportVersion": batch.AcceptedVersion, "baseRevisionId": project.CurrentRevisionID}, &preview)
			if preview.CandidateHash == nil {
				t.Fatalf("runtime preview: %+v", preview)
			}
			commit := map[string]any{"projectId": project.ID, "importId": session.ID, "expectedVersion": project.Version, "expectedImportVersion": preview.Version, "candidateHash": *preview.CandidateHash, "idempotencyKey": "flow-commit"}
			var out backendmodel.ImportCommitResult
			receipt := call("commit_backend_import", commit, &out)
			if !bytes.Equal(receipt, call("commit_backend_import", commit, nil)) {
				t.Fatal("runtime original receipt changed")
			}
			query := func(view string, selectors map[string]any) backendmodel.FlowPage {
				t.Helper()
				fields := map[string]any{"projectId": project.ID, "revisionId": out.Revision.ID, "view": view}
				for k, v := range selectors {
					fields[k] = v
				}
				var page backendmodel.FlowPage
				call("query_backend_flow", fields, &page)
				return page
			}
			entries := query("entrypoints", nil)
			if len(entries.EntryPointItems) != 1 || !slices.Equal(entries.EntryPointItems[0].FlowIDs, []string{ids["flow:cancel"]}) {
				t.Fatalf("entrypoints %+v", entries)
			}
			steps := query("steps", map[string]any{"flowId": ids["flow:cancel"]})
			if len(steps.StepItems) != 7 {
				t.Fatalf("steps %+v", steps)
			}
			transitions := query("transitions", map[string]any{"flowId": ids["flow:cancel"]})
			if len(transitions.TransitionItems) != 10 {
				t.Fatalf("transitions %+v", transitions)
			}
			for _, key := range []string{"branch:ok", "branch:error", "boundary:begins", "boundary:commits", "boundary:rolls_back", "returns:commit", "returns:rollback"} {
				if !slices.ContainsFunc(transitions.TransitionItems, func(edge backendmodel.Edge) bool { return edge.ID == ids[key] }) {
					t.Fatalf("source transaction/control edge %s absent", key)
				}
			}
			accesses := query("accesses", map[string]any{"entrypointId": ids["operation:cancel"]})
			if len(accesses.AccessItems) != 3 {
				t.Fatalf("endpoint accesses %+v", accesses)
			}
			want := []string{ids["handles:cancel"], ids["contains:flow"], ids["contains:step:begin"], ids["next:begin-query"], ids["call:query"], ids["access:write-status"]}
			found := false
			for _, item := range accesses.AccessItems {
				if item.AccessEdgeID == ids["access:write-status"] {
					found = true
					if !slices.Equal(item.PathEdgeIDs, want) || item.Relation != "direct" {
						t.Fatalf("source witness %+v want %v", item, want)
					}
					proofs := []string{ids["proof:v1:column:orders:status:sql"]}
					for _, key := range []string{"operation:cancel", "handler:cancel", "flow:cancel", "step:begin", "step:query", "query:cancel", "handles:cancel", "contains:flow", "contains:step:begin", "next:begin-query", "call:query", "access:write-status"} {
						proofs = append(proofs, ids["runtime-proof:"+key])
					}
					slices.Sort(proofs)
					if !slices.Equal(item.EvidenceIDs, proofs) {
						t.Fatalf("source witness proof set %+v want %v", item.EvidenceIDs, proofs)
					}
				}
			}
			if !found {
				t.Fatal("write absent")
			}
			reverse := query("accesses", map[string]any{"dataNodeId": ids["column:orders:status"]})
			if len(reverse.AccessItems) != 3 {
				t.Fatalf("direct/possible reverse %+v", reverse)
			}
			possible := false
			for _, item := range reverse.AccessItems {
				if item.AccessEdgeID == ids["access:possible"] {
					possible = true
					if item.Relation != "possible" || item.EntrypointID != nil || len(item.PathEdgeIDs) != 0 {
						t.Fatalf("unknown table access claimed confirmed/caller: %+v", item)
					}
				}
			}
			if !possible {
				t.Fatal("possible unattached access absent")
			}
			var nodeRead backendmodel.Node
			call("get_backend_node", map[string]any{"projectId": project.ID, "revisionId": out.Revision.ID, "nodeId": ids["query:cancel"]}, &nodeRead)
			var got string
			if e := json.Unmarshal(nodeRead.Attributes["nativeDefinition"], &got); e != nil || got != native {
				t.Fatalf("native truncated %q %v", got, e)
			}
			var evidence backendmodel.EvidencePage
			call("get_backend_evidence", map[string]any{"projectId": project.ID, "revisionId": out.Revision.ID, "subjectId": nodeRead.ID}, &evidence)
			if len(evidence.Items) != 1 || evidence.Items[0].Source.File != "cancel.go" || evidence.Items[0].Source.ContentHash != sourceHash || evidence.Items[0].Source.SnapshotID != session.SnapshotID || evidence.Items[0].Source.StartLine == nil || evidence.Items[0].Source.EndLine == nil || *evidence.Items[0].Source.StartLine != 1 || *evidence.Items[0].Source.EndLine != sourceLines {
				t.Fatalf("native query source locator changed: %+v", evidence)
			}
			var detail backendmodel.ProposalDetail
			call("create_backend_proposal", map[string]any{"projectId": project.ID, "name": "source3 DB intent", "baseRevisionId": out.Revision.ID, "repositoryId": session.RepositoryID, "datastoreId": ids["database:orders"], "facetKey": "sql", "idempotencyKey": "runtime-proposal"}, &detail)
			if detail.Proposal.Status != "draft" {
				t.Fatal("source3 proposal not draft")
			}
		})
	}
}

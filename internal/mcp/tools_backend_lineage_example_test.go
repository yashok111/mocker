package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/guide"
)

// Public SDK example: source is read as inert bytes, never executed. Expected
// witnesses and ordered inputs come from the independently authored fixture.
func TestBackendLineageRealSDKGuideFixtureExample(t *testing.T) {
	server, _ := newResourcesTestServer(t, resourcesTestConfig(t))
	fixture := newToolFixture(server)
	call := func(name string, input any, out any) []byte {
		t.Helper()
		encoded, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		raw, message := fixture.Call(t, name, string(encoded))
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
	for _, topic := range []string{"backend-import", "backend-examples", "backend-inspect", "backend-flow-reference"} {
		var out GetGuideOutput
		call("get_guide", map[string]any{"topic": topic, "guideSetId": guide.CurrentGuideSetID()}, &out)
		owner, _ := guide.WorkflowForTopic(topic)
		if out.WorkflowID != owner.WorkflowID || out.WorkflowVersion != owner.WorkflowVersion || out.ContentHash != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(out.Markdown))) {
			t.Fatalf("guide identity: %+v", out)
		}
	}
	var project backendmodel.Project
	call("create_backend_project", map[string]any{"name": "Lineage fixture", "idempotencyKey": "lineage-example-create"}, &project)
	source, err := os.ReadFile("../backendmodel/testdata/lineage/orders/source.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	sourceHash := fmt.Sprintf("%x", sha256.Sum256(source))
	inventory := []map[string]any{}
	for _, category := range strings.Fields("files endpoints datastores migrations producers consumers jobs contracts tests") {
		item := map[string]any{"category": category, "status": "unsupported", "knownCount": 0, "denominator": nil, "discoverySource": "source fixture", "gaps": []string{}, "reason": "Outside captured fixture"}
		if slices.Contains([]string{"files", "endpoints", "datastores"}, category) {
			item["status"] = "complete"
			item["knownCount"] = 1
			item["denominator"] = 1
			item["reason"] = ""
		}
		inventory = append(inventory, item)
	}
	begin := map[string]any{"projectId": project.ID, "expectedVersion": project.Version, "baseRevisionId": project.CurrentRevisionID, "idempotencyKey": "lineage-example-begin", "profile": "field-lineage-v1", "mode": "initial", "inventory": inventory, "manifest": map[string]any{"repositoryName": "orders", "provider": map[string]any{"name": "orders-fixture", "version": "1", "namespace": "lineage-fixture", "method": "agent", "profiles": []string{"foundation-graph-v1", "relational-graph-v1", "runtime-flow-v1", "field-lineage-v1"}, "limitations": []string{"Static bounded fixture"}}, "snapshot": map[string]any{"dirty": false, "consistency": "verified", "capturedAt": "2026-10-01T12:00:00Z", "files": []map[string]any{{"path": "source.go.txt", "contentHash": sourceHash, "fileType": "go", "analysisStatus": "analyzed"}}}}}
	var session backendmodel.ImportSession
	call("begin_backend_import", begin, &session)
	template, err := os.ReadFile("../backendmodel/testdata/lineage/orders/commands.json")
	if err != nil {
		t.Fatal(err)
	}
	var commands []backendmodel.ImportCommand
	if err := json.Unmarshal([]byte(strings.NewReplacer("@repositoryId@", session.RepositoryID, "@snapshotId@", session.SnapshotID).Replace(string(template))), &commands); err != nil {
		t.Fatal(err)
	}
	hash, err := backendmodel.ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	var batch backendmodel.BatchReceipt
	call("put_backend_import_batch", map[string]any{"projectId": project.ID, "importId": session.ID, "batchId": "lineage-example", "expectedImportVersion": session.Version, "payloadHash": hash, "commands": commands}, &batch)
	ids := map[string]string{}
	for _, identity := range batch.Identities {
		if identity.RecordType == "node" {
			ids[identity.ExternalKey] = identity.ID
		}
	}
	var preview backendmodel.ImportPreview
	call("preview_backend_import", map[string]any{"projectId": project.ID, "importId": session.ID, "expectedImportVersion": batch.AcceptedVersion, "baseRevisionId": project.CurrentRevisionID}, &preview)
	if preview.State != "ready" || preview.CandidateHash == nil {
		t.Fatalf("preview: %+v", preview)
	}
	commit := map[string]any{"projectId": project.ID, "importId": session.ID, "expectedVersion": project.Version, "expectedImportVersion": preview.Version, "candidateHash": *preview.CandidateHash, "idempotencyKey": "lineage-example-commit"}
	var result backendmodel.ImportCommitResult
	receipt := call("commit_backend_import", commit, &result)
	if !bytes.Equal(receipt, call("commit_backend_import", commit, nil)) {
		t.Fatal("commit replay changed")
	}
	expectedRaw, err := os.ReadFile("../backendmodel/testdata/lineage/orders/expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected struct {
		Request               backendmodel.ImportLineageValueRef   `json:"request"`
		AmountColumn          backendmodel.ImportLineageValueRef   `json:"amountColumn"`
		Response              backendmodel.ImportLineageValueRef   `json:"response"`
		AggregateSources      []backendmodel.ImportLineageValueRef `json:"aggregateSources"`
		RequestColumnWitness  []string                             `json:"requestColumnWitness"`
		AmountResponseWitness []string                             `json:"amountResponseWitness"`
		ReverseTaxWitness     []string                             `json:"reverseTaxWitness"`
	}
	if err := json.Unmarshal(expectedRaw, &expected); err != nil {
		t.Fatal(err)
	}
	ref := func(r backendmodel.ImportLineageValueRef) backendmodel.LineageValueRef {
		return backendmodel.LineageValueRef{Kind: r.Kind, NodeID: ids[r.NodeKey], FacetKey: r.FacetKey, Collection: r.Collection, PortKey: r.PortKey}
	}
	query := func(seed backendmodel.LineageValueRef, direction string) []backendmodel.LineageItem {
		t.Helper()
		items := []backendmodel.LineageItem{}
		cursor := ""
		for {
			input := map[string]any{"projectId": project.ID, "revisionId": result.Revision.ID, "seed": seed, "direction": direction, "limit": 1, "maxDepth": 8}
			if cursor != "" {
				input["cursor"] = cursor
			}
			var page backendmodel.LineagePage
			call("query_backend_lineage", input, &page)
			if page.ProjectID != project.ID || page.RevisionID != result.Revision.ID || page.SemanticHash != result.Revision.SemanticHash || page.Seed != seed || page.Direction != direction {
				t.Fatalf("scope lost: %+v", page)
			}
			items = append(items, page.Items...)
			if page.NextCursor == "" {
				break
			}
			if page.NextCursor == cursor {
				t.Fatal("cursor repeated")
			}
			cursor = page.NextCursor
		}
		return items
	}
	witness := func(items []backendmodel.LineageItem, keys []string) {
		t.Helper()
		want := make([]string, len(keys))
		for i, key := range keys {
			want[i] = ids[key]
		}
		for _, item := range items {
			if item.Mapping.ID == want[len(want)-1] {
				if !slices.Equal(item.WitnessMappingIDs, want) {
					t.Fatalf("witness: %+v want %v", item, want)
				}
				return
			}
		}
		t.Fatalf("missing witness %v", keys)
	}
	witness(query(ref(expected.Request), "forward"), expected.RequestColumnWitness)
	witness(query(ref(expected.AmountColumn), "forward"), expected.AmountResponseWitness)
	reverse := query(ref(expected.Response), "reverse")
	witness(reverse, expected.ReverseTaxWitness)
	for _, item := range reverse {
		if item.Mapping.ID == ids["m06-total"] {
			encoded, _ := json.Marshal(item.Mapping.Attributes)
			var attrs backendmodel.LineageMappingAttributes
			if err := json.Unmarshal(encoded, &attrs); err != nil {
				t.Fatal(err)
			}
			want := []backendmodel.LineageValueRef{ref(expected.AggregateSources[0]), ref(expected.AggregateSources[1])}
			if !slices.Equal(attrs.Sources, want) || attrs.Transform.Kind != "aggregate" {
				t.Fatalf("whole aggregate lost: %+v", attrs)
			}
		}
	}
	unknown := query(backendmodel.LineageValueRef{Kind: "api_field", NodeID: ids["token"]}, "reverse")
	if len(unknown) != 1 || unknown[0].Mapping.ID != ids["m10-unknown"] || unknown[0].Expansion != "boundary" || len(unknown[0].ExpandedValues) != 0 || !unknown[0].RequiresReview {
		t.Fatalf("unknown boundary: %+v", unknown)
	}
	raw, err := json.Marshal(unknown[0].Mapping.Attributes)
	if err != nil {
		t.Fatal(err)
	}
	var boundary backendmodel.LineageMappingAttributes
	if err := json.Unmarshal(raw, &boundary); err != nil {
		t.Fatal(err)
	}
	if len(boundary.Sources) != 2 || boundary.Destination.NodeID != ids["token"] || !boundary.Transform.Redacted || boundary.Transform.Kind != "unknown_transform" {
		t.Fatalf("unknown full shape lost: %+v", boundary)
	}

}

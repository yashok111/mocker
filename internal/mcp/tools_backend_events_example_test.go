package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/guide"
)

type eventsExamplePhase struct {
	Input    backendmodel.BeginImportInput `json:"input"`
	Commands []backendmodel.ImportCommand  `json:"commands"`
}

type eventsExampleTemplate struct {
	Placeholders    map[string]string             `json:"placeholders"`
	Baseline        eventsExamplePhase            `json:"baseline"`
	ResolvedInput   backendmodel.BeginImportInput `json:"resolvedInput"`
	ResolvedUpdates []backendmodel.ImportCommand  `json:"resolvedUpdates"`
	ResolvedOmitted [][]string                    `json:"resolvedOmitted"`
}

type eventsExampleOracle struct {
	SourceManifest []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"sourceManifest"`
	ExpectedViews struct {
		Routes []struct {
			EmitsEdgeKey        string   `json:"emitsEdgeKey"`
			DeliveryEdgeKey     string   `json:"deliveryEdgeKey"`
			ExpectedHandlerKeys []string `json:"expectedHandlerKeys"`
		} `json:"routes"`
		Jobs []string `json:"jobs"`
	} `json:"expectedViews"`
}

// One real SDK server imports inert source, reads exact contextual routes and
// lineage, then reconciles the same project. Expected witnesses come from the
// independent source oracle; docs and temporary QA artifacts are never inputs.
func TestBackendEventsRealSDKGuideFixtureExample(t *testing.T) {
	server, _ := newResourcesTestServer(t, resourcesTestConfig(t))
	fixture := newToolFixture(server)
	call := func(name string, input, out any) []byte {
		t.Helper()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		result, message := fixture.Call(t, name, string(raw))
		if message != "" {
			t.Fatalf("%s: %s", name, message)
		}
		if out != nil {
			if err := json.Unmarshal(result, out); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	for _, topic := range []string{"backend-import", "backend-inspect", "backend-events"} {
		var out GetGuideOutput
		call("get_guide", map[string]any{"topic": topic, "guideSetId": guide.CurrentGuideSetID()}, &out)
		owner, ok := guide.WorkflowForTopic(topic)
		if !ok || out.WorkflowID != owner.WorkflowID || out.WorkflowVersion != owner.WorkflowVersion || out.GuideSetID != guide.CurrentGuideSetID() || out.ManifestHash != guide.CurrentGuideSetID() || out.ContentHash != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(out.Markdown))) {
			t.Fatalf("unqualified guide: %+v", out)
		}
	}
	read := func(path string, out any) []byte {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if out != nil {
			if err := json.Unmarshal(raw, out); err != nil {
				t.Fatal(err)
			}
		}
		return raw
	}
	var template eventsExampleTemplate
	read("testdata/events/orders-import.json", &template)
	var oracle eventsExampleOracle
	read("../backendmodel/testdata/events/orders/expected.json", &oracle)
	if len(oracle.ExpectedViews.Routes) != 6 || len(oracle.SourceManifest) != 5 {
		t.Fatal("independent source expectations are empty or incomplete")
	}
	sourceBytes := map[string][]byte{}
	for _, member := range oracle.SourceManifest {
		raw := read(filepath.Join("../backendmodel/testdata/events/orders", member.Path), nil)
		if fmt.Sprintf("%x", sha256.Sum256(raw)) != member.SHA256 {
			t.Fatalf("independent source hash drift: %s", member.Path)
		}
		sourceBytes[member.Path] = raw
	}
	address := func(c backendmodel.ImportCommand) [2]string {
		switch {
		case c.Node != nil:
			return [2]string{"node", c.Node.ExternalKey}
		case c.Edge != nil:
			return [2]string{"edge", c.Edge.ExternalKey}
		case c.Evidence != nil:
			return [2]string{"evidence", c.Evidence.ExternalKey}
		default:
			t.Fatal("unexpected fixture command")
			return [2]string{}
		}
	}
	resolvedCommands := slices.Clone(template.Baseline.Commands)
	omitted := map[[2]string]bool{}
	for _, value := range template.ResolvedOmitted {
		omitted[[2]string{strings.TrimPrefix(value[0], "upsert_"), value[1]}] = true
	}
	resolvedCommands = slices.DeleteFunc(resolvedCommands, func(c backendmodel.ImportCommand) bool { return omitted[address(c)] })
	for _, replacement := range template.ResolvedUpdates {
		i := slices.IndexFunc(resolvedCommands, func(c backendmodel.ImportCommand) bool { return address(c) == address(replacement) })
		if i < 0 {
			resolvedCommands = append(resolvedCommands, replacement)
		} else {
			resolvedCommands[i] = replacement
		}
	}
	if len(template.Baseline.Commands) != 433 || len(resolvedCommands) != 442 {
		t.Fatal("whole-scope source template lost commands")
	}
	verifyProof := func(input backendmodel.BeginImportInput, commands []backendmodel.ImportCommand) {
		t.Helper()
		members := map[string]backendmodel.ManifestFile{}
		for _, file := range input.Manifest.Snapshot.Files {
			if file.AnalysisStatus != "analyzed" || fmt.Sprintf("%x", sha256.Sum256(sourceBytes[file.Path])) != file.ContentHash {
				t.Fatalf("manifest source drift: %s", file.Path)
			}
			members[file.Path] = file
		}
		for _, c := range commands {
			if c.Evidence == nil {
				continue
			}
			e := c.Evidence
			member, exists := members[e.Source.File]
			lines := strings.Split(strings.TrimSuffix(string(sourceBytes[e.Source.File]), "\n"), "\n")
			if !exists || member.ContentHash != e.Source.ContentHash || e.Source.StartLine == nil || e.Source.EndLine == nil || *e.Source.StartLine < 1 || *e.Source.EndLine < *e.Source.StartLine || *e.Source.EndLine > int64(len(lines)) {
				t.Fatalf("unbacked physical evidence: %s", e.ExternalKey)
			}
			if e.Snippet == nil || *e.Snippet != strings.Join(lines[*e.Source.StartLine-1:*e.Source.EndLine], "\n") {
				t.Fatalf("physical snippet drift: %s", e.ExternalKey)
			}
		}
	}
	verifyProof(template.Baseline.Input, template.Baseline.Commands)
	verifyProof(template.ResolvedInput, resolvedCommands)

	var project backendmodel.Project
	call("create_backend_project", map[string]any{"name": "Events public source fixture", "idempotencyKey": "events-example-create"}, &project)
	ids := map[[2]string]string{}
	importPhase := func(input backendmodel.BeginImportInput, commands []backendmodel.ImportCommand, previous *backendmodel.ImportSession, phase string) (backendmodel.ImportCommitResult, backendmodel.ImportSession) {
		t.Helper()
		input.ExpectedVersion, input.BaseRevisionID, input.IdempotencyKey = project.Version, project.CurrentRevisionID, "events-example-begin-"+phase
		input.Mode = "initial"
		if previous != nil {
			input.Mode = "reconcile"
			input.RepositoryID = new(previous.RepositoryID)
			input.GraphScope = &backendmodel.GraphScope{Profile: "events-service-v1", Status: "complete", Gaps: []string{}}
			for i := range input.Inventory {
				entry := &input.Inventory[i]
				if slices.Contains([]string{"files", "endpoints", "datastores"}, entry.Category) {
					entry.Status, entry.Denominator, entry.Gaps, entry.Reason = "complete", new(entry.KnownCount), []string{}, ""
					entry.DiscoverySource = "Exact static enumeration of the five supplied members; schema details and external private-ledger remain unknown"
				}
			}
		}
		var session backendmodel.ImportSession
		beginRaw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		var begin map[string]any
		if err := json.Unmarshal(beginRaw, &begin); err != nil {
			t.Fatal(err)
		}
		begin["projectId"] = project.ID
		call("begin_backend_import", begin, &session)
		raw, err := json.Marshal(commands)
		if err != nil {
			t.Fatal(err)
		}
		replaced := strings.NewReplacer(template.Placeholders["repositoryId"], session.RepositoryID, template.Placeholders["snapshotId"], session.SnapshotID).Replace(string(raw))
		var staged []backendmodel.ImportCommand
		if err := json.Unmarshal([]byte(replaced), &staged); err != nil {
			t.Fatal(err)
		}
		commands = staged
		if previous != nil {
			for _, key := range [][2]string{{"node", "unknown.fraud"}, {"edge", "handles.unknown.fraud"}, {"edge", "contains:unknown.fraud"}} {
				commands = append(commands, backendmodel.ImportCommand{Op: "delete_assertion", Deletion: &backendmodel.ImportDeletion{RecordType: key[0], ExternalKey: key[1], ExpectedID: ids[key], Reason: "Analyzed fraud-v1 registration and handler replace the old unknown assertion"}})
			}
		}
		version := session.Version
		for start := 0; start < len(commands); start += 100 {
			batchCommands := commands[start:min(start+100, len(commands))]
			hash, err := backendmodel.ImportBatchHash(batchCommands)
			if err != nil {
				t.Fatal(err)
			}
			var batch backendmodel.BatchReceipt
			call("put_backend_import_batch", map[string]any{"projectId": project.ID, "importId": session.ID, "batchId": fmt.Sprintf("events-example-%s-%d", phase, start), "expectedImportVersion": version, "payloadHash": hash, "commands": batchCommands}, &batch)
			version = batch.AcceptedVersion
			for _, identity := range batch.Identities {
				key := [2]string{identity.RecordType, identity.ExternalKey}
				if old := ids[key]; old != "" && old != identity.ID {
					t.Fatalf("same-provider identity drift: %v", key)
				}
				ids[key] = identity.ID
			}
		}
		var preview backendmodel.ImportPreview
		call("preview_backend_import", map[string]any{"projectId": project.ID, "importId": session.ID, "expectedImportVersion": version, "baseRevisionId": project.CurrentRevisionID}, &preview)
		if preview.State != "ready" || preview.CandidateHash == nil || preview.ModelSchemaVersion != "5" {
			t.Fatalf("preview: %+v", preview)
		}
		var result backendmodel.ImportCommitResult
		commit := map[string]any{"projectId": project.ID, "importId": session.ID, "expectedVersion": project.Version, "expectedImportVersion": preview.Version, "candidateHash": *preview.CandidateHash, "idempotencyKey": "events-example-commit-" + phase}
		receipt := call("commit_backend_import", commit, &result)
		if !bytes.Equal(receipt, call("commit_backend_import", commit, nil)) {
			t.Fatal("exact receipt replay changed")
		}
		project = result.Project
		return result, session
	}
	baseline, session := importPhase(template.Baseline.Input, template.Baseline.Commands, nil, "baseline")
	queryEvents := func(revision backendmodel.Revision, view, seed, service string, limit int) backendmodel.EventsPage {
		t.Helper()
		var page backendmodel.EventsPage
		input := map[string]any{"projectId": project.ID, "revisionId": revision.ID, "view": view, "limit": limit}
		if seed != "" {
			input["seedNodeId"] = seed
		}
		if service != "" {
			input["serviceId"] = service
		}
		call("query_backend_events", input, &page)
		if page.ProjectID != project.ID || page.RevisionID != revision.ID || page.SemanticHash != revision.SemanticHash || page.Policy != "source-events-projection-v1" || page.View != view || page.SeedNodeID != seed || page.ServiceID != service || page.Truncated || !page.Complete {
			t.Fatalf("event response lost exact scope: %+v", page)
		}
		return page
	}
	all := queryEvents(baseline.Revision, "routes", "", "", 100)
	original, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	refs := func(item backendmodel.EventsItem) backendmodel.EventsReferences {
		if item.Route != nil {
			return item.Route.References
		}
		if item.Boundary != nil {
			return item.Boundary.References
		}
		t.Fatal("unexpected route variant")
		return backendmodel.EventsReferences{}
	}
	for _, want := range oracle.ExpectedViews.Routes {
		i := slices.IndexFunc(all.Items, func(item backendmodel.EventsItem) bool {
			r := refs(item)
			return r.EmitsEdgeID == ids[[2]string{"edge", want.EmitsEdgeKey}] && r.DeliveryEdgeID == ids[[2]string{"edge", want.DeliveryEdgeKey}]
		})
		if i < 0 {
			t.Fatalf("missing independent route %s/%s", want.EmitsEdgeKey, want.DeliveryEdgeKey)
		}
		item := all.Items[i]
		var dispatch []backendmodel.EventsDispatch
		if item.Route != nil {
			dispatch = item.Route.Dispatch
		} else {
			dispatch = item.Boundary.Dispatch
		}
		for _, key := range want.ExpectedHandlerKeys {
			if !slices.ContainsFunc(dispatch, func(d backendmodel.EventsDispatch) bool { return d.HandlerID == ids[[2]string{"node", key}] }) {
				t.Fatalf("missing oracle handler %s", key)
			}
		}
	}
	paged := []backendmodel.EventsItem{}
	cursor := ""
	for {
		var page backendmodel.EventsPage
		input := map[string]any{"projectId": project.ID, "revisionId": baseline.Revision.ID, "view": "routes", "limit": 1}
		if cursor != "" {
			input["cursor"] = cursor
		}
		call("query_backend_events", input, &page)
		if page.ProjectID != project.ID || page.RevisionID != baseline.Revision.ID || page.SemanticHash != baseline.Revision.SemanticHash || page.View != "routes" || page.SeedNodeID != "" || page.ServiceID != "" || page.Policy != "source-events-projection-v1" || page.Truncated || !page.Complete {
			t.Fatal("pagination lost full pinned scope")
		}
		paged = append(paged, page.Items...)
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor || len(paged) >= 7 {
			t.Fatal("cursor did not advance within independent fixture bound")
		}
		cursor = page.NextCursor
	}
	pagedRaw, err := json.Marshal(paged)
	if err != nil {
		t.Fatal(err)
	}
	allRaw, err := json.Marshal(all.Items)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pagedRaw, allRaw) {
		t.Fatal("bounded route pagination lost or duplicated source tuples")
	}
	orphan := queryEvents(baseline.Revision, "routes", ids[[2]string{"node", "consumer.orphan"}], "", 100)
	if len(orphan.Items) != 1 || orphan.Items[0].Boundary == nil || orphan.Items[0].Boundary.References.ProducerID != "" || orphan.Items[0].Boundary.References.DeliveryEdgeID != ids[[2]string{"edge", "delivery.orphan"}] {
		t.Fatal("reverse orphan subscription lost its exact boundary")
	}
	if len(all.Items) != 7 {
		t.Fatalf("routes/orphan count = %d", len(all.Items))
	}
	for _, key := range []string{"consumer.fraud", "consumer.legacy"} {
		page := queryEvents(baseline.Revision, "routes", ids[[2]string{"node", key}], "", 100)
		if len(page.Items) != 1 || page.Items[0].Boundary == nil || len(page.Items[0].Boundary.Dispatch) != 1 || page.Items[0].Boundary.Dispatch[0].UnresolvedTargetID == "" {
			t.Fatalf("unknown handler vanished: %s %+v", key, page)
		}
	}
	jobs := queryEvents(baseline.Revision, "jobs", "", ids[[2]string{"node", "svc.orders"}], 100)
	if len(jobs.Items) != len(oracle.ExpectedViews.Jobs) {
		t.Fatal("static jobs lost")
	}
	calls := queryEvents(baseline.Revision, "service_calls", "", ids[[2]string{"node", "svc.orders"}], 100)
	if len(calls.Items) != 1 || calls.Items[0].ServiceCall == nil || calls.Items[0].ServiceCall.References.CallsEdgeID != ids[[2]string{"edge", "call.billing"}] {
		t.Fatal("explicit service-call identity lost")
	}

	for _, route := range []string{"primary", "secondary"} {
		seed := backendmodel.LineageValueRef{Kind: "event_field", NodeID: ids[[2]string{"node", "field.orders.status"}], EndpointID: ids[[2]string{"node", "consumer.cancel"}], RouteID: ids[[2]string{"edge", "delivery." + route}]}
		var page backendmodel.LineagePage
		call("query_backend_lineage", map[string]any{"projectId": project.ID, "revisionId": baseline.Revision.ID, "seed": seed, "direction": "reverse", "maxDepth": 8, "limit": 100}, &page)
		if page.ProjectID != project.ID || page.Seed != seed || page.RevisionID != baseline.Revision.ID || page.SemanticHash != baseline.Revision.SemanticHash || page.Policy != "field-lineage-traversal-v2" || page.Truncated {
			t.Fatalf("context collapsed: %+v", page)
		}
		mappingID := ids[[2]string{"node", "map.transport." + route + ".status"}]
		i := slices.IndexFunc(page.Items, func(item backendmodel.LineageItem) bool { return item.Mapping.ID == mappingID })
		if i < 0 || !slices.Equal(page.Items[i].WitnessMappingIDs, []string{mappingID}) || page.Items[i].Expansion != "expanded" {
			t.Fatalf("explicit transport witness lost: %s", route)
		}
		var attrs backendmodel.LineageMappingAttributes
		raw, err := json.Marshal(page.Items[i].Mapping.Attributes)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &attrs); err != nil {
			t.Fatal(err)
		}
		if attrs.Transport == nil || attrs.Transport.EmitsEdgeID != ids[[2]string{"edge", "emits." + route}] || attrs.Transport.DeliveryEdgeID != seed.RouteID || attrs.Destination != seed || len(attrs.Sources) != 1 || attrs.Sources[0].EndpointID != ids[[2]string{"node", "step.emit." + route}] {
			t.Fatalf("transport manufactured/collapsed: %+v", attrs)
		}
	}

	var column backendmodel.LineagePage
	columnSeed := backendmodel.LineageValueRef{Kind: "column", NodeID: ids[[2]string{"node", "column.status"}], FacetKey: "sql"}
	call("query_backend_lineage", map[string]any{"projectId": project.ID, "revisionId": baseline.Revision.ID, "seed": columnSeed, "direction": "forward", "maxDepth": 8, "limit": 100}, &column)
	if column.ProjectID != project.ID || column.RevisionID != baseline.Revision.ID || column.SemanticHash != baseline.Revision.SemanticHash || column.Seed != columnSeed || column.Policy != "field-lineage-traversal-v2" || column.Truncated {
		t.Fatal("column lineage lost its source pin")
	}
	for _, route := range []string{"primary", "secondary"} {
		keys := []string{"map.db.status", "map.emit_input." + route + ".status", "map.serialize." + route + ".status", "map.transport." + route + ".status", "map.deserialize." + route + ".status"}
		want := make([]string, len(keys))
		for i, key := range keys {
			want[i] = ids[[2]string{"node", key}]
		}
		i := slices.IndexFunc(column.Items, func(item backendmodel.LineageItem) bool { return item.Mapping.ID == want[len(want)-1] })
		if i < 0 || !slices.Equal(column.Items[i].WitnessMappingIDs, want) || column.Items[i].Expansion != "expanded" {
			t.Fatalf("column→explicit event→consumer path lost: %s", route)
		}
	}

	resolved, nextSession := importPhase(template.ResolvedInput, resolvedCommands, &session, "resolved")
	if nextSession.RepositoryID != session.RepositoryID || resolved.Revision.ID == baseline.Revision.ID || resolved.Revision.ParentRevisionID == nil || *resolved.Revision.ParentRevisionID != baseline.Revision.ID {
		t.Fatal("resolution did not reconcile the original project/repository")
	}
	fraud := queryEvents(resolved.Revision, "routes", ids[[2]string{"node", "consumer.fraud"}], "", 100)
	if len(fraud.Items) != 1 || fraud.Items[0].Route == nil || len(fraud.Items[0].Route.Dispatch) != 1 || fraud.Items[0].Route.Dispatch[0].HandlerID != ids[[2]string{"node", "handler.fraud"}] || !slices.Equal(fraud.Items[0].Route.Dispatch[0].FlowIDs, []string{ids[[2]string{"node", "flow.fraud"}]}) {
		t.Fatalf("gap criterion remains unmet on successor: %+v", fraud)
	}
	var handler backendmodel.Node
	call("get_backend_node", map[string]any{"projectId": project.ID, "revisionId": resolved.Revision.ID, "nodeId": ids[[2]string{"node", "handler.fraud"}]}, &handler)
	var handlerProof backendmodel.EvidencePage
	call("get_backend_evidence", map[string]any{"projectId": project.ID, "revisionId": resolved.Revision.ID, "subjectId": handler.ID, "limit": 100}, &handlerProof)
	if len(handlerProof.Items) != 1 {
		t.Fatal("resolved handler lacks exact member evidence")
	}
	proof := handlerProof.Items[0]
	if proof.Source.RepositoryID != session.RepositoryID || proof.Source.SnapshotID != nextSession.SnapshotID || proof.Source.File != "fraud-plugin.ts.txt" || proof.Source.StartLine == nil || *proof.Source.StartLine != 3 || proof.Source.EndLine == nil || *proof.Source.EndLine != 6 || proof.Source.ContentHash != fmt.Sprintf("%x", sha256.Sum256(sourceBytes["fraud-plugin.ts.txt"])) {
		t.Fatalf("new handler criterion has no physical successor proof: %+v", proof)
	}
	legacy := queryEvents(resolved.Revision, "routes", ids[[2]string{"node", "consumer.legacy"}], "", 100)
	if len(legacy.Items) != 1 || legacy.Items[0].Boundary == nil || len(legacy.Items[0].Boundary.Dispatch) != 1 || legacy.Items[0].Boundary.Dispatch[0].UnresolvedTargetID != ids[[2]string{"node", "unknown.legacy"}] {
		t.Fatal("missing external source was presented as resolved/absent")
	}
	var missing backendmodel.Node
	call("get_backend_node", map[string]any{"projectId": project.ID, "revisionId": resolved.Revision.ID, "nodeId": ids[[2]string{"node", "unknown.legacy"}]}, &missing)
	var reason, scope string
	if json.Unmarshal(missing.Attributes["reason"], &reason) != nil || json.Unmarshal(missing.Attributes["searchScope"], &scope) != nil || !strings.Contains(reason, "private-ledger") || scope != "Supplied fixture manifest members only" {
		t.Fatalf("unresolved inspected scope/reason lost: %+v", missing)
	}
	old := queryEvents(baseline.Revision, "routes", "", "", 100)
	oldRaw, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, oldRaw) {
		t.Fatal("evidence reimport changed the original immutable pinned page")
	}
}

package admin

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendEventsRouteStrictPins(t *testing.T) {
	s := loopbackTestServer(t, nil)
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Events", IdempotencyKey: "events"})
	if err != nil {
		t.Fatal(err)
	}
	route := "/api/backend-projects/" + p.ID + "/events/query"
	base := `{"revisionId":"` + p.CurrentRevisionID + `","view":"routes"`
	for _, tc := range []struct {
		suffix string
		status int
	}{{`}`, 422}, {`,"proposal":{}}`, 400}, {`,"other":true}`, 400}, {`,"limit":0}`, 400}, {`,"seedNodeId":null}`, 400}, {`,"serviceId":"` + p.ID + `"}`, 400}} {
		status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", route, []byte(base+tc.suffix))
		if err != nil || status != tc.status || !strings.Contains(string(raw), "backend_") {
			t.Errorf("%s: %d %s %v", tc.suffix, status, raw, err)
		}
	}
}
func TestBackendEventsNeverCreatesCheckpoint(t *testing.T) {
	for _, r := range (&Server{}).routes() {
		if r.pattern == "POST /api/backend-projects/{id}/events/query" {
			if r.checkpoint != cpNeverTouchesLayer {
				t.Fatal(r.checkpoint)
			}
			return
		}
	}
	t.Fatal("events route missing")
}

// A small independently authored inert declaration graph crosses the public
// import, query and response-schema boundaries. No fixture source is executed.
func eventsTransportFixture(t *testing.T, s *Server) (*backendmodel.ImportCommitResult, map[string]string) {
	t.Helper()
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Events fixture", IdempotencyKey: "events-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	in := transportImportFixture(*p)
	in.Profile = backendmodel.EventsProfile
	in.Manifest.Provider.Profiles = []string{backendmodel.GraphProfile, backendmodel.RelationalProfile, backendmodel.RuntimeProfile, backendmodel.LineageProfile, backendmodel.EventsProfile}
	call := func(method, path string, input, output any) []byte {
		t.Helper()
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), method, path, body)
		if err != nil || status != 200 {
			t.Fatalf("%s %s: %d %s %v", method, path, status, raw, err)
		}
		validateBackendImportResponse(t, method, path, raw)
		if output != nil {
			if err := json.Unmarshal(raw, output); err != nil {
				t.Fatal(err)
			}
		}
		return raw
	}
	base := "/api/backend-projects/" + p.ID
	var session backendmodel.ImportSession
	call("POST", base+"/imports", in, &session)
	commands := []backendmodel.ImportCommand{}
	proof := func(typ, key string) string {
		t.Helper()
		external := "proof:" + typ + ":" + key
		commands = append(commands, backendmodel.ImportCommand{Op: "upsert_evidence", Evidence: &backendmodel.ImportEvidence{ExternalKey: external, SubjectType: typ, SubjectKey: key, Method: "ast", Status: "explicit", Source: backendmodel.EvidenceSource{RepositoryID: session.RepositoryID, SnapshotID: session.SnapshotID, File: "main.go", ContentHash: strings.Repeat("a", 64), StartLine: new(int64(1)), EndLine: new(int64(1))}}})
		return external
	}
	edge := func(key, kind, from, to string, attrs map[string]any) {
		t.Helper()
		raw, err := json.Marshal(attrs)
		if err != nil {
			t.Fatal(err)
		}
		var a map[string]jsontext.Value
		if err := json.Unmarshal(raw, &a); err != nil {
			t.Fatal(err)
		}
		evidence := proof("edge", key)
		commands = append(commands, backendmodel.ImportCommand{Op: "upsert_edge", Edge: &backendmodel.ImportEdge{ExternalKey: key, Kind: kind, FromKey: from, ToKey: to, Attributes: a, EvidenceKeys: []string{evidence}}})
	}
	node := func(key, kind, parent string, attrs map[string]any) {
		t.Helper()
		raw, err := json.Marshal(attrs)
		if err != nil {
			t.Fatal(err)
		}
		var a map[string]jsontext.Value
		if err := json.Unmarshal(raw, &a); err != nil {
			t.Fatal(err)
		}
		n := &backendmodel.ImportNode{ExternalKey: key, Kind: kind, Name: key, Attributes: a, EvidenceKeys: []string{proof("node", key)}}
		if parent != "" {
			n.ParentKey = new(parent)
		}
		commands = append(commands, backendmodel.ImportCommand{Op: "upsert_node", Node: n})
		if parent != "" {
			edge("contains:"+key, "contains", parent, key, map[string]any{})
		}
	}
	known := func(v string) map[string]any { return map[string]any{"status": "known", "value": v} }
	complete := func(extra map[string]any) map[string]any {
		extra["analysisStatus"] = "complete"
		extra["gaps"] = []string{}
		return extra
	}
	node("service", "service", "", map[string]any{})
	node("channel", "channel", "service", complete(map[string]any{"protocol": known("kafka"), "address": known("orders"), "scope": known("local")}))
	node("message", "message", "service", complete(map[string]any{"fieldInventory": "complete"}))
	node("field", "event_field", "message", complete(map[string]any{"section": "payload", "path": []any{map[string]any{"property": "status"}}, "nativeType": known("string")}))
	node("consumer", "consumer", "service", complete(map[string]any{"dispatchStatus": "complete"}))
	node("job", "job", "service", complete(map[string]any{"dispatchStatus": "complete", "trigger": map[string]any{"kind": "cron", "expression": known("0 3 * * *"), "timezone": known("UTC")}}))
	node("handler", "handler", "service", map[string]any{})
	node("flow", "flow", "handler", complete(map[string]any{"entryStepKey": "emit", "exitStepKeys": []string{"emit"}, "exitStatus": "complete"}))
	node("emit", "flow_step", "flow", complete(map[string]any{"stepKind": "emit", "inputs": []any{}, "outputs": []any{}, "nativeText": "publish orders", "transactionContext": map[string]any{"status": "none", "reason": "outside local transaction"}}))
	edge("handles:consumer", "handles", "consumer", "handler", map[string]any{})
	edge("handles:job", "handles", "job", "handler", map[string]any{})
	edge("emits", "emits", "emit", "message", map[string]any{"channelKey": "channel", "deliveryStatus": "declared"})
	edge("delivery", "delivered_to", "channel", "consumer", map[string]any{"messageKey": "message", "deliveryStatus": "declared", "condition": known("always"), "group": known("billing")})
	edge("delivery-second", "delivered_to", "channel", "consumer", map[string]any{"messageKey": "message", "deliveryStatus": "declared", "condition": known("second route"), "group": known("billing")})
	edge("retry", "retries", "consumer", "channel", map[string]any{"messageKey": "message", "reason": "configured retry", "delay": known("5s"), "maxAttempts": known("3")})
	edge("dead-letter", "dead_letters", "consumer", "channel", map[string]any{"messageKey": "message", "reason": "configured DLQ"})
	version := session.Version
	importPath := base + "/imports/" + session.ID
	for i := 0; i < len(commands); i += backendmodel.MaxImportCommands {
		batch := commands[i:min(i+backendmodel.MaxImportCommands, len(commands))]
		hash, err := backendmodel.ImportBatchHash(batch)
		if err != nil {
			t.Fatal(err)
		}
		var receipt backendmodel.BatchReceipt
		call("PUT", importPath+fmt.Sprintf("/batches/b%d", i), backendmodel.ImportBatchInput{ExpectedImportVersion: version, PayloadHash: hash, Commands: batch}, &receipt)
		version = receipt.AcceptedVersion
	}
	var preview backendmodel.ImportPreview
	call("POST", importPath+"/preview", backendmodel.PreviewImportInput{ExpectedImportVersion: version, BaseRevisionID: p.CurrentRevisionID}, &preview)
	if preview.State != "ready" {
		t.Fatal(preview.Diagnostics)
	}
	out := new(backendmodel.ImportCommitResult)
	call("POST", importPath+"/commit", backendmodel.CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "commit-events"}, out)
	var graph backendmodel.GraphPage
	call("POST", base+"/graph/query", backendmodel.GraphQueryInput{RevisionID: out.Revision.ID, RecordType: "nodes", Limit: 500}, &graph)
	ids := map[string]string{}
	for _, n := range graph.Nodes {
		ids[n.ExternalKey] = n.ID
	}
	return out, ids
}

func TestBackendEventsPersistedPublicResponseAndPureReads(t *testing.T) {
	s := loopbackTestServer(t, nil)
	out, ids := eventsTransportFixture(t, s)
	route := "/api/backend-projects/" + out.Project.ID + "/events/query"
	for _, view := range []string{"routes", "jobs", "service_calls"} {
		in := backendmodel.EventsQueryInput{RevisionID: out.Revision.ID, View: view, Limit: 1}
		raw, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		status, wire, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", route, raw)
		if err != nil || status != 200 {
			t.Fatalf("%s: %d %s %v", view, status, wire, err)
		}
		validateBackendImportResponse(t, "POST", route, wire)
		var page backendmodel.EventsPage
		if err := json.Unmarshal(wire, &page); err != nil {
			t.Fatal(err)
		}
		if page.RevisionID != out.Revision.ID || page.ProjectID != out.Project.ID || page.SemanticHash != out.Revision.SemanticHash || page.View != view || !page.Complete || page.Truncated || page.Limits.ScanPolicy != "complete-scan-admission" {
			t.Fatalf("lost projection %+v", page)
		}
		if view == "routes" && (len(page.Items) != 1 || page.Items[0].Route == nil || page.Items[0].Route.References.ProducerID != ids["emit"] || len(page.Items[0].Route.Related) != 2 || page.Items[0].Route.Group.Status != "known" || string(page.Items[0].Route.Group.Value) != `"billing"`) {
			t.Fatalf("lost exact route %+v", page)
		}
		if view == "jobs" && (len(page.Items) != 1 || page.Items[0].Job == nil || page.Items[0].Job.Trigger.Kind != "cron") {
			t.Fatalf("lost static job %+v", page)
		}

		if view == "routes" {
			if page.NextCursor == "" {
				t.Fatal("route page did not expose cursor")
			}
			in.Cursor = page.NextCursor
			nextRaw, err := json.Marshal(in)
			if err != nil {
				t.Fatal(err)
			}
			status, next, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", route, nextRaw)
			if err != nil || status != 200 || string(next) == string(wire) {
				t.Fatalf("cursor did not advance %d %s %v", status, next, err)
			}
			validateBackendImportResponse(t, "POST", route, next)
			var second backendmodel.EventsPage
			if err := json.Unmarshal(next, &second); err != nil {
				t.Fatal(err)
			}
			if len(second.Items) != 1 || second.Items[0].Route == nil || second.Items[0].Route.References.DeliveryEdgeID == page.Items[0].Route.References.DeliveryEdgeID {
				t.Fatal("exact edge pairs collapsed")
			}
			in.Limit = 2
			changed, _ := json.Marshal(in)
			status, _, err = s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", route, changed)
			if err != nil || status != 400 {
				t.Fatalf("changed cursor limit %d %v", status, err)
			}
			in.Limit = 1
			in.SeedNodeID = ids["consumer"]
			changed, _ = json.Marshal(in)
			status, _, err = s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", route, changed)
			if err != nil || status != 400 {
				t.Fatalf("changed cursor selector %d %v", status, err)
			}
		}
		user, err := s.mcpIdentity(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, route, strings.NewReader(string(raw))).WithContext(withAuthContext(t.Context(), nil, user))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.routeMux().ServeHTTP(rec, req)
		if rec.Code != 200 || rec.Body.String() != string(wire) {
			t.Fatalf("REST/MCP mismatch %d %s", rec.Code, rec.Body)
		}
	}
	current, err := s.backendRepo.Get(t.Context(), out.Project.ID)
	if err != nil || current.CurrentRevisionID != out.Project.CurrentRevisionID || current.Version != out.Project.Version {
		t.Fatalf("read mutated project %+v %v", current, err)
	}
	missing := `{"revisionId":"` + out.Project.ID + `","view":"routes"}`
	status, _, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", route, []byte(missing))
	if err != nil || status != 404 {
		t.Fatalf("missing revision %d %v", status, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	status, _, err = s.CallAsMCP(ctx, loopbackTestSrc(), "POST", route, []byte(`{"revisionId":"`+out.Revision.ID+`","view":"routes"}`))
	if err == nil && status == 200 {
		t.Fatal("cancelled request returned a page")
	}
	other, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Other", IdempotencyKey: "other-events"})
	if err != nil {
		t.Fatal(err)
	}
	status, _, err = s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", "/api/backend-projects/"+other.ID+"/events/query", []byte(`{"revisionId":"`+out.Revision.ID+`","view":"routes"}`))
	if err != nil || status != 404 {
		t.Fatalf("foreign revision %d %v", status, err)
	}
}

func TestBackendEventsAuthenticationBodyLimitAndQueryPolicy(t *testing.T) {
	s := loopbackTestServer(t, nil)
	rec := httptest.NewRecorder()
	s.handleQueryBackendEvents(rec, httptest.NewRequest(http.MethodPost, "/api/backend-projects/x/events/query", strings.NewReader(`{}`)))
	if rec.Code != 401 {
		t.Fatal(rec.Code)
	}
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Policy", IdempotencyKey: "events-policy"})
	if err != nil {
		t.Fatal(err)
	}
	route := "/api/backend-projects/" + p.ID + "/events/query"
	status, _, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", route+"?view=routes", []byte(`{}`))
	if err != nil || status != 400 {
		t.Fatalf("query params %d %v", status, err)
	}
	s.cfg.MaxBody = 16
	status, _, err = s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", route, []byte(`{"revisionId":"`+p.CurrentRevisionID+`","view":"routes"}`))
	if err != nil || status != 413 {
		t.Fatalf("body limit %d %v", status, err)
	}
}

package admin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/mockplane"
)

const responseRuleTestDocument = `{"openapi":"3.1.0","info":{"title":"Rules","version":"1"},"paths":{"/orders":{"get":{"responses":{"200":{"description":"OK"}}}}},"x-mocker-response-rules":{"formatVersion":1,"rules":[{"id":"r","name":"Rule","binding":{"method":"GET","path":"/orders"},"nodes":[{"id":"s","type":"start","name":"Start","x":0,"y":0},{"id":"f","type":"fallback","name":"Fallback","x":200,"y":0}],"edges":[{"id":"e","from":"s","port":"next","to":"f"}]}]}}`
const responseRuleTestRule = `{"id":"r","name":"Rule","nodes":[],"edges":[]}`

func TestResponseRuleRoutesAndReadOnlyEvaluation(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, func(cfg *config.Config) { cfg.CheckpointDebounce = 300 })
	call := func(method, path, body string, want int) []byte {
		t.Helper()
		status, out, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), method, path, []byte(body))
		if err != nil || status != want {
			t.Fatalf("%s %s: %d %s %v", method, path, status, out, err)
		}
		return out
	}
	raw := call("POST", "/api/designs", `{"name":"Rules"}`, 201)
	var d apidesign.Detail
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/designs/%d/response-rules", d.Design.ID)
	call("POST", base, `{"expectedVersion":1,"rule":`+responseRuleTestRule+`}`, 201)
	call("GET", base, "", 200)
	call("GET", base+"/r", "", 200)
	before, err := s.designsRepo.Detail(t.Context(), d.Design.ID)
	if err != nil {
		t.Fatal(err)
	}
	wsBefore, err := s.ws.ByID(t.Context(), d.Design.DraftWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	checkpointsBefore, err := s.checkpointsRepo.List(t.Context(), d.Design.DraftWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"validate", "simulate"} {
		payload := map[string]any{"document": responseRuleTestDocument}
		if suffix == "simulate" {
			payload["request"] = map[string]any{"query": []any{}, "headers": []any{}}
		}
		body, _ := json.Marshal(payload)
		out := call("POST", base+"/r/"+suffix, string(body), 200)
		var value map[string]any
		if err := json.Unmarshal(out, &value); err != nil {
			t.Fatal(err)
		}
		source, _ := value["source"].(map[string]any)
		if value["valid"] != true || source["kind"] != "proposal" || source["version"] != nil || source["revisionId"] != nil || source["documentHash"] == nil {
			t.Fatalf("bad source/result: %s", out)
		}
		if suffix == "simulate" && (value["outcome"] != "fallback" || value["response"] != nil || len(value["trace"].([]any)) != 2 || value["totalDelayMs"] != float64(0)) {
			t.Fatalf("bad fallback: %s", out)
		}
	}
	out := call("POST", base+"/r/simulate", `{"request":{"query":[],"headers":[]}}`, 200)
	if !strings.Contains(string(out), `"outcome":"invalid"`) || !strings.Contains(string(out), `"trace":[]`) || !strings.Contains(string(out), `"kind":"saved"`) {
		t.Fatalf("incomplete graph: %s", out)
	}
	after, err := s.designsRepo.Detail(t.Context(), d.Design.ID)
	if err != nil {
		t.Fatal(err)
	}
	wsAfter, err := s.ws.ByID(t.Context(), d.Design.DraftWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	checkpointsAfter, err := s.checkpointsRepo.List(t.Context(), d.Design.DraftWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(wsBefore, wsAfter) || !reflect.DeepEqual(checkpointsBefore, checkpointsAfter) {
		t.Fatal("evaluation changed design/workspace/checkpoints")
	}
	call("PUT", base+"/r", `{"expectedVersion":2,"rule":`+strings.Replace(responseRuleTestRule, "Rule", "Saved", 1)+`}`, 200)
	call("POST", base+"/r/commands", `{"expectedVersion":3,"commands":[{"type":"set_rule","name":"Commands"}]}`, 200)
	call("POST", base+"/r/commands", `{"expectedVersion":4,"commands":[{"type":"set_rule","name":"rollback"},{"type":"remove_node","nodeId":"missing"}]}`, 400)
	out = call("GET", base+"/r", "", 200)
	if strings.Contains(string(out), "rollback") || !strings.Contains(string(out), "Commands") {
		t.Fatalf("batch not atomic: %s", out)
	}
	call("DELETE", base+"/r", `{"expectedVersion":3}`, 409)
	call("DELETE", base+"/r", `{"expectedVersion":4}`, 200)
	call("GET", base+"/r", "", 404)
}

func TestResponseRuleStrictWireAdmission(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, nil)
	raw, err := s.designsRepo.Create(t.Context(), apidesign.CreateInput{Name: "Rules", Document: responseRuleTestDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/designs/%d/response-rules", raw.Design.ID)
	cases := []struct{ method, suffix, body string }{
		{"POST", "", `{"rule":` + responseRuleTestRule + `}`},
		{"POST", "", `{"expectedVersion":null,"rule":` + responseRuleTestRule + `}`},
		{"POST", "", `{"expectedVersion":0,"rule":` + responseRuleTestRule + `}`},
		{"POST", "", `{"expectedVersion":1,"rule":null}`},
		{"POST", "", `{"expectedVersion":1,"rule":` + responseRuleTestRule + `,"commands":[]}`},
		{"PUT", "/r", `{"expectedVersion":1,"rule":{"id":"r","name":"Rule","nodes":[{"id":"s","type":"start","name":"Start","y":0}],"edges":[]}}`},
		{"PUT", "/r", `{"expectedVersion":1,"rule":{"id":"r","name":"Rule","nodes":[{"id":"s","type":"start","name":"Start","x":0,"y":0,"delayMs":0}],"edges":[]}}`},
		{"POST", "/r/commands", `{"expectedVersion":1,"commands":[{"type":"set_rule","name":"x","nodeId":"s"}]}`},
		{"POST", "/r/commands", `{"expectedVersion":1,"commands":null}`},
		{"DELETE", "/r", `{"expectedVersion":1,"extra":false}`},
		{"POST", "/r/validate", `null`},
		{"POST", "/r/validate", `{"document":null}`},
		{"POST", "/r/validate", `{"document":""}`},
		{"POST", "/r/validate", `{"request":{}}`},
		{"POST", "/r/simulate", `{}`},
		{"POST", "/r/simulate", `{"request":null}`},
		{"POST", "/r/simulate", `{"request":{"headers":[]}}`},
		{"POST", "/r/simulate", `{"request":{"query":[],"headers":[],"bodyJSON":null}}`},
		{"POST", "/r/simulate", `{"request":{"query":[],"headers":[],"bodyJSON":"{\"x\":1,\"x\":2}"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.suffix+tc.body, func(t *testing.T) {
			status, out, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), tc.method, base+tc.suffix, []byte(tc.body))
			if err != nil || status != 400 {
				t.Fatalf("status=%d %s %v", status, out, err)
			}
		})
	}
	after, err := s.designsRepo.Detail(t.Context(), raw.Design.ID)
	if err != nil || after.Design.Version != 1 || after.Draft.ID != raw.Draft.ID {
		t.Fatalf("rejected writes mutated: %+v %v", after, err)
	}
	// Browser POSTs must still pass the normal CSRF middleware.
	req := httptest.NewRequest(http.MethodPost, base+"/r/simulate", strings.NewReader(`{"request":{"query":[],"headers":[]}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "mocker.local"
	req.Header.Set("Origin", "http://mocker.local")
	req = req.WithContext(withAuthContext(req.Context(), &auth.Session{CSRFToken: "real-token"}, &auth.User{ID: 1}))
	recorder := httptest.NewRecorder()
	s.enforceCSRF(s.routeMux()).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("browser missing CSRF: %d %s", recorder.Code, recorder.Body.String())
	}
}

// Snapshot actual rows, including entities, traffic and scenario revisions, so
// an accidental update is caught even when it does not change table counts.
func responseRuleRows(t *testing.T, s *Server) map[string][]byte {
	t.Helper()
	result := map[string][]byte{}
	for _, table := range []string{"api_designs", "api_design_revisions", "workspaces", "scenarios", "checkpoints", "entities", "traffic", "design_scenarios", "design_scenario_revisions", "op_overrides", "custom_endpoints", "resources"} {
		rows, err := s.db.R.QueryContext(t.Context(), "SELECT * FROM "+table+" ORDER BY rowid")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		values := [][]any{}
		for rows.Next() {
			row := make([]any, len(columns))
			targets := make([]any, len(columns))
			for i := range row {
				targets[i] = &row[i]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatal(err)
			}
			values = append(values, row)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		result[table], err = json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func TestResponseRuleEvaluationCannotTouchRuntimeState(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, func(cfg *config.Config) { cfg.CheckpointDebounce = 300 })
	document := strings.Replace(responseRuleTestDocument, `"type":"fallback","name":"Fallback"`, `"type":"response","name":"Created","response":{"status":201,"mediaType":"application/json","headers":[],"bodyJSON":"{\"created\":true}"}`, 1)
	d, err := s.designsRepo.Create(t.Context(), apidesign.CreateInput{Name: "Rules", Document: document, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	state := livestate.NewStore(0, nil)
	defer state.Close()
	s.SetLiveState(state)
	for _, directive := range []livestate.Directive{{Target: livestate.Target{All: true}, Action: livestate.ActionFail, Status: 503, N: 3}, {Target: livestate.Target{All: true}, Action: livestate.ActionPause}} {
		if err := state.Set(d.Design.DraftWorkspaceID, directive); err != nil {
			t.Fatal(err)
		}
	}
	// Seed rows that an accidental runtime action could consume or overwrite.
	err = s.db.Write(t.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO scenarios(workspace_id,name,snapshot,created_at) VALUES(?,?,?,1)`, d.Design.DraftWorkspaceID, "Sentinel", `{}`); err != nil {
			return err
		}
		resource, err := tx.ExecContext(t.Context(), `INSERT INTO resources(workspace_id,route_family,name,entity_schema,seq) VALUES(?,?,?,?,?)`, d.Design.DraftWorkspaceID, "/orders", "Orders", `{"type":"object"}`, 7)
		if err != nil {
			return err
		}
		resourceID, err := resource.LastInsertId()
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(t.Context(), `INSERT INTO entities(resource_id,entity_key,data,created_at,updated_at) VALUES(?,?,?,1,1)`, resourceID, "7", `{"id":7,"state":"keep"}`); err != nil {
			return err
		}
		if _, err = tx.ExecContext(t.Context(), `INSERT INTO traffic(workspace_id,ts,method,path,status,duration_ms) VALUES(?,1,'GET','/orders',200,0)`, d.Design.DraftWorkspaceID); err != nil {
			return err
		}
		_, err = tx.ExecContext(t.Context(), `INSERT INTO checkpoints(workspace_id,kind,label,config_snap,created_at) VALUES(?,'manual','Sentinel','{}',1)`, d.Design.DraftWorkspaceID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	before := responseRuleRows(t, s)
	liveBefore := state.List(d.Design.DraftWorkspaceID)
	base := fmt.Sprintf("/api/designs/%d/response-rules/r", d.Design.ID)
	for _, suffix := range []string{"validate", "simulate"} {
		body := `{}`
		if suffix == "simulate" {
			body = `{"request":{"query":[],"headers":[{"name":"Authorization","value":"private-test-value"}]}}`
		}
		status, out, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", base+"/"+suffix, []byte(body))
		if err != nil || status != 200 {
			t.Fatalf("%s: %d %s %v", suffix, status, out, err)
		}
		if bytes.Contains(out, []byte("private-test-value")) {
			t.Fatal("fixture echoed in result")
		}
		if suffix == "simulate" && !bytes.Contains(out, []byte(`"status":201`)) {
			t.Fatalf("not configured response: %s", out)
		}
	}
	if !reflect.DeepEqual(before, responseRuleRows(t, s)) || !reflect.DeepEqual(liveBefore, state.List(d.Design.DraftWorkspaceID)) {
		t.Fatal("evaluation touched persistent or live state")
	}
	status, out, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", fmt.Sprintf("/api/workspaces/%d/openapi.json", d.Design.DraftWorkspaceID), nil)
	if err != nil || status != 200 || !strings.Contains(string(out), "x-mocker-response-rules") {
		t.Fatalf("export lost authored graph: %d %s %v", status, out, err)
	}
	ws, err := s.ws.ByID(t.Context(), d.Design.DraftWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	plane := mockplane.New(s.cfg, s.ws, s.specsRepo, s.log)
	recorder := httptest.NewRecorder()
	plane.ServeWorkspace(recorder, httptest.NewRequest("GET", "http://mock.local/orders", nil), ws)
	if recorder.Code != 200 || strings.Contains(recorder.Body.String(), `"created":true`) {
		t.Fatalf("authored rule changed live behavior: %d %s", recorder.Code, recorder.Body.String())
	}
	// The first real request after simulation must still observe both armed
	// directives. Observe the real store's effect, then release the pause
	// explicitly; no sleep or assumption about goroutine scheduling is needed.
	observed := &responseRulePauseObserver{Store: state, paused: make(chan struct{})}
	livePlane := mockplane.New(s.cfg, s.ws, s.specsRepo, s.log)
	livePlane.SetLiveState(observed)
	liveRecorder := httptest.NewRecorder()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		livePlane.ServeWorkspace(liveRecorder, httptest.NewRequest("GET", "http://mock.local/orders", nil).WithContext(ctx), ws)
	}()
	defer func() { cancel(); <-done }()
	select {
	case <-observed.paused:
	case <-done:
		t.Fatalf("first live request bypassed armed pause: %d %s", liveRecorder.Code, liveRecorder.Body.String())
	case <-ctx.Done():
		t.Fatal("live request never reached pause")
	}
	select {
	case <-done:
		t.Fatal("paused HTTP request completed before explicit release")
	default:
	}
	remaining := state.List(ws.ID)
	failCount := -1
	for _, directive := range remaining {
		if directive.Action == livestate.ActionFail {
			failCount = directive.N
		}
	}
	if failCount != 2 {
		t.Fatalf("first live request should consume exactly one of three failures, got %d", failCount)
	}
	if removed, err := state.Delete(ws.ID, livestate.Target{All: true}, livestate.ActionPause); err != nil || removed != 1 {
		t.Fatalf("release pause: %d %v", removed, err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("released HTTP request did not finish")
	}
	if liveRecorder.Code != 503 {
		t.Fatalf("simulation consumed fail-next: subsequent HTTP returned %d %s", liveRecorder.Code, liveRecorder.Body.String())
	}
	// Publishing the authored extension also keeps the ordinary mock behavior.
	review, err := s.designsRepo.RequestReview(t.Context(), d.Design.ID, d.Design.Version, "Publish metadata", "ui")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.designsRepo.Publish(t.Context(), d.Design.ID, review.ID, d.Design.Version, "ui"); err != nil {
		t.Fatal(err)
	}
	published, err := s.ws.ByID(t.Context(), d.Design.PublishedWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	publishedRecorder := httptest.NewRecorder()
	plane.ServeWorkspace(publishedRecorder, httptest.NewRequest("GET", "http://mock.local/orders", nil), published)
	if publishedRecorder.Code != 200 || strings.Contains(publishedRecorder.Body.String(), `"created":true`) {
		t.Fatalf("publishing enabled live response rules: %d %s", publishedRecorder.Code, publishedRecorder.Body.String())
	}
}

// This test adapter only observes the real Store result; it never substitutes
// its own state or status decision.
type responseRulePauseObserver struct {
	*livestate.Store
	paused chan struct{}
	once   sync.Once
}

func (s *responseRulePauseObserver) Apply(workspaceID int64, method, path string) livestate.Effect {
	effect := s.Store.Apply(workspaceID, method, path)
	if effect.Pause {
		s.once.Do(func() { close(s.paused) })
	}
	return effect
}

func TestResponseRuleAdmissionReportsPreciseField(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, nil)
	d, err := s.designsRepo.Create(t.Context(), apidesign.CreateInput{Name: "Rules", Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/designs/%d/response-rules", d.Design.ID)
	status, out, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", base, []byte(`{"expectedVersion":1,"rule":{"id":"r","name":"Rule","nodes":[{"id":"s","type":"start","name":"Start","y":0}],"edges":[]}}`))
	if err != nil || status != 400 || !strings.Contains(string(out), `"pointer":"/rule/nodes/0/x"`) {
		t.Fatalf("field path: %d %s %v", status, out, err)
	}
}

func TestResponseRuleRequestSizeLimit(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, nil)
	d, err := s.designsRepo.Create(t.Context(), apidesign.CreateInput{Name: "Rules", Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.MaxBody = 1024
	status, out, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", fmt.Sprintf("/api/designs/%d/response-rules/r/validate", d.Design.ID), []byte(`{"document":"`+strings.Repeat(" ", 2048)+`"}`))
	if err != nil || status != 413 {
		t.Fatalf("size limit: %d %s %v", status, out, err)
	}
	after, err := s.designsRepo.Detail(t.Context(), d.Design.ID)
	if err != nil || after.Design.Version != 1 {
		t.Fatalf("size failure wrote: %+v %v", after, err)
	}
}

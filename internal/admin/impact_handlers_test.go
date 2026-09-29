package admin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

func TestImpactHTTPStrictInputsAndReadOnly(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, nil)
	d, err := s.designsRepo.Create(t.Context(), apidesign.CreateInput{Name: "Impact", Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/designs/%d/impact", d.Design.ID)
	for _, tc := range []struct {
		body   string
		status int
	}{
		{fmt.Sprintf(`{"fromRevisionId":%d,"document":"{}"}`, d.Draft.ID), 200},
		{fmt.Sprintf(`{"fromRevisionId":%d,"toRevisionId":%d}`, d.Draft.ID, d.Draft.ID), 200},
		{`{}`, 400},
		{fmt.Sprintf(`{"fromRevisionId":%d}`, d.Draft.ID), 400},
		{fmt.Sprintf(`{"fromRevisionId":%d,"document":null}`, d.Draft.ID), 400},
		{fmt.Sprintf(`{"fromRevisionId":%d,"document":"{}","toRevisionId":null}`, d.Draft.ID), 400},
		{fmt.Sprintf(`{"fromRevisionId":%d,"document":null,"toRevisionId":%d}`, d.Draft.ID, d.Draft.ID), 400},
		{fmt.Sprintf(`{"fromRevisionId":%d,"document":"{}","toRevisionId":%d}`, d.Draft.ID, d.Draft.ID), 400},
		{fmt.Sprintf(`{"fromRevisionId":%d,"document":"{}","extra":true}`, d.Draft.ID), 400},
		{fmt.Sprintf(`{"fromRevisionId":%d,"document":"[]"}`, d.Draft.ID), 400},
		{fmt.Sprintf(`{"fromRevisionId":%d,"document":"openapi: 3.1.0"}`, d.Draft.ID), 400},
		{`{"fromRevisionId":0,"document":"{}"}`, 400},
		{`{"fromRevisionId":99999,"document":"{}"}`, 404},
	} {
		status, body, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", path, []byte(tc.body))
		if err != nil || status != tc.status {
			t.Fatalf("%s => %d %s %v", tc.body, status, body, err)
		}
		if status == 200 {
			var report apidesign.ImpactReport
			if err := jsonx.Unmarshal(body, &report); err != nil {
				t.Fatal(err)
			}
			if report.Changes == nil || report.Affected == nil || report.Evidence == nil || report.Diagnostics == nil || report.Coverage.TruncatedReasons == nil {
				t.Fatalf("null arrays: %s", body)
			}
		}
	}
	status, _, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", path, []byte(strings.Repeat(" ", int(s.cfg.MaxBody)+1)))
	if err != nil || status != 413 {
		t.Fatalf("oversize: %d %v", status, err)
	}
	after, err := s.designsRepo.Detail(t.Context(), d.Design.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Design.Version != 1 || len(after.Revisions) != 1 {
		t.Fatal("analysis created revision")
	}
	var checkpoints int
	if err := s.db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM checkpoints").Scan(&checkpoints); err != nil {
		t.Fatal(err)
	}
	if checkpoints != 0 {
		t.Fatal("analysis created checkpoint")
	}
}

func TestImpactHTTPUnknownScenarioJoinMakesCompleteFalse(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, nil)
	const document = `{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{"/orders":{"get":{"x-mocker-canvas-operation-id":"orders","operationId":"orders","responses":{"200":{"description":"OK"}}}}}}`
	d, err := s.designsRepo.Create(t.Context(), apidesign.CreateInput{Name: "API", Source: "ui", Document: document})
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := s.designScenariosRepo.Create(t.Context(), designscenario.CreateInput{Source: "ui", Document: designscenario.Document{
		FormatVersion: 1, Title: "Checkout", Fragments: []designscenario.Fragment{},
		Participants: []designscenario.Participant{{ID: "client", Name: "Client", Kind: "client"}, {ID: "api", Name: "API", Kind: "service"}},
		Contracts:    []designscenario.Contract{{ID: "api", Name: "API", Mode: "copy", Source: &designscenario.ContractSource{DesignID: d.Design.ID, RevisionID: d.Draft.ID, Version: 1}, Document: jsonx.RawMessage(d.Draft.Document)}},
		Messages: []designscenario.Message{
			{ID: "known", FromID: "client", ToID: "api", Kind: "request", Operation: &designscenario.OperationBinding{ContractID: "api", OperationKey: "orders"}},
			{ID: "unknown", FromID: "client", ToID: "api", Kind: "request", Operation: &designscenario.OperationBinding{ContractID: "api", OperationKey: "missing"}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	candidate := strings.Replace(d.Draft.Document, `"operationId": "orders"`, `"operationId": "ordersV2"`, 1)
	body, err := jsonx.Marshal(apidesign.ImpactInput{FromRevisionID: d.Draft.ID, Document: &candidate})
	if err != nil {
		t.Fatal(err)
	}
	status, out, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", fmt.Sprintf("/api/designs/%d/impact", d.Design.ID), body)
	if err != nil || status != 200 {
		t.Fatalf("analyze: %d %s %v", status, out, err)
	}
	var report apidesign.ImpactReport
	if err := jsonx.Unmarshal(out, &report); err != nil {
		t.Fatal(err)
	}
	if report.Complete || len(report.Coverage.TruncatedReasons) != 0 || report.Coverage.ScenariosScanned != 1 {
		t.Fatalf("unknown join became complete: %s", out)
	}
	seen := map[string]bool{}
	for _, e := range report.Affected {
		if e.Kind != "scenario_message" {
			continue
		}
		loc := e.Before
		if loc == nil {
			loc = e.After
		}
		if loc == nil || loc.ScenarioID != scenario.Scenario.ID || loc.ScenarioRevision != scenario.Draft.ID {
			t.Fatalf("wrong scenario snapshot: %+v", e)
		}
		seen[loc.MessageID] = true
	}
	if !seen["known"] || !seen["unknown"] {
		t.Fatalf("lost usage or unknown locator: %s", out)
	}
}

func TestImpactHTTPRequiresSession(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, nil)
	r := httptest.NewRequest("POST", "http://mocker.local/api/designs/1/impact", strings.NewReader(`{"fromRevisionId":1,"document":"{}"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://mocker.local")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d %s", w.Code, w.Body)
	}
}

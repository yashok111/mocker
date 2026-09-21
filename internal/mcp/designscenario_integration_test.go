package mcp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/testauth"
)

func callDesignScenarioTool(t *testing.T, calls Caller, name string, args any, out any) string {
	t.Helper()
	body, err := jsonx.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	raw, errMsg := callTool(t, calls, name, string(body))
	if errMsg != "" {
		return errMsg
	}
	if err := jsonx.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode %s: %v; body=%s", name, err, raw)
	}
	return ""
}

func TestDesignScenarioMCPCommandsCASHistoryAndHTTPReadback(t *testing.T) {
	t.Parallel()
	cfg := resourcesTestConfig(t)
	srv, _ := newResourcesTestServer(t, cfg)

	document := designscenario.Document{
		FormatVersion: 1,
		Title:         "Checkout",
		Participants:  []designscenario.Participant{},
		Messages:      []designscenario.Message{},
		Fragments:     []designscenario.Fragment{},
		Contracts:     []designscenario.Contract{},
	}
	var created designscenario.Detail
	if errMsg := callDesignScenarioTool(t, srv, "create_design_scenario", map[string]any{
		"document":   document,
		"formDrafts": map[string]string{"operation:pending": `{"path":"/orders"}`},
		"summary":    "initial",
	}, &created); errMsg != "" {
		t.Fatal(errMsg)
	}
	if created.Scenario.Version != 1 || created.Draft.Source != "mcp" {
		t.Fatalf("created detail = %+v", created)
	}

	commands := []designscenario.Command{
		{Type: "upsert_participant", Participant: &designscenario.Participant{ID: "buyer", Name: "Buyer", Kind: "user"}},
		{Type: "upsert_participant", Participant: &designscenario.Participant{ID: "orders", Name: "Orders", Kind: "service"}},
		{Type: "upsert_message", Message: &designscenario.Message{ID: "create-order", FromID: "buyer", ToID: "orders", Kind: "request", Label: "Create order"}},
		{Type: "set_title", Title: "Checkout updated"},
	}
	var updated designscenario.Detail
	if errMsg := callDesignScenarioTool(t, srv, "apply_design_scenario_commands", map[string]any{
		"scenarioId":      created.Scenario.ID,
		"expectedVersion": 1,
		"commands":        commands,
		"summary":         "add checkout call",
	}, &updated); errMsg != "" {
		t.Fatal(errMsg)
	}
	if updated.Scenario.Version != 2 || updated.Draft.Document.Title != "Checkout updated" || len(updated.Draft.Document.Messages) != 1 {
		t.Fatalf("updated detail = %+v", updated)
	}
	if updated.Draft.FormDrafts["operation:pending"] == "" {
		t.Fatalf("commands lost pending form buffers: %+v", updated.Draft.FormDrafts)
	}

	var ignored designscenario.Detail
	errMsg := callDesignScenarioTool(t, srv, "apply_design_scenario_commands", map[string]any{
		"scenarioId":      created.Scenario.ID,
		"expectedVersion": 1,
		"commands":        []designscenario.Command{{Type: "set_title", Title: "stale"}},
	}, &ignored)
	if !strings.Contains(errMsg, "409") || !strings.Contains(errMsg, `"version":2`) {
		t.Fatalf("stale command error = %q", errMsg)
	}

	var diff designscenario.Diff
	if errMsg = callDesignScenarioTool(t, srv, "get_design_scenario_diff", map[string]any{
		"scenarioId":     created.Scenario.ID,
		"fromRevisionId": created.Draft.ID,
		"toRevisionId":   updated.Draft.ID,
	}, &diff); errMsg != "" {
		t.Fatal(errMsg)
	}
	if diff.FromRevisionID != created.Draft.ID || diff.ToRevisionID != updated.Draft.ID || len(diff.Changes) == 0 {
		t.Fatalf("diff = %+v", diff)
	}

	var historical designscenario.Revision
	if errMsg = callDesignScenarioTool(t, srv, "get_design_scenario_revision", map[string]any{
		"scenarioId": created.Scenario.ID,
		"revisionId": created.Draft.ID,
	}, &historical); errMsg != "" {
		t.Fatal(errMsg)
	}
	if historical.Document.Title != "Checkout" {
		t.Fatalf("historical revision changed: %+v", historical)
	}

	var restored designscenario.Detail
	if errMsg = callDesignScenarioTool(t, srv, "restore_design_scenario_revision", map[string]any{
		"scenarioId":      created.Scenario.ID,
		"expectedVersion": 2,
		"revisionId":      created.Draft.ID,
		"summary":         "restore initial",
	}, &restored); errMsg != "" {
		t.Fatal(errMsg)
	}
	if restored.Scenario.Version != 3 || restored.Draft.Document.Title != "Checkout" || len(restored.Revisions) != 3 {
		t.Fatalf("restored detail = %+v", restored)
	}

	handler := srv.Handler()
	login := httptest.NewRequest(http.MethodPost, "http://mocker.local/api/auth/login",
		strings.NewReader(fmt.Sprintf(`{"name":"Analyst","password":%q}`, testauth.Password)))
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Origin", "http://mocker.local")
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK || len(loginResponse.Result().Cookies()) != 1 {
		t.Fatalf("login: status=%d body=%s", loginResponse.Code, loginResponse.Body.String())
	}

	read := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("http://mocker.local/api/design-scenarios/%d", created.Scenario.ID), nil)
	read.AddCookie(loginResponse.Result().Cookies()[0])
	readResponse := httptest.NewRecorder()
	handler.ServeHTTP(readResponse, read)
	if readResponse.Code != http.StatusOK {
		t.Fatalf("HTTP readback: status=%d body=%s", readResponse.Code, readResponse.Body.String())
	}
	var viaHTTP designscenario.Detail
	if err := jsonx.Unmarshal(readResponse.Body.Bytes(), &viaHTTP); err != nil {
		t.Fatal(err)
	}
	if viaHTTP.Scenario.Version != 3 || viaHTTP.Draft.ID != restored.Draft.ID || viaHTTP.Draft.Document.Title != "Checkout" {
		t.Fatalf("HTTP and MCP do not share state: %+v", viaHTTP)
	}
}

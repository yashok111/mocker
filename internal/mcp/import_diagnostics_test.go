package mcp

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

func TestImportSchemaErrorsStayFocusedAndBounded(t *testing.T) {
	caller := &recordingCaller{status: 200, body: []byte(`{}`)}
	args := `{"projectId":"` + backendTestID + `","importId":"` + backendTestID + `","batchId":"bad","expectedImportVersion":1,"payloadHash":"` + strings.Repeat("a", 64) + `","commands":[{"op":"upsert_node","node":{"externalKey":"db","kind":"datastore","name":null,"attributes":{},"evidenceKeys":[]}}]}`
	_, message := callTool(t, caller, "put_backend_import_batch", args)
	if caller.method != "" || len(message) > 4096 || !strings.Contains(message, "/commands/0/node/name") || strings.Contains(message, "resolve_assertion") {
		t.Fatalf("unfocused schema diagnostic (%d bytes): %.300s", len(message), message)
	}
}

func TestImportSchemaDetailPagesRetainAllSelectedErrors(t *testing.T) {
	caller := &recordingCaller{status: 200, body: []byte(`{}`)}
	commands := strings.Repeat(`{"op":"upsert_node","node":{"externalKey":"bad","kind":"handler","name":null,"attributes":{},"evidenceKeys":[]}},`, 19) + `{"op":"upsert_node","node":{"externalKey":"bad-last","kind":"handler","name":null,"attributes":{},"evidenceKeys":[]}}`
	arguments := `{"projectId":"` + backendTestID + `","importId":"` + backendTestID + `","batchId":"bad","expectedImportVersion":1,"payloadHash":"` + strings.Repeat("a", 64) + `","commands":[` + commands + `]}`
	cursor := ""
	var full strings.Builder
	for {
		request := `{"name":"put_backend_import_batch","arguments":` + arguments + `,"cursor":"` + cursor + `"}`
		raw, message := callTool(t, caller, "diagnose_backend_import_request", request)
		if message != "" {
			t.Fatal(message)
		}
		var page struct {
			DetailChunk string `json:"detailChunk"`
			NextCursor  string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatal(err)
		}
		if len(raw) > 8000 {
			t.Fatal("unbounded diagnostic detail")
		}
		full.WriteString(page.DetailChunk)
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if caller.method != "" || !strings.Contains(full.String(), "/commands/19/node/name") {
		t.Fatal("lost detail or executed request")
	}
}

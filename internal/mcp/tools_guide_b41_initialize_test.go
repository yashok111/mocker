package mcp

import (
	"encoding/json/v2"
	"net/http"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/guide"
)

func TestB41InitializeWireUsesCurrentGuideOwners(t *testing.T) {
	t.Parallel()
	response := doMCP(t, newTestEndpoint(t).Handler(), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"guide-owner-test","version":"1"}}}`, map[string]string{"Authorization": "Bearer " + testKey})
	if response.Code != http.StatusOK {
		t.Fatalf("initialize status=%d: %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Result struct {
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	text := envelope.Result.Instructions
	if text != guide.Instructions() {
		t.Fatal("initialize wire differs from the checked guide instructions")
	}
	for _, topic := range []string{"backend-events", "backend-import", "backend-sync", "backend-change-proposals"} {
		owner, ok := guide.WorkflowForTopic(topic)
		label := strings.TrimPrefix(owner.WorkflowID, "mocker-backend-") + owner.WorkflowVersion + "/" + topic
		if !ok || !strings.Contains(text, label) {
			t.Errorf("initialize wire omits current guide owner %s", label)
		}
	}
}

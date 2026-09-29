package designscenario

import (
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestOperationKeyDiagnosticsExplainHowToRepairWorkspaceKeys(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"POST%20%2Flogin", "POST /login", "createLogin"} {
		t.Run(key, func(t *testing.T) {
			revision := bindingRevision()
			revision.Document.Messages[0].Operation.OperationKey = key
			warningFound := false
			for _, diagnostic := range validateDocument(revision.Document) {
				if diagnostic.Pointer == "/messages/0/operation/operationKey" {
					warningFound = true
					if diagnostic.Severity != "warning" || !strings.Contains(diagnostic.Message, "x-mocker-canvas-operation-id") {
						t.Fatalf("saved broken reference needs repair guidance: %+v", diagnostic)
					}
				}
			}
			if !warningFound {
				t.Fatal("saving a broken operation reference must report a warning")
			}
			analysis := AnalyzeDataFlow(revision.Document)
			found := false
			for _, diagnostic := range analysis.Diagnostics {
				if diagnostic.Pointer == "/messages/1/execution/bindings/0/sourceMessageId" && diagnostic.Severity == "error" {
					found = true
					if !strings.Contains(diagnostic.Message, "x-mocker-canvas-operation-id") || !strings.Contains(diagnostic.Message, "opKey") {
						t.Fatalf("analysis must explain the key to copy: %+v", diagnostic)
					}
				}
			}
			if !found {
				t.Fatal("wrong workspace key did not block binding analysis")
			}
			revision.Document.Messages[1].Execution.Bindings = nil
			if _, err := PrepareRun(revision, "wrong-key", "", "mcp", nil); err == nil || !strings.Contains(err.Error(), "x-mocker-canvas-operation-id") {
				t.Fatalf("plain run needs the same repair guidance: %v", err)
			}
			var contract struct {
				Paths map[string]map[string]struct {
					Key string `json:"x-mocker-canvas-operation-id"`
				} `json:"paths"`
			}
			if err := jsonx.Unmarshal(revision.Document.Contracts[0].Document, &contract); err != nil {
				t.Fatal(err)
			}
			revision.Document.Messages[0].Operation.OperationKey = contract.Paths["/login"]["post"].Key
			if _, err := PrepareRun(revision, "repaired-key", "", "mcp", nil); err != nil {
				t.Fatalf("copying the pinned contract key should repair the run: %v", err)
			}
		})
	}
}

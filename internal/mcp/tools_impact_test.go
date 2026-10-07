package mcp

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestImpactToolPreservesExactInputAndOutput(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`{"fromRevisionId":3,"document":" {\"n\":9007199254740993,\"v\":null} "}`, `{"fromRevisionId":3,"toRevisionId":5}`} {
		response := `{"changes":[{"beforeJSON":"null","afterJSON":"9007199254740993"}],"fieldImpacts":[{"id":"field-1","usagePointer":"/messages/0/execution/bindings/0/sourcePointer"}],"coverage":{"fieldUsagesChecked":1,"fieldImpactsReturned":1},"complete":false}`
		calls := &recordingCaller{status: http.StatusOK, body: []byte(response)}
		raw, msg := callTool(t, calls, "analyze_api_design_impact", `{"designId":7,`+body[1:])
		if msg != "" || calls.method != "POST" || calls.path != "/api/designs/7/impact" || string(calls.sent) != body || string(raw) != response {
			t.Fatalf("dispatch: %s %s %s output=%s error=%s", calls.method, calls.path, calls.sent, raw, msg)
		}
	}
}

func TestImpactToolStrictSchemaAndAnnotations(t *testing.T) {
	t.Parallel()
	for _, args := range []string{`{"designId":7}`, `{"designId":7,"fromRevisionId":1}`, `{"designId":7,"fromRevisionId":0,"document":"{}"}`, `{"designId":7,"fromRevisionId":1,"document":null}`, `{"designId":7,"fromRevisionId":1,"document":"{}","toRevisionId":null}`, `{"designId":7,"fromRevisionId":1,"document":"{}","toRevisionId":2}`, `{"designId":7,"fromRevisionId":1,"document":"{}","extra":1}`} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, msg := callTool(t, calls, "analyze_api_design_impact", args)
		if msg == "" || calls.method != "" {
			t.Fatalf("invalid input reached route: %s %s", args, msg)
		}
	}
	w := describedToolsList(t)
	var env struct {
		Result struct {
			Tools []struct {
				Name        string
				Description string
				Annotations struct {
					ReadOnlyHint   bool
					IdempotentHint bool
				}
				InputSchema jsonx.RawMessage
			}
		}
	}
	if err := jsonx.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	for _, tool := range env.Result.Tools {
		if tool.Name == "analyze_api_design_impact" {
			if !tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint || !strings.Contains(string(tool.InputSchema), `"oneOf"`) {
				t.Fatalf("bad tool contract: %+v", tool)
			}
			if !strings.Contains(tool.Description, "field") || !strings.Contains(tool.Description, "copy") || !strings.Contains(tool.Description, "pinned") {
				t.Fatalf("field scope and provenance caveats absent from description: %q", tool.Description)
			}
			return
		}
	}
	t.Fatal("impact tool missing")
}

func TestImpactFieldOpenAPIContract(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Components struct {
			Schemas map[string]struct {
				Required             []string                    `json:"required"`
				Properties           map[string]jsonx.RawMessage `json:"properties"`
				AdditionalProperties *bool                       `json:"additionalProperties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := jsonx.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	schemas := document.Components.Schemas
	for _, name := range []string{"ApiImpactFieldSelector", "ApiImpactFieldState", "ApiImpactFieldImpact"} {
		if schema, ok := schemas[name]; !ok || schema.AdditionalProperties == nil || *schema.AdditionalProperties {
			t.Errorf("%s must be a closed schema", name)
		}
	}
	for _, field := range []string{"fieldImpacts"} {
		if !strings.Contains(strings.Join(schemas["ApiImpactReport"].Required, ","), field) || schemas["ApiImpactReport"].Properties[field] == nil {
			t.Errorf("report missing required %s", field)
		}
	}
	for _, field := range []string{"fieldUsagesChecked", "fieldImpactsReturned"} {
		if !strings.Contains(strings.Join(schemas["ApiImpactCoverage"].Required, ","), field) || schemas["ApiImpactCoverage"].Properties[field] == nil {
			t.Errorf("coverage missing required %s", field)
		}
	}
	if !strings.Contains(string(schemas["ApiImpactCoverage"].Properties["truncatedReasons"]), `"scenario_fields"`) {
		t.Error("scenario_fields truncation reason missing")
	}
	selector := schemas["ApiImpactFieldSelector"]
	if strings.Contains(strings.Join(selector.Required, ","), "pointer") || strings.Contains(strings.Join(selector.Required, ","), "name") {
		t.Error("empty whole-body pointer and absent parameter name must remain optional")
	}
}

package mcp

import (
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/guide"
)

func TestRuntimeGuideSDKOwnersAndDiscovery(t *testing.T) {
	server, _ := newResourcesTestServer(t, resourcesTestConfig(t))
	for _, topic := range []string{"backend-inspect", "backend-flow-reference", "backend-analysis", "backend-editor-projections", "backend-events"} {
		owner, ok := guide.WorkflowForTopic(topic)
		if !ok {
			t.Fatalf("new standalone topic %s has no advertised owner", topic)
		}
		raw, message := callTool(t, server, "get_guide", `{"topic":"`+topic+`","guideSetId":"`+owner.GuideSetID+`"}`)
		if message != "" {
			t.Fatal(message)
		}
		var out GetGuideOutput
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		if out.WorkflowID != "mocker-backend-inspect" || out.WorkflowVersion != "7" || out.ManifestHash != owner.ManifestHash || out.GuideSetID != owner.GuideSetID || out.ContentHash != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(out.Markdown))) {
			t.Fatalf("SDK cannot verify %s against the selected inspect owner: %#v", topic, out)
		}
		if !slices.Contains(out.Topics, topic) || strings.HasPrefix(out.Markdown, "---") {
			t.Fatalf("SDK topic %s is missing or carries loader frontmatter", topic)
		}
	}
	response := doMCP(t, newTestEndpoint(t).Handler(), `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{"Authorization": "Bearer " + testKey})
	for _, topic := range []string{"backend-inspect", "backend-flow-reference", "backend-analysis", "backend-editor-projections", "backend-events"} {
		if !strings.Contains(response.Body.String(), topic) {
			t.Fatalf("tool discovery cannot locate %s", topic)
		}
	}
}

// A pinned local old/new identity cannot silently serve today's procedure,
// even though get_guide and all workflow tool names are still identical.
func TestRuntimeLeafOlderNewerSetRefusalAndCurrentFallback(t *testing.T) {
	for _, topic := range []string{"backend-import", "backend-database", "backend-inspect"} {
		for _, set := range []string{
			"sha256:2d798d65181489ad1c8c08cea728fd9da21f41c97ba295a5a1af742973b16cb1",
			"sha256:54ad47e9cec57296780d3ef881fa90d192c4bb467fcdcde124bd7df8692776de",
			"sha256:0000000000000000000000000000000000000000000000000000000000000000",
		} {
			calls := &recordingCaller{status: 200, body: []byte(`{}`)}
			_, message := callTool(t, calls, "get_guide", `{"topic":"`+topic+`","guideSetId":"`+set+`"}`)
			if !strings.Contains(message, "unknown guide set") || calls.method != "" {
				t.Fatalf("incompatible leaf %s silently substituted or mutated: %s", topic, message)
			}
			owner, ok := guide.WorkflowForTopic(topic)
			if !ok {
				t.Fatalf("compatible advertised fallback missing for %s", topic)
			}
			_, out, err := handleGetGuide(t.Context(), nil, GetGuideInput{Topic: topic, GuideSetID: owner.GuideSetID})
			if err != nil || out.WorkflowID != owner.WorkflowID || out.WorkflowVersion != owner.WorkflowVersion || out.ManifestHash != owner.ManifestHash {
				t.Fatalf("compatible fallback left its complete owner identity: %#v %v", out, err)
			}
		}
	}
}

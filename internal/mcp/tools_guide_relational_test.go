package mcp

import (
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/guide"
)

func TestRelationalGuideTopicDiscovery(t *testing.T) {
	t.Parallel()
	response := describedToolsList(t)
	var envelope struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				Annotations struct {
					ReadOnlyHint   bool `json:"readOnlyHint"`
					IdempotentHint bool `json:"idempotentHint"`
				} `json:"annotations"`
				InputSchema struct {
					Properties map[string]struct {
						Description string `json:"description"`
					} `json:"properties"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	for _, tool := range envelope.Result.Tools {
		if tool.Name != "get_guide" {
			continue
		}
		for _, topic := range []string{"backend-database", "backend-database-reference", "backend-profile-go-sql", "backend-sync", "backend-change-proposals", "backend-annotations"} {
			if !strings.Contains(tool.Description, topic) || !strings.Contains(tool.InputSchema.Properties["topic"].Description, topic) {
				t.Errorf("guide discovery omits %s", topic)
			}
		}
		if !tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint {
			t.Error("guide discovery lost its read-only/idempotent annotations")
		}
		return
	}
	t.Fatal("get_guide discovery tool missing")
}

// A database leaf must verify shared import-owned topics against their actual
// owner advertised by the same server, without starting an import mutation.
func TestCrossOwnerRelationalGuideSDKUsesAdvertisedPinnedSet(t *testing.T) {
	t.Parallel()
	server, _ := newResourcesTestServer(t, resourcesTestConfig(t))
	raw, message := callTool(t, server, "get_backend_capabilities", `{}`)
	if message != "" {
		t.Fatal(message)
	}
	var capabilities struct {
		ModelSchemaVersions []string         `json:"modelSchemaVersions"`
		ViewSchemaVersions  []string         `json:"viewSchemaVersions"`
		Features            []string         `json:"features"`
		WorkflowVersions    []guide.Workflow `json:"workflowVersions"`
	}
	if err := json.Unmarshal(raw, &capabilities); err != nil {
		t.Fatal(err)
	}
	// Discovery advertises exactly the guide's backend owners. Compared by
	// ID against guide.BackendWorkflows rather than a literal count, which
	// stayed at six while replay and verify joined and failed every run.
	want := map[string]bool{}
	for _, w := range guide.BackendWorkflows() {
		want[w.WorkflowID] = true
	}
	got := map[string]bool{}
	for _, w := range capabilities.WorkflowVersions {
		got[w.WorkflowID] = true
	}
	if len(want) == 0 || !maps.Equal(got, want) {
		t.Fatalf("backend discovery owners = %v; want guide.BackendWorkflows %v", got, want)
	}
	for _, workflow := range capabilities.WorkflowVersions {
		for _, required := range workflow.RequiredModelSchemaVersions {
			if !slices.Contains(capabilities.ModelSchemaVersions, required) {
				t.Errorf("workflow %s requires unserved schema %s", workflow.WorkflowID, required)
			}
		}
		for _, required := range workflow.RequiredViewSchemaVersions {
			if !slices.Contains(capabilities.ViewSchemaVersions, required) {
				t.Errorf("workflow %s requires unserved view %s", workflow.WorkflowID, required)
			}
		}
		for _, required := range workflow.RequiredCapabilities {
			if !slices.Contains(capabilities.Features, required) {
				t.Errorf("workflow %s requires unserved capability %s", workflow.WorkflowID, required)
			}
		}
	}
	for _, item := range []struct {
		topic, owner, version string
	}{
		{topic: "backend-database", owner: "mocker-backend-database", version: "7"},
		{topic: "backend-database-reference", owner: "mocker-backend-database", version: "7"},
		{topic: "backend-model", owner: "mocker-backend-import", version: "8"},
		{topic: "backend-recovery", owner: "mocker-backend-import", version: "8"},
		{topic: "backend-profile-go-sql", owner: "mocker-backend-import", version: "8"},
	} {
		t.Run(item.topic, func(t *testing.T) {
			ownerIndex := slices.IndexFunc(capabilities.WorkflowVersions, func(w guide.Workflow) bool { return w.WorkflowID == item.owner })
			if ownerIndex < 0 {
				t.Fatalf("owner %s absent from discovery", item.owner)
			}
			owner := capabilities.WorkflowVersions[ownerIndex]
			raw, message := callTool(t, server, "get_guide", `{"topic":"`+item.topic+`","guideSetId":"`+owner.GuideSetID+`"}`)
			if message != "" {
				t.Fatal(message)
			}
			var out GetGuideOutput
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatal(err)
			}
			if out.WorkflowID != item.owner || out.WorkflowVersion != item.version {
				t.Fatalf("served owner = %s v%s; want %s v%s", out.WorkflowID, out.WorkflowVersion, item.owner, item.version)
			}
			if out.GuideSetID != owner.GuideSetID || out.ManifestHash != owner.ManifestHash {
				t.Fatal("shared topic left the pinned advertised set")
			}
			index := slices.IndexFunc(owner.Topics, func(entry guide.TopicMetadata) bool { return entry.Topic == item.topic })
			if index < 0 || out.ContentHash != owner.Topics[index].ContentHash {
				t.Fatal("served topic cannot be verified against its actual advertised owner")
			}
			if out.ContentHash != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(out.Markdown))) {
				t.Fatal("served topic body does not match its advertised hash")
			}
			if len(out.Topics) != len(guide.Topics()) || !slices.Contains(out.Topics, item.topic) || !strings.HasPrefix(out.Markdown, "# ") {
				t.Fatal("SDK served body/discovery is incomplete or carries skill frontmatter")
			}
		})
	}
}

func TestRelationalGetGuideNeverCallsAdminOrSubstitutesObsoleteSet(t *testing.T) {
	t.Parallel()
	for _, topic := range []string{"backend-database", "backend-database-reference", "backend-profile-go-sql"} {
		t.Run(topic, func(t *testing.T) {
			t.Parallel()
			calls := &recordingCaller{status: 200, body: []byte(`{}`)}
			_, message := callTool(t, calls, "get_guide", `{"topic":"`+topic+`","guideSetId":"`+guide.CurrentGuideSetID()+`"}`)
			if message != "" || calls.method != "" {
				t.Fatalf("guide should be static without an admin call: method=%s message=%s", calls.method, message)
			}
			const b04Set = "sha256:bed6895643e95b8de9162f074a8d52bb1dfebcfc28c0e4a4737d379a39d32412"
			_, message = callTool(t, calls, "get_guide", `{"topic":"`+topic+`","guideSetId":"`+b04Set+`"}`)
			if !strings.Contains(message, "unknown guide set") || calls.method != "" {
				t.Fatalf("unsupported pinned guide substituted or called admin: method=%s message=%s", calls.method, message)
			}
		})
	}
}

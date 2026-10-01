package guide

import (
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Missing registration or an incorrect owner would make a leaf-only client
// unable to load and verify its next reference from the selected guide set.
func TestRelationalGuideTopicsHaveServedOwnerAndBody(t *testing.T) {
	t.Parallel()
	for _, item := range []struct {
		topic, owner, version string
	}{
		{topic: "backend-database", owner: "mocker-backend-database", version: "3"},
		{topic: "backend-database-reference", owner: "mocker-backend-database", version: "3"},
		{topic: "backend-profile-go-sql", owner: "mocker-backend-import", version: "4"},
	} {
		t.Run(item.topic, func(t *testing.T) {
			t.Parallel()
			if !slices.Contains(Topics(), item.topic) {
				t.Errorf("served topic discovery omits %q", item.topic)
			}
			body, ok := Topic(item.topic)
			if !ok || !strings.HasPrefix(body, "# ") {
				t.Errorf("topic %q has no served Markdown body without skill frontmatter", item.topic)
			}
			owner, ok := WorkflowForTopic(item.topic)
			if !ok {
				t.Fatalf("topic %q has no selectable workflow owner", item.topic)
			}
			if owner.WorkflowID != item.owner || owner.WorkflowVersion != item.version {
				t.Fatalf(
					"topic %q owner = %s v%s; want %s v%s",
					item.topic,
					owner.WorkflowID,
					owner.WorkflowVersion,
					item.owner,
					item.version,
				)
			}
			if owner.GuideSetID != CurrentGuideSetID() || owner.ManifestHash != CurrentGuideSetID() {
				t.Fatalf("topic %q owner is outside the selected global guide set", item.topic)
			}
			index := slices.IndexFunc(owner.Topics, func(entry TopicMetadata) bool { return entry.Topic == item.topic })
			if index < 0 || owner.Topics[index].ContentHash != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body))) {
				t.Fatalf("topic %q served body cannot be verified against its owner", item.topic)
			}
		})
	}
}

func TestRelationalGuideWorkflowRequirements(t *testing.T) {
	t.Parallel()
	workflows := BackendWorkflows()
	if len(workflows) != 4 {
		t.Errorf("backend discovery offers %d workflows; project, import and database are required", len(workflows))
	}
	for _, item := range []struct {
		entrypoint, owner, version string
		schemas, capabilities      []string
	}{
		{
			entrypoint: "backend-import", owner: "mocker-backend-import", version: "4",
			schemas: []string{"1", "2", "3"},
			capabilities: []string{
				"backend-projects", "backend-revisions", "backend-graph-query", "backend-source-import",
				"backend-source-reconcile", "backend-revision-compare", "backend-relational-import",
				"backend-database-query", "backend-database-er", "backend-runtime-flow-import", "backend-flow-query", "backend-data-access-query",
			},
		},
		{
			entrypoint: "backend-database", owner: "mocker-backend-database", version: "3",
			schemas: []string{"2", "3"},
			capabilities: []string{
				"backend-projects", "backend-revisions", "backend-graph-query", "backend-database-query", "backend-database-er", "backend-db-proposals", "backend-db-typed-edits", "backend-flow-query", "backend-data-access-query",
			},
		},
	} {
		t.Run(item.entrypoint, func(t *testing.T) {
			t.Parallel()
			index := slices.IndexFunc(workflows, func(w Workflow) bool { return w.WorkflowID == item.owner })
			if index < 0 {
				t.Fatalf("backend discovery omits %s", item.owner)
			}
			w := workflows[index]
			if w.WorkflowVersion != item.version || w.Entrypoint != item.entrypoint {
				t.Errorf(
					"workflow identity = %s v%s at %s; want %s v%s at %s",
					w.WorkflowID,
					w.WorkflowVersion,
					w.Entrypoint,
					item.owner,
					item.version,
					item.entrypoint,
				)
			}
			if !slices.Equal(w.RequiredModelSchemaVersions, item.schemas) || !slices.Equal(w.RequiredCapabilities, item.capabilities) {
				t.Errorf(
					"%s requirements = schemas %v capabilities %v; want schemas %v capabilities %v",
					item.owner,
					w.RequiredModelSchemaVersions,
					w.RequiredCapabilities,
					item.schemas,
					item.capabilities,
				)
			}
		})
	}
}

func TestGlobalGuideSetHasOneOwnerForEveryServedTopic(t *testing.T) {
	t.Parallel()
	raw, ok := Raw("manifest.json")
	if !ok {
		t.Fatal("guide manifest unavailable")
	}
	var manifest guideManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Workflows) != 5 {
		t.Fatalf("global guide set has %d owners; want routing/project/import/database/inspect", len(manifest.Workflows))
	}
	want := map[string]string{
		"overview": "mocker-routing", "tools": "mocker-routing", "shapes": "mocker-routing",
		"cookbook": "mocker-routing", "http": "mocker-routing", "design": "mocker-routing", "functions": "mocker-routing",
		"backend-overview": "mocker-backend-project",
		"backend-import":   "mocker-backend-import", "backend-model": "mocker-backend-import",
		"backend-import-protocol": "mocker-backend-import", "backend-recovery": "mocker-backend-import",
		"backend-examples": "mocker-backend-import", "backend-profile-go-sql": "mocker-backend-import",
		"backend-database": "mocker-backend-database", "backend-database-reference": "mocker-backend-database",
		"backend-inspect": "mocker-backend-inspect", "backend-flow-reference": "mocker-backend-inspect", "backend-analysis": "mocker-backend-inspect",
	}
	seen := make(map[string]string)
	for _, owner := range manifest.Workflows {
		if owner.GuideSetID != manifest.GuideSetID || owner.ManifestHash != manifest.ManifestHash {
			t.Errorf("owner %s has a different global guide identity", owner.WorkflowID)
		}
		for _, entry := range owner.Topics {
			if previous, duplicate := seen[entry.Topic]; duplicate {
				t.Errorf("topic %s has multiple owners: %s and %s", entry.Topic, previous, owner.WorkflowID)
			}
			seen[entry.Topic] = owner.WorkflowID
			if want[entry.Topic] != owner.WorkflowID {
				t.Errorf("topic %s owner = %s; want %s", entry.Topic, owner.WorkflowID, want[entry.Topic])
			}
			body, ok := Topic(entry.Topic)
			if !ok || entry.ContentHash != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body))) {
				t.Errorf("owner %s advertises an unavailable or changed topic %s", owner.WorkflowID, entry.Topic)
			}
		}
	}
	for topic := range want {
		if _, ok := seen[topic]; !ok {
			t.Errorf("global set omits owner for %s", topic)
		}
	}
}

func TestRelationalTopicOwnersReturnDefensiveRequirements(t *testing.T) {
	t.Parallel()
	for _, topic := range []string{"backend-database", "backend-database-reference", "backend-profile-go-sql"} {
		t.Run(topic, func(t *testing.T) {
			t.Parallel()
			original, ok := WorkflowForTopic(topic)
			if !ok {
				t.Fatalf("topic %s owner unavailable", topic)
			}
			changed, _ := WorkflowForTopic(topic)
			changed.RequiredModelSchemaVersions[0] = "corrupted"
			changed.RequiredCapabilities[0] = "corrupted"
			changed.Topics[0].ContentHash = "corrupted"
			after, _ := WorkflowForTopic(topic)
			if !slices.Equal(original.RequiredModelSchemaVersions, after.RequiredModelSchemaVersions) ||
				!slices.Equal(original.RequiredCapabilities, after.RequiredCapabilities) ||
				!slices.Equal(original.Topics, after.Topics) {
				t.Fatal("topic owner requirements or content hashes alias internal manifest state")
			}
			for _, workflow := range BackendWorkflows() {
				if workflow.WorkflowID == original.WorkflowID && (!slices.Equal(workflow.RequiredCapabilities, original.RequiredCapabilities) ||
					!slices.Equal(workflow.RequiredModelSchemaVersions, original.RequiredModelSchemaVersions) ||
					!slices.Equal(workflow.Topics, original.Topics)) {
					t.Fatal("topic owner mutation corrupted backend capability discovery")
				}
			}
		})
	}
}

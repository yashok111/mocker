package guide

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestB41GuideOwnersAndContracts(t *testing.T) {
	t.Parallel()
	for _, item := range []struct {
		topic, owner, version       string
		models, views, capabilities []string
	}{
		{"backend-sync", "mocker-backend-sync", "2", []string{"1", "5", "6"}, []string{"import-candidate-v1"}, []string{"backend-projects", "backend-revisions", "backend-graph-query", "backend-source-import", "backend-source-sync", "backend-source-incremental-sync", "backend-source-assertions", "backend-import-candidate", "backend-representations", "backend-analysis-jobs", "backend-analysis-diff", "backend-analysis-impact"}},
		{"backend-change-proposals", "mocker-backend-change", "4", []string{"5", "6"}, []string{"proposal-graph-v1", "backend-diagram-v1", "diagram-view-v1"}, []string{"backend-projects", "backend-revisions", "backend-graph-query", "backend-change-proposals", "backend-change-typed-edits", "backend-source-assertions", "backend-representations", "backend-analysis-jobs", "backend-analysis-diff", "backend-analysis-impact", "backend-change-rebase", "backend-change-ready", "backend-change-package", "backend-conformance", "backend-endpoint-review", "backend-change-implemented", "backend-change-archive", "backend-change-unarchive", "backend-diagrams", "backend-architecture", "backend-diagram-views"}},
		{"backend-annotations", "mocker-backend-project", "2", []string{"1"}, nil, []string{"backend-projects", "backend-project-metadata", "backend-revisions", "backend-annotations"}},
	} {
		t.Run(item.topic, func(t *testing.T) {
			owner, ok := WorkflowForTopic(item.topic)
			if !ok || owner.WorkflowID != item.owner || owner.WorkflowVersion != item.version {
				t.Fatalf("topic owner unavailable: %s %+v", item.topic, owner)
			}
			if !slices.Equal(owner.RequiredModelSchemaVersions, item.models) || !slices.Equal(owner.RequiredViewSchemaVersions, item.views) || !slices.Equal(owner.RequiredCapabilities, item.capabilities) {
				t.Fatalf("topic has incomplete requirements: %s %+v", item.topic, owner)
			}
			body, ok := Topic(item.topic)
			index := slices.IndexFunc(owner.Topics, func(entry TopicMetadata) bool { return entry.Topic == item.topic })
			if !ok || !strings.HasPrefix(body, "# ") || index < 0 || owner.Topics[index].ContentHash != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body))) || owner.GuideSetID != CurrentGuideSetID() || owner.ManifestHash != CurrentGuideSetID() {
				t.Fatalf("topic cannot be verified in its exact set: %s", item.topic)
			}
		})
	}
}

func TestB41InitializeInstructionsUseCurrentOwners(t *testing.T) {
	t.Parallel()
	text := Instructions()
	owners := map[string]Workflow{}
	for _, owner := range currentManifest.Workflows {
		name := strings.TrimPrefix(strings.TrimPrefix(owner.WorkflowID, "mocker-"), "backend-")
		owners[name] = owner
	}
	labels := regexp.MustCompile(`(?i)\b(import|inspect|database|project|sync|change|routing)(?:\s+v)?([0-9]+)\b`).FindAllStringSubmatch(text, -1)
	for _, label := range labels {
		owner, ok := owners[strings.ToLower(label[1])]
		if !ok || owner.WorkflowVersion != label[2] {
			t.Errorf("initialize owner %s is not current: %s v%s", label[0], owner.WorkflowID, owner.WorkflowVersion)
		}
	}
	for _, topic := range []string{"backend-events", "backend-import", "backend-sync", "backend-change-proposals"} {
		owner, ok := WorkflowForTopic(topic)
		label := strings.TrimPrefix(owner.WorkflowID, "mocker-backend-") + owner.WorkflowVersion + "/" + topic
		if !ok || !strings.Contains(text, label) {
			t.Errorf("initialize does not route %s through its exact current owner %s", topic, label)
		}
	}
}

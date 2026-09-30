package guide

import (
	"crypto/sha256"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// A leaf-only installation must work without a neighboring root package, and
// its protocol references must be available from the selected served set.
func TestStandaloneImportPackageOwnsServedEntrypoint(t *testing.T) {
	leaf, err := os.ReadFile("../../skills/mocker-backend-import/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := os.ReadFile("../../skills/mocker/references/backend/import.md")
	if err != nil {
		t.Fatal(err)
	}
	embedded, ok := Raw("backend-import.md")
	if !ok || string(leaf) != string(legacy) || string(leaf) != embedded {
		t.Fatal("standalone, compatibility and served import procedures diverge")
	}
	root := t.TempDir()
	installed := filepath.Join(root, "mocker-backend-import")
	if err := os.MkdirAll(installed, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installed, "SKILL.md"), leaf, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "mocker")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("isolated import package unexpectedly has a neighboring root package")
	}
	for _, topic := range []string{"backend-import", "backend-model", "backend-import-protocol", "backend-recovery", "backend-examples"} {
		t.Run(topic, func(t *testing.T) {
			workflow, ok := WorkflowForTopic(topic)
			if !ok || workflow.WorkflowID != "mocker-backend-import" || workflow.WorkflowVersion != "2" || workflow.GuideSetID != CurrentGuideSetID() {
				t.Fatalf("standalone reference has no compatible served identity: %s", topic)
			}
			markdown, ok := Topic(topic)
			if !ok || strings.TrimSpace(markdown) == "" {
				t.Fatalf("unavailable standalone reference: %s", topic)
			}
			index := slices.IndexFunc(workflow.Topics, func(item TopicMetadata) bool { return item.Topic == topic })
			if index < 0 || workflow.Topics[index].ContentHash != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(markdown))) {
				t.Fatalf("standalone reference cannot be verified against its selected manifest: %s", topic)
			}
		})
	}
}

// Invalid skill metadata prevents a host from selecting the package before a
// tool call. Decode the actual installed format, including JSON-list strings.
func TestStandaloneSkillMetadataAndIsolation(t *testing.T) {
	leaf, err := os.ReadFile("../../skills/mocker-backend-import/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	front, body, ok := strings.Cut(strings.TrimPrefix(string(leaf), "---\n"), "\n---\n")
	if !ok {
		t.Fatal("standalone skill has no frontmatter")
	}
	var document struct {
		Name        string            `yaml:"name"`
		Description string            `yaml:"description"`
		Metadata    map[string]string `yaml:"metadata"`
	}
	if err := yaml.Unmarshal([]byte(front), &document); err != nil {
		t.Fatal(err)
	}
	if document.Name != "mocker-backend-import" || strings.TrimSpace(document.Description) == "" {
		t.Fatal("standalone package cannot be selected by its name and description")
	}
	workflow, ok := WorkflowForTopic("backend-import")
	if !ok {
		t.Fatal("standalone workflow is not served")
	}
	for key, want := range map[string]string{
		"workflowId":      "mocker-backend-import",
		"workflowVersion": "2",
		"guideSetId":      workflow.GuideSetID,
		"manifestHash":    workflow.ManifestHash,
	} {
		if document.Metadata[key] != want {
			t.Errorf("standalone metadata %s = %q; want %q", key, document.Metadata[key], want)
		}
	}
	for key, want := range map[string][]string{
		"requiredModelSchemaVersions": {"1"},
		"requiredCapabilities":        {"backend-projects", "backend-revisions", "backend-graph-query", "backend-source-import", "backend-source-reconcile", "backend-revision-compare"},
	} {
		var got []string
		if err := json.Unmarshal([]byte(document.Metadata[key]), &got); err != nil {
			t.Fatalf("standalone metadata %s is not a JSON-list string: %v", key, err)
		}
		if !slices.Equal(got, want) {
			t.Errorf("standalone %s = %v; want %v", key, got, want)
		}
	}
	for _, link := range regexp.MustCompile(`\]\(([^)]+)\)`).FindAllStringSubmatch(body, -1) {
		target := strings.Trim(link[1], "<>")
		if strings.HasPrefix(target, "/") || strings.Contains(target, "../") || strings.HasPrefix(target, "file:") {
			t.Errorf("standalone skill requires an external filesystem link: %s", target)
		}
	}
}

func TestSkillEntrypointContextBudgets(t *testing.T) {
	for _, item := range []struct {
		path string
		max  int
	}{
		{"../../skills/mocker/SKILL.md", 250},
		{"../../skills/mocker-backend-import/SKILL.md", 200},
	} {
		data, err := os.ReadFile(item.path)
		if err != nil {
			t.Fatal(err)
		}
		if lines := strings.Count(strings.TrimRight(string(data), "\n"), "\n") + 1; lines > item.max {
			t.Errorf("%s contains %d lines; move conditional detail into server reference topics (budget %d)", item.path, lines, item.max)
		}
	}
}

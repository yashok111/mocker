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
	for _, topic := range []string{"backend-import", "backend-model", "backend-import-protocol", "backend-recovery", "backend-examples", "backend-profile-go-sql"} {
		t.Run(topic, func(t *testing.T) {
			workflow, ok := WorkflowForTopic(topic)
			if !ok || workflow.WorkflowID != "mocker-backend-import" || workflow.WorkflowVersion != "7" || workflow.GuideSetID != CurrentGuideSetID() {
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
		"workflowVersion": "7",
		"guideSetId":      workflow.GuideSetID,
		"manifestHash":    workflow.ManifestHash,
	} {
		if document.Metadata[key] != want {
			t.Errorf("standalone metadata %s = %q; want %q", key, document.Metadata[key], want)
		}
	}
	for key, want := range map[string][]string{
		"requiredModelSchemaVersions": {"1", "2", "3", "4", "5", "6"},
		"requiredCapabilities": {
			"backend-projects", "backend-revisions", "backend-graph-query", "backend-source-import",
			"backend-source-reconcile", "backend-revision-compare", "backend-relational-import",
			"backend-database-query", "backend-database-er", "backend-runtime-flow-import", "backend-flow-query", "backend-data-access-query", "backend-field-lineage-import", "backend-field-lineage-query", "backend-events-import", "backend-events-query", "backend-source-sync", "backend-representations",
		},
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

func TestStandaloneDatabasePackageOwnsServedEntrypoint(t *testing.T) {
	t.Parallel()
	leaf, err := os.ReadFile("../../skills/mocker-backend-database/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	alias, err := os.ReadFile("../../skills/mocker/references/backend/database-workflow.md")
	if err != nil {
		t.Fatal(err)
	}
	embedded, ok := Raw("backend-database.md")
	if !ok || string(alias) != string(leaf) || embedded != string(leaf) {
		t.Fatal("database leaf, root compatibility alias and embedded entrypoint diverge")
	}
	reference, err := os.ReadFile("../../skills/mocker/references/backend/database.md")
	if err != nil {
		t.Fatal(err)
	}
	servedReference, ok := Topic("backend-database-reference")
	if !ok || servedReference != string(reference) || servedReference == stripFrontmatter(string(leaf)) {
		t.Fatal("database shared reference was replaced with the workflow entrypoint alias")
	}
	root := t.TempDir()
	installed := filepath.Join(root, "mocker-backend-database")
	if err := os.CopyFS(installed, os.DirFS("../../skills/mocker-backend-database")); err != nil {
		t.Fatal(err)
	}
	for _, missing := range []string{"mocker", "mocker-backend-import"} {
		if _, err := os.Stat(filepath.Join(root, missing)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("isolated database leaf unexpectedly requires neighboring %s", missing)
		}
	}
	readInstalledGuideIdentity(t, filepath.Join(installed, "SKILL.md"), "backend-database")
	for _, item := range []struct {
		topic, owner, version string
	}{
		{topic: "backend-database-reference", owner: "mocker-backend-database", version: "7"},
		{topic: "backend-model", owner: "mocker-backend-import", version: "7"},
		{topic: "backend-recovery", owner: "mocker-backend-import", version: "7"},
	} {
		workflow, ok := WorkflowForTopic(item.topic)
		if !ok || workflow.WorkflowID != item.owner || workflow.WorkflowVersion != item.version {
			t.Fatalf("database leaf reference %s has no compatible actual owner", item.topic)
		}
		if workflow.GuideSetID != CurrentGuideSetID() || workflow.ManifestHash != CurrentGuideSetID() {
			t.Fatalf("database leaf reference %s left the selected global set", item.topic)
		}
		body, ok := Topic(item.topic)
		index := slices.IndexFunc(workflow.Topics, func(entry TopicMetadata) bool { return entry.Topic == item.topic })
		if !ok || index < 0 || workflow.Topics[index].ContentHash != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body))) {
			t.Fatalf("database leaf reference %s cannot be loaded and verified without a root package", item.topic)
		}
	}
}

// These are physical installations. Local entrypoint identities are decoded
// from the installed bytes and shared references are verified against their
// actual owner in the server set, including when no package is installed.
func TestGuideInstallationModesKeepOnePinnedSet(t *testing.T) {
	t.Parallel()
	for _, item := range []struct {
		name     string
		packages []string
	}{
		{name: "root-only", packages: []string{"mocker"}},
		{name: "import-only", packages: []string{"mocker-backend-import"}},
		{name: "database-only", packages: []string{"mocker-backend-database"}},
		{name: "inspect-only", packages: []string{"mocker-backend-inspect"}},
		{name: "sync-only", packages: []string{"mocker-backend-sync"}},
		{name: "change-only", packages: []string{"mocker-backend-change"}},
		{name: "bundle", packages: []string{"mocker", "mocker-backend-import", "mocker-backend-database", "mocker-backend-inspect", "mocker-backend-sync", "mocker-backend-change"}},
		{name: "server-only", packages: []string{}},
	} {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for _, pkg := range item.packages {
				if err := os.CopyFS(filepath.Join(root, pkg), os.DirFS(filepath.Join("../../skills", pkg))); err != nil {
					t.Fatal(err)
				}
			}
			for _, entry := range []struct {
				topic, pkg, path string
			}{
				{topic: "overview", pkg: "mocker", path: "SKILL.md"},
				{topic: "backend-import", pkg: "mocker-backend-import", path: "SKILL.md"},
				{topic: "backend-database", pkg: "mocker-backend-database", path: "SKILL.md"},
				{topic: "backend-inspect", pkg: "mocker-backend-inspect", path: "SKILL.md"},
				{topic: "backend-sync", pkg: "mocker-backend-sync", path: "SKILL.md"},
				{topic: "backend-change-proposals", pkg: "mocker-backend-change", path: "SKILL.md"},
			} {
				path := filepath.Join(root, entry.pkg, entry.path)
				if !slices.Contains(item.packages, entry.pkg) && slices.Contains(item.packages, "mocker") {
					switch entry.topic {
					case "backend-import":
						path = filepath.Join(root, "mocker/references/backend/import.md")
					case "backend-inspect":
						path = filepath.Join(root, "mocker/references/backend/inspect.md")
					case "backend-database":
						path = filepath.Join(root, "mocker/references/backend/database-workflow.md")
					case "backend-sync":
						path = filepath.Join(root, "mocker/references/backend/sync.md")
					case "backend-change-proposals":
						path = filepath.Join(root, "mocker/references/backend/change-proposals.md")
					}
				}
				if slices.Contains(item.packages, entry.pkg) || slices.Contains(item.packages, "mocker") {
					readInstalledGuideIdentity(t, path, entry.topic)
				} else if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("isolated mode %s unexpectedly contains neighboring %s", item.name, entry.pkg)
				}
			}
			for _, topic := range []string{
				"overview", "backend-import", "backend-database", "backend-database-reference",
				"backend-profile-go-sql", "backend-model", "backend-recovery",
				"backend-inspect", "backend-flow-reference", "backend-analysis",
				"backend-sync", "backend-change-proposals", "backend-annotations",
				"backend-change-rebase", "backend-analysis-jobs",
			} {
				owner, ok := WorkflowForTopic(topic)
				if !ok || owner.GuideSetID != CurrentGuideSetID() || owner.ManifestHash != CurrentGuideSetID() {
					t.Fatalf("%s cannot resolve the selected topic owner for %s", item.name, topic)
				}
				body, ok := Topic(topic)
				index := slices.IndexFunc(owner.Topics, func(entry TopicMetadata) bool { return entry.Topic == topic })
				if !ok || index < 0 || owner.Topics[index].ContentHash != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body))) {
					t.Fatalf("%s cannot load the verified topic %s", item.name, topic)
				}
			}
		})
	}
}

func readInstalledGuideIdentity(t *testing.T, path, topic string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	front, body, ok := strings.Cut(strings.TrimPrefix(string(raw), "---\n"), "\n---\n")
	if !ok {
		t.Fatalf("installed skill %s has no frontmatter", path)
	}
	var document struct {
		Name        string            `yaml:"name"`
		Description string            `yaml:"description"`
		Metadata    map[string]string `yaml:"metadata"`
	}
	if err := yaml.Unmarshal([]byte(front), &document); err != nil {
		t.Fatal(err)
	}
	owner, ok := WorkflowForTopic(topic)
	if !ok || document.Name == "" || strings.TrimSpace(document.Description) == "" {
		t.Fatalf("installed topic %s cannot be selected by its name, description and owner", topic)
	}
	for key, want := range map[string]string{
		"workflowId": owner.WorkflowID, "workflowVersion": owner.WorkflowVersion,
		"guideSetId": owner.GuideSetID, "manifestHash": owner.ManifestHash,
	} {
		if document.Metadata[key] != want {
			t.Errorf("installed topic %s metadata %s = %q; want %q", topic, key, document.Metadata[key], want)
		}
	}
	for key, want := range map[string][]string{
		"requiredModelSchemaVersions": owner.RequiredModelSchemaVersions,
		"requiredCapabilities":        owner.RequiredCapabilities,
	} {
		var got []string
		if err := json.Unmarshal([]byte(document.Metadata[key]), &got); err != nil {
			t.Fatalf("installed topic %s metadata %s is not a JSON-list string: %v", topic, key, err)
		}
		if !slices.Equal(got, want) {
			t.Errorf("installed topic %s requirements %s = %v; want %v", topic, key, got, want)
		}
	}
	served, ok := Topic(topic)
	if !ok || strings.TrimLeft(body, "\n") != served {
		t.Fatalf("installed topic %s differs from the selected served procedure", topic)
	}
	if document.Name != "mocker" {
		for _, link := range regexp.MustCompile(`\]\(([^)]+)\)`).FindAllStringSubmatch(body, -1) {
			target := strings.Trim(link[1], "<>")
			if strings.HasPrefix(target, "/") || strings.Contains(target, "../") || strings.HasPrefix(target, "file:") {
				t.Errorf("installed leaf %s requires an external filesystem link: %s", topic, target)
			}
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
		{"../../skills/mocker-backend-database/SKILL.md", 200},
		{"../../skills/mocker-backend-inspect/SKILL.md", 200},
		{"../../skills/mocker-backend-sync/SKILL.md", 200},
		{"../../skills/mocker-backend-change/SKILL.md", 200},
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

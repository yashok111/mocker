package guide

import (
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestVersionedWorkflowContract(t *testing.T) {
	workflows := BackendWorkflows()
	if len(workflows) != 6 {
		t.Fatalf("workflows = %#v", workflows)
	}
	w := workflows[0]
	if w.WorkflowID != "mocker-backend-project" || w.WorkflowVersion != "2" || w.Entrypoint != "backend-overview" || w.GuideSetID != CurrentGuideSetID() || w.ManifestHash == "" {
		t.Fatalf("workflow = %#v", w)
	}
	if !slices.Equal(w.RequiredModelSchemaVersions, []string{"1"}) || !slices.Equal(w.RequiredCapabilities, []string{"backend-projects", "backend-project-metadata", "backend-revisions", "backend-annotations"}) {
		t.Fatalf("requirements = %#v", w)
	}
	for _, topic := range w.Topics {
		body, ok := Topic(topic.Topic)
		if !ok || topic.ContentHash != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body))) {
			t.Errorf("bad content hash: %#v", topic)
		}
	}
	workflows[0].RequiredCapabilities[0] = "corrupted"
	workflows[0].RequiredModelSchemaVersions[0] = "99"
	workflows[0].Topics[0].Topic = "corrupted"
	if BackendWorkflows()[0].RequiredCapabilities[0] != "backend-projects" || BackendWorkflows()[0].RequiredModelSchemaVersions[0] != "1" || BackendWorkflows()[0].Topics[0].Topic == "corrupted" {
		t.Fatal("workflow aliases internal state")
	}
}

func TestImportWorkflowUsesPinnedAvailableTopics(t *testing.T) {
	var selected Workflow
	for _, workflow := range BackendWorkflows() {
		if workflow.WorkflowID == "mocker-backend-import" {
			selected = workflow
		}
	}
	if selected.Entrypoint != "backend-import" || selected.WorkflowVersion != "7" {
		t.Fatalf("source import workflow unavailable: %#v", selected)
	}
	if selected.GuideSetID != CurrentGuideSetID() || selected.ManifestHash != CurrentGuideSetID() {
		t.Fatal("source import workflow cannot be pinned to the served guide set")
	}
	for _, required := range []string{"backend-projects", "backend-revisions", "backend-graph-query", "backend-source-import", "backend-source-reconcile", "backend-revision-compare", "backend-relational-import", "backend-database-query", "backend-database-er"} {
		if !slices.Contains(selected.RequiredCapabilities, required) {
			t.Errorf("import workflow does not require %s", required)
		}
	}
	if !slices.Equal(selected.RequiredModelSchemaVersions, []string{"1", "2", "3", "4", "5", "6"}) || len(selected.Topics) == 0 {
		t.Fatal("import workflow has no supported schema or procedure")
	}
	for _, topic := range selected.Topics {
		markdown, ok := Topic(topic.Topic)
		if !ok || topic.ContentHash != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(markdown))) {
			t.Errorf("import topic unavailable or changed: %#v", topic)
		}
	}
}

func TestManifestSourceParityAndDeterminism(t *testing.T) {
	cmd := exec.Command("python3", "../../scripts/guide-sync.py", "--check")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("guide manifest validation: %v\n%s", err, output)
	}
}

// The digest is recomputed independently from the stored manifest, so edits to
// either generated identity or its hash inputs are detected.
func TestManifestHashCanonicalForm(t *testing.T) {
	raw, ok := Raw("manifest.json")
	if !ok {
		t.Fatal("missing manifest")
	}
	var manifest map[string]any
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		t.Fatal(err)
	}
	advertised := manifest["manifestHash"]
	delete(manifest, "manifestHash")
	delete(manifest, "guideSetId")
	for _, value := range manifest["workflows"].([]any) {
		workflow := value.(map[string]any)
		delete(workflow, "guideSetId")
		delete(workflow, "manifestHash")
		delete(workflow, "topics")
	}
	canonical, err := json.Marshal(manifest, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	actual := fmt.Sprintf("sha256:%x", sha256.Sum256(canonical))
	if advertised != actual || CurrentGuideSetID() != actual {
		t.Fatalf("canonical hash = %s; advertised = %s", actual, advertised)
	}
}

func TestGuideSyncDetectsChangesAndGeneratesStableIdentity(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"scripts", "skills/mocker", "internal/guide"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0700); err != nil {
			t.Fatal(err)
		}
	}
	script, err := os.ReadFile("../../scripts/guide-sync.py")
	if err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(root, "scripts/guide-sync.py")
	if err := os.WriteFile(scriptPath, script, 0600); err != nil {
		t.Fatal(err)
	}
	declaration, err := os.ReadFile("../../skills/mocker/guide-sources.json")
	if err != nil {
		t.Fatal(err)
	}
	var sources guideSourceDeclarations
	if err := json.Unmarshal(declaration, &sources); err != nil {
		t.Fatal(err)
	}
	packages := map[string]bool{"mocker": true}
	for _, source := range sources.Sources {
		if source.Package != "" {
			packages[source.Package] = true
		}
		for _, copy := range source.Copies {
			packages[copy.Package] = true
		}
	}
	for name := range packages {
		if err := os.CopyFS(filepath.Join(root, "skills", name), os.DirFS(filepath.Join("../../skills", name))); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) ([]byte, error) {
		return exec.Command("python3", append([]string{scriptPath}, args...)...).CombinedOutput()
	}
	if output, err := run(); err != nil {
		t.Fatalf("generate: %v %s", err, output)
	}
	before, err := os.ReadFile(filepath.Join(root, "internal/guide/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	owner := filepath.Join(root, "skills/mocker/references/backend/overview.md")
	original, err := os.ReadFile(owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(owner, append(original, []byte("\nChanged procedure.\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := run("--check"); err == nil {
		t.Fatal("changed guide incorrectly passed --check")
	}
	if output, err := run(); err != nil {
		t.Fatalf("regenerate: %v %s", err, output)
	}
	after, err := os.ReadFile(filepath.Join(root, "internal/guide/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var oldManifest, newManifest guideManifest
	if err := json.Unmarshal(before, &oldManifest); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(after, &newManifest); err != nil {
		t.Fatal(err)
	}
	if oldManifest.GuideSetID == newManifest.GuideSetID {
		t.Fatal("content change retained identity")
	}
	if output, err := run(); err != nil {
		t.Fatalf("repeat: %v %s", err, output)
	}
	repeated, err := os.ReadFile(filepath.Join(root, "internal/guide/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(repeated) {
		t.Fatal("repeated generation changed the manifest")
	}
	if output, err := run("--check"); err != nil {
		t.Fatalf("check: %v %s", err, output)
	}
	current, err := os.ReadFile(owner)
	if err != nil {
		t.Fatal(err)
	}
	corrupted := strings.Replace(string(current), `workflowVersion: "2"`, `workflowVersion: "99"`, 1)
	if err := os.WriteFile(owner, []byte(corrupted), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := run(); err == nil || !strings.Contains(string(output), "incorrect workflowVersion") {
		t.Fatalf("inconsistent metadata accepted: %v %s", err, output)
	}
}

func TestSkillFrontmatterInstallationContract(t *testing.T) {
	allowed := map[string]bool{"name": true, "description": true, "license": true, "allowed-tools": true, "metadata": true}
	for _, topic := range []string{"overview", "backend-overview", "backend-import", "backend-database", "backend-inspect", "backend-sync", "backend-change-proposals"} {
		t.Run(topic, func(t *testing.T) {
			raw, ok := Raw(topic + ".md")
			if !ok {
				t.Fatal("missing entrypoint")
			}
			front, _, ok := strings.Cut(strings.TrimPrefix(raw, "---\n"), "\n---\n")
			if !ok {
				t.Fatal("missing frontmatter")
			}
			var document map[string]yaml.Node
			if err := yaml.Unmarshal([]byte(front), &document); err != nil {
				t.Fatal(err)
			}
			for key := range document {
				if !allowed[key] {
					t.Errorf("nonstandard top-level skill field %s", key)
				}
			}
			node, ok := document["metadata"]
			if !ok {
				t.Fatal("missing workflow metadata")
			}
			var metadata map[string]string
			if err := node.Decode(&metadata); err != nil {
				t.Fatalf("metadata must be string-to-string: %v", err)
			}
			workflow, ok := WorkflowForTopic(topic)
			if !ok {
				t.Fatal("missing workflow")
			}
			if metadata["workflowId"] != workflow.WorkflowID || metadata["workflowVersion"] != workflow.WorkflowVersion || metadata["guideSetId"] != workflow.GuideSetID || metadata["manifestHash"] != workflow.ManifestHash {
				t.Fatalf("entrypoint identity does not match manifest: %#v", metadata)
			}
			for key, want := range map[string][]string{"requiredModelSchemaVersions": workflow.RequiredModelSchemaVersions, "requiredCapabilities": workflow.RequiredCapabilities} {
				var got []string
				if err := json.Unmarshal([]byte(metadata[key]), &got); err != nil {
					t.Fatalf("%s must be a JSON list string: %v", key, err)
				}
				if !slices.Equal(got, want) {
					t.Errorf("%s = %v; want %v", key, got, want)
				}
			}
		})
	}
}

package guide

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestRuntimeGuideOwnersAndRequirements(t *testing.T) {
	for _, item := range []struct {
		topic, owner, version string
	}{
		{"backend-import", "mocker-backend-import", "4"},
		{"backend-database", "mocker-backend-database", "3"},
		{"backend-inspect", "mocker-backend-inspect", "1"},
		{"backend-flow-reference", "mocker-backend-inspect", "1"},
		{"backend-analysis", "mocker-backend-inspect", "1"},
	} {
		owner, ok := WorkflowForTopic(item.topic)
		if !ok || owner.WorkflowID != item.owner || owner.WorkflowVersion != item.version {
			t.Fatalf("topic %s owner = %#v, available=%v", item.topic, owner, ok)
		}
		if owner.GuideSetID != CurrentGuideSetID() || owner.ManifestHash != CurrentGuideSetID() || !slices.Contains(owner.RequiredModelSchemaVersions, "3") {
			t.Fatalf("topic %s is not in the schema3 pinned set", item.topic)
		}
		for _, capability := range []string{"backend-flow-query", "backend-data-access-query"} {
			if !slices.Contains(owner.RequiredCapabilities, capability) {
				t.Fatalf("%s can qualify without %s", item.owner, capability)
			}
		}
		body, ok := Topic(item.topic)
		index := slices.IndexFunc(owner.Topics, func(entry TopicMetadata) bool { return entry.Topic == item.topic })
		if !ok || index < 0 || owner.Topics[index].ContentHash != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body))) {
			t.Fatalf("topic %s cannot be verified against its actual owner", item.topic)
		}
	}
	importer, _ := WorkflowForTopic("backend-import")
	if !slices.Contains(importer.RequiredCapabilities, "backend-runtime-flow-import") {
		t.Fatal("runtime import can qualify without its profile capability")
	}
}

func TestStandaloneInspectInstallationAndPinnedDependencies(t *testing.T) {
	root := t.TempDir()
	installed := filepath.Join(root, "mocker-backend-inspect")
	if err := os.CopyFS(installed, os.DirFS("../../skills/mocker-backend-inspect")); err != nil {
		t.Fatal(err)
	}
	readInstalledGuideIdentity(t, filepath.Join(installed, "SKILL.md"), "backend-inspect")
	for _, packageName := range []string{"mocker", "mocker-backend-import", "mocker-backend-database"} {
		if _, err := os.Stat(filepath.Join(root, packageName)); !os.IsNotExist(err) {
			t.Fatalf("standalone inspect unexpectedly needs %s", packageName)
		}
	}
	leaf, err := os.ReadFile(filepath.Join(installed, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	alias, err := os.ReadFile("../../skills/mocker/references/backend/inspect.md")
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := Raw("backend-inspect.md")
	if !ok || string(leaf) != string(alias) || string(leaf) != raw {
		t.Fatal("inspect leaf, root alias and embedded procedure diverge")
	}
	for _, topic := range []string{"backend-flow-reference", "backend-analysis", "backend-model", "backend-recovery", "backend-import", "backend-database-reference"} {
		owner, ok := WorkflowForTopic(topic)
		if !ok || owner.GuideSetID != CurrentGuideSetID() || owner.ManifestHash != CurrentGuideSetID() {
			t.Fatalf("standalone dependency %s left the pinned global set", topic)
		}
	}
}

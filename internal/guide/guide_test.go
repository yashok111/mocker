package guide

import (
	"cmp"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type guideSourceDeclaration struct {
	Topic    string `json:"topic"`
	Package  string `json:"package"`
	Source   string `json:"source"`
	Embedded string `json:"embedded"`
	Copies   []struct {
		Package string `json:"package"`
		Path    string `json:"path"`
	} `json:"copies"`
}

type guideSourceDeclarations struct {
	Sources []guideSourceDeclaration `json:"sources"`
}

// Every source and compatibility alias follows the manifest's package owner,
// so new topics do not require a second manually maintained parity table.
func TestEmbeddedCopiesMatchTheSkill(t *testing.T) {
	t.Parallel()
	raw, ok := Raw("manifest.json")
	if !ok {
		t.Fatal("embedded guide manifest missing")
	}
	var declarations guideSourceDeclarations
	if err := json.Unmarshal([]byte(raw), &declarations); err != nil {
		t.Fatal(err)
	}
	if len(declarations.Sources) == 0 {
		t.Fatal("manifest declares no canonical guide sources")
	}
	root := filepath.Join("..", "..", "skills")
	for _, source := range declarations.Sources {
		owner := filepath.Join(root, cmp.Or(source.Package, "mocker"), source.Source)
		want, err := os.ReadFile(owner)
		if err != nil {
			t.Fatalf("read %s: %v", owner, err)
		}
		got, ok := Raw(source.Embedded)
		if !ok {
			t.Fatalf("embedded %s missing", source.Embedded)
		}
		if got != string(want) {
			t.Errorf("internal/guide/%s differs from %s — run `make guide-sync`", source.Embedded, owner)
		}
		for _, copy := range source.Copies {
			alias := filepath.Join(root, copy.Package, copy.Path)
			data, err := os.ReadFile(alias)
			if err != nil {
				t.Fatalf("read compatibility copy %s: %v", alias, err)
			}
			if string(data) != string(want) {
				t.Errorf("compatibility copy %s differs from canonical %s", alias, owner)
			}
		}
	}
}

func TestTopics(t *testing.T) {
	t.Parallel()
	want := []string{
		"overview", "tools", "shapes", "cookbook", "http", "design", "functions",
		"backend-overview", "backend-import", "backend-model", "backend-import-protocol",
		"backend-recovery", "backend-examples", "backend-database",
		"backend-database-reference", "backend-profile-go-sql",
		"backend-inspect", "backend-flow-reference", "backend-analysis", "backend-editor-projections", "backend-events", "backend-sync", "backend-change-proposals", "backend-annotations",
	}
	if !slices.Equal(Topics(), want) {
		t.Errorf("served topics = %v; want complete ordered inventory %v", Topics(), want)
	}
	for _, name := range Topics() {
		text, ok := Topic(name)
		if !ok || strings.TrimSpace(text) == "" {
			t.Errorf("topic %q: ok=%v, empty=%v", name, ok, strings.TrimSpace(text) == "")
		}
		if !strings.HasPrefix(text, "# ") {
			t.Errorf("topic %q does not start with a markdown title; got %q", name, firstLine(text))
		}
	}
	if _, ok := Topic("nope"); ok {
		t.Error("unknown topic reported ok")
	}
}

// TestOverviewHasNoFrontmatter pins what Topic strips: SKILL.md's YAML
// block is for the skill loader, not for a tool result.
func TestOverviewHasNoFrontmatter(t *testing.T) {
	t.Parallel()
	raw, _ := Raw("overview.md")
	if !strings.HasPrefix(raw, "---\n") {
		t.Fatalf("overview.md lost its frontmatter; the sync test should have caught that first")
	}
	text, _ := Topic(TopicOverview)
	if strings.HasPrefix(text, "---") || strings.Contains(text, "\nname: mocker\n") {
		t.Errorf("frontmatter survived stripping: %q", firstLine(text))
	}
}

// TestInstructionsStaySmall pins the budget the initialize field spends on
// every session an MCP host opens: the orientation must name get_guide and
// stay well under the size where it stops being an orientation.
func TestInstructionsStaySmall(t *testing.T) {
	t.Parallel()
	s := Instructions()
	if !strings.Contains(s, "get_guide") {
		t.Error("instructions do not point at get_guide")
	}
	if len(s) > 4096 {
		t.Errorf("instructions are %d bytes; keep them under 4096 — the full text belongs in get_guide", len(s))
	}
}

func TestStripFrontmatterLeavesABodyRuleAlone(t *testing.T) {
	t.Parallel()
	body := "# T\n\ntext\n\n---\n\nmore\n"
	if got := stripFrontmatter(body); got != body {
		t.Errorf("a thematic break in the body was treated as frontmatter: %q", got)
	}
	if got := stripFrontmatter("---\nname: x\n---\n\n# T\n"); got != "# T\n" {
		t.Errorf("stripFrontmatter = %q, want %q", got, "# T\n")
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

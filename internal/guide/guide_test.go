package guide

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skillFiles maps each embedded copy to its owner under skills/mocker/.
// The test walks up from the package directory, so it runs from `go test`
// in any working directory the module allows.
var skillFiles = map[string]string{
	"overview.md": "SKILL.md",
	"tools.md":    "references/tools.md",
	"shapes.md":   "references/shapes.md",
	"cookbook.md": "references/cookbook.md",
	"http.md":     "references/http.md",
	"design.md":   "references/design.md",
}

// TestEmbeddedCopiesMatchTheSkill is the whole reason the copies are
// allowed to exist: the skill directory is the one owner, and a copy that
// drifts is a server telling an agent something the installed skill does
// not. `make guide-sync` refreshes the copies.
func TestEmbeddedCopiesMatchTheSkill(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "skills", "mocker")
	for embedded, source := range skillFiles {
		want, err := os.ReadFile(filepath.Join(root, source))
		if err != nil {
			t.Fatalf("read %s: %v", source, err)
		}
		got, ok := Raw(embedded)
		if !ok {
			t.Fatalf("embedded %s missing", embedded)
		}
		if got != string(want) {
			t.Errorf("internal/guide/%s differs from skills/mocker/%s — run `make guide-sync`", embedded, source)
		}
	}
}

func TestTopics(t *testing.T) {
	t.Parallel()
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

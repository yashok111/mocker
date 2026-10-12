package guide

import (
	"regexp"
	"strings"
	"testing"
)

// TestFrozenTopicsMentionNoBackend is the frozen guide's whole promise: no
// topic a frozen server serves names the workbench, and the mock material
// around the removed passages is still there.
func TestFrozenTopicsMentionNoBackend(t *testing.T) {
	keep := map[string][]string{
		TopicOverview: {overviewMockIntro, "## Mental model", "## Mock workspace workflow", "## Rules that bite", "## When to open which reference", "suggest_design_scenario_tests"},
		TopicTools:    {"upload_asset", "## Compare-and-swap (`editVersion`)", "### Sequence branch test suggestions"},
		TopicShapes:   {"## 8. Assets", "dataBase64"},
		TopicCookbook: {"## 12. Pictures, PDFs, downloads", "upload_asset"},
		TopicHTTP:     {"claude mcp add"},
		TopicDesign:   {"backend team"},
	}
	for _, name := range FrozenTopics() {
		text, _, ok := FrozenTopic(name)
		if !ok {
			t.Fatalf("FrozenTopic(%q) unknown, but FrozenTopics lists it", name)
		}
		for _, block := range splitBlocks(text) {
			if isBackendPassage(block.text) {
				t.Errorf("%s: frozen text still names the workbench: %.160q", name, block.text)
			}
		}
		for _, want := range keep[name] {
			if !strings.Contains(text, want) {
				t.Errorf("%s: frozen text lost mock material %q", name, want)
			}
		}
	}
}

func TestFrozenTopicsHideBackendTopics(t *testing.T) {
	topics := FrozenTopics()
	if len(topics) == 0 || len(topics) == len(Topics()) {
		t.Fatalf("FrozenTopics = %v; want the mock topics only", topics)
	}
	for _, name := range topics {
		if IsBackendTopic(name) {
			t.Errorf("FrozenTopics lists %q", name)
		}
	}
	if _, _, ok := FrozenTopic(TopicBackendImport); ok {
		t.Error("FrozenTopic serves a workbench topic")
	}
	if _, _, ok := FrozenTopic("no-such-topic"); ok {
		t.Error("FrozenTopic serves an unknown topic")
	}
}

// TestWithoutBackendKeepsCleanTextByteForByte: a topic with nothing to
// remove keeps its manifest contentHash, so it must come back unchanged —
// not re-joined with normalised blank lines.
func TestWithoutBackendKeepsCleanTextByteForByte(t *testing.T) {
	for _, name := range []string{TopicShapes, TopicFunctions} {
		original, _ := Topic(name)
		text, changed, _ := FrozenTopic(name)
		if changed || text != original {
			t.Errorf("%s: changed = %v; a topic with no workbench passage must be served as is", name, changed)
		}
	}
}

func TestWithoutBackendDropsEmptiedSections(t *testing.T) {
	in := "# T\n\nintro\n\n## Mock\n\nkeep me\n\n## Backend things\n\n### Sub\n\nx\n\n## Mixed\n\nuse inspect13 here\n\n```bash\nbackend_tool\n```\n\n## Tail\n\ndataBase64 stays\n"
	want := "# T\n\nintro\n\n## Mock\n\nkeep me\n\n## Tail\n\ndataBase64 stays\n"
	if got := WithoutBackend(in); got != want {
		t.Fatalf("WithoutBackend:\n%s\nwant:\n%s", got, want)
	}
}

// TestFrozenInstructionsKeepTheMockParagraphs holds the hand-written
// instructions-frozen.md to instructions.md: every paragraph of the original
// that does not name the workbench is carried over verbatim, and nothing in
// the frozen file names it.
func TestFrozenInstructionsKeepTheMockParagraphs(t *testing.T) {
	frozen := FrozenInstructions()
	if isBackendPassage(frozen) {
		t.Errorf("instructions-frozen.md names the workbench: %q", backendTerm.FindString(frozen))
	}
	for p := range strings.SplitSeq(Instructions(), "\n\n") {
		p = strings.TrimSpace(p)
		if p == "" || isBackendPassage(p) {
			continue
		}
		if !strings.Contains(frozen, p) {
			t.Errorf("instructions-frozen.md lost a mock paragraph of instructions.md: %.120q", p)
		}
	}
	// The two mixed paragraphs keep their mock sentences.
	for _, want := range []string{"A WORKSPACE serves mocks and records traffic.", "Mocks use `get_server_config` → `list_workspaces` → `get_workspace` → `find_operations`; API design/sequences read topic design."} {
		if !strings.Contains(frozen, want) {
			t.Errorf("instructions-frozen.md lost %q", want)
		}
	}
	if regexp.MustCompile(`\n{3,}`).MatchString(frozen) {
		t.Error("instructions-frozen.md has a stray blank line")
	}
}

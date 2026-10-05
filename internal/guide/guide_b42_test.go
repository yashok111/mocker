package guide

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Owners are protocol boundaries: old inspection clients must never silently
// acquire mutation procedures when a new change workflow is published.
func TestB42GuideOwnership(t *testing.T) {
	for topic, want := range map[string]string{
		"backend-analysis":      "mocker-backend-inspect",
		"backend-recovery":      "mocker-backend-import",
		"backend-change-rebase": "mocker-backend-change",
		"backend-analysis-jobs": "mocker-backend-change",
	} {
		owner, ok := WorkflowForTopic(topic)
		if !ok || owner.WorkflowID != want || !slices.Contains(Topics(), topic) {
			t.Errorf("topic %s owner=%+v available=%v; want %s", topic, owner, ok, want)
		}
	}
	for _, topic := range []string{"backend-sync", "backend-change-proposals"} {
		owner, _ := WorkflowForTopic(topic)
		wantVersion := "2"
		if topic == "backend-change-proposals" {
			wantVersion = "3"
		}
		if owner.WorkflowVersion != wantVersion || !slices.Contains(owner.RequiredCapabilities, "backend-analysis-jobs") {
			t.Errorf("%s does not negotiate B4.2: %+v", topic, owner)
		}
	}
}

// A generated compatibility entrypoint must not direct root-only installations
// into nonexistent leaf-relative files. Both modes retain a pinned server route.
func TestB42ChangeReferencesArePortable(t *testing.T) {
	for _, mode := range []struct{ pkg, entry string }{
		{"mocker", "references/backend/change-proposals.md"},
		{"mocker-backend-change", "SKILL.md"},
	} {
		t.Run(mode.pkg, func(t *testing.T) {
			installed := filepath.Join(t.TempDir(), mode.pkg)
			if err := os.CopyFS(installed, os.DirFS(filepath.Join("../../skills", mode.pkg))); err != nil {
				t.Fatal(err)
			}
			entry := filepath.Join(installed, mode.entry)
			raw, err := os.ReadFile(entry)
			if err != nil {
				t.Fatal(err)
			}
			text := stripFrontmatter(string(raw))
			for _, link := range regexp.MustCompile(`\]\(([^)]+)\)`).FindAllStringSubmatch(text, -1) {
				target := link[1]
				if strings.Contains(target, "://") || strings.HasPrefix(target, "#") {
					continue
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(entry), target)); err != nil {
					t.Errorf("unresolvable installed reference %s: %v", target, err)
				}
			}
			for topic, local := range map[string]string{"backend-change-rebase": "references/rebase.md", "backend-analysis-jobs": "references/analysis-jobs.md", "backend-change-handoff": "references/handoff.md", "backend-endpoint-review": "references/endpoint-review.md"} {
				call := `get_guide {topic:"` + topic + `",guideSetId:selected.guideSetId}`
				if !strings.Contains(text, call) {
					t.Errorf("%s has no exact pinned fallback for %s", mode.pkg, topic)
				}
				if mode.pkg == "mocker-backend-change" {
					body, err := os.ReadFile(filepath.Join(installed, local))
					if err != nil {
						t.Fatal(err)
					}
					served, ok := Topic(topic)
					if !ok || string(body) != served {
						t.Errorf("standalone %s differs from pinned topic", local)
					}
				}
			}
		})
	}
}

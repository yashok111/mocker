package guide

import (
	_ "embed"
	"regexp"
	"strings"
)

// The Backend Workbench was frozen on 2026-10-12 (config.Config's
// BackendWorkbenchFrozen, MOCKER_BACKEND_WORKBENCH): its routes, tools and
// screens are hidden, and so is every word of this guide that points at
// them. The embedded texts themselves are NOT edited for the freeze: each
// one is a hashed member of the guide set in manifest.json and a byte copy
// of a file under skills/ (scripts/guide-sync.py), so editing them would
// reissue the whole guide set for a switch that is meant to be thrown back.
// The frozen server instead serves a filtered VIEW of the same texts, built
// here, and reports the hash of what it actually served.

// instructionsFrozen is instructions.md with the workbench paragraphs and
// sentences left out. It is a hand-written sibling rather than a filtered
// view because instructions.md mixes mock and workbench sentences inside
// one paragraph, and a sentence filter leaves orphaned workbench sentences
// that never say "backend" themselves (measured on this file: "Comparisons
// pin revisions." survives one). Keep the two in step when the mock half of
// instructions.md changes; TestFrozenInstructionsKeepTheMockParagraphs
// checks that every mock paragraph of the original is still here.
//
//go:embed instructions-frozen.md
var instructionsFrozen string

// FrozenInstructions is [Instructions] for a server whose Backend Workbench
// is frozen.
func FrozenInstructions() string { return instructionsFrozen }

// IsBackendTopic reports whether a topic documents the Backend Workbench
// alone; a frozen server neither lists nor serves such a topic.
func IsBackendTopic(name string) bool { return strings.HasPrefix(name, "backend-") }

// FrozenTopics is [Topics] without the workbench topics.
func FrozenTopics() []string {
	var out []string
	for _, t := range Topics() {
		if !IsBackendTopic(t) {
			out = append(out, t)
		}
	}
	return out
}

// FrozenTopic is [Topic] for a frozen server: a workbench topic is unknown,
// and every other topic is served with its workbench passages removed (see
// [WithoutBackend]). changed reports whether the served text differs from
// [Topic]'s, which is when the manifest's contentHash no longer describes it.
func FrozenTopic(name string) (text string, changed, ok bool) {
	if IsBackendTopic(name) {
		return "", false, false
	}
	original, ok := Topic(name)
	if !ok {
		return "", false, false
	}
	text = original
	if name == TopicOverview {
		text = trimOverviewIntro(text)
	}
	text = WithoutBackend(text)
	return text, text != original, true
}

// backendTerm matches what marks a passage as workbench documentation: the
// word itself (tool names, topic names, "backend project"), and the pinned
// workflow and source-profile identifiers the workbench guides route by
// (inspect13, import8, sync2, change5, database7, verify1, project2,
// replay1, routing2, source5, schema3). The identifiers match in lower case
// or capitalised at a sentence start, never case-insensitively: the
// `dataBase64` argument of upload_asset reads as "database64" otherwise, and
// took the assets sections of three mock topics with it on the first run.
// "backend team" is the one ordinary use of the word in the mock topics
// (design.md hands "the backend team" an OpenAPI document) and is blanked
// before matching by [isBackendPassage].
var backendTerm = regexp.MustCompile(`(?i:backend)|\b(?:[Ii]nspect|[Ii]mport|[Ss]ync|[Cc]hange|[Dd]atabase|[Vv]erify|[Pp]roject|[Rr]eplay|[Rr]outing|[Ss]ource|[Ss]chema)\d+\b`)

var backendTeam = regexp.MustCompile(`(?i)backend team`)

func isBackendPassage(s string) bool {
	return backendTerm.MatchString(backendTeam.ReplaceAllString(s, ""))
}

// overviewMockIntro is the anchor of the first mock sentence in overview.md's
// opening paragraph; everything in that paragraph before it describes the
// workbench.
const overviewMockIntro = "For mocks, one OpenAPI"

// trimOverviewIntro rewrites overview.md's opening paragraph, the one place
// where workbench and mock sentences share a paragraph that the mock reader
// cannot do without. Missing anchor: the text is returned unchanged, and
// [WithoutBackend] then drops the whole paragraph — which
// TestFrozenTopicsMentionNoBackend catches, since the overview loses its
// mock introduction.
func trimOverviewIntro(text string) string {
	at := strings.Index(text, overviewMockIntro)
	if at < 0 {
		return text
	}
	start := strings.LastIndex(text[:at], "\n\n")
	if start < 0 {
		return text
	}
	start += len("\n\n")
	return text[:start] + "mocker provides OpenAPI mocks, API design and saved sequences. " + text[at:]
}

// mdBlock is one unit [WithoutBackend] keeps or drops whole: a heading line,
// a fenced code block, or a run of non-blank lines.
type mdBlock struct {
	text  string
	level int // heading level; 0 for anything else
}

// WithoutBackend removes the Backend Workbench passages from a markdown
// guide text: a section whose heading names the workbench goes whole, with
// its subsections; any other block that names it goes alone; and a heading
// left with nothing under it goes too. A text with nothing to remove is
// returned byte for byte, so its manifest hash still holds.
func WithoutBackend(text string) string {
	blocks := splitBlocks(text)
	kept := make([]mdBlock, 0, len(blocks))
	dropUnder := 0 // > 0: inside a dropped section of this heading level
	dropped := false
	for _, b := range blocks {
		if b.level > 0 && dropUnder > 0 && b.level <= dropUnder {
			dropUnder = 0
		}
		if dropUnder > 0 {
			dropped = true
			continue
		}
		if isBackendPassage(b.text) {
			dropped = true
			if b.level > 0 {
				dropUnder = b.level
			}
			continue
		}
		kept = append(kept, b)
	}
	if !dropped {
		return text
	}
	// A heading (below the title) with no content left before the next
	// heading of its own level or higher is removed; repeat, because
	// removing an empty subsection can empty its parent.
	for changed := true; changed; {
		changed = false
		for i := 0; i < len(kept); i++ {
			if kept[i].level < 2 {
				continue
			}
			if i+1 == len(kept) || (kept[i+1].level > 0 && kept[i+1].level <= kept[i].level) {
				kept = append(kept[:i], kept[i+1:]...)
				changed = true
				i--
			}
		}
	}
	parts := make([]string, len(kept))
	for i, b := range kept {
		parts[i] = b.text
	}
	return strings.Join(parts, "\n\n") + "\n"
}

var headingLine = regexp.MustCompile(`^(#{1,6}) `)

func splitBlocks(text string) []mdBlock {
	var (
		blocks  []mdBlock
		current []string
		fenced  bool
	)
	flush := func() {
		if len(current) > 0 {
			blocks = append(blocks, mdBlock{text: strings.Join(current, "\n")})
			current = nil
		}
	}
	for line := range strings.SplitSeq(strings.TrimRight(text, "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if fenced {
			current = append(current, line)
			if strings.HasPrefix(trimmed, "```") {
				fenced = false
				flush()
			}
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "```"):
			flush()
			current = append(current, line)
			fenced = true
		case trimmed == "":
			flush()
		case headingLine.MatchString(line):
			flush()
			blocks = append(blocks, mdBlock{text: line, level: len(headingLine.FindStringSubmatch(line)[1])})
		default:
			current = append(current, line)
		}
	}
	flush()
	return blocks
}

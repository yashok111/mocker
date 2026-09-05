// Package guide embeds the agent-facing documentation of this product so
// that a running server can hand it to an MCP client: a short orientation
// returned in initialize's `instructions` field, and the full skill —
// SKILL.md plus its five reference files — behind the get_guide tool
// (internal/mcp/tools_guide.go).
//
// The ONE OWNER of the text is skills/mocker/ at the repository root: that
// directory is what a human reads on the forge and what `skills add`
// copies into a consumer project, so it has to stand on its own outside
// this binary. go:embed cannot reach above its own package directory, so
// the six files here are byte copies, and guide_test.go fails the build
// the moment one of them drifts from its source — `make guide-sync`
// refreshes them. instructions.md is the exception: it exists only to be
// served at initialize and has no counterpart in the skill.
//
// Why a copy rather than moving the skill under internal/: a skill is
// discovered by path (`<repo>/skills/<name>/SKILL.md`), and a documentation
// directory a Go build tag governs is a documentation directory nobody
// installs.
package guide

import (
	"embed"
	"strings"
)

// Topic names, in the order get_guide reports them. "overview" is
// SKILL.md's body; the other six are its references.
const (
	TopicOverview = "overview"
	TopicTools    = "tools"
	TopicShapes   = "shapes"
	TopicCookbook = "cookbook"
	TopicHTTP     = "http"
	// TopicDesign is P7a's (DESIGN §34.5): designing an API on top of a
	// workspace, from a brief to a contract and back as the next base.
	TopicDesign = "design"
	// TopicFunctions is A18's: endpoint functions and the two stream hooks
	// — the contract, the sandbox, the guards and the two serving matrices.
	// It is its own topic rather than a section of shapes.md because it is
	// the one feature whose text a caller reads BEFORE writing anything,
	// and a caller who has already found the shape is not the reader it is
	// for.
	TopicFunctions = "functions"
)

//go:embed instructions.md overview.md tools.md shapes.md cookbook.md http.md design.md functions.md
var files embed.FS

// topicFiles maps a topic to its embedded file. overview.md is SKILL.md
// verbatim, frontmatter included; Topic strips the frontmatter because a
// tool result is not a skill file and the YAML block would be noise to the
// model reading it.
var topicFiles = map[string]string{
	TopicOverview:  "overview.md",
	TopicTools:     "tools.md",
	TopicShapes:    "shapes.md",
	TopicCookbook:  "cookbook.md",
	TopicHTTP:      "http.md",
	TopicDesign:    "design.md",
	TopicFunctions: "functions.md",
}

// Topics is the ordered list of topic names get_guide accepts.
func Topics() []string {
	return []string{TopicOverview, TopicTools, TopicShapes, TopicCookbook, TopicHTTP, TopicDesign, TopicFunctions}
}

// Instructions is the orientation text initialize returns to every MCP
// client. It is deliberately short: an MCP host injects it into the
// model's context on every session, so it pays for itself only by naming
// where the rest is (get_guide) and the handful of rules a first call gets
// wrong without it.
func Instructions() string {
	return mustRead("instructions.md")
}

// Topic returns the markdown of one topic and whether the name is known.
func Topic(name string) (string, bool) {
	file, ok := topicFiles[name]
	if !ok {
		return "", false
	}
	text := mustRead(file)
	if name == TopicOverview {
		text = stripFrontmatter(text)
	}
	return text, true
}

// Raw returns an embedded file byte for byte, frontmatter included; it is
// what the sync test compares against the skill directory.
func Raw(file string) (string, bool) {
	b, err := files.ReadFile(file)
	if err != nil {
		return "", false
	}
	return string(b), true
}

func mustRead(file string) string {
	b, err := files.ReadFile(file)
	if err != nil {
		// Every name passed here is a literal in this file and every file is
		// named in the go:embed directive above; a miss is a build that
		// should not have linked, not a runtime condition to report.
		panic("guide: embedded file missing: " + file)
	}
	return string(b)
}

// stripFrontmatter drops a leading YAML block delimited by "---" lines.
// Only the first block, only at the very start: a "---" later in the body
// is a thematic break and stays.
func stripFrontmatter(text string) string {
	if !strings.HasPrefix(text, "---\n") {
		return text
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return text
	}
	return strings.TrimLeft(text[4+end+len("\n---\n"):], "\n")
}

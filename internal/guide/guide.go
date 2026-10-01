// Package guide embeds the agent-facing documentation served by initialize
// and get_guide. skills/mocker/guide-sources.json declares canonical owners under
// skills/: the root package, its references and the standalone import/database
// packages.
// make guide-sync generates embedded and compatibility copies, metadata and an
// immutable manifest. Edit declared owners rather than generated copies.
// instructions.md is the small initialize-only orientation, maintained here.
// Tests validate every declared source, generated metadata and content hash.
// Root, standalone leaf and bundle installations share the served procedures.
package guide

import (
	"embed"
	"strings"
)

// Topic names, in the order get_guide reports them. "overview" is
// SKILL.md's body; the remaining topics are its references.
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
	// Backend topics are loaded progressively from one selected guide set.
	TopicBackendOverview          = "backend-overview"
	TopicBackendImport            = "backend-import"
	TopicBackendModel             = "backend-model"
	TopicBackendImportProtocol    = "backend-import-protocol"
	TopicBackendRecovery          = "backend-recovery"
	TopicBackendExamples          = "backend-examples"
	TopicBackendDatabase          = "backend-database"
	TopicBackendDatabaseReference = "backend-database-reference"
	TopicBackendProfileGoSQL      = "backend-profile-go-sql"
	TopicBackendInspect           = "backend-inspect"
	TopicBackendFlowReference     = "backend-flow-reference"
	TopicBackendAnalysis          = "backend-analysis"
)

//go:embed instructions.md overview.md tools.md shapes.md cookbook.md http.md design.md functions.md backend-overview.md backend-import.md backend-model.md backend-import-protocol.md backend-recovery.md backend-examples.md manifest.json
//go:embed backend-database.md backend-database-reference.md backend-profile-go-sql.md
//go:embed backend-inspect.md backend-flow-reference.md backend-analysis.md
var files embed.FS

// topicFiles maps a topic to its embedded file. overview.md is SKILL.md
// verbatim, frontmatter included; Topic strips the frontmatter because a
// tool result is not a skill file and the YAML block would be noise to the
// model reading it.
var topicFiles = map[string]string{
	TopicOverview:                 "overview.md",
	TopicTools:                    "tools.md",
	TopicShapes:                   "shapes.md",
	TopicCookbook:                 "cookbook.md",
	TopicHTTP:                     "http.md",
	TopicDesign:                   "design.md",
	TopicFunctions:                "functions.md",
	TopicBackendOverview:          "backend-overview.md",
	TopicBackendImport:            "backend-import.md",
	TopicBackendModel:             "backend-model.md",
	TopicBackendImportProtocol:    "backend-import-protocol.md",
	TopicBackendRecovery:          "backend-recovery.md",
	TopicBackendExamples:          "backend-examples.md",
	TopicBackendDatabase:          "backend-database.md",
	TopicBackendDatabaseReference: "backend-database-reference.md",
	TopicBackendProfileGoSQL:      "backend-profile-go-sql.md",
	TopicBackendInspect:           "backend-inspect.md",
	TopicBackendFlowReference:     "backend-flow-reference.md",
	TopicBackendAnalysis:          "backend-analysis.md",
}

// Topics is the ordered list of topic names get_guide accepts.
func Topics() []string {
	return []string{
		TopicOverview, TopicTools, TopicShapes, TopicCookbook, TopicHTTP,
		TopicDesign, TopicFunctions, TopicBackendOverview, TopicBackendImport,
		TopicBackendModel, TopicBackendImportProtocol, TopicBackendRecovery,
		TopicBackendExamples, TopicBackendDatabase, TopicBackendDatabaseReference,
		TopicBackendProfileGoSQL,
		TopicBackendInspect, TopicBackendFlowReference, TopicBackendAnalysis,
	}
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
	switch name {
	case TopicOverview, TopicBackendOverview, TopicBackendImport, TopicBackendDatabase, TopicBackendInspect:
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

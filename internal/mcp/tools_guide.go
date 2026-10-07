// tools_guide.go registers the one tool in this package that calls NO
// admin route: get_guide, which hands the model the product's own usage
// guide — internal/guide's embedded copy of skills/mocker/. Tool
// fifty-five.
//
// It exists because an agent connected over MCP has, without it, exactly
// two sources of orientation: the tool descriptions (one verb each, no
// order between them) and whatever initialize's `instructions` field
// carries (kept short on purpose, since a host injects it into every
// session). Neither says which reads must precede which writes, what a
// scenario does not carry, or that opKey arrives already encoded — the
// guide does, in five topics an agent can pull one at a time instead of
// paying for all of them at once.
//
// Its toolRoutes row is [noRoute] (routes.go), the sentinel whose own doc
// comment states the rule once: the tool reaches no handler, so there is
// nothing for admin's route allowlist to carry and nothing for CallAsMCP to
// refuse. That makes it the one tool "an adapter over the admin API" does
// not describe — recorded here rather than left for a reader to reconcile
// with the package comment.
package mcp

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/guide"
)

func addGuideTools(s *sdk.Server) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "get_guide",
		Description: "Returns mocker's own usage guide for agents, as markdown, one topic per call. " +
			"Call it FIRST in a session that is about to configure anything. Topics: " +
			"\"overview\" (the default — what mocker is, the two planes and four response layers, " +
			"the order of reads and writes, and the rules a first call gets wrong: whole-object " +
			"writes replace, editVersion compare-and-swap, confirmSlug, opKey encoding); " +
			"\"tools\" (every tool with its inputs, outputs and gotchas); \"shapes\" (the JSON " +
			"documents you write: override, when[], recipes, custom endpoint, stream, session " +
			"directive, settings, resources, assets, the error envelope); \"cookbook\" (twelve " +
			"ordered recipes: stand up a workspace, make login work, force an error, shape a body, " +
			"add a route, confirm a resource, scenarios, undo, streams, spec drift, debugging, " +
			"assets); \"http\" (the same over curl for scripts and CI, plus MCP client config). " +
			"For backend project preparation read \"backend-overview\" and pin guideSetId after capability discovery. Unknown guide sets fail explicitly. " +
			"For source import, same-provider reconciliation and pinned revision comparison select the compatible \"backend-import\" workflow and its guideSetId. Relational import requires schema2 and every declared capability; foundation-only fallback cannot perform a relational task. " +
			"Load shared topics progressively from that same set: \"backend-model\" for records and evidence, \"backend-import-protocol\" before staging, \"backend-recovery\" before commit/resume, \"backend-examples\" for worked fixtures, and \"backend-profile-go-sql\" for the relational source profile. " +
			"For database inspection and typed draft proposals select \"backend-database\" and load \"backend-database-reference\" as needed. For source3 flow or imported data accesses select \"backend-inspect\", with \"backend-flow-reference\" and \"backend-analysis\" owned by inspect. Runtime import requires runtime-flow-v1/schema3 and every declared capability. Model/recovery/profile topics belong to import. Verify each actual owner's advertised identity, all required schemas/capabilities and topic hash in the same guideSetId/manifestHash. For saved authored editor content select inspect10 with backend-editor-projections/backend-editor-artifacts-v1 and read \"backend-editor-projections\" for full rosters, exact linked/copy provenance, budgets and retry. For source5 event routes/jobs/service calls select inspect10 with backend-events-query and read \"backend-events\"; import8 owns events-service-v1/schema5 and explicit4→5 extension. Pure inspection writes nothing; an authorized resolvable source gap uses the full compatible whole-scope import and audit procedure. " +
			"For composed source6 synchronization select sync2 and read \"backend-sync\" for selected-partition extension, qualified claims, conflicts, incremental scopes and exact request recovery. For full desired edits select change4 and read \"backend-change-proposals\" for immutable baselines, typed intent, preview/apply/history and permanent command IDs. Read \"backend-change-rebase\" for reasoned B/O/N conflicts and exact preview/apply; read \"backend-analysis-jobs\" for start/poll/frozen pages, explicit retry/cancel and exact report-to-ready. Static reports do not verify runtime. Project2 owns \"backend-annotations\" for metadata notes, CAS, cursor conflicts and orphans. Import8 retains legacy extraction procedures and owns schema6 representation import; inspect10/database7 describe their exact source/legacy/full targets and SavedView-v2. Current READY candidates expose basic graph/node/evidence/coverage/assertions only. " +
			"For exact C4 architecture mappings, aggregate members and diagram-view-v1 read backend-architecture under inspect10. Architecture and static interactions are supported; read backend-interactions before the read-only candidate builder; source assertions, authored intent and gaps remain distinct. " +
			"For verification and transfer read backend-verify, backend-diagnostics, backend-observations, backend-correlation, backend-measurements, backend-benchmarks, backend-replay and backend-portable; backend-lifecycle and backend-business-map cover the other diagram kinds. " +
			"Static text: calls no admin route, reads no workspace, changes nothing.",
		InputSchema: getGuideInputSchema(),
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleGetGuide)
}

// getGuideInputSchema is GetGuideInput's schema with the topic list taken
// from guide.Topics(). Review 2026-10-06, F134: the list was a hand copy in
// the struct tag that stopped at backend-replay while the guide served forty
// topics, so ten (backend-portable, -verify, -measurements among them) were
// invisible to an agent reading the input schema.
func getGuideInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"guideSetId": map[string]any{"type": "string", "description": "optional immutable guide set selector; unknown selectors fail explicitly"},
			"topic":      map[string]any{"type": "string", "description": "one of " + strings.Join(guide.Topics(), ", ") + "; omitted means overview"},
		},
	}
}

// GetGuideInput is get_guide's input.
type GetGuideInput struct {
	GuideSetID string `json:"guideSetId,omitempty" jsonschema:"optional immutable guide set selector; unknown selectors fail explicitly"`
	Topic      string `json:"topic,omitempty"` // published schema: getGuideInputSchema
}

// GetGuideOutput is get_guide's declared output.
type GetGuideOutput struct {
	GuideSetID      string   `json:"guideSetId"`
	ManifestHash    string   `json:"manifestHash"`
	ContentHash     string   `json:"contentHash"`
	WorkflowID      string   `json:"workflowId"`
	WorkflowVersion string   `json:"workflowVersion"`
	Topic           string   `json:"topic"`
	Topics          []string `json:"topics"`
	Markdown        string   `json:"markdown"`
}

func handleGetGuide(_ context.Context, _ *sdk.CallToolRequest, in GetGuideInput) (*sdk.CallToolResult, GetGuideOutput, error) {
	if in.GuideSetID != "" && in.GuideSetID != guide.CurrentGuideSetID() {
		return nil, GetGuideOutput{}, fmt.Errorf("get_guide: unknown guide set %q", in.GuideSetID)
	}
	topic := strings.ToLower(strings.TrimSpace(in.Topic))
	if topic == "" {
		topic = guide.TopicOverview
	}
	text, ok := guide.Topic(topic)
	if !ok {
		return nil, GetGuideOutput{}, fmt.Errorf("get_guide: unknown topic %q; one of %s", in.Topic, strings.Join(guide.Topics(), ", "))
	}
	workflow, ok := guide.WorkflowForTopic(topic)
	if !ok {
		return nil, GetGuideOutput{}, fmt.Errorf("get_guide: topic %q has no workflow metadata", topic)
	}
	contentHash := ""
	for _, entry := range workflow.Topics {
		if entry.Topic == topic {
			contentHash = entry.ContentHash
			break
		}
	}
	return nil, GetGuideOutput{Topic: topic, Topics: guide.Topics(), Markdown: text,
		GuideSetID: workflow.GuideSetID, ManifestHash: workflow.ManifestHash,
		ContentHash: contentHash, WorkflowID: workflow.WorkflowID, WorkflowVersion: workflow.WorkflowVersion}, nil
}

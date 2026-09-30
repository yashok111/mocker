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
			"Static text: calls no admin route, reads no workspace, changes nothing.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleGetGuide)
}

// GetGuideInput is get_guide's input.
type GetGuideInput struct {
	GuideSetID string `json:"guideSetId,omitempty" jsonschema:"optional immutable guide set selector; unknown selectors fail explicitly"`
	Topic      string `json:"topic,omitempty" jsonschema:"one of overview, tools, shapes, cookbook, http, design, functions, backend-overview; omitted means overview"`
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

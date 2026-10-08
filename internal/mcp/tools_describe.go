// tools_describe.go registers describe_tool, the detailed half of the
// tool catalog (tool_catalog.go): tools/list publishes a one-sentence
// summary and a one-level argument schema per tool, and describe_tool
// {name} returns one tool whole. Review 2026-10-06, F23.
//
// Like get_guide and get_server_config its toolRoutes row is [noRoute]
// (routes.go): it reads the server's own tool registry and calls no admin
// handler, so there is nothing for admin's allowlist to carry and nothing
// for CallAsMCP to refuse.
package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/jsonx"
)

// describe_tool is registered RAW (s.AddTool), not through the typed
// sdk.AddTool every get_* tool uses, for one measured reason: the SDK's
// typed path validates an object result and then re-marshals it through
// float64 (mcp/tool.go applySchema, internal/json without UseNumber), so a
// schema bound such as an int64 maximum 9223372036854775807 reached the
// client as 9223372036854775808 (TestDescribeToolKeepsExactNumbers). The
// raw path writes jsonx's own bytes, and its arguments are validated the
// way every raw tool here validates them: a schema compiled once from the
// registered input schema, unknown members refused.
func addDescribeTools(s *sdk.Server, catalog *toolCatalog) {
	addImportDiagnosticTool(s, catalog)
	tool := &sdk.Tool{
		Name: "describe_tool",
		Description: "Returns one tool's full description and argument schema; tools/list carries only a one-sentence summary and top-level argument names. " +
			"Call it before the first use of a tool whose arguments are not obvious from that summary. " +
			"Answers name, description (full), inputSchema (the complete JSON Schema the server validates arguments against; repeated subtrees are shared through $defs and #/$defs/... references), " +
			"outputSchema when the tool declares one, annotations, and routes (the admin API routes the tool calls; empty for a tool that calls none). " +
			"An unknown name is an error naming the closest tool names. Reads the server's own tool registry: calls no admin route, changes nothing.",
		InputSchema: designScenarioSchemaObject([]string{"name"}, map[string]any{
			"name": map[string]any{"type": "string", "minLength": 1, "description": "the exact tool name as tools/list publishes it"},
		}),
		OutputSchema: designScenarioSchemaObject([]string{"name", "description", "inputSchema", "routes"}, map[string]any{
			"name":         map[string]any{"type": "string"},
			"title":        map[string]any{"type": "string"},
			"description":  map[string]any{"type": "string"},
			"inputSchema":  map[string]any{"type": "object"},
			"outputSchema": map[string]any{"type": "object"},
			"annotations":  map[string]any{"type": "object"},
			"routes":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		}),
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}
	schema, err := compileDesignScenarioToolSchema(tool)
	if err != nil {
		panic(fmt.Errorf("AddTool %q: %w", tool.Name, err))
	}
	s.AddTool(tool, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var in DescribeToolInput
		if err := decodeDesignScenarioToolInput(req.Params.Arguments, &in, schema); err != nil {
			return designScenarioToolErrorResult(fmt.Errorf("describe_tool: invalid arguments: %w", err)), nil
		}
		answer, err := catalog.lookup(ctx, req.Session, in.Name)
		if err != nil {
			return designScenarioToolErrorResult(err), nil
		}
		return &sdk.CallToolResult{StructuredContent: answer, Content: []sdk.Content{&sdk.TextContent{Text: string(answer)}}}, nil
	})
}

// DescribeToolInput is describe_tool's input.
type DescribeToolInput struct {
	Name string `json:"name"`
}

// DescribeToolOutput is describe_tool's answer: the registered tool, with
// its input and output schemas in compactSchema's lossless $defs form
// (start_backend_analysis expands to about 900 KB inline).
type DescribeToolOutput struct {
	Name         string               `json:"name"`
	Title        string               `json:"title,omitempty"`
	Description  string               `json:"description"`
	InputSchema  any                  `json:"inputSchema"`
	OutputSchema any                  `json:"outputSchema,omitempty"`
	Annotations  *sdk.ToolAnnotations `json:"annotations,omitempty"`
	Routes       []string             `json:"routes"`
}

// lookup answers describe_tool for name: the tool's encoded description,
// or an error naming the closest registered tools.
func (c *toolCatalog) lookup(ctx context.Context, session *sdk.ServerSession, name string) (jsonx.RawMessage, error) {
	tools, err := c.registered(ctx, session)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(name)
	for _, tool := range tools {
		if tool.Name == trimmed {
			return c.describe(tool)
		}
	}
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	if closest := closestToolNames(name, names); len(closest) > 0 {
		return nil, fmt.Errorf("describe_tool: unknown tool %q; closest: %s", name, strings.Join(closest, ", "))
	}
	return nil, fmt.Errorf("describe_tool: unknown tool %q; tools/list names every tool", name)
}

// describe encodes one tool's answer once and caches the bytes by the
// registered pointer: the backend schemas take milliseconds to compact.
func (c *toolCatalog) describe(tool *sdk.Tool) (jsonx.RawMessage, error) {
	if cached, ok := c.described.Load(tool); ok {
		return cached.(jsonx.RawMessage), nil
	}
	routes := slices.Clone(toolRoutes[tool.Name])
	if routes == nil {
		routes = []string{}
	}
	encoded, err := jsonx.Marshal(DescribeToolOutput{
		Name:         tool.Name,
		Title:        tool.Title,
		Description:  tool.Description,
		InputSchema:  compactSchema(tool.InputSchema),
		OutputSchema: compactSchema(tool.OutputSchema),
		Annotations:  tool.Annotations,
		Routes:       routes,
	})
	if err != nil {
		return nil, fmt.Errorf("describe_tool: encode %s: %w", tool.Name, err)
	}
	actual, _ := c.described.LoadOrStore(tool, jsonx.RawMessage(encoded))
	return actual.(jsonx.RawMessage), nil
}

// maxClosestTools bounds the suggestion list of an unknown name.
const maxClosestTools = 5

// closestToolNames ranks tool names against a mistyped one, cheaply and
// without a fuzzy-matching library: a shared prefix first, then containment
// either way, then the number of shared "_"-separated words. Case and
// surrounding space are ignored.
func closestToolNames(query string, names []string) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	words := strings.FieldsFunc(q, func(r rune) bool { return r == '_' || r == '-' || r == ' ' || r == '.' })
	type candidate struct {
		name   string
		rank   int
		shared int
	}
	var found []candidate
	for _, name := range names {
		switch {
		case strings.HasPrefix(name, q) || strings.HasPrefix(q, name):
			found = append(found, candidate{name: name, rank: 0})
		case strings.Contains(name, q) || strings.Contains(q, name):
			found = append(found, candidate{name: name, rank: 1})
		default:
			shared := 0
			for _, word := range strings.Split(name, "_") {
				if slices.Contains(words, word) {
					shared++
				}
			}
			if shared > 0 {
				found = append(found, candidate{name: name, rank: 2, shared: shared})
			}
		}
	}
	slices.SortFunc(found, func(a, b candidate) int {
		if a.rank != b.rank {
			return a.rank - b.rank
		}
		if a.shared != b.shared {
			return b.shared - a.shared
		}
		// Among equals the name closest in length to the query is the
		// likelier intent: "backend_analysis" ranks start_backend_analysis
		// above get_backend_analysis_results.
		if da, db := lengthGap(a.name, q), lengthGap(b.name, q); da != db {
			return da - db
		}
		return strings.Compare(a.name, b.name)
	})
	out := make([]string, 0, maxClosestTools)
	for _, c := range found {
		if len(out) == maxClosestTools {
			break
		}
		out = append(out, c.name)
	}
	return out
}

func lengthGap(a, b string) int {
	if len(a) > len(b) {
		return len(a) - len(b)
	}
	return len(b) - len(a)
}

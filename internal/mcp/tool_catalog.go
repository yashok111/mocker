package mcp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/jsonx"
)

// toolCatalog owns the two published forms of every registered tool: the
// LIGHT summary tools/list answers with, and the FULL form describe_tool
// returns for one tool at a time.
//
// Review 2026-10-06, F23, and the owner's decision on it (a Russian string
// quoted as data: «нужно сделать очень легкий запрос на описание всех
// методов и точечный запрос на описание конкретного метода подробно»).
// Sharing repeated subtrees through $defs (compactSchema) brought tools/list
// from 4.7 MB to about 1.55 MB, which is still more than a client that puts
// every tool schema into the model context can carry on each turn. So
// tools/list now publishes, per tool, a one-sentence description and a
// one-level input schema (property names, their stated JSON type, the
// required set), and describe_tool {name} returns that one tool's full
// description, compacted input schema, output schema, annotations and the
// admin routes it calls.
//
// Only the PUBLISHED copy changes. The SDK validates a typed tool's
// arguments against the schema it resolved at AddTool (mcp/server.go,
// toolForErr: inputResolved), and every raw-registered tool here compiles
// its own validator from the registered *sdk.Tool at registration
// (addBackendTool, compileDesignScenarioToolSchema); neither ever reads a
// tools/list answer. One validation path, pinned by
// TestWrongNestedArgumentStillRefused.
type toolCatalog struct {
	// next is the receiving method handler below the catalog's middleware,
	// captured once when New installs it, before any request is served.
	// describe_tool lists the registered tools through it: the SDK has no
	// exported accessor for a server's tools, and listing through the
	// method table keeps the SDK the one owner of the registry, so the
	// catalog cannot drift from what is actually registered.
	next sdk.MethodHandler

	light     sync.Map // *sdk.Tool -> *sdk.Tool
	described sync.Map // *sdk.Tool -> DescribeToolOutput
}

// middleware is the tools/list rewrite: every other method passes through
// untouched. Each light copy is built once per registered tool and cached by
// the registered pointer (stable for the process: tools are registered once
// in New).
func (c *toolCatalog) middleware(next sdk.MethodHandler) sdk.MethodHandler {
	c.next = next
	return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
		res, err := next(ctx, method, req)
		list, ok := res.(*sdk.ListToolsResult)
		if err != nil || method != "tools/list" || !ok {
			return res, err
		}
		out := *list
		out.Tools = make([]*sdk.Tool, len(list.Tools))
		for i, tool := range list.Tools {
			out.Tools[i] = c.lightTool(tool)
		}
		return &out, nil
	}
}

func (c *toolCatalog) lightTool(tool *sdk.Tool) *sdk.Tool {
	if cached, ok := c.light.Load(tool); ok {
		return cached.(*sdk.Tool)
	}
	// OutputSchema is left out on purpose. MCP has a client validate
	// structuredContent only against an outputSchema the server DECLARED
	// (the TypeScript SDK checks it when the listed tool has one, the Go
	// SDK client never does), so omitting it cannot make a client reject a
	// result; a shortened output schema could. describe_tool carries it.
	light := &sdk.Tool{
		Meta:        tool.Meta,
		Name:        tool.Name,
		Title:       tool.Title,
		Icons:       tool.Icons,
		Annotations: tool.Annotations,
		Description: summarizeDescription(tool.Description),
		InputSchema: shallowInputSchema(tool.InputSchema),
	}
	actual, _ := c.light.LoadOrStore(tool, light)
	return actual.(*sdk.Tool)
}

// errCatalogNotInstalled is a wiring defect (describe_tool registered on a
// server whose catalog middleware was never installed), never a client's.
var errCatalogNotInstalled = errors.New("describe_tool: the tool catalog is not installed on this server")

// registered lists every registered tool through the method table below
// the catalog, following the SDK's pagination (DefaultPageSize is 1000
// today, above this surface, but a cursor costs nothing to honour).
func (c *toolCatalog) registered(ctx context.Context, session *sdk.ServerSession) ([]*sdk.Tool, error) {
	if c.next == nil {
		return nil, errCatalogNotInstalled
	}
	var tools []*sdk.Tool
	cursor := ""
	for {
		res, err := c.next(ctx, "tools/list", &sdk.ListToolsRequest{Session: session, Params: &sdk.ListToolsParams{Cursor: cursor}})
		if err != nil {
			return nil, err
		}
		list, ok := res.(*sdk.ListToolsResult)
		if !ok {
			return nil, errCatalogNotInstalled
		}
		tools = append(tools, list.Tools...)
		if list.NextCursor == "" {
			return tools, nil
		}
		cursor = list.NextCursor
	}
}

// summaryMaxRunes caps the light description. A sentence is what a model
// needs to choose a tool; the full text is one describe_tool call away.
const summaryMaxRunes = 200

// summarizeDescription returns the first line's first sentence, cut at a
// word boundary with an ellipsis when it is still over summaryMaxRunes.
func summarizeDescription(full string) string {
	s := strings.TrimSpace(full)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if end := firstSentenceEnd(s); end > 0 {
		s = s[:end]
	}
	if utf8.RuneCountInString(s) <= summaryMaxRunes {
		return s
	}
	runes := []rune(s)
	budget := summaryMaxRunes - 1 // the ellipsis takes the last rune
	cut := budget
	if !unicode.IsSpace(runes[budget]) {
		// The word straddles the cap: back up to the last space, unless the
		// text has none worth keeping (one huge token), where a mid-token
		// cut is honest only because the ellipsis marks it.
		for i := budget - 1; i > budget/2; i-- {
			if unicode.IsSpace(runes[i]) {
				cut = i
				break
			}
		}
	}
	return strings.TrimRight(string(runes[:cut]), " \t,;:") + "…"
}

// sentenceAbbreviations end in a period that does not end a sentence.
var sentenceAbbreviations = []string{"e.g", "i.e", "vs", "cf", "etc"}

// firstSentenceEnd is the byte offset just past the first '.', '!' or '?'
// that is followed by a space and does not close an abbreviation, or -1.
// "1.5" and "x.y" never end a sentence because no space follows the period.
func firstSentenceEnd(s string) int {
	for i := 0; i < len(s)-1; i++ {
		c := s[i]
		if (c != '.' && c != '!' && c != '?') || s[i+1] != ' ' {
			continue
		}
		if c == '.' {
			word := s[strings.LastIndexAny(s[:i], " (\"")+1 : i]
			if isAbbreviation(word) {
				continue
			}
		}
		return i + 1
	}
	return -1
}

func isAbbreviation(word string) bool {
	word = strings.ToLower(word)
	for _, a := range sentenceAbbreviations {
		if word == a {
			return true
		}
	}
	return false
}

// shallowInputSchema is the light listing's input schema: an object whose
// properties are the registered schema's top-level property names, each with
// its JSON type when the registered schema states one, and the registered
// top-level required set. No nested schema, description, enum or $defs: a
// valid JSON Schema object MCP requires on every tool, strictly looser than
// the registered one, so a client that validates against it never refuses a
// call the server would accept.
func shallowInputSchema(schema any) map[string]any {
	root := schemaObject(schema)
	props := map[string]any{}
	if registered, ok := root["properties"].(map[string]any); ok {
		for name, sub := range registered {
			entry := map[string]any{}
			if typ, ok := schemaObject(sub)["type"]; ok {
				switch typ.(type) {
				case string, []any, []string:
					entry["type"] = typ
				}
			}
			props[name] = entry
		}
	}
	out := map[string]any{"type": "object", "properties": props}
	switch required := root["required"].(type) {
	case []string:
		if len(required) > 0 {
			out["required"] = required
		}
	case []any:
		if len(required) > 0 {
			out["required"] = required
		}
	}
	return out
}

// schemaObject reads a schema as a JSON object. Hand-built schemas already
// are one; an SDK-inferred *jsonschema.Schema goes through its own JSON form
// (the form the SDK publishes), once per tool since the result is cached.
// A schema with no object form (true, false) reads as empty.
func schemaObject(schema any) map[string]any {
	if m, ok := schema.(map[string]any); ok {
		return m
	}
	raw, err := jsonx.Marshal(schema)
	if err != nil {
		return nil
	}
	var m map[string]any
	if jsonx.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

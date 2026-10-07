package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Review 2026-10-06, F23, the owner's decision (a Russian string quoted as
// data: «нужно сделать очень легкий запрос на описание всех методов и
// точечный запрос на описание конкретного метода подробно»): tools/list is a
// light summary, describe_tool the detailed answer for one tool.

// registeredSurface is the oracle every test below compares against: the
// tools exactly as registered, listed by a fresh server that carries NO
// tools/list middleware, through the SDK's own in-memory client. It is
// independent of toolCatalog by construction — nothing here calls it — so
// a catalog that dropped or rewrote a field cannot agree with itself.
var registeredSurface = sync.OnceValues(func() (map[string]map[string]any, error) {
	srv := sdk.NewServer(&sdk.Implementation{Name: "oracle", Version: "0"}, nil)
	registerTools(srv, newLoopback(fixtureCaller{}))
	addConfigTools(srv, testConfig())
	addDescribeTools(srv, &toolCatalog{})
	ctx := context.Background()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = serverSession.Wait() }()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "oracle-client", Version: "0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()
	out := map[string]map[string]any{}
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(tool)
		if err != nil {
			return nil, err
		}
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, err
		}
		out[tool.Name] = decoded
	}
	return out, nil
})

// describedSurface is every tool's FULL form as the protocol serves it
// since F23: one describe_tool answer per name the light tools/list
// publishes, wrapped in a tools/list-shaped envelope
// ({"result":{"tools":[...]}}) and kept as raw bytes, so the many tests
// that read descriptions and input schemas from that envelope keep reading
// the full text, numbers digit for digit. Descriptions do not depend on the
// Caller, so one build serves every test.
var describedSurface = sync.OnceValues(func() ([]byte, error) {
	handler := newToolFixture(&fakeCaller{status: http.StatusOK, body: []byte(`{}`)}).Handler()
	post := func(body string) ([]byte, error) {
		req := httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+testKey)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			return nil, fmt.Errorf("status %d: %s", rec.Code, rec.Body)
		}
		return rec.Body.Bytes(), nil
	}
	raw, err := post(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	if err != nil {
		return nil, err
	}
	var list struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteString(`{"jsonrpc":"2.0","id":1,"result":{"tools":[`)
	for i, tool := range list.Result.Tools {
		raw, err := post(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"describe_tool","arguments":{"name":` + strconvQuote(tool.Name) + `}}}`)
		if err != nil {
			return nil, err
		}
		var call struct {
			Result struct {
				IsError           bool            `json:"isError"`
				StructuredContent json.RawMessage `json:"structuredContent"`
			} `json:"result"`
		}
		if err := json.Unmarshal(raw, &call); err != nil {
			return nil, err
		}
		if call.Result.IsError || len(call.Result.StructuredContent) == 0 {
			return nil, fmt.Errorf("describe_tool %s: %s", tool.Name, raw)
		}
		if i > 0 {
			out.WriteByte(',')
		}
		out.Write(call.Result.StructuredContent)
	}
	out.WriteString(`]}}`)
	return out.Bytes(), nil
})

// describedToolsList returns describedSurface as a response recorder, the
// shape the tools/list tests already read.
func describedToolsList(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	body, err := describedSurface()
	if err != nil {
		t.Fatalf("described surface: %v", err)
	}
	rec := httptest.NewRecorder()
	rec.Body.Write(body)
	return rec
}

func oracleTools(t *testing.T) map[string]map[string]any {
	t.Helper()
	tools, err := registeredSurface()
	if err != nil {
		t.Fatalf("oracle listing: %v", err)
	}
	return tools
}

type listedTool struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema any            `json:"outputSchema"`
	Annotations  map[string]any `json:"annotations"`
}

func lightToolsList(t *testing.T) ([]listedTool, int) {
	t.Helper()
	rec := doMCP(t, newTestEndpoint(t).Handler(), `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`,
		map[string]string{"Authorization": "Bearer " + testKey})
	var env struct {
		Result struct {
			Tools      []listedTool `json:"tools"`
			NextCursor string       `json:"nextCursor"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	if env.Result.NextCursor != "" {
		t.Fatalf("tools/list paginated (%q); the tests below assume one page", env.Result.NextCursor)
	}
	return env.Result.Tools, rec.Body.Len()
}

// describe calls describe_tool and decodes its structured answer.
func describe(t *testing.T, name string) (map[string]any, string) {
	t.Helper()
	raw, msg := callTool(t, &recordingCaller{status: 200, body: []byte(`{}`)}, "describe_tool", `{"name":`+strconvQuote(name)+`}`)
	if msg != "" {
		return nil, msg
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode describe_tool: %v", err)
	}
	return out, ""
}

func strconvQuote(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

func requiredSet(schema map[string]any) []string {
	list, _ := schema["required"].([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, item.(string))
	}
	sort.Strings(out)
	return out
}

func propertyNames(schema map[string]any) []string {
	props, _ := schema["properties"].(map[string]any)
	out := make([]string, 0, len(props))
	for name := range props {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// TestToolsListIsALightSummary pins the light listing: the whole answer
// under maxToolsListBytes, no outputSchema, a short description, and an
// input schema one level deep whose property names, stated types and
// required set are the registered schema's own.
func TestToolsListIsALightSummary(t *testing.T) {
	tools, size := lightToolsList(t)
	t.Logf("tools/list: %d bytes, %d tools", size, len(tools))
	if size > maxToolsListBytes {
		t.Errorf("tools/list is %d bytes, cap %d", size, maxToolsListBytes)
	}
	oracle := oracleTools(t)
	if len(tools) != len(oracle) {
		t.Errorf("tools/list has %d tools, registered %d", len(tools), len(oracle))
	}
	for _, tool := range tools {
		full, ok := oracle[tool.Name]
		if !ok {
			t.Errorf("%s is listed but not registered", tool.Name)
			continue
		}
		if tool.OutputSchema != nil {
			t.Errorf("%s: the light listing carries an outputSchema", tool.Name)
		}
		if n := utf8.RuneCountInString(tool.Description); n == 0 || n > summaryMaxRunes {
			t.Errorf("%s: summary is %d runes: %q", tool.Name, n, tool.Description)
		}
		if !reflect.DeepEqual(tool.Annotations, mapOrNil(full["annotations"])) {
			t.Errorf("%s: annotations %v, registered %v", tool.Name, tool.Annotations, full["annotations"])
		}
		if tool.InputSchema["type"] != "object" {
			t.Errorf("%s: shallow schema type %v", tool.Name, tool.InputSchema["type"])
		}
		for key := range tool.InputSchema {
			if key != "type" && key != "properties" && key != "required" {
				t.Errorf("%s: shallow schema carries %q", tool.Name, key)
			}
		}
		fullSchema := full["inputSchema"].(map[string]any)
		if got, want := propertyNames(tool.InputSchema), propertyNames(fullSchema); !slices.Equal(got, want) {
			t.Errorf("%s: properties %v, registered %v", tool.Name, got, want)
		}
		if got, want := requiredSet(tool.InputSchema), requiredSet(fullSchema); !slices.Equal(got, want) {
			t.Errorf("%s: required %v, registered %v", tool.Name, got, want)
		}
		fullProps, _ := fullSchema["properties"].(map[string]any)
		props, _ := tool.InputSchema["properties"].(map[string]any)
		for name, value := range props {
			entry := value.(map[string]any)
			for key := range entry {
				if key != "type" {
					t.Errorf("%s.%s: shallow property carries %q", tool.Name, name, key)
				}
			}
			fullProp, _ := fullProps[name].(map[string]any)
			if !reflect.DeepEqual(entry["type"], fullProp["type"]) {
				t.Errorf("%s.%s: type %v, registered %v", tool.Name, name, entry["type"], fullProp["type"])
			}
		}
	}
}

func mapOrNil(value any) map[string]any {
	m, _ := value.(map[string]any)
	return m
}

// TestDescribeToolRoundTripsTheFullTool proves describe_tool hands back
// every registered tool whole — the backend tools whose expanded contract
// schemas are the reason F23 exists, the older tools (struct-inferred and
// hand-built schemas) and describe_tool itself: the full description, the
// input schema that inlining $defs turns back into the registered one, the
// output schema, the annotations and the routes toolRoutes names.
func TestDescribeToolRoundTripsTheFullTool(t *testing.T) {
	oracle := oracleTools(t)
	names := make([]string, 0, len(oracle))
	for name := range oracle {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			got, msg := describe(t, name)
			if msg != "" {
				t.Fatalf("describe_tool %s: %s", name, msg)
			}
			full := oracle[name]
			if got["name"] != name || got["description"] != full["description"] {
				t.Errorf("name/description differ:\n got %v\nwant %v", got["description"], full["description"])
			}
			input := got["inputSchema"].(map[string]any)
			want := full["inputSchema"].(map[string]any)
			if want["$defs"] == nil {
				input = inlineLocalDefs(input)
			}
			if !reflect.DeepEqual(input, want) {
				t.Error("inputSchema does not round-trip to the registered one")
			}
			if !reflect.DeepEqual(got["outputSchema"], full["outputSchema"]) {
				t.Error("outputSchema differs from the registered one")
			}
			if !reflect.DeepEqual(got["annotations"], full["annotations"]) {
				t.Errorf("annotations %v, registered %v", got["annotations"], full["annotations"])
			}
			routes := []string{}
			for _, route := range got["routes"].([]any) {
				routes = append(routes, route.(string))
			}
			if !slices.Equal(routes, toolRoutes[name]) {
				t.Errorf("routes %v, toolRoutes %v", routes, toolRoutes[name])
			}
		})
	}
	// The backend schema really is shared through $defs in this copy — the
	// form schema_compact.go measured — not the 900 KB expansion.
	got, _ := describe(t, "start_backend_analysis")
	if got["inputSchema"].(map[string]any)["$defs"] == nil {
		t.Error("start_backend_analysis' described schema is not $defs-compacted")
	}
}

// TestDescribeToolKeepsExactNumbers: a schema bound such as an int64
// maximum must reach the client digit for digit. The SDK's typed-tool path
// re-marshals an object result through float64 after validating it, which
// turned 9223372036854775807 into 9223372036854775808 — so describe_tool is
// a raw tool that writes its own JSON (jsonx keeps the integer exact). Its
// arguments are still validated: a missing name, an unknown member and a
// wrong type are refused.
func TestDescribeToolKeepsExactNumbers(t *testing.T) {
	raw, msg := callTool(t, &recordingCaller{status: 200, body: []byte(`{}`)}, "describe_tool", `{"name":"abort_backend_import"}`)
	if msg != "" {
		t.Fatal(msg)
	}
	if !strings.Contains(string(raw), `"maximum":9223372036854775807`) {
		t.Errorf("int64 maximum rounded in describe_tool's schema: %.400s", raw)
	}
	for _, args := range []string{`{}`, `{"name":"get_guide","extra":1}`, `{"name":1}`} {
		if _, msg := callTool(t, &recordingCaller{}, "describe_tool", args); msg == "" {
			t.Errorf("describe_tool accepted %s", args)
		}
	}
}

// TestDescribeToolUnknownNameSuggests:an unknown name is a tool error that
// names the closest existing tools, and nothing is called.
func TestDescribeToolUnknownNameSuggests(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{"describe", "describe_tool"},
		{"backend_analysis", "start_backend_analysis"},
		{"GET_GUIDE ", "get_guide"},
		{"workspace_settings", "update_workspace_settings"},
	} {
		_, msg := describe(t, tc.query)
		if msg == "" || !strings.Contains(msg, "unknown tool") || !strings.Contains(msg, tc.want) {
			t.Errorf("describe_tool %q: %q, want an unknown-tool error naming %s", tc.query, msg, tc.want)
		}
	}
	_, msg := describe(t, "zzzz")
	if !strings.Contains(msg, "unknown tool") || !strings.Contains(msg, "tools/list") {
		t.Errorf("describe_tool zzzz: %q, want an unknown-tool error pointing at tools/list", msg)
	}
}

// TestWrongNestedArgumentStillRefused: the published schema is shallow, but
// what each handler validates is the REGISTERED one, so a call whose error
// sits below the top level is still refused before any admin route is
// reached — for a backend tool (validated by its own compiled contract
// schema) and for a typed SDK tool (validated by the SDK against the schema
// it resolved at AddTool). Each case has a control that differs only in the
// nested value and does reach the route.
func TestWrongNestedArgumentStillRefused(t *testing.T) {
	id := backendTestID
	proposal := `{"proposalId":"` + id + `","proposalRevisionId":"` + id + `"}`
	backend := `{"projectId":"` + id + `","kind":"conformance","changeProposal":` + proposal + `,"resultRevisionId":"` + id + `","identityMap":[],"testAttachments":[],"limits":{},"observationMode":"none","idempotencyKey":"key"}`
	settings := `{"seed":1,"basePath":"","basePathValues":[],"listSize":3,"nullRate":0,"envelope":null,` +
		`"identity":{"id":1,"name":"a","email":"a@example.com","roles":[]},` +
		`"auth":{"jwtTtlSec":60,"alg":"HS256","signingKey":"k","requireHeader":false},` +
		`"cors":{"mode":"mirror","credentials":false},"validateRequests":false,"delayMs":0}`
	typed := `{"workspaceId":1,"editVersion":1,"settings":` + settings + `}`
	for _, tc := range []struct{ tool, good, bad string }{
		{"start_backend_analysis", backend, strings.Replace(backend, `"proposalId":"`+id+`"`, `"proposalId":42`, 1)},
		{"update_workspace_settings", typed, strings.Replace(typed, `"seed":1`, `"seed":"one"`, 1)},
		{"update_workspace_settings", typed, strings.Replace(typed, `"jwtTtlSec":60`, `"jwtTtlSec":"sixty"`, 1)},
	} {
		control := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, _ = callTool(t, control, tc.tool, tc.good)
		if control.method == "" {
			t.Fatalf("%s: the control call never reached a route; the case proves nothing", tc.tool)
		}
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, msg := callTool(t, calls, tc.tool, tc.bad)
		if msg == "" || calls.method != "" {
			t.Errorf("%s accepted a wrong nested argument: msg=%q, called %s %s", tc.tool, msg, calls.method, calls.path)
		}
	}
}

func TestSummarizeDescription(t *testing.T) {
	long := strings.Repeat("word ", 60)
	for _, tc := range []struct{ in, want string }{
		{"Lists workspaces. Then more.", "Lists workspaces."},
		{"First line\nsecond line", "First line"},
		{"Uses e.g. a thing. Next.", "Uses e.g. a thing."},
		{"Version 1.5 is here. Next.", "Version 1.5 is here."},
		{"No terminator at all", "No terminator at all"},
		{long, strings.TrimSpace(strings.Repeat("word ", 40)) + "…"},
		{strings.Repeat("ab ", 66) + "abcdef", strings.TrimSpace(strings.Repeat("ab ", 66)) + "…"},
		{strings.Repeat("x", 300), strings.Repeat("x", summaryMaxRunes-1) + "…"},
	} {
		got := summarizeDescription(tc.in)
		if got != tc.want {
			t.Errorf("summarizeDescription(%.40q) = %q, want %q", tc.in, got, tc.want)
		}
		if utf8.RuneCountInString(got) > summaryMaxRunes {
			t.Errorf("summary of %.40q is over the cap: %d runes", tc.in, utf8.RuneCountInString(got))
		}
	}
}

// TestToolsListSummariesAreUnique: the light listing is what a model picks a
// tool from, so two tools with the same summary are indistinguishable there
// until describe_tool is called for each. Before this test every backend
// family registered its tools in a loop over one shared Description, and
// F23's first-sentence cut turned that into one identical summary per
// family (all analysis tools read "Static, project-owned analysis of exact
// immutable targets."). Each family row now carries its own summary
// sentence; this is the guard for the next family.
func TestToolsListSummariesAreUnique(t *testing.T) {
	tools, _ := lightToolsList(t)
	byText := map[string][]string{}
	for _, tool := range tools {
		byText[tool.Description] = append(byText[tool.Description], tool.Name)
	}
	texts := make([]string, 0, len(byText))
	for text, names := range byText {
		if len(names) > 1 {
			texts = append(texts, text)
		}
	}
	sort.Strings(texts)
	dup := 0
	for _, text := range texts {
		dup += len(byText[text])
		t.Errorf("%d tools share the summary %q: %v", len(byText[text]), text, byText[text])
	}
	if dup > 0 {
		t.Logf("%d of %d tools have a non-unique summary", dup, len(tools))
	}
}

// TestFamilySummaryIsTheWholeFirstSentence: a family tool's description is
// its own summary sentence followed by the shared family text, and the light
// listing must cut it exactly between the two. An abbreviation or a ". "
// inside a summary would make firstSentenceEnd cut early (half a summary in
// tools/list); a summary without its closing period would run the family's
// first sentence into it. Either way light+" "+family stops equalling the
// registered description.
func TestFamilySummaryIsTheWholeFirstSentence(t *testing.T) {
	families := []string{
		backendAnalysisFamily, backendMaterializationFamily, backendObservationFamily,
		backendReplayFamily, backendDiagramFamily, backendPortableFamily, backendChangeProposalFamily,
	}
	tools, _ := lightToolsList(t)
	oracle := oracleTools(t)
	seen := map[string]int{}
	for _, tool := range tools {
		full, _ := oracle[tool.Name]["description"].(string)
		for _, family := range families {
			if !strings.HasSuffix(full, " "+family) {
				continue
			}
			seen[family]++
			if tool.Description+" "+family != full {
				t.Errorf("%s: light summary %q is not the whole sentence before the family text in %q", tool.Name, tool.Description, full)
			}
			if !strings.HasSuffix(tool.Description, ".") || utf8.RuneCountInString(tool.Description) > 150 {
				t.Errorf("%s: summary %q must be one sentence of at most 150 runes", tool.Name, tool.Description)
			}
		}
	}
	for _, family := range families {
		if seen[family] == 0 {
			t.Errorf("no listed tool carries the family text %.60q", family)
		}
	}
}

package responserules

import (
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func entityRuleWire(t *testing.T, nodes, edges string) Rule {
	t.Helper()
	var r Rule
	raw := `{"id":"r","name":"Entities","binding":{"method":"GET","path":"/orders"},"nodes":[{"id":"s","type":"start","name":"Start","x":0,"y":0},` + nodes + `],"edges":[{"id":"e0","from":"s","port":"next","to":"a"},` + edges + `]}`
	if err := jsonx.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

const entityResponse = `{"id":"z","type":"response","name":"Result","x":0,"y":0,"response":{"status":200,"mediaType":"application/json","headers":[],"bodyFrom":{"source":"result","nodeId":"a"}}}`

func TestEntitySimulationCreatePrecisionAndRepeatability(t *testing.T) {
	r := entityRuleWire(t, `{"id":"a","type":"entity_create","name":"Create","x":0,"y":0,"entity":{"family":"/orders","data":{"source":"body"}}},`+entityResponse, `{"id":"e1","from":"a","port":"next","to":"z"}`)
	var q Request
	if err := jsonx.Unmarshal([]byte(`{"query":[],"headers":[],"bodyJSON":"{\"amount\":9007199254740993,\"decimal\":1.00,\"id\":99}","entities":[{"family":"/orders","idField":"id","idType":"integer","rows":[]}]}`), &q); err != nil {
		t.Fatal(err)
	}
	first := run(t, r, q)
	second := run(t, r, q)
	if !first.Valid || first.Response == nil || first.Response.BodyJSON == nil {
		t.Fatalf("%+v", first)
	}
	if got := *first.Response.BodyJSON; got != `{"amount":9007199254740993,"decimal":1.00,"id":1}` {
		t.Fatalf("body %s", got)
	}
	a, _ := jsonx.Marshal(first)
	b, _ := jsonx.Marshal(second)
	if string(a) != string(b) {
		t.Fatal("simulation changed initial fixture")
	}
	if !strings.Contains(string(a), `\"amount\":9007199254740993`) {
		t.Fatalf("result lost precision: %s", a)
	}
}
func TestEntityRefsStrictWire(t *testing.T) {
	for _, raw := range []string{
		`{"source":"literal","valueJSON":"null","name":"extra"}`,
		`{"source":"body","pointer":"/bad~2"}`,
		`{"source":"result","nodeId":"x","valueJSON":"1"}`,
		`{"source":"path"}`,
	} {
		var n Node
		err := jsonx.Unmarshal([]byte(`{"id":"a","type":"entity_create","name":"Create","x":0,"y":0,"entity":{"family":"/orders","data":`+raw+`}}`), &n)
		if err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func TestEntityReadMissingBranch(t *testing.T) {
	r := entityRuleWire(t, `{"id":"a","type":"entity_read","name":"Get","x":0,"y":0,"entity":{"family":"/orders","operation":"get","key":{"source":"query","name":"id"}}},`+entityResponse+`,{"id":"missing","type":"response","name":"Missing","x":0,"y":0,"response":{"status":404,"mediaType":"application/json","headers":[],"bodyJSON":"null"}}`, `{"id":"e1","from":"a","port":"found","to":"z"},{"id":"e2","from":"a","port":"missing","to":"missing"}`)
	var q Request
	if err := jsonx.Unmarshal([]byte(`{"query":[{"name":"id","value":"42"}],"headers":[],"entities":[{"family":"/orders","idField":"id","idType":"integer","rows":[]}]}`), &q); err != nil {
		t.Fatal(err)
	}
	s := run(t, r, q)
	if !s.Valid || s.TerminalNodeID != "missing" {
		t.Fatalf("%+v", s)
	}
}
func TestEntityWriteAdmissionRejectsFallback(t *testing.T) {
	r := entityRuleWire(t, `{"id":"a","type":"entity_create","name":"Create","x":0,"y":0,"entity":{"family":"/orders","data":{"source":"literal","valueJSON":"{}"}}},{"id":"f","type":"fallback","name":"Fallback","x":0,"y":0}`, `{"id":"e1","from":"a","port":"next","to":"f"}`)
	s := run(t, r, fixture())
	if s.Valid {
		t.Fatal("write admitted fallback")
	}
	found := false
	for _, d := range s.Diagnostics {
		if d.Code == "unsafe_entity_terminal" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing diagnostic: %+v", s.Diagnostics)
	}
}

func TestEntityUpdateKeepsUntouchedNumbersAndIdentity(t *testing.T) {
	r := entityRuleWire(t, `{"id":"a","type":"entity_update","name":"Update","x":0,"y":0,"entity":{"family":"/orders","key":{"source":"literal","valueJSON":"\"7\""},"data":{"source":"body"}}},`+entityResponse+`,{"id":"missing","type":"response","name":"Missing","x":0,"y":0,"response":{"status":404,"mediaType":"application/json","headers":[]}}`, `{"id":"e1","from":"a","port":"found","to":"z"},{"id":"e2","from":"a","port":"missing","to":"missing"}`)
	var q Request
	if err := jsonx.Unmarshal([]byte(`{"query":[],"headers":[],"bodyJSON":"{\"state\":\"ready\",\"id\":99}","entities":[{"family":"/orders","idField":"id","idType":"integer","rows":[{"key":"7","scope":[],"dataJSON":"{\"id\":7,\"amount\":9007199254740993,\"scale\":1.00}"}]}]}`), &q); err != nil {
		t.Fatal(err)
	}
	s := run(t, r, q)
	if s.Response == nil || *s.Response.BodyJSON != `{"amount":9007199254740993,"id":7,"scale":1.00,"state":"ready"}` {
		t.Fatalf("%+v", s)
	}
}
func TestEntityListScopedInheritedAndExactPointers(t *testing.T) {
	r := entityRuleWire(t, `{"id":"a","type":"entity_read","name":"List","x":0,"y":0,"entity":{"family":"/orgs/{}/orders","operation":"list"}},`+entityResponse, `{"id":"e1","from":"a","port":"next","to":"z"}`)
	r.Binding.Path = "/orgs/{org}/orders"
	root := rootDocument()
	root["paths"].(map[string]any)[r.Binding.Path] = root["paths"].(map[string]any)["/orders"]
	var q Request
	if err := jsonx.Unmarshal([]byte(`{"query":[],"headers":[],"path":[{"name":"org","value":"7"}],"entities":[{"family":"/orgs/{}/orders","idField":"id","idType":"integer","rows":[{"key":"1","scope":["7"],"dataJSON":"{\"id\":1,\"a/b\":{\"~key\":9007199254740993}}"},{"key":"2","scope":["8"],"dataJSON":"{\"id\":2}"}]}]}`), &q); err != nil {
		t.Fatal(err)
	}
	r.Nodes[2].Response.BodyFrom = &ValueRef{Source: "result", NodeID: "a", Pointer: "/0/a~1b/~0key"}
	s, err := Simulate(t.Context(), Envelope{FormatVersion: 1, Rules: []Rule{r}}, r.ID, root, q)
	if err != nil || s.Response == nil || *s.Response.BodyJSON != "9007199254740993" {
		t.Fatalf("%+v %v", s, err)
	}
}
func TestEntityResultRefsRequireFoundDominance(t *testing.T) {
	r := entityRuleWire(t, `{"id":"a","type":"entity_read","name":"Get","x":0,"y":0,"entity":{"family":"/orders","operation":"get","key":{"source":"literal","valueJSON":"1"}}},`+entityResponse, `{"id":"e1","from":"a","port":"found","to":"z"},{"id":"e2","from":"a","port":"missing","to":"z"}`)
	v, err := Validate(t.Context(), Envelope{FormatVersion: 1, Rules: []Rule{r}}, r.ID, rootDocument())
	if err != nil || v.Valid {
		t.Fatalf("%+v %v", v, err)
	}
	if len(v.Diagnostics) == 0 || v.Diagnostics[0].Code != "missing_result_reference" {
		t.Fatalf("%+v", v.Diagnostics)
	}
}

func TestEntitySimulationInheritsParentDetailScope(t *testing.T) {
	r := entityRuleWire(t, `{"id":"a","type":"entity_read","name":"List","x":0,"y":0,"entity":{"family":"/orgs/{}/orders","operation":"list"}},`+entityResponse, `{"id":"e1","from":"a","port":"next","to":"z"}`)
	r.Binding.Path = "/orgs/{org}"
	root := rootDocument()
	root["paths"].(map[string]any)[r.Binding.Path] = root["paths"].(map[string]any)["/orders"]
	q := fixture()
	q.Path = []Field{{Name: "org", Value: "7"}}
	q.Entities = []EntityFixture{{Family: "/orgs/{}/orders", IDField: "id", IDType: "integer", Rows: []EntityFixtureRow{
		{Key: "1", Scope: []string{"7"}, DataJSON: `{"id":1,"name":"selected"}`},
		{Key: "2", Scope: []string{"8"}, DataJSON: `{"id":2,"name":"other"}`},
	}}}
	s, err := Simulate(t.Context(), Envelope{FormatVersion: 1, Rules: []Rule{r}}, r.ID, root, q)
	if err != nil || s.Response == nil || s.Response.BodyJSON == nil || *s.Response.BodyJSON != `[{"id":1,"name":"selected"}]` {
		t.Fatalf("parent detail inherited scope: simulation=%+v err=%v", s, err)
	}
}
func TestEntityWriteAdmissionRejectsMixedBodyClasses(t *testing.T) {
	r := entityRuleWire(t, `{"id":"a","type":"entity_update","name":"Update","x":0,"y":0,"entity":{"family":"/orders","key":{"source":"literal","valueJSON":"1"},"data":{"source":"literal","valueJSON":"{}"}}},`+entityResponse+`,{"id":"empty","type":"response","name":"Empty","x":0,"y":0,"response":{"status":204,"mediaType":"application/json","headers":[]}}`, `{"id":"e1","from":"a","port":"found","to":"z"},{"id":"e2","from":"a","port":"missing","to":"empty"}`)
	v, err := Validate(t.Context(), Envelope{FormatVersion: 1, Rules: []Rule{r}}, r.ID, rootDocument())
	if err != nil || v.Valid {
		t.Fatalf("%+v %v", v, err)
	}
}
func TestEntityFixtureRejectsInvalidScopeAndDuplicates(t *testing.T) {
	for _, entities := range []string{
		`[ {"family":"/orders","idField":"id","idType":"integer","rows":[{"key":"007","scope":[],"dataJSON":"{}"}]}]`,
		`[ {"family":"/orgs/{}/orders","idField":"id","idType":"integer","rows":[{"key":"1","scope":[],"dataJSON":"{}"}]}]`,
		`[ {"family":"/orders","idField":"id","idType":"integer","rows":[{"key":"1","scope":[],"dataJSON":"{}"},{"key":"1","scope":[],"dataJSON":"{}"}]}]`,
	} {
		var q Request
		if err := jsonx.Unmarshal([]byte(`{"query":[],"headers":[],"entities":`+entities+`}`), &q); err != nil {
			t.Fatal(err)
		}
		if _, err := Simulate(t.Context(), Envelope{FormatVersion: 1, Rules: []Rule{baseRule()}}, "r", rootDocument(), q); err == nil {
			t.Fatalf("accepted %s", entities)
		}
	}
}
func TestEntityLargeListWithinResultBudget(t *testing.T) {
	r := entityRuleWire(t, `{"id":"a","type":"entity_read","name":"List","x":0,"y":0,"entity":{"family":"/orders","operation":"list"}},{"id":"z","type":"response","name":"OK","x":0,"y":0,"response":{"status":200,"mediaType":"application/json","headers":[],"bodyJSON":"null"}}`, `{"id":"e1","from":"a","port":"next","to":"z"}`)
	q := fixture()
	q.Entities = []EntityFixture{{Family: "/orders", IDField: "id", IDType: "integer", Rows: []EntityFixtureRow{{Key: "1", Scope: []string{}, DataJSON: `{"text":"` + strings.Repeat("a", 35000) + `"}`}, {Key: "2", Scope: []string{}, DataJSON: `{"text":"` + strings.Repeat("b", 35000) + `"}`}}}}
	s, err := Simulate(t.Context(), Envelope{FormatVersion: 1, Rules: []Rule{r}}, "r", rootDocument(), q)
	if err != nil || len(s.Results["a"]) < 70000 {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestEntitySimulationSharesSequenceAcrossScopes(t *testing.T) {
	r := entityRuleWire(t, `{"id":"a","type":"entity_create","name":"Create","x":0,"y":0,"entity":{"family":"/orgs/{}/orders","scope":[{"source":"literal","valueJSON":"\"8\""}],"data":{"source":"literal","valueJSON":"{}"}}},`+entityResponse, `{"id":"e1","from":"a","port":"next","to":"z"}`)
	q := fixture()
	q.Entities = []EntityFixture{{Family: "/orgs/{}/orders", IDField: "id", IDType: "string", Rows: []EntityFixtureRow{{Key: "11", Scope: []string{"7"}, DataJSON: `{"id":"11"}`}}}}
	s := run(t, r, q)
	if *s.Response.BodyJSON != `{"id":"12"}` || s.Entities[0].Rows[1].Scope[0] != "8" {
		t.Fatalf("%+v", s)
	}
}
func TestEntityBodyFromRefRoundtripAndBodyStatus(t *testing.T) {
	raw := `{"status":200,"mediaType":"application/json","headers":[],"bodyFrom":{"source":"literal","valueJSON":"9007199254740993"}}`
	var response Response
	if err := jsonx.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatal(err)
	}
	encoded, err := jsonx.Marshal(response)
	if err != nil || string(encoded) != raw {
		t.Fatalf("%s %v", encoded, err)
	}
	rule := baseRule()
	rule.Nodes[1] = Node{ID: "f", Type: "response", Name: "Response", Response: &response}
	s := run(t, rule, fixture())
	if s.Response == nil || *s.Response.BodyJSON != "9007199254740993" {
		t.Fatalf("%+v", s)
	}
	response.Status = 204
	v, err := Validate(t.Context(), Envelope{FormatVersion: 1, Rules: []Rule{rule}}, "r", rootDocument())
	if err != nil || v.Valid {
		t.Fatalf("%+v %v", v, err)
	}
}
func TestEntityAdmissionExposesCopy(t *testing.T) {
	r := entityRuleWire(t, `{"id":"a","type":"entity_create","name":"Create","x":0,"y":0,"entity":{"family":"/orders","data":{"source":"literal","valueJSON":"{}"}}},`+entityResponse, `{"id":"e1","from":"a","port":"next","to":"z"}`)
	p := newProgram(r)
	if !p.HasEntities() || p.EntityAdmission() == nil || p.EntityAdmission().NoBody || p.EntityAdmission().MediaType != "application/json" {
		t.Fatal("missing write admission")
	}
	p.EntityAdmission().MediaType = "text/html"
	if p.EntityAdmission().MediaType != "application/json" {
		t.Fatal("admission alias escaped")
	}
}

func TestProgramEntityFamiliesAreStableAndIsolated(t *testing.T) {
	p := newProgram(Rule{Nodes: []Node{
		{ID: "first", Entity: &EntityOperation{Family: "/widgets"}},
		{ID: "second", Entity: &EntityOperation{Family: "/orgs/{}/orders"}},
		{ID: "repeat", Entity: &EntityOperation{Family: "/widgets"}},
	}})
	families := p.EntityFamilies()
	if strings.Join(families, ",") != "/orgs/{}/orders,/widgets" {
		t.Fatalf("families=%v", families)
	}
	families[0] = "/changed"
	if strings.Join(p.EntityFamilies(), ",") != "/orgs/{}/orders,/widgets" {
		t.Fatal("family list aliases program state")
	}
}
func TestEntityFixtureStrictWire(t *testing.T) {
	for _, bad := range []string{
		`{"family":"/orders","idField":"id","idType":"integer","rows":null}`,
		`{"family":"/orders","idField":"id","idType":"integer","rows":[{"key":"1","scope":null,"dataJSON":"{}"}]}`,
		`{"family":"/orders","idField":"id","idType":"integer","rows":[],"unexpected":true}`,
	} {
		var q Request
		if err := jsonx.Unmarshal([]byte(`{"query":[],"headers":[],"entities":[`+bad+`]}`), &q); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestEntityRuntimeErrorNamesArrayPointer(t *testing.T) {
	r := entityRuleWire(t, `{"id":"a","type":"entity_read","name":"Get","x":0,"y":0,"entity":{"family":"/orders","operation":"get","key":{"source":"query","name":"missing"}}},{"id":"yes","type":"response","name":"Yes","x":0,"y":0,"response":{"status":200,"mediaType":"application/json","headers":[]}},{"id":"no","type":"response","name":"No","x":0,"y":0,"response":{"status":404,"mediaType":"application/json","headers":[]}}`, `{"id":"e1","from":"a","port":"found","to":"yes"},{"id":"e2","from":"a","port":"missing","to":"no"}`)
	_, err := newProgram(r).EvaluateWithEntities(t.Context(), EvaluationInput{}, EvaluationOptions{Entities: &fixtureEntities{}})
	field, ok := err.(*FieldError)
	if !ok || field.Pointer != "/nodes/1/entity/key" {
		t.Fatalf("%v", err)
	}
}

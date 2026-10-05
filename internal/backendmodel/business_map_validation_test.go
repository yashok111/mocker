package backendmodel

import (
	"encoding/json/v2"
	"os"
	"slices"
	"testing"
)

func businessMapRaw(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("testdata/diagrams/business_map.json")
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err = json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	return d
}
func businessMapDecode(t *testing.T, raw map[string]any) DiagramDocument {
	t.Helper()
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var d DiagramDocument
	if err = json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}
func TestBusinessMapIntentWithoutMessage(t *testing.T) {
	d := businessMapDecode(t, businessMapRaw(t))
	r, _ := testRepo(t)
	p := createProject(t, r, "business-intent")
	d.Target = BackendReadTarget{RevisionID: p.CurrentRevisionID}
	v, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: d, IdempotencyKey: "intent"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(v.Gaps, func(g DiagramGap) bool {
		return g.SubjectID == "47000000-0000-4000-8000-000000000003" && g.Code == "authored_unresolved"
	}) {
		t.Fatal("missing implementation gap")
	}
	q, err := r.QueryDiagram(t.Context(), p.ID, DiagramQueryInput{Pin: v.Pin, Section: "elements", Origin: "all", Limit: 100})
	if err != nil || q.Total != 8 {
		t.Fatalf("elements: %+v %v", q, err)
	}
}
func TestBusinessMapRoleMatrix(t *testing.T) {
	raw := businessMapRaw(t)
	businessMapDecode(t, raw)
	p := raw["payload"].(map[string]any)
	link := p["links"].([]any)[0].(map[string]any)
	link["to"] = "47000000-0000-4000-8000-000000000006"
	link["relation"] = "produces"
	b, _ := json.Marshal(raw)
	var d DiagramDocument
	if json.Unmarshal(b, &d) == nil {
		t.Fatal("actor produces read model admitted")
	}
}
func TestBusinessMapLayoutNotCausality(t *testing.T) {
	raw := businessMapRaw(t)
	a := businessMapDecode(t, raw)
	p := raw["payload"].(map[string]any)
	slices.Reverse(p["elements"].([]any))
	b := businessMapDecode(t, raw)
	a, err := normalizeDiagram(a)
	if err != nil {
		t.Fatal(err)
	}
	b, err = normalizeDiagram(b)
	if err != nil {
		t.Fatal(err)
	}
	ah, _ := requestDigest(a)
	bh, _ := requestDigest(b)
	if ah != bh {
		t.Fatal("card order changed semantics")
	}
}
func TestBusinessMapCyclesNotExecution(t *testing.T) {
	raw := businessMapRaw(t)
	p := raw["payload"].(map[string]any)
	p["links"].([]any)[3].(map[string]any)["to"] = "47000000-0000-4000-8000-000000000002"
	d := businessMapDecode(t, raw)
	r, db := testRepo(t)
	project := createProject(t, r, "cycles")
	d.Target = BackendReadTarget{RevisionID: project.CurrentRevisionID}
	var before, after int
	_ = db.R.QueryRowContext(t.Context(), "SELECT count(*) FROM backend_analysis_jobs").Scan(&before)
	if _, err := r.CreateDiagram(t.Context(), project.ID, DiagramCreateInput{Document: d, IdempotencyKey: "cycle"}); err != nil {
		t.Fatal(err)
	}
	if err := db.R.QueryRowContext(t.Context(), "SELECT count(*) FROM backend_analysis_jobs").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("map scheduled jobs")
	}
}
func TestBusinessMapStrictWire(t *testing.T) {
	businessMapDecode(t, businessMapRaw(t))
	for _, key := range []string{"execute", "layout", "unknown"} {
		t.Run(key, func(t *testing.T) {
			raw := businessMapRaw(t)
			raw["payload"].(map[string]any)[key] = true
			b, _ := json.Marshal(raw)
			var d DiagramDocument
			if json.Unmarshal(b, &d) == nil {
				t.Fatal("unknown payload field accepted")
			}
		})
	}
	for _, value := range []any{nil, "unknown", 2} {
		raw := businessMapRaw(t)
		raw["kind"] = value
		b, _ := json.Marshal(raw)
		var d DiagramDocument
		if json.Unmarshal(b, &d) == nil {
			t.Fatal("unknown kind accepted")
		}
	}
}

func TestBusinessMapNonMessageImplementationGap(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "business-non-message")
	s := runtimeQueryFixture(t)
	s.Revision.ProjectID = p.ID
	s.Revision.ParentRevisionID = new(p.CurrentRevisionID)
	runtimeQueryPersist(t, r, s)
	d := businessMapDecode(t, businessMapRaw(t))
	d.Target = BackendReadTarget{RevisionID: s.Revision.ID}
	d.BusinessMap.Elements[2].Refs = []DiagramRef{{Kind: "record", RecordType: "node", ID: runtimeQueryID(1)}}
	v, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: d, IdempotencyKey: "non-message"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(v.Gaps, func(g DiagramGap) bool {
		return g.SubjectID == d.BusinessMap.Elements[2].ID && g.Code == "unresolved_business_message"
	}) {
		t.Fatal("technical operation falsely establishes business event message implementation")
	}
}

func TestBusinessMapIndependentOracle(t *testing.T) {
	var expected struct {
		EventID    string   `json:"eventId"`
		QuestionID string   `json:"questionId"`
		Elements   int      `json:"elements"`
		Links      int      `json:"links"`
		EventCount int      `json:"eventCount"`
		Relations  []string `json:"relations"`
	}
	loadDiagramFixture(t, "business_map_expected.json", &expected)
	d := businessMapDecode(t, businessMapRaw(t))
	p := d.BusinessMap
	if len(p.Elements) != expected.Elements || len(p.Links) != expected.Links {
		t.Fatal("independent counts")
	}
	count := 0
	for _, e := range p.Elements {
		if e.Role == "business_event" {
			count++
		}
		if e.ID == expected.EventID && e.Label != "OrderPaid" {
			t.Fatal("event identity")
		}
		if e.ID == expected.QuestionID && e.Role != "question" {
			t.Fatal("question promoted")
		}
	}
	if count != expected.EventCount {
		t.Fatal("event count")
	}
	for i, l := range p.Links {
		if l.Relation != expected.Relations[i] {
			t.Fatal("relation oracle")
		}
	}
}

func TestBusinessMapClosedRolePairs(t *testing.T) {
	roles := []string{"actor", "command", "business_event", "policy", "read_model", "question"}
	relations := []string{"initiates", "produces", "reacts_to", "issues", "updates", "reads", "questions"}
	allowed := map[string]bool{"actor/command/initiates": true, "command/business_event/produces": true, "business_event/policy/reacts_to": true, "policy/command/issues": true, "business_event/read_model/updates": true, "actor/read_model/reads": true, "command/read_model/reads": true, "policy/read_model/reads": true}
	for _, role := range roles {
		allowed["question/"+role+"/questions"] = true
	}
	for _, from := range roles {
		for _, to := range roles {
			for _, relation := range relations {
				d := businessMapDecode(t, businessMapRaw(t))
				p := d.BusinessMap
				p.Elements = p.Elements[:2]
				p.Elements[0].Role = from
				p.Elements[1].Role = to
				p.Links = p.Links[:1]
				p.Links[0].Relation = relation
				if (d.Validate() == nil) != allowed[from+"/"+to+"/"+relation] {
					t.Fatalf("matrix %s/%s/%s", from, to, relation)
				}
			}
		}
	}
}

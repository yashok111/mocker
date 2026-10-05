package backendmodel

import (
	"encoding/json/v2"
	"os"
	"slices"
	"strings"
	"testing"
)

func lifecycleFixture(t *testing.T) DiagramDocument {
	t.Helper()
	b, err := os.ReadFile("testdata/diagrams/lifecycle.json")
	if err != nil {
		t.Fatal(err)
	}
	var d DiagramDocument
	if err = json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestLifecycleFixtureAdmission(t *testing.T) {
	d := lifecycleFixture(t)
	expectedRaw, err := os.ReadFile("testdata/diagrams/lifecycle_expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected struct {
		States      []string `json:"states"`
		Transitions []string `json:"transitions"`
		Rules       []string `json:"rules"`
	}
	if err = json.Unmarshal(expectedRaw, &expected); err != nil {
		t.Fatal(err)
	}
	ids := func() ([]string, []string, []string) {
		a, b, c := []string{}, []string{}, []string{}
		for _, v := range d.Lifecycle.States {
			a = append(a, v.ID)
		}
		for _, v := range d.Lifecycle.Transitions {
			b = append(b, v.ID)
		}
		for _, v := range d.Lifecycle.Rules {
			c = append(c, v.ID)
		}
		return a, b, c
	}
	a, b, c := ids()
	if !slices.Equal(a, expected.States) || !slices.Equal(b, expected.Transitions) || !slices.Equal(c, expected.Rules) {
		t.Fatal("independent fixture IDs differ")
	}

	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "external approval is unknown") || !strings.Contains(string(raw), "forbidden") {
		t.Fatal("authored semantics lost")
	}
}

func TestLifecycleStrictWire(t *testing.T) {
	d := lifecycleFixture(t)
	raw, _ := json.Marshal(d)
	for _, bad := range []string{
		strings.Replace(string(raw), `"coverage":"partial"`, `"coverage":"partial","simulate":true`, 1),
		strings.Replace(string(raw), `"guard":{"kind":"none"}`, `"guard":{"kind":"none","text":"ignored"}`, 1),
		strings.Replace(string(raw), `"stateFields":[`, `"stateFields":null,"discard":[`, 1),
	} {
		var out DiagramDocument
		if json.Unmarshal([]byte(bad), &out) == nil {
			t.Fatal("invalid union accepted", bad)
		}
	}
}

func TestLifecycleEnumHasNoTransitions(t *testing.T) {
	d := lifecycleFixture(t)
	d.Lifecycle.Transitions = []LifecycleTransition{}
	d.Lifecycle.Rules = []LifecycleRule{}
	n, err := normalizeDiagram(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Lifecycle.Transitions) != 0 {
		t.Fatal("enum invented transitions")
	}
}
func TestLifecycleOpaqueGuard(t *testing.T) {
	d := lifecycleFixture(t)
	n, err := normalizeDiagram(d)
	if err != nil {
		t.Fatal(err)
	}
	if n.Lifecycle.Transitions[1].Guard.Text != "external approval is unknown" {
		t.Fatal("guard changed")
	}
}
func TestLifecycleRuleConflict(t *testing.T) {
	d := lifecycleFixture(t)
	rule := d.Lifecycle.Rules[0]
	rule.ID = "46000000-0000-4000-8000-000000000021"
	rule.Verdict = "allowed"
	d.Lifecycle.Rules = append(d.Lifecycle.Rules, rule)
	if d.Validate() == nil {
		t.Fatal("conflict accepted")
	}
}
func TestLifecycleLosslessValueAndCompoundMapping(t *testing.T) {
	d := lifecycleFixture(t)
	for _, raw := range []string{"9007199254740993", "9223372036854775808", "null", "1e1000"} {
		d.Lifecycle.States[0].Value = &LifecycleValue{JSON: raw}
		n, err := normalizeDiagram(d)
		if err != nil {
			t.Fatal(err)
		}
		if n.Lifecycle.States[0].Value.JSON != raw {
			t.Fatal("scalar rounded")
		}
	}
	for _, raw := range []string{"[]", "{}", "0 1", "NaN"} {
		d.Lifecycle.States[0].Value = &LifecycleValue{JSON: raw}
		if d.Validate() == nil {
			t.Fatal("non-scalar accepted")
		}
	}
	d = lifecycleFixture(t)
	ref := d.Lifecycle.StateFields[0]
	ref.ID = "46000000-0000-4000-8000-000000000103"
	d.Lifecycle.StateFields = append(d.Lifecycle.StateFields, ref)
	if d.Validate() == nil {
		t.Fatal("inferred compound mapping")
	}
	d.Lifecycle.CompoundMappingReason = "Explicit paired mapping"
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
}
func TestLifecycleProjectionAndReferences(t *testing.T) {
	d := lifecycleFixture(t)
	g := &EffectiveGraphSnapshot{State: RevisionState{Nodes: []Node{{ID: d.Lifecycle.Entity.ID, Kind: "table"}, {ID: d.Lifecycle.StateFields[0].ID, Kind: "column", ParentID: new(d.Lifecycle.Entity.ID)}, {ID: d.Lifecycle.Rules[0].Trigger.ID, Kind: "http_operation"}}}}
	gaps, err := resolveDiagramEvidence(t.Context(), g, d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) == 0 {
		t.Fatal("partial coverage not exposed")
	}
	g.State.Nodes[1].Kind = "http_operation"
	if _, err = resolveDiagramEvidence(t.Context(), g, d, nil); err == nil {
		t.Fatal("wrong semantic field kind accepted")
	}
}

func TestLifecycleHistoricalRefRolesCannotChange(t *testing.T) {
	d := lifecycleFixture(t)
	previous := &DiagramVersion{Document: d}
	raw, _ := json.Marshal(d)
	var changed DiagramDocument
	if err := json.Unmarshal(raw, &changed); err != nil {
		t.Fatal(err)
	}
	changed.Lifecycle.Entity = d.Lifecycle.StateFields[0]
	changed.Lifecycle.StateFields = []DiagramRef{d.Lifecycle.Entity}
	if _, err := resolveDiagramEvidence(t.Context(), &EffectiveGraphSnapshot{}, changed, previous); err == nil {
		t.Fatal("historical entity/field roles exchanged")
	}
	changed = lifecycleFixture(t)
	historical := DiagramRef{Kind: "record", RecordType: "node", ID: "46000000-0000-4000-8000-000000000777"}
	d.Lifecycle.Transitions[0].Refs = []DiagramRef{historical}
	previous.Document = d
	changed.Lifecycle.Transitions[0].Triggers = []DiagramRef{historical}
	if _, err := resolveDiagramEvidence(t.Context(), &EffectiveGraphSnapshot{}, changed, previous); err == nil {
		t.Fatal("historical generic ref promoted to trigger")
	}
}

package apidesign

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/resourcemap"
	"github.com/yashok111/mocker/internal/statediagram"
)

func TestImpactAcceptanceSchemaChainAndCycle(t *testing.T) {
	before := `{"openapi":"3.1.0","paths":{"/orders":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/A"}}}}}}}},"components":{"schemas":{"A":{"allOf":[{"$ref":"#/components/schemas/B"}]},"B":{"type":"object","properties":{"child":{"$ref":"#/components/schemas/C"}}},"C":{"type":"object","properties":{"root":{"$ref":"#/components/schemas/A"},"value":{"type":"string"}}}}}}`
	after := strings.Replace(before, `"value":{"type":"string"}`, `"value":{"type":"number"}`, 1)
	result := impactAnalyze(t, before, after)
	if !result.Complete || len(result.Changes) != 1 {
		t.Fatalf("finite supported cycle must complete: changes=%d coverage=%+v diagnostics=%+v", len(result.Changes), result.Coverage, result.Diagnostics)
	}
	wantSites := []ImpactReferenceSite{
		{Pointer: "/components/schemas/B/properties/child/$ref", TargetPointer: "/components/schemas/C", Kind: "ref"},
		{Pointer: "/components/schemas/A/allOf/0/$ref", TargetPointer: "/components/schemas/B", Kind: "ref"},
		{Pointer: "/paths/~1orders/get/responses/200/content/application~1json/schema/$ref", TargetPointer: "/components/schemas/A", Kind: "ref"},
	}
	for _, side := range []string{"before", "after"} {
		evidence := impactOperationEvidence(result, "/orders", side)
		if len(evidence) != 1 || evidence[0].Direction != "response" || !slices.Equal(evidence[0].ReferenceSites, wantSites) {
			t.Fatalf("%s: expected the finite three-reference consumer chain, got %+v", side, evidence)
		}
	}
	for _, name := range []string{"A", "B", "C"} {
		if !slices.ContainsFunc(result.Affected, func(entity ImpactEntity) bool {
			return entity.Kind == "schema" && entity.Label == name && entity.Before != nil && entity.After != nil
		}) {
			t.Errorf("schema %s is missing from the dependency chain", name)
		}
	}
	first := impactAcceptanceJSON(t, result)
	for range 3 {
		next := impactAcceptanceJSON(t, impactAnalyze(t, before, after))
		if !bytes.Equal(first, next) {
			t.Fatal("schema cycle produced nondeterministic evidence")
		}
	}
}

func TestImpactAcceptanceFinalizerEntityLimit(t *testing.T) {
	report := ImpactReport{ImpactAnalysis: ImpactAnalysis{
		Complete: true,
		Changes:  []ImpactChange{{ID: "change", Pointer: "/components/schemas/Shared", Kind: "changed", ChangeClass: "contract", Compatibility: "review"}},
	}}
	for i := range 5001 {
		id := fmt.Sprintf("entity-%04d", i)
		report.Affected = append(report.Affected, ImpactEntity{ID: id, Kind: "schema", Label: id})
		report.Evidence = append(report.Evidence, ImpactEvidence{ID: "proof-" + id, ChangeID: "change", EntityID: id, Side: "after", Direction: "response"})
	}
	report.Diagnostics = []ImpactDiagnostic{{Code: "test_warning", Severity: "warning", Side: "after", EntityID: "entity-5000"}}
	if err := FinalizeImpactReport(t.Context(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Complete || len(report.Affected) != 5000 || len(report.Evidence) != 5000 || !slices.Equal(report.Coverage.TruncatedReasons, []string{"entities"}) {
		t.Fatalf("entity limit: entities=%d evidence=%d complete=%v coverage=%+v", len(report.Affected), len(report.Evidence), report.Complete, report.Coverage)
	}
	if len(report.Diagnostics) != 1 || report.Diagnostics[0].EntityID != "" {
		t.Fatalf("diagnostic must remain without a dangling entity: %+v", report.Diagnostics)
	}
	impactAcceptanceAssertIntegrity(t, report)
}

func TestImpactAcceptanceFinalizerEvidenceLimit(t *testing.T) {
	report := ImpactReport{ImpactAnalysis: ImpactAnalysis{Complete: true}}
	for entity := range 20 {
		id := fmt.Sprintf("entity-%02d", entity)
		report.Affected = append(report.Affected, ImpactEntity{ID: id, Kind: "schema", Label: id})
	}
	for change := range 251 {
		id := fmt.Sprintf("change-%03d", change)
		report.Changes = append(report.Changes, ImpactChange{ID: id, Pointer: "/components/schemas/" + id, Kind: "changed", ChangeClass: "contract", Compatibility: "review"})
		for _, entity := range report.Affected {
			for _, side := range []string{"before", "after"} {
				report.Evidence = append(report.Evidence, ImpactEvidence{ID: id + "-" + entity.ID + "-" + side, ChangeID: id, EntityID: entity.ID, Side: side, Direction: "response"})
			}
		}
	}
	if err := FinalizeImpactReport(t.Context(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Complete || len(report.Changes) != 251 || len(report.Affected) != 20 || len(report.Evidence) != 10000 || !slices.Equal(report.Coverage.TruncatedReasons, []string{"evidence"}) {
		t.Fatalf("evidence limit: changes=%d entities=%d evidence=%d complete=%v coverage=%+v", len(report.Changes), len(report.Affected), len(report.Evidence), report.Complete, report.Coverage)
	}
	impactAcceptanceAssertIntegrity(t, report)
}

func TestImpactAcceptanceSharedTraversalBudget(t *testing.T) {
	t.Run("both documents count toward the same budget", func(t *testing.T) {
		// Each document is below the 200000-visit budget by itself. Together,
		// their opaque data alone exhausts the budget before dependency discovery.
		raw := `{"x-data":[` + strings.Repeat("0,", 110000) + `0]}`
		if single := impactAnalyze(t, `{}`, raw); !single.Complete {
			t.Fatalf("one document must fit the budget: %+v", single.Coverage)
		}
		result := impactAnalyze(t, raw, raw)
		if result.Complete || !slices.Contains(result.Coverage.TruncatedReasons, "traversal") {
			t.Fatalf("document traversal was not bounded jointly: %+v", result.Coverage)
		}
	})
	t.Run("dependency propagation shares the budget", func(t *testing.T) {
		paths := map[string]any{}
		for i := range 1000 {
			paths[fmt.Sprintf("/resource-%04d", i)] = map[string]any{"get": map[string]any{
				"responses": map[string]any{"200": map[string]any{"content": map[string]any{
					"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Shared"}},
				}}},
			}}
		}
		before := string(impactAcceptanceJSON(t, map[string]any{
			"paths":      paths,
			"components": map[string]any{"schemas": map[string]any{"Shared": map[string]any{"type": "string"}}},
		}))
		after := strings.Replace(before, `"type":"string"`, `"type":"number"`, 1)
		if unchanged := impactAnalyze(t, before, before); !unchanged.Complete {
			t.Fatalf("fixture exceeds the budget before propagation: %+v", unchanged.Coverage)
		}
		result := impactAnalyze(t, before, after)
		if result.Complete || !slices.Equal(result.Coverage.TruncatedReasons, []string{"traversal"}) || len(result.Changes) != 1 {
			t.Fatalf("propagation budget: changes=%d complete=%v coverage=%+v", len(result.Changes), result.Complete, result.Coverage)
		}
		if len(result.Evidence) == 0 {
			t.Fatal("fixture must reach propagation and retain discovered dependencies")
		}
		impactAcceptanceAssertIntegrity(t, ImpactReport{ImpactAnalysis: result})
	})
}

func TestImpactAcceptanceAuxiliaryProjectionFailuresPreserveContract(t *testing.T) {
	resources := make([]resourcemap.Resource, 0, 201)
	for i := range 201 {
		resources = append(resources, resourcemap.Resource{ID: fmt.Sprintf("r%d", i), Name: "Resource", OperationKeys: []string{}})
	}
	diagrams := make([]statediagram.Diagram, 0, 21)
	for i := range 21 {
		diagrams = append(diagrams, statediagram.Diagram{ID: fmt.Sprintf("d%d", i), Name: "Diagram", States: []statediagram.State{}, Transitions: []statediagram.Transition{}})
	}
	for _, test := range []struct {
		name, extension, diagnostic string
		value                       any
	}{
		{"invalid resource projection", resourcemap.Extension, "resource_projection_failed", map[string]any{"formatVersion": 99, "resources": []any{}, "relations": []any{}}},
		{"resource projection overflow", resourcemap.Extension, "resource_projection_failed", map[string]any{"formatVersion": 1, "resources": resources, "relations": []any{}}},
		{"invalid state projection", statediagram.Extension, "state_projection_failed", statediagram.Envelope{FormatVersion: 99, Diagrams: []statediagram.Diagram{}}},
		{"state projection overflow", statediagram.Extension, "state_projection_failed", statediagram.Envelope{FormatVersion: 1, Diagrams: diagrams}},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := `{"paths":{"/orders":{"post":{"requestBody":{"required":false,"content":{"application/json":{"schema":{"type":"object"}}}}}}},` + string(impactAcceptanceJSON(t, test.extension)) + `:` + string(impactAcceptanceJSON(t, test.value)) + `}`
			after := strings.Replace(before, `"required":false`, `"required":true`, 1)
			result := impactAnalyze(t, before, after)
			if result.Complete || len(result.Changes) != 1 || result.Changes[0].Compatibility != "breaking" || result.Changes[0].Pointer != "/paths/~1orders/post/requestBody/required" {
				t.Fatalf("projection failure lost contract result: complete=%v changes=%+v diagnostics=%+v", result.Complete, result.Changes, result.Diagnostics)
			}
			for _, side := range []string{"before", "after"} {
				if !slices.ContainsFunc(result.Diagnostics, func(d ImpactDiagnostic) bool {
					return d.Code == test.diagnostic && d.Side == side && d.Pointer == "/"+test.extension
				}) {
					t.Errorf("%s projection warning missing: %+v", side, result.Diagnostics)
				}
				if len(impactOperationEvidence(result, "/orders", side)) == 0 {
					t.Errorf("%s operation evidence lost with auxiliary projection", side)
				}
			}
		})
	}
}

func impactAcceptanceJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := jsonx.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func impactAcceptanceAssertIntegrity(t *testing.T, report ImpactReport) {
	t.Helper()
	changes, entities, evidenceIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, change := range report.Changes {
		if changes[change.ID] {
			t.Fatalf("duplicate change ID %q", change.ID)
		}
		changes[change.ID] = true
	}
	for _, entity := range report.Affected {
		if entities[entity.ID] {
			t.Fatalf("duplicate entity ID %q", entity.ID)
		}
		entities[entity.ID] = true
	}
	for _, evidence := range report.Evidence {
		if evidenceIDs[evidence.ID] || !changes[evidence.ChangeID] || !entities[evidence.EntityID] || evidence.ReferenceSites == nil {
			t.Fatalf("duplicate, dangling or malformed evidence: %+v", evidence)
		}
		evidenceIDs[evidence.ID] = true
	}
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.EntityID != "" && !entities[diagnostic.EntityID] {
			t.Fatalf("dangling diagnostic: %+v", diagnostic)
		}
	}
	if report.Coverage.ChangesReturned != len(report.Changes) || report.Coverage.EntitiesReturned != len(report.Affected) || report.Coverage.EvidenceReturned != len(report.Evidence) {
		t.Fatalf("returned counts disagree with retained items: %+v", report.Coverage)
	}
	if size := len(impactAcceptanceJSON(t, report)); size > 4<<20 {
		t.Fatalf("report exceeds response limit: %d bytes", size)
	}
}

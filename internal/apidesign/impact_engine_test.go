package apidesign

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func impactAnalyze(t *testing.T, before, after string) ImpactAnalysis {
	t.Helper()
	result, err := AnalyzeImpactDocuments(t.Context(), before, after)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func impactOperationEvidence(result ImpactAnalysis, path, side string) []ImpactEvidence {
	ids := map[string]bool{}
	for _, entity := range result.Affected {
		locator := entity.Before
		if side == "after" {
			locator = entity.After
		}
		if entity.Kind == "operation" && locator != nil && locator.Path == path {
			ids[entity.ID] = true
		}
	}
	out := []ImpactEvidence{}
	for _, evidence := range result.Evidence {
		if ids[evidence.EntityID] && evidence.Side == side {
			out = append(out, evidence)
		}
	}
	return out
}
func TestImpactEngineDeletedSchemaKeepsOldConsumers(t *testing.T) {
	before := `{"openapi":"3.1.0","paths":{"/orders":{"get":{"x-mocker-canvas-operation-id":"get-orders","responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Order"}}}}}}}},"components":{"schemas":{"Order":{"properties":{"id":{"type":"string"}}}}}}`
	after := strings.Replace(before, `"Order":{"properties":{"id":{"type":"string"}}}`, ``, 1)
	result := impactAnalyze(t, before, after)
	evidence := impactOperationEvidence(result, "/orders", "before")
	if len(evidence) != 1 || evidence[0].Direction != "response" || len(evidence[0].ReferenceSites) != 1 {
		t.Fatalf("old consumer missing: %+v", result)
	}
	if result.Complete {
		t.Fatal("dangling reference must make candidate incomplete")
	}
}
func TestImpactEngineRequiredSetsAndReadOnly(t *testing.T) {
	for _, test := range []struct{ name, from, to, property, want string }{
		{"reorder", `["a","b"]`, `["b","a"]`, `{}`, "compatible"},
		{"remove", `["a","b"]`, `["b"]`, `{}`, "compatible"},
		{"add", `["a"]`, `["a","b"]`, `{}`, "breaking"},
		{"readOnly", `["a"]`, `["a","b"]`, `{"readOnly":true}`, "review"},
	} {
		t.Run(test.name, func(t *testing.T) {
			template := `{"openapi":"3.0.3","paths":{"/orders":{"post":{"requestBody":{"content":{"application/json":{"schema":{"type":"object","properties":{"a":{},"b":%s},"required":%s}}}}}}}}`
			result := impactAnalyze(t, fmt.Sprintf(template, test.property, test.from), fmt.Sprintf(template, test.property, test.to))
			if len(result.Changes) != 1 || result.Changes[0].Compatibility != test.want {
				t.Fatalf("required rule: %+v", result.Changes)
			}
		})
	}
}
func TestImpactEngineMetadataUsesStructuralContext(t *testing.T) {
	before := `{"components":{"schemas":{"A":{"description":"old","properties":{"description":{"type":"string"},"example":{"type":"string"},"x-id":{"type":"string"}}}}}}`
	after := strings.ReplaceAll(before, "string", "number")
	result := impactAnalyze(t, before, after)
	for _, c := range result.Changes {
		if c.ChangeClass != "contract" {
			t.Fatalf("property classified metadata: %+v", c)
		}
	}
	result = impactAnalyze(t, before, strings.Replace(before, "old", "new", 1))
	if len(result.Changes) != 1 || result.Changes[0].ChangeClass != "metadata" || len(result.Evidence) != 0 {
		t.Fatalf("description metadata: %+v", result)
	}
}
func TestImpactEngineSharedPathOverridesAndParameters(t *testing.T) {
	before := `{"paths":{"/a":{"$ref":"#/components/pathItems/Shared","get":{"parameters":[{"name":"q","in":"query","schema":{"type":"string"}}],"responses":{}}},"/b":{"$ref":"#/components/pathItems/Shared"}},"components":{"pathItems":{"Shared":{"get":{"responses":{"200":{"description":"OK","content":{"application/json":{"schema":{"$ref":"#/components/schemas/A"}}}}}},"parameters":[{"name":"q","in":"query","schema":{"$ref":"#/components/schemas/A"}}]}},"schemas":{"A":{"type":"string"}}}}`
	result := impactAnalyze(t, before, strings.Replace(before, `"A":{"type":"string"}`, `"A":{"type":"number"}`, 1))
	if len(impactOperationEvidence(result, "/a", "after")) != 0 {
		t.Fatalf("overridden fields reached local operation: %+v", result)
	}
	if len(impactOperationEvidence(result, "/b", "after")) != 2 {
		t.Fatalf("expected request and response consumers: %+v", result)
	}
}
func TestImpactEngineIdentityDoesNotInventHTTPRemoval(t *testing.T) {
	before := `{"paths":{"/a":{"get":{"x-mocker-canvas-operation-id":"a"}},"/b":{"get":{"x-mocker-canvas-operation-id":"b"}}}}`
	after := `{"paths":{"/a":{"get":{"x-mocker-canvas-operation-id":"b"}},"/b":{"get":{"x-mocker-canvas-operation-id":"a"}}}}`
	result := impactAnalyze(t, before, after)
	for _, c := range result.Changes {
		if c.Compatibility != "review" || c.ChangeClass != "metadata" {
			t.Fatalf("key swaps: %+v", c)
		}
	}
	if len(result.Evidence) == 0 {
		t.Fatal("identity needs usage evidence")
	}
}
func TestImpactEngineRootSecurityAndServersRespectOverride(t *testing.T) {
	before := `{"security":[{"Auth":[]}],"servers":[{"url":"https://old.test"}],"paths":{"/a":{"get":{}},"/b":{"servers":[{"url":"https://own.test"}],"get":{"security":[]}}},"components":{"securitySchemes":{"Auth":{"type":"http","scheme":"bearer"}}}}`
	after := strings.ReplaceAll(before, "old.test", "new.test")
	after = strings.Replace(after, "bearer", "basic", 1)
	result := impactAnalyze(t, before, after)
	if len(impactOperationEvidence(result, "/a", "after")) != 2 || len(impactOperationEvidence(result, "/b", "after")) != 0 {
		t.Fatalf("inheritance: %+v", result)
	}
}
func TestImpactEngineExactValuesNullAndBounds(t *testing.T) {
	result := impactAnalyze(t, `{"x-n":9007199254740993,"x-null":null}`, `{"x-n":9007199254740995}`)
	if result.Changes[0].BeforeJSON == nil || *result.Changes[0].BeforeJSON != "9007199254740993" {
		t.Fatalf("number changed: %+v", result)
	}
	if result.Changes[1].BeforeJSON == nil || *result.Changes[1].BeforeJSON != "null" || result.Changes[1].AfterJSON != nil {
		t.Fatalf("null lost: %+v", result)
	}
	values := map[string]any{}
	for i := range 600 {
		values[fmt.Sprintf("x-%04d", i)] = i
	}
	raw, _ := jsonx.Marshal(values)
	result = impactAnalyze(t, `{}`, string(raw))
	if result.Complete || len(result.Changes) != 500 || len(result.Coverage.TruncatedReasons) == 0 {
		t.Fatalf("changes unbounded: %+v", result.Coverage)
	}
}
func TestImpactEngineCancellationAndStrictJSON(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := AnalyzeImpactDocuments(ctx, `{}`, `{}`)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	for _, raw := range []string{`[]`, `null`, `{} {}`, `openapi: 3.1.0`} {
		if _, err := AnalyzeImpactDocuments(t.Context(), `{}`, raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestImpactEngineDuplicateKeysAndTokenBoundaries(t *testing.T) {
	before := `{"paths":{"/a":{"get":{"x-mocker-canvas-operation-id":"duplicate","responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/A/properties/id"}}}}}}},"/b":{"get":{"x-mocker-canvas-operation-id":"duplicate"}}},"components":{"schemas":{"A":{"properties":{"id":{"type":"string"},"identity":{"type":"string"}}}}}}`
	result := impactAnalyze(t, before, strings.Replace(before, `"identity":{"type":"string"}`, `"identity":{"type":"number"}`, 1))
	if len(impactOperationEvidence(result, "/a", "after")) != 0 {
		t.Fatalf("pointer prefix crossed token: %+v", result)
	}
	result = impactAnalyze(t, before, strings.Replace(before, `"id":{"type":"string"}`, `"id":{"type":"number"}`, 1))
	for _, entity := range result.Affected {
		if entity.Kind == "operation" && entity.After != nil && entity.After.OperationKey != "" {
			t.Fatalf("ambiguous key exposed for joins: %+v", entity)
		}
	}
	if result.Complete {
		t.Fatal("duplicate key must be diagnosed")
	}
}
func TestImpactEngineUnknownScopeAndPresentationExtensions(t *testing.T) {
	before := `{"components":{"schemas":{"A":{"$id":"https://example.test/A","$ref":"#/components/schemas/B"},"B":{"type":"string"}}},"x-mocker-schema-layout":{"schemas":{"A":{"x":1,"y":2}},"x-behavior":1}}`
	result := impactAnalyze(t, before, strings.Replace(before, `"x-behavior":1`, `"x-behavior":2`, 1))
	if result.Changes[0].Compatibility != "review" {
		t.Fatalf("unknown layout extension hidden: %+v", result.Changes)
	}
	if result.Complete || len(result.Diagnostics) == 0 {
		t.Fatal("scope uncertainty lost")
	}
}
func TestImpactEngineDepthAndExcerpts(t *testing.T) {
	raw := `{"x-deep":` + strings.Repeat(`{"child":`, 150) + `1` + strings.Repeat(`}`, 150) + `}`
	result := impactAnalyze(t, `{}`, raw)
	if result.Complete {
		t.Fatal("deep inserted object escaped traversal bound")
	}
	result = impactAnalyze(t, `{}`, `{"x-big":"`+strings.Repeat("a", 5000)+`"}`)
	if len(result.Changes) != 1 || !result.Changes[0].AfterTruncated || result.Changes[0].AfterJSON != nil || !result.Complete {
		t.Fatalf("excerpt truncation should retain complete analysis: %+v", result)
	}
}
func TestImpactEngineRequiredMissingOrReadOnlyAncestor(t *testing.T) {
	for _, schema := range []string{
		`{"type":"object","properties":{},"required":["missing"]}`,
		`{"type":"object","properties":{"nested":{"readOnly":true,"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}}}`,
	} {
		after := `{"paths":{"/a":{"post":{"requestBody":{"content":{"application/json":{"schema":` + schema + `}}}}}}}`
		before := strings.Replace(after, `"required":["missing"]`, `"required":[]`, 1)
		before = strings.Replace(before, `"required":["value"]`, `"required":[]`, 1)
		result := impactAnalyze(t, before, after)
		if result.Changes[0].Compatibility != "review" {
			t.Fatalf("ambiguous required became breaking: %+v", result.Changes)
		}
	}
}

func TestImpactEngineNestedExamplesAndResourceCoordinatesAreMetadata(t *testing.T) {
	before := `{"components":{"schemas":{"A":{"example":{"description":"old","items":[1,2]}}}},"x-mocker-resource-map":{"formatVersion":1,"resources":[{"id":"r","name":"Orders","service":"","description":"","operationKeys":[],"x":0,"y":0}],"relations":[]}}`
	after := strings.Replace(before, `"old"`, `"new"`, 1)
	after = strings.Replace(after, `[1,2]`, `[1,3]`, 1)
	after = strings.Replace(after, `"x":0`, `"x":20`, 1)
	result := impactAnalyze(t, before, after)
	for _, change := range result.Changes {
		if change.ChangeClass != "metadata" || change.Compatibility != "compatible" {
			t.Fatalf("presentation propagated: %+v", change)
		}
	}
	if len(result.Evidence) != 0 {
		t.Fatalf("presentation evidence: %+v", result)
	}
}
func TestImpactEngineResourceAndStateBindings(t *testing.T) {
	before := `{"paths":{"/a":{"get":{"x-mocker-canvas-operation-id":"get-a"}}},"x-mocker-resource-map":{"formatVersion":1,"resources":[{"id":"r","name":"Orders","service":"","description":"","operationKeys":["get-a"],"x":0,"y":0},{"id":"r2","name":"Other","service":"","description":"","operationKeys":[],"x":0,"y":0}],"relations":[{"id":"link","fromResourceId":"r","toResourceId":"r2","label":"dependency annotation"}]},"x-mocker-state-diagrams":{"formatVersion":1,"diagrams":[{"id":"d","name":"Order","initialStateId":"a","states":[{"id":"a","name":"Start","x":0,"y":0,"terminal":false}],"transitions":[{"id":"tr","name":"Get","from":"a","to":"a","binding":{"method":"get","path":"/a"},"patchJSON":"{}","responseStatus":200}]}]}}`
	after := strings.Replace(before, `"/a":{"get"`, `"/b":{"get"`, 1)
	result := impactAnalyze(t, before, after)
	resource, state, missing := false, false, false
	for _, e := range result.Affected {
		if e.Kind == "resource" {
			if e.Label == "Other" {
				t.Fatal("resource annotation propagated impact")
			}
			resource = true
		}
		if e.Kind == "state_transition" {
			state = true
			if e.After == nil {
				t.Fatal("retained transition lost candidate locator")
			}
		}
	}
	for _, d := range result.Diagnostics {
		if d.Code == "state_binding_missing_operation" {
			missing = true
		}
	}
	if !resource || !state || !missing {
		t.Fatalf("usage join missing: %+v", result)
	}
}

func TestImpactEngineNoKeyFallbackAndDeterminism(t *testing.T) {
	before := `{"paths":{"/a":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/A"}}}}}}}},"components":{"schemas":{"A":{"type":"string"}}}}`
	after := strings.Replace(before, `"get":{`, `"get":{"x-mocker-canvas-operation-id":"new-key",`, 1)
	after = strings.Replace(after, `"type":"string"`, `"type":"number"`, 1)
	result := impactAnalyze(t, before, after)
	operations := 0
	for _, entity := range result.Affected {
		if entity.Kind == "operation" {
			operations++
			if entity.Before == nil || entity.After == nil {
				t.Fatalf("address fallback did not pair locators: %+v", entity)
			}
		}
	}
	if operations != 1 {
		t.Fatalf("operations: %+v", result.Affected)
	}
	first, _ := jsonx.Marshal(result)
	for range 5 {
		next, _ := jsonx.Marshal(impactAnalyze(t, before, after))
		if string(first) != string(next) {
			t.Fatal("nondeterministic report")
		}
	}
}

func TestImpactEngineRequiredUnknownParameterBaselineAndUnknownPathAddition(t *testing.T) {
	before := `{"paths":{"/a":{"get":{"parameters":[{"$ref":"https://example.test/param"}]}}}}`
	after := `{"paths":{"/a":{"get":{"parameters":[{"name":"q","in":"query","required":true}]}}}}`
	result := impactAnalyze(t, before, after)
	if result.Changes[0].Compatibility != "review" {
		t.Fatalf("unknown baseline classified breaking: %+v", result.Changes)
	}
	result = impactAnalyze(t, `{"paths":{}}`, `{"paths":{"/a":{"$ref":"https://example.test/path","get":{}}}}`)
	if result.Changes[0].Compatibility != "review" {
		t.Fatalf("uncertain path addition classified compatible: %+v", result.Changes)
	}
}
func TestImpactEngineStateCoordinatesAreMetadata(t *testing.T) {
	before := `{"x-mocker-state-diagrams":{"formatVersion":1,"diagrams":[{"id":"d","name":"Order","initialStateId":"a","states":[{"id":"a","name":"Start","x":0,"y":0,"terminal":false}],"transitions":[]}]}}`
	result := impactAnalyze(t, before, strings.Replace(before, `"x":0`, `"x":42`, 1))
	if len(result.Changes) != 1 || result.Changes[0].ChangeClass != "metadata" || len(result.Evidence) != 0 {
		t.Fatalf("state position classified contract: %+v", result)
	}
}

func TestImpactEngineInheritedEvidenceIncludesConsumerReferences(t *testing.T) {
	before := `{"paths":{"/a":{"$ref":"#/components/pathItems/A"}},"components":{"pathItems":{"A":{"$ref":"#/components/pathItems/B"},"B":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Data"}}}}}}}},"schemas":{"Data":{"type":"string"}}}}`
	result := impactAnalyze(t, before, strings.Replace(before, `"type":"string"`, `"type":"number"`, 1))
	evidence := impactOperationEvidence(result, "/a", "after")
	if len(evidence) != 1 || len(evidence[0].ReferenceSites) != 3 {
		t.Fatalf("incomplete inherited proof: %+v", evidence)
	}
	sites := evidence[0].ReferenceSites
	if sites[0].TargetPointer != "/components/schemas/Data" || sites[2].Pointer != "/paths/~1a/$ref" {
		t.Fatalf("proof order: %+v", sites)
	}
}
func TestImpactEngineMaterializedInferredResource(t *testing.T) {
	id := fmt.Sprintf("auto-%x", sha256.Sum256([]byte("/orders")))
	id = id[:len("auto-")+24]
	before := `{"paths":{"/orders":{"get":{"x-mocker-canvas-operation-id":"get"}}},"x-mocker-resource-map":{"formatVersion":1,"resources":[{"id":"` + id + `","name":"Renamed family","service":"","description":"","operationKeys":[],"x":0,"y":0}],"relations":[]}}`
	result := impactAnalyze(t, before, strings.Replace(before, `"get":{"x-mocker-canvas-operation-id":"get"}`, `"get":{"x-mocker-canvas-operation-id":"get","operationId":"new"}`, 1))
	for _, entity := range result.Affected {
		if entity.Kind == "resource" && (entity.Label != "Renamed family" || entity.After.Pointer != "/x-mocker-resource-map/resources/0") {
			t.Fatalf("materialized family ignored: %+v", entity)
		}
	}
}

func TestImpactEngineShadowedPathReferenceDoesNotReachLocalMethod(t *testing.T) {
	before := `{"paths":{"/a":{"$ref":"#/components/pathItems/A","get":{"responses":{}}}},"components":{"pathItems":{"A":{"get":{"responses":{}}},"B":{"get":{"responses":{}}}}}}`
	after := strings.Replace(before, `"$ref":"#/components/pathItems/A"`, `"$ref":"#/components/pathItems/B"`, 1)
	result := impactAnalyze(t, before, after)
	if len(impactOperationEvidence(result, "/a", "before"))+len(impactOperationEvidence(result, "/a", "after")) != 0 {
		t.Fatalf("unused reference reached local override: %+v", result.Evidence)
	}
}

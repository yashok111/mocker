package apidesign

import (
	"strings"
	"testing"
)

func TestImpactReviewInheritedIdentityChanges(t *testing.T) {
	before := `{"paths":{"/a":{"$ref":"#/components/pathItems/S","x-mocker-canvas-operation-ids":{"get":"old","post":"other"}}},"components":{"pathItems":{"S":{"get":{},"post":{}}}}}`
	for _, candidate := range []struct{ name, after string }{
		{"replace", strings.Replace(before, `"get":"old"`, `"get":"new"`, 1)},
		{"delete", strings.Replace(before, `"get":"old",`, "", 1)},
		{"remove map", strings.Replace(before, `,"x-mocker-canvas-operation-ids":{"get":"old","post":"other"}`, "", 1)},
		{"swap", strings.Replace(strings.Replace(before, `"get":"old"`, `"get":"other"`, 1), `"post":"other"`, `"post":"old"`, 1)},
	} {
		t.Run(candidate.name, func(t *testing.T) {
			result := impactAnalyze(t, before, candidate.after)
			for _, change := range result.Changes {
				if change.ChangeClass != "metadata" || change.Compatibility != "review" {
					t.Fatalf("identity reassignment hidden: %+v", change)
				}
			}
			if len(impactOperationEvidence(result, "/a", "before")) == 0 {
				t.Fatal("previous key consumers hidden")
			}
		})
	}
}
func TestImpactReviewEffectiveRequestBodyRequired(t *testing.T) {
	for _, test := range []struct {
		name, base   string
		wantBreaking bool
	}{
		{"already required", `{"$ref":"#/components/requestBodies/B"}`, false},
		{"external", `{"$ref":"https://example.test/body"}`, false},
		{"missing", `{"$ref":"#/missing"}`, false},
		{"optional", `{"required":false}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			template := `{"paths":{"/a":{"post":{"requestBody":BODY}}},"components":{"requestBodies":{"B":{"required":true,"content":{}}}}}`
			result := impactAnalyze(t, strings.Replace(template, "BODY", test.base, 1), strings.Replace(template, "BODY", `{"required":true,"content":{}}`, 1))
			breaking := false
			for _, change := range result.Changes {
				breaking = breaking || change.Compatibility == "breaking"
			}
			if breaking != test.wantBreaking {
				t.Fatalf("effective body required: %+v", result.Changes)
			}
		})
	}
}
func TestImpactReviewParameterSemanticChanges(t *testing.T) {
	before := `{"paths":{"/a":{"get":{"parameters":[{"name":"q","in":"query","description":"old","schema":{"type":"object","properties":{"x":{"type":"string"}},"required":[]}},{"name":"other","in":"header","schema":{"type":"string"}}]}}}}`
	t.Run("description", func(t *testing.T) {
		result := impactAnalyze(t, before, strings.Replace(before, `"old"`, `"new"`, 1))
		if result.Changes[0].ChangeClass != "metadata" || result.Changes[0].Compatibility != "compatible" || len(result.Evidence) != 0 {
			t.Fatalf("parameter description propagated: %+v", result)
		}
	})
	t.Run("schema required", func(t *testing.T) {
		result := impactAnalyze(t, before, strings.Replace(before, `"required":[]`, `"required":["x"]`, 1))
		if result.Changes[0].Compatibility != "breaking" {
			t.Fatalf("parameter schema requirement missed: %+v", result.Changes)
		}
	})
	t.Run("reorder and description", func(t *testing.T) {
		after := `{"paths":{"/a":{"get":{"parameters":[{"name":"other","in":"header","schema":{"type":"string"}},{"name":"q","in":"query","description":"new","schema":{"type":"object","properties":{"x":{"type":"string"}},"required":[]}}]}}}}`
		result := impactAnalyze(t, before, after)
		if result.Changes[0].ChangeClass != "metadata" || len(result.Evidence) != 0 {
			t.Fatalf("parameter identities lost: %+v", result)
		}
	})
}
func TestImpactReviewInheritedResourceSource(t *testing.T) {
	before := `{"paths":{"/a":{"$ref":"#/components/pathItems/S"}},"components":{"pathItems":{"S":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/D"}}}}}}}},"schemas":{"D":{"type":"string"}}}}`
	result := impactAnalyze(t, before, strings.Replace(before, `"type":"string"`, `"type":"number"`, 1))
	for _, entity := range result.Affected {
		if entity.Kind == "resource" && (entity.After == nil || entity.After.SourcePointer != "/components/pathItems/S/get") {
			t.Fatalf("authored source missing: %+v", entity.After)
		}
	}
}
func TestImpactReviewContractPathItemConsumers(t *testing.T) {
	for _, location := range []struct{ name, prefix, pointer string }{
		{"webhook", `"webhooks":{"event":ITEM},`, "/webhooks/event"},
		{"callback", `"components":{"callbacks":{"event":{"{$request.body#/callback}":ITEM}},"pathItems":PATHITEMS,"schemas":SCHEMAS}`, "/components/callbacks/event/{$request.body#~1callback}"},
	} {
		for _, override := range []bool{false, true} {
			t.Run(location.name+map[bool]string{false: " alias", true: " override"}[override], func(t *testing.T) {
				item := `{"$ref":"#/components/pathItems/S"}`
				if override {
					item = `{"$ref":"#/components/pathItems/S","post":{"requestBody":{"content":{"application/json":{"schema":{"type":"string"}}}}}}`
				}
				paths := `{"S":{"$ref":"#/components/pathItems/T"},"T":{"post":{"requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/D"}}}}}}}`
				schemas := `{"D":{"type":"string"}}`
				before := "{" + strings.Replace(location.prefix, "ITEM", item, 1)
				if location.name == "webhook" {
					before += `"components":{"pathItems":` + paths + `,"schemas":` + schemas + `}}`
				} else {
					before = strings.Replace(strings.Replace(before, "PATHITEMS", paths, 1), "SCHEMAS", schemas, 1) + "}"
				}
				result := impactAnalyze(t, before, strings.Replace(before, `"D":{"type":"string"}`, `"D":{"type":"number"}`, 1))
				found := false
				for _, entity := range result.Affected {
					if entity.Kind == "operation" {
						t.Fatalf("contract path became normal operation: %+v", entity)
					}
					if entity.Kind == "contract_node" && entity.After != nil && entity.After.Pointer == location.pointer {
						found = true
					}
				}
				if found == override {
					t.Fatalf("contract path alias/override wrong: %+v", result.Affected)
				}
				if !result.Complete {
					t.Fatalf("supported paths unexpectedly partial: %+v", result.Diagnostics)
				}
			})
		}
	}
}

func TestImpactReviewCallbackReferenceReachesContainingOperation(t *testing.T) {
	before := `{"paths":{"/a":{"post":{"callbacks":{"notify":{"$ref":"#/components/callbacks/C"}}}}},"components":{"callbacks":{"C":{"{$request.body#/url}":{"$ref":"#/components/pathItems/S"}}},"pathItems":{"S":{"post":{"requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/D"}}}}}}},"schemas":{"D":{"type":"string"}}}}`
	result := impactAnalyze(t, before, strings.Replace(before, `"D":{"type":"string"}`, `"D":{"type":"number"}`, 1))
	evidence := impactOperationEvidence(result, "/a", "after")
	if len(evidence) == 0 {
		t.Fatalf("callback use not propagated: %+v", result)
	}
	if len(evidence[0].ReferenceSites) != 3 {
		t.Fatalf("callback causal proof missing: %+v", evidence)
	}
}
func TestImpactReviewReorderedParameterRequirements(t *testing.T) {
	before := `{"paths":{"/a":{"get":{"parameters":[{"name":"q","in":"query","schema":{"type":"object","properties":{"x":{"type":"string"}},"required":[]}},{"name":"other","in":"header","schema":{"type":"string"}}]}}}}`
	after := `{"paths":{"/a":{"get":{"parameters":[{"name":"other","in":"header","schema":{"type":"string"}},{"name":"q","in":"query","schema":{"type":"object","properties":{"x":{"type":"string"}},"required":["x"]}}]}}}}`
	result := impactAnalyze(t, before, after)
	if result.Changes[0].Compatibility != "breaking" {
		t.Fatalf("reorder obscured required schema: %+v", result.Changes)
	}
	readOnly := strings.Replace(after, `"x":{"type":"string"}`, `"x":{"type":"string","readOnly":true}`, 1)
	result = impactAnalyze(t, before, readOnly)
	if result.Changes[0].Compatibility != "review" {
		t.Fatalf("readOnly became input requirement: %+v", result.Changes)
	}
}

func TestImpactReviewParameterRequirementUsesEffectiveConsumers(t *testing.T) {
	for _, test := range []struct {
		name, operation, afterOperation string
		want                            string
	}{
		{"shadowed", `{"parameters":[{"name":"q","in":"query","schema":{"type":"string"}}]}`, `{"parameters":[{"name":"q","in":"query","schema":{"type":"string"}}]}`, "review"},
		{"consumed", `{}`, `{}`, "breaking"},
		{"mixed consumers", `{"parameters":[{"name":"q","in":"query","schema":{"type":"string"}}]},"post":{}`, `{"parameters":[{"name":"q","in":"query","schema":{"type":"string"}}]},"post":{}`, "breaking"},
		{"previous override already required", `{"parameters":[{"name":"q","in":"query","schema":{"type":"object","properties":{"x":{"type":"string"}},"required":["x"]}}]}`, `{}`, "review"},
	} {
		t.Run(test.name, func(t *testing.T) {
			template := `{"paths":{"/a":{"parameters":[{"name":"q","in":"query","schema":{"type":"object","properties":{"x":{"type":"string"}},"required":REQUIRED}}],"get":OPERATION}}}`
			before := strings.Replace(strings.Replace(template, "REQUIRED", `[]`, 1), "OPERATION", test.operation, 1)
			after := strings.Replace(strings.Replace(template, "REQUIRED", `["x"]`, 1), "OPERATION", test.afterOperation, 1)
			result := impactAnalyze(t, before, after)
			for _, change := range result.Changes {
				if change.Pointer == "/paths/~1a/parameters" && change.Compatibility != test.want {
					t.Fatalf("ignored actual parameter consumers: %+v", change)
				}
			}
		})
	}
}

func TestImpactReviewShortestCallbackProofAcrossWeightedAliases(t *testing.T) {
	before := `{"paths":{"/a":{"post":{"callbacks":{"notify":{"$ref":"#/components/callbacks/C"}}}}},"components":{"callbacks":{"C":{"{$request.body#/url}":{"$ref":"#/components/pathItems/Z"}}},"pathItems":{"A":{"parameters":[{"name":"q","in":"query","schema":{"$ref":"#/components/schemas/D"}}]},"Z":{"$ref":"#/components/pathItems/A","post":{"requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/D"}}}}}}},"schemas":{"D":{"type":"string"}}}}`
	result := impactAnalyze(t, before, strings.Replace(before, `"D":{"type":"string"}`, `"D":{"type":"number"}`, 1))
	evidence := impactOperationEvidence(result, "/a", "after")
	if len(evidence) != 1 || len(evidence[0].ReferenceSites) != 3 {
		t.Fatalf("shorter callback path lost: %+v", evidence)
	}
	if evidence[0].ReferenceSites[0].Pointer != "/components/pathItems/Z/post/requestBody/content/application~1json/schema/$ref" {
		t.Fatalf("wrong shortest route: %+v", evidence[0].ReferenceSites)
	}
	if !result.Complete {
		t.Fatalf("small graph truncated: %+v", result.Coverage)
	}
}

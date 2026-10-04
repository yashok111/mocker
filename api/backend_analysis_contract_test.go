package api

import (
	"strings"
	"testing"
)

func TestBackendAnalysisClosedContracts(t *testing.T) {
	for _, name := range []string{"BackendAnalysisJobDetail", "BackendAnalysisResultPage", "BackendAnalysisJobPage"} {
		t.Run(name, func(t *testing.T) { _ = lineageSchemaValidator(t, name) })
	}
	validate := lineageSchemaValidator(t, "StartBackendAnalysisRequest")
	id := source6ContractID
	raw := `{"kind":"diff","fromRevisionId":"` + id + `","target":{"revisionId":"` + id + `"},"scope":{},"limits":{},"observationMode":"none","idempotencyKey":"start"}`
	if err := validate(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(raw, `"target":{`, `"target":{"importCandidate":{},`, 1), strings.Replace(raw, `"scope":{}`, `"scope":{"depth":33}`, 1), strings.Replace(raw, `"limits":{}`, `"limits":{"states":null}`, 1), strings.Replace(raw, `"observationMode":"none"`, `"observationMode":"runtime"`, 1)} {
		if validate(bad) == nil {
			t.Fatal("accepted", bad)
		}
	}
}

func TestBackendAnalysisHistoricalArtifactContextBranches(t *testing.T) {
	v := lineageSchemaValidator(t, "BackendEffectiveArtifactContext")
	legacy := `{"sourceContentHash":"` + strings.Repeat("a", 64) + `","sourceSemanticHash":"` + strings.Repeat("b", 64) + `","bindings":[]}`
	if err := v(legacy); err != nil {
		t.Fatal(err)
	}
}

func TestBackendAnalysisSourcePinsAllowHistoricalContentHash(t *testing.T) {
	validate := lineageSchemaValidator(t, "BackendAnalysisSourcePins")
	hash := strings.Repeat("a", 64)
	raw := `{"revisionId":"` + source6ContractID + `","semanticHash":"` + hash + `","contentHash":"","sourceVectorHash":"` + hash + `","sourceSnapshotIds":["` + source6ContractID + `"]}`
	if err := validate(raw); err != nil {
		t.Fatal(err)
	}
	for name, invalid := range map[string]string{
		"malformed hash":  strings.Replace(raw, `"contentHash":""`, `"contentHash":"not-a-hash"`, 1),
		"non-string hash": strings.Replace(raw, `"contentHash":""`, `"contentHash":null`, 1),
		"unknown field":   strings.Replace(raw, `"contentHash":""`, `"contentHash":"","unknown":true`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validate(invalid); err == nil {
				t.Fatal("accepted invalid source pins")
			}
		})
	}
}

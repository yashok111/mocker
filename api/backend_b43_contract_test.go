package api

import (
	"strings"
	"testing"
)

func TestBackendB43StartAndLifecycleClosed(t *testing.T) {
	id := source6ContractID
	proposal := `{"proposalId":"` + id + `","proposalRevisionId":"` + id + `"}`
	common := `,"limits":{},"observationMode":"none","idempotencyKey":"b43"}`
	starts := []string{
		`{"kind":"change_package","changeProposal":` + proposal + common,
		`{"kind":"conformance","changeProposal":` + proposal + `,"resultRevisionId":"` + id + `","identityMap":[],"testAttachments":[]` + common,
		`{"kind":"endpoint_review","fromRevisionId":"` + id + `","toRevisionId":"` + id + `","beforeEndpointId":"` + id + `","afterEndpointId":null` + common,
	}
	validate := lineageSchemaValidator(t, "StartBackendAnalysisRequest")
	for _, raw := range starts {
		if err := validate(raw); err != nil {
			t.Error(err)
		}
		for _, bad := range []string{strings.Replace(raw, `"limits":{}`, `"limits":null`, 1), strings.Replace(raw, `"limits":{},`, "", 1), strings.Replace(raw, `"limits":{}`, `"limits":{},"scope":{}`, 1)} {
			if validate(bad) == nil {
				t.Error("accepted", bad)
			}
		}
	}
	for _, bad := range []string{strings.Replace(starts[1], `"identityMap":[],`, "", 1), strings.Replace(starts[1], `"testAttachments":[]`, `"testAttachments":null`, 1), strings.Replace(starts[2], `,"afterEndpointId":null`, "", 1)} {
		if validate(bad) == nil {
			t.Error("accepted", bad)
		}
	}
	lifecycle := lineageSchemaValidator(t, "ApplyBackendChangeProposalLifecycleRequest")
	report := `{"jobId":"` + id + `","resultVersion":9223372036854775807,"inputHash":"` + strings.Repeat("a", 64) + `","resultHash":"` + strings.Repeat("b", 64) + `"}`
	for _, action := range []string{"implemented", "archive", "unarchive"} {
		raw := `{"expectedVersion":9223372036854775807,"proposalRevisionId":"` + id + `","action":"` + action + `","idempotencyKey":"b43"`
		if action == "implemented" {
			raw += `,"report":` + report + `,"resultRevisionId":"` + id + `","exceptions":[]`
		}
		raw += "}"
		if action == "implemented" {
			for _, bad := range []string{strings.Replace(raw, `,"exceptions":[]`, "", 1), strings.Replace(raw, `"exceptions":[]`, `"exceptions":null`, 1)} {
				if lifecycle(bad) == nil {
					t.Error("accepted", bad)
				}
			}
		}
		if err := lifecycle(raw); err != nil {
			t.Error(err)
		}
		for _, bad := range []string{strings.Replace(raw, `"expectedVersion":9223372036854775807`, `"expectedVersion":9223372036854775808`, 1), strings.Replace(raw, `"idempotencyKey":"b43"`, `"idempotencyKey":"b43","acknowledgedGapIds":[]`, 1)} {
			if lifecycle(bad) == nil {
				t.Error("accepted", bad)
			}
		}
	}
}

package api

import (
	"strings"
	"testing"
)

func TestBackendB41ExactReadTargets(t *testing.T) {
	validate := lineageSchemaValidator(t, "BackendReadTarget")
	id := source6ContractID
	targets := []string{
		`"revisionId":"` + id + `"`,
		`"proposal":{"proposalId":"` + id + `","proposalRevisionId":"` + id + `"}`,
		`"changeProposal":{"proposalId":"` + id + `","proposalRevisionId":"` + id + `"}`,
		`"importCandidate":{"importId":"` + id + `","importVersion":9223372036854775807,"candidateHash":"` + strings.Repeat("a", 64) + `"}`,
	}
	for _, target := range targets {
		if err := validate(`{` + target + `}`); err != nil {
			t.Fatal(err)
		}
		for _, other := range targets {
			if other != target && validate(`{`+target+`,`+other+`}`) == nil {
				t.Fatal("accepted multiple targets")
			}
		}
	}
	for _, raw := range []string{`{}`, `null`, `{"changeProposal":null}`, `{"importCandidate":null}`, strings.Replace(`{`+targets[3]+`}`, "9223372036854775807", "9223372036854775808", 1)} {
		if validate(raw) == nil {
			t.Fatalf("accepted invalid target %s", raw)
		}
	}
}

func TestBackendB41PublicResponseSchemas(t *testing.T) {
	for _, name := range []string{"BackendSourceVector", "BackendProviderAssertion", "BackendAssertionsPage", "BackendEffectiveGraphPins", "BackendGraphResponse", "BackendNodeResponse", "BackendEvidenceResponse", "BackendCoverageResponse", "BackendImportSessionResponse", "BackendImportPreviewResponse", "BackendImportChangesResponse", "BackendSavedViewResponse", "BackendDatabaseResponse", "BackendFlowResponse", "BackendLineageResponse", "BackendEventsResponse", "BackendAPIArtifactResponse", "BackendArtifactProjectionResponse", "BackendChangeProposalDetail", "BackendChangeProposalCandidate", "BackendChangeProposalApplyResult"} {
		t.Run(name, func(t *testing.T) { _ = lineageSchemaValidator(t, name) })
	}
}

func TestBackendB41DesiredCommandUnion(t *testing.T) {
	validate := lineageSchemaValidator(t, "BackendChangeProposalCommand")
	base := `"commandId":"` + source6ContractID + `","reason":"Desired edit"`
	valid := `{"type":"create_node",` + base + `,"id":"` + source6ContractID + `","kind":"dto","name":"DTO","parentId":null,"attributes":{"qualifiedName":"example.DTO","analysisStatus":"complete","gaps":[]}}`
	for _, raw := range []string{valid, `{"type":"set_criteria",` + base + `,"criteria":[]}`} {
		if err := validate(raw); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{strings.Replace(valid, `"parentId":null,`, "", 1), strings.Replace(valid, `"attributes":{`, `"attributes":{"evidenceIds":[],`, 1), strings.Replace(valid, `"kind":"dto"`, `"kind":"invented"`, 1), `{"type":"set_criteria",` + base + `,"criteria":null}`} {
		if validate(raw) == nil {
			t.Fatalf("accepted invalid command %s", raw)
		}
	}
}

func TestBackendB41ColumnTypeFamilyUsesPersistedEnum(t *testing.T) {
	validate := lineageSchemaValidator(t, "BackendChangeProposalCommand")
	valid := `{"type":"alter_column","commandId":"` + source6ContractID + `","reason":"Use integer family","columnId":"` + source6ContractID + `","facetKey":"sql","change":{"group":"native_type","nativeType":{"status":"known","value":"INTEGER"},"typeFamily":{"status":"known","value":"integer"}}}`
	if err := validate(valid); err != nil {
		t.Fatal(err)
	}
	if validate(strings.Replace(valid, `"value":"integer"`, `"value":"invented_family"`, 1)) == nil {
		t.Fatal("accepted a type family outside the persisted relational enum")
	}
}

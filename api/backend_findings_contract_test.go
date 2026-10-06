package api

import (
	"strings"
	"testing"
)

func TestBackendFindingClosedContracts(t *testing.T) {
	validate := lineageSchemaValidator(t, "ReviewBackendFindingRequest")
	hash := strings.Repeat("a", 64)
	raw := `{"expectedVersion":1,"basisHash":"` + hash + `","status":"accepted_risk","reason":"reviewed","idempotencyKey":"key"}`
	if err := validate(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(raw, `"accepted_risk"`, `"resolved"`, 1), strings.Replace(raw, `"reason":"reviewed"`, `"author":"forged","reason":"reviewed"`, 1), strings.Replace(raw, `"basisHash":"`+hash+`"`, `"basisHash":null`, 1)} {
		if validate(bad) == nil {
			t.Fatal("accepted", bad)
		}
	}
	scope := lineageSchemaValidator(t, "BackendDiagramScopeSelector")
	if err := scope(`{"kind":"semantic","id":"` + source6ContractID + `"}`); err != nil {
		t.Fatal(err)
	}
	if scope(`{"kind":"semantic","id":"`+source6ContractID+`","projection":null}`) == nil {
		t.Fatal("mixed selector")
	}
}

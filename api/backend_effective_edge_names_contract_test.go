package api

import "testing"

func TestBackendEffectiveEdgeNameClosedSchema(t *testing.T) {
	validate := lineageSchemaValidator(t, "BackendEffectiveEdgeName")
	good := `{"id":"` + source6ContractID + `","name":"Desired edge"}`
	if err := validate(good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{"id":"` + source6ContractID + `","name":""}`, `{"id":"` + source6ContractID + `","name":null}`, `{"id":"not-a-uuid","name":"Desired"}`, `{"id":"` + source6ContractID + `","name":"Desired","source":{}}`} {
		if validate(bad) == nil {
			t.Fatalf("accepted invalid edge-name sidecar: %s", bad)
		}
	}
}

package api

import (
	"slices"
	"testing"
)

func TestBackendCapabilityReadSupportSchema(t *testing.T) {
	validate := lineageSchemaValidator(t, "BackendReadTargetSupport")
	for _, raw := range []string{
		`{"read":"graph","targets":["revisionId","proposal","changeProposal","importCandidate"],"sourceSchemaVersions":["1","2","3","4","5","6"],"fullBaseSchemaVersions":["5","6"]}`,
		`{"read":"assertions","targets":["revisionId","changeProposal","importCandidate"],"sourceSchemaVersions":["6"],"fullBaseSchemaVersions":["6"]}`,
		`{"read":"flow","targets":["revisionId","changeProposal"],"sourceSchemaVersions":["3","4","5","6"],"fullBaseSchemaVersions":["5","6"]}`,
		`{"read":"api_artifacts","targets":["revisionId","changeProposal"],"sourceSchemaVersions":["1","2","3","4","5","6"],"fullBaseSchemaVersions":["5","6"]}`,
		`{"read":"editor_artifacts","targets":["revisionId","changeProposal"],"sourceSchemaVersions":["1","2","3","4","5","6"],"fullBaseSchemaVersions":["5","6"]}`,
		`{"read":"saved_view_v2","targets":["revisionId","proposal","changeProposal"],"sourceSchemaVersions":["2","3","4","5","6"],"fullBaseSchemaVersions":["5","6"]}`,
	} {
		if err := validate(raw); err != nil {
			t.Fatalf("valid support row %s: %v", raw, err)
		}
	}
	for _, raw := range []string{
		`null`, `{}`,
		`{"read":"unknown","targets":[],"sourceSchemaVersions":[],"fullBaseSchemaVersions":[]}`,
		`{"read":"graph","targets":null,"sourceSchemaVersions":["6"],"fullBaseSchemaVersions":["6"]}`,
		`{"read":"graph","targets":["revisionId","revisionId"],"sourceSchemaVersions":["6"],"fullBaseSchemaVersions":["6"]}`,
		`{"read":"graph","targets":["revisionId"],"sourceSchemaVersions":["6"],"fullBaseSchemaVersions":["6"],"fallback":"head"}`,
		`{"read":"assertions","targets":["proposal"],"sourceSchemaVersions":["6"],"fullBaseSchemaVersions":["6"]}`,
		`{"read":"assertions","targets":["revisionId"],"sourceSchemaVersions":["5"],"fullBaseSchemaVersions":["6"]}`,
		`{"read":"flow","targets":["revisionId"],"sourceSchemaVersions":["2"],"fullBaseSchemaVersions":["5","6"]}`,
		`{"read":"events","targets":["importCandidate"],"sourceSchemaVersions":["5","6"],"fullBaseSchemaVersions":["5","6"]}`,
		`{"read":"api_artifacts","targets":["proposal"],"sourceSchemaVersions":["4"],"fullBaseSchemaVersions":["5","6"]}`,
		`{"read":"editor_artifacts","targets":["proposal"],"sourceSchemaVersions":["4"],"fullBaseSchemaVersions":["5","6"]}`,
		`{"read":"saved_view_v2","targets":["importCandidate"],"sourceSchemaVersions":["6"],"fullBaseSchemaVersions":["6"]}`,
	} {
		if validate(raw) == nil {
			t.Fatalf("invalid support row accepted: %s", raw)
		}
	}
}

func TestBackendCapabilityCommandsMatchClosedUnions(t *testing.T) {
	caps, err := BackendSchema("BackendCapabilities")
	if err != nil {
		t.Fatal(err)
	}
	props := caps["properties"].(map[string]any)
	for _, spec := range []struct{ field, schema, discriminator string }{
		{field: "changeProposalCommands", schema: "BackendChangeProposalCommand", discriminator: "type"},
		{field: "importCommands", schema: "BackendImportCommand", discriminator: "op"},
		{field: "sourceScopes", schema: "BackendSourceScope", discriminator: "kind"},
	} {
		t.Run(spec.field, func(t *testing.T) {
			inventory, ok := props[spec.field].(map[string]any)
			if !ok {
				t.Fatalf("capability inventory %s missing", spec.field)
			}
			items := inventory["items"].(map[string]any)
			actual := []string{}
			for _, value := range items["enum"].([]any) {
				actual = append(actual, value.(string))
			}
			canonical, err := BackendSchema(spec.schema)
			if err != nil {
				t.Fatal(err)
			}
			unique := map[string]bool{}
			for _, value := range canonical["oneOf"].([]any) {
				branch := value.(map[string]any)["properties"].(map[string]any)
				unique[branch[spec.discriminator].(map[string]any)["const"].(string)] = true
			}
			if len(actual) != len(unique) {
				t.Fatalf("inventory %v != canonical types %v", actual, unique)
			}
			for name := range unique {
				if !slices.Contains(actual, name) {
					t.Errorf("missing canonical branch %s", name)
				}
			}
		})
	}
}

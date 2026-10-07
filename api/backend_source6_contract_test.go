package api

import (
	"fmt"
	"strings"
	"testing"
)

const source6ContractID = "0197aaf9-5555-7000-8000-000000000001"

func TestBackendSource6ScopeSchemas(t *testing.T) {
	validate := lineageSchemaValidator(t, "BackendSourceScope")
	for _, raw := range []string{
		`{"kind":"add_repository"}`,
		`{"kind":"add_provider","repositoryId":"` + source6ContractID + `"}`,
		`{"kind":"reconcile","repositoryId":"` + source6ContractID + `","providerNamespace":"compiler"}`,
		`{"kind":"migrate_provider","repositoryId":"` + source6ContractID + `","fromProviderNamespace":"compiler","fromSnapshotId":"` + source6ContractID + `","reason":"New collector"}`,
	} {
		if err := validate(raw); err != nil {
			t.Fatalf("valid scope %s: %v", raw, err)
		}
	}
	for _, raw := range []string{
		`null`, `{}`, `{"kind":"add_repository","repositoryId":"` + source6ContractID + `"}`,
		`{"kind":"add_provider","repositoryId":null}`,
		`{"kind":"reconcile","repositoryId":"` + source6ContractID + `"}`,
		`{"kind":"reconcile","repositoryId":"` + source6ContractID + `","providerNamespace":"compiler","fromSnapshotId":"` + source6ContractID + `"}`,
		`{"kind":"migrate_provider","repositoryId":"` + source6ContractID + `","fromProviderNamespace":"compiler","reason":"New collector"}`,
	} {
		if validate(raw) == nil {
			t.Fatalf("accepted mixed/incomplete scope %s", raw)
		}
	}
	status := lineageSchemaValidator(t, "BackendSourceScopeStatus")
	for _, raw := range []string{`{"status":"complete","gaps":[]}`, `{"status":"partial","gaps":["Unchecked external handler"]}`} {
		if err := status(raw); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{`{"status":"complete","gaps":["Missing"]}`, `{"status":"partial","gaps":[]}`, `{"status":"partial","gaps":null}`} {
		if status(raw) == nil {
			t.Fatalf("accepted inconsistent scope status %s", raw)
		}
	}
}

func TestBackendSource6ReferenceAndResolutionSchemas(t *testing.T) {
	hash := strings.Repeat("a", 64)
	base := `{"repositoryId":"` + source6ContractID + `","providerNamespace":"compiler","recordType":"node","externalKey":"field","expectedId":"` + source6ContractID + `","assertionHash":"` + hash + `"}`
	validate := lineageSchemaValidator(t, "BackendImportRecordRef")
	for _, raw := range []string{`{"localKey":"field"}`, `{"base":` + base + `}`} {
		if err := validate(raw); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{
		`{}`, `null`, `{"localKey":null}`, `{"base":null}`,
		`{"localKey":"field","base":` + base + `}`,
		`{"base":` + strings.Replace(base, `"providerNamespace":"compiler",`, "", 1) + `}`,
		`{"base":` + strings.Replace(base, `"assertionHash":"`+hash+`"`, `"assertionHash":"old"`, 1) + `}`,
	} {
		if validate(raw) == nil {
			t.Fatalf("accepted invalid qualified reference %s", raw)
		}
	}
	claim := lineageSchemaValidator(t, "BackendSourceClaimIdentity")
	claimBody := `{"decisionId":"` + source6ContractID + `","recordType":"node","externalKey":"own-field","target":` + base + `,"reason":"Same source object","evidenceKeys":["own-proof"]}`
	if err := claim(claimBody); err != nil {
		t.Fatal(err)
	}
	if claim(strings.Replace(claimBody, `"evidenceKeys":["own-proof"]`, `"evidenceKeys":null`, 1)) == nil {
		t.Fatal("accepted null own evidence keys")
	}
	resolution := lineageSchemaValidator(t, "BackendSourceAssertionResolution")
	resolutionBody := `{"decisionId":"` + source6ContractID + `","recordType":"node","id":"` + source6ContractID + `","property":{"kind":"relational_facet","facetKey":"sql","group":"nullable"},"conflictHash":"` + hash + `","select":{"repositoryId":"` + source6ContractID + `","providerNamespace":"compiler","assertionHash":"` + hash + `"},"reason":"Use this declaration"}`
	if err := resolution(resolutionBody); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		strings.Replace(resolutionBody, `"kind":"relational_facet"`, `"kind":"name"`, 1),
		strings.Replace(resolutionBody, `"group":"nullable"`, `"group":"inventedProperty"`, 1),
		strings.Replace(resolutionBody, `"reason":"Use this declaration"`, `"reason":"Use this declaration","replacementValue":true`, 1),
	} {
		if resolution(raw) == nil {
			t.Fatalf("accepted untyped replacement/selector %s", raw)
		}
	}
}

func TestBackendSource6ChangeManifestSchema(t *testing.T) {
	validate := lineageSchemaValidator(t, "BackendChangeManifest")
	hash := strings.Repeat("a", 64)
	valid := `{"scope":"affected-subgraph","files":[{"kind":"modified","path":"service.go","beforeHash":"` + hash + `","afterHash":"` + strings.Repeat("b", 64) + `"}],"affectedRoots":[{"recordType":"node","id":"` + source6ContractID + `"}]}`
	if err := validate(valid); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		strings.Replace(valid, `"kind":"modified"`, `"kind":"added"`, 1),
		strings.Replace(valid, `"kind":"modified"`, `"kind":"deleted"`, 1),
		strings.Replace(valid, `"beforeHash":"`+hash+`",`, "", 1),
		strings.Replace(valid, `"scope":"affected-subgraph"`, `"scope":"whole-source"`, 1),
		strings.Replace(valid, `"recordType":"node"`, `"recordType":"project"`, 1),
	} {
		if validate(raw) == nil {
			t.Fatalf("accepted malformed change manifest %s", raw)
		}
	}
}

func source6ContractBeginBody() string {
	categories := strings.Fields("files endpoints datastores migrations producers consumers jobs contracts tests")
	items := make([]string, 0, len(categories))
	for _, category := range categories {
		items = append(items, fmt.Sprintf(`{"category":%q,"status":"complete","knownCount":0,"denominator":0,"discoverySource":"Static fixture","gaps":[],"reason":""}`, category))
	}
	return `{"expectedVersion":9007199254740993,"baseRevisionId":"` + source6ContractID + `","idempotencyKey":"source6",` +
		`"mode":"composed","profile":"composed-source-v1","sourceScope":{"kind":"add_repository"},"scopeStatus":{"status":"complete","gaps":[]},"syncPolicy":"whole-source-v1",` +
		`"manifest":{"repositoryName":"repo","provider":{"name":"compiler","version":"1","namespace":"compiler","method":"ast","profiles":["foundation-graph-v1","relational-graph-v1","runtime-flow-v1","field-lineage-v1","events-service-v1","composed-source-v1"],"limitations":[]},` +
		`"snapshot":{"dirty":false,"consistency":"verified","capturedAt":"2026-10-03T00:00:00Z","files":[]}},"inventory":[` + strings.Join(items, ",") + `]}`
}

func TestBackendSource6BeginModeAndPolicySchema(t *testing.T) {
	validate := lineageSchemaValidator(t, "BeginBackendImportRequest")
	base := source6ContractBeginBody()
	if err := validate(base); err != nil {
		t.Fatalf("composed begin rejected: %v", err)
	}
	reconcile := strings.Replace(base, `"sourceScope":{"kind":"add_repository"}`, `"sourceScope":{"kind":"reconcile","repositoryId":"`+source6ContractID+`","providerNamespace":"compiler"}`, 1)
	manifest := `"changeManifest":{"scope":"affected-subgraph","files":[],"affectedRoots":[]}`
	incremental := strings.Replace(reconcile, `"syncPolicy":"whole-source-v1"`, `"syncPolicy":"incremental-source-v1",`+manifest, 1)
	extension := `"profileExtension":{"fromProfile":"events-service-v1","toProfile":"composed-source-v1"}`
	for _, valid := range []string{
		reconcile, incremental,
		strings.Replace(base, `"idempotencyKey":"source6"`, `"idempotencyKey":"source6",`+extension, 1),
		strings.Replace(reconcile, `"idempotencyKey":"source6"`, `"idempotencyKey":"source6",`+extension, 1),
	} {
		if err := validate(valid); err != nil {
			t.Fatalf("valid scope/policy/extension rejected: %v", err)
		}
	}
	for _, invalid := range []string{
		strings.Replace(base, `"mode":"composed"`, `"mode":"initial"`, 1),
		strings.Replace(base, `"profile":"composed-source-v1"`, `"profile":"events-service-v1"`, 1),
		strings.Replace(base, `"sourceScope":{"kind":"add_repository"}`, `"repositoryId":"`+source6ContractID+`","sourceScope":{"kind":"add_repository"}`, 1),
		strings.Replace(base, `"sourceScope":{"kind":"add_repository"}`, `"graphScope":null,"sourceScope":{"kind":"add_repository"}`, 1),
		strings.Replace(base, `"sourceScope":{"kind":"add_repository"},`, "", 1),
		strings.Replace(base, `"sourceScope":{"kind":"add_repository"}`, `"sourceScope":null`, 1),
		strings.Replace(base, `"syncPolicy":"whole-source-v1"`, `"syncPolicy":"whole-source-v1",`+manifest, 1),
		strings.Replace(base, `"syncPolicy":"whole-source-v1"`, `"syncPolicy":"incremental-source-v1",`+manifest, 1),
		strings.Replace(reconcile, `"syncPolicy":"whole-source-v1"`, `"syncPolicy":"incremental-source-v1"`, 1),
		strings.Replace(incremental, `"idempotencyKey":"source6"`, `"idempotencyKey":"source6",`+extension, 1),
		strings.Replace(base, `"idempotencyKey":"source6"`, `"idempotencyKey":"source6","profileExtension":{"fromProfile":"field-lineage-v1","toProfile":"events-service-v1"}`, 1),
		strings.Replace(base, `,"composed-source-v1"],"limitations"`, `],"limitations"`, 1),
	} {
		if validate(invalid) == nil {
			t.Fatalf("accepted forbidden source6 mode/policy mix: %s", invalid)
		}
	}
}

func TestBackendSource6BatchRefsAndRepresentationSchemas(t *testing.T) {
	validate := lineageSchemaValidator(t, "BackendImportCommand")
	service := `{"op":"upsert_node","node":{"externalKey":"service","kind":"service","name":"Service","parentRef":{"localKey":"system"},"attributes":{},"evidenceKeys":["proof"]}}`
	edge := `{"op":"upsert_edge","edge":{"externalKey":"contains","kind":"contains","fromRef":{"localKey":"service"},"toRef":{"localKey":"dto"},"attributes":{},"evidenceKeys":["proof"]}}`
	dto := `{"op":"upsert_node","node":{"externalKey":"dto","kind":"dto","name":"DTO","parentRef":{"localKey":"service"},"attributes":{"qualifiedName":"edu.User","analysisStatus":"complete","gaps":[]},"evidenceKeys":["proof"]}}`
	field := `{"op":"upsert_node","node":{"externalKey":"dto-id","kind":"representation_field","name":"ID","parentRef":{"localKey":"dto"},"attributes":{"selector":[{"property":"user"},{"property":"id"}],"nativeType":{"status":"known","value":"int64"},"nullable":{"status":"known","value":false},"cardinality":{"status":"known","value":"one"},"analysisStatus":"complete","gaps":[]},"evidenceKeys":["proof"]}}`
	mapping := `{"op":"upsert_node","node":{"externalKey":"mapping","kind":"field_mapping","name":"Serialize ID","parentRef":{"localKey":"dto"},"attributes":{"sources":[{"kind":"representation_field","nodeRef":{"localKey":"dto-id"}}],"destination":{"kind":"api_field","nodeRef":{"localKey":"response-id"}},"transform":{"kind":"copy","description":"Direct field mapping","redacted":false},"analysisStatus":"complete","gaps":[]},"evidenceKeys":["proof"]}}`
	for _, valid := range []string{service, edge, dto, field, mapping} {
		if err := validate(valid); err != nil {
			t.Fatalf("valid source6 command rejected: %s: %v", valid, err)
		}
	}
	for _, invalid := range []string{
		strings.Replace(service, `"parentRef":{"localKey":"system"}`, `"parentRef":{"localKey":"system"},"parentKey":"system"`, 1),
		strings.Replace(service, `"parentRef":{"localKey":"system"}`, `"parentRef":null`, 1),
		strings.Replace(edge, `"fromRef":{"localKey":"service"}`, `"fromKey":"service"`, 1),
		strings.Replace(field, `"selector":[{"property":"user"},{"property":"id"}]`, `"selector":[]`, 1),
		strings.Replace(field, `"nullable":{"status":"known","value":false}`, `"nullable":{"status":"known","value":"false"}`, 1),
		strings.Replace(field, `"cardinality":{"status":"known","value":"one"}`, `"cardinality":{"status":"known","value":"zero"}`, 1),
		strings.Replace(mapping, `"nodeRef":{"localKey":"dto-id"}`, `"nodeRef":{"localKey":"dto-id"},"facetKey":"sql"`, 1),
		strings.Replace(mapping, `"nodeRef":{"localKey":"response-id"}`, `"nodeKey":"response-id"`, 1),
	} {
		if validate(invalid) == nil {
			t.Fatalf("accepted mixed refs/invalid representation: %s", invalid)
		}
	}
}

func TestBackendSource6NestedReferenceSchemas(t *testing.T) {
	for _, tc := range []struct{ schema, valid, invalid string }{
		{"BackendComposedTransactionContextInput", `{"status":"known","transactionRef":{"localKey":"tx"}}`, `{"status":"known","transactionRef":{"localKey":"tx"},"transactionKey":"tx"}`},
		{"BackendComposedIndexTermInput", `{"columnRef":{"localKey":"column"},"direction":"asc","nulls":"unknown"}`, `{"columnRef":{"localKey":"column"},"columnKey":"column","direction":"asc","nulls":"unknown"}`},
		{"BackendComposedReferenceColumnPairInput", `{"fromColumnRef":{"localKey":"from"},"toColumnRef":{"localKey":"to"}}`, `{"fromColumnRef":{"localKey":"from"},"toColumnKey":"to"}`},
		{"BackendComposedMigrationCandidateTargetInput", `{"kind":"candidate","objectRef":{"localKey":"table"}}`, `{"kind":"candidate","objectKey":"table"}`},
		{"BackendComposedImportLineageTransport", `{"emitsEdgeRef":{"localKey":"emit"},"deliveryEdgeRef":{"localKey":"deliver"}}`, `{"emitsEdgeRef":{"localKey":"emit"},"deliveryEdgeKey":"deliver"}`},
	} {
		t.Run(tc.schema, func(t *testing.T) {
			validate := lineageSchemaValidator(t, tc.schema)
			if err := validate(tc.valid); err != nil {
				t.Fatal(err)
			}
			if validate(tc.invalid) == nil {
				t.Fatalf("accepted unqualified nested source6 ref %s", tc.invalid)
			}
		})
	}
}

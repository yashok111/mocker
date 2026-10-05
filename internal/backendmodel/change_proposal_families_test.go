package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"testing"
	"uuid"
)

func changeMapCommand(t *testing.T, typ string, payload map[string]any) ChangeProposalCommand {
	t.Helper()
	payload = maps.Clone(payload)
	payload["type"], payload["commandId"], payload["reason"] = typ, uuid.NewV7().String(), "Desired structural change"
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var c ChangeProposalCommand
	if err = json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return c
}
func changeCreateNode(t *testing.T, id, kind string, parent *string, attrs any) ChangeProposalCommand {
	return changeMapCommand(t, "create_node", map[string]any{"id": id, "kind": kind, "name": kind, "parentId": parent, "attributes": attrs})
}
func changeContains(t *testing.T, from, to string) ChangeProposalCommand {
	return changeMapCommand(t, "upsert_edge", map[string]any{"id": uuid.NewV7().String(), "kind": "contains", "from": from, "to": to, "attributes": map[string]any{}})
}
func changeDesiredFacet(extra map[string]any) map[string]any {
	m := map[string]any{"dialect": "postgresql", "analysisStatus": "complete", "gaps": []string{}}
	maps.Copy(m, extra)
	return m
}
func changeFacets(facet map[string]any) map[string]any {
	return map[string]any{"facets": map[string]any{"sql": facet}}
}
func changeRelationalFixture(t *testing.T) (*Repo, *ChangeProposalDetail, map[string]string) {
	t.Helper()
	r, _, d := changeFixture(t)
	ids := map[string]string{}
	for _, key := range []string{"database", "schema", "table", "column"} {
		ids[key] = uuid.NewV7().String()
	}
	ds := map[string]any{"technology": "postgresql", "relational": changeFacets(changeDesiredFacet(map[string]any{"databaseName": "db", "qualifiedName": "db", "nativeDefinition": nil}))}
	table := changeFacets(changeDesiredFacet(map[string]any{"qualifiedName": "orders", "nativeDefinition": nil, "columnsStatus": "complete", "constraintsStatus": "complete"}))
	column := changeFacets(changeDesiredFacet(map[string]any{"nativeType": known("integer"), "typeFamily": known("integer"), "nullable": known(true), "defaultExpression": known(nil), "generatedExpression": known(nil), "identity": known(nil), "ordinal": known(1)}))
	d, _ = saveChange(t, r, d, "relational-setup", changeCreateNode(t, ids["database"], "datastore", nil, ds), changeCreateNode(t, ids["schema"], "db_schema", new(ids["database"]), changeFacets(changeDesiredFacet(map[string]any{"qualifiedName": "public", "nativeDefinition": nil}))), changeCreateNode(t, ids["table"], "table", new(ids["schema"]), table), changeCreateNode(t, ids["column"], "column", new(ids["table"]), column), changeContains(t, ids["database"], ids["schema"]), changeContains(t, ids["schema"], ids["table"]), changeContains(t, ids["table"], ids["column"]))
	return r, d, ids
}

func TestChangeProposalRelationalFamilies(t *testing.T) {
	r, d, ids := changeRelationalFixture(t)
	for _, group := range []map[string]any{{"group": "nullable", "nullable": known(false)}, {"group": "default", "defaultExpression": known("0")}, {"group": "native_type", "nativeType": known("bigint"), "typeFamily": known("integer")}} {
		command := changeMapCommand(t, "alter_column", map[string]any{"columnId": ids["column"], "facetKey": "sql", "change": group})
		d, _ = saveChange(t, r, d, uuid.NewV7().String(), command)
	}
	constraintID, indexID := uuid.NewV7().String(), uuid.NewV7().String()
	definition := changeDesiredFacet(map[string]any{"constraintKind": "unique", "columnIds": []string{ids["column"]}, "expression": known(nil), "nativeDefinition": nil, "deferrable": known(false), "initiallyDeferred": known(false)})
	constraint := changeMapCommand(t, "alter_constraint", map[string]any{"action": "create", "constraintId": constraintID, "facetKey": "sql", "name": "Unique", "tableId": ids["table"], "definition": definition})
	indexDefinition := changeDesiredFacet(map[string]any{"terms": []any{map[string]any{"columnId": ids["column"], "direction": "asc", "nulls": "last"}}, "unique": known(false), "predicate": known(nil), "method": known("btree"), "nativeDefinition": nil})
	index := changeMapCommand(t, "alter_index", map[string]any{"action": "create", "indexId": indexID, "facetKey": "sql", "name": "Search", "tableId": ids["table"], "definition": indexDefinition})
	containsConstraint, containsIndex := changeContains(t, ids["table"], constraintID), changeContains(t, ids["table"], indexID)
	d, _ = saveChange(t, r, d, "definitions", constraint, index, containsConstraint, containsIndex)
	definition["deferrable"] = known(true)
	indexDefinition["unique"] = known(true)
	d, _ = saveChange(t, r, d, "update-definitions", changeMapCommand(t, "alter_constraint", map[string]any{"action": "update", "constraintId": constraintID, "facetKey": "sql", "definition": definition}), changeMapCommand(t, "alter_index", map[string]any{"action": "update", "indexId": indexID, "facetKey": "sql", "definition": indexDefinition}))
	snapshot := changeReadSnapshot(t, r, d)
	if len(snapshot.Nodes) != 7 {
		t.Fatalf("missing desired relational records: %d", len(snapshot.Nodes))
	}
	d, _ = saveChange(t, r, d, "remove-definitions", changeMapCommand(t, "alter_constraint", map[string]any{"action": "remove", "constraintId": constraintID, "facetKey": "sql"}), changeMapCommand(t, "alter_index", map[string]any{"action": "remove", "indexId": indexID, "facetKey": "sql"}), changeMapCommand(t, "remove_edge", map[string]any{"id": containsConstraint.ID}), changeMapCommand(t, "remove_edge", map[string]any{"id": containsIndex.ID}))
	if slices.ContainsFunc(changeReadSnapshot(t, r, d).Nodes, func(n Node) bool { return n.ID == constraintID || n.ID == indexID }) {
		t.Fatal("last facet removal did not tombstone")
	}
}

func TestChangeProposalForeignKeyExplicitIdentityAndFacetRemoval(t *testing.T) {
	r, d, ids := changeRelationalFixture(t)
	constraintID, referenceID := uuid.NewV7().String(), uuid.NewV7().String()
	definition := changeDesiredFacet(map[string]any{"constraintKind": "foreign_key", "columnIds": []string{ids["column"]}, "expression": known(nil), "nativeDefinition": nil, "deferrable": known(false), "initiallyDeferred": known(false), "reference": map[string]any{"id": referenceID, "targetTableId": ids["table"], "columnPairs": []any{map[string]any{"fromColumnId": ids["column"], "toColumnId": ids["column"]}}, "updateAction": known("no_action"), "deleteAction": known("cascade"), "matchType": known("simple")}})
	command := changeMapCommand(t, "alter_constraint", map[string]any{"action": "create", "constraintId": constraintID, "facetKey": "sql", "name": "FK", "tableId": ids["table"], "definition": definition})
	d, _ = saveChange(t, r, d, "fk", command, changeContains(t, ids["table"], constraintID))
	if !slices.ContainsFunc(changeReadSnapshot(t, r, d).Edges, func(e Edge) bool { return e.ID == referenceID && e.Kind == "references" }) {
		t.Fatal("client FK edge ID lost")
	}
	// A second facet is added and then removed. The remaining facet must survive
	// a persisted reload and a subsequent no-op preview.
	d, _ = saveChange(t, r, d, "second-facet", changeMapCommand(t, "alter_constraint", map[string]any{"action": "update", "constraintId": constraintID, "facetKey": "orm", "definition": definition}))
	var repair ChangeProposalCommand
	for _, edge := range changeReadSnapshot(t, r, d).Edges {
		if edge.ID == referenceID {
			facets, _, err := relationalFacetObject(edge.Kind, edge.Attributes)
			if err != nil {
				t.Fatal(err)
			}
			delete(facets, "orm")
			repair = changeMapCommand(t, "upsert_edge", map[string]any{"id": edge.ID, "kind": edge.Kind, "from": edge.From, "to": edge.To, "attributes": map[string]any{"facets": facets}})
		}
	}
	d, _ = saveChange(t, r, d, "remove-facet", changeMapCommand(t, "alter_constraint", map[string]any{"action": "remove", "constraintId": constraintID, "facetKey": "orm"}), repair)
	_, _ = saveChange(t, r, d, "reload-facets", changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{}}))
}

func TestChangeProposalFlowMappingAndEdgeNames(t *testing.T) {
	t.Parallel()
	r, _, d := changeFixture(t)
	service, flow, input, output := uuid.NewV7().String(), uuid.NewV7().String(), uuid.NewV7().String(), uuid.NewV7().String()
	step := func(kind string) map[string]any {
		return map[string]any{"analysisStatus": "complete", "gaps": []string{}, "stepKind": kind, "inputs": []any{}, "outputs": []any{}, "transactionContext": map[string]any{"status": "none", "reason": "No transaction"}, "nativeText": "inert"}
	}
	flowAttrs := map[string]any{"analysisStatus": "complete", "gaps": []string{}, "entryStepId": input, "exitStepIds": []string{output}, "exitStatus": "complete"}
	next := changeMapCommand(t, "edit_branch", map[string]any{"edgeId": uuid.NewV7().String(), "kind": "next", "from": input, "to": output, "attributes": map[string]any{}})
	d, _ = saveChange(t, r, d, "flow", changeCreateNode(t, service, "handler", nil, map[string]any{}), changeCreateNode(t, flow, "flow", new(service), flowAttrs), changeCreateNode(t, input, "flow_step", new(flow), step("input")), changeCreateNode(t, output, "flow_step", new(flow), step("return")), changeContains(t, service, flow), changeContains(t, flow, input), changeContains(t, flow, output), next)
	attrs := step("return")
	attrs["description"] = "Desired output"
	rename := changeMapCommand(t, "rename", map[string]any{"recordType": "edge", "id": next.EdgeID, "name": "Happy path"})
	criterion := changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{map[string]any{"key": "route", "kind": "edge_exists", "required": true, "description": "The next step remains explicit", "id": next.EdgeID, "edgeKind": "next", "from": input, "to": output}}})
	d, _ = saveChange(t, r, d, "step", changeMapCommand(t, "edit_flow_step", map[string]any{"stepId": output, "attributes": attrs}), rename, criterion)
	snap := changeReadSnapshot(t, r, d)
	if len(snap.EdgeNames) != 1 || snap.EdgeNames[0].Name != "Happy path" {
		t.Fatal("edge sidecar missing")
	}
	// Representation values provide exact addresses for ordered multi-source mapping.
	owner := uuid.NewV7().String()
	fieldIDs := []string{uuid.NewV7().String(), uuid.NewV7().String(), uuid.NewV7().String()}
	module := uuid.NewV7().String()
	commands := make([]ChangeProposalCommand, 0, 3+2*len(fieldIDs)+2)
	commands = append(commands, changeCreateNode(t, module, "module", nil, map[string]any{}), changeCreateNode(t, owner, "dto", new(module), representationOwnerAttrs()), changeContains(t, module, owner))
	for i, id := range fieldIDs {
		a := representationFieldAttrs()
		a["selector"] = jsontext.Value(fmt.Sprintf(`[{"property":"field%d"}]`, i))
		commands = append(commands, changeCreateNode(t, id, "representation_field", new(owner), a), changeContains(t, owner, id))
	}
	mappingID := uuid.NewV7().String()
	sources := []any{map[string]any{"kind": "representation_field", "nodeId": fieldIDs[1]}, map[string]any{"kind": "representation_field", "nodeId": fieldIDs[0]}}
	commands = append(commands, changeMapCommand(t, "set_field_mapping", map[string]any{"mappingId": mappingID, "parentId": input, "sources": sources, "destination": map[string]any{"kind": "representation_field", "nodeId": fieldIDs[2]}, "transform": map[string]any{"kind": "aggregate", "description": "Combine values", "redacted": false}, "analysisStatus": "complete", "gaps": []string{}}), changeContains(t, input, mappingID))
	d, _ = saveChange(t, r, d, "mapping", commands...)
	for _, n := range changeReadSnapshot(t, r, d).Nodes {
		if n.ID == mappingID {
			var refs []LineageValueRef
			if err := json.Unmarshal(n.Attributes["sources"], &refs); err != nil {
				t.Fatal(err)
			}
			if refs[0].NodeID != fieldIDs[1] {
				t.Fatal("ordered sources changed")
			}
			return
		}
	}
	t.Fatal("mapping missing")
}

func TestChangeProposalQualifiedAndCreatedIdentity(t *testing.T) {
	t.Parallel()
	r, _, d := changeFixture(t)
	source, err := loadComposedBase(t.Context(), r.db.R, d.Proposal.ProjectID, d.Revision.BaseRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	identity := source.Identities[0]
	mapSource := changeMapCommand(t, "map_identity", map[string]any{"target": map[string]any{"kind": "source_identity", "source": identity}, "expectedExternalKey": identity.ExternalKey, "newExternalKey": "desired-provider-key"})
	d, _ = saveChange(t, r, d, "source-identity", mapSource)
	if source.Identities[0].ExternalKey != identity.ExternalKey {
		t.Fatal("source identity mutated")
	}
	for _, target := range []any{map[string]any{"kind": "intent_identity", "recordType": identity.RecordType, "id": identity.ID}, map[string]any{"kind": "source_identity", "source": QualifiedSourceIdentity{RecordType: identity.RecordType, ID: identity.ID, RepositoryID: identity.RepositoryID, ProviderNamespace: "unknown-provider", ExternalKey: identity.ExternalKey, AssertionHash: identity.AssertionHash}}} {
		c := changeMapCommand(t, "map_identity", map[string]any{"target": target, "expectedExternalKey": identity.ExternalKey, "newExternalKey": "bad"})
		if _, err := r.PreviewChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{c}}); err == nil {
			t.Fatal("invalid identity target accepted")
		}
	}
	createdID := uuid.NewV7().String()
	created := changeCreateNode(t, createdID, "service", nil, map[string]any{})
	target := map[string]any{"kind": "intent_identity", "recordType": "node", "id": createdID}
	assign := changeMapCommand(t, "map_identity", map[string]any{"target": target, "expectedExternalKey": nil, "newExternalKey": "first"})
	again := changeMapCommand(t, "map_identity", map[string]any{"target": target, "expectedExternalKey": "first", "newExternalKey": "second"})
	d, _ = saveChange(t, r, d, "created-identity", created, assign, again)
	for _, i := range changeReadSnapshot(t, r, d).Identities {
		if changeIdentityRef(i.Target).ID == createdID && i.ExternalKey != nil && *i.ExternalKey == "second" {
			return
		}
	}
	t.Fatal("created identity CAS lost")
}

func TestChangeProposalSource6CallEndpointVocabulary(t *testing.T) {
	t.Parallel()
	r, _, d := changeFixture(t)
	handler, operation := uuid.NewV7().String(), uuid.NewV7().String()
	d, _ = saveChange(t, r, d, "source6-call", changeCreateNode(t, handler, "handler", nil, map[string]any{}), changeCreateNode(t, operation, "http_operation", nil, map[string]any{"method": "GET", "path": "/planned"}), changeMapCommand(t, "upsert_edge", map[string]any{"id": uuid.NewV7().String(), "kind": "calls", "from": handler, "to": operation, "attributes": map[string]any{}}))
	snapshot := changeReadSnapshot(t, r, d)
	if snapshot.SchemaVersion != "6" || snapshot.BaselineSchemaVersion != "6" {
		t.Fatal("source6 desired structural pins changed")
	}
}

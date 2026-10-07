package backendmodel

import (
	"encoding/json/jsontext"
	"strings"
	"testing"
	"uuid"
)

// mutateAllRelationalFacets applies fn to every facet of the keyed record.
func mutateAllRelationalFacets(t *testing.T, cs []ImportCommand, key string, fn func(map[string]jsontext.Value)) {
	t.Helper()
	c := relationalCommand(cs, key)
	var kind string
	var attrs map[string]jsontext.Value
	if c.Node != nil {
		kind, attrs = c.Node.Kind, c.Node.Attributes
	} else {
		kind, attrs = c.Edge.Kind, c.Edge.Attributes
	}
	fs, _, err := relationalFacetObject(kind, attrs)
	if err != nil {
		t.Fatal(err)
	}
	for fk := range fs {
		mutateRelationalFacet(t, cs, key, fk, fn)
	}
}

// Review 2026-10-06, F77: a unique key on a strict subset of the FK source
// columns makes the source columns globally unique, so source max is one,
// not a confident "many".
func TestDatabaseSourceKeySubsetProvesSourceMaxOne(t *testing.T) {
	t.Parallel()
	r, out, ids := databaseVariant(t, func(cs []ImportCommand) {
		mutateAllRelationalFacets(t, cs, "constraint:payments:order_unique", func(m map[string]jsontext.Value) {
			m["columnKeys"] = relationalRaw(t, []string{"column:payments:order_id"})
		})
	})
	item := databaseRelationship(t, r, out, ids, "references:payments:order_fk")
	if item.SourceCardinality.Max == nil || *item.SourceCardinality.Max != "1" {
		t.Fatalf("source max=%v basis=%v", item.SourceCardinality.Max, item.SourceCardinality.Basis)
	}
}

// Review 2026-10-06, F78 (projection half): the referenced columns are
// unique as a set, so a target key declared in another column order still
// proves target max one.
func TestDatabaseTargetKeyOrderDoesNotHideTargetMaxOne(t *testing.T) {
	t.Parallel()
	r, out, ids := databaseVariant(t, func(cs []ImportCommand) {
		mutateAllRelationalFacets(t, cs, "constraint:users:pk", func(m map[string]jsontext.Value) {
			m["columnKeys"] = relationalRaw(t, []string{"column:users:id", "column:users:tenant_id"})
		})
	})
	item := databaseRelationship(t, r, out, ids, "references:orders:user_fk")
	if item.TargetCardinality.Max == nil || *item.TargetCardinality.Max != "1" {
		t.Fatalf("target max=%v basis=%v", item.TargetCardinality.Max, item.TargetCardinality.Basis)
	}
}

// Review 2026-10-06, F81: a designed foreign key is a child of its table in
// the proposal view, but it can never be a unique key, so it must not turn
// the table's complete constraint inventory incomplete.
func TestDatabaseDesignedForeignKeyKeepsSourceInventoryComplete(t *testing.T) {
	t.Parallel()
	r, d, _, ids := proposalEvaluationFixture(t, "postgresql")
	in := DatabaseQueryInput{RevisionID: d.Proposal.BaseRevisionID, DatastoreID: d.Proposal.DatastoreID, FacetKey: "sql", RecordType: "relationships"}
	before, err := r.QueryDatabase(t.Context(), d.Proposal.ProjectID, in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.ApplyProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, proposalApplyInput(t, r, d, "design", proposalFK(ids)))
	if err != nil {
		t.Fatal(err)
	}
	designed := out.Changes[0].GeneratedIDs["constraintId"]
	in.RevisionID = ""
	in.Proposal = &ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: out.Revision.ID}
	after, err := r.QueryDatabase(t.Context(), d.Proposal.ProjectID, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, limitation := range after.Limitations {
		if strings.Contains(limitation, designed) {
			t.Errorf("designed FK flagged as incomplete proof: %s", limitation)
		}
	}
	maxOf := func(items []RelationshipItem) *string {
		for _, item := range items {
			if item.EdgeID == ids["references:orders:user_fk"] {
				return item.SourceCardinality.Max
			}
		}
		t.Fatal("relationship absent")
		return nil
	}
	if b, a := maxOf(before.RelationshipItems), maxOf(after.RelationshipItems); b == nil || *b != "many" || a == nil || *a != "many" {
		t.Fatalf("source max before=%v after=%v", b, a)
	}
}

// Review 2026-10-06, F45: alter_constraint whose reference id names another
// constraint's FK edge must be refused, not silently re-point that edge.
func TestChangeProposalForeignKeyEdgeIDBelongsToConstraint(t *testing.T) {
	t.Parallel()
	r, base, ids := effectiveFiveRelationalFixture(t)
	draft, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Edge reuse", BaseRevisionID: base.Revision.ID, IdempotencyKey: "edge-reuse"})
	if err != nil {
		t.Fatal(err)
	}
	designedFK := func(table, constraintID, edgeID string) []ChangeProposalCommand {
		definition := changeDesiredFacet(map[string]any{"constraintKind": "foreign_key", "columnIds": []string{ids["column:"+table+":tenant_id"], ids["column:"+table+":order_id"]}, "expression": known(nil), "nativeDefinition": nil, "deferrable": known(false), "initiallyDeferred": known(false), "reference": map[string]any{"id": edgeID, "targetTableId": ids["table:orders"], "columnPairs": []any{map[string]any{"fromColumnId": ids["column:"+table+":tenant_id"], "toColumnId": ids["column:orders:tenant_id"]}, map[string]any{"fromColumnId": ids["column:"+table+":order_id"], "toColumnId": ids["column:orders:id"]}}, "updateAction": known("cascade"), "deleteAction": known("cascade"), "matchType": known("simple")}})
		return []ChangeProposalCommand{changeMapCommand(t, "alter_constraint", map[string]any{"action": "create", "constraintId": constraintID, "facetKey": "sql", "name": table + "_designed_fk", "tableId": ids["table:"+table], "definition": definition}), changeContains(t, ids["table:"+table], constraintID)}
	}
	first := uuid.NewV7().String()
	draft, _ = saveChange(t, r, draft, "first-fk", designedFK("order_items", uuid.NewV7().String(), first)...)
	preview := func(edgeID string) (*ChangeProposalCandidate, error) {
		return r.PreviewChangeProposal(t.Context(), draft.Proposal.ProjectID, draft.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: draft.Proposal.Version, ProposalRevisionID: draft.Revision.ID, Commands: designedFK("payments", uuid.NewV7().String(), edgeID)})
	}
	if own, err := preview(uuid.NewV7().String()); err != nil || own.CandidateHash == nil {
		t.Fatalf("fresh FK edge refused: %+v %v", own, err)
	}
	if other, err := preview(first); err == nil && other.CandidateHash != nil {
		t.Fatal("reference id of another constraint's FK edge was accepted")
	}
}

// Review 2026-10-06, F82: under known MATCH SIMPLE a known-nullable source
// column proves target min zero whatever the deferrability: a NULL source
// row references no target row.
func TestDatabaseNullableSourceProvesTargetMinZeroWhenDeferrable(t *testing.T) {
	t.Parallel()
	r, out, ids := databaseVariant(t, func(cs []ImportCommand) {
		mutateAllRelationalFacets(t, cs, "constraint:payments:order_fk", func(m map[string]jsontext.Value) {
			m["deferrable"] = relationalRaw(t, known(true))
		})
	})
	item := databaseRelationship(t, r, out, ids, "references:payments:order_fk")
	if item.TargetCardinality.Min == nil || *item.TargetCardinality.Min != 0 {
		t.Fatalf("target min=%v basis=%v", item.TargetCardinality.Min, item.TargetCardinality.Basis)
	}
}

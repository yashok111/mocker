package backendmodel

import (
	"encoding/json/jsontext"
	"strings"
)

func (p *databaseProjection) effectiveRelationshipIntent(e Edge) bool {
	if p.effective == nil {
		return false
	}
	ids := []string{e.ID, e.From, e.To}
	if constraint := p.nodes[e.From]; constraint.ParentID != nil {
		ids = append(ids, *constraint.ParentID)
	}
	if f := p.selected(e.ID); f != nil {
		for _, pair := range f.ColumnPairs {
			ids = append(ids, pair.FromColumnID, pair.ToColumnID)
		}
	}
	sourceTable := ""
	if parent := p.nodes[e.From].ParentID; parent != nil {
		sourceTable = *parent
	}
	for _, table := range []string{sourceTable, e.To} {
		for _, node := range p.children[table] {
			if node.Kind == "constraint" || node.Kind == "index" {
				ids = append(ids, node.ID)
			}
		}
	}
	for _, id := range ids {
		if proof := p.sourceProof[p.selected(id)]; proof.status == "desired" {
			return true
		}
	}
	return false
}
func (p *databaseProjection) effectiveNullableBasis(id string, value jsontext.Value) string {
	if p.effective == nil {
		return ""
	}
	property := TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: p.in.FacetKey, Group: "nullable"}
	origin := p.effective.indexedReads().origins["node\x00"+id+"\x00"+sourcePropertyKey(property)]
	if origin.Kind != "intent" {
		return ""
	}
	return "Desired column " + id + " nullable " + string(value) + " from command " + origin.CommandID + "; runtime is unverified"
}
func (p *databaseProjection) selectedEvidence(f *relationalFacet) string {
	if p.source != nil || p.effective != nil {
		return strings.Join(p.sourceProof[f].evidenceIDs, ",")
	}
	return strings.Join(f.EvidenceIDs, ",")
}

package backendmodel

import (
	"encoding/json/jsontext"
	"testing"
)

// review 2026-10-06, F67: the criteria pass of a full proposal revision reads
// the ORIGINAL payload of a created node, but the seeded copy shared the
// *string ParentID with Delta.Created, which the created-record pass rewrites
// in place first. A field_equals criterion on such a node then re-mapped an
// already local parent ID and a valid bundle failed Preview with 422
// "Missing typed mapping: node <local uuid>".
func TestPortableRemapCriterionOnCreatedNodeWithParent(t *testing.T) {
	t.Parallel()
	const (
		created  = "30000000-0000-4000-8000-000000000001"
		parent   = "30000000-0000-4000-8000-000000000002"
		localNew = "30000000-0000-4000-8000-000000000011"
		localPar = "30000000-0000-4000-8000-000000000012"
	)
	parentID := parent
	revision := ChangeProposalRevision{Delta: ChangeDelta{Created: []ChangeCreatedRecord{{
		ChangeRecordRef: ChangeRecordRef{RecordType: "node", ID: created},
		Payload:         SourceAssertionPayload{RecordType: "node", Kind: "service", Name: "planned", ParentID: &parentID, Attributes: map[string]jsontext.Value{}},
	}}}, Criteria: []ChangeCriterion{{
		Key: "name", Kind: "field_equals", RecordType: "node", ID: created,
		Selector: jsontext.Value(`{"kind":"source","source":{"kind":"name"}}`),
		Expected: &SourcePropertyValue{Present: true, Value: jsontext.Value(`"planned"`)},
	}}}
	model := PortableModel{Proposals: []PortableProposal{{Full: &ChangeProposal{}, FullRevisions: []ChangeProposalRevision{revision}}}}
	remap := PortableRemap{IDs: []PortableMapping{
		{Origin: PortableIdentity{Kind: "node", ID: created}, LocalID: localNew},
		{Origin: PortableIdentity{Kind: "node", ID: parent}, LocalID: localPar},
	}}
	m := newPortableMapper(&model, remap, false)
	m.proposal(&model.Proposals[0], map[string]SourceAssertionPayload{})
	if m.err != nil {
		t.Fatal("valid criterion on a created child node failed to remap:", m.err)
	}
	got := model.Proposals[0].FullRevisions[0]
	if p := got.Delta.Created[0].Payload.ParentID; p == nil || *p != localPar {
		t.Fatal("created parent not mapped exactly once", p)
	}
	if got.Criteria[0].ID != localNew || got.Criteria[0].Expected == nil || string(got.Criteria[0].Expected.Value) != `"planned"` {
		t.Fatal("criterion not mapped", got.Criteria[0])
	}
}

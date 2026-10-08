package backendmodel

import "slices"

const ArchitectureMembershipVersion = "exact-node-members-v1"
const MaxArchitectureElementMembers = 5000
const MaxArchitectureDocumentMembers = 20000

// Membership is an exact set embedded in the immutable diagram version. It is
// deliberately not a recursive package selector: later source changes cannot
// silently change the ownership used by a historical diagram or saved view.
type ArchitectureMembership struct {
	Format  string   `json:"format"`
	NodeIDs []string `json:"nodeIds"`
}

func (m *ArchitectureMembership) UnmarshalJSON(b []byte) error {
	type plain ArchitectureMembership
	*m = ArchitectureMembership{}
	return strictAPIObject(b, []string{"format", "nodeIds"}, nil, (*plain)(m))
}

func architectureElementRefs(e ArchitectureElement) []DiagramRef {
	if e.Membership == nil {
		return e.Refs
	}
	refs := slices.Clone(e.Refs)
	for _, id := range e.Membership.NodeIDs {
		refs = append(refs, DiagramRef{Kind: "record", RecordType: "node", ID: id})
	}
	return refs
}

func validateArchitectureMembership(e ArchitectureElement) error {
	if e.Membership == nil {
		return nil
	}
	m := e.Membership
	if m.Format != ArchitectureMembershipVersion || m.NodeIDs == nil || len(m.NodeIDs) == 0 || len(m.NodeIDs) > MaxArchitectureElementMembers {
		return &FaultError{Status: 422, Code: "backend_architecture_membership_invalid", Message: "Use a nonempty exact-node-members-v1 membership within the element limit", Details: map[string]any{"elementId": e.ID, "path": "membership/nodeIds", "count": len(m.NodeIDs), "limit": MaxArchitectureElementMembers}}
	}
	seen := map[string]bool{}
	for _, ref := range e.Refs {
		if ref.Kind == "record" && ref.RecordType == "node" {
			seen[ref.ID] = true
		}
	}
	for _, id := range m.NodeIDs {
		if !ValidID(id) || seen[id] {
			return &FaultError{Status: 422, Code: "backend_architecture_membership_invalid", Message: "Membership needs unique exact node UUIDs, disjoint from inline node refs", Details: map[string]any{"elementId": e.ID, "path": "membership/nodeIds"}}
		}
		seen[id] = true
	}
	return nil
}

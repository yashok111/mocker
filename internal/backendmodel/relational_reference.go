package backendmodel

// relationalPairParents is the shared containment check for imported and
// designed ordered FK pairs. A native name never creates a reference.
func relationalPairParents(nodes map[string]Node, sourceTableID, targetTableID string, pair DatabaseColumnPair) bool {
	a, b := nodes[pair.FromColumnID], nodes[pair.ToColumnID]
	return a.Kind == "column" && b.Kind == "column" && a.ParentID != nil && b.ParentID != nil && *a.ParentID == sourceTableID && *b.ParentID == targetTableID
}

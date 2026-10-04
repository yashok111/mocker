package backendmodel

import "slices"

// Full desired snapshots use structural schema6. The explicitly selected
// immutable baseline separately controls which features a command may request.
func validateChangeBaselineVocabulary(graph *ChangeEvaluationSnapshot) []ImportDiagnostic {
	out := []ImportDiagnostic{}
	if graph.BaselineSchemaVersion != EventsSchemaVersion {
		return out
	}
	nodes := map[string]Node{}
	add := func(path, message string) {
		out = append(out, ImportDiagnostic{Code: "backend_change_vocabulary", Path: path, Message: message})
	}
	for _, node := range graph.Nodes {
		nodes[node.ID] = node
		if !slices.Contains(SupportedNodeKindsForProfile(EventsProfile), node.Kind) {
			add("nodes/"+node.ID, "Source5 proposals cannot introduce source6 node kinds")
			continue
		}
		if err := validateSourceStructuralAttributes(EventsProfile, node.Kind, node.Attributes, false); err != nil {
			add("nodes/"+node.ID+"/attributes", err.Error())
		}
	}
	for _, edge := range graph.Edges {
		if !slices.Contains(SupportedEdgeKindsForProfile(EventsProfile), edge.Kind) {
			add("edges/"+edge.ID, "Source5 proposals cannot introduce source6 edge kinds")
			continue
		}
		if err := validateSourceStructuralAttributes(EventsProfile, edge.Kind, edge.Attributes, true); err != nil {
			add("edges/"+edge.ID+"/attributes", err.Error())
		}
		from, fromOK := nodes[edge.From]
		to, toOK := nodes[edge.To]
		if fromOK && toOK && !sourceStructuralEndpoints(EventsProfile, edge, from, to) {
			add("edges/"+edge.ID, "Source5 proposals retain source5 endpoint semantics")
		}
	}
	return out
}

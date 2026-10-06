package backendmodel

import (
	"context"
	"slices"
)

func workspaceFiltered(in GraphQueryInput) bool {
	return in.ServiceID != "" || in.SourceSnapshotID != "" || in.Certainty != ""
}
func validateWorkspaceFilters(in GraphQueryInput) error {
	for _, id := range []string{in.ServiceID, in.SourceSnapshotID} {
		if id != "" && !ValidID(id) {
			return invalid("filters", "Expected exact service/snapshot UUID")
		}
	}
	if in.Certainty != "" && !slices.Contains([]string{"explicit", "inferred", "unresolved", "desired", "stale"}, in.Certainty) {
		return invalid("certainty", "Unsupported source certainty")
	}
	return nil
}

// Workspace filters run over the exact effective model, never the visible page.
func workspaceMatches(ctx context.Context, g *EffectiveGraphSnapshot, in GraphQueryInput, typ, id string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !workspaceFiltered(in) {
		return true, nil
	}
	if in.ServiceID != "" {
		nodes := g.indexedReads().payloads
		root, exists := nodes["node\x00"+in.ServiceID]
		if !exists || root.Kind != "service" {
			return false, invalid("serviceId", "Expected a service in the exact model")
		}
		belongs := func(id string) bool {
			seen := map[string]bool{}
			for id != "" && !seen[id] {
				seen[id] = true
				if id == in.ServiceID {
					return true
				}
				payload, ok := nodes["node\x00"+id]
				if !ok || payload.ParentID == nil {
					return false
				}
				id = *payload.ParentID
			}
			return false
		}
		match := belongs(id)
		if typ == "edge" {
			payload, ok := nodes["edge\x00"+id]
			match = ok && (belongs(payload.From) || belongs(payload.To))
		}
		if !match {
			return false, nil
		}
	}
	if in.Certainty == "" && in.SourceSnapshotID == "" {
		return true, nil
	}
	if g.Source == nil {
		return in.Certainty == "unresolved" && in.SourceSnapshotID == "", nil
	}
	proof, err := effectiveRecordProof(g, typ, id, nil)
	if err != nil {
		return false, err
	}
	if in.Certainty != "" && proof.status != in.Certainty {
		return false, nil
	}
	if in.SourceSnapshotID != "" {
		for _, ev := range g.Source.State.Evidence {
			if ev.Source.SnapshotID == in.SourceSnapshotID && slices.Contains(proof.evidenceIDs, ev.ID) {
				return true, nil
			}
		}
		return false, nil
	}
	return true, nil
}

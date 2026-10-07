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

// validateWorkspaceService refuses a serviceId that names no service in the
// exact effective model. queryEffectiveGraph calls it once before iterating:
// checked only inside workspaceMatches, a bad selector was refused only when a
// record survived the kind/search/parent prefilters, so the same request
// answered 400 or an empty 200 depending on the data (review 2026-10-06, F120).
func validateWorkspaceService(g *EffectiveGraphSnapshot, serviceID string) error {
	root, exists := g.indexedReads().payloads["node\x00"+serviceID]
	if !exists || root.Kind != "service" {
		return invalid("serviceId", "Expected a service in the exact model")
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
		if err := validateWorkspaceService(g, in.ServiceID); err != nil {
			return false, err
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

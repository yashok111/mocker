package backendmodel

import (
	"context"
	"slices"
)

// Architecture ownership is resolved against the exact pinned version, never its head.
func (r *Repo) resolveInteractionArchitecture(ctx context.Context, pid string, g *EffectiveGraphSnapshot, d DiagramDocument, previous *DiagramVersion) ([]DiagramGap, error) {
	p := d.Interactions
	if p == nil || p.Architecture == nil {
		return nil, nil
	}
	architecture, err := r.GetDiagram(ctx, pid, *p.Architecture)
	if err != nil {
		return nil, err
	}
	if architecture.Document.Kind != "architecture" || architecture.TargetHash != g.Pins.TargetHash {
		return nil, invalid("architecture", "Architecture must belong to the exact target")
	}
	members := map[string]bool{}
	for _, v := range architecture.Document.Payload.Elements {
		members[v.ID] = true
	}
	gaps := []DiagramGap{}
	for _, v := range p.Participants {
		if v.ArchitectureElementID == "" || members[v.ArchitectureElementID] {
			continue
		}
		retained := false
		if previous != nil && previous.Document.Interactions != nil && interactionRetainsMembership(previous, p, g.Pins.TargetHash, v.ID) {
			for _, old := range previous.Document.Interactions.Participants {
				if old.ID == v.ID && old.ArchitectureElementID == v.ArchitectureElementID {
					retained = true
				}
			}
		}
		if !retained {
			return nil, invalid("architectureElementId", "Participant does not belong to pinned architecture")
		}
		gaps = append(gaps, diagramGap(v.ID, "unresolved_membership", "Historical architecture membership is unavailable on the new target"))
	}
	return gaps, nil
}

func interactionRetainsMembership(previous *DiagramVersion, next *InteractionPayload, targetHash, id string) bool {
	if previous.TargetHash != targetHash || !interactionDependencyEqual(previous.Document.Interactions, next) {
		return true
	}
	return slices.ContainsFunc(previous.Gaps, func(gap DiagramGap) bool { return gap.SubjectID == id && gap.Code == "unresolved_membership" })
}

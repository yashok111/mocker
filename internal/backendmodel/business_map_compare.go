package backendmodel

import (
	"context"
	"slices"
)

// Architecture ownership is resolved against the exact pinned version, never its head.
func (r *Repo) resolveBusinessMapArchitecture(ctx context.Context, pid string, g *EffectiveGraphSnapshot, d DiagramDocument, previous *DiagramVersion) ([]DiagramGap, error) {
	return resolveBusinessMapArchitectureRead(ctx, r.db.R, pid, g, d, previous)
}
func resolveBusinessMapArchitectureRead(ctx context.Context, q importReader, pid string, g *EffectiveGraphSnapshot, d DiagramDocument, previous *DiagramVersion) ([]DiagramGap, error) {
	p := d.BusinessMap
	if p == nil || p.Architecture == nil {
		return nil, nil
	}
	architecture, err := loadDiagram(ctx, q, pid, p.Architecture.ID, p.Architecture.Version)
	if err == nil && architecture.Pin != *p.Architecture {
		return nil, diagramPinMismatch()
	}
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
	for _, v := range p.Elements {
		if v.ArchitectureElementID == "" || members[v.ArchitectureElementID] {
			continue
		}
		retained := false
		if previous != nil && previous.Document.BusinessMap != nil && businessMapRetainsMembership(previous, p, g.Pins.TargetHash, v.ID) {
			for _, old := range previous.Document.BusinessMap.Elements {
				if old.ID == v.ID && old.ArchitectureElementID == v.ArchitectureElementID {
					retained = true
				}
			}
		}
		if !retained {
			return nil, invalid("architectureElementId", "Element does not belong to pinned architecture")
		}
		gaps = append(gaps, diagramGap(v.ID, "unresolved_membership", "Historical architecture membership is unavailable on the new target"))
	}
	return gaps, nil
}

func businessMapRetainsMembership(previous *DiagramVersion, next *BusinessMapPayload, targetHash, id string) bool {
	if previous.TargetHash != targetHash || !businessMapDependencyEqual(previous.Document.BusinessMap, next) {
		return true
	}
	return slices.ContainsFunc(previous.Gaps, func(gap DiagramGap) bool { return gap.SubjectID == id && gap.Code == "unresolved_membership" })
}

func businessMapDependencyEqual(a, b *BusinessMapPayload) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Architecture == nil || b.Architecture == nil {
		return a.Architecture == b.Architecture
	}
	return *a.Architecture == *b.Architecture
}

package backendmodel

import (
	"context"
	"slices"
	"strings"
)

// Reconciliation carries associations without reading any API owner. Its source
// anchors describe the new graph, while the candidate binds the exact base vector.
func carryAPIArtifactContext(ctx context.Context, s *ImportSession, base *RevisionState, g *graphCandidate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if base.ArtifactContextV3 != nil {
		return invalid("context", "Legacy source import cannot drop a namespaced context; use composed source import")
	}
	if len(base.Revision.ArtifactPins) == 0 {
		return nil
	}
	full := revisionArtifactContext(base)
	bindings := artifactBindings(base.APIArtifactContext)
	pins := slices.Clone(base.Revision.ArtifactPins)
	if full != nil {
		bindings = full.APIBindings
	}
	var validation error
	if full != nil {
		validation = full.Validate(pins)
	} else {
		validation = ValidateAPIArtifactVector(pins, bindings)
	}
	if validation != nil {
		return validation
	}
	// Keep the original empty-pin candidate serialization as the source anchor.
	legacy, err := candidateJSON(s, g)
	if err != nil {
		return err
	}
	coverage := RevisionCoverage{Coverage: g.Coverage, Inventory: s.Inventory, Snapshots: g.Sources, StaleCounts: g.StaleCounts, ReconciliationGaps: g.ReconciliationGaps}
	state := RevisionState{Revision: Revision{SourceSnapshotIDs: sourceIDs(g.Sources)}, Nodes: g.Nodes, Edges: g.Edges, Evidence: g.Evidence, Sources: g.Sources, Inventory: s.Inventory}
	content, err := APIArtifactSourceContentHash(state, coverage)
	if err != nil {
		return err
	}
	g.ArtifactPins = pins
	if full != nil && !ArtifactContextUsesV1(pins, *full) {
		c := *full
		c.SourceContentHash = content
		c.SourceSemanticHash = hashBytes(legacy)
		if _, err := EncodeArtifactContext(c, pins); err != nil {
			return err
		}
		g.ArtifactContext = &c
	} else {
		g.APIArtifactContext = &APIArtifactContext{SourceContentHash: content, SourceSemanticHash: hashBytes(legacy), Bindings: bindings}
	}
	return ctx.Err()
}

func compareAPIArtifacts(ctx context.Context, before, after RevisionState, out *RevisionDelta) error {
	left, right := map[string]APIArtifactBinding{}, map[string]APIArtifactBinding{}
	for _, b := range artifactBindings(legacyArtifactContext(revisionArtifactContext(&before))) {
		left[b.SourceNodeID] = b
	}
	for _, b := range artifactBindings(legacyArtifactContext(revisionArtifactContext(&after))) {
		right[b.SourceNodeID] = b
	}
	ids := map[string]bool{}
	for id := range left {
		ids[id] = true
	}
	for id := range right {
		ids[id] = true
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	slices.Sort(ordered)
	for _, id := range ordered {
		if err := ctx.Err(); err != nil {
			return err
		}
		a, aOK := left[id]
		b, bOK := right[id]
		change := RecordDelta{RecordType: "artifact", ID: id, ChangeKinds: []string{}, ChangedPaths: []string{}}
		kind := "modified"
		if aOK {
			change.Before = &RecordSide{RecordType: "artifact", ID: id, Name: new(a.Ref.LastKnownLabel), Artifact: new(a.Ref)}
		}
		if bOK {
			change.After = &RecordSide{RecordType: "artifact", ID: id, Name: new(b.Ref.LastKnownLabel), Artifact: new(b.Ref)}
		}
		switch {
		case !aOK:
			kind = "added"
		case !bOK:
			kind = "removed"
		default:
			ah, err := requestDigest(a)
			if err != nil {
				return err
			}
			bh, err := requestDigest(b)
			if err != nil {
				return err
			}
			if ah == bh {
				continue
			}
			apiArtifactBindingChanges(a, b, &change)
		}
		countArtifactChange(&out.Summary, kind)
		change.ChangeKinds = append(change.ChangeKinds, kind)
		slices.Sort(change.ChangedPaths)
		out.Changes = append(out.Changes, change)
	}
	slices.SortFunc(out.Changes, func(a, b RecordDelta) int {
		if n := strings.Compare(a.RecordType, b.RecordType); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	})
	return nil
}

// apiArtifactBindingChanges records which fields of a binding differ; only a
// changed artifact content hash marks the change as a context change.
func apiArtifactBindingChanges(a, b APIArtifactBinding, change *RecordDelta) {
	if a.Ref.ContentHash != b.Ref.ContentHash {
		change.ContextChanged = true
		change.ChangedPaths = append(change.ChangedPaths, "/ref/contentHash")
	}
	for _, f := range []struct {
		differ bool
		path   string
	}{
		{a.Ref.ObjectHash != b.Ref.ObjectHash, "/ref/objectHash"},
		{a.Ref.RevisionID != b.Ref.RevisionID, "/ref/revisionId"},
		{a.Ref.ArtifactID != b.Ref.ArtifactID, "/ref/artifactId"},
		{a.Ref.ResolvedPointer != b.Ref.ResolvedPointer, "/ref/resolvedPointer"},
		{a.Ref.Selector != b.Ref.Selector, "/ref/selector"},
		{a.Reason != b.Reason, "/reason"},
		{a.Ref.LastKnownLabel != b.Ref.LastKnownLabel, "/ref/lastKnownLabel"},
		{a.SourceLastKnownLabel != b.SourceLastKnownLabel, "/sourceLastKnownLabel"},
		{a.SourceKind != b.SourceKind, "/sourceKind"},
	} {
		if f.differ {
			change.ChangedPaths = append(change.ChangedPaths, f.path)
		}
	}
}

func countArtifactChange(summary *ComparisonSummary, kind string) {
	if summary.Artifacts == nil {
		summary.Artifacts = new(ComparisonCounts)
	}
	switch kind {
	case "added":
		summary.Artifacts.Added++
	case "removed":
		summary.Artifacts.Removed++
	default:
		summary.Artifacts.Modified++
	}
}

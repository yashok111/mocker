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
	if len(base.Revision.ArtifactPins) == 0 {
		return nil
	}
	bindings := artifactBindings(base.APIArtifactContext)
	pins := slices.Clone(base.Revision.ArtifactPins)
	if err := ValidateAPIArtifactVector(pins, bindings); err != nil {
		return err
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
	g.APIArtifactContext = &APIArtifactContext{SourceContentHash: content, SourceSemanticHash: hashBytes(legacy), Bindings: bindings}
	return ctx.Err()
}

func compareAPIArtifacts(ctx context.Context, before, after RevisionState, out *RevisionDelta) error {
	left, right := map[string]APIArtifactBinding{}, map[string]APIArtifactBinding{}
	for _, b := range artifactBindings(before.APIArtifactContext) {
		left[b.SourceNodeID] = b
	}
	for _, b := range artifactBindings(after.APIArtifactContext) {
		right[b.SourceNodeID] = b
	}
	ids := map[string]bool{}
	for id := range left {
		ids[id] = true
	}
	for id := range right {
		ids[id] = true
	}
	ordered := []string{}
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
			if a.Ref.ContentHash != b.Ref.ContentHash {
				change.ContextChanged = true
				change.ChangedPaths = append(change.ChangedPaths, "/ref/contentHash")
			}
			if a.Ref.ObjectHash != b.Ref.ObjectHash {
				change.ChangedPaths = append(change.ChangedPaths, "/ref/objectHash")
			}
			if a.Ref.RevisionID != b.Ref.RevisionID {
				change.ChangedPaths = append(change.ChangedPaths, "/ref/revisionId")
			}
			if a.Ref.ArtifactID != b.Ref.ArtifactID {
				change.ChangedPaths = append(change.ChangedPaths, "/ref/artifactId")
			}
			if a.Ref.ResolvedPointer != b.Ref.ResolvedPointer {
				change.ChangedPaths = append(change.ChangedPaths, "/ref/resolvedPointer")
			}
			if a.Ref.Selector != b.Ref.Selector {
				change.ChangedPaths = append(change.ChangedPaths, "/ref/selector")
			}
			if a.Reason != b.Reason {
				change.ChangedPaths = append(change.ChangedPaths, "/reason")
			}
			if a.Ref.LastKnownLabel != b.Ref.LastKnownLabel {
				change.ChangedPaths = append(change.ChangedPaths, "/ref/lastKnownLabel")
			}
			if a.SourceLastKnownLabel != b.SourceLastKnownLabel {
				change.ChangedPaths = append(change.ChangedPaths, "/sourceLastKnownLabel")
			}
			if a.SourceKind != b.SourceKind {
				change.ChangedPaths = append(change.ChangedPaths, "/sourceKind")
			}
		}
		if out.Summary.Artifacts == nil {
			out.Summary.Artifacts = new(ComparisonCounts)
		}
		switch kind {
		case "added":
			out.Summary.Artifacts.Added++
		case "removed":
			out.Summary.Artifacts.Removed++
		default:
			out.Summary.Artifacts.Modified++
		}
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

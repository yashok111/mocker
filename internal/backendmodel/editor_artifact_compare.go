package backendmodel

import (
	"context"
	"maps"
	"slices"
	"strings"
)

func compareEditorArtifacts(ctx context.Context, before, after RevisionState, out *RevisionDelta) error {
	leftContext, rightContext := revisionArtifactContext(&before), revisionArtifactContext(&after)
	if (leftContext == nil || leftContext.DocumentVersion == "") && (rightContext == nil || rightContext.DocumentVersion == "") {
		return nil
	}
	left, right := map[string]EditorArtifactSide{}, map[string]EditorArtifactSide{}
	if err := collectEditorArtifactSides(before, leftContext, left); err != nil {
		return err
	}
	if err := collectEditorArtifactSides(after, rightContext, right); err != nil {
		return err
	}
	ids := map[string]bool{}
	for id := range left {
		ids[id] = true
	}
	for id := range right {
		ids[id] = true
	}
	if err := compareEditorBindingSides(ctx, left, right, ids, out); err != nil {
		return err
	}
	if err := compareEditorPinGroups(ctx, before, after, out); err != nil {
		return err
	}
	slices.SortFunc(out.Changes, func(a, b RecordDelta) int {
		if n := strings.Compare(a.RecordType, b.RecordType); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	})
	return nil
}

func collectEditorArtifactSides(state RevisionState, c *ArtifactContext, items map[string]EditorArtifactSide) error {
	if c == nil {
		return nil
	}
	pins := map[ArtifactKey]ArtifactPin{}
	for _, pin := range state.Revision.ArtifactPins {
		pins[artifactKey(pin)] = pin
	}
	for _, binding := range c.EditorBindings {
		id, err := EditorArtifactComparisonIdentity(binding)
		if err != nil {
			return err
		}
		items[id] = EditorArtifactSide{Pin: pins[ArtifactKey{binding.ArtifactKind, binding.ArtifactID}], Binding: binding}
	}
	return nil
}

func appendEditorArtifactDelta(out *RevisionDelta, id string, a, b *RecordSide, changedPaths []string, contextChanged bool) {
	kind := "modified"
	if a == nil {
		kind = "added"
	} else if b == nil {
		kind = "removed"
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
	slices.Sort(changedPaths)
	out.Changes = append(out.Changes, RecordDelta{RecordType: "artifact", ID: artifactRecordID(id), ChangeKinds: []string{kind}, ChangedPaths: changedPaths, Before: a, After: b, ContextChanged: contextChanged})
}

func compareEditorBindingSides(ctx context.Context, left, right map[string]EditorArtifactSide, ids map[string]bool, out *RevisionDelta) error {
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		if err := ctx.Err(); err != nil {
			return err
		}
		a, aOK := left[id]
		b, bOK := right[id]
		var old, next *RecordSide
		paths := []string{}
		contextChanged := false
		if aOK {
			old = &RecordSide{RecordType: "artifact", ID: artifactRecordID(id), Name: new(a.Binding.LastKnownLabel), EditorArtifact: new(a)}
		}
		if bOK {
			next = &RecordSide{RecordType: "artifact", ID: artifactRecordID(id), Name: new(b.Binding.LastKnownLabel), EditorArtifact: new(b)}
		}
		if aOK && bOK {
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
			contextChanged = a.Pin.ContentHash != b.Pin.ContentHash
			paths = editorBindingChangedPaths(a, b)
		}
		appendEditorArtifactDelta(out, id, old, next, paths, contextChanged)
	}
	return nil
}

func compareEditorPinGroups(ctx context.Context, before, after RevisionState, out *RevisionDelta) error {
	// Every group delta has its own identity, including pins with empty rosters.
	lp, rp := map[string]ArtifactPin{}, map[string]ArtifactPin{}
	groups := map[string]bool{}
	for _, pin := range before.Revision.ArtifactPins {
		id, err := ArtifactGroupIdentity(pin)
		if err != nil {
			return err
		}
		lp[id] = pin
		groups[id] = true
	}
	for _, pin := range after.Revision.ArtifactPins {
		id, err := ArtifactGroupIdentity(pin)
		if err != nil {
			return err
		}
		rp[id] = pin
		groups[id] = true
	}
	for _, id := range slices.Sorted(maps.Keys(groups)) {
		if err := ctx.Err(); err != nil {
			return err
		}
		a, aOK := lp[id]
		b, bOK := rp[id]
		if aOK && bOK && a == b {
			continue
		}
		var old, next *RecordSide
		if aOK {
			old = &RecordSide{RecordType: "artifact", ID: artifactRecordID(id), ArtifactGroup: &ArtifactGroupSide{Pin: a}}
		}
		if bOK {
			next = &RecordSide{RecordType: "artifact", ID: artifactRecordID(id), ArtifactGroup: &ArtifactGroupSide{Pin: b}}
		}
		paths := []string{}
		if aOK && bOK {
			if a.RevisionID != b.RevisionID {
				paths = append(paths, "/pin/revisionId")
			}
			if a.ContentHash != b.ContentHash {
				paths = append(paths, "/pin/contentHash")
			}
		}
		appendEditorArtifactDelta(out, id, old, next, paths, aOK && bOK && a.ContentHash != b.ContentHash)
	}
	return nil
}

func editorBindingChangedPaths(a, b EditorArtifactSide) []string {
	paths := []string{}
	if a.Pin != b.Pin {
		paths = append(paths, "/pin")
	}
	if a.Binding.ObjectHash != b.Binding.ObjectHash {
		paths = append(paths, "/binding/objectHash")
	}
	if !slices.Equal(a.Binding.SourceNodeIDs, b.Binding.SourceNodeIDs) {
		paths = append(paths, "/binding/sourceNodeIds")
	}
	if !slices.Equal(a.Binding.SourceLabels, b.Binding.SourceLabels) {
		paths = append(paths, "/binding/sourceLabels")
	}
	if a.Binding.Reason != b.Binding.Reason {
		paths = append(paths, "/binding/reason")
	}
	if a.Binding.LastKnownLabel != b.Binding.LastKnownLabel {
		paths = append(paths, "/binding/lastKnownLabel")
	}
	return paths
}

package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"slices"
	"strconv"
	"strings"
)

type compareRecord struct {
	id, key, name string
	fields        map[string]any
	freshness     *AssertionFreshness
}

// CompareRevisionStates computes a semantic delta without inventing committed
// revision pins for an unpublished candidate.
func CompareRevisionStates(ctx context.Context, before, after RevisionState) (*RevisionDelta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := &RevisionDelta{Changes: []RecordDelta{}}
	for _, typ := range []string{"node", "edge", "evidence"} {
		left, err := comparisonRecords(ctx, before, typ)
		if err != nil {
			return nil, err
		}
		right, err := comparisonRecords(ctx, after, typ)
		if err != nil {
			return nil, err
		}
		keys := map[string]bool{}
		for id := range left {
			keys[id] = true
		}
		for id := range right {
			keys[id] = true
		}
		for _, id := range slices.Sorted(maps.Keys(keys)) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			old, oldOK := left[id]
			current, newOK := right[id]
			change := RecordDelta{RecordType: typ, ID: id, ChangeKinds: []string{}, ChangedPaths: []string{}}
			if oldOK {
				change.Before = recordSide(typ, old)
			}
			if newOK {
				change.After = recordSide(typ, current)
			}
			counts := comparisonCounts(&out.Summary, typ)
			switch {
			case !oldOK:
				change.ChangeKinds = append(change.ChangeKinds, "added")
				counts.Added++
			case !newOK:
				change.ChangeKinds = append(change.ChangeKinds, "removed")
				counts.Removed++
			default:
				paths, err := changedFields(old.fields, current.fields)
				if err != nil {
					return nil, err
				}
				if len(paths) > 0 {
					change.ChangeKinds = append(change.ChangeKinds, "modified")
					change.ChangedPaths = append(change.ChangedPaths, paths...)
					counts.Modified++
				}
				oldFresh, err := requestDigest(old.freshness)
				if err != nil {
					return nil, err
				}
				newFresh, err := requestDigest(current.freshness)
				if err != nil {
					return nil, err
				}
				if oldFresh != newFresh {
					change.ChangeKinds = append(change.ChangeKinds, "freshness_changed")
					change.ChangedPaths = append(change.ChangedPaths, "/freshness")
					out.Summary.FreshnessChanges++
				}
				if old.key != current.key {
					if typ == "node" || typ == "edge" {
						out.Changes = append(out.Changes, RecordDelta{RecordType: "identity", ID: typ + "/" + current.key + "/" + id, ChangeKinds: []string{"identity_mapped"}, ChangedPaths: []string{"/externalKey"}, Before: recordSide(typ, old), After: recordSide(typ, current)})
						out.Summary.IdentityMappings++
					} else {
						change.ChangeKinds = append(change.ChangeKinds, "modified")
						change.ChangedPaths = append(change.ChangedPaths, "/externalKey")
						if len(paths) == 0 {
							counts.Modified++
						}
					}
				}
			}
			if len(change.ChangeKinds) > 0 {
				slices.Sort(change.ChangeKinds)
				change.ChangeKinds = slices.Compact(change.ChangeKinds)
				slices.Sort(change.ChangedPaths)
				change.ChangedPaths = slices.Compact(change.ChangedPaths)
				out.Changes = append(out.Changes, change)
			}
		}
	}
	for _, change := range sourceChanges(before, after) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		facet := "modified"
		if change.Before == nil {
			facet = "added"
		} else if change.After == nil && change.DeletionConfirmed {
			facet = "removed"
		}
		sourceID := change.Path
		if change.RepositoryID != "" || change.ProviderNamespace != "" {
			sourceID = change.RepositoryID + "/" + change.ProviderNamespace + "/" + change.Path
		}
		delta := RecordDelta{RecordType: "source", ID: sourceID, ChangeKinds: []string{facet}, ChangedPaths: []string{"/" + change.Kind}}
		if change.Before != nil {
			delta.Before = &RecordSide{RecordType: "source", ID: sourceID, SnapshotID: change.Before.SnapshotID, Path: change.Path, Name: new(change.Path)}
		}
		if change.After != nil {
			delta.After = &RecordSide{RecordType: "source", ID: sourceID, SnapshotID: change.After.SnapshotID, Path: change.Path, Name: new(change.Path)}
		}
		out.Changes = append(out.Changes, delta)
		out.Summary.SourceChanges++
	}
	if err := compareRevisionArtifacts(ctx, before, after, out); err != nil {
		return nil, err
	}
	slices.SortFunc(out.Changes, func(a, b RecordDelta) int {
		if n := strings.Compare(a.RecordType, b.RecordType); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, ctx.Err()
}

func recordSide(typ string, record compareRecord) *RecordSide {
	return &RecordSide{RecordType: typ, ID: record.id, Name: new(record.name), Key: new(record.key), Freshness: record.freshness}
}
func comparisonCounts(summary *ComparisonSummary, typ string) *ComparisonCounts {
	switch typ {
	case "node":
		return &summary.Nodes
	case "edge":
		return &summary.Edges
	default:
		return &summary.Evidence
	}
}

func comparisonRecords(ctx context.Context, state RevisionState, typ string) (map[string]compareRecord, error) {
	out := map[string]compareRecord{}
	attributes := func(values map[string]jsontext.Value) (map[string]any, error) {
		out := map[string]any{}
		for key, value := range values {
			// Preserve exact int64/number lexemes inside schema2 typed facets.
			var decoded jsontext.Value
			if err := json.Unmarshal(value, &decoded); err != nil {
				return nil, err
			}
			out[key] = decoded
		}
		return out, nil
	}
	switch typ {
	case "node":
		for _, n := range state.Nodes {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			attrs, err := attributes(n.Attributes)
			if err != nil {
				return nil, err
			}
			out[n.ID] = compareRecord{id: n.ID, key: n.ExternalKey, name: n.Name, freshness: n.Freshness, fields: map[string]any{"kind": n.Kind, "name": n.Name, "parentId": n.ParentID, "attributes": attrs}}
		}
	case "edge":
		for _, e := range state.Edges {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			attrs, err := attributes(e.Attributes)
			if err != nil {
				return nil, err
			}
			out[e.ID] = compareRecord{id: e.ID, key: e.ExternalKey, name: e.Kind, freshness: e.Freshness, fields: map[string]any{"kind": e.Kind, "from": e.From, "to": e.To, "attributes": attrs}}
		}
	case "evidence":
		for _, e := range state.Evidence {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			out[e.ID] = compareRecord{id: e.ID, key: e.ExternalKey, name: e.ExternalKey, freshness: e.Freshness, fields: map[string]any{"subjectId": e.SubjectID, "propertyPath": e.PropertyPath, "method": e.Method, "status": e.Status, "source": e.Source, "explanation": e.Explanation, "snippet": e.Snippet}}
		}
	}
	return out, nil
}

func changedFields(before, after map[string]any) ([]string, error) {
	paths := []string{}
	keys := map[string]bool{}
	for key := range before {
		keys[key] = true
	}
	for key := range after {
		keys[key] = true
	}
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		old, oldOK := before[key]
		current, newOK := after[key]
		if key == "attributes" && oldOK && newOK {
			inner, err := changedFields(old.(map[string]any), current.(map[string]any))
			if err != nil {
				return nil, err
			}
			for _, path := range inner {
				paths = append(paths, "/attributes"+path)
			}
			continue
		}
		left, err := requestDigest(old)
		if err != nil {
			return nil, err
		}
		right, err := requestDigest(current)
		if err != nil {
			return nil, err
		}
		if oldOK != newOK || left != right {
			paths = append(paths, "/"+strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1"))
		}
	}
	return paths, nil
}

// CompareRevisions reads exact immutable revisions; head changes cannot alter
// comparison pins, hashes or page ordering.
func (r *Repo) CompareRevisions(ctx context.Context, pid string, in CompareRevisionsInput) (*RevisionComparison, error) {
	if !ValidID(pid) || !ValidID(in.FromRevisionID) || !ValidID(in.ToRevisionID) {
		return nil, notFound()
	}
	if in.RecordType != "" && !slices.Contains([]string{"node", "edge", "evidence", "source", "identity", "artifact"}, in.RecordType) {
		return nil, invalid("recordType", "Unsupported comparison record type")
	}
	if in.ChangeKind != "" && !slices.Contains([]string{"added", "removed", "modified", "identity_mapped", "freshness_changed"}, in.ChangeKind) {
		return nil, invalid("changeKind", "Unsupported comparison change kind")
	}
	from, err := loadRevisionState(ctx, r.db.R, pid, in.FromRevisionID)
	if err != nil {
		return nil, err
	}
	to, err := loadRevisionState(ctx, r.db.R, pid, in.ToRevisionID)
	if err != nil {
		return nil, err
	}
	delta, err := CompareRevisionStates(ctx, *from, *to)
	if err != nil {
		return nil, err
	}
	var sourceBefore, sourceAfter *SourceReadContext
	var sourceGraphBefore, sourceGraphAfter *SourceGraphSnapshot
	if from.Revision.SchemaVersion == ComposedSchemaVersion {
		graph, err := r.ResolveSourceGraph(ctx, pid, in.FromRevisionID)
		if err != nil {
			return nil, err
		}
		sourceGraphBefore = graph
		sourceBefore = sourceReadContext(graph, "", "", "")
	}
	if to.Revision.SchemaVersion == ComposedSchemaVersion {
		graph, err := r.ResolveSourceGraph(ctx, pid, in.ToRevisionID)
		if err != nil {
			return nil, err
		}
		sourceGraphAfter = graph
		sourceAfter = sourceReadContext(graph, "", "", "")
	}
	if sourceGraphBefore != nil || sourceGraphAfter != nil {
		if err := appendSourceClaimDeltas(ctx, delta, sourceGraphBefore, sourceGraphAfter); err != nil {
			return nil, err
		}
	}
	left, right := comparisonPin(*from), comparisonPin(*to)
	hash, err := requestDigest(struct {
		Version  int
		From, To ComparisonPin
		Changes  []RecordDelta
	}{1, left, right, delta.Changes})
	if err != nil {
		return nil, err
	}
	if sourceBefore != nil || sourceAfter != nil {
		hash, err = requestDigest(struct {
			Version                   int
			From, To                  ComparisonPin
			Changes                   []RecordDelta
			SourceBefore, SourceAfter *SourceReadContext
		}{1, left, right, delta.Changes, sourceBefore, sourceAfter})
		if err != nil {
			return nil, err
		}
	}
	scope, err := requestDigest(struct{ Hash, RecordType, ChangeKind string }{hash, in.RecordType, in.ChangeKind})
	if err != nil {
		return nil, err
	}
	limit, after, err := decodeGraphPage(in.Limit, in.Cursor, "revision-compare", pid, scope, false)
	if err != nil {
		return nil, err
	}
	start := 0
	if after != "" {
		start, err = strconv.Atoi(after)
		if err != nil || start < 0 {
			return nil, invalid("cursor", "Invalid comparison offset")
		}
	}
	out := &RevisionComparison{SourceBefore: sourceBefore, SourceAfter: sourceAfter, From: left, To: right, ComparisonVersion: 1, ComparisonHash: hash, Summary: delta.Summary, CoverageBefore: from.Revision.Coverage, CoverageAfter: to.Revision.Coverage, Limitations: []string{"Structural comparison describes provider assertions; it does not verify runtime behavior or impact safety."}, Items: []ComparisonItem{}}
	for _, state := range []*RevisionState{from, to} {
		for _, gap := range state.Revision.Coverage.Gaps {
			out.Limitations = append(out.Limitations, gap)
		}
	}
	slices.Sort(out.Limitations)
	out.Limitations = slices.Compact(out.Limitations)
	filtered := []RecordDelta{}
	for _, change := range delta.Changes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if in.RecordType != "" && in.RecordType != change.RecordType || in.ChangeKind != "" && !slices.Contains(change.ChangeKinds, in.ChangeKind) {
			continue
		}
		filtered = append(filtered, change)
	}
	if start > len(filtered) {
		return nil, invalid("cursor", "Comparison offset exceeds result")
	}
	end := min(start+limit, len(filtered))
	for _, change := range filtered[start:end] {
		item := ComparisonItem{RecordType: change.RecordType, ID: change.ID, ChangeKinds: change.ChangeKinds, ChangedPaths: change.ChangedPaths}
		item.ContextChanged = change.ContextChanged
		if change.Before != nil {
			item.SourceClaimBefore = change.Before.SourceClaim
			item.EditorArtifactBefore = change.Before.EditorArtifact
			item.ArtifactGroupBefore = change.Before.ArtifactGroup
			item.ArtifactBefore = change.Before.Artifact
			item.Before = comparisonRef(pid, in.FromRevisionID, change.Before)
			item.NameBefore = change.Before.Name
			item.KeyBefore = change.Before.Key
			item.FreshnessBefore = change.Before.Freshness
		}
		if change.After != nil {
			item.SourceClaimAfter = change.After.SourceClaim
			item.EditorArtifactAfter = change.After.EditorArtifact
			item.ArtifactGroupAfter = change.After.ArtifactGroup
			item.ArtifactAfter = change.After.Artifact
			item.After = comparisonRef(pid, in.ToRevisionID, change.After)
			item.NameAfter = change.After.Name
			item.KeyAfter = change.After.Key
			item.FreshnessAfter = change.After.Freshness
		}
		out.Items = append(out.Items, item)
	}
	if end < len(filtered) {
		out.NextCursor = encodeGraphPage("revision-compare", pid, scope, strconv.Itoa(end))
	}
	return out, ctx.Err()
}

func comparisonPin(state RevisionState) ComparisonPin {
	out := ComparisonPin{RevisionID: state.Revision.ID, SemanticHash: state.Revision.SemanticHash, SourceSnapshotIDs: slices.Clone(state.Revision.SourceSnapshotIDs)}
	if source := primarySource(state); source != nil {
		out.PrimarySnapshotID = new(source.ID)
		out.ManifestHash = new(source.ManifestHash)
	}
	return out
}
func comparisonRef(pid, rid string, side *RecordSide) *ComparisonRef {
	ref := &ComparisonRef{ProjectID: pid, RevisionID: rid, RecordType: side.RecordType, ID: side.ID}
	if side.RecordType == "source" {
		ref.ID = ""
		ref.SnapshotID = side.SnapshotID
		ref.Path = side.Path
	}
	return ref
}

func compareRevisionArtifacts(ctx context.Context, before, after RevisionState, out *RevisionDelta) error {
	if err := compareAPIArtifacts(ctx, before, after, out); err != nil {
		return err
	}
	return compareEditorArtifacts(ctx, before, after, out)
}

package backendmodel

import "slices"

func primarySource(s RevisionState) *SourceSnapshot {
	for i := range s.Sources {
		if s.Sources[i].Role == "primary" {
			return &s.Sources[i]
		}
	}
	if len(s.Sources) == 1 {
		return &s.Sources[0]
	}
	return nil
}
func completeFiles(s RevisionState) bool {
	src := primarySource(s)
	if src == nil || src.Consistency != "verified" {
		return false
	}
	for _, x := range s.Inventory {
		if x.Category == "files" {
			return x.Status == "complete" && x.Denominator != nil && *x.Denominator == x.KnownCount && x.KnownCount == int64(len(src.Files))
		}
	}
	return false
}
func sourceChanges(before, after RevisionState) []SourceChange {
	if before.Revision.SchemaVersion == ComposedSchemaVersion || after.Revision.SchemaVersion == ComposedSchemaVersion {
		return composedSourceChanges(before, after)
	}
	old, next := map[string]SourceFileSummary{}, map[string]SourceFileSummary{}
	fill := func(s RevisionState, m map[string]SourceFileSummary) {
		if src := primarySource(s); src != nil {
			for _, f := range src.Files {
				m[f.Path] = SourceFileSummary{SnapshotID: src.ID, Path: f.Path, ContentHash: f.ContentHash, AnalysisStatus: f.AnalysisStatus}
			}
		}
	}
	fill(before, old)
	fill(after, next)
	paths := []string{}
	for p := range old {
		paths = append(paths, p)
	}
	for p := range next {
		if _, ok := old[p]; !ok {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	out := []SourceChange{}
	for _, p := range paths {
		a, aok := old[p]
		b, bok := next[p]
		c := SourceChange{Path: p}
		if aok {
			c.Before = &a
		}
		if bok {
			c.After = &b
		}
		switch {
		case !aok:
			c.Kind = "newly_observed"
			c.AdditionConfirmed = completeFiles(before)
		case !bok:
			c.Kind = "no_longer_observed"
			c.DeletionConfirmed = completeFiles(after)
		case a.ContentHash != b.ContentHash:
			c.Kind = "content_changed"
		case a.AnalysisStatus != b.AnalysisStatus:
			c.Kind = "analysis_changed"
		default:
			continue
		}
		out = append(out, c)
	}
	return out
}

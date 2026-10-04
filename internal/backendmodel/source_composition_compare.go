package backendmodel

import (
	"maps"
	"slices"
)

func composedSourceFiles(state RevisionState) map[string]SourceFileSummary {
	out := map[string]SourceFileSummary{}
	for _, source := range state.Sources {
		if state.Revision.SchemaVersion == ComposedSchemaVersion && source.Role != "active_source" {
			continue
		}
		if state.Revision.SchemaVersion != ComposedSchemaVersion && source.Role != "primary" && len(state.Sources) != 1 {
			continue
		}
		for _, file := range source.Files {
			key := source.RepositoryID + "\x00" + source.Provider.Namespace + "\x00" + file.Path
			out[key] = SourceFileSummary{RepositoryID: source.RepositoryID, ProviderNamespace: source.Provider.Namespace, SnapshotID: source.ID, Path: file.Path, ContentHash: file.ContentHash, AnalysisStatus: file.AnalysisStatus}
		}
	}
	return out
}
func composedSourceFilesComplete(state RevisionState, file SourceFileSummary) bool {
	// RevisionState carries only the importing partition inventory. It cannot
	// certify absence for another source6 repository/provider partition.
	if state.Revision.SchemaVersion == ComposedSchemaVersion {
		return false
	}
	for _, source := range state.Sources {
		if source.RepositoryID != file.RepositoryID || source.Provider.Namespace != file.ProviderNamespace || source.Consistency != "verified" {
			continue
		}
		for _, inv := range state.Inventory {
			if inv.Category == "files" && inv.Status == "complete" && inv.Denominator != nil && *inv.Denominator == inv.KnownCount && inv.KnownCount == int64(len(source.Files)) {
				return true
			}
		}
	}
	return false
}
func composedSourceChanges(before, after RevisionState) []SourceChange {
	old, next := composedSourceFiles(before), composedSourceFiles(after)
	keys := map[string]bool{}
	for key := range old {
		keys[key] = true
	}
	for key := range next {
		keys[key] = true
	}
	out := make([]SourceChange, 0, len(keys))
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		a, aok := old[key]
		b, bok := next[key]
		file := b
		if !bok {
			file = a
		}
		change := SourceChange{Path: file.Path, RepositoryID: file.RepositoryID, ProviderNamespace: file.ProviderNamespace}
		if aok {
			change.Before = new(a)
		}
		if bok {
			change.After = new(b)
		}
		switch {
		case !aok:
			change.Kind = "newly_observed"
			change.AdditionConfirmed = composedSourceFilesComplete(before, b)
		case !bok:
			change.Kind = "no_longer_observed"
			change.DeletionConfirmed = composedSourceFilesComplete(after, a)
		case a.ContentHash != b.ContentHash:
			change.Kind = "content_changed"
		case a.AnalysisStatus != b.AnalysisStatus:
			change.Kind = "analysis_changed"
		default:
			continue
		}
		out = append(out, change)
	}
	return out
}

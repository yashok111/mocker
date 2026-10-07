package backendmodel

import (
	"context"
	"maps"
	"slices"
	"strings"
)

// Desired historical references are exact project-owned structural addresses.
// They do not grant source ownership or evidence to a proposed record.
func validateChangeHistoricalReferences(ctx context.Context, q importReader, pid string, e *changeEvaluation) error {
	var ancestors map[string]bool
	history := map[string]*SourceGraphSnapshot{}
	// Records are walked in ID order: the first failure is the one diagnostic,
	// and ranging over the map reported a different record (and message) on
	// identical previews (review 2026-10-06, F41).
	for _, id := range slices.Sorted(maps.Keys(e.records)) {
		record := e.records[id]
		p := record.Payload
		if !relationalSubject(p.Kind, p.Attributes, record.RecordType == "edge") {
			continue
		}
		refs, err := relationalReferencesMode(p.Kind, p.Attributes, record.RecordType == "edge", true, false)
		if err != nil {
			return err
		}
		// relationalReferencesMode ranges over the facet map, so within one
		// record the order is random too.
		slices.SortStableFunc(refs, func(a, b relationalReference) int { return strings.Compare(a.Path, b.Path) })
		for _, ref := range refs {
			if ref.HistoricalRevisionID == "" {
				continue
			}
			if ancestors == nil {
				ancestors, err = sourceAncestors(ctx, q, pid, e.revision.BaseRevisionID)
				if err != nil {
					return err
				}
			}
			if !ancestors[ref.HistoricalRevisionID] {
				return invalid(ref.Path, "Historical reference must pin the exact project baseline or its ancestor")
			}
			graph := history[ref.HistoricalRevisionID]
			if graph == nil {
				graph, err = e.referencedSource(ctx, q, pid, ref.HistoricalRevisionID)
				if err != nil {
					return err
				}
				history[ref.HistoricalRevisionID] = graph
			}
			if err = validateChangeHistoricalSubject(e.source, graph, record, ref); err != nil {
				return err
			}

		}
	}
	return ctx.Err()
}

func validateChangeHistoricalSubject(source, graph *SourceGraphSnapshot, record ChangeCreatedRecord, ref relationalReference) error {
	found := false
	for _, node := range graph.State.Nodes {
		if node.ID == ref.ID && relationalSubject(node.Kind, node.Attributes, false) {
			found = true
			break
		}
	}
	if !found {
		return invalid(ref.Path, "Historical reference must identify a relational subject in that exact revision")
	}
	// An imported subject retains every baseline repository qualification.
	owners := map[string]bool{}
	for _, identity := range source.Identities {
		if identity.RecordType == record.RecordType && identity.ID == record.ID {
			owners[identity.RepositoryID] = true
		}
	}
	if len(owners) > 0 {
		sameOwner := false
		for repository := range owners {
			if sourceHistoricalTargetExists(graph, ref.ID, repository) {
				sameOwner = true
				break
			}
		}
		if !sameOwner {
			return invalid(ref.Path, "Imported subject historical reference changed repository")
		}
	}
	return nil
}

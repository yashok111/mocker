package backendmodel

import "context"

// Desired historical references are exact project-owned structural addresses.
// They do not grant source ownership or evidence to a proposed record.
func validateChangeHistoricalReferences(ctx context.Context, q importReader, pid string, e *changeEvaluation) error {
	var ancestors map[string]bool
	history := map[string]*SourceGraphSnapshot{}
	for _, record := range e.records {
		p := record.Payload
		if !relationalSubject(p.Kind, p.Attributes, record.RecordType == "edge") {
			continue
		}
		refs, err := relationalReferencesMode(p.Kind, p.Attributes, record.RecordType == "edge", true, false)
		if err != nil {
			return err
		}
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

package backendmodel

import (
	"context"
	"encoding/json/v2"
)

func sourceAncestors(ctx context.Context, q importReader, pid, rid string) (map[string]bool, error) {
	ancestors := map[string]bool{}
	for rid != "" && !ancestors[rid] {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		state, err := loadSourceState(ctx, q, pid, rid)
		if err != nil {
			return nil, err
		}
		ancestors[rid] = true
		if state.Revision.ParentRevisionID == nil {
			break
		}
		rid = *state.Revision.ParentRevisionID
	}
	return ancestors, nil
}

func validateComposedHistory(ctx context.Context, q importReader, s *ImportSession, graph *SourceGraphSnapshot) error {
	ancestors, err := sourceAncestors(ctx, q, s.ProjectID, s.BaseRevisionID)
	if err != nil {
		return err
	}
	history := map[string]*SourceGraphSnapshot{}
	for _, a := range graph.Assertions {
		if !relationalSubject(a.Payload.Kind, a.Payload.Attributes, a.RecordType == "edge") {
			continue
		}
		refs, err := relationalReferences(a.Payload.Kind, a.Payload.Attributes, a.RecordType == "edge", true)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			if ref.HistoricalRevisionID == "" {
				continue
			}
			if !ancestors[ref.HistoricalRevisionID] {
				return semantic(ref.Path, "Historical source reference must pin the base or an ancestor in this project")
			}
			prior := history[ref.HistoricalRevisionID]
			if prior == nil {
				prior, err = loadSourceGraph(ctx, q, s.ProjectID, ref.HistoricalRevisionID)
				if err != nil {
					return err
				}
				history[ref.HistoricalRevisionID] = prior
			}
			if !sourceHistoricalTargetExists(prior, ref.ID, a.Owner.RepositoryID) {
				return semantic(ref.Path, "Historical target must be a relational subject in the same repository")
			}
		}
	}
	return validateSourceMigrationSupport(graph)
}

func sourceHistoricalTargetExists(graph *SourceGraphSnapshot, id, repository string) bool {
	for _, n := range graph.State.Nodes {
		if n.ID != id || !relationalSubject(n.Kind, n.Attributes, false) {
			continue
		}
		if n.Ownership != nil && n.Ownership.RepositoryID == repository {
			return true
		}
		for _, identity := range graph.Identities {
			if identity.RecordType == "node" && identity.ID == id && identity.RepositoryID == repository {
				return true
			}
		}
	}
	return false
}

func validateSourceMigrationSupport(graph *SourceGraphSnapshot) error {
	for _, a := range graph.Assertions {
		if a.RecordType != "node" || a.Payload.Kind == "migration" || !relationalSubject(a.Payload.Kind, a.Payload.Attributes, false) {
			continue
		}
		facets, _, err := relationalFacetObject(a.Payload.Kind, a.Payload.Attributes)
		if err != nil {
			return err
		}
		for key, raw := range facets {
			facet, err := decodeRelationalFacet(a.Payload.Kind, raw, true)
			if err != nil {
				return err
			}
			if facet.SourceKind != "migration" || !sourceFacetComplete(facet) {
				continue
			}
			if err := validateSourceMigrationFacet(graph, a, key, facet); err != nil {
				return err
			}
		}
	}
	return nil
}

func sourceFacetComplete(f *relationalFacet) bool {
	return f.AnalysisStatus == "complete" || f.ColumnsStatus == "complete" || f.ConstraintsStatus == "complete" || f.DependenciesStatus == "complete" || f.BodyStatus == "complete"
}

func validateSourceMigrationFacet(graph *SourceGraphSnapshot, a ProviderAssertion, key string, facet *relationalFacet) error {
	parents := map[string]string{}
	migrations := []ProviderAssertion{}
	for _, claim := range graph.Assertions {
		if claim.Owner.RepositoryID != a.Owner.RepositoryID || claim.Owner.ProviderNamespace != a.Owner.ProviderNamespace {
			continue
		}
		if claim.Payload.ParentID != nil {
			parents[claim.RecordID] = *claim.Payload.ParentID
		}
		if claim.Payload.Kind == "migration" {
			migrations = append(migrations, claim)
		}
	}
	supported, incomplete := false, false
	for _, migration := range migrations {
		facets, _, err := relationalFacetObject(migration.Payload.Kind, migration.Payload.Attributes)
		if err != nil {
			return err
		}
		if facets[key] == nil {
			continue
		}
		var mf relationalFacet
		if err := json.Unmarshal(facets[key], &mf); err != nil {
			return err
		}
		if mf.Dialect != facet.Dialect {
			continue
		}
		found, gap := sourceMigrationChangesSupport(mf, a.RecordID, parents)
		supported = supported || found
		incomplete = incomplete || gap
	}
	if !supported || incomplete {
		return semantic("facets", "Complete migration-derived claim requires ordered supported migration changes from its own provider")
	}
	return nil
}

func sourceMigrationChangesSupport(facet relationalFacet, subject string, parents map[string]string) (supported, incomplete bool) {
	for _, change := range facet.Changes {
		if change.Target.Kind != "candidate" || !sourceContainmentRelated(subject, change.Target.ObjectID, parents) {
			continue
		}
		supported = true
		if facet.DerivationStatus != "complete" || facet.Order == nil || facet.Order.Status != "known" || string(facet.Order.Value) == "null" || change.Operation == "unknown" {
			incomplete = true
		}
	}
	return supported, incomplete
}

func sourceContainmentRelated(a, b string, parents map[string]string) bool {
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		seen := map[string]bool{}
		for id := pair[0]; id != "" && !seen[id]; id = parents[id] {
			if id == pair[1] {
				return true
			}
			seen[id] = true
		}
	}
	return false
}

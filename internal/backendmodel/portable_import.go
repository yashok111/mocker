package backendmodel

import (
	"cmp"
	"context"
	"database/sql"
	"slices"
	"time"
)

type PortableAttribution struct {
	InstallationID string `json:"installationId"`
	ProjectID      string `json:"projectId"`
	ID             string `json:"id"`
	Version        int64  `json:"version"`
	ContentHash    string `json:"contentHash"`
}
type PortableImportOptions struct {
	Remap           PortableRemap
	OriginProjectID string
	AfterStage      func(string) error
}

// ImportPortableModelTx never opens, commits or publishes a second transaction.
// Preview calls it under a savepoint and rolls back owner rows before saving its
// prepared map. Commit calls the same validation path before its durable receipt.
func (r *Repo) ImportPortableModelTx(ctx context.Context, tx *sql.Tx, input PortableModel, options PortableImportOptions) (*PortableModel, error) {
	model, err := portableClone(input)
	if err != nil {
		return nil, err
	}
	p := &model.Project
	if !ValidID(p.ID) || !ValidID(p.CurrentRevisionID) || len(model.Sources) == 0 || len(model.Sources) > 1000 || len(p.Repositories) > MaxSourceRepositories {
		return nil, invalid("project", "Invalid bounded portable project")
	}
	name, err := normalizeName(p.Name)
	if err != nil {
		return nil, err
	}
	p.Name = name
	installation, err := installationID(ctx, tx)
	if err != nil {
		return nil, err
	}
	if installation != options.Remap.InstallationID {
		return nil, invalid("installationId", "Prepared map belongs to another installation")
	}
	if err := r.validatePortableMappingsTx(ctx, tx, options.Remap); err != nil {
		return nil, err
	}
	now := p.CreatedAt
	if now.IsZero() || p.UpdatedAt.IsZero() {
		return nil, invalid("project", "Imported project timestamps are required")
	}
	p.Version = 1
	p.Capabilities = Features()
	p.CreatedAt = now

	if _, err := tx.ExecContext(ctx, `INSERT INTO backend_projects(id,name,version,current_revision_id,created_at,updated_at) VALUES(?,?,1,?,?,?)`, p.ID, p.Name, p.CurrentRevisionID, now.Format(time.RFC3339Nano), p.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		return nil, err
	}
	repositories := map[string]bool{}
	for _, repo := range p.Repositories {
		if !ValidID(repo.ID) || repositories[repo.ID] {
			return nil, invalid("repository", "Duplicate or invalid repository")
		}
		repositories[repo.ID] = true
		if _, err := normalizeName(repo.LogicalName); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO backend_repositories(id,project_id,logical_name) VALUES(?,?,?)`, repo.ID, p.ID, repo.LogicalName); err != nil {
			return nil, err
		}
	}
	hashes := map[string]string{}
	sources := map[string]bool{}
	for i := range model.Sources {
		source := &model.Sources[i]
		if source.Revision.ProjectID != p.ID || sources[source.Revision.ID] {
			return nil, invalid("revision", "Duplicate or foreign source revision")
		}
		dependencies, err := PortableRevisionDependencies(*source)
		if err != nil {
			return nil, err
		}
		for _, id := range dependencies {
			if !sources[id] {
				return nil, invalid("closure", "Source dependencies must be complete and dependency-first")
			}
		}
		for _, s := range source.Coverage.Snapshots {
			if !repositories[s.RepositoryID] {
				return nil, invalid("repository", "Source snapshot references missing repository")
			}
		}
		origin := portableAttribution(options, "revision", source.Revision.ID, 1, source.Revision.SemanticHash)
		if err := rehashPortableSource(ctx, tx, source, hashes); err != nil {
			return nil, err
		}
		source.Revision.ImportOrigin = &origin
		if err := r.resolvePortableContextTx(ctx, tx, source); err != nil {
			return nil, err
		}
		// Binding object hashes may change under an explicit mapped owner. Recompute
		// the final v3 semantic identity after exact selector resolution.
		c, err := DecodeVersionedArtifactContext(source.ArtifactContext, nil)
		if err != nil {
			return nil, err
		}
		source.Revision.SemanticHash, err = ArtifactContextV3SemanticHash(*c.V3)
		if err != nil {
			return nil, err
		}
		hashes[origin.ContentHash] = source.Revision.SemanticHash
		if err := insertPortableSourceTx(ctx, tx, source); err != nil {
			return nil, err
		}
		sources[source.Revision.ID] = true
	}
	if !sources[p.CurrentRevisionID] {
		return nil, invalid("project", "Selected source revision missing")
	}
	if err := r.publishPortableIdentityBindingsTx(ctx, tx, &model); err != nil {
		return nil, err
	}
	for i := range model.Proposals {
		if err := r.importPortableProposalTx(ctx, tx, p.ID, &model.Proposals[i], hashes, options); err != nil {
			return nil, err
		}
	}
	// Target validation also pins the selected source/proposal after local rehash.
	if _, err := resolveEffectiveGraph(ctx, tx, p.ID, model.Target); err != nil {
		return nil, err
	}
	diagramPins := map[DiagramPin]DiagramPin{}
	for i := range model.Diagrams {
		v := &model.Diagrams[i]
		old := v.Pin
		if v.ProjectID != p.ID {
			return nil, invalid("diagram", "Foreign local diagram project")
		}
		origin := portableAttribution(options, "diagram", old.ID, old.Version, old.ContentHash)
		if err := r.importPortableDiagramTx(ctx, tx, v, diagramPins); err != nil {
			return nil, err
		}
		v.ImportOrigin = &origin
		// Imported attribution is stored with the immutable version, before insert.
		if err := insertPortableDiagramTx(ctx, tx, v); err != nil {
			return nil, err
		}
		diagramPins[old] = v.Pin
		if options.AfterStage != nil && v.Document.Kind == "architecture" {
			if err := options.AfterStage("after_architecture"); err != nil {
				return nil, err
			}
		}
	}
	if options.AfterStage != nil {
		if err := options.AfterStage("before_views"); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(model.DiagramViews, func(a, b DiagramView) int { return cmp.Or(cmp.Compare(a.ID, b.ID), cmp.Compare(b.Version, a.Version)) })
	for i := range model.DiagramViews {
		v := &model.DiagramViews[i]
		pin, ok := diagramPins[v.State.Diagram]
		if !ok {
			return nil, invalid("view", "Missing exact diagram dependency")
		}
		v.State.Diagram = pin
		if err := r.ImportPortableDiagramViewTx(ctx, tx, p.ID, v); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(model.SavedViews, func(a, b SavedView) int { return cmp.Or(cmp.Compare(a.ID, b.ID), cmp.Compare(b.Version, a.Version)) })
	for i := range model.SavedViews {
		if err := r.importPortableSavedViewTx(ctx, tx, p.ID, &model.SavedViews[i]); err != nil {
			return nil, err
		}
	}
	if err := importPortableAnnotationsTx(ctx, tx, p.ID, model.Annotations); err != nil {
		return nil, err
	}
	return &model, nil
}
func portableAttribution(options PortableImportOptions, kind, id string, version int64, hash string) PortableAttribution {
	original := id
	for _, entry := range options.Remap.IDs {
		if entry.Origin.Kind == kind && entry.LocalID == id {
			original = entry.Origin.ID
			break
		}
	}
	return PortableAttribution{InstallationID: options.Remap.OriginInstallationID, ProjectID: options.OriginProjectID, ID: original, Version: version, ContentHash: hash}
}
func (r *Repo) publishPortableIdentityBindingsTx(ctx context.Context, tx *sql.Tx, model *PortableModel) error {
	var source *PortableSource
	for i := range model.Sources {
		if model.Sources[i].Revision.ID == model.Project.CurrentRevisionID {
			source = &model.Sources[i]
		}
	}
	if source == nil {
		return invalid("source", "Selected source not imported")
	}
	type binding struct {
		owner         AssertionOwnership
		kind, key, id string
	}
	bindings := []binding{}
	for _, a := range source.Assertions {
		bindings = append(bindings, binding{a.Owner, a.RecordType, a.ExternalKey, a.RecordID})
	}
	if source.Revision.SchemaVersion != ComposedSchemaVersion {
		for _, n := range source.Nodes {
			if n.Ownership != nil {
				bindings = append(bindings, binding{*n.Ownership, "node", n.ExternalKey, n.ID})
			}
		}
		for _, e := range source.Edges {
			if e.Ownership != nil {
				bindings = append(bindings, binding{*e.Ownership, "edge", e.ExternalKey, e.ID})
			}
		}
	}
	for _, e := range source.Evidence {
		if e.Ownership != nil {
			bindings = append(bindings, binding{*e.Ownership, "evidence", e.ExternalKey, e.ID})
		}
	}
	seen := map[string]bool{}
	for _, b := range bindings {
		key := b.owner.RepositoryID + ":" + b.owner.ProviderNamespace + ":" + b.kind + ":" + b.key
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, err := tx.ExecContext(ctx, `INSERT INTO backend_identity_bindings(project_id,repository_id,provider_namespace,record_type,external_key,id,state,revision_id) VALUES(?,?,?,?,?,?,'active',?)`, model.Project.ID, b.owner.RepositoryID, b.owner.ProviderNamespace, b.kind, b.key, b.id, source.Revision.ID); err != nil {
			return err
		}
	}
	return nil
}

func portableReplaceHash(value *string, hashes map[string]string) {
	if v, ok := hashes[*value]; ok {
		*value = v
	}
}

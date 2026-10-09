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
	if err := r.insertPortableProjectTx(ctx, tx, &model, options); err != nil {
		return nil, err
	}
	repositories, err := insertPortableRepositoriesTx(ctx, tx, p)
	if err != nil {
		return nil, err
	}
	hashes := map[string]string{}
	sources := map[string]bool{}
	for i := range model.Sources {
		if err := r.importPortableSourceTx(ctx, tx, p.ID, &model.Sources[i], options, repositories, sources, hashes); err != nil {
			return nil, err
		}
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
	diagramPins, err := r.importPortableDiagramsTx(ctx, tx, &model, options)
	if err != nil {
		return nil, err
	}
	if err := r.importPortableViewsTx(ctx, tx, &model, diagramPins); err != nil {
		return nil, err
	}
	if err := writeProjectStartView(ctx, tx, p); err != nil {
		return nil, err
	}
	if err := importPortableAnnotationsTx(ctx, tx, p.ID, model.Annotations); err != nil {
		return nil, err
	}
	return &model, nil
}

// insertPortableProjectTx validates the imported project against this
// installation's prepared map and inserts its row at version 1.
func (r *Repo) insertPortableProjectTx(ctx context.Context, tx *sql.Tx, model *PortableModel, options PortableImportOptions) error {
	p := &model.Project
	if !ValidID(p.ID) || !ValidID(p.CurrentRevisionID) || len(model.Sources) == 0 || len(model.Sources) > 1000 || len(p.Repositories) > MaxSourceRepositories {
		return invalid("project", "Invalid bounded portable project")
	}
	name, err := normalizeName(p.Name)
	if err != nil {
		return err
	}
	p.Name = name
	installation, err := installationID(ctx, tx)
	if err != nil {
		return err
	}
	if installation != options.Remap.InstallationID {
		return invalid("installationId", "Prepared map belongs to another installation")
	}
	if err := r.validatePortableMappingsTx(ctx, tx, options.Remap); err != nil {
		return err
	}
	now := p.CreatedAt
	if now.IsZero() || p.UpdatedAt.IsZero() {
		return invalid("project", "Imported project timestamps are required")
	}
	p.Version = 1
	p.Capabilities = Features()
	p.CreatedAt = now

	_, err = tx.ExecContext(ctx, `INSERT INTO backend_projects(id,name,version,current_revision_id,created_at,updated_at) VALUES(?,?,1,?,?,?)`, p.ID, p.Name, p.CurrentRevisionID, now.Format(time.RFC3339Nano), p.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

// insertPortableRepositoriesTx inserts the project's repositories and returns
// the set later source snapshots must reference.
func insertPortableRepositoriesTx(ctx context.Context, tx *sql.Tx, p *Project) (map[string]bool, error) {
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
	return repositories, nil
}

// importPortableSourceTx rehashes and inserts one source revision. Sources
// arrive dependency-first, so sources holds every revision a later one may
// depend on; hashes maps each origin content hash to its local semantic hash.
func (r *Repo) importPortableSourceTx(ctx context.Context, tx *sql.Tx, projectID string, source *PortableSource, options PortableImportOptions, repositories, sources map[string]bool, hashes map[string]string) error {
	if source.Revision.ProjectID != projectID || sources[source.Revision.ID] {
		return invalid("revision", "Duplicate or foreign source revision")
	}
	if err := checkPortableSourceClosure(*source, repositories, sources); err != nil {
		return err
	}
	origin := portableAttribution(options, "revision", source.Revision.ID, 1, source.Revision.SemanticHash)
	if err := rehashPortableSource(ctx, tx, source, hashes); err != nil {
		return err
	}
	source.Revision.ImportOrigin = &origin
	if err := r.resolvePortableContextTx(ctx, tx, source); err != nil {
		return err
	}
	// Binding object hashes may change under an explicit mapped owner. Recompute
	// the final v3 semantic identity after exact selector resolution.
	c, err := DecodeVersionedArtifactContext(source.ArtifactContext, nil)
	if err != nil {
		return err
	}
	source.Revision.SemanticHash, err = ArtifactContextV3SemanticHash(*c.V3)
	if err != nil {
		return err
	}
	hashes[origin.ContentHash] = source.Revision.SemanticHash
	if err := insertPortableSourceTx(ctx, tx, source); err != nil {
		return err
	}
	sources[source.Revision.ID] = true
	return nil
}

// checkPortableSourceClosure requires every dependency to be imported already
// and every snapshot to name an imported repository.
func checkPortableSourceClosure(source PortableSource, repositories, sources map[string]bool) error {
	dependencies, err := PortableRevisionDependencies(source)
	if err != nil {
		return err
	}
	for _, id := range dependencies {
		if !sources[id] {
			return invalid("closure", "Source dependencies must be complete and dependency-first")
		}
	}
	for _, s := range source.Coverage.Snapshots {
		if !repositories[s.RepositoryID] {
			return invalid("repository", "Source snapshot references missing repository")
		}
	}
	return nil
}

// importPortableDiagramsTx imports every diagram version and returns the map
// from each exported pin to its local one, which views are rebound through.
func (r *Repo) importPortableDiagramsTx(ctx context.Context, tx *sql.Tx, model *PortableModel, options PortableImportOptions) (map[DiagramPin]DiagramPin, error) {
	diagramPins := map[DiagramPin]DiagramPin{}
	for i := range model.Diagrams {
		v := &model.Diagrams[i]
		old := v.Pin
		if v.ProjectID != model.Project.ID {
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
	return diagramPins, nil
}

// importPortableViewsTx imports diagram views, rebound to the local diagram
// pins, then saved views; both newest version first within an ID.
func (r *Repo) importPortableViewsTx(ctx context.Context, tx *sql.Tx, model *PortableModel, diagramPins map[DiagramPin]DiagramPin) error {
	slices.SortFunc(model.DiagramViews, func(a, b DiagramView) int { return cmp.Or(cmp.Compare(a.ID, b.ID), cmp.Compare(b.Version, a.Version)) })
	for i := range model.DiagramViews {
		v := &model.DiagramViews[i]
		pin, ok := diagramPins[v.State.Diagram]
		if !ok {
			return invalid("view", "Missing exact diagram dependency")
		}
		v.State.Diagram = pin
		if err := r.ImportPortableDiagramViewTx(ctx, tx, model.Project.ID, v); err != nil {
			return err
		}
	}
	slices.SortFunc(model.SavedViews, func(a, b SavedView) int { return cmp.Or(cmp.Compare(a.ID, b.ID), cmp.Compare(b.Version, a.Version)) })
	for i := range model.SavedViews {
		if err := r.importPortableSavedViewTx(ctx, tx, model.Project.ID, &model.SavedViews[i]); err != nil {
			return err
		}
	}
	return nil
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

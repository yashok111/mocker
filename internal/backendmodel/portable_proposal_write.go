package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"time"

	"github.com/yashok111/mocker/internal/backendblob"
)

func portableOriginHashes(o *EffectiveOrigin, hashes map[string]string) {
	if o.BaseRef != nil {
		portableReplaceHash(&o.BaseRef.SemanticHash, hashes)
	}
}
func (r *Repo) importPortableProposalTx(ctx context.Context, tx *sql.Tx, pid string, p *PortableProposal, hashes map[string]string, options PortableImportOptions) error {
	if (p.Full == nil) == (p.Legacy == nil) {
		return invalid("proposal", "Exactly one typed proposal owner required")
	}
	if p.Legacy != nil {
		return r.importPortableLegacyProposalTx(ctx, tx, pid, p, hashes, options)
	}
	head := p.Full
	if head.ProjectID != pid || len(p.FullRevisions) == 0 || len(p.FullRevisions) > MaxChangeProposalRevisions || len(p.LegacyRevisions) != 0 {
		return invalid("proposal", "Invalid full proposal closure")
	}
	head.Status = "draft"
	head.ReadyReference = nil
	head.ImplementedReference = nil
	head.Version = int64(len(p.FullRevisions))
	if _, err := tx.ExecContext(ctx, `INSERT INTO backend_change_proposals(id,project_id,version,name,status,current_draft_revision_id,current_draft_hash,created_at,updated_at,ready_reference) VALUES(?,?,?,?,'draft',?,?,?,?,NULL)`, head.ID, pid, head.Version, head.Name, head.CurrentDraftRevisionID, head.CurrentDraftHash, head.CreatedAt.Format(time.RFC3339Nano), head.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	imported := map[string]bool{}
	for i := range p.FullRevisions {
		if err := r.importPortableFullRevisionTx(ctx, tx, pid, p, i, imported, hashes, options); err != nil {
			return err
		}
	}
	if !imported[head.CurrentDraftRevisionID] {
		return invalid("proposal", "Selected proposal revision missing")
	}
	for _, v := range p.FullRevisions {
		if v.ID == head.CurrentDraftRevisionID {
			head.CurrentDraftHash = v.SemanticHash
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE backend_change_proposals SET current_draft_hash=? WHERE id=?`, head.CurrentDraftHash, head.ID)
	if err != nil {
		return err
	}
	return checkChangeProposalQuota(ctx, tx, pid, head.ID)
}

// importPortableFullRevisionTx rebases revision i onto the local source,
// re-evaluates it to its local semantic hash and persists it with its batch.
// imported holds every earlier revision of the chain.
func (r *Repo) importPortableFullRevisionTx(ctx context.Context, tx *sql.Tx, pid string, p *PortableProposal, i int, imported map[string]bool, hashes map[string]string, options PortableImportOptions) error {
	head := p.Full
	v := &p.FullRevisions[i]
	if v.ProposalID != head.ID || imported[v.ID] || v.ParentRevisionID != nil && !imported[*v.ParentRevisionID] {
		return invalid("proposal", "Proposal history is not a closed acyclic chain")
	}
	source, err := loadComposedBase(ctx, tx, pid, v.BaseRevisionID)
	if err != nil {
		return err
	}
	originalHash := v.SemanticHash
	v.BaseSemanticHash = source.State.Revision.SemanticHash
	v.SourceSnapshotIDs = source.State.Revision.SourceSnapshotIDs
	v.SourceVector = *source.SourceVector
	rehashPortableChangeDelta(&v.Delta, hashes)
	if v.ArtifactContextV3 == nil {
		return invalid("context", "Imported full proposal requires context-v3")
	}
	if c := source.State.ArtifactContextV3; c != nil {
		v.ArtifactContextV3.SourceContentHash, v.ArtifactContextV3.SourceSemanticHash = c.SourceContentHash, c.SourceSemanticHash
	}
	v.SemanticHash, err = r.reevaluatePortableRevisionTx(ctx, tx, pid, source, v)
	if err != nil {
		return err
	}
	hashes[originalHash] = v.SemanticHash
	attribution := portableAttribution(options, "change_proposal_revision", v.ID, int64(i+1), originalHash)
	v.ImportOrigin = &attribution
	batch, err := portableRevisionBatch(p, v.ID, imported)
	if err != nil {
		return err
	}
	identities := portableRevisionIdentities(p, v.ID, hashes)
	batch.CommandsHash, err = requestDigest(batch.Commands)
	if err != nil {
		return err
	}
	eventHead := *head
	eventHead.Version = int64(i + 1)
	if err := persistChangeRevision(ctx, tx, eventHead, *v, batch.Action, batch.RestoreRevisionID, batch.Commands, identities); err != nil {
		return err
	}
	imported[v.ID] = true
	return nil
}

// rehashPortableChangeDelta rewrites every origin and basis hash a delta
// cites from its exported value to the locally recomputed one.
func rehashPortableChangeDelta(d *ChangeDelta, hashes map[string]string) {
	for j := range d.Properties {
		portableOriginHashes(&d.Properties[j].Origin, hashes)
	}
	for j := range d.Created {
		portableOriginHashes(&d.Created[j].Origin, hashes)
	}
	for j := range d.Removed {
		portableOriginHashes(&d.Removed[j].Origin, hashes)
	}
	for j := range d.EdgeNames {
		portableOriginHashes(&d.EdgeNames[j].Origin, hashes)
	}
	for j := range d.IdentityIntents {
		value := &d.IdentityIntents[j]
		portableOriginHashes(&value.Origin, hashes)
		if value.Target.Source != nil {
			portableReplaceHash(&value.Target.Source.AssertionHash, hashes)
		}
		if value.Target.Basis != nil {
			portableReplaceHash(&value.Target.Basis.SemanticHash, hashes)
		}
	}
	for j := range d.CarriedIdentities {
		portableReplaceHash(&d.CarriedIdentities[j].Source.AssertionHash, hashes)
		portableReplaceHash(&d.CarriedIdentities[j].Basis.SemanticHash, hashes)
	}
}

// reevaluatePortableRevisionTx resolves the revision's artifact context here
// and validates it against the local source; an imported revision is trusted
// only after the same evaluation a local save runs. It returns the revision's
// local semantic hash.
func (r *Repo) reevaluatePortableRevisionTx(ctx context.Context, tx *sql.Tx, pid string, source *SourceGraphSnapshot, v *ChangeProposalRevision) (string, error) {
	installation, err := installationID(ctx, tx)
	if err != nil {
		return "", err
	}
	request := r.portableArtifactRequest(ctx, tx)
	if err := resolvePortableContext(v.ArtifactContextV3, installation, request); err != nil {
		return "", err
	}
	e, err := newChangeEvaluation(source, *v, map[string]ChangeObjectIdentity{})
	if err != nil {
		return "", err
	}
	e.artifactRequest = request
	evaluated, diagnostics, err := e.validate(ctx)
	if err != nil {
		return "", err
	}
	if len(diagnostics) > 0 {
		return "", changeInvalid(diagnostics)
	}
	if err := r.validateChangeCriteriaReferences(ctx, tx, pid, e); err != nil {
		return "", err
	}
	return changeSemanticHash(evaluated)
}

// portableRevisionBatch finds the one immutable batch that produced revision
// id; a restore batch may only name an already imported revision.
func portableRevisionBatch(p *PortableProposal, id string, imported map[string]bool) (*ChangeAppliedBatch, error) {
	var batch *ChangeAppliedBatch
	for j := range p.Batches {
		if p.Batches[j].RevisionID == id {
			if batch != nil {
				return nil, invalid("batch", "Duplicate batch")
			}
			batch = &p.Batches[j]
		}
	}
	if batch == nil || batch.ProposalID != p.Full.ID {
		return nil, invalid("batch", "Missing exact immutable proposal batch")
	}
	if batch.Action == "restore" && !imported[batch.RestoreRevisionID] {
		return nil, invalid("batch", "Missing restore dependency")
	}
	return batch, nil
}

// portableRevisionIdentities returns the identities revision id introduced,
// with their origin hashes rewritten to local ones.
func portableRevisionIdentities(p *PortableProposal, id string, hashes map[string]string) []ChangeObjectIdentity {
	identities := []ChangeObjectIdentity{}
	for j := range p.Identities {
		identity := &p.Identities[j]
		if identity.FirstRevisionID == id {
			portableOriginHashes(&identity.Origin, hashes)
			identities = append(identities, *identity)
		}
	}
	return identities
}

func (r *Repo) importPortableLegacyProposalTx(ctx context.Context, tx *sql.Tx, pid string, p *PortableProposal, hashes map[string]string, options PortableImportOptions) error {
	h := p.Legacy
	if h.ProjectID != pid || len(p.LegacyRevisions) == 0 || len(p.LegacyRevisions) > 1000 || len(p.FullRevisions) != 0 {
		return invalid("proposal", "Invalid legacy proposal closure")
	}
	source, err := loadRevisionState(ctx, tx, pid, h.BaseRevisionID)
	if err != nil {
		return err
	}
	h.BaseSemanticHash = source.Revision.SemanticHash
	h.Version = int64(len(p.LegacyRevisions))
	h.Status = "draft"
	if _, err := tx.ExecContext(ctx, `INSERT INTO backend_proposals(id,project_id,version,name,status,base_revision_id,base_semantic_hash,repository_id,datastore_id,facet_key,draft_revision_id,draft_hash,created_at,updated_at) VALUES(?,?,?,?,'draft',?,?,?,?,?,?,?,?,?)`, h.ID, pid, h.Version, h.Name, h.BaseRevisionID, h.BaseSemanticHash, h.RepositoryID, h.DatastoreID, h.FacetKey, h.DraftRevisionID, h.DraftHash, h.CreatedAt.Format(time.RFC3339Nano), h.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	imported := map[string]bool{}
	for i := range p.LegacyRevisions {
		if err := importPortableLegacyRevisionTx(ctx, tx, pid, h, source, &p.LegacyRevisions[i], i, imported, hashes, options); err != nil {
			return err
		}
	}
	if !imported[h.DraftRevisionID] {
		return invalid("proposal", "Selected legacy revision missing")
	}
	for _, v := range p.LegacyRevisions {
		if v.ID == h.DraftRevisionID {
			h.DraftHash = v.SemanticHash
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE backend_proposals SET draft_hash=? WHERE id=?`, h.DraftHash, h.ID)
	if err != nil {
		return err
	}
	return r.checkProposalStaging(ctx, tx, pid, 0)
}

// importPortableLegacyRevisionTx validates and persists legacy revision i
// against the proposal's one source revision, then checks the effective
// graph it produces. imported holds every earlier revision of the chain.
func importPortableLegacyRevisionTx(ctx context.Context, tx *sql.Tx, pid string, h *Proposal, source *RevisionState, v *ProposalRevision, i int, imported map[string]bool, hashes map[string]string, options PortableImportOptions) error {
	if v.ProposalID != h.ID || v.BaseRevisionID != h.BaseRevisionID || imported[v.ID] || v.ParentRevisionID != nil && !imported[*v.ParentRevisionID] {
		return invalid("proposal", "Invalid legacy history")
	}
	originalHash := v.SemanticHash
	v.BaseSemanticHash = h.BaseSemanticHash
	v.SourceSnapshotIDs = source.Revision.SourceSnapshotIDs
	// Legacy proposal effective reads inherit the source's separate v3 context.
	v.ArtifactPins = []ArtifactPin{}
	rehashPortableLegacyOverlays(v.Overlays, hashes)
	base := &graphCandidate{Nodes: source.Nodes, Edges: source.Edges, Evidence: source.Evidence}
	checked, err := validateProposalSnapshot(base, *h, *v)
	if err != nil {
		return err
	}
	if len(checked.Diagnostics) > 0 {
		return invalid("proposal", "Legacy owner validation failed")
	}
	v.SemanticHash, err = proposalEffectiveGraphHash(*h, v.Overlays)
	if err != nil {
		return err
	}
	hashes[originalHash] = v.SemanticHash
	origin := portableAttribution(options, "proposal_revision", v.ID, int64(i+1), originalHash)
	v.ImportOrigin = &origin
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := backendblob.Exec(ctx, tx, `INSERT INTO backend_proposal_revisions(id,proposal_id,parent_revision_id,document) VALUES(?,?,?,?)`, v.ID, h.ID, v.ParentRevisionID, string(raw)); err != nil {
		return err
	}
	effective, err := resolveEffectiveGraph(ctx, tx, pid, BackendReadTarget{Proposal: &ProposalReadTarget{ProposalID: h.ID, ProposalRevisionID: v.ID}})
	if err != nil {
		return err
	}
	diagnostics, err := validatePortableLegacyStructure(ctx, source, *h, *v, effective.State)
	if err != nil {
		return err
	}
	if len(diagnostics) > 0 {
		return changeInvalid(diagnostics)
	}
	imported[v.ID] = true
	return nil
}

// rehashPortableLegacyOverlays rewrites the base hashes legacy overlays and
// their property origins cite to the locally recomputed ones.
func rehashPortableLegacyOverlays(overlays []ProposalOverlay, hashes map[string]string) {
	for j := range overlays {
		o := &overlays[j]
		if o.Base != nil {
			portableReplaceHash(&o.Base.SemanticHash, hashes)
		}
		for key, origin := range o.PropertyOrigins {
			if origin.Base != nil {
				portableReplaceHash(&origin.Base.SemanticHash, hashes)
			}
			o.PropertyOrigins[key] = origin
		}
	}
}

// Legacy overlays deliberately omit source metadata. Adapt only a detached
// validation projection: use the pinned dialect and explicit unknown analysis,
// never persist fabricated source proof or change the legacy overlay bytes.
func validatePortableLegacyStructure(ctx context.Context, base *RevisionState, p Proposal, v ProposalRevision, state RevisionState) ([]ImportDiagnostic, error) {
	var dialect jsontext.Value
	for _, n := range base.Nodes {
		if n.ID == p.DatastoreID {
			facets, _, err := relationalFacetObject(n.Kind, n.Attributes)
			if err != nil {
				return nil, err
			}
			fields, err := relationalObject(facets[p.FacetKey])
			if err != nil {
				return nil, err
			}
			dialect = fields["dialect"]
		}
	}
	if len(dialect) == 0 {
		return nil, invalid("facet", "Pinned legacy dialect is missing")
	}
	detached, err := detachedEffectiveState(state)
	if err != nil {
		return nil, err
	}
	enrich := func(kind string, attrs map[string]jsontext.Value, facetKey string) (map[string]jsontext.Value, error) {
		facets, _, err := relationalFacetObject(kind, attrs)
		if err != nil {
			return nil, err
		}
		fields, err := relationalObject(facets[facetKey])
		if err != nil {
			return nil, err
		}
		fields["dialect"] = dialect
		fields["analysisStatus"] = jsontext.Value(`"partial"`)
		fields["gaps"] = jsontext.Value(`["Authored proposal intent; source analysis unavailable"]`)
		facets[facetKey], err = json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		return replaceRelationalFacets(kind, attrs, facets), nil
	}
	for _, o := range v.Overlays {
		if o.RecordType == "node" {
			for i := range detached.Nodes {
				n := &detached.Nodes[i]
				if n.ID == o.SubjectID {
					n.Attributes, err = enrich(n.Kind, n.Attributes, o.FacetKey)
					if err != nil {
						return nil, err
					}
				}
			}
		} else {
			for i := range detached.Edges {
				e := &detached.Edges[i]
				if e.ID == o.SubjectID {
					e.Attributes, err = enrich(e.Kind, e.Attributes, o.FacetKey)
					if err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return ValidateSourceStructure(ctx, SourceStructuralGraph{SchemaVersion: ComposedSchemaVersion, Nodes: detached.Nodes, Edges: detached.Edges})
}

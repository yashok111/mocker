package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"time"
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
		for j := range v.Delta.Properties {
			portableOriginHashes(&v.Delta.Properties[j].Origin, hashes)
		}
		for j := range v.Delta.Created {
			portableOriginHashes(&v.Delta.Created[j].Origin, hashes)
		}
		for j := range v.Delta.Removed {
			portableOriginHashes(&v.Delta.Removed[j].Origin, hashes)
		}
		for j := range v.Delta.EdgeNames {
			portableOriginHashes(&v.Delta.EdgeNames[j].Origin, hashes)
		}
		for j := range v.Delta.IdentityIntents {
			value := &v.Delta.IdentityIntents[j]
			portableOriginHashes(&value.Origin, hashes)
			if value.Target.Source != nil {
				portableReplaceHash(&value.Target.Source.AssertionHash, hashes)
			}
			if value.Target.Basis != nil {
				portableReplaceHash(&value.Target.Basis.SemanticHash, hashes)
			}
		}
		for j := range v.Delta.CarriedIdentities {
			portableReplaceHash(&v.Delta.CarriedIdentities[j].Source.AssertionHash, hashes)
			portableReplaceHash(&v.Delta.CarriedIdentities[j].Basis.SemanticHash, hashes)
		}
		if v.ArtifactContextV3 == nil {
			return invalid("context", "Imported full proposal requires context-v3")
		}
		if c := source.State.ArtifactContextV3; c != nil {
			v.ArtifactContextV3.SourceContentHash, v.ArtifactContextV3.SourceSemanticHash = c.SourceContentHash, c.SourceSemanticHash
		}
		installation, err := installationID(ctx, tx)
		if err != nil {
			return err
		}
		request := r.portableArtifactRequest(ctx, tx)
		if err := resolvePortableContext(v.ArtifactContextV3, installation, request); err != nil {
			return err
		}
		e, err := newChangeEvaluation(source, *v, map[string]ChangeObjectIdentity{})
		if err != nil {
			return err
		}
		e.artifactRequest = request
		evaluated, diagnostics, err := e.validate(ctx)
		if err != nil {
			return err
		}
		if len(diagnostics) > 0 {
			return changeInvalid(diagnostics)
		}
		if err := r.validateChangeCriteriaReferences(ctx, tx, pid, e); err != nil {
			return err
		}
		v.SemanticHash, err = changeSemanticHash(evaluated)
		if err != nil {
			return err
		}
		hashes[originalHash] = v.SemanticHash
		attribution := portableAttribution(options, "change_proposal_revision", v.ID, int64(i+1), originalHash)
		v.ImportOrigin = &attribution
		var batch *ChangeAppliedBatch
		for j := range p.Batches {
			if p.Batches[j].RevisionID == v.ID {
				if batch != nil {
					return invalid("batch", "Duplicate batch")
				}
				batch = &p.Batches[j]
			}
		}
		if batch == nil || batch.ProposalID != head.ID {
			return invalid("batch", "Missing exact immutable proposal batch")
		}
		if batch.Action == "restore" && !imported[batch.RestoreRevisionID] {
			return invalid("batch", "Missing restore dependency")
		}
		identities := []ChangeObjectIdentity{}
		for j := range p.Identities {
			identity := &p.Identities[j]
			if identity.FirstRevisionID == v.ID {
				portableOriginHashes(&identity.Origin, hashes)
				identities = append(identities, *identity)
			}
		}
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
		v := &p.LegacyRevisions[i]
		if v.ProposalID != h.ID || v.BaseRevisionID != h.BaseRevisionID || imported[v.ID] || v.ParentRevisionID != nil && !imported[*v.ParentRevisionID] {
			return invalid("proposal", "Invalid legacy history")
		}
		originalHash := v.SemanticHash
		v.BaseSemanticHash = h.BaseSemanticHash
		v.SourceSnapshotIDs = source.Revision.SourceSnapshotIDs
		// Legacy proposal effective reads inherit the source's separate v3 context.
		v.ArtifactPins = []ArtifactPin{}
		for j := range v.Overlays {
			o := &v.Overlays[j]
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
		if _, err := tx.ExecContext(ctx, `INSERT INTO backend_proposal_revisions(id,proposal_id,parent_revision_id,document) VALUES(?,?,?,?)`, v.ID, h.ID, v.ParentRevisionID, string(raw)); err != nil {
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
	copy, err := detachedEffectiveState(state)
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
			for i := range copy.Nodes {
				n := &copy.Nodes[i]
				if n.ID == o.SubjectID {
					n.Attributes, err = enrich(n.Kind, n.Attributes, o.FacetKey)
					if err != nil {
						return nil, err
					}
				}
			}
		} else {
			for i := range copy.Edges {
				e := &copy.Edges[i]
				if e.ID == o.SubjectID {
					e.Attributes, err = enrich(e.Kind, e.Attributes, o.FacetKey)
					if err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return ValidateSourceStructure(ctx, SourceStructuralGraph{SchemaVersion: ComposedSchemaVersion, Nodes: copy.Nodes, Edges: copy.Edges})
}

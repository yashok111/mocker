package backendmodel

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"slices"
	"strings"
)

type ProposalReadTarget struct {
	ProposalID         string `json:"proposalId"`
	ProposalRevisionID string `json:"proposalRevisionId"`
}

func (p *ProposalReadTarget) UnmarshalJSON(b []byte) error {
	m, err := relationalObject(b)
	if err != nil {
		return invalid("proposal", "Expected an exact proposal target")
	}
	if err := relationalFields(m, []string{"proposalId", "proposalRevisionId"}, nil); err != nil {
		return invalid("proposal", err.Error())
	}
	for key, raw := range m {
		var id string
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &id) != nil || !ValidID(id) {
			return invalid(key, "Expected a canonical UUID")
		}
	}
	type target ProposalReadTarget
	return json.Unmarshal(b, (*target)(p))
}

type BackendReadTarget struct {
	RevisionID      string                     `json:"revisionId,omitempty"`
	Proposal        *ProposalReadTarget        `json:"proposal,omitzero"`
	ChangeProposal  *ProposalReadTarget        `json:"changeProposal,omitzero"`
	ImportCandidate *ImportCandidateReadTarget `json:"importCandidate,omitzero"`
}

type ProposalReadPins struct {
	ProposalID           string `json:"proposalId"`
	ProposalRevisionID   string `json:"proposalRevisionId"`
	ProposalSemanticHash string `json:"proposalSemanticHash"`
	BaseRevisionID       string `json:"baseRevisionId"`
	BaseSemanticHash     string `json:"baseSemanticHash"`
	RepositoryID         string `json:"repositoryId"`
	DatastoreID          string `json:"datastoreId"`
	FacetKey             string `json:"facetKey"`
	EffectiveGraphHash   string `json:"effectiveGraphHash"`
}

type ProposalGraphProjection struct {
	Nodes []ProposalProjectedNode `json:"nodes"`
	Edges []ProposalProjectedEdge `json:"edges"`
}

type BackendNodeRead struct {
	Target             *BackendReadTarget       `json:"target,omitzero"`
	Pins               *EffectiveGraphPins      `json:"pins,omitzero"`
	Node               *Node                    `json:"node,omitzero"`
	Origins            []EffectiveFieldOrigin   `json:"origins,omitempty"`
	BaselineEvidence   []EffectiveEvidenceBasis `json:"baselineEvidence,omitempty"`
	SourceRecord       *Node                    `json:"-"`
	ViewSchemaVersion  string                   `json:"viewSchemaVersion"`
	ProposalPins       *ProposalReadPins        `json:"proposalPins"`
	ProposalProjection *ProposalProjectedNode   `json:"proposalProjection"`
}

func (n BackendNodeRead) MarshalJSON() ([]byte, error) {
	if n.ProposalProjection == nil && n.Pins == nil {
		return json.Marshal(n.SourceRecord)
	}
	type read BackendNodeRead
	return json.Marshal(read(n))
}

type resolvedBackendTarget struct {
	revisionID string
	proposal   *Proposal
	draft      *ProposalRevision
	pins       *ProposalReadPins
	overlays   map[string]ProposalOverlay
}

func (r *Repo) resolveBackendTarget(ctx context.Context, pid string, in BackendReadTarget) (*resolvedBackendTarget, error) {
	return resolveBackendTarget(ctx, r.db.R, pid, in)
}

func resolveBackendTarget(ctx context.Context, q importReader, pid string, in BackendReadTarget) (*resolvedBackendTarget, error) {
	if in.ChangeProposal != nil || in.ImportCandidate != nil {
		return nil, invalid("target", "This legacy reader requires a source revision or legacy proposal")
	}
	if (in.RevisionID == "") == (in.Proposal == nil) {
		return nil, invalid("target", "Select exactly one source revision or pinned proposal")
	}
	if in.Proposal == nil {
		if !ValidID(in.RevisionID) {
			return nil, notFound()
		}
		return &resolvedBackendTarget{revisionID: in.RevisionID}, nil
	}
	if !ValidID(in.Proposal.ProposalID) || !ValidID(in.Proposal.ProposalRevisionID) {
		return nil, notFound()
	}
	p, err := loadProposal(ctx, q, pid, in.Proposal.ProposalID)
	if err != nil {
		return nil, err
	}
	draft, err := loadProposalRevision(ctx, q, p.ID, in.Proposal.ProposalRevisionID)
	if err != nil {
		return nil, err
	}
	if draft.DocumentVersion != ProposalDocumentVersion {
		return nil, &FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Unsupported proposal view version"}
	}
	if draft.BaseRevisionID != p.BaseRevisionID || draft.BaseSemanticHash != p.BaseSemanticHash {
		return nil, notFound()
	}
	hash, err := proposalEffectiveGraphHash(*p, draft.Overlays)
	if err != nil {
		return nil, err
	}
	result := &resolvedBackendTarget{revisionID: p.BaseRevisionID, proposal: p, draft: draft, pins: &ProposalReadPins{ProposalID: p.ID, ProposalRevisionID: draft.ID, ProposalSemanticHash: draft.SemanticHash, BaseRevisionID: p.BaseRevisionID, BaseSemanticHash: p.BaseSemanticHash, RepositoryID: p.RepositoryID, DatastoreID: p.DatastoreID, FacetKey: p.FacetKey, EffectiveGraphHash: hash}, overlays: map[string]ProposalOverlay{}}
	for _, overlay := range draft.Overlays {
		result.overlays[overlay.SubjectID] = overlay
	}
	return result, nil
}

func (target *resolvedBackendTarget) overlay(id string) *ProposalOverlay {
	if o, ok := target.overlays[id]; ok {
		return &o
	}
	return nil
}

func (r *Repo) ReadNode(ctx context.Context, pid string, in BackendReadTarget, nid string) (*BackendNodeRead, error) {
	if in.ChangeProposal != nil || in.ImportCandidate != nil {
		return r.readEffectiveNode(ctx, pid, in, nid)
	}
	if in.Proposal == nil && in.RevisionID != "" {
		revision, err := r.Revision(ctx, pid, in.RevisionID)
		if err != nil {
			return nil, err
		}
		if revision.SchemaVersion == ComposedSchemaVersion {
			return r.readEffectiveNode(ctx, pid, in, nid)
		}
	}

	target, err := r.resolveBackendTarget(ctx, pid, in)
	if err != nil {
		return nil, err
	}
	if !ValidID(nid) {
		return nil, notFound()
	}
	var source *Node
	if o := target.overlay(nid); o == nil || o.Base != nil {
		source, err = r.Node(ctx, pid, target.revisionID, nid)
		if err != nil {
			return nil, err
		}
	}
	out := &BackendNodeRead{SourceRecord: source}
	if target.proposal != nil {
		out.ViewSchemaVersion, out.ProposalPins = ProposalDocumentVersion, target.pins
		out.ProposalProjection, err = projectProposalNode(*target.proposal, new(target.draft.ID), source, target.overlay(nid))
	}
	return out, err
}

func (r *Repo) ReadEvidence(ctx context.Context, pid string, in BackendReadTarget, query EvidenceQueryInput) (*EvidencePage, error) {
	if in.ChangeProposal != nil || in.ImportCandidate != nil {
		return r.readEffectiveEvidence(ctx, pid, in, query)
	}
	if in.Proposal == nil && in.RevisionID != "" {
		revision, err := r.Revision(ctx, pid, in.RevisionID)
		if err != nil {
			return nil, err
		}
		if revision.SchemaVersion == ComposedSchemaVersion {
			return r.readEffectiveEvidence(ctx, pid, in, query)
		}
	}

	target, err := r.resolveBackendTarget(ctx, pid, in)
	if err != nil {
		return nil, err
	}
	if o := target.overlay(query.SubjectID); o != nil && o.Base == nil {
		return nil, notFound()
	}
	scope := target.revisionID + ":" + query.SubjectID + ":" + query.EvidenceID
	if target.pins != nil {
		scope, err = requestDigest(struct {
			Pins                  *ProposalReadPins
			SubjectID, EvidenceID string
		}{target.pins, query.SubjectID, query.EvidenceID})
		if err != nil {
			return nil, err
		}
	}
	out, err := r.evidence(ctx, pid, target.revisionID, query, scope)
	if err != nil {
		return nil, err
	}
	if target.pins != nil {
		out.ViewSchemaVersion, out.ProposalPins = ProposalDocumentVersion, target.pins
	}
	return out, nil
}

func (r *Repo) ReadCoverage(ctx context.Context, pid string, in BackendReadTarget) (*RevisionCoverage, error) {
	if in.ChangeProposal != nil || in.ImportCandidate != nil {
		return r.readEffectiveCoverage(ctx, pid, in)
	}
	if in.Proposal == nil && in.RevisionID != "" {
		revision, err := r.Revision(ctx, pid, in.RevisionID)
		if err != nil {
			return nil, err
		}
		if revision.SchemaVersion == ComposedSchemaVersion {
			return r.readEffectiveCoverage(ctx, pid, in)
		}
	}

	target, err := r.resolveBackendTarget(ctx, pid, in)
	if err != nil {
		return nil, err
	}
	out, err := r.RevisionCoverage(ctx, pid, target.revisionID)
	if err != nil {
		return nil, err
	}
	if target.pins != nil {
		out.ViewSchemaVersion, out.ProposalPins = ProposalDocumentVersion, target.pins
	}
	return out, nil
}

// decodeReadQuery preserves presence before Go zero values erase a mixed target.
func decodeReadQuery(b []byte, required, optional []string, out any) error {
	m, err := relationalObject(b)
	if err != nil {
		return invalid("body", "Expected a strict query object")
	}
	if err := relationalFields(m, required, append(optional, "revisionId", "proposal", "changeProposal", "importCandidate")); err != nil {
		return invalid("body", err.Error())
	}
	count := 0
	for _, key := range []string{"revisionId", "proposal", "changeProposal", "importCandidate"} {
		if _, present := m[key]; present {
			count++
		}
	}
	if count != 1 {
		return invalid("target", "Select exactly one pinned graph target")
	}
	for key, raw := range m {
		raw = bytes.TrimSpace(raw)
		switch key {
		case "proposal", "changeProposal":
			var target ProposalReadTarget
			if err := json.Unmarshal(raw, &target); err != nil {
				return err
			}
		case "importCandidate":
			var target ImportCandidateReadTarget
			if err := json.Unmarshal(raw, &target); err != nil {
				return err
			}
		case "limit":
			var limit int
			if string(raw) == "null" || json.Unmarshal(raw, &limit) != nil || limit < 1 || limit > MaxGraphPageSize {
				return invalid(key, "limit must be between 1 and 500")
			}
		default:
			if len(raw) == 0 || raw[0] != '"' {
				return invalid(key, "Expected a string")
			}
		}
	}
	return json.Unmarshal(b, out, json.RejectUnknownMembers(true))
}

func (in *GraphQueryInput) UnmarshalJSON(b []byte) error {
	type query GraphQueryInput
	return decodeReadQuery(b, []string{"recordType"}, []string{"id", "kind", "search", "parentId", "from", "to", "limit", "cursor", "serviceId", "sourceSnapshotId", "certainty"}, (*query)(in))
}

// The shared SQL page engine sees virtual designed records and effective edge
// endpoints. Its documents remain the original source bytes; overlays are
// projected separately after selection. One JSON parameter avoids bind limits.
func (target *resolvedBackendTarget) graphRecords(pid, typ string) (string, []any, error) {
	if target.proposal == nil {
		return "backend_graph_records_documents", []any{pid, target.revisionID, typ}, nil
	}
	b, err := json.Marshal(target.draft.Overlays)
	if err != nil {
		return "", nil, err
	}
	cte := `WITH overlays AS (SELECT json_extract(value,'$.subjectId') AS id,json_extract(value,'$.recordType') AS record_type,json_extract(value,'$.kind') AS kind,json_extract(value,'$.name') AS name,json_extract(value,'$.parentId') AS parent_id,json_extract(value,'$.fromId') AS from_id,json_extract(value,'$.toId') AS to_id FROM json_each(?)), records AS (
	SELECT b.document,b.id,b.kind,b.name,b.parent_id,coalesce(o.from_id,b.from_id) AS from_id,coalesce(o.to_id,b.to_id) AS to_id,b.project_id,b.revision_id,b.record_type FROM backend_graph_records_documents b LEFT JOIN overlays o ON o.id=b.id AND o.record_type=b.record_type WHERE b.project_id=? AND b.revision_id=? AND b.record_type=?
	UNION ALL SELECT '',o.id,o.kind,o.name,o.parent_id,o.from_id,o.to_id,?,?,o.record_type FROM overlays o WHERE o.record_type=? AND NOT EXISTS (SELECT 1 FROM backend_graph_records_documents b WHERE b.project_id=? AND b.revision_id=? AND b.record_type=o.record_type AND b.id=o.id)) `
	return cte, []any{string(b), pid, target.revisionID, typ, pid, target.revisionID, typ, pid, target.revisionID}, nil
}

func (p *databaseProjection) propertyCurrent(id, property string) bool {
	if p.effective != nil {
		node := p.nodes[id]
		selector := sourcePropertyForPointer(sourceNodePayload(node), "/attributes/facets/"+escapeRelationalPointer(p.in.FacetKey)+property)
		if selector == nil {
			return false
		}
		proof, err := effectivePropertyProof(p.effective, "node", id, *selector)
		return err == nil && !proof.boundary && (proof.status == "explicit" || proof.status == "desired")
	}
	if p.proposal != nil {
		if o := p.proposal.overlay(id); o != nil && o.PropertyOrigins[property].Kind == "intent" {
			return true
		}
	}
	return p.proofCurrent(p.selected(id))
}

func (p *databaseProjection) projectRelationship(item RelationshipItem, e Edge) RelationshipItem {
	if p.effective != nil {
		if p.effectiveRelationshipIntent(e) {
			item.Status, item.RuntimeStatus = "desired", "unverified"
			if p.sourceProof[p.selected(e.ID)].status == "desired" || p.sourceProof[p.selected(e.From)].status == "desired" {
				item.EvidenceIDs = []string{}
			}
		}
		return item
	}

	if p.proposal == nil || p.proposal.proposal == nil {
		return item
	}
	target := p.proposal
	node := p.nodes[e.From]
	f, err := effectiveProposalFacet(*target.proposal, new(target.draft.ID), e.ID, e.Kind, e.Attributes, target.overlay(e.ID))
	if err != nil { // Persisted overlays were validated before publication.
		p.limitation(e.ID, "proposal_projection_unavailable", "Proposal relationship projection unavailable for "+e.ID)
	}
	item.EffectiveFacet, item.Status, item.RuntimeStatus = f, "proposed", "unverified"
	if target.overlay(e.ID) != nil {
		item.EvidenceIDs = []string{}
	}
	basis := "Desired structure in proposal " + target.proposal.ID + " revision " + target.draft.ID + "; runtime enforcement is unverified"
	item.SourceCardinality.Basis = append(item.SourceCardinality.Basis, basis)
	item.TargetCardinality.Basis = append(item.TargetCardinality.Basis, basis)
	add := func(id, kind string, attrs map[string]jsontext.Value) {
		facet, err := effectiveProposalFacet(*target.proposal, new(target.draft.ID), id, kind, attrs, target.overlay(id))
		if err != nil || facet == nil {
			return
		}
		for _, property := range slices.Sorted(maps.Keys(facet.PropertyOrigins)) {
			origin := facet.PropertyOrigins[property]
			detail := id + property + " inherits baseline " + target.revisionID + " evidence " + strings.Join(origin.EvidenceIDs, ",")
			if origin.Kind == "intent" {
				detail = id + property + " designed by command " + origin.CommandID + ": " + origin.Reason
			}
			item.SourceCardinality.Basis = append(item.SourceCardinality.Basis, detail)
			item.TargetCardinality.Basis = append(item.TargetCardinality.Basis, detail)
		}
	}
	add(e.ID, e.Kind, e.Attributes)
	add(node.ID, node.Kind, node.Attributes)
	for _, pair := range item.ColumnPairs {
		for _, id := range []string{pair.FromColumnID, pair.ToColumnID} {
			col := p.nodes[id]
			add(col.ID, col.Kind, col.Attributes)
		}
	}
	return item
}

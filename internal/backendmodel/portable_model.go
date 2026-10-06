package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
)

// PortableSource is a domain snapshot, not a table dump. Transport splits its
// members into bounded records; source code bodies and live runtime data are absent.
type PortableSource struct {
	RawEvidence       map[string]jsontext.Value   `json:"rawEvidence"`
	Revision          Revision                    `json:"revision"`
	Coverage          RevisionCoverage            `json:"coverage"`
	Nodes             []Node                      `json:"nodes"`
	Edges             []Edge                      `json:"edges"`
	Evidence          []Evidence                  `json:"evidence"`
	ArtifactContext   jsontext.Value              `json:"artifactContext,omitzero"`
	SourceVector      *SourceVector               `json:"sourceVector,omitzero"`
	Assertions        []ProviderAssertion         `json:"assertions"`
	Selections        []SourceAssertionResolution `json:"selections"`
	Currentness       []SourceClaimCurrentness    `json:"currentness"`
	LegacyProofBases  []LegacyProofBasis          `json:"legacyProofBases"`
	SourceContentHash string                      `json:"sourceContentHash"`
}

type PortableProposal struct {
	Batches         []ChangeAppliedBatch     `json:"batches"`
	Identities      []ChangeObjectIdentity   `json:"identities"`
	Legacy          *Proposal                `json:"legacy,omitzero"`
	Full            *ChangeProposal          `json:"full,omitzero"`
	LegacyRevisions []ProposalRevision       `json:"legacyRevisions"`
	FullRevisions   []ChangeProposalRevision `json:"fullRevisions"`
}

type PortableModel struct {
	Project      Project            `json:"project"`
	Target       BackendReadTarget  `json:"target"`
	Sources      []PortableSource   `json:"sources"`
	Proposals    []PortableProposal `json:"proposals"`
	Diagrams     []DiagramVersion   `json:"diagrams"`
	DiagramViews []DiagramView      `json:"diagramViews"`
	SavedViews   []SavedView        `json:"savedViews"`
	Annotations  []Annotation       `json:"annotations"`
}

type PortableIdentity struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Parent string `json:"parent,omitempty"`
}
type PortableMapping struct {
	Origin  PortableIdentity `json:"origin"`
	LocalID string           `json:"localId"`
}
type PortableArtifactMapping struct {
	Origin NamespacedArtifactPin `json:"origin"`
	Local  NamespacedArtifactPin `json:"local"`
}

type PortableRemap struct {
	OriginInstallationID string                    `json:"originInstallationId"`
	InstallationID       string                    `json:"installationId"`
	IDs                  []PortableMapping         `json:"ids"`
	Artifacts            []PortableArtifactMapping `json:"artifacts"`
}

// ExportPortableSourceTx reads one exact immutable revision on the caller's
// snapshot. The finite closure is selected by the transport orchestrator.
func (r *Repo) ExportPortableSourceTx(ctx context.Context, tx *sql.Tx, pid, rid string) (*PortableSource, error) {
	graph, err := loadSourceGraph(ctx, tx, pid, rid)
	if err != nil {
		return nil, err
	}
	state := graph.State
	coverage, err := loadAPIArtifactCoverage(ctx, tx, &state)
	if err != nil {
		return nil, err
	}
	out := &PortableSource{RawEvidence: graph.RawEvidence, Revision: state.Revision, Coverage: *coverage, Nodes: state.Nodes, Edges: state.Edges, Evidence: state.Evidence, SourceVector: graph.SourceVector, Assertions: graph.Assertions, Selections: graph.Selections, Currentness: graph.Currentness, LegacyProofBases: graph.LegacyProofBases, SourceContentHash: graph.SourceContentHash}
	if state.Revision.SchemaVersion != ComposedSchemaVersion {
		out.SourceVector = nil
		out.Assertions = []ProviderAssertion{}
		out.Selections = []SourceAssertionResolution{}
		out.Currentness = []SourceClaimCurrentness{}
		out.LegacyProofBases = []LegacyProofBasis{}
		if state.ArtifactContextV3 != nil {
			out.SourceContentHash = state.ArtifactContextV3.SourceContentHash
		} else if c := revisionArtifactContext(&state); c != nil {
			out.SourceContentHash = c.SourceContentHash
		}
	}
	for i := range out.Nodes {
		out.Nodes[i].Source = nil
	}
	for i := range out.Edges {
		out.Edges[i].Source = nil
	}
	if state.ArtifactContextV3 != nil {
		out.ArtifactContext, err = EncodeArtifactContextV3(*state.ArtifactContextV3)
	} else if c := revisionArtifactContext(&state); c != nil {
		out.ArtifactContext, err = EncodeArtifactContext(*c, state.Revision.ArtifactPins)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Repo) ExportPortableProposalTx(ctx context.Context, tx *sql.Tx, pid string, target BackendReadTarget) (*PortableProposal, error) {
	out := &PortableProposal{Batches: []ChangeAppliedBatch{}, Identities: []ChangeObjectIdentity{}, LegacyRevisions: []ProposalRevision{}, FullRevisions: []ChangeProposalRevision{}}
	if target.ChangeProposal != nil {
		pin := target.ChangeProposal
		p, err := loadChangeProposal(ctx, tx, pid, pin.ProposalID)
		if err != nil {
			return nil, err
		}
		out.Full = p
		seen := map[string]bool{}
		for rid := pin.ProposalRevisionID; rid != ""; {
			if seen[rid] || len(seen) >= MaxChangeProposalRevisions {
				return nil, invalid("proposal", "Cyclic or excessive proposal history")
			}
			seen[rid] = true
			v, err := loadChangeProposalRevision(ctx, tx, pid, pin.ProposalID, rid)
			if err != nil {
				return nil, err
			}
			out.FullRevisions = append(out.FullRevisions, *v)
			rid = ""
			if v.ParentRevisionID != nil {
				rid = *v.ParentRevisionID
			}
		}
		for _, v := range out.FullRevisions {
			var raw string
			if err := tx.QueryRowContext(ctx, `SELECT document FROM backend_change_proposal_batches WHERE proposal_id=? AND revision_id=?`, pin.ProposalID, v.ID).Scan(&raw); err != nil {
				return nil, err
			}
			var batch ChangeAppliedBatch
			if err := json.Unmarshal([]byte(raw), &batch); err != nil {
				return nil, err
			}
			out.Batches = append(out.Batches, batch)
			rows, err := tx.QueryContext(ctx, `SELECT document FROM backend_change_proposal_identities WHERE proposal_id=? AND first_revision_id=?`, pin.ProposalID, v.ID)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var raw string
				if err := rows.Scan(&raw); err != nil {
					_ = rows.Close()
					return nil, err
				}
				var identity ChangeObjectIdentity
				if err := json.Unmarshal([]byte(raw), &identity); err != nil {
					_ = rows.Close()
					return nil, err
				}
				out.Identities = append(out.Identities, identity)
			}
			err = rows.Err()
			_ = rows.Close()
			if err != nil {
				return nil, err
			}
		}
		slices.Reverse(out.FullRevisions)
		selected := out.FullRevisions[len(out.FullRevisions)-1]
		p.CurrentDraftRevisionID, p.CurrentDraftHash = selected.ID, selected.SemanticHash
		// Export selection is a draft snapshot, never a forged local implementation/review association.
		p.Status = "draft"
		p.ReadyReference = nil
		p.ImplementedReference = nil
		return out, nil
	}
	if target.Proposal != nil {
		pin := target.Proposal
		p, err := loadProposal(ctx, tx, pid, pin.ProposalID)
		if err != nil {
			return nil, err
		}
		out.Legacy = p
		seen := map[string]bool{}
		for rid := pin.ProposalRevisionID; rid != ""; {
			if seen[rid] || len(seen) >= 1000 {
				return nil, invalid("proposal", "Cyclic or excessive proposal history")
			}
			seen[rid] = true
			v, err := loadProposalRevision(ctx, tx, pin.ProposalID, rid)
			if err != nil {
				return nil, err
			}
			out.LegacyRevisions = append(out.LegacyRevisions, *v)
			rid = ""
			if v.ParentRevisionID != nil {
				rid = *v.ParentRevisionID
			}
		}
		slices.Reverse(out.LegacyRevisions)
		selected := out.LegacyRevisions[len(out.LegacyRevisions)-1]
		p.DraftRevisionID, p.DraftHash = selected.ID, selected.SemanticHash
		p.Status = "draft"
		return out, nil
	}
	return nil, invalid("target", "Expected an exact proposal target")
}

func portableClone[T any](in T) (T, error) {
	var out T
	raw, err := json.Marshal(in)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out, json.RejectUnknownMembers(true))
	return out, err
}

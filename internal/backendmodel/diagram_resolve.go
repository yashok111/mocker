package backendmodel

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"slices"
)

type diagramArtifactsKey struct{}

// DiagramContext gives diagram validation the same pinned owner readers as existing artifact reads.
func (s *ArtifactService) DiagramContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, diagramArtifactsKey{}, s)
}
func diagramIdentity(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(p)))
		_, _ = h.Write(n[:])
		_, _ = h.Write([]byte(p))
	}
	b := h.Sum(nil)[:16]
	b[6] = (b[6] & 15) | 128
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func diagramGap(id, code, message string) DiagramGap {
	return DiagramGap{ID: diagramIdentity("diagram-gap-v1", id, code), SubjectID: id, Code: code, Explanation: message}
}
func diagramBases(d DiagramDocument) map[string]struct {
	origin DiagramOrigin
	refs   []DiagramRef
} {
	out := map[string]struct {
		origin DiagramOrigin
		refs   []DiagramRef
	}{}
	for _, e := range d.Payload.Elements {
		out[e.ID] = struct {
			origin DiagramOrigin
			refs   []DiagramRef
		}{e.Origin, e.Refs}
	}
	for _, e := range d.Payload.Links {
		out[e.ID] = struct {
			origin DiagramOrigin
			refs   []DiagramRef
		}{e.Origin, e.Refs}
	}
	if d.Interactions != nil {
		for id, v := range interactionRows(d.Interactions) {
			var o DiagramOrigin
			refs := []DiagramRef{}
			switch v := v.(type) {
			case InteractionParticipant:
				o = v.Origin
				refs = v.Refs
			case InteractionStep:
				o = v.Origin
				refs = v.Refs
			case InteractionBranch:
				o = v.Origin
			case InteractionOrder:
				o = v.Origin
			}
			out[id] = struct {
				origin DiagramOrigin
				refs   []DiagramRef
			}{o, refs}
		}
	}
	return out
}
func resolveDiagramEvidence(ctx context.Context, g *EffectiveGraphSnapshot, d DiagramDocument, previous *DiagramVersion) ([]DiagramGap, error) {
	gaps := []DiagramGap{}
	resolver := newDiagramArtifactResolver(ctx, g)
	nodes := map[string]bool{}
	edges := map[string]bool{}
	evidence := map[DiagramEvidenceRef]bool{}
	for _, n := range g.State.Nodes {
		nodes[n.ID] = true
	}
	for _, e := range g.State.Edges {
		edges[e.ID] = true
	}
	for _, e := range g.State.Evidence {
		evidence[DiagramEvidenceRef{RevisionID: g.Pins.BaseRevisionID, EvidenceID: e.ID, SubjectID: e.SubjectID}] = true
	}
	for _, e := range g.BaselineEvidence {
		evidence[DiagramEvidenceRef{RevisionID: e.RevisionID, EvidenceID: e.EvidenceID, SubjectID: e.SubjectID}] = true
	}
	old := diagramBases(DiagramDocument{})
	if previous != nil {
		old = diagramBases(previous.Document)
	}
	targetMoved := false
	if previous != nil {
		a, _ := requestDigest(previous.Document.Target)
		b, _ := requestDigest(d.Target)
		targetMoved = a != b
	}
	for id, b := range diagramBases(d) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		retained := old[id]
		refGaps, err := diagramReferenceGaps(resolver, id, b.refs, retained.refs, nodes, edges)
		if err != nil {
			return nil, err
		}
		gaps = append(gaps, refGaps...)
		proofGaps, err := diagramEvidenceGaps(id, b.origin.Evidence, retained.origin.Evidence, evidence, previous, targetMoved)
		if err != nil {
			return nil, err
		}
		gaps = append(gaps, proofGaps...)
		if b.origin.Kind == "authored" && len(b.refs) == 0 {
			gaps = append(gaps, diagramGap(id, "authored_unresolved", "Authored boundary or relation has no source mapping"))
		}
	}
	extra, err := resolveInteractionGaps(resolver, d, previous, nodes, edges)
	if err != nil {
		return nil, err
	}
	gaps = append(gaps, extra...)
	slices.SortFunc(gaps, func(a, b DiagramGap) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return gaps, nil
}
func resolveDiagramArtifact(g *EffectiveGraphSnapshot, request *EditorArtifactRequest, ref DiagramRef) (bool, error) {
	if ref.Locator == nil {
		return false, nil
	}
	if !slices.Contains(g.Pins.ArtifactPins, ref.Locator.Pin) {
		return false, nil
	}
	if request == nil {
		return false, invalid("artifact", "Pinned artifact readers are unavailable")
	}
	if g.Pins.ArtifactContext == nil {
		return false, nil
	}
	in := ArtifactQueryInput{RevisionID: g.State.Revision.ID, Artifact: ArtifactKey{Kind: ref.Locator.Pin.Kind, ID: ref.Locator.Pin.ID}, View: ref.Locator.View, Limit: 100}
	if ref.Locator.Embedded != nil {
		in.EmbeddedContractID = ref.Locator.Embedded.ContractID
	}
	desired, _ := requestDigest(ref.Locator)
	for {
		page, err := request.Project(&g.State, *g.Pins.ArtifactContext, in)
		if err != nil {
			return false, err
		}
		for _, row := range page.Items {
			hash, _ := requestDigest(row.Locator)
			if row.ID == ref.RowID && hash == desired && validHash(row.ObjectHash) {
				return true, nil
			}
		}
		if page.NextCursor == "" {
			return false, nil
		}
		in.Cursor = page.NextCursor
	}
}

func diagramReferenceGaps(resolver *diagramArtifactResolver, id string, refs, oldRefs []DiagramRef, nodes, edges map[string]bool) ([]DiagramGap, error) {
	gaps := []DiagramGap{}
	for _, ref := range refs {
		exists := false
		switch ref.Kind {
		case "record":
			exists = ref.RecordType == "node" && nodes[ref.ID] || ref.RecordType == "edge" && edges[ref.ID]
		case "artifact":
			var err error
			exists, err = resolver.resolve(ref)
			if err != nil {
				return nil, err
			}
		}
		if exists {
			continue
		}
		digest, _ := requestDigest(ref)
		inherited := false
		for _, r := range oldRefs {
			hash, _ := requestDigest(r)
			if hash == digest {
				inherited = true
			}
		}
		if !inherited {
			return nil, invalid("refs", "Reference is not owned by the exact target")
		}
		gaps = append(gaps, diagramGap(id, "historical_ref:"+digest, "Retained reference is unavailable on this target; no name-based replacement was made"))
	}

	return gaps, nil
}

func diagramEvidenceGaps(id string, proofs, oldProofs []DiagramEvidenceRef, evidence map[DiagramEvidenceRef]bool, previous *DiagramVersion, targetMoved bool) ([]DiagramGap, error) {
	gaps := []DiagramGap{}
	for _, proof := range proofs {
		inherited := slices.Contains(oldProofs, proof)
		if !evidence[proof] && !inherited {
			return nil, invalid("evidence", "Proof or subject is not owned by the exact target")
		}
		hash, _ := requestDigest(proof)
		historical := false
		if previous != nil && inherited {
			for _, gap := range previous.Gaps {
				if gap.SubjectID == id && gap.Code == "historical_evidence:"+hash {
					historical = true
				}
			}
		}
		if !evidence[proof] || targetMoved && inherited || historical {
			gaps = append(gaps, diagramGap(id, "historical_evidence:"+hash, "Historical source assertion; current target is unverified"))
		}
	}

	return gaps, nil
}

// One owner snapshot budget and exact-ref cache cover the entire diagram write,
// including different rows and repeated refs across C4 elements.
type diagramArtifactResolver struct {
	graph   *EffectiveGraphSnapshot
	request *EditorArtifactRequest
	known   map[string]bool
}

func newDiagramArtifactResolver(ctx context.Context, g *EffectiveGraphSnapshot) *diagramArtifactResolver {
	out := &diagramArtifactResolver{graph: g, known: map[string]bool{}}
	if service, ok := ctx.Value(diagramArtifactsKey{}).(*ArtifactService); ok && service != nil {
		out.request = NewEditorArtifactRequest(ctx, service.api, service.scenarios)
		out.request.effective = g
	}
	return out
}
func (r *diagramArtifactResolver) resolve(ref DiagramRef) (bool, error) {
	key, err := requestDigest(ref)
	if err != nil {
		return false, err
	}
	if found, ok := r.known[key]; ok {
		return found, nil
	}
	found, err := resolveDiagramArtifact(r.graph, r.request, ref)
	if err == nil {
		r.known[key] = found
	}
	return found, err
}

func resolveInteractionGaps(resolver *diagramArtifactResolver, d DiagramDocument, previous *DiagramVersion, nodes, edges map[string]bool) ([]DiagramGap, error) {
	gaps := []DiagramGap{}
	if d.Interactions != nil {
		oldRefs := []DiagramRef{}
		if previous != nil && previous.Document.Interactions != nil {
			oldRefs = previous.Document.Interactions.ScopeRefs
		}
		scopeGaps, err := diagramReferenceGaps(resolver, diagramIdentity("interaction-scope-v1"), d.Interactions.ScopeRefs, oldRefs, nodes, edges)
		if err != nil {
			return nil, err
		}
		gaps = append(gaps, scopeGaps...)
		for _, step := range d.Interactions.Steps {
			if step.Kind == "boundary" {
				gaps = append(gaps, diagramGap(step.ID, "interaction_boundary", "Explicit static boundary; behavior beyond it is unverified"))
			}
			if step.To == "" {
				gaps = append(gaps, diagramGap(step.ID, "unresolved_receiver", "Receiver is not established; no receive or reply was inferred"))
			}
		}
	}
	return gaps, nil
}

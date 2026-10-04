package backendmodel

import (
	"context"
	"slices"
	"strings"
)

func (r *Repo) readEffectiveNode(ctx context.Context, pid string, target BackendReadTarget, nid string) (*BackendNodeRead, error) {
	if !ValidID(nid) {
		return nil, notFound()
	}
	graph, err := r.ResolveEffectiveGraph(ctx, pid, target)
	if err != nil {
		return nil, err
	}
	at := slices.IndexFunc(graph.State.Nodes, func(n Node) bool { return n.ID == nid })
	if at < 0 {
		return nil, notFound()
	}
	node := effectiveNodeRecord(graph, graph.State.Nodes[at])
	out := &BackendNodeRead{Target: new(graph.Target), Pins: new(graph.Pins), Node: new(node), ViewSchemaVersion: graph.Pins.ViewSchemaVersion, Origins: []EffectiveFieldOrigin{}, BaselineEvidence: []EffectiveEvidenceBasis{}}
	for _, origin := range graph.Origins {
		if origin.RecordType == "node" && origin.SubjectID == nid {
			out.Origins = append(out.Origins, origin)
		}
	}
	for _, proof := range graph.BaselineEvidence {
		if proof.SubjectID == nid {
			out.BaselineEvidence = append(out.BaselineEvidence, proof)
		}
	}
	return out, nil
}
func (r *Repo) readEffectiveEvidence(ctx context.Context, pid string, target BackendReadTarget, query EvidenceQueryInput) (*EvidencePage, error) {
	graph, err := r.ResolveEffectiveGraph(ctx, pid, target)
	if err != nil {
		return nil, err
	}
	if query.EvidenceID != "" && (!ValidID(query.EvidenceID) || query.SubjectID != "" || query.Cursor != "") {
		return nil, invalid("evidenceId", "Evidence selector cannot be combined with filters or cursor")
	}
	if query.SubjectID != "" && !ValidID(query.SubjectID) {
		return nil, semantic("subjectId", "Subject ID must be a UUID")
	}
	scope, err := requestDigest(struct {
		Pins                  EffectiveGraphPins
		SubjectID, EvidenceID string
	}{graph.Pins, query.SubjectID, query.EvidenceID})
	if err != nil {
		return nil, err
	}
	limit, after, err := decodeGraphPage(query.Limit, query.Cursor, "effective-evidence", pid, scope, true)
	if err != nil {
		return nil, err
	}
	basis := "baseline"
	if target.ImportCandidate != nil {
		basis = "candidate"
	}
	out := &EvidencePage{Target: new(graph.Target), Pins: new(graph.Pins), ViewSchemaVersion: graph.Pins.ViewSchemaVersion, Basis: basis, Items: []Evidence{}, BaselineEvidence: []EffectiveEvidenceBasis{}}
	proofs := slices.Clone(graph.State.Evidence)
	slices.SortFunc(proofs, func(a, b Evidence) int { return strings.Compare(a.ID, b.ID) })
	ids := []string{}
	for _, proof := range proofs {
		if !effectiveEvidenceMatches(proof, query, after) {
			continue
		}
		if len(out.Items) == limit {
			out.NextCursor = encodeGraphPage("effective-evidence", pid, scope, out.Items[len(out.Items)-1].ID)
			break
		}
		out.Items = append(out.Items, proof)
		ids = append(ids, proof.ID)
	}
	if graph.Source != nil && graph.Source.SourceVector != nil {
		out.Source = sourceEvidenceReadContext(graph.Source, ids)
	}
	for _, proof := range graph.BaselineEvidence {
		if slices.Contains(ids, proof.EvidenceID) {
			out.BaselineEvidence = append(out.BaselineEvidence, proof)
		}
	}
	return out, nil
}
func (r *Repo) readEffectiveCoverage(ctx context.Context, pid string, target BackendReadTarget) (*RevisionCoverage, error) {
	graph, err := r.ResolveEffectiveGraph(ctx, pid, target)
	if err != nil {
		return nil, err
	}
	out := &RevisionCoverage{Target: new(graph.Target), Pins: new(graph.Pins), ViewSchemaVersion: graph.Pins.ViewSchemaVersion, Coverage: graph.State.Revision.Coverage, Inventory: slices.Clone(graph.State.Inventory), Snapshots: slices.Clone(graph.State.Sources), ReconciliationGaps: []string{}}
	if graph.coverage != nil {
		out.Coverage = graph.coverage.Coverage
		out.StaleCounts = graph.coverage.StaleCounts
		out.ReconciliationGaps = append([]string{}, graph.coverage.ReconciliationGaps...)
	}
	if graph.Source != nil {
		out.Source = sourceVectorReadContext(graph.Source)
	}
	return out, nil
}

func effectiveEvidenceMatches(proof Evidence, query EvidenceQueryInput, after string) bool {
	return proof.ID > after && (query.SubjectID == "" || proof.SubjectID == query.SubjectID) && (query.EvidenceID == "" || proof.ID == query.EvidenceID)
}

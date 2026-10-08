package backendmodel

import (
	"context"
	"slices"
)

func validateArchitectureGapQuery(in DiagramQueryInput) error {
	if err := in.GapScope.Validate(); err != nil {
		return err
	}
	if in.GapScope != nil && (in.ResponseMode != "compact-v1" || in.Level == "") {
		return invalid("gapScope", "Explicit scope requires a compact architecture projection")
	}
	return nil
}

// The exact intended scope is independent of current membership: an unmapped
// endpoint may still belong inside the responsibility being audited.
type ArchitectureGapScope struct {
	Format  string   `json:"format"`
	NodeIDs []string `json:"nodeIds"`
}

func (s *ArchitectureGapScope) UnmarshalJSON(raw []byte) error {
	type plain ArchitectureGapScope
	*s = ArchitectureGapScope{}
	return strictAPIObject(raw, []string{"format", "nodeIds"}, nil, (*plain)(s))
}
func (s *ArchitectureGapScope) Validate() error {
	if s == nil {
		return nil
	}
	if s.Format != "exact-node-scope-v1" || len(s.NodeIDs) == 0 || len(s.NodeIDs) > 20000 {
		return invalid("gapScope", "Use an explicit exact-node-scope-v1 set of1–20000 nodes")
	}
	seen := map[string]bool{}
	for _, id := range s.NodeIDs {
		if !ValidID(id) || seen[id] {
			return invalid("gapScope/nodeIds", "Use unique exact node UUIDs")
		}
		seen[id] = true
	}
	return nil
}
func qualifyArchitectureGaps(ctx context.Context, p *architectureProjection, g *EffectiveGraphSnapshot, scope *ArchitectureGapScope) error {
	if scope == nil {
		return nil
	}
	nodes := map[string]bool{}
	for _, n := range g.State.Nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		nodes[n.ID] = true
	}
	selected := map[string]bool{}
	for _, id := range scope.NodeIDs {
		if !nodes[id] {
			return invalid("gapScope/nodeIds", "Scope node is not present at this exact target")
		}
		selected[id] = true
	}
	edges := map[string]Edge{}
	for _, e := range g.State.Edges {
		if err := ctx.Err(); err != nil {
			return err
		}
		edges[e.ID] = e
	}
	for i := range p.gaps {
		if err := ctx.Err(); err != nil {
			return err
		}
		gap := &p.gaps[i]
		gap.Scope = "unqualified"
		if gap.Code == "collapsed_internal" {
			gap.Scope = "collapsed_internal"
			continue
		}
		if !slices.Contains([]string{"unresolved_membership", "ambiguous_membership", "missing_evidence"}, gap.Code) {
			continue
		}
		e, ok := edges[gap.SubjectID]
		if !ok {
			continue
		}
		switch {
		case selected[e.From] && selected[e.To]:
			gap.Scope = "in_scope"
		case selected[e.From] || selected[e.To]:
			gap.Scope = "boundary"
		default:
			gap.Scope = "unrelated"
		}
	}
	return nil
}

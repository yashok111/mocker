package backendmodel

import (
	"cmp"
	"context"
	"slices"
)

// DiagramScopeInput selects identities at an exact semantic version, never a canvas page.
type DiagramScopeInput struct {
	Pin       DiagramPin             `json:"pin"`
	Selectors []DiagramScopeSelector `json:"selectors"`
}
type DiagramScopeSelector struct {
	Kind       string                         `json:"kind"`
	ID         string                         `json:"id"`
	Projection *ArchitectureProjectionContext `json:"projection,omitzero"`
}
type DiagramScope struct {
	Pin        DiagramPin             `json:"pin"`
	Selectors  []DiagramScopeSelector `json:"selectors"`
	Target     BackendReadTarget      `json:"target"`
	TargetHash string                 `json:"targetHash"`
	ScopeHash  string                 `json:"scopeHash"`
	SourceRefs []DiagramRef           `json:"sourceRefs"`
	Members    []DiagramMember        `json:"members"`
	Gaps       []DiagramGap           `json:"gaps"`
	Truncated  bool                   `json:"truncated"`
}

func (s *DiagramScopeSelector) UnmarshalJSON(raw []byte) error {
	type plain DiagramScopeSelector
	if err := strictAPIObject(raw, []string{"kind", "id"}, []string{"projection"}, (*plain)(s)); err != nil {
		return err
	}
	return s.validate()
}
func (s DiagramScopeSelector) validate() error {
	if !ValidID(s.ID) {
		return invalid("selector", "Canonical semantic identity required")
	}
	switch s.Kind {
	case "semantic":
		if s.Projection != nil {
			return invalid("projection", "Semantic selector cannot contain projection")
		}
	case "architecture_relation":
		p := s.Projection
		if p == nil || p.Policy != "architecture-v1" || !slices.Contains([]string{"context", "containers", "components"}, p.Level) || !ValidID(p.RootID) {
			return invalid("projection", "Exact supported projection required")
		}
	default:
		return invalid("selector", "Unknown selector kind")
	}
	return nil
}
func (s *DiagramScopeInput) UnmarshalJSON(raw []byte) error {
	type plain DiagramScopeInput
	if err := strictAPIObject(raw, []string{"pin", "selectors"}, nil, (*plain)(s)); err != nil {
		return err
	}
	return s.Validate()
}
func (s DiagramScopeInput) Validate() error {
	if err := s.Pin.Validate(); err != nil {
		return err
	}
	if len(s.Selectors) == 0 || len(s.Selectors) > 1000 {
		return invalid("selectors", "Select 1–1000 identities")
	}
	for _, v := range s.Selectors {
		if err := v.validate(); err != nil {
			return err
		}
	}
	return nil
}
func (r *Repo) ResolveDiagramScope(ctx context.Context, pid string, in DiagramScopeInput) (*DiagramScope, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	v, err := r.GetDiagram(ctx, pid, in.Pin)
	if err != nil {
		return nil, err
	}
	g, err := r.ResolveEffectiveGraph(ctx, pid, v.Document.Target)
	if err != nil {
		return nil, err
	}
	return resolveDiagramScope(ctx, v, g, in)
}
func resolveDiagramScope(ctx context.Context, v *DiagramVersion, g *EffectiveGraphSnapshot, in DiagramScopeInput) (*DiagramScope, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	if in.Pin != v.Pin || v.TargetHash != g.Pins.TargetHash {
		return nil, diagramPinMismatch()
	}
	out := &DiagramScope{Pin: v.Pin, Target: v.Document.Target, TargetHash: v.TargetHash, Selectors: slices.Clone(in.Selectors), SourceRefs: []DiagramRef{}, Members: []DiagramMember{}, Gaps: slices.Clone(v.Gaps)}
	key := func(v any) string { h, _ := requestDigest(v); return h }
	slices.SortFunc(out.Selectors, func(a, b DiagramScopeSelector) int { return cmp.Compare(key(a), key(b)) })
	out.Selectors = slices.CompactFunc(out.Selectors, func(a, b DiagramScopeSelector) bool { return key(a) == key(b) })
	bases := diagramBases(v.Document)
	projections := map[string]*architectureProjection{}
	refs := map[string]bool{}
	members := map[string]bool{}
	add := func(m DiagramMember) {
		if k := key(m.Ref); !refs[k] {
			refs[k] = true
			out.SourceRefs = append(out.SourceRefs, m.Ref)
		}
		if k := key(m); !members[k] {
			members[k] = true
			out.Members = append(out.Members, m)
		}
	}
	for _, s := range out.Selectors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if s.Kind == "semantic" {
			b, ok := bases[s.ID]
			if !ok {
				return nil, invalid("selector", "Identity is not in the exact diagram")
			}
			for _, ref := range b.refs {
				add(DiagramMember{Ref: ref, Origin: b.origin, TargetHash: v.TargetHash})
			}
			continue
		}
		if v.Document.Kind != "architecture" {
			return nil, invalid("selector", "Projected relations require architecture")
		}
		k := key(s.Projection)
		p := projections[k]
		if p == nil {
			var err error
			p, err = projectArchitecture(ctx, v, g, DiagramQueryInput{Pin: v.Pin, Level: s.Projection.Level, RootID: s.Projection.RootID, Origin: "all", Section: "links", Limit: 500})
			if err != nil {
				return nil, err
			}
			projections[k] = p
			out.Gaps = append(out.Gaps, p.gaps...)
			out.Truncated = out.Truncated || p.truncated
		}
		if _, ok := p.links[s.ID]; !ok {
			return nil, invalid("selector", "Relation is not in the exact projection")
		}
		for _, m := range p.sortedMembers(s.ID) {
			add(m)
		}
	}
	slices.SortFunc(out.SourceRefs, func(a, b DiagramRef) int { return cmp.Compare(key(a), key(b)) })
	slices.SortFunc(out.Members, func(a, b DiagramMember) int { return cmp.Compare(key(a), key(b)) })
	slices.SortFunc(out.Gaps, func(a, b DiagramGap) int { return cmp.Compare(a.ID, b.ID) })
	out.Gaps = slices.CompactFunc(out.Gaps, func(a, b DiagramGap) bool { return a.ID == b.ID })
	if out.Gaps == nil {
		out.Gaps = []DiagramGap{}
	}
	var err error
	out.ScopeHash, err = requestDigest(struct {
		Pin        DiagramPin
		Selectors  []DiagramScopeSelector
		TargetHash string
		Refs       []DiagramRef
	}{out.Pin, out.Selectors, out.TargetHash, out.SourceRefs})
	return out, err
}

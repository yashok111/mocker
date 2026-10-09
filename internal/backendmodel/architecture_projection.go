package backendmodel

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strings"
)

func ProjectArchitecture(ctx context.Context, v *DiagramVersion, g *EffectiveGraphSnapshot, in DiagramQueryInput) (*DiagramPage, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	if v.Pin != in.Pin || v.TargetHash != g.Pins.TargetHash {
		return nil, diagramPinMismatch()
	}
	p, err := projectArchitecture(ctx, v, g, in)
	if err != nil {
		return nil, err
	}
	return architecturePage(ctx, v, p, in)
}

func architecturePage(ctx context.Context, v *DiagramVersion, p *architectureProjection, in DiagramQueryInput) (*DiagramPage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := architectureFilteredRows(ctx, p, in)
	if err != nil {
		return nil, err
	}
	scopeInput := in
	scopeInput.Cursor = ""
	scope, _ := requestDigest(struct {
		ProjectID string
		Query     DiagramQueryInput
		Policy    string
	}{v.ProjectID, scopeInput, "architecture-v1"})
	offset, err := diagramOffset(in.Cursor, scope, len(rows))
	if err != nil {
		return nil, err
	}
	end := min(len(rows), offset+in.Limit)
	page := &DiagramPage{Projection: &ArchitectureProjectionContext{Policy: "architecture-v1", Level: in.Level, RootID: in.RootID}, Pin: v.Pin, TargetHash: v.TargetHash, Total: len(rows), NextCursor: diagramNext(scope, end, len(rows)), Items: rows[offset:end], Gaps: p.gaps, Truncated: p.truncated}
	page.Cache = p.cache
	if err := compactDiagramPage(ctx, page, in); err != nil {
		return nil, err
	}
	return page, ctx.Err()
}
func projectArchitecture(ctx context.Context, v *DiagramVersion, g *EffectiveGraphSnapshot, in DiagramQueryInput) (*architectureProjection, error) {
	all := map[string]ArchitectureElement{}
	for _, e := range v.Document.Payload.Elements {
		all[e.ID] = e
	}
	root, ok := all[in.RootID]
	if !ok || in.Level == "components" && root.Role != "application" || in.Level != "components" && root.Role != "software_system" {
		return nil, invalid("rootId", "Root role does not match architecture level")
	}
	p := newArchitectureProjection(v)
	for _, e := range v.Document.Payload.Elements {
		id := architectureProject(all, in, e.ID)
		if id == "" {
			continue
		}
		p.elements[id] = all[id]
		if _, ok := p.members[id]; !ok {
			p.members[id] = []DiagramMember{}
		}
		for _, ref := range architectureElementRefs(e) {
			p.addMember(id, DiagramMember{Ref: ref, Origin: e.Origin, TargetHash: v.TargetHash})
		}
	}
	memberships := map[string][]string{}
	for _, e := range v.Document.Payload.Elements {
		for _, ref := range architectureElementRefs(e) {
			if ref.Kind == "record" && ref.RecordType == "node" {
				memberships[ref.ID] = append(memberships[ref.ID], e.ID)
			}
		}
	}
	if err := architectureRelations(ctx, p, all, memberships, in, v, g); err != nil {
		return nil, err
	}
	if p.identityErr != nil {
		return nil, p.identityErr
	}
	for id, link := range p.links {
		for _, member := range p.sortedMembers(id) {
			link.Refs = append(link.Refs, member.Ref)
		}
		p.links[id] = link
	}
	slices.SortFunc(p.gaps, func(a, b DiagramGap) int { return cmp.Compare(a.ID, b.ID) })
	p.gaps = slices.CompactFunc(p.gaps, func(a, b DiagramGap) bool { return a.ID == b.ID })
	if err := qualifyArchitectureGaps(ctx, p, g, in.GapScope); err != nil {
		return nil, err
	}
	return p, nil
}

func architectureAncestor(all map[string]ArchitectureElement, id, parent string) bool {
	for i := 0; i <= len(all); i++ {
		if id == parent {
			return true
		}
		e, ok := all[id]
		if !ok || e.ParentID == "" {
			return false
		}
		id = e.ParentID
	}
	return false

}

func architectureProject(all map[string]ArchitectureElement, in DiagramQueryInput, id string) string {
	e, ok := all[id]
	if !ok {
		return ""
	}
	for i := 0; i <= len(all); i++ {
		stop := e.ParentID == ""
		if in.Level == "containers" && e.ParentID == in.RootID {
			stop = true
		}
		if in.Level == "components" && (e.ParentID == in.RootID || e.Role == "application" && !architectureAncestor(all, e.ID, in.RootID) || e.Role == "data_store") {
			stop = true
		}
		if stop {
			return e.ID
		}
		e = all[e.ParentID]
	}
	return ""

}

func architectureOwner(all map[string]ArchitectureElement, memberships map[string][]string, source string) (string, string) {
	candidates := memberships[source]
	if len(candidates) == 0 {
		return "", "unresolved_membership"
	}
	leaves := []string{}
	for _, id := range candidates {
		leaf := true
		for _, other := range candidates {
			if other != id && architectureAncestor(all, other, id) {
				leaf = false
				break
			}
		}
		if leaf {
			leaves = append(leaves, id)
		}
	}
	if len(leaves) != 1 {
		return "", "ambiguous_membership"
	}
	return leaves[0], ""

}

func architectureAdd(p *architectureProjection, all map[string]ArchitectureElement, in DiagramQueryInput, v *DiagramVersion, from, to, relation, label string, origin DiagramOrigin, refs []DiagramRef, explicitSelf bool) {
	a, b := architectureProject(all, in, from), architectureProject(all, in, to)
	if a == "" || b == "" {
		return
	}
	id := diagramIdentity("architecture-v1", in.RootID, in.Level, a, b, relation, origin.Kind)
	fullKey, _ := canonicalJSON([]string{"architecture-v1", in.RootID, in.Level, a, b, relation, origin.Kind})
	if old, exists := p.identityKeys[id]; exists && old != string(fullKey) {
		p.identityErr = &FaultError{Status: 409, Code: "backend_diagram_identity_conflict", Message: "Projected relation identity conflicts with a different full identity key"}
		return
	}
	p.identityKeys[id] = string(fullKey)

	if _, ok := p.members[id]; !ok {
		p.members[id] = []DiagramMember{}
	}
	for _, ref := range refs {
		p.addMember(id, DiagramMember{Ref: ref, Origin: origin, TargetHash: v.TargetHash})
	}
	if a == b && !explicitSelf {
		p.gaps = append(p.gaps, diagramGap(id, "collapsed_internal", "Internal dependency is collapsed at this level; members remain inspectable"))
		return
	}
	link, exists := p.links[id]
	if !exists {
		link = ArchitectureLink{ID: id, Label: label, From: a, To: b, Relation: relation, Origin: origin, Refs: []DiagramRef{}}
	}
	p.links[id] = link

}

func architectureRelations(ctx context.Context, p *architectureProjection, all map[string]ArchitectureElement, memberships map[string][]string, in DiagramQueryInput, v *DiagramVersion, g *EffectiveGraphSnapshot) error {
	architectureExplicitLinks(p, all, memberships, in, v, g)
	proofBySubject, intentBySubject := architectureSourceOrigins(g)
	for i, e := range g.State.Edges {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i >= 250000 {
			p.truncated = true
			p.gaps = append(p.gaps, diagramGap(in.RootID, "traversal_budget", "Whole source traversal budget exhausted"))
			break
		}
		if e.Kind == "contains" {
			continue
		}
		from, fromGap := architectureOwner(all, memberships, e.From)
		to, toGap := architectureOwner(all, memberships, e.To)
		if fromGap != "" || toGap != "" {
			// Unmapped unrelated inventory is not silently assigned to this system.
			code := fromGap
			if toGap == "ambiguous_membership" || code == "" {
				code = toGap
			}
			p.gaps = append(p.gaps, diagramGap(e.ID, code, "Source dependency has a missing or ambiguous explicit architecture membership"))
			continue
		}
		proof := []DiagramEvidenceRef{}
		for _, ev := range proofBySubject[e.ID] {
			if ev.SubjectID == e.ID && slices.Contains(e.EvidenceIDs, ev.ID) {
				proof = append(proof, DiagramEvidenceRef{RevisionID: g.Pins.BaseRevisionID, EvidenceID: ev.ID, SubjectID: e.ID})
			}
		}
		origin, authored := intentBySubject[e.ID]
		if len(proof) == 0 && !authored {
			p.gaps = append(p.gaps, diagramGap(e.ID, "missing_evidence", "Source dependency has no inspectable assertion proof"))
			continue
		}
		if !authored {
			origin = DiagramOrigin{Kind: "source_assertion", Evidence: proof}
		}
		architectureAdd(p, all, in, v, from, to, e.Kind, e.Kind, origin, []DiagramRef{{Kind: "record", RecordType: "edge", ID: e.ID}}, e.From == e.To)
	}

	return nil
}

func architectureFilteredRows(ctx context.Context, p *architectureProjection, in DiagramQueryInput) ([]DiagramRow, error) {
	rows := []DiagramRow{}
	accepts := func(label string, o DiagramOrigin) bool {
		return (in.Origin == "all" || in.Origin == o.Kind) && strings.Contains(strings.ToLower(label), strings.ToLower(in.Search))
	}
	switch in.Section {
	case "elements":
		for _, id := range slices.Sorted(maps.Keys(p.elements)) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			e := p.elements[id]
			if accepts(e.Label, e.Origin) {
				rows = append(rows, DiagramRow{ArchitectureElement: new(e)})
			}
		}
	case "links":
		for _, id := range slices.Sorted(maps.Keys(p.links)) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			e := p.links[id]
			if accepts(e.Label, e.Origin) {
				rows = append(rows, DiagramRow{ArchitectureLink: new(e)})
			}
		}
	case "members":
		return architectureMemberRows(ctx, p, in, accepts)
	case "gaps":
		for _, gap := range p.gaps {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if (in.GapClassification == "" || gap.Scope == in.GapClassification) && strings.Contains(strings.ToLower(gap.Explanation), strings.ToLower(in.Search)) {
				rows = append(rows, DiagramRow{Gap: new(gap)})
			}
		}
	}

	return rows, nil
}

func architectureMemberRows(ctx context.Context, p *architectureProjection, in DiagramQueryInput, accepts func(string, DiagramOrigin) bool) ([]DiagramRow, error) {
	rows := []DiagramRow{}
	if _, ok := p.members[in.SubjectID]; !ok {
		return nil, notFound()
	}
	for _, m := range p.sortedMembers(in.SubjectID) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		label := m.Ref.ID
		if label == "" {
			label = m.Ref.RowID
		}
		if accepts(label, m.Origin) {
			rows = append(rows, DiagramRow{Member: new(m)})
		}
	}
	return rows, ctx.Err()
}

// Source relations always come from the effective graph. A client's C4 link
// cannot consume a source edge, reverse it, or bypass ambiguous membership.
func architectureExplicitLinks(p *architectureProjection, all map[string]ArchitectureElement, memberships map[string][]string, in DiagramQueryInput, v *DiagramVersion, g *EffectiveGraphSnapshot) {
	edges := map[string]Edge{}
	for _, edge := range g.State.Edges {
		edges[edge.ID] = edge
	}
	for _, link := range v.Document.Payload.Links {
		if link.Origin.Kind == "authored" {
			architectureAdd(p, all, in, v, link.From, link.To, link.Relation, link.Label, link.Origin, link.Refs, link.From == link.To)
			continue
		}
		if !architectureSourceLinkMatches(all, memberships, edges, link) {
			p.gaps = append(p.gaps, diagramGap(link.ID, "source_relation_mismatch", "Declared source relation is unresolved or differs from exact source endpoints/relation; source projection uses only verified dependencies"))
		}
	}
}
func architectureSourceLinkMatches(all map[string]ArchitectureElement, memberships map[string][]string, edges map[string]Edge, link ArchitectureLink) bool {
	matched := false
	for _, ref := range link.Refs {
		if ref.Kind != "record" || ref.RecordType != "edge" {
			continue
		}
		edge, ok := edges[ref.ID]
		if !ok {
			return false
		}
		from, fromGap := architectureOwner(all, memberships, edge.From)
		to, toGap := architectureOwner(all, memberships, edge.To)
		if fromGap != "" || toGap != "" || link.Relation != edge.Kind {
			return false
		}
		if !architectureAncestor(all, from, link.From) || !architectureAncestor(all, to, link.To) {
			return false
		}
		matched = true
	}
	return matched
}

func architectureSourceOrigins(g *EffectiveGraphSnapshot) (map[string][]Evidence, map[string]DiagramOrigin) {
	proofBySubject := map[string][]Evidence{}
	for _, proof := range g.State.Evidence {
		proofBySubject[proof.SubjectID] = append(proofBySubject[proof.SubjectID], proof)
	}
	intentBySubject := map[string]DiagramOrigin{}
	for _, origin := range g.Origins {
		if origin.RecordType == "edge" && origin.Kind != "source" {
			reason := origin.Reason
			if reason == "" {
				reason = "Explicit desired graph change; source execution is unverified"
			}
			intentBySubject[origin.SubjectID] = DiagramOrigin{Kind: "authored", Reason: reason}
		}
	}

	return proofBySubject, intentBySubject
}

func newArchitectureProjection(v *DiagramVersion) *architectureProjection {
	p := &architectureProjection{elements: map[string]ArchitectureElement{}, links: map[string]ArchitectureLink{}, members: map[string][]DiagramMember{}, gaps: slices.Clone(v.Gaps)}
	p.targetHash = v.TargetHash
	p.identityKeys = map[string]string{}
	for _, element := range v.Document.Payload.Elements {
		p.identityKeys[element.ID] = "element:" + element.ID
	}
	for _, link := range v.Document.Payload.Links {
		p.identityKeys[link.ID] = "semantic-link:" + link.ID
	}
	if p.gaps == nil {
		p.gaps = []DiagramGap{}
	}

	return p
}

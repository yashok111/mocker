package backendmodel

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strings"
)

func businessMapRows(p *BusinessMapPayload) map[string]any {
	rows := map[string]any{}
	for _, e := range p.Elements {
		rows[e.ID] = e
	}
	for _, l := range p.Links {
		rows[l.ID] = l
	}
	return rows
}
func businessMapBasis(v any) (DiagramOrigin, []DiagramRef) {
	switch v := v.(type) {
	case BusinessElement:
		return v.Origin, v.Refs
	case BusinessLink:
		return v.Origin, v.Refs
	}
	return DiagramOrigin{}, nil
}
func normalizeBusinessMap(p *BusinessMapPayload, normalize func(*DiagramOrigin, []DiagramRef)) {
	slices.SortFunc(p.Elements, func(a, b BusinessElement) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(p.Links, func(a, b BusinessLink) int { return cmp.Compare(a.ID, b.ID) })
	for i := range p.Elements {
		normalize(&p.Elements[i].Origin, p.Elements[i].Refs)
	}
	for i := range p.Links {
		normalize(&p.Links[i].Origin, p.Links[i].Refs)
	}
}
func ProjectBusinessMap(ctx context.Context, v *DiagramVersion, in DiagramQueryInput) (*DiagramPage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if in.Level != "" || in.RootID != "" {
		return nil, invalid("projection", "Architecture selectors forbidden for business_map")
	}
	items, err := businessMapItems(v, in)
	if err != nil {
		return nil, err
	}
	scopeInput := in
	scopeInput.Cursor = ""
	scope, _ := requestDigest(struct {
		Project, Policy string
		Input           DiagramQueryInput
	}{v.ProjectID, "business_map-v1", scopeInput})
	start, err := diagramOffset(in.Cursor, scope, len(items))
	if err != nil {
		return nil, err
	}
	end := min(start+in.Limit, len(items))
	return &DiagramPage{Pin: v.Pin, TargetHash: v.TargetHash, Total: len(items), Items: items[start:end], Gaps: slices.Clone(v.Gaps), NextCursor: diagramNext(scope, end, len(items))}, nil
}

// A technical operation alone does not establish a broker-message implementation.
func businessMapImplementationGaps(g *EffectiveGraphSnapshot, p *BusinessMapPayload) []DiagramGap {
	gaps := []DiagramGap{}
	if p == nil {
		return gaps
	}
	messages := map[string]bool{}
	for _, n := range g.State.Nodes {
		if n.Kind == "message" {
			messages[n.ID] = true
		}
	}
	owners := map[string][]string{}
	for _, e := range p.Elements {
		if e.Role != "business_event" {
			continue
		}
		mapped := false
		for _, ref := range e.Refs {
			message := ref.Kind == "record" && ref.RecordType == "node" && messages[ref.ID] || ref.Kind == "artifact" && ref.Locator != nil && ref.Locator.View == "event_model" && ref.Locator.Owner.MessageID != ""
			if message {
				mapped = true
				key, _ := requestDigest(ref)
				owners[key] = append(owners[key], e.ID)
			}
		}
		if !mapped {
			gaps = append(gaps, diagramGap(e.ID, "unresolved_business_message", "No exact message implementation is established for this business event"))
		}
	}
	for ref, ids := range owners {
		if len(ids) > 1 {
			for _, id := range ids {
				gaps = append(gaps, diagramGap(id, "shared_business_message:"+ref, "A message supports several explicitly distinct business concepts; their identity is not inferred"))
			}
		}
	}
	return gaps
}

func businessMapItems(v *DiagramVersion, in DiagramQueryInput) ([]DiagramRow, error) {
	rows := businessMapRows(v.Document.BusinessMap)
	bases := diagramBases(v.Document)
	items := []DiagramRow{}
	if in.Section == "members" {
		if _, ok := rows[in.SubjectID]; !ok {
			return nil, notFound()
		}
	}
	for _, id := range slices.Sorted(maps.Keys(rows)) {
		b := bases[id]
		if in.Origin != "all" && b.origin.Kind != in.Origin {
			continue
		}
		if in.Section == "members" {
			if id == in.SubjectID {
				for _, ref := range b.refs {
					items = append(items, DiagramRow{Member: &DiagramMember{Ref: ref, Origin: b.origin, TargetHash: v.TargetHash}})
				}
			}
			continue
		}
		row, label := businessMapQueryRow(id, rows[id])

		if in.Search != "" && !strings.Contains(strings.ToLower(label), strings.ToLower(in.Search)) {
			continue
		}
		if in.Section == "elements" && row.BusinessElement != nil || in.Section == "links" && row.BusinessLink != nil {
			items = append(items, row)
		}
	}
	if in.Section == "gaps" {
		for _, gap := range v.Gaps {
			if in.Search == "" || strings.Contains(strings.ToLower(gap.Explanation), strings.ToLower(in.Search)) {
				items = append(items, DiagramRow{Gap: &gap})
			}
		}
	}

	return items, nil
}

func businessMapQueryRow(id string, value any) (DiagramRow, string) {
	var row DiagramRow
	label := id
	switch value := value.(type) {
	case BusinessElement:
		row.BusinessElement = &value
		label += " " + value.Label + " " + value.Role + " " + value.Responsibility
	case BusinessLink:
		row.BusinessLink = &value
		label += " " + value.Label + " " + value.Relation
	}

	return row, label
}

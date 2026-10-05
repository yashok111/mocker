package backendmodel

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"maps"
	"slices"
	"strings"
)

func interactionRows(p *InteractionPayload) map[string]any {
	rows := map[string]any{}
	for _, v := range p.Participants {
		rows[v.ID] = v
	}
	for _, v := range p.Steps {
		rows[v.ID] = v
	}
	for _, v := range p.Branches {
		rows[v.ID] = v
	}
	for _, v := range p.Order {
		rows[v.ID] = v
	}
	return rows
}
func normalizeInteractions(p *InteractionPayload, normalize func(*DiagramOrigin, []DiagramRef)) {
	slices.SortFunc(p.Participants, func(a, b InteractionParticipant) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(p.Steps, func(a, b InteractionStep) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(p.Branches, func(a, b InteractionBranch) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(p.Order, func(a, b InteractionOrder) int { return cmp.Compare(a.ID, b.ID) })
	for i := range p.Participants {
		v := &p.Participants[i]
		normalize(&v.Origin, v.Refs)
	}
	for i := range p.Steps {
		v := &p.Steps[i]
		normalize(&v.Origin, v.Refs)
	}
	for i := range p.Branches {
		normalize(&p.Branches[i].Origin, nil)
	}
	for i := range p.Order {
		normalize(&p.Order[i].Origin, nil)
	}
	normalize(&DiagramOrigin{}, p.ScopeRefs)
}
func ProjectInteractions(ctx context.Context, v *DiagramVersion, in DiagramQueryInput) (*DiagramPage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if in.Level != "" || in.RootID != "" {
		return nil, invalid("projection", "Architecture level/root forbidden for interactions")
	}
	items, err := interactionItems(v, in)
	if err != nil {
		return nil, err
	}
	scopeInput := in
	scopeInput.Cursor = ""
	scope, _ := requestDigest(struct {
		Project, Policy string
		Input           DiagramQueryInput
	}{v.ProjectID, "interactions-v1", scopeInput})
	start, err := diagramOffset(in.Cursor, scope, len(items))
	if err != nil {
		return nil, err
	}
	end := min(start+in.Limit, len(items))
	return &DiagramPage{Pin: v.Pin, TargetHash: v.TargetHash, Total: len(items), Items: items[start:end], Gaps: slices.Clone(v.Gaps), NextCursor: diagramNext(scope, end, len(items))}, nil
}

func marshalInteractionRow(r DiagramRow) (string, any, int) {
	count := 0
	kind := ""
	var value any
	if r.Participant != nil {
		count++
		kind = "participant"
		value = r.Participant
	}
	if r.Step != nil {
		count++
		kind = "step"
		value = r.Step
	}
	if r.Branch != nil {
		count++
		kind = "branch"
		value = r.Branch
	}
	if r.Order != nil {
		count++
		kind = "order"
		value = r.Order
	}
	return kind, value, count
}

func interactionDependencyEqual(a, b *InteractionPayload) bool {
	aa, _ := json.Marshal(a.Architecture)
	bb, _ := json.Marshal(b.Architecture)
	return string(aa) == string(bb)
}

func interactionItems(v *DiagramVersion, in DiagramQueryInput) ([]DiagramRow, error) {
	rows := interactionRows(v.Document.Interactions)
	items := []DiagramRow{}
	bases := diagramBases(v.Document)
	for _, id := range slices.Sorted(maps.Keys(rows)) {
		b := bases[id]
		if in.Origin != "all" && in.Origin != b.origin.Kind {
			continue
		}
		if in.Section == "members" {
			if id != in.SubjectID {
				continue
			}
			for _, ref := range b.refs {
				items = append(items, DiagramRow{Member: &DiagramMember{Ref: ref, Origin: b.origin, TargetHash: v.TargetHash}})
			}
			continue
		}
		row, label := interactionQueryRow(id, rows[id])
		if in.Search != "" && !strings.Contains(strings.ToLower(label), strings.ToLower(in.Search)) {
			continue
		}
		if in.Section == "elements" && row.Order == nil || in.Section == "links" && row.Order != nil {
			items = append(items, row)
		}
	}
	if in.Section == "members" {
		if _, ok := rows[in.SubjectID]; !ok {
			return nil, notFound()
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

func interactionQueryRow(id string, value any) (DiagramRow, string) {
	var row DiagramRow
	label := id
	switch value := value.(type) {
	case InteractionParticipant:
		row.Participant = &value
		label += " " + value.Label
	case InteractionStep:
		row.Step = &value
		label += " " + value.Label
	case InteractionBranch:
		row.Branch = &value
		label += " " + value.Label
	case InteractionOrder:
		row.Order = &value
	}
	return row, label
}

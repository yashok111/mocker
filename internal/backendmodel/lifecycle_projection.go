package backendmodel

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strings"
)

func lifecycleRows(p *LifecyclePayload) map[string]any {
	rows := map[string]any{}
	for _, v := range p.States {
		rows[v.ID] = v
	}
	for _, v := range p.Transitions {
		rows[v.ID] = v
	}
	for _, v := range p.Rules {
		rows[v.ID] = v
	}
	return rows
}
func normalizeLifecycle(p *LifecyclePayload, normalize func(*DiagramOrigin, []DiagramRef)) {
	slices.SortFunc(p.States, func(a, b LifecycleState) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(p.Transitions, func(a, b LifecycleTransition) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(p.Rules, func(a, b LifecycleRule) int { return cmp.Compare(a.ID, b.ID) })
	for i := range p.States {
		s := &p.States[i]
		normalize(&s.Origin, s.Refs)
	}
	for i := range p.Transitions {
		tr := &p.Transitions[i]
		normalize(&tr.Origin, tr.Refs)
		for _, refs := range [][]DiagramRef{tr.Triggers, tr.Writes, tr.Events} {
			normalize(&DiagramOrigin{}, refs)
		}
	}
	for i := range p.Rules {
		normalize(&p.Rules[i].Origin, nil)
	}
	normalize(&p.CoverageOrigin, nil)
	// stateFields retains explicit mapping order; it is not an inferred set.
}
func ProjectLifecycle(ctx context.Context, v *DiagramVersion, in DiagramQueryInput) (*DiagramPage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if in.Level != "" || in.RootID != "" {
		return nil, invalid("projection", "Architecture selectors forbidden for lifecycle")
	}
	rows := lifecycleRows(v.Document.Lifecycle)
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
		var row DiagramRow
		label := id
		switch value := rows[id].(type) {
		case LifecycleState:
			row.State = &value
			label += " " + value.Label
		case LifecycleTransition:
			row.Transition = &value
			label += " " + value.Label
		case LifecycleRule:
			row.Rule = &value
			label += " " + value.Verdict
		}
		if in.Search != "" && !strings.Contains(strings.ToLower(label), strings.ToLower(in.Search)) {
			continue
		}
		if in.Section == "elements" || in.Section == "links" && row.Transition != nil {
			items = append(items, row)
		}
	}
	if in.Section == "gaps" {
		for _, gap := range uniqueDiagramGaps(v.Gaps) { // F104: stored duplicates
			if in.Search == "" || strings.Contains(strings.ToLower(gap.Explanation), strings.ToLower(in.Search)) {
				items = append(items, DiagramRow{Gap: &gap})
			}
		}
	}
	scopeInput := in
	scopeInput.Cursor = ""
	scope, _ := requestDigest(struct {
		Project, Policy string
		Input           DiagramQueryInput
	}{v.ProjectID, "lifecycle-v1", scopeInput})
	start, err := diagramOffset(in.Cursor, scope, len(items))
	if err != nil {
		return nil, err
	}
	end := min(start+in.Limit, len(items))
	return &DiagramPage{Pin: v.Pin, TargetHash: v.TargetHash, Total: len(items), Items: items[start:end], Gaps: slices.Clone(v.Gaps), NextCursor: diagramNext(scope, end, len(items))}, nil
}

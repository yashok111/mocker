package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"slices"
	"strconv"
)

type DiagramCompareInput struct {
	Before DiagramPin `json:"before"`
	After  DiagramPin `json:"after"`
	Limit  int        `json:"limit"`
	Cursor string     `json:"cursor,omitempty"`
}
type DiagramDifference struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind"`
	Fields []string `json:"fields"`
}
type DiagramComparison struct {
	Before     DiagramPin          `json:"before"`
	After      DiagramPin          `json:"after"`
	Items      []DiagramDifference `json:"items"`
	NextCursor string              `json:"nextCursor"`
}

func (v *DiagramCompareInput) UnmarshalJSON(b []byte) error {
	type plain DiagramCompareInput
	*v = DiagramCompareInput{}
	return strictAPIObject(b, []string{"before", "after", "limit"}, []string{"cursor"}, (*plain)(v))
}
func (r *Repo) CompareDiagrams(ctx context.Context, pid string, in DiagramCompareInput) (*DiagramComparison, error) {
	if in.Limit < 1 || in.Limit > 500 {
		return nil, invalid("limit", "Use 1–500")
	}
	before, err := r.GetDiagram(ctx, pid, in.Before)
	if err != nil {
		return nil, err
	}
	after, err := r.GetDiagram(ctx, pid, in.After)
	if err != nil {
		return nil, err
	}
	if before.Document.Kind != after.Document.Kind {
		return nil, diagramUnsupported()
	}
	oldGraph, err := r.ResolveEffectiveGraph(ctx, pid, before.Document.Target)
	if err != nil {
		return nil, err
	}
	newGraph, err := r.ResolveEffectiveGraph(ctx, pid, after.Document.Target)
	if err != nil {
		return nil, err
	}
	metadataID := ""
	if before.Document.Kind == "business_map" {
		occupied := businessMapRows(before.Document.BusinessMap)
		for id, v := range businessMapRows(after.Document.BusinessMap) {
			occupied[id] = v
		}
		for salt := 0; salt <= len(occupied); salt++ {
			id := diagramIdentity("business-map-metadata-v1", strconv.Itoa(salt))
			if _, ok := occupied[id]; !ok {
				metadataID = id
				break
			}
		}
	}
	if before.Document.Kind == "lifecycle" {
		metadataID = lifecycleComparisonMetadataID(before.Document.Lifecycle, after.Document.Lifecycle)
	}
	if before.Document.Kind == "interactions" {
		metadataID, err = interactionComparisonMetadataID(before.Document.Interactions, after.Document.Interactions)
		if err != nil {
			return nil, err
		}
	}
	a, err := diagramComparisonRows(ctx, before, oldGraph, metadataID)
	if err != nil {
		return nil, err
	}
	b, err := diagramComparisonRows(ctx, after, newGraph, metadataID)
	if err != nil {
		return nil, err
	}
	items := diagramDifferences(a, b)
	scopeInput := in
	scopeInput.Cursor = ""
	scope, _ := requestDigest(struct {
		Project, Kind string
		Input         DiagramCompareInput
	}{pid, "diagram-compare-v1", scopeInput})
	start, err := diagramOffset(in.Cursor, scope, len(items))
	if err != nil {
		return nil, err
	}
	end := min(start+in.Limit, len(items))
	return &DiagramComparison{Before: in.Before, After: in.After, Items: items[start:end], NextCursor: diagramNext(scope, end, len(items))}, nil
}

// Compare has no level selector. Its canonical relationship view is Context at
// each document's primary system; payload rows still expose container/component
// edits. Pins retain the before-side graph so removed members remain readable.
func diagramComparisonRows(ctx context.Context, v *DiagramVersion, g *EffectiveGraphSnapshot, metadataID string) (map[string]map[string]jsontext.Value, error) {
	if v.TargetHash != g.Pins.TargetHash {
		return nil, diagramPinMismatch()
	}
	if v.Document.Lifecycle != nil {
		return lifecycleComparisonRows(v.Document.Lifecycle, metadataID)
	}
	rows := map[string]map[string]jsontext.Value{}
	add := func(id string, value any) error {
		raw, err := canonicalJSON(value)
		if err != nil {
			return err
		}
		fields := map[string]jsontext.Value{}
		if err = json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		rows[id] = fields
		return nil
	}
	if v.Document.BusinessMap != nil {
		for id, value := range businessMapRows(v.Document.BusinessMap) {
			if err := add(id, value); err != nil {
				return nil, err
			}
		}
		if !ValidID(metadataID) {
			return nil, invalid("comparison", "Exact free metadata identity required")
		}
		dependency, err := canonicalJSON(v.Document.BusinessMap.Architecture)
		if err != nil {
			return nil, err
		}
		rows[metadataID] = map[string]jsontext.Value{"architecture": dependency}
		return rows, nil
	}
	if v.Document.Interactions != nil {
		for id, value := range interactionRows(v.Document.Interactions) {
			if err := add(id, value); err != nil {
				return nil, err
			}
		}

		id := metadataID
		if !ValidID(id) {
			return nil, invalid("comparison", "Exact metadata identity required")
		}
		if rows[id] == nil {
			rows[id] = map[string]jsontext.Value{}
		}
		scope, err := canonicalJSON(v.Document.Interactions.ScopeRefs)
		if err != nil {
			return nil, err
		}
		dependency, err := canonicalJSON(v.Document.Interactions.Architecture)
		if err != nil {
			return nil, err
		}
		rows[id]["scopeRefs"] = scope
		rows[id]["architecture"] = dependency
		return rows, nil
	}
	for _, e := range v.Document.Payload.Elements {
		if err := add(e.ID, e); err != nil {
			return nil, err
		}
	}
	for _, e := range v.Document.Payload.Links {
		if err := add(e.ID, e); err != nil {
			return nil, err
		}
	}
	rows[v.Document.Payload.PrimarySystemID]["primarySystemId"] = jsontext.Value(`true`)
	in := DiagramQueryInput{Pin: v.Pin, Level: "context", RootID: v.Document.Payload.PrimarySystemID, Origin: "all", Section: "links", Limit: 100}
	p, err := projectArchitecture(ctx, v, g, in)
	if err != nil {
		return nil, err
	}
	for id := range p.members {
		// Include hidden internal groups too. Evidence revision/target hashes are
		// navigation context, not semantic membership changes on an unchanged edge.
		refs := []DiagramRef{}
		for _, member := range p.sortedMembers(id) {
			refs = append(refs, member.Ref)
		}
		raw, err := canonicalJSON(refs)
		if err != nil {
			return nil, err
		}
		if fields, exists := rows[id]; exists {
			fields["members"] = raw
			continue
		}
		rows[id] = map[string]jsontext.Value{"members": raw}
	}
	return rows, nil
}
func diagramDifferences(a, b map[string]map[string]jsontext.Value) []DiagramDifference {
	ids := map[string]bool{}
	for id := range a {
		ids[id] = true
	}
	for id := range b {
		ids[id] = true
	}
	items := make([]DiagramDifference, 0, len(ids))
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		old, oldOK := a[id]
		next, nextOK := b[id]
		diff := DiagramDifference{ID: id, Fields: []string{}}
		switch {
		case !oldOK:
			diff.Kind = "added"
		case !nextOK:
			diff.Kind = "removed"
		default:
			keys := map[string]bool{}
			for k := range old {
				keys[k] = true
			}
			for k := range next {
				keys[k] = true
			}
			for _, k := range slices.Sorted(maps.Keys(keys)) {
				if string(old[k]) != string(next[k]) {
					diff.Fields = append(diff.Fields, k)
				}
			}
			if len(diff.Fields) == 0 {
				continue
			}
			diff.Kind = "changed"
		}
		items = append(items, diff)
	}
	return items
}

// Choose one free metadata identity jointly for both pins. Semantic IDs remain
// caller-controlled; even an intentional namespace collision cannot hide a removal.
func interactionComparisonMetadataID(before, after *InteractionPayload) (string, error) {
	occupied := map[string]bool{}
	for _, payload := range []*InteractionPayload{before, after} {
		for id := range interactionRows(payload) {
			occupied[id] = true
		}
	}
	id := diagramIdentity("interaction-document-metadata-v1")
	if !occupied[id] {
		return id, nil
	}
	for salt := range len(occupied) + 1 {
		id = diagramIdentity("interaction-document-metadata-v1", strconv.Itoa(salt))
		if !occupied[id] {
			return id, nil
		}
	}
	return "", invalid("comparison", "Metadata identity collision budget exhausted")
}

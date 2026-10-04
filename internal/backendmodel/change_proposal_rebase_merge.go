package backendmodel

import (
	"bytes"
	"encoding/json/v2"
	"maps"
	"slices"
)

type changeRebaseMerge struct {
	oursIdentityEdits, nextIdentityEdits map[string]bool
	baseIdentities                       map[string]ChangeEvaluationIdentity
	oursIdentities                       map[string]ChangeEvaluationIdentity
	input                                PreviewChangeProposalRebaseInput
	draft                                ChangeProposalRevision
	next                                 *SourceGraphSnapshot
	conflicts                            []ChangeRebaseConflict
	resolutions                          map[string]ChangeRebaseResolution
	seen                                 map[string]bool
}

func rebaseValue(present bool, value any) (ChangeRebaseValue, error) {
	if !present {
		return ChangeRebaseValue{Presence: "absent_property"}, nil
	}
	raw, err := canonicalJSON(value)
	presence := "value"
	if bytes.Equal(raw, []byte("null")) {
		presence = "null"
	}
	return ChangeRebaseValue{Presence: presence, Value: raw}, err
}
func sameRebaseValue(a, b ChangeRebaseValue) bool {
	if a.Presence != b.Presence {
		return false
	}
	x, e1 := canonicalJSON(a)
	y, e2 := canonicalJSON(b)
	return e1 == nil && e2 == nil && bytes.Equal(x, y)
}
func (m *changeRebaseMerge) choose(ref ChangeRecordRef, selector ChangeRebaseSelector, b, o, n ChangeRebaseValue, force bool) (ChangeRebaseValue, *RebaseResolutionOrigin, error) {
	if !force {
		if sameRebaseValue(o, b) || sameRebaseValue(o, n) {
			return n, nil, nil
		}
		if sameRebaseValue(n, b) {
			return o, nil, nil
		}
	}
	hashes := [3]string{}
	for i, v := range []ChangeRebaseValue{b, o, n} {
		h, err := requestDigest(v)
		if err != nil {
			return ChangeRebaseValue{}, nil, err
		}
		hashes[i] = h
	}
	id, err := requestDigest(struct {
		Protocol                                               string
		DraftID, DraftHash, BaseID, BaseHash, NextID, NextHash string
		Object                                                 ChangeRecordRef
		Selector                                               ChangeRebaseSelector
		Values                                                 [3]string
	}{changeRebaseProtocol, m.draft.ID, m.draft.SemanticHash, m.draft.BaseRevisionID, m.draft.BaseSemanticHash, m.next.State.Revision.ID, m.next.State.Revision.SemanticHash, ref, selector, hashes})
	if err != nil {
		return ChangeRebaseValue{}, nil, err
	}
	resolution, ok := m.resolutions[id]
	if !ok {
		m.conflicts = append(m.conflicts, ChangeRebaseConflict{ID: id, Object: ref, Selector: selector, Base: b, Ours: o, NewSource: n})
		return n, nil, nil
	}
	m.seen[id] = true
	origin := &RebaseResolutionOrigin{ProposalRevisionID: m.draft.ID, ResolutionID: id, Reason: resolution.Reason}
	switch resolution.Choice {
	case "take_source":
		if selector.Kind == "identity" && n.Presence == "absent_property" {
			return n, origin, nil
		}
		return n, nil, nil
	case "keep_proposal":
		return o, origin, nil
	case "replace":
		if err := validateChangeRebaseReplacement(selector, resolution.Value); err != nil {
			return ChangeRebaseValue{}, nil, err
		}
		v, err := rebaseValue(true, resolution.Value)
		return v, origin, err
	default:
		return ChangeRebaseValue{}, nil, invalid("resolution/choice", "Unknown resolution")
	}
}
func rebaseRecordValue(r ChangeCreatedRecord, ok bool) (ChangeRebaseValue, error) {
	if !ok {
		return ChangeRebaseValue{Presence: "absent_record"}, nil
	}
	p := r.Payload
	var err error
	p.Attributes, err = changeSemanticAttributes(p.Attributes)
	if err != nil {
		return ChangeRebaseValue{}, err
	}
	return rebaseValue(true, p)
}
func rebasePropertyValue(p SourceAssertionPayload, s TypedSourcePropertySelector) (ChangeRebaseValue, error) {
	value, err := SelectSourceProperty(p, s)
	if err != nil {
		return ChangeRebaseValue{}, err
	}
	return rebaseValue(value.Present, value.Value)
}
func rebaseDesiredOrigin(old EffectiveOrigin, resolution *RebaseResolutionOrigin, ref ChangeRecordRef, base ChangeProposalRevision) EffectiveOrigin {
	if resolution != nil {
		return EffectiveOrigin{Kind: "intent", RebaseResolution: resolution, BaseRef: &EffectiveBasis{RevisionID: base.BaseRevisionID, SemanticHash: base.BaseSemanticHash, RecordType: ref.RecordType, ID: ref.ID}}
	}
	return old
}
func rebasePropertyOrigin(e *changeEvaluation, r ChangeCreatedRecord, s TypedSourcePropertySelector) EffectiveOrigin {
	for _, p := range e.revision.Delta.Properties {
		if p.ChangeRecordRef == r.ChangeRecordRef && p.Selector == s {
			return p.Origin
		}
	}
	return r.Origin
}
func (m *changeRebaseMerge) records(base, ours, result *changeEvaluation) error {
	if err := m.recordIdentityEdits(base, ours, result); err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, e := range []*changeEvaluation{base, ours, result} {
		for id := range e.records {
			ids[id] = true
		}
	}
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		if err := m.record(id, base, ours, result); err != nil {
			return err
		}
	}
	return nil
}
func (m *changeRebaseMerge) properties(b, o, n ChangeCreatedRecord, ours, result *changeEvaluation) error {
	selectors, err := sourceSelectors([]SourceAssertionPayload{b.Payload, o.Payload, n.Payload})
	if err != nil {
		return err
	}
	for _, s := range selectors {
		bv, err := rebasePropertyValue(b.Payload, s)
		if err != nil {
			return err
		}
		ov, err := rebasePropertyValue(o.Payload, s)
		if err != nil {
			return err
		}
		nv, err := rebasePropertyValue(n.Payload, s)
		if err != nil {
			return err
		}
		v, resolution, err := m.choose(o.ChangeRecordRef, ChangeRebaseSelector{Kind: "property", Property: &EffectivePropertySelector{Kind: "source", Source: new(s)}}, bv, ov, nv, false)
		if err != nil {
			return err
		}
		if sameRebaseValue(v, nv) && resolution == nil {
			continue
		}
		value := SourcePropertyValue{Present: v.Presence != "absent_property", Value: v.Value}
		if err = validateChangeSourceExpected(ComposedSchemaVersion, n.Payload, s, value); err != nil {
			return err
		}
		current := result.records[n.ID]
		current.Payload, err = ApplySourceProperty(current.Payload, s, value)
		if err != nil {
			return err
		}
		result.records[n.ID] = current
		origin := m.selectedOrigin(rebasePropertyOrigin(ours, o, s), resolution, o.ChangeRecordRef, m.draft)
		result.revision.Delta.Properties = append(result.revision.Delta.Properties, ChangeProperty{ChangeRecordRef: o.ChangeRecordRef, Selector: s, Value: value, Origin: origin})
	}
	return nil
}
func (m *changeRebaseMerge) residualProperties(o, n ChangeCreatedRecord, ours, result *changeEvaluation, resolution *RebaseResolutionOrigin) error {
	selectors, err := sourceSelectors([]SourceAssertionPayload{o.Payload, n.Payload})
	if err != nil {
		return err
	}
	for _, s := range selectors {
		ov, err := rebasePropertyValue(o.Payload, s)
		if err != nil {
			return err
		}
		nv, err := rebasePropertyValue(n.Payload, s)
		if err != nil {
			return err
		}
		if sameRebaseValue(ov, nv) {
			continue
		}
		value := SourcePropertyValue{Present: ov.Presence != "absent_property", Value: ov.Value}
		result.revision.Delta.Properties = append(result.revision.Delta.Properties, ChangeProperty{ChangeRecordRef: o.ChangeRecordRef, Selector: s, Value: value, Origin: m.selectedOrigin(rebasePropertyOrigin(ours, o, s), resolution, o.ChangeRecordRef, m.draft)})
	}
	return nil
}

// Keep record/property comparison independent of the JSON representation of
// source evidence. Source proof wrappers never create merge conflicts.
func rebaseDecodeValue(v ChangeRebaseValue, out any) error { return json.Unmarshal(v.Value, out) }

func (m *changeRebaseMerge) selectedOrigin(old EffectiveOrigin, resolution *RebaseResolutionOrigin, ref ChangeRecordRef, base ChangeProposalRevision) EffectiveOrigin {
	if resolution != nil && m.resolutions[resolution.ResolutionID].Choice == "keep_proposal" && old.Kind == "intent" {
		return old
	}
	return rebaseDesiredOrigin(old, resolution, ref, base)
}

func (m *changeRebaseMerge) record(id string, base, ours, result *changeEvaluation) error {

	b, bok := base.records[id]
	o, ook := ours.records[id]
	n, nok := result.records[id]
	ref := b.ChangeRecordRef
	if !bok {
		ref = n.ChangeRecordRef
	}
	if ook {
		ref = o.ChangeRecordRef
	}
	bv, err := rebaseRecordValue(b, bok)
	if err != nil {
		return err
	}
	ov, err := rebaseRecordValue(o, ook)
	if err != nil {
		return err
	}
	nv, err := rebaseRecordValue(n, nok)
	if err != nil {
		return err
	}
	compatible := bok && ook && nok && b.RecordType == o.RecordType && o.RecordType == n.RecordType && b.Payload.Kind == o.Payload.Kind && o.Payload.Kind == n.Payload.Kind
	if compatible {
		if err = m.properties(b, o, n, ours, result); err != nil {
			return err
		}
		return nil
	}
	collision := rebaseRecordCollision(bok, ook, nok, b, o, n, m.oursIdentityEdits[id], m.nextIdentityEdits[id])
	selected, resolution, err := m.choose(ref, ChangeRebaseSelector{Kind: "record"}, bv, ov, nv, collision)
	if err != nil {
		return err
	}
	if sameRebaseValue(selected, nv) && resolution == nil {
		return nil
	}
	return m.retainRecord(id, ref, o, n, nok, selected, resolution, ours, result)
}

func (m *changeRebaseMerge) retainRecord(id string, ref ChangeRecordRef, o, n ChangeCreatedRecord, nok bool, selected ChangeRebaseValue, resolution *RebaseResolutionOrigin, ours, result *changeEvaluation) error {
	if selected.Presence == "absent_record" {
		origin := EffectiveOrigin{}
		for _, removed := range ours.revision.Delta.Removed {
			if removed.ID == id {
				origin = removed.Origin
			}
		}
		result.revision.Delta.Removed = append(result.revision.Delta.Removed, ChangeRemoval{ChangeRecordRef: ref, Origin: m.selectedOrigin(origin, resolution, ref, m.draft)})
		delete(result.records, id)
		return nil
	}
	// Whole-record keep retains O's typed payload. A deleted imported object is
	// carried with its qualified historical identity, never allocated as new intent.
	o.Origin = m.selectedOrigin(o.Origin, resolution, ref, m.draft)
	result.records[id] = o
	if nok {
		if o.RecordType != n.RecordType || o.Payload.Kind != n.Payload.Kind {
			return invalid("resolution", "A stable graph ID cannot change kind")
		}
		if err := m.residualProperties(o, n, ours, result, resolution); err != nil {
			return err
		}
	} else {
		result.revision.Delta.Created = append(result.revision.Delta.Created, o)
		for _, property := range ours.revision.Delta.Properties {
			if property.ChangeRecordRef == o.ChangeRecordRef {
				var err error
				property.Value, err = SelectSourceProperty(o.Payload, property.Selector)
				if err != nil {
					return err
				}
				result.revision.Delta.Properties = append(result.revision.Delta.Properties, property)
			}
		}
	}
	return nil
}

func rebaseRecordCollision(bok, ook, nok bool, b, o, n ChangeCreatedRecord, oursIdentityEdited, nextIdentityEdited bool) bool {
	if ook && nok {
		return o.RecordType != n.RecordType || o.Payload.Kind != n.Payload.Kind || !bok
	}
	if bok && ook && !nok {
		return oursIdentityEdited
	}
	return bok && !ook && nok && nextIdentityEdited
}

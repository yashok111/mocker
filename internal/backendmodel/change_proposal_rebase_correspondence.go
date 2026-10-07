package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strconv"
	"strings"
)

func (m *changeRebaseMerge) correspondence(base, ours, result *changeEvaluation) error {
	mapping, err := m.correspondenceMap(base, ours, result)
	if err != nil {
		return err
	}
	if len(mapping) == 0 {
		return nil
	}
	m.baseIdentities, err = rebaseCorrespondingIdentities(base, mapping)
	if err != nil {
		return err
	}
	m.oursIdentities, err = rebaseCorrespondingIdentities(ours, mapping)
	if err != nil {
		return err
	}
	if err = rebaseCorrespondenceRecords(base, ours, mapping); err != nil {
		return err
	}
	// Qualified historical targets remain exact. Current provider mapping is
	// reconstructed against N by identity merge, rather than changing old claims.
	for i := range ours.revision.Delta.EdgeNames {
		if id := mapping[ours.revision.Delta.EdgeNames[i].ID]; id != "" {
			ours.revision.Delta.EdgeNames[i].ID = id
		}
	}
	if err = rebaseCorrespondenceCriteria(ours, mapping); err != nil {
		return err
	}
	for i := range ours.revision.ArtifactContext.APIBindings {
		b := &ours.revision.ArtifactContext.APIBindings[i]
		if id := mapping[b.SourceNodeID]; id != "" {
			b.SourceNodeID = id
		}
	}
	for i := range ours.revision.ArtifactContext.EditorBindings {
		b := &ours.revision.ArtifactContext.EditorBindings[i]
		for j := range b.SourceNodeIDs {
			if id := mapping[b.SourceNodeIDs[j]]; id != "" {
				b.SourceNodeIDs[j] = id
			}
		}
	}
	return nil
}
func rebaseRemapPayload(p SourceAssertionPayload, mapping map[string]string) (SourceAssertionPayload, error) {
	refs, err := sourcePayloadReferenceSites(p)
	if err != nil {
		return p, err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return p, err
	}
	for _, ref := range refs {
		if id := mapping[ref.ID]; id != "" {
			raw, err = rebaseSetReference(raw, strings.Split(strings.TrimPrefix(ref.Path, "/"), "/"), id)
			if err != nil {
				return p, err
			}
		}
	}
	err = json.Unmarshal(raw, &p)
	return p, err
}
func rebaseSetReference(raw jsontext.Value, path []string, id string) (jsontext.Value, error) {
	if len(path) == 0 {
		return json.Marshal(id)
	}
	part := strings.ReplaceAll(strings.ReplaceAll(path[0], "~1", "/"), "~0", "~")
	if len(raw) > 0 && raw[0] == '[' {
		var values []jsontext.Value
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, err
		}
		index, err := strconv.Atoi(part)
		if err != nil || index < 0 || index >= len(values) {
			return nil, invalid("reference", "Invalid declared array reference")
		}
		values[index], err = rebaseSetReference(values[index], path[1:], id)
		if err != nil {
			return nil, err
		}
		return json.Marshal(values)
	}
	object, err := relationalObject(raw)
	if err != nil {
		return nil, err
	}
	child, ok := object[part]
	if !ok {
		return nil, invalid("reference", "Declared reference path is missing")
	}
	object[part], err = rebaseSetReference(child, path[1:], id)
	if err != nil {
		return nil, err
	}
	return json.Marshal(object)
}

// Comparison-only addresses follow explicit correspondence. The historical
// source snapshots and their qualified claims remain untouched; persistence
// retargets any retained desired key to N's exact assertion.
func rebaseCorrespondingIdentities(e *changeEvaluation, mapping map[string]string) (map[string]ChangeEvaluationIdentity, error) {
	identities, err := rebaseIdentityMap(e)
	if err != nil {
		return nil, err
	}
	out := map[string]ChangeEvaluationIdentity{}
	for _, identity := range identities {
		ref := changeIdentityRef(identity.Target)
		if next := mapping[ref.ID]; next != "" {
			if identity.Target.Source != nil {
				claim := *identity.Target.Source
				claim.ID = next
				identity.Target.Source = &claim
			} else {
				identity.Target.ID = next
				identity.Target.Intent = nil
			}
		}
		out[rebaseIdentityPartition(identity.Target)] = identity
	}
	return out, nil
}

func (m *changeRebaseMerge) correspondenceMap(base, ours, result *changeEvaluation) (map[string]string, error) {
	mapping := map[string]string{}
	targets := map[string]bool{}
	for _, c := range m.input.IdentityResolutions {
		old, ok := base.records[c.OldSourceID]
		next, exists := result.records[c.NewSourceID]
		if !ok || !exists || c.OldSourceID == c.NewSourceID || mapping[c.OldSourceID] != "" || targets[c.NewSourceID] || !validChangeReason(c.Reason) || old.RecordType != next.RecordType || old.Payload.Kind != next.Payload.Kind {
			return nil, invalid("identityResolutions", "Expected one-to-one same-kind source correspondence with reason")
		}
		if _, collision := base.records[c.NewSourceID]; collision {
			return nil, invalid("identityResolutions", "Target collides with an old source object")
		}
		if reserved, ok := ours.used[c.NewSourceID]; ok && changeAllocatedIntent(reserved) {
			return nil, invalid("identityResolutions", "Target collides with a permanently allocated intent ID")
		}
		if _, stillLive := result.records[c.OldSourceID]; stillLive {
			return nil, invalid("identityResolutions", "Old source ID still exists in selected new base")
		}
		mapping[c.OldSourceID] = c.NewSourceID
		targets[c.NewSourceID] = true
	}

	return mapping, nil
}

func rebaseCorrespondenceRecords(base, ours *changeEvaluation, mapping map[string]string) error {
	for _, e := range []*changeEvaluation{base, ours} {
		records := map[string]ChangeCreatedRecord{}
		for id, record := range e.records {
			if mapped := mapping[id]; mapped != "" {
				id = mapped
				record.ID = mapped
			}
			p, err := rebaseRemapPayload(record.Payload, mapping)
			if err != nil {
				return err
			}
			record.Payload = p
			records[id] = record
		}
		e.records = records
		for i := range e.revision.Delta.Properties {
			if id := mapping[e.revision.Delta.Properties[i].ID]; id != "" {
				e.revision.Delta.Properties[i].ID = id
			}
		}
		for i := range e.revision.Delta.Removed {
			if id := mapping[e.revision.Delta.Removed[i].ID]; id != "" {
				e.revision.Delta.Removed[i].ID = id
			}
		}
	}

	return nil
}

func rebaseCorrespondenceCriteria(ours *changeEvaluation, mapping map[string]string) error {
	for i := range ours.revision.Criteria {
		c := &ours.revision.Criteria[i]
		for _, field := range []*string{&c.ID, &c.From, &c.To} {
			if id := mapping[*field]; id != "" {
				*field = id
			}
		}
		for j := range c.TargetIDs {
			if id := mapping[c.TargetIDs[j]]; id != "" {
				c.TargetIDs[j] = id
			}
		}
		if c.Kind == "field_equals" {
			var s EffectivePropertySelector
			if err := json.Unmarshal(c.Selector, &s); err != nil {
				return err
			}
			if id := mapping[s.ID]; id != "" {
				s.ID = id
				s.SourceIdentity = nil
				s.IntentIdentity = nil
			}
			raw, err := json.Marshal(s)
			if err != nil {
				return err
			}
			c.Selector = raw
		}
	}

	return nil
}

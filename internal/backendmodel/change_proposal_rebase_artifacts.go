package backendmodel

import (
	"maps"
	"slices"
	"strings"
)

type changeRebaseArtifact struct {
	Pin    ArtifactPin          `json:"pin"`
	API    []APIArtifactBinding `json:"apiBindings"`
	Editor []EditorBinding      `json:"editorBindings"`
}

func rebaseArtifactMap(rev ChangeProposalRevision) map[ArtifactKey]changeRebaseArtifact {
	out := map[ArtifactKey]changeRebaseArtifact{}
	for _, pin := range rev.ArtifactPins {
		key := artifactKey(pin)
		u := changeRebaseArtifact{Pin: pin, API: []APIArtifactBinding{}, Editor: []EditorBinding{}}
		for _, b := range rev.ArtifactContext.APIBindings {
			if b.Ref.Kind == key.Kind && b.Ref.ArtifactID == key.ID {
				u.API = append(u.API, b)
			}
		}
		for _, b := range rev.ArtifactContext.EditorBindings {
			if b.ArtifactKind == key.Kind && b.ArtifactID == key.ID {
				u.Editor = append(u.Editor, b)
			}
		}
		out[key] = u
	}
	return out
}
func (m *changeRebaseMerge) artifacts(base, ours, result *changeEvaluation) error {
	// B uses source pins/context, not the proposal's desired artifact vector.
	bRev := base.revision
	bRev.ArtifactPins = base.source.State.Revision.ArtifactPins
	if ctx := revisionArtifactContext(&base.source.State); ctx != nil {
		bRev.ArtifactContext = *ctx
	} else {
		bRev.ArtifactContext = ArtifactContext{}
	}
	b, o, n := rebaseArtifactMap(bRev), rebaseArtifactMap(ours.revision), rebaseArtifactMap(result.revision)
	keys := map[ArtifactKey]bool{}
	for _, side := range []map[ArtifactKey]changeRebaseArtifact{b, o, n} {
		for key := range side {
			keys[key] = true
		}
	}
	ordered := slices.Collect(maps.Keys(keys))
	slices.SortFunc(ordered, func(a, b ArtifactKey) int { return strings.Compare(a.Kind+"\x00"+a.ID, b.Kind+"\x00"+b.ID) })
	result.revision.ArtifactPins = []ArtifactPin{}
	result.revision.ArtifactContext.APIBindings = []APIArtifactBinding{}
	result.revision.ArtifactContext.EditorBindings = []EditorBinding{}
	for _, key := range ordered {
		bi, bok := b[key]
		oi, ook := o[key]
		ni, nok := n[key]
		bv, err := rebaseValue(bok, bi)
		if err != nil {
			return err
		}
		ov, err := rebaseValue(ook, oi)
		if err != nil {
			return err
		}
		nv, err := rebaseValue(nok, ni)
		if err != nil {
			return err
		}
		v, resolution, err := m.choose(ChangeRecordRef{}, ChangeRebaseSelector{Kind: "artifact", Artifact: new(key)}, bv, ov, nv, false)
		if err != nil {
			return err
		}
		if v.Presence != "absent_property" {
			var selected changeRebaseArtifact
			if err = rebaseDecodeValue(v, &selected); err != nil {
				return err
			}
			result.revision.ArtifactPins = append(result.revision.ArtifactPins, selected.Pin)
			result.revision.ArtifactContext.APIBindings = append(result.revision.ArtifactContext.APIBindings, selected.API...)
			result.revision.ArtifactContext.EditorBindings = append(result.revision.ArtifactContext.EditorBindings, selected.Editor...)
		}
		if !sameRebaseValue(v, nv) {
			origin := EffectiveOrigin{}
			for _, intent := range ours.revision.Delta.ArtifactIntents {
				if intent.Artifact == key {
					origin = intent.Origin
				}
			}
			result.revision.Delta.ArtifactIntents = append(result.revision.Delta.ArtifactIntents, ChangeArtifactIntent{Artifact: key, Removed: v.Presence == "absent_property", Origin: m.selectedOrigin(origin, resolution, ChangeRecordRef{}, m.draft)})
		}
	}
	c := &result.revision.ArtifactContext
	if ArtifactContextUsesV1(result.revision.ArtifactPins, *c) {
		c.DocumentVersion = ""
	} else {
		c.DocumentVersion = EditorArtifactDocumentVersion
	}
	return ValidateArtifactVector(result.revision.ArtifactPins, c.APIBindings, c.EditorBindings)
}

// Final validation covers retained and source-selected bindings as well as
// repairs. Owner reads share the request cache and record exact digests for the
// short publication check; no owner resolution occurs under the writer lock.
func validateChangeRebaseArtifacts(e *changeEvaluation) error {
	if err := e.revision.ArtifactContext.Validate(e.revision.ArtifactPins); err != nil {
		return err
	}
	pins := map[ArtifactKey]ArtifactPin{}
	for _, pin := range e.revision.ArtifactPins {
		owner, err := e.artifactRequest.pinnedSnapshot(pin)
		if err != nil {
			return err
		}
		if !editorOwnerVerified(owner) {
			return invalid("artifacts", "Final artifact envelope is unverified")
		}
		pins[artifactKey(pin)] = pin
	}
	for _, binding := range e.revision.ArtifactContext.APIBindings {
		record, ok := e.records[binding.SourceNodeID]
		if !ok || record.RecordType != "node" || record.Payload.Kind != binding.SourceKind {
			return invalid("artifacts", "Final API binding source is missing or changed kind")
		}
		pin := pins[ArtifactKey{Kind: binding.Ref.Kind, ID: binding.Ref.ArtifactID}]
		object, err := e.artifactRequest.ResolveAPIObject(pin, binding.Ref.Selector)
		if err != nil {
			return err
		}
		if object.ObjectHash != binding.Ref.ObjectHash {
			return invalid("artifacts", "Final API binding object hash differs")
		}
	}
	for _, binding := range e.revision.ArtifactContext.EditorBindings {
		for _, id := range binding.SourceNodeIDs {
			if record, ok := e.records[id]; !ok || record.RecordType != "node" {
				return invalid("artifacts", "Final editor binding source is missing")
			}
		}
		object, err := e.artifactRequest.ResolveObject(pins[ArtifactKey{Kind: binding.ArtifactKind, ID: binding.ArtifactID}], binding.Selector)
		if err != nil {
			return err
		}
		if object.ObjectHash != binding.ObjectHash {
			return invalid("artifacts", "Final editor binding object hash differs")
		}
	}
	return nil
}

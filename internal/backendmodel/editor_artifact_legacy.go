package backendmodel

import (
	"context"
	"slices"
)

// The legacy DTO can show the shared API pin but cannot show editor-object edits.
func (s *APIArtifactService) prepareV2APIPins(ctx context.Context, pid string, in PreviewAPIPinsInput, state *RevisionState) (*preparedAPIPins, error) {
	full := *state.ArtifactContext
	out := &APIPinsPreview{BaseRevisionID: in.BaseRevisionID, ExpectedVersion: in.ExpectedVersion, Pins: slices.Clone(state.Revision.ArtifactPins), Bindings: slices.Clone(full.APIBindings), SourceSnapshotIDs: slices.Clone(state.Revision.SourceSnapshotIDs), Diagnostics: []APIArtifactDiagnostic{}, Diff: []APIArtifactDiff{}, SemanticHash: state.Revision.SemanticHash, CanApply: true}
	block := func(id string) {
		out.CanApply = false
		if !slices.ContainsFunc(out.Diagnostics, func(d APIArtifactDiagnostic) bool {
			return d.Code == "backend_api_editor_generic_required" && d.ArtifactID == id
		}) {
			out.Diagnostics = append(out.Diagnostics, APIArtifactDiagnostic{Code: "backend_api_editor_generic_required", ArtifactID: id, Message: "Selected editor associations require generic preview_backend_artifact_pins with both full binding sets"})
		}
	}
	commands := make([]ArtifactPinCommand, 0, len(in.Commands))
	for _, c := range in.Commands {
		key := ArtifactKey{"api_design", c.ArtifactID}
		_, editors, err := SelectedArtifactBindings(full, key)
		if err != nil {
			return nil, err
		}
		generic := ArtifactPinCommand{Artifact: key, Reason: c.Reason}
		if c.Type == "remove_api_pin" {
			generic.Type = "remove_artifact_pin"
			if len(editors) > 0 {
				block(c.ArtifactID)
			}
		} else {
			generic.Type = "set_artifact_pin"
			generic.RevisionID = c.RevisionID
			generic.APIBindings = c.Bindings
			generic.EditorBindings = []EditorBindingInput{}
			for _, b := range editors {
				generic.EditorBindings = append(generic.EditorBindings, EditorBindingInput{Selector: b.Selector, SourceNodeIDs: slices.Clone(b.SourceNodeIDs)})
			}
		}
		commands = append(commands, generic)
	}
	if !out.CanApply {
		out.CandidateHash, _ = APIArtifactCandidateHash(in, *out)
		return &preparedAPIPins{preview: out}, nil
	}
	prepared, err := NewArtifactService(s.repo, s.artifacts, nil).prepareArtifacts(ctx, pid, PreviewArtifactPinsInput{in.BaseRevisionID, in.ExpectedVersion, commands})
	if err != nil {
		return nil, err
	}
	if err := retainLegacyEditorBindings(full, in.Commands, prepared, block); err != nil {
		return nil, err
	}
	out.Pins = prepared.preview.Pins
	out.Bindings = prepared.preview.APIBindings
	out.SemanticHash = prepared.preview.SemanticHash
	out.CanApply = out.CanApply && prepared.preview.CanApply
	out.DiffTruncated = prepared.preview.DiffTruncated
	appendLegacyArtifactPreview(out, prepared.preview)
	// Re-admit the complete roster after restoring retained frozen editor metadata.
	if _, err = EncodeArtifactContext(prepared.frozen, out.Pins); err != nil {
		return nil, err
	}
	out.CandidateHash, err = APIArtifactCandidateHash(in, *out)
	if err != nil {
		return nil, err
	}
	return &preparedAPIPins{preview: out, frozen: *legacyArtifactContext(&prepared.frozen), baselineHash: prepared.baselineHash, fullContext: &prepared.frozen, artifactDigests: prepared.digests}, ctx.Err()
}

func retainLegacyEditorBindings(full ArtifactContext, commands []APIPinCommand, prepared *preparedArtifactPins, block func(string)) error {
	next := map[string]int{}
	for i, b := range prepared.frozen.EditorBindings {
		id, e := EditorBindingIdentity(b)
		if e != nil {
			return e
		}
		next[id] = i
	}
	for _, c := range commands {
		_, editors, e := SelectedArtifactBindings(full, ArtifactKey{"api_design", c.ArtifactID})
		if e != nil {
			return e
		}
		for _, old := range editors {
			id, e := EditorBindingIdentity(old)
			if e != nil {
				return e
			}
			i, ok := next[id]
			if !ok {
				block(c.ArtifactID)
				continue
			}
			current := prepared.frozen.EditorBindings[i]
			if old.Selector != current.Selector || old.ObjectHash != current.ObjectHash || !slices.Equal(old.SourceNodeIDs, current.SourceNodeIDs) {
				block(c.ArtifactID)
			}
			// Hash/selector/association are unchanged. Preserve all invisible frozen
			// labels/origin/reason exactly, including the original source-label alignment.
			prepared.frozen.EditorBindings[i] = old
		}
	}
	return nil
}

func appendLegacyArtifactPreview(out *APIPinsPreview, preview *ArtifactPinsPreview) {
	for _, d := range preview.Diagnostics {
		id := ""
		if d.Artifact != nil {
			id = d.Artifact.ID
		}
		source := ""
		if len(d.SourceNodeIDs) > 0 {
			source = d.SourceNodeIDs[0]
		}
		out.Diagnostics = append(out.Diagnostics, APIArtifactDiagnostic{Code: d.Code, ArtifactID: id, SourceNodeID: source, Pointer: d.Pointer, Message: d.Message})
	}
	for _, d := range preview.Diff {
		if d.Kind != "api" {
			continue
		}
		var before, after *ArtifactRef
		if d.Before != nil {
			before = d.Before.API
		}
		if d.After != nil {
			after = d.After.API
		}
		out.Diff = append(out.Diff, APIArtifactDiff{SourceNodeID: d.SourceNodeID, Status: d.Status, Before: before, After: after, ContextChanged: d.ContextChanged, Changes: d.Changes})
	}
}

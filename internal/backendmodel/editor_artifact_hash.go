package backendmodel

import (
	"slices"
	"strings"
)

type editorSemanticBinding struct {
	ArtifactKind  string         `json:"artifactKind"`
	ArtifactID    string         `json:"artifactId"`
	Selector      EditorSelector `json:"selector"`
	ObjectHash    string         `json:"objectHash"`
	SourceNodeIDs []string       `json:"sourceNodeIds"`
}

// ArtifactSemanticHash hashes current authored associations and the full owner
// vector from the preserved source anchor, never a previous pin-envelope hash.
func ArtifactSemanticHash(sourceContentHash, sourceSemanticHash string, pins []ArtifactPin, api []APIArtifactBinding, editor []EditorBinding) (string, error) {
	if !validHash(sourceContentHash) || !validHash(sourceSemanticHash) {
		return "", invalid("sourceHash", "Expected lowercase SHA-256 anchors")
	}
	if err := ValidateArtifactVector(pins, api, editor); err != nil {
		return "", err
	}
	c := ArtifactContext{SourceContentHash: sourceContentHash, SourceSemanticHash: sourceSemanticHash, APIBindings: api, EditorBindings: editor}
	if ArtifactContextUsesV1(pins, c) {
		return APIArtifactSemanticHash(sourceContentHash, sourceSemanticHash, pins, api)
	}
	apiSemantics := make([]apiSemanticBinding, 0, len(api))
	for _, b := range api {
		r := b.Ref
		apiSemantics = append(apiSemantics, apiSemanticBinding{b.SourceNodeID, b.SourceKind, apiSemanticRef{r.Kind, r.ArtifactID, r.RevisionID, r.ContentHash, r.Selector, r.ObjectHash, r.ResolvedPointer}})
	}
	slices.SortFunc(apiSemantics, func(a, b apiSemanticBinding) int { return strings.Compare(a.SourceNodeID, b.SourceNodeID) })
	canonical, err := CanonicalEditorBindings(editor)
	if err != nil {
		return "", err
	}
	editorSemantics := make([]editorSemanticBinding, 0, len(canonical))
	for _, b := range canonical {
		editorSemantics = append(editorSemantics, editorSemanticBinding{b.ArtifactKind, b.ArtifactID, b.Selector, b.ObjectHash, b.SourceNodeIDs})
	}
	return hashAPIJSON(struct {
		Domain            string                  `json:"domain"`
		SourceContentHash string                  `json:"sourceContentHash"`
		Pins              []ArtifactPin           `json:"pins"`
		APIBindings       []apiSemanticBinding    `json:"apiBindings"`
		EditorBindings    []editorSemanticBinding `json:"editorBindings"`
	}{"backend-editor-artifacts-semantic-v1", sourceContentHash, canonicalAPIPins(pins), apiSemantics, editorSemantics})
}

// ArtifactCandidateHash binds original command input (including order/reason)
// separately from canonical semantics; exact retries use the original body/key.
func ArtifactCandidateHash(input PreviewArtifactPinsInput, candidate ArtifactPinsPreview) (string, error) {
	if err := input.Validate(); err != nil {
		return "", err
	}
	candidate.CandidateHash = ""
	candidate.Pins = canonicalAPIPins(candidate.Pins)
	candidate.APIBindings = slices.Clone(candidate.APIBindings)
	slices.SortFunc(candidate.APIBindings, func(a, b APIArtifactBinding) int { return strings.Compare(a.SourceNodeID, b.SourceNodeID) })
	var err error
	candidate.EditorBindings, err = CanonicalEditorBindings(candidate.EditorBindings)
	if err != nil {
		return "", err
	}
	candidate.SourceSnapshotIDs = sortedAPIStrings(candidate.SourceSnapshotIDs)
	return hashAPIJSON(struct {
		Domain    string                   `json:"domain"`
		Input     PreviewArtifactPinsInput `json:"input"`
		Candidate ArtifactPinsPreview      `json:"candidate"`
	}{"backend-editor-artifacts-candidate-v1", input, candidate})
}
func ArtifactGroupIdentity(pin ArtifactPin) (string, error) {
	return stringIdentity(struct {
		Kind     string      `json:"kind"`
		Artifact ArtifactKey `json:"artifact"`
	}{"artifact_group", ArtifactKey{pin.Kind, pin.ID}})
}
func EditorArtifactComparisonIdentity(binding EditorBinding) (string, error) {
	identity, err := EditorBindingIdentity(binding)
	if err != nil {
		return "", err
	}
	return stringIdentity(struct {
		Kind     string `json:"kind"`
		Identity string `json:"identity"`
	}{"editor_artifact", identity})
}
func stringIdentity(v any) (string, error) { raw, err := canonicalAPIJSON(v); return string(raw), err }

package backendmodel

import (
	"encoding/json/v2"
	"errors"
	"slices"
	"strings"
)

// MaxEditorArtifactContextBytes limits only NEW serialized tagged v2 context
// documents. It is independent of owner EventMap and complete response bounds.
const MaxEditorArtifactContextBytes = 4 << 20

func EditorArtifactContextLimit(actualLowerBoundBytes int64) *FaultError {
	return &FaultError{Status: 413, Code: "backend_artifact_context_limit", Message: "Frozen editor artifact context exceeds 4 MiB", Details: map[string]any{"allowedBytes": MaxEditorArtifactContextBytes, "actualLowerBoundBytes": actualLowerBoundBytes}}
}

// editorArtifactCountingWriter retains no output. The streaming JSON encoder
// flushes bounded tokens; a rejected write immediately stops further traversal.
type editorArtifactCountingWriter struct {
	count  int64
	writes int
}

func (w *editorArtifactCountingWriter) Write(p []byte) (int, error) {
	w.writes++
	next := w.count + int64(len(p))
	if next > MaxEditorArtifactContextBytes {
		return 0, EditorArtifactContextLimit(next)
	}
	w.count = next
	return len(p), nil
}

// CheckEditorArtifactContextSize measures actual compact UTF-8 JSON bytes with
// escapes, stopping at the first over-limit flush. Callers validate/canonicalize
// the complete vector first. A tagged document is required; v1 is never capped.
func CheckEditorArtifactContextSize(c ArtifactContext) (int64, error) {
	if c.DocumentVersion != EditorArtifactDocumentVersion {
		return 0, invalid("documentVersion", "Size admission requires tagged editor context")
	}
	w := editorArtifactCountingWriter{}
	if err := json.MarshalWrite(&w, &c); err != nil {
		var fault *FaultError
		if errors.As(err, &fault) {
			return 0, fault
		}
		return 0, err
	}
	return w.count, nil
}

// ArtifactContext is the in-memory union. Empty DocumentVersion denotes the
// unchanged untagged B24 branch; only the fixed literal denotes tagged v2.
type ArtifactContext struct {
	DocumentVersion    string               `json:"documentVersion"`
	SourceContentHash  string               `json:"sourceContentHash"`
	SourceSemanticHash string               `json:"sourceSemanticHash"`
	APIBindings        []APIArtifactBinding `json:"apiBindings"`
	EditorBindings     []EditorBinding      `json:"editorBindings"`
}

func ValidateArtifactVector(pins []ArtifactPin, api []APIArtifactBinding, editor []EditorBinding) error {
	if len(pins) > MaxAPIArtifactPins || len(api)+len(editor) > MaxAPIArtifactBindings {
		return invalid("pins", "Full artifact vector exceeds limits")
	}
	owners := map[ArtifactKey]ArtifactPin{}
	for _, p := range pins {
		key := ArtifactKey{p.Kind, p.ID}
		// Legacy opaque groups are carried as-is. Only the two declared owner kinds
		// can be created/resolved by generic commands; count every retained group.
		if p.Kind != "api_design" && p.Kind != "design_scenario" {
			continue
		}
		if _, ok := owners[key]; ok {
			return invalid("pins", "Duplicate declared owner pin")
		}
		owners[key] = p
		if err := key.Validate(); err != nil {
			return err
		}
		if !ValidAPIArtifactID(p.RevisionID) || !validHash(p.ContentHash) {
			return invalid("pins", "Invalid exact revision/hash")
		}
	}
	return validateArtifactBindings(owners, api, editor)
}

func validateArtifactBindings(owners map[ArtifactKey]ArtifactPin, api []APIArtifactBinding, editor []EditorBinding) error {
	sources := map[string]bool{}
	for _, b := range api {
		if err := b.Validate(); err != nil {
			return err
		}
		p, ok := owners[ArtifactKey{b.Ref.Kind, b.Ref.ArtifactID}]
		if !ok || p.RevisionID != b.Ref.RevisionID || p.ContentHash != b.Ref.ContentHash {
			return invalid("apiBinding", "Binding differs from exact pin")
		}
		if sources[b.SourceNodeID] {
			return invalid("apiBinding", "Duplicate API source binding")
		}
		sources[b.SourceNodeID] = true
	}
	objects := map[string]bool{}
	for _, b := range editor {
		if err := b.Validate(); err != nil {
			return err
		}
		if _, ok := owners[ArtifactKey{b.ArtifactKind, b.ArtifactID}]; !ok {
			return invalid("editorBinding", "Missing exact owner pin")
		}
		identity, err := EditorBindingIdentity(b)
		if err != nil {
			return err
		}
		if objects[identity] {
			return invalid("editorBinding", "Duplicate editor object binding")
		}
		objects[identity] = true
	}
	return nil
}
func (c ArtifactContext) Validate(pins []ArtifactPin) error {
	if c.DocumentVersion != "" && c.DocumentVersion != EditorArtifactDocumentVersion {
		return invalid("documentVersion", "Unknown artifact document version")
	}
	if !validHash(c.SourceContentHash) || !validHash(c.SourceSemanticHash) {
		return invalid("context", "Invalid frozen source anchors")
	}
	if c.DocumentVersion == "" {
		if len(c.EditorBindings) > 0 {
			return invalid("context", "Legacy branch cannot carry editor bindings")
		}
		for _, p := range pins {
			if p.Kind == "design_scenario" {
				return invalid("context", "Scenario pin requires tagged branch")
			}
		}
		return ValidateAPIArtifactVector(pins, c.APIBindings)
	}
	return ValidateArtifactVector(pins, c.APIBindings, c.EditorBindings)
}
func (c *ArtifactContext) UnmarshalJSON(raw []byte) error {
	*c = ArtifactContext{}
	type plain ArtifactContext
	if err := strictAPIObject(raw, []string{"documentVersion", "sourceContentHash", "sourceSemanticHash", "apiBindings", "editorBindings"}, nil, (*plain)(c)); err != nil {
		return err
	}
	if c.DocumentVersion != EditorArtifactDocumentVersion {
		return invalid("documentVersion", "Unknown artifact document version")
	}
	if !validHash(c.SourceContentHash) || !validHash(c.SourceSemanticHash) {
		return invalid("context", "Invalid frozen source anchors")
	}
	return nil
}

// DecodeArtifactContext dispatches on presence before typed decoding. Its legacy
// branch deliberately uses the exact ordinary B24 APIArtifactContext decoder.
// Nothing in this pure helper changes persistent loaders or rewrites old rows.
func DecodeArtifactContext(raw []byte, pins []ArtifactPin) (*ArtifactContext, error) {
	m, err := relationalObject(raw)
	if err != nil {
		return nil, invalid("context", "Expected artifact context object")
	}
	var out ArtifactContext
	if m["documentVersion"] != nil {
		if err = json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
	} else {
		var old APIArtifactContext
		if err = json.Unmarshal(raw, &old); err != nil {
			return nil, err
		}
		out = ArtifactContext{SourceContentHash: old.SourceContentHash, SourceSemanticHash: old.SourceSemanticHash, APIBindings: old.Bindings, EditorBindings: []EditorBinding{}}
	}
	if err = out.Validate(pins); err != nil {
		return nil, err
	}
	return &out, nil
}

// ArtifactContextUsesV1 considers the ENTIRE remaining state, including scenario
// groups that the legacy validator intentionally treats as opaque non-API pins.
func ArtifactContextUsesV1(pins []ArtifactPin, c ArtifactContext) bool {
	if len(c.EditorBindings) != 0 {
		return false
	}
	for _, p := range pins {
		if p.Kind == "design_scenario" {
			return false
		}
	}
	return ValidateAPIArtifactVector(pins, c.APIBindings) == nil
}
func CanonicalEditorBindings(bindings []EditorBinding) ([]EditorBinding, error) {
	type keyedBinding struct {
		binding  EditorBinding
		identity string
	}
	keyed := make([]keyedBinding, len(bindings))
	for i, b := range bindings {
		canonical, err := CanonicalEditorBinding(b)
		if err != nil {
			return nil, err
		}
		identity, err := EditorBindingIdentity(canonical)
		if err != nil {
			return nil, err
		}
		keyed[i] = keyedBinding{canonical, identity}
	}
	slices.SortFunc(keyed, func(a, b keyedBinding) int { return strings.Compare(a.identity, b.identity) })
	out := make([]EditorBinding, len(keyed))
	for i, k := range keyed {
		out[i] = k.binding
	}
	return out, nil
}

// EncodeArtifactContext chooses the representable branch from current state,
// never from its history. The B24 marshaler/hash/admission remain unchanged.
func EncodeArtifactContext(c ArtifactContext, pins []ArtifactPin) ([]byte, error) {
	if c.DocumentVersion != "" && c.DocumentVersion != EditorArtifactDocumentVersion {
		return nil, invalid("documentVersion", "Unknown artifact document version")
	}
	if !validHash(c.SourceContentHash) || !validHash(c.SourceSemanticHash) {
		return nil, invalid("context", "Invalid frozen source anchors")
	}
	if err := ValidateArtifactVector(pins, c.APIBindings, c.EditorBindings); err != nil {
		return nil, err
	}
	api := slices.Clone(c.APIBindings)
	if api == nil {
		api = []APIArtifactBinding{}
	}
	slices.SortFunc(api, func(a, b APIArtifactBinding) int { return strings.Compare(a.SourceNodeID, b.SourceNodeID) })
	if ArtifactContextUsesV1(pins, c) {
		return json.Marshal(APIArtifactContext{c.SourceContentHash, c.SourceSemanticHash, api})
	}
	editor, err := CanonicalEditorBindings(c.EditorBindings)
	if err != nil {
		return nil, err
	}
	c.DocumentVersion = EditorArtifactDocumentVersion
	c.APIBindings = api
	c.EditorBindings = editor
	// Count before allocating an output buffer. Only admitted <=4MiB documents
	// enter ordinary marshaling; over-limit state is never fully serialized.
	if _, err = CheckEditorArtifactContextSize(c); err != nil {
		return nil, err
	}
	return json.Marshal(c)
}

// SelectedArtifactBindings returns the complete immutable group independently
// of item pagination and live owner resolution, including broken/orphan objects.
func SelectedArtifactBindings(c ArtifactContext, key ArtifactKey) ([]APIArtifactBinding, []EditorBinding, error) {
	if err := key.Validate(); err != nil {
		return nil, nil, err
	}
	api := []APIArtifactBinding{}
	editor := []EditorBinding{}
	for _, b := range c.APIBindings {
		if b.Ref.Kind == key.Kind && b.Ref.ArtifactID == key.ID {
			api = append(api, b)
		}
	}
	for _, b := range c.EditorBindings {
		if b.ArtifactKind == key.Kind && b.ArtifactID == key.ID {
			editor = append(editor, b)
		}
	}
	slices.SortFunc(api, func(a, b APIArtifactBinding) int { return strings.Compare(a.SourceNodeID, b.SourceNodeID) })
	editor, err := CanonicalEditorBindings(editor)
	return api, editor, err
}

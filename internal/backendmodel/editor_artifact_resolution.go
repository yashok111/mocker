package backendmodel

import (
	"crypto/sha256"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/responserules"
	"github.com/yashok111/mocker/internal/statediagram"
)

type editorRawTree struct {
	raw  string
	root map[string]any
}

func decodeEditorRaw(raw []byte, out any) error { return jsonx.Unmarshal(raw, out) }
func newEditorRawTree(raw string) (*editorRawTree, error) {
	dec := jsonx.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return nil, err
	}
	if root == nil {
		return nil, invalid("document", "Expected authored document object")
	}
	return &editorRawTree{raw: raw, root: root}, nil
}
func editorObject(v any) map[string]any { m, _ := v.(map[string]any); return m }
func editorArray(v any) []any           { a, _ := v.([]any); return a }
func editorString(v any) string         { s, _ := v.(string); return s }
func editorUniqueID(a []any, id string) (any, int, error) {
	var selected any
	index := -1
	for i, v := range a {
		if editorString(editorObject(v)["id"]) == id {
			if index >= 0 {
				return nil, -1, invalid("selector", "Ambiguous authored owner ID")
			}
			selected, index = v, i
		}
	}
	if index < 0 {
		return nil, -1, invalid("selector", "Exact authored object is missing")
	}
	return selected, index, nil
}
func editorPointerPart(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}
func editorAt(root any, pointer string) (any, error) {
	if pointer == "" {
		return root, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, invalid("pointer", "Invalid owner pointer")
	}
	v := root
	for _, p := range strings.Split(pointer[1:], "/") {
		p = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
		switch n := v.(type) {
		case map[string]any:
			var ok bool
			v, ok = n[p]
			if !ok {
				return nil, invalid("pointer", "Authored value missing")
			}
		case []any:
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(n) {
				return nil, invalid("pointer", "Authored index missing")
			}
			v = n[i]
		default:
			return nil, invalid("pointer", "Owner pointer traverses scalar")
		}
	}
	return v, nil
}

// editorAuthoredHash keeps owner number spellings and exact integer values. RFC
// number canonicalization would collapse unsafe adjacent authored integers.
func editorAuthoredHash(v any) (string, error) {
	raw, err := jsonx.Marshal(struct {
		Domain string `json:"domain"`
		Object any    `json:"object"`
	}{"backend-editor-object-v1", v})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

// editorExactRaw scans offsets without decoding/copying each ancestor subtree.
// Validation and selection each consume at most the document once. Decoder state
// grows with JSON depth; its reusable buffer is bounded by the input, not depth
// times input. The returned slice retains exact number spellings and whitespace.
func editorExactRaw(raw, pointer string) (string, error) {
	if pointer == "" {
		return raw, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return "", invalid("pointer", "Invalid owner pointer")
	}
	dec := jsontext.NewDecoder(strings.NewReader(raw))
	if err := dec.SkipValue(); err != nil {
		return "", err
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		if err != nil {
			return "", err
		}
		return "", invalid("document", "Trailing JSON value")
	}
	dec.Reset(strings.NewReader(raw))
	for part := range strings.SplitSeq(pointer[1:], "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		if err := editorSeekRawPart(dec, part); err != nil {
			return "", err
		}
	}
	value, err := dec.ReadValue()
	if err != nil {
		return "", err
	}
	end := dec.InputOffset()
	return raw[end-int64(len(value)) : end], nil
}

type ResolvedEditorArtifactObject struct {
	Selector     EditorSelector
	Locator      ArtifactProjectionLocator
	Label        string
	ObjectHash   string
	DocumentJSON string
	Data         ArtifactProjectionData
	Diagnostics  []ArtifactDiagnostic
}

func editorView(kind string) string {
	switch kind {
	case "participant", "sequence_message":
		return "sequence"
	case "event_operation", "event_channel", "event_message":
		return "event_model"
	case "state_diagram", "state_transition":
		return "states"
	default:
		return "response_rules"
	}
}
func (r *EditorArtifactRequest) embedded(s *editorOwnerSnapshot, id string) (*editorRawTree, string, *ArtifactEmbeddedContract, []ArtifactDiagnostic, error) {
	value, index, err := editorUniqueID(editorArray(s.tree.root["contracts"]), id)
	if err != nil {
		return nil, "", nil, nil, err
	}
	c := editorObject(value)
	raw, err := editorExactRaw(s.tree.raw, fmt.Sprintf("/contracts/%d/document", index))
	if err != nil {
		return nil, "", nil, nil, err
	}
	tree, err := newEditorRawTree(raw)
	if err != nil {
		return nil, "", nil, nil, err
	}
	mode := editorString(c["mode"])
	if mode == "" {
		mode = "copy"
	}
	info := &ArtifactEmbeddedContract{ContractID: id, Mode: mode, DocumentHash: fmt.Sprintf("%x", sha256.Sum256([]byte(raw))), OriginStatus: "copy"}
	var source designscenario.ContractSource
	if c["source"] != nil {
		encoded, _ := jsonx.Marshal(c["source"])
		if err := decodeEditorRaw(encoded, &source); err != nil {
			return nil, "", nil, nil, err
		}
		info.Origin = &ArtifactContractSource{strconv.FormatInt(source.DesignID, 10), strconv.FormatInt(source.RevisionID, 10), strconv.FormatInt(source.Version, 10)}
	}
	diagnostics := []ArtifactDiagnostic{}
	if !editorOwnerVerified(s) {
		info.OriginStatus = "unverified"
		diagnostics = append(diagnostics, editorProjectionDiagnostic("scenario_envelope_unverified", "Saved scenario envelope cannot be verified; nested origin verification is unavailable"))
		return tree, fmt.Sprintf("/contracts/%d/document", index), info, diagnostics, nil
	}
	if mode == "linked" {
		info.OriginStatus = "unavailable"
		if info.Origin == nil {
			diagnostics = append(diagnostics, ArtifactDiagnostic{Code: "linked_origin_missing", Severity: "warning", Message: "Saved linked contract has no exact origin"})
		} else {
			origin, readErr := r.snapshot(ArtifactKey{"api_design", info.Origin.DesignID}, info.Origin.RevisionID)
			if r.ctx.Err() != nil {
				return nil, "", nil, nil, r.ctx.Err()
			}
			if readErr != nil {
				code := "linked_origin_unavailable"
				if f, ok := errors.AsType[*FaultError](readErr); ok && f.Status == 413 {
					code = "linked_origin_budget"
					info.OriginStatus = "unverified"
				}
				diagnostics = append(diagnostics, ArtifactDiagnostic{Code: code, Severity: "warning", Message: "Exact linked origin could not be verified"})
			} else {
				info.APIContentHash = origin.api.ContentHash
				if origin.api.Version != source.Version {
					info.OriginStatus = "version_mismatch"
				} else {
					same, eqErr := designscenario.SameContractDocument([]byte(raw), []byte(origin.api.IdentityDocument))
					if eqErr != nil {
						info.OriginStatus = "unavailable"
					} else if !same {
						info.OriginStatus = "content_mismatch"
					} else {
						info.OriginStatus = "verified"
					}
				}
				if info.OriginStatus != "verified" {
					diagnostics = append(diagnostics, ArtifactDiagnostic{Code: "linked_origin_" + info.OriginStatus, Severity: "warning", Message: "Saved linked contract differs from its exact origin identity or version"})
				}
			}
		}
	} else if mode != "copy" {
		info.OriginStatus = "unsupported"
		diagnostics = append(diagnostics, ArtifactDiagnostic{Code: "embedded_mode_unsupported", Severity: "warning", Message: "Unsupported saved embedded contract mode"})
	}
	return tree, fmt.Sprintf("/contracts/%d/document", index), info, diagnostics, nil
}
func (r *EditorArtifactRequest) ResolveObject(pin ArtifactPin, selector EditorSelector) (*ResolvedEditorArtifactObject, error) {
	if err := selector.ValidateForArtifact(pin.Kind); err != nil {
		return nil, err
	}
	s, err := r.pinnedSnapshot(pin)
	if err != nil {
		return nil, err
	}
	if !editorOwnerVerified(s) {
		return nil, invalid("artifact", "Unverified scenario envelope cannot resolve a bindable object")
	}
	tree := s.tree
	prefix := ""
	var embedded *ArtifactEmbeddedContract
	diagnostics := []ArtifactDiagnostic{}
	if selector.EmbeddedContractID != "" {
		tree, prefix, embedded, diagnostics, err = r.embedded(s, selector.EmbeddedContractID)
		if err != nil {
			return nil, err
		}
	}
	pointer, label, owner, value, err := resolveEditorValue(tree, selector)
	if err != nil {
		return nil, err
	}
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	hash, err := editorAuthoredHash(value)
	if err != nil {
		return nil, err
	}
	raw, err := editorExactRaw(tree.raw, pointer)
	if err != nil {
		return nil, err
	}
	data, err := editorTypedData(selector.Kind, raw)
	if err != nil {
		return nil, err
	}
	owner.Pointer = prefix + pointer
	out := &ResolvedEditorArtifactObject{selector, ArtifactProjectionLocator{pin, editorView(selector.Kind), owner, embedded}, label, hash, raw, data, diagnostics}
	return out, r.ctx.Err()
}
func resolveEditorValue(tree *editorRawTree, s EditorSelector) (string, string, ArtifactOwnerAddress, any, error) {
	root := tree.root
	owner := ArtifactOwnerAddress{}
	var v any
	var i int
	var err error
	pointer := ""
	label := ""
	switch s.Kind {
	case "participant", "sequence_message":
		field, id := "participants", s.ParticipantID
		if s.Kind == "sequence_message" {
			field, id = "messages", s.MessageID
		}
		v, i, err = editorUniqueID(editorArray(root[field]), id)
		pointer = fmt.Sprintf("/%s/%d", field, i)
		if s.Kind == "participant" {
			owner.ParticipantID = id
			label = editorString(editorObject(v)["name"])
		} else {
			owner.MessageID = id
			label = editorString(editorObject(v)["label"])
		}
	case "event_channel", "event_message", "event_operation":
		m := editorObject(root["eventModel"])
		field, id := "channels", s.ChannelID
		if s.Kind == "event_message" {
			field, id = "messages", s.MessageID
		}
		if s.Kind == "event_operation" {
			field, id = "contracts", s.ContractID
		}
		v, i, err = editorUniqueID(editorArray(m[field]), id)
		pointer = fmt.Sprintf("/eventModel/%s/%d", field, i)
		if s.Kind == "event_operation" && err == nil {
			var j int
			v, j, err = editorUniqueID(editorArray(editorObject(v)["operations"]), s.OperationID)
			pointer += fmt.Sprintf("/operations/%d", j)
			owner.ContractID = s.ContractID
			owner.OperationID = s.OperationID
		} else if s.Kind == "event_channel" {
			owner.ChannelID = id
		} else {
			owner.MessageID = id
		}
		label = editorString(editorObject(v)["name"])
	case "state_diagram", "state_transition":
		if _, decodeErr := statediagram.Decode(root); decodeErr != nil {
			return "", "", owner, nil, invalid("states", decodeErr.Error())
		}
		v, i, err = editorUniqueID(editorArray(editorObject(root[statediagram.Extension])["diagrams"]), s.DiagramID)
		pointer = fmt.Sprintf("/%s/diagrams/%d", statediagram.Extension, i)
		owner.DiagramID = s.DiagramID
		if s.Kind == "state_transition" && err == nil {
			var j int
			v, j, err = editorUniqueID(editorArray(editorObject(v)["transitions"]), s.TransitionID)
			pointer += fmt.Sprintf("/transitions/%d", j)
			owner.TransitionID = s.TransitionID
		}
		label = editorString(editorObject(v)["name"])
	case "response_rule", "response_node":
		if _, decodeErr := responserules.Decode(root); decodeErr != nil {
			return "", "", owner, nil, invalid("responseRules", decodeErr.Error())
		}
		v, i, err = editorUniqueID(editorArray(editorObject(root[responserules.Extension])["rules"]), s.RuleID)
		pointer = fmt.Sprintf("/%s/rules/%d", responserules.Extension, i)
		owner.RuleID = s.RuleID
		if s.Kind == "response_node" && err == nil {
			var j int
			v, j, err = editorUniqueID(editorArray(editorObject(v)["nodes"]), s.NodeID)
			pointer += fmt.Sprintf("/nodes/%d", j)
			owner.NodeID = s.NodeID
		}
		label = editorString(editorObject(v)["name"])
	default:
		err = invalid("selector", "Unsupported selector")
	}
	if label == "" {
		label = editorString(editorObject(v)["id"])
	}
	return pointer, label, owner, v, err
}
func editorTypedData(kind, raw string) (ArtifactProjectionData, error) {
	d := ArtifactProjectionData{Kind: kind}
	var target any
	switch kind {
	case "participant":
		d.Participant = &designscenario.Participant{}
		target = d.Participant
	case "sequence_message":
		d.SequenceMessage = &designscenario.Message{}
		target = d.SequenceMessage
	case "fragment":
		d.Fragment = &designscenario.Fragment{}
		target = d.Fragment
	case "state_diagram":
		d.StateDiagram = &statediagram.Diagram{}
		target = d.StateDiagram
	case "state":
		d.State = &statediagram.State{}
		target = d.State
	case "state_transition":
		d.StateTransition = &statediagram.Transition{}
		target = d.StateTransition
	case "response_rule":
		d.ResponseRule = &responserules.Rule{}
		target = d.ResponseRule
	case "response_node":
		d.ResponseNode = &responserules.Node{}
		target = d.ResponseNode
	case "response_edge":
		d.ResponseEdge = &responserules.Edge{}
		target = d.ResponseEdge
	case "event_channel":
		d.EventChannel = &designscenario.EventChannel{}
		target = d.EventChannel
	case "event_server":
		d.EventServer = &designscenario.EventServer{}
		target = d.EventServer
	case "event_schema":
		d.EventSchema = &designscenario.EventSchema{}
		target = d.EventSchema
	case "event_message":
		d.EventMessage = &designscenario.EventMessage{}
		target = d.EventMessage
	case "event_operation":
		d.EventOperation = &designscenario.EventOperation{}
		target = d.EventOperation
	default:
		return d, invalid("kind", "Unsupported typed projection")
	}
	// Owner decoders remain responsible for their strict authored domains.
	if err := json.Unmarshal([]byte(raw), target, json.RejectUnknownMembers(true)); err != nil {
		return d, err
	}
	return d, nil
}
func editorClone[T any](v T) (T, error) {
	var out T
	raw, err := json.Marshal(v)
	if err == nil {
		err = json.Unmarshal(raw, &out)
	}
	return out, err
}

func editorSeekRawPart(dec *jsontext.Decoder, part string) error {
	switch dec.PeekKind() {
	case '{':
		if _, err := dec.ReadToken(); err != nil {
			return err
		}
		found := false
		for dec.PeekKind() != '}' {
			key, err := dec.ReadToken()
			if err != nil {
				return err
			}
			if key.String() == part {
				found = true
				break
			}
			if err := dec.SkipValue(); err != nil {
				return err
			}
		}
		if !found {
			return invalid("pointer", "Missing raw object member")
		}
	case '[':
		index, err := strconv.Atoi(part)
		if err != nil || index < 0 {
			return invalid("pointer", "Missing raw index")
		}
		if _, err := dec.ReadToken(); err != nil {
			return err
		}
		for i := 0; i < index; i++ {
			if dec.PeekKind() == ']' {
				return invalid("pointer", "Missing raw index")
			}
			if err := dec.SkipValue(); err != nil {
				return err
			}
		}
		if dec.PeekKind() == ']' {
			return invalid("pointer", "Missing raw index")
		}
	default:
		return invalid("pointer", "Owner pointer traverses scalar")
	}
	return nil
}

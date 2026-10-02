package backendmodel

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/responserules"
	"github.com/yashok111/mocker/internal/statediagram"
)

const (
	EditorArtifactDocumentVersion = "backend-editor-artifacts-v1"
	MaxEditorBindingSources       = 100
)

type ArtifactKey struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func (k ArtifactKey) Validate() error {
	if (k.Kind != "api_design" && k.Kind != "design_scenario") || !ValidAPIArtifactID(k.ID) {
		return invalid("artifact", "Expected declared owner kind and exact positive decimal int64 ID")
	}
	return nil
}
func (k *ArtifactKey) UnmarshalJSON(raw []byte) error {
	*k = ArtifactKey{}
	type plain ArtifactKey
	if err := strictAPIObject(raw, []string{"kind", "id"}, nil, (*plain)(k)); err != nil {
		return err
	}
	return k.Validate()
}

// EditorSelector is the exact nine-variant binding union. Projection locators
// separately describe auxiliary owner rows that cannot accept a binding.
type EditorSelector struct {
	Kind               string `json:"kind"`
	MessageID          string `json:"messageId,omitempty"`
	ParticipantID      string `json:"participantId,omitempty"`
	ContractID         string `json:"contractId,omitempty"`
	OperationID        string `json:"operationId,omitempty"`
	ChannelID          string `json:"channelId,omitempty"`
	DiagramID          string `json:"diagramId,omitempty"`
	TransitionID       string `json:"transitionId,omitempty"`
	RuleID             string `json:"ruleId,omitempty"`
	NodeID             string `json:"nodeId,omitempty"`
	EmbeddedContractID string `json:"embeddedContractId,omitempty"`
}

func editorSelectorFields(kind string) (required, optional []string) {
	required = []string{"kind"}
	switch kind {
	case "sequence_message", "event_message":
		required = append(required, "messageId")
	case "participant":
		required = append(required, "participantId")
	case "event_operation":
		required = append(required, "contractId", "operationId")
	case "event_channel":
		required = append(required, "channelId")
	case "state_diagram":
		required = append(required, "diagramId")
		optional = []string{"embeddedContractId"}
	case "state_transition":
		required = append(required, "diagramId", "transitionId")
		optional = []string{"embeddedContractId"}
	case "response_rule":
		required = append(required, "ruleId")
		optional = []string{"embeddedContractId"}
	case "response_node":
		required = append(required, "ruleId", "nodeId")
		optional = []string{"embeddedContractId"}
	default:
		return nil, nil
	}
	return
}
func scenarioOwnerID(id string) bool {
	return id != "" && utf8.ValidString(id) && utf8.RuneCountInString(id) <= 50000
}
func eventOwnerID(id string) bool {
	if len(id) < 1 || len(id) > 100 {
		return false
	}
	for _, c := range []byte(id) {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}
func (s EditorSelector) Validate() error {
	required, optional := editorSelectorFields(s.Kind)
	if required == nil {
		return invalid("selector", "Unknown editor kind")
	}
	fields := map[string]string{"messageId": s.MessageID, "participantId": s.ParticipantID, "contractId": s.ContractID, "operationId": s.OperationID, "channelId": s.ChannelID, "diagramId": s.DiagramID, "transitionId": s.TransitionID, "ruleId": s.RuleID, "nodeId": s.NodeID, "embeddedContractId": s.EmbeddedContractID}
	for _, name := range required[1:] {
		if fields[name] == "" {
			return invalid(name, "Required owner ID")
		}
	}
	for name, value := range fields {
		if value == "" {
			continue
		}
		if !slices.Contains(required, name) && !slices.Contains(optional, name) {
			return invalid(name, "Member is not part of selected variant")
		}
		valid := scenarioOwnerID(value)
		if strings.HasPrefix(s.Kind, "event_") {
			valid = eventOwnerID(value)
		}
		if name == "diagramId" || name == "transitionId" {
			valid = statediagram.ValidID(value)
		}
		if name == "ruleId" || name == "nodeId" {
			valid = responserules.ValidID(value)
		}
		if !valid {
			return invalid(name, "Invalid exact owner ID")
		}
	}
	return nil
}
func (s EditorSelector) ValidateForArtifact(kind string) error {
	if err := s.Validate(); err != nil {
		return err
	}
	_, optional := editorSelectorFields(s.Kind)
	switch kind {
	case "api_design":
		if len(optional) == 0 || s.EmbeddedContractID != "" {
			return invalid("selector", "API accepts only state/rule selectors without embeddedContractId")
		}
	case "design_scenario":
		if len(optional) > 0 && s.EmbeddedContractID == "" {
			return invalid("embeddedContractId", "Scenario state/rule selector requires embedded contract")
		}
	default:
		return invalid("artifactKind", "Unknown owner kind")
	}
	return nil
}
func (s *EditorSelector) UnmarshalJSON(raw []byte) error {
	*s = EditorSelector{}
	m, err := relationalObject(raw)
	if err != nil {
		return invalid("selector", "Expected strict object")
	}
	var kind string
	if json.Unmarshal(m["kind"], &kind) != nil {
		return invalid("kind", "Expected editor kind")
	}
	required, optional := editorSelectorFields(kind)
	if required == nil {
		return invalid("kind", "Unknown editor kind")
	}
	type plain EditorSelector
	if err = strictAPIObject(raw, required, optional, (*plain)(s)); err != nil {
		return err
	}
	if m["embeddedContractId"] != nil && s.EmbeddedContractID == "" {
		return invalid("embeddedContractId", "Expected nonempty owner ID")
	}
	return s.Validate()
}

type EditorBindingInput struct {
	Selector      EditorSelector `json:"selector"`
	SourceNodeIDs []string       `json:"sourceNodeIds"`
}

func validateEditorSources(ids []string) error {
	if len(ids) < 1 || len(ids) > MaxEditorBindingSources {
		return invalid("sourceNodeIds", "Expected 1–100 distinct source UUIDs")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !ValidID(id) || seen[id] {
			return invalid("sourceNodeIds", "Invalid or duplicate source UUID")
		}
		seen[id] = true
	}
	return nil
}
func (b EditorBindingInput) ValidateForArtifact(kind string) error {
	if err := b.Selector.ValidateForArtifact(kind); err != nil {
		return err
	}
	return validateEditorSources(b.SourceNodeIDs)
}
func (b *EditorBindingInput) UnmarshalJSON(raw []byte) error {
	*b = EditorBindingInput{}
	type plain EditorBindingInput
	if err := strictAPIObject(raw, []string{"selector", "sourceNodeIds"}, nil, (*plain)(b)); err != nil {
		return err
	}
	if err := b.Selector.Validate(); err != nil {
		return err
	}
	return validateEditorSources(b.SourceNodeIDs)
}

type EditorBinding struct {
	ArtifactKind   string         `json:"artifactKind"`
	ArtifactID     string         `json:"artifactId"`
	Selector       EditorSelector `json:"selector"`
	SourceNodeIDs  []string       `json:"sourceNodeIds"`
	SourceLabels   []string       `json:"sourceLabels"`
	ObjectHash     string         `json:"objectHash"`
	LastKnownLabel string         `json:"lastKnownLabel"`
	Origin         string         `json:"origin"`
	Reason         string         `json:"reason"`
}

func (b EditorBinding) Validate() error {
	if err := (ArtifactKey{b.ArtifactKind, b.ArtifactID}).Validate(); err != nil {
		return err
	}
	if err := b.Selector.ValidateForArtifact(b.ArtifactKind); err != nil {
		return err
	}
	if err := validateEditorSources(b.SourceNodeIDs); err != nil {
		return err
	}
	if len(b.SourceLabels) != len(b.SourceNodeIDs) || !validHash(b.ObjectHash) || b.Origin != "manual" || !validAPIText(b.LastKnownLabel, 0, MaxAPIArtifactLabelBytes) || !validAPIText(b.Reason, 1, 4096) {
		return invalid("editorBinding", "Invalid frozen editor binding")
	}
	for _, label := range b.SourceLabels {
		if !validAPIText(label, 0, MaxAPIArtifactLabelBytes) {
			return invalid("sourceLabels", "Frozen label exceeds bound")
		}
	}
	return nil
}
func (b *EditorBinding) UnmarshalJSON(raw []byte) error {
	*b = EditorBinding{}
	type plain EditorBinding
	if err := strictAPIObject(raw, []string{"artifactKind", "artifactId", "selector", "sourceNodeIds", "sourceLabels", "objectHash", "lastKnownLabel", "origin", "reason"}, nil, (*plain)(b)); err != nil {
		return err
	}
	m, _ := relationalObject(raw)
	var labels []jsontext.Value
	if err := json.Unmarshal(m["sourceLabels"], &labels); err != nil {
		return err
	}
	for _, label := range labels {
		if bytes.Equal(bytes.TrimSpace(label), []byte("null")) {
			return invalid("sourceLabels", "Null label is not allowed")
		}
	}
	if err := b.Validate(); err != nil {
		return err
	}
	if !slices.IsSorted(b.SourceNodeIDs) {
		return invalid("sourceNodeIds", "Stored source set must be sorted")
	}
	return nil
}

// CanonicalEditorBinding copies and sorts the source set with its frozen labels.
// It rejects duplicates rather than silently changing the association.
func CanonicalEditorBinding(b EditorBinding) (EditorBinding, error) {
	if err := b.Validate(); err != nil {
		return b, err
	}
	order := make([]int, len(b.SourceNodeIDs))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(i, j int) int { return strings.Compare(b.SourceNodeIDs[i], b.SourceNodeIDs[j]) })
	ids, labels := make([]string, len(order)), make([]string, len(order))
	for i, j := range order {
		ids[i], labels[i] = b.SourceNodeIDs[j], b.SourceLabels[j]
	}
	b.SourceNodeIDs, b.SourceLabels = ids, labels
	return b, nil
}

// JSON tuple encoding makes identity collision-free even for opaque IDs that
// contain delimiters. Owner revision is pin context, not object identity.
func EditorBindingIdentity(b EditorBinding) (string, error) {
	if err := b.Validate(); err != nil {
		return "", err
	}
	raw, err := canonicalAPIJSON(struct {
		Artifact ArtifactKey    `json:"artifact"`
		Selector EditorSelector `json:"selector"`
	}{ArtifactKey{b.ArtifactKind, b.ArtifactID}, b.Selector})
	return string(raw), err
}

type ArtifactPinCommand struct {
	Type           string               `json:"type"`
	Artifact       ArtifactKey          `json:"artifact"`
	RevisionID     string               `json:"revisionId,omitempty"`
	APIBindings    []APIPinBindingInput `json:"apiBindings,omitzero"`
	EditorBindings []EditorBindingInput `json:"editorBindings,omitzero"`
	Reason         string               `json:"reason"`
}

func (c ArtifactPinCommand) Validate() error {
	if err := c.Artifact.Validate(); err != nil {
		return err
	}
	if !validAPIText(c.Reason, 1, 4096) {
		return invalid("reason", "Expected 1–4096 UTF-8 bytes")
	}
	switch c.Type {
	case "remove_artifact_pin":
		if c.RevisionID != "" || c.APIBindings != nil || c.EditorBindings != nil {
			return invalid("command", "Remove only accepts artifact and reason")
		}
	case "set_artifact_pin":
		if !ValidAPIArtifactID(c.RevisionID) || c.EditorBindings == nil {
			return invalid("command", "Set requires exact revision and full editorBindings")
		}
		if (c.Artifact.Kind == "api_design") != (c.APIBindings != nil) {
			return invalid("apiBindings", "API requires full apiBindings; scenario forbids apiBindings")
		}
		if len(c.APIBindings)+len(c.EditorBindings) > MaxAPIArtifactBindings {
			return invalid("bindings", "Combined vector exceeds 200")
		}
		sources := map[string]bool{}
		for _, b := range c.APIBindings {
			if err := b.Validate(); err != nil {
				return err
			}
			if sources[b.SourceNodeID] {
				return invalid("sourceNodeId", "Duplicate API source binding")
			}
			sources[b.SourceNodeID] = true
		}
		selectors := map[string]bool{}
		for _, b := range c.EditorBindings {
			if err := b.ValidateForArtifact(c.Artifact.Kind); err != nil {
				return err
			}
			raw, err := canonicalAPIJSON(b.Selector)
			if err != nil {
				return err
			}
			if selectors[string(raw)] {
				return invalid("selector", "Duplicate editor object binding")
			}
			selectors[string(raw)] = true
		}
	default:
		return invalid("type", "Unknown artifact command")
	}
	return nil
}
func (c *ArtifactPinCommand) UnmarshalJSON(raw []byte) error {
	*c = ArtifactPinCommand{}
	m, err := relationalObject(raw)
	if err != nil {
		return invalid("command", "Expected object")
	}
	var kind string
	if json.Unmarshal(m["type"], &kind) != nil {
		return invalid("type", "Expected command type")
	}
	var artifact ArtifactKey
	if err = json.Unmarshal(m["artifact"], &artifact); err != nil {
		return err
	}
	required := []string{"type", "artifact", "reason"}
	if kind == "set_artifact_pin" {
		required = append(required, "revisionId", "editorBindings")
		if artifact.Kind == "api_design" {
			required = append(required, "apiBindings")
		}
	}
	type plain ArtifactPinCommand
	if err = strictAPIObject(raw, required, nil, (*plain)(c)); err != nil {
		return err
	}
	return c.Validate()
}
func ValidateArtifactPinCommands(commands []ArtifactPinCommand) error {
	if len(commands) < 1 || len(commands) > MaxAPIPinCommands {
		return invalid("commands", "Expected 1–20 commands")
	}
	artifacts := map[ArtifactKey]bool{}
	sources := map[string]bool{}
	count := 0
	for _, c := range commands {
		if err := c.Validate(); err != nil {
			return err
		}
		if artifacts[c.Artifact] {
			return invalid("artifact", "Duplicate artifact command")
		}
		artifacts[c.Artifact] = true
		count += len(c.APIBindings) + len(c.EditorBindings)
		for _, b := range c.APIBindings {
			if sources[b.SourceNodeID] {
				return invalid("sourceNodeId", "Duplicate API source binding")
			}
			sources[b.SourceNodeID] = true
		}
	}
	if count > MaxAPIArtifactBindings {
		return invalid("bindings", "Combined bindings exceed 200")
	}
	return nil
}

type PreviewArtifactPinsInput struct {
	BaseRevisionID  string               `json:"baseRevisionId"`
	ExpectedVersion int64                `json:"expectedVersion"`
	Commands        []ArtifactPinCommand `json:"commands"`
}
type ApplyArtifactPinsInput struct {
	BaseRevisionID  string               `json:"baseRevisionId"`
	ExpectedVersion int64                `json:"expectedVersion"`
	Commands        []ArtifactPinCommand `json:"commands"`
	CandidateHash   string               `json:"candidateHash"`
	IdempotencyKey  string               `json:"idempotencyKey"`
}

func (in PreviewArtifactPinsInput) Validate() error {
	if !ValidID(in.BaseRevisionID) || in.ExpectedVersion < 1 {
		return invalid("body", "Expected canonical base UUID and positive version")
	}
	return ValidateArtifactPinCommands(in.Commands)
}
func (in ApplyArtifactPinsInput) Validate() error {
	if err := (PreviewArtifactPinsInput{in.BaseRevisionID, in.ExpectedVersion, in.Commands}).Validate(); err != nil {
		return err
	}
	if !validHash(in.CandidateHash) {
		return invalid("candidateHash", "Expected lowercase SHA-256")
	}
	return validateKey(in.IdempotencyKey)
}
func artifactBodyLimit(raw []byte) error {
	if len(raw) > MaxAPIPinBodyBytes {
		return &FaultError{Status: 413, Code: "backend_artifact_pins_limit", Message: "Artifact pin body exceeds 128 KiB"}
	}
	return nil
}
func (in *PreviewArtifactPinsInput) UnmarshalJSON(raw []byte) error {
	*in = PreviewArtifactPinsInput{}
	if err := artifactBodyLimit(raw); err != nil {
		return err
	}
	type plain PreviewArtifactPinsInput
	if err := strictAPIObject(raw, []string{"baseRevisionId", "expectedVersion", "commands"}, nil, (*plain)(in)); err != nil {
		return err
	}
	return in.Validate()
}
func (in *ApplyArtifactPinsInput) UnmarshalJSON(raw []byte) error {
	*in = ApplyArtifactPinsInput{}
	if err := artifactBodyLimit(raw); err != nil {
		return err
	}
	type plain ApplyArtifactPinsInput
	if err := strictAPIObject(raw, []string{"baseRevisionId", "expectedVersion", "commands", "candidateHash", "idempotencyKey"}, nil, (*plain)(in)); err != nil {
		return err
	}
	return in.Validate()
}

type ArtifactQueryInput struct {
	RevisionID         string      `json:"revisionId"`
	Artifact           ArtifactKey `json:"artifact"`
	View               string      `json:"view"`
	EmbeddedContractID string      `json:"embeddedContractId,omitempty"`
	Limit              int         `json:"limit,omitzero"`
	Cursor             string      `json:"cursor,omitempty"`
}

func (in ArtifactQueryInput) Validate() error {
	if err := in.Artifact.Validate(); err != nil {
		return err
	}
	if !ValidID(in.RevisionID) || in.Limit < 0 || in.Limit > 100 || !validAPIText(in.Cursor, 0, 4096) {
		return invalid("query", "Invalid revision, page limit or cursor")
	}
	switch in.View {
	case "sequence", "event_model":
		if in.Artifact.Kind != "design_scenario" || in.EmbeddedContractID != "" {
			return invalid("view", "Sequence/event views require scenario and no embedded selection")
		}
	case "states", "response_rules":
		if in.Artifact.Kind == "api_design" {
			if in.EmbeddedContractID != "" {
				return invalid("embeddedContractId", "API view forbids embedded selection")
			}
		} else if !scenarioOwnerID(in.EmbeddedContractID) {
			return invalid("embeddedContractId", "Scenario state/rule view requires embedded selection")
		}
	default:
		return invalid("view", "Unknown projection view")
	}
	return nil
}
func (in *ArtifactQueryInput) UnmarshalJSON(raw []byte) error {
	*in = ArtifactQueryInput{}
	type plain ArtifactQueryInput
	if err := strictAPIObject(raw, []string{"revisionId", "artifact", "view"}, []string{"embeddedContractId", "limit", "cursor"}, (*plain)(in)); err != nil {
		return err
	}
	m, _ := relationalObject(raw)
	if m["limit"] != nil && in.Limit == 0 {
		return invalid("limit", "Explicit limit must be 1–100")
	}
	if m["embeddedContractId"] != nil && in.EmbeddedContractID == "" {
		return invalid("embeddedContractId", "Expected nonempty owner ID")
	}
	return in.Validate()
}

type ScenarioArtifactReader interface {
	ArtifactSnapshot(context.Context, int64, int64) (*designscenario.ArtifactSnapshot, error)
	ArtifactInspectionSnapshot(context.Context, int64, int64) (*designscenario.ArtifactInspectionSnapshot, error)
	ArtifactDigestTx(context.Context, *sql.Tx, int64, int64) (string, error)
}

// Every NEW wire DTO uses decimal strings for numeric owner identities. Raw
// owner documents stay strings; typed authored structures below contain no
// numeric design/revision/version identity that needs lossy conversion.
type ArtifactContractSource struct {
	DesignID   string `json:"designId"`
	RevisionID string `json:"revisionId"`
	Version    string `json:"version"`
}
type ArtifactEmbeddedContract struct {
	ContractID             string                  `json:"contractId"`
	Mode                   string                  `json:"mode"`
	DocumentHash           string                  `json:"documentHash"`
	Origin                 *ArtifactContractSource `json:"origin,omitzero"`
	OriginStatus           string                  `json:"originStatus"`
	APIContentHash         string                  `json:"apiContentHash,omitempty"`
	CurrentDraftRevisionID string                  `json:"currentDraftRevisionId,omitempty"`
}
type ArtifactEventMapLocator struct {
	Pointer          string `json:"pointer"`
	EntityID         string `json:"entityId,omitempty"`
	ContractID       string `json:"contractId,omitempty"`
	OperationID      string `json:"operationId,omitempty"`
	ParticipantID    string `json:"participantId,omitempty"`
	HTTPContractID   string `json:"httpContractId,omitempty"`
	OperationKey     string `json:"operationKey,omitempty"`
	DiagramID        string `json:"diagramId,omitempty"`
	TransitionID     string `json:"transitionId,omitempty"`
	Mode             string `json:"mode,omitempty"`
	PinnedRevisionID string `json:"pinnedRevisionId,omitempty"`
	Method           string `json:"method,omitempty"`
	Path             string `json:"path,omitempty"`
}
type ArtifactOwnerAddress struct {
	Pointer       string                   `json:"pointer"`
	EntityID      string                   `json:"entityId,omitempty"`
	MessageID     string                   `json:"messageId,omitempty"`
	ParticipantID string                   `json:"participantId,omitempty"`
	FragmentID    string                   `json:"fragmentId,omitempty"`
	BranchID      string                   `json:"branchId,omitempty"`
	ContractID    string                   `json:"contractId,omitempty"`
	OperationID   string                   `json:"operationId,omitempty"`
	ChannelID     string                   `json:"channelId,omitempty"`
	SchemaID      string                   `json:"schemaId,omitempty"`
	ServerID      string                   `json:"serverId,omitempty"`
	DiagramID     string                   `json:"diagramId,omitempty"`
	StateID       string                   `json:"stateId,omitempty"`
	TransitionID  string                   `json:"transitionId,omitempty"`
	RuleID        string                   `json:"ruleId,omitempty"`
	NodeID        string                   `json:"nodeId,omitempty"`
	EdgeID        string                   `json:"edgeId,omitempty"`
	OperationKey  string                   `json:"operationKey,omitempty"`
	EventMap      *ArtifactEventMapLocator `json:"eventMap,omitzero"`
}
type ArtifactProjectionLocator struct {
	Pin      ArtifactPin               `json:"pin"`
	View     string                    `json:"view"`
	Owner    ArtifactOwnerAddress      `json:"owner"`
	Embedded *ArtifactEmbeddedContract `json:"embedded,omitzero"`
}
type ArtifactEventMapNode struct {
	ID       string                  `json:"id"`
	Kind     string                  `json:"kind"`
	Label    string                  `json:"label"`
	Locator  ArtifactEventMapLocator `json:"locator"`
	GroupID  string                  `json:"groupId,omitempty"`
	ClientID string                  `json:"clientId,omitempty"`
}
type ArtifactEventMapEdge struct {
	ID      string                  `json:"id"`
	Kind    string                  `json:"kind"`
	Source  string                  `json:"source"`
	Target  string                  `json:"target"`
	Label   string                  `json:"label"`
	Locator ArtifactEventMapLocator `json:"locator"`
}
type ArtifactAPIOperationData struct {
	OperationKey string `json:"operationKey"`
	Method       string `json:"method"`
	Path         string `json:"path"`
	Summary      string `json:"summary"`
	DocumentJSON string `json:"documentJSON"`
}
type ArtifactUnsupportedData struct {
	DocumentJSON string `json:"documentJSON"`
	Description  string `json:"description"`
}

// Named optional members form a typed data union. Producers choose exactly one
// member matching Kind; no unbounded arbitrary settings/object map is exposed.
type ArtifactProjectionData struct {
	Kind            string                         `json:"kind"`
	Participant     *designscenario.Participant    `json:"participant,omitzero"`
	SequenceMessage *designscenario.Message        `json:"sequenceMessage,omitzero"`
	Fragment        *designscenario.Fragment       `json:"fragment,omitzero"`
	StateDiagram    *statediagram.Diagram          `json:"stateDiagram,omitzero"`
	State           *statediagram.State            `json:"state,omitzero"`
	StateTransition *statediagram.Transition       `json:"stateTransition,omitzero"`
	ResponseRule    *responserules.Rule            `json:"responseRule,omitzero"`
	ResponseNode    *responserules.Node            `json:"responseNode,omitzero"`
	ResponseEdge    *responserules.Edge            `json:"responseEdge,omitzero"`
	EventNode       *ArtifactEventMapNode          `json:"eventNode,omitzero"`
	EventEdge       *ArtifactEventMapEdge          `json:"eventEdge,omitzero"`
	EventServer     *designscenario.EventServer    `json:"eventServer,omitzero"`
	EventChannel    *designscenario.EventChannel   `json:"eventChannel,omitzero"`
	EventMessage    *designscenario.EventMessage   `json:"eventMessage,omitzero"`
	EventSchema     *designscenario.EventSchema    `json:"eventSchema,omitzero"`
	EventContract   *designscenario.EventContract  `json:"eventContract,omitzero"`
	EventOperation  *designscenario.EventOperation `json:"eventOperation,omitzero"`
	APIOperation    *ArtifactAPIOperationData      `json:"apiOperation,omitzero"`
	Unsupported     *ArtifactUnsupportedData       `json:"unsupported,omitzero"`
}

func (d ArtifactProjectionData) Validate() error {
	fields := map[string]bool{"participant": d.Participant != nil, "sequenceMessage": d.SequenceMessage != nil, "fragment": d.Fragment != nil, "stateDiagram": d.StateDiagram != nil, "state": d.State != nil, "stateTransition": d.StateTransition != nil, "responseRule": d.ResponseRule != nil, "responseNode": d.ResponseNode != nil, "responseEdge": d.ResponseEdge != nil, "eventNode": d.EventNode != nil, "eventEdge": d.EventEdge != nil, "eventServer": d.EventServer != nil, "eventChannel": d.EventChannel != nil, "eventMessage": d.EventMessage != nil, "eventSchema": d.EventSchema != nil, "eventContract": d.EventContract != nil, "eventOperation": d.EventOperation != nil, "apiOperation": d.APIOperation != nil, "unsupported": d.Unsupported != nil}
	member := artifactProjectionDataMember(d.Kind)
	if member == "" || !fields[member] {
		return invalid("data", "Expected exact typed projection data variant")
	}
	for name, present := range fields {
		if present && name != member {
			return invalid("data", "Mixed projection data variants")
		}
	}
	return nil
}
func artifactProjectionDataMember(kind string) string {
	switch kind {
	case "participant":
		return "participant"
	case "sequence_message":
		return "sequenceMessage"
	case "fragment":
		return "fragment"
	case "state_diagram":
		return "stateDiagram"
	case "state":
		return "state"
	case "state_transition":
		return "stateTransition"
	case "response_rule":
		return "responseRule"
	case "response_node":
		return "responseNode"
	case "response_edge":
		return "responseEdge"
	case "event_node":
		return "eventNode"
	case "event_edge":
		return "eventEdge"
	case "event_server":
		return "eventServer"
	case "event_channel":
		return "eventChannel"
	case "event_message":
		return "eventMessage"
	case "event_schema":
		return "eventSchema"
	case "event_contract":
		return "eventContract"
	case "event_operation":
		return "eventOperation"
	case "api_operation":
		return "apiOperation"
	case "unsupported":
		return "unsupported"
	}
	return ""
}
func (d *ArtifactProjectionData) UnmarshalJSON(raw []byte) error {
	*d = ArtifactProjectionData{}
	m, err := relationalObject(raw)
	if err != nil {
		return invalid("data", "Expected strict projection union")
	}
	var kind string
	if json.Unmarshal(m["kind"], &kind) != nil {
		return invalid("kind", "Expected data kind")
	}
	member := artifactProjectionDataMember(kind)
	if member == "" {
		return invalid("kind", "Unknown projection data kind")
	}
	type plain ArtifactProjectionData
	if err = strictAPIObject(raw, []string{"kind", member}, nil, (*plain)(d)); err != nil {
		return err
	}
	return d.Validate()
}
func (d ArtifactProjectionData) MarshalJSON() ([]byte, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	type plain ArtifactProjectionData
	return json.Marshal(plain(d))
}

type ArtifactDiagnostic struct {
	Code          string                     `json:"code"`
	Severity      string                     `json:"severity,omitempty"`
	Message       string                     `json:"message"`
	Artifact      *ArtifactKey               `json:"artifact,omitzero"`
	Selector      *EditorSelector            `json:"selector,omitzero"`
	Locator       *ArtifactProjectionLocator `json:"locator,omitzero"`
	SourceNodeIDs []string                   `json:"sourceNodeIds,omitzero"`
	Pointer       string                     `json:"pointer,omitempty"`
}
type ArtifactResolution struct {
	Status                 string               `json:"status"`
	Diagnostics            []ArtifactDiagnostic `json:"diagnostics"`
	CurrentDraftRevisionID string               `json:"currentDraftRevisionId,omitempty"`
	UpdateAvailable        bool                 `json:"updateAvailable"`
}
type ArtifactProjectionItem struct {
	ID              string                    `json:"id"`
	Kind            string                    `json:"kind"`
	Label           string                    `json:"label"`
	Locator         ArtifactProjectionLocator `json:"locator"`
	BindingSelector *EditorSelector           `json:"bindingSelector,omitzero"`
	ObjectHash      string                    `json:"objectHash"`
	SourceNodeIDs   []string                  `json:"sourceNodeIds"`
	Data            ArtifactProjectionData    `json:"data"`
	Diagnostics     []ArtifactDiagnostic      `json:"diagnostics"`
}
type ArtifactProjectionCoverage struct {
	ItemsReturned       int      `json:"itemsReturned"`
	TotalItems          int      `json:"totalItems"`
	NodesReturned       int      `json:"nodesReturned"`
	EdgesReturned       int      `json:"edgesReturned"`
	DiagnosticsReturned int      `json:"diagnosticsReturned"`
	TruncatedReasons    []string `json:"truncatedReasons"`
}
type ArtifactProjectionPage struct {
	RevisionID         string                     `json:"revisionId"`
	SemanticHash       string                     `json:"semanticHash"`
	SourceSnapshotIDs  []string                   `json:"sourceSnapshotIds"`
	Pins               []ArtifactPin              `json:"pins"`
	SelectedPin        ArtifactPin                `json:"selectedPin"`
	HashPolicy         string                     `json:"hashPolicy"`
	View               string                     `json:"view"`
	EmbeddedContractID string                     `json:"embeddedContractId,omitempty"`
	APIBindings        []APIArtifactBinding       `json:"apiBindings"`
	EditorBindings     []EditorBinding            `json:"editorBindings"`
	BindingsComplete   bool                       `json:"bindingsComplete"`
	Items              []ArtifactProjectionItem   `json:"items"`
	NextCursor         string                     `json:"nextCursor"`
	Resolution         ArtifactResolution         `json:"resolution"`
	Diagnostics        []ArtifactDiagnostic       `json:"diagnostics"`
	Coverage           ArtifactProjectionCoverage `json:"coverage"`
	Complete           bool                       `json:"complete"`
}

// Comparison sides retain full association and pin context. Existing B24
// ArtifactRef/RecordSide fields remain unchanged until the E4 coupled switch.
type EditorArtifactSide struct {
	Pin     ArtifactPin   `json:"pin"`
	Binding EditorBinding `json:"binding"`
}
type ArtifactGroupSide struct {
	Pin ArtifactPin `json:"pin"`
}
type ArtifactComparisonSide struct {
	API    *ArtifactRef        `json:"api,omitzero"`
	Editor *EditorArtifactSide `json:"editor,omitzero"`
	Group  *ArtifactGroupSide  `json:"group,omitzero"`
}
type ArtifactPinsDiff struct {
	Identity       string                    `json:"identity"`
	SourceNodeID   string                    `json:"sourceNodeId,omitempty"`
	Kind           string                    `json:"kind"`
	Status         string                    `json:"status"`
	Before         *ArtifactComparisonSide   `json:"before,omitzero"`
	After          *ArtifactComparisonSide   `json:"after,omitzero"`
	ContextChanged bool                      `json:"contextChanged"`
	Changes        []APIArtifactObjectChange `json:"changes"`
}
type ArtifactPinsPreview struct {
	BaseRevisionID    string               `json:"baseRevisionId"`
	ExpectedVersion   int64                `json:"expectedVersion"`
	CandidateHash     string               `json:"candidateHash"`
	SemanticHash      string               `json:"semanticHash"`
	Pins              []ArtifactPin        `json:"pins"`
	APIBindings       []APIArtifactBinding `json:"apiBindings"`
	EditorBindings    []EditorBinding      `json:"editorBindings"`
	SourceSnapshotIDs []string             `json:"sourceSnapshotIds"`
	Diagnostics       []ArtifactDiagnostic `json:"diagnostics"`
	Diff              []ArtifactPinsDiff   `json:"diff"`
	DiffTruncated     bool                 `json:"diffTruncated"`
	CanApply          bool                 `json:"canApply"`
}
type ArtifactPinsResult struct {
	receiptJSON string
	Project     Project  `json:"project"`
	Revision    Revision `json:"revision"`
}

func (out ArtifactPinsResult) MarshalJSON() ([]byte, error) {
	if out.receiptJSON != "" {
		return []byte(out.receiptJSON), nil
	}
	type plain ArtifactPinsResult
	return json.Marshal(plain(out))
}

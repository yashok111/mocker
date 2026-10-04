package backendmodel

import (
	"bytes"
	"encoding/json/v2"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	MaxAPIPinCommands         = 20
	MaxAPIArtifactPins        = 20
	MaxAPIArtifactBindings    = 200
	MaxAPIPinBodyBytes        = 128 << 10
	MaxAPIArtifactDiffEntries = 1000
	MaxAPIArtifactLabelBytes  = 4096
)

type APIArtifactSelector struct {
	ObjectKey   string `json:"objectKey,omitempty"`
	JSONPointer string `json:"jsonPointer,omitempty"`
}
type ArtifactRef struct {
	Kind            string              `json:"kind"`
	ArtifactID      string              `json:"artifactId"`
	RevisionID      string              `json:"revisionId"`
	ContentHash     string              `json:"contentHash"`
	Selector        APIArtifactSelector `json:"selector"`
	ObjectHash      string              `json:"objectHash"`
	LastKnownLabel  string              `json:"lastKnownLabel"`
	ResolvedPointer string              `json:"resolvedPointer"`
}
type APIArtifactBinding struct {
	SourceNodeID         string      `json:"sourceNodeId"`
	SourceKind           string      `json:"sourceKind"`
	SourceLastKnownLabel string      `json:"sourceLastKnownLabel"`
	Ref                  ArtifactRef `json:"ref"`
	Origin               string      `json:"origin"`
	Reason               string      `json:"reason"`
}
type APIPinBindingInput struct {
	SourceNodeID string              `json:"sourceNodeId"`
	Selector     APIArtifactSelector `json:"selector"`
}
type APIPinCommand struct {
	Type       string               `json:"type"`
	ArtifactID string               `json:"artifactId"`
	RevisionID string               `json:"revisionId,omitempty"`
	Bindings   []APIPinBindingInput `json:"bindings,omitempty"`
	Reason     string               `json:"reason"`
}
type APIArtifactQueryInput struct {
	Proposal        *ProposalReadTarget        `json:"proposal,omitzero"`
	ChangeProposal  *ProposalReadTarget        `json:"changeProposal,omitzero"`
	ImportCandidate *ImportCandidateReadTarget `json:"importCandidate,omitzero"`
	RevisionID      string                     `json:"revisionId"`
	SourceNodeID    string                     `json:"sourceNodeId,omitempty"`
	Limit           int                        `json:"limit,omitzero"`
	Cursor          string                     `json:"cursor,omitempty"`
}
type PreviewAPIPinsInput struct {
	BaseRevisionID  string          `json:"baseRevisionId"`
	ExpectedVersion int64           `json:"expectedVersion"`
	Commands        []APIPinCommand `json:"commands"`
}
type ApplyAPIPinsInput struct {
	BaseRevisionID  string          `json:"baseRevisionId"`
	ExpectedVersion int64           `json:"expectedVersion"`
	Commands        []APIPinCommand `json:"commands"`
	CandidateHash   string          `json:"candidateHash"`
	IdempotencyKey  string          `json:"idempotencyKey"`
}
type APIArtifactDiagnostic struct {
	Code         string `json:"code"`
	SourceNodeID string `json:"sourceNodeId,omitempty"`
	ArtifactID   string `json:"artifactId,omitempty"`
	Pointer      string `json:"pointer,omitempty"`
	Message      string `json:"message"`
}
type APIArtifactResolution struct {
	Status                 string                  `json:"status"`
	Diagnostics            []APIArtifactDiagnostic `json:"diagnostics"`
	CurrentDraftRevisionID string                  `json:"currentDraftRevisionId,omitempty"`
	UpdateAvailable        bool                    `json:"updateAvailable"`
}
type APIArtifactItem struct {
	Binding    APIArtifactBinding    `json:"binding"`
	Resolution APIArtifactResolution `json:"resolution"`
}
type APIArtifactPage struct {
	Target            *BackendReadTarget  `json:"target,omitzero"`
	EffectivePins     *EffectiveGraphPins `json:"effectivePins,omitzero"`
	RevisionID        string              `json:"revisionId"`
	SemanticHash      string              `json:"semanticHash"`
	SourceSnapshotIDs []string            `json:"sourceSnapshotIds"`
	Pins              []ArtifactPin       `json:"pins"`
	Items             []APIArtifactItem   `json:"items"`
	NextCursor        string              `json:"nextCursor"`
}
type APIArtifactObjectChange struct {
	Pointer    string `json:"pointer"`
	Kind       string `json:"kind"`
	BeforeHash string `json:"beforeHash,omitempty"`
	AfterHash  string `json:"afterHash,omitempty"`
}
type APIArtifactDiff struct {
	SourceNodeID   string                    `json:"sourceNodeId"`
	Status         string                    `json:"status"`
	Before         *ArtifactRef              `json:"before,omitzero"`
	After          *ArtifactRef              `json:"after,omitzero"`
	ContextChanged bool                      `json:"contextChanged"`
	Changes        []APIArtifactObjectChange `json:"changes"`
}
type APIArtifactContext struct {
	SourceContentHash  string               `json:"sourceContentHash"`
	SourceSemanticHash string               `json:"sourceSemanticHash"`
	Bindings           []APIArtifactBinding `json:"bindings"`
}
type APIPinsPreview struct {
	BaseRevisionID    string                  `json:"baseRevisionId"`
	ExpectedVersion   int64                   `json:"expectedVersion"`
	CandidateHash     string                  `json:"candidateHash"`
	SemanticHash      string                  `json:"semanticHash"`
	Pins              []ArtifactPin           `json:"pins"`
	Bindings          []APIArtifactBinding    `json:"bindings"`
	SourceSnapshotIDs []string                `json:"sourceSnapshotIds"`
	Diagnostics       []APIArtifactDiagnostic `json:"diagnostics"`
	Diff              []APIArtifactDiff       `json:"diff"`
	DiffTruncated     bool                    `json:"diffTruncated"`
	CanApply          bool                    `json:"canApply"`
}
type APIPinsResult struct {
	receiptJSON string
	Project     Project  `json:"project"`
	Revision    Revision `json:"revision"`
}

func (out APIPinsResult) MarshalJSON() ([]byte, error) {
	if out.receiptJSON != "" {
		return []byte(out.receiptJSON), nil
	}
	type plain APIPinsResult
	return json.Marshal(plain(out))
}

// strictAPIObject checks required members, duplicate/unknown members and nulls
// before decoding. Nested DTOs apply the same admission independently.
func strictAPIObject(raw []byte, required, optional []string, out any) error {
	m, err := relationalObject(raw)
	if err != nil {
		return invalid("body", "Expected a strict object")
	}
	if err = relationalFields(m, required, optional); err != nil {
		return invalid("body", err.Error())
	}
	for k, v := range m {
		if string(bytes.TrimSpace(v)) == "null" {
			return invalid(k, "Null is not allowed")
		}
	}
	if err = json.Unmarshal(raw, out, json.RejectUnknownMembers(true)); err != nil {
		return invalid("body", err.Error())
	}
	return nil
}
func ValidAPIArtifactID(id string) bool {
	n, err := strconv.ParseInt(id, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == id
}
func validAPIText(s string, minBytes, maxBytes int) bool {
	return utf8.ValidString(s) && len(s) >= minBytes && len(s) <= maxBytes
}
func ValidateAPIArtifactPointer(p string) error {
	if !utf8.ValidString(p) || len(p) > 2048 || !strings.HasPrefix(p, "/") || strings.Count(p, "/") > 64 {
		return invalid("jsonPointer", "Expected a bounded RFC6901 pointer")
	}
	for i := 0; i < len(p); i++ {
		if p[i] == '~' {
			if i+1 >= len(p) || (p[i+1] != '0' && p[i+1] != '1') {
				return invalid("jsonPointer", "Invalid RFC6901 escape")
			}
			i++
		}
	}
	return nil
}
func (s APIArtifactSelector) Validate() error {
	if (s.ObjectKey != "") == (s.JSONPointer != "") {
		return invalid("selector", "Select exactly one objectKey or jsonPointer")
	}
	if s.ObjectKey != "" {
		if !validAPIText(s.ObjectKey, 1, 200) {
			return invalid("objectKey", "Expected 1–200 UTF-8 bytes")
		}
		return nil
	}
	return ValidateAPIArtifactPointer(s.JSONPointer)
}
func (s *APIArtifactSelector) UnmarshalJSON(raw []byte) error {
	*s = APIArtifactSelector{}
	type plain APIArtifactSelector
	m, err := relationalObject(raw)
	if err != nil || len(m) != 1 {
		return invalid("selector", "Select exactly one member")
	}
	if err = strictAPIObject(raw, nil, []string{"objectKey", "jsonPointer"}, (*plain)(s)); err != nil {
		return err
	}
	return s.Validate()
}
func (r ArtifactRef) Validate() error {
	if r.Kind != "api_design" || !ValidAPIArtifactID(r.ArtifactID) || !ValidAPIArtifactID(r.RevisionID) || !validHash(r.ContentHash) || !validHash(r.ObjectHash) {
		return invalid("ref", "Expected exact API revision IDs and lowercase SHA-256 hashes")
	}
	if !validAPIText(r.LastKnownLabel, 0, MaxAPIArtifactLabelBytes) {
		return invalid("lastKnownLabel", "Label exceeds bound")
	}
	if err := r.Selector.Validate(); err != nil {
		return err
	}
	return ValidateAPIArtifactPointer(r.ResolvedPointer)
}
func (r *ArtifactRef) UnmarshalJSON(raw []byte) error {
	*r = ArtifactRef{}
	type plain ArtifactRef
	if err := strictAPIObject(raw, []string{"kind", "artifactId", "revisionId", "contentHash", "selector", "objectHash", "lastKnownLabel", "resolvedPointer"}, nil, (*plain)(r)); err != nil {
		return err
	}
	return r.Validate()
}
func (b APIPinBindingInput) Validate() error {
	if !ValidID(b.SourceNodeID) {
		return invalid("sourceNodeId", "Expected canonical UUID")
	}
	return b.Selector.Validate()
}
func (b *APIPinBindingInput) UnmarshalJSON(raw []byte) error {
	*b = APIPinBindingInput{}
	type plain APIPinBindingInput
	if err := strictAPIObject(raw, []string{"sourceNodeId", "selector"}, nil, (*plain)(b)); err != nil {
		return err
	}
	return b.Validate()
}
func (b APIArtifactBinding) Validate() error {
	if !ValidID(b.SourceNodeID) || (b.SourceKind != "http_operation" && b.SourceKind != "api_field") || b.Origin != "manual" || !validAPIText(b.Reason, 1, 4096) || !validAPIText(b.SourceLastKnownLabel, 0, MaxAPIArtifactLabelBytes) {
		return invalid("binding", "Invalid frozen binding")
	}
	if (b.SourceKind == "http_operation") != (b.Ref.Selector.ObjectKey != "") {
		return invalid("selector", "Selector differs from source kind")
	}
	return b.Ref.Validate()
}
func (b *APIArtifactBinding) UnmarshalJSON(raw []byte) error {
	*b = APIArtifactBinding{}
	type plain APIArtifactBinding
	if err := strictAPIObject(raw, []string{"sourceNodeId", "sourceKind", "sourceLastKnownLabel", "ref", "origin", "reason"}, nil, (*plain)(b)); err != nil {
		return err
	}
	return b.Validate()
}
func (c APIPinCommand) Validate() error {
	if !ValidAPIArtifactID(c.ArtifactID) || !validAPIText(c.Reason, 1, 4096) {
		return invalid("command", "Invalid artifact ID or reason")
	}
	switch c.Type {
	case "remove_api_pin":
		if c.RevisionID != "" || c.Bindings != nil {
			return invalid("command", "Remove cannot select revision or bindings")
		}
	case "set_api_pin":
		if !ValidAPIArtifactID(c.RevisionID) || len(c.Bindings) < 1 || len(c.Bindings) > MaxAPIArtifactBindings {
			return invalid("bindings", "Set requires exact revision and 1–200 bindings")
		}
		seen := map[string]bool{}
		for _, b := range c.Bindings {
			if err := b.Validate(); err != nil {
				return err
			}
			if seen[b.SourceNodeID] {
				return invalid("sourceNodeId", "Duplicate source binding")
			}
			seen[b.SourceNodeID] = true
		}
	default:
		return invalid("type", "Unknown API pin command")
	}
	return nil
}
func (c *APIPinCommand) UnmarshalJSON(raw []byte) error {
	*c = APIPinCommand{}
	m, err := relationalObject(raw)
	if err != nil {
		return invalid("command", "Expected object")
	}
	var kind string
	if json.Unmarshal(m["type"], &kind) != nil {
		return invalid("type", "Expected command type")
	}
	required := []string{"type", "artifactId", "reason"}
	if kind == "set_api_pin" {
		required = append(required, "revisionId", "bindings")
	}
	type plain APIPinCommand
	if err = strictAPIObject(raw, required, nil, (*plain)(c)); err != nil {
		return err
	}
	return c.Validate()
}
func ValidateAPIPinCommands(commands []APIPinCommand) error {
	if len(commands) < 1 || len(commands) > MaxAPIPinCommands {
		return invalid("commands", "Expected 1–20 commands")
	}
	artifacts := map[string]bool{}
	sources := map[string]bool{}
	for _, c := range commands {
		if err := c.Validate(); err != nil {
			return err
		}
		if artifacts[c.ArtifactID] {
			return invalid("artifactId", "Duplicate artifact command")
		}
		artifacts[c.ArtifactID] = true
		for _, b := range c.Bindings {
			if sources[b.SourceNodeID] {
				return invalid("sourceNodeId", "Duplicate source binding")
			}
			sources[b.SourceNodeID] = true
		}
	}
	return nil
}
func (in PreviewAPIPinsInput) Validate() error {
	if !ValidID(in.BaseRevisionID) || in.ExpectedVersion < 1 {
		return invalid("body", "Expected canonical base UUID and positive int64 version")
	}
	return ValidateAPIPinCommands(in.Commands)
}
func (in ApplyAPIPinsInput) Validate() error {
	if err := (PreviewAPIPinsInput{in.BaseRevisionID, in.ExpectedVersion, in.Commands}).Validate(); err != nil {
		return err
	}
	if !validHash(in.CandidateHash) {
		return invalid("candidateHash", "Expected lowercase SHA-256")
	}
	return validateKey(in.IdempotencyKey)
}
func (in APIArtifactQueryInput) Validate() error {
	if in.Proposal != nil || in.ChangeProposal != nil || in.ImportCandidate != nil {
		target := graphTarget(in.RevisionID, in.Proposal, in.ChangeProposal, in.ImportCandidate)
		if err := target.Validate(); err != nil {
			return err
		}
		if err := rejectStagedView(target); err != nil {
			return err
		}
	}
	if (in.Proposal == nil && in.ChangeProposal == nil && !ValidID(in.RevisionID)) || (in.SourceNodeID != "" && !ValidID(in.SourceNodeID)) || in.Limit < 0 || in.Limit > 100 || !validAPIText(in.Cursor, 0, 4096) {
		return invalid("query", "Invalid explicit revision, source filter, page limit or cursor")
	}
	return nil
}
func (in *PreviewAPIPinsInput) UnmarshalJSON(raw []byte) error {
	*in = PreviewAPIPinsInput{}
	if len(raw) > MaxAPIPinBodyBytes {
		return &FaultError{Status: 413, Code: "backend_api_pins_limit", Message: "API pin body exceeds 128 KiB"}
	}
	type plain PreviewAPIPinsInput
	if err := strictAPIObject(raw, []string{"baseRevisionId", "expectedVersion", "commands"}, nil, (*plain)(in)); err != nil {
		return err
	}
	return in.Validate()
}
func (in *ApplyAPIPinsInput) UnmarshalJSON(raw []byte) error {
	*in = ApplyAPIPinsInput{}
	if len(raw) > MaxAPIPinBodyBytes {
		return &FaultError{Status: 413, Code: "backend_api_pins_limit", Message: "API pin body exceeds 128 KiB"}
	}
	type plain ApplyAPIPinsInput
	if err := strictAPIObject(raw, []string{"baseRevisionId", "expectedVersion", "commands", "candidateHash", "idempotencyKey"}, nil, (*plain)(in)); err != nil {
		return err
	}
	return in.Validate()
}
func (in *APIArtifactQueryInput) UnmarshalJSON(raw []byte) error {
	if advancedReadQuery(raw) {
		if err := validateAdvancedQueryWire(raw, nil, 100); err != nil {
			return err
		}
		type plain APIArtifactQueryInput
		var value plain
		if err := json.Unmarshal(raw, &value, json.RejectUnknownMembers(true)); err != nil {
			return err
		}
		*in = APIArtifactQueryInput(value)
		return in.Validate()
	}
	*in = APIArtifactQueryInput{}
	type plain APIArtifactQueryInput
	if err := strictAPIObject(raw, []string{"revisionId"}, []string{"sourceNodeId", "limit", "cursor"}, (*plain)(in)); err != nil {
		return err
	}
	if in.Limit == 0 {
		m, _ := relationalObject(raw)
		if m["limit"] != nil {
			return invalid("limit", "Explicit limit must be 1–100")
		}
	}
	return in.Validate()
}
func ValidateAPIArtifactVector(pins []ArtifactPin, bindings []APIArtifactBinding) error {
	apis := map[string]ArtifactPin{}
	for _, p := range pins {
		if p.Kind != "api_design" {
			continue
		}
		if !ValidAPIArtifactID(p.ID) || !ValidAPIArtifactID(p.RevisionID) || !validHash(p.ContentHash) {
			return invalid("pins", "Invalid exact API pin")
		}
		if _, ok := apis[p.ID]; ok {
			return invalid("pins", "Duplicate artifact pin")
		}
		apis[p.ID] = p
	}
	if len(pins) > MaxAPIArtifactPins || len(bindings) > MaxAPIArtifactBindings {
		return invalid("pins", "API pin vector exceeds bounds")
	}
	sources := map[string]bool{}
	counts := map[string]int{}
	for _, b := range bindings {
		if err := b.Validate(); err != nil {
			return err
		}
		p, ok := apis[b.Ref.ArtifactID]
		if !ok || p.RevisionID != b.Ref.RevisionID || p.ContentHash != b.Ref.ContentHash {
			return invalid("binding", "Binding differs from exact pin")
		}
		if sources[b.SourceNodeID] {
			return invalid("binding", "Duplicate source binding")
		}
		sources[b.SourceNodeID] = true
		counts[p.ID]++
	}
	for id := range apis {
		if counts[id] == 0 {
			return invalid("pins", "API pin must contain bindings")
		}
	}
	return nil
}

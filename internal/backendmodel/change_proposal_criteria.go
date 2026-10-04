package backendmodel

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"
)

type ChangeCriterion struct {
	Key          string               `json:"key"`
	Kind         string               `json:"kind"`
	Required     bool                 `json:"required"`
	Description  string               `json:"description"`
	RecordType   string               `json:"recordType,omitempty"`
	ID           string               `json:"id,omitempty"`
	ObjectKind   string               `json:"objectKind,omitempty"`
	EdgeKind     string               `json:"edgeKind,omitempty"`
	From         string               `json:"from,omitempty"`
	To           string               `json:"to,omitempty"`
	Selector     jsontext.Value       `json:"selector,omitzero"`
	Expected     *SourcePropertyValue `json:"expected,omitzero"`
	Artifact     *ArtifactPin         `json:"artifact,omitzero"`
	ExpectedHash string               `json:"expectedHash,omitempty"`
	TargetIDs    []string             `json:"targetIds,omitzero"`
	Attachment   *TestAttachmentRef   `json:"attachment,omitzero"`
}

func changeCriterionFields(c ChangeCriterion) ([]string, error) {
	fields := make([]string, 4, 10)
	copy(fields, []string{"key", "kind", "required", "description"})
	var additional string
	switch c.Kind {
	case "object_exists", "object_absent":
		additional = "recordType id objectKind"
	case "edge_exists":
		additional = "id edgeKind from to"
	case "field_equals":
		additional = "recordType id selector expected"
	case "artifact_object_matches":
		additional = "artifact selector expectedHash"
	case "test_attachment":
		additional = "targetIds attachment"
	case "runtime_check":
		additional = "targetIds"
	default:
		return nil, invalid("kind", "Unknown criterion kind")
	}
	return append(fields, strings.Fields(additional)...), nil
}
func (c ChangeCriterion) MarshalJSON() ([]byte, error) {
	type plain ChangeCriterion
	return json.Marshal(plain(c))
}
func (c *ChangeCriterion) UnmarshalJSON(b []byte) error {
	type plain ChangeCriterion
	var v plain
	if err := json.Unmarshal(b, &v, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	d := ChangeCriterion(v)
	fields, err := changeCriterionFields(d)
	if err != nil {
		return err
	}
	m, err := relationalObject(b)
	if err != nil {
		return err
	}
	if err = relationalFields(m, fields, nil); err != nil {
		return err
	}
	for k, raw := range m {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return invalid(k, "Null is not accepted")
		}
	}

	if d.Kind == "field_equals" {
		expected, err := relationalObject(m["expected"])
		if err != nil {
			return err
		}
		if err = relationalFields(expected, []string{"present"}, []string{"value"}); err != nil {
			return err
		}
		if string(expected["present"]) != "true" && string(expected["present"]) != "false" {
			return invalid("expected/present", "Expected an explicit boolean")
		}
	}
	if err = d.Validate(); err != nil {
		return err
	}
	*c = d
	return nil
}
func (c ChangeCriterion) Validate() error {
	if !externalKey(c.Key) || !validAPIText(c.Description, 1, 4096) {
		return invalid("criteria", "Bounded key and description are required")
	}
	if _, err := changeCriterionFields(c); err != nil {
		return err
	}
	switch c.Kind {
	case "object_exists", "object_absent":
		return c.validateObjectCriterion()
	case "field_equals":
		return c.validateFieldCriterion()
	case "edge_exists":
		if !ValidID(c.ID) || !ValidID(c.From) || !ValidID(c.To) || !slices.Contains(SupportedEdgeKindsForProfile(ComposedProfile), c.EdgeKind) {
			return invalid("criteria", "Invalid exact edge target")
		}
	case "artifact_object_matches":
		return c.validateArtifactCriterion()
	case "test_attachment", "runtime_check":
		return c.validateCheckCriterion()
	}
	return nil
}
func (c ChangeCriterion) validateCriterionTarget() error {
	if !ValidID(c.ID) || !slices.Contains([]string{"node", "edge"}, c.RecordType) {
		return invalid("criteria", "Invalid object target")
	}
	return nil
}
func (c ChangeCriterion) validateObjectCriterion() error {
	if err := c.validateCriterionTarget(); err != nil {
		return err
	}
	kinds := SupportedNodeKindsForProfile(ComposedProfile)
	if c.RecordType == "edge" {
		kinds = SupportedEdgeKindsForProfile(ComposedProfile)
	}
	if !slices.Contains(kinds, c.ObjectKind) {
		return invalid("objectKind", "Unknown object kind")
	}
	return nil
}
func (c ChangeCriterion) validateFieldCriterion() error {
	if err := c.validateCriterionTarget(); err != nil {
		return err
	}
	var selector EffectivePropertySelector
	if err := json.Unmarshal(c.Selector, &selector); err != nil {
		return err
	}
	if c.Expected == nil {
		return invalid("expected", "Typed expected value required")
	}
	if c.Expected.Present != (len(c.Expected.Value) > 0) {
		return invalid("expected", "Present values require value; absent values forbid it")
	}
	return nil
}
func (c ChangeCriterion) validateArtifactCriterion() error {
	if c.Artifact == nil || !validHash(c.ExpectedHash) {
		return invalid("criteria", "Exact artifact pin and expected hash required")
	}
	if err := validateChangeArtifactPin(*c.Artifact); err != nil {
		return err
	}
	if c.Artifact.Kind == "api_design" {
		var selector APIArtifactSelector
		return json.Unmarshal(c.Selector, &selector)
	}
	var selector EditorSelector
	return json.Unmarshal(c.Selector, &selector)
}
func (c ChangeCriterion) validateCheckCriterion() error {
	if c.TargetIDs == nil || len(c.TargetIDs) > 64 {
		return invalid("targetIds", "Expected at most64 targets")
	}
	seen := map[string]bool{}
	for _, id := range c.TargetIDs {
		if !ValidID(id) || seen[id] {
			return invalid("targetIds", "Expected distinct canonical UUIDs")
		}
		seen[id] = true
	}
	if c.Kind == "test_attachment" {
		if c.Attachment == nil {
			return invalid("attachment", "Exact test attachment required")
		}
		return c.Attachment.Validate()
	}
	return nil
}

func validateChangeCriteria(criteria []ChangeCriterion) error {
	if criteria == nil || len(criteria) > MaxChangeProposalCriteria {
		return invalid("criteria", "Expected a complete list of at most100 criteria")
	}
	keys := map[string]bool{}
	for _, c := range criteria {
		if keys[c.Key] {
			return invalid("key", "Criterion keys must be unique")
		}
		keys[c.Key] = true
		if err := c.Validate(); err != nil {
			return err
		}
	}
	return nil
}
func validateChangeArtifactPin(pin ArtifactPin) error {
	if err := (ArtifactKey{Kind: pin.Kind, ID: pin.ID}).Validate(); err != nil {
		return err
	}
	if !ValidAPIArtifactID(pin.RevisionID) || !validHash(pin.ContentHash) {
		return invalid("artifact", "Exact revision and contentHash required")
	}
	return nil
}
func (r TestAttachmentRef) Validate() error {
	switch r.Kind {
	case "source":
		if !ValidID(r.RevisionID) || !ValidID(r.RepositoryID) || !ValidID(r.SnapshotID) || !validHash(r.ContentHash) || r.Artifact != nil {
			return invalid("attachment", "Expected an exact source locator")
		}
		if !validPath(r.File) || r.StartLine < 1 || r.EndLine < r.StartLine {
			return invalid("attachment", "Expected normalized relative file and positive ordered line bounds")
		}
		if r.Symbol != "" && !validAPIText(r.Symbol, 1, 4096) {
			return invalid("symbol", "Symbol exceeds bounds")
		}
	case "artifact":
		if r.Artifact == nil || r.Artifact.Kind != "design_scenario" || r.JSONPointer != "" {
			return invalid("attachment", "Only the exact design scenario root is supported")
		}
		return validateChangeArtifactPin(*r.Artifact)
	default:
		return invalid("attachment/kind", "Unknown attachment kind")
	}
	return nil
}
func (r TestAttachmentRef) MarshalJSON() ([]byte, error) {
	if r.Kind == "artifact" {
		return json.Marshal(struct {
			Kind        string       `json:"kind"`
			Artifact    *ArtifactPin `json:"artifact"`
			JSONPointer string       `json:"jsonPointer"`
		}{Kind: r.Kind, Artifact: r.Artifact, JSONPointer: r.JSONPointer})
	}
	type plain TestAttachmentRef
	return json.Marshal(plain(r))
}
func (r *TestAttachmentRef) UnmarshalJSON(b []byte) error {
	type plain TestAttachmentRef
	var v plain
	if err := json.Unmarshal(b, &v, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	fields, optional := []string{"kind", "artifact", "jsonPointer"}, []string{}
	if v.Kind == "source" {
		fields = []string{"kind", "revisionId", "repositoryId", "snapshotId", "file", "contentHash", "startLine", "endLine"}
		optional = []string{"symbol"}
	}
	m, err := relationalObject(b)
	if err != nil {
		return err
	}
	if err = relationalFields(m, fields, optional); err != nil {
		return err
	}
	for k, raw := range m {
		if string(raw) == "null" {
			return invalid(k, "Null is not accepted")
		}
	}
	d := TestAttachmentRef(v)
	if err = d.Validate(); err != nil {
		return err
	}
	*r = d
	return nil
}

type TestAttachmentRef struct {
	Kind         string       `json:"kind"`
	RevisionID   string       `json:"revisionId,omitempty"`
	RepositoryID string       `json:"repositoryId,omitempty"`
	SnapshotID   string       `json:"snapshotId,omitempty"`
	File         string       `json:"file,omitempty"`
	ContentHash  string       `json:"contentHash,omitempty"`
	StartLine    int64        `json:"startLine,omitzero"`
	EndLine      int64        `json:"endLine,omitzero"`
	Symbol       string       `json:"symbol,omitempty"`
	Artifact     *ArtifactPin `json:"artifact,omitzero"`
	JSONPointer  string       `json:"jsonPointer,omitempty"`
}

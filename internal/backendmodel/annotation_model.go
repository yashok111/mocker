package backendmodel

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxAnnotationBodyBytes    = 16 << 10
	MaxAnnotationIdentities   = 1000
	MaxAnnotationTextBytes    = 8 << 20
	MaxProjectCommands        = 100
	MaxProjectCommandBytes    = 1 << 20
	DefaultAnnotationPageSize = 100
	MaxAnnotationPageSize     = 500
)

type AnnotationTarget struct {
	RecordType string `json:"recordType"`
	ID         string `json:"id"`
	RevisionID string `json:"revisionId,omitempty"`
}
type Annotation struct {
	ID           string           `json:"id"`
	Target       AnnotationTarget `json:"target"`
	Body         string           `json:"body"`
	Author       string           `json:"author"`
	CreatedAt    time.Time        `json:"createdAt"`
	UpdatedAt    time.Time        `json:"updatedAt"`
	TargetStatus string           `json:"targetStatus"`
}
type AnnotationListInput struct {
	AnnotationID, TargetID, RecordType, RevisionID string
	Orphaned                                       *bool
	Limit                                          int
	Cursor                                         string
}
type AnnotationPage struct {
	ProjectID      string       `json:"projectId"`
	ProjectVersion int64        `json:"projectVersion"`
	Items          []Annotation `json:"items"`
	NextCursor     string       `json:"nextCursor"`
}

func annotationObject(raw []byte, required, optional []string) (map[string]jsontext.Value, error) {
	fields, err := relationalObject(raw)
	if err != nil {
		return nil, invalid("body", "Expected a strict JSON object")
	}
	if err := relationalFields(fields, required, optional); err != nil {
		return nil, invalid("body", err.Error())
	}
	for name, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, invalid(name, "Null is not allowed")
		}
	}
	return fields, nil
}
func (target *AnnotationTarget) UnmarshalJSON(raw []byte) error {
	fields, err := annotationObject(raw, []string{"recordType", "id"}, []string{"revisionId"})
	if err != nil {
		return err
	}
	type wire AnnotationTarget
	var out wire
	if err := json.Unmarshal(raw, &out, json.RejectUnknownMembers(true)); err != nil {
		return invalid("target", err.Error())
	}
	if _, present := fields["revisionId"]; present && out.RevisionID == "" {
		return invalid("target.revisionId", "Use a canonical nonzero UUID")
	}
	if err := validateAnnotationTarget(AnnotationTarget(out)); err != nil {
		return err
	}
	*target = AnnotationTarget(out)
	return nil
}
func validateAnnotationTarget(target AnnotationTarget) error {
	if target.RecordType != "node" && target.RecordType != "edge" {
		return invalid("target.recordType", "Expected node or edge")
	}
	if !ValidID(target.ID) {
		return invalid("target.id", "Use a canonical nonzero UUID")
	}
	if target.RevisionID != "" && !ValidID(target.RevisionID) {
		return invalid("target.revisionId", "Use a canonical nonzero UUID")
	}
	return nil
}

// MarshalJSON preserves the exact pre-annotation rename encoding and field order.
func (c Command) MarshalJSON() ([]byte, error) {
	switch c.Type {
	case "rename_project":
		return json.Marshal(struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}{Type: c.Type, Name: c.Name})
	case "create_annotation", "update_annotation":
		return json.Marshal(struct {
			Type         string            `json:"type"`
			AnnotationID string            `json:"annotationId"`
			Target       *AnnotationTarget `json:"target"`
			Body         string            `json:"body"`
		}{Type: c.Type, AnnotationID: c.AnnotationID, Target: c.Target, Body: c.Body})
	case "remove_annotation":
		return json.Marshal(struct {
			Type         string `json:"type"`
			AnnotationID string `json:"annotationId"`
		}{Type: c.Type, AnnotationID: c.AnnotationID})
	default:
		return nil, invalid("commands.type", "Unsupported project command")
	}
}
func (c *Command) UnmarshalJSON(raw []byte) error {
	fields, err := relationalObject(raw)
	if err != nil {
		return invalid("commands", "Expected a strict project command object")
	}
	var kind string
	if err := json.Unmarshal(fields["type"], &kind); err != nil {
		return invalid("commands.type", "Command type is required")
	}
	required := []string{"type"}
	switch kind {
	case "rename_project":
		required = append(required, "name")
	case "create_annotation", "update_annotation":
		required = append(required, "annotationId", "target", "body")
	case "remove_annotation":
		required = append(required, "annotationId")
	default:
		return invalid("commands.type", "Unsupported project command")
	}
	if _, err := annotationObject(raw, required, nil); err != nil {
		return err
	}
	type wire Command
	var out wire
	if err := json.Unmarshal(raw, &out, json.RejectUnknownMembers(true)); err != nil {
		return invalid("commands", err.Error())
	}
	if err := validateProjectCommand(Command(out)); err != nil {
		return err
	}
	*c = Command(out)
	return nil
}
func (in *CommandsInput) UnmarshalJSON(raw []byte) error {
	if len(raw) > MaxProjectCommandBytes {
		return invalid("body", "Project commands exceed 1 MiB")
	}
	if _, err := annotationObject(raw, []string{"expectedVersion", "idempotencyKey", "commands"}, nil); err != nil {
		return err
	}
	type wire CommandsInput
	var out wire
	if err := json.Unmarshal(raw, &out, json.RejectUnknownMembers(true)); err != nil {
		return invalid("body", err.Error())
	}
	if out.ExpectedVersion <= 0 {
		return invalid("expectedVersion", "expectedVersion must be positive")
	}
	if err := validateKey(out.IdempotencyKey); err != nil {
		return err
	}
	if _, err := normalizeProjectCommands(out.Commands); err != nil {
		return err
	}
	*in = CommandsInput(out)
	return nil
}
func validateProjectCommand(c Command) error {
	switch c.Type {
	case "rename_project":
		if c.AnnotationID != "" || c.Target != nil || c.Body != "" {
			return invalid("commands", "rename_project only accepts name")
		}
		_, err := normalizeName(c.Name)
		return err
	case "create_annotation", "update_annotation":
		if c.Name != "" || c.Target == nil {
			return invalid("commands", "Annotation creation and replacement require target and body")
		}
		if err := validateAnnotationTarget(*c.Target); err != nil {
			return err
		}
		if !utf8.ValidString(c.Body) || strings.TrimSpace(c.Body) == "" || len(c.Body) > MaxAnnotationBodyBytes {
			return invalid("commands.body", "Use nonblank UTF-8 text of at most 16 KiB")
		}
	case "remove_annotation":
		if c.Name != "" || c.Target != nil || c.Body != "" {
			return invalid("commands", "remove_annotation only accepts annotationId")
		}
	default:
		return invalid("commands.type", "Unsupported project command")
	}
	if !ValidID(c.AnnotationID) {
		return invalid("commands.annotationId", "Use a canonical nonzero UUID")
	}
	return nil
}
func normalizeProjectCommands(commands []Command) ([]Command, error) {
	if len(commands) == 0 || len(commands) > MaxProjectCommands {
		return nil, invalid("commands", "Use 1–100 project commands")
	}
	normalized := slices.Clone(commands)
	renamed := false
	for i, c := range normalized {
		if err := validateProjectCommand(c); err != nil {
			return nil, err
		}
		if c.Type == "rename_project" {
			if renamed {
				return nil, invalid("commands", "Use at most one rename_project per batch")
			}
			renamed = true
			normalized[i].Name, _ = normalizeName(c.Name)
		}
		if c.Target != nil {
			normalized[i].Target = new(*c.Target)
		}
	}
	return normalized, nil
}

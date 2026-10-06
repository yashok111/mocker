// Package backendmodel stores backend projects and their immutable model revisions.
package backendmodel

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
	"uuid"
)

const (
	SchemaVersion   = "1"
	MaxNameLength   = 200
	MaxKeyLength    = 128
	DefaultPageSize = 50
	MaxPageSize     = 100
)

// Features lists only the currently implemented workbench operations.
func Features() []string {
	return []string{"backend-projects", "backend-project-metadata", "backend-revisions", "backend-graph-query", "backend-source-import", "backend-source-reconcile", "backend-revision-compare"}
}

type Repository struct {
	ID          string `json:"id"`
	LogicalName string `json:"logicalName"`
}

type Project struct {
	ID                string       `json:"id"`
	Name              string       `json:"name"`
	Version           int64        `json:"version"`
	CurrentRevisionID string       `json:"currentRevisionId"`
	Repositories      []Repository `json:"repositories"`
	Capabilities      []string     `json:"capabilities"`
	CreatedAt         time.Time    `json:"createdAt"`
	UpdatedAt         time.Time    `json:"updatedAt"`
}

type Coverage struct {
	Status       string   `json:"status"`
	Denominator  *int64   `json:"denominator"`
	KnownObjects int64    `json:"knownObjects"`
	Gaps         []string `json:"gaps"`
}

type ArtifactPin struct {
	ContentHash string `json:"contentHash,omitempty"`
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	RevisionID  string `json:"revisionId"`
}

type Revision struct {
	ImportOrigin      *PortableAttribution `json:"importOrigin,omitzero"`
	ID                string               `json:"id"`
	ProjectID         string               `json:"projectId"`
	ParentRevisionID  *string              `json:"parentRevisionId"`
	SchemaVersion     string               `json:"schemaVersion"`
	SemanticHash      string               `json:"semanticHash"`
	SourceSnapshotIDs []string             `json:"sourceSnapshotIds"`
	ArtifactPins      []ArtifactPin        `json:"artifactPins"`
	Coverage          Coverage             `json:"coverage"`
	Author            string               `json:"author"`
	Summary           string               `json:"summary"`
	CreatedAt         time.Time            `json:"createdAt"`
}

type CreateInput struct {
	Name           string `json:"name"`
	IdempotencyKey string `json:"idempotencyKey"`
}

type Command struct {
	Type         string            `json:"type"`
	Name         string            `json:"name"`
	AnnotationID string            `json:"annotationId,omitempty"`
	Target       *AnnotationTarget `json:"target,omitempty"`
	Body         string            `json:"body,omitempty"`
}

type CommandsInput struct {
	ExpectedVersion int64     `json:"expectedVersion"`
	IdempotencyKey  string    `json:"idempotencyKey"`
	Commands        []Command `json:"commands"`
}

type ListInput struct {
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type ProjectPage struct {
	Items      []Project `json:"items"`
	NextCursor string    `json:"nextCursor"`
}

type RevisionPage struct {
	Items      []Revision `json:"items"`
	NextCursor string     `json:"nextCursor"`
}

type FaultError struct {
	Status         int            `json:"-"`
	Code           string         `json:"code"`
	Message        string         `json:"message"`
	Details        map[string]any `json:"details,omitempty"`
	Retryable      bool           `json:"retryable"`
	CurrentVersion int64          `json:"currentVersion,omitzero"`
}

func (f *FaultError) Error() string { return f.Message }

func invalid(field, message string) *FaultError {
	return &FaultError{Status: 400, Code: "backend_invalid", Message: message, Details: map[string]any{"field": field}}
}

func notFound() *FaultError {
	return &FaultError{Status: 404, Code: "backend_not_found", Message: "Backend project or revision not found"}
}

func ValidID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u != uuid.Nil() && u.String() == id
}

func normalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxNameLength || strings.ContainsFunc(name, unicode.IsControl) {
		return "", invalid("name", fmt.Sprintf("Name must contain 1–%d characters without control characters", MaxNameLength))
	}
	return name, nil
}

func validateKey(key string) error {
	if len(key) == 0 || len(key) > MaxKeyLength || strings.ContainsFunc(key, func(r rune) bool { return r < 33 || r > 126 }) {
		return invalid("idempotencyKey", "Use 1–128 printable ASCII characters without spaces for idempotencyKey")
	}
	return nil
}

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

// Features lists every implemented workbench capability: the features of
// get_backend_capabilities and the capabilities of each project resource.
// Review 2026-10-06, F90: projects carried only the original seven while the
// capabilities endpoint appended the rest in the admin plane, so a client
// reading project.capabilities concluded that annotations, relational import
// or sync were unsupported. The list lives here, once, for both readers.
func Features() []string {
	return []string{"backend-projects", "backend-project-metadata", "backend-revisions", "backend-graph-query", "backend-source-import", "backend-source-reconcile", "backend-revision-compare",
		"backend-scenario-measurements", "backend-observed-impact", "backend-observed-sequences", "backend-observations", "backend-observation-correlation", "backend-replay", "backend-test-profiles", "backend-replay-bindings",
		"backend-materialization", "backend-diagram-svg", "backend-portable", "backend-artifact-context-v3", "backend-namespaced-artifact-query",
		"backend-architecture", "backend-interactions", "backend-lifecycle", "backend-business-map", "backend-diagrams", "backend-diagram-views",
		"backend-relational-import", "backend-database-query", "backend-database-er",
		"backend-db-proposals", "backend-db-typed-edits", "backend-runtime-flow-import",
		"backend-flow-query", "backend-data-access-query", "backend-saved-views",
		"backend-field-lineage-import", "backend-field-lineage-query", "backend-api-artifact-pins",
		"backend-editor-projections", "backend-events-import", "backend-events-query",
		"backend-annotations", "backend-source-sync", "backend-source-incremental-sync",
		"backend-source-assertions", "backend-import-candidate", "backend-change-proposals",
		"backend-change-typed-edits", "backend-representations", "backend-saved-views-v2",
		"backend-analysis-diagnostics", "backend-finding-review", "backend-analysis-jobs", "backend-analysis-diff", "backend-analysis-impact", "backend-change-rebase", "backend-change-ready",
		"backend-change-package", "backend-conformance", "backend-endpoint-review", "backend-change-implemented", "backend-change-archive", "backend-change-unarchive",
	}
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

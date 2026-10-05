package backendmodel

import (
	"context"
	"encoding/json/v2"
)

const DiagramDocumentVersion = "backend-diagram-v1"

type DiagramPin struct {
	ID          string `json:"id"`
	Version     int64  `json:"version"`
	ContentHash string `json:"contentHash"`
}
type DiagramEvidenceRef struct {
	RevisionID string `json:"revisionId"`
	EvidenceID string `json:"evidenceId"`
	SubjectID  string `json:"subjectId"`
}
type DiagramOrigin struct {
	Kind     string               `json:"kind"`
	Evidence []DiagramEvidenceRef `json:"evidence,omitzero"`
	Reason   string               `json:"reason,omitempty"`
}
type DiagramRef struct {
	Kind       string                     `json:"kind"`
	RecordType string                     `json:"recordType,omitempty"`
	ID         string                     `json:"id,omitempty"`
	Locator    *ArtifactProjectionLocator `json:"locator,omitzero"`
	RowID      string                     `json:"rowId,omitempty"`
}
type DiagramGap struct {
	ID          string `json:"id"`
	SubjectID   string `json:"subjectId"`
	Code        string `json:"code"`
	Explanation string `json:"explanation"`
}
type DiagramDocument struct {
	BusinessMap  *BusinessMapPayload `json:"-"`
	Format       string              `json:"format"`
	Kind         string              `json:"kind"`
	Target       BackendReadTarget   `json:"target"`
	Payload      ArchitecturePayload `json:"payload"`
	Lifecycle    *LifecyclePayload   `json:"-"`
	Interactions *InteractionPayload `json:"-"`
}
type DiagramProvenanceEvent struct {
	Pin       DiagramPin `json:"pin"`
	ElementID string     `json:"elementId"`
	Author    string     `json:"author"`
	At        string     `json:"at"`
}
type DiagramInherited struct {
	Pin       DiagramPin `json:"pin"`
	ElementID string     `json:"elementId"`
}
type DiagramElementProvenance struct {
	ElementID     string                 `json:"elementId"`
	Introduced    DiagramProvenanceEvent `json:"introduced"`
	LastEdited    DiagramProvenanceEvent `json:"lastEdited"`
	InheritedFrom *DiagramInherited      `json:"inheritedFrom,omitzero"`
}
type DiagramForkProvenance struct {
	Source               DiagramPin `json:"source"`
	SourceProvenanceHash string     `json:"sourceProvenanceHash"`
	Reason               string     `json:"reason"`
}
type DiagramProvenance struct {
	Format   string                     `json:"format"`
	Action   string                     `json:"action"`
	Previous *DiagramPin                `json:"previous,omitzero"`
	Fork     *DiagramForkProvenance     `json:"fork,omitzero"`
	Elements []DiagramElementProvenance `json:"elements"`
}
type DiagramVersion struct {
	receiptJSON    string
	Pin            DiagramPin        `json:"pin"`
	ProjectID      string            `json:"projectId"`
	Document       DiagramDocument   `json:"document"`
	TargetHash     string            `json:"targetHash"`
	Author         string            `json:"author"`
	CreatedAt      string            `json:"createdAt"`
	Gaps           []DiagramGap      `json:"gaps"`
	Provenance     DiagramProvenance `json:"provenance"`
	ProvenanceHash string            `json:"provenanceHash"`
}

func (v DiagramVersion) MarshalJSON() ([]byte, error) {
	if v.receiptJSON != "" {
		return []byte(v.receiptJSON), nil
	}
	type plain DiagramVersion
	return json.Marshal(plain(v))
}

type DiagramCreateInput struct {
	Document       DiagramDocument `json:"document"`
	IdempotencyKey string          `json:"idempotencyKey"`
}
type DiagramSaveInput struct {
	ExpectedVersion int64           `json:"expectedVersion"`
	Document        DiagramDocument `json:"document"`
	IdempotencyKey  string          `json:"idempotencyKey"`
}
type DiagramForkInput struct {
	Source         DiagramPin        `json:"source"`
	Target         BackendReadTarget `json:"target"`
	Architecture   *DiagramPin       `json:"architecture,omitzero"`
	Reason         string            `json:"reason"`
	IdempotencyKey string            `json:"idempotencyKey"`
}

// The authenticated transport supplies this context; author is never a wire field.
type diagramActorKey struct{}

func WithDiagramActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, diagramActorKey{}, actor)
}
func diagramActor(ctx context.Context) string {
	if actor, ok := ctx.Value(diagramActorKey{}).(string); ok && actor != "" {
		return actor
	}
	return "system"
}

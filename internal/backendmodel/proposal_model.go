package backendmodel

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"time"
)

const ProposalDocumentVersion = "proposal-relational-v1"

type Proposal struct {
	ID               string    `json:"id"`
	ProjectID        string    `json:"projectId"`
	Version          int64     `json:"version"`
	Name             string    `json:"name"`
	Status           string    `json:"status"`
	BaseRevisionID   string    `json:"baseRevisionId"`
	BaseSemanticHash string    `json:"baseSemanticHash"`
	RepositoryID     string    `json:"repositoryId"`
	DatastoreID      string    `json:"datastoreId"`
	FacetKey         string    `json:"facetKey"`
	DraftRevisionID  string    `json:"draftRevisionId"`
	DraftHash        string    `json:"draftHash"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

// ProposalCommand carries the strict tagged edit variants used by preview/apply.
type ProposalCommand struct {
	Type              string                   `json:"type"`
	CommandID         string                   `json:"commandId"`
	Reason            string                   `json:"reason"`
	ColumnID          string                   `json:"columnId,omitempty"`
	Nullable          *bool                    `json:"nullable,omitzero"`
	Action            string                   `json:"action,omitempty"`
	Name              string                   `json:"name,omitempty"`
	TableID           string                   `json:"tableId,omitempty"`
	ConstraintID      string                   `json:"constraintId,omitempty"`
	TargetTableID     string                   `json:"targetTableId,omitempty"`
	ColumnPairs       []DatabaseColumnPair     `json:"columnPairs,omitempty"`
	UpdateAction      string                   `json:"updateAction,omitempty"`
	DeleteAction      string                   `json:"deleteAction,omitempty"`
	MatchType         string                   `json:"matchType,omitempty"`
	Deferrable        *bool                    `json:"deferrable,omitzero"`
	InitiallyDeferred *bool                    `json:"initiallyDeferred,omitzero"`
	Criteria          []ProposalCriterionInput `json:"criteria,omitempty"`
}

type ProposalCriterionInput struct {
	Key         string   `json:"key"`
	Kind        string   `json:"kind"`
	TargetIDs   []string `json:"targetIds"`
	Description string   `json:"description"`
}

type ProposalCriterion struct {
	Key         string   `json:"key"`
	Kind        string   `json:"kind"`
	TargetIDs   []string `json:"targetIds"`
	Description string   `json:"description"`
	Origin      string   `json:"origin"`
	Status      string   `json:"status"`
	CommandID   string   `json:"commandId"`
}

type ProposalBasis struct {
	RevisionID   string `json:"revisionId"`
	SemanticHash string `json:"semanticHash"`
	SubjectID    string `json:"subjectId"`
	FacetKey     string `json:"facetKey"`
}

type ProposalPropertyOrigin struct {
	Kind        string         `json:"kind"`
	CommandID   string         `json:"commandId,omitempty"`
	Reason      string         `json:"reason,omitempty"`
	Base        *ProposalBasis `json:"base,omitzero"`
	Property    string         `json:"property,omitempty"`
	EvidenceIDs []string       `json:"evidenceIds"`
}

type ProposalOverlay struct {
	SubjectID       string                            `json:"subjectId"`
	RecordType      string                            `json:"recordType"`
	Kind            string                            `json:"kind"`
	FacetKey        string                            `json:"facetKey"`
	Base            *ProposalBasis                    `json:"base"`
	Values          map[string]jsontext.Value         `json:"values"`
	PropertyOrigins map[string]ProposalPropertyOrigin `json:"propertyOrigins"`
	CommandID       string                            `json:"commandId"`
	Reason          string                            `json:"reason"`
	Name            string                            `json:"name,omitempty"`
	ParentID        *string                           `json:"parentId,omitzero"`
	FromID          string                            `json:"fromId,omitempty"`
	ToID            string                            `json:"toId,omitempty"`
}

type ProposalRevision struct {
	ImportOrigin      *PortableAttribution `json:"importOrigin,omitzero"`
	ID                string               `json:"id"`
	ProposalID        string               `json:"proposalId"`
	ParentRevisionID  *string              `json:"parentRevisionId"`
	DocumentVersion   string               `json:"documentVersion"`
	SemanticHash      string               `json:"semanticHash"`
	BaseRevisionID    string               `json:"baseRevisionId"`
	BaseSemanticHash  string               `json:"baseSemanticHash"`
	SourceSnapshotIDs []string             `json:"sourceSnapshotIds"`
	ArtifactPins      []ArtifactPin        `json:"artifactPins"`
	Commands          []ProposalCommand    `json:"commands"`
	Overlays          []ProposalOverlay    `json:"overlays"`
	Criteria          []ProposalCriterion  `json:"criteria"`
	Author            string               `json:"author"`
	Summary           string               `json:"summary"`
	CreatedAt         time.Time            `json:"createdAt"`
}

type ProposalRevisionSummary struct {
	ID               string    `json:"id"`
	ParentRevisionID *string   `json:"parentRevisionId"`
	SemanticHash     string    `json:"semanticHash"`
	Author           string    `json:"author"`
	Summary          string    `json:"summary"`
	CreatedAt        time.Time `json:"createdAt"`
}

type ProposalDetail struct {
	receiptJSON               string
	Proposal                  Proposal                  `json:"proposal"`
	Revision                  ProposalRevision          `json:"revision"`
	History                   []ProposalRevisionSummary `json:"history"`
	NextCursor                string                    `json:"nextCursor"`
	LastApplyReceipt          jsontext.Value            `json:"lastApplyReceipt"`
	BaseOutdated              bool                      `json:"baseOutdated"`
	CurrentSourceRevisionID   string                    `json:"currentSourceRevisionId"`
	CurrentSourceSemanticHash string                    `json:"currentSourceSemanticHash"`
	EffectiveGraphHash        string                    `json:"effectiveGraphHash"`
}

// Acknowledged receipts keep the original representation. Ordinary reads derive
// source-head warnings anew and never set receiptJSON.
func (d ProposalDetail) MarshalJSON() ([]byte, error) {
	if d.receiptJSON != "" {
		return []byte(d.receiptJSON), nil
	}
	type detail ProposalDetail
	return json.Marshal(detail(d))
}

type ProposalPage struct {
	Items      []Proposal `json:"items"`
	NextCursor string     `json:"nextCursor"`
}

type CreateProposalInput struct {
	Name           string `json:"name"`
	BaseRevisionID string `json:"baseRevisionId"`
	RepositoryID   string `json:"repositoryId"`
	DatastoreID    string `json:"datastoreId"`
	FacetKey       string `json:"facetKey"`
	IdempotencyKey string `json:"idempotencyKey"`
}

func (in *CreateProposalInput) UnmarshalJSON(b []byte) error {
	m, err := relationalObject(b)
	if err != nil {
		return invalid("body", "Expected a strict proposal creation object")
	}
	if err := relationalFields(m, []string{"name", "baseRevisionId", "repositoryId", "datastoreId", "facetKey", "idempotencyKey"}, nil); err != nil {
		return invalid("body", err.Error())
	}
	for key, raw := range m {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 || raw[0] != '"' {
			return invalid(key, "Expected a string")
		}
	}
	type input CreateProposalInput
	var decoded input
	if err := json.Unmarshal(b, &decoded, json.RejectUnknownMembers(true)); err != nil {
		return invalid("body", err.Error())
	}
	*in = CreateProposalInput(decoded)
	return nil
}

type ProposalListInput struct {
	BaseRevisionID string `json:"baseRevisionId,omitempty"`
	Status         string `json:"status,omitempty"`
	Limit          int    `json:"limit,omitzero"`
	Cursor         string `json:"cursor,omitempty"`
}

type GetProposalInput struct {
	ProposalRevisionID string `json:"proposalRevisionId,omitempty"`
	Limit              int    `json:"limit,omitzero"`
	Cursor             string `json:"cursor,omitempty"`
}

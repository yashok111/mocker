// Package apidesign owns authored contracts and their immutable publication history.
package apidesign

import "errors"

var (
	ErrNotFound  = errors.New("design not found")
	ErrConflict  = errors.New("design version conflict")
	ErrInvalid   = errors.New("invalid API document")
	ErrForbidden = errors.New("publication requires a UI session")
)

type Design struct {
	ID                   int64  `json:"id"`
	Name                 string `json:"name"`
	Version              int64  `json:"version"`
	DraftWorkspaceID     int64  `json:"draftWorkspaceId"`
	PublishedWorkspaceID int64  `json:"publishedWorkspaceId"`
	DraftURL             string `json:"draftUrl"`
	PublishedURL         string `json:"publishedUrl"`
	DraftRevisionID      int64  `json:"draftRevisionId"`
	PublishedRevisionID  *int64 `json:"publishedRevisionId"`
	LatestReviewID       *int64 `json:"latestReviewId"`
	CreatedAt            int64  `json:"createdAt"`
	UpdatedAt            int64  `json:"updatedAt"`
}
type RevisionSummary struct {
	ID          int64  `json:"id"`
	DesignID    int64  `json:"designId"`
	Version     int64  `json:"version"`
	Hash        string `json:"hash"`
	Source      string `json:"source"`
	Summary     string `json:"summary"`
	ChangeSetID *int64 `json:"changeSetId"`
	CreatedAt   int64  `json:"createdAt"`
}
type Revision struct {
	RevisionSummary
	Document string `json:"document"`
}
type ChangeSet struct {
	ID             int64  `json:"id"`
	DesignID       int64  `json:"designId"`
	Title          string `json:"title"`
	BaseRevisionID int64  `json:"baseRevisionId"`
	Status         string `json:"status"`
	Source         string `json:"source"`
	CreatedAt      int64  `json:"createdAt"`
}
type Review struct {
	ID             int64  `json:"id"`
	DesignID       int64  `json:"designId"`
	RevisionID     int64  `json:"revisionId"`
	BaseRevisionID int64  `json:"baseRevisionId"`
	Status         string `json:"status"`
	Summary        string `json:"summary"`
	Source         string `json:"source"`
	CreatedAt      int64  `json:"createdAt"`
}
type Release struct {
	ID         int64 `json:"id"`
	DesignID   int64 `json:"designId"`
	RevisionID int64 `json:"revisionId"`
	ReviewID   int64 `json:"reviewId"`
	Number     int64 `json:"number"`
	CreatedAt  int64 `json:"createdAt"`
}
type Detail struct {
	Design     Design            `json:"design"`
	Draft      Revision          `json:"draft"`
	Published  *Revision         `json:"published"`
	Revisions  []RevisionSummary `json:"revisions"`
	ChangeSets []ChangeSet       `json:"changeSets"`
	Reviews    []Review          `json:"reviews"`
	Releases   []Release         `json:"releases"`
}
type Diagnostic struct {
	Pointer  string `json:"pointer"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}
type InvalidError struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
}

func (e *InvalidError) Error() string { return ErrInvalid.Error() }
func (e *InvalidError) Unwrap() error { return ErrInvalid }

type ConflictError struct {
	Version         int64 `json:"version"`
	DraftRevisionID int64 `json:"draftRevisionId"`
}

func (e *ConflictError) Error() string { return ErrConflict.Error() }
func (e *ConflictError) Unwrap() error { return ErrConflict }

// Package designscenario owns persisted sequence-design documents and their
// immutable revision history. It is intentionally separate from the runtime
// workspace snapshots in internal/scenarios.
package designscenario

import (
	"errors"

	"github.com/yashok111/mocker/internal/jsonx"
)

var (
	ErrNotFound = errors.New("design scenario not found")
	ErrConflict = errors.New("design scenario version conflict")
	ErrInvalid  = errors.New("invalid design scenario")
	ErrTooLarge = errors.New("design scenario is too large")
)

type Scenario struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Version         int64  `json:"version"`
	DraftRevisionID int64  `json:"draftRevisionId"`
	CreatedAt       int64  `json:"createdAt"`
	UpdatedAt       int64  `json:"updatedAt"`
}

type RevisionSummary struct {
	ID         int64  `json:"id"`
	ScenarioID int64  `json:"scenarioId"`
	Version    int64  `json:"version"`
	Hash       string `json:"hash"`
	Source     string `json:"source"`
	Summary    string `json:"summary"`
	CreatedAt  int64  `json:"createdAt"`
}

type Revision struct {
	RevisionSummary
	Document   Document          `json:"document"`
	FormDrafts map[string]string `json:"formDrafts"`
}

type Detail struct {
	Scenario        Scenario          `json:"scenario"`
	Draft           Revision          `json:"draft"`
	Revisions       []RevisionSummary `json:"revisions"`
	Diagnostics     []Diagnostic      `json:"diagnostics"`
	ContractUpdates []ContractUpdate  `json:"contractUpdates"`
}

type Diagnostic struct {
	Pointer  string `json:"pointer"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

type ContractUpdate struct {
	ContractID string `json:"contractId"`
	DesignID   int64  `json:"designId"`
	RevisionID int64  `json:"revisionId"`
	Version    int64  `json:"version"`
}

type Diff struct {
	FromRevisionID int64    `json:"fromRevisionId"`
	ToRevisionID   int64    `json:"toRevisionId"`
	Changes        []Change `json:"changes"`
}

// Change uses pointers to raw JSON so an omitted side remains distinct from a
// present JSON null value.
type Change struct {
	Pointer string            `json:"pointer"`
	Before  *jsonx.RawMessage `json:"before,omitempty"`
	After   *jsonx.RawMessage `json:"after,omitempty"`
}

type Document struct {
	FormatVersion int           `json:"formatVersion"`
	Title         string        `json:"title"`
	Participants  []Participant `json:"participants"`
	Messages      []Message     `json:"messages"`
	Fragments     []Fragment    `json:"fragments"`
	Contracts     []Contract    `json:"contracts"`
	Execution     *Execution    `json:"execution,omitempty"`
}

type Participant struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Description string   `json:"description"`
	Color       HexColor `json:"color,omitempty"`
}

type OperationBinding struct {
	ContractID   string `json:"contractId"`
	OperationKey string `json:"operationKey"`
}

type Message struct {
	ID          string            `json:"id"`
	FromID      string            `json:"fromId"`
	ToID        string            `json:"toId"`
	Kind        string            `json:"kind"`
	Label       string            `json:"label"`
	Description string            `json:"description"`
	Color       HexColor          `json:"color,omitempty"`
	ArrowColor  HexColor          `json:"arrowColor,omitempty"`
	ReplyToID   string            `json:"replyToId,omitempty"`
	Operation   *OperationBinding `json:"operation,omitempty"`
	Execution   *StepExecution    `json:"execution,omitempty"`
}

type Fragment struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Label         string `json:"label"`
	FromMessageID string `json:"fromMessageId"`
	ToMessageID   string `json:"toMessageId"`
}

type Contract struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Document jsonx.RawMessage `json:"document"`
	Mode     string           `json:"mode,omitempty"`
	Source   *ContractSource  `json:"source,omitempty"`
}

// ContractSource always identifies one exact API revision. Version is that
// revision's own version, including when RevisionID points at historical data.
type ContractSource struct {
	DesignID   int64 `json:"designId"`
	RevisionID int64 `json:"revisionId"`
	Version    int64 `json:"version"`
}

type CreateInput struct {
	Document   Document          `json:"document"`
	FormDrafts map[string]string `json:"formDrafts,omitempty"`
	Summary    string            `json:"summary,omitempty"`
	Source     string            `json:"-"`
	OwnerID    *int64            `json:"-"`
}

type SaveInput struct {
	ExpectedVersion int64             `json:"expectedVersion"`
	Document        Document          `json:"document"`
	FormDrafts      map[string]string `json:"formDrafts,omitempty"`
	Summary         string            `json:"summary,omitempty"`
	Source          string            `json:"-"`
	OwnerID         *int64            `json:"-"`
}

type CommandsInput struct {
	ExpectedVersion int64     `json:"expectedVersion"`
	Commands        []Command `json:"commands"`
	Summary         string    `json:"summary,omitempty"`
	Source          string    `json:"-"`
	OwnerID         *int64    `json:"-"`
}

// Command is the wire union for atomic scenario edits. Fields irrelevant to
// the selected Type must be absent and are rejected during validation.
type Command struct {
	Type         string       `json:"type"`
	Title        string       `json:"title,omitempty"`
	Participant  *Participant `json:"participant,omitempty"`
	Message      *Message     `json:"message,omitempty"`
	Fragment     *Fragment    `json:"fragment,omitempty"`
	Contract     *Contract    `json:"contract,omitempty"`
	ID           string       `json:"id,omitempty"`
	Index        *int         `json:"index,omitempty"`
	MessageID    string       `json:"messageId,omitempty"`
	ContractID   string       `json:"contractId,omitempty"`
	OperationKey string       `json:"operationKey,omitempty"`
	Method       string       `json:"method,omitempty"`
	Path         string       `json:"path,omitempty"`
	Label        string       `json:"label,omitempty"`
	DesignID     int64        `json:"designId,omitempty"`
	RevisionID   *int64       `json:"revisionId,omitempty"`
	Mode         string       `json:"mode,omitempty"`
}

type RestoreInput struct {
	ExpectedVersion int64  `json:"expectedVersion"`
	RevisionID      int64  `json:"revisionId"`
	Summary         string `json:"summary,omitempty"`
	Source          string `json:"-"`
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

// LinkedConflictError identifies which canvas contract could not update its
// shared API and exposes the source API's current draft fence.
type LinkedConflictError struct {
	ContractID      string `json:"contractId"`
	DesignID        int64  `json:"designId"`
	Version         int64  `json:"version"`
	DraftRevisionID int64  `json:"draftRevisionId"`
}

func (e *LinkedConflictError) Error() string { return ErrConflict.Error() }
func (e *LinkedConflictError) Unwrap() error { return ErrConflict }

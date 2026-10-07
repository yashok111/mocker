// Package backendmaterialize applies explicitly authored draft translations. It
// never executes source code, publishes an owner, or starts a simulation.
package backendmaterialize

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/store"
)

const ProfileVersion = "backend-http-draft-v1"
const MaxBytes = 1 << 20

type Target struct {
	Key             string                              `json:"key"`
	Kind            string                              `json:"kind"`
	Name            string                              `json:"name"`
	Pin             *backendmodel.NamespacedArtifactPin `json:"pin,omitzero"`
	ExpectedVersion int64                               `json:"expectedVersion"`
	Commands        []Command                           `json:"commands"`
}

// The command vocabulary is deliberately a closed union of typed owner inputs.
// Native source expressions are never parsed as executable owner commands.
type Command struct {
	Selector    *backendmodel.APIArtifactSelector   `json:"selector,omitzero"`
	Destination string                              `json:"destination,omitempty"`
	Type        string                              `json:"type"`
	APIDocument string                              `json:"apiDocument,omitempty"`
	CopyFrom    *backendmodel.NamespacedArtifactPin `json:"copyFrom,omitzero"`
	Scenario    *designscenario.Document            `json:"scenario,omitzero"`
}

type Translation struct {
	SourceID  string `json:"sourceId"`
	TargetKey string `json:"targetKey"`
	Selector  string `json:"selector"`
	Reason    string `json:"reason"`
}
type DiagramViewPin struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}
type PreviewInput struct {
	DiagramView       *DiagramViewPin                 `json:"diagramView,omitzero"`
	ProfileVersion    string                          `json:"profileVersion"`
	Target            backendmodel.BackendReadTarget  `json:"target"`
	TargetHash        string                          `json:"targetHash"`
	SourceScope       []string                        `json:"sourceScope"`
	DiagramScope      *backendmodel.DiagramScopeInput `json:"diagramScope,omitzero"`
	Targets           []Target                        `json:"targets"`
	Translations      []Translation                   `json:"translations"`
	PartialSimulation bool                            `json:"partialSimulation"`
	ExcludedIDs       []string                        `json:"excludedIds"`
	Reason            string                          `json:"reason"`
}
type ApplyInput struct {
	PreviewInput
	CandidateHash  string `json:"candidateHash"`
	IdempotencyKey string `json:"idempotencyKey"`
}
type Coverage struct {
	SourceID       string                              `json:"sourceId"`
	DiagramPin     *backendmodel.DiagramPin            `json:"diagramPin,omitzero"`
	Status         string                              `json:"status"`
	TargetKey      string                              `json:"targetKey,omitempty"`
	TargetOwnerRef *backendmodel.NamespacedArtifactPin `json:"targetOwnerRef,omitzero"`
	Selector       string                              `json:"selector,omitempty"`
	Reason         string                              `json:"reason"`
}
type Effect struct {
	TargetKey  string   `json:"targetKey"`
	Kind       string   `json:"kind"`
	DraftMock  bool     `json:"draftMock"`
	LinkedFrom []string `json:"linkedFrom"`
}
type Preview struct {
	DiagramView   *backendmodel.DiagramView  `json:"diagramView,omitzero"`
	Input         PreviewInput               `json:"input"`
	Effects       []Effect                   `json:"effects"`
	Coverage      []Coverage                 `json:"coverage"`
	DiagramScope  *backendmodel.DiagramScope `json:"diagramScope,omitzero"`
	Diagnostics   []string                   `json:"diagnostics"`
	Equivalence   string                     `json:"equivalence"`
	CandidateHash string                     `json:"candidateHash"`
	CanApply      bool                       `json:"canApply"`
}
type OwnerResult struct {
	TargetKey string                             `json:"targetKey"`
	Pin       backendmodel.NamespacedArtifactPin `json:"pin"`
	Version   int64                              `json:"version"`
}
type Receipt struct {
	Author        string `json:"author"`
	raw           string
	ID            string        `json:"id"`
	ProjectID     string        `json:"projectId"`
	CandidateHash string        `json:"candidateHash"`
	RequestHash   string        `json:"requestHash"`
	Owners        []OwnerResult `json:"owners"`
	Coverage      []Coverage    `json:"coverage"`
	Equivalence   string        `json:"equivalence"`
}

func (r Receipt) MarshalJSON() ([]byte, error) {
	if r.raw != "" {
		return []byte(r.raw), nil
	}
	type plain Receipt
	return json.Marshal(plain(r))
}

type Service struct {
	db         *store.DB
	models     *backendmodel.Repo
	apis       *apidesign.Repo
	scenarios  *designscenario.Repo
	afterWrite func(string) error // tests inject transaction failures, never transport input
}

func NewService(db *store.DB, models *backendmodel.Repo, apis *apidesign.Repo, scenarios *designscenario.Repo) *Service {
	return &Service{db: db, models: models, apis: apis, scenarios: scenarios}
}
func invalid(message string) error {
	return &backendmodel.FaultError{Status: 422, Code: "backend_materialization_invalid", Message: message}
}
func conflict(message string) error {
	return &backendmodel.FaultError{Status: 409, Code: "backend_materialization_conflict", Message: message}
}
func quota() error {
	return &backendmodel.FaultError{Status: 413, Code: "backend_materialization_limit", Message: "Materialization exceeds 5 targets, 100 commands or 1 MiB"}
}
func digest(v any) (string, error) { return backendmodel.CanonicalDocumentHash(v) }
func validHash(s string) bool      { return len(s) == 64 && strings.Trim(s, "0123456789abcdef") == "" }
func (s *Service) Preview(ctx context.Context, pid string, in PreviewInput) (*Preview, error) {
	tx, err := s.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	return s.previewTx(ctx, tx, pid, in)
}
func cloneInput(in PreviewInput) (PreviewInput, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return in, err
	}
	if len(raw) > MaxBytes {
		return in, quota()
	}
	var out PreviewInput
	err = json.Unmarshal(raw, &out, json.RejectUnknownMembers(true))
	return out, err
}
func equalJSON(a, b string) bool {
	var x, y jsontext.Value
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	// Member order and whitespace are not semantic; number lexemes are. RFC 8785
	// number canonicalisation made 9007199254740993 equal 9007199254740992, so a
	// scenario contract that disagreed with the planned API document passed and
	// was overwritten at Apply (review 2026-10-06, F36).
	exact := []jsontext.Options{jsontext.CanonicalizeRawInts(false), jsontext.CanonicalizeRawFloats(false)}
	if x.Canonicalize(exact...) != nil || y.Canonicalize(exact...) != nil {
		return false
	}
	return string(x) == string(y)
}

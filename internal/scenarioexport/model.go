// Package scenarioexport produces read-only artifacts from an immutable scenario revision.
package scenarioexport

import (
	"errors"

	"github.com/yashok111/mocker/internal/designscenario"
)

type Format string

const (
	PlantUML    Format = "plantuml"
	Mermaid     Format = "mermaid"
	OpenAPIJSON Format = "openapi-json"
	OpenAPIYAML Format = "openapi-yaml"
)

var (
	ErrUnsupportedFormat = errors.New("unsupported scenario export format")
	ErrContractNotFound  = errors.New("scenario export contract not found")
	ErrTooLarge          = errors.New("scenario export exceeds byte limit")
	ErrInvalidRequest    = errors.New("invalid scenario export request")
)

type Request struct {
	Format     Format `json:"format"`
	ContractID string `json:"contractId,omitempty"`
}

type Target struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type Diagnostic struct {
	Code     string  `json:"code"`
	Severity string  `json:"severity"`
	Message  string  `json:"message"`
	Pointer  string  `json:"pointer,omitempty"`
	Target   *Target `json:"target,omitempty"`
}

type Option struct {
	Format      Format       `json:"format"`
	ContractID  string       `json:"contractId,omitempty"`
	Ready       bool         `json:"ready"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

type Artifact struct {
	ScenarioID  int64        `json:"scenarioId"`
	RevisionID  int64        `json:"revisionId"`
	SourceHash  string       `json:"sourceHash"`
	Format      Format       `json:"format"`
	Filename    string       `json:"filename"`
	MediaType   string       `json:"mediaType"`
	Content     string       `json:"content"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

type OptionsResponse struct {
	ScenarioID int64    `json:"scenarioId"`
	RevisionID int64    `json:"revisionId"`
	SourceHash string   `json:"sourceHash"`
	Options    []Option `json:"options"`
}

type BlockedError struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
}

func (e *BlockedError) Error() string {
	return "Дополните данные для выбранного результата"
}

type ValidateContract func(string) ([]designscenario.Diagnostic, error)

type Service struct {
	validate ValidateContract
	maxBytes int64
}

func New(validate ValidateContract, maxBytes int64) *Service {
	return &Service{validate: validate, maxBytes: maxBytes}
}

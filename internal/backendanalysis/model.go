package backendanalysis

import (
	"encoding/json/jsontext"
	"time"

	"github.com/yashok111/mocker/internal/backendmodel"
	o "github.com/yashok111/mocker/internal/backendobservations"
)

type ObjectAddress struct {
	RecordType string `json:"recordType"`
	ID         string `json:"id"`
}
type Scope struct {
	ChangedIDs []ObjectAddress `json:"changedIds"`
	Service    string          `json:"service"`
	Kind       string          `json:"kind"`
	Certainty  string          `json:"certainty"`
	Direction  string          `json:"direction"`
	Depth      int             `json:"depth"`
}
type Limits struct {
	States             int   `json:"states"`
	DependencyVisits   int   `json:"dependencyVisits"`
	Depth              int   `json:"depth"`
	Findings           int   `json:"findings"`
	Records            int   `json:"records"`
	WitnessesPerObject int   `json:"witnessesPerObject"`
	ResultBytes        int64 `json:"resultBytes"`
}
type CommandPreviewTarget struct {
	ChangeProposal  backendmodel.ProposalReadTarget      `json:"changeProposal"`
	ExpectedVersion int64                                `json:"expectedVersion"`
	Commands        []backendmodel.ChangeProposalCommand `json:"commands"`
	CandidateHash   string                               `json:"candidateHash"`
}
type AnalysisTarget struct {
	RevisionID     string                           `json:"revisionId,omitempty"`
	Proposal       *backendmodel.ProposalReadTarget `json:"proposal,omitzero"`
	ChangeProposal *backendmodel.ProposalReadTarget `json:"changeProposal,omitzero"`
	CommandPreview *CommandPreviewTarget            `json:"commandPreview,omitzero"`
}
type StartInput struct {
	Measurement     *StartMeasurementInput          `json:"-"`
	DiagramScope    *backendmodel.DiagramScopeInput `json:"diagramScope,omitzero"`
	Package         *StartPackageInput              `json:"-"`
	Conformance     *StartConformanceInput          `json:"-"`
	EndpointReview  *StartEndpointReviewInput       `json:"-"`
	Kind            string                          `json:"kind"`
	FromRevisionID  string                          `json:"fromRevisionId"`
	Target          AnalysisTarget                  `json:"target"`
	Scope           Scope                           `json:"scope"`
	Limits          Limits                          `json:"limits"`
	ObservationMode string                          `json:"observationMode"`
	ObservationPins []jsontext.Value                `json:"observationPins"`
	IdempotencyKey  string                          `json:"idempotencyKey"`
}
type CancelInput struct {
	IdempotencyKey string `json:"idempotencyKey"`
}
type RetryInput struct {
	IdempotencyKey string `json:"idempotencyKey"`
}
type ImmutableInput struct {
	ImpactObservations *o.PinnedObservations             `json:"impactObservations,omitzero"`
	Observations       *FrozenObservationAnalysis        `json:"observations,omitzero"`
	DiagramScope       *backendmodel.DiagramScope        `json:"diagramScope,omitzero"`
	DiagnosticDiagram  *backendmodel.DiagramVersion      `json:"diagnosticDiagram,omitzero"`
	V2                 *ImmutableInputV2                 `json:"-"`
	DocumentVersion    string                            `json:"documentVersion"`
	Kind               string                            `json:"kind"`
	ProjectID          string                            `json:"projectId"`
	From               backendmodel.BackendReadTarget    `json:"from"`
	To                 *backendmodel.BackendReadTarget   `json:"to,omitzero"`
	CommandPreview     *backendmodel.FrozenChangePreview `json:"commandPreview,omitzero"`
	BeforePins         backendmodel.EffectiveGraphPins   `json:"beforePins"`
	AfterPins          backendmodel.EffectiveGraphPins   `json:"afterPins"`
	BeforeSource       backendmodel.AnalysisSourcePins   `json:"beforeSource"`
	AfterSource        backendmodel.AnalysisSourcePins   `json:"afterSource"`
	Scope              Scope                             `json:"scope"`
	Limits             Limits                            `json:"limits"`
	RuleSetVersion     string                            `json:"ruleSetVersion"`
	TraversalVersion   string                            `json:"traversalVersion"`
	ObservationMode    string                            `json:"observationMode"`
}
type Progress struct {
	States           int `json:"states"`
	DependencyVisits int `json:"dependencyVisits"`
	Findings         int `json:"findings"`
	Records          int `json:"records"`
}
type Diagnostic struct {
	ID      string          `json:"id"`
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Objects []ObjectAddress `json:"objects"`
}
type Job struct {
	ID                        string      `json:"id"`
	ProjectID                 string      `json:"projectId"`
	Kind                      string      `json:"kind"`
	Status                    string      `json:"status"`
	AnalysisInputHash         string      `json:"analysisInputHash"`
	Version                   int64       `json:"version"`
	ResultVersion             *int64      `json:"resultVersion,omitzero"`
	RecommendedPollIntervalMs int         `json:"recommendedPollIntervalMs"`
	Progress                  Progress    `json:"progress"`
	Diagnostic                *Diagnostic `json:"diagnostic,omitzero"`
	CreatedAt                 time.Time   `json:"createdAt"`
	UpdatedAt                 time.Time   `json:"updatedAt"`
}
type SectionManifest struct {
	Section string `json:"section"`
	Count   int    `json:"count"`
	Bytes   int64  `json:"bytes"`
}
type ResultManifest struct {
	DiagramScope         *backendmodel.DiagramScope `json:"diagramScope,omitzero"`
	JobID                string                     `json:"jobId"`
	AnalysisInputHash    string                     `json:"analysisInputHash"`
	SemanticResultHash   string                     `json:"semanticResultHash"`
	ResultVersion        int64                      `json:"resultVersion"`
	HighWaterSequence    int64                      `json:"highWaterSequence"`
	Complete             bool                       `json:"complete"`
	Sections             []SectionManifest          `json:"sections"`
	ChangedIDs           []ObjectAddress            `json:"changedIds"`
	CoveredChangedIDs    []ObjectAddress            `json:"coveredChangedIds"`
	Gaps                 []Diagnostic               `json:"gaps"`
	TruncationReasons    []Diagnostic               `json:"truncationReasons"`
	SourceCoverageBefore backendmodel.Coverage      `json:"sourceCoverageBefore"`
	SourceCoverageAfter  backendmodel.Coverage      `json:"sourceCoverageAfter"`
	Scope                Scope                      `json:"scope"`
	RuleSetVersion       string                     `json:"ruleSetVersion"`
	TraversalVersion     string                     `json:"traversalVersion"`
	Verdict              string                     `json:"verdict"`
	RuntimeVerified      bool                       `json:"runtimeVerified"`
}
type ResultChunk struct {
	JobID       string `json:"jobId"`
	Section     string `json:"section"`
	ContentHash string `json:"contentHash"`
	Sequence    int64  `json:"sequence"`
	ItemsJSON   []byte `json:"-"`
}
type ResultQuery struct {
	ResultVersion int64  `json:"resultVersion"`
	Section       string `json:"section"`
	Cursor        string `json:"cursor"`
	Service       string `json:"service"`
	Kind          string `json:"kind"`
	Certainty     string `json:"certainty"`
	Direction     string `json:"direction"`
	Depth         int    `json:"depth"`
	Limit         int    `json:"limit"`
}
type ResultPage struct {
	Manifest   ResultManifest `json:"manifest"`
	Section    string         `json:"section"`
	ItemsJSON  []byte         `json:"-"`
	NextCursor string         `json:"nextCursor"`
}
type ListQuery struct {
	Status string `json:"status"`
	Kind   string `json:"kind"`
	Cursor string `json:"cursor"`
	Limit  int    `json:"limit"`
}
type JobPage struct {
	Items      []Job  `json:"items"`
	NextCursor string `json:"nextCursor"`
}

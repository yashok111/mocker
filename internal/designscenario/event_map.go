package designscenario

import "context"

const (
	MaxEventMapNodes       = 5000
	MaxEventMapEdges       = 10000
	MaxEventMapDiagnostics = 2000
	MaxEventMapBytes       = 4 << 20
)

type EventMapReport struct {
	EventMapAnalysis
	ScenarioID int64 `json:"scenarioId"`
	Version    int64 `json:"version"`
	RevisionID int64 `json:"revisionId,omitempty"`
	Proposed   bool  `json:"proposed"`
}

type EventMapAnalysis struct {
	Nodes       []EventMapNode       `json:"nodes"`
	Edges       []EventMapEdge       `json:"edges"`
	Diagnostics []EventMapDiagnostic `json:"diagnostics"`
	Coverage    EventMapCoverage     `json:"coverage"`
	Complete    bool                 `json:"complete"`
}

type EventMapNode struct {
	ID       string          `json:"id"`
	Kind     string          `json:"kind"`
	Label    string          `json:"label"`
	Locator  EventMapLocator `json:"locator"`
	GroupID  string          `json:"groupId,omitempty"`
	ClientID string          `json:"clientId,omitempty"`
}

type EventMapEdge struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Source  string          `json:"source"`
	Target  string          `json:"target"`
	Label   string          `json:"label"`
	Locator EventMapLocator `json:"locator"`
}

type EventMapLocator struct {
	Pointer          string `json:"pointer"`
	EntityID         string `json:"entityId,omitempty"`
	ContractID       string `json:"contractId,omitempty"`
	OperationID      string `json:"operationId,omitempty"`
	ParticipantID    string `json:"participantId,omitempty"`
	HTTPContractID   string `json:"httpContractId,omitempty"`
	OperationKey     string `json:"operationKey,omitempty"`
	DiagramID        string `json:"diagramId,omitempty"`
	TransitionID     string `json:"transitionId,omitempty"`
	Mode             string `json:"mode,omitempty"`
	PinnedRevisionID int64  `json:"pinnedRevisionId,omitempty"`
	Method           string `json:"method,omitempty"`
	Path             string `json:"path,omitempty"`
}

type EventMapDiagnostic struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	Severity  string `json:"severity"`
	Pointer   string `json:"pointer"`
	Message   string `json:"message"`
	ElementID string `json:"elementId,omitempty"`
}

type EventMapCoverage struct {
	NodesReturned       int      `json:"nodesReturned"`
	EdgesReturned       int      `json:"edgesReturned"`
	DiagnosticsReturned int      `json:"diagnosticsReturned"`
	TruncatedReasons    []string `json:"truncatedReasons"`
}

// AnalyzeEventMap projects a complete scenario document without reading or writing state.
func AnalyzeEventMap(ctx context.Context, document Document) (EventMapAnalysis, error) {
	return analyzeEventMap(ctx, document)
}

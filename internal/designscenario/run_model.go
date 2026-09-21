package designscenario

import "context"

// StepRequest is a resolved request for one operation in a saved revision.
type StepRequest struct {
	RevisionID int64           `json:"revisionId"`
	MessageID  string          `json:"messageId"`
	PathParams ExecutionValues `json:"pathParams"`
	Query      ExecutionValues `json:"query"`
	Headers    ExecutionValues `json:"headers"`
	Body       string          `json:"body"`
}

func (s *StepRequest) UnmarshalJSON(data []byte) error {
	type wire StepRequest
	var decoded wire
	if err := decodeExecutionObject(data, &decoded, "revisionId", "messageId", "pathParams", "query", "headers", "body"); err != nil {
		return err
	}
	*s = StepRequest(decoded)
	return nil
}

type StepResponse struct {
	ScenarioRevisionID int64             `json:"scenarioRevisionId"`
	DesignID           int64             `json:"designId"`
	DesignRevisionID   int64             `json:"designRevisionId"`
	WorkspaceRevision  int64             `json:"workspaceRevision"`
	Method             string            `json:"method"`
	Path               string            `json:"path"`
	Status             int               `json:"status"`
	Headers            map[string]string `json:"headers"`
	Body               string            `json:"body"`
	DurationMS         float64           `json:"durationMs"`
}

// StepExecutor must honor context cancellation and must finish before returning.
type StepExecutor func(context.Context, StepRequest) (StepResponse, error)

type RunSummary struct {
	ID         string `json:"id"`
	ScenarioID int64  `json:"scenarioId"`
	RevisionID int64  `json:"revisionId"`
	Version    int64  `json:"version"`
	Name       string `json:"name"`
	Source     string `json:"source"`
	Status     string `json:"status"`
	StartedAt  int64  `json:"startedAt"`
	FinishedAt *int64 `json:"finishedAt,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type RunReport struct {
	RunSummary
	Document       Document        `json:"document"`
	InputVariables ExecutionValues `json:"inputVariables"`
	Variables      ExecutionValues `json:"variables"`
	Steps          []StepResult    `json:"steps"`
}

type StepResult struct {
	MessageID  string            `json:"messageId"`
	Status     string            `json:"status"`
	Reason     string            `json:"reason,omitempty"`
	Request    *StepRequest      `json:"request,omitempty"`
	Response   *StepResponse     `json:"response,omitempty"`
	Assertions []AssertionResult `json:"assertions"`
}

// ActualJSON is absent for a missing pointer and contains "null" for JSON null.
type AssertionResult struct {
	Pointer      string  `json:"pointer"`
	ExpectedJSON string  `json:"expectedJson"`
	ActualJSON   *string `json:"actualJson,omitempty"`
	Passed       bool    `json:"passed"`
	Error        string  `json:"error,omitempty"`
}

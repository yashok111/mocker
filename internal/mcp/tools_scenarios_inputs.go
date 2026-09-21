package mcp

import "github.com/yashok111/mocker/internal/designscenario"

type designScenarioIDInput struct {
	ScenarioID int64 `json:"scenarioId" jsonschema:"scenario project id from list_design_scenarios"`
}

type runDesignScenarioInput struct {
	ScenarioID int64 `json:"scenarioId"`
	runDesignScenarioBody
}

type runDesignScenarioBody struct {
	RunID      string                         `json:"runId"`
	RevisionID int64                          `json:"revisionId"`
	Variables  designscenario.ExecutionValues `json:"variables,omitempty"`
	Name       string                         `json:"name,omitempty"`
}

type designScenarioRunInput struct {
	ScenarioID int64  `json:"scenarioId"`
	RunID      string `json:"runId"`
}

type createDesignScenarioInput struct {
	Document   designscenario.Document `json:"document" jsonschema:"complete sequence-canvas document; starts as formatVersion 1"`
	FormDrafts map[string]string       `json:"formDrafts,omitempty" jsonschema:"unfinished API form buffers keyed by the UI form identity"`
	Summary    string                  `json:"summary,omitempty" jsonschema:"short explanation of the initial scenario"`
}

type saveDesignScenarioInput struct {
	ScenarioID      int64                   `json:"scenarioId"`
	ExpectedVersion int64                   `json:"expectedVersion" jsonschema:"exact scenario.version from your own get_design_scenario read; never blind-retry a conflict"`
	Document        designscenario.Document `json:"document" jsonschema:"complete replacement canvas document; every participant, message, fragment and contract to retain must be present"`
	FormDrafts      map[string]string       `json:"formDrafts,omitempty" jsonschema:"complete unfinished API form buffers to retain"`
	Summary         string                  `json:"summary,omitempty" jsonschema:"short explanation of this revision"`
}

type applyDesignScenarioCommandsInput struct {
	ScenarioID      int64                    `json:"scenarioId"`
	ExpectedVersion int64                    `json:"expectedVersion" jsonschema:"exact scenario.version from your own get_design_scenario read; never blind-retry a conflict"`
	Commands        []designscenario.Command `json:"commands" jsonschema:"ordered atomic command batch; all commands succeed or none are saved"`
	Summary         string                   `json:"summary,omitempty" jsonschema:"short explanation of this revision"`
}

type designScenarioRevisionInput struct {
	ScenarioID int64 `json:"scenarioId"`
	RevisionID int64 `json:"revisionId"`
}

type designScenarioDiffInput struct {
	ScenarioID     int64  `json:"scenarioId"`
	FromRevisionID *int64 `json:"fromRevisionId,omitempty" jsonschema:"baseline revision belonging to this scenario; defaults to the previous revision"`
	ToRevisionID   *int64 `json:"toRevisionId,omitempty" jsonschema:"target revision belonging to this scenario; defaults to the draft"`
}

type restoreDesignScenarioInput struct {
	ScenarioID      int64  `json:"scenarioId"`
	ExpectedVersion int64  `json:"expectedVersion" jsonschema:"exact scenario.version from your own get_design_scenario read"`
	RevisionID      int64  `json:"revisionId" jsonschema:"immutable revision belonging to this scenario"`
	Summary         string `json:"summary,omitempty" jsonschema:"short explanation of why this revision is restored"`
}

type validateDesignScenarioInput struct {
	ScenarioID int64                   `json:"scenarioId"`
	Document   designscenario.Document `json:"document" jsonschema:"proposed complete sequence-canvas document to validate without saving"`
}

type executeDesignScenarioStepInput struct {
	ScenarioID int64 `json:"scenarioId"`
	executeDesignScenarioStepBody
}

type executeDesignScenarioStepBody struct {
	RevisionID int64             `json:"revisionId"`
	MessageID  string            `json:"messageId"`
	PathParams map[string]string `json:"pathParams"`
	Query      map[string]string `json:"query"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
}

package designscenario

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/jsonx"
)

const MaxRunRetainedData = 20 << 20

var (
	runIDPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
	runTemplate     = regexp.MustCompile(`\{\{([^{}]+)\}\}`)
	errRunDataLimit = errors.New("суммарный размер запросов, ответов и проверок превышает 20 МиБ; последнее значение не сохранено")
)

// PrepareRun validates all executable bindings before any request is dispatched.
// The report owns its document and variables; overrides never modify the revision.
func PrepareRun(revision Revision, runID, name, source string, variables ExecutionValues) (RunReport, error) {
	invalid := func(message string) (RunReport, error) { return RunReport{}, fmt.Errorf("%w: %s", ErrInvalid, message) }
	if err := validateRunRevision(revision, runID, name, source); err != nil {
		return invalid(err.Error())
	}
	document, err := cloneDocument(revision.Document)
	if err != nil {
		return invalid(err.Error())
	}
	input := maps.Clone(variables)
	if input == nil {
		input = ExecutionValues{}
	}
	merged := ExecutionValues{}
	if document.Execution != nil {
		maps.Copy(merged, document.Execution.Variables)
	}
	maps.Copy(merged, input)
	if err := validateRunVariables(merged); err != nil {
		return invalid(err.Error())
	}
	if err := validateRunVariableSnapshots(input, merged); err != nil {
		return invalid(err.Error())
	}
	report := RunReport{
		ID: runID, ScenarioID: revision.ScenarioID, RevisionID: revision.ID, Version: revision.Version,
		Name: name, Source: source, Status: "running", StartedAt: time.Now().UnixMilli(),
		Document: document, InputVariables: input, Variables: merged, Steps: make([]StepResult, len(document.Messages)),
	}
	executable := 0
	for i, message := range document.Messages {
		step := StepResult{MessageID: message.ID, Status: "pending", Assertions: []AssertionResult{}}
		switch {
		case message.Kind == "event":
			step.Status, step.Reason = "skipped", "event_execution_unsupported"
		case message.Execution != nil && !message.Execution.Enabled:
			step.Status, step.Reason = "skipped", "Шаг выключен."
		case message.Kind != "request" || (message.Operation == nil && message.Execution == nil):
			step.Status, step.Reason = "skipped", "Описательное сообщение: HTTP-запрос не выполняется."
		default:
			if message.Operation == nil {
				return invalid(fmt.Sprintf("Укажите операцию API для включённого сообщения %q", message.ID))
			}
			if _, err := runContract(document, message); err != nil {
				return invalid(err.Error())
			}
			executable++
		}
		report.Steps[i] = step
	}
	if executable == 0 {
		for _, message := range document.Messages {
			if message.Kind == "event" {
				return invalid("В сценарии нет исполняемых HTTP-шагов: Kafka runtime недоступен")
			}
		}
		return invalid("В сценарии нет включённых HTTP-запросов с операцией API")
	}
	return report, nil
}

func validateRunRevision(revision Revision, runID, name, source string) error {
	if !runIDPattern.MatchString(runID) || utf8.RuneCountInString(name) > 200 || (source != "ui" && source != "mcp") || revision.ID <= 0 || revision.ScenarioID <= 0 {
		return errors.New("укажите допустимые ID, название и источник запуска")
	}
	if diagnostics := ValidateFragments(revision.Document); len(diagnostics) > 0 {
		return fmt.Errorf("%s: %s", diagnostics[0].Pointer, diagnostics[0].Message)
	}
	if revision.Document.FormatVersion == 1 && overlappingRunFragments(revision.Document) {
		return errors.New("неоднозначные фрагменты formatVersion 1: обновите формат сценария")
	}
	if diagnostics := validateControlFlow(revision.Document, true); len(diagnostics) > 0 {
		return fmt.Errorf("%s: %s", diagnostics[0].Pointer, diagnostics[0].Message)
	}
	if runFormDraftsPending(revision.FormDrafts) {
		return errors.New("сначала завершите редактирование форм сценария")
	}
	if hasDataBindings(revision.Document) {
		for _, diagnostic := range AnalyzeDataFlow(revision.Document).Diagnostics {
			if diagnostic.Severity == "error" {
				return fmt.Errorf("%s: %s", diagnostic.Pointer, diagnostic.Message)
			}
		}
	}
	if diagnostics := executionDiagnostics(revision.Document); len(diagnostics) != 0 {
		return fmt.Errorf("%s: %s", diagnostics[0].Pointer, diagnostics[0].Message)
	}
	return nil
}

// overlappingRunFragments reports formatVersion 1 fragments whose message
// ranges overlap, which a run cannot nest unambiguously.
func overlappingRunFragments(document Document) bool {
	positions := map[string]int{}
	for i, m := range document.Messages {
		positions[m.ID] = i
	}
	for i, a := range document.Fragments {
		ar, _ := messageRange(a.FromMessageID, a.ToMessageID, positions)
		for _, b := range document.Fragments[i+1:] {
			br, _ := messageRange(b.FromMessageID, b.ToMessageID, positions)
			if ar.start <= br.end && br.start <= ar.end {
				return true
			}
		}
	}
	return false
}

// runFormDraftsPending is true unless the only draft is the empty "all" form.
func runFormDraftsPending(drafts map[string]string) bool {
	for key, value := range drafts {
		var fields map[string]jsonx.RawMessage
		if key == "all" && jsonx.Unmarshal([]byte(value), &fields) == nil && fields != nil && len(fields) == 0 {
			continue
		}
		return true
	}
	return false
}

func validateRunVariables(variables ExecutionValues) error {
	if len(variables) > MaxExecutionEntries {
		return errors.New("прогон не может содержать более 100 переменных")
	}
	for key, value := range variables {
		if !executionVariableName.MatchString(key) || utf8.RuneCountInString(value) > maxText {
			return fmt.Errorf("недопустимое имя или слишком большое значение переменной %q", key)
		}
	}
	return nil
}

// Escaping control characters can expand a value sixfold. Bound the two saved
// variable maps independently of retained HTTP data, before committing them.
func validateRunVariableSnapshots(input, variables ExecutionValues) error {
	retained := 4 // Two JSON object delimiters.
	for _, values := range []ExecutionValues{input, variables} {
		for key, value := range values {
			encodedKey, _ := jsonx.Marshal(key)
			encodedValue, _ := jsonx.Marshal(value)
			retained += len(encodedKey) + len(encodedValue) + 2
			if retained > MaxRunRetainedData {
				return errors.New("суммарный размер снимков переменных превышает 20 МиБ")
			}
		}
	}
	return nil
}

func runContract(document Document, message Message) (*Contract, error) {
	for i := range document.Contracts {
		contract := &document.Contracts[i]
		if contract.ID != message.Operation.ContractID {
			continue
		}
		if contract.Mode != "linked" || contract.Source == nil || contract.Source.DesignID <= 0 || contract.Source.RevisionID <= 0 {
			return nil, fmt.Errorf("свяжите контракт сообщения %q с проектом API", message.ID)
		}
		keys, diagnostics := operationKeys(contract.Document, "")
		invalidKeys := slices.ContainsFunc(diagnostics, func(d Diagnostic) bool { return d.Severity == "error" })
		if _, ok := keys[message.Operation.OperationKey]; !ok || invalidKeys {
			return nil, fmt.Errorf("операция API сообщения %q недоступна; %s", message.ID, OperationKeyDescription)
		}
		return contract, nil
	}
	return nil, fmt.Errorf("контракт сообщения %q недоступен", message.ID)
}

type runEngine struct {
	bindingSchemas []bindingSchema
	report         RunReport
	progress       func(RunReport) error
	retained       int
	progressFailed bool
}

// Run executes the snapshot prepared by PrepareRun, synchronously and in order.
// Progress receives independently owned snapshots. A persistence error prevents
// further dispatch; the caller must persist the returned terminal report.
func Run(ctx context.Context, revision Revision, initial RunReport, execute StepExecutor, progress func(RunReport) error) RunReport {
	e := runEngine{report: cloneRunReport(initial), progress: progress, retained: 2 * len(initial.Steps)}
	if initial.Status != "running" {
		return e.report
	}
	if initial.RevisionID != revision.ID || initial.ScenarioID != revision.ScenarioID || len(initial.Steps) != len(initial.Document.Messages) {
		return e.finish("failed", "Отчёт относится к другой ревизии сценария")
	}
	if err := e.publish(); err != nil {
		return e.finish("failed", err.Error())
	}
	if len(e.report.Document.Fragments) > 0 {
		return e.runControlFlow(ctx, execute)
	}
	for i, message := range e.report.Document.Messages {
		if i >= maxRunOccurrences {
			return e.finish("failed", "превышен предел 1000 посещений сообщений")
		}
		if e.report.Steps[i].Status == "skipped" {
			continue
		}
		if ctx.Err() != nil {
			return e.finish("cancelled", runCancellationReason(ctx))
		}
		e.report.Steps[i].Status = "running"
		if err := e.publish(); err != nil {
			return e.failStep(i, "failed", err)
		}
		if err := e.executeStep(ctx, i, message, execute); err != nil {
			if ctx.Err() != nil {
				return e.failStep(i, "cancelled", errors.New(runCancellationReason(ctx)))
			}
			return e.failStep(i, "failed", err)
		}
		e.report.Steps[i].Status = "passed"
		if err := e.publish(); err != nil {
			return e.failStep(i, "failed", err)
		}
	}
	if ctx.Err() != nil {
		return e.finish("cancelled", runCancellationReason(ctx))
	}
	return e.finish("passed", "")
}

func (e *runEngine) executeStep(ctx context.Context, index int, message Message, execute StepExecutor) error {
	config := message.Execution
	if config == nil {
		config = &StepExecution{Enabled: true, PathParams: ExecutionValues{}, Query: ExecutionValues{}, Headers: ExecutionValues{}}
	}
	request, results, err := e.resolveBindingRequest(index, message, config)
	if err != nil {
		return err
	}
	if len(results) > 0 {
		if err = e.retain(results, 0); err != nil {
			return err
		}
		e.report.Steps[index].BindingResults = results
	}
	if err = e.retain(request, 0); err != nil {
		return err
	}
	e.report.Steps[index].Request = &request
	if err = e.publish(); err != nil {
		return err
	}
	response, err := dispatchRunRequest(ctx, request, execute)
	if err != nil {
		return err
	}
	if err = e.saveStepResponse(index, response); err != nil {
		return err
	}
	if err = e.validateStepResponse(message, config, response); err != nil {
		return err
	}
	if len(config.Assertions) == 0 && len(config.Extract) == 0 {
		return nil
	}
	if !jsonx.Valid([]byte(response.Body)) {
		return errors.New("ответ не содержит допустимый JSON")
	}
	body, err := decodeJSONValue([]byte(response.Body))
	if err != nil {
		return fmt.Errorf("не удалось прочитать JSON ответа: %w", err)
	}
	if err = e.checkStepAssertions(index, config.Assertions, body); err != nil {
		return err
	}
	return e.extractStepVariables(config.Extract, body)
}

func dispatchRunRequest(ctx context.Context, request StepRequest, execute StepExecutor) (StepResponse, error) {
	if err := ctx.Err(); err != nil {
		return StepResponse{}, err
	}
	if execute == nil {
		return StepResponse{}, errors.New("исполнение мока недоступно")
	}
	stepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	response, err := execute(stepCtx, cloneStepRequest(request))
	stepError := stepCtx.Err()
	cancel()
	if stepError != nil {
		return StepResponse{}, fmt.Errorf("запрос отменён или превысил время ожидания: %w", stepError)
	}
	if err != nil {
		return StepResponse{}, err
	}
	if err = ctx.Err(); err != nil {
		return StepResponse{}, err
	}
	return response, nil
}

func (e *runEngine) saveStepResponse(index int, response StepResponse) error {
	if len(response.Body) > MaxExecutionBody {
		return errors.New("ответ мока превышает 1 МиБ; тело не сохранено")
	}
	if err := e.retain(response, 0); err != nil {
		return err
	}
	response.Headers = maps.Clone(response.Headers)
	if response.Headers == nil {
		response.Headers = map[string]string{}
	}
	e.report.Steps[index].Response = &response
	return e.publish()
}

func (e *runEngine) validateStepResponse(message Message, config *StepExecution, response StepResponse) error {
	contract, err := runContract(e.report.Document, message)
	if err != nil {
		return err
	}
	if response.ScenarioRevisionID != e.report.RevisionID || response.DesignID != contract.Source.DesignID || response.DesignRevisionID != contract.Source.RevisionID {
		return errors.New("ответ сервера относится к другой ревизии сценария или API")
	}
	if config.ExpectedStatus == nil {
		if response.Status < 200 || response.Status >= 300 {
			return fmt.Errorf("HTTP %d: ожидался статус 2xx", response.Status)
		}
	} else if response.Status != *config.ExpectedStatus {
		return fmt.Errorf("HTTP %d: ожидался %d", response.Status, *config.ExpectedStatus)
	}
	return nil
}

func (e *runEngine) checkStepAssertions(index int, assertions []ExecutionAssertion, body any) error {
	assertionsPassed := true
	for _, assertion := range assertions {
		result := AssertionResult{Pointer: assertion.Pointer, ExpectedJSON: string(assertion.Equals)}
		actual, exists := runReadPointer(body, assertion.Pointer)
		if exists {
			encoded, err := jsonx.Marshal(actual)
			if err != nil {
				return err
			}
			result.ActualJSON = new(string(encoded))
			expected, err := decodeJSONValue(assertion.Equals)
			if err != nil {
				return err
			}
			result.Passed = runEqualJSON(actual, expected)
		} else {
			result.Error = "Значение по JSON Pointer отсутствует."
		}
		// Each appended array element adds a comma after the first element.
		if err := e.retain(result, -1); err != nil {
			return err
		}
		e.report.Steps[index].Assertions = append(e.report.Steps[index].Assertions, result)
		assertionsPassed = assertionsPassed && result.Passed
	}
	if err := e.publish(); err != nil {
		return err
	}
	if !assertionsPassed {
		return errors.New("проверка JSON-ответа не пройдена")
	}
	return nil
}

func (e *runEngine) extractStepVariables(extractions []ExecutionExtraction, body any) error {
	variables := maps.Clone(e.report.Variables)
	for _, extraction := range extractions {
		value, exists := runReadPointer(body, extraction.Pointer)
		if !exists {
			return fmt.Errorf("не найден JSON Pointer %q для переменной %q", extraction.Pointer, extraction.Name)
		}
		text, ok := value.(string)
		if !ok {
			encoded, err := jsonx.Marshal(value)
			if err != nil {
				return err
			}
			text = string(encoded)
		}
		variables[extraction.Name] = text
	}
	if err := validateRunVariables(variables); err != nil {
		return err
	}
	if err := validateRunVariableSnapshots(e.report.InputVariables, variables); err != nil {
		return err
	}
	e.report.Variables = variables
	return nil
}

func (e *runEngine) retain(value any, replacing int) error {
	encoded, err := jsonx.Marshal(value)
	if err != nil {
		return err
	}
	if e.retained+len(encoded)-replacing > MaxRunRetainedData {
		return errRunDataLimit
	}
	e.retained += len(encoded) - replacing
	return nil
}

func (e *runEngine) publish() error {
	if e.progress == nil || e.progressFailed {
		return nil
	}
	if err := e.progress(cloneRunReport(e.report)); err != nil {
		e.progressFailed = true
		return fmt.Errorf("не удалось сохранить прогресс запуска: %w", err)
	}
	return nil
}

func (e *runEngine) failStep(index int, status string, err error) RunReport {
	e.report.Steps[index].Status, e.report.Steps[index].Reason = status, boundedRunReason(err.Error())
	return e.finish(status, err.Error())
}

func (e *runEngine) finish(status, reason string) RunReport {
	e.report.Status, e.report.Reason = status, boundedRunReason(reason)
	e.report.FinishedAt = new(time.Now().UnixMilli())
	for i := range e.report.Steps {
		step := &e.report.Steps[i]
		switch step.Status {
		case "pending":
			step.Status, step.Reason = "skipped", "Предыдущий шаг не выполнен."
			if status == "cancelled" {
				step.Reason = "Прогон отменён."
			}
		case "running":
			step.Status, step.Reason = status, e.report.Reason
		}
	}
	if err := e.publish(); err != nil {
		e.report.Status, e.report.Reason = "failed", boundedRunReason(err.Error())
	}
	return e.report
}

func boundedRunReason(reason string) string {
	if len(reason) > 4096 {
		return strings.ToValidUTF8(reason[:4096], "")
	}
	return reason
}

func runCancellationReason(ctx context.Context) string {
	if cause := context.Cause(ctx); cause != nil {
		return "Прогон отменён: " + cause.Error()
	}
	return "Прогон отменён."
}

// CancelRunReport normalizes an active report during cancellation or recovery.
// Callers decide whether a run is still active; persisted terminal runs are left alone.
func CancelRunReport(report RunReport, reason string) RunReport {
	e := runEngine{report: cloneRunReport(report)}
	return e.finish("cancelled", reason)
}

func resolveRunRequest(revisionID int64, messageID string, config *StepExecution, variables ExecutionValues) (StepRequest, error) {
	request := StepRequest{RevisionID: revisionID, MessageID: messageID}
	for _, pair := range []struct {
		input  ExecutionValues
		output *ExecutionValues
	}{{config.PathParams, &request.PathParams}, {config.Query, &request.Query}, {config.Headers, &request.Headers}} {
		*pair.output = ExecutionValues{}
		for key, source := range pair.input {
			text, err := substituteRunValue(source, variables, maxText*utf8.UTFMax)
			if err != nil {
				return StepRequest{}, err
			}
			if utf8.RuneCountInString(text) > maxText {
				return StepRequest{}, fmt.Errorf("значение параметра %q после подстановки слишком большое", key)
			}
			(*pair.output)[key] = text
		}
	}
	body, err := substituteRunValue(config.Body, variables, MaxExecutionBody)
	if err != nil {
		return StepRequest{}, err
	}
	request.Body = body
	return request, nil
}

func substituteRunValue(source string, variables ExecutionValues, limit int) (string, error) {
	var out strings.Builder
	appendText := func(text string) error {
		if out.Len()+len(text) > limit {
			return errors.New("значение после подстановки превышает допустимый размер")
		}
		out.WriteString(text)
		return nil
	}
	for source != "" {
		match := runTemplate.FindStringSubmatchIndex(source)
		if match == nil {
			if err := appendText(source); err != nil {
				return "", err
			}
			break
		}
		if err := appendText(source[:match[0]]); err != nil {
			return "", err
		}
		name := source[match[2]:match[3]]
		value, ok := variables[name]
		if !ok {
			return "", fmt.Errorf("неизвестная переменная %q", name)
		}
		if err := appendText(value); err != nil {
			return "", err
		}
		source = source[match[1]:]
	}
	return out.String(), nil
}

func runReadPointer(value any, pointer string) (any, bool) {
	if !validExecutionPointer(pointer) {
		return nil, false
	}
	if pointer == "" {
		return value, true
	}
	for raw := range strings.SplitSeq(pointer[1:], "/") {
		token := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		switch node := value.(type) {
		case map[string]any:
			var exists bool
			value, exists = node[token]
			if !exists {
				return nil, false
			}
		case []any:
			if token == "" || (len(token) > 1 && token[0] == '0') {
				return nil, false
			}
			for _, ch := range token {
				if ch < '0' || ch > '9' {
					return nil, false
				}
			}
			index, err := strconv.Atoi(token)
			if err != nil || index >= len(node) {
				return nil, false
			}
			value = node[index]
		default:
			return nil, false
		}
	}
	return value, true
}

func runEqualJSON(left, right any) bool {
	switch left := left.(type) {
	case nil:
		return right == nil
	case bool:
		other, ok := right.(bool)
		return ok && left == other
	case string:
		other, ok := right.(string)
		return ok && left == other
	case jsonx.Number:
		other, ok := right.(jsonx.Number)
		if !ok {
			return false
		}
		ls, le := normalizeRunNumber(string(left))
		rs, re := normalizeRunNumber(string(other))
		return ls == rs && le.Cmp(re) == 0
	case []any:
		other, ok := right.([]any)
		if !ok || len(left) != len(other) {
			return false
		}
		for i := range left {
			if !runEqualJSON(left[i], other[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		other, ok := right.(map[string]any)
		if !ok || len(left) != len(other) {
			return false
		}
		for key, value := range left {
			actual, ok := other[key]
			if !ok || !runEqualJSON(value, actual) {
				return false
			}
		}
		return true
	}
	return false
}

// Normalize decimal coefficients instead of expanding powers of ten. Exponents
// such as 1e1000000000 stay small and exact without allocating a billion digits.
func normalizeRunNumber(text string) (string, *big.Int) {
	coefficient, exponentText, hasExponent := strings.Cut(strings.ToLower(text), "e")
	exponent := new(big.Int)
	if hasExponent {
		exponent.SetString(exponentText, 10)
	}
	negative := strings.HasPrefix(coefficient, "-")
	coefficient = strings.TrimPrefix(coefficient, "-")
	if integer, fraction, ok := strings.Cut(coefficient, "."); ok {
		coefficient = integer + fraction
		exponent.Sub(exponent, big.NewInt(int64(len(fraction))))
	}
	coefficient = strings.TrimLeft(coefficient, "0")
	if coefficient == "" {
		return "0", new(big.Int)
	}
	trimmed := strings.TrimRight(coefficient, "0")
	exponent.Add(exponent, big.NewInt(int64(len(coefficient)-len(trimmed))))
	if negative {
		trimmed = "-" + trimmed
	}
	return trimmed, exponent
}

func cloneStepRequest(request StepRequest) StepRequest {
	request.PathParams = maps.Clone(request.PathParams)
	request.Query = maps.Clone(request.Query)
	request.Headers = maps.Clone(request.Headers)
	return request
}

func cloneRunReport(report RunReport) RunReport {
	// Prepared documents have already passed cloneDocument's JSON boundary.
	report.Document = cloneRunDocument(report.Document)
	report.InputVariables = maps.Clone(report.InputVariables)
	report.Variables = maps.Clone(report.Variables)
	if report.FinishedAt != nil {
		report.FinishedAt = new(*report.FinishedAt)
	}
	report.Steps = slices.Clone(report.Steps)
	report.ControlFlow = slices.Clone(report.ControlFlow)
	for i := range report.ControlFlow {
		report.ControlFlow[i].Iterations = slices.Clone(report.ControlFlow[i].Iterations)
	}
	for i := range report.Steps {
		step := &report.Steps[i]
		step.Iterations = slices.Clone(step.Iterations)
		step.BindingResults = slices.Clone(step.BindingResults)
		for j := range step.BindingResults {
			step.BindingResults[j].SourceIterations = slices.Clone(step.BindingResults[j].SourceIterations)
			if step.BindingResults[j].TransformedValueJSON != nil {
				step.BindingResults[j].TransformedValueJSON = new(*step.BindingResults[j].TransformedValueJSON)
			}
		}
		if step.Request != nil {
			step.Request = new(cloneStepRequest(*step.Request))
		}
		if step.Response != nil {
			step.Response = new(*step.Response)
			step.Response.Headers = maps.Clone(step.Response.Headers)
		}
		step.Assertions = slices.Clone(step.Assertions)
		for j := range step.Assertions {
			if step.Assertions[j].ActualJSON != nil {
				step.Assertions[j].ActualJSON = new(*step.Assertions[j].ActualJSON)
			}
		}
	}
	return report
}

func cloneRunDocument(document Document) Document {
	document.Participants = slices.Clone(document.Participants)
	document.Fragments = cloneRunFragments(document.Fragments)
	document.Contracts = slices.Clone(document.Contracts)
	for i := range document.Contracts {
		contract := &document.Contracts[i]
		contract.Document = slices.Clone(contract.Document)
		if contract.Source != nil {
			contract.Source = new(*contract.Source)
		}
	}
	if document.Execution != nil {
		document.Execution = &Execution{Variables: maps.Clone(document.Execution.Variables)}
	}
	document.Messages = slices.Clone(document.Messages)
	for i := range document.Messages {
		message := &document.Messages[i]
		message.EventBindings = slices.Clone(message.EventBindings)
		if message.Operation != nil {
			message.Operation = new(*message.Operation)
		}
		if message.Execution != nil {
			message.Execution = cloneRunStepExecution(*message.Execution)
		}
	}
	if document.EventModel != nil {
		document.EventModel = cloneRunEventModel(*document.EventModel)
	}
	return document
}

func cloneRunFragments(fragments []Fragment) []Fragment {
	fragments = slices.Clone(fragments)
	for i := range fragments {
		fragments[i].Branches = slices.Clone(fragments[i].Branches)
		if fragments[i].Execution != nil {
			fragments[i].Execution = new(*fragments[i].Execution)
			fragments[i].Execution.Condition = cloneRunCondition(fragments[i].Execution.Condition)
		}
		for j := range fragments[i].Branches {
			if b := fragments[i].Branches[j].Execution; b != nil {
				fragments[i].Branches[j].Execution = new(*b)
				fragments[i].Branches[j].Execution.Condition = cloneRunCondition(b.Condition)
			}
		}
	}
	return fragments
}

func cloneRunCondition(c *ExecutionCondition) *ExecutionCondition {
	if c == nil {
		return nil
	}
	out := new(*c)
	if c.Value != nil {
		out.Value = new(*c.Value)
	}
	return out
}

func cloneRunStepExecution(config StepExecution) *StepExecution {
	config.PathParams, config.Query, config.Headers = maps.Clone(config.PathParams), maps.Clone(config.Query), maps.Clone(config.Headers)
	if config.ExpectedStatus != nil {
		config.ExpectedStatus = new(*config.ExpectedStatus)
	}
	config.Assertions = slices.Clone(config.Assertions)
	for j := range config.Assertions {
		config.Assertions[j].Equals = slices.Clone(config.Assertions[j].Equals)
	}
	config.Extract = slices.Clone(config.Extract)
	config.Bindings = slices.Clone(config.Bindings)
	for j := range config.Bindings {
		config.Bindings[j].Transforms = slices.Clone(config.Bindings[j].Transforms)
	}
	return &config
}

func cloneRunEventModel(model EventModel) *EventModel {
	model.Servers = slices.Clone(model.Servers)
	model.Channels = slices.Clone(model.Channels)
	for i := range model.Channels {
		model.Channels[i].ServerIDs = slices.Clone(model.Channels[i].ServerIDs)
		model.Channels[i].MessageIDs = slices.Clone(model.Channels[i].MessageIDs)
		if model.Channels[i].Kafka != nil {
			kafka := *model.Channels[i].Kafka
			if kafka.Partitions != nil {
				kafka.Partitions = new(*kafka.Partitions)
			}
			if kafka.Replicas != nil {
				kafka.Replicas = new(*kafka.Replicas)
			}
			model.Channels[i].Kafka = &kafka
		}
	}
	model.Messages = slices.Clone(model.Messages)
	for i := range model.Messages {
		model.Messages[i].Examples = slices.Clone(model.Messages[i].Examples)
	}
	model.Schemas = slices.Clone(model.Schemas)
	model.Contracts = slices.Clone(model.Contracts)
	for i := range model.Contracts {
		model.Contracts[i].Operations = slices.Clone(model.Contracts[i].Operations)
		for j := range model.Contracts[i].Operations {
			operation := &model.Contracts[i].Operations[j]
			if operation.Kafka != nil {
				operation.Kafka = new(*operation.Kafka)
			}
			if operation.FailureRoutes != nil {
				operation.FailureRoutes = new(*operation.FailureRoutes)
			}
			operation.APILinks = slices.Clone(operation.APILinks)
			operation.StateLinks = slices.Clone(operation.StateLinks)
		}
	}
	return &model
}

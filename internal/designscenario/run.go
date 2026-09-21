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
	errRunDataLimit = errors.New("Суммарный размер запросов, ответов и проверок превышает 20 МиБ; последнее значение не сохранено")
)

// PrepareRun validates all executable bindings before any request is dispatched.
// The report owns its document and variables; overrides never modify the revision.
func PrepareRun(revision Revision, runID, name, source string, variables ExecutionValues) (RunReport, error) {
	invalid := func(message string) (RunReport, error) { return RunReport{}, fmt.Errorf("%w: %s", ErrInvalid, message) }
	if !runIDPattern.MatchString(runID) || utf8.RuneCountInString(name) > 200 || (source != "ui" && source != "mcp") || revision.ID <= 0 || revision.ScenarioID <= 0 {
		return invalid("Укажите допустимые ID, название и источник запуска")
	}
	if len(revision.Document.Fragments) != 0 {
		return invalid("Исполнение opt/loop пока не поддерживается; удалите фрагменты")
	}
	for key, value := range revision.FormDrafts {
		var fields map[string]jsonx.RawMessage
		if key == "all" && jsonx.Unmarshal([]byte(value), &fields) == nil && fields != nil && len(fields) == 0 {
			continue
		}
		return invalid("Сначала завершите редактирование форм сценария")
	}
	if diagnostics := executionDiagnostics(revision.Document); len(diagnostics) != 0 {
		return invalid(diagnostics[0].Pointer + ": " + diagnostics[0].Message)
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
		return invalid("В сценарии нет включённых HTTP-запросов с операцией API")
	}
	return report, nil
}

func validateRunVariables(variables ExecutionValues) error {
	if len(variables) > MaxExecutionEntries {
		return errors.New("Прогон не может содержать более 100 переменных")
	}
	for key, value := range variables {
		if !executionVariableName.MatchString(key) || utf8.RuneCountInString(value) > maxText {
			return fmt.Errorf("Недопустимое имя или слишком большое значение переменной %q", key)
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
				return errors.New("Суммарный размер снимков переменных превышает 20 МиБ")
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
			return nil, fmt.Errorf("Свяжите контракт сообщения %q с проектом API", message.ID)
		}
		keys, diagnostics := operationKeys(contract.Document, "")
		if _, ok := keys[message.Operation.OperationKey]; !ok || len(diagnostics) > 0 {
			return nil, fmt.Errorf("Операция API сообщения %q недоступна", message.ID)
		}
		return contract, nil
	}
	return nil, fmt.Errorf("Контракт сообщения %q недоступен", message.ID)
}

type runEngine struct {
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
	for i, message := range e.report.Document.Messages {
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
	request, err := resolveRunRequest(e.report.RevisionID, message.ID, config, e.report.Variables)
	if err != nil {
		return err
	}
	if err = e.retain(request, 0); err != nil {
		return err
	}
	e.report.Steps[index].Request = &request
	if err = e.publish(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if execute == nil {
		return errors.New("Исполнение мока недоступно")
	}
	stepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	response, err := execute(stepCtx, cloneStepRequest(request))
	stepError := stepCtx.Err()
	cancel()
	if stepError != nil {
		return fmt.Errorf("Запрос отменён или превысил время ожидания: %w", stepError)
	}
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if len(response.Body) > MaxExecutionBody {
		return errors.New("Ответ мока превышает 1 МиБ; тело не сохранено")
	}
	if err = e.retain(response, 0); err != nil {
		return err
	}
	response.Headers = maps.Clone(response.Headers)
	if response.Headers == nil {
		response.Headers = map[string]string{}
	}
	e.report.Steps[index].Response = &response
	if err = e.publish(); err != nil {
		return err
	}
	contract, err := runContract(e.report.Document, message)
	if err != nil {
		return err
	}
	if response.ScenarioRevisionID != e.report.RevisionID || response.DesignID != contract.Source.DesignID || response.DesignRevisionID != contract.Source.RevisionID {
		return errors.New("Ответ сервера относится к другой ревизии сценария или API")
	}
	if config.ExpectedStatus == nil {
		if response.Status < 200 || response.Status >= 300 {
			return fmt.Errorf("HTTP %d: ожидался статус 2xx", response.Status)
		}
	} else if response.Status != *config.ExpectedStatus {
		return fmt.Errorf("HTTP %d: ожидался %d", response.Status, *config.ExpectedStatus)
	}
	if len(config.Assertions) == 0 && len(config.Extract) == 0 {
		return nil
	}
	if !jsonx.Valid([]byte(response.Body)) {
		return errors.New("Ответ не содержит допустимый JSON")
	}
	body, err := decodeJSONValue([]byte(response.Body))
	if err != nil {
		return fmt.Errorf("Не удалось прочитать JSON ответа: %w", err)
	}
	assertionsPassed := true
	for _, assertion := range config.Assertions {
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
		if err = e.retain(result, -1); err != nil {
			return err
		}
		e.report.Steps[index].Assertions = append(e.report.Steps[index].Assertions, result)
		assertionsPassed = assertionsPassed && result.Passed
	}
	if err = e.publish(); err != nil {
		return err
	}
	if !assertionsPassed {
		return errors.New("Проверка JSON-ответа не пройдена")
	}
	variables := maps.Clone(e.report.Variables)
	for _, extraction := range config.Extract {
		value, exists := runReadPointer(body, extraction.Pointer)
		if !exists {
			return fmt.Errorf("Не найден JSON Pointer %q для переменной %q", extraction.Pointer, extraction.Name)
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
	if err = validateRunVariables(variables); err != nil {
		return err
	}
	if err = validateRunVariableSnapshots(e.report.InputVariables, variables); err != nil {
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
		return fmt.Errorf("Не удалось сохранить прогресс запуска: %w", err)
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
		if step.Status == "pending" {
			step.Status, step.Reason = "skipped", "Предыдущий шаг не выполнен."
			if status == "cancelled" {
				step.Reason = "Прогон отменён."
			}
		} else if step.Status == "running" {
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
				return StepRequest{}, fmt.Errorf("Значение параметра %q после подстановки слишком большое", key)
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
			return errors.New("Значение после подстановки превышает допустимый размер")
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
			return "", fmt.Errorf("Неизвестная переменная %q", name)
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
	for i := range report.Steps {
		step := &report.Steps[i]
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
	document.Fragments = slices.Clone(document.Fragments)
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
		if message.Operation != nil {
			message.Operation = new(*message.Operation)
		}
		if message.Execution == nil {
			continue
		}
		message.Execution = new(*message.Execution)
		config := message.Execution
		config.PathParams, config.Query, config.Headers = maps.Clone(config.PathParams), maps.Clone(config.Query), maps.Clone(config.Headers)
		if config.ExpectedStatus != nil {
			config.ExpectedStatus = new(*config.ExpectedStatus)
		}
		config.Assertions = slices.Clone(config.Assertions)
		for j := range config.Assertions {
			config.Assertions[j].Equals = slices.Clone(config.Assertions[j].Equals)
		}
		config.Extract = slices.Clone(config.Extract)
	}
	return document
}

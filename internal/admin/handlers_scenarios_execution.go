package admin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/workspaces"
)

// ScenarioExecutor shares the live runtime without dispatching an HTTP client
// to a user-controlled URL or resolving a workspace again during execution.
type ScenarioExecutor interface {
	ServeWorkspace(http.ResponseWriter, *http.Request, *workspaces.Workspace)
}

// SetScenarioExecutor must be called during startup, before serving requests.
func (s *Server) SetScenarioExecutor(executor ScenarioExecutor) { s.scenarioExecutor = executor }

type ExecuteDesignScenarioStepRequest = designscenario.StepRequest
type ExecuteDesignScenarioStepResponse = designscenario.StepResponse

type scenarioStepError struct {
	status  int
	code    string
	message string
}

func (e *scenarioStepError) Error() string { return e.message }
func stepError(status int, code, message string) error {
	return &scenarioStepError{status: status, code: code, message: message}
}

func (s *Server) handleExecuteDesignScenarioStep(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designScenarioRequest(w, r)
	if !ok {
		return
	}
	var input ExecuteDesignScenarioStepRequest
	if !s.designScenarioBody(w, r, &input) {
		return
	}
	revision, err := s.designScenariosRepo.Revision(r.Context(), id, input.RevisionID)
	if err != nil {
		s.designScenarioError(w, err)
		return
	}
	response, err := s.executeScenarioStep(r.Context(), revision, input)
	if err != nil {
		s.writeScenarioStepError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, response)
}

func (s *Server) writeScenarioStepError(w http.ResponseWriter, err error) {
	if failure, ok := errors.AsType[*scenarioStepError](err); ok {
		httpx.Err(w, failure.status, failure.code, failure.message)
		return
	}
	s.designScenarioError(w, err)
}

func (s *Server) stepRepositoryError(err error) error {
	if errors.Is(err, apidesign.ErrNotFound) || errors.Is(err, workspaces.ErrNotFound) {
		return stepError(404, httpx.CodeNotFound, "Проект или версия API не найдены")
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return stepError(504, "execution_cancelled", "Запрос отменён или превысил время ожидания")
	}
	s.log.Error("execute scenario step", "err", err)
	return stepError(500, httpx.CodeInternal, "Не удалось выполнить запрос мока")
}

// executeScenarioStep is shared by the single-step endpoint and server runs.
// It dispatches only a saved operation and never opens a network connection.
func (s *Server) executeScenarioStep(ctx context.Context, revision designscenario.Revision, input designscenario.StepRequest) (designscenario.StepResponse, error) {
	fail := func(err error) (designscenario.StepResponse, error) {
		return designscenario.StepResponse{}, stepError(400, "design_scenario_execution_invalid", err.Error())
	}
	if err := validateStepRequest(input); err != nil {
		return fail(err)
	}
	if input.RevisionID != revision.ID {
		return fail(errors.New("версия запроса не совпадает с версией сценария"))
	}
	contract, err := executableContract(revision, input.MessageID)
	if err != nil {
		return fail(err)
	}
	if s.scenarioExecutor == nil {
		return designscenario.StepResponse{}, stepError(503, "service_unavailable", "Исполнение мока недоступно")
	}
	design, ws, err := s.stepExecutionWorkspace(ctx, contract)
	if err != nil {
		return designscenario.StepResponse{}, err
	}
	method, route, err := savedStepOperation(revision.Document, input.MessageID, design.Draft.Document)
	if err != nil {
		return fail(err)
	}
	path, err := stepRequestPath(ws.Settings.BasePath, route, input.PathParams, input.Query, s.cfg.ReservedPrefix)
	if err != nil {
		return fail(err)
	}
	stepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(stepCtx, method, path, strings.NewReader(input.Body))
	if err != nil {
		return fail(errors.New("не удалось сформировать запрос"))
	}
	for key, value := range input.Headers {
		req.Header.Set(key, value)
	}
	capBytes := int64(designscenario.MaxExecutionBody)
	if s.cfg.MaxResponse > 0 {
		capBytes = min(capBytes, s.cfg.MaxResponse)
	}
	response := &stepResponseWriter{header: http.Header{}, limit: capBytes, cancel: cancel}
	started := time.Now()
	if stepCtx.Err() == nil {
		s.scenarioExecutor.ServeWorkspace(response, req, ws)
	}
	duration := float64(time.Since(started).Microseconds()) / 1000
	if response.overflow {
		return designscenario.StepResponse{}, stepError(413, httpx.CodeTooLarge, "Ответ мока превышает допустимый размер")
	}
	if stepCtx.Err() != nil {
		return designscenario.StepResponse{}, stepError(504, "execution_cancelled", "Запрос отменён или превысил время ожидания")
	}
	current, err := s.designsRepo.Detail(ctx, contract.Source.DesignID)
	if err != nil {
		return designscenario.StepResponse{}, s.stepRepositoryError(err)
	}
	if err := stepDesignConflict(contract, current); err != nil {
		return designscenario.StepResponse{}, err
	}
	headers := map[string]string{}
	var headerBytes int64
	for key, values := range response.header {
		value := strings.Join(values, ", ")
		headerBytes += int64(len(key) + len(value))
		if headerBytes > 64<<10 {
			return designscenario.StepResponse{}, stepError(413, httpx.CodeTooLarge, "Заголовки ответа мока превышают допустимый размер")
		}
		headers[key] = value
	}
	status := response.status
	if status == 0 {
		status = http.StatusOK
	}
	return designscenario.StepResponse{ScenarioRevisionID: revision.ID, DesignID: design.Design.ID, DesignRevisionID: design.Draft.ID, WorkspaceRevision: ws.Revision, Method: method, Path: path, Status: status, Headers: headers, Body: response.body.String(), DurationMS: duration}, nil
}

func (s *Server) stepExecutionWorkspace(ctx context.Context, contract *designscenario.Contract) (*apidesign.Detail, *workspaces.Workspace, error) {
	design, err := s.designsRepo.Detail(ctx, contract.Source.DesignID)
	if err != nil {
		return nil, nil, s.stepRepositoryError(err)
	}
	if err := stepDesignConflict(contract, design); err != nil {
		return nil, nil, err
	}
	ws, err := s.ws.ByID(ctx, design.Design.DraftWorkspaceID)
	if err != nil {
		return nil, nil, s.stepRepositoryError(err)
	}
	// Fence the two repository snapshots before dispatching the workspace snapshot.
	current, err := s.designsRepo.Detail(ctx, contract.Source.DesignID)
	if err != nil {
		return nil, nil, s.stepRepositoryError(err)
	}
	if err := stepDesignConflict(contract, current); err != nil {
		return nil, nil, err
	}
	return design, ws, nil
}

func stepDesignConflict(contract *designscenario.Contract, design *apidesign.Detail) error {
	if design.Design.DraftRevisionID == contract.Source.RevisionID {
		return nil
	}
	return &designscenario.LinkedConflictError{ContractID: contract.ID, DesignID: design.Design.ID, Version: design.Design.Version, DraftRevisionID: design.Design.DraftRevisionID}
}

func executableContract(revision designscenario.Revision, messageID string) (*designscenario.Contract, error) {
	if len(revision.Document.Fragments) != 0 {
		return nil, errors.New("исполнение opt/loop пока не поддерживается; удалите фрагменты")
	}
	for key, value := range revision.FormDrafts {
		// The UI persists its complete form-store envelope even when clean.
		var fields map[string]jsonx.RawMessage
		if key == "all" && jsonx.Unmarshal([]byte(value), &fields) == nil && fields != nil && len(fields) == 0 {
			continue
		}
		return nil, errors.New("сначала завершите редактирование форм сценария")
	}
	for _, message := range revision.Document.Messages {
		if message.ID != messageID {
			continue
		}
		if message.Kind != "request" || message.Operation == nil {
			return nil, errors.New("сообщение не является HTTP-запросом с API-операцией")
		}
		if message.Execution != nil && !message.Execution.Enabled {
			return nil, errors.New("шаг выключен")
		}
		for _, contract := range revision.Document.Contracts {
			if contract.ID == message.Operation.ContractID && contract.Mode == "linked" && contract.Source != nil {
				return &contract, nil
			}
		}
		return nil, errors.New("свяжите контракт шага с API через панель контрактов")
	}
	return nil, errors.New("сообщение отсутствует в сохранённой версии сценария")
}

func savedStepOperation(document designscenario.Document, messageID, raw string) (string, string, error) {
	var key string
	for _, message := range document.Messages {
		if message.ID == messageID && message.Operation != nil {
			key = message.Operation.OperationKey
			break
		}
	}
	var root struct {
		Paths map[string]map[string]jsonx.RawMessage `json:"paths"`
	}
	if err := jsonx.Unmarshal([]byte(raw), &root); err != nil {
		return "", "", err
	}
	for path, item := range root.Paths {
		for _, method := range []string{"get", "post", "put", "patch", "delete", "options", "head", "trace"} {
			var operation map[string]jsonx.RawMessage
			if err := jsonx.Unmarshal(item[method], &operation); err != nil {
				continue
			}
			var operationKey string
			if err := jsonx.Unmarshal(operation[apidesign.OperationKey], &operationKey); err == nil && operationKey == key {
				return strings.ToUpper(method), path, nil
			}
		}
	}
	return "", "", errors.New("операция отсутствует в закреплённой версии API")
}

var stepPathParameter = regexp.MustCompile(`\{([^{}]+)\}`)
var stepHeaderName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

func validateStepRequest(input ExecuteDesignScenarioStepRequest) error {
	if input.RevisionID <= 0 || input.MessageID == "" || len(input.MessageID) > 50_000 {
		return errors.New("укажите версию сценария и сообщение")
	}
	if len(input.Body) > designscenario.MaxExecutionBody {
		return errors.New("тело запроса превышает 1 МиБ")
	}
	for _, values := range []map[string]string{input.PathParams, input.Query, input.Headers} {
		if values == nil || len(values) > designscenario.MaxExecutionEntries {
			return errors.New("параметры должны содержать не более 100 значений")
		}
		for key, value := range values {
			if key == "" || utf8.RuneCountInString(key) > 256 || utf8.RuneCountInString(value) > 50_000 {
				return errors.New("параметр запроса превышает допустимую длину")
			}
		}
	}
	return validateStepHeaders(input.Headers)
}

func validateStepHeaders(headers map[string]string) error {
	names := map[string]bool{}
	for name, value := range headers {
		lower := strings.ToLower(name)
		if names[lower] || !stepHeaderName.MatchString(name) || strings.HasPrefix(lower, "access-control-") || strings.HasPrefix(lower, "x-mocker-") || strings.HasPrefix(lower, "sec-") || strings.HasPrefix(lower, "proxy-") || strings.HasPrefix(lower, "x-forwarded-") || slices.Contains([]string{"host", "cookie", "cookie2", "connection", "content-length", "transfer-encoding", "te", "trailer", "upgrade", "expect", "keep-alive", "forwarded", "origin", "referer", "x-csrf-token", "accept-encoding"}, lower) {
			return fmt.Errorf("заголовок %q запрещён", name)
		}
		names[lower] = true
		for _, ch := range value {
			if ch < 32 || ch == 127 {
				return fmt.Errorf("заголовок %q содержит управляющие символы", name)
			}
		}
	}
	return nil
}

func stepRequestPath(base, route string, params, query map[string]string, reserved string) (string, error) {
	path := strings.TrimRight(base, "/") + route
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "?#\\\r\n") {
		return "", errors.New("недопустимый путь API-операции")
	}
	path, err := expandStepPath(path, params)
	if err != nil {
		return "", err
	}
	segments, err := executionPathSegments(path)
	if err != nil {
		return "", err
	}
	prefix, err := executionPathSegments(reserved)
	if err != nil {
		return "", err
	}
	if len(prefix) > 0 && len(segments) >= len(prefix) && slices.Equal(segments[:len(prefix)], prefix) {
		return "", errors.New("управляющие маршруты мока недоступны для исполнения")
	}
	values := url.Values{}
	for key, value := range query {
		values.Set(key, value)
	}
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	if len(path) > 64<<10 {
		return "", errors.New("путь запроса превышает допустимую длину")
	}
	return path, nil
}

func expandStepPath(path string, params map[string]string) (string, error) {
	used := map[string]bool{}
	var invalid error
	path = stepPathParameter.ReplaceAllStringFunc(path, func(token string) string {
		name := token[1 : len(token)-1]
		value, ok := params[name]
		used[name] = true
		if !ok || value == "" || value == "." || value == ".." || strings.ContainsAny(value, "\\\x00\r\n") {
			invalid = fmt.Errorf("укажите допустимый path-параметр %q", name)
		}
		return url.PathEscape(value)
	})
	if invalid != nil {
		return "", invalid
	}
	if strings.ContainsAny(path, "{}") {
		return "", errors.New("неразрешённый path-параметр")
	}
	for name := range params {
		if !used[name] {
			return "", fmt.Errorf("неизвестный path-параметр %q", name)
		}
	}
	return path, nil
}

func executionPathSegments(path string) ([]string, error) {
	segments := []string{}
	for segment := range strings.SplitSeq(path, "/") {
		if segment == "" {
			continue
		}
		decoded, err := url.PathUnescape(segment)
		if err != nil || decoded == "." || decoded == ".." || strings.ContainsAny(decoded, "\\\x00\r\n") {
			return nil, errors.New("недопустимый сегмент пути")
		}
		segments = append(segments, decoded)
	}
	return segments, nil
}

type stepResponseWriter struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	limit    int64
	overflow bool
	cancel   context.CancelFunc
}

func (w *stepResponseWriter) Header() http.Header { return w.header }
func (w *stepResponseWriter) WriteHeader(status int) {
	if w.status == 0 && status >= 200 {
		w.status = status
	}
}
func (w *stepResponseWriter) Write(body []byte) (int, error) {
	if int64(w.body.Len())+int64(len(body)) > w.limit {
		w.overflow = true
		w.cancel()
		return 0, errors.New("mock response is too large")
	}
	w.WriteHeader(http.StatusOK)
	return w.body.Write(body)
}

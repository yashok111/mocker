package admin

import (
	"bytes"
	"errors"
	"net/http"
	"slices"
	"strconv"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/responserules"
)

func (s *Server) handleListResponseRules(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	result, err := s.designsRepo.ResponseRules(r.Context(), id)
	if err != nil {
		s.designError(w, err)
		return
	}
	httpx.JSON(w, 200, result)
}
func (s *Server) handleGetResponseRule(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	result, err := s.designsRepo.ResponseRules(r.Context(), id)
	if err != nil {
		s.designError(w, err)
		return
	}
	for _, rule := range result.Rules {
		if rule.ID == r.PathValue("rid") {
			httpx.JSON(w, 200, map[string]any{"designId": id, "version": result.Version, "revisionId": result.RevisionID, "rule": rule})
			return
		}
	}
	s.designError(w, apidesign.ErrNotFound)
}
func (s *Server) handleCreateResponseRule(w http.ResponseWriter, r *http.Request) {
	s.editResponseRule(w, r, "create")
}
func (s *Server) handleSaveResponseRule(w http.ResponseWriter, r *http.Request) {
	s.editResponseRule(w, r, "save")
}
func (s *Server) handleDeleteResponseRule(w http.ResponseWriter, r *http.Request) {
	s.editResponseRule(w, r, "delete")
}
func (s *Server) handleResponseRuleCommands(w http.ResponseWriter, r *http.Request) {
	s.editResponseRule(w, r, "commands")
}

// responseRuleBody retains raw payloads so union decoders see field presence,
// including forbidden nulls and zero-valued fields of another node kind.
func (s *Server) responseRuleBody(w http.ResponseWriter, r *http.Request, allowed, required []string) (map[string]jsonx.RawMessage, bool) {
	var raw jsonx.RawMessage
	if !s.designBody(w, r, &raw) {
		return nil, false
	}
	if r.Context().Err() != nil {
		return nil, false
	}
	var fields map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(raw, &fields); err != nil || fields == nil {
		s.responseRuleInvalid(w, "", "Ожидается объект запроса")
		return nil, false
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			s.responseRuleInvalid(w, "/"+key, "Обязательное поле отсутствует")
			return nil, false
		}
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if !slices.Contains(allowed, key) {
			s.responseRuleInvalid(w, "/"+key, "Неизвестное поле")
			return nil, false
		}
		if bytes.Equal(bytes.TrimSpace(fields[key]), []byte("null")) {
			s.responseRuleInvalid(w, "/"+key, "null недопустим")
			return nil, false
		}
	}
	return fields, true
}
func (s *Server) responseRuleInvalid(w http.ResponseWriter, pointer, message string) {
	s.designError(w, &apidesign.InvalidError{Diagnostics: []apidesign.Diagnostic{{Pointer: pointer, Message: message, Severity: "error"}}})
}

func (s *Server) editResponseRule(w http.ResponseWriter, r *http.Request, action string) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	fields := []string{"expectedVersion"}
	switch action {
	case "create", "save":
		fields = append(fields, "rule")
	case "commands":
		fields = append(fields, "commands")
	}
	body, ok := s.responseRuleBody(w, r, fields, fields)
	if !ok {
		return
	}
	var version int64
	if err := jsonx.Unmarshal(body["expectedVersion"], &version); err != nil || version <= 0 {
		s.responseRuleInvalid(w, "/expectedVersion", "Обязательна положительная целая версия API")
		return
	}
	var rule *responserules.Rule
	var commands []responserules.Command
	if raw, exists := body["rule"]; exists {
		if err := jsonx.Unmarshal(raw, &rule); err != nil {
			s.responseRuleDecodeError(w, "/rule", err)
			return
		}
	}

	if raw, exists := body["commands"]; exists {
		var items []jsonx.RawMessage
		if err := jsonx.Unmarshal(raw, &items); err != nil {
			s.responseRuleDecodeError(w, "/commands", err)
			return
		}
		if len(items) > responserules.MaxCommands {
			s.responseRuleInvalid(w, "/commands", "Допустимо не более 200 команд")
			return
		}
		commands = make([]responserules.Command, len(items))
		for i, item := range items {
			if err := r.Context().Err(); err != nil {
				return
			}
			if err := jsonx.Unmarshal(item, &commands[i]); err != nil {
				s.responseRuleDecodeError(w, "/commands/"+strconv.Itoa(i), err)
				return
			}
		}
	}

	ruleID := r.PathValue("rid")
	if action == "create" {
		ruleID = rule.ID
	}
	d, err := s.designsRepo.EditResponseRule(r.Context(), id, version, designActor(r), ruleID, action, rule, commands)
	status := 200
	if action == "create" {
		status = 201
	}
	s.designDetailResponse(w, r, status, d, err)
}
func (s *Server) handleValidateResponseRule(w http.ResponseWriter, r *http.Request) {
	s.evaluateResponseRule(w, r, false)
}
func (s *Server) handleSimulateResponseRule(w http.ResponseWriter, r *http.Request) {
	s.evaluateResponseRule(w, r, true)
}
func (s *Server) evaluateResponseRule(w http.ResponseWriter, r *http.Request, simulate bool) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	fields := []string{"document"}
	required := []string{}
	if simulate {
		fields = append(fields, "request", "exampleId")
	}
	body, ok := s.responseRuleBody(w, r, fields, required)
	if !ok {
		return
	}
	var proposal apidesign.ResponseRuleProposal
	if raw, exists := body["document"]; exists {
		if err := jsonx.Unmarshal(raw, &proposal.Document); err != nil {
			s.responseRuleInvalid(w, "/document", "Ожидается строка документа")
			return
		}
	}
	var request responserules.Request
	var exampleID string
	if simulate {
		if request, exampleID, ok = s.responseRuleSimulationInput(w, body); !ok {
			return
		}
	}
	resolved, err := s.designsRepo.ResolveResponseRule(r.Context(), id, r.PathValue("rid"), proposal)
	if err != nil {
		s.designError(w, err)
		return
	}
	var result any
	if simulate {
		if exampleID != "" {
			rule := slices.IndexFunc(resolved.Envelope.Rules, func(rule responserules.Rule) bool { return rule.ID == r.PathValue("rid") })
			examples := resolved.Envelope.Rules[rule].Examples
			i := slices.IndexFunc(examples, func(example responserules.Example) bool { return example.ID == exampleID })
			if i < 0 {
				s.designError(w, apidesign.ErrNotFound)
				return
			}
			request = examples[i].Request
		}
		simulation, err := responserules.Simulate(r.Context(), resolved.Envelope, r.PathValue("rid"), resolved.Root, request)
		if err != nil {
			httpx.Err(w, 400, "simulation_invalid", err.Error())
			return
		}
		result = struct {
			responserules.Simulation
			Source apidesign.ResponseRuleSource `json:"source"`
		}{simulation, resolved.Source}
	} else {
		validation, err := responserules.Validate(r.Context(), resolved.Envelope, r.PathValue("rid"), resolved.Root)
		if err != nil {
			httpx.Err(w, 400, "simulation_invalid", err.Error())
			return
		}
		result = struct {
			responserules.Validation
			Source apidesign.ResponseRuleSource `json:"source"`
		}{validation, resolved.Source}
	}
	raw, err := jsonx.Marshal(result)
	if err != nil {
		s.designError(w, err)
		return
	}
	if len(raw) > responserules.MaxResultBytes {
		httpx.Err(w, 400, "simulation_invalid", "Результат превышает допустимый размер")
		return
	}
	if r.Context().Err() != nil {
		return
	}
	httpx.JSON(w, 200, result)
}

// responseRuleSimulationInput reads the simulation's subject: exactly one of
// an inline request or the ID of a stored example, which the caller resolves
// once the rule itself is resolved. It writes the refusal itself.
func (s *Server) responseRuleSimulationInput(w http.ResponseWriter, body map[string]jsonx.RawMessage) (responserules.Request, string, bool) {
	var request responserules.Request
	var exampleID string
	requestRaw, hasRequest := body["request"]
	exampleRaw, hasExample := body["exampleId"]
	if hasRequest == hasExample {
		s.responseRuleInvalid(w, "/request", "Требуется ровно одно из request и exampleId")
		return request, "", false
	}
	if hasRequest {
		if err := jsonx.Unmarshal(requestRaw, &request); err != nil {
			httpx.Err(w, 400, "simulation_invalid", err.Error())
			return request, "", false
		}
	} else if err := jsonx.Unmarshal(exampleRaw, &exampleID); err != nil || !responserules.ValidID(exampleID) {
		s.responseRuleInvalid(w, "/exampleId", "Ожидается корректный ID примера")
		return request, "", false
	}
	return request, exampleID, true
}

func (s *Server) responseRuleDecodeError(w http.ResponseWriter, prefix string, err error) {
	if field, ok := errors.AsType[*responserules.FieldError](err); ok {
		s.responseRuleInvalid(w, prefix+field.Pointer, field.Message)
		return
	}
	s.responseRuleInvalid(w, prefix, err.Error())
}

package scenarioexport

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

var kafkaTopic = regexp.MustCompile(`^[A-Za-z0-9._-]{1,249}$`)

type eventValidationCache struct {
	ctx             context.Context
	rev             designscenario.Revision
	model           *designscenario.EventModel
	servers         map[string]designscenario.EventServer
	channels        map[string]designscenario.EventChannel
	messages        map[string]designscenario.EventMessage
	schemas         map[string]designscenario.EventSchema
	parsed          map[string]eventParsedSchema
	schemaPointers  map[string]string
	messagePointers map[string]string
}

type eventParsedSchema struct {
	value  any
	refs   []string
	issues []Diagnostic
}

func newEventValidationCache(ctx context.Context, rev designscenario.Revision) *eventValidationCache {
	c := &eventValidationCache{
		ctx: ctx, rev: rev, model: rev.Document.EventModel,
		servers: map[string]designscenario.EventServer{}, channels: map[string]designscenario.EventChannel{},
		messages: map[string]designscenario.EventMessage{}, schemas: map[string]designscenario.EventSchema{},
		parsed: map[string]eventParsedSchema{}, schemaPointers: map[string]string{}, messagePointers: map[string]string{},
	}
	if c.model != nil {
		for _, item := range c.model.Servers {
			c.servers[item.ID] = item
		}
		for _, item := range c.model.Channels {
			c.channels[item.ID] = item
		}
		for i, item := range c.model.Messages {
			c.messages[item.ID] = item
			c.messagePointers[item.ID] = fmt.Sprintf("/eventModel/messages/%d", i)
		}
		for i, item := range c.model.Schemas {
			c.schemas[item.ID] = item
			c.schemaPointers[item.ID] = fmt.Sprintf("/eventModel/schemas/%d/schemaJSON", i)
		}
	}
	return c
}

func (c *eventValidationCache) schemaPointer(id string) string {
	if pointer, ok := c.schemaPointers[id]; ok {
		return pointer
	}
	return "/eventModel/schemas"
}

func (s *Service) eventContractDiagnostics(c *eventValidationCache, contract designscenario.EventContract) ([]Diagnostic, error) {
	ds, _, err := s.prepareAsyncAPI(c, contract)
	if errors.Is(err, ErrTooLarge) {
		return []Diagnostic{diagnostic("event_export_too_large", "error", "Контракт превышает допустимый размер экспорта", "event-contract", contract.ID, "/eventModel/contracts")}, nil
	}
	return ds, err
}

func (s *Service) prepareAsyncAPI(c *eventValidationCache, contract designscenario.EventContract) ([]Diagnostic, []byte, error) {
	if err := c.ctx.Err(); err != nil {
		return nil, nil, err
	}
	ds := &eventDiagnostics{}
	add := func(code, severity, message, kind, id, pointer string) {
		ds.add(diagnostic(code, severity, message, kind, id, pointer))
	}
	if strings.TrimSpace(contract.Name) == "" || strings.TrimSpace(contract.Version) == "" || len(contract.Operations) == 0 {
		add("event_contract_empty", "error", "Заполните название, версию и операции событийного контракта", "event-contract", contract.ID, "/eventModel/contracts")
	}
	if c.model == nil {
		return ds.list, nil, nil
	}
	channels := map[string]designscenario.EventChannel{}
	messages := map[string]designscenario.EventMessage{}
	servers := map[string]designscenario.EventServer{}
	bound := map[string]bool{}
	for _, step := range c.rev.Document.Messages {
		for _, binding := range step.EventBindings {
			if binding.ContractID == contract.ID {
				bound[binding.OperationID] = true
			}
		}
	}
	for _, op := range contract.Operations {
		if err := c.ctx.Err(); err != nil {
			return nil, nil, err
		}
		if !bound[op.ID] {
			add("event_operation_unbound", "info", "Операция "+op.Name+" входит в контракт без привязки к стрелке", "event-contract", contract.ID, "/eventModel/contracts")
		}
		channel, ok := c.channels[op.ChannelID]
		if !ok {
			add("event_channel_missing", "error", "Канал операции отсутствует", "event-contract", contract.ID, "/eventModel/contracts")
			continue
		}
		channels[channel.ID] = channel
		if !slices.Contains(channel.MessageIDs, op.MessageID) {
			add("event_message_missing", "error", "Сообщение операции отсутствует в канале", "event-contract", contract.ID, "/eventModel/contracts")
		}
		for _, id := range channel.MessageIDs {
			m, found := c.messages[id]
			if !found {
				add("event_message_missing", "error", "Тип сообщения канала отсутствует", "event-channel", channel.ID, "/eventModel/channels")
				continue
			}
			messages[id] = m
		}
		for _, id := range channel.ServerIDs {
			server, found := c.servers[id]
			if !found {
				add("event_servers_incomplete", "error", "Сервер канала отсутствует", "event-channel", channel.ID, "/eventModel/channels")
				continue
			}
			servers[id] = server
		}
		if op.Action != "send" && op.Action != "receive" {
			add("event_operation_invalid", "error", "Действие операции должно быть send или receive", "event-contract", contract.ID, "/eventModel/contracts")
		}
		if op.Action == "receive" && (op.Kafka == nil || op.Kafka.GroupID == "") {
			add("event_group_unspecified", "warning", "Группа потребителя Kafka не указана", "event-contract", contract.ID, "/eventModel/contracts")
		}
		if op.Kafka != nil && (op.Kafka.GroupID != "" || op.Kafka.ClientID != "") && op.Action != "receive" {
			add("event_binding_invalid", "error", "groupId и clientId доступны только для receive", "event-contract", contract.ID, "/eventModel/contracts")
		}
		if op.Kafka != nil && (!validKafkaMetadata(op.Kafka.GroupID) || !validKafkaMetadata(op.Kafka.ClientID)) {
			add("event_binding_invalid", "error", "groupId/clientId содержат недопустимое значение", "event-contract", contract.ID, "/eventModel/contracts")
		}
	}
	known, unknown := 0, 0
	for _, id := range slices.Sorted(maps.Keys(channels)) {
		ch := channels[id]
		if len(ch.ServerIDs) == 0 {
			unknown++
		} else {
			known++
		}
	}
	if known > 0 && unknown > 0 {
		add("event_servers_incomplete", "error", "Укажите серверы для всех каналов контракта", "event-contract", contract.ID, "/eventModel/channels")
	}
	if known == 0 && unknown > 0 {
		add("event_servers_unspecified", "warning", "Серверы Kafka не описаны", "event-contract", contract.ID, "/eventModel/channels")
	}
	for _, id := range slices.Sorted(maps.Keys(channels)) {
		channel := channels[id]
		if channel.Address == "" || !kafkaTopic.MatchString(channel.Address) || channel.Address == "." || channel.Address == ".." {
			add("event_channel_invalid", "error", "Укажите допустимое имя Kafka topic", "event-channel", channel.ID, "/eventModel/channels")
		}
		if channel.Kafka != nil && ((channel.Kafka.Partitions != nil && *channel.Kafka.Partitions <= 0) || (channel.Kafka.Replicas != nil && *channel.Kafka.Replicas <= 0)) {
			add("event_binding_invalid", "error", "Partitions и replicas должны быть положительными", "event-channel", channel.ID, "/eventModel/channels")
		}
		if len(channel.MessageIDs) > 1 {
			c.checkDiscriminator(channel, ds)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(servers)) {
		server := servers[id]
		if !validKafkaHost(server.Host) {
			add("event_servers_incomplete", "error", "Укажите один допустимый Kafka host с необязательным портом", "event-server", server.ID, "/eventModel/servers")
		}
		if server.Protocol != "kafka" && server.Protocol != "kafka-secure" {
			add("event_binding_invalid", "error", "Недопустимый Kafka protocol", "event-server", server.ID, "/eventModel/servers")
		}
		if !slices.Contains([]string{"unspecified", "none", "plain", "scramSha256", "scramSha512"}, server.Auth) {
			add("event_binding_invalid", "error", "Недопустимый способ Kafka auth", "event-server", server.ID, "/eventModel/servers")
		}
		if server.Auth == "unspecified" {
			add("event_auth_unspecified", "warning", "Способ аутентификации Kafka не указан", "event-server", server.ID, "/eventModel/servers")
		}
	}
	usedSchemas := map[string]any{}
	for _, id := range slices.Sorted(maps.Keys(messages)) {
		m := messages[id]
		if m.PayloadSchemaID == "" {
			add("event_payload_missing", "error", "Укажите схему payload", "event-message", m.ID, "/eventModel/messages")
			continue
		}
		for _, id := range []string{m.PayloadSchemaID, m.HeadersSchemaID, m.KeySchemaID} {
			if id != "" {
				c.collectSchema(id, usedSchemas, ds)
			}
		}
		if m.HeadersSchemaID != "" {
			if v, ok := c.resolvedRoot(m.HeadersSchemaID, map[string]bool{}).(map[string]any); !ok || v["type"] != "object" {
				add("event_schema_invalid", "error", "Схема headers должна иметь type object", "event-schema", m.HeadersSchemaID, "/eventModel/schemas")
			}
		}
	}
	for _, d := range eventFormDiagnostics(c.rev.FormDrafts, contract.ID, channels, messages, usedSchemas, servers) {
		ds.add(d)
	}
	for _, step := range c.rev.Document.Messages {
		if step.Kind == "event" && len(step.EventBindings) == 0 {
			add("event_steps_descriptive", "info", "Описательная стрелка события не входит в AsyncAPI", "message", step.ID, "/messages")
		}
	}
	if len(c.rev.Document.Fragments) > 0 {
		add("event_sequence_semantics_omitted", "info", "Условия alt/opt/loop остаются в диаграмме", "event-contract", contract.ID, "/fragments")
	}
	if ds.hasErrors() {
		return ds.list, nil, nil
	}
	doc := c.renderAsyncAPIDocument(contract, channels, messages, servers, usedSchemas)
	raw, err := jsonx.Marshal(doc)
	if err != nil {
		return nil, nil, err
	}
	if s.maxBytes <= 0 || int64(len(raw)) > s.maxBytes {
		return nil, nil, ErrTooLarge
	}
	if err := c.validateExamples(doc, messages, ds); err != nil {
		return nil, nil, err
	}
	if !ds.hasErrors() {
		grammar, err := officialAsyncAPIGrammar()
		if err != nil {
			return nil, nil, err
		}
		if err := grammar.Validate(doc); err != nil {
			add("event_schema_invalid", "error", "AsyncAPI grammar: "+err.Error(), "event-contract", contract.ID, "/")
		}
	}
	if err := c.ctx.Err(); err != nil {
		return nil, nil, err
	}
	return ds.list, raw, nil
}

func ref(path string) map[string]any { return map[string]any{"$ref": path} }

func (c *eventValidationCache) renderAsyncAPIDocument(contract designscenario.EventContract, channels map[string]designscenario.EventChannel, messages map[string]designscenario.EventMessage, servers map[string]designscenario.EventServer, schemas map[string]any) map[string]any {
	doc := map[string]any{"asyncapi": "3.0.0", "info": map[string]any{"title": contract.Name, "version": contract.Version}, "defaultContentType": "application/json", "channels": map[string]any{}, "operations": map[string]any{}, "components": map[string]any{"messages": map[string]any{}, "schemas": schemas}, "x-mocker-scenario": map[string]any{"scenarioId": c.rev.ScenarioID, "revisionId": c.rev.ID, "sourceHash": c.rev.Hash, "contractId": contract.ID, "participantId": contract.ParticipantID}}
	if contract.Description != "" {
		doc["info"].(map[string]any)["description"] = contract.Description
	}
	if len(servers) > 0 {
		doc["servers"] = map[string]any{}
	}
	security := map[string]any{}
	for _, id := range slices.Sorted(maps.Keys(servers)) {
		server := servers[id]
		item := map[string]any{"host": server.Host, "protocol": server.Protocol}
		if server.Description != "" {
			item["description"] = server.Description
		}
		if server.Name != "" {
			item["title"] = server.Name
		}
		if server.Auth != "none" && server.Auth != "unspecified" {
			item["security"] = []any{ref("#/components/securitySchemes/" + server.ID)}
			security[server.ID] = map[string]any{"type": server.Auth}
		}
		doc["servers"].(map[string]any)[server.ID] = item
	}
	if len(security) > 0 {
		doc["components"].(map[string]any)["securitySchemes"] = security
	}
	for _, id := range slices.Sorted(maps.Keys(channels)) {
		ch := channels[id]
		item := map[string]any{"address": ch.Address, "messages": map[string]any{}}
		if ch.Name != "" {
			item["title"] = ch.Name
		}
		if ch.Description != "" {
			item["description"] = ch.Description
		}
		if len(ch.ServerIDs) > 0 {
			refs := make([]any, 0, len(ch.ServerIDs))
			for _, id := range ch.ServerIDs {
				refs = append(refs, ref("#/servers/"+id))
			}
			item["servers"] = refs
		}
		for _, id := range ch.MessageIDs {
			item["messages"].(map[string]any)[id] = ref("#/components/messages/" + id)
		}
		if ch.Kafka != nil {
			binding := map[string]any{"bindingVersion": "0.5.0"}
			if ch.Kafka.Partitions != nil {
				binding["partitions"] = *ch.Kafka.Partitions
			}
			if ch.Kafka.Replicas != nil {
				binding["replicas"] = *ch.Kafka.Replicas
			}
			item["bindings"] = map[string]any{"kafka": binding}
		}
		doc["channels"].(map[string]any)[ch.ID] = item
	}
	for _, id := range slices.Sorted(maps.Keys(messages)) {
		m := messages[id]
		item := map[string]any{"name": m.ID, "title": m.Name, "contentType": "application/json", "payload": ref("#/components/schemas/" + m.PayloadSchemaID)}
		if m.Description != "" {
			item["description"] = m.Description
		}
		if m.HeadersSchemaID != "" {
			item["headers"] = ref("#/components/schemas/" + m.HeadersSchemaID)
		}
		if m.KeySchemaID != "" {
			item["bindings"] = map[string]any{"kafka": map[string]any{"key": map[string]any{"allOf": []any{ref("#/components/schemas/" + m.KeySchemaID)}}, "bindingVersion": "0.5.0"}}
		}
		if len(m.Examples) > 0 {
			examples := make([]any, 0, len(m.Examples))
			for _, example := range m.Examples {
				payload, _ := decodeJSON([]byte(example.PayloadJSON))
				e := map[string]any{"name": example.Name, "payload": payload}
				if example.HeadersJSON != "" {
					headers, _ := decodeJSON([]byte(example.HeadersJSON))
					e["headers"] = headers
				}
				examples = append(examples, e)
			}
			item["examples"] = examples
		}
		doc["components"].(map[string]any)["messages"].(map[string]any)[m.ID] = item
	}
	for _, op := range contract.Operations {
		item := map[string]any{"action": op.Action, "channel": ref("#/channels/" + op.ChannelID), "messages": []any{ref("#/channels/" + op.ChannelID + "/messages/" + op.MessageID)}}
		if op.Name != "" {
			item["title"] = op.Name
		}
		if op.Description != "" {
			item["description"] = op.Description
		}
		if op.Kafka != nil && (op.Kafka.GroupID != "" || op.Kafka.ClientID != "") {
			binding := map[string]any{"bindingVersion": "0.5.0"}
			if op.Kafka.GroupID != "" {
				binding["groupId"] = map[string]any{"type": "string", "const": op.Kafka.GroupID}
			}
			if op.Kafka.ClientID != "" {
				binding["clientId"] = map[string]any{"type": "string", "const": op.Kafka.ClientID}
			}
			item["bindings"] = map[string]any{"kafka": binding}
		}
		doc["operations"].(map[string]any)[op.ID] = item
	}
	return doc
}

func (c *eventValidationCache) validateExamples(doc map[string]any, messages map[string]designscenario.EventMessage, ds *eventDiagnostics) error {
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft7)
	compiler.UseLoader(offlineLoader{})
	const uri = "https://mocker.invalid/asyncapi.json"
	if err := compiler.AddResource(uri, doc); err != nil {
		return err
	}
	allSchemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	compiled := map[string]*jsonschema.Schema{}
	for _, id := range slices.Sorted(maps.Keys(allSchemas)) {
		if err := c.ctx.Err(); err != nil {
			return err
		}
		schema, err := compiler.Compile(uri + "#/components/schemas/" + id)
		if err != nil {
			ds.add(diagnostic("event_schema_invalid", "error", "Схема не компилируется: "+err.Error(), "event-schema", id, c.schemaPointer(id)))
		} else {
			compiled[id] = schema
		}
	}
	for _, id := range slices.Sorted(maps.Keys(messages)) {
		m := messages[id]
		if err := c.ctx.Err(); err != nil {
			return err
		}
		for _, example := range m.Examples {
			if example.HeadersJSON == "" {
				continue
			}
			if len(example.HeadersJSON) > 256<<10 {
				ds.add(diagnostic("event_example_invalid", "error", "Headers примера превышают 256 KiB", "event-message", m.ID, "/eventModel/messages"))
				continue
			}
			value, err := decodeJSON([]byte(example.HeadersJSON))
			if err != nil {
				ds.add(diagnostic("event_example_invalid", "error", "Headers примера содержат неверный JSON", "event-message", m.ID, "/eventModel/messages"))
				continue
			}
			if _, ok := value.(map[string]any); !ok || !validJSONShape(value, 0, new(int)) {
				ds.add(diagnostic("event_example_invalid", "error", "Headers примера должны быть JSON-объектом в пределах лимитов", "event-message", m.ID, "/eventModel/messages"))
			}
		}
		for _, pair := range []struct {
			id   string
			kind string
		}{{m.PayloadSchemaID, "payload"}, {m.HeadersSchemaID, "headers"}} {
			if pair.id == "" {
				continue
			}
			schema := compiled[pair.id]
			if schema == nil {
				continue
			}
			for i, example := range m.Examples {
				pointer := fmt.Sprintf("%s/examples/%d/%sJSON", c.messagePointers[m.ID], i, pair.kind)
				raw := example.PayloadJSON
				if pair.kind == "headers" {
					raw = example.HeadersJSON
					if raw == "" {
						continue
					}
				}
				if len(raw) > 256<<10 {
					ds.add(diagnostic("event_example_invalid", "error", "Пример превышает 256 KiB", "event-message", m.ID, pointer))
					continue
				}
				value, err := decodeJSON([]byte(raw))
				if err != nil {
					ds.add(diagnostic("event_example_invalid", "error", "Пример содержит неверный JSON", "event-message", m.ID, pointer))
					continue
				}
				if !validJSONShape(value, 0, new(int)) {
					ds.add(diagnostic("event_example_invalid", "error", "Пример превышает лимит глубины или узлов", "event-message", m.ID, pointer))
					continue
				}
				if pair.kind == "headers" {
					if _, ok := value.(map[string]any); !ok {
						ds.add(diagnostic("event_example_invalid", "error", "Headers примера должны быть JSON-объектом", "event-message", m.ID, pointer))
						continue
					}
				}
				if err := schema.Validate(value); err != nil {
					ds.add(diagnostic("event_example_invalid", "error", "Пример не соответствует схеме "+pair.id+": "+err.Error(), "event-message", m.ID, pointer))
				}
			}
		}
	}
	return nil
}

func validJSONShape(value any, depth int, nodes *int) bool {
	*nodes++
	if depth > 64 || *nodes > 10000 {
		return false
	}
	switch value := value.(type) {
	case map[string]any:
		for _, child := range value {
			if !validJSONShape(child, depth+1, nodes) {
				return false
			}
		}
	case []any:
		for _, child := range value {
			if !validJSONShape(child, depth+1, nodes) {
				return false
			}
		}
	}
	return true
}

func validKafkaHost(host string) bool {
	if host == "" || strings.ContainsAny(host, "/@,{} \t\r\n\x00") || strings.Contains(host, "://") {
		return false
	}
	if strings.HasPrefix(host, "[") {
		if strings.HasSuffix(host, "]") {
			return net.ParseIP(strings.Trim(host, "[]")) != nil
		}
		name, port, err := net.SplitHostPort(host)
		return err == nil && net.ParseIP(name) != nil && validKafkaPort(port)
	}
	parts := strings.Split(host, ":")
	if len(parts) > 2 || parts[0] == "" {
		return false
	}
	if len(parts) == 2 && !validKafkaPort(parts[1]) {
		return false
	}
	for _, r := range parts[0] {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func validKafkaPort(port string) bool {
	for _, r := range port {
		if r < '0' || r > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n <= 65535
}
func validKafkaMetadata(value string) bool {
	return utf8.RuneCountInString(value) <= 256 && !strings.ContainsAny(value, "\r\n\x00") && !strings.Contains(value, "{{") && !strings.Contains(value, "${")
}

type eventDiagnostics struct {
	list    []Diagnostic
	omitted string
}

func (d *eventDiagnostics) add(value Diagnostic) {
	if len(d.list) < 99 {
		d.list = append(d.list, value)
		return
	}
	if severityRank(value.Severity) > severityRank(d.omitted) {
		d.omitted = value.Severity
	}
	truncated := Diagnostic{Code: "event_diagnostics_truncated", Severity: d.omitted, Message: "Дополнительные замечания сокращены"}
	if len(d.list) == 99 {
		d.list = append(d.list, truncated)
	} else {
		d.list[99] = truncated
	}
}
func (d *eventDiagnostics) hasErrors() bool {
	return slices.ContainsFunc(d.list, func(v Diagnostic) bool { return v.Severity == "error" })
}
func severityRank(s string) int {
	switch s {
	case "error":
		return 3
	case "warning":
		return 2
	case "info":
		return 1
	}
	return 0
}

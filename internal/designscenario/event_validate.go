package designscenario

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/jsonx"
)

const (
	maxEventJSONBytes = 256 << 10
	maxEventJSONDepth = 64
	maxEventJSONNodes = 10_000
)

var eventIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

func (v *documentValidator) eventID(pointer, id string) {
	if !eventIDPattern.MatchString(id) {
		v.errorAt(pointer, "must match [A-Za-z0-9_-]{1,100}")
	}
}

func (v *documentValidator) eventText(pointer, value string) {
	v.checkText(pointer, value, true)
}

func (v *documentValidator) eventJSON(pointer, source string, schema bool) {
	if len(source) > maxEventJSONBytes {
		v.errorAt(pointer, "JSON text exceeds 256 KiB")
		return
	}
	if source == "" && schema { // An empty schema remains an incomplete definition.
		return
	}
	if !jsonx.Valid([]byte(source)) {
		v.errorAt(pointer, "must contain valid JSON")
		return
	}
	value, err := decodeJSONValue([]byte(source))
	if err != nil {
		v.errorAt(pointer, "must contain valid JSON")
		return
	}
	if schema {
		switch value.(type) {
		case bool, map[string]any:
		default:
			v.errorAt(pointer, "schema root must be an object or boolean")
		}
	}
	nodes, maximumDepth := 0, 0
	var visit func(any, int)
	visit = func(value any, depth int) {
		nodes++
		maximumDepth = max(maximumDepth, depth)
		if depth > maxEventJSONDepth || nodes > maxEventJSONNodes {
			return
		}
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				visit(child, depth+1)
			}
		case map[string]any:
			for _, child := range node {
				visit(child, depth+1)
			}
		}
	}
	visit(value, 1)
	if nodes > maxEventJSONNodes {
		v.errorAt(pointer, "JSON text exceeds 10000 nodes")
	}
	if maximumDepth > maxEventJSONDepth {
		v.errorAt(pointer, "JSON text exceeds depth 64")
	}
}

func (v *documentValidator) validateEventModel(document Document, participants map[string]int) {
	if document.FormatVersion < 3 {
		if document.EventModel != nil {
			v.errorAt("/eventModel", "eventModel requires formatVersion 3")
		}
		for i, message := range document.Messages {
			if message.EventBindings != nil {
				v.errorAt(fmt.Sprintf("/messages/%d/eventBindings", i), "eventBindings require formatVersion 3")
			}
		}
		return
	}
	if document.EventModel == nil {
		for i, message := range document.Messages {
			if message.EventBindings != nil {
				v.errorAt(fmt.Sprintf("/messages/%d/eventBindings", i), "eventBindings require eventModel")
			}
		}
		return
	}
	m := document.EventModel
	for _, field := range []struct {
		name          string
		missing       bool
		length, limit int
	}{
		{"servers", m.Servers == nil, len(m.Servers), 100},
		{"channels", m.Channels == nil, len(m.Channels), 500},
		{"messages", m.Messages == nil, len(m.Messages), 1000},
		{"schemas", m.Schemas == nil, len(m.Schemas), 1000},
		{"contracts", m.Contracts == nil, len(m.Contracts), 200},
	} {
		if field.missing {
			v.errorAt("/eventModel/"+field.name, "must be an array; use [] for an empty collection")
		}
		if field.length > field.limit {
			v.errorAt("/eventModel/"+field.name, "contains too many entries")
		}
	}
	servers := map[string]bool{}
	for i, server := range m.Servers {
		p := fmt.Sprintf("/eventModel/servers/%d", i)
		v.eventUniqueID(p+"/id", server.ID, servers)
		v.eventText(p+"/name", server.Name)
		v.eventText(p+"/description", server.Description)
		v.eventText(p+"/host", server.Host)
		if server.Protocol != "kafka" && server.Protocol != "kafka-secure" {
			v.errorAt(p+"/protocol", "unknown Kafka protocol")
		}
		if !slices.Contains([]string{"unspecified", "none", "plain", "scramSha256", "scramSha512"}, server.Auth) {
			v.errorAt(p+"/auth", "unknown Kafka auth")
		}
	}
	schemas := map[string]bool{}
	for i, schema := range m.Schemas {
		p := fmt.Sprintf("/eventModel/schemas/%d", i)
		v.eventUniqueID(p+"/id", schema.ID, schemas)
		v.eventText(p+"/name", schema.Name)
		v.eventText(p+"/description", schema.Description)
		v.eventJSON(p+"/schemaJSON", schema.SchemaJSON, true)
	}
	messages := map[string]bool{}
	for i, message := range m.Messages {
		p := fmt.Sprintf("/eventModel/messages/%d", i)
		v.eventUniqueID(p+"/id", message.ID, messages)
		v.eventText(p+"/name", message.Name)
		v.eventText(p+"/description", message.Description)
		for _, ref := range []struct{ name, id string }{{"payloadSchemaId", message.PayloadSchemaID}, {"headersSchemaId", message.HeadersSchemaID}, {"keySchemaId", message.KeySchemaID}} {
			if ref.id != "" && !schemas[ref.id] {
				v.errorAt(p+"/"+ref.name, "schema does not exist")
			}
		}
		if message.Examples == nil {
			v.errorAt(p+"/examples", "must be an array")
		}
		if len(message.Examples) > 20 {
			v.errorAt(p+"/examples", "contains too many examples")
		}
		for j, example := range message.Examples {
			ep := fmt.Sprintf("%s/examples/%d", p, j)
			v.eventText(ep+"/name", example.Name)
			v.eventJSON(ep+"/payloadJSON", example.PayloadJSON, false)
			if example.HeadersJSON != "" {
				v.eventJSON(ep+"/headersJSON", example.HeadersJSON, false)
			}
		}
	}
	channels := map[string]EventChannel{}
	for i, channel := range m.Channels {
		p := fmt.Sprintf("/eventModel/channels/%d", i)
		v.eventID(p+"/id", channel.ID)
		if _, exists := channels[channel.ID]; exists {
			v.errorAt(p+"/id", "duplicate channel id")
		}
		channels[channel.ID] = channel
		v.eventText(p+"/name", channel.Name)
		v.eventText(p+"/description", channel.Description)
		v.eventText(p+"/address", channel.Address)
		v.eventText(p+"/discriminatorProperty", channel.DiscriminatorProperty)
		if channel.ServerIDs == nil {
			v.errorAt(p+"/serverIds", "must be an array")
		}
		if channel.MessageIDs == nil {
			v.errorAt(p+"/messageIds", "must be an array")
		}
		if len(channel.MessageIDs) > 100 {
			v.errorAt(p+"/messageIds", "contains too many messages")
		}
		seen := map[string]bool{}
		for j, id := range channel.ServerIDs {
			if seen[id] {
				v.errorAt(fmt.Sprintf("%s/serverIds/%d", p, j), "duplicate server id")
			}
			seen[id] = true
			if !servers[id] {
				v.errorAt(fmt.Sprintf("%s/serverIds/%d", p, j), "server does not exist")
			}
		}
		seen = map[string]bool{}
		for j, id := range channel.MessageIDs {
			if seen[id] {
				v.errorAt(fmt.Sprintf("%s/messageIds/%d", p, j), "duplicate message id")
			}
			seen[id] = true
			if !messages[id] {
				v.errorAt(fmt.Sprintf("%s/messageIds/%d", p, j), "event message does not exist")
			}
		}
		if channel.Kafka != nil {
			if channel.Kafka.Partitions != nil && *channel.Kafka.Partitions <= 0 {
				v.errorAt(p+"/kafka/partitions", "must be positive")
			}
			if channel.Kafka.Replicas != nil && *channel.Kafka.Replicas <= 0 {
				v.errorAt(p+"/kafka/replicas", "must be positive")
			}
		}
		if channel.Address != "" {
			for j, previous := range m.Channels[:i] {
				if previous.Address != channel.Address {
					continue
				}
				if len(previous.ServerIDs) == 0 && len(channel.ServerIDs) == 0 || intersects(previous.ServerIDs, channel.ServerIDs) {
					v.errorAt(p+"/address", fmt.Sprintf("ambiguous with channel %d", j))
				}
			}
		}
	}
	contracts := map[string]EventContract{}
	owners := map[string]bool{}
	operations := 0
	for i, contract := range m.Contracts {
		p := fmt.Sprintf("/eventModel/contracts/%d", i)
		v.eventID(p+"/id", contract.ID)
		if _, exists := contracts[contract.ID]; exists {
			v.errorAt(p+"/id", "duplicate event contract id")
		}
		contracts[contract.ID] = contract
		for _, http := range document.Contracts {
			if http.ID == contract.ID {
				v.errorAt(p+"/id", "event contract id overlaps HTTP contract")
			}
		}
		v.eventText(p+"/name", contract.Name)
		v.eventText(p+"/description", contract.Description)
		v.eventText(p+"/version", contract.Version)
		ownerIndex, exists := participants[contract.ParticipantID]
		if !exists {
			v.errorAt(p+"/participantId", "participant does not exist")
		} else if !slices.Contains([]string{"client", "service", "external", "other"}, document.Participants[ownerIndex].Kind) {
			v.errorAt(p+"/participantId", "participant cannot own an event contract")
		}
		if owners[contract.ParticipantID] {
			v.errorAt(p+"/participantId", "participant already owns an event contract")
		}
		owners[contract.ParticipantID] = true
		if contract.Operations == nil {
			v.errorAt(p+"/operations", "must be an array")
		}
		operations += len(contract.Operations)
		ids, triplets := map[string]bool{}, map[string]bool{}
		for j, operation := range contract.Operations {
			op := fmt.Sprintf("%s/operations/%d", p, j)
			v.eventUniqueID(op+"/id", operation.ID, ids)
			v.eventText(op+"/name", operation.Name)
			v.eventText(op+"/description", operation.Description)
			if operation.Action != "send" && operation.Action != "receive" {
				v.errorAt(op+"/action", "action must be send or receive")
			}
			channel, ok := channels[operation.ChannelID]
			if !ok {
				v.errorAt(op+"/channelId", "channel does not exist")
			} else if !slices.Contains(channel.MessageIDs, operation.MessageID) {
				v.errorAt(op+"/messageId", "message is not in channel")
			}
			if !messages[operation.MessageID] {
				v.errorAt(op+"/messageId", "event message does not exist")
			}
			key := operation.Action + "\x00" + operation.ChannelID + "\x00" + operation.MessageID
			if triplets[key] {
				v.errorAt(op, "duplicate action/channel/message operation")
			}
			triplets[key] = true
			if operation.Kafka != nil {
				for _, field := range []struct{ name, value string }{{"groupId", operation.Kafka.GroupID}, {"clientId", operation.Kafka.ClientID}} {
					if field.value != "" && operation.Action != "receive" {
						v.errorAt(op+"/kafka/"+field.name, "only receive supports this Kafka field")
					}
					if utf8.RuneCountInString(field.value) > 256 || strings.ContainsAny(field.value, "\r\n\x00") || strings.Contains(field.value, "{{") {
						v.errorAt(op+"/kafka/"+field.name, "invalid Kafka identifier")
					}
				}
			}
			v.validateEventOperationMetadata(op, operation, channels)
		}
	}
	if operations > 2000 {
		v.errorAt("/eventModel/contracts", "contains too many operations")
	}
	for i, message := range document.Messages {
		if message.EventBindings == nil {
			continue
		}
		p := fmt.Sprintf("/messages/%d/eventBindings", i)
		if message.Kind != "event" {
			v.errorAt(p, "only event messages accept eventBindings")
		}
		if message.Operation != nil {
			v.errorAt(p, "HTTP operation and eventBindings are exclusive")
		}
		if len(message.EventBindings) < 1 || len(message.EventBindings) > 2 {
			v.errorAt(p, "requires one or two bindings")
		}
		seen := map[string]bool{}
		var first *EventOperation
		for j, binding := range message.EventBindings {
			bp := fmt.Sprintf("%s/%d", p, j)
			if seen[binding.ContractID+"\x00"+binding.OperationID] {
				v.errorAt(bp, "duplicate event binding")
			}
			seen[binding.ContractID+"\x00"+binding.OperationID] = true
			contract, ok := contracts[binding.ContractID]
			if !ok {
				v.errorAt(bp+"/contractId", "event contract does not exist")
				continue
			}
			k := slices.IndexFunc(contract.Operations, func(op EventOperation) bool { return op.ID == binding.OperationID })
			if k < 0 {
				v.errorAt(bp+"/operationId", "event operation does not exist")
				continue
			}
			op := contract.Operations[k]
			if op.Action == "send" && contract.ParticipantID != message.FromID || op.Action == "receive" && contract.ParticipantID != message.ToID {
				v.errorAt(bp, "event binding direction does not match owner")
			}
			if first != nil && (first.ChannelID != op.ChannelID || first.MessageID != op.MessageID || first.Action == op.Action) {
				v.errorAt(bp, "bindings must share channel and message with opposite actions")
			}
			first = &op
		}
	}
}

func (v *documentValidator) eventUniqueID(pointer, id string, seen map[string]bool) {
	v.eventID(pointer, id)
	if seen[id] {
		v.errorAt(pointer, "duplicate id")
	}
	seen[id] = true
}

func intersects(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}

func (v *documentValidator) validateEventOperationMetadata(pointer string, operation EventOperation, channels map[string]EventChannel) {
	if routes := operation.FailureRoutes; routes != nil {
		if operation.Action != "receive" {
			v.errorAt(pointer+"/failureRoutes", "только получатель может задавать маршруты ошибок")
		}
		for _, route := range []struct{ name, id string }{{"retryChannelId", routes.RetryChannelID}, {"deadLetterChannelId", routes.DeadLetterChannelID}} {
			if route.id == "" {
				continue
			}
			if route.id == operation.ChannelID {
				v.errorAt(pointer+"/failureRoutes/"+route.name, "канал назначения совпадает с исходным")
			} else if _, exists := channels[route.id]; !exists {
				v.errorAt(pointer+"/failureRoutes/"+route.name, "канал назначения не существует")
			}
		}
		if routes.RetryChannelID != "" && routes.RetryChannelID == routes.DeadLetterChannelID {
			v.errorAt(pointer+"/failureRoutes/deadLetterChannelId", "каналы повтора и ошибок должны различаться")
		}
	}
	if len(operation.APILinks) > 100 {
		v.errorAt(pointer+"/apiLinks", "слишком много связей API")
	}
	seenAPI := map[EventAPILink]bool{}
	for i, link := range operation.APILinks {
		p := fmt.Sprintf("%s/apiLinks/%d", pointer, i)
		v.checkText(p+"/contractId", link.ContractID, false)
		v.checkText(p+"/operationKey", link.OperationKey, false)
		if seenAPI[link] {
			v.errorAt(p, "повторяющаяся связь API")
		}
		seenAPI[link] = true
	}
	if len(operation.StateLinks) > 100 {
		v.errorAt(pointer+"/stateLinks", "слишком много связей состояний")
	}
	seenState := map[EventStateLink]bool{}
	for i, link := range operation.StateLinks {
		p := fmt.Sprintf("%s/stateLinks/%d", pointer, i)
		v.checkText(p+"/contractId", link.ContractID, false)
		v.checkText(p+"/diagramId", link.DiagramID, false)
		v.checkText(p+"/transitionId", link.TransitionID, false)
		if seenState[link] {
			v.errorAt(p, "повторяющаяся связь состояний")
		}
		seenState[link] = true
	}
}

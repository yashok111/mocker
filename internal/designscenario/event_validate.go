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

// validateEventModel checks the model section by section in the order the
// diagnostics have always been emitted: each section only reads the id sets
// the earlier ones built.
func (v *documentValidator) validateEventModel(document Document, participants map[string]int) {
	if !v.eventModelAdmitted(document) {
		return
	}
	m := document.EventModel
	v.eventCollectionBounds(m)
	servers := v.eventServers(m.Servers)
	schemas := v.eventSchemas(m.Schemas)
	messages := v.eventMessages(m.Messages, schemas)
	channels := v.eventChannels(m.Channels, servers, messages)
	contracts := v.eventContracts(document, participants, channels, messages)
	v.eventMessageBindings(document, contracts)
}

// eventModelAdmitted reports whether the model is present on a format that
// allows it; otherwise it flags every eventBindings that has nothing to bind to.
func (v *documentValidator) eventModelAdmitted(document Document) bool {
	if document.FormatVersion < 3 {
		if document.EventModel != nil {
			v.errorAt("/eventModel", "eventModel requires formatVersion 3")
		}
		for i, message := range document.Messages {
			if message.EventBindings != nil {
				v.errorAt(fmt.Sprintf("/messages/%d/eventBindings", i), "eventBindings require formatVersion 3")
			}
		}
		return false
	}
	if document.EventModel == nil {
		for i, message := range document.Messages {
			if message.EventBindings != nil {
				v.errorAt(fmt.Sprintf("/messages/%d/eventBindings", i), "eventBindings require eventModel")
			}
		}
		return false
	}
	return true
}

func (v *documentValidator) eventCollectionBounds(m *EventModel) {
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
}

func (v *documentValidator) eventServers(list []EventServer) map[string]bool {
	servers := map[string]bool{}
	for i, server := range list {
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
	return servers
}

func (v *documentValidator) eventSchemas(list []EventSchema) map[string]bool {
	schemas := map[string]bool{}
	for i, schema := range list {
		p := fmt.Sprintf("/eventModel/schemas/%d", i)
		v.eventUniqueID(p+"/id", schema.ID, schemas)
		v.eventText(p+"/name", schema.Name)
		v.eventText(p+"/description", schema.Description)
		v.eventJSON(p+"/schemaJSON", schema.SchemaJSON, true)
	}
	return schemas
}

func (v *documentValidator) eventMessages(list []EventMessage, schemas map[string]bool) map[string]bool {
	messages := map[string]bool{}
	for i, message := range list {
		p := fmt.Sprintf("/eventModel/messages/%d", i)
		v.eventUniqueID(p+"/id", message.ID, messages)
		v.eventText(p+"/name", message.Name)
		v.eventText(p+"/description", message.Description)
		for _, ref := range []struct{ name, id string }{{"payloadSchemaId", message.PayloadSchemaID}, {"headersSchemaId", message.HeadersSchemaID}, {"keySchemaId", message.KeySchemaID}} {
			if ref.id != "" && !schemas[ref.id] {
				v.errorAt(p+"/"+ref.name, "schema does not exist")
			}
		}
		v.eventMessageExamples(p, message.Examples)
	}
	return messages
}

func (v *documentValidator) eventMessageExamples(p string, examples []EventExample) {
	if examples == nil {
		v.errorAt(p+"/examples", "must be an array")
	}
	if len(examples) > 20 {
		v.errorAt(p+"/examples", "contains too many examples")
	}
	for j, example := range examples {
		ep := fmt.Sprintf("%s/examples/%d", p, j)
		v.eventText(ep+"/name", example.Name)
		v.eventJSON(ep+"/payloadJSON", example.PayloadJSON, false)
		if example.HeadersJSON != "" {
			v.eventJSON(ep+"/headersJSON", example.HeadersJSON, false)
		}
	}
}

func (v *documentValidator) eventChannels(list []EventChannel, servers, messages map[string]bool) map[string]EventChannel {
	channels := map[string]EventChannel{}
	for i, channel := range list {
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
		v.eventChannelRefs(p+"/serverIds", channel.ServerIDs, servers, "duplicate server id", "server does not exist")
		v.eventChannelRefs(p+"/messageIds", channel.MessageIDs, messages, "duplicate message id", "event message does not exist")
		if channel.Kafka != nil {
			if channel.Kafka.Partitions != nil && *channel.Kafka.Partitions <= 0 {
				v.errorAt(p+"/kafka/partitions", "must be positive")
			}
			if channel.Kafka.Replicas != nil && *channel.Kafka.Replicas <= 0 {
				v.errorAt(p+"/kafka/replicas", "must be positive")
			}
		}
		v.eventChannelAddress(p, channel, list[:i])
	}
	return channels
}

// eventChannelRefs flags repeated and dangling ids in one of a channel's id lists.
func (v *documentValidator) eventChannelRefs(p string, ids []string, known map[string]bool, duplicate, missing string) {
	seen := map[string]bool{}
	for j, id := range ids {
		if seen[id] {
			v.errorAt(fmt.Sprintf("%s/%d", p, j), duplicate)
		}
		seen[id] = true
		if !known[id] {
			v.errorAt(fmt.Sprintf("%s/%d", p, j), missing)
		}
	}
}

// eventChannelAddress flags an earlier channel that a broker could not tell
// apart: the same address on overlapping (or both unspecified) servers.
func (v *documentValidator) eventChannelAddress(p string, channel EventChannel, earlier []EventChannel) {
	if channel.Address == "" {
		return
	}
	for j, previous := range earlier {
		if previous.Address != channel.Address {
			continue
		}
		if len(previous.ServerIDs) == 0 && len(channel.ServerIDs) == 0 || intersects(previous.ServerIDs, channel.ServerIDs) {
			v.errorAt(p+"/address", fmt.Sprintf("ambiguous with channel %d", j))
		}
	}
}

func (v *documentValidator) eventContracts(document Document, participants map[string]int, channels map[string]EventChannel, messages map[string]bool) map[string]EventContract {
	contracts := map[string]EventContract{}
	owners := map[string]bool{}
	operations := 0
	for i, contract := range document.EventModel.Contracts {
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
		v.eventContractOwner(p, document, participants, contract.ParticipantID, owners)
		if contract.Operations == nil {
			v.errorAt(p+"/operations", "must be an array")
		}
		operations += len(contract.Operations)
		ids, triplets := map[string]bool{}, map[string]bool{}
		for j, operation := range contract.Operations {
			v.eventOperation(fmt.Sprintf("%s/operations/%d", p, j), operation, channels, messages, ids, triplets)
		}
	}
	if operations > 2000 {
		v.errorAt("/eventModel/contracts", "contains too many operations")
	}
	return contracts
}

// eventContractOwner admits one contract per participant of an owning kind.
func (v *documentValidator) eventContractOwner(p string, document Document, participants map[string]int, participantID string, owners map[string]bool) {
	ownerIndex, exists := participants[participantID]
	if !exists {
		v.errorAt(p+"/participantId", "participant does not exist")
	} else if !slices.Contains([]string{"client", "service", "external", "other"}, document.Participants[ownerIndex].Kind) {
		v.errorAt(p+"/participantId", "participant cannot own an event contract")
	}
	if owners[participantID] {
		v.errorAt(p+"/participantId", "participant already owns an event contract")
	}
	owners[participantID] = true
}

func (v *documentValidator) eventOperation(op string, operation EventOperation, channels map[string]EventChannel, messages, ids, triplets map[string]bool) {
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

func (v *documentValidator) eventMessageBindings(document Document, contracts map[string]EventContract) {
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
			if op, ok := v.eventBindingOperation(bp, message, binding, contracts); ok {
				if first != nil && (first.ChannelID != op.ChannelID || first.MessageID != op.MessageID || first.Action == op.Action) {
					v.errorAt(bp, "bindings must share channel and message with opposite actions")
				}
				first = &op
			}
		}
	}
}

// eventBindingOperation resolves a binding to its operation and checks the
// direction against the owner; false means the binding names nothing.
func (v *documentValidator) eventBindingOperation(bp string, message Message, binding EventBinding, contracts map[string]EventContract) (EventOperation, bool) {
	contract, ok := contracts[binding.ContractID]
	if !ok {
		v.errorAt(bp+"/contractId", "event contract does not exist")
		return EventOperation{}, false
	}
	k := slices.IndexFunc(contract.Operations, func(op EventOperation) bool { return op.ID == binding.OperationID })
	if k < 0 {
		v.errorAt(bp+"/operationId", "event operation does not exist")
		return EventOperation{}, false
	}
	op := contract.Operations[k]
	if op.Action == "send" && contract.ParticipantID != message.FromID || op.Action == "receive" && contract.ParticipantID != message.ToID {
		v.errorAt(bp, "event binding direction does not match owner")
	}
	return op, true
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

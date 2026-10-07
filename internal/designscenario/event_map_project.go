package designscenario

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/statediagram"
)

type eventMapBuilder struct {
	ctx           context.Context
	doc           Document
	out           EventMapAnalysis
	nodes         map[string]bool
	ambiguous     map[string]bool
	channels      map[string]EventChannel
	messages      map[string]EventMessage
	schemas       map[string]bool
	servers       map[string]EventServer
	http          map[string]eventMapHTTPContract
	httpAmbiguous map[string]bool
	bound         map[string]bool
}

type eventMapHTTPContract struct {
	contract   Contract
	index      int
	root       map[string]any
	operations map[string][]ContractOperation
	states     map[string]eventMapStateDiagram
	badState   bool
}

type eventMapStateDiagram struct {
	diagram statediagram.Diagram
	index   int
}

func eventMapID(parts ...string) string {
	encoded := make([]string, len(parts))
	for i, part := range parts {
		encoded[i] = strings.NewReplacer("%", "%25", ":", "%3A").Replace(part)
	}
	return strings.Join(encoded, ":")
}
func analyzeEventMap(ctx context.Context, document Document) (EventMapAnalysis, error) {
	b := &eventMapBuilder{ctx: ctx, doc: document, out: EventMapAnalysis{Nodes: []EventMapNode{}, Edges: []EventMapEdge{}, Diagnostics: []EventMapDiagnostic{}, Coverage: EventMapCoverage{TruncatedReasons: []string{}}, Complete: true}, nodes: map[string]bool{}, ambiguous: map[string]bool{}, channels: map[string]EventChannel{}, messages: map[string]EventMessage{}, schemas: map[string]bool{}, servers: map[string]EventServer{}, http: map[string]eventMapHTTPContract{}, httpAmbiguous: map[string]bool{}, bound: map[string]bool{}}
	if err := ctx.Err(); err != nil {
		return b.out, err
	}
	if document.EventModel == nil {
		return b.finalize()
	}
	m := document.EventModel
	// Each phase reads the indexes the earlier ones filled, so the order is fixed.
	for _, phase := range []func(*EventModel) error{b.participantNodes, b.serverNodes, b.channelNodes, b.messageNodes, b.schemaNodes, b.boundOperations, b.httpContracts, b.contractOperations} {
		if err := phase(m); err != nil {
			return b.out, err
		}
	}
	b.topologyDiagnostics()
	return b.finalize()
}

// participantNodes adds only explicit event-contract owners: sequence
// participants also include brokers and HTTP-only actors, which do not
// belong to this contract topology.
func (b *eventMapBuilder) participantNodes(m *EventModel) error {
	owners := make(map[string]bool, len(m.Contracts))
	for _, contract := range m.Contracts {
		if err := b.ctx.Err(); err != nil {
			return err
		}
		owners[contract.ParticipantID] = true
	}
	for i, p := range b.doc.Participants {
		if err := b.ctx.Err(); err != nil {
			return err
		}
		if !owners[p.ID] {
			continue
		}
		b.node(EventMapNode{ID: eventMapID("participant", p.ID), Kind: "participant", Label: cmp.Or(p.Name, p.ID), Locator: EventMapLocator{Pointer: fmt.Sprintf("/participants/%d", i), EntityID: p.ID, ParticipantID: p.ID}})
	}
	return nil
}

func (b *eventMapBuilder) serverNodes(m *EventModel) error {
	for i, s := range m.Servers {
		if err := b.ctx.Err(); err != nil {
			return err
		}
		b.servers[s.ID] = s
		b.node(EventMapNode{ID: eventMapID("server", s.ID), Kind: "server", Label: cmp.Or(s.Name, s.Host, s.ID), Locator: EventMapLocator{Pointer: fmt.Sprintf("/eventModel/servers/%d", i), EntityID: s.ID}})
	}
	return nil
}

func (b *eventMapBuilder) channelNodes(m *EventModel) error {
	for i, ch := range m.Channels {
		if err := b.ctx.Err(); err != nil {
			return err
		}
		b.channels[ch.ID] = ch
		p := fmt.Sprintf("/eventModel/channels/%d", i)
		b.node(EventMapNode{ID: eventMapID("channel", ch.ID), Kind: "channel", Label: cmp.Or(ch.Address, ch.Name, ch.ID), Locator: EventMapLocator{Pointer: p, EntityID: ch.ID}})
		for j, id := range ch.ServerIDs {
			if _, ok := b.servers[id]; !ok {
				b.diagnostic("event_server_missing", "warning", fmt.Sprintf("%s/serverIds/%d", p, j), "Kafka-сервер не найден.", eventMapID("channel", ch.ID), true)
			}
			b.edge("server_channel", eventMapID("server", id), eventMapID("channel", ch.ID), "Kafka", EventMapLocator{Pointer: fmt.Sprintf("%s/serverIds/%d", p, j), EntityID: ch.ID})
		}
		for j, id := range ch.MessageIDs {
			if !slices.ContainsFunc(m.Messages, func(msg EventMessage) bool { return msg.ID == id }) {
				b.diagnostic("event_message_missing", "warning", fmt.Sprintf("%s/messageIds/%d", p, j), "Тип сообщения топика не найден.", eventMapID("channel", ch.ID), true)
			}
			b.edge("channel_message", eventMapID("channel", ch.ID), eventMapID("message", id), "Сообщение", EventMapLocator{Pointer: fmt.Sprintf("%s/messageIds/%d", p, j), EntityID: ch.ID})
		}
	}
	return nil
}

func (b *eventMapBuilder) messageNodes(m *EventModel) error {
	for i, msg := range m.Messages {
		if err := b.ctx.Err(); err != nil {
			return err
		}
		b.messages[msg.ID] = msg
		p := fmt.Sprintf("/eventModel/messages/%d", i)
		b.node(EventMapNode{ID: eventMapID("message", msg.ID), Kind: "message", Label: cmp.Or(msg.Name, msg.ID), Locator: EventMapLocator{Pointer: p, EntityID: msg.ID}})
		for _, ref := range []struct{ kind, id, field string }{{"payload", msg.PayloadSchemaID, "payloadSchemaId"}, {"key", msg.KeySchemaID, "keySchemaId"}, {"headers", msg.HeadersSchemaID, "headersSchemaId"}} {
			if ref.id != "" {
				if !slices.ContainsFunc(m.Schemas, func(s EventSchema) bool { return s.ID == ref.id }) {
					b.diagnostic("event_schema_missing", "warning", p+"/"+ref.field, "Схема сообщения не найдена.", eventMapID("message", msg.ID), true)
				}
				b.edge(ref.kind, eventMapID("message", msg.ID), eventMapID("schema", ref.id), strings.ToUpper(ref.kind), EventMapLocator{Pointer: p + "/" + ref.field, EntityID: msg.ID})
			}
		}
	}
	return nil
}

func (b *eventMapBuilder) schemaNodes(m *EventModel) error {
	for i, schema := range m.Schemas {
		if err := b.ctx.Err(); err != nil {
			return err
		}
		b.schemas[schema.ID] = true
		b.node(EventMapNode{ID: eventMapID("schema", schema.ID), Kind: "schema", Label: cmp.Or(schema.Name, schema.ID), Locator: EventMapLocator{Pointer: fmt.Sprintf("/eventModel/schemas/%d", i), EntityID: schema.ID}})
		if schema.SchemaJSON == "" || !jsonx.Valid([]byte(schema.SchemaJSON)) {
			b.diagnostic("event_schema_incomplete", "warning", fmt.Sprintf("/eventModel/schemas/%d/schemaJSON", i), "JSON-схема не заполнена или некорректна.", eventMapID("schema", schema.ID), true)
		}
	}
	return nil
}

// boundOperations marks which operations a scenario step binds.
func (b *eventMapBuilder) boundOperations(*EventModel) error {
	for _, step := range b.doc.Messages {
		for _, binding := range step.EventBindings {
			b.bound[eventMapID(binding.ContractID, binding.OperationID)] = true
		}
	}
	return nil
}

// httpContracts indexes the embedded HTTP contracts' operations and state
// diagrams, which API and state links resolve against.
func (b *eventMapBuilder) httpContracts(*EventModel) error {
	for i, contract := range b.doc.Contracts {
		if err := b.ctx.Err(); err != nil {
			return err
		}
		entry := eventMapHTTPContract{contract: contract, index: i, operations: map[string][]ContractOperation{}, states: map[string]eventMapStateDiagram{}}
		value, err := decodeJSONValue(contract.Document)
		entry.root = object(value)
		if err == nil && entry.root != nil {
			entry.indexRoot()
		}
		if _, exists := b.http[contract.ID]; exists {
			b.httpAmbiguous[contract.ID] = true
			b.diagnostic("event_http_contract_duplicate", "warning", fmt.Sprintf("/contracts/%d/id", i), "ID HTTP-контракта повторяется; ссылки неоднозначны.", "", true)
		}
		b.http[contract.ID] = entry
	}
	return nil
}

func (b *eventMapBuilder) contractOperations(m *EventModel) error {
	seenContracts := map[string]bool{}
	for i, contract := range m.Contracts {
		if err := b.ctx.Err(); err != nil {
			return err
		}
		if seenContracts[contract.ID] {
			b.diagnostic("event_contract_duplicate", "warning", fmt.Sprintf("/eventModel/contracts/%d/id", i), "ID событийного контракта повторяется.", "", true)
		}
		seenContracts[contract.ID] = true
		for j, op := range contract.Operations {
			if err := b.ctx.Err(); err != nil {
				return err
			}
			b.operation(contract, i, op, j)
		}
	}
	return nil
}

// indexRoot collects the operations by key and the state diagrams by id.
func (e *eventMapHTTPContract) indexRoot() {
	for _, op := range ContractOperations(e.root) {
		if op.Key != "" {
			e.operations[op.Key] = append(e.operations[op.Key], op)
		}
	}
	env, stateErr := statediagram.Decode(e.root)
	e.badState = stateErr != nil
	if stateErr == nil {
		for di, d := range env.Diagrams {
			e.states[d.ID] = eventMapStateDiagram{diagram: d, index: di}
		}
	}
}

func (b *eventMapBuilder) node(n EventMapNode) {
	if b.nodes[n.ID] && n.Kind != "api_operation" && n.Kind != "state_transition" {
		b.ambiguous[n.ID] = true
		b.diagnostic("event_duplicate_id", "warning", n.Locator.Pointer+"/id", "ID элемента карты повторяется.", n.ID, true)
	}
	b.out.Nodes = append(b.out.Nodes, n)
	b.nodes[n.ID] = true
}
func (b *eventMapBuilder) edge(kind, source, target, label string, loc EventMapLocator) {
	b.out.Edges = append(b.out.Edges, EventMapEdge{ID: eventMapID("edge", kind, source, target), Kind: kind, Source: source, Target: target, Label: label, Locator: loc})
}
func (b *eventMapBuilder) diagnostic(code, severity, pointer, message, elementID string, unresolved bool) {
	b.out.Diagnostics = append(b.out.Diagnostics, EventMapDiagnostic{ID: eventMapID("diagnostic", code, pointer), Code: code, Severity: severity, Pointer: pointer, Message: message, ElementID: elementID})
	if unresolved {
		b.out.Complete = false
	}
}

func (b *eventMapBuilder) operation(contract EventContract, ci int, op EventOperation, oi int) {
	p := fmt.Sprintf("/eventModel/contracts/%d/operations/%d", ci, oi)
	id := eventMapID("operation", contract.ID, op.ID)
	loc := EventMapLocator{Pointer: p, EntityID: op.ID, ContractID: contract.ID, OperationID: op.ID, ParticipantID: contract.ParticipantID}
	b.operationNode(contract, op, p, id, loc)
	routed := b.operationChannel(op, p, id, loc)
	if !b.bound[eventMapID(contract.ID, op.ID)] {
		b.diagnostic("event_operation_unbound", "info", p, "Операция не привязана к стрелке сценария.", id, false)
	}
	if op.Action == "receive" && (op.Kafka == nil || op.Kafka.GroupID == "") {
		b.diagnostic("event_group_unspecified", "warning", p+"/kafka/groupId", "Группа потребителя не указана.", id, false)
	}
	if op.FailureRoutes != nil && !b.failureRoutes(contract, op, p, id, routed) {
		return
	}
	for k, link := range op.APILinks {
		if b.ctx.Err() != nil {
			return
		}
		b.apiLink(id, p, k, link)
	}
	for k, link := range op.StateLinks {
		if b.ctx.Err() != nil {
			return
		}
		b.stateLink(id, p, k, link)
	}
}

// operationNode adds the operation and its ownership edge.
func (b *eventMapBuilder) operationNode(contract EventContract, op EventOperation, p, id string, loc EventMapLocator) {
	n := EventMapNode{ID: id, Kind: "operation", Label: cmp.Or(op.Name, op.ID), Locator: loc}
	if message, ok := b.messages[op.MessageID]; ok {
		n.Label += " · " + cmp.Or(message.Name, message.ID)
	} else {
		b.diagnostic("event_operation_message_missing", "warning", p+"/messageId", "Тип сообщения операции не найден.", id, true)
	}
	if op.Kafka != nil {
		n.GroupID, n.ClientID = op.Kafka.GroupID, op.Kafka.ClientID
	}
	b.node(n)
	if !slices.ContainsFunc(b.doc.Participants, func(participant Participant) bool { return participant.ID == contract.ParticipantID }) {
		b.diagnostic("event_participant_missing", "warning", p, "Владелец событийной операции не найден.", id, true)
	}
	b.edge("ownership", eventMapID("participant", contract.ParticipantID), id, "Контракт", loc)
}

// operationChannel draws the send/receive edge; true means the operation's
// channel exists and carries its message, so failure routes may be drawn too.
func (b *eventMapBuilder) operationChannel(op EventOperation, p, id string, loc EventMapLocator) bool {
	channel, channelOK := b.channels[op.ChannelID]
	if !channelOK {
		b.diagnostic("event_operation_channel_missing", "warning", p+"/channelId", "Топик операции не найден.", id, true)
	}
	messageOK := channelOK && slices.Contains(channel.MessageIDs, op.MessageID)
	if channelOK && !messageOK {
		b.diagnostic("event_operation_message_mismatch", "warning", p+"/messageId", "Тип сообщения не входит в выбранный топик.", id, true)
	}
	actionOK := op.Action == "send" || op.Action == "receive"
	if !actionOK {
		b.diagnostic("event_action_invalid", "warning", p+"/action", "Действие операции должно быть send или receive.", id, true)
	}
	if channelOK && messageOK && actionOK {
		if op.Action == "send" {
			b.edge("send", id, eventMapID("channel", op.ChannelID), "Публикует · "+op.MessageID, loc)
		} else {
			b.edge("receive", eventMapID("channel", op.ChannelID), id, "Получает · "+op.MessageID, loc)
		}
	}
	return messageOK
}

// failureRoutes checks and draws the retry/dead-letter routes; false means
// the context was cancelled and the operation must stop.
func (b *eventMapBuilder) failureRoutes(contract EventContract, op EventOperation, p, id string, routed bool) bool {
	if op.Action != "receive" {
		b.diagnostic("event_route_invalid_action", "warning", p+"/failureRoutes", "Маршруты ошибок доступны только для receive.", id, true)
	}
	if op.FailureRoutes.RetryChannelID != "" && op.FailureRoutes.RetryChannelID == op.FailureRoutes.DeadLetterChannelID {
		b.diagnostic("event_route_duplicate_target", "warning", p+"/failureRoutes", "Повтор и dead-letter должны вести в разные топики.", id, true)
	}
	for _, route := range []struct{ kind, id, field string }{{"retry", op.FailureRoutes.RetryChannelID, "retryChannelId"}, {"dead_letter", op.FailureRoutes.DeadLetterChannelID, "deadLetterChannelId"}} {
		if b.ctx.Err() != nil {
			return false
		}
		if route.id == "" {
			continue
		}
		rp := p + "/failureRoutes/" + route.field
		if route.id == op.ChannelID {
			b.diagnostic("event_route_self_target", "warning", rp, "Маршрут не может вести в исходный топик.", id, true)
			continue
		}
		if _, ok := b.channels[route.id]; !ok {
			b.diagnostic("event_route_target_missing", "warning", rp, "Целевой топик не найден.", id, true)
			continue
		}
		if op.Action == "receive" && routed {
			b.edge(route.kind, id, eventMapID("channel", route.id), map[string]string{"retry": "Повтор", "dead_letter": "Dead-letter"}[route.kind], EventMapLocator{Pointer: rp, EntityID: op.ID, ContractID: contract.ID, OperationID: op.ID})
		}
		if !b.sameServers(op.ChannelID, route.id) {
			b.diagnostic("event_route_cross_server", "warning", rp, "Маршрут пересекает Kafka-серверы; доставка не проверена.", id, false)
		}
	}
	return true
}

func (b *eventMapBuilder) sameServers(a, c string) bool {
	one, okA := b.channels[a]
	two, okB := b.channels[c]
	if !okA || !okB || len(one.ServerIDs) == 0 || len(two.ServerIDs) == 0 {
		return true
	}
	for _, id := range one.ServerIDs {
		if slices.Contains(two.ServerIDs, id) {
			return true
		}
	}
	return false
}

func (b *eventMapBuilder) linkedLocator(pointer string, entry eventMapHTTPContract) EventMapLocator {
	loc := EventMapLocator{Pointer: pointer, HTTPContractID: entry.contract.ID, Mode: cmp.Or(entry.contract.Mode, "copy")}
	if entry.contract.Source != nil {
		loc.PinnedRevisionID = entry.contract.Source.RevisionID
	}
	return loc
}

func (b *eventMapBuilder) apiLink(source, p string, k int, link EventAPILink) {
	pointer := fmt.Sprintf("%s/apiLinks/%d", p, k)
	if b.httpAmbiguous[link.ContractID] {
		b.diagnostic("event_api_contract_ambiguous", "warning", pointer, "HTTP-контракт ссылки неоднозначен.", source, true)
		return
	}
	entry, ok := b.http[link.ContractID]
	if !ok {
		b.diagnostic("event_api_contract_missing", "warning", pointer, "HTTP-контракт ссылки не найден.", source, true)
		return
	}
	ops := entry.operations[link.OperationKey]
	if len(ops) != 1 || ops[0].Uncertain {
		b.diagnostic("event_api_operation_unresolved", "warning", pointer, "Операция HTTP не найдена или неоднозначна во встроенном контракте.", source, true)
		return
	}
	op := ops[0]
	id := eventMapID("api_operation", link.ContractID, link.OperationKey)
	loc := b.linkedLocator(fmt.Sprintf("/contracts/%d/document%s", entry.index, op.Pointer), entry)
	loc.OperationKey, loc.Method, loc.Path = link.OperationKey, op.Method, op.Path
	b.node(EventMapNode{ID: id, Kind: "api_operation", Label: strings.ToUpper(op.Method) + " " + op.Path, Locator: loc})
	b.edge("api_link", source, id, "API", EventMapLocator{Pointer: pointer, HTTPContractID: link.ContractID, OperationKey: link.OperationKey, Mode: loc.Mode, PinnedRevisionID: loc.PinnedRevisionID})
}

func (b *eventMapBuilder) stateLink(source, p string, k int, link EventStateLink) {
	pointer := fmt.Sprintf("%s/stateLinks/%d", p, k)
	if b.httpAmbiguous[link.ContractID] {
		b.diagnostic("event_state_contract_ambiguous", "warning", pointer, "HTTP-контракт диаграммы неоднозначен.", source, true)
		return
	}
	entry, ok := b.http[link.ContractID]
	if !ok {
		b.diagnostic("event_state_contract_missing", "warning", pointer, "HTTP-контракт диаграммы не найден.", source, true)
		return
	}
	if entry.badState {
		b.diagnostic("event_state_diagram_invalid", "warning", pointer, "Диаграммы состояний во встроенном контракте не удалось прочитать.", source, true)
		return
	}
	state, ok := entry.states[link.DiagramID]
	if !ok {
		b.diagnostic("event_state_diagram_missing", "warning", pointer, "Диаграмма состояний не найдена.", source, true)
		return
	}
	diagram := state.diagram
	var transition *statediagram.Transition
	transitionIndex := -1
	for i := range diagram.Transitions {
		if diagram.Transitions[i].ID == link.TransitionID {
			transition = &diagram.Transitions[i]
			transitionIndex = i
			break
		}
	}
	if transition == nil {
		b.diagnostic("event_state_transition_missing", "warning", pointer, "Переход состояний не найден.", source, true)
		return
	}
	id := eventMapID("state_transition", link.ContractID, link.DiagramID, link.TransitionID)
	loc := b.linkedLocator(fmt.Sprintf("/contracts/%d/document/%s/diagrams/%d/transitions/%d", entry.index, statediagram.Extension, state.index, transitionIndex), entry)
	loc.DiagramID, loc.TransitionID = link.DiagramID, link.TransitionID
	if transition.Binding != nil {
		loc.Method, loc.Path = transition.Binding.Method, transition.Binding.Path
	} else {
		b.diagnostic("event_state_transition_unbound", "warning", pointer, "Переход не привязан к операции API.", id, false)
	}
	b.node(EventMapNode{ID: id, Kind: "state_transition", Label: diagram.Name + " / " + transition.Name, Locator: loc})
	b.edge("state_link", source, id, "Состояние", EventMapLocator{Pointer: pointer, HTTPContractID: link.ContractID, DiagramID: link.DiagramID, TransitionID: link.TransitionID, Mode: loc.Mode, PinnedRevisionID: loc.PinnedRevisionID})
}

// eventRouteSite is one failure route between two channels, kept with the
// pointer and element its diagnostics name.
type eventRouteSite struct{ from, to, pointer, element string }

func (b *eventMapBuilder) topologyDiagnostics() {
	producers, consumers, channelSenders := b.channelTraffic()
	routes, routeSites, ok := b.failureRouteSites(channelSenders)
	if !ok {
		return
	}
	for _, site := range routeSites {
		if b.ctx.Err() != nil {
			return
		}
		if b.routeReaches(routes, site.to, site.from, map[string]bool{}) {
			b.diagnostic("event_route_cycle", "warning", site.pointer, "Маршрут повторов образует цикл; завершение доставки не проверено.", site.element, false)
		}
	}
	for i, ch := range b.doc.EventModel.Channels {
		for j, id := range ch.MessageIDs {
			if b.ctx.Err() != nil {
				return
			}
			p := fmt.Sprintf("/eventModel/channels/%d/messageIds/%d", i, j)
			key := eventMapID(ch.ID, id)
			if !producers[key] {
				b.diagnostic("event_producer_missing", "info", p, "Для типа сообщения не указан производитель.", eventMapID("channel", ch.ID), false)
			}
			if !consumers[key] {
				b.diagnostic("event_consumer_missing", "info", p, "Для типа сообщения не указан потребитель.", eventMapID("channel", ch.ID), false)
			}
		}
	}
}

// channelTraffic indexes which channel/message pairs have a sender and a
// receiver, counting only operations whose message belongs to their channel.
func (b *eventMapBuilder) channelTraffic() (producers, consumers, channelSenders map[string]bool) {
	producers, consumers, channelSenders = map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, contract := range b.doc.EventModel.Contracts {
		for _, op := range contract.Operations {
			ch, ok := b.channels[op.ChannelID]
			if !ok || !slices.Contains(ch.MessageIDs, op.MessageID) {
				continue
			}
			key := eventMapID(op.ChannelID, op.MessageID)
			switch op.Action {
			case "send":
				producers[key] = true
				channelSenders[op.ChannelID] = true
			case "receive":
				consumers[key] = true
			}
		}
	}
	return producers, consumers, channelSenders
}

// failureRouteSites collects the valid failure routes as a channel graph and
// flags targets nobody publishes to; false means the context was cancelled.
func (b *eventMapBuilder) failureRouteSites(channelSenders map[string]bool) (map[string][]string, []eventRouteSite, bool) {
	routes := map[string][]string{}
	routeSites := []eventRouteSite{}
	for ci, contract := range b.doc.EventModel.Contracts {
		for oi, op := range contract.Operations {
			if b.ctx.Err() != nil {
				return nil, nil, false
			}
			if op.FailureRoutes == nil {
				continue
			}
			ch, ok := b.channels[op.ChannelID]
			if op.Action != "receive" || !ok || !slices.Contains(ch.MessageIDs, op.MessageID) {
				continue
			}
			for _, route := range []struct{ id, field string }{{op.FailureRoutes.RetryChannelID, "retryChannelId"}, {op.FailureRoutes.DeadLetterChannelID, "deadLetterChannelId"}} {
				if route.id == "" || route.id == op.ChannelID {
					continue
				}
				if _, exists := b.channels[route.id]; !exists {
					continue
				}
				p := fmt.Sprintf("/eventModel/contracts/%d/operations/%d/failureRoutes/%s", ci, oi, route.field)
				id := eventMapID("operation", contract.ID, op.ID)
				if !channelSenders[route.id] {
					b.diagnostic("event_route_sender_missing", "warning", p, "Для целевого топика не указан производитель.", id, false)
				}
				routes[op.ChannelID] = append(routes[op.ChannelID], route.id)
				routeSites = append(routeSites, eventRouteSite{op.ChannelID, route.id, p, id})
			}
		}
	}
	return routes, routeSites, true
}

func (b *eventMapBuilder) routeReaches(routes map[string][]string, id, target string, seen map[string]bool) bool {
	if id == target {
		return true
	}
	if seen[id] || b.ctx.Err() != nil {
		return false
	}
	seen[id] = true
	for _, next := range routes[id] {
		if b.routeReaches(routes, next, target, seen) {
			return true
		}
	}
	return false
}

func (b *eventMapBuilder) finalize() (EventMapAnalysis, error) {
	if len(b.ambiguous) > 0 {
		edges := b.out.Edges[:0]
		for _, edge := range b.out.Edges {
			if !b.ambiguous[edge.Source] && !b.ambiguous[edge.Target] {
				edges = append(edges, edge)
			}
		}
		b.out.Edges = edges
	}
	return finalizeEventMap(b.ctx, b.out)
}

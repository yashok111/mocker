package designscenario

import (
	"fmt"
	"slices"
	"strings"
)

func eventDefinitionCommand(kind string) bool {
	return strings.HasPrefix(kind, "upsert_event_") && kind != "upsert_event_api_link" && kind != "upsert_event_state_link"
}

func eventModelForCommand(document *Document, command Command) (*EventModel, error) {
	if document.EventModel != nil {
		return document.EventModel, nil
	}
	if !eventDefinitionCommand(command.Type) || command.Type == "upsert_event_operation" {
		return nil, invalidAt("/id", "модель событий не существует")
	}
	if document.FormatVersion == 1 {
		candidate := *document
		candidate.FormatVersion = 2
		if diagnostics := ValidateFragments(candidate); len(diagnostics) != 0 {
			return nil, &InvalidError{Diagnostics: diagnostics}
		}
	}
	document.FormatVersion = 3
	document.EventModel = &EventModel{Servers: []EventServer{}, Channels: []EventChannel{}, Messages: []EventMessage{}, Schemas: []EventSchema{}, Contracts: []EventContract{}}
	return document.EventModel, nil
}

func upsertEventItem[T any](items *[]T, value T, id func(T) string) {
	for i := range *items {
		if id((*items)[i]) == id(value) {
			(*items)[i] = value
			return
		}
	}
	*items = append(*items, value)
}

func removeEventItem[T any](items *[]T, target string, id func(T) string) bool {
	i := slices.IndexFunc(*items, func(value T) bool { return id(value) == target })
	if i < 0 {
		return false
	}
	*items = slices.Delete(*items, i, i+1)
	return true
}

func eventOperationForCommand(model *EventModel, contractID, operationID string) (*EventOperation, error) {
	for i := range model.Contracts {
		if model.Contracts[i].ID != contractID {
			continue
		}
		for j := range model.Contracts[i].Operations {
			if model.Contracts[i].Operations[j].ID == operationID {
				return &model.Contracts[i].Operations[j], nil
			}
		}
		return nil, invalidAt("/id", "операция события не существует")
	}
	return nil, invalidAt("/contractId", "контракт события не существует")
}

func applyEventCommand(document *Document, command Command) error {
	model, err := eventModelForCommand(document, command)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(command.Type, "upsert_") {
		if dependents := eventCommandDependents(*document, command); len(dependents) > 0 {
			return invalidAt("/id", fmt.Sprintf("зависимые элементы: %s", strings.Join(dependents, ", ")))
		}
	}
	switch command.Type {
	case "upsert_event_server", "remove_event_server", "upsert_event_channel", "remove_event_channel":
		return applyEventTransportCommand(model, command)
	case "upsert_event_message", "remove_event_message", "upsert_event_schema", "remove_event_schema":
		return applyEventPayloadCommand(model, command)
	case "upsert_event_contract", "remove_event_contract", "upsert_event_operation", "remove_event_operation":
		return applyEventContractCommand(model, command)
	case "set_event_failure_routes", "upsert_event_api_link", "remove_event_api_link", "upsert_event_state_link", "remove_event_state_link":
		operation, err := eventOperationForCommand(model, command.ContractID, command.ID)
		if err != nil {
			return err
		}
		return applyEventOperationLinkCommand(operation, command)
	}
	return nil
}

// applyEventTransportCommand edits servers and channels.
func applyEventTransportCommand(model *EventModel, command Command) error {
	switch command.Type {
	case "upsert_event_server":
		if command.EventServer == nil {
			return invalidAt("/eventServer", "сервер обязателен")
		}
		upsertEventItem(&model.Servers, *command.EventServer, func(v EventServer) string { return v.ID })
	case "remove_event_server":
		if !removeEventItem(&model.Servers, command.ID, func(v EventServer) string { return v.ID }) {
			return invalidAt("/id", "сервер не существует")
		}
	case "upsert_event_channel":
		if command.EventChannel == nil {
			return invalidAt("/eventChannel", "канал обязателен")
		}
		upsertEventItem(&model.Channels, *command.EventChannel, func(v EventChannel) string { return v.ID })
	case "remove_event_channel":
		if !removeEventItem(&model.Channels, command.ID, func(v EventChannel) string { return v.ID }) {
			return invalidAt("/id", "канал не существует")
		}
	}
	return nil
}

// applyEventPayloadCommand edits messages and schemas.
func applyEventPayloadCommand(model *EventModel, command Command) error {
	switch command.Type {
	case "upsert_event_message":
		if command.EventMessage == nil {
			return invalidAt("/eventMessage", "сообщение обязательно")
		}
		upsertEventItem(&model.Messages, *command.EventMessage, func(v EventMessage) string { return v.ID })
	case "remove_event_message":
		if !removeEventItem(&model.Messages, command.ID, func(v EventMessage) string { return v.ID }) {
			return invalidAt("/id", "сообщение не существует")
		}
	case "upsert_event_schema":
		if command.EventSchema == nil {
			return invalidAt("/eventSchema", "схема обязательна")
		}
		upsertEventItem(&model.Schemas, *command.EventSchema, func(v EventSchema) string { return v.ID })
	case "remove_event_schema":
		if !removeEventItem(&model.Schemas, command.ID, func(v EventSchema) string { return v.ID }) {
			return invalidAt("/id", "схема не существует")
		}
	}
	return nil
}

// applyEventContractCommand edits contracts and the operations inside them.
func applyEventContractCommand(model *EventModel, command Command) error {
	switch command.Type {
	case "upsert_event_contract":
		if command.EventContract == nil {
			return invalidAt("/eventContract", "контракт обязателен")
		}
		upsertEventItem(&model.Contracts, *command.EventContract, func(v EventContract) string { return v.ID })
	case "remove_event_contract":
		if !removeEventItem(&model.Contracts, command.ID, func(v EventContract) string { return v.ID }) {
			return invalidAt("/id", "контракт не существует")
		}
	case "upsert_event_operation":
		if command.EventOperation == nil {
			return invalidAt("/eventOperation", "операция обязательна")
		}
		for i := range model.Contracts {
			if model.Contracts[i].ID == command.ContractID {
				upsertEventItem(&model.Contracts[i].Operations, *command.EventOperation, func(v EventOperation) string { return v.ID })
				return nil
			}
		}
		return invalidAt("/contractId", "контракт события не существует")
	case "remove_event_operation":
		for i := range model.Contracts {
			if model.Contracts[i].ID == command.ContractID {
				if !removeEventItem(&model.Contracts[i].Operations, command.ID, func(v EventOperation) string { return v.ID }) {
					return invalidAt("/id", "операция события не существует")
				}
				return nil
			}
		}
		return invalidAt("/contractId", "контракт события не существует")
	}
	return nil
}

// applyEventOperationLinkCommand edits the routes and links of one resolved operation.
func applyEventOperationLinkCommand(operation *EventOperation, command Command) error {
	switch command.Type {
	case "set_event_failure_routes":
		operation.FailureRoutes = command.FailureRoutes
	case "upsert_event_api_link":
		if command.APILink == nil {
			return invalidAt("/apiLink", "связь API обязательна")
		}
		if !slices.Contains(operation.APILinks, *command.APILink) {
			operation.APILinks = append(operation.APILinks, *command.APILink)
		}
	case "remove_event_api_link":
		if command.APILink == nil {
			return invalidAt("/apiLink", "связь API обязательна")
		}
		i := slices.Index(operation.APILinks, *command.APILink)
		if i < 0 {
			return invalidAt("/apiLink", "связь API не существует")
		}
		operation.APILinks = slices.Delete(operation.APILinks, i, i+1)
	case "upsert_event_state_link":
		if command.StateLink == nil {
			return invalidAt("/stateLink", "связь состояния обязательна")
		}
		if !slices.Contains(operation.StateLinks, *command.StateLink) {
			operation.StateLinks = append(operation.StateLinks, *command.StateLink)
		}
	case "remove_event_state_link":
		if command.StateLink == nil {
			return invalidAt("/stateLink", "связь состояния обязательна")
		}
		i := slices.Index(operation.StateLinks, *command.StateLink)
		if i < 0 {
			return invalidAt("/stateLink", "связь состояния не существует")
		}
		operation.StateLinks = slices.Delete(operation.StateLinks, i, i+1)
	}
	return nil
}

func eventCommandDependents(document Document, command Command) []string {
	model := document.EventModel
	if model == nil {
		return nil
	}
	var dependents []string
	add := func(kind, id string) { dependents = append(dependents, kind+":"+id) }
	switch command.Type {
	case "remove_event_server":
		eventChannelDependents(model, add, func(channel EventChannel) bool { return slices.Contains(channel.ServerIDs, command.ID) })
	case "remove_event_channel":
		eventOperationDependents(model, add, func(operation EventOperation) bool {
			return operation.ChannelID == command.ID || operation.FailureRoutes != nil && (operation.FailureRoutes.RetryChannelID == command.ID || operation.FailureRoutes.DeadLetterChannelID == command.ID)
		})
	case "remove_event_message":
		eventChannelDependents(model, add, func(channel EventChannel) bool { return slices.Contains(channel.MessageIDs, command.ID) })
		eventOperationDependents(model, add, func(operation EventOperation) bool { return operation.MessageID == command.ID })
	case "remove_event_schema":
		for _, message := range model.Messages {
			if message.PayloadSchemaID == command.ID || message.KeySchemaID == command.ID || message.HeadersSchemaID == command.ID {
				add("message", message.ID)
			}
		}
	case "remove_event_contract":
		eventBindingDependents(document, add, func(binding EventBinding) bool { return binding.ContractID == command.ID })
	case "remove_event_operation":
		eventBindingDependents(document, add, func(binding EventBinding) bool {
			return binding.ContractID == command.ContractID && binding.OperationID == command.ID
		})
	}
	slices.Sort(dependents)
	return slices.Compact(dependents)
}

func eventChannelDependents(model *EventModel, add func(kind, id string), uses func(EventChannel) bool) {
	for _, channel := range model.Channels {
		if uses(channel) {
			add("channel", channel.ID)
		}
	}
}

func eventOperationDependents(model *EventModel, add func(kind, id string), uses func(EventOperation) bool) {
	for _, contract := range model.Contracts {
		for _, operation := range contract.Operations {
			if uses(operation) {
				add("operation", contract.ID+"/"+operation.ID)
			}
		}
	}
}

func eventBindingDependents(document Document, add func(kind, id string), uses func(EventBinding) bool) {
	for _, message := range document.Messages {
		for _, binding := range message.EventBindings {
			if uses(binding) {
				add("message", message.ID)
			}
		}
	}
}

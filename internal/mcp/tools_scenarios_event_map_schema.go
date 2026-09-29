package mcp

func eventFailureRoutesSchema() map[string]any {
	return designScenarioSchemaObject([]string{}, map[string]any{
		"retryChannelId": designScenarioRunIDSchema(), "deadLetterChannelId": designScenarioRunIDSchema(),
	})
}

func eventAPILinkSchema() map[string]any {
	return designScenarioSchemaObject([]string{"contractId", "operationKey"}, map[string]any{
		"contractId": map[string]any{"type": "string", "minLength": 1}, "operationKey": designScenarioOperationKeySchema(),
	})
}

func eventStateLinkSchema() map[string]any {
	return designScenarioSchemaObject([]string{"contractId", "diagramId", "transitionId"}, map[string]any{
		"contractId":   map[string]any{"type": "string", "minLength": 1},
		"diagramId":    map[string]any{"type": "string", "minLength": 1},
		"transitionId": map[string]any{"type": "string", "minLength": 1},
	})
}

// Reuse exactly the full-document entity schemas for narrow commands. This
// keeps editor saves and agent batches on the same strict metadata contract.
func eventMapCommandSchemas() []any {
	properties := eventModelSchema()["properties"].(map[string]any)
	entity := func(collection string) any { return properties[collection].(map[string]any)["items"] }
	command := func(kind string, required []string, fields map[string]any) any {
		fields["type"] = map[string]any{"type": "string", "const": kind}
		return designScenarioSchemaObject(append([]string{"type"}, required...), fields)
	}
	result := []any{}
	for _, item := range []struct{ name, collection, payload string }{
		{"server", "servers", "eventServer"}, {"channel", "channels", "eventChannel"},
		{"message", "messages", "eventMessage"}, {"schema", "schemas", "eventSchema"}, {"contract", "contracts", "eventContract"},
	} {
		result = append(result, command("upsert_event_"+item.name, []string{item.payload}, map[string]any{item.payload: entity(item.collection)}),
			command("remove_event_"+item.name, []string{"id"}, map[string]any{"id": designScenarioRunIDSchema()}))
	}
	operation := entity("contracts").(map[string]any)["properties"].(map[string]any)["operations"].(map[string]any)["items"]
	result = append(result,
		command("upsert_event_operation", []string{"contractId", "eventOperation"}, map[string]any{"contractId": designScenarioRunIDSchema(), "eventOperation": operation}),
		command("remove_event_operation", []string{"contractId", "id"}, map[string]any{"contractId": designScenarioRunIDSchema(), "id": designScenarioRunIDSchema()}),
		command("set_event_failure_routes", []string{"contractId", "id"}, map[string]any{"contractId": designScenarioRunIDSchema(), "id": designScenarioRunIDSchema(), "failureRoutes": eventFailureRoutesSchema()}),
	)
	for _, item := range []struct {
		name, payload string
		schema        any
	}{{"api", "apiLink", eventAPILinkSchema()}, {"state", "stateLink", eventStateLinkSchema()}} {
		for _, verb := range []string{"upsert", "remove"} {
			result = append(result, command(verb+"_event_"+item.name+"_link", []string{"contractId", "id", item.payload}, map[string]any{"contractId": designScenarioRunIDSchema(), "id": designScenarioRunIDSchema(), item.payload: item.schema}))
		}
	}
	return result
}

func eventMapCommandsInputSchema() map[string]any {
	return designScenarioSchemaObject([]string{"scenarioId", "expectedVersion", "commands"}, map[string]any{
		"scenarioId": designScenarioPositiveIntegerSchema(), "expectedVersion": designScenarioPositiveIntegerSchema(),
		"commands": map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "items": map[string]any{"oneOf": eventMapCommandSchemas()}},
		"summary":  map[string]any{"type": "string"},
	})
}

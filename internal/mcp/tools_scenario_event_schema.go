package mcp

func eventArraySchema(item any, maximum int) map[string]any {
	return map[string]any{"type": "array", "items": item, "maxItems": maximum}
}

func eventEntitySchema(required []string, fields map[string]any) map[string]any {
	fields["id"] = designScenarioRunIDSchema()
	fields["name"] = map[string]any{"type": "string", "maxLength": 50_000}
	fields["description"] = map[string]any{"type": "string", "maxLength": 50_000}
	return designScenarioSchemaObject(append([]string{"id", "name", "description"}, required...), fields)
}

func eventBindingSchema() map[string]any {
	return designScenarioSchemaObject(
		[]string{"contractId", "operationId"},
		map[string]any{"contractId": designScenarioRunIDSchema(), "operationId": designScenarioRunIDSchema()},
	)
}

func eventBindingsSchema() map[string]any {
	schema := eventArraySchema(eventBindingSchema(), 2)
	schema["minItems"] = 1
	schema["uniqueItems"] = true
	return schema
}

func eventMessageBindingCondition() map[string]any {
	return map[string]any{
		"if": map[string]any{"required": []string{"eventBindings"}},
		"then": map[string]any{"properties": map[string]any{
			"kind": map[string]any{"const": "event"}, "operation": false,
		}},
	}
}

func eventModelSchema() map[string]any {
	text := func() map[string]any { return map[string]any{"type": "string", "maxLength": 50_000} }
	idList := func() map[string]any {
		schema := eventArraySchema(designScenarioRunIDSchema(), 100)
		schema["uniqueItems"] = true
		return schema
	}
	server := eventEntitySchema([]string{"host", "protocol", "auth"}, map[string]any{
		"host":     text(),
		"protocol": map[string]any{"type": "string", "enum": []string{"kafka", "kafka-secure"}},
		"auth": map[string]any{
			"type": "string", "enum": []string{"unspecified", "none", "plain", "scramSha256", "scramSha512"},
		},
	})
	channel := eventEntitySchema([]string{"address", "serverIds", "messageIds"}, map[string]any{
		"address": text(), "serverIds": idList(), "messageIds": idList(),
		"discriminatorProperty": text(),
		"kafka": designScenarioSchemaObject([]string{}, map[string]any{
			"partitions": map[string]any{"type": "integer", "minimum": 1, "maximum": 2147483647},
			"replicas":   map[string]any{"type": "integer", "minimum": 1, "maximum": 2147483647},
		}),
	})
	example := designScenarioSchemaObject([]string{"name", "payloadJSON"}, map[string]any{
		"name":        text(),
		"payloadJSON": map[string]any{"type": "string", "maxLength": 262144},
		"headersJSON": map[string]any{"type": "string", "maxLength": 262144},
	})
	message := eventEntitySchema([]string{"examples"}, map[string]any{
		"payloadSchemaId": designScenarioRunIDSchema(),
		"headersSchemaId": designScenarioRunIDSchema(),
		"keySchemaId":     designScenarioRunIDSchema(),
		"examples":        eventArraySchema(example, 20),
	})
	schema := eventEntitySchema([]string{"schemaJSON"}, map[string]any{
		"schemaJSON": map[string]any{"type": "string", "maxLength": 262144},
	})
	kafkaID := func() map[string]any {
		return map[string]any{"type": "string", "minLength": 1, "maxLength": 256, "pattern": "^[^\r\n\x00]+$"}
	}
	operation := eventEntitySchema([]string{"action", "channelId", "messageId"}, map[string]any{
		"action":    map[string]any{"type": "string", "enum": []string{"send", "receive"}},
		"channelId": designScenarioRunIDSchema(),
		"messageId": designScenarioRunIDSchema(),
		"kafka": designScenarioSchemaObject([]string{}, map[string]any{
			"groupId": kafkaID(), "clientId": kafkaID(),
		}),
	})
	contract := eventEntitySchema([]string{"participantId", "version", "operations"}, map[string]any{
		"participantId": map[string]any{"type": "string", "minLength": 1, "maxLength": 50_000},
		"version":       text(), "operations": eventArraySchema(operation, 2000),
	})
	return designScenarioSchemaObject(
		[]string{"servers", "channels", "messages", "schemas", "contracts"},
		map[string]any{
			"servers": eventArraySchema(server, 100), "channels": eventArraySchema(channel, 500),
			"messages": eventArraySchema(message, 1000), "schemas": eventArraySchema(schema, 1000),
			"contracts": eventArraySchema(contract, 200),
		},
	)
}

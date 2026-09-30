package mcp

func responseRuleInputSchema(name string) map[string]any {
	p := map[string]any{"designId": designScenarioPositiveIntegerSchema()}
	required := []string{"designId"}
	if name != "list_response_rules" && name != "create_response_rule" {
		p["ruleId"] = responseRuleIDSchema()
		required = append(required, "ruleId")
	}
	switch name {
	case "create_response_rule", "save_response_rule", "delete_response_rule", "apply_response_rule_commands":
		p["expectedVersion"] = schemaModelVersionSchema()
		required = append(required, "expectedVersion")
	}
	switch name {
	case "create_response_rule", "save_response_rule":
		p["rule"] = responseRuleSchema()
		required = append(required, "rule")
	case "apply_response_rule_commands":
		p["commands"] = responseRuleArraySchema(responseRuleCommandSchema(), 200)
		required = append(required, "commands")
	case "validate_response_rule", "simulate_response_rule":
		p["document"] = map[string]any{"type": "string", "description": "Exact proposed OpenAPI text; omission loads saved draft, empty is invalid."}
		if name == "simulate_response_rule" {
			p["request"] = designScenarioSchemaObject([]string{"query", "headers"}, map[string]any{
				"query": responseRuleFieldsSchema(), "headers": responseRuleFieldsSchema(),
				"bodyJSON": responseRuleTextSchema(65536), "path": responseRuleFieldsSchema(), "entities": responseRuleArraySchema(responseRuleEntityFixtureSchema(), 100),
			})
			required = append(required, "request")
		}
	}
	return designScenarioSchemaObject(required, p)
}

func responseRuleSchema() map[string]any {
	return designScenarioSchemaObject([]string{"id", "name", "nodes", "edges"}, map[string]any{
		"id": responseRuleIDSchema(), "name": responseRuleTextSchema(200), "binding": responseRuleBindingSchema(),
		"nodes": responseRuleArraySchema(responseRuleNodeSchema(), 100),
		"edges": responseRuleArraySchema(responseRuleEdgeSchema(), 200),
	})
}

func responseRuleNodeSchema() map[string]any {
	variants := []any{}
	for _, kind := range []string{"start", "condition", "delay", "response", "fallback", "entity_read", "entity_create", "entity_update"} {
		p := map[string]any{"id": responseRuleIDSchema(), "name": responseRuleTextSchema(200),
			"type": map[string]any{"type": "string", "const": kind}, "x": responseRuleCoordinateSchema(), "y": responseRuleCoordinateSchema()}
		required := []string{"id", "type", "name", "x", "y"}
		switch kind {
		case "condition":
			p["condition"] = responseRuleConditionSchema()
			required = append(required, "condition")
		case "delay":
			p["delayMs"] = map[string]any{"type": "integer"}
			required = append(required, "delayMs")
		case "response":
			response := designScenarioSchemaObject([]string{"status", "mediaType", "headers"}, map[string]any{
				"status": map[string]any{"type": "integer"}, "mediaType": responseRuleTextSchema(4096),
				"headers": responseRuleFieldsSchema(), "bodyJSON": responseRuleTextSchema(65536), "bodyFrom": responseRuleValueRefSchema(),
			})
			response["not"] = map[string]any{"required": []string{"bodyJSON", "bodyFrom"}}
			p["response"] = response
			required = append(required, "response")
		case "entity_read", "entity_create", "entity_update":
			p["entity"] = responseRuleEntityOperationSchema(kind)
			required = append(required, "entity")
		}
		variants = append(variants, designScenarioSchemaObject(required, p))
	}
	return map[string]any{"oneOf": variants}
}

func responseRuleConditionSchema() map[string]any {
	return designScenarioSchemaObject([]string{"in", "name", "op"}, map[string]any{
		"in": map[string]any{"type": "string"}, "name": responseRuleTextSchema(256),
		"op": map[string]any{"type": "string"}, "value": responseRuleTextSchema(4096),
	})
}

func responseRuleEdgeSchema() map[string]any {
	return designScenarioSchemaObject([]string{"id", "from", "port", "to"}, map[string]any{
		"id": responseRuleIDSchema(), "from": responseRuleIDSchema(), "to": responseRuleIDSchema(),
		"port": map[string]any{"type": "string"},
	})
}

func responseRuleCommandSchema() map[string]any {
	variants := []any{}
	for _, kind := range []string{"set_rule", "add_node", "update_node", "remove_node", "add_edge", "update_edge", "remove_edge", "move_nodes"} {
		p := map[string]any{"type": map[string]any{"type": "string", "const": kind}}
		required := []string{"type"}
		switch kind {
		case "set_rule":
			p["name"], p["binding"] = responseRuleTextSchema(200), responseRuleBindingSchema()
			required = append(required, "name")
		case "add_node", "update_node":
			p["node"] = responseRuleNodeSchema()
			required = append(required, "node")
		case "add_edge", "update_edge":
			p["edge"] = responseRuleEdgeSchema()
			required = append(required, "edge")
		case "remove_node":
			p["nodeId"] = responseRuleIDSchema()
			required = append(required, "nodeId")
		case "remove_edge":
			p["edgeId"] = responseRuleIDSchema()
			required = append(required, "edgeId")
		case "move_nodes":
			p["positions"] = responseRuleArraySchema(designScenarioSchemaObject([]string{"nodeId", "x", "y"}, map[string]any{
				"nodeId": responseRuleIDSchema(), "x": responseRuleCoordinateSchema(), "y": responseRuleCoordinateSchema(),
			}), 100)
			required = append(required, "positions")
		}
		variants = append(variants, designScenarioSchemaObject(required, p))
	}
	return map[string]any{"oneOf": variants}
}

func responseRuleBindingSchema() map[string]any {
	return designScenarioSchemaObject([]string{"method", "path"}, map[string]any{
		"method": map[string]any{"type": "string"}, "path": responseRuleTextSchema(2048),
	})
}

func responseRuleFieldsSchema() map[string]any {
	return responseRuleArraySchema(designScenarioSchemaObject([]string{"name", "value"}, map[string]any{
		"name": responseRuleTextSchema(256), "value": responseRuleTextSchema(4096),
	}), 100)
}

func responseRuleIDSchema() map[string]any {
	return map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]{1,80}$"}
}

func responseRuleCoordinateSchema() map[string]any {
	return map[string]any{"type": "number", "minimum": -100000, "maximum": 100000}
}

func responseRuleTextSchema(maxLength int) map[string]any {
	return map[string]any{"type": "string", "maxLength": maxLength}
}

func responseRuleArraySchema(items any, maxItems int) map[string]any {
	return map[string]any{"type": "array", "items": items, "maxItems": maxItems}
}

func responseRuleValueRefSchema() map[string]any {
	variants := []any{}
	for _, source := range []string{"literal", "path", "query", "header", "body", "result"} {
		p := map[string]any{"source": map[string]any{"type": "string", "const": source}}
		required := []string{"source"}
		switch source {
		case "literal":
			p["valueJSON"] = responseRuleTextSchema(65536)
			required = append(required, "valueJSON")
		case "path", "query", "header":
			p["name"] = responseRuleTextSchema(256)
			required = append(required, "name")
		case "body":
			p["pointer"] = responseRuleTextSchema(2048)
		case "result":
			p["pointer"] = responseRuleTextSchema(2048)
			p["nodeId"] = responseRuleIDSchema()
			required = append(required, "nodeId")
		}
		variants = append(variants, designScenarioSchemaObject(required, p))
	}
	return map[string]any{"oneOf": variants}
}
func responseRuleEntityOperationSchema(kind string) map[string]any {
	if kind == "entity_read" {
		return map[string]any{"oneOf": []any{responseRuleEntityOperationSchema("get"), responseRuleEntityOperationSchema("list")}}
	}
	p := map[string]any{"family": responseRuleTextSchema(2048), "scope": responseRuleArraySchema(responseRuleValueRefSchema(), 3)}
	required := []string{"family"}
	switch kind {
	case "get", "list":
		p["operation"] = map[string]any{"type": "string", "const": kind}
		required = append(required, "operation")
		if kind == "get" {
			p["key"] = responseRuleValueRefSchema()
			required = append(required, "key")
		}
	case "entity_create":
		p["data"] = responseRuleValueRefSchema()
		required = append(required, "data")
	case "entity_update":
		p["key"], p["data"] = responseRuleValueRefSchema(), responseRuleValueRefSchema()
		required = append(required, "key", "data")
	}
	return designScenarioSchemaObject(required, p)
}
func responseRuleEntityFixtureSchema() map[string]any {
	row := designScenarioSchemaObject([]string{"key", "scope", "dataJSON"}, map[string]any{"key": responseRuleTextSchema(128), "scope": responseRuleArraySchema(responseRuleTextSchema(4096), 3), "dataJSON": responseRuleTextSchema(65536)})
	return designScenarioSchemaObject([]string{"family", "idField", "idType", "rows"}, map[string]any{"family": responseRuleTextSchema(2048), "idField": responseRuleTextSchema(256), "idType": map[string]any{"type": "string", "enum": []string{"integer", "string"}}, "rows": responseRuleArraySchema(row, 100)})
}

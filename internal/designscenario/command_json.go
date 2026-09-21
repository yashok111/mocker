package designscenario

import (
	"bytes"
	"fmt"

	"github.com/yashok111/mocker/internal/jsonx"
)

type commandWireSpec struct {
	required []string
	optional []string
}

var commandWireSpecs = map[string]commandWireSpec{
	"set_title":          {required: []string{"title"}},
	"upsert_participant": {required: []string{"participant"}},
	"remove_participant": {required: []string{"id"}},
	"move_participant":   {required: []string{"id", "index"}},
	"upsert_message":     {required: []string{"message"}},
	"remove_message":     {required: []string{"id"}},
	"move_message":       {required: []string{"id", "index"}},
	"upsert_fragment":    {required: []string{"fragment"}},
	"create_contract":    {required: []string{"contract"}},
	"remove_fragment":    {required: []string{"id"}},
	"bind_operation":     {required: []string{"messageId", "contractId", "operationKey"}},
	"create_operation": {
		required: []string{"messageId", "method", "path"},
		optional: []string{"contractId", "label"},
	},
	"import_contract": {
		required: []string{"designId", "mode"},
		optional: []string{"id", "revisionId"},
	},
	"refresh_contract": {
		required: []string{"contractId"},
		optional: []string{"revisionId"},
	},
	"detach_contract":      {required: []string{"contractId"}},
	"materialize_contract": {required: []string{"contractId"}},
}

// UnmarshalJSON keeps Command's convenient service representation while
// enforcing its tagged-union wire contract. The default decoder only sees one
// struct containing every variant's fields, so it cannot distinguish a missing
// required string from a deliberately empty string or reject a field belonging
// to another command type.
func (c *Command) UnmarshalJSON(data []byte) error {
	fields := map[string]jsonx.RawMessage{}
	if err := jsonx.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("design scenario command must be an object: %w", err)
	}
	typeRaw, exists := fields["type"]
	if !exists || isJSONNull(typeRaw) {
		return fmt.Errorf("design scenario command field %q is required", "type")
	}
	var commandType string
	if err := jsonx.Unmarshal(typeRaw, &commandType); err != nil || commandType == "" {
		return fmt.Errorf("design scenario command field %q must be a non-empty string", "type")
	}
	spec, exists := commandWireSpecs[commandType]
	if !exists {
		return fmt.Errorf("unknown command type %q", commandType)
	}

	allowed := make(map[string]bool, 1+len(spec.required)+len(spec.optional))
	allowed["type"] = true
	for _, name := range spec.required {
		allowed[name] = true
		raw, present := fields[name]
		if !present || isJSONNull(raw) {
			return fmt.Errorf("design scenario command field %q is required for %q", name, commandType)
		}
	}
	for _, name := range spec.optional {
		allowed[name] = true
		if raw, present := fields[name]; present && isJSONNull(raw) {
			return fmt.Errorf("design scenario command field %q cannot be null", name)
		}
	}
	for name := range fields {
		if !allowed[name] {
			return fmt.Errorf("design scenario command field %q is not allowed for %q", name, commandType)
		}
	}
	if commandType == "create_contract" {
		var contractFields map[string]jsonx.RawMessage
		if err := jsonx.Unmarshal(fields["contract"], &contractFields); err != nil {
			return fmt.Errorf("create_contract contract must be an object: %w", err)
		}
		if _, exists := contractFields["source"]; exists {
			return fmt.Errorf("create_contract contract must not have a source")
		}
		for _, name := range []string{"id", "name", "document"} {
			if raw, exists := contractFields[name]; !exists || isJSONNull(raw) {
				return fmt.Errorf("create_contract contract field %q is required", name)
			}
		}
		if !isJSONObject(contractFields["document"]) {
			return fmt.Errorf("create_contract contract document must be a JSON object")
		}
		if raw, exists := contractFields["mode"]; exists && isJSONNull(raw) {
			return fmt.Errorf("create_contract contract mode cannot be null")
		}
	}

	type commandAlias Command
	var decoded commandAlias
	decoder := jsonx.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Errorf("decode design scenario command %q: %w", commandType, err)
	}
	*c = Command(decoded)
	return nil
}

func isJSONNull(raw jsonx.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func isJSONObject(raw jsonx.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '{' && jsonx.Valid(raw)
}

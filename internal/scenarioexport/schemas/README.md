# AsyncAPI 3.0.0 grammar

- Source: https://github.com/asyncapi/spec-json-schemas/blob/master/schemas/3.0.0.json
- Embedded resource ID: `http://asyncapi.com/definitions/3.0.0/asyncapi.json`
- License: Apache-2.0, AsyncAPI Initiative (source repository license)
- SHA-256: `1786a007ac00344a8f529e5a6fc5db786b0be6757e0b94a8b95a8a6a52b7fe9a`

The exporter verifies the checksum before compiling this schema with a loader that rejects external resources. The generated Kafka key uses `allOf: [{$ref: ...}]` because a direct reference matches two branches of the pinned grammar's `oneOf` rule.

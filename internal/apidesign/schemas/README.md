# OpenAPI document grammars

Unmodified OpenAPI Initiative JSON Schemas, downloaded 2026-09-19:

- `oas30-schema.json`: https://spec.openapis.org/oas/3.0/schema/2024-10-18
- `oas31-schema.json`: https://spec.openapis.org/oas/3.1/schema/2022-10-07
- `oas31-base.json`: https://spec.openapis.org/oas/3.1/schema-base/2022-10-07
- `oas31-dialect.json`: https://spec.openapis.org/oas/3.1/dialect/base
- `oas31-meta.json`: https://spec.openapis.org/oas/3.1/meta/base

Source project: https://github.com/OAI/OpenAPI-Specification (Apache-2.0).
These resources ship in the binary; the validator disables remote loading.
JSON Schema meta-schemas are provided by the existing jsonschema/v6 dependency.
At compilation, `grammar.go` extends the schema-base dialect allowlist to accept
the standard JSON Schema 2020-12 URI as well as the default OpenAPI dialect.

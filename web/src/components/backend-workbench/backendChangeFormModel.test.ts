import { describe, expect, it } from "vitest";
import { backendChangeSchemas } from "./backendChangeSchema";
import {
  changeSchemaDefault,
  completeChangeObject,
  changeSchemaIssues,
  parseChangeCommand,
} from "./backendChangeFormModel";

const id = "0197aaf9-5555-7000-8000-000000000001";
describe("typed command form values", () => {
  it("rejects unknown members, missing reason, and forged proof input", () => {
    const input = {
      type: "create_node",
      commandId: id,
      reason: "Desired",
      id,
      kind: "dto",
      name: "DTO",
      parentId: null,
      attributes: { qualifiedName: "edu.DTO", analysisStatus: "complete", gaps: [] },
    };
    expect(parseChangeCommand(input)).toEqual(input);
    expect(() => parseChangeCommand({ ...input, extra: true })).toThrow();
    expect(() => parseChangeCommand({ ...input, constructor: "untyped" })).toThrow();
    expect(() => parseChangeCommand({ ...input, reason: "" })).toThrow();
    expect(() =>
      parseChangeCommand({ ...input, attributes: { ...input.attributes, evidenceIds: [] } }),
    ).toThrow();
  });
  it("distinguishes omitted expected value, explicit null and a typed value", () => {
    const schema = backendChangeSchemas.BackendChangeExpectedValue!;
    expect(changeSchemaIssues(schema, { present: false })).toEqual([]);
    expect(changeSchemaIssues(schema, { present: true, value: null })).toEqual([]);
    expect(changeSchemaIssues(schema, { present: false, value: null })).not.toEqual([]);
    expect(changeSchemaIssues(schema, { present: true })).not.toEqual([]);
  });
  it("creates complete required union defaults with bounded collections", () => {
    expect(changeSchemaDefault(backendChangeSchemas.BackendChangeExpectedValue!)).toEqual({
      present: false,
    });
    expect(
      changeSchemaDefault({
        type: "array",
        minItems: 2,
        maxItems: 3,
        items: { type: "string", const: "one" },
      }),
    ).toEqual(["one", "one"]);
  });
});

it("materializes conditional FK fields and removes them when the kind changes", () => {
  const schema = backendChangeSchemas.BackendChangeConstraintDefinition!;
  const value = completeChangeObject(schema, { constraintKind: "foreign_key" });
  expect(value.reference).toEqual(
    expect.objectContaining({ id: "", targetTableId: "", columnPairs: expect.any(Array) }),
  );
  expect(completeChangeObject(schema, { ...value, constraintKind: "unique" })).not.toHaveProperty(
    "reference",
  );
});

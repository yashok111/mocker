import { describe, expect, it } from "vitest";
import { formCommands, makeForm, objectCompatible } from "./form";

describe("schema form commands", () => {
  it("retains unknown field keywords while applying constraints, examples and rename", () => {
    const form = makeForm({ schema: "Order", property: "id" });
    form.name = "key";
    form.json = '{"type":"integer","x-private":{"a":true},"deprecated":true}';
    form.required = true;
    form.edits = { minimum: "1", maximum: "100", example: "12", enum: "[1,12,100]" };
    const commands = formCommands(form);
    expect(commands).toHaveLength(2);
    expect(commands[0]).toMatchObject({
      kind: "upsert_property",
      propertyName: "id",
      required: true,
    });
    expect(JSON.parse(commands[0]!.schemaJSON!)).toEqual({
      type: "integer",
      "x-private": { a: true },
      deprecated: true,
      minimum: 1,
      maximum: 100,
      example: 12,
      enum: [1, 12, 100],
    });
    expect(commands[1]).toEqual({
      kind: "rename_property",
      schemaName: "Order",
      propertyName: "id",
      newName: "key",
    });
  });
  it("replaces before renaming so the server rewrites recursive references", () => {
    const form = makeForm({ schema: "User" });
    form.name = "Customer";
    form.json = '{"properties":{"parent":{"$ref":"#/components/schemas/User"}}}';
    expect(formCommands(form).map((c) => c.kind)).toEqual([
      "replace_schema",
      "rename_schema",
      "move_schema",
    ]);
  });
  it("binds array references through the canonical command", () => {
    const form = makeForm({ schema: "Order", property: "items", create: true });
    form.name = "items";
    form.target = "OrderItem";
    form.referenceChanged = true;
    form.array = true;
    expect(formCommands(form).at(-1)).toEqual({
      kind: "set_reference",
      schemaName: "Order",
      propertyName: "items",
      targetSchema: "OrderItem",
      array: true,
    });
  });
  it("initializes existing references so either array-mode toggle emits a command", () => {
    for (const array of [false, true]) {
      const json = JSON.stringify(
        array
          ? { type: "array", items: { $ref: "#/components/schemas/User" } }
          : { $ref: "#/components/schemas/User" },
      );
      const form = makeForm(
        { schema: "Order", property: "user" },
        {
          name: "Order",
          pointer: "",
          description: "",
          schemaJSON: "{}",
          type: "object",
          x: 0,
          y: 0,
          properties: [
            {
              name: "user",
              pointer: "",
              type: array ? "array" : "reference",
              required: false,
              schemaJSON: json,
            },
          ],
        },
      );
      expect(form.target).toBe("User");
      expect(form.array).toBe(array);
      form.array = !array;
      form.referenceChanged = true;
      expect(formCommands(form).at(-1)).toEqual({
        kind: "set_reference",
        schemaName: "Order",
        propertyName: "user",
        targetSchema: "User",
        array: !array,
      });
    }
  });
  it("preserves type narrowing for unrelated edits and decodes component names", () => {
    const schemaJSON =
      '{"$ref":"#/components/schemas/Percent%25%20name","type":"integer","minimum":1}';
    const form = makeForm(
      { schema: "Order", property: "value" },
      {
        name: "Order",
        pointer: "",
        description: "",
        schemaJSON: "{}",
        type: "object",
        x: 0,
        y: 0,
        properties: [{ name: "value", pointer: "", type: "integer", required: false, schemaJSON }],
      },
    );
    expect(form.target).toBe("Percent% name");
    form.required = true;
    const commands = formCommands(form);
    expect(commands).toHaveLength(1);
    expect(commands[0]).toMatchObject({ kind: "upsert_property", schemaJSON, required: true });
  });
  it("preserves suffix references during unrelated edits", () => {
    const form = makeForm(
      { schema: "Order", property: "user" },
      {
        name: "Order",
        pointer: "",
        description: "",
        schemaJSON: "{}",
        type: "object",
        x: 0,
        y: 0,
        properties: [
          {
            name: "user",
            pointer: "",
            type: "reference",
            required: false,
            schemaJSON: '{"$ref":"#/components/schemas/User/properties/id"}',
          },
        ],
      },
    );
    form.edits = { description: "Identifier" };
    expect(form.target).toBe("");
    expect(formCommands(form)).toHaveLength(1);
    expect(JSON.parse(formCommands(form)[0]!.schemaJSON!).$ref).toBe(
      "#/components/schemas/User/properties/id",
    );
  });
  it("allows deliberate raw boolean schemas and rejects destructive structured edits", () => {
    const form = makeForm({ schema: "Boolean" });
    form.json = "false";
    expect(formCommands(form)[0]!.schemaJSON).toBe("false");
    expect(objectCompatible("false")).toBe(false);
    expect(objectCompatible('{"$ref":"#/components/schemas/Other"}')).toBe(false);
    form.edits = { type: "object" };
    expect(() => formCommands(form)).toThrow(/Boolean/);
  });
  it("rejects invalid JSON, unsafe numbers and invalid constraints without losing input", () => {
    const form = makeForm({ schema: "A", property: "id" });
    form.json = '{"example":9007199254740993}';
    expect(() => formCommands(form)).toThrow(/Число/);
    form.json = "{}";
    for (const edits of [
      { enum: "{}" },
      { minLength: "-1" },
      { minimum: "0x20" },
      { example: "{" },
    ] as Record<string, string>[]) {
      form.edits = edits;
      expect(() => formCommands(form)).toThrow();
    }
  });
});

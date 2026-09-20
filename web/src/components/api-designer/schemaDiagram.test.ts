import { describe, expect, it } from "vitest";
import mermaid from "mermaid";
import type { ApiDocument } from "./documentModel";
import { buildSchemaDiagram } from "./schemaDiagramModel";

mermaid.initialize({ startOnLoad: false, securityLevel: "strict" });

function documentWith(schemas: Record<string, unknown>): ApiDocument {
  return { openapi: "3.1.0", components: { schemas } };
}

const orders = documentWith({
  Order: {
    type: "object",
    required: ["id", "items"],
    properties: {
      id: { type: "string", format: "uuid" },
      customer: { $ref: "#/components/schemas/User" },
      items: { type: "array", items: { $ref: "#/components/schemas/Item" } },
      shipping: { type: "object", properties: { city: { type: "string" } } },
    },
  },
  User: { properties: { address: { $ref: "#/components/schemas/Address" } } },
  Item: { properties: { quantity: { type: "integer" } } },
  Address: { type: "string" },
  Unrelated: { type: "boolean" },
});

describe("buildSchemaDiagram", () => {
  it("selects outgoing references by depth and retains optional and array fields", async () => {
    const result = buildSchemaDiagram(orders, { schema: "Order", depth: 1 });
    expect(result.schemaCount).toBe(3);
    expect(result.relationCount).toBe(2);
    expect(result.source).toContain('class s0["Item"]');
    expect(result.source).toContain('class s1["Order"]');
    expect(result.source).toContain('class s2["User"]');
    expect(result.source).not.toContain("class s3");
    expect(result.source).toContain("string / uuid id");
    expect(result.source).toContain("User customer?");
    expect(result.source).toContain("Item[] items");
    expect(result.source).toContain("string shipping.city?");
    expect(result.source).toContain("s1 --> s0 : items[]");
    expect(result.source).toContain("s1 --> s2 : customer");
    expect(await mermaid.parse(result.source)).toMatchObject({ diagramType: "classDiagram" });
    expect(buildSchemaDiagram(orders, { schema: "Order", depth: 0 }).schemaCount).toBe(1);
    expect(buildSchemaDiagram(orders, { schema: "Order", depth: 2 }).schemaCount).toBe(4);
    expect(buildSchemaDiagram(orders, {}).schemaCount).toBe(5);
  });

  it("keeps recursive and mutual references as edges without duplicating schemas", async () => {
    const result = buildSchemaDiagram(
      documentWith({
        Node: {
          properties: {
            children: { type: "array", items: { $ref: "#/components/schemas/Node" } },
            parent: { $ref: "#/components/schemas/Parent" },
          },
        },
        Parent: { $ref: "#/components/schemas/Node" },
      }),
      { schema: "Node", depth: 3 },
    );
    expect(result.schemaCount).toBe(2);
    expect(result.relationCount).toBe(3);
    expect(result.source).toContain("s0 --> s0 : children[]");
    await expect(mermaid.parse(result.source)).resolves.toBeTruthy();
  });

  it("labels composition without inventing inheritance and ignores refs inside examples", () => {
    const result = buildSchemaDiagram(
      documentWith({
        Choice: {
          oneOf: [{ $ref: "#/components/schemas/A" }, { type: "boolean" }],
          allOf: [{ $ref: "#/components/schemas/B" }],
          anyOf: [{ $ref: "#/components/schemas/A" }],
          example: { $ref: "#/components/schemas/ExampleOnly" },
          default: { $ref: "#/components/schemas/ExampleOnly" },
          "x-data": { $ref: "#/components/schemas/ExampleOnly" },
        },
        A: { type: "string", enum: ["yes", "no"] },
        B: { type: "object" },
        ExampleOnly: {},
      }),
      { schema: "Choice", depth: 1 },
    );
    expect(result.schemaCount).toBe(3);
    expect(result.relationCount).toBe(3);
    expect(result.source).toContain("oneOf[1]");
    expect(result.source).toContain("allOf[1]");
    expect(result.source).toContain("anyOf[1]");
    expect(result.source).not.toContain("<|--");
    expect(result.source).not.toContain("ExampleOnly");
    expect(result.source).toContain("enum");
  });

  it("decodes escaped and percent-encoded JSON pointers, including subtree references", () => {
    const result = buildSchemaDiagram(
      documentWith({
        Root: {
          properties: {
            part: { $ref: "#/components/schemas/A~1B~0C/properties/value" },
            other: { $ref: "#/components/schemas/With%20Space" },
          },
        },
        "A/B~C": { properties: { value: { type: "number" } } },
        "With Space": true,
      }),
      { schema: "Root", depth: 1 },
    );
    expect(result.schemaCount).toBe(3);
    expect(result.relationCount).toBe(2);
    expect(result.warnings).toEqual([]);
    expect(result.source).toContain("properties/value");
  });

  it("reports unresolved and external refs instead of drawing invented targets", () => {
    const result = buildSchemaDiagram(
      documentWith({
        Root: {
          properties: {
            missing: { $ref: "#/components/schemas/Missing" },
            subtree: { $ref: "#/components/schemas/Root/properties/absent" },
            external: { $ref: "https://example.com/other.json#/Thing" },
          },
        },
      }),
      { schema: "Root", depth: 1 },
    );
    expect(result.schemaCount).toBe(1);
    expect(result.relationCount).toBe(0);
    expect(result.warnings.join(" ")).toContain("Missing");
    expect(result.warnings.join(" ")).toContain("absent");
    expect(result.warnings.join(" ")).toContain("https://example.com");
  });

  it.each(["$defs", "definitions"])("includes references inside the %s schema map", (keyword) => {
    const result = buildSchemaDiagram(
      documentWith({
        Root: {
          properties: { current: { $ref: `#/components/schemas/Root/${keyword}/Embedded` } },
          [keyword]: { Embedded: { $ref: "#/components/schemas/Leaf" } },
        },
        Leaf: { type: "string" },
      }),
      { schema: "Root", depth: 2 },
    );
    expect(result.schemaCount).toBe(2);
    expect(result.relationCount).toBe(2);
    expect(result.source).toContain('class s0["Leaf"]');
    expect(result.warnings).toEqual([]);
  });

  it("keeps hostile labels inside text and produces parseable Mermaid", async () => {
    const name = 'A"}\nclick evil callback\n<script>alert(1)</script>%%';
    const result = buildSchemaDiagram(
      documentWith({
        [name]: { properties: { ['x(){}<>";`#%\\\ny']: { type: "string" } } },
      }),
      {},
    );
    expect(result.schemaCount).toBe(1);
    expect(result.source).not.toMatch(/^\s*(click|callback|style|%%)/m);
    expect(result.source).not.toContain("<script>");
    await expect(mermaid.parse(result.source)).resolves.toBeTruthy();
  });

  it("is deterministic without changing the input document", () => {
    const before = JSON.stringify(orders);
    const reversed = documentWith(
      Object.fromEntries(
        Object.entries(
          (orders.components as { schemas: Record<string, unknown> }).schemas,
        ).reverse(),
      ),
    );
    expect(buildSchemaDiagram(reversed, {})).toEqual(buildSchemaDiagram(orders, {}));
    expect(JSON.stringify(orders)).toBe(before);
  });

  it("bounds a large graph and tells the reader it is truncated", () => {
    const schemas = Object.fromEntries(
      Array.from({ length: 80 }, (_, i) => [
        `Schema${i}`,
        {
          properties: Object.fromEntries(
            Array.from({ length: 80 }, (_, j) => [`field${j}`, { type: "string" }]),
          ),
        },
      ]),
    );
    const result = buildSchemaDiagram(documentWith(schemas), {});
    expect(result.schemaCount).toBeGreaterThan(0);
    expect(result.schemaCount).toBeLessThan(80);
    expect(result.source.length).toBeLessThan(50_000);
    expect(result.warnings.join(" ")).toMatch(/огранич|показан|сокращ/);
  });

  it("has explicit empty output for missing schemas and supports boolean schemas", async () => {
    expect(buildSchemaDiagram({}, {}).source).toBe("");
    expect(buildSchemaDiagram(orders, { schema: "Deleted" }).schemaCount).toBe(0);
    const result = buildSchemaDiagram(documentWith({ Any: true, Never: false }), {});
    expect(result.schemaCount).toBe(2);
    await expect(mermaid.parse(result.source)).resolves.toBeTruthy();
  });

  it("keeps long escaped labels under Mermaid's input limit", async () => {
    const schemas = Object.fromEntries(
      Array.from({ length: 40 }, (_, i) => [
        `Schema${i}`,
        {
          properties: Object.fromEntries(
            Array.from({ length: 24 }, (_, j) => [`${j}${'"{}<>'.repeat(30)}`, { type: "string" }]),
          ),
        },
      ]),
    );
    const result = buildSchemaDiagram(documentWith(schemas));
    expect(result.source.length).toBeLessThan(50_000);
    expect(result.warnings.length).toBeGreaterThan(0);
    await expect(mermaid.parse(result.source)).resolves.toBeTruthy();
  });
});

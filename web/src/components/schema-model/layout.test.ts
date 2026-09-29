import { expect, it } from "vitest";
import type { SchemaModel } from "@/api/generated/schemas";
import { layoutDiagram } from "../diagram/elkLayout";
import { schemaLayoutInput } from "./layout";

it("preserves named field ports and root references without making saved coordinates part of layout input", async () => {
  const model: SchemaModel = {
    schemas: [
      {
        name: "Order",
        type: "object",
        description: "",
        pointer: "",
        schemaJSON: "{}",
        x: 900,
        y: 400,
        properties: [
          { name: "customer", type: "object", required: false, pointer: "", schemaJSON: "{}" },
        ],
      },
      {
        name: "Customer",
        type: "object",
        description: "",
        pointer: "",
        schemaJSON: "{}",
        x: -200,
        y: 60,
        properties: [],
      },
    ],
    references: [
      {
        sourceSchema: "Order",
        sourceProperty: "customer",
        targetSchema: "Customer",
        pointer: "",
        ref: "",
      },
      { sourceSchema: "Customer", targetSchema: "Order", pointer: "", ref: "" },
    ],
    operations: [],
  };
  const input = schemaLayoutInput(model);
  const original = structuredClone(model);
  expect(input.edges).toEqual([
    {
      id: "ref:0",
      source: "schema:0",
      target: "schema:1",
      sourcePort: "property:customer",
      targetPort: "target",
    },
    {
      id: "ref:1",
      source: "schema:1",
      target: "schema:0",
      sourcePort: "source",
      targetPort: "target",
    },
  ]);
  const result = await layoutDiagram(input);
  const order = result.nodes.find((node) => node.id === "schema:0")!;
  expect(result.edges[0]!.points[0]).toEqual({ x: order.x + 280, y: order.y + 59 });
  expect(model).toEqual(original);
  model.schemas[0]!.x = -500;
  expect(schemaLayoutInput(model)).toEqual(input);
});

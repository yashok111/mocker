// @vitest-environment node
import { expect, it } from "vitest";
import type { SchemaModel, SchemaModelSchema } from "@/api/generated/schemas";
import { referenceVertices } from "./projection";
it("routes Order references through distinct lanes outside all three cards", () => {
  const schema = (name: string, x: number, y: number): SchemaModelSchema => ({
    name,
    x,
    y,
    pointer: "",
    schemaJSON: "{}",
    description: "",
    type: "object",
    properties: [],
  });
  const order = schema("Order", 60, 100);
  order.properties = ["items", "user"].map((name) => ({
    name,
    pointer: "",
    schemaJSON: "{}",
    type: "object",
    required: false,
  }));
  const model: SchemaModel = {
    schemas: [order, schema("User", 480, 60), schema("OrderItem", 480, 360)],
    operations: [],
    references: [
      {
        sourceSchema: "Order",
        sourceProperty: "items",
        targetSchema: "OrderItem",
        pointer: "",
        ref: "",
      },
      { sourceSchema: "Order", sourceProperty: "user", targetSchema: "User", pointer: "", ref: "" },
    ],
  };
  const lanes = model.references.map((ref) => referenceVertices(model, ref));
  expect(lanes[0]![0]!.x).not.toBe(lanes[1]![0]!.x);
  for (const vertices of lanes)
    for (const point of vertices) {
      expect(point.x).toBeGreaterThan(340);
      expect(point.x).toBeLessThan(480);
    }
  // A card placed across the first leg must trigger obstacle routing instead
  // of drawing the explicit route through the card.
  model.schemas.push(schema("Obstacle", 355, 130));
  expect(referenceVertices(model, model.references[0]!)).toEqual([]);
  // Reversed layouts and self references also need obstacle routing.
  model.schemas[1]!.x = -300;
  expect(referenceVertices(model, model.references[1]!)).toEqual([]);
  expect(referenceVertices(model, { ...model.references[0]!, targetSchema: "Order" })).toEqual([]);
});

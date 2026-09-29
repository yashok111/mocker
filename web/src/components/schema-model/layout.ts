import type { SchemaModel } from "@/api/generated/schemas";
import type { DiagramLayoutInput } from "../diagram/elkLayout";
import { CARD_WIDTH, CARD_HEADER, FIELD_ROW } from "./projection";

export function schemaCardHeight(properties: number) {
  return CARD_HEADER + Math.max(1, properties) * FIELD_ROW + 8;
}

export function schemaLayoutInput(model: SchemaModel): DiagramLayoutInput {
  const ids = new Map(model.schemas.map((schema, index) => [schema.name, `schema:${index}`]));
  return {
    nodes: model.schemas.map((schema) => ({
      id: ids.get(schema.name)!,
      width: CARD_WIDTH,
      height: schemaCardHeight(schema.properties.length),
      ports: [
        { id: "target", x: 0, y: 23, side: "WEST" },
        { id: "source", x: CARD_WIDTH, y: 23, side: "EAST" },
        ...schema.properties.map((property, index) => ({
          id: `property:${property.name}`,
          x: CARD_WIDTH,
          y: CARD_HEADER + index * FIELD_ROW + 13,
          side: "EAST" as const,
        })),
      ],
    })),
    edges: model.references.flatMap((reference, index) => {
      const source = ids.get(reference.sourceSchema),
        target = ids.get(reference.targetSchema);
      if (!source || !target) return [];
      const field = model.schemas
        .find((schema) => schema.name === reference.sourceSchema)
        ?.properties.some((property) => property.name === reference.sourceProperty);
      return [
        {
          id: `ref:${index}`,
          source,
          target,
          sourcePort: field ? `property:${reference.sourceProperty}` : "source",
          targetPort: "target",
        },
      ];
    }),
  };
}

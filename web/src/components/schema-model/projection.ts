import type { SchemaModel, SchemaModelReference } from "@/api/generated/schemas";

export const CARD_WIDTH = 280;
export const CARD_HEADER = 46;
export const FIELD_ROW = 30;

// Distinct lanes keep adjacent references legible instead of merging them into a
// single bus. Outside a clear left-to-right gap the obstacle router chooses a path.
export function referenceVertices(model: SchemaModel, ref: SchemaModelReference) {
  const source = model.schemas.find((s) => s.name === ref.sourceSchema);
  const target = model.schemas.find((s) => s.name === ref.targetSchema);
  if (!source || !target) return [];
  const fieldIndex = source.properties.findIndex((p) => p.name === ref.sourceProperty);
  const gap = target.x - (source.x + CARD_WIDTH);
  if (fieldIndex < 0 || gap < 80) return [];
  const siblings = model.references.filter((r) => r.sourceSchema === ref.sourceSchema);
  const lane =
    source.x + CARD_WIDTH + 24 + ((gap - 48) * (siblings.indexOf(ref) + 1)) / (siblings.length + 1);
  const vertices = [
    { x: lane, y: source.y + CARD_HEADER + fieldIndex * FIELD_ROW + 13 },
    { x: lane, y: target.y + 23 },
  ];
  const points = [
    { x: source.x + CARD_WIDTH, y: vertices[0]!.y },
    ...vertices,
    { x: target.x, y: vertices[1]!.y },
  ];
  // Only use the explicit orthogonal route when every segment is clear.
  // Otherwise let the obstacle router find the entire path, without waypoints.
  const blocked = model.schemas.some((schema) => {
    if (schema === source || schema === target) return false;
    const left = schema.x - 12,
      right = schema.x + CARD_WIDTH + 12;
    const top = schema.y - 12;
    const bottom = schema.y + CARD_HEADER + Math.max(1, schema.properties.length) * FIELD_ROW + 20;
    return points.slice(1).some((b, index) => {
      const a = points[index]!;
      return (
        Math.max(a.x, b.x) >= left &&
        Math.min(a.x, b.x) <= right &&
        Math.max(a.y, b.y) >= top &&
        Math.min(a.y, b.y) <= bottom
      );
    });
  });
  return blocked ? [] : vertices;
}

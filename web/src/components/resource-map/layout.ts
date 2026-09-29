import type { DiagramLayoutInput } from "../diagram/elkLayout";
import { measureDiagramLabel } from "../diagram/elkX6";
import type { Model, Resource } from "./model";

export const RESOURCE_CARD_WIDTH = 220;
export function resourceCardHeight(model: Model, resource: Resource) {
  const operations = model.operations.filter((operation) =>
    resource.operationKeys.includes(operation.key),
  );
  const rows = Math.max(1, Math.min(4, operations.length)) + (operations.length > 4 ? 1 : 0);
  return 76 + rows * 24;
}

export function resourceLayoutInput(model: Model): DiagramLayoutInput {
  const ids = new Set(model.resources.map((resource) => resource.id));
  return {
    nodes: model.resources.map((resource) => ({
      id: `resource:${resource.id}`,
      width: RESOURCE_CARD_WIDTH,
      height: resourceCardHeight(model, resource),
    })),
    edges: model.relations
      .filter((relation) => ids.has(relation.fromResourceId) && ids.has(relation.toResourceId))
      .map((relation) => ({
        id: `relation:${relation.id}`,
        source: `resource:${relation.fromResourceId}`,
        target: `resource:${relation.toResourceId}`,
        ...(relation.label ? { label: measureDiagramLabel(relation.label, 160, 3) } : {}),
      })),
  };
}

import type { DiagramLayoutInput } from "../diagram/elkLayout";
import { measureDiagramLabel } from "../diagram/elkX6";
import type { ImpactGraphModel } from "./model";

export function impactLayoutInput(model: ImpactGraphModel): DiagramLayoutInput {
  return {
    nodes: model.nodes.map(({ id, width, height }) => ({ id, width, height })),
    edges: model.edges.map(({ id, source, target, label }) => ({
      id,
      source,
      target,
      ...(label ? { label: measureDiagramLabel(label, 160, 3) } : {}),
    })),
  };
}

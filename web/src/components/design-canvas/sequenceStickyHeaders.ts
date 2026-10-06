import type { Graph } from "@antv/x6";
import type { SequenceLayout } from "./sequenceLayout";
import { cellId } from "./sequenceProjection";

export function stickyParticipantY(graph: Graph, originalY: number): number {
  // Keep the inset in screen pixels so zoom does not change the top clearance.
  const inset = 12;
  return Math.max(originalY, (inset - graph.translate().ty) / graph.zoom());
}

export function positionSequenceHeaders(
  graph: Graph,
  layout: SequenceLayout,
  restore = false,
): void {
  for (const participant of layout.participants) {
    const id = cellId("participant", participant.id);
    const node = graph.getCellById(id);
    if (!node?.isNode()) continue;
    const position = node.getPosition();
    const y = restore ? participant.header.y : stickyParticipantY(graph, participant.header.y);
    const delta = y - position.y;
    if (delta === 0) continue;
    node.setPosition(position.x, y);
    const highlight = graph.getCellById(`highlight:${id}`);
    if (highlight?.isNode()) {
      const outline = highlight.getPosition();
      highlight.setPosition(outline.x, outline.y + delta);
    }
  }
}

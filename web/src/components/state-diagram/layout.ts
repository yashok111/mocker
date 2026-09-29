import type { DiagramLayoutInput, DiagramLayoutResult } from "../diagram/elkLayout";
import { measureDiagramLabel } from "../diagram/elkX6";
import type { StateDiagram, StateDiagramTransition } from "./model";

export const STATE_WIDTH = 190;
export const STATE_HEIGHT = 76;

export function transitionLabel(transition: StateDiagramTransition): string {
  return (
    transition.name +
    (transition.binding
      ? `\n${transition.binding.method.toUpperCase()} ${transition.binding.path}`
      : "") +
    (transition.guard ? "\n[условие]" : "")
  );
}

export function stateLayoutInput(diagram?: StateDiagram): DiagramLayoutInput {
  const ids = new Set(diagram?.states.map((state) => state.id));
  return {
    direction: "RIGHT",
    nodes: (diagram?.states ?? []).map((state) => ({
      id: `state:${state.id}`,
      width: STATE_WIDTH,
      height: STATE_HEIGHT,
      ports: [
        { id: "left", x: 0, y: STATE_HEIGHT / 2, side: "WEST" },
        { id: "right", x: STATE_WIDTH, y: STATE_HEIGHT / 2, side: "EAST" },
      ],
    })),
    edges: (diagram?.transitions ?? [])
      .filter((transition) => ids.has(transition.from) && ids.has(transition.to))
      .map((transition) => ({
        id: `transition:${transition.id}`,
        source: `state:${transition.from}`,
        target: `state:${transition.to}`,
        sourcePort: "right",
        targetPort: "left",
        label: measureDiagramLabel(transitionLabel(transition)),
      })),
  };
}

export function applyStateLayout(diagram: StateDiagram, layout: DiagramLayoutResult): StateDiagram {
  const positions = new Map(layout.nodes.map((node) => [node.id, node]));
  return {
    ...diagram,
    states: diagram.states.map((state) => {
      const position = positions.get(`state:${state.id}`);
      return position ? { ...state, x: Math.round(position.x), y: Math.round(position.y) } : state;
    }),
  };
}

import { cleanup, render } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import StateGraph from "./StateGraph";
import { orderTemplate } from "./model";
import { STATE_HEIGHT, STATE_WIDTH } from "./layout";

const graph = vi.hoisted(() => ({
  on: vi.fn(),
  addNode: vi.fn(),
  addEdge: vi.fn(),
  clearCells: vi.fn(),
  getNodes: vi.fn(() => [{}]),
  getEdges: vi.fn(() => [{}]),
  zoomToFit: vi.fn(),
  dispose: vi.fn(),
}));
vi.mock("@antv/x6", () => ({
  Graph: class {
    constructor() {
      return graph;
    }
  },
}));
beforeEach(() => {
  vi.clearAllMocks();
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(800);
  vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(460);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

it("keeps the live dragged state position when a layout result arrives", () => {
  const diagram = orderTemplate();
  const callbacks = { onSelect: vi.fn(), onMove: vi.fn(), onConnect: vi.fn() };
  const view = render(
    <MantineProvider>
      <StateGraph diagram={diagram} selection={null} {...callbacks} />
    </MantineProvider>,
  );
  const nodes = diagram.states.map((state) => ({
    id: `state:${state.id}`,
    x: state.x,
    y: state.y,
    width: STATE_WIDTH,
    height: STATE_HEIGHT,
  }));
  graph.getNodes.mockReturnValue(
    nodes.map((node, index) => ({
      id: node.id,
      getBBox: () => ({ ...node, x: node.x + (index === 0 ? 99 : 0) }),
    })),
  );
  graph.getEdges.mockReturnValue(diagram.transitions);
  view.rerender(
    <MantineProvider>
      <StateGraph
        diagram={diagram}
        selection={null}
        {...callbacks}
        layout={{
          nodes,
          edges: diagram.transitions.map((edge) => ({
            id: `transition:${edge.id}`,
            source: `state:${edge.from}`,
            target: `state:${edge.to}`,
            points: [],
          })),
        }}
      />
    </MantineProvider>,
  );
  expect(graph.clearCells).toHaveBeenCalledOnce();
  expect(graph.addNode).toHaveBeenCalledTimes(diagram.states.length);
  expect(callbacks.onMove).not.toHaveBeenCalled();
});

import { cleanup, render } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import ResponseRuleGraph from "./ResponseRuleGraph";
import { headerTemplate } from "./model";

const graph = vi.hoisted(() => ({
  on: vi.fn(),
  addNode: vi.fn(),
  addEdge: vi.fn(),
  clearCells: vi.fn(),
  getNodes: vi.fn(() => [{}]),
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

describe("controlled response graph", () => {
  it("namespaces equal node and edge identities so both render", () => {
    const rule = headerTemplate();
    rule.edges[0]!.id = rule.nodes[0]!.id;
    render(
      <MantineProvider>
        <ResponseRuleGraph
          rule={rule}
          selection={null}
          onSelect={() => {}}
          onMove={() => {}}
          onConnect={() => {}}
        />
      </MantineProvider>,
    );
    expect(graph.addNode.mock.calls.some(([node]) => node.id === "node:start")).toBe(true);
    expect(graph.addEdge.mock.calls.some(([edge]) => edge.id === "edge:start")).toBe(true);
    expect(graph.addNode).toHaveBeenCalledTimes(5);
    expect(graph.addEdge).toHaveBeenCalledTimes(4);
  });
  it("fits once per rule while preserving manual viewport on trace, selection and resize", () => {
    const rule = headerTemplate();
    const callbacks = { onSelect: vi.fn(), onMove: vi.fn(), onConnect: vi.fn() };
    const view = render(
      <MantineProvider>
        <ResponseRuleGraph rule={rule} selection={null} {...callbacks} />
      </MantineProvider>,
    );
    expect(graph.zoomToFit).toHaveBeenCalledTimes(1);
    view.rerender(
      <MantineProvider>
        <ResponseRuleGraph
          rule={{ ...rule }}
          selection={{ kind: "node", id: "auth" }}
          trace={[{ step: 1, nodeId: "start", edgeId: "e1" }]}
          {...callbacks}
        />
      </MantineProvider>,
    );
    const resize = graph.on.mock.calls.find(([name]) => name === "resize")![1];
    resize();
    expect(graph.zoomToFit).toHaveBeenCalledTimes(1);
    view.rerender(
      <MantineProvider>
        <ResponseRuleGraph rule={{ ...rule, id: "another" }} selection={null} {...callbacks} />
      </MantineProvider>,
    );
    expect(graph.zoomToFit).toHaveBeenCalledTimes(2);
    view.unmount();
    expect(graph.dispose).toHaveBeenCalledOnce();
  });
});

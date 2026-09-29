import { act, render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import type { ReactNode } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import type { DesignScenarioEventMapReport } from "@/api/generated/schemas";
import EventMapGraph from "./EventMapGraph";
import type { DiagramLayoutResult } from "../diagram/elkLayout";

const boundary = vi.hoisted(() => ({
  layout: vi.fn(),
  nodes: new Map<string, { attr: ReturnType<typeof vi.fn> }>(),
  clear: vi.fn(),
  dispose: vi.fn(),
}));

vi.mock("../diagram/elkLayout", () => ({ layoutDiagram: boundary.layout }));
vi.mock("@antv/x6", () => ({
  Graph: class {
    clearCells() {
      boundary.clear();
      boundary.nodes.clear();
    }
    addNode(node: { id: string }) {
      boundary.nodes.set(node.id, { attr: vi.fn() });
    }
    getCellById(id: string) {
      return boundary.nodes.get(id);
    }
    getNodes() {
      return [...boundary.nodes.values()];
    }
    on() {}
    dispose() {
      boundary.dispose();
    }
  },
}));

const report: DesignScenarioEventMapReport = {
  scenarioId: 1,
  revisionId: 1,
  version: 1,
  proposed: false,
  complete: true,
  nodes: [],
  edges: [],
  diagnostics: [],
  coverage: { nodesReturned: 1, edgesReturned: 0, diagnosticsReturned: 0, truncatedReasons: [] },
};

function subset(id: string) {
  return {
    nodes: [
      { id, kind: "channel" as const, label: id, locator: { pointer: "/eventModel/channels/0" } },
    ],
    edges: [],
  };
}
function result(id: string): DiagramLayoutResult {
  return { nodes: [{ id, x: 10, y: 10, width: 240, height: 64 }], edges: [] };
}
function pending() {
  let resolve!: (result: DiagramLayoutResult) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<DiagramLayoutResult>((success, failure) => {
    resolve = success;
    reject = failure;
  });
  return { promise, resolve, reject };
}
function renderGraph(ui: ReactNode) {
  return render(ui, {
    wrapper: ({ children }) => <MantineProvider env="test">{children}</MantineProvider>,
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  boundary.layout.mockReset();
  boundary.nodes.clear();
});

it("keeps the new map when an older layout finishes later and retains selection", async () => {
  const old = pending(),
    current = pending();
  boundary.layout.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise);
  const view = renderGraph(
    <EventMapGraph report={report} subset={subset("old")} selectedId="" onSelect={vi.fn()} />,
  );
  view.rerender(
    <EventMapGraph
      report={report}
      subset={subset("current")}
      selectedId="current"
      onSelect={vi.fn()}
    />,
  );
  await act(async () => current.resolve(result("current")));
  expect(screen.queryByRole("status")).not.toBeInTheDocument();
  expect([...boundary.nodes.keys()]).toEqual(["current"]);
  expect(boundary.nodes.get("current")!.attr).toHaveBeenLastCalledWith(
    "body",
    expect.objectContaining({ strokeWidth: 2 }),
  );
  await act(async () => old.resolve(result("old")));
  expect([...boundary.nodes.keys()]).toEqual(["current"]);
});

it("does not draw a layout after closing the map", async () => {
  const task = pending();
  boundary.layout.mockReturnValueOnce(task.promise);
  const view = renderGraph(
    <EventMapGraph report={report} subset={subset("closed")} selectedId="" onSelect={vi.fn()} />,
  );
  view.unmount();
  const clears = boundary.clear.mock.calls.length;
  await act(async () => task.resolve(result("closed")));
  expect(boundary.nodes.size).toBe(0);
  expect(boundary.clear).toHaveBeenCalledTimes(clears);
  expect(boundary.dispose).toHaveBeenCalledOnce();
});

it("shows layout failure without silently drawing straight fallback connections", async () => {
  const task = pending();
  boundary.layout.mockReturnValueOnce(task.promise);
  renderGraph(
    <EventMapGraph report={report} subset={subset("failed")} selectedId="" onSelect={vi.fn()} />,
  );
  await act(async () => task.reject(new Error("layout failed")));
  expect(screen.getByRole("alert")).toHaveTextContent("Узлы и связи доступны в полном списке");
  expect(screen.queryByRole("status")).not.toBeInTheDocument();
  expect(boundary.nodes.size).toBe(0);
});

import { afterEach, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import type { BackendDatabaseTableItem } from "@/api/generated/schemas";
import type { DiagramLayoutInput, DiagramLayoutResult } from "../diagram/elkLayout";
import { BackendDatabaseGraph } from "./BackendDatabaseGraph";

const state = vi.hoisted(() => ({
  pending: [] as { input: DiagramLayoutInput; resolve: (layout: DiagramLayoutResult) => void }[],
  graphs: [] as {
    options: Record<string, unknown>;
    dispose: ReturnType<typeof vi.fn>;
    addNode: ReturnType<typeof vi.fn>;
  }[],
}));
vi.mock("../diagram/elkLayout", () => ({
  layoutDiagram: (input: DiagramLayoutInput) =>
    new Promise<DiagramLayoutResult>((resolve) => state.pending.push({ input, resolve })),
}));
vi.mock("../diagram/elkX6", () => ({
  applyDiagramRoutes: vi.fn(),
  measureDiagramLabel: (text: string) => ({ text, width: 120, height: 32 }),
}));
vi.mock("../diagram/canvasControls", () => ({
  canvasInteractionOptions: () => ({}),
  installCanvasWheelZoom: () => vi.fn(),
}));
vi.mock("@antv/x6", () => ({
  Graph: class {
    dispose = vi.fn();
    addNode = vi.fn();
    addEdge = vi.fn();
    on = vi.fn();
    getNodes = () => [];
    constructor(options: Record<string, unknown>) {
      state.graphs.push({ options, dispose: this.dispose, addNode: this.addNode });
    }
  },
}));
afterEach(() => {
  state.pending.length = 0;
  state.graphs.length = 0;
  vi.clearAllMocks();
});

const table: BackendDatabaseTableItem = {
  tableId: "same-id",
  schemaId: "schema",
  qualifiedName: "old-caption",
  columnCount: 1,
  facetKeys: ["sql"],
  driftStatus: "unknown",
};
const result = (input: DiagramLayoutInput): DiagramLayoutResult => ({
  nodes: input.nodes.map((node) => ({ ...node, x: 0, y: 0 })),
  edges: [],
});

it("discards late layout from a prior context even when table geometry IDs match, and disposes read-only graph", async () => {
  const view = render(
    <BackendDatabaseGraph
      key="old-context"
      tables={[table]}
      relationships={[]}
      onSelect={vi.fn()}
    />,
    { wrapper: ({ children }) => <MantineProvider env="test">{children}</MantineProvider> },
  );
  expect(screen.getByText("Рассчитываем расположение ER")).toBeInTheDocument();
  const old = state.pending[0];
  view.rerender(
    <BackendDatabaseGraph
      key="new-context"
      tables={[{ ...table, qualifiedName: "new-caption" }]}
      relationships={[]}
      onSelect={vi.fn()}
    />,
  );
  const latest = state.pending[1];
  if (!old || !latest) throw new Error("Expected old and current layout requests");
  await act(async () => latest.resolve(result(latest.input)));
  expect(state.graphs).toHaveLength(1);
  const graph = state.graphs[0];
  if (!graph) throw new Error("Expected current graph instance");
  expect(graph.options.interacting).toBe(false);
  expect(graph.addNode).toHaveBeenCalledWith(
    expect.objectContaining({ label: expect.stringContaining("new-caption") }),
  );
  await act(async () => old.resolve(result(old.input)));
  expect(state.graphs).toHaveLength(1);
  view.unmount();
  expect(graph.dispose).toHaveBeenCalledOnce();
});

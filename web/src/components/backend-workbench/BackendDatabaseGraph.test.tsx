import { afterEach, expect, it, vi } from "vitest";
import { useState } from "react";
import userEvent from "@testing-library/user-event";
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
    events: Record<string, (value: unknown) => void>;
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
    events: Record<string, (value: unknown) => void> = {};
    on = vi.fn((name: string, callback: (value: unknown) => void) => {
      this.events[name] = callback;
    });
    getNodes = () => [];
    constructor(options: Record<string, unknown>) {
      state.graphs.push({
        options,
        dispose: this.dispose,
        addNode: this.addNode,
        events: this.events,
      });
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
  expect(graph.options.interacting).toMatchObject({
    nodeMovable: true,
    edgeMovable: false,
    vertexMovable: false,
    magnetConnectable: false,
  });
  expect(graph.addNode).toHaveBeenCalledWith(
    expect.objectContaining({ label: expect.stringContaining("new-caption") }),
  );
  await act(async () => old.resolve(result(old.input)));
  expect(state.graphs).toHaveLength(1);
  view.unmount();
  expect(graph.dispose).toHaveBeenCalledOnce();
});

it("moves by keyboard and drag, previews/cancels/applies ELK, retains off-page coordinates and undoes independently of groups", async () => {
  function Harness() {
    const [positions, setPositions] = useState([
      { nodeId: "same-id", x: 100, y: 40 },
      { nodeId: "offpage", x: -10, y: 50 },
    ]);
    const [preview, setPreview] = useState(false);
    const [groups, setGroups] = useState<string[]>([]);
    return (
      <>
        <BackendDatabaseGraph
          tables={[table]}
          relationships={[]}
          onSelect={() => {}}
          positions={positions}
          onPositionsChange={setPositions}
          collapsedGroupIds={groups}
          onCollapsedGroupsChange={setGroups}
          onPreview={setPreview}
        />
        <button disabled={preview}>Persist layout</button>
        <button onClick={() => setGroups(["other-schema"])}>Other group</button>
        <output data-testid="coordinates">{JSON.stringify(positions)}</output>
        <output data-testid="groups">{JSON.stringify(groups)}</output>
      </>
    );
  }
  render(<Harness />, {
    wrapper: ({ children }) => <MantineProvider env="test">{children}</MantineProvider>,
  });
  const request = state.pending[0]!;
  await act(async () => request.resolve(result(request.input)));
  expect(state.graphs.at(-1)?.addNode).toHaveBeenCalledWith(
    expect.objectContaining({ x: 100, y: 40 }),
  );
  const move = screen.getByRole("button", { name: "Вправо карточку ER" });
  move.focus();
  await userEvent.keyboard("{Enter}");
  expect(screen.getByTestId("coordinates")).toHaveTextContent('"x":120');
  expect(screen.getByTestId("coordinates")).toHaveTextContent('"nodeId":"offpage"');
  await userEvent.click(screen.getByRole("button", { name: "Предпросмотр автораскладки" }));
  expect(screen.getByRole("button", { name: "Persist layout" })).toBeDisabled();
  expect(state.graphs.at(-1)?.addNode).toHaveBeenCalledWith(
    expect.objectContaining({ x: 0, y: 0 }),
  );
  await userEvent.click(screen.getByRole("button", { name: "Отменить предпросмотр" }));
  expect(screen.getByRole("button", { name: "Persist layout" })).toBeEnabled();
  expect(state.graphs.at(-1)?.addNode).toHaveBeenCalledWith(
    expect.objectContaining({ x: 120, y: 40 }),
  );
  await userEvent.click(screen.getByRole("button", { name: "Предпросмотр автораскладки" }));
  await userEvent.click(screen.getByRole("button", { name: "Применить расположение" }));
  expect(screen.getByTestId("coordinates")).toHaveTextContent(
    '[{"nodeId":"offpage","x":-10,"y":50}]',
  );
  await userEvent.click(screen.getByRole("button", { name: "Other group" }));
  await userEvent.click(screen.getByRole("button", { name: "Отменить расположение" }));
  expect(screen.getByTestId("groups")).toHaveTextContent('["other-schema"]');
  expect(screen.getByTestId("coordinates")).toHaveTextContent('"x":120');
  await act(async () =>
    state.graphs
      .at(-1)
      ?.events["node:moved"]?.({ node: { id: "same-id", position: () => ({ x: 200, y: -40 }) } }),
  );
  expect(screen.getByTestId("coordinates")).toHaveTextContent('"x":200,"y":-40');
  await userEvent.click(screen.getByRole("button", { name: "Отменить расположение" }));
  expect(screen.getByTestId("coordinates")).toHaveTextContent('"x":120,"y":40');
});

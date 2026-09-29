import { MantineProvider } from "@mantine/core";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import ImpactGraph from "./ImpactGraph";
import { impactGraphModel } from "./model";
import { reportFixture } from "./fixtures.test-support";
import * as elk from "../diagram/elkLayout";
import { impactLayoutInput } from "./layout";

const state = vi.hoisted(() => ({
  resize: () => {},
  options: {} as Record<string, unknown>,
  nodes: [] as unknown[],
  edges: [] as Record<string, unknown>[],
  fit: vi.fn(),
  zoom: vi.fn(),
  dispose: vi.fn(),
}));
vi.mock("@antv/x6", () => ({
  Graph: class {
    constructor(options: Record<string, unknown>) {
      state.options = options;
    }
    on(_event: string, fn: () => void) {
      state.resize = fn;
    }
    addNode(node: unknown) {
      state.nodes.push(node);
    }
    addEdge(edge: Record<string, unknown>) {
      state.edges.push(edge);
    }
    getNodes() {
      return state.nodes;
    }
    clearCells() {
      state.nodes = [];
      state.edges = [];
    }
    zoomToFit = state.fit;
    zoom = state.zoom;
    dispose = state.dispose;
  },
}));
afterEach(() => {
  vi.restoreAllMocks();
  vi.clearAllMocks();
});

describe("ImpactGraph lifecycle", () => {
  it("renders the calculated routes and exact label boxes after layout completes", async () => {
    const report = reportFixture();
    const model = impactGraphModel(report, report.changes[0]!);
    const layout = await elk.layoutDiagram(impactLayoutInput(model));
    render(
      <MantineProvider>
        <ImpactGraph model={model} changeId="routes" />
      </MantineProvider>,
    );
    expect(screen.getByText("Располагаем граф влияния…")).toBeInTheDocument();
    await waitFor(() => expect(state.edges).toHaveLength(model.edges.length));
    expect(screen.queryByText("Располагаем граф влияния…")).not.toBeInTheDocument();
    for (const [index, edge] of state.edges.entries()) {
      const planned = layout.edges[index]!;
      expect(edge.router).toEqual({ name: "normal" });
      expect(edge.vertices).toEqual(planned.points.slice(1, -1));
      if (planned.label) {
        expect(edge.labels).toEqual([
          expect.objectContaining({
            attrs: expect.objectContaining({
              body: expect.objectContaining({
                width: planned.label.width,
                height: planned.label.height,
              }),
            }),
          }),
        ]);
      }
    }
    expect(state.edges).toHaveLength(model.edges.length);
  });

  it("is read-only, fits each selected change once and preserves manual view on resize", async () => {
    vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(900);
    vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(360);
    const report = reportFixture();
    const model = impactGraphModel(report, report.changes[0]!);
    const view = render(
      <MantineProvider>
        <ImpactGraph model={model} changeId="first" />
      </MantineProvider>,
    );
    expect(state.options.interacting).toBe(false);
    await waitFor(() => expect(state.fit).toHaveBeenCalledTimes(1));
    await userEvent.click(screen.getByRole("button", { name: "Увеличить граф влияния" }));
    expect(state.zoom).toHaveBeenCalledWith(0.15);
    act(() => state.resize());
    expect(state.fit).toHaveBeenCalledTimes(1);
    view.rerender(
      <MantineProvider>
        <ImpactGraph model={model} changeId="second" />
      </MantineProvider>,
    );
    expect(state.fit).toHaveBeenCalledTimes(2);
    act(() => state.resize());
    expect(state.fit).toHaveBeenCalledTimes(2);
    view.unmount();
    expect(state.dispose).toHaveBeenCalledTimes(1);
  });

  it("reports a rejected layout and retains the evidence outside the empty graph", async () => {
    vi.spyOn(elk, "layoutDiagram").mockRejectedValueOnce(new Error("ELK failed"));
    const report = reportFixture();
    render(
      <MantineProvider>
        <ImpactGraph model={impactGraphModel(report, report.changes[0]!)} changeId="failed" />
      </MantineProvider>,
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Связи доступны в списке доказательств",
    );
    expect(screen.queryByText("Располагаем граф влияния…")).not.toBeInTheDocument();
    expect(state.nodes).toEqual([]);
    expect(state.edges).toEqual([]);
  });
});

import { MantineProvider } from "@mantine/core";
import { render } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import ResourceGraph from "./ResourceGraph";
import SchemaGraph from "../schema-model/SchemaGraph";

const boundary = vi.hoisted(() => ({ fit: vi.fn(), nodes: [] as unknown[] }));
vi.mock("../diagram/useDiagramLayout", () => ({ useDiagramLayout: () => ({ pending: false }) }));
vi.mock("@antv/x6", () => ({
  routerPresets: {},
  Graph: class {
    on() {}
    clearCells() {
      boundary.nodes = [];
    }
    addNode(node: unknown) {
      boundary.nodes.push(node);
    }
    getNodes() {
      return boundary.nodes;
    }
    getEdges() {
      return [];
    }
    batchUpdate(_name: string, update: () => void) {
      update();
    }
    dispose() {}
    zoomToFit = boundary.fit;
  },
}));
beforeEach(() => {
  boundary.fit.mockClear();
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(900);
  vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(430);
});
afterEach(() => vi.restoreAllMocks());

it.each(["resources", "schemas"])(
  "fits %s when a distant layout preview is shown or cancelled, preserving manual views otherwise",
  (kind) => {
    const graph = (x: number, fitIdentity: number, selected = false) => (
      <MantineProvider>
        {kind === "resources" ? (
          <ResourceGraph
            model={{
              resources: [
                {
                  id: "orders",
                  name: "Orders",
                  service: "",
                  description: "",
                  operationKeys: [],
                  inferred: false,
                  x,
                  y: x,
                },
              ],
              operations: [],
              relations: [],
              diagnostics: [],
            }}
            disabled={false}
            selectedId={selected ? "orders" : undefined}
            onSelect={() => {}}
            onMove={() => {}}
            fitIdentity={fitIdentity}
          />
        ) : (
          <SchemaGraph
            model={{
              schemas: [
                {
                  name: "Order",
                  type: "object",
                  description: "",
                  pointer: "",
                  schemaJSON: "{}",
                  properties: [],
                  x,
                  y: x,
                },
              ],
              references: [],
              operations: [],
            }}
            disabled={false}
            selection={selected ? { schema: "Order" } : undefined}
            onSelect={() => {}}
            onMove={() => {}}
            onConnect={() => {}}
            fitIdentity={fitIdentity}
          />
        )}
      </MantineProvider>
    );
    const view = render(graph(10000, 0));
    expect(boundary.fit).toHaveBeenCalledTimes(1);
    view.rerender(graph(10000, 0, true));
    view.rerender(graph(10200, 0, true));
    expect(boundary.fit).toHaveBeenCalledTimes(1);
    view.rerender(graph(12, 1, true));
    expect(boundary.fit).toHaveBeenCalledTimes(2);
    view.rerender(graph(12, 1));
    expect(boundary.fit).toHaveBeenCalledTimes(2);
    view.rerender(graph(10200, 2));
    expect(boundary.fit).toHaveBeenCalledTimes(3);
  },
);

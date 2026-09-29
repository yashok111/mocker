import { MantineProvider } from "@mantine/core";
import { act, render } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import SchemaGraph from "./SchemaGraph";

const boundary = vi.hoisted(() => ({
  setProp: vi.fn(),
  setRouter: vi.fn(),
  move: () => {},
}));
vi.mock("../diagram/useDiagramLayout", () => ({ useDiagramLayout: () => ({ pending: false }) }));
vi.mock("@antv/x6", () => ({
  routerPresets: { manhattan: vi.fn(() => []) },
  Graph: class {
    edge = {
      isEdge: () => true,
      setSource: vi.fn(),
      setTarget: vi.fn(),
      setVertices: vi.fn(),
      setConnector: vi.fn(),
      setProp: boundary.setProp,
      setRouter: boundary.setRouter,
    };
    on(name: string, callback: () => void) {
      if (name === "node:change:position") boundary.move = callback;
    }
    addNode() {}
    addEdge() {}
    getCellById() {
      return this.edge;
    }
    getNodes() {
      return [];
    }
    clearCells() {}
    dispose() {}
  },
}));

it("restores a custom router through the raw router property after schema movement", () => {
  const schema = (name: string) => ({
    name,
    description: "",
    pointer: "",
    schemaJSON: "{}",
    type: "object",
    properties: [],
    x: 40,
    y: 40,
  });
  render(
    <MantineProvider>
      <SchemaGraph
        model={{
          schemas: [schema("Order"), schema("User")],
          references: [{ sourceSchema: "Order", targetSchema: "User", pointer: "", ref: "" }],
          operations: [],
        }}
        disabled={false}
        onSelect={() => {}}
        onMove={() => {}}
        onConnect={() => {}}
      />
    </MantineProvider>,
  );
  expect(boundary.setProp).toHaveBeenLastCalledWith("router", expect.any(Function));
  const calls = boundary.setProp.mock.calls.length;
  act(() => boundary.move());
  expect(boundary.setProp).toHaveBeenCalledTimes(calls + 1);
  expect(boundary.setProp).toHaveBeenLastCalledWith("router", expect.any(Function));
  // X6 setRouter expects a name/options object and wraps functions as a name.
  expect(boundary.setRouter).not.toHaveBeenCalled();
});

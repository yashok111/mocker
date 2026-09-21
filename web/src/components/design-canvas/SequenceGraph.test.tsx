import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import SequenceGraph, { type SequenceGraphProps } from "./SequenceGraph";
import type { CanvasDocument, CanvasSelection, MessageKind } from "./types";

type CellMetadata = {
  id: string;
  attrs: Record<string, Record<string, unknown>>;
  vertices?: { x: number; y: number }[];
  x: number;
  y: number;
  width: number;
  height: number;
  markup?: { selector: string }[];
  data?: { selection: CanvasSelection; role?: string; origin?: { x: number; y: number } };
};

const graph = vi.hoisted(() => ({
  construct: vi.fn(),
  createNode: vi.fn((metadata: CellMetadata) => metadata),
  createEdge: vi.fn((metadata: CellMetadata) => metadata),
  resetCells: vi.fn(),
  zoomToFit: vi.fn(),
  zoom: vi.fn(() => 1),
  zoomTo: vi.fn(),
  on: vi.fn(),
  resize: vi.fn(),
  dispose: vi.fn(),
}));

vi.mock("@antv/x6", () => ({
  Graph: class {
    constructor(options: unknown) {
      graph.construct(options);
      return graph;
    }
  },
}));

function documentOf(kind: MessageKind = "request"): CanvasDocument {
  return {
    formatVersion: 1,
    title: "Colors",
    participants: [
      { id: "client", name: "Client", kind: "client", description: "" },
      { id: "api", name: "API", kind: "service", description: "" },
    ],
    messages: [{ id: "call", fromId: "client", toId: "api", kind, label: "Call", description: "" }],
    fragments: [],
    contracts: [],
  };
}

function view(
  document: CanvasDocument,
  selection: CanvasSelection = null,
  props: Partial<SequenceGraphProps> = {},
) {
  return (
    <SequenceGraph
      document={document}
      selection={selection}
      onSelect={vi.fn()}
      onMoveParticipant={vi.fn()}
      onMoveMessage={vi.fn()}
      {...props}
    />
  );
}

function cell(id: string): CellMetadata {
  const cells = graph.resetCells.mock.lastCall?.[0] as CellMetadata[];
  const result = cells.find((item) => item.id === id);
  if (!result) throw new Error(`Cell ${id} was not rendered`);
  return result;
}

beforeEach(() => vi.clearAllMocks());
afterEach(cleanup);

describe("SequenceGraph colors", () => {
  it("keeps defaults and selected defaults for documents without colors", () => {
    const document = documentOf();
    const result = render(view(document));
    expect(cell("participant:client").attrs.body!.fill).toBe("#ffffff");
    expect(cell("message-label:call").attrs.body).toMatchObject({
      fill: "#ffffff",
      fillOpacity: 0.96,
    });
    expect(cell("message:call").attrs.line!.stroke).toBe("#087f70");
    result.rerender(view(document, { kind: "participant", id: "client" }));
    expect(cell("participant:client").attrs.body!.fill).toBe("#edf6f3");
  });

  it("keeps custom fill selected, readable names and grips, and an independent badge", () => {
    const document = documentOf();
    document.participants[0]!.color = "#111111";
    const result = render(view(document));
    const normal = cell("participant:client");
    expect(normal.attrs.body!.fill).toBe("#111111");
    expect(normal.attrs.name!.fill).toBe("#ffffff");
    expect(normal.attrs.drag!.fill).toBe("#ffffff");
    expect(normal.attrs.kind!.fill).not.toBe("#ffffff");
    result.rerender(view(document, { kind: "participant", id: "client" }));
    const selected = cell("participant:client");
    expect(selected.attrs.body!.fill).toBe("#111111");
    expect(selected.attrs.body!.stroke).toBe("#ffffff");
    expect(selected.attrs.body!.strokeWidth).toBeGreaterThan(
      normal.attrs.body!.strokeWidth as number,
    );
    expect(graph.zoomToFit).toHaveBeenCalledTimes(1);
  });

  it.each(["request", "response", "event"] as const)(
    "colors %s cards and both arrow parts independently while preserving dash patterns",
    (kind) => {
      const document = documentOf(kind);
      document.messages[0]!.color = "#111111";
      document.messages[0]!.arrowColor = "#dc2626";
      document.messages[0]!.operation = { contractId: "missing", operationKey: "missing" };
      render(view(document, { kind: "message", id: "call" }));
      expect(cell("message-label:call").attrs).toMatchObject({
        body: { fill: "#111111", fillOpacity: 1, stroke: "#ffffff" },
        label: { fill: "#ffffff" },
        grip: { fill: "#ffffff" },
        operation: { fill: "#ffffff" },
      });
      expect(cell("message:call").attrs.line).toMatchObject({
        stroke: "#dc2626",
        targetMarker: { fill: "#dc2626", stroke: "#dc2626" },
        strokeDasharray: kind === "response" ? "7 5" : kind === "event" ? "3 3" : "none",
      });
    },
  );

  it("colors notes without creating an arrow and preserves self-call geometry", () => {
    const document = documentOf("note");
    document.messages[0]!.color = "#111111";
    document.messages[0]!.arrowColor = "#dc2626";
    document.messages[0]!.operation = { contractId: "missing", operationKey: "missing" };
    const result = render(view(document, { kind: "message", id: "call" }));
    expect(cell("note:call").attrs).toMatchObject({
      body: { fill: "#111111", stroke: "#ffffff" },
      label: { fill: "#ffffff" },
      grip: { fill: "#ffffff" },
      operation: { fill: "#ffffff" },
    });
    expect(cell("note-link:call").attrs.line!.targetMarker).toBeNull();
    document.messages[0] = { ...document.messages[0]!, kind: "request", toId: "client" };
    result.rerender(view({ ...document }));
    expect(cell("message:call").vertices).toHaveLength(2);
    expect(cell("message:call").attrs.line!.stroke).toBe("#dc2626");
  });

  it("restores default presentation on reset without changing the viewport", () => {
    const document = documentOf();
    document.messages[0]!.color = "#111111";
    document.messages[0]!.arrowColor = "#dc2626";
    const result = render(view(document));
    result.rerender(view(documentOf()));
    expect(cell("message-label:call").attrs.body).toMatchObject({
      fill: "#ffffff",
      fillOpacity: 0.96,
    });
    expect(cell("message:call").attrs.line!.stroke).toBe("#087f70");
    expect(graph.zoomToFit).toHaveBeenCalledTimes(1);
  });
});

describe("SequenceGraph history", () => {
  function nodeOf(id: string) {
    const metadata = cell(id);
    return {
      getData: () => metadata.data,
      getPosition: () => ({ x: metadata.x + 500, y: metadata.y + 500 }),
      setPosition: vi.fn(),
    };
  }

  function emit(event: string, payload: unknown = {}) {
    const handler = graph.on.mock.calls.find(([name]) => name === event)?.[1];
    if (!handler) throw new Error(`No handler for ${event}`);
    handler(payload);
  }

  it("prevents read-only moves while retaining selection, pan and zoom", () => {
    const onMoveParticipant = vi.fn();
    const onMoveMessage = vi.fn();
    const onSelect = vi.fn();
    render(
      view(documentOf(), null, { readOnly: true, onMoveParticipant, onMoveMessage, onSelect }),
    );
    expect(graph.zoomToFit).toHaveBeenLastCalledWith({
      padding: { top: 60, right: 20, bottom: 44, left: 20 },
      maxScale: 1,
    });
    const options = graph.construct.mock.lastCall![0];
    expect(options.panning.enabled).toBe(true);
    for (const id of ["participant:client", "message-label:call"]) {
      const node = nodeOf(id);
      expect(options.interacting({ cell: node }).nodeMovable).toBe(false);
      emit("node:moving", { node });
      emit("node:moved", { node });
      expect(node.setPosition).not.toHaveBeenCalled();
      emit("cell:click", { cell: node });
      expect(onSelect).toHaveBeenLastCalledWith(cell(id).data!.selection);
      expect(
        cell(id).markup?.some(({ selector }) => ["drag", "grip", "dragZone"].includes(selector)),
      ).toBe(false);
    }
    emit("blank:click");
    expect(onSelect).toHaveBeenLastCalledWith(null);
    expect(onMoveParticipant).not.toHaveBeenCalled();
    expect(onMoveMessage).not.toHaveBeenCalled();
    expect(screen.queryByText(/Карточки и захваты/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Только просмотр/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Масштаб диаграммы")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Показать всё" })).not.toBeInTheDocument();
  });

  it("switches read-only mode without resetting viewport and restores editing afterwards", () => {
    const onMoveParticipant = vi.fn();
    const document = documentOf();
    const result = render(view(document, null, { onMoveParticipant }));
    expect(graph.zoomToFit).toHaveBeenLastCalledWith({
      padding: { top: 72, right: 28, bottom: 112, left: 28 },
      maxScale: 1,
    });
    const options = graph.construct.mock.lastCall![0];
    const node = nodeOf("participant:client");
    expect(options.interacting({ cell: node }).nodeMovable).toBe(true);
    result.rerender(view(document, null, { readOnly: true, onMoveParticipant }));
    expect(options.interacting({ cell: node }).nodeMovable).toBe(false);
    emit("node:moved", { node });
    expect(onMoveParticipant).not.toHaveBeenCalled();
    result.rerender(view(document, null, { readOnly: false, onMoveParticipant }));
    expect(options.interacting({ cell: node }).nodeMovable).toBe(true);
    expect(cell("participant:client").markup?.some(({ selector }) => selector === "drag")).toBe(
      true,
    );
    emit("node:moved", { node });
    expect(onMoveParticipant).toHaveBeenCalledWith("client", 1);
    expect(node.setPosition).toHaveBeenCalledWith(
      cell("participant:client").x,
      cell("participant:client").y,
    );
    expect(graph.construct).toHaveBeenCalledTimes(1);
    expect(graph.zoomToFit).toHaveBeenCalledTimes(1);
    expect(screen.getByText(/Карточки и захваты/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Увеличить масштаб" }));
    expect(graph.zoomTo).toHaveBeenCalledWith(1.12, { minScale: 0.35, maxScale: 2 });
    fireEvent.click(screen.getByRole("button", { name: "Показать всё" }));
    expect(graph.zoomToFit).toHaveBeenLastCalledWith({
      padding: { top: 72, right: 28, bottom: 112, left: 28 },
      maxScale: 1,
    });
  });

  it("draws distinct status contours without replacing colors or hiding selection", () => {
    const document = documentOf();
    document.participants[0]!.color = "#111111";
    document.messages[0]!.color = "#225588";
    document.messages[0]!.arrowColor = "#dc2626";
    document.messages.push({ ...document.messages[0]!, id: "note", kind: "note" });
    document.fragments.push({
      id: "loop",
      kind: "loop",
      label: "Retry",
      fromMessageId: "call",
      toMessageId: "note",
    });
    const highlights: NonNullable<SequenceGraphProps["highlights"]> = [
      { kind: "participant", id: "client", status: "added" },
      { kind: "message", id: "call", status: "moved" },
      { kind: "message", id: "note", status: "removed" },
      { kind: "fragment", id: "loop", status: "changed" },
      { kind: "participant", id: "missing", status: "removed" },
    ];
    const result = render(
      view(document, { kind: "participant", id: "client" }, { readOnly: true, highlights }),
    );
    const contours = ["participant:client", "message-label:call", "note:note", "fragment:loop"].map(
      (id) => {
        const content = cell(id);
        const contour = cell(`highlight:${id}`);
        expect(contour.x).toBeLessThan(content.x);
        expect(contour.y).toBeLessThan(content.y);
        expect(contour.width).toBeGreaterThan(content.width);
        expect(contour.height).toBeGreaterThan(content.height);
        expect(contour.attrs.body).toMatchObject({ fill: "none", pointerEvents: "none" });
        return contour.attrs.body!;
      },
    );
    expect(new Set(contours.map((contour) => contour.stroke)).size).toBe(4);
    expect(cell("participant:client").attrs.body).toMatchObject({
      fill: "#111111",
      stroke: "#ffffff",
    });
    expect(cell("message-label:call").attrs.body!.fill).toBe("#225588");
    expect(cell("note:note").attrs.body!.fill).toBe("#225588");
    expect(cell("note:note").markup?.some(({ selector }) => selector === "grip")).toBe(false);
    expect(cell("message:call").attrs.line).toMatchObject({
      stroke: "#dc2626",
      targetMarker: { fill: "#dc2626", stroke: "#dc2626" },
    });
    expect(
      (graph.resetCells.mock.lastCall![0] as CellMetadata[]).filter(({ id }) =>
        id.startsWith("highlight:"),
      ),
    ).toHaveLength(4);
    result.rerender(view(document, null, { readOnly: true, highlights: [] }));
    expect(
      (graph.resetCells.mock.lastCall![0] as CellMetadata[]).some(({ id }) =>
        id.startsWith("highlight:"),
      ),
    ).toBe(false);
    expect(graph.zoomToFit).toHaveBeenCalledTimes(1);
  });
});

describe("SequenceGraph execution", () => {
  it("labels run states separately from saved colors and clears them without moving the viewport", () => {
    const document = documentOf();
    document.messages[0]!.color = "#111111";
    document.messages[0]!.arrowColor = "#dc2626";
    const result = render(
      view(document, null, { readOnly: true, executionStatuses: { call: "running" } }),
    );
    expect(cell("execution:call").attrs.label!.text).toBe("Выполняется");
    expect(cell("message-label:call").attrs.body!.fill).toBe("#111111");
    expect(cell("message:call").attrs.line!.stroke).toBe("#dc2626");
    const runningColor = cell("execution:call").attrs.body!.fill;
    result.rerender(
      view(document, null, { readOnly: true, executionStatuses: { call: "failed" } }),
    );
    expect(cell("execution:call").attrs.label!.text).toBe("Ошибка");
    expect(cell("execution:call").attrs.body!.fill).not.toBe(runningColor);
    result.rerender(view(document, null, { readOnly: true }));
    expect(
      (graph.resetCells.mock.lastCall![0] as CellMetadata[]).some(({ id }) =>
        id.startsWith("execution:"),
      ),
    ).toBe(false);
    expect(graph.zoomToFit).toHaveBeenCalledTimes(1);
  });
});

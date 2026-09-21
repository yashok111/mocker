import { type Cell, Graph } from "@antv/x6";
import { useEffect, useRef, useState } from "react";

import { readableCanvasTextColor } from "./canvasColors";
import type { CanvasExecutionStatus } from "./canvasExecution";
import { resolveOperation } from "./canvasModel";
import { installCanvasWheelZoom } from "./canvasZoom";
import { ObjectDescriptionTooltip, type TooltipBounds } from "./ObjectDescriptionTooltip";
import styles from "./SequenceGraph.module.css";
import {
  layoutSequence,
  messageIndexAtY,
  participantIndexAtX,
  type SequenceLayout,
} from "./sequenceLayout";
import type { CanvasDocument, CanvasSelection, MessageKind, ParticipantKind } from "./types";

const palette = {
  ink: "#25332f",
  muted: "#69756e",
  accent: "#087f70",
  border: "#e0e5e1",
  canvas: "#f8faf8",
  surface: "#ffffff",
  accentSoft: "#edf6f3",
};

const absoluteText = { refX: 0, refY: 0 } as const;
const fitPadding = { top: 72, right: 28, bottom: 112, left: 28 } as const;
const readOnlyFitPadding = { top: 60, right: 20, bottom: 44, left: 20 } as const;

const participantKinds: Record<ParticipantKind, string> = {
  user: "Пользователь",
  client: "Клиент",
  service: "Сервис",
  external: "Внешний",
  database: "База данных",
  queue: "Очередь",
  other: "Объект",
};

const messageColors: Record<MessageKind, string> = {
  request: palette.accent,
  response: palette.muted,
  event: "#4f6f66",
  note: palette.muted,
};

type DiagramCellData = {
  selection?: Exclude<CanvasSelection, null>;
  role?: "participantGrip" | "messageGrip";
  origin?: { x: number; y: number };
  anchor?: { x: number; y: number };
};

interface ProjectionBuilder {
  addNode: (metadata: Parameters<Graph["createNode"]>[0]) => void;
  addEdge: (metadata: Parameters<Graph["createEdge"]>[0]) => void;
}

export interface SequenceGraphHighlight {
  kind: "participant" | "message" | "fragment";
  id: string;
  status: "added" | "removed" | "changed" | "moved";
}

const highlightStyles: Record<
  SequenceGraphHighlight["status"],
  { stroke: string; strokeDasharray: string }
> = {
  added: { stroke: "#2b8a3e", strokeDasharray: "none" },
  removed: { stroke: "#c92a2a", strokeDasharray: "6 3" },
  changed: { stroke: "#d97706", strokeDasharray: "none" },
  moved: { stroke: "#7048e8", strokeDasharray: "3 3" },
};

const executionStyles: Record<
  CanvasExecutionStatus,
  { label: string; fill: string; color: string }
> = {
  pending: { label: "Ожидает", fill: "#f1f3f5", color: "#495057" },
  running: { label: "Выполняется", fill: "#e7f5ff", color: "#1864ab" },
  passed: { label: "Успешно", fill: "#ebfbee", color: "#2b8a3e" },
  failed: { label: "Ошибка", fill: "#fff5f5", color: "#c92a2a" },
  skipped: { label: "Пропущен", fill: "#f1f3f5", color: "#495057" },
  cancelled: { label: "Отменён", fill: "#fff4e6", color: "#9c4600" },
};

export interface SequenceGraphProps {
  document: CanvasDocument;
  selection: CanvasSelection;
  onSelect: (selection: CanvasSelection) => void;
  onMoveParticipant: (id: string, index: number) => void;
  onMoveMessage: (id: string, index: number) => void;
  readOnly?: boolean;
  highlights?: SequenceGraphHighlight[];
  executionStatuses?: Record<string, CanvasExecutionStatus>;
}

function isSelected(
  selection: CanvasSelection,
  kind: NonNullable<CanvasSelection>["kind"],
  id: string,
) {
  return selection?.kind === kind && selection.id === id;
}

function cellId(role: string, id: string) {
  return `${role}:${id}`;
}

function addFragmentCells(
  graph: ProjectionBuilder,
  document: CanvasDocument,
  layout: SequenceLayout,
  selection: CanvasSelection,
) {
  const fragmentsById = new Map(document.fragments.map((fragment) => [fragment.id, fragment]));
  for (const geometry of layout.fragments) {
    const fragment = fragmentsById.get(geometry.id);
    if (!fragment) continue;
    const selected = isSelected(selection, "fragment", fragment.id);
    graph.addNode({
      id: cellId("fragment", fragment.id),
      x: geometry.x,
      y: geometry.y,
      width: geometry.width,
      height: geometry.height,
      zIndex: 1,
      markup: [
        { tagName: "rect", selector: "body" },
        { tagName: "path", selector: "tab" },
        { tagName: "text", selector: "kind" },
        { tagName: "text", selector: "label" },
      ],
      attrs: {
        root: { style: { cursor: "pointer" } },
        body: {
          fill: palette.accentSoft,
          fillOpacity: 0.28,
          stroke: selected ? palette.accent : "#aab9b2",
          strokeWidth: selected ? 2 : 1,
          strokeDasharray: "6 4",
          rx: 6,
          ry: 6,
        },
        tab: {
          d: "M 0 0 H 48 L 58 20 H 0 Z",
          fill: selected ? "#dceee8" : "#eef2ef",
          stroke: selected ? palette.accent : "#aab9b2",
          strokeWidth: 1,
        },
        kind: {
          ...absoluteText,
          text: fragment.kind,
          x: 12,
          y: 11,
          fill: palette.ink,
          fontSize: 10,
          fontWeight: 700,
          textAnchor: "start",
          textVerticalAnchor: "middle",
        },
        label: {
          ...absoluteText,
          text: fragment.label,
          x: 68,
          y: 11,
          fill: palette.muted,
          fontSize: 11,
          textAnchor: "start",
          textVerticalAnchor: "middle",
        },
      },
      data: { selection: { kind: "fragment", id: fragment.id } } satisfies DiagramCellData,
    });
  }
}

function addParticipantCells(
  graph: ProjectionBuilder,
  document: CanvasDocument,
  layout: SequenceLayout,
  selection: CanvasSelection,
  readOnly: boolean,
) {
  const participantsById = new Map(
    document.participants.map((participant) => [participant.id, participant]),
  );
  for (const geometry of layout.participants) {
    const participant = participantsById.get(geometry.id);
    if (!participant) continue;
    const selected = isSelected(selection, "participant", participant.id);
    const fill = participant.color ?? (selected ? palette.accentSoft : palette.surface);
    const textColor = (preferred: string) =>
      participant.color ? readableCanvasTextColor(fill, preferred) : preferred;
    const selectedBorder = participant.color ? readableCanvasTextColor(fill) : palette.accent;

    graph.addEdge({
      id: cellId("lifeline", participant.id),
      source: geometry.lifeline.source,
      target: geometry.lifeline.target,
      zIndex: 2,
      attrs: {
        line: {
          stroke: selected ? palette.accent : "#aeb9b4",
          strokeWidth: selected ? 1.5 : 1,
          strokeDasharray: "5 5",
          sourceMarker: null,
          targetMarker: null,
        },
      },
      data: { selection: { kind: "participant", id: participant.id } } satisfies DiagramCellData,
    });

    graph.addNode({
      id: cellId("participant", participant.id),
      x: geometry.header.x,
      y: geometry.header.y,
      width: geometry.header.width,
      height: geometry.header.height,
      zIndex: 12,
      markup: [
        { tagName: "rect", selector: "body" },
        { tagName: "rect", selector: "kindPill" },
        { tagName: "text", selector: "name" },
        { tagName: "text", selector: "kind" },
        ...(readOnly
          ? []
          : [
              { tagName: "rect", selector: "dragZone" },
              { tagName: "text", selector: "drag" },
            ]),
      ],
      attrs: {
        root: { style: { cursor: "pointer" } },
        body: {
          fill,
          stroke: selected ? selectedBorder : palette.border,
          strokeWidth: selected ? 2 : 1,
          rx: 9,
          ry: 9,
          cursor: "pointer",
        },
        kindPill: {
          x: 12,
          y: 30,
          width: 93,
          height: 15,
          rx: 7.5,
          ry: 7.5,
          fill: "#eef2ef",
          stroke: "none",
        },
        name: {
          ...absoluteText,
          text: participant.name,
          textWrap: { width: 120, height: 14, ellipsis: "…" },
          "aria-label": participant.name,
          x: 12,
          y: 17,
          fill: textColor(palette.ink),
          fontSize: 12,
          fontWeight: 650,
          textAnchor: "start",
          textVerticalAnchor: "middle",
          pointerEvents: "none",
        },
        kind: {
          ...absoluteText,
          text: participantKinds[participant.kind],
          x: 20,
          y: 37.5,
          fill: participant.color
            ? readableCanvasTextColor("#eef2ef", palette.muted)
            : palette.muted,
          fontSize: 9,
          fontWeight: 600,
          textAnchor: "start",
          textVerticalAnchor: "middle",
          pointerEvents: "none",
        },
        dragZone: {
          x: 132,
          y: 6,
          width: 26,
          height: geometry.header.height - 12,
          fill: "transparent",
          stroke: "none",
          cursor: "ew-resize",
        },
        drag: {
          ...absoluteText,
          text: "⠿",
          x: 145,
          y: 26,
          fill: textColor(palette.muted),
          fontSize: 14,
          textAnchor: "middle",
          textVerticalAnchor: "middle",
          pointerEvents: "none",
        },
      },
      data: {
        selection: { kind: "participant", id: participant.id },
        role: "participantGrip",
        origin: { x: geometry.header.x, y: geometry.header.y },
        anchor: { x: geometry.x, y: geometry.header.y + geometry.header.height / 2 },
      } satisfies DiagramCellData,
    });
  }
}

function addMessageCells(
  graph: ProjectionBuilder,
  document: CanvasDocument,
  layout: SequenceLayout,
  selection: CanvasSelection,
  readOnly: boolean,
) {
  const messagesById = new Map(document.messages.map((message) => [message.id, message]));
  for (const geometry of layout.messages) {
    const message = messagesById.get(geometry.id);
    if (!message) continue;
    const selected = isSelected(selection, "message", message.id);
    const color = message.arrowColor ?? messageColors[message.kind];
    const defaultFill =
      message.kind === "note"
        ? selected
          ? "#fff6d9"
          : "#fffaf0"
        : selected
          ? palette.accentSoft
          : palette.surface;
    const fill = message.color ?? defaultFill;
    const textColor = (preferred: string) =>
      message.color ? readableCanvasTextColor(fill, preferred) : preferred;
    const selectedBorder = message.color ? readableCanvasTextColor(fill) : palette.accent;
    const resolvedOperation = message.operation
      ? resolveOperation(document, message.operation)
      : null;
    const operationLabel = resolvedOperation
      ? `${resolvedOperation.location.method.toUpperCase()} ${resolvedOperation.location.path}`
      : message.operation
        ? "API-связь недоступна"
        : null;

    if (message.kind === "note" && geometry.note) {
      const noteWidth = Math.max(
        geometry.note.width,
        operationLabel ? Array.from(operationLabel).length * 6.4 + 38 : 0,
      );
      const noteHeight = operationLabel ? 58 : geometry.note.height;
      const noteY = geometry.rowY - noteHeight / 2;
      graph.addEdge({
        id: cellId("note-link", message.id),
        source: geometry.source,
        target: { x: geometry.note.x, y: geometry.rowY },
        zIndex: 4,
        attrs: {
          line: {
            stroke: "#aeb9b4",
            strokeWidth: 1,
            strokeDasharray: "3 3",
            sourceMarker: null,
            targetMarker: null,
          },
        },
        data: { selection: { kind: "message", id: message.id } } satisfies DiagramCellData,
      });
      graph.addNode({
        id: cellId("note", message.id),
        x: geometry.note.x,
        y: noteY,
        width: noteWidth,
        height: noteHeight,
        zIndex: 11,
        markup: [
          { tagName: "path", selector: "body" },
          { tagName: "path", selector: "fold" },
          ...(readOnly
            ? []
            : [
                { tagName: "rect", selector: "dragZone" },
                { tagName: "text", selector: "grip" },
              ]),
          { tagName: "text", selector: "label" },
          { tagName: "text", selector: "operation" },
        ],
        attrs: {
          root: { style: { cursor: "pointer" } },
          body: {
            d: `M 0 0 H ${noteWidth - 12} L ${noteWidth} 12 V ${noteHeight} H 0 Z`,
            fill,
            stroke: selected ? selectedBorder : "#d6caa5",
            strokeWidth: selected ? 2 : 1,
            cursor: "pointer",
          },
          fold: {
            d: `M ${noteWidth - 12} 0 V 12 H ${noteWidth}`,
            fill: "none",
            stroke: selected ? selectedBorder : textColor("#d6caa5"),
            strokeWidth: 1,
          },
          dragZone: {
            x: 0,
            y: 0,
            width: 24,
            height: noteHeight,
            fill: "transparent",
            stroke: "none",
            cursor: "ns-resize",
          },
          grip: {
            ...absoluteText,
            text: "⋮⋮",
            x: 12,
            y: noteHeight / 2,
            fill: textColor(palette.muted),
            fontSize: 10,
            textAnchor: "middle",
            textVerticalAnchor: "middle",
            pointerEvents: "none",
          },
          label: {
            ...absoluteText,
            text: message.label,
            x: 25,
            y: operationLabel ? 21 : noteHeight / 2,
            fill: textColor(palette.ink),
            fontSize: 11,
            textAnchor: "start",
            textVerticalAnchor: "middle",
            pointerEvents: "none",
          },
          operation: {
            ...absoluteText,
            text: operationLabel ?? "",
            x: 25,
            y: 39,
            fill: textColor(palette.accent),
            fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
            fontSize: 9,
            fontWeight: 650,
            textAnchor: "start",
            textVerticalAnchor: "middle",
            pointerEvents: "none",
          },
        },
        data: {
          selection: { kind: "message", id: message.id },
          role: "messageGrip",
          origin: { x: geometry.note.x, y: noteY },
          anchor: { x: geometry.note.x + noteWidth / 2, y: geometry.rowY },
        } satisfies DiagramCellData,
      });
      continue;
    }

    graph.addEdge({
      id: cellId("message", message.id),
      source: geometry.source,
      target: geometry.target,
      vertices: geometry.vertices,
      connector: { name: "rounded", args: { radius: 7 } },
      zIndex: 5,
      attrs: {
        line: {
          stroke: color,
          strokeWidth: selected ? 2.2 : 1.45,
          strokeDasharray:
            message.kind === "response" ? "7 5" : message.kind === "event" ? "3 3" : "none",
          sourceMarker: null,
          targetMarker: { name: "block", width: 8, height: 6, fill: color, stroke: color },
          cursor: "pointer",
        },
      },
      data: { selection: { kind: "message", id: message.id } } satisfies DiagramCellData,
    });

    const labelWidth = Math.max(
      geometry.label.width,
      operationLabel ? Array.from(operationLabel).length * 6.4 + 38 : 0,
    );
    const labelHeight = operationLabel ? 42 : geometry.label.height;
    const labelX = geometry.label.x + geometry.label.width / 2 - labelWidth / 2;
    const labelY = geometry.rowY - labelHeight + 6;
    graph.addNode({
      id: cellId("message-label", message.id),
      x: labelX,
      y: labelY,
      width: labelWidth,
      height: labelHeight,
      zIndex: 11,
      markup: [
        { tagName: "rect", selector: "body" },
        ...(readOnly
          ? []
          : [
              { tagName: "rect", selector: "dragZone" },
              { tagName: "text", selector: "grip" },
            ]),
        { tagName: "text", selector: "label" },
        { tagName: "text", selector: "operation" },
      ],
      attrs: {
        root: { style: { cursor: "pointer" } },
        body: {
          fill,
          fillOpacity: message.color ? 1 : 0.96,
          stroke: selected ? selectedBorder : palette.border,
          strokeWidth: selected ? 1.5 : 1,
          rx: 6,
          ry: 6,
          cursor: "pointer",
        },
        dragZone: {
          x: 0,
          y: 0,
          width: 24,
          height: labelHeight,
          fill: "transparent",
          stroke: "none",
          cursor: "ns-resize",
        },
        grip: {
          ...absoluteText,
          text: "⋮⋮",
          x: 12,
          y: labelHeight / 2,
          fill: textColor(palette.muted),
          fontSize: 10,
          textAnchor: "middle",
          textVerticalAnchor: "middle",
          pointerEvents: "none",
        },
        label: {
          ...absoluteText,
          text: message.label,
          x: 25,
          y: operationLabel ? 13 : labelHeight / 2,
          fill: textColor(palette.ink),
          fontSize: 11,
          fontWeight: 550,
          textAnchor: "start",
          textVerticalAnchor: "middle",
          pointerEvents: "none",
        },
        operation: {
          ...absoluteText,
          text: operationLabel ?? "",
          x: 25,
          y: 29,
          fill: textColor(resolvedOperation ? palette.accent : "#a15c4b"),
          fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
          fontSize: 9,
          fontWeight: 650,
          textAnchor: "start",
          textVerticalAnchor: "middle",
          pointerEvents: "none",
        },
      },
      data: {
        selection: { kind: "message", id: message.id },
        role: "messageGrip",
        origin: { x: labelX, y: labelY },
        anchor: { x: labelX + labelWidth / 2, y: geometry.rowY },
      } satisfies DiagramCellData,
    });
  }
}

function renderSequence(
  graph: Graph,
  document: CanvasDocument,
  selection: CanvasSelection,
  readOnly: boolean,
  highlights: SequenceGraphHighlight[] | undefined,
  executionStatuses: SequenceGraphProps["executionStatuses"],
) {
  const layout = layoutSequence(document);
  const cells: Cell[] = [];
  const statusByEntity = new Map(
    highlights?.map(({ kind, id, status }) => [cellId(kind, id), status]),
  );
  const projection: ProjectionBuilder = {
    addNode(metadata) {
      const entity = (metadata.data as DiagramCellData | undefined)?.selection;
      const status = entity && statusByEntity.get(cellId(entity.kind, entity.id));
      if (status) {
        // Keep change contours outside the card, independent of saved colors and selection.
        cells.push(
          graph.createNode({
            id: `highlight:${metadata.id}`,
            shape: "rect",
            x: (metadata.x ?? 0) - 4,
            y: (metadata.y ?? 0) - 4,
            width: (metadata.width ?? 0) + 8,
            height: (metadata.height ?? 0) + 8,
            zIndex: metadata.zIndex,
            attrs: {
              root: { pointerEvents: "none" },
              body: {
                fill: "none",
                ...highlightStyles[status],
                strokeWidth: 2,
                rx: 10,
                ry: 10,
                pointerEvents: "none",
              },
            },
          }),
        );
      }
      cells.push(graph.createNode(metadata));
      const execution = entity?.kind === "message" && executionStatuses?.[entity.id];
      if (execution && entity) {
        const style = executionStyles[execution];
        cells.push(
          graph.createNode({
            id: cellId("execution", entity.id),
            shape: "rect",
            x: (metadata.x ?? 0) + (metadata.width ?? 0) + 8,
            y: (metadata.y ?? 0) + ((metadata.height ?? 0) - 22) / 2,
            width: 100,
            height: 22,
            zIndex: 12,
            attrs: {
              body: { fill: style.fill, stroke: style.color, strokeWidth: 1, rx: 5, ry: 5 },
              label: { text: style.label, fill: style.color, fontSize: 10, fontWeight: 650 },
            },
            data: { selection: entity } satisfies DiagramCellData,
          }),
        );
      }
    },
    addEdge(metadata) {
      cells.push(graph.createEdge(metadata));
    },
  };
  addFragmentCells(projection, document, layout, selection);
  addParticipantCells(projection, document, layout, selection, readOnly);
  addMessageCells(projection, document, layout, selection, readOnly);
  graph.resetCells(cells);
  return layout;
}

export default function SequenceGraph({
  document,
  selection,
  onSelect,
  onMoveParticipant,
  onMoveMessage,
  readOnly = false,
  highlights,
  executionStatuses,
}: SequenceGraphProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const graphRef = useRef<Graph | null>(null);
  const layoutRef = useRef<SequenceLayout>(layoutSequence(document));
  const fitOnFirstRenderRef = useRef(true);
  const onSelectRef = useRef(onSelect);
  const onMoveParticipantRef = useRef(onMoveParticipant);
  const onMoveMessageRef = useRef(onMoveMessage);
  const documentRef = useRef(document);
  const readOnlyRef = useRef(readOnly);
  const [hoveredObject, setHoveredObject] = useState<{
    id: string;
    kind: "participant" | "message";
    document: CanvasDocument;
    bounds: TooltipBounds;
  } | null>(null);
  const hoveredDescription =
    hoveredObject?.document === document
      ? (hoveredObject.kind === "participant" ? document.participants : document.messages)
          .find((item) => item.id === hoveredObject.id)
          ?.description.trim()
      : undefined;

  useEffect(() => {
    onSelectRef.current = onSelect;
    onMoveParticipantRef.current = onMoveParticipant;
    onMoveMessageRef.current = onMoveMessage;
    documentRef.current = document;
    readOnlyRef.current = readOnly;
  }, [document, onMoveMessage, onMoveParticipant, onSelect, readOnly]);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    const graph = new Graph({
      container,
      background: { color: palette.canvas },
      grid: { visible: true, size: 16, type: "dot", args: [{ color: "#d8dfda", thickness: 1 }] },
      panning: { enabled: true, eventTypes: ["leftMouseDown"] },
      mousewheel: { enabled: false },
      connecting: { allowBlank: false, allowEdge: false, allowNode: false, allowPort: false },
      async: false,
      interacting(cellView) {
        const role = cellView.cell.getData<DiagramCellData>()?.role;
        return {
          nodeMovable:
            !readOnlyRef.current && (role === "participantGrip" || role === "messageGrip"),
          edgeMovable: false,
          edgeLabelMovable: false,
          arrowheadMovable: false,
          vertexMovable: false,
          vertexAddable: false,
          vertexDeletable: false,
          magnetConnectable: false,
          toolsAddable: false,
        };
      },
    });
    graphRef.current = graph;
    const removeWheelZoom = installCanvasWheelZoom(graph, container);
    const clearHover = () => setHoveredObject(null);
    graph.on("cell:mouseenter", ({ cell, view, e }) => {
      const data = cell.getData<DiagramCellData>();
      const selected = data?.selection;
      if (!selected || e.buttons) return;
      if (selected.kind !== "message" && data.role !== "participantGrip") return;
      if (selected.kind === "fragment") return;
      const current = documentRef.current;
      const item = (selected.kind === "participant" ? current.participants : current.messages).find(
        (item) => item.id === selected.id,
      );
      if (!item?.description.trim()) return;
      const card = view.container.getBoundingClientRect();
      const shell = (container.parentElement ?? container).getBoundingClientRect();
      setHoveredObject({
        id: item.id,
        kind: selected.kind,
        document: current,
        bounds: {
          left: card.left - shell.left,
          top: card.top - shell.top,
          width: Math.max(1, card.width),
          height: Math.max(1, card.height),
        },
      });
    });
    graph.on("cell:mouseleave", clearHover);
    graph.on("cell:mousedown", clearHover);
    graph.on("blank:mousedown", clearHover);
    graph.on("scale", clearHover);
    graph.on("translate", clearHover);
    container.addEventListener("mouseleave", clearHover);
    const dismissTooltip = (event: KeyboardEvent) => {
      if (event.key === "Escape") clearHover();
    };
    window.addEventListener("keydown", dismissTooltip);

    graph.on("cell:click", ({ cell }) => {
      const cellSelection = cell.getData<DiagramCellData>()?.selection;
      if (cellSelection) onSelectRef.current(cellSelection);
    });
    graph.on("blank:click", () => onSelectRef.current(null));
    graph.on("node:moving", ({ node }) => {
      if (readOnlyRef.current) return;
      const data = node.getData<DiagramCellData>();
      const origin = data?.origin;
      if (!origin) return;
      const position = node.getPosition();
      if (data.role === "participantGrip") node.setPosition(position.x, origin.y);
      if (data.role === "messageGrip") node.setPosition(origin.x, position.y);
    });
    graph.on("node:moved", ({ node }) => {
      if (readOnlyRef.current) return;
      const data = node.getData<DiagramCellData>();
      const movedSelection = data?.selection;
      const origin = data?.origin;
      if (!movedSelection || !origin) return;
      const position = node.getPosition();
      const anchor = data.anchor;
      const projectedX = anchor ? anchor.x + position.x - origin.x : position.x;
      const projectedY = anchor ? anchor.y + position.y - origin.y : position.y;
      try {
        if (data.role === "participantGrip" && movedSelection.kind === "participant") {
          onMoveParticipantRef.current(
            movedSelection.id,
            participantIndexAtX(layoutRef.current, projectedX),
          );
        }
        if (data.role === "messageGrip" && movedSelection.kind === "message") {
          onMoveMessageRef.current(
            movedSelection.id,
            messageIndexAtY(layoutRef.current, projectedY),
          );
        }
      } finally {
        node.setPosition(origin.x, origin.y);
      }
    });

    const resizeTarget = container.parentElement ?? container;
    const resize = () => {
      const bounds = resizeTarget.getBoundingClientRect();
      graph.resize(Math.max(1, bounds.width), Math.max(1, bounds.height));
    };
    resize();
    const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(resize);
    observer?.observe(resizeTarget);

    return () => {
      container.removeEventListener("mouseleave", clearHover);
      window.removeEventListener("keydown", dismissTooltip);
      removeWheelZoom();
      observer?.disconnect();
      graph.dispose();
      if (graphRef.current === graph) graphRef.current = null;
      fitOnFirstRenderRef.current = true;
    };
  }, []);

  useEffect(() => {
    const graph = graphRef.current;
    if (!graph) return;
    layoutRef.current = renderSequence(
      graph,
      document,
      selection,
      readOnly,
      highlights,
      executionStatuses,
    );
    if (fitOnFirstRenderRef.current && layoutRef.current.participants.length > 0) {
      graph.zoomToFit({ padding: readOnly ? readOnlyFitPadding : fitPadding, maxScale: 1 });
      fitOnFirstRenderRef.current = false;
    }
  }, [document, selection, readOnly, highlights, executionStatuses]);

  const zoomBy = (delta: number) => {
    const graph = graphRef.current;
    if (!graph) return;
    graph.zoomTo(graph.zoom() + delta, { minScale: 0.35, maxScale: 2 });
  };

  const fit = () => {
    const graph = graphRef.current;
    if (!graph || layoutRef.current.participants.length === 0) return;
    graph.zoomToFit({ padding: readOnly ? readOnlyFitPadding : fitPadding, maxScale: 1 });
  };

  return (
    <section className={styles.shell} aria-label="Диаграмма последовательности">
      {!readOnly ? (
        <div className={styles.controls} aria-label="Масштаб диаграммы">
          <button
            type="button"
            onClick={() => zoomBy(-0.12)}
            aria-label="Уменьшить масштаб"
            title="Уменьшить масштаб"
          >
            −
          </button>
          <button
            type="button"
            onClick={() => zoomBy(0.12)}
            aria-label="Увеличить масштаб"
            title="Увеличить масштаб"
          >
            +
          </button>
          <button
            type="button"
            onClick={fit}
            className={styles.fitButton}
            title="Показать всю диаграмму в видимой области"
          >
            Показать всё
          </button>
        </div>
      ) : null}
      <div ref={containerRef} className={styles.graph} />
      {hoveredObject && hoveredDescription ? (
        <ObjectDescriptionTooltip bounds={hoveredObject.bounds} description={hoveredDescription} />
      ) : null}
      {!readOnly ? (
        <div className={styles.hint}>
          ЛКМ по свободному месту — перемещение · Колёсико — масштаб · Карточки и захваты — порядок
        </div>
      ) : null}
    </section>
  );
}

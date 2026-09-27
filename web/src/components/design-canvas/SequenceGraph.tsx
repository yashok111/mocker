import {
  palette,
  cellId,
  renderSequenceProjection,
  type DiagramCellData,
  type SequenceGraphHighlight,
} from "./sequenceProjection";
export type { SequenceGraphHighlight } from "./sequenceProjection";
import { Graph } from "@antv/x6";
import { useCallback, useEffect, useRef, useState, type RefObject } from "react";

import type { CanvasExecutionStatus } from "./canvasExecution";
import { installCanvasWheelZoom } from "./canvasZoom";
import { ObjectDescriptionTooltip, type TooltipBounds } from "./ObjectDescriptionTooltip";
import styles from "./SequenceGraph.module.css";
import { layoutSequence, messageIndexAtY, type SequenceLayout } from "./sequenceLayout";
import type { CanvasDocument, CanvasSelection } from "./types";
import { MAX_PARTICIPANT_OFFSET_X } from "./types";

const fitPadding = { top: 72, right: 28, bottom: 112, left: 28 } as const;
const readOnlyFitPadding = { top: 60, right: 20, bottom: 44, left: 20 } as const;

export interface SequenceGraphProps {
  document: CanvasDocument;
  selection: CanvasSelection;
  onSelect: (selection: CanvasSelection) => void;
  onSpaceParticipant: (id: string, offsetX: number) => void;
  onMoveMessage: (id: string, index: number) => void;
  onEditLabel?: (selection: CanvasSelection) => void;
  editingSelection?: CanvasSelection;
  onCommitLabel?: (selection: Exclude<CanvasSelection, null>, label: string) => void;
  onCancelEditLabel?: () => void;
  readOnly?: boolean;
  highlights?: SequenceGraphHighlight[];
  executionStatuses?: Record<string, CanvasExecutionStatus>;
}
function InlineLabelEditor({
  initialValue,
  containerRef,
  editingCellId,
  onCommit,
  onCancel,
}: {
  initialValue: string;
  containerRef: RefObject<HTMLDivElement | null>;
  editingCellId: string;
  onCommit: (value: string) => void;
  onCancel: () => void;
}) {
  const [value, setValue] = useState(initialValue);
  const finished = useRef(false);
  const setInputRef = useCallback(
    (node: HTMLInputElement | null) => {
      if (!node) return;
      const container = containerRef.current;
      const element = Array.from(container?.querySelectorAll("[data-cell-id]") ?? []).find(
        (candidate) => candidate.getAttribute("data-cell-id") === editingCellId,
      );
      const rect = element?.getBoundingClientRect();
      const shell = container?.parentElement?.getBoundingClientRect();
      node.style.left = `${rect && shell ? rect.left - shell.left : 80}px`;
      node.style.top = `${rect && shell ? rect.top - shell.top : 80}px`;
      node.style.width = `${rect ? Math.max(rect.width, 160) : 220}px`;
      node.focus();
      node.select();
    },
    [containerRef, editingCellId],
  );
  const finish = () => {
    if (finished.current) return;
    finished.current = true;
    onCommit(value);
  };
  return (
    <input
      ref={setInputRef}
      className={styles.inlineLabel}
      aria-label="Изменить подпись на диаграмме"
      value={value}
      onChange={(event) => setValue(event.currentTarget.value)}
      onKeyDown={(event) => {
        if (event.key === "Escape") {
          event.preventDefault();
          finished.current = true;
          onCancel();
        }
        if (event.key === "Enter") {
          event.preventDefault();
          finish();
        }
      }}
      onBlur={finish}
    />
  );
}

export default function SequenceGraph({
  document,
  selection,
  onSelect,
  onSpaceParticipant,
  onMoveMessage,
  onEditLabel,
  editingSelection,
  onCommitLabel,
  onCancelEditLabel,
  readOnly = false,
  highlights,
  executionStatuses,
}: SequenceGraphProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const graphRef = useRef<Graph | null>(null);
  const layoutRef = useRef<SequenceLayout>(layoutSequence(document));
  const fitOnFirstRenderRef = useRef(true);
  const onSelectRef = useRef(onSelect);
  const onSpaceParticipantRef = useRef(onSpaceParticipant);
  const onMoveMessageRef = useRef(onMoveMessage);
  const onEditLabelRef = useRef(onEditLabel);
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
    onSpaceParticipantRef.current = onSpaceParticipant;
    onMoveMessageRef.current = onMoveMessage;
    onEditLabelRef.current = onEditLabel;
    documentRef.current = document;
    readOnlyRef.current = readOnly;
  }, [document, onMoveMessage, onSpaceParticipant, onSelect, onEditLabel, readOnly]);

  const editingItem =
    editingSelection?.kind === "message"
      ? document.messages.find((item) => item.id === editingSelection.id)
      : editingSelection?.kind === "participant"
        ? document.participants.find((item) => item.id === editingSelection.id)
        : editingSelection?.kind === "fragment"
          ? document.fragments.find((item) => item.id === editingSelection.id)
          : undefined;
  const editingCellId = editingSelection
    ? cellId(
        editingSelection.kind === "message"
          ? editingItem && "kind" in editingItem && editingItem.kind === "note"
            ? "note"
            : "message-label"
          : editingSelection.kind,
        editingSelection.id,
      )
    : null;

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
    graph.on("cell:dblclick", ({ cell }) => {
      if (readOnlyRef.current) return;
      const cellSelection = cell.getData<DiagramCellData>()?.selection;
      if (cellSelection) onEditLabelRef.current?.(cellSelection);
    });
    graph.on("blank:click", () => onSelectRef.current(null));
    graph.on("node:moving", ({ node }) => {
      if (readOnlyRef.current) return;
      const data = node.getData<DiagramCellData>();
      const origin = data?.origin;
      if (!origin) return;
      const position = node.getPosition();
      if (data.role === "participantGrip") {
        const offsetX =
          documentRef.current.participants.find((item) => item.id === data.selection?.id)
            ?.offsetX ?? 0;
        node.setPosition(
          Math.max(
            origin.x - offsetX,
            Math.min(origin.x + MAX_PARTICIPANT_OFFSET_X - offsetX, position.x),
          ),
          origin.y,
        );
      }
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
      const projectedY = anchor ? anchor.y + position.y - origin.y : position.y;
      try {
        if (data.role === "participantGrip" && movedSelection.kind === "participant") {
          const offsetX =
            documentRef.current.participants.find((item) => item.id === movedSelection.id)
              ?.offsetX ?? 0;
          onSpaceParticipantRef.current(
            movedSelection.id,
            Math.max(
              0,
              Math.min(MAX_PARTICIPANT_OFFSET_X, Math.round(offsetX + position.x - origin.x)),
            ),
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
    layoutRef.current = renderSequenceProjection(
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
      {!readOnly && editingSelection && editingItem ? (
        <InlineLabelEditor
          key={`${editingSelection.kind}:${editingSelection.id}`}
          initialValue={"name" in editingItem ? editingItem.name : editingItem.label}
          containerRef={containerRef}
          editingCellId={editingCellId!}
          onCommit={(label) => onCommitLabel?.(editingSelection, label)}
          onCancel={() => onCancelEditLabel?.()}
        />
      ) : null}
      {hoveredObject && hoveredDescription ? (
        <ObjectDescriptionTooltip bounds={hoveredObject.bounds} description={hoveredDescription} />
      ) : null}
      {!readOnly ? (
        <div className={styles.hint}>
          ЛКМ по фону — перемещение · Колёсико — масштаб · Заголовки — расстояние между колонками ·
          Карточки и захваты сообщений — порядок
        </div>
      ) : null}
    </section>
  );
}

import { useEffect, useMemo, useRef, useState } from "react";
import type {
  BackendStructuralNodeResponse,
  BackendStructuralEdgeResponse,
  BackendDatabaseTableItem,
  BackendDatabaseRelationshipItem,
  BackendPinnedDatabaseRelationshipItem,
} from "@/api/generated/schemas";
import type { DiagramLayoutResult } from "../diagram/elkLayout";
import type { SavedViewState } from "./backendSavedViewState";
type Positions = SavedViewState["positions"];
export function updateSavedPosition(positions: Positions, next: Positions[number]): Positions {
  if (![next.x, next.y].every((value) => Number.isFinite(value) && Math.abs(value) <= 1000000))
    throw new Error("Координаты должны быть конечными числами от −1000000 до 1000000");
  if (!positions.some((p) => p.nodeId === next.nodeId) && positions.length >= 200)
    throw new Error("Лимит 200 координат. Очистите расположение перед добавлением новой карточки.");
  return [...positions.filter((p) => p.nodeId !== next.nodeId), next];
}
export const acceptAutomaticPositions = (positions: Positions, ids: string[]) =>
  positions.filter((p) => !ids.includes(p.nodeId));
export function overlaySavedPositions(
  layout: DiagramLayoutResult,
  positions: Positions,
): DiagramLayoutResult {
  const byId = new Map(positions.map((p) => [p.nodeId, p]));
  const nodes = layout.nodes.map((node) =>
    byId.has(node.id) ? { ...node, x: byId.get(node.id)!.x, y: byId.get(node.id)!.y } : node,
  );
  const rendered = new Map(nodes.map((n) => [n.id, n]));
  return {
    nodes,
    edges: layout.edges.map((edge) => {
      if (!byId.has(edge.source) && !byId.has(edge.target)) return edge;
      const source = rendered.get(edge.source)!,
        target = rendered.get(edge.target)!;
      if (edge.source === edge.target) {
        const previous = layout.nodes.find((node) => node.id === edge.source)!;
        const dx = source.x - previous.x,
          dy = source.y - previous.y;
        return {
          ...edge,
          points: edge.points.map((point) => ({ x: point.x + dx, y: point.y + dy })),
          ...(edge.label
            ? { label: { ...edge.label, x: edge.label.x + dx, y: edge.label.y + dy } }
            : {}),
        };
      }
      const start = { x: source.x + source.width, y: source.y + source.height / 2 },
        end = { x: target.x, y: target.y + target.height / 2 };
      const middle = (start.x + end.x) / 2;
      return {
        ...edge,
        points: [start, { x: middle, y: start.y }, { x: middle, y: end.y }, end],
        ...(edge.label
          ? {
              label: {
                ...edge.label,
                x: middle - edge.label.width / 2,
                y: (start.y + end.y) / 2 - edge.label.height / 2,
              },
            }
          : {}),
      };
    }),
  };
}
export function collapseFlowScene(
  nodes: BackendStructuralNodeResponse[],
  edges: BackendStructuralEdgeResponse[],
  groups: string[],
) {
  const hidden = new Set(
    nodes
      .filter(
        (n) =>
          "transactionContext" in n.attributes &&
          n.attributes.transactionContext.status === "known" &&
          groups.includes(n.attributes.transactionContext.transactionId),
      )
      .map((n) => n.id),
  );
  const eligible = edges.filter((e) => !hidden.has(e.from) && !hidden.has(e.to));
  return {
    nodes: nodes.filter((n) => !hidden.has(n.id)),
    edges: eligible,
    hiddenNodes: hidden.size,
    hiddenEdges: edges.length - eligible.length,
  };
}
export function collapseDatabaseScene(
  tables: BackendDatabaseTableItem[],
  relationships: (BackendDatabaseRelationshipItem | BackendPinnedDatabaseRelationshipItem)[],
  groups: string[],
) {
  const hidden = new Set(
    tables.filter((t) => t.schemaId && groups.includes(t.schemaId)).map((t) => t.tableId),
  );
  const eligible = relationships.filter(
    (e) => !hidden.has(e.sourceTableId) && (!e.targetTableId || !hidden.has(e.targetTableId)),
  );
  return {
    tables: tables.filter((t) => !hidden.has(t.tableId)),
    relationships: eligible,
    hiddenNodes: hidden.size,
    hiddenEdges: relationships.length - eligible.length,
  };
}
export function useSavedViewLayout(
  layout: DiagramLayoutResult | null | undefined,
  positions: Positions,
  onChange: (next: Positions) => void,
  onPreview?: (value: boolean) => void,
) {
  const [preview, setPreview] = useState(false);
  const [undo, setUndo] = useState<Positions | null>(null);
  const [error, setError] = useState("");
  const identity = JSON.stringify(layout?.nodes.map((n) => n.id) ?? []);
  const previousIdentity = useRef(identity);
  useEffect(() => {
    if (previousIdentity.current !== identity) {
      previousIdentity.current = identity;
      setPreview(false);
      onPreview?.(false);
    }
  }, [identity, onPreview]);
  const previewCallback = useRef(onPreview);
  useEffect(() => {
    previewCallback.current = onPreview;
  });
  useEffect(() => () => previewCallback.current?.(false), []);
  const display = useMemo(
    () => (layout ? overlaySavedPositions(layout, preview ? [] : positions) : null),
    [layout, preview, positions],
  );
  function move(nodeId: string, x: number, y: number) {
    if (preview) return false;
    try {
      const next = updateSavedPosition(positions, { nodeId, x, y });
      setUndo(structuredClone(positions));
      onChange(next);
      setError("");
      return true;
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : "Ошибка координат");
      return false;
    }
  }
  return {
    display,
    preview,
    error,
    canUndo: !!undo,
    move,
    startPreview: () => {
      if (layout) {
        setPreview(true);
        onPreview?.(true);
      }
    },
    cancelPreview: () => {
      setPreview(false);
      onPreview?.(false);
    },
    apply: () => {
      if (!layout || !preview) return;
      setUndo(structuredClone(positions));
      onChange(
        acceptAutomaticPositions(
          positions,
          layout.nodes.map((n) => n.id),
        ),
      );
      setPreview(false);
      onPreview?.(false);
    },
    undo: () => {
      if (undo) {
        onChange(undo);
        setUndo(null);
        setPreview(false);
        onPreview?.(false);
      }
    },
    clear: () => {
      setUndo(structuredClone(positions));
      onChange([]);
      setPreview(false);
      onPreview?.(false);
      setError("");
    },
  };
}
export type SavedViewLayoutController = ReturnType<typeof useSavedViewLayout>;

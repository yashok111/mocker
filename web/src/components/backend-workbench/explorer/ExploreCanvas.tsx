import { Graph, Shape } from "@antv/x6";
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Button, Group, Text } from "@mantine/core";
import { useDiagramLayout } from "../../diagram/useDiagramLayout";
import { applyDiagramRoutes, measureDiagramLabel } from "../../diagram/elkX6";
import { installCanvasWheelZoom } from "../../diagram/canvasControls";
import { diagramOptions, diagramEdgeLine } from "../../diagram/presentation";
import type { Camera } from "./navigation";
import { relationNames, type MapNode, type MapEdge } from "./model";
import styles from "./Explorer.module.css";
import { positionSavedScene, preserveAnchorCamera, revealScenarioStart } from "./sceneLayout";
import type { DiagramLayoutResult } from "../../diagram/elkLayout";
import { sourceOverviewLayout, architectureOverviewLayout } from "./overviewLayout";
import { createCardClicks } from "./cardClicks";
import { createExploreCard, exploreCardWidth, exploreCardMinHeight } from "./exploreCard";
import {
  ObjectDescriptionTooltip,
  type TooltipBounds,
} from "../../design-canvas/ObjectDescriptionTooltip";

Shape.HTML.register({
  shape: "backend-explore-card",
  effect: ["data"],
  html(node) {
    const data = node.getData() as MapNode & { selected?: boolean };
    return createExploreCard(data);
  },
});
export function ExploreCanvas({
  nodes,
  edges,
  selection,
  camera,
  onCamera,
  onSelect,
  onEnter,
  controls,
  anchorId,
  startId,
}: {
  nodes: MapNode[];
  edges: MapEdge[];
  selection?: string;
  camera?: Camera;
  onCamera: (c: Camera) => void;
  onSelect: (id: string, type?: "node" | "edge") => void;
  onEnter: (node: MapNode) => void;
  controls?: ReactNode;
  anchorId?: string;
  startId?: string;
}) {
  const host = useRef<HTMLElement>(null);
  const graphRef = useRef<Graph | null>(null);
  const callback = useRef({ onSelect, onCamera, onEnter });
  useEffect(() => {
    callback.current = { onSelect, onCamera, onEnter };
  }, [onSelect, onCamera, onEnter]);
  const initialCamera = useRef(camera);
  const previousNodes = useRef<DiagramLayoutResult["nodes"]>([]);
  const [zoom, setZoom] = useState(100);
  const [heights, setHeights] = useState<ReadonlyMap<string, number>>(new Map());
  const [tooltip, setTooltip] = useState<{ description: string; bounds: TooltipBounds } | null>(
    null,
  );
  useLayoutEffect(() => {
    if (!host.current) return;
    let disposed = false;
    const measure = () => {
      if (disposed || !host.current) return;
      const probe = document.createElement("div");
      probe.style.cssText = `position:absolute;visibility:hidden;pointer-events:none;width:${exploreCardWidth}px;left:0;top:0;`;
      probe.setAttribute("aria-hidden", "true");
      host.current.append(probe);
      const cards = nodes.map((node) => {
        const card = createExploreCard(node);
        card.style.height = "auto";
        card.style.minHeight = `${exploreCardMinHeight}px`;
        probe.append(card);
        return { id: node.id, card };
      });
      const measured = new Map(
        cards.map(({ id, card }) => [
          id,
          Math.max(exploreCardMinHeight, Math.ceil(card.getBoundingClientRect().height)),
        ]),
      );
      probe.remove();
      setHeights((previous) =>
        previous.size === measured.size &&
        [...measured].every(([id, height]) => previous.get(id) === height)
          ? previous
          : measured,
      );
    };
    measure();
    void document.fonts?.ready.then(measure);
    return () => {
      disposed = true;
    };
  }, [nodes]);
  const nodeById = useMemo(() => new Map(nodes.map((n) => [n.id, n])), [nodes]);
  const edgeById = useMemo(() => new Map(edges.map((e) => [e.id, e])), [edges]);
  const shape = useMemo(
    () => ({
      nodes: nodes.map((n) => ({
        id: n.id,
        width: exploreCardWidth,
        height: heights.get(n.id) ?? exploreCardMinHeight,
      })),
      edges: edges
        .filter((e) => nodeById.has(e.from) && nodeById.has(e.to))
        .map((e) => ({
          id: e.id,
          source: e.from,
          target: e.to,
          label: measureDiagramLabel(e.label || relationNames[e.kind] || e.kind, 150, 2),
        })),
    }),
    [nodes, edges, nodeById, heights],
  );
  const sourceOverview = useMemo(
    () => sourceOverviewLayout(nodes, edges, heights),
    [nodes, edges, heights],
  );
  const overview = useMemo(
    () => sourceOverview ?? architectureOverviewLayout(nodes, edges, heights),
    [sourceOverview, nodes, edges, heights],
  );
  const computed = useDiagramLayout(overview ? { nodes: [], edges: [] } : shape);
  const rawLayout = overview ?? computed.layout;
  const positioned = useMemo(
    () =>
      rawLayout
        ? positionSavedScene(
            rawLayout,
            nodes.flatMap((n) =>
              n.x !== undefined && n.y !== undefined ? [{ nodeId: n.id, x: n.x, y: n.y }] : [],
            ),
          )
        : undefined,
    [rawLayout, nodes],
  );
  const layout = { ...computed, layout: positioned };
  useEffect(() => {
    if (!host.current || !layout.layout) return;
    const renderedNodes = layout.layout.nodes;
    const graph = new Graph({
      container: host.current,
      ...diagramOptions(),
      background: { color: "#f7f9f8" },
      grid: false,
      interacting: false,
      async: false,
    });
    graphRef.current = graph;
    const stopWheel = installCanvasWheelZoom(graph, host.current);
    for (const position of layout.layout.nodes) {
      const data = nodeById.get(position.id)!;
      if (overview && (data.kind === "system" || data.boundary))
        graph.addNode({
          ...position,
          shape: "rect",
          zIndex: -1,
          label: data.name,
          attrs: {
            body: { fill: "#f0f5f2", stroke: "#758d7d", strokeWidth: 1, rx: 16, ry: 16 },
            label: {
              refX: 28,
              refY: 26,
              textAnchor: "start",
              textVerticalAnchor: "middle",
              fill: "#476958",
              fontSize: 15,
              fontWeight: 600,
            },
          },
        });
      else
        graph.addNode({
          ...position,
          ...(data.x !== undefined ? { x: data.x, y: data.y } : {}),
          shape: "backend-explore-card",
          data,
        });
    }
    for (const e of layout.layout.edges) {
      const edge = edgeById.get(e.id)!;
      const baseStroke =
        edge.origin === "Удалено"
          ? "#af4e4e"
          : edge.origin === "Авторское изменение"
            ? "#a07727"
            : "#66816c";
      graph.addEdge({
        data: { baseStroke },
        id: e.id,
        source: e.source,
        target: e.target,
        attrs: {
          line: {
            ...diagramEdgeLine(),
            stroke: baseStroke,
            strokeDasharray:
              edge.kind === "contains" || edge.origin === "Авторское утверждение"
                ? "5 4"
                : undefined,
            ...(edge.kind === "contains" ? { targetMarker: null } : {}),
          },
        },
      });
    }
    applyDiagramRoutes(graph, layout.layout);
    for (const edge of graph.getEdges()) edge.setConnector("rounded", { radius: 8 });
    // Routing raises every node above edges; containment frames belong behind both.
    if (overview)
      for (const node of nodes)
        if (node.kind === "system" || node.boundary) graph.getCellById(node.id)?.setZIndex(-1);
    const clicks = createCardClicks(
      (id) => callback.current.onSelect(id),
      (id) => {
        const data = nodeById.get(id);
        if (data) callback.current.onEnter(data);
      },
    );
    graph.on("node:click", ({ node, e }) => clicks.click(node.id, e.detail));
    graph.on("node:dblclick", ({ node, e }) => {
      e.preventDefault();
      clicks.doubleClick(node.id);
    });
    graph.on("edge:click", ({ edge }) => {
      clicks.cancel();
      callback.current.onSelect(edge.id, "edge");
    });
    const clearTooltip = () => setTooltip(null);
    const showTooltip = (button: HTMLElement) => {
      const data = nodeById.get(button.dataset.objectId ?? "");
      if (!data?.description.trim() || data.description === data.name) return clearTooltip();
      const card = button.getBoundingClientRect();
      const shell = host.current!.parentElement!.getBoundingClientRect();
      setTooltip({
        description: data.description,
        bounds: {
          left: card.left - shell.left,
          top: card.top - shell.top,
          width: card.width,
          height: card.height,
        },
      });
    };
    const focusTooltip = (event: FocusEvent) => {
      const button = (event.target as HTMLElement).closest<HTMLElement>("button[data-object-id]");
      if (button) showTooltip(button);
    };
    graph.on("node:mouseenter", ({ view, e }) => {
      if (e.buttons) return;
      const button = view.container.querySelector<HTMLElement>("button[data-object-id]");
      if (button) showTooltip(button);
    });
    graph.on("node:mouseleave", clearTooltip);
    graph.on("cell:mousedown", clearTooltip);
    graph.on("blank:mousedown", clearTooltip);
    graph.on("scale", clearTooltip);
    graph.on("translate", clearTooltip);
    host.current.addEventListener("focusin", focusTooltip);
    host.current.addEventListener("focusout", clearTooltip);
    host.current.addEventListener("mouseleave", clearTooltip);
    const dismissTooltip = (event: KeyboardEvent) => {
      if (event.key === "Escape") clearTooltip();
    };
    window.addEventListener("keydown", dismissTooltip);
    const keyboard = (e: KeyboardEvent) => {
      const button = (e.target as HTMLElement).closest<HTMLButtonElement>("button[data-object-id]");
      if (button && (e.key === "Enter" || e.key === " ")) {
        e.preventDefault();
        clicks.click(button.dataset.objectId!, 0);
      }
    };
    host.current.addEventListener("keydown", keyboard);
    if (initialCamera.current) {
      const restored = preserveAnchorCamera(
        initialCamera.current,
        previousNodes.current,
        layout.layout.nodes,
        anchorId,
      );
      graph.zoomTo(restored.zoom);
      graph.translate(restored.x, restored.y);
    } else {
      graph.zoomToFit({ padding: 32, maxScale: 1 });
      const start = renderedNodes.find((n) => n.id === startId);
      if (start) {
        const { tx, ty } = graph.translate();
        const restored = revealScenarioStart({ x: tx, y: ty, zoom: graph.zoom() }, start, {
          width: host.current.clientWidth,
          height: host.current.clientHeight,
        });
        graph.translate(restored.x, restored.y);
      }
    }
    const save = () => {
      const { tx, ty } = graph.translate();
      const z = graph.zoom();
      setZoom(Math.round(z * 100));
      callback.current.onCamera({ x: tx, y: ty, zoom: z });
    };
    graph.on("scale", save);
    graph.on("translate", save);
    save();
    const element = host.current;
    let width = element.clientWidth;
    let height = element.clientHeight;
    let resizeFrame = 0;
    graph.on("resize", (size) => {
      if (size.width === width && size.height === height) return;
      width = size.width;
      height = size.height;
      cancelAnimationFrame(resizeFrame);
      resizeFrame = requestAnimationFrame(() => {
        if (width > 0 && height > 0) graph.zoomToFit({ padding: 32, maxScale: 1 });
      });
    });
    return () => {
      cancelAnimationFrame(resizeFrame);
      clicks.cancel();
      const { tx, ty } = graph.translate();
      initialCamera.current = { x: tx, y: ty, zoom: graph.zoom() };
      previousNodes.current = renderedNodes;
      element.removeEventListener("keydown", keyboard);
      element.removeEventListener("focusin", focusTooltip);
      element.removeEventListener("focusout", clearTooltip);
      element.removeEventListener("mouseleave", clearTooltip);
      window.removeEventListener("keydown", dismissTooltip);
      clearTooltip();
      stopWheel();
      graph.dispose();
      graphRef.current = null;
    };
  }, [
    layout.layout,
    nodes,
    edges,
    overview,
    sourceOverview,
    nodeById,
    edgeById,
    anchorId,
    startId,
  ]);
  useEffect(() => {
    const graph = graphRef.current;
    if (!graph) return;
    for (const button of host.current?.querySelectorAll<HTMLButtonElement>(
      "button[data-object-id]",
    ) ?? []) {
      const active = button.dataset.objectId === selection;
      button.dataset.selected = String(active);
      button.setAttribute("aria-pressed", String(active));
    }
    for (const e of graph.getEdges())
      e.attr(
        "line/stroke",
        e.id === selection ? "#087f70" : String(e.getData()?.baseStroke ?? "#66816c"),
      );
  }, [selection, layout.layout]);
  return (
    <div className={styles.canvas}>
      <figure ref={host} className={styles.graph} aria-label="Карта системы" />
      {tooltip && (
        <ObjectDescriptionTooltip bounds={tooltip.bounds} description={tooltip.description} />
      )}
      {layout.error && (
        <Text role="alert" className={styles.canvasMessage}>
          {layout.error} Откройте список объектов.
        </Text>
      )}
      <div className={styles.legend}>
        <span className={styles.legendDot} />
        Исходники и авторские схемы — не запись выполнения
      </div>
      <Group className={styles.canvasControls} gap={3}>
        {controls}
        <Button
          variant="subtle"
          size="compact-sm"
          aria-label="Уменьшить карту"
          onClick={() => graphRef.current?.zoom(-0.15)}
        >
          −
        </Button>
        <Text size="xs" w={42} ta="center">
          {zoom}%
        </Text>
        <Button
          variant="subtle"
          size="compact-sm"
          aria-label="Увеличить карту"
          onClick={() => graphRef.current?.zoom(0.15)}
        >
          +
        </Button>
        <Button
          variant="subtle"
          size="compact-sm"
          onClick={() => graphRef.current?.zoomToFit({ padding: 32, maxScale: 1 })}
        >
          Вписать
        </Button>
      </Group>
    </div>
  );
}

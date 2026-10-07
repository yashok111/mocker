import { Graph, Shape } from "@antv/x6";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Button, Group, Text } from "@mantine/core";
import { useDiagramLayout } from "../../diagram/useDiagramLayout";
import { applyDiagramRoutes, measureDiagramLabel } from "../../diagram/elkX6";
import { installCanvasWheelZoom } from "../../diagram/canvasControls";
import { diagramOptions, diagramEdgeLine } from "../../diagram/presentation";
import type { Camera } from "./navigation";
import { kindName, nodeSubtitle, relationNames, type MapNode, type MapEdge } from "./model";
import styles from "./Explorer.module.css";
import { positionSavedScene, preserveAnchorCamera, revealScenarioStart } from "./sceneLayout";
import type { DiagramLayoutResult } from "../../diagram/elkLayout";
import { sourceOverviewLayout, architectureOverviewLayout } from "./overviewLayout";
import { createCardClicks } from "./cardClicks";

Shape.HTML.register({
  shape: "backend-explore-card",
  effect: ["data"],
  html(node) {
    const data = node.getData() as MapNode & { selected?: boolean };
    const button = document.createElement("button");
    button.type = "button";
    button.className = styles.mapCard ?? "";
    button.dataset.objectId = data.id;
    button.dataset.kind = data.kind;
    if (data.change) button.dataset.change = data.change;
    button.dataset.selected = String(!!data.selected);
    button.setAttribute("aria-pressed", String(!!data.selected));
    const type = document.createElement("span");
    type.className = styles.cardType ?? "";
    const icon = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    icon.setAttribute("viewBox", "0 0 24 24");
    icon.setAttribute("width", "14");
    icon.setAttribute("height", "14");
    icon.setAttribute("fill", "none");
    icon.setAttribute("stroke", "currentColor");
    icon.setAttribute("stroke-width", "1.6");
    icon.setAttribute("aria-hidden", "true");
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    path.setAttribute(
      "d",
      ["datastore", "data_store", "table"].includes(data.kind)
        ? "M4 6c0-4 16-4 16 0v12c0 4-16 4-16 0V6Zm0 0c0 4 16 4 16 0M4 12c0 4 16 4 16 0"
        : data.kind === "external_system"
          ? "M13 5h6v6m0-6L9 15M9 5H5v14h14v-4"
          : "M4 4h16v16H4zM4 9h16M9 9v11",
    );
    icon.append(path);
    type.append(icon, document.createTextNode(kindName(data.kind)));
    const title = document.createElement("strong");
    title.className = styles.cardTitle ?? "";
    title.textContent = data.name;
    const description = document.createElement("span");
    description.className = styles.cardDescription ?? "";
    description.textContent = data.description === data.name ? "" : data.description;
    const foot = document.createElement("span");
    foot.className = styles.cardFoot ?? "";
    foot.textContent = data.badge ?? nodeSubtitle(data) ?? "";
    button.append(type, title, description, foot);
    return button;
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
  const nodeById = useMemo(() => new Map(nodes.map((n) => [n.id, n])), [nodes]);
  const edgeById = useMemo(() => new Map(edges.map((e) => [e.id, e])), [edges]);
  const shape = useMemo(
    () => ({
      nodes: nodes.map((n) => ({ id: n.id, width: 248, height: 146 })),
      edges: edges
        .filter((e) => nodeById.has(e.from) && nodeById.has(e.to))
        .map((e) => ({
          id: e.id,
          source: e.from,
          target: e.to,
          label: measureDiagramLabel(e.label || relationNames[e.kind] || e.kind, 150, 2),
        })),
    }),
    [nodes, edges, nodeById],
  );
  const sourceOverview = useMemo(() => sourceOverviewLayout(nodes, edges), [nodes, edges]);
  const overview = useMemo(
    () => sourceOverview ?? architectureOverviewLayout(nodes, edges),
    [sourceOverview, nodes, edges],
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
      graph.zoomToFit({ padding: 32, maxScale: 1, minScale: 0.65 });
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
    return () => {
      clicks.cancel();
      const { tx, ty } = graph.translate();
      initialCamera.current = { x: tx, y: ty, zoom: graph.zoom() };
      previousNodes.current = renderedNodes;
      element.removeEventListener("keydown", keyboard);
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

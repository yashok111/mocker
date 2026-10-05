import { Graph } from "@antv/x6";
import { useEffect, useMemo, useRef } from "react";
import { Stack, Text } from "@mantine/core";
import type {
  BackendArchitectureElement,
  BackendArchitectureLink,
  BackendDiagramViewState,
} from "@/api/generated/schemas";
import DiagramViewport from "../diagram/DiagramViewport";
import { useDiagramLayout } from "../diagram/useDiagramLayout";
import { applyDiagramRoutes, measureDiagramLabel } from "../diagram/elkX6";
import { createInitialFit } from "../diagram/initialFit";
import { installCanvasWheelZoom } from "../diagram/canvasControls";
import {
  diagramCardBody,
  diagramEdgeLine,
  diagramOptions,
  diagramFontFamily,
} from "../diagram/presentation";
import styles from "./BackendFlowGraph.module.css";
export function BackendArchitectureGraph({
  elements,
  links,
  state,
  onSelect,
}: {
  elements: BackendArchitectureElement[];
  links: BackendArchitectureLink[];
  state: BackendDiagramViewState;
  onSelect: (selection: NonNullable<BackendDiagramViewState["selection"]>) => void;
}) {
  const visible = useMemo(
    () => elements.filter((e) => !state.collapsedIds.includes(e.id)).slice(0, 200),
    [elements, state.collapsedIds],
  );
  const input = useMemo(() => {
    const ids = new Set(visible.map((e) => e.id));
    return {
      nodes: visible.map((e) => ({ id: e.id, width: 260, height: 120 })),
      edges: links
        .filter((e) => ids.has(e.from) && ids.has(e.to))
        .slice(0, 600)
        .map((e) => ({
          id: e.id,
          source: e.from,
          target: e.to,
          label: measureDiagramLabel(e.relation),
        })),
    };
  }, [visible, links]);
  const geometry = useDiagramLayout(input);
  const host = useRef<HTMLElement>(null);
  const graphRef = useRef<Graph | null>(null);
  const callback = useRef(onSelect);
  useEffect(() => {
    callback.current = onSelect;
  });
  useEffect(() => {
    if (!host.current || !geometry.layout) return;
    const graph = new Graph({
      container: host.current,
      ...diagramOptions(),
      async: false,
      interacting: false,
    });
    graphRef.current = graph;
    const wheel = installCanvasWheelZoom(graph, host.current);
    const fit = createInitialFit(graph, host.current);
    for (const node of geometry.layout.nodes) {
      const e = visible.find((e) => e.id === node.id)!;
      const pos = state.positions.find((p) => p.id === node.id);
      graph.addNode({
        ...node,
        ...(pos ? { x: pos.x, y: pos.y } : {}),
        shape: e.role === "person" ? "ellipse" : "rect",
        label: `${e.label}\n${e.role}\n${e.origin.kind === "authored" ? "Авторское утверждение" : "Исходный код"}`,
        attrs: {
          body: diagramCardBody(),
          label: {
            fill: "#213728",
            fontSize: 13,
            fontFamily: diagramFontFamily,
            textWrap: { width: 240, height: 104, ellipsis: true },
          },
        },
      });
    }
    for (const e of geometry.layout.edges)
      graph.addEdge({
        id: e.id,
        source: e.source,
        target: e.target,
        attrs: { line: diagramEdgeLine() },
      });
    if (state.positions.length === 0) applyDiagramRoutes(graph, geometry.layout);
    graph.on("node:click", ({ node }) => callback.current({ type: "element", id: node.id }));
    graph.on("edge:click", ({ edge }) => callback.current({ type: "link", id: edge.id }));
    graph.on("resize", () => fit());
    fit();
    return () => {
      wheel();
      graph.dispose();
      graphRef.current = null;
    };
  }, [geometry.layout, visible, state.positions]);
  return (
    <Stack gap="xs" style={{ minWidth: 0 }}>
      <Text size="sm" component="output">
        Canvas: {visible.length}/200 элементов, {input.edges.length}/600 связей. Списки и поиск
        охватывают всю проекцию; скрытые объекты остаются доступны.
      </Text>
      {geometry.error && <Text>{geometry.error} Используйте списки.</Text>}
      <DiagramViewport
        hostRef={host}
        graphRef={graphRef}
        className={styles.host}
        ariaLabel="C4 выбранных страниц; клавиатурная альтернатива в списках"
        zoomLabel="C4"
      />
    </Stack>
  );
}

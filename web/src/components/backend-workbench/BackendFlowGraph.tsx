import { Graph } from "@antv/x6";
import { useEffect, useMemo, useRef } from "react";
import { Alert, Button, Stack, Text } from "@mantine/core";
import type { BackendNode, BackendEdge } from "@/api/generated/schemas";
import DiagramViewport from "../diagram/DiagramViewport";
import { useDiagramLayout } from "../diagram/useDiagramLayout";
import { applyDiagramRoutes } from "../diagram/elkX6";
import { createInitialFit } from "../diagram/initialFit";
import { installCanvasWheelZoom } from "../diagram/canvasControls";
import {
  diagramCardBody,
  diagramEdgeLine,
  diagramFontFamily,
  diagramOptions,
} from "../diagram/presentation";
import { buildFlowScene, flowStepContext, flowTransitionLabel } from "./backendFlowLayout";
import { databaseButtonStyles } from "./backendDatabaseReads";
import type { FlowSelection } from "./backendFlowReads";
import styles from "./BackendFlowGraph.module.css";

export function BackendFlowGraph({
  nodes,
  edges,
  onSelect,
}: {
  nodes: BackendNode[];
  edges: BackendEdge[];
  onSelect: (selection: FlowSelection) => void;
}) {
  const scene = useMemo(() => buildFlowScene(nodes, edges), [nodes, edges]);
  const geometry = useDiagramLayout(scene.input);
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
      connecting: { allowBlank: false, allowEdge: false, allowNode: false },
      translating: { restrict: true },
    });
    graphRef.current = graph;
    const stopWheel = installCanvasWheelZoom(graph, host.current);
    const fit = createInitialFit(graph, host.current);
    for (const node of geometry.layout.nodes) {
      const record = scene.nodes.find((item) => item.id === node.id)!;
      const stepKind = "stepKind" in record.attributes ? record.attributes.stepKind : record.kind;
      graph.addNode({
        ...node,
        shape: "rect",
        label: [record.name, stepKind, ...flowStepContext(record)].join("\n"),
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
    for (const edge of geometry.layout.edges)
      graph.addEdge({
        id: edge.id,
        source: edge.source,
        target: edge.target,
        attrs: { line: diagramEdgeLine() },
      });
    applyDiagramRoutes(graph, geometry.layout);
    graph.on("node:click", ({ node }) => callback.current({ type: "node", id: node.id }));
    graph.on("edge:click", ({ edge }) => callback.current({ type: "edge", id: edge.id }));
    graph.on("resize", () => fit());
    fit();
    return () => {
      stopWheel();
      graph.dispose();
      graphRef.current = null;
    };
  }, [geometry.layout, scene]);
  return (
    <Stack gap="xs" style={{ minWidth: 0 }}>
      <Text size="sm" component="output">
        Canvas текущих страниц: {scene.nodes.length} шагов, {scene.edges.length} переходов. За
        пределами canvas: {scene.excludedNodes} шагов по лимиту, {scene.boundaries.length}{" "}
        переходов. Списки выше содержат все записи текущих страниц; другие страницы загружаются
        отдельно.
      </Text>
      {scene.boundaries.map((edge) => (
        <Button
          key={edge.id}
          variant="subtle"
          h="auto"
          styles={databaseButtonStyles}
          onClick={() => onSelect({ type: "edge", id: edge.id })}
        >
          Граница canvas: {flowTransitionLabel(edge)} · {edge.from} → {edge.to}
        </Button>
      ))}
      {geometry.pending && (
        <Text component="output" aria-live="polite">
          Рассчитываем расположение Flow
        </Text>
      )}
      {geometry.error && (
        <Alert color="yellow">{geometry.error} Шаги и переходы доступны в списках.</Alert>
      )}
      <DiagramViewport
        hostRef={host}
        graphRef={graphRef}
        className={styles.host}
        ariaLabel="Flow выбранных страниц; клавиатурная альтернатива в списках"
        zoomLabel="Flow"
      />
    </Stack>
  );
}

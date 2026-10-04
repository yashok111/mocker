import { Graph } from "@antv/x6";
import { useEffect, useMemo, useRef, useState } from "react";
import { Alert, Button, Stack, Text } from "@mantine/core";
import type {
  ProjectionNode as BackendNode,
  ProjectionEdge as BackendEdge,
} from "./backendEffectiveProjectionReads";
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

import type { SavedLayoutProps } from "./backendSavedViewState";
import { useSavedViewLayout, collapseFlowScene } from "./backendSavedViewLayout";
import { SavedViewLayoutControls } from "./BackendSavedViews";

export function BackendFlowGraph({
  nodes,
  edges,
  onSelect,
  positions: externalPositions,
  collapsedGroupIds = [],
  onPositionsChange,
  onPreview,
}: Partial<SavedLayoutProps> & {
  onPreview?: (value: boolean) => void;
  nodes: BackendNode[];
  edges: BackendEdge[];
  onSelect: (selection: FlowSelection) => void;
}) {
  const collapsed = useMemo(
    () => collapseFlowScene(nodes, edges, collapsedGroupIds),
    [nodes, edges, collapsedGroupIds],
  );
  const scene = useMemo(() => buildFlowScene(collapsed.nodes, collapsed.edges), [collapsed]);
  const geometry = useDiagramLayout(scene.input);
  const [localPositions, setLocalPositions] = useState<SavedLayoutProps["positions"]>([]);
  const layout = useSavedViewLayout(
    geometry.layout,
    externalPositions ?? localPositions,
    onPositionsChange ?? setLocalPositions,
    onPreview,
  );
  const movement = useRef(layout.move);
  useEffect(() => {
    movement.current = layout.move;
  });
  const host = useRef<HTMLElement>(null);
  const graphRef = useRef<Graph | null>(null);
  const callback = useRef(onSelect);
  useEffect(() => {
    callback.current = onSelect;
  });
  useEffect(() => {
    if (!host.current || !layout.display) return;
    const graph = new Graph({
      container: host.current,
      ...diagramOptions(),
      async: false,
      interacting: {
        nodeMovable: !layout.preview,
        edgeMovable: false,
        edgeLabelMovable: false,
        arrowheadMovable: false,
        vertexMovable: false,
        vertexAddable: false,
        vertexDeletable: false,
        magnetConnectable: false,
      },
      connecting: { allowBlank: false, allowEdge: false, allowNode: false },
      translating: { restrict: false },
    });
    graphRef.current = graph;
    const stopWheel = installCanvasWheelZoom(graph, host.current);
    const fit = createInitialFit(graph, host.current);
    for (const node of layout.display.nodes) {
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
    for (const edge of layout.display.edges)
      graph.addEdge({
        id: edge.id,
        source: edge.source,
        target: edge.target,
        attrs: { line: diagramEdgeLine() },
      });
    applyDiagramRoutes(graph, layout.display);
    graph.on("node:click", ({ node }) => callback.current({ type: "node", id: node.id }));
    graph.on("edge:click", ({ edge }) => callback.current({ type: "edge", id: edge.id }));
    graph.on("node:moved", ({ node }) => {
      const point = node.position();
      if (!movement.current(node.id, point.x, point.y)) {
        const previous = layout.display?.nodes.find((item) => item.id === node.id);
        if (previous) {
          node.position(previous.x, previous.y);
          applyDiagramRoutes(graph, layout.display!);
        }
      }
    });
    graph.on("resize", () => fit());
    fit();
    return () => {
      stopWheel();
      graph.dispose();
      graphRef.current = null;
    };
  }, [layout.display, scene, layout.preview]);
  return (
    <Stack gap="xs" style={{ minWidth: 0 }}>
      <SavedViewLayoutControls
        layout={layout}
        label="Flow"
        names={new Map(scene.nodes.map((node) => [node.id, node.name]))}
      />
      <Text size="sm" component="output">
        Свёрнуто на текущих страницах: {collapsed.hiddenNodes} карточек, {collapsed.hiddenEdges}{" "}
        связей. Списки и инспектор сохраняют все записи; координаты других страниц сохранены.
      </Text>
      <Text size="sm" component="output">
        Canvas текущих страниц: {scene.nodes.length} шагов, {scene.edges.length} переходов. За
        пределами canvas: {scene.excludedNodes} шагов по лимиту, {scene.excludedEdges} переходов по
        лимиту, {scene.excludedEndpoints} переходов без обоих шагов на текущем canvas. Списки выше
        содержат все записи текущих страниц; другие страницы загружаются отдельно.
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

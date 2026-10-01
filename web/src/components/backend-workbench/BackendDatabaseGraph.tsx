import { Graph } from "@antv/x6";
import { useEffect, useMemo, useRef } from "react";
import { Alert, Stack, Text } from "@mantine/core";
import type {
  BackendDatabaseRelationshipItem,
  BackendDatabaseTableItem,
} from "@/api/generated/schemas";
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
import { buildDatabaseScene } from "./backendDatabaseLayout";
import { databaseStatus, type DatabaseSelection } from "./backendDatabaseReads";
import styles from "./BackendDatabaseGraph.module.css";

export function BackendDatabaseGraph({
  tables,
  relationships,
  onSelect,
}: {
  tables: BackendDatabaseTableItem[];
  relationships: BackendDatabaseRelationshipItem[];
  onSelect: (selection: DatabaseSelection) => void;
}) {
  const scene = useMemo(() => buildDatabaseScene(tables, relationships), [tables, relationships]);
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
      const table = scene.tables.find((table) => table.tableId === node.id)!;
      graph.addNode({
        ...node,
        shape: "rect",
        label: `${table.qualifiedName}\nКолонок: ${table.columnCount}\n${databaseStatus(table.driftStatus)}`,
        attrs: {
          body: diagramCardBody(),
          label: {
            fill: "#213728",
            fontSize: 13,
            fontFamily: diagramFontFamily,
            textWrap: { width: 224, height: 80, ellipsis: true },
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
        Canvas текущих страниц: {scene.input.nodes.length} таблиц, {scene.input.edges.length} FK. За
        пределами canvas: {scene.excludedTables} таблиц по лимиту, {scene.excludedEdges} FK по
        лимиту, {scene.excludedEndpoints} FK без обеих таблиц в canvas. Полные строки доступны в
        списках.
      </Text>
      {geometry.pending && <Text component="output">Рассчитываем расположение ER</Text>}
      {geometry.error && (
        <Alert color="yellow">{geometry.error} Таблицы и связи доступны выше.</Alert>
      )}
      <DiagramViewport
        hostRef={host}
        graphRef={graphRef}
        className={styles.host}
        ariaLabel="Схема таблиц и внешних ключей; клавиатурная альтернатива в списках"
        zoomLabel="ER схему"
      />
    </Stack>
  );
}

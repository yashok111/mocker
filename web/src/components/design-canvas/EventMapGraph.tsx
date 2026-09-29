import { Graph } from "@antv/x6";
import { useEffect, useMemo, useRef } from "react";
import { Alert, Text } from "@mantine/core";
import type { DesignScenarioEventMapReport } from "@/api/generated/schemas";
import DiagramViewport from "../diagram/DiagramViewport";
import { installCanvasWheelZoom } from "../diagram/canvasControls";
import { createInitialFit } from "../diagram/initialFit";
import {
  diagramCardBody,
  diagramEdgeLine,
  diagramFontFamily,
  diagramOptions,
} from "../diagram/presentation";
import styles from "./EventMap.module.css";
import type { GraphSubset } from "./eventMapSubset";
import { useDiagramLayout } from "../diagram/useDiagramLayout";
import { diagramRouteOptions } from "../diagram/elkX6";
import { eventMapPresentation } from "./eventMapPresentation";
import { diagnosticColor, diagnosticMarkers, type DiagnosticSeverity } from "./eventMapDiagnostics";

function nodeBody(selected: boolean, severity?: DiagnosticSeverity) {
  return {
    ...diagramCardBody(selected),
    stroke: diagnosticColor(severity) ?? (selected ? "#315b3a" : "#abbcac"),
  };
}

function edgeLine(
  selected: boolean,
  kind: GraphSubset["edges"][number]["kind"],
  severity?: DiagnosticSeverity,
) {
  return {
    ...diagramEdgeLine(selected),
    stroke:
      diagnosticColor(severity) ??
      (kind === "dead_letter"
        ? "#a0444a"
        : kind === "retry"
          ? "#a16e2f"
          : selected
            ? "#245e39"
            : "#66816c"),
    strokeDasharray: kind === "retry" || kind === "dead_letter" ? "5 4" : undefined,
  };
}

export default function EventMapGraph({
  report,
  subset,
  selectedId,
  onSelect,
}: {
  report: DesignScenarioEventMapReport;
  subset: GraphSubset;
  selectedId: string;
  onSelect: (id: string) => void;
}) {
  const host = useRef<HTMLElement>(null);
  const graphRef = useRef<Graph | null>(null);
  const callback = useRef(onSelect);
  const markers = useMemo(() => diagnosticMarkers(report.diagnostics), [report.diagnostics]);
  const presentation = useMemo(() => eventMapPresentation(subset, markers), [subset, markers]);
  const { layout: result, error } = useDiagramLayout(presentation.input);
  useEffect(() => {
    callback.current = onSelect;
  }, [onSelect]);
  const initialFit = useRef<ReturnType<typeof createInitialFit> | null>(null);
  useEffect(() => {
    if (!host.current) return;
    const graph = new Graph({
      container: host.current,
      ...diagramOptions(),
      async: false,
      interacting: false,
    });
    graphRef.current = graph;
    const removeWheelZoom = installCanvasWheelZoom(graph, host.current);
    initialFit.current = createInitialFit(graph, host.current, 28);
    graph.on("node:click", ({ node }) => callback.current(node.id));
    graph.on("edge:click", ({ edge }) => callback.current(edge.id));
    return () => {
      removeWheelZoom();
      graph.dispose();
      graphRef.current = null;
      initialFit.current = null;
    };
  }, []);
  useEffect(() => {
    const graph = graphRef.current;
    if (!graph) return;
    graph.clearCells();
    if (!result) return;
    const nodePositions = new Map(result.nodes.map((node) => [node.id, node]));
    const edgePositions = new Map(result.edges.map((edge) => [edge.id, edge]));
    for (const node of subset.nodes) {
      const severity = markers.get(node.id);
      const bounds = nodePositions.get(node.id)!;
      graph.addNode({
        id: node.id,
        shape: "rect",
        zIndex: 1,
        x: bounds.x,
        y: bounds.y,
        width: bounds.width,
        height: bounds.height,
        label: presentation.nodeLabels.get(node.id),
        attrs: {
          body: nodeBody(false, severity),
          label: {
            fill: "#24452b",
            fontSize: 12,
            fontWeight: 600,
            fontFamily: diagramFontFamily,
            lineHeight: 16,
          },
        },
      });
    }
    for (const edge of subset.edges) {
      const severity = markers.get(edge.id);
      const route = edgePositions.get(edge.id)!;
      graph.addEdge({
        id: edge.id,
        zIndex: 0,
        ...diagramRouteOptions(result, route),
        attrs: {
          line: edgeLine(false, edge.kind, severity),
        },
      });
    }
    initialFit.current?.(`${report.scenarioId}:${report.revisionId ?? "proposed"}`);
  }, [report.scenarioId, report.revisionId, subset, markers, presentation, result]);
  useEffect(() => {
    const graph = graphRef.current;
    if (!graph) return;
    for (const node of subset.nodes) {
      graph
        .getCellById(node.id)
        ?.attr("body", nodeBody(node.id === selectedId, markers.get(node.id)));
    }
    for (const edge of subset.edges) {
      graph
        .getCellById(edge.id)
        ?.attr("line", edgeLine(edge.id === selectedId, edge.kind, markers.get(edge.id)));
    }
  }, [selectedId, subset, markers, result]);
  return (
    <div>
      {error ? (
        <Alert color="red" role="alert">
          Не удалось расположить карту. Узлы и связи доступны в полном списке ниже.
        </Alert>
      ) : !result ? (
        <Text component="output" size="sm">
          Располагаем узлы и связи…
        </Text>
      ) : null}
      <DiagramViewport
        hostRef={host}
        graphRef={graphRef}
        className={styles.graph}
        ariaHidden
        zoomLabel="карту событий"
        fitPadding={28}
      />
    </div>
  );
}

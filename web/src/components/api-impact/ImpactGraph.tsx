import { Graph } from "@antv/x6";
import { useEffect, useRef } from "react";
import { Alert, Text } from "@mantine/core";
import { canvasTextMeasurer, wrapCanvasText } from "../design-canvas/canvasText";
import DiagramViewport from "../diagram/DiagramViewport";
import { installCanvasWheelZoom } from "../diagram/canvasControls";
import { createInitialFit } from "../diagram/initialFit";
import {
  diagramCardBody,
  diagramEdgeLine,
  diagramFontFamily,
  diagramOptions,
} from "../diagram/presentation";
import type { ImpactGraphModel } from "./model";
import { impactGraphLabel } from "./graphLabels";
import styles from "./Impact.module.css";
import { useDiagramLayout } from "../diagram/useDiagramLayout";
import { diagramRouteOptions } from "../diagram/elkX6";
import { impactLayoutInput } from "./layout";

export default function ImpactGraph({
  model,
  changeId,
}: {
  model: ImpactGraphModel;
  changeId: string;
}) {
  const { layout, pending, error } = useDiagramLayout(impactLayoutInput(model));
  const host = useRef<HTMLElement>(null);
  const graphRef = useRef<Graph | null>(null);
  const fitRef = useRef<ReturnType<typeof createInitialFit> | null>(null);
  const identity = useRef(changeId);
  useEffect(() => {
    identity.current = changeId;
  }, [changeId]);
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
    const fit = createInitialFit(graph, host.current, 36);
    fitRef.current = fit;
    graph.on("resize", () => fit(identity.current));
    return () => {
      removeWheelZoom();
      graph.dispose();
      graphRef.current = null;
      fitRef.current = null;
    };
  }, []);
  useEffect(() => {
    const graph = graphRef.current;
    if (!graph) return;
    graph.clearCells();
    if (!layout) return;
    const positions = new Map(layout.nodes.map((node) => [node.id, node]));
    const routes = new Map(layout.edges.map((edge) => [edge.id, edge]));
    const measure = canvasTextMeasurer(12, 400, diagramFontFamily);
    for (const node of model.nodes) {
      const lines = wrapCanvasText(impactGraphLabel(node.label), 180, measure);
      graph.addNode({
        ...node,
        ...positions.get(node.id)!,
        zIndex: 1,
        shape: "rect",
        markup: [
          { tagName: "title", textContent: node.label },
          { tagName: "rect", selector: "body" },
          { tagName: "text", selector: "label" },
        ],
        attrs: {
          body: diagramCardBody(node.id === "change"),
          label: {
            text: lines.slice(0, 4).join("\n") + (lines.length > 4 ? "…" : ""),
            fontFamily: diagramFontFamily,
            fontSize: 12,
            fill: "#24452b",
          },
        },
      });
    }
    for (const edge of model.edges) {
      graph.addEdge({
        id: edge.id,
        ...diagramRouteOptions(layout, routes.get(edge.id)!),
        attrs: { line: diagramEdgeLine() },
      });
    }
    fitRef.current?.(changeId);
  }, [model, changeId, layout]);
  return (
    <div>
      {error ? (
        <Alert color="red" role="alert">
          Не удалось расположить граф. Связи доступны в списке доказательств.
        </Alert>
      ) : pending ? (
        <Text component="output" size="sm">
          Располагаем граф влияния…
        </Text>
      ) : null}
      <DiagramViewport
        hostRef={host}
        graphRef={graphRef}
        className={styles.graph}
        ariaHidden
        zoomLabel="граф влияния"
        fitLabel="Уместить граф"
        fitPadding={36}
      />
    </div>
  );
}

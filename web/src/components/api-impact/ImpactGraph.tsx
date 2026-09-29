import { Graph } from "@antv/x6";
import { useEffect, useRef } from "react";
import { canvasTextMeasurer, wrapCanvasText } from "../design-canvas/canvasText";
import DiagramViewport from "../diagram/DiagramViewport";
import { createInitialFit } from "../diagram/initialFit";
import {
  diagramCardBody,
  diagramEdgeLabel,
  diagramEdgeLine,
  diagramFontFamily,
  diagramOptions,
} from "../diagram/presentation";
import type { ImpactGraphModel } from "./model";
import { impactGraphLabel } from "./graphLabels";
import styles from "./Impact.module.css";

export default function ImpactGraph({
  model,
  changeId,
}: {
  model: ImpactGraphModel;
  changeId: string;
}) {
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
    const fit = createInitialFit(graph, host.current, 36);
    fitRef.current = fit;
    graph.on("resize", () => fit(identity.current));
    return () => {
      graph.dispose();
      graphRef.current = null;
      fitRef.current = null;
    };
  }, []);
  useEffect(() => {
    const graph = graphRef.current;
    if (!graph) return;
    graph.clearCells();
    const measure = canvasTextMeasurer(12, 400, diagramFontFamily);
    for (const node of model.nodes) {
      const lines = wrapCanvasText(impactGraphLabel(node.label), 180, measure);
      graph.addNode({
        ...node,
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
        source: { cell: edge.source, anchor: "right", connectionPoint: "boundary" },
        target: { cell: edge.target, anchor: "left", connectionPoint: "boundary" },
        vertices: edge.vertices,
        router: { name: "normal" },
        connector: { name: "rounded", args: { radius: 6 } },
        attrs: { line: diagramEdgeLine() },
        labels: edge.label
          ? [
              diagramEdgeLabel(edge.label, {
                position: edge.labelPosition,
                maxWidth: edge.labelMaxWidth,
                maxHeight: edge.labelMaxHeight,
              }),
            ]
          : [],
      });
    }
    fitRef.current?.(changeId);
  }, [model, changeId]);
  return (
    <DiagramViewport
      hostRef={host}
      graphRef={graphRef}
      className={styles.graph}
      ariaHidden
      zoomLabel="граф влияния"
      fitLabel="Уместить граф"
      fitPadding={36}
    />
  );
}

import { Graph } from "@antv/x6";
import { useEffect, useRef } from "react";
import DiagramViewport from "../diagram/DiagramViewport";
import { createInitialFit } from "../diagram/initialFit";
import {
  diagramCardBody,
  diagramEdgeLabel,
  diagramEdgeLine,
  diagramFontFamily,
  diagramOptions,
} from "../diagram/presentation";
import type { Model } from "./model";
import { resourceRelationRoute } from "./routing";
import styles from "./ResourceMap.module.css";

const CARD_WIDTH = 220;
const CARD_PADDING = 16;
const ROW_HEIGHT = 24;

function updateRoutes(graph: Graph) {
  const nodes = graph.getNodes();
  graph.batchUpdate("resource-routes", () => {
    for (const edge of graph.getEdges()) {
      const source = edge.getSourceNode();
      const target = edge.getTargetNode();
      if (!source || !target) continue;
      const data = edge.getData<{ label: string; reciprocal: boolean }>();
      const route = resourceRelationRoute(
        source.getBBox(),
        target.getBBox(),
        nodes.filter((node) => node !== source && node !== target).map((node) => node.getBBox()),
        data.reciprocal,
      );
      edge.setSource({
        cell: source.id,
        anchor: { name: route.sourceSide, args: route.sourceOffset },
        connectionPoint: "anchor",
      });
      edge.setTarget({
        cell: target.id,
        anchor: { name: route.targetSide, args: route.targetOffset },
        connectionPoint: "anchor",
      });
      edge.setVertices(route.vertices ?? route.waypoints ?? []);
      edge.setRouter(
        route.vertices === undefined
          ? {
              name: "manhattan",
              args: {
                padding: 24,
                startDirections: [route.sourceSide],
                endDirections: [route.targetSide],
              },
            }
          : { name: "normal" },
      );
      edge.setLabels(
        data.label
          ? [
              diagramEdgeLabel(data.label, {
                position: route.labelPosition,
                maxWidth: route.labelMaxWidth,
                maxHeight: 48,
              }),
            ]
          : [],
      );
    }
  });
}

type Props = {
  model: Model;
  selectedId?: string;
  disabled: boolean;
  onSelect: (id: string) => void;
  onMove: (id: string, x: number, y: number) => void;
};
export default function ResourceGraph(props: Props) {
  const host = useRef<HTMLElement>(null);
  const graphRef = useRef<Graph | null>(null);
  const current = useRef(props);
  const initialFit = useRef<ReturnType<typeof createInitialFit> | null>(null);
  useEffect(() => {
    current.current = props;
  });
  useEffect(() => {
    if (!host.current) return;
    const graph = new Graph({
      container: host.current,
      ...diagramOptions(0.2),
      // Rebuilds reuse cell IDs; remove old SVG views before adding replacements.
      async: false,
      interacting: () => ({
        nodeMovable: !current.current.disabled,
        edgeMovable: false,
        arrowheadMovable: false,
        vertexMovable: false,
        vertexAddable: false,
        vertexDeletable: false,
      }),
    });
    graphRef.current = graph;
    const fit = createInitialFit(graph, host.current, 36);
    initialFit.current = fit;
    graph.on("node:click", ({ node }) =>
      current.current.onSelect(node.getData().resourceId as string),
    );
    graph.on("node:moved", ({ node }) => {
      const { x, y } = node.position();
      current.current.onMove(node.getData().resourceId as string, Math.round(x), Math.round(y));
    });
    // Re-evaluate sides and obstacles on live movement, before position saving.
    graph.on("node:change:position", () => updateRoutes(graph));
    graph.on("resize", () => {
      fit();
    });
    return () => {
      graph.dispose();
      graphRef.current = null;
      initialFit.current = null;
    };
  }, []);
  useEffect(() => {
    const graph = graphRef.current;
    if (!graph) return;
    graph.clearCells();
    for (const resource of props.model.resources) {
      const operationRows = props.model.operations.filter((operation) =>
        resource.operationKeys.includes(operation.key),
      );
      const selected = props.selectedId === resource.id;
      const shownOperations = operationRows.slice(0, 4);
      const rows = Math.max(1, shownOperations.length) + (operationRows.length > 4 ? 1 : 0);
      const text = {
        textAnchor: "start",
        textVerticalAnchor: "middle",
        fontFamily: diagramFontFamily,
      };
      graph.addNode({
        id: `resource:${resource.id}`,
        data: { resourceId: resource.id },
        shape: "rect",
        x: resource.x,
        y: resource.y,
        width: CARD_WIDTH,
        height: 76 + rows * ROW_HEIGHT,
        markup: [
          { tagName: "rect", selector: "body" },
          { tagName: "text", selector: "title" },
          { tagName: "text", selector: "service" },
          { tagName: "path", selector: "divider" },
          ...shownOperations.flatMap((_, i) => [
            { tagName: "text", selector: `method${i}` },
            { tagName: "text", selector: `path${i}` },
          ]),
          { tagName: "text", selector: "summary" },
        ],
        attrs: {
          body: diagramCardBody(selected),
          title: {
            ...text,
            text: resource.name,
            refX: CARD_PADDING,
            refY: 24,
            fill: "#24452b",
            fontSize: 15,
            fontWeight: 650,
            textWrap: { width: CARD_WIDTH - CARD_PADDING * 2, height: 22, ellipsis: true },
          },
          service: {
            ...text,
            text: resource.service || "Сервис не указан",
            refX: CARD_PADDING,
            refY: 46,
            fill: "#617164",
            fontSize: 12,
            textWrap: { width: CARD_WIDTH - CARD_PADDING * 2, height: 20, ellipsis: true },
          },
          divider: {
            d: `M ${CARD_PADDING} 62 H ${CARD_WIDTH - CARD_PADDING}`,
            stroke: "#e0e7df",
            strokeWidth: 1,
          },
          ...Object.fromEntries(
            shownOperations.flatMap((operation, i) => [
              [
                `method${i}`,
                {
                  ...text,
                  text: operation.method.toUpperCase(),
                  refX: CARD_PADDING,
                  refY: 84 + i * ROW_HEIGHT,
                  fontSize: 10,
                  fontWeight: 650,
                  fill: "#45624d",
                },
              ],
              [
                `path${i}`,
                {
                  ...text,
                  text: operation.path,
                  refX: 66,
                  refY: 84 + i * ROW_HEIGHT,
                  fontSize: 12,
                  fill: "#344638",
                  textWrap: { width: CARD_WIDTH - 66 - CARD_PADDING, height: 20, ellipsis: true },
                },
              ],
            ]),
          ),
          summary: {
            ...text,
            text:
              operationRows.length > 4
                ? `Ещё операций: ${operationRows.length - 4}`
                : operationRows.length === 0
                  ? "Нет операций"
                  : "",
            refX: CARD_PADDING,
            refY: 84 + shownOperations.length * ROW_HEIGHT,
            fontSize: 11,
            fill: "#617164",
          },
        },
      });
    }
    for (const relation of props.model.relations) {
      if (
        !graph.hasCell(`resource:${relation.fromResourceId}`) ||
        !graph.hasCell(`resource:${relation.toResourceId}`)
      )
        continue;
      graph.addEdge({
        id: `relation:${relation.id}`,
        data: {
          label: relation.label,
          reciprocal: props.model.relations.some(
            (other) =>
              other.fromResourceId === relation.toResourceId &&
              other.toResourceId === relation.fromResourceId,
          ),
        },
        source: `resource:${relation.fromResourceId}`,
        target: `resource:${relation.toResourceId}`,
        connector: { name: "rounded", args: { radius: 6 } },
        attrs: { line: diagramEdgeLine() },
      });
    }
    updateRoutes(graph);
    initialFit.current?.();
  }, [props.model, props.selectedId]);
  return (
    <DiagramViewport
      hostRef={host}
      graphRef={graphRef}
      className={styles.graph}
      ariaHidden
      zoomLabel="карту"
      zoomStep={0.2}
      fitPadding={36}
      fitLabel="Уместить"
    />
  );
}

import { Graph } from "@antv/x6";
import { useEffect, useRef } from "react";
import type { ResponseRuleStep } from "@/api/generated/schemas";
import { canvasTextMeasurer, wrapCanvasText } from "../design-canvas/canvasText";
import DiagramViewport from "../diagram/DiagramViewport";
import { installCanvasWheelZoom } from "../diagram/canvasControls";
import { createInitialFit } from "../diagram/initialFit";
import type { DiagramLayoutResult } from "../diagram/elkLayout";
import { applyDiagramRoutes } from "../diagram/elkX6";
import {
  diagramCardBody,
  diagramEdgeLabel,
  diagramEdgeLine,
  diagramFontFamily,
  diagramOptions,
} from "../diagram/presentation";
import { buildRoutes, CARD_HEIGHT, CARD_WIDTH, nodeSummary, portY } from "./layout";
import { ports, type GraphSelection, type ResponseRule, type ResponseRuleEdge } from "./model";
import styles from "./ResponseRules.module.css";

type Props = {
  rule: ResponseRule;
  layout?: DiagramLayoutResult | null;
  fitRequest?: number;
  selection: GraphSelection;
  trace?: ResponseRuleStep[];
  blocked?: boolean;
  onSelect: (selection: GraphSelection) => void;
  onMove: (positions: { nodeId: string; x: number; y: number }[]) => void;
  onConnect: (from: string, port: ResponseRuleEdge["port"], to: string) => void;
};

export default function ResponseRuleGraph(props: Props) {
  const host = useRef<HTMLElement>(null);
  const graphRef = useRef<Graph | null>(null);
  const current = useRef(props);
  const fitRef = useRef<ReturnType<typeof createInitialFit> | null>(null);
  const appliedLayout = useRef<DiagramLayoutResult | null>(null);
  const previousFitRequest = useRef(props.fitRequest);
  useEffect(() => {
    current.current = props;
  });
  useEffect(() => {
    if (!host.current) return;
    const graph = new Graph({
      container: host.current,
      ...diagramOptions(),
      async: false,
      interacting: () => ({
        nodeMovable: !current.current.blocked,
        edgeMovable: false,
        edgeLabelMovable: false,
        arrowheadMovable: false,
        vertexMovable: false,
        vertexAddable: false,
        vertexDeletable: false,
      }),
      connecting: {
        allowBlank: false,
        allowNode: false,
        allowEdge: false,
        allowLoop: false,
        snap: true,
        highlight: true,
        validateMagnet: ({ magnet }) =>
          !current.current.blocked && magnet.getAttribute("port") !== "in",
        validateConnection: ({ targetMagnet }) =>
          !current.current.blocked && targetMagnet?.getAttribute("port") === "in",
        createEdge: () =>
          graph.createEdge({
            router: { name: "manhattan" },
            connector: { name: "rounded" },
            attrs: { line: diagramEdgeLine() },
          }),
      },
    });
    graphRef.current = graph;
    const removeWheelZoom = installCanvasWheelZoom(graph, host.current);
    const fit = createInitialFit(graph, host.current, 48);
    fitRef.current = fit;
    graph.on("resize", () => fit(current.current.rule.id));
    graph.on("node:click", ({ node }) =>
      current.current.onSelect({ kind: "node", id: node.id.slice(5) }),
    );
    graph.on("edge:click", ({ edge }) => {
      if (edge.id.startsWith("edge:"))
        current.current.onSelect({ kind: "edge", id: edge.id.slice(5) });
    });
    graph.on("blank:click", () => current.current.onSelect(null));
    graph.on("node:moved", ({ node }) => {
      if (current.current.blocked) return;
      const p = node.position();
      current.current.onMove([
        {
          nodeId: node.id.slice(5),
          x: Math.max(-100000, Math.min(100000, Math.round(p.x))),
          y: Math.max(-100000, Math.min(100000, Math.round(p.y))),
        },
      ]);
    });
    graph.on("edge:connected", ({ edge, isNew }) => {
      if (!isNew) return;
      const from = edge.getSourceCellId(),
        to = edge.getTargetCellId(),
        port = edge.getSourcePortId();
      graph.removeEdge(edge);
      if (current.current.blocked || !from?.startsWith("node:") || !to?.startsWith("node:")) return;
      if (
        port === "next" ||
        port === "true" ||
        port === "false" ||
        port === "found" ||
        port === "missing"
      )
        current.current.onConnect(from.slice(5), port, to.slice(5));
    });
    return () => {
      removeWheelZoom();
      graph.dispose();
      graphRef.current = null;
      fitRef.current = null;
    };
  }, []);
  const { rule, selection, trace } = props;
  useEffect(() => {
    const graph = graphRef.current;
    if (!graph) return;
    graph.clearCells();
    const activeNodes = new Set(trace?.map((step) => step.nodeId));
    const activeEdges = new Set(trace?.flatMap((step) => (step.edgeId ? [step.edgeId] : [])));
    const measure = canvasTextMeasurer(14, 400, diagramFontFamily);
    for (const node of rule.nodes) {
      const selected = selection?.kind === "node" && selection.id === node.id;
      const active = activeNodes.has(node.id);
      const summary = nodeSummary(node);
      const lines = [
        ...wrapCanvasText(node.name || "Без названия", CARD_WIDTH - 36, measure).slice(0, 2),
        ...wrapCanvasText(summary, CARD_WIDTH - 36, measure),
      ];
      graph.addNode({
        id: `node:${node.id}`,
        shape: "rect",
        x: node.x,
        y: node.y,
        width: CARD_WIDTH,
        height: CARD_HEIGHT,
        zIndex: 1,
        markup: [
          {
            tagName: "title",
            textContent: `${node.name}\n${summary}${active ? "\nВ выбранном пути" : ""}`,
          },
          { tagName: "rect", selector: "body" },
          { tagName: "text", selector: "label" },
        ],
        attrs: {
          body: {
            ...diagramCardBody(selected),
            ...(active
              ? { fill: "#e5f2e1", stroke: "#387147", strokeWidth: selected ? 3 : 2 }
              : {}),
            ...(node.type === "start" || node.type === "fallback" ? { rx: 24, ry: 24 } : {}),
          },
          label: {
            text: `${active ? "✓ " : ""}${lines.slice(0, 4).join("\n")}${lines.length > 4 ? "…" : ""}`,
            fill: "#24452b",
            fontFamily: diagramFontFamily,
            fontSize: 14,
            lineHeight: 20,
          },
        },
        ports: {
          groups: {
            input: {
              position: "absolute",
              attrs: { circle: { r: 4, fill: "#fff", stroke: "#66816c", magnet: "passive" } },
            },
            output: {
              position: "absolute",
              attrs: { circle: { r: 4, fill: "#fff", stroke: "#387147", magnet: true } },
            },
          },
          items: [
            ...(node.type === "start"
              ? []
              : [{ id: "in", group: "input", args: { x: 0, y: CARD_HEIGHT / 2 } }]),
            ...ports(node).map((port) => ({
              id: port,
              group: "output",
              args: { x: CARD_WIDTH, y: portY(node, port) - node.y },
            })),
          ],
        },
      });
    }
    for (const route of buildRoutes(rule)) {
      const source = rule.nodes.find((node) => node.id === route.from)!;
      const validPort = ports(source).includes(route.port);
      const selected = selection?.kind === "edge" && selection.id === route.id;
      const active = activeEdges.has(route.id);
      graph.addEdge({
        id: `edge:${route.id}`,
        zIndex: 0,
        source: validPort ? { cell: `node:${route.from}`, port: route.port } : route.points[0],
        target:
          rule.nodes.find((node) => node.id === route.to)?.type === "start"
            ? route.points.at(-1)
            : { cell: `node:${route.to}`, port: "in" },
        vertices: route.vertices,
        router: route.router
          ? {
              name: "manhattan",
              args: {
                startDirections: ["right"],
                endDirections: ["left"],
                padding: 0,
                snapToGrid: false,
              },
            }
          : { name: "normal" },
        connector: { name: "rounded", args: { radius: 8 } },
        attrs: {
          line: {
            ...diagramEdgeLine(selected || active),
            ...(active ? { stroke: "#287b3a", strokeWidth: 3 } : {}),
          },
        },
        labels: route.label
          ? [
              diagramEdgeLabel(route.label.text, {
                maxWidth: route.label.width - 20,
                maxHeight: 16,
                position: {
                  distance: 0,
                  offset: {
                    x: route.label.x + route.label.width / 2 - route.points[0]!.x,
                    y: route.label.y + route.label.height / 2 - route.points[0]!.y,
                  },
                },
              }),
            ]
          : [],
      });
    }
    fitRef.current?.(rule.id);
  }, [rule, selection, trace]);
  useEffect(() => {
    const graph = graphRef.current;
    if (!graph) return;
    // Preserve live drag coordinates while an asynchronous layout finishes.
    const matching = props.layout && applyDiagramRoutes(graph, props.layout);
    if (
      (matching && appliedLayout.current !== props.layout) ||
      previousFitRequest.current !== props.fitRequest
    )
      graph.zoomToFit({ padding: 48, maxScale: 1 });
    previousFitRequest.current = props.fitRequest;
    appliedLayout.current = matching ? props.layout! : null;
  }, [rule, selection, trace, props.layout, props.fitRequest]);
  return (
    <DiagramViewport
      hostRef={host}
      graphRef={graphRef}
      className={styles.graph}
      fitPadding={48}
      ariaLabel={`Правило ${rule.name}. ${rule.nodes.length} блоков. Выбор и редактирование доступны в списке блоков и связей.`}
      zoomLabel="правило"
    />
  );
}

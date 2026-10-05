import { Graph } from "@antv/x6";
import { useEffect, useRef } from "react";
import DiagramViewport from "../diagram/DiagramViewport";
import { installCanvasWheelZoom } from "../diagram/canvasControls";
import { createInitialFit } from "../diagram/initialFit";
import type { DiagramLayoutResult } from "../diagram/elkLayout";
import { applyDiagramRoutes } from "../diagram/elkX6";
import {
  diagramCardBody,
  diagramEdgeLabel,
  diagramEdgeLine,
  diagramOptions,
} from "../diagram/presentation";
import type { StateDiagram, Selection } from "./model";
import styles from "./StateDiagram.module.css";
import { STATE_HEIGHT, STATE_WIDTH, transitionLabel } from "./layout";

// Leave room for labels above horizontal edges when fitting the model bounds.
const FIT_PADDING = 86;
const SIDES = ["top", "right", "bottom", "left"] as const;

type Props = {
  readOnly?: boolean;
  diagram: StateDiagram;
  layout?: DiagramLayoutResult | null;
  selection: Selection;
  activeState?: string;
  onSelect: (s: Selection) => void;
  onMove: (id: string, x: number, y: number) => void;
  onConnect: (from: string, to: string) => void;
};
export default function StateGraph(props: Props) {
  const host = useRef<HTMLElement>(null);
  const graphRef = useRef<Graph | null>(null);
  const current = useRef(props);
  const initialFit = useRef<ReturnType<typeof createInitialFit> | null>(null);
  const appliedLayout = useRef<DiagramLayoutResult | null>(null);
  const { diagram, selection, activeState } = props;
  useEffect(() => {
    current.current = props;
  });
  useEffect(() => {
    if (!host.current) return;
    const graph = new Graph({
      container: host.current,
      ...diagramOptions(),
      // Remove old SVG views before rebuilding states and transitions with the same IDs.
      async: false,
      connecting: {
        allowBlank: false,
        allowEdge: false,
        allowNode: false,
        allowLoop: true,
        snap: true,
        highlight: true,
        createEdge() {
          return graph.createEdge({
            router: { name: "manhattan" },
            connector: { name: "rounded" },
            attrs: { line: diagramEdgeLine() },
          });
        },
      },
      interacting: {
        nodeMovable: () => !current.current.readOnly,
        magnetConnectable: () => !current.current.readOnly,
        edgeMovable: false,
        edgeLabelMovable: false,
        arrowheadMovable: false,
        vertexMovable: false,
        vertexAddable: false,
        vertexDeletable: false,
      },
    });
    graphRef.current = graph;
    const removeWheelZoom = installCanvasWheelZoom(graph, host.current);
    const fit = createInitialFit(graph, host.current, FIT_PADDING);
    initialFit.current = fit;
    graph.on("resize", () => {
      fit(current.current.diagram.id);
    });
    graph.on("node:click", ({ node }) => {
      if (node.id.startsWith("state:"))
        current.current.onSelect({ kind: "state", id: node.id.slice(6) });
    });
    graph.on("edge:click", ({ edge }) => {
      if (edge.id.startsWith("transition:"))
        current.current.onSelect({ kind: "transition", id: edge.id.slice(11) });
    });
    graph.on("blank:click", () => current.current.onSelect(null));
    graph.on("node:moved", ({ node }) => {
      if (current.current.readOnly) return;
      if (node.id.startsWith("state:")) {
        const p = node.position();
        current.current.onMove(node.id.slice(6), Math.round(p.x), Math.round(p.y));
      }
    });
    graph.on("edge:connected", ({ edge, isNew }) => {
      if (!isNew) return;
      if (current.current.readOnly) {
        graph.removeEdge(edge);
        return;
      }
      const from = edge.getSourceCellId();
      const to = edge.getTargetCellId();
      graph.removeEdge(edge);
      if (from?.startsWith("state:") && to?.startsWith("state:"))
        current.current.onConnect(from.slice(6), to.slice(6));
    });
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
    for (const state of diagram.states) {
      const selected = selection?.kind === "state" && selection.id === state.id;
      const active = activeState === state.id;
      graph.addNode({
        id: `state:${state.id}`,
        shape: "rect",
        zIndex: 1,
        x: state.x,
        y: state.y,
        width: STATE_WIDTH,
        height: STATE_HEIGHT,
        label: `${state.name}\n${active ? "● Сейчас · " : ""}${state.id === diagram.initialStateId ? "Начальное" : state.terminal ? "Конечное" : "Состояние"}`,
        attrs: {
          body: {
            ...diagramCardBody(selected),
            ...(active ? { fill: "#e1f0df", ...(selected ? {} : { stroke: "#527b42" }) } : {}),
            strokeWidth: selected || state.terminal ? 3 : 1.5,
            ...(state.terminal ? { rx: 28, ry: 28 } : {}),
          },
          label: {
            fill: "#213728",
            fontSize: 13,
            fontFamily: "inherit",
            textWrap: { width: 166, height: 64, ellipsis: true },
          },
        },
        ports: {
          groups: Object.fromEntries(
            SIDES.map((side) => [
              side,
              {
                position: side,
                attrs: {
                  circle: {
                    r: 4,
                    magnet: state.terminal ? "passive" : true,
                    stroke: "#527b42",
                    fill: "#fff",
                  },
                },
              },
            ]),
          ),
          items: SIDES.map((side) => ({ id: side, group: side })),
        },
      });
    }
    for (const transition of diagram.transitions) {
      const source = diagram.states.find((s) => s.id === transition.from);
      const target = diagram.states.find((s) => s.id === transition.to);
      if (!source || !target) continue;
      const selected = selection?.kind === "transition" && selection.id === transition.id;
      const same = source.id === target.id;
      // Ports follow geometry, including saved diagrams and states moved by MCP.
      // Vertical neighbours must not be joined through their left/right faces.
      const vertical = Math.abs(target.y - source.y) > Math.abs(target.x - source.x);
      const sourceSide = same
        ? "right"
        : vertical
          ? target.y > source.y
            ? "bottom"
            : "top"
          : target.x > source.x
            ? "right"
            : "left";
      const targetSide = same
        ? "left"
        : vertical
          ? target.y > source.y
            ? "top"
            : "bottom"
          : target.x > source.x
            ? "left"
            : "right";
      const peers = diagram.transitions.filter(
        (t) =>
          (t.from === transition.from && t.to === transition.to) ||
          (t.from === transition.to && t.to === transition.from),
      );
      const index = peers.indexOf(transition);
      const vertices = same
        ? [
            { x: source.x + 240, y: source.y - 60 - index * 36 },
            { x: source.x - 50, y: source.y - 60 - index * 36 },
          ]
        : peers.length > 1
          ? [
              {
                x:
                  (source.x + target.x) / 2 +
                  95 +
                  (vertical ? (index - (peers.length - 1) / 2) * 90 : 0),
                y:
                  (source.y + target.y) / 2 +
                  38 +
                  (vertical ? 0 : (index - (peers.length - 1) / 2) * 90),
              },
            ]
          : undefined;
      graph.addEdge({
        id: `transition:${transition.id}`,
        zIndex: 0,
        source: { cell: `state:${source.id}`, port: sourceSide },
        target: { cell: `state:${target.id}`, port: targetSide },
        vertices,
        connector: { name: "rounded" },
        router: {
          name: "manhattan",
          args: { startDirections: [sourceSide], endDirections: [targetSide], padding: 16 },
        },
        attrs: { line: diagramEdgeLine(selected) },
        labels: [
          diagramEdgeLabel(transitionLabel(transition), {
            // A horizontal gap can be narrower than an API path. Lift its
            // label above the states instead of covering their ports/bodies.
            position: { distance: 0.5, offset: { x: 0, y: !vertical && !same ? -84 : 0 } },
            fill: "#f5f7f4",
          }),
        ],
      });
    }
    initialFit.current?.(diagram.id);
  }, [diagram, selection, activeState]);
  useEffect(() => {
    const graph = graphRef.current;
    if (!graph) return;
    // A late layout must inspect live positions without rebuilding a node
    // currently being dragged from its last saved coordinates.
    const matching = props.layout && applyDiagramRoutes(graph, props.layout);
    if (matching && appliedLayout.current !== props.layout)
      graph.zoomToFit({ padding: FIT_PADDING, maxScale: 1 });
    appliedLayout.current = matching ? props.layout! : null;
  }, [diagram, selection, activeState, props.layout]);
  return (
    <DiagramViewport
      hostRef={host}
      graphRef={graphRef}
      className={styles.graph}
      ariaLabel={`Диаграмма ${props.diagram.name}. ${props.diagram.states.length} состояний. ${props.readOnly ? "Основания доступны в списке ниже." : "Редактирование доступно в списках ниже."}`}
      zoomLabel="диаграмму"
      fitPadding={FIT_PADDING}
    />
  );
}

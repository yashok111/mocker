import { Graph } from "@antv/x6";
import { useEffect, useRef } from "react";
import { Button, Group } from "@mantine/core";
import type { StateDiagram, Selection } from "./model";
import styles from "./StateDiagram.module.css";

// Leave room for labels above horizontal edges when fitting the model bounds.
const FIT_PADDING = 86;
const SIDES = ["top", "right", "bottom", "left"] as const;

type Props = {
  diagram: StateDiagram;
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
  const fitted = useRef("");
  const { diagram, selection, activeState } = props;
  useEffect(() => {
    current.current = props;
  });
  useEffect(() => {
    if (!host.current) return;
    const graph = new Graph({
      container: host.current,
      autoResize: true,
      background: { color: "#f5f7f4" },
      grid: { visible: true, size: 20, type: "dot", args: { color: "#ccd5cc", thickness: 1 } },
      panning: { enabled: true, modifiers: "shift", eventTypes: ["leftMouseDown"] },
      mousewheel: { enabled: true, modifiers: ["ctrl", "meta"], minScale: 0.25, maxScale: 2 },
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
            attrs: { line: { stroke: "#46624c", targetMarker: "block" } },
          });
        },
      },
      interacting: {
        edgeMovable: false,
        edgeLabelMovable: false,
        arrowheadMovable: false,
        vertexMovable: false,
        vertexAddable: false,
        vertexDeletable: false,
      },
    });
    graphRef.current = graph;
    graph.on("resize", () => {
      if (current.current.diagram.states.length)
        graph.zoomToFit({ padding: FIT_PADDING, maxScale: 1 });
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
      if (node.id.startsWith("state:")) {
        const p = node.position();
        current.current.onMove(node.id.slice(6), Math.round(p.x), Math.round(p.y));
      }
    });
    graph.on("edge:connected", ({ edge, isNew }) => {
      if (!isNew) return;
      const from = edge.getSourceCellId();
      const to = edge.getTargetCellId();
      graph.removeEdge(edge);
      if (from?.startsWith("state:") && to?.startsWith("state:"))
        current.current.onConnect(from.slice(6), to.slice(6));
    });
    return () => {
      graph.dispose();
      graphRef.current = null;
      fitted.current = "";
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
        width: 190,
        height: 76,
        label: `${state.name}\n${active ? "● Сейчас · " : ""}${state.id === diagram.initialStateId ? "Начальное" : state.terminal ? "Конечное" : "Состояние"}`,
        attrs: {
          body: {
            fill: active ? "#e1f0df" : "#ffffff",
            stroke: selected ? "#225936" : active ? "#527b42" : "#8c9b8d",
            strokeWidth: selected || state.terminal ? 3 : 1.5,
            rx: state.terminal ? 28 : 12,
            ry: state.terminal ? 28 : 12,
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
        attrs: {
          line: {
            stroke: selected ? "#245e39" : "#667868",
            strokeWidth: selected ? 3 : 1.5,
            targetMarker: { name: "block", width: 8, height: 6 },
          },
        },
        labels: [
          {
            // A horizontal gap can be narrower than an API path. Lift its
            // label above the states instead of covering their ports/bodies.
            position: { distance: 0.5, offset: { x: 0, y: !vertical && !same ? -84 : 0 } },
            attrs: {
              label: {
                text:
                  transition.name +
                  (transition.binding
                    ? `\n${transition.binding.method.toUpperCase()} ${transition.binding.path}`
                    : "") +
                  (transition.guard ? "\n[условие]" : ""),
                fill: "#263c2d",
                fontSize: 11,
                textWrap: { width: 180, height: 65, ellipsis: true },
              },
              body: {
                fill: "#f5f7f4",
                stroke: "#d5dfd4",
                rx: 4,
                ry: 4,
                refX: -10,
                refY: -6,
                refWidth: 20,
                refHeight: 12,
              },
            },
          },
        ],
      });
    }
    if (diagram.states.length && fitted.current !== diagram.id) {
      graph.zoomToFit({ padding: FIT_PADDING, maxScale: 1 });
      fitted.current = diagram.id;
    }
  }, [diagram, selection, activeState]);
  return (
    <div className={styles.graphShell}>
      <figure
        ref={host}
        className={styles.graph}
        aria-label={`Диаграмма ${props.diagram.name}. ${props.diagram.states.length} состояний. Редактирование доступно в списках ниже.`}
      />
      <Group className={styles.graphTools} gap={6}>
        <Button
          size="compact-sm"
          variant="default"
          onClick={() => graphRef.current?.zoom(-0.15)}
          aria-label="Уменьшить диаграмму"
        >
          −
        </Button>
        <Button
          size="compact-sm"
          variant="default"
          onClick={() => graphRef.current?.zoom(0.15)}
          aria-label="Увеличить диаграмму"
        >
          +
        </Button>
        <Button
          size="compact-sm"
          variant="default"
          onClick={() => graphRef.current?.zoomToFit({ padding: FIT_PADDING, maxScale: 1 })}
        >
          Вместить
        </Button>
      </Group>
    </div>
  );
}

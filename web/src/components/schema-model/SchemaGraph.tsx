import { Graph, routerPresets, type EdgeView, type PointLike } from "@antv/x6";
import { useEffect, useRef } from "react";
import { Button, Group } from "@mantine/core";
import type { SchemaModel } from "@/api/generated/schemas";
import type { Selection } from "./form";
import styles from "./SchemaModel.module.css";
import {
  CARD_WIDTH as WIDTH,
  CARD_HEADER as HEADER,
  FIELD_ROW as ROW,
  referenceVertices,
} from "./projection";

type Props = {
  model: SchemaModel;
  selection?: Selection;
  disabled: boolean;
  onSelect: (s: Selection) => void;
  onMove: (schema: string, x: number, y: number) => void;
  onConnect: (schema: string, property: string, target: string) => void;
};
export default function SchemaGraph(props: Props) {
  const host = useRef<HTMLElement>(null);
  const graphRef = useRef<Graph | null>(null);
  const current = useRef(props);
  const fitted = useRef(false);
  useEffect(() => {
    current.current = props;
  });
  useEffect(() => {
    if (!host.current) return;
    const graph = new Graph({
      container: host.current,
      autoResize: true,
      // Rebuilding the projection must remove old SVG views before reusing IDs.
      async: false,
      background: { color: "#f5f7f4" },
      grid: { visible: true, size: 20, type: "dot", args: { color: "#ccd5cc" } },
      panning: { enabled: true, modifiers: "shift", eventTypes: ["leftMouseDown"] },
      mousewheel: { enabled: true, modifiers: ["ctrl", "meta"], minScale: 0.15, maxScale: 2 },
      interacting: () => ({
        nodeMovable: !current.current.disabled,
        edgeMovable: false,
        arrowheadMovable: false,
        vertexMovable: false,
        vertexAddable: false,
        vertexDeletable: false,
      }),
      connecting: {
        allowBlank: false,
        allowEdge: false,
        allowNode: false,
        allowLoop: true,
        snap: true,
        validateMagnet: () => !current.current.disabled,
        validateConnection: ({ sourcePort, targetPort }) =>
          !!sourcePort?.startsWith("property:") && targetPort === "target",
        createEdge() {
          return graph.createEdge({
            router: { name: "manhattan", args: { padding: 24 } },
            connector: { name: "rounded" },
            attrs: { line: { stroke: "#55715b", targetMarker: "block" } },
          });
        },
      },
    });
    graphRef.current = graph;
    graph.on("node:click", ({ node, e }) => {
      const property =
        (e.target as Element).closest("[data-property]")?.getAttribute("data-property") ?? null;
      current.current.onSelect({
        schema: node.getData().name as string,
        ...(property === null ? {} : { property }),
      });
    });
    graph.on("node:moved", ({ node }) => {
      const p = node.position();
      current.current.onMove(node.getData().name as string, Math.round(p.x), Math.round(p.y));
    });
    graph.on("edge:connected", ({ edge, isNew }) => {
      if (!isNew) return;
      const from = edge.getSourceCellId(),
        to = edge.getTargetCellId(),
        port = edge.getSourcePortId();
      graph.removeEdge(edge);
      if (from && to && port?.startsWith("property:"))
        current.current.onConnect(
          graph.getCellById(from).getData().name as string,
          port.slice(9),
          graph.getCellById(to).getData().name as string,
        );
    });
    graph.on("resize", () => {
      if (current.current.model.schemas.length) graph.zoomToFit({ padding: 32, maxScale: 1 });
    });
    return () => {
      graph.dispose();
      graphRef.current = null;
      fitted.current = false;
    };
  }, []);
  useEffect(() => {
    const graph = graphRef.current;
    if (!graph) return;
    graph.clearCells();
    const ids = new Map(props.model.schemas.map((schema, i) => [schema.name, `schema:${i}`]));
    for (const schema of props.model.schemas) {
      const height = HEADER + Math.max(1, schema.properties.length) * ROW + 8;
      const selected = props.selection?.schema === schema.name;
      graph.addNode({
        id: ids.get(schema.name),
        data: { name: schema.name },
        shape: "rect",
        x: schema.x,
        y: schema.y,
        width: WIDTH,
        height,
        markup: [
          { tagName: "rect", selector: "body" },
          { tagName: "text", selector: "title" },
          ...schema.properties.map((_, i) => ({ tagName: "text", selector: `field${i}` })),
        ],
        attrs: {
          body: {
            fill: "#fff",
            stroke: selected ? "#315b3a" : "#abbcac",
            strokeWidth: selected ? 2 : 1,
            rx: 8,
            ry: 8,
          },
          title: {
            text: schema.name,
            refX: 14,
            refY: 23,
            fontSize: 15,
            fontWeight: 650,
            textAnchor: "start",
            textVerticalAnchor: "middle",
            fill: "#24452b",
            textWrap: { width: WIDTH - 30, height: 30, ellipsis: true },
          },
          ...Object.fromEntries(
            schema.properties.map((p, i) => [
              `field${i}`,
              {
                text: `${p.name}${p.required ? " *" : "?"} : ${p.type || "schema"}`,
                "data-property": p.name,
                refX: 14,
                refY: HEADER + i * ROW + 13,
                fontSize: 12,
                textAnchor: "start",
                textVerticalAnchor: "middle",
                fill: "#344638",
                cursor: "pointer",
                textWrap: { width: WIDTH - 30, height: 25, ellipsis: true },
              },
            ]),
          ),
        },
        ports: {
          groups: {
            input: {
              position: { name: "absolute" },
              attrs: { circle: { r: 5, magnet: "passive", fill: "#e4eee3", stroke: "#55715b" } },
            },
            output: {
              position: { name: "absolute" },
              attrs: { circle: { r: 5, magnet: true, fill: "#fff", stroke: "#55715b" } },
            },
          },
          items: [
            { id: "target", group: "input", args: { x: 0, y: 23 } },
            ...schema.properties.map((p, i) => ({
              id: `property:${p.name}`,
              group: "output",
              args: { x: WIDTH, y: HEADER + i * ROW + 13 },
            })),
          ],
        },
      });
    }
    props.model.references.forEach((ref, i) => {
      if (!ids.has(ref.sourceSchema) || !ids.has(ref.targetSchema)) return;
      const sourceSchema = props.model.schemas.find((s) => s.name === ref.sourceSchema);
      const hasPort = sourceSchema?.properties.some((p) => p.name === ref.sourceProperty);
      graph.addEdge({
        id: `ref:${i}`,
        source: {
          cell: ids.get(ref.sourceSchema)!,
          ...(hasPort ? { port: `property:${ref.sourceProperty}` } : {}),
        },
        target: { cell: ids.get(ref.targetSchema)!, port: "target" },
        router(_vertices: PointLike[], _options: unknown, view: EdgeView) {
          // Read live positions so the route stays attached while dragging.
          const model = {
            ...props.model,
            schemas: props.model.schemas.map((schema) => {
              const node = graph.getCellById(ids.get(schema.name)!);
              return node?.isNode() ? { ...schema, ...node.position() } : schema;
            }),
          };
          const vertices = referenceVertices(model, ref);
          // Feeding complete orthogonal legs back into Manhattan produces
          // loops around short segments when it snaps them to its search grid.
          return vertices.length
            ? vertices
            : routerPresets.manhattan.call(
                view,
                [],
                {
                  padding: { top: 24, right: 24, bottom: 24, left: 24 },
                  startDirections: ["right"],
                  endDirections: ["left"],
                },
                view,
              );
        },
        connector: { name: "rounded", args: { radius: 6 } },
        attrs: { line: { stroke: "#69896d", strokeWidth: 1.5, targetMarker: "block" } },
      });
    });
    if (!fitted.current && props.model.schemas.length) {
      graph.zoomToFit({ padding: 32, maxScale: 1 });
      fitted.current = true;
    }
  }, [props.model, props.selection]);
  return (
    <div className={styles.graphShell}>
      <figure
        ref={host}
        className={styles.graph}
        aria-label="Модель схем. Выбор схем и полей также доступен в списке."
      />
      <Group className={styles.tools} gap={6}>
        <Button
          size="compact-sm"
          variant="default"
          aria-label="Уменьшить модель"
          onClick={() => graphRef.current?.zoom(-0.15)}
        >
          −
        </Button>
        <Button
          size="compact-sm"
          variant="default"
          aria-label="Увеличить модель"
          onClick={() => graphRef.current?.zoom(0.15)}
        >
          +
        </Button>
        <Button
          size="compact-sm"
          variant="default"
          onClick={() => graphRef.current?.zoomToFit({ padding: 32, maxScale: 1 })}
        >
          Вместить
        </Button>
      </Group>
    </div>
  );
}

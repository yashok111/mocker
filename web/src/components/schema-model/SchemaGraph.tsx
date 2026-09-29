import { Graph, routerPresets, type EdgeView, type PointLike } from "@antv/x6";
import { useEffect, useRef } from "react";
import { Alert } from "@mantine/core";
import type { SchemaModel } from "@/api/generated/schemas";
import DiagramViewport from "../diagram/DiagramViewport";
import { installCanvasWheelZoom } from "../diagram/canvasControls";
import { createInitialFit } from "../diagram/initialFit";
import { diagramCardBody, diagramEdgeLine, diagramOptions } from "../diagram/presentation";
import type { Selection } from "./form";
import styles from "./SchemaModel.module.css";
import {
  CARD_WIDTH as WIDTH,
  CARD_HEADER as HEADER,
  FIELD_ROW as ROW,
  referenceVertices,
} from "./projection";
import { schemaCardHeight, schemaLayoutInput } from "./layout";
import { useDiagramLayout } from "../diagram/useDiagramLayout";
import { applyDiagramRoutes } from "../diagram/elkX6";

function restoreManualRoutes(graph: Graph, model: SchemaModel) {
  const ids = new Map(model.schemas.map((schema, i) => [schema.name, `schema:${i}`]));
  model.references.forEach((ref, index) => {
    const edge = graph.getCellById(`ref:${index}`);
    if (!edge?.isEdge() || !ids.has(ref.sourceSchema) || !ids.has(ref.targetSchema)) return;
    const hasPort = model.schemas
      .find((schema) => schema.name === ref.sourceSchema)
      ?.properties.some((property) => property.name === ref.sourceProperty);
    edge.setSource({
      cell: ids.get(ref.sourceSchema)!,
      ...(hasPort ? { port: `property:${ref.sourceProperty}` } : {}),
    });
    edge.setTarget({ cell: ids.get(ref.targetSchema)!, port: "target" });
    edge.setVertices([]);
    edge.setConnector({ name: "rounded", args: { radius: 6 } });
    // setRouter treats its first argument as a router name, including functions.
    edge.setProp("router", function (_vertices: PointLike[], _options: unknown, view: EdgeView) {
      const live = {
        ...model,
        schemas: model.schemas.map((schema) => {
          const node = graph.getCellById(ids.get(schema.name)!);
          return node?.isNode() ? { ...schema, ...node.position() } : schema;
        }),
      };
      const vertices = referenceVertices(live, ref);
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
    });
  });
}

type Props = {
  model: SchemaModel;
  selection?: Selection;
  disabled: boolean;
  onSelect: (s: Selection) => void;
  onMove: (schema: string, x: number, y: number) => void;
  onConnect: (schema: string, property: string, target: string) => void;
  fitIdentity?: number;
};
export default function SchemaGraph(props: Props) {
  const { layout, error } = useDiagramLayout(schemaLayoutInput(props.model));
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
      ...diagramOptions(),
      // Rebuilding the projection must remove old SVG views before reusing IDs.
      async: false,
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
            attrs: { line: diagramEdgeLine() },
          });
        },
      },
    });
    graphRef.current = graph;
    const removeWheelZoom = installCanvasWheelZoom(graph, host.current);
    const fit = createInitialFit(graph, host.current, 32);
    initialFit.current = fit;
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
    graph.on("node:change:position", () => restoreManualRoutes(graph, current.current.model));
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
      fit(String(current.current.fitIdentity ?? 0));
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
    const ids = new Map(props.model.schemas.map((schema, i) => [schema.name, `schema:${i}`]));
    for (const schema of props.model.schemas) {
      const height = schemaCardHeight(schema.properties.length);
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
          body: diagramCardBody(selected),
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
        connector: { name: "rounded", args: { radius: 6 } },
        attrs: { line: diagramEdgeLine() },
      });
    });
    restoreManualRoutes(graph, props.model);
  }, [props.model, props.selection]);
  useEffect(() => {
    if (layout && graphRef.current) applyDiagramRoutes(graphRef.current, layout);
    initialFit.current?.(String(props.fitIdentity ?? 0));
  }, [layout, props.model, props.selection, props.fitIdentity]);
  return (
    <>
      {error && (
        <Alert color="red" role="alert">
          Не удалось рассчитать расположение схем. Ручное редактирование доступно.
        </Alert>
      )}
      <DiagramViewport
        hostRef={host}
        graphRef={graphRef}
        className={styles.graph}
        ariaLabel="Модель схем. Выбор схем и полей также доступен в списке."
        zoomLabel="модель"
      />
    </>
  );
}

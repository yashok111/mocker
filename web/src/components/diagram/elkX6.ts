import type { EdgeLabel, EdgeMetadata, Graph } from "@antv/x6";
import { canvasTextMeasurer, wrapCanvasText } from "../design-canvas/canvasText";
import { diagramFontFamily } from "./presentation";
import type { DiagramLayoutResult } from "./elkLayout";

type Route = DiagramLayoutResult["edges"][number];

export function measureDiagramLabel(text: string, maxWidth = 180, maxLines = 4) {
  const measure = canvasTextMeasurer(11, 400, diagramFontFamily);
  const allLines = wrapCanvasText(text, maxWidth, measure);
  const lines = allLines.slice(0, maxLines);
  if (allLines.length > lines.length) {
    let last = lines.at(-1) ?? "";
    while (last && measure(`${last}…`) > maxWidth) last = Array.from(last).slice(0, -1).join("");
    lines[lines.length - 1] = `${last}…`;
  }
  return {
    text: lines.join("\n"),
    width: Math.ceil(Math.max(0, ...lines.map(measure))) + 24,
    height: lines.length * 16 + 12,
  };
}

const fixedLabel = {
  markup: [
    { tagName: "rect", selector: "body" },
    { tagName: "text", selector: "label" },
  ],
};

export function diagramLayoutEdgeLabel(
  label: NonNullable<Route["label"]>,
  start: { x: number; y: number },
): EdgeLabel {
  return {
    position: {
      distance: 0,
      offset: { x: label.x + label.width / 2 - start.x, y: label.y + label.height / 2 - start.y },
    },
    attrs: {
      body: {
        x: -label.width / 2,
        y: -label.height / 2,
        width: label.width,
        height: label.height,
        fill: "#ffffff",
        stroke: "#c7d3c8",
        rx: 6,
        ry: 6,
      },
      label: {
        text: label.text,
        fill: "#314638",
        fontSize: 11,
        fontFamily: diagramFontFamily,
        lineHeight: 16,
        textAnchor: "middle",
        textVerticalAnchor: "middle",
      },
    },
  };
}

export function diagramRouteOptions(
  layout: DiagramLayoutResult,
  route: Route,
): Partial<EdgeMetadata> {
  const source = layout.nodes.find((node) => node.id === route.source)!;
  const target = layout.nodes.find((node) => node.id === route.target)!;
  const start = route.points[0]!;
  const end = route.points.at(-1)!;
  return {
    source: {
      cell: route.source,
      anchor: { name: "topLeft", args: { dx: start.x - source.x, dy: start.y - source.y } },
      connectionPoint: "anchor",
    },
    target: {
      cell: route.target,
      anchor: { name: "topLeft", args: { dx: end.x - target.x, dy: end.y - target.y } },
      connectionPoint: "anchor",
    },
    vertices: route.points.slice(1, -1),
    router: { name: "normal" },
    connector: { name: "jumpover", args: { radius: 6, size: 5 } },
    defaultLabel: fixedLabel,
    labels: route.label ? [diagramLayoutEdgeLabel(route.label, start)] : [],
    zIndex: 0,
  };
}

// Persisted x/y remain authoritative. Restore deterministic routes after reload,
// but leave the editor's manual routing in charge after a card has been moved.
export function applyDiagramRoutes(graph: Graph, layout: DiagramLayoutResult): boolean {
  const nodes = graph.getNodes();
  if (nodes.length !== layout.nodes.length || graph.getEdges().length !== layout.edges.length)
    return false;
  const byId = new Map(nodes.map((node) => [node.id, node]));
  if (
    layout.nodes.some((expected) => {
      const node = byId.get(expected.id);
      if (!node) return true;
      const bounds = node.getBBox();
      return (["x", "y", "width", "height"] as const).some(
        (key) => Math.abs(bounds[key] - expected[key]) > 1,
      );
    }) ||
    layout.edges.some((route) => !graph.getCellById(route.id)?.isEdge())
  )
    return false;
  graph.batchUpdate("elk-routes", () => {
    nodes.forEach((node) => node.setZIndex(1));
    for (const route of layout.edges) {
      const edge = graph.getCellById(route.id)!;
      // The object overload deep-merges old ports, router args and labels.
      // Each layout property must replace its manual-routing counterpart.
      for (const [key, value] of Object.entries(diagramRouteOptions(layout, route))) {
        edge.setProp(key, value);
      }
    }
  });
  return true;
}

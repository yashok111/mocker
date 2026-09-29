import type { ElkNode } from "elkjs/lib/elk-api";

type Size = { width: number; height: number };
export type DiagramLayoutPoint = { x: number; y: number };
export type DiagramLayoutPort = DiagramLayoutPoint & {
  id: string;
  side?: "NORTH" | "SOUTH" | "EAST" | "WEST";
};
export type DiagramLayoutInput = {
  direction?: "RIGHT" | "DOWN";
  nodes: readonly ({ id: string; ports?: readonly DiagramLayoutPort[] } & Size)[];
  edges: readonly {
    id: string;
    source: string;
    target: string;
    sourcePort?: string;
    targetPort?: string;
    label?: Size & { text: string };
  }[];
};
export type DiagramLayoutResult = {
  nodes: ({ id: string } & Size & DiagramLayoutPoint)[];
  edges: {
    id: string;
    source: string;
    target: string;
    points: DiagramLayoutPoint[];
    label?: Size & DiagramLayoutPoint & { text: string };
  }[];
};

function finite(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

function validSize(size: Size) {
  return finite(size.width) && finite(size.height) && size.width > 0 && size.height > 0;
}

function validPoint(point: DiagramLayoutPoint) {
  return finite(point.x) && finite(point.y);
}

export async function layoutDiagram(input: DiagramLayoutInput): Promise<DiagramLayoutResult> {
  const nodeIds = new Set(input.nodes.map((node) => node.id));
  const portIds = new Map(
    input.nodes.map((node) => [node.id, new Set(node.ports?.map((port) => port.id))]),
  );
  const portId = (node: string, port: string) => `port:${JSON.stringify([node, port])}`;
  const terminal = (node: string, port?: string) =>
    port === undefined ? node : portId(node, port);
  const allIds = [...input.nodes, ...input.edges].map((item) => item.id);
  const engineIds = [
    ...allIds,
    ...input.nodes.flatMap((node) => node.ports?.map((port) => portId(node.id, port.id)) ?? []),
  ];
  if (
    allIds.some((id) => !id) ||
    new Set(engineIds).size !== engineIds.length ||
    (input.direction !== undefined && !["RIGHT", "DOWN"].includes(input.direction)) ||
    input.nodes.some(
      (node) =>
        !validSize(node) ||
        node.ports?.some(
          (port) =>
            !port.id ||
            !validPoint(port) ||
            port.x < 0 ||
            port.y < 0 ||
            port.x > node.width ||
            port.y > node.height ||
            (port.side !== undefined && !["NORTH", "SOUTH", "EAST", "WEST"].includes(port.side)),
        ),
    ) ||
    input.edges.some(
      (edge) =>
        !nodeIds.has(edge.source) ||
        !nodeIds.has(edge.target) ||
        (edge.sourcePort !== undefined && !portIds.get(edge.source)?.has(edge.sourcePort)) ||
        (edge.targetPort !== undefined && !portIds.get(edge.target)?.has(edge.targetPort)) ||
        (edge.label && !validSize(edge.label)),
    )
  ) {
    throw new Error("Invalid diagram layout input");
  }
  if (!input.nodes.length) return { nodes: [], edges: [] };

  const { default: ELK } = await import("elkjs/lib/elk.bundled.js");
  const elk = new ELK();
  const graph: ElkNode = {
    id: "diagram-layout",
    layoutOptions: {
      "elk.algorithm": "layered",
      "elk.direction": input.direction ?? "RIGHT",
      "elk.edgeRouting": "ORTHOGONAL",
      "elk.randomSeed": "1",
      "elk.layered.mergeEdges": "false",
      "elk.spacing.edgeEdge": "18",
      "elk.spacing.edgeNode": "24",
      "elk.spacing.nodeNode": "40",
      "elk.layered.spacing.edgeEdgeBetweenLayers": "18",
      "elk.layered.spacing.edgeNodeBetweenLayers": "24",
      "elk.layered.spacing.nodeNodeBetweenLayers": "80",
    },
    children: input.nodes.map((node) => ({
      id: node.id,
      width: node.width,
      height: node.height,
      ...(node.ports?.length
        ? {
            layoutOptions: { "elk.portConstraints": "FIXED_POS" },
            ports: node.ports.map((port) => ({
              id: portId(node.id, port.id),
              x: port.x,
              y: port.y,
              width: 0,
              height: 0,
              ...(port.side ? { layoutOptions: { "elk.port.side": port.side } } : {}),
            })),
          }
        : {}),
    })),
    edges: input.edges.map((edge) => ({
      id: edge.id,
      sources: [terminal(edge.source, edge.sourcePort)],
      targets: [terminal(edge.target, edge.targetPort)],
      ...(edge.label
        ? {
            labels: [
              {
                id: `${edge.id}:label`,
                ...edge.label,
                layoutOptions: {
                  "elk.edgeLabels.placement": "CENTER",
                  "elk.edgeLabels.inline": "true",
                },
              },
            ],
          }
        : {}),
    })),
  };
  const result = await elk.layout(graph);
  const nodes = new Map(result.children?.map((node) => [node.id, node]));
  const edges = new Map(result.edges?.map((edge) => [edge.id, edge]));
  if (
    nodes.size !== input.nodes.length ||
    edges.size !== input.edges.length ||
    result.children?.length !== nodes.size ||
    (result.edges?.length ?? 0) !== edges.size
  ) {
    throw new Error("Diagram layout returned incomplete elements");
  }
  return {
    nodes: input.nodes.map((original) => {
      const node = nodes.get(original.id);
      if (
        !node ||
        !finite(node.x) ||
        !finite(node.y) ||
        !finite(node.width) ||
        !finite(node.height) ||
        !validSize({ width: node.width, height: node.height })
      ) {
        throw new Error(`Diagram layout returned an invalid node: ${original.id}`);
      }
      return { id: original.id, x: node.x, y: node.y, width: node.width, height: node.height };
    }),
    edges: input.edges.map((original) => {
      const edge = edges.get(original.id);
      const section = edge?.sections?.[0];
      if (
        !section ||
        edge?.sections?.length !== 1 ||
        edge.sources.length !== 1 ||
        edge.sources[0] !== terminal(original.source, original.sourcePort) ||
        edge.targets.length !== 1 ||
        edge.targets[0] !== terminal(original.target, original.targetPort)
      ) {
        throw new Error(`Diagram layout returned an incomplete route: ${original.id}`);
      }
      const points = [section.startPoint, ...(section.bendPoints ?? []), section.endPoint];
      if (points.some((point) => !point || !validPoint(point))) {
        throw new Error(`Diagram layout returned an invalid route: ${original.id}`);
      }
      const label = original.label ? edge.labels?.[0] : undefined;
      if (
        original.label &&
        (!label ||
          edge.labels?.length !== 1 ||
          !finite(label.x) ||
          !finite(label.y) ||
          !finite(label.width) ||
          !finite(label.height) ||
          !validSize({ width: label.width, height: label.height }))
      ) {
        throw new Error(`Diagram layout returned an invalid label: ${original.id}`);
      }
      return {
        id: original.id,
        source: original.source,
        target: original.target,
        points: points.map(({ x, y }) => ({ x, y })),
        ...(label
          ? {
              label: {
                x: label.x!,
                y: label.y!,
                width: label.width!,
                height: label.height!,
                text: original.label!.text,
              },
            }
          : {}),
      };
    }),
  };
}

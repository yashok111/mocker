import type { DiagramLayoutInput, DiagramLayoutResult } from "../diagram/elkLayout";
import { measureDiagramLabel } from "../diagram/elkX6";
import { CARD_HEIGHT, CARD_WIDTH, portY } from "./layout";
import { portNames, ports, type ResponseRule } from "./model";

export function ruleLayoutInput(rule?: ResponseRule): DiagramLayoutInput {
  const ids = new Map(rule?.nodes.map((node) => [node.id, node]));
  return {
    direction: "RIGHT",
    nodes: (rule?.nodes ?? []).map((node) => ({
      id: `node:${node.id}`,
      width: CARD_WIDTH,
      height: CARD_HEIGHT,
      ports: [
        ...(node.type === "start"
          ? []
          : [{ id: "in", x: 0, y: CARD_HEIGHT / 2, side: "WEST" as const }]),
        ...ports(node).map((port) => ({
          id: port,
          x: CARD_WIDTH,
          y: portY({ ...node, y: 0 }, port),
          side: "EAST" as const,
        })),
      ],
    })),
    edges: (rule?.edges ?? [])
      .filter((edge) => ids.has(edge.from) && ids.has(edge.to))
      .map((edge) => ({
        id: `edge:${edge.id}`,
        source: `node:${edge.from}`,
        target: `node:${edge.to}`,
        ...(ports(ids.get(edge.from)!).includes(edge.port) ? { sourcePort: edge.port } : {}),
        ...(ids.get(edge.to)!.type === "start" ? {} : { targetPort: "in" }),
        ...(edge.port !== "next"
          ? { label: measureDiagramLabel(portNames[edge.port]!, 110, 1) }
          : {}),
      })),
  };
}

export function applyRuleLayout(rule: ResponseRule, layout: DiagramLayoutResult): ResponseRule {
  const positions = new Map(layout.nodes.map((node) => [node.id, node]));
  return {
    ...rule,
    nodes: rule.nodes.map((node) => {
      const position = positions.get(`node:${node.id}`);
      return position ? { ...node, x: Math.round(position.x), y: Math.round(position.y) } : node;
    }),
  };
}

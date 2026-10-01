import type { BackendNode, BackendEdge } from "@/api/generated/schemas";
import type { DiagramLayoutInput } from "../diagram/elkLayout";
import { measureDiagramLabel } from "../diagram/elkX6";

export function flowTransitionLabel(edge: BackendEdge) {
  return "label" in edge.attributes ? `${edge.kind} · ${edge.attributes.label}` : edge.kind;
}

export function flowStepContext(node: BackendNode): string[] {
  const result: string[] = [];
  if ("expression" in node.attributes) {
    const value = node.attributes.expression;
    result.push(
      value.status === "known" ? `Условие: ${value.value}` : `Условие неизвестно: ${value.reason}`,
    );
  }
  if ("transactionContext" in node.attributes) {
    const value = node.attributes.transactionContext;
    result.push(
      value.status === "known"
        ? `Локальная транзакция: ${value.transactionId}`
        : value.status === "none"
          ? `Без транзакции: ${value.reason}`
          : `Транзакция неизвестна: ${value.reason}`,
    );
  }
  return result;
}

export function buildFlowScene(nodes: BackendNode[], edges: BackendEdge[]) {
  const visible = nodes.slice(0, 200);
  const ids = new Set(visible.map((node) => node.id));
  const matching = edges.filter((edge) => ids.has(edge.from) && ids.has(edge.to));
  const eligible = matching.slice(0, 600);
  const rendered = new Set(eligible.map((edge) => edge.id));
  const input: DiagramLayoutInput = {
    nodes: visible.map((node) => ({ id: node.id, width: 260, height: 120 })),
    edges: eligible.map((edge) => ({
      id: edge.id,
      source: edge.from,
      target: edge.to,
      label: measureDiagramLabel(flowTransitionLabel(edge)),
    })),
  };
  return {
    input,
    nodes: visible,
    edges: eligible,
    excludedNodes: nodes.length - visible.length,
    excludedEdges: Math.max(0, matching.length - 600),
    excludedEndpoints: edges.length - matching.length,
    boundaries: edges.filter((edge) => !rendered.has(edge.id)),
  };
}

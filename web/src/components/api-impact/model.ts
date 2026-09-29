import type {
  ApiImpactChange,
  ApiImpactEntity,
  ApiImpactEvidence,
  ApiImpactReport,
} from "@/api/generated/schemas";

export const sideLabel = (side: "before" | "after") => (side === "before" ? "Было" : "Стало");
export const directionLabel: Record<ApiImpactEvidence["direction"], string> = {
  request: "Запрос",
  response: "Ответ",
  mixed: "Запрос и ответ",
  unknown: "Направление неизвестно",
};
export const entityLabel: Record<ApiImpactEntity["kind"], string> = {
  schema: "Схема",
  contract_node: "Узел контракта",
  operation: "Операция",
  resource: "Ресурс",
  scenario_message: "Шаг сценария",
  state_transition: "Переход состояния",
};
export const compatibilityLabel: Record<ApiImpactChange["compatibility"], string> = {
  breaking: "Нарушает контракт",
  compatible: "Совместимое изменение",
  review: "Требует проверки",
};
export const kindLabel: Record<ApiImpactChange["kind"], string> = {
  added: "Добавлено",
  removed: "Удалено",
  changed: "Изменено",
};
export type ImpactGraphModel = {
  nodes: { id: string; label: string; width: number; height: number }[];
  edges: {
    id: string;
    source: string;
    target: string;
    label: string;
  }[];
  truncated: boolean;
};

// Reference sites are already ordered from the changed definition toward its consumer.
export function impactGraphModel(
  report: ApiImpactReport,
  change: ApiImpactChange,
): ImpactGraphModel {
  const model: ImpactGraphModel = {
    nodes: [{ id: "change", label: change.pointer || "/", width: 230, height: 84 }],
    edges: [],
    truncated: false,
  };
  const entities = new Map(report.affected.map((entity) => [entity.id, entity]));
  for (const evidence of report.evidence) {
    if (evidence.changeId !== change.id) continue;
    const entity = entities.get(evidence.entityId);
    if (!entity) continue;
    const labels = [...evidence.referenceSites.map((site) => site.pointer), entity.label];
    if (model.nodes.length + labels.length > 100) {
      model.truncated = true;
      continue;
    }
    let source = model.nodes[0]!;
    labels.forEach((label, index) => {
      const id = `${evidence.id}:${index}`;
      const target = {
        id,
        label,
        width: 230,
        height: 84,
      };
      model.nodes.push(target);
      model.edges.push({
        id: `edge:${id}`,
        source: source.id,
        target: id,
        label:
          index === 0 ? `${sideLabel(evidence.side)}\n${directionLabel[evidence.direction]}` : "",
      });
      source = target;
    });
  }
  return model;
}

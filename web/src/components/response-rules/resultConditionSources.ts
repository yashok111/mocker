import type { ResponseRule, ResponseRuleNode } from "./model";

export function resultConditionSources(rule: ResponseRule, consumerId: string) {
  const nodes = new Map(rule.nodes.map((node) => [node.id, node]));
  const starts = rule.nodes.filter((node) => node.type === "start");
  const edges = new Map<string, ResponseRule["edges"]>();
  for (const edge of rule.edges) {
    if (!nodes.has(edge.from) || !nodes.has(edge.to)) continue;
    const outgoing = edges.get(edge.from) ?? [];
    outgoing.push(edge);
    edges.set(edge.from, outgoing);
  }
  function reachable(start: string, target: string, blocked = "", blockedPort = "") {
    const queue = [start];
    const seen = new Set<string>();
    for (let index = 0; index < queue.length; index += 1) {
      const id = queue[index]!;
      if (seen.has(id) || (id === blocked && !blockedPort)) continue;
      seen.add(id);
      if (id === target) return true;
      for (const edge of edges.get(id) ?? [])
        if (!(id === blocked && edge.port === blockedPort)) queue.push(edge.to);
    }
    return false;
  }
  function unavailableReason(sourceId: string): string | undefined {
    if (!sourceId) return "Выберите узел сущности, который выполняется до условия.";
    const producer = nodes.get(sourceId);
    if (!producer || !producer.type.startsWith("entity_"))
      return "Узел результата недоступен. Выберите существующий узел сущности.";
    if (producer.id === consumerId || reachable(consumerId, producer.id))
      return "Результат должен появиться до условия. Перенесите условие после узла сущности.";
    if (
      rule.nodes.length > 100 ||
      rule.edges.length > 200 ||
      starts.length !== 1 ||
      !nodes.has(consumerId) ||
      !reachable(starts[0]!.id, consumerId)
    )
      return "Соедините условие с единственным началом правила и проверьте связи графа.";
    const start = starts[0]!.id;
    if (
      !reachable(start, producer.id) ||
      !reachable(producer.id, consumerId) ||
      reachable(start, consumerId, producer.id)
    )
      return "Результат недоступен на некоторых путях. Проведите все пути к условию через этот узел сущности.";
    if (
      (producer.type === "entity_update" ||
        (producer.type === "entity_read" && producer.entity.operation === "get")) &&
      reachable(start, consumerId, producer.id, "found")
    )
      return "Используйте результат после выхода «Найдена». Выход «Не найдена» направьте в отдельную ветку.";
    return undefined;
  }
  return {
    producers: rule.nodes.filter(
      (node): node is ResponseRuleNode =>
        node.type.startsWith("entity_") && unavailableReason(node.id) === undefined,
    ),
    unavailableReason,
  };
}

import type { BackendBusinessMapPayload } from "@/api/generated/schemas";

// Presentation order groups responsibility, never creates causal links.
export function businessMapGraph(
  payload: BackendBusinessMapPayload,
  filter = { search: "", origin: "all" },
) {
  const matches = (
    row: BackendBusinessMapPayload["elements"][number] | BackendBusinessMapPayload["links"][number],
  ) =>
    (filter.origin === "all" || row.origin.kind === filter.origin) &&
    `${row.id} ${row.label} ${"role" in row ? `${row.role} ${row.responsibility}` : row.relation}`
      .toLowerCase()
      .includes(filter.search.toLowerCase());
  const elements = [...payload.elements]
    .filter(matches)
    .sort((a, b) => a.responsibility.localeCompare(b.responsibility) || a.id.localeCompare(b.id));
  const ids = new Set(elements.map((e) => e.id));
  return {
    elements,
    links: [...payload.links]
      .filter((link) => matches(link) && ids.has(link.from) && ids.has(link.to))
      .sort((a, b) => a.id.localeCompare(b.id)),
  };
}
export const businessRoles = {
  actor: "Участник",
  command: "Команда",
  business_event: "Бизнес-событие",
  policy: "Политика",
  read_model: "Модель чтения",
  question: "Вопрос",
} as const;
export const businessRelations = {
  initiates: "Инициирует",
  produces: "Порождает",
  reacts_to: "Вызывает реакцию",
  issues: "Выдаёт команду",
  updates: "Обновляет",
  reads: "Читает",
  questions: "Задаёт вопрос",
} as const;
export function businessRolePair(from: string, to: string, relation: string) {
  switch (relation) {
    case "initiates":
      return from === "actor" && to === "command";
    case "produces":
      return from === "command" && to === "business_event";
    case "reacts_to":
      return from === "business_event" && to === "policy";
    case "issues":
      return from === "policy" && to === "command";
    case "updates":
      return from === "business_event" && to === "read_model";
    case "reads":
      return ["actor", "command", "policy"].includes(from) && to === "read_model";
    case "questions":
      return from === "question";
    default:
      return false;
  }
}

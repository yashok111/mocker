import { Alert, Stack, Text } from "@mantine/core";
import type { BackendImportPreflight as Preflight } from "@/api/generated/schemas";

const names: Record<string, string> = {
  graph: "Граф и доказательства",
  events: "События и задания",
  data_access: "Доступ к данным",
  lineage: "Происхождение полей",
  architecture: "Архитектура",
};
const statuses: Record<string, string> = {
  cardinality_admitted: "число объектов допустимо",
  scope_check_required: "нужна проверка выбранной области",
  unsupported_profile: "не входит в профиль импорта",
  blocked: "недоступно",
};

export function BackendImportPreflight({ value }: { value: Preflight }) {
  return (
    <Stack gap="xs" aria-label="Доступность после импорта">
      <Text fw={500}>Доступность после импорта</Text>
      <Text size="sm">Сохранение модели и доступность представлений проверяются отдельно.</Text>
      {value.consumers.map((consumer) => (
        <Alert key={consumer.surface} color={consumer.status === "blocked" ? "yellow" : "gray"}>
          <Text size="sm" fw={500}>
            {names[consumer.surface] ?? consumer.surface}: {statuses[consumer.status]}
          </Text>
          {consumer.reasons.includes("edge_limit") && (
            <Text size="sm">
              В модели {value.counts.edges} связей; предел этого представления —{" "}
              {consumer.admission.maxTotalEdges}. Уменьшение страницы или фильтр сервиса не меняют
              этот предел. Полный граф и доказательства остаются доступны.
            </Text>
          )}
          {consumer.traversal.maxVisitedStates !== undefined && (
            <Text size="sm">Обход: до {consumer.traversal.maxVisitedStates} состояний.</Text>
          )}
          {consumer.surface === "architecture" && (
            <Text size="sm">
              Перед сохранением проверьте соответствия компонентов исходным объектам.
            </Text>
          )}
        </Alert>
      ))}
    </Stack>
  );
}

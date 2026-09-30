import { Stack, Text } from "@mantine/core";
import type { ResponseRuleResultConditionTrace } from "@/api/generated/schemas";
import { resultConditionOpNames } from "./model";
import styles from "./ResponseRules.module.css";

export default function ResultConditionTrace({
  value,
}: {
  value: ResponseRuleResultConditionTrace;
}) {
  if ("children" in value)
    return (
      <Stack gap="xs" className={styles.conditionGroup}>
        <Text size="xs" fw={600}>
          {value.op === "all" ? "И" : "ИЛИ"} · {value.matched ? "Да" : "Нет"}
        </Text>
        {value.children.map((child, index) => (
          <ResultConditionTrace key={index} value={child} />
        ))}
        {value.shortCircuited && (
          <Text size="xs" c="dimmed">
            Оставшиеся условия пропущены: результат группы уже определён.
          </Text>
        )}
      </Stack>
    );
  return (
    <Stack gap={2}>
      <Text size="xs">
        Результат: {value.sourceNodeId} · {value.pointer || "весь JSON"} ·{" "}
        {resultConditionOpNames[value.op]}
        {value.matched !== undefined && ` · ${value.matched ? "Да" : "Нет"}`}
      </Text>
      <Text size="xs">{value.present ? "Поле присутствует" : "Поле отсутствует"}</Text>
      {value.actualJSON !== undefined && (
        <Text component="div" size="xs" className={styles.data}>
          Фактическое JSON: <code>{value.actualJSON}</code>
        </Text>
      )}
      {value.valueFrom && (
        <Text size="xs">
          Ожидаемый результат: {value.valueFrom.nodeId} · {value.valueFrom.pointer || "весь JSON"}
        </Text>
      )}
      {value.expectedJSON !== undefined && (
        <Text component="div" size="xs" className={styles.data}>
          Ожидаемое JSON: <code>{value.expectedJSON}</code>
        </Text>
      )}
    </Stack>
  );
}

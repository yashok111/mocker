import { Stack, Text } from "@mantine/core";
import type { ResponseRuleSimulation } from "@/api/generated/schemas";
import type { ResponseRule } from "./model";
import styles from "./ResponseRules.module.css";

export default function EntitySimulationResults({
  simulation,
  rule,
}: {
  simulation: ResponseRuleSimulation;
  rule: ResponseRule;
}) {
  return (
    <>
      {simulation.results && Object.keys(simulation.results).length > 0 && (
        <section aria-label="Результаты узлов">
          <Text fw={600}>Результаты узлов</Text>
          {Object.entries(simulation.results).map(([nodeId, valueJSON]) => (
            <div key={nodeId}>
              <Text size="sm">{rule.nodes.find((node) => node.id === nodeId)?.name || nodeId}</Text>
              <pre className={styles.data}>
                <code>{valueJSON}</code>
              </pre>
            </div>
          ))}
        </section>
      )}
      {simulation.entities && simulation.entities.length > 0 && (
        <section aria-label="Сущности после симуляции">
          <Text fw={600}>Сущности после симуляции</Text>
          <Stack gap="sm">
            {simulation.entities.map((entity, index) => (
              <div key={index}>
                <Text size="sm" fw={600}>
                  {entity.family} · {entity.rows.length} записей
                </Text>
                {entity.rows.length === 0 && (
                  <Text size="sm" c="dimmed">
                    Нет записей
                  </Text>
                )}
                {entity.rows.map((row, rowIndex) => (
                  <div key={rowIndex}>
                    <Text size="xs">
                      Ключ: {row.key} · Область:{" "}
                      {row.scope.length ? row.scope.join(" → ") : "корневая"}
                    </Text>
                    <pre className={styles.data}>
                      <code>{row.dataJSON}</code>
                    </pre>
                  </div>
                ))}
              </div>
            ))}
          </Stack>
        </section>
      )}
    </>
  );
}

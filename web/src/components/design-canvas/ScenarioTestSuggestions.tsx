import { useEffect, useRef, useState, type ReactElement } from "react";
import { Alert, Button, Group, Loader, Stack, Text } from "@mantine/core";
import type {
  DesignScenarioTestSuggestions,
  DesignScenarioSuggestedTest,
  DesignScenarioTestTarget,
} from "@/api/generated/schemas";
import type { CanvasDocument } from "./types";
import styles from "./ScenarioExecutionPanel.module.css";

export type TestSuggestions = DesignScenarioTestSuggestions;
export type SuggestedTest = DesignScenarioSuggestedTest;
export type SuggestTests = (revisionId: number, signal: AbortSignal) => Promise<TestSuggestions>;
type Props = {
  document: CanvasDocument;
  revisionId: number;
  refreshToken: number;
  disabled: boolean;
  suggest: SuggestTests;
  onRun: (test: SuggestedTest) => void;
};

function targetLabel(document: CanvasDocument, target: DesignScenarioTestTarget): string {
  const fragment = document.fragments.find((item) => item.id === target.fragmentId);
  const branch = fragment?.branches?.find((item) => item.id === target.branchId);
  return `${fragment?.label || target.fragmentId}${target.branchId ? ` · ${branch?.label || target.branchId}` : ""} · ${target.outcome === "taken" ? "выполнить" : "пропустить"}`;
}

export function ScenarioTestSuggestions(props: Props): ReactElement {
  // A revision, form or observed-coverage change retires the entire preview and
  // aborts its request. It cannot later submit inputs from the previous snapshot.
  return (
    <SuggestionsSession
      key={`${props.revisionId}:${props.refreshToken}:${props.disabled}`}
      {...props}
    />
  );
}
function SuggestionsSession({
  document,
  revisionId,
  disabled,
  suggest,
  onRun,
}: Props): ReactElement {
  const [result, setResult] = useState<TestSuggestions | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const api = useRef(suggest);
  useEffect(() => {
    api.current = suggest;
  }, [suggest]);
  useEffect(() => {
    if (disabled) return;
    const controller = new AbortController();
    async function generate(): Promise<void> {
      try {
        const next = await api.current(revisionId, controller.signal);
        if (controller.signal.aborted) return;
        if (next.revisionId !== revisionId)
          throw new Error("Сервер вернул варианты для другой ревизии.");
        setResult(next);
      } catch (cause) {
        if (!controller.signal.aborted)
          setError(cause instanceof Error ? cause.message : "Не удалось подобрать тесты.");
      }
    }
    void generate();
    return () => controller.abort();
  }, [revisionId, disabled, attempt]);
  const loading = !disabled && !result && !error;
  return (
    <Stack p="md" gap="md">
      <div>
        <Text fw={650}>Проверить непройденные ветки</Text>
        <Text size="sm" c="dimmed" mt={4}>
          Подберём начальные переменные для веток без наблюдений. Покрытие подтвердит фактический
          запуск.
        </Text>
      </div>
      {loading ? (
        <Group component="output" aria-live="polite" gap="xs">
          <Loader size="sm" aria-hidden="true" />
          <Text component="span" size="sm" c="dimmed">
            Подбираем тесты…
          </Text>
        </Group>
      ) : null}
      {disabled ? (
        <Text size="sm" c="dimmed">
          Подбор и запуск станут доступны после сохранения настроек и завершения текущего прогона.
        </Text>
      ) : null}
      {error ? (
        <Alert color="red" role="alert">
          {error}
          <Button
            mt="xs"
            size="xs"
            variant="light"
            onClick={() => {
              setError(null);
              setAttempt((value) => value + 1);
            }}
          >
            Повторить
          </Button>
        </Alert>
      ) : null}
      {result ? (
        <>
          <Text size="xs" c="dimmed">
            Ревизия {result.revisionId} · учтено прогонов: {result.runCount} из последних{" "}
            {result.sampleLimit}
          </Text>
          {result.truncated ? (
            <Alert color="yellow">
              Подбор ограничен по объёму. Ниже указаны ветки, для которых вариант пока не найден.
            </Alert>
          ) : null}
          {result.cases.length === 0 && result.unresolved.length === 0 ? (
            <Text size="sm">
              {document.fragments.length
                ? "Все ветки уже наблюдались в сохранённых прогонах."
                : "В сценарии пока нет веток и циклов для подбора тестов."}
            </Text>
          ) : null}
          {result.cases.map((test) => (
            <section key={test.id} aria-label={test.name} className={styles.suggestedTest}>
              <Text fw={650} size="sm">
                {test.name}
              </Text>
              <Text size="xs" c="dimmed" mt={4}>
                Ожидаемые ветки
              </Text>
              <ul className={styles.targetList}>
                {test.targets.map((target, index) => (
                  <li key={index}>{targetLabel(document, target)}</li>
                ))}
              </ul>
              {Object.keys(test.variables).length ? (
                <details className={styles.suggestionVariables}>
                  <summary>Переменные запуска · {Object.keys(test.variables).length}</summary>
                  <pre className={styles.code}>{JSON.stringify(test.variables, null, 2)}</pre>
                </details>
              ) : (
                <Text size="xs" c="dimmed">
                  Используются переменные из настроек сценария.
                </Text>
              )}
              <Button
                mt="sm"
                size="xs"
                variant="light"
                disabled={disabled}
                onClick={() => {
                  if (!disabled && result.revisionId === revisionId) onRun(test);
                }}
              >
                Запустить вариант
              </Button>
            </section>
          ))}
          {result.unresolved.length ? (
            <section aria-label="Ветки без подобранного теста">
              <Text fw={650} size="sm">
                Нужна настройка · {result.unresolved.length}
              </Text>
              <Stack gap="sm" mt="sm">
                {result.unresolved.map((item, index) => (
                  <div key={index}>
                    <Text size="sm" className={styles.wrap}>
                      {targetLabel(document, item.target)}
                    </Text>
                    <Text size="xs" c="dimmed">
                      {item.reason}
                    </Text>
                  </div>
                ))}
              </Stack>
            </section>
          ) : null}
        </>
      ) : null}
    </Stack>
  );
}

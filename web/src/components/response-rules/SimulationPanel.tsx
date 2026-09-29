import { useEffect, useEffectEvent, useLayoutEffect, useRef, useState } from "react";
import { Alert, Badge, Button, Group, Stack, Text, UnstyledButton } from "@mantine/core";
import {
  simulateResponseRule,
  validateResponseRule,
} from "@/api/generated/response-rules/response-rules";
import type {
  ResponseRule,
  ResponseRuleRequest,
  ResponseRuleSimulation,
  ResponseRuleStep,
  ResponseRuleValidation,
} from "@/api/generated/schemas";
import { describeApiFailureDetailed } from "@/api/errors";
import RequestFixtureEditor from "./RequestFixtureEditor";
import { createEvaluationGate, evaluationIdentity } from "./simulationState";
import type { GraphSelection } from "./model";
import styles from "./ResponseRules.module.css";

type Props = {
  designId: number;
  document: string;
  rule: ResponseRule;
  generation: string;
  pendingForm: boolean;
  onSelect: (selection: GraphSelection) => void;
  onTrace: (trace: ResponseRuleStep[]) => void;
  onSource?: (pointer: string) => void;
};
type Evaluation = ResponseRuleValidation | ResponseRuleSimulation;
type State = {
  identity: string;
  pending: boolean;
  stale?: boolean;
  result?: Evaluation;
  error?: string;
};
export default function SimulationPanel(props: Props) {
  // The parent keys the editor by API identity; the fixture remains in memory.
  const [request, setRequest] = useState<ResponseRuleRequest>({ query: [], headers: [] });
  const [state, setState] = useState<State | null>(null);
  const [gate] = useState(createEvaluationGate);
  const controller = useRef<AbortController | null>(null);
  const identity = evaluationIdentity({
    designId: props.designId,
    ruleId: props.rule.id,
    document: props.document,
    generation: props.generation,
    request,
  });
  useLayoutEffect(() => {
    gate.update(identity);
    controller.current?.abort();
    // oxlint-disable-next-line react/set-state-in-effect -- Invalidate even a completed result when inputs change and later return to the same text.
    setState((previous) => (previous ? { identity, pending: false, stale: true } : null));
    return () => {
      gate.dispose();
      controller.current?.abort();
    };
  }, [gate, identity, props.pendingForm]);
  const current = state?.identity === identity && !props.pendingForm ? state : null;
  const result = current?.result;
  const simulation = result && "trace" in result ? result : undefined;
  const trace = simulation?.trace;
  const notifyTrace = useEffectEvent((steps: ResponseRuleStep[]) => props.onTrace(steps));
  useEffect(() => {
    notifyTrace(trace ?? []);
  }, [trace]);
  async function evaluate(simulate: boolean) {
    if (props.pendingForm) return;
    controller.current?.abort();
    const active = new AbortController();
    controller.current = active;
    const token = gate.begin();
    setState({ identity, pending: true });
    try {
      const response = simulate
        ? await simulateResponseRule(
            props.designId,
            props.rule.id,
            { document: props.document, request },
            { signal: active.signal },
          )
        : await validateResponseRule(
            props.designId,
            props.rule.id,
            { document: props.document },
            { signal: active.signal },
          );
      if (!active.signal.aborted && gate.accept(token)) {
        if (response.status !== 200) throw new Error("Не удалось выполнить проверку");
        const evaluated = response.data;
        if (
          evaluated.source.designId !== props.designId ||
          evaluated.source.ruleId !== props.rule.id ||
          evaluated.source.kind !== "proposal"
        )
          throw new Error("Получен результат для другого документа");
        setState({ identity, pending: false, result: evaluated });
      }
    } catch (cause) {
      if (!active.signal.aborted && gate.accept(token))
        setState({ identity, pending: false, error: describeApiFailureDetailed(cause) });
    }
  }
  return (
    <section className={styles.simulation} aria-label="Проверка и симуляция">
      <div className={styles.simulationGrid}>
        <RequestFixtureEditor value={request} onChange={setRequest} />
        <Stack gap="sm">
          <Group>
            <Button
              variant="default"
              disabled={props.pendingForm || current?.pending}
              onClick={() => void evaluate(false)}
            >
              Проверить
            </Button>
            <Button
              disabled={props.pendingForm || current?.pending}
              loading={current?.pending}
              onClick={() => void evaluate(true)}
            >
              Симулировать
            </Button>
          </Group>
          {props.pendingForm && (
            <Text size="sm" c="dimmed">
              Примените или отмените изменения формы и расстановки перед проверкой.
            </Text>
          )}
          {(current?.stale || (!current && state)) && (
            <Text component="output" size="sm">
              Результат устарел. Запустите проверку снова.
            </Text>
          )}
          {current?.error && (
            <Alert color="red" role="alert">
              {current.error}
            </Alert>
          )}
          {result && (
            <>
              <Badge color={result.valid ? "green" : "red"}>
                {result.valid ? "Проверка пройдена" : "Есть ошибки в правиле"}
              </Badge>
              {result.diagnosticsTruncated && (
                <Alert color="yellow">
                  Показаны первые 200 ошибок. Исправьте их и повторите проверку.
                </Alert>
              )}
              <ul className={styles.list} aria-label="Диагностика правила">
                {result.diagnostics.map((diagnostic, index) => (
                  <li key={index}>
                    <UnstyledButton
                      className={styles.row}
                      onClick={() => {
                        if (diagnostic.nodeId)
                          props.onSelect({ kind: "node", id: diagnostic.nodeId });
                        else if (diagnostic.edgeId)
                          props.onSelect({ kind: "edge", id: diagnostic.edgeId });
                        else props.onSelect(null);
                      }}
                    >
                      {diagnostic.severity === "warning" ? "⚠ Предупреждение: " : "! Ошибка: "}
                      {diagnostic.message}
                      <Text size="xs" c="dimmed">
                        {diagnostic.pointer}
                      </Text>
                    </UnstyledButton>
                    {props.onSource && (
                      <Button
                        size="compact-xs"
                        variant="subtle"
                        onClick={() => props.onSource?.(diagnostic.pointer)}
                      >
                        Поле в исходнике
                      </Button>
                    )}
                  </li>
                ))}
              </ul>
              {simulation && simulation.outcome !== "invalid" && (
                <>
                  {simulation.outcome === "fallback" && (
                    <Alert>
                      Передать стандартной обработке. Конкретный ответ в этой симуляции не
                      рассчитывается.
                    </Alert>
                  )}
                  <Text fw={600}>Суммарная задержка: {simulation.totalDelayMs ?? 0} мс</Text>
                  <Text size="xs" c="dimmed">
                    Расчётное время: симуляция не ждёт задержку.
                  </Text>
                  <ol className={styles.steps} aria-label="Шаги симуляции">
                    {simulation.trace.map((step) => (
                      <li key={step.step}>
                        <UnstyledButton
                          className={styles.row}
                          onClick={() => props.onSelect({ kind: "node", id: step.nodeId })}
                        >
                          {props.rule.nodes.find((node) => node.id === step.nodeId)?.name ||
                            step.nodeId}
                          {step.matched !== undefined &&
                            ` · Условие: ${step.matched ? "Да" : "Нет"}`}
                          {step.delayMs !== undefined && ` · ${step.delayMs} мс`}
                        </UnstyledButton>
                      </li>
                    ))}
                  </ol>
                  {simulation.response && (
                    <section aria-label="Результат ответа">
                      <Text fw={600}>Ответ {simulation.response.status}</Text>
                      <Text size="sm">{simulation.response.mediaType}</Text>
                      {simulation.response.headers.map((header, index) => (
                        <Text size="sm" key={index}>
                          {header.name}: {header.value}
                        </Text>
                      ))}
                      {simulation.response.bodyJSON !== undefined ? (
                        <pre className={styles.data}>
                          <code>{simulation.response.bodyJSON}</code>
                        </pre>
                      ) : (
                        <Text size="sm">Без тела ответа</Text>
                      )}
                      <Text size="xs" c="dimmed">
                        Схема тела и согласование Accept не проверяются. Ответ 201 не создаёт
                        сущность.
                      </Text>
                    </section>
                  )}
                </>
              )}
              <details className={styles.identity}>
                <summary>Источник результата: текущий буфер</summary>
                <Text size="xs">{result.source.documentHash}</Text>
                {simulation && <Text size="xs">Вход: {simulation.inputHash}</Text>}
              </details>
            </>
          )}
        </Stack>
      </div>
    </section>
  );
}

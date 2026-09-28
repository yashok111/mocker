import { useEffect, useRef, useState } from "react";
import { Alert, Badge, Button, Group, Stack, Text, Textarea } from "@mantine/core";
import {
  simulateStateDiagram,
  validateStateDiagram,
} from "@/api/generated/state-diagrams/state-diagrams";
import type { StateDiagramDiagnostic, StateDiagramSimulation } from "@/api/generated/schemas";
import { describeApiFailureDetailed } from "@/api/errors";
import type { ApiDocument } from "../api-designer/documentModel";
import type { StateDiagram } from "./model";
import styles from "./StateDiagram.module.css";

export default function StateSimulation({
  designId,
  diagram,
  document,
  onActiveState,
}: {
  designId: number;
  diagram: StateDiagram;
  document: ApiDocument;
  onActiveState: (id?: string) => void;
}) {
  const [seed, setSeed] = useState('{"paymentAllowed":true,"status":"created"}');
  const [result, setResult] = useState<StateDiagramSimulation | null>(null);
  const [diagnostics, setDiagnostics] = useState<StateDiagramDiagnostic[] | null>(null);
  const [path, setPath] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const epoch = useRef(0);
  useEffect(
    () => () => {
      epoch.current++;
    },
    [],
  );
  const proposal = { diagram, document: JSON.stringify(document) };
  const state = result?.stateId ?? diagram.initialStateId;
  async function evaluate(ids: string[], validation = false) {
    const ticket = ++epoch.current;
    setBusy(true);
    setError("");
    try {
      if (validation) {
        const response = await validateStateDiagram(designId, diagram.id, proposal);
        if (ticket !== epoch.current) return;
        if (response.status === 200) setDiagnostics(response.data.diagnostics);
      } else {
        const response = await simulateStateDiagram(designId, diagram.id, {
          ...proposal,
          dataJSON: seed,
          transitionIds: ids,
        });
        if (ticket !== epoch.current) return;
        if (response.status === 200) {
          setResult(response.data);
          setDiagnostics(response.data.diagnostics);
          setPath(response.data.steps.filter((s) => s.accepted).map((s) => s.transitionId));
          onActiveState(response.data.stateId);
        }
      }
    } catch (e) {
      if (ticket === epoch.current) setError(describeApiFailureDetailed(e));
    } finally {
      if (ticket === epoch.current) setBusy(false);
    }
  }
  function reset() {
    epoch.current++;
    setBusy(false);
    setResult(null);
    setPath([]);
    setError("");
    setDiagnostics(null);
    onActiveState(undefined);
  }
  return (
    <section className={styles.simulation} aria-label="Симуляция диаграммы">
      <Group justify="space-between" mb="sm">
        <Text fw={650}>Проверка и симуляция</Text>
        <Group gap="xs">
          <Button
            variant="default"
            size="xs"
            loading={busy}
            onClick={() => void evaluate([], true)}
          >
            Проверить диаграмму
          </Button>
          <Button variant="subtle" size="xs" onClick={reset}>
            Сбросить прогон
          </Button>
        </Group>
      </Group>
      <Text size="sm" c="dimmed" mb="md">
        Выберите действие и проследите изменения состояния и данных.
      </Text>
      <div className={styles.simulationGrid}>
        <Stack gap="sm">
          <Textarea
            label="Начальные данные JSON"
            value={seed}
            autosize
            minRows={3}
            maxRows={8}
            onChange={(e) => {
              setSeed(e.currentTarget.value);
              reset();
            }}
          />
          <Button variant="light" loading={busy} onClick={() => void evaluate([])}>
            Начать заново
          </Button>
          <Text size="sm" fw={600}>
            Действия
          </Text>
          <Group gap="xs">
            {diagram.transitions.map((t) => (
              <Button
                key={t.id}
                variant={t.from === state ? "filled" : "default"}
                size="xs"
                disabled={busy || path.length >= 100}
                onClick={() => void evaluate([...path, t.id])}
              >
                {t.name}
              </Button>
            ))}
          </Group>
          {path.length >= 100 ? (
            <Text size="sm">Достигнут лимит 100 шагов. Начните новый прогон.</Text>
          ) : null}
        </Stack>
        <Stack gap="xs" aria-live="polite">
          <Group>
            <Text size="sm">Текущее состояние:</Text>
            <Badge variant="light">
              {diagram.states.find((s) => s.id === state)?.name ?? "Не выбрано"}
            </Badge>
          </Group>
          {result ? (
            <pre className={styles.data} aria-label="Данные после перехода">
              {result.dataJSON}
            </pre>
          ) : (
            <Text size="sm" c="dimmed">
              Запустите симуляцию или выберите действие.
            </Text>
          )}
          {result?.steps.length ? (
            <ol>
              {result.steps.map((step, i) => (
                <li key={`${i}:${step.transitionId}`}>
                  <Text size="sm" c={step.accepted ? undefined : "red"}>
                    {diagram.transitions.find((t) => t.id === step.transitionId)?.name ??
                      step.transitionId}
                    :{" "}
                    {step.accepted
                      ? `выполнен · ${step.responseStatus} → ${diagram.states.find((s) => s.id === step.to)?.name}`
                      : `отказ — ${step.reason}`}
                  </Text>
                </li>
              ))}
            </ol>
          ) : null}
        </Stack>
      </div>
      {error ? (
        <Alert color="red" role="alert" mt="md">
          {error}
        </Alert>
      ) : null}
      {diagnostics !== null ? (
        <Stack gap={4} mt="md">
          {diagnostics.length === 0 ? (
            <Text c="green" size="sm" component="output">
              Ошибок и предупреждений нет.
            </Text>
          ) : (
            diagnostics.map((d, i) => (
              <Text
                key={`${d.elementId}:${i}`}
                size="sm"
                c={d.severity === "error" ? "red" : "dimmed"}
              >
                {d.severity === "error" ? "Ошибка" : "Предупреждение"}: {d.message} (
                {diagram.states.find((s) => s.id === d.elementId)?.name ??
                  diagram.transitions.find((t) => t.id === d.elementId)?.name ??
                  diagram.name}
                )
              </Text>
            ))
          )}
        </Stack>
      ) : null}
    </section>
  );
}

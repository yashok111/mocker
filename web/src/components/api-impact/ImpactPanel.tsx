import { useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Code,
  Divider,
  Group,
  NativeSelect,
  Paper,
  SegmentedControl,
  Stack,
  Switch,
  Text,
  Title,
  UnstyledButton,
} from "@mantine/core";
import { useMediaQuery } from "@mantine/hooks";
import { useGetApiDesignDiff } from "@/api/generated/api-designs/api-designs";
import type {
  ApiImpactEntity,
  ApiImpactEvidence,
  ApiImpactLocator,
  ApiImpactReport,
} from "@/api/generated/schemas";
import { describeApiFailureDetailed } from "@/api/errors";
import { SourceDiff } from "../api-designer/SourceEditor";
import ImpactGraph from "./ImpactGraph";
import {
  compatibilityLabel,
  directionLabel,
  entityLabel,
  impactGraphModel,
  kindLabel,
  sideLabel,
} from "./model";
import { useImpactAnalysis } from "./useImpactAnalysis";
import styles from "./Impact.module.css";

export type ImpactPanelProps = {
  designId: number;
  document: string;
  baseDocument: string;
  baseRevisionId: number;
  revisions: { id: number; version: number }[];
  pendingForm: boolean;
  pendingLayout: boolean;
  sourceError: string | null;
  onSource: (pointer: string, sharedOperation: boolean) => void;
  onScenario: (id: number) => void;
};
type Mode = "current" | "revisions";
type Focus = { pointer: string; side: "before" | "after" };

export default function ImpactPanel(props: ImpactPanelProps) {
  const [mode, setMode] = useState<Mode>("current");
  const [fromRevisionId, setFromRevisionId] = useState(props.baseRevisionId);
  const [toRevisionId, setToRevisionId] = useState(
    props.revisions.reduce(
      (latest, revision) => (revision.version > latest.version ? revision : latest),
      { id: props.baseRevisionId, version: -1 },
    ).id,
  );
  const request =
    mode === "current"
      ? { designId: props.designId, fromRevisionId: props.baseRevisionId, document: props.document }
      : { designId: props.designId, fromRevisionId, toRevisionId };
  const blocked =
    mode === "current" &&
    (props.pendingForm ||
      props.pendingLayout ||
      props.sourceError !== null ||
      props.baseRevisionId <= 0);
  const analysis = useImpactAnalysis(request, blocked);
  const revisions = props.revisions.map((revision) => ({
    value: String(revision.id),
    label: `Ревизия ${revision.id} · версия ${revision.version}`,
  }));
  return (
    <Stack gap="md" data-testid="api-impact-panel">
      <Group justify="space-between" align="end">
        <Stack gap="xs">
          <Title order={3}>Влияние изменений API</Title>
          <SegmentedControl
            value={mode}
            onChange={(value) => setMode(value as Mode)}
            aria-label="Режим анализа влияния"
            data={[
              { value: "current", label: "Текущие правки" },
              { value: "revisions", label: "Ревизии" },
            ]}
          />
        </Stack>
        <Button
          loading={analysis.pending}
          disabled={blocked}
          onClick={() => void analysis.analyze()}
        >
          Проанализировать
        </Button>
      </Group>
      {mode === "current" ? (
        <Text size="sm">Текущие правки → от ревизии {props.baseRevisionId}</Text>
      ) : (
        <Group align="end">
          <NativeSelect
            label="Базовая ревизия"
            data={revisions}
            value={fromRevisionId}
            onChange={(event) => setFromRevisionId(Number(event.currentTarget.value))}
          />
          <Text aria-hidden>→</Text>
          <NativeSelect
            label="Новая ревизия"
            data={revisions}
            value={toRevisionId}
            onChange={(event) => setToRevisionId(Number(event.currentTarget.value))}
          />
        </Group>
      )}
      {mode === "current" && (props.pendingForm || props.pendingLayout) && (
        <Alert color="yellow" aria-live="polite">
          Примените или отмените изменения в формах и расположении диаграмм. Они ещё не входят в
          исходник.
        </Alert>
      )}
      {mode === "current" && props.sourceError && (
        <Alert color="yellow" role="alert">
          {props.sourceError}
        </Alert>
      )}
      <Text size="xs" c="dimmed">
        Проверяются зависимости контракта и использование операций в текущих сохранённых сценариях.
        Совместимость конкретных bindings, assertions и extracts требует отдельной проверки. Анализ
        ничего не сохраняет.
      </Text>
      {analysis.error && (
        <Alert color="red" role="alert">
          {analysis.error}
        </Alert>
      )}
      {analysis.pending && <Text component="output">Анализируем зависимости…</Text>}
      {analysis.report ? (
        <ImpactResult
          key={`${analysis.report.fromHash}:${analysis.report.proposedHash}:${mode}`}
          report={analysis.report}
          mode={mode}
          {...props}
        />
      ) : (
        !analysis.pending &&
        !analysis.error &&
        !blocked && (
          <Text c="dimmed" component="output">
            Нажмите «Проанализировать», чтобы проверить выбранные документы.
          </Text>
        )
      )}
    </Stack>
  );
}

function ImpactResult({
  report,
  mode,
  ...props
}: ImpactPanelProps & { report: ApiImpactReport; mode: Mode }) {
  const [metadata, setMetadata] = useState(false);
  const [selectedId, setSelectedId] = useState<string>();
  const [focus, setFocus] = useState<Focus | null>(null);
  const narrow = useMediaQuery("(max-width: 62em)", false, { getInitialValueInEffect: false });
  const changes = report.changes.filter(
    (change) =>
      metadata || change.changeClass !== "metadata" || change.compatibility !== "compatible",
  );
  const selected = changes.find((change) => change.id === selectedId) ?? changes[0];
  const evidence = report.evidence.filter((item) => item.changeId === selected?.id);
  const grouped = new Map<string, ApiImpactEvidence[]>();
  for (const item of evidence) {
    const items = grouped.get(item.entityId);
    if (items) items.push(item);
    else grouped.set(item.entityId, [item]);
  }
  const entities = new Map(report.affected.map((entity) => [entity.id, entity]));
  const graph = selected ? impactGraphModel(report, selected) : null;
  const history = useGetApiDesignDiff(
    props.designId,
    { fromRevisionId: report.fromRevisionId, toRevisionId: report.toRevisionId },
    {
      query: {
        enabled: mode === "revisions" && focus !== null && report.toRevisionId !== undefined,
        retry: false,
      },
    },
  );
  const diff = history.data?.status === 200 ? history.data.data : null;
  const operationCount = report.affected.filter((entity) => entity.kind === "operation").length;
  const scenarioCount = new Set(
    report.affected
      .flatMap((entity) => [entity.before?.scenarioId, entity.after?.scenarioId])
      .filter(Boolean),
  ).size;
  const reviewCount = report.changes.filter((change) => change.compatibility === "review").length;

  return (
    <Stack gap="md">
      <Text size="sm" component="output">
        {report.complete ? "В отчёте" : "Найдено в проверенной части"}: изменений{" "}
        {report.changes.length} · операций {operationCount} · сценариев {scenarioCount} · требуют
        проверки {reviewCount} · диагностик {report.diagnostics.length}
      </Text>
      <Text size="xs" c="dimmed">
        Проверено сохранённых сценариев: {report.coverage.scenariosScanned}. Полнота зависимостей не
        доказывает совместимость всех клиентов.
      </Text>
      {!report.complete && (
        <Alert color="yellow" title="Результат неполный" aria-live="polite">
          Некоторые зависимости не удалось проверить. Найденные количества могут быть меньше полного
          набора.
          {report.coverage.truncatedReasons.map((reason) => (
            <Text size="sm" key={reason}>
              {limitLabel[reason]}
            </Text>
          ))}
        </Alert>
      )}
      {report.diagnostics.length > 0 && (
        <Stack gap="xs" aria-label="Диагностика анализа">
          {report.diagnostics.map((diagnostic, index) => {
            const entity = diagnostic.entityId ? entities.get(diagnostic.entityId) : undefined;
            const locator = entity?.[diagnostic.side] ?? entity?.after ?? entity?.before;
            return (
              <Alert
                key={`${diagnostic.code}:${index}`}
                color={diagnostic.severity === "error" ? "red" : "yellow"}
                title={`${diagnostic.side === "before" ? "Было" : diagnostic.side === "after" ? "Стало" : "Анализ"} · ${diagnostic.code}`}
              >
                <Text size="sm">{diagnostic.message}</Text>
                <Text size="xs" className={styles.pointer}>
                  {diagnostic.pointer}
                </Text>
                {entity && (
                  <Text size="sm" fw={600}>
                    {entityLabel[entity.kind]} · {entity.label}
                  </Text>
                )}
                {locator?.scenarioId && (
                  <ScenarioLocator
                    locator={locator}
                    fromRevisionId={report.fromRevisionId}
                    onScenario={props.onScenario}
                  />
                )}
              </Alert>
            );
          })}
        </Stack>
      )}
      <Switch
        checked={metadata}
        onChange={(event) => setMetadata(event.currentTarget.checked)}
        label="Показать совместимые метаданные"
      />
      {report.changes.length === 0 ? (
        <Text component="output">Изменений нет</Text>
      ) : changes.length === 0 ? (
        <Text component="output">Найдены только совместимые изменения метаданных.</Text>
      ) : (
        <div className={styles.columns}>
          <Stack gap="xs" className={styles.changes} aria-label="Изменения API">
            {changes.map((change) => (
              <UnstyledButton
                key={change.id}
                className={styles.change}
                aria-pressed={selected?.id === change.id}
                onClick={() => {
                  setSelectedId(change.id);
                  setFocus(null);
                }}
              >
                <Group gap="xs">
                  <Badge color={compatibilityColor(change.compatibility)}>
                    {compatibilityLabel[change.compatibility]}
                  </Badge>
                  <Text size="xs">{kindLabel[change.kind]}</Text>
                </Group>
                <Text size="sm" fw={600} mt="xs">
                  {change.explanation}
                </Text>
                <Text size="xs" ff="monospace" mt="xs">
                  {change.pointer || "/"}
                </Text>
              </UnstyledButton>
            ))}
          </Stack>
          <Stack gap="md">
            {selected && (
              <>
                <Paper withBorder p="md">
                  <Text size="sm" fw={600}>
                    Значение изменения
                  </Text>
                  <Group align="start" gap="lg" mt="xs">
                    <div>
                      <Text size="xs" c="dimmed">
                        Было
                      </Text>
                      <Code block className={styles.values}>
                        {selected.beforeTruncated
                          ? "Значение сокращено; откройте сравнение"
                          : (selected.beforeJSON ?? "Отсутствует")}
                      </Code>
                    </div>
                    <div>
                      <Text size="xs" c="dimmed">
                        Стало
                      </Text>
                      <Code block className={styles.values}>
                        {selected.afterTruncated
                          ? "Значение сокращено; откройте сравнение"
                          : (selected.afterJSON ?? "Отсутствует")}
                      </Code>
                    </div>
                  </Group>
                  <Group mt="sm">
                    <Button
                      size="xs"
                      variant="default"
                      onClick={() =>
                        setFocus({
                          pointer: selected.pointer,
                          side: selected.kind === "removed" ? "before" : "after",
                        })
                      }
                    >
                      Сравнить изменение
                    </Button>
                    {mode === "current" && selected.kind !== "removed" && (
                      <Button
                        size="xs"
                        variant="subtle"
                        onClick={() => props.onSource(selected.pointer, false)}
                      >
                        Исходник
                      </Button>
                    )}
                  </Group>
                </Paper>
                {graph && (
                  <>
                    <ImpactGraph model={graph} changeId={selected.id} />
                    {graph.truncated && (
                      <Text size="xs" component="output">
                        Граф ограничен 100 узлами. Все возвращённые причины доступны в списке ниже.
                      </Text>
                    )}
                  </>
                )}
                <Text fw={600}>Причины влияния</Text>
                {grouped.size === 0 ? (
                  <Text size="sm">Зависимостей не найдено в проверенной части.</Text>
                ) : (
                  <ol className={styles.evidence}>
                    {[...grouped].map(([id, items]) => {
                      const entity = entities.get(id);
                      return entity ? (
                        <li key={id}>
                          <Text fw={600}>
                            {entityLabel[entity.kind]} · {entity.label}
                          </Text>
                          <Stack gap="sm" mt="xs">
                            {items.map((item) => (
                              <Evidence
                                key={item.id}
                                evidence={item}
                                entity={entity}
                                report={report}
                                local={mode === "current"}
                                onCompare={setFocus}
                                onSource={props.onSource}
                                onScenario={props.onScenario}
                              />
                            ))}
                          </Stack>
                        </li>
                      ) : null;
                    })}
                  </ol>
                )}
              </>
            )}
          </Stack>
        </div>
      )}
      {focus && (
        <>
          <Divider />
          <Group justify="space-between">
            <Text fw={600}>
              Сравнение · {sideLabel(focus.side)} · {focus.pointer || "/"}
            </Text>
            <Button variant="subtle" size="xs" onClick={() => setFocus(null)}>
              Закрыть сравнение
            </Button>
          </Group>
          {mode === "revisions" && history.isError ? (
            <Alert color="red" role="alert">
              {describeApiFailureDetailed(history.error)}
              <Button variant="subtle" size="xs" onClick={() => void history.refetch()}>
                Повторить загрузку сравнения
              </Button>
            </Alert>
          ) : mode === "revisions" && !diff ? (
            <Text component="output">Загружаем выбранные ревизии…</Text>
          ) : (
            <SourceDiff
              original={mode === "current" ? props.baseDocument : diff!.from.document}
              modified={mode === "current" ? props.document : diff!.to.document}
              narrow={narrow}
              focusPointer={focus.pointer}
              focusSide={focus.side}
            />
          )}
        </>
      )}
    </Stack>
  );
}

function Evidence({
  evidence,
  entity,
  report,
  local,
  onCompare,
  onSource,
  onScenario,
}: {
  evidence: ApiImpactEvidence;
  entity: ApiImpactEntity;
  report: ApiImpactReport;
  local: boolean;
  onCompare: (focus: Focus) => void;
  onSource: ImpactPanelProps["onSource"];
  onScenario: ImpactPanelProps["onScenario"];
}) {
  const locator = entity[evidence.side];
  const pointer = locator?.sourcePointer ?? locator?.pointer;
  return (
    <Paper withBorder p="sm">
      <Text size="xs" c="dimmed">
        {sideLabel(evidence.side)} · {directionLabel[evidence.direction]}
      </Text>
      <Text size="sm">{evidence.explanation}</Text>
      {locator && (
        <Text size="xs" className={styles.pointer}>
          {locator.pointer}
        </Text>
      )}
      {evidence.referenceSites.length > 0 && (
        <details>
          <summary>Цепочка ссылок</summary>
          <ol className={styles.evidence}>
            {evidence.referenceSites.map((site, index) => (
              <li key={`${site.pointer}:${index}`}>
                <Text size="xs" className={styles.pointer}>
                  {site.targetPointer} → {site.pointer} · {site.kind}
                </Text>
              </li>
            ))}
          </ol>
        </details>
      )}
      {locator?.scenarioId ? (
        <ScenarioLocator
          locator={locator}
          fromRevisionId={report.fromRevisionId}
          onScenario={onScenario}
        />
      ) : (
        pointer !== undefined && (
          <Group mt="xs">
            <Button
              size="xs"
              variant="default"
              onClick={() => onCompare({ pointer, side: evidence.side })}
            >
              Сравнение · {sideLabel(evidence.side)}
            </Button>
            {local && evidence.side === "after" && (
              <Button
                size="xs"
                variant="subtle"
                onClick={() =>
                  onSource(
                    pointer,
                    entity.kind === "operation" &&
                      Boolean(locator?.sourcePointer && locator.sourcePointer !== locator.pointer),
                  )
                }
              >
                Исходник
              </Button>
            )}
          </Group>
        )
      )}
    </Paper>
  );
}

function ScenarioLocator({
  locator,
  fromRevisionId,
  onScenario,
}: {
  locator: ApiImpactLocator;
  fromRevisionId: number;
  onScenario: ImpactPanelProps["onScenario"];
}) {
  return (
    <Stack gap="xs" mt="xs">
      <Text size="xs">
        {locator.scenarioName} · ревизия сценария {locator.scenarioRevision} · контракт{" "}
        {locator.contractId} · API-ревизия {locator.pinnedRevisionId} ·{" "}
        {locator.mode === "copy" ? "Копия" : "Связанный"}
      </Text>
      {locator.mode === "copy" && (
        <Text size="xs">
          Копия контракта могла быть изменена; последствия обновления требуют отдельной проверки.
        </Text>
      )}
      {locator.pinnedRevisionId !== fromRevisionId && (
        <Text size="xs">Ревизия контракта шага отличается от базы сравнения.</Text>
      )}
      <Text size="xs" c="dimmed">
        В отчёте показана ревизия {locator.scenarioRevision}. Текущая версия сценария могла
        измениться.
      </Text>
      <Button size="xs" variant="default" onClick={() => onScenario(locator.scenarioId!)}>
        Открыть текущий сценарий
      </Button>
    </Stack>
  );
}

function compatibilityColor(value: string) {
  return value === "breaking" ? "red" : value === "compatible" ? "teal" : "yellow";
}
const limitLabel: Record<string, string> = {
  changes: "Лимит изменений",
  references: "Лимит ссылок",
  traversal: "Лимит обхода зависимостей",
  entities: "Лимит затронутых элементов",
  evidence: "Лимит причин",
  scenario_scan: "Лимит сценариев",
  scenario_usages: "Лимит использований в сценариях",
  scenario_bytes: "Лимит объёма сценариев",
  output: "Лимит размера ответа",
};

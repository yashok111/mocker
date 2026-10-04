import {
  projectionTarget,
  projectionKey,
  projectionURLTarget,
  type ProjectionReadProps,
} from "./backendEffectiveProjectionReads";
import { lazy, Suspense, useEffect, useRef, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Code,
  Group,
  NativeSelect,
  Paper,
  Stack,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import type {
  BackendEventsResponse as BackendEventsPage,
  BackendEventsScalar,
  BackendEventsTrigger,
  BackendLineageValueRef,
  QueryBackendEventsRequest,
} from "@/api/generated/schemas";
import { CoverageDetails, LoadState, Pages } from "./BackendGraphInventory";
import {
  databaseButtonStyles,
  databaseStatus,
  databaseWrap,
  useDatabaseCancellation,
} from "./backendDatabaseReads";
import {
  captureEventsFragment,
  eventsItemKey,
  eventsPayload,
  eventsReason,
  readEventsGapEvidence,
  readEventsFieldSeeds,
  useEventsPage,
  type EventsItem,
} from "./backendEventsReads";
import { lineageRefKey } from "./backendLineageReads";
import { BackendLineageActions } from "./BackendLineageActions";
import { BackendValueInspector } from "./BackendValueInspector";
import { BackendFlowInspector } from "./BackendFlowInspector";
import type { BackendSourcePin, FlowSelection } from "./backendFlowReads";
import { useBackendAPIDeparture } from "./useBackendAPIDeparture";

type Props = ProjectionReadProps & {
  projectId: string;
  semanticHash?: string;
  onFlowNavigate?: (pin: BackendSourcePin) => void;
};
const BackendFlow = lazy(() =>
  import("./BackendFlow").then((module) => ({ default: module.BackendFlow })),
);
export function BackendEvents(props: Props) {
  return (
    <EventsWorkspace
      key={`${props.projectId}:${projectionKey(projectionTarget(props), props.pins)}:${props.semanticHash}`}
      {...props}
    />
  );
}
function EventsWorkspace({
  projectId,
  revisionId: selectedRevision,
  target: explicitTarget,
  pins,
  semanticHash: selectedHash,
  onFlowNavigate,
}: Props) {
  const target = projectionTarget({ revisionId: selectedRevision, target: explicitTarget, pins });
  const revisionId = pins?.baseRevisionId ?? selectedRevision ?? "";
  const semanticHash = pins?.effectiveSemanticHash ?? selectedHash ?? "";
  const [view, setView] = useState<BackendEventsPage["view"]>("routes");
  const [filter, setFilter] = useState("");
  const [draft, setDraft] = useState("");
  const [cursors, setCursors] = useState([""]);
  const [fields, setFields] = useState<EventsItem>();
  const [value, setValue] = useState<BackendLineageValueRef>();
  const [selection, setSelection] = useState<FlowSelection>();
  const [flow, setFlow] = useState<BackendSourcePin>();
  const [gap, setGap] = useState<{ item: EventsItem; page: BackendEventsPage }>();
  const origin = useRef<HTMLElement | null>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const depart = useBackendAPIDeparture();
  const input: QueryBackendEventsRequest =
    view === "routes"
      ? {
          ...target,
          view,
          ...(filter ? { seedNodeId: filter } : {}),
          limit: 50,
          cursor: cursors.at(-1) ?? "",
        }
      : {
          ...target,
          view,
          ...(filter ? { serviceId: filter } : {}),
          limit: 50,
          cursor: cursors.at(-1) ?? "",
        };
  const query = useEventsPage(projectId, input, semanticHash, pins);
  const page = query.data;
  function reset(nextFilter: string, nextView = view) {
    depart(() => {
      setFilter(nextFilter);
      setDraft(nextFilter);
      setView(nextView);
      setCursors([""]);
      setFields(undefined);
      setValue(undefined);
      setSelection(undefined);
      requestAnimationFrame(() => heading.current?.focus());
    });
  }
  function openRecord(next: FlowSelection, trigger?: HTMLElement) {
    depart(() => {
      origin.current = trigger ?? origin.current;
      setSelection(next);
      setValue(undefined);
    });
  }
  function openFlow(pin: BackendSourcePin) {
    depart(() => {
      if (onFlowNavigate) onFlowNavigate(pin);
      else setFlow(pin);
    });
  }
  function restore() {
    requestAnimationFrame(() =>
      (origin.current?.isConnected ? origin.current : heading.current)?.focus(),
    );
  }
  function button(label: string, id: string | undefined, type: "node" | "edge" = "node") {
    return id ? (
      <Button
        variant="subtle"
        h="auto"
        styles={databaseButtonStyles}
        onClick={(e) => openRecord({ type, id }, e.currentTarget)}
      >
        {label} {id}
      </Button>
    ) : null;
  }
  return (
    <Paper
      component="section"
      withBorder
      p="md"
      style={{ minWidth: 0 }}
      aria-label="События и задачи исходников"
    >
      <Stack>
        <Title order={2} ref={heading} tabIndex={-1}>
          События и задачи
        </Title>
        <Text size="sm" c="dimmed" style={databaseWrap}>
          {target.changeProposal
            ? "Желаемая структура предложения · базовая ревизия"
            : "Статическая модель исходников · только чтение · ревизия"}{" "}
          {revisionId}
        </Text>
        <Text size="sm">
          Объявленные маршруты, расписания и вызовы. Доставка, запуск задачи и атомарность доставки
          не установлены.
        </Text>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            reset(draft.trim());
          }}
        >
          <Group align="flex-end">
            <NativeSelect
              label="Представление событий"
              value={view}
              data={[
                { value: "routes", label: "Маршруты событий" },
                { value: "jobs", label: "Фоновые задачи" },
                { value: "service_calls", label: "Вызовы сервисов" },
              ]}
              onChange={(e) => reset("", e.currentTarget.value as BackendEventsPage["view"])}
              style={{ flex: "1 1 200px", minWidth: 0 }}
            />
            <TextInput
              label={
                view === "routes"
                  ? "ID отправителя, сообщения, канала или получателя"
                  : "ID сервиса"
              }
              value={draft}
              onChange={(e) => setDraft(e.currentTarget.value)}
              style={{ flex: "1 1 240px", minWidth: 0 }}
            />
            <Button type="submit">Показать область</Button>
            {filter && (
              <Button variant="default" onClick={() => reset("")}>
                Весь проект
              </Button>
            )}
          </Group>
        </form>
        <LoadState query={query} label="событий и задач" />
        {page && (
          <>
            <EventsNotice page={page} />
            <Text component="output" aria-live="polite">
              На странице: {page.items.length} · страница {cursors.length}
            </Text>
            {!page.items.length && (
              <Text>
                В этой странице и области анализа нет результатов. Другие маршруты и зависимости
                неизвестны.
              </Text>
            )}
            <Stack aria-label="Результаты событий" gap="md">
              {page.items.map((item) => {
                const payload = eventsPayload(item),
                  refs = payload.references;
                return (
                  <Stack
                    key={eventsItemKey(item)}
                    gap="xs"
                    style={{
                      minWidth: 0,
                      borderTop: "1px solid var(--mantine-color-default-border)",
                      paddingTop: 12,
                    }}
                  >
                    <Title order={3} style={databaseWrap}>
                      {item.kind === "boundary"
                        ? "Граница анализа"
                        : item.kind === "job"
                          ? "Фоновая задача"
                          : item.kind === "service_call"
                            ? "Вызов сервиса"
                            : "Маршрут события"}
                    </Title>
                    <Badge w="fit-content">{databaseStatus(payload.witness.status)}</Badge>
                    {item.kind === "boundary" && (
                      <Alert color="yellow" style={databaseWrap}>
                        {eventsReason(item.boundary.reason)}
                      </Alert>
                    )}
                    <Group>
                      {button("Отправитель", refs.producerId)}
                      {button("Сообщение", refs.messageId)}
                      {button("Канал", refs.channelId)}
                      {button("Получатель", refs.consumerId)}
                      {button("Задача", refs.jobId)}
                      {button("Шаг вызова", refs.callStepId)}
                      {button("Операция", refs.operationId)}
                      {button("Цель", refs.targetId)}
                      {button("Сервис цели", refs.targetServiceId)}
                    </Group>
                    <Group>
                      {button("Объявление отправки", refs.emitsEdgeId, "edge")}
                      {button("Маршрут доставки", refs.deliveryEdgeId, "edge")}
                      {button("Связь вызова", refs.callsEdgeId, "edge")}
                    </Group>
                    {view === "routes" && (
                      <Group>
                        {refs.producerId && (
                          <Button
                            variant="default"
                            h="auto"
                            styles={databaseButtonStyles}
                            onClick={() => reset(refs.producerId!)}
                          >
                            Маршруты отправителя {refs.producerId}
                          </Button>
                        )}
                        {refs.consumerId && (
                          <Button
                            variant="default"
                            h="auto"
                            styles={databaseButtonStyles}
                            onClick={() => reset(refs.consumerId!)}
                          >
                            Маршруты получателя {refs.consumerId}
                          </Button>
                        )}
                      </Group>
                    )}
                    {"condition" in payload && payload.condition && (
                      <Text style={databaseWrap}>Условие: {scalar(payload.condition)}</Text>
                    )}
                    {"group" in payload && payload.group && (
                      <Text style={databaseWrap}>Группа: {scalar(payload.group)}</Text>
                    )}
                    {"trigger" in payload && payload.trigger && (
                      <Text style={databaseWrap}>
                        Статический триггер: {triggerLabel(payload.trigger)}
                      </Text>
                    )}
                    {!payload.dispatch.length && <Text>Известный handler не установлен.</Text>}
                    {payload.dispatch.map((dispatch, i) => (
                      <Stack key={`${dispatch.handlesEdgeId}:${i}`} gap="xs">
                        <Group>
                          {button("Handler", dispatch.handlerId)}
                          {button("Неустановленный handler", dispatch.unresolvedTargetId)}
                          {button("Связь обработчика", dispatch.handlesEdgeId, "edge")}
                          {dispatch.flowIds.map((flowId) => (
                            <Button
                              key={flowId}
                              variant="default"
                              h="auto"
                              styles={databaseButtonStyles}
                              onClick={() =>
                                openFlow({
                                  ...projectionURLTarget(target, revisionId),
                                  entrypointId: refs.consumerId ?? refs.jobId ?? refs.operationId,
                                  flowId,
                                  ...(dispatch.handlerId
                                    ? { recordId: dispatch.handlerId, recordType: "node" as const }
                                    : {}),
                                })
                              }
                            >
                              Открыть Flow {flowId}
                            </Button>
                          ))}
                        </Group>
                        {dispatch.witness.limitations.map((reason, j) => (
                          <Text key={j} size="sm" style={databaseWrap}>
                            {eventsReason(reason)}
                          </Text>
                        ))}
                      </Stack>
                    ))}
                    {payload.related?.map((related) => (
                      <Stack key={related.edgeId} gap={2}>
                        <Text fw={600}>
                          {related.kind === "retries"
                            ? "Повторная доставка"
                            : "Очередь недоставленных сообщений (DLQ)"}
                        </Text>
                        <Text style={databaseWrap}>{related.reason}</Text>
                        <Text size="sm">{databaseStatus(related.witness.status)}</Text>
                        {related.witness.limitations.map((reason, i) => (
                          <Text key={i} size="sm" style={databaseWrap}>
                            {eventsReason(reason)}
                          </Text>
                        ))}
                        {related.kind === "retries" && (
                          <>
                            <Text style={databaseWrap}>Задержка: {scalar(related.delay)}</Text>
                            <Text style={databaseWrap}>Попытки: {scalar(related.maxAttempts)}</Text>
                          </>
                        )}
                        <Group>
                          {button("Альтернативный маршрут", related.edgeId, "edge")}
                          {button("Канал", related.channelId)}
                        </Group>
                      </Stack>
                    ))}
                    {"emitContext" in payload && payload.emitContext && (
                      <Stack gap="xs">
                        <Text fw={600}>Локальный контекст отправки</Text>
                        <Text style={databaseWrap}>
                          Транзакция:{" "}
                          {payload.emitContext.transaction.status === "known"
                            ? payload.emitContext.transaction.transactionId
                            : payload.emitContext.transaction.reason}
                        </Text>
                        {button(
                          "Исходная транзакция",
                          payload.emitContext.transaction.status === "known"
                            ? payload.emitContext.transaction.transactionId
                            : undefined,
                        )}
                        {payload.emitContext.flowId && (
                          <Button
                            variant="default"
                            h="auto"
                            styles={databaseButtonStyles}
                            onClick={() =>
                              openFlow({
                                ...projectionURLTarget(target, revisionId),
                                flowId: payload.emitContext!.flowId,
                                recordId: refs.producerId,
                                recordType: "node",
                              })
                            }
                          >
                            Flow отправителя {payload.emitContext.flowId}
                          </Button>
                        )}
                        {payload.emitContext.controlWitness && (
                          <>
                            <Text size="sm">
                              Свидетельство пути управления; возможный путь до commit не доказывает
                              атомарность доставки.
                            </Text>
                            <Group>
                              {payload.emitContext.controlWitness.edgeIds.map((id) => (
                                <span key={id}>{button("Переход управления", id, "edge")}</span>
                              ))}
                            </Group>
                          </>
                        )}
                        {payload.emitContext.limitations.map((reason, i) => (
                          <Text key={i} size="sm" style={databaseWrap}>
                            {eventsReason(reason)}
                          </Text>
                        ))}
                      </Stack>
                    )}
                    {payload.witness.limitations.map((reason, i) => (
                      <Text key={i} size="sm" style={databaseWrap}>
                        {eventsReason(reason)}
                      </Text>
                    ))}
                    <Text size="xs" style={databaseWrap}>
                      {payload.witness.provenance === "desired"
                        ? "Исторические основания объектов базовой ревизии"
                        : "Основания"}
                      : {payload.witness.evidenceIds.join(", ") || "не указаны"}
                    </Text>
                    <Group>
                      {refs.messageId && (
                        <Button
                          variant="default"
                          h="auto"
                          styles={databaseButtonStyles}
                          onClick={(e) =>
                            depart(() => {
                              origin.current = e.currentTarget;
                              setFields(item);
                            })
                          }
                        >
                          Поля сообщения {refs.messageId}
                        </Button>
                      )}
                      <Button
                        variant="default"
                        h="auto"
                        styles={databaseButtonStyles}
                        onClick={(e) =>
                          depart(() => {
                            origin.current = e.currentTarget;
                            setGap({ item, page });
                          })
                        }
                      >
                        Подготовить исследование пробела
                      </Button>
                    </Group>
                  </Stack>
                );
              })}
            </Stack>
            <Pages
              label="событий"
              cursors={cursors}
              setCursors={(next) => depart(() => setCursors(next))}
              busy={query.isFetching}
              next={page.nextCursor}
            />
          </>
        )}
        {fields && (
          <EventFieldSeeds
            key={eventsItemKey(fields)}
            projectId={projectId}
            revisionId={revisionId}
            target={target}
            pins={pins}
            item={fields}
            onValueSelect={(ref, trigger) =>
              depart(() => {
                origin.current = trigger;
                setValue(ref);
              })
            }
          />
        )}
        {value && (
          <BackendValueInspector
            key={lineageRefKey(value)}
            projectId={projectId}
            revisionId={revisionId}
            target={target}
            pins={pins}
            value={value}
            onClose={() =>
              depart(() => {
                setValue(undefined);
                restore();
              })
            }
          />
        )}
        {selection?.valueRef ? (
          <BackendValueInspector
            key={lineageRefKey(selection.valueRef)}
            projectId={projectId}
            revisionId={revisionId}
            target={target}
            pins={pins}
            value={selection.valueRef}
            onClose={() =>
              depart(() => {
                setSelection(undefined);
                restore();
              })
            }
          />
        ) : (
          selection && (
            <BackendFlowInspector
              key={JSON.stringify(selection)}
              projectId={projectId}
              revisionId={revisionId}
              target={target}
              pins={pins}
              selection={selection}
              onSelect={(next) => openRecord(next)}
              onClose={() =>
                depart(() => {
                  setSelection(undefined);
                  restore();
                })
              }
            />
          )
        )}
        {flow && (
          <Suspense
            fallback={
              <Text component="output" aria-live="polite">
                Загружаем Flow
              </Text>
            }
          >
            <BackendFlow
              projectId={projectId}
              revisionId={revisionId}
              target={target}
              pins={pins}
              pin={flow}
              onPinChange={setFlow}
            />
          </Suspense>
        )}
        {gap && (
          <EventsGap
            key={eventsItemKey(gap.item)}
            projectId={projectId}
            page={gap.page}
            item={gap.item}
            onClose={() => {
              setGap(undefined);
              restore();
            }}
          />
        )}
      </Stack>
    </Paper>
  );
}
function scalar(value: BackendEventsScalar) {
  return value.status === "known" ? value.value : `Неизвестно: ${value.reason}`;
}
function triggerLabel(trigger: BackendEventsTrigger) {
  switch (trigger.kind) {
    case "cron":
      return `cron ${scalar(trigger.expression)} · timezone ${scalar(trigger.timezone)}`;
    case "interval":
      return `интервал ${scalar(trigger.duration)}`;
    case "manual":
      return "Ручной запуск";
    case "unknown":
      return `Неизвестно: ${trigger.reason}`;
  }
}
export function EventsNotice({ page }: { page: BackendEventsPage }) {
  return (
    <Stack gap="xs">
      <Text size="sm">
        {page.complete
          ? "Перечисление выбранной области завершено"
          : "Перечисление выбранной области неполно"}
        . Полнота исходников, доставка и исполнение неизвестны.
      </Text>
      <Text size="sm">
        Изучено связей: {page.examinedEdgeCount} из {page.totalEdgeCount} · построено результатов:{" "}
        {page.constructedItemCount} · вспомогательных объектов: {page.auxiliaryRecordCount}
      </Text>
      {page.truncated && (
        <Alert color="yellow">
          Чтение ограничено: {page.truncationReasons.map(eventsReason).join("; ")}. Отсутствие
          маршрута в странице не доказывает его отсутствие.
        </Alert>
      )}
      {page.limitations.map((reason, i) => (
        <Text key={i} size="sm" style={databaseWrap}>
          {eventsReason(reason)}
        </Text>
      ))}
      <details>
        <summary>Покрытие и границы чтения</summary>
        <CoverageDetails data={page.coverage} />
        <Text size="sm">
          Связей до {page.limits.maxExaminedEdges}; результатов до {page.limits.maxItems};
          вспомогательных объектов до {page.limits.maxAuxiliaryRecords}; свидетельство до{" "}
          {page.limits.maxWitnessRecords}. Снимок принимается к чтению при возможности полного
          сканирования связей.
        </Text>
      </details>
    </Stack>
  );
}
function EventFieldSeeds({
  projectId,
  revisionId,
  target: explicitTarget,
  pins,
  item,
  onValueSelect,
}: ProjectionReadProps & {
  projectId: string;
  item: EventsItem;
  onValueSelect: (ref: BackendLineageValueRef, trigger: HTMLElement) => void;
}) {
  const target = projectionTarget({ revisionId, target: explicitTarget, pins });
  const refs = eventsPayload(item).references;
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    heading.current?.focus();
  }, []);
  const key = ["backend-event-fields", projectId, projectionKey(target, pins), eventsItemKey(item)];
  useDatabaseCancellation(key);
  const query = useQuery({
    queryKey: key,
    retry: false,
    staleTime: Infinity,
    enabled: !!refs.messageId,
    queryFn: ({ signal }) => readEventsFieldSeeds(projectId, target, refs, signal, pins),
  });
  return (
    <Stack aria-label="Значения сообщения">
      <Title order={3} ref={heading} tabIndex={-1}>
        Поля сообщения в точном маршруте
      </Title>
      <LoadState query={query} label="полей сообщения" />
      {query.data?.limitations.map((reason, i) => (
        <Text key={i} size="sm" style={databaseWrap}>
          {reason}
        </Text>
      ))}
      {query.data?.nodes.map((node) => (
        <Stack key={node.id} gap="xs">
          <Text fw={600}>{node.name}</Text>
          {query.data?.addresses.map(({ endpointId, routeId }) => {
            if (!endpointId || !routeId) return null;
            const ref: BackendLineageValueRef = {
              kind: "event_field",
              nodeId: node.id,
              endpointId,
              routeId,
            };
            return (
              <Stack key={lineageRefKey(ref)} gap="xs">
                <Button
                  variant="subtle"
                  h="auto"
                  styles={databaseButtonStyles}
                  onClick={(e) => onValueSelect(ref, e.currentTarget)}
                >
                  Открыть значение {node.name} · {endpointId} · {routeId}
                </Button>
                <BackendLineageActions
                  projectId={projectId}
                  revisionId={revisionId}
                  target={target}
                  pins={pins}
                  seed={ref}
                  onValueSelect={(next) =>
                    onValueSelect(next, document.activeElement as HTMLElement)
                  }
                />
              </Stack>
            );
          })}
        </Stack>
      ))}
      {query.data?.nodes.length === 0 && (
        <Text>Поля сообщения не импортированы в выбранном снимке.</Text>
      )}
    </Stack>
  );
}
function EventsGap({
  projectId,
  page,
  item,
  onClose,
}: {
  projectId: string;
  page: BackendEventsPage;
  item: EventsItem;
  onClose: () => void;
}) {
  // Captured source context remains immutable if the list filters or project head change.
  const [capture] = useState(() => {
    let filesRemaining = 256;
    return {
      target: ("target" in page ? page.target : undefined) ?? { revisionId: page.revisionId },
      pins: "pins" in page ? page.pins : undefined,
      revisionId: page.revisionId,
      semanticHash: page.semanticHash,
      ...captureEventsFragment(item),
      view: page.view,
      coverage: page.coverage.coverage,
      availableSearchScope: page.coverage.snapshots.slice(0, 64).map((snapshot) => {
        const files = snapshot.files.slice(0, filesRemaining);
        filesRemaining -= files.length;
        return {
          repositoryId: snapshot.repositoryId,
          snapshotId: snapshot.id,
          manifestHash: snapshot.manifestHash,
          files,
          filesTruncated: snapshot.files.length > files.length,
        };
      }),
      scopeTruncated:
        page.coverage.snapshots.length > 64 ||
        page.coverage.snapshots.reduce((n, s) => n + s.files.length, 0) > 256,
      enumeration: {
        complete: page.complete,
        truncated: page.truncated,
        truncationReasons: page.truncationReasons,
        limitations: page.limitations,
      },
    };
  });
  const [question, setQuestion] = useState("");
  const [criterion, setCriterion] = useState("");
  const [requested, setRequested] = useState<{ question: string; completionCriterion: string }>();
  const key = [
    "backend-events-gap",
    projectId,
    projectionKey(capture.target, capture.pins),
    capture.semanticHash,
    eventsItemKey(item),
  ];
  useDatabaseCancellation(key);
  const query = useQuery({
    queryKey: key,
    enabled: !!requested,
    retry: false,
    staleTime: Infinity,
    queryFn: ({ signal }) =>
      readEventsGapEvidence(projectId, capture.target, item, signal, capture.pins),
  });
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    heading.current?.focus();
  }, []);
  const depart = useBackendAPIDeparture();
  const result =
    requested && query.data
      ? { format: "backend-events-gap-v1", projectId, ...capture, ...requested, ...query.data }
      : undefined;
  const text = result ? JSON.stringify(result, null, 2) : "";
  return (
    <Stack component="section" aria-label="Исследование пробела" style={{ minWidth: 0 }}>
      <Title order={3} ref={heading} tabIndex={-1}>
        Контекст исследования пробела
      </Title>
      <Text size="sm" style={databaseWrap}>
        Ревизия {capture.revisionId} · {capture.semanticHash}. После импорта оснований откройте
        точную новую ревизию через историю. При недоступном исходнике сохраните причину и
        проверенную область.
      </Text>
      <Textarea
        label="Вопрос исследования"
        value={question}
        maxLength={4096}
        onChange={(e) => setQuestion(e.currentTarget.value)}
        autosize
        minRows={2}
      />
      <Textarea
        label="Критерий завершения"
        value={criterion}
        maxLength={4096}
        onChange={(e) => setCriterion(e.currentTarget.value)}
        autosize
        minRows={2}
      />
      <Group>
        <Button
          disabled={!question.trim() || !criterion.trim() || query.isFetching}
          onClick={() =>
            setRequested({ question: question.trim(), completionCriterion: criterion.trim() })
          }
        >
          Собрать контекст исследования
        </Button>
        <Button variant="default" onClick={() => depart(onClose)}>
          Закрыть исследование
        </Button>
      </Group>
      {requested && <LoadState query={query} label="оснований исследования" />}
      {result && (
        <>
          {(result.truncated ||
            result.missingEvidenceIds.length > 0 ||
            result.fragmentTruncated ||
            result.scopeTruncated) && (
            <Alert color="yellow">
              Контекст ограничен: часть фрагмента, области или оснований не загружена.
              Неустановленная зависимость остаётся неизвестной.
            </Alert>
          )}
          <Code block aria-label="Контекст исследования JSON" style={databaseWrap}>
            {text}
          </Code>
          <Button
            component="a"
            download={`backend-events-gap-${capture.revisionId}.json`}
            href={`data:application/json;charset=utf-8,${encodeURIComponent(text)}`}
            variant="default"
          >
            Скачать контекст исследования
          </Button>
        </>
      )}
    </Stack>
  );
}

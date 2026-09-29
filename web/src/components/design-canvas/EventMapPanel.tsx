import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import { Alert, Button, Group, Modal, Stack, Text, TextInput } from "@mantine/core";
import type {
  DesignScenarioEventMapEdge,
  DesignScenarioEventMapLocator,
  DesignScenarioEventMapNode,
  DesignScenarioEventMapReport,
} from "@/api/generated/schemas";
import { listCanvasOperations } from "./canvasOperations";
import { readDiagrams } from "../state-diagram/model";
import type { CanvasDocument } from "./types";
import { graphSubset } from "./eventMapSubset";
import styles from "./EventMap.module.css";

const EventMapGraph = lazy(() => import("./EventMapGraph"));

export type EventEditorTarget = { kind: string; id: string; pointer?: string };
export type EventMapPanelProps = {
  opened: boolean;
  onClose: () => void;
  document: CanvasDocument;
  baseRevisionId: number;
  dirty: boolean;
  pendingForms: boolean;
  getEventMap: (revisionId: number, signal: AbortSignal) => Promise<DesignScenarioEventMapReport>;
  analyzeEventMap: (
    document: CanvasDocument,
    signal: AbortSignal,
  ) => Promise<DesignScenarioEventMapReport>;
  onEditEvent: (target: EventEditorTarget) => void;
};

const nodeKind: Record<DesignScenarioEventMapNode["kind"], string> = {
  participant: "Приложение",
  server: "Kafka сервер",
  channel: "Topic",
  message: "Тип события",
  schema: "JSON-схема",
  operation: "Операция события",
  api_operation: "Операция API",
  state_transition: "Переход состояния",
};
const edgeKind: Record<DesignScenarioEventMapEdge["kind"], string> = {
  ownership: "Владеет",
  server_channel: "Сервер topic",
  channel_message: "Тип на topic",
  payload: "Payload",
  key: "Kafka key",
  headers: "Headers",
  send: "Отправляет",
  receive: "Получает",
  retry: "retry",
  dead_letter: "DLQ",
  api_link: "Связь API",
  state_link: "Связь с переходом",
};
const PAGE_SIZE = 50;

function targetForPointer(document: CanvasDocument, pointer: string): EventEditorTarget | null {
  const parts = pointer.split("/").filter(Boolean);
  if (parts[0] !== "eventModel") return null;
  const collection = parts[1];
  const index = Number(parts[2]);
  if (!Number.isInteger(index) || index < 0) return null;
  const entity =
    collection &&
    document.eventModel?.[collection as keyof NonNullable<CanvasDocument["eventModel"]>]?.[index];
  if (!entity) return null;
  const kind = (
    {
      servers: "event-server",
      channels: "event-channel",
      messages: "event-message",
      schemas: "event-schema",
      contracts: "event-contract",
    } as Record<string, string>
  )[collection];
  return kind ? { kind, id: entity.id, pointer } : null;
}

function targetForSelection(
  document: CanvasDocument,
  item: DesignScenarioEventMapNode | DesignScenarioEventMapEdge,
) {
  const locator = item.locator;
  if (item.kind === "api_operation" || item.kind === "state_transition") return null;
  if (locator.contractId && locator.operationId)
    return { kind: "event-contract", id: locator.contractId, pointer: locator.pointer };
  if ("kind" in item && item.kind === "participant") return null;
  return targetForPointer(document, locator.pointer);
}

function EmbeddedSnapshot({
  document,
  locator,
}: {
  document: CanvasDocument;
  locator: DesignScenarioEventMapLocator;
}) {
  if (!locator.httpContractId) return null;
  const contract = document.contracts.find((item) => item.id === locator.httpContractId);
  if (!contract) return <Alert color="yellow">Встроенный HTTP-контракт больше не найден.</Alert>;
  let summary = "";
  if (locator.operationKey) {
    const matches = listCanvasOperations(contract.document).filter(
      (item) => item.key === locator.operationKey,
    );
    summary =
      matches.length === 1
        ? `${matches[0]!.location.method.toUpperCase()} ${matches[0]!.location.path} · ${String(matches[0]!.operation.summary ?? "")}`
        : matches.length === 0
          ? "Операция больше не найдена в этом снимке."
          : "Ключ операции неоднозначен в этом снимке.";
  } else if (locator.diagramId && locator.transitionId) {
    try {
      const diagram = readDiagrams(contract.document).find((item) => item.id === locator.diagramId);
      const transition = diagram?.transitions.find((item) => item.id === locator.transitionId);
      summary = transition
        ? `${diagram!.name} · ${transition.name || transition.id} · ${transition.from} → ${transition.to}${transition.binding ? ` · ${transition.binding.method} ${transition.binding.path}` : " · без HTTP-привязки"}`
        : "Переход больше не найден в этом снимке.";
    } catch {
      summary = "Диаграмма состояний в этом снимке недоступна.";
    }
  }
  return (
    <Stack gap={4}>
      <Text size="sm" fw={600}>
        Встроенный снимок · {contract.name}
      </Text>
      <Text size="xs">
        {contract.mode === "linked" ? "Связанный контракт" : "Копия"}
        {contract.source ? ` · API-ревизия ${contract.source.revisionId}` : ""}
      </Text>
      <Text size="sm">{summary}</Text>
      <Text size="xs" c="dimmed">
        Показан документ, сохранённый в сценарии. Текущий черновик API может отличаться.
      </Text>
    </Stack>
  );
}

export default function EventMapPanel(props: EventMapPanelProps) {
  const [report, setReport] = useState<DesignScenarioEventMapReport | null>(null);
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);
  const [selectedId, setSelectedId] = useState("");
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(0);
  const [diagnosticPage, setDiagnosticPage] = useState(0);
  const callers = useRef({ get: props.getEventMap, analyze: props.analyzeEventMap });
  useEffect(() => {
    callers.current = { get: props.getEventMap, analyze: props.analyzeEventMap };
  }, [props.getEventMap, props.analyzeEventMap]);
  useEffect(() => {
    if (!props.opened || props.pendingForms) return;
    const controller = new AbortController();
    setReport(null);
    setError("");
    setPending(true);
    setPage(0);
    setDiagnosticPage(0);
    const request = props.dirty
      ? callers.current.analyze(props.document, controller.signal)
      : callers.current.get(props.baseRevisionId, controller.signal);
    void request
      .then((next) => {
        if (!controller.signal.aborted) setReport(next);
      })
      .catch((cause: unknown) => {
        if (!controller.signal.aborted)
          setError(cause instanceof Error ? cause.message : "Не удалось построить карту");
      })
      .finally(() => {
        if (!controller.signal.aborted) setPending(false);
      });
    return () => controller.abort();
  }, [props.opened, props.pendingForms, props.dirty, props.document, props.baseRevisionId]);
  const nodesById = useMemo(
    () => new Map(report?.nodes.map((node) => [node.id, node]) ?? []),
    [report],
  );
  const entries = useMemo(() => {
    if (!report) return [];
    const query = search.trim().toLocaleLowerCase();
    return [...report.nodes, ...report.edges].filter((item) => {
      if (!query) return true;
      const text =
        "source" in item
          ? `${edgeKind[item.kind]} ${item.label} ${nodesById.get(item.source)?.label ?? item.source} ${nodesById.get(item.target)?.label ?? item.target}`
          : `${nodeKind[item.kind]} ${item.label} ${item.groupId ?? ""} ${item.clientId ?? ""}`;
      return text.toLocaleLowerCase().includes(query);
    });
  }, [report, search, nodesById]);
  const selected =
    report && ([...report.nodes, ...report.edges].find((item) => item.id === selectedId) ?? null);
  const selectedTarget = selected ? targetForSelection(props.document, selected) : null;
  const selectedLocator = selected?.locator;
  const graph = useMemo(() => (report ? graphSubset(report) : null), [report]);
  const visible = entries.slice(page * PAGE_SIZE, (page + 1) * PAGE_SIZE);
  const diagnostics =
    report?.diagnostics.slice(diagnosticPage * PAGE_SIZE, (diagnosticPage + 1) * PAGE_SIZE) ?? [];
  const select = (id: string) => setSelectedId(id);
  const openTarget = (pointer?: string) => {
    const target = pointer ? targetForPointer(props.document, pointer) : selectedTarget;
    if (target) props.onEditEvent(target);
  };
  return (
    <Modal
      opened={props.opened}
      onClose={props.onClose}
      title="Карта событий"
      size="min(96vw, 1200px)"
      trapFocus
      returnFocus
    >
      <Stack gap="md">
        {props.pendingForms ? (
          <Alert color="yellow">
            Незавершённая правка JSON. Предпросмотр остановлен до применения или отмены изменений
            поля.
          </Alert>
        ) : null}
        {!props.pendingForms && pending ? (
          <Text component="output">Строим карту событий…</Text>
        ) : null}
        {!props.pendingForms && error ? (
          <Alert color="red" role="alert">
            {error}
          </Alert>
        ) : null}
        {!props.pendingForms && report ? (
          <>
            <Text size="sm" component="output">
              {report.proposed
                ? "Предпросмотр несохранённого черновика"
                : `Сохранённая ревизия ${report.revisionId ?? "—"}`}{" "}
              · версия сценария {report.version}
            </Text>
            {!report.complete ? (
              <Alert color="yellow" title="Карта неполная">
                Часть связей не разрешена или результат ограничен. Проверьте диагностику и полный
                список.
                {report.coverage.truncatedReasons.length
                  ? ` Ограничения: ${report.coverage.truncatedReasons.join(", ")}.`
                  : ""}
              </Alert>
            ) : null}
            <Text size="xs" c="dimmed">
              Узлов: {report.coverage.nodesReturned} · связей: {report.coverage.edgesReturned} ·
              диагностик: {report.coverage.diagnosticsReturned}. Информационные внешние границы не
              означают сбой доставки.
            </Text>
            {graph ? (
              <>
                <Suspense fallback={<Text>Загружаем схему…</Text>}>
                  <EventMapGraph
                    report={report}
                    subset={graph}
                    selectedId={selectedId}
                    onSelect={select}
                  />
                </Suspense>
                <Text size="xs" c="dimmed">
                  Диагностика на схеме: ⛔ ошибка · ⚠ предупреждение · ⓘ информация. Подробности в
                  списке ниже.
                </Text>
                {graph.nodes.length < report.nodes.length ||
                graph.edges.length < report.edges.length ? (
                  <Text size="xs">
                    На схеме показаны {graph.nodes.length} из {report.nodes.length} узлов и{" "}
                    {graph.edges.length} из {report.edges.length} связей. Все возвращённые элементы
                    доступны в списке ниже.
                  </Text>
                ) : null}
              </>
            ) : null}
            <section aria-label="Полный список карты событий" className={styles.list}>
              <Text fw={600}>Все узлы и связи</Text>
              <TextInput
                label="Поиск по карте"
                value={search}
                onChange={(event) => {
                  setSearch(event.currentTarget.value);
                  setPage(0);
                }}
              />
              <Text size="xs">Найдено: {entries.length}</Text>
              {visible.map((item) => {
                const isEdge = "source" in item;
                const label = isEdge
                  ? `${edgeKind[item.kind]} · ${nodesById.get(item.source)?.label ?? item.source} → ${nodesById.get(item.target)?.label ?? item.target}`
                  : `${nodeKind[item.kind]} · ${item.label}`;
                return (
                  <Button
                    key={item.id}
                    variant="subtle"
                    className={styles.listButton}
                    aria-pressed={selectedId === item.id}
                    onClick={() => select(item.id)}
                  >
                    {label}
                    {!isEdge && item.groupId
                      ? ` · ${item.groupId}${item.clientId ? ` · ${item.clientId}` : ""}`
                      : ""}
                  </Button>
                );
              })}
              <Group gap="xs">
                <Button
                  size="xs"
                  variant="default"
                  disabled={page === 0}
                  onClick={() => setPage(page - 1)}
                >
                  Назад
                </Button>
                <Text size="xs">
                  Страница {page + 1} из {Math.max(1, Math.ceil(entries.length / PAGE_SIZE))}
                </Text>
                <Button
                  size="xs"
                  variant="default"
                  disabled={(page + 1) * PAGE_SIZE >= entries.length}
                  onClick={() => setPage(page + 1)}
                >
                  Далее
                </Button>
              </Group>
            </section>
            {selected && selectedLocator ? (
              <section aria-label="Выбранный элемент карты" className={styles.inspector}>
                <Text fw={600}>
                  {"source" in selected ? edgeKind[selected.kind] : nodeKind[selected.kind]} ·{" "}
                  {selected.label}
                </Text>
                {"groupId" in selected && selected.groupId ? (
                  <Text size="sm">
                    Consumer group: {selected.groupId}
                    {selected.clientId ? ` · client ID: ${selected.clientId}` : ""}
                  </Text>
                ) : null}
                <Text size="xs" className={styles.pointer}>
                  {selectedLocator.pointer}
                </Text>
                <EmbeddedSnapshot document={props.document} locator={selectedLocator} />
                {"kind" in selected &&
                selected.kind === "message" &&
                props.document.eventModel?.messages.find(
                  (item) => item.id === selectedLocator.entityId,
                )
                  ? (() => {
                      const message = props.document.eventModel!.messages.find(
                        (item) => item.id === selectedLocator.entityId,
                      )!;
                      return (
                        <Stack gap={4}>
                          {(["payloadSchemaId", "keySchemaId", "headersSchemaId"] as const).map(
                            (field) =>
                              message[field] ? (
                                <Button
                                  key={field}
                                  variant="subtle"
                                  size="xs"
                                  onClick={() =>
                                    props.onEditEvent({
                                      kind: "event-schema",
                                      id: message[field]!,
                                      pointer: selectedLocator.pointer,
                                    })
                                  }
                                >
                                  {field === "payloadSchemaId"
                                    ? "Payload"
                                    : field === "keySchemaId"
                                      ? "Kafka key"
                                      : "Headers"}
                                  :{" "}
                                  {props.document.eventModel?.schemas.find(
                                    (schema) => schema.id === message[field],
                                  )?.name ?? message[field]}
                                </Button>
                              ) : null,
                          )}
                        </Stack>
                      );
                    })()
                  : null}
                {selectedTarget ? (
                  <Button size="xs" variant="default" onClick={() => openTarget()}>
                    Открыть в редакторе событий
                  </Button>
                ) : null}
              </section>
            ) : null}
            {report.diagnostics.length ? (
              <section aria-label="Диагностика карты событий" className={styles.list}>
                <Text fw={600}>Диагностика</Text>
                {diagnostics.map((diagnostic) => (
                  <Alert
                    key={diagnostic.id}
                    color={
                      diagnostic.severity === "error"
                        ? "red"
                        : diagnostic.severity === "warning"
                          ? "yellow"
                          : "gray"
                    }
                    title={diagnostic.code}
                  >
                    <Text size="sm">{diagnostic.message}</Text>
                    <Text size="xs" className={styles.pointer}>
                      {diagnostic.pointer}
                    </Text>
                    {targetForPointer(props.document, diagnostic.pointer) ? (
                      <Button
                        size="xs"
                        variant="default"
                        onClick={() => openTarget(diagnostic.pointer)}
                      >
                        Перейти к полю
                      </Button>
                    ) : null}
                  </Alert>
                ))}
                {report.diagnostics.length > PAGE_SIZE ? (
                  <Group gap="xs">
                    <Button
                      size="xs"
                      variant="default"
                      disabled={diagnosticPage === 0}
                      onClick={() => setDiagnosticPage(diagnosticPage - 1)}
                    >
                      Назад
                    </Button>
                    <Text size="xs">Страница {diagnosticPage + 1}</Text>
                    <Button
                      size="xs"
                      variant="default"
                      disabled={(diagnosticPage + 1) * PAGE_SIZE >= report.diagnostics.length}
                      onClick={() => setDiagnosticPage(diagnosticPage + 1)}
                    >
                      Далее
                    </Button>
                  </Group>
                ) : null}
              </section>
            ) : null}
          </>
        ) : null}
        <Group justify="flex-end">
          <Button variant="default" onClick={props.onClose}>
            Закрыть
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}

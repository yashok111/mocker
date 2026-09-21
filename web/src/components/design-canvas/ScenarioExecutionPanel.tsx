import {
  lazy,
  Suspense,
  useEffect,
  useMemo,
  useRef,
  useState,
  useCallback,
  type ReactElement,
} from "react";
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Checkbox,
  Group,
  Loader,
  Modal,
  NativeSelect,
  Stack,
  Tabs,
  Text,
  Textarea,
  TextInput,
  UnstyledButton,
} from "@mantine/core";
import { IconPlayerPlay, IconPlayerStop, IconPlus, IconTrash } from "@tabler/icons-react";
import { parseBrowserSafeJson } from "@/api/preciseJson";
import { ApiFailure } from "@/api/client";
import {
  canvasExecutionBlockReason,
  defaultStepExecution,
  type CanvasExecutionRunInput,
  type CanvasExecutionRunSummary,
  type CanvasExecutionReport,
  type CanvasExecutionStatus,
  type CanvasExecutionStepResult,
} from "./canvasExecution";
import { resolveOperation } from "./canvasModel";
import { createCanvasId } from "./canvasId";
import { validateCanvasExecutionSettings } from "./canvasStorage";
import type { CanvasDocument, CanvasSelection, CanvasStepExecution, JSONValue } from "./types";
import styles from "./ScenarioExecutionPanel.module.css";

const SequenceGraph = lazy(() => import("./SequenceGraph"));
const noMove = () => {};
const statusLabels: Record<CanvasExecutionStatus, string> = {
  pending: "Ожидает",
  running: "Выполняется",
  passed: "Успешно",
  failed: "Ошибка",
  skipped: "Пропущен",
  cancelled: "Отменён",
};
const statusColors: Record<CanvasExecutionStatus, string> = {
  pending: "gray",
  running: "blue",
  passed: "green",
  failed: "red",
  skipped: "gray",
  cancelled: "orange",
};

export interface ScenarioExecutionPanelProps {
  opened: boolean;
  onClose: () => void;
  document: CanvasDocument;
  revisionId: number;
  version: number;
  disabled: boolean;
  onChangeDocument: (document: CanvasDocument) => void;
  runScenario: (
    input: CanvasExecutionRunInput,
    signal: AbortSignal,
  ) => Promise<CanvasExecutionReport>;
  listRuns: (signal: AbortSignal) => Promise<CanvasExecutionRunSummary[]>;
  getRun: (runId: string, signal: AbortSignal) => Promise<CanvasExecutionReport>;
  cancelRun: (runId: string, signal: AbortSignal) => Promise<CanvasExecutionReport>;
}

type OwnedRun = {
  id: string;
  input: CanvasExecutionRunInput;
  version: number;
  creationSettled: boolean;
  confirmed: boolean;
  cancelRequested: boolean;
  terminal: boolean;
  cancelPromise?: Promise<void>;
};

function errorText(cause: unknown): string {
  return cause instanceof Error ? cause.message : "Сервер недоступен";
}

function runSummary(report: CanvasExecutionReport): CanvasExecutionRunSummary {
  const {
    id,
    scenarioId,
    revisionId,
    version,
    name,
    source,
    status,
    reason,
    startedAt,
    finishedAt,
  } = report;
  return {
    id,
    scenarioId,
    revisionId,
    version,
    name,
    source,
    status,
    reason,
    startedAt,
    finishedAt,
  };
}

type Pair = { name: string; value: string };
type StepForm = {
  enabled: boolean;
  pathParams: Pair[];
  query: Pair[];
  headers: Pair[];
  body: string;
  expectedStatus: string;
  assertions: { pointer: string; equals: string }[];
  extract: Pair[];
};
type SettingsForm = { variables: Pair[]; steps: Record<string, StepForm> };

function pairs(values: Record<string, string>): Pair[] {
  return Object.entries(values)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([name, value]) => ({ name, value }));
}

function settingsOf(document: CanvasDocument): SettingsForm {
  return {
    variables: pairs(document.execution?.variables ?? {}),
    steps: Object.fromEntries(
      document.messages
        .filter((message) => message.kind === "request" && message.operation)
        .map((message) => {
          const config = message.execution ?? defaultStepExecution();
          return [
            message.id,
            {
              enabled: config.enabled,
              pathParams: pairs(config.pathParams),
              query: pairs(config.query),
              headers: pairs(config.headers),
              body: config.body,
              expectedStatus:
                config.expectedStatus === undefined ? "" : String(config.expectedStatus),
              assertions: config.assertions.map((item) => ({
                pointer: item.pointer,
                equals: JSON.stringify(item.equals),
              })),
              extract: config.extract.map((item) => ({ name: item.name, value: item.pointer })),
            },
          ];
        }),
    ),
  };
}

function variableName(name: string): void {
  if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name) || name.length > 100)
    throw new Error(
      "Имя переменной: латинские буквы, цифры и _, первый символ — буква или _. Не более 100 символов.",
    );
}

function pointer(value: string): void {
  if (value.length > 2000 || (value !== "" && !value.startsWith("/")) || /~(?:[^01]|$)/.test(value))
    throw new Error(
      "JSON Pointer должен быть пустым (весь ответ) или начинаться с /, например /user/id. Экранирование: ~0 и ~1.",
    );
}

function mapFromRows(rows: Pair[], label: string, variable = false): Record<string, string> {
  if (rows.length > 100) throw new Error(`${label}: не более 100 значений.`);
  const names = new Set<string>();
  for (const row of rows) {
    if (!row.name.trim()) throw new Error(`${label}: заполните имя или удалите пустую строку.`);
    if (names.has(row.name)) throw new Error(`${label}: имя «${row.name}» повторяется.`);
    if (row.value.length > 50_000)
      throw new Error(`${label}: значение не должно превышать 50 000 символов.`);
    if (variable) variableName(row.name);
    names.add(row.name);
  }
  return Object.fromEntries(rows.map(({ name, value }) => [name, value]));
}

function applySettings(document: CanvasDocument, form: SettingsForm): CanvasDocument {
  const variables = mapFromRows(form.variables, "Переменные", true);
  return {
    ...document,
    ...(document.execution || Object.keys(variables).length > 0
      ? { execution: { variables } }
      : {}),
    messages: document.messages.map((message) => {
      const step = form.steps[message.id];
      if (!step) return message;
      const expectedStatus =
        step.expectedStatus.trim() === "" ? undefined : Number(step.expectedStatus);
      if (
        expectedStatus !== undefined &&
        (!Number.isInteger(expectedStatus) || expectedStatus < 100 || expectedStatus > 599)
      )
        throw new Error(
          "Ожидаемый статус должен быть целым числом от 100 до 599. Пустое поле означает любой 2xx.",
        );
      if (new TextEncoder().encode(step.body).length > 1_048_576)
        throw new Error("Тело запроса не должно превышать 1 МиБ.");
      if (step.assertions.length > 100 || step.extract.length > 100)
        throw new Error("Не более 100 проверок и 100 извлекаемых переменных на шаг.");
      const extracted = new Set<string>();
      const execution: CanvasStepExecution = {
        enabled: step.enabled,
        pathParams: mapFromRows(step.pathParams, "Параметры пути"),
        query: mapFromRows(step.query, "Query-параметры"),
        headers: mapFromRows(step.headers, "Заголовки"),
        body: step.body,
        ...(expectedStatus === undefined ? {} : { expectedStatus }),
        assertions: step.assertions.map((assertion, index) => {
          pointer(assertion.pointer);
          let equals: JSONValue;
          try {
            equals = parseBrowserSafeJson(assertion.equals) as JSONValue;
          } catch {
            throw new Error(
              `Проверка ${index + 1}: введите корректный JSON. Строки — в двойных кавычках, например "ok".`,
            );
          }
          return { pointer: assertion.pointer, equals };
        }),
        extract: step.extract.map(({ name, value }) => {
          variableName(name);
          pointer(value);
          if (extracted.has(name)) throw new Error(`Извлечение: переменная «${name}» повторяется.`);
          extracted.add(name);
          return { name, pointer: value };
        }),
      };
      if (
        !message.execution &&
        JSON.stringify(execution) === JSON.stringify(defaultStepExecution())
      )
        return message;
      return { ...message, execution };
    }),
  };
}

export function ScenarioExecutionPanel(props: ScenarioExecutionPanelProps): ReactElement | null {
  return <ExecutionSession {...props} />;
}

function ExecutionSession({
  opened,
  document,
  revisionId,
  version,
  disabled,
  onClose,
  onChangeDocument,
  runScenario,
  listRuns,
  getRun,
  cancelRun,
}: ScenarioExecutionPanelProps): ReactElement {
  const sourceForm = useMemo(() => settingsOf(document), [document]);
  const sourceSignature = JSON.stringify(sourceForm);
  const [form, setForm] = useState(sourceForm);
  const [baseSignature, setBaseSignature] = useState(sourceSignature);
  const formDirty = JSON.stringify(form) !== baseSignature;
  const [error, setError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState(
    document.messages.find((message) => message.kind === "request" && message.operation)?.id ??
      document.messages[0]?.id ??
      "",
  );
  const [tab, setTab] = useState<string | null>("settings");
  const [report, setReport] = useState<CanvasExecutionReport | null>(null);
  const [runs, setRuns] = useState<CanvasExecutionRunSummary[]>([]);
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null);
  const selectedRunRef = useRef<string | null>(null);
  const selectionExplicit = useRef(false);
  const [ownedRunId, setOwnedRunId] = useState<string | null>(null);
  const [recoverableRun, setRecoverableRun] = useState<{ id: string; version: number } | null>(
    null,
  );
  const recoverableRunId = recoverableRun?.id ?? null;
  const ownedRun = useRef<OwnedRun | null>(null);
  const [refresh, setRefresh] = useState(0);
  const [listError, setListError] = useState<string | null>(null);
  const [reportError, setReportError] = useState<string | null>(null);
  const mounted = useRef(true);
  const api = useRef({ runScenario, listRuns, getRun, cancelRun });
  useEffect(() => {
    api.current = { runScenario, listRuns, getRun, cancelRun };
  }, [runScenario, listRuns, getRun, cancelRun]);
  const running = ownedRunId !== null;
  const reportRef = useRef<CanvasExecutionReport | null>(null);
  const ingestReport = useCallback((next: CanvasExecutionReport) => {
    if (!mounted.current) return;
    const previous = reportRef.current;
    // A slow start/poll response must not replace an already terminal snapshot.
    if (previous?.id === next.id && previous.status !== "running" && next.status === "running")
      return;
    if (selectedRunRef.current === next.id) {
      reportRef.current = next;
      setReport(next);
      setReportError(null);
    }
    setRuns((current) =>
      [runSummary(next), ...current.filter((item) => item.id !== next.id)]
        .sort((a, b) => b.startedAt - a.startedAt)
        .slice(0, 50),
    );
    if (ownedRun.current?.id === next.id) {
      ownedRun.current.confirmed = true;
      setRecoverableRun(null);
    }
    if (ownedRun.current?.id === next.id && next.status !== "running") {
      ownedRun.current.terminal = true;
      ownedRun.current = null;
      setOwnedRunId(null);
    }
  }, []);
  const markRecoverable = useCallback((id: string) => {
    const owned = ownedRun.current;
    if (owned?.id === id && owned.creationSettled && !owned.confirmed && !owned.terminal) {
      setRecoverableRun({ id, version: owned.version });
    }
  }, []);
  const selectRun = useCallback((id: string, explicit = true) => {
    if (explicit) selectionExplicit.current = true;
    selectedRunRef.current = id;
    setSelectedRunId(id);
    if (reportRef.current?.id !== id) {
      reportRef.current = null;
      setReport(null);
    }
    setReportError(null);
  }, []);
  const cancelOwned = useCallback(
    (owned: OwnedRun): Promise<void> => {
      owned.cancelRequested = true;
      if (owned.terminal) return Promise.resolve();
      if (owned.cancelPromise) return owned.cancelPromise;
      const controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(), 10_000);
      owned.cancelPromise = api.current
        .cancelRun(owned.id, controller.signal)
        .then((next) => {
          if (next.status !== "running") owned.terminal = true;
          ingestReport(next);
          if (mounted.current) setRefresh((value) => value + 1);
        })
        .catch((cause) => {
          if (mounted.current) {
            setError(`Не удалось подтвердить отмену: ${errorText(cause)}`);
            markRecoverable(owned.id);
          }
        })
        .finally(() => {
          clearTimeout(timeout);
          owned.cancelPromise = undefined;
        });
      return owned.cancelPromise;
    },
    [ingestReport, markRecoverable],
  );
  const externalSettingsChanged = formDirty && sourceSignature !== baseSignature;
  const blockReason = useMemo(() => canvasExecutionBlockReason(document), [document]);
  const canRun =
    !running && !disabled && !formDirty && !blockReason && sourceSignature === baseSignature;

  // API pin/version updates leave buffers intact. Actual config changes refresh a clean form.
  if (!formDirty && sourceSignature !== baseSignature) {
    setForm(sourceForm);
    setBaseSignature(sourceSignature);
  }
  useEffect(() => {
    mounted.current = true;
    const leavingPage = () => {
      if (ownedRun.current) void cancelOwned(ownedRun.current);
    };
    window.addEventListener("pagehide", leavingPage);
    return () => {
      window.removeEventListener("pagehide", leavingPage);
      mounted.current = false;
      if (ownedRun.current) void cancelOwned(ownedRun.current);
    };
  }, [cancelOwned]);
  useEffect(() => {
    if (!opened && ownedRun.current) void cancelOwned(ownedRun.current);
  }, [opened, cancelOwned]);

  useEffect(() => {
    if (!opened) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function poll(): Promise<void> {
      try {
        const next = await api.current.listRuns(controller.signal);
        if (controller.signal.aborted) return;
        setRuns(next);
        setListError(null);
        if (!selectionExplicit.current && next[0] && selectedRunRef.current !== next[0].id)
          selectRun(next[0].id, false);
      } catch (cause) {
        if (!controller.signal.aborted) setListError(errorText(cause));
      } finally {
        if (!controller.signal.aborted) timer = setTimeout(() => void poll(), 2000);
      }
    }
    void poll();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [opened, refresh, selectRun]);

  useEffect(() => {
    if (!opened || !selectedRunId) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    const runId = selectedRunId;
    async function poll(): Promise<void> {
      let again = true;
      try {
        const next = await api.current.getRun(runId, controller.signal);
        if (controller.signal.aborted) return;
        ingestReport(next);
        again = next.status === "running";
      } catch (cause) {
        if (!controller.signal.aborted) {
          setReportError(errorText(cause));
          markRecoverable(runId);
        }
      } finally {
        if (!controller.signal.aborted && again) timer = setTimeout(() => void poll(), 1000);
      }
    }
    // Creation returns a full report; avoid racing its initial persistence with GET.
    if (ownedRun.current?.id !== runId || ownedRun.current.creationSettled) void poll();
    else timer = setTimeout(() => void poll(), 1000);
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [opened, selectedRunId, refresh, ingestReport, markRecoverable]);

  useEffect(() => {
    if (!opened || !ownedRunId || ownedRunId === selectedRunId) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function poll(): Promise<void> {
      try {
        const next = await api.current.getRun(ownedRunId!, controller.signal);
        if (controller.signal.aborted) return;
        ingestReport(next);
        if (next.status !== "running") return;
      } catch {
        /* Keep the owned run cancellable while another report is selected. */
        if (!controller.signal.aborted) markRecoverable(ownedRunId!);
      }
      if (!controller.signal.aborted) timer = setTimeout(() => void poll(), 1000);
    }
    timer = setTimeout(() => void poll(), 1000);
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [opened, ownedRunId, selectedRunId, ingestReport, markRecoverable]);

  const shownDocument = tab === "results" && report ? report.document : document;
  if (
    shownDocument.messages.length > 0 &&
    !shownDocument.messages.some((message) => message.id === selectedId)
  ) {
    setSelectedId(
      shownDocument.messages.find((message) => message.kind === "request" && message.operation)
        ?.id ?? shownDocument.messages[0]!.id,
    );
  }
  const selectedMessage = shownDocument.messages.find((message) => message.id === selectedId);
  const selectedStep = report?.steps.find((step) => step.messageId === selectedId);
  const selectedForm = form.steps[selectedId];
  const resolved = selectedMessage?.operation
    ? resolveOperation(shownDocument, selectedMessage.operation)
    : null;
  const statuses = useMemo(
    () =>
      report
        ? Object.fromEntries(report.steps.map((step) => [step.messageId, step.status]))
        : undefined,
    [report],
  );
  const selectGraph = (selection: CanvasSelection) => {
    if (selection?.kind === "message") setSelectedId(selection.id);
  };

  function close(): void {
    if (ownedRun.current) void cancelOwned(ownedRun.current);
    onClose();
  }

  function updateStep(patch: Partial<StepForm>): void {
    if (!selectedForm) return;
    setForm({ ...form, steps: { ...form.steps, [selectedId]: { ...selectedForm, ...patch } } });
    setError(null);
  }

  function save(): void {
    try {
      const next = applySettings(document, form);
      validateCanvasExecutionSettings(next);
      const normalized = settingsOf(next);
      onChangeDocument(next);
      setForm(normalized);
      setBaseSignature(JSON.stringify(normalized));
      setError(null);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Не удалось сохранить настройки.");
    }
  }

  async function run(): Promise<void> {
    if (!canRun || ownedRun.current) return;
    const id = createCanvasId();
    const owned: OwnedRun = {
      id,
      input: Object.freeze({ runId: id, revisionId }),
      version,
      creationSettled: false,
      confirmed: false,
      cancelRequested: false,
      terminal: false,
    };
    ownedRun.current = owned;
    setOwnedRunId(id);
    await submitRun(owned);
  }

  async function retryRun(): Promise<void> {
    const owned = ownedRun.current;
    if (
      !owned ||
      owned.id !== recoverableRunId ||
      !owned.creationSettled ||
      owned.confirmed ||
      owned.terminal
    )
      return;
    // Serialize retry with any close/cancel request so it cannot cancel a newly accepted retry.
    owned.creationSettled = false;
    owned.cancelRequested = false;
    setRecoverableRun(null);
    await owned.cancelPromise;
    if (ownedRun.current !== owned || owned.terminal || !mounted.current) return;
    if (owned.cancelRequested) {
      owned.creationSettled = true;
      markRecoverable(owned.id);
      return;
    }
    await submitRun(owned);
  }

  async function submitRun(owned: OwnedRun): Promise<void> {
    const id = owned.id;
    owned.creationSettled = false;
    setRecoverableRun(null);
    selectRun(id);
    setTab("results");
    setError(null);
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 30_000);
    let next: CanvasExecutionReport | null = null;
    try {
      next = await api.current.runScenario(owned.input, controller.signal);
    } catch (cause) {
      const failure =
        cause instanceof Error && cause.cause instanceof ApiFailure ? cause.cause : cause;
      if (
        failure instanceof ApiFailure &&
        failure.status >= 400 &&
        failure.status < 500 &&
        failure.status !== 408
      ) {
        // An explicit rejection never grants ownership of another run with this ID.
        owned.terminal = true;
        if (ownedRun.current === owned) ownedRun.current = null;
        if (mounted.current) {
          setOwnedRunId(null);
          setRecoverableRun(null);
          setError(errorText(cause));
          selectedRunRef.current = null;
          selectionExplicit.current = false;
          setSelectedRunId(null);
          setRefresh((value) => value + 1);
        }
        return;
      }
      // A lost acknowledgement is not permission to replay the HTTP sequence.
      try {
        next = await api.current.getRun(id, new AbortController().signal);
      } catch {
        if (mounted.current)
          setError(
            `Не удалось подтвердить запуск: ${errorText(cause)}. Обновите отчёт по сохранённому ID.`,
          );
      }
    } finally {
      clearTimeout(timeout);
      owned.creationSettled = true;
    }
    if (next) {
      if (next.status !== "running") owned.terminal = true;
      ingestReport(next);
    }
    if (!next && mounted.current) markRecoverable(id);
    if (owned.cancelRequested && !owned.terminal) {
      await owned.cancelPromise;
      await cancelOwned(owned);
    }
    if (mounted.current) setRefresh((value) => value + 1);
  }

  return (
    <Modal
      opened={opened}
      onClose={close}
      closeButtonProps={{ "aria-label": "Закрыть запуск" }}
      title={
        <Group gap="xs">
          <IconPlayerPlay size={20} />
          <Text fw={650}>Запуск сценария</Text>
          <Badge variant="light" color="gray">
            Текущая версия {version}
          </Badge>
        </Group>
      }
      size="calc(100vw - 48px)"
      padding={0}
      yOffset={24}
      xOffset={24}
      classNames={{ content: styles.modal, header: styles.header, body: styles.body }}
      onKeyDown={(event) => {
        if ((event.metaKey || event.ctrlKey) && ["z", "y"].includes(event.key.toLowerCase())) {
          event.stopPropagation();
          if (
            !(event.target instanceof HTMLElement) ||
            !event.target.closest("input,textarea,[contenteditable=true]")
          )
            event.preventDefault();
        }
      }}
    >
      <div className={styles.workspace}>
        <aside className={styles.sidebar} aria-label="Шаги сценария">
          <Text size="xs" c="dimmed" fw={650} className={styles.sidebarTitle}>
            ПОСЛЕДОВАТЕЛЬНОСТЬ · {shownDocument.messages.length}
          </Text>
          <ol className={styles.steps}>
            {shownDocument.messages.map((message, index) => {
              const step =
                tab === "results"
                  ? report?.steps.find((item) => item.messageId === message.id)
                  : undefined;
              const operation = message.operation
                ? resolveOperation(shownDocument, message.operation)
                : null;
              return (
                <li key={message.id}>
                  <UnstyledButton
                    className={styles.step}
                    aria-pressed={message.id === selectedId}
                    data-active={message.id === selectedId || undefined}
                    onClick={() => setSelectedId(message.id)}
                  >
                    <Text size="sm" fw={650}>
                      {index + 1}. {message.label || "Без названия"}
                    </Text>
                    <Text size="xs" c="dimmed" mt={4} className={styles.wrap}>
                      {message.kind === "request" && operation
                        ? `${operation.location.method.toUpperCase()} ${operation.location.path}`
                        : "Не исполняется"}
                    </Text>
                    {step ? (
                      <Badge variant="light" mt={7} color={statusColors[step.status]} size="xs">
                        {statusLabels[step.status]}
                      </Badge>
                    ) : message.execution?.enabled === false ? (
                      <Text size="xs" c="dimmed" mt={4}>
                        Выключен
                      </Text>
                    ) : null}
                  </UnstyledButton>
                </li>
              );
            })}
          </ol>
          {shownDocument.messages.length === 0 ? (
            <Text size="sm" c="dimmed" p="md">
              В сценарии пока нет сообщений.
            </Text>
          ) : null}
        </aside>
        <Tabs value={tab} onChange={setTab} className={styles.tabs} keepMounted={false}>
          <Tabs.List className={styles.tabList}>
            <Tabs.Tab value="settings">Настройка</Tabs.Tab>
            <Tabs.Tab
              value="results"
              aria-label="Результат"
              rightSection={
                runs[0]?.source === "mcp" ? (
                  <Badge
                    component="output"
                    variant="light"
                    size="xs"
                    maw={280}
                    color={statusColors[runs[0].status]}
                    aria-label={`Прогон MCP: ${runs[0].name || runs[0].id}`}
                    title={`${runs[0].name || "Прогон агента"} · версия ${runs[0].version}`}
                  >
                    MCP · {runs[0].name || new Date(runs[0].startedAt).toLocaleTimeString("ru-RU")}{" "}
                    · {statusLabels[runs[0].status]}
                  </Badge>
                ) : undefined
              }
            >
              Результат
            </Tabs.Tab>
          </Tabs.List>
          <Tabs.Panel value="settings" className={styles.settings}>
            <fieldset disabled={running} className={styles.fieldset}>
              <section className={styles.section} aria-label="Начальные переменные">
                <Text fw={650} size="sm">
                  Начальные переменные
                </Text>
                <Text c="dimmed" size="xs" mt={4} mb="sm">
                  Подстановка в параметрах, заголовках и теле: {"{{token}}"}. Каждый прогон начинает
                  с этих значений.
                </Text>
                <PairEditor
                  rows={form.variables}
                  onChange={(variables) => {
                    setForm({ ...form, variables });
                    setError(null);
                  }}
                  label="Переменная"
                  addLabel="Добавить переменную"
                  namePlaceholder="token"
                  valuePlaceholder="Значение"
                />
              </section>
              {selectedMessage &&
              selectedForm &&
              selectedMessage.kind === "request" &&
              selectedMessage.operation ? (
                <section className={styles.section} aria-label="Настройки HTTP-шага">
                  <Group justify="space-between" align="start" mb="md">
                    <div>
                      <Text fw={650}>{selectedMessage.label || "HTTP-запрос"}</Text>
                      <Text size="sm" c="dimmed" className={styles.wrap}>
                        {resolved
                          ? `${resolved.location.method.toUpperCase()} ${resolved.location.path}`
                          : "API-связь недоступна"}
                      </Text>
                    </div>
                    <Checkbox
                      label="Выполнять шаг"
                      checked={selectedForm.enabled}
                      onChange={(event) => updateStep({ enabled: event.currentTarget.checked })}
                    />
                  </Group>
                  {resolved?.contract.mode !== "linked" || !resolved?.contract.source ? (
                    <Alert color="yellow" mb="md">
                      Для запуска свяжите контракт с API в панели «Контракты API».
                    </Alert>
                  ) : null}
                  <Stack gap="lg">
                    <FieldSection
                      title="Параметры пути"
                      hint={
                        resolved?.location.path.includes("{")
                          ? `Подставляются в ${resolved.location.path}`
                          : "Для параметров в фигурных скобках, например /users/{id}."
                      }
                    >
                      <PairEditor
                        label="Параметр пути"
                        rows={selectedForm.pathParams}
                        onChange={(pathParams) => updateStep({ pathParams })}
                        addLabel="Добавить параметр пути"
                        namePlaceholder="id"
                        valuePlaceholder="{{userId}}"
                      />
                    </FieldSection>
                    <FieldSection title="Query-параметры">
                      <PairEditor
                        label="Query-параметр"
                        rows={selectedForm.query}
                        onChange={(query) => updateStep({ query })}
                        addLabel="Добавить query-параметр"
                        namePlaceholder="limit"
                        valuePlaceholder="10"
                      />
                    </FieldSection>
                    <FieldSection title="Заголовки">
                      <PairEditor
                        label="Заголовок"
                        rows={selectedForm.headers}
                        onChange={(headers) => updateStep({ headers })}
                        addLabel="Добавить заголовок"
                        namePlaceholder="Authorization"
                        valuePlaceholder="Bearer {{token}}"
                      />
                    </FieldSection>
                    <Textarea
                      label="Тело запроса"
                      description="Для JSON задайте заголовок Content-Type: application/json."
                      placeholder={'{"name":"{{name}}"}'}
                      value={selectedForm.body}
                      onChange={(event) => updateStep({ body: event.currentTarget.value })}
                      rows={4}
                      resize="vertical"
                      styles={{ input: { fontFamily: "monospace" } }}
                    />
                    <TextInput
                      label="Ожидаемый статус"
                      description="Пустое поле — любой успешный ответ 2xx."
                      placeholder="Любой 2xx"
                      inputMode="numeric"
                      value={selectedForm.expectedStatus}
                      onChange={(event) =>
                        updateStep({ expectedStatus: event.currentTarget.value })
                      }
                      maw={320}
                    />
                    <FieldSection
                      title="Проверки JSON"
                      hint={
                        'JSON Pointer: /user/id или пустое поле для всего ответа. Значение — JSON, например 42, true или "ok".'
                      }
                    >
                      <Stack gap="xs">
                        {selectedForm.assertions.map((assertion, index) => (
                          <div className={styles.pair} key={index}>
                            <TextInput
                              aria-label={`Указатель проверки ${index + 1}`}
                              placeholder="/user/id"
                              value={assertion.pointer}
                              onChange={(event) =>
                                updateStep({
                                  assertions: selectedForm.assertions.map((item, i) =>
                                    i === index
                                      ? { ...item, pointer: event.currentTarget.value }
                                      : item,
                                  ),
                                })
                              }
                            />
                            <TextInput
                              aria-label={`Ожидаемый JSON ${index + 1}`}
                              placeholder='"ok"'
                              value={assertion.equals}
                              onChange={(event) =>
                                updateStep({
                                  assertions: selectedForm.assertions.map((item, i) =>
                                    i === index
                                      ? { ...item, equals: event.currentTarget.value }
                                      : item,
                                  ),
                                })
                              }
                            />
                            <ActionIcon
                              variant="subtle"
                              color="gray"
                              aria-label={`Удалить проверку ${index + 1}`}
                              onClick={() =>
                                updateStep({
                                  assertions: selectedForm.assertions.filter((_, i) => i !== index),
                                })
                              }
                            >
                              <IconTrash size={16} />
                            </ActionIcon>
                          </div>
                        ))}
                      </Stack>
                      <Button
                        variant="subtle"
                        size="xs"
                        leftSection={<IconPlus size={14} />}
                        mt={6}
                        disabled={selectedForm.assertions.length >= 100}
                        onClick={() =>
                          updateStep({
                            assertions: [
                              ...selectedForm.assertions,
                              { pointer: "", equals: "null" },
                            ],
                          })
                        }
                      >
                        Добавить проверку
                      </Button>
                    </FieldSection>
                    <FieldSection
                      title="Извлечь переменные из ответа"
                      hint="Имя переменной и JSON Pointer, например token → /access_token. Переменная доступна следующим шагам."
                    >
                      <PairEditor
                        label="Извлечение"
                        rows={selectedForm.extract}
                        onChange={(extract) => updateStep({ extract })}
                        addLabel="Добавить извлечение"
                        namePlaceholder="token"
                        valuePlaceholder="/access_token"
                      />
                    </FieldSection>
                  </Stack>
                </section>
              ) : (
                <Text size="sm" c="dimmed" p="lg">
                  {selectedMessage
                    ? "Это сообщение не выполняет HTTP-запрос. Выберите запрос с привязанной API-операцией."
                    : "Добавьте HTTP-запрос с API-операцией на диаграмму."}
                </Text>
              )}
            </fieldset>
          </Tabs.Panel>
          <Tabs.Panel value="results" className={styles.results}>
            <div className={styles.runSelector}>
              <NativeSelect
                label="Сохранённый прогон"
                value={selectedRunId ?? ""}
                onChange={(event) => selectRun(event.currentTarget.value)}
                data={
                  runs.length || selectedRunId
                    ? [
                        ...(selectedRunId && !runs.some((item) => item.id === selectedRunId)
                          ? [{ value: selectedRunId, label: "Новый прогон · создаётся" }]
                          : []),
                        ...runs.map((item) => ({
                          value: item.id,
                          label: `${item.source.toUpperCase()} · ${item.name || "Без имени"} · версия ${item.version} · ${statusLabels[item.status]} · ${new Date(item.startedAt).toLocaleString("ru-RU")}`,
                        })),
                      ]
                    : [{ value: "", label: "Сохранённых прогонов пока нет" }]
                }
              />
              <Button variant="subtle" size="xs" onClick={() => setRefresh((value) => value + 1)}>
                Обновить отчёт
              </Button>
            </div>
            {listError ? (
              <Alert color="yellow" mx="md" mb="md">
                Не удалось обновить список: {listError}
              </Alert>
            ) : null}
            {reportError ? (
              <Alert color="yellow" mx="md" mb="md">
                Не удалось обновить отчёт: {reportError}
              </Alert>
            ) : null}
            {!report && selectedRunId ? (
              <Group p="lg">
                <Loader size="sm" />
                <Text size="sm" c="dimmed">
                  Загружаем прогон {selectedRunId}…
                </Text>
              </Group>
            ) : null}
            {report ? (
              <>
                <div className={styles.reportHeading} aria-live="polite">
                  <Group gap="sm">
                    <Badge variant="light" color={statusColors[report.status]}>
                      {statusLabels[report.status]}
                    </Badge>
                    <Text fw={650} size="sm">
                      {report.status === "running"
                        ? "Сценарий выполняется"
                        : report.status === "passed"
                          ? "Прогон завершён"
                          : report.status === "cancelled"
                            ? "Прогон отменён"
                            : "Прогон остановлен"}
                    </Text>
                  </Group>
                  <Text size="xs" c="dimmed" mt={6}>
                    Результат для версии {report.version} · ревизия {report.revisionId} ·{" "}
                    {report.steps.filter((step) => step.status === "passed").length} из{" "}
                    {report.steps.filter((step) => step.status !== "skipped").length} шагов успешно
                    {report.finishedAt ? ` · ${report.finishedAt - report.startedAt} мс` : ""}
                  </Text>
                  {report.reason ? (
                    <Alert color="red" mt="sm">
                      {report.reason}
                    </Alert>
                  ) : null}
                </div>
                <div className={styles.graph}>
                  <Suspense fallback={<Loader size="sm" m="md" aria-label="Загружаем диаграмму" />}>
                    <SequenceGraph
                      document={report.document}
                      selection={selectedId ? { kind: "message", id: selectedId } : null}
                      onSelect={selectGraph}
                      onMoveMessage={noMove}
                      onMoveParticipant={noMove}
                      readOnly
                      executionStatuses={statuses}
                    />
                  </Suspense>
                </div>
                <div className={styles.details}>
                  {selectedStep ? (
                    <StepResult step={selectedStep} label={selectedMessage?.label ?? selectedId} />
                  ) : (
                    <Text c="dimmed" size="sm">
                      Выберите шаг, чтобы увидеть запрос и ответ.
                    </Text>
                  )}
                  <details className={styles.disclosure}>
                    <summary>Переменные прогона</summary>
                    <pre className={styles.code}>{JSON.stringify(report.variables, null, 2)}</pre>
                  </details>
                </div>
              </>
            ) : null}
          </Tabs.Panel>
        </Tabs>
      </div>
      <footer className={styles.footer}>
        {error ? (
          <Alert color="red" role="alert" w="100%">
            {error}
          </Alert>
        ) : null}
        {externalSettingsChanged ? (
          <Alert color="yellow" w="100%">
            Настройки изменились в другой вкладке. Сбросьте локальный ввод перед продолжением.
            <Button
              size="xs"
              variant="subtle"
              onClick={() => {
                setForm(sourceForm);
                setBaseSignature(sourceSignature);
                setError(null);
              }}
            >
              Сбросить локальный ввод
            </Button>
          </Alert>
        ) : null}
        <Text size="xs" c="dimmed" className={styles.footerNote}>
          {recoverableRunId
            ? `Запуск не подтверждён. Повтор отправит тот же запрос для версии ${recoverableRun?.version}. Существующий прогон не запустится заново.`
            : running
              ? "Отмена остановит дальнейшие запросы. Уже выполненные действия сохранятся."
              : formDirty
                ? "Сохраните настройки перед запуском."
                : (blockReason ??
                  (disabled
                    ? "Запуск станет доступен после сохранения сценария и завершения открытых форм."
                    : `Новый запуск: версия ${version}. Запросы выполняются по порядку на draft-моках, до первой ошибки.`))}
        </Text>
        <Group gap="xs">
          {recoverableRunId ? (
            <Button onClick={() => void retryRun()}>Повторить запрос запуска</Button>
          ) : null}
          <Button
            variant="default"
            disabled={!formDirty || running || externalSettingsChanged}
            onClick={save}
          >
            Сохранить настройки
          </Button>
          {running ? (
            <Button
              color="red"
              variant="light"
              leftSection={<IconPlayerStop size={16} />}
              onClick={() => {
                if (ownedRun.current) void cancelOwned(ownedRun.current);
              }}
            >
              {selectedRunId === ownedRunId ? "Отменить запуск" : "Отменить свой запуск"}
            </Button>
          ) : (
            <Button
              disabled={!canRun}
              leftSection={<IconPlayerPlay size={16} />}
              onClick={() => void run()}
            >
              Запустить
            </Button>
          )}
        </Group>
      </footer>
    </Modal>
  );
}

function FieldSection({
  title,
  hint,
  children,
}: {
  title: string;
  hint?: string;
  children: React.ReactNode;
}): ReactElement {
  return (
    <div>
      <Text size="sm" fw={600} mb={hint ? 3 : 8}>
        {title}
      </Text>
      {hint ? (
        <Text size="xs" c="dimmed" mb="xs">
          {hint}
        </Text>
      ) : null}
      {children}
    </div>
  );
}

function PairEditor({
  rows,
  onChange,
  label,
  addLabel,
  namePlaceholder,
  valuePlaceholder,
}: {
  rows: Pair[];
  onChange: (rows: Pair[]) => void;
  label: string;
  addLabel: string;
  namePlaceholder: string;
  valuePlaceholder: string;
}): ReactElement {
  return (
    <>
      <Stack gap="xs">
        {rows.map((row, index) => (
          <div className={styles.pair} key={index}>
            <TextInput
              aria-label={`${label} ${index + 1}: имя`}
              placeholder={namePlaceholder}
              value={row.name}
              onChange={(event) =>
                onChange(
                  rows.map((item, i) =>
                    i === index ? { ...item, name: event.currentTarget.value } : item,
                  ),
                )
              }
            />
            <TextInput
              aria-label={`${label} ${index + 1}: значение`}
              placeholder={valuePlaceholder}
              value={row.value}
              onChange={(event) =>
                onChange(
                  rows.map((item, i) =>
                    i === index ? { ...item, value: event.currentTarget.value } : item,
                  ),
                )
              }
            />
            <ActionIcon
              variant="subtle"
              color="gray"
              aria-label={`Удалить: ${label.toLowerCase()} ${index + 1}`}
              onClick={() => onChange(rows.filter((_, i) => i !== index))}
            >
              <IconTrash size={16} />
            </ActionIcon>
          </div>
        ))}
      </Stack>
      <Button
        variant="subtle"
        size="xs"
        leftSection={<IconPlus size={14} />}
        mt={6}
        disabled={rows.length >= 100}
        onClick={() => onChange([...rows, { name: "", value: "" }])}
      >
        {addLabel}
      </Button>
    </>
  );
}

function StepResult({
  step,
  label,
}: {
  step: CanvasExecutionStepResult;
  label: string;
}): ReactElement {
  return (
    <section aria-label="Детали шага">
      <Group justify="space-between" mb="sm">
        <Text fw={650}>{label}</Text>
        <Badge variant="light" color={statusColors[step.status]}>
          {statusLabels[step.status]}
        </Badge>
      </Group>
      {step.reason ? (
        <Alert color={step.status === "failed" ? "red" : "gray"} mb="md">
          {step.reason}
        </Alert>
      ) : null}
      {step.response ? (
        <>
          <Group gap="sm">
            <Text size="sm" ff="monospace">
              {step.response.method} {step.response.path}
            </Text>
            <Badge color={step.response.status < 400 ? "green" : "red"} variant="light">
              HTTP {step.response.status}
            </Badge>
            <Text size="xs" c="dimmed">
              {step.response.durationMs} мс
            </Text>
          </Group>
          <Text size="xs" c="dimmed" mt={6}>
            API #{step.response.designId} · ревизия {step.response.designRevisionId} · мок{" "}
            {step.response.workspaceRevision}
          </Text>
        </>
      ) : null}
      {step.assertions.length > 0 ? (
        <Stack gap={6} mt="md">
          {step.assertions.map((assertion, index) => (
            <div key={index} className={styles.assertion}>
              <Badge size="xs" color={assertion.passed ? "green" : "red"}>
                {assertion.passed ? "Успешно" : "Ошибка"}
              </Badge>
              <Text size="xs" ff="monospace" className={styles.wrap}>
                {assertion.pointer || "Весь ответ"}: ожидалось {assertion.expectedJson}
                {assertion.passed
                  ? ""
                  : ` · получено ${assertion.actualJson === undefined ? "отсутствует" : assertion.actualJson}`}
                {assertion.error ? ` · ${assertion.error}` : ""}
              </Text>
            </div>
          ))}
        </Stack>
      ) : null}
      {step.request ? (
        <details className={styles.disclosure}>
          <summary>Параметры и тело запроса</summary>
          <pre className={styles.code}>
            {JSON.stringify(
              {
                pathParams: step.request.pathParams,
                query: step.request.query,
                headers: step.request.headers,
              },
              null,
              2,
            )}
          </pre>
          {step.request.body ? <pre className={styles.code}>{step.request.body}</pre> : null}
        </details>
      ) : null}
      {step.response ? (
        <>
          <details className={styles.disclosure}>
            <summary>Заголовки ответа</summary>
            <pre className={styles.code}>{JSON.stringify(step.response.headers, null, 2)}</pre>
          </details>
          <Text size="sm" fw={600} mt="md">
            Тело ответа
          </Text>
          <pre className={styles.code}>{step.response.body || "Пустой ответ"}</pre>
        </>
      ) : null}
    </section>
  );
}

import { lazy, Suspense, useMemo, useState, type ReactElement } from "react";
import {
  Alert,
  Badge,
  Button,
  Group,
  Loader,
  Modal,
  NativeSelect,
  Stack,
  Text,
  UnstyledButton,
} from "@mantine/core";
import { IconArrowRight, IconHistory, IconRestore } from "@tabler/icons-react";
import { describeApiFailureDetailed } from "@/api/errors";
import { useGetDesignScenarioRevision } from "@/api/generated/design-scenarios/design-scenarios";
import {
  canvasHistoryHighlights,
  compareCanvasRevisions,
  type CanvasHistoryChange,
} from "./canvasHistoryDiff";
import {
  useRestoreDesignScenarioRevision,
  type DesignScenarioDetail,
  type DesignScenarioRevision,
  type DesignScenarioRevisionSummary,
} from "./designScenarioApi";
import type { CanvasSelection } from "./types";
import styles from "./ScenarioHistoryPanel.module.css";

const SequenceGraph = lazy(() => import("./SequenceGraph"));
const noMove = () => {};
const dateFormat = new Intl.DateTimeFormat("ru-RU", {
  day: "numeric",
  month: "short",
  year: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
});
const statusLabels = {
  added: "Добавлено",
  removed: "Удалено",
  changed: "Изменено",
  moved: "Перемещено",
};
const statusColors = { added: "green", removed: "red", changed: "orange", moved: "violet" };

interface HistoryProps {
  opened: boolean;
  onClose: () => void;
  scenarioId: number;
  currentRevisionId: number;
  baseVersion: number;
  revisions: DesignScenarioRevisionSummary[];
  dirty: boolean;
  onRestored: (detail: DesignScenarioDetail) => void;
}

export function ScenarioHistoryPanel(props: HistoryProps): ReactElement | null {
  // Closing history releases both previews and starts a fresh pair next time.
  return props.opened ? <HistorySession key={props.scenarioId} {...props} /> : null;
}

function HistorySession({
  onClose,
  scenarioId,
  currentRevisionId,
  baseVersion,
  revisions,
  dirty,
  onRestored,
}: HistoryProps): ReactElement {
  const ordered = useMemo(() => [...revisions].sort((a, b) => b.version - a.version), [revisions]);
  const [fromId, setFromId] = useState(() => {
    const currentIndex = ordered.findIndex((item) => item.id === currentRevisionId);
    return ordered[currentIndex + 1]?.id ?? ordered[currentIndex]?.id ?? ordered[0]?.id ?? 0;
  });
  const [toId, setToId] = useState(currentRevisionId);
  const fromSummary = ordered.find((item) => item.id === fromId);
  const toSummary = ordered.find((item) => item.id === toId);
  const queryOptions = {
    query: { enabled: ordered.length > 0, staleTime: Infinity, retry: false as const },
  };
  const fromQuery = useGetDesignScenarioRevision(scenarioId, fromId, queryOptions);
  const toQuery = useGetDesignScenarioRevision(scenarioId, toId, queryOptions);
  const before =
    fromQuery.data?.status === 200 ? (fromQuery.data.data as DesignScenarioRevision) : undefined;
  const after =
    toQuery.data?.status === 200 ? (toQuery.data.data as DesignScenarioRevision) : undefined;
  const failed = fromQuery.isError || toQuery.isError;
  const ready = before !== undefined && after !== undefined && !failed;
  const restore = useRestoreDesignScenarioRevision({
    onSuccess: (response) => {
      onRestored(response.data);
      onClose();
    },
  });
  const options = ordered.map((item) => ({
    value: String(item.id),
    label: `Версия ${item.version}${item.id === currentRevisionId ? " · текущая" : ""} · ${dateFormat.format(item.createdAt * 1000)}`,
  }));

  function restoreSelected(): void {
    if (!ready || !fromSummary || fromId === currentRevisionId || restore.isPending) return;
    if (dirty && !window.confirm("Заменить локальные правки выбранной серверной версией?")) return;
    restore.mutate({
      id: scenarioId,
      data: {
        expectedVersion: baseVersion,
        revisionId: fromId,
        summary: `Восстановлена версия ${fromSummary.version}`,
      },
    });
  }

  return (
    <Modal
      opened
      onKeyDown={(event) => {
        if ((event.metaKey || event.ctrlKey) && ["z", "y"].includes(event.key.toLowerCase())) {
          // The editor's window-level undo handler must not edit behind history.
          event.stopPropagation();
          if (
            !(event.target instanceof HTMLElement) ||
            !event.target.closest("input,textarea,[contenteditable=true]")
          ) {
            event.preventDefault();
          }
        }
      }}
      onClose={restore.isPending ? noMove : onClose}
      closeButtonProps={{ disabled: restore.isPending, "aria-label": "Закрыть историю" }}
      title={
        <Group gap="xs">
          <IconHistory size={20} />
          <Text fw={650}>История сценария</Text>
        </Group>
      }
      size="calc(100vw - 48px)"
      padding={0}
      yOffset={24}
      xOffset={24}
      classNames={{ content: styles.modal, header: styles.header, body: styles.body }}
    >
      <div className={styles.workspace}>
        <aside className={styles.sidebar} aria-label="Версии сценария">
          <Text size="xs" c="dimmed" fw={650} className={styles.sidebarTitle}>
            СОХРАНЁННЫЕ ВЕРСИИ · {ordered.length}
          </Text>
          <ol className={styles.timeline}>
            {ordered.map((item) => (
              <li key={item.id}>
                <UnstyledButton
                  className={styles.revision}
                  data-active={fromId === item.id || undefined}
                  aria-pressed={fromId === item.id}
                  disabled={restore.isPending}
                  aria-label={`Версия ${item.version} · ${item.summary || "Без описания"}`}
                  onClick={() => {
                    setFromId(item.id);
                    restore.reset();
                  }}
                >
                  <Group gap={6} justify="space-between" wrap="nowrap">
                    <Text size="sm" fw={650}>
                      Версия {item.version}
                    </Text>
                    <Badge
                      size="xs"
                      variant="light"
                      color={item.source === "mcp" ? "violet" : "gray"}
                    >
                      {item.source === "mcp" ? "MCP" : "UI"}
                    </Badge>
                  </Group>
                  <Text size="sm" mt={5} className={styles.summary}>
                    {item.summary || "Без описания"}
                  </Text>
                  <Text
                    component="time"
                    dateTime={new Date(item.createdAt * 1000).toISOString()}
                    size="xs"
                    c="dimmed"
                    mt={7}
                  >
                    {dateFormat.format(item.createdAt * 1000)}
                  </Text>
                  {item.id === currentRevisionId ? (
                    <Text size="xs" c="teal" fw={650} mt={4}>
                      Текущая версия
                    </Text>
                  ) : null}
                </UnstyledButton>
              </li>
            ))}
          </ol>
        </aside>
        <div className={styles.comparison}>
          {ordered.length === 0 ? (
            <Text c="dimmed" p="xl">
              Сохранённых версий пока нет.
            </Text>
          ) : (
            <>
              <div className={styles.pair}>
                <NativeSelect
                  label="Было"
                  value={String(fromId)}
                  data={options}
                  disabled={restore.isPending}
                  onChange={(event) => {
                    setFromId(Number(event.currentTarget.value));
                    restore.reset();
                  }}
                />
                <IconArrowRight size={20} className={styles.pairArrow} aria-hidden />
                <NativeSelect
                  label="Стало"
                  value={String(toId)}
                  data={options}
                  disabled={restore.isPending}
                  onChange={(event) => setToId(Number(event.currentTarget.value))}
                />
              </div>
              {dirty ? (
                <Alert color="yellow" mx="md" mb="md">
                  Несохранённые правки не включены в сравнение. Здесь показаны сохранённые версии.
                </Alert>
              ) : null}
              {failed ? (
                <Alert color="red" role="alert" m="md" title="Не удалось загрузить версии">
                  {describeApiFailureDetailed(fromQuery.error ?? toQuery.error)}
                  <Button
                    variant="light"
                    color="red"
                    size="xs"
                    mt="sm"
                    onClick={() => {
                      void fromQuery.refetch();
                      void toQuery.refetch();
                    }}
                  >
                    Повторить загрузку
                  </Button>
                </Alert>
              ) : ready ? (
                <Comparison key={`${before.id}:${after.id}`} before={before} after={after} />
              ) : (
                <Group p="xl" justify="center">
                  <Loader size="sm" aria-label="Загружаем версии" />
                  <Text c="dimmed" size="sm">
                    Загружаем сохранённые снимки…
                  </Text>
                </Group>
              )}
            </>
          )}
        </div>
      </div>
      <footer className={styles.footer}>
        {restore.isError ? (
          <Alert color="red" role="alert" w="100%">
            {describeApiFailureDetailed(restore.error)}
          </Alert>
        ) : null}
        <Text size="xs" c="dimmed" className={styles.restoreNote}>
          Восстановление создаст новую версию сценария. Общие API останутся в текущем состоянии.
        </Text>
        <Group gap="xs" wrap="nowrap">
          <Button variant="default" disabled={restore.isPending} onClick={onClose}>
            Закрыть
          </Button>
          <Button
            leftSection={<IconRestore size={16} />}
            loading={restore.isPending}
            disabled={!ready || !fromSummary || !toSummary || fromId === currentRevisionId}
            onClick={restoreSelected}
          >
            {fromSummary ? `Восстановить версию ${fromSummary.version}` : "Восстановить версию"}
          </Button>
        </Group>
      </footer>
    </Modal>
  );
}

function Comparison({
  before,
  after,
}: {
  before: DesignScenarioRevision;
  after: DesignScenarioRevision;
}): ReactElement {
  const changes = useMemo(() => compareCanvasRevisions(before, after), [before, after]);
  const [selection, setSelection] = useState<CanvasSelection>(null);
  const highlights = useMemo(() => canvasHistoryHighlights(changes), [changes]);
  return (
    <div className={styles.results}>
      <div className={styles.previews}>
        {(
          [
            { revision: before, side: "Было" },
            { revision: after, side: "Стало" },
          ] as const
        ).map(({ revision, side }) => (
          <section
            key={side}
            className={styles.preview}
            aria-label={`${side}: версия ${revision.version}`}
          >
            <div className={styles.previewHeading}>
              <Text size="sm" fw={650}>
                {side} · версия {revision.version}
              </Text>
              <Text size="xs" c="dimmed" className={styles.summary}>
                {revision.document.title || "Без названия"} · объектов:{" "}
                {revision.document.participants.length} · сообщений:{" "}
                {revision.document.messages.length}
              </Text>
            </div>
            <div className={styles.graph}>
              <Suspense fallback={<Loader size="sm" m="md" aria-label="Загружаем диаграмму" />}>
                <SequenceGraph
                  document={revision.document}
                  readOnly
                  selection={selection}
                  onSelect={setSelection}
                  highlights={highlights}
                  onMoveParticipant={noMove}
                  onMoveMessage={noMove}
                />
              </Suspense>
              {revision.document.participants.length === 0 ? (
                <Text className={styles.emptyGraph} size="sm" c="dimmed">
                  В этой версии ещё нет объектов
                </Text>
              ) : null}
            </div>
          </section>
        ))}
      </div>
      <section className={styles.changes} aria-label="Изменения версий">
        <Group justify="space-between" gap="sm" mb="sm">
          <Text fw={650} size="sm">
            Изменения{" "}
            <Text component="span" c="dimmed" inherit>
              · {changes.length}
            </Text>
          </Text>
          <Group gap="xs" aria-label="Обозначения изменений">
            {(Object.keys(statusLabels) as Array<CanvasHistoryChange["status"]>).map((status) => (
              <Badge key={status} color={statusColors[status]} variant="light" size="sm">
                {statusLabels[status]} · {changes.filter((item) => item.status === status).length}
              </Badge>
            ))}
          </Group>
        </Group>
        {changes.length === 0 ? (
          <Text c="dimmed" size="sm" py="md">
            Различий нет.
          </Text>
        ) : (
          <Stack gap={0}>
            {changes.map((change) => (
              <article
                key={change.key}
                className={styles.change}
                data-selected={
                  (change.selection &&
                    selection?.kind === change.selection.kind &&
                    selection.id === change.selection.id) ||
                  undefined
                }
              >
                <Group gap="xs" mb="xs" wrap="nowrap">
                  <Badge size="xs" color={statusColors[change.status]} variant="light">
                    {statusLabels[change.status]}
                  </Badge>
                  {change.selection ? (
                    <UnstyledButton
                      className={styles.changeTitle}
                      onClick={() => setSelection(change.selection ?? null)}
                      aria-label={`Показать на диаграмме: ${change.label}`}
                    >
                      <Text size="sm" fw={650}>
                        {change.label}
                      </Text>
                    </UnstyledButton>
                  ) : (
                    <Text size="sm" fw={650}>
                      {change.label}
                    </Text>
                  )}
                </Group>
                <table className={styles.fields} aria-label={change.label}>
                  <thead>
                    <tr>
                      <th>Свойство</th>
                      <th>Было</th>
                      <th>Стало</th>
                    </tr>
                  </thead>
                  <tbody>
                    {change.fields.map((field, index) => (
                      <tr key={`${field.label}:${index}`}>
                        <th scope="row">{field.label}</th>
                        <td>
                          <FieldValue value={field.before} color={field.color} />
                        </td>
                        <td>
                          <FieldValue value={field.after} color={field.color} />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </article>
            ))}
          </Stack>
        )}
      </section>
    </div>
  );
}

function FieldValue({ value, color }: { value?: string; color?: boolean }): ReactElement {
  return (
    <div className={styles.value}>
      {color && value && /^#[\da-f]{6}$/i.test(value) ? (
        <span className={styles.swatch} style={{ backgroundColor: value }} aria-hidden />
      ) : null}
      {value === undefined ? (
        <span className={styles.absent}>Отсутствует</span>
      ) : value === "" ? (
        <span className={styles.absent}>Пусто</span>
      ) : (
        value
      )}
    </div>
  );
}

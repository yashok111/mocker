import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Code,
  Group,
  Loader,
  Paper,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { IconArrowLeft } from "@tabler/icons-react";
import { useBlocker, useNavigate } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  getGetBackendProjectQueryKey,
  getListBackendProjectsQueryKey,
  useApplyBackendProjectCommands,
  useGetBackendProject,
  useGetBackendRevision,
  useListBackendRevisions,
  getBackendSavedView,
  getGetBackendSavedViewQueryKey,
} from "@/api/generated/backend-projects/backend-projects";
import type { ApplyBackendProjectCommandsRequest, BackendSavedView } from "@/api/generated/schemas";
import { ApiFailure } from "@/api/client";
import { describeApiFailureDetailed } from "@/api/errors";
import { BackendGraphInventory } from "./BackendGraphInventory";
import { BackendRevisionCompare } from "./BackendRevisionCompare";
import { BackendImportReview } from "./BackendImportReview";
import { BackendDatabase } from "./BackendDatabase";
import { BackendFlow } from "./BackendFlow";
import { usePinnedValue, type BackendSourcePin } from "./backendFlowReads";
import { BackendSavedViews } from "./BackendSavedViews";
import { BackendSavedViewContext, savedViewSourcePin } from "./backendSavedViewState";
import { useBackendSavedViewSession } from "./useBackendSavedViewSession";

type ProjectPageProps = {
  projectId: string;
  sourcePin?: BackendSourcePin;
  onSourceNavigate?: (pin: BackendSourcePin, replace?: boolean) => void;
  initialSaved?: BackendSavedView;
};

export function BackendProjectPage(props: ProjectPageProps) {
  return <BackendProjectSavedGate key={props.projectId} {...props} />;
}

function BackendProjectSavedGate(props: ProjectPageProps) {
  const { sourcePin, projectId, onSourceNavigate } = props;
  const viewId = sourcePin?.viewId;
  const version = sourcePin?.viewVersion;
  const valid =
    !!viewId &&
    /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(viewId) &&
    (version === undefined || (Number.isSafeInteger(version) && version > 0));
  const queryClient = useQueryClient();
  // Every unversioned open is a fresh intent. Immutable version keys remain reusable.
  const [latestMountId] = useState(() => crypto.randomUUID());
  const resolutionIdentity = JSON.stringify([viewId, version]);
  const [resolution, setResolution] = useState({ identity: resolutionIdentity, generation: 0 });
  const generation =
    resolution.identity === resolutionIdentity ? resolution.generation : resolution.generation + 1;
  if (resolution.identity !== resolutionIdentity)
    setResolution({ identity: resolutionIdentity, generation });
  const versionKey = getGetBackendSavedViewQueryKey(
    projectId,
    viewId ?? "",
    version === undefined ? undefined : { version },
  );
  const key =
    version === undefined
      ? [...versionKey, "latest-intent", latestMountId, String(generation)]
      : versionKey;
  const keyIdentity = JSON.stringify(key);
  useEffect(
    () => () => {
      void queryClient.cancelQueries({ queryKey: JSON.parse(keyIdentity) });
    },
    [queryClient, keyIdentity],
  );
  const query = useQuery({
    queryKey: key,
    enabled: valid,
    retry: false,
    staleTime: Infinity,
    queryFn: async ({ signal }) => {
      const response = await getBackendSavedView(
        projectId,
        viewId!,
        version === undefined ? undefined : { version },
        { signal },
      );
      signal.throwIfAborted();
      if (response.status !== 200) throw new Error("Не удалось прочитать сохранённый вид");
      const value = response.data;
      if (
        value.id !== viewId ||
        value.projectId !== projectId ||
        !Number.isSafeInteger(value.version) ||
        value.version < 1 ||
        (version !== undefined && value.version !== version) ||
        value.documentVersion !== "saved-view-v1"
      )
        throw new Error("Получен другой сохранённый вид или версия");
      return value;
    },
  });
  const saved = valid ? query.data : undefined;
  const expectedPin = saved ? savedViewSourcePin(saved) : undefined;
  const pinMatches =
    !!expectedPin &&
    Object.keys(sourcePin ?? {}).filter(
      (key) => (sourcePin as Record<string, unknown>)[key] !== undefined,
    ).length === Object.keys(expectedPin).length &&
    Object.entries(expectedPin).every(
      ([key, value]) => (sourcePin as Record<string, unknown> | undefined)?.[key] === value,
    );
  useEffect(() => {
    if (saved && !pinMatches) onSourceNavigate?.(savedViewSourcePin(saved), true);
  }, [saved, pinMatches, onSourceNavigate]);
  if (viewId !== undefined && !saved)
    return (
      <Stack aria-label="Загрузка сохранённого вида">
        {valid && query.isPending && <Loader aria-label="Загружаем сохранённый вид" />}
        {(!valid || query.isError) && (
          <Alert color="red" role="alert">
            {!valid
              ? "Неверный идентификатор или версия сохранённого вида"
              : query.error instanceof Error
                ? query.error.message
                : describeApiFailureDetailed(query.error)}
            <Group>
              <Button disabled={!valid} onClick={() => void query.refetch()}>
                Повторить загрузку вида
              </Button>
              <Button variant="default" onClick={() => onSourceNavigate?.({}, true)}>
                Вернуться к источнику
              </Button>
            </Group>
          </Alert>
        )}
      </Stack>
    );
  return (
    <BackendProjectDetail
      key={projectId}
      {...props}
      initialSaved={saved}
      sourcePin={saved ? savedViewSourcePin(saved) : sourcePin}
    />
  );
}

function BackendProjectDetail({
  projectId,
  sourcePin,
  onSourceNavigate,
  initialSaved,
}: ProjectPageProps) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const query = useGetBackendProject(projectId);
  const project = query.data?.status === 200 ? query.data.data : undefined;
  const [selectedRevisionId, setSelectedRevisionId] = usePinnedValue<string | null>(
    sourcePin?.revisionId,
    sourcePin?.revisionId ?? null,
  );
  const pinIdentity = JSON.stringify(sourcePin ?? {});
  const [pin, setPin] = usePinnedValue<BackendSourcePin>(pinIdentity, sourcePin ?? {});
  const [databaseDirty, setDatabaseDirty] = useState(false);
  const acknowledgedNavigation = useRef(false);
  const session = useBackendSavedViewSession(projectId, initialSaved, (value) => {
    acknowledgedNavigation.current = true;
    setPin(savedViewSourcePin(value));
    setSelectedRevisionId(value.pins.revisionId);
    onSourceNavigate?.(savedViewSourcePin(value), true);
  });
  const dirty = databaseDirty || session.dirty || !!session.pending;
  useBlocker({
    shouldBlockFn: ({ current, next }) => {
      if (acknowledgedNavigation.current) {
        acknowledgedNavigation.current = false;
        return false;
      }
      const nextPin = next.search as BackendSourcePin,
        currentPin = current.search as BackendSourcePin;
      const sameWorkspace =
        current.pathname === next.pathname &&
        nextPin.viewId === currentPin.viewId &&
        nextPin.viewVersion === currentPin.viewVersion &&
        nextPin.revisionId === currentPin.revisionId;
      return (
        dirty &&
        !sameWorkspace &&
        !window.confirm(
          "Есть несохранённые изменения вида или предложения либо неизвестный результат сохранения. Покинуть рабочую область?",
        )
      );
    },
    enableBeforeUnload: dirty,
    withResolver: false,
  });
  const [historyOpen, setHistoryOpen] = useState(false);
  const [compareOpen, setCompareOpen] = useState(false);
  const [importsOpen, setImportsOpen] = useState(false);
  const [importsMounted, setImportsMounted] = useState(false);
  const [historyCursors, setHistoryCursors] = useState([""]);
  const historyCursor = historyCursors.at(-1) ?? "";
  const historyQuery = useListBackendRevisions(
    projectId,
    historyCursor ? { cursor: historyCursor } : undefined,
    { query: { enabled: historyOpen } },
  );
  const history = historyQuery.data?.status === 200 ? historyQuery.data.data : undefined;
  const revisionQuery = useGetBackendRevision(
    projectId,
    selectedRevisionId ?? project?.currentRevisionId ?? "",
    {
      query: { enabled: !!project },
    },
  );
  const revision = revisionQuery.data?.status === 200 ? revisionQuery.data.data : undefined;
  if (revision && ["3", "4"].includes(revision.schemaVersion) && selectedRevisionId === null) {
    setSelectedRevisionId(revision.id);
    setPin({ ...pin, revisionId: revision.id });
  }
  useEffect(() => {
    if (
      revision &&
      ["3", "4"].includes(revision.schemaVersion) &&
      !sourcePin?.revisionId &&
      pin.revisionId === revision.id
    )
      onSourceNavigate?.(pin);
  }, [revision, sourcePin?.revisionId, pin, onSourceNavigate]);
  function navigateSource(value: BackendSourcePin) {
    setPin(value);
    if (value.revisionId) setSelectedRevisionId(value.revisionId);
    onSourceNavigate?.(value);
  }
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState("");
  const [baseVersion, setBaseVersion] = useState(0);
  const [attempt, setAttempt] = useState<ApplyBackendProjectCommandsRequest | null>(null);
  const mutation = useApplyBackendProjectCommands({
    mutation: {
      retry: false,
      onSuccess: (response) => {
        if (response.status !== 200) return;
        queryClient.setQueryData(getGetBackendProjectQueryKey(projectId), response);
        void queryClient.invalidateQueries({ queryKey: getListBackendProjectsQueryKey() });
        setEditing(false);
        setAttempt(null);
      },
      onError: (error) => {
        if (
          error instanceof ApiFailure &&
          error.status >= 400 &&
          error.status < 500 &&
          error.status !== 408 &&
          error.status !== 429
        )
          setAttempt(null);
      },
    },
  });
  const conflict = mutation.error instanceof ApiFailure && mutation.error.status === 409;

  function submit() {
    if (mutation.isPending || conflict || !name.trim() || !Number.isSafeInteger(baseVersion))
      return;
    const input: ApplyBackendProjectCommandsRequest = attempt ?? {
      expectedVersion: baseVersion,
      idempotencyKey: crypto.randomUUID(),
      commands: [{ type: "rename_project", name: name.trim() }],
    };
    setAttempt(input);
    mutation.mutate({ id: projectId, data: input });
  }

  async function reloadForEdit() {
    const response = await query.refetch();
    if (response.isError || response.data?.status !== 200) return;
    setBaseVersion(response.data.data.version);
    setAttempt(null);
    mutation.reset();
  }

  return (
    <BackendSavedViewContext value={session}>
      <Stack gap="xl" data-testid="backend-project-page">
        <Button
          variant="subtle"
          w="fit-content"
          leftSection={<IconArrowLeft size={16} />}
          onClick={() => void navigate({ to: "/backend-projects" })}
        >
          Бэкенд-проекты
        </Button>
        {query.isPending && <Loader aria-label="Загружаем проект" />}
        {query.isError && (
          <Alert color="red" role="alert">
            {describeApiFailureDetailed(query.error)}
            <Button mt="sm" variant="light" onClick={() => void query.refetch()}>
              Повторить загрузку
            </Button>
          </Alert>
        )}
        {project && (
          <>
            <Group justify="space-between" align="flex-start">
              <div style={{ minWidth: 0 }}>
                <Title order={1} style={{ overflowWrap: "anywhere" }}>
                  {project.name}
                </Title>
                <Text c="dimmed" size="sm" mt={4}>
                  Версия проекта {project.version}
                </Text>
              </div>
              {!editing && (
                <Button
                  variant="default"
                  onClick={() => {
                    setName(project.name);
                    setBaseVersion(project.version);
                    setEditing(true);
                    mutation.reset();
                  }}
                >
                  Переименовать
                </Button>
              )}
            </Group>
            {editing && (
              <Paper withBorder p="md" maw={640}>
                <form
                  onSubmit={(event) => {
                    event.preventDefault();
                    submit();
                  }}
                >
                  <Stack>
                    <TextInput
                      label="Название проекта"
                      value={name}
                      onChange={(event) => setName(event.currentTarget.value)}
                      disabled={attempt !== null}
                      required
                      data-autofocus
                    />
                    {mutation.isError && (
                      <Alert color="red" role="alert">
                        {describeApiFailureDetailed(mutation.error)}
                      </Alert>
                    )}
                    {conflict && (
                      <Text size="sm">
                        Загрузите текущую версию, сравните название и повторите сохранение. Ваш
                        текст сохранён в форме.
                      </Text>
                    )}
                    {attempt && mutation.isError && (
                      <Text size="sm">
                        Результат запроса неизвестен. Повтор сохранит исходный запрос и защитит от
                        дублирования.
                      </Text>
                    )}
                    <Group>
                      <Button
                        type="submit"
                        loading={mutation.isPending}
                        disabled={conflict || !name.trim() || !Number.isSafeInteger(baseVersion)}
                      >
                        {attempt && mutation.isError ? "Повторить запрос" : "Сохранить название"}
                      </Button>
                      {conflict && (
                        <Button
                          variant="default"
                          loading={query.isFetching}
                          onClick={() => void reloadForEdit()}
                        >
                          Загрузить текущую версию
                        </Button>
                      )}
                      <Button
                        variant="subtle"
                        disabled={mutation.isPending || attempt !== null}
                        onClick={() => {
                          setEditing(false);
                          mutation.reset();
                        }}
                      >
                        Отмена
                      </Button>
                    </Group>
                  </Stack>
                </form>
              </Paper>
            )}
            <BackendSavedViews
              projectId={projectId}
              session={session}
              onOpen={(value) => onSourceNavigate?.(value)}
            />
            <Group>
              <Button
                variant="default"
                aria-expanded={compareOpen}
                onClick={() => setCompareOpen((value) => !value)}
              >
                Сравнение ревизий
              </Button>
              <Button
                variant="default"
                aria-expanded={importsOpen}
                onClick={() => {
                  setImportsMounted(true);
                  setImportsOpen((value) => !value);
                }}
              >
                Проверить импорты
              </Button>
            </Group>
            {compareOpen && (
              <BackendRevisionCompare
                projectId={projectId}
                initialRevisionId={selectedRevisionId ?? project.currentRevisionId}
              />
            )}
            {importsMounted && (
              <div hidden={!importsOpen}>
                <BackendImportReview
                  projectId={projectId}
                  currentRevisionId={project.currentRevisionId}
                />
              </div>
            )}
            {revisionQuery.isPending && <Loader aria-label="Загружаем ревизию модели" />}
            {revisionQuery.isError && (
              <Alert color="red" role="alert">
                {describeApiFailureDetailed(revisionQuery.error)}
                <Button mt="sm" variant="light" onClick={() => void revisionQuery.refetch()}>
                  Повторить загрузку ревизии
                </Button>
              </Alert>
            )}
            {revision && (
              <>
                {["2", "3", "4"].includes(revision.schemaVersion) && (
                  <BackendDatabase
                    projectId={projectId}
                    revisionId={revision.id}
                    repositoryId={project.repositories[0]?.id}
                    pin={pin}
                    onFlowNavigate={
                      ["3", "4"].includes(revision.schemaVersion)
                        ? (value) => {
                            navigateSource(value);
                            requestAnimationFrame(() =>
                              document
                                .querySelector<HTMLElement>('[aria-label="Flow исходников"] h2')
                                ?.focus(),
                            );
                          }
                        : undefined
                    }
                    onDirty={(dirty) => {
                      setDatabaseDirty(dirty);
                      if (dirty) setSelectedRevisionId((current) => current ?? revision.id);
                    }}
                  />
                )}
                {["3", "4"].includes(revision.schemaVersion) && (
                  <BackendFlow
                    projectId={projectId}
                    revisionId={revision.id}
                    pin={pin}
                    onPinChange={navigateSource}
                    onDatabaseNavigate={() => {
                      requestAnimationFrame(() =>
                        document
                          .querySelector<HTMLElement>('[aria-label="База данных"]')
                          ?.scrollIntoView({ block: "start" }),
                      );
                    }}
                  />
                )}
                {revision.sourceSnapshotIds.length > 0 ? (
                  <BackendGraphInventory
                    key={`${projectId}:${revision.id}`}
                    projectId={projectId}
                    revisionId={revision.id}
                    schemaVersion={revision.schemaVersion}
                  />
                ) : (
                  <Paper withBorder p="lg">
                    <Stack gap="sm">
                      <Group justify="space-between">
                        <Title order={2}>Модель бэкенда</Title>
                        <Badge color="yellow">Покрытие неизвестно</Badge>
                      </Group>
                      <Text>
                        Исходный код ещё не импортирован. Список таблиц, связей и endpoint’ов пока
                        не исследован.
                      </Text>
                      <Text c="dimmed" size="sm">
                        Импортируйте первый граф исходников через агента, выбрав совместимые
                        инструкции импорта. Покрытие и основания появятся в новой ревизии.
                      </Text>
                      <Text size="sm">
                        Объектов в модели: {revision.coverage.knownObjects}. Общее количество
                        неизвестно.
                      </Text>
                    </Stack>
                  </Paper>
                )}
                <Stack gap="sm">
                  <Group justify="space-between">
                    <Title order={2}>
                      {revision.id === project.currentRevisionId
                        ? "Текущая ревизия"
                        : "Ревизия модели"}
                    </Title>
                    <Button
                      variant="default"
                      aria-expanded={historyOpen}
                      onClick={() => setHistoryOpen((open) => !open)}
                    >
                      История ревизий
                    </Button>
                  </Group>
                  {historyOpen && (
                    <Stack gap="xs" aria-label="История ревизий">
                      {historyQuery.isPending && <Loader aria-label="Загружаем историю" />}
                      {historyQuery.isError && (
                        <Alert color="red" role="alert">
                          {describeApiFailureDetailed(historyQuery.error)}
                          <Button variant="light" onClick={() => void historyQuery.refetch()}>
                            Повторить загрузку истории
                          </Button>
                        </Alert>
                      )}
                      {history?.items.map((item) => (
                        <Button
                          key={item.id}
                          variant={item.id === revision.id ? "light" : "subtle"}
                          w="fit-content"
                          maw="100%"
                          onClick={() => {
                            if (
                              !dirty ||
                              window.confirm(
                                "В предложении есть несохранённые изменения. Открыть другую ревизию источника?",
                              )
                            ) {
                              setDatabaseDirty(false);
                              setSelectedRevisionId(item.id);
                              navigateSource({ revisionId: item.id });
                            }
                          }}
                          aria-label={`Открыть ревизию ${item.id}`}
                        >
                          {new Date(item.createdAt).toLocaleString("ru-RU")}
                          {item.id === project.currentRevisionId ? " · текущая" : ""}
                        </Button>
                      ))}
                      {(historyCursors.length > 1 || history?.nextCursor) && (
                        <Group>
                          <Button
                            variant="default"
                            disabled={historyCursors.length === 1 || historyQuery.isFetching}
                            onClick={() => setHistoryCursors((previous) => previous.slice(0, -1))}
                          >
                            Предыдущие ревизии
                          </Button>
                          <Button
                            variant="default"
                            disabled={!history?.nextCursor || historyQuery.isFetching}
                            onClick={() => {
                              if (history?.nextCursor)
                                setHistoryCursors((previous) => [...previous, history.nextCursor]);
                            }}
                          >
                            Следующие ревизии
                          </Button>
                        </Group>
                      )}
                    </Stack>
                  )}
                  <Text size="sm" c="dimmed">
                    Создана {new Date(revision.createdAt).toLocaleString("ru-RU")}. Название проекта
                    хранится отдельно от модели.
                  </Text>
                  <Text size="sm">ID ревизии</Text>
                  <Code block style={{ overflowWrap: "anywhere", whiteSpace: "pre-wrap" }}>
                    {revision.id}
                  </Code>
                  <Text size="sm">Хеш содержимого</Text>
                  <Code block style={{ overflowWrap: "anywhere", whiteSpace: "pre-wrap" }}>
                    {revision.semanticHash}
                  </Code>
                </Stack>
              </>
            )}
          </>
        )}
      </Stack>
    </BackendSavedViewContext>
  );
}

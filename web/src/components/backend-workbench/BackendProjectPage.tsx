import { useState } from "react";
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
import { useQueryClient } from "@tanstack/react-query";
import {
  getGetBackendProjectQueryKey,
  getListBackendProjectsQueryKey,
  useApplyBackendProjectCommands,
  useGetBackendProject,
  useGetBackendRevision,
  useListBackendRevisions,
} from "@/api/generated/backend-projects/backend-projects";
import type { ApplyBackendProjectCommandsRequest } from "@/api/generated/schemas";
import { ApiFailure } from "@/api/client";
import { describeApiFailureDetailed } from "@/api/errors";
import { BackendGraphInventory } from "./BackendGraphInventory";
import { BackendRevisionCompare } from "./BackendRevisionCompare";
import { BackendImportReview } from "./BackendImportReview";
import { BackendDatabase } from "./BackendDatabase";

export function BackendProjectPage({ projectId }: { projectId: string }) {
  return <BackendProjectDetail key={projectId} projectId={projectId} />;
}

function BackendProjectDetail({ projectId }: { projectId: string }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const query = useGetBackendProject(projectId);
  const project = query.data?.status === 200 ? query.data.data : undefined;
  const [selectedRevisionId, setSelectedRevisionId] = useState<string | null>(null);
  const [databaseDirty, setDatabaseDirty] = useState(false);
  useBlocker({
    shouldBlockFn: () =>
      databaseDirty &&
      !window.confirm("В предложении есть несохранённые изменения. Покинуть проект?"),
    enableBeforeUnload: databaseDirty,
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
                      Загрузите текущую версию, сравните название и повторите сохранение. Ваш текст
                      сохранён в форме.
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
              {revision.schemaVersion === "2" && (
                <BackendDatabase
                  projectId={projectId}
                  revisionId={revision.id}
                  repositoryId={project.repositories[0]?.id}
                  onDirty={(dirty) => {
                    setDatabaseDirty(dirty);
                    if (dirty) setSelectedRevisionId((current) => current ?? revision.id);
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
                      Исходный код ещё не импортирован. Список таблиц, связей и endpoint’ов пока не
                      исследован.
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
                            !databaseDirty ||
                            window.confirm(
                              "В предложении есть несохранённые изменения. Открыть другую ревизию источника?",
                            )
                          ) {
                            setDatabaseDirty(false);
                            setSelectedRevisionId(item.id);
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
  );
}

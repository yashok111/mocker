import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Checkbox,
  Code,
  Group,
  Loader,
  Modal,
  NativeSelect,
  Paper,
  Stack,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  applyBackendProjectCommands,
  getGetBackendProjectQueryKey,
  listBackendAnnotations,
  useGetBackendProject,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  ApplyBackendProjectCommandsRequest,
  BackendAnnotation,
  BackendAnnotationTarget,
  BackendProjectCommand,
  ListBackendAnnotationsParams,
} from "@/api/generated/schemas";
import { parseBrowserSafeJson } from "@/api/preciseJson";
import { ApiFailure } from "@/api/client";
import { describeApiFailureDetailed } from "@/api/errors";

export type BackendAnnotationsProps = {
  projectId: string;
  selection?: { recordType: "node" | "edge"; id: string; revisionId: string };
  onNavigate: (target: BackendAnnotationTarget) => void;
  onDirtyChange?: (dirty: boolean) => void;
};
type Form = {
  type: "create_annotation" | "update_annotation";
  id: string;
  target: BackendAnnotationTarget;
  body: string;
  version: number;
};
type Attempt = { request: ApplyBackendProjectCommandsRequest; conflict: boolean };
const wrap = { overflowWrap: "anywhere" as const, whiteSpace: "pre-wrap" as const };
const validID = (id: string) =>
  /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(id) &&
  id !== "00000000-0000-0000-0000-000000000000";
const statusLabels = {
  current: "Текущая цель",
  historical: "Историческая ревизия",
  orphaned: "Цель удалена",
};

function pendingKey(projectId: string) {
  return `backend-annotation-attempt:${projectId}`;
}
function readAttempt(projectId: string): Attempt | null {
  try {
    const raw = sessionStorage.getItem(pendingKey(projectId));
    if (!raw) return null;
    const stored = parseBrowserSafeJson(raw) as Attempt | ApplyBackendProjectCommandsRequest;
    if (!stored || typeof stored !== "object") return null;
    const attempt = "request" in stored ? stored : { request: stored, conflict: false };
    const request = attempt.request;
    if (
      !request ||
      !Number.isSafeInteger(request.expectedVersion) ||
      request.expectedVersion <= 0 ||
      typeof request.idempotencyKey !== "string" ||
      !Array.isArray(request.commands) ||
      request.commands.length !== 1
    )
      return null;
    const command = request.commands[0];
    if (
      !command ||
      !["create_annotation", "update_annotation", "remove_annotation"].includes(command.type)
    )
      return null;
    return { request, conflict: attempt.conflict === true };
  } catch {
    return null;
  }
}

function annotationDraft(request: ApplyBackendProjectCommandsRequest): Form | null {
  const command = request.commands[0];
  if (!command || (command.type !== "create_annotation" && command.type !== "update_annotation"))
    return null;
  return {
    type: command.type,
    id: command.annotationId,
    target: { ...command.target },
    body: command.body,
    version: request.expectedVersion,
  };
}

function annotationError(error: unknown) {
  if (error instanceof ApiFailure) {
    if (error.code === "backend_annotation_quota")
      return "Достигнут лимит заметок или их общего объёма.";
    if (error.code === "backend_not_found")
      return "Заметка или выбранная цель не найдена. Проверьте объект и ревизию.";
    if (error.code === "backend_invalid")
      return "Проверьте текст заметки, идентификатор цели и ревизию.";
  }
  return describeApiFailureDetailed(error);
}

export function BackendAnnotations({
  projectId,
  selection,
  onNavigate,
  onDirtyChange,
}: BackendAnnotationsProps) {
  const client = useQueryClient();
  const projectQuery = useGetBackendProject(projectId, { query: { retry: false } });
  const project = projectQuery.data?.status === 200 ? projectQuery.data.data : undefined;
  const [scope, setScope] = useState("all");
  const [exactRevision, setExactRevision] = useState(false);
  const filters: ListBackendAnnotationsParams = {
    ...(scope === "orphaned" ? { orphaned: true } : {}),
    ...(scope === "selected" && selection
      ? { recordType: selection.recordType, targetId: selection.id }
      : {}),
    ...(exactRevision && selection ? { revisionId: selection.revisionId } : {}),
  };
  const filterKey = JSON.stringify(filters);
  const [pagination, setPagination] = useState({ filterKey: "", pages: [""] });
  const cursors = pagination.filterKey === filterKey ? pagination.pages : [""];
  const cursor = cursors.at(-1) ?? "";
  const notes = useQuery({
    queryKey: ["backend-annotations", projectId, filters, cursor],
    queryFn: async ({ signal }) => {
      const response = await listBackendAnnotations(
        projectId,
        { ...filters, limit: 100, ...(cursor ? { cursor } : {}) },
        { signal },
      ).catch((error: unknown) => {
        if (error instanceof ApiFailure && error.code === "backend_annotation_page_conflict") {
          void client.invalidateQueries({
            queryKey: getGetBackendProjectQueryKey(projectId),
            exact: true,
          });
        }
        throw error;
      });
      signal.throwIfAborted();
      if (
        response.status !== 200 ||
        response.data.projectId !== projectId ||
        !Number.isSafeInteger(response.data.projectVersion)
      )
        throw new Error("Не удалось прочитать заметки проекта");
      return response.data;
    },
    retry: false,
  });
  const page = notes.data;

  const [form, setForm] = useState<Form | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [remove, setRemove] = useState<BackendAnnotation | null>(null);
  const [removeVersion, setRemoveVersion] = useState(0);
  const [removedID, setRemovedID] = useState<string | null>(null);
  const addButton = useRef<HTMLButtonElement>(null);
  const notesHeading = useRef<HTMLHeadingElement>(null);
  const focusedRemoval = useRef<string | null>(null);
  const [pending, setPending] = useState<Attempt | null>(() => readAttempt(projectId));
  const updatePending = (value: Attempt | null) => {
    try {
      if (value) sessionStorage.setItem(pendingKey(projectId), JSON.stringify(value));
      else sessionStorage.removeItem(pendingKey(projectId));
    } catch {
      /* The mounted panel still retains the exact attempt if storage is disabled. */
    }
    setPending(value);
  };
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<unknown>(null);
  const [fresh, setFresh] = useState<{
    version: number;
    body?: string;
    target?: BackendAnnotationTarget;
  } | null>(null);
  const [notice, setNotice] = useState("");
  if (
    cursor &&
    notes.error instanceof ApiFailure &&
    notes.error.code === "backend_annotation_page_conflict"
  ) {
    setPagination({ filterKey, pages: [""] });
    setNotice("Список заметок изменился. Просмотр начат с первой страницы.");
  }
  useEffect(() => {
    onDirtyChange?.(!!pending || !!formOpen || !!remove);
    return () => onDirtyChange?.(false);
  }, [pending, formOpen, remove, onDirtyChange]);
  const version = page?.projectVersion;
  const writable =
    version !== undefined && Number.isSafeInteger(version) && version > 0 && !busy && !pending;
  useEffect(() => {
    if (!removedID || busy || remove || focusedRemoval.current === removedID) return;
    if (writable) addButton.current?.focus();
    else notesHeading.current?.focus();
    focusedRemoval.current = removedID;
  }, [removedID, busy, remove, writable]);
  const bodyBytes = new TextEncoder().encode(form?.body ?? "").length;
  const formValid =
    !!form &&
    form.body.trim() !== "" &&
    bodyBytes <= 16384 &&
    validID(form.target.id) &&
    (form.target.revisionId === undefined || validID(form.target.revisionId));

  const refresh = async () => {
    setPagination({ filterKey, pages: [""] });
    await Promise.all([
      client.invalidateQueries({ queryKey: getGetBackendProjectQueryKey(projectId), exact: true }),
      client.invalidateQueries({ queryKey: ["backend-annotations", projectId] }),
    ]);
  };
  const send = async (request: ApplyBackendProjectCommandsRequest) => {
    updatePending({ request, conflict: false });
    setBusy(true);
    setFailure(null);
    setFresh(null);
    try {
      const result = await applyBackendProjectCommands(projectId, request);
      if (result.status !== 200) throw new Error("Ответ на изменение заметки не получен");
      const command = request.commands[0];
      if (command?.type === "remove_annotation") setRemovedID(command.annotationId);
      updatePending(null);
      setFormOpen(false);
      setForm(null);
      setRemove(null);
      setNotice("Заметки сохранены. Выбранная ревизия модели сохранена.");
      await refresh();
    } catch (error) {
      setFailure(error);
      if (
        error instanceof ApiFailure &&
        error.status >= 400 &&
        error.status < 500 &&
        error.status !== 408 &&
        error.status !== 429
      ) {
        if (error.status === 409) updatePending({ request, conflict: true });
        else {
          const draft = annotationDraft(request);
          if (draft) {
            setForm(draft);
            setFormOpen(true);
          }
          updatePending(null);
        }
      }
    } finally {
      setBusy(false);
    }
  };
  const submit = (commands: BackendProjectCommand[], expectedVersion: number) => {
    if (!Number.isSafeInteger(expectedVersion) || expectedVersion <= 0 || busy || pending) return;
    void send({ expectedVersion, idempotencyKey: crypto.randomUUID(), commands });
  };
  const openCreate = () => {
    if (!writable) return;
    const target: BackendAnnotationTarget = {
      recordType: selection?.recordType ?? "node",
      id: selection?.id ?? "",
    };
    if (selection && project && selection.revisionId !== project.currentRevisionId)
      target.revisionId = selection.revisionId;
    setForm({ type: "create_annotation", id: crypto.randomUUID(), target, body: "", version });
    setFormOpen(true);
    setFailure(null);
    setNotice("");
  };
  const openEdit = (note: BackendAnnotation) => {
    if (!writable) return;
    setForm({
      type: "update_annotation",
      id: note.id,
      target: { ...note.target },
      body: note.body,
      version,
    });
    setFormOpen(true);
    setFailure(null);
    setNotice("");
  };
  const reloadConflict = async () => {
    setBusy(true);
    try {
      const command = pending?.request.commands[0];
      const annotationID = pending
        ? command && "annotationId" in command
          ? command.annotationId
          : undefined
        : (form?.id ?? remove?.id);
      const [projectResult, current] = await Promise.all([
        projectQuery.refetch(),
        listBackendAnnotations(projectId, annotationID ? { annotationId: annotationID } : {}),
      ]);
      if (projectResult.isError) throw projectResult.error;
      if (current.status !== 200) throw new Error("Не удалось перечитать заметки");
      setFresh({
        version: current.data.projectVersion,
        body: current.data.items[0]?.body,
        target: current.data.items[0]?.target,
      });
      await refresh();
    } catch (error) {
      setFailure(error);
    } finally {
      setBusy(false);
    }
  };
  const recovery =
    pending && !busy ? (
      <Alert
        color="yellow"
        role="alert"
        title={pending.conflict ? "Проект изменился" : "Результат запроса неизвестен"}
      >
        <Stack gap="sm">
          <Text size="sm">
            {pending.conflict
              ? "Перечитайте состояние и сравните его с вашим текстом перед новой попыткой."
              : "Повторите сохранённый запрос с теми же данными и ключом."}
          </Text>
          {pending.conflict ? (
            <Button variant="light" onClick={() => void reloadConflict()}>
              Перечитать проект и заметки
            </Button>
          ) : (
            <Button variant="light" onClick={() => void send(pending.request)}>
              Повторить тот же запрос
            </Button>
          )}
          {fresh && (
            <>
              <Text size="sm">Перечитанная версия проекта: {fresh.version}</Text>
              <Text size="sm" style={wrap}>
                {fresh.body === undefined
                  ? "Заметка отсутствует в перечитанном состоянии."
                  : `Сохранённый текст: ${fresh.body}`}
              </Text>
              {fresh.target && (
                <Code style={wrap}>
                  {fresh.target.recordType} · {fresh.target.id} ·{" "}
                  {fresh.target.revisionId ?? "текущий источник"}
                </Code>
              )}
              <Button
                variant="default"
                onClick={() => {
                  if (remove) setRemoveVersion(fresh.version);
                  const draft = annotationDraft(pending.request);
                  if (draft) {
                    setForm({ ...draft, version: fresh.version });
                    setFormOpen(true);
                  }
                  updatePending(null);
                  setFailure(null);
                  setFresh(null);
                }}
              >
                Продолжить после сравнения
              </Button>
            </>
          )}
        </Stack>
      </Alert>
    ) : null;

  return (
    <Paper
      component="section"
      withBorder
      p="md"
      aria-label="Заметки проекта"
      style={{ minWidth: 0 }}
    >
      <Stack>
        <Group justify="space-between">
          <Title ref={notesHeading} order={2} tabIndex={-1}>
            Заметки
          </Title>
          <Button ref={addButton} disabled={!writable} onClick={openCreate}>
            Добавить заметку
          </Button>
        </Group>
        <Group align="end">
          <NativeSelect
            label="Показать заметки"
            value={scope}
            onChange={(event) => setScope(event.currentTarget.value)}
            data={[
              { value: "all", label: "Все заметки проекта" },
              { value: "orphaned", label: "С удалённой целью" },
              ...(selection ? [{ value: "selected", label: "Выбранного объекта" }] : []),
            ]}
          />
          {selection && (
            <Checkbox
              label="Только привязанные к выбранной ревизии"
              checked={exactRevision}
              onChange={(event) => setExactRevision(event.currentTarget.checked)}
            />
          )}
        </Group>
        {(notes.isPending || projectQuery.isPending) && <Loader aria-label="Загружаем заметки" />}
        {notes.isError && (
          <Alert color="red" role="alert">
            {notes.error instanceof ApiFailure &&
            notes.error.code === "backend_annotation_page_conflict"
              ? "Список заметок изменился. Начните просмотр с первой страницы."
              : annotationError(notes.error)}
            <Button variant="light" onClick={() => void refresh()}>
              Перечитать список заметок
            </Button>
          </Alert>
        )}
        {projectQuery.isError && (
          <Alert color="red" role="alert">
            {annotationError(projectQuery.error)}
          </Alert>
        )}
        {notice && (
          <Text component="output" size="sm">
            {notice}
          </Text>
        )}
        {!formOpen && !remove && recovery}
        {!formOpen && !remove && !pending && !!failure && (
          <Alert role="alert" color="red">
            {annotationError(failure)}
          </Alert>
        )}
        {page?.items.length === 0 && <Text c="dimmed">Заметок пока нет</Text>}
        {page?.items.map((note) => (
          <Stack
            key={note.id}
            component="article"
            gap="xs"
            py="sm"
            style={{ borderBottom: "1px solid var(--mantine-color-default-border)" }}
          >
            <Group justify="space-between">
              <Badge color={note.targetStatus === "orphaned" ? "yellow" : "gray"}>
                {statusLabels[note.targetStatus]}
              </Badge>
              <Text size="xs" c="dimmed">
                {note.author} · {new Date(note.updatedAt).toLocaleString("ru-RU")}
              </Text>
            </Group>
            <Text style={wrap}>{note.body}</Text>
            <Code style={wrap}>
              {note.target.recordType} · {note.target.id}
              {note.target.revisionId ? ` · ${note.target.revisionId}` : ""}
            </Code>
            <Group gap="xs">
              <Button
                size="xs"
                variant="subtle"
                disabled={note.targetStatus === "orphaned"}
                onClick={() => onNavigate(note.target)}
              >
                Открыть цель
              </Button>
              <Button
                size="xs"
                variant="default"
                disabled={!writable}
                onClick={() => openEdit(note)}
              >
                Редактировать заметку
              </Button>
              <Button
                size="xs"
                variant="subtle"
                color="red"
                disabled={!writable}
                onClick={() => {
                  setRemovedID(null);
                  setRemove(note);
                  setRemoveVersion(version!);
                  setFailure(null);
                }}
              >
                Удалить заметку
              </Button>
            </Group>
          </Stack>
        ))}
        {(cursors.length > 1 || page?.nextCursor) && (
          <Group>
            <Button
              variant="default"
              disabled={cursors.length === 1 || notes.isFetching}
              onClick={() => setPagination({ filterKey, pages: cursors.slice(0, -1) })}
            >
              Предыдущие заметки
            </Button>
            <Button
              variant="default"
              disabled={!page?.nextCursor || notes.isFetching}
              onClick={() => {
                if (page?.nextCursor)
                  setPagination({ filterKey, pages: [...cursors, page.nextCursor] });
              }}
            >
              Следующие заметки
            </Button>
          </Group>
        )}
      </Stack>
      <Modal
        opened={formOpen}
        onClose={() => setFormOpen(false)}
        title={form?.type === "update_annotation" ? "Редактировать заметку" : "Новая заметка"}
        size="lg"
      >
        {form && (
          <form
            onSubmit={(event) => {
              event.preventDefault();
              if (!formValid) return;
              submit(
                [
                  {
                    type: form.type,
                    annotationId: form.id,
                    target: form.target,
                    body: form.body,
                  },
                ],
                form.version,
              );
            }}
          >
            <Stack>
              <Textarea
                data-autofocus
                label="Текст заметки"
                value={form.body}
                onChange={(event) => setForm({ ...form, body: event.currentTarget.value })}
                autosize
                minRows={4}
                maxRows={12}
                disabled={busy || !!pending}
                description={`${bodyBytes} / 16384 байт UTF-8`}
                error={bodyBytes > 16384 ? "Текст превышает 16 КиБ" : undefined}
              />
              <Group grow align="start">
                <NativeSelect
                  label="Тип цели"
                  value={form.target.recordType}
                  disabled={busy || !!pending}
                  onChange={(event) =>
                    setForm({
                      ...form,
                      target: {
                        ...form.target,
                        recordType: event.currentTarget.value as "node" | "edge",
                      },
                    })
                  }
                  data={[
                    { value: "node", label: "Объект" },
                    { value: "edge", label: "Связь" },
                  ]}
                />
                <TextInput
                  label="ID цели"
                  value={form.target.id}
                  disabled={busy || !!pending}
                  onChange={(event) =>
                    setForm({ ...form, target: { ...form.target, id: event.currentTarget.value } })
                  }
                />
              </Group>
              <Checkbox
                label="Привязать к выбранной ревизии"
                checked={form.target.revisionId !== undefined}
                disabled={busy || !!pending}
                onChange={(event) => {
                  const { revisionId: _revision, ...unbound } = form.target;
                  setForm({
                    ...form,
                    target: event.currentTarget.checked
                      ? {
                          ...unbound,
                          revisionId: selection?.revisionId ?? project?.currentRevisionId ?? "",
                        }
                      : unbound,
                  });
                }}
              />
              {form.target.revisionId !== undefined && (
                <TextInput
                  label="ID ревизии"
                  value={form.target.revisionId}
                  disabled={busy || !!pending}
                  onChange={(event) =>
                    setForm({
                      ...form,
                      target: { ...form.target, revisionId: event.currentTarget.value },
                    })
                  }
                />
              )}
              {!!failure && !pending && (
                <Alert color="red" role="alert">
                  {annotationError(failure)}
                </Alert>
              )}
              {recovery}
              <Group justify="flex-end">
                <Button variant="default" onClick={() => setFormOpen(false)}>
                  Закрыть
                </Button>
                <Button type="submit" disabled={!formValid || !!pending} loading={busy}>
                  Сохранить заметку
                </Button>
              </Group>
            </Stack>
          </form>
        )}
      </Modal>
      <Modal
        opened={!!remove}
        onClose={() => setRemove(null)}
        returnFocus={removedID === null}
        title="Удалить заметку?"
      >
        <Stack>
          <Text style={wrap}>{remove?.body}</Text>
          {recovery}
          {!!failure && !pending && (
            <Alert color="red" role="alert">
              {annotationError(failure)}
            </Alert>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setRemove(null)}>
              Отмена
            </Button>
            <Button
              color="red"
              disabled={!writable}
              loading={busy}
              onClick={() => {
                if (remove && removeVersion)
                  submit([{ type: "remove_annotation", annotationId: remove.id }], removeVersion);
              }}
            >
              Удалить
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Paper>
  );
}

import type { BackendNode } from "@/api/generated/schemas";
import { useContext, useEffect, useRef, useState } from "react";
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
  TextInput,
  Title,
} from "@mantine/core";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiFailure } from "@/api/client";
import {
  createBackendProposal,
  getBackendProposal,
  listBackendProposals,
  previewBackendProposalCommands,
  applyBackendProposalCommands,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  ApplyBackendProposalCommandsRequest,
  BackendProposalCommand,
  BackendProposalDetail,
  BackendProposalPreview,
  BackendProposalCriterionInput,
  CreateBackendProposalRequest,
} from "@/api/generated/schemas";
import { BackendSavedViewContext } from "./backendSavedViewState";
import { BackendDatabaseEditForm } from "./BackendDatabaseEditForm";
import { readProposalRecovery, writeProposalRecovery } from "./backendProposalRecovery";
import {
  databaseWrap,
  datastoreScope,
  readDatabaseGraph,
  type DatabaseContext,
} from "./backendDatabaseReads";

export type ProposalView = {
  proposalId: string;
  proposalRevisionId: string;
  baseRevisionId: string;
};
const message = (error: unknown) =>
  error instanceof Error ? error.message : "Не удалось выполнить запрос";
const uncertain = (error: unknown) =>
  !(error instanceof ApiFailure) ||
  error.status >= 500 ||
  error.status === 408 ||
  error.status === 429;

export function BackendDatabaseProposal({
  ...props
}: {
  context: DatabaseContext;
  repositoryId: string;
  onView?: (view: ProposalView | null) => void;
  onDirty?: (dirty: boolean) => void;
  initialColumnId?: string;
  initialView?: ProposalView;
}) {
  const { context, repositoryId } = props;
  return (
    <DatabaseProposalControls
      key={`${context.projectId}:${context.revisionId}:${context.datastoreId}:${context.facetKey}:${repositoryId}`}
      {...props}
    />
  );
}

function DatabaseProposalControls({
  context,
  repositoryId,
  onView,
  onDirty,
  initialColumnId,
  initialView,
}: {
  context: DatabaseContext;
  repositoryId: string;
  onView?: (view: ProposalView | null) => void;
  onDirty?: (dirty: boolean) => void;
  initialColumnId?: string;
  initialView?: ProposalView;
}) {
  const client = useQueryClient();
  const savedViewSession = useContext(BackendSavedViewContext);
  const recoveryKey = `backend-proposal-create:${context.projectId}:${context.revisionId}:${repositoryId}:${context.datastoreId}:${context.facetKey}`;
  const listKey = ["backend-proposals", context.projectId, context.datastoreId, context.facetKey];
  const [selected, setSelected] = useState(initialView?.proposalId ?? "");
  const [revision, setRevision] = useState(initialView?.proposalRevisionId ?? "");
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [creating, setCreating] = useState(false);
  const [createAttempt, updateCreateAttempt] = useState<CreateBackendProposalRequest | null>(
    () => readProposalRecovery(recoveryKey, "create") as CreateBackendProposalRequest | null,
  );
  function setCreateAttempt(input: CreateBackendProposalRequest | null) {
    writeProposalRecovery(recoveryKey, input);
    updateCreateAttempt(input);
  }
  const dirty = useRef(false);
  const alive = useRef(true);
  const callbacks = useRef({ onView, onDirty });
  useEffect(() => {
    callbacks.current = { onView, onDirty };
  }, [onView, onDirty]);
  useEffect(() => {
    onDirty?.(dirty.current || !!createAttempt);
  }, [createAttempt, onDirty]);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
      callbacks.current.onView?.(null);
      callbacks.current.onDirty?.(false);
    };
  }, []);
  const list = useQuery({
    queryKey: listKey,
    retry: false,
    queryFn: async ({ signal }) => {
      const items: BackendProposalDetail["proposal"][] = [];
      const seen = new Set<string>();
      let cursor = "";
      do {
        const response = await listBackendProposals(
          context.projectId,
          { limit: 500, ...(cursor ? { cursor } : {}) },
          { signal },
        );
        signal.throwIfAborted();
        if (response.status !== 200) throw new Error("Не удалось прочитать предложения");
        items.push(...response.data.items);
        cursor = response.data.nextCursor;
        if (cursor && seen.has(cursor)) throw new Error("Список предложений неполон");
        seen.add(cursor);
      } while (cursor);
      return items.filter(
        (p) => p.datastoreId === context.datastoreId && p.facetKey === context.facetKey,
      );
    },
  });
  const detailKey = ["backend-proposal", context.projectId, selected, revision];
  const detail = useQuery({
    queryKey: detailKey,
    enabled: !!selected,
    retry: false,
    queryFn: async ({ signal }) => {
      const response = await getBackendProposal(
        context.projectId,
        selected,
        { limit: 500, ...(revision ? { proposalRevisionId: revision } : {}) },
        { signal },
      );
      signal.throwIfAborted();
      if (response.status !== 200) throw new Error("Не удалось прочитать предложение");
      const value = response.data;
      const history = [...value.history];
      let cursor = value.nextCursor;
      const seen = new Set<string>();
      while (cursor) {
        if (seen.has(cursor)) throw new Error("История неполна");
        seen.add(cursor);
        const next = await getBackendProposal(
          context.projectId,
          selected,
          { limit: 500, cursor, proposalRevisionId: value.revision.id },
          { signal },
        );
        signal.throwIfAborted();
        if (next.status !== 200) throw new Error("История неполна");
        history.push(...next.data.history);
        cursor = next.data.nextCursor;
      }
      return { ...value, history, nextCursor: "" };
    },
  });
  const value = detail.data;
  useEffect(() => {
    if (value)
      onView?.({
        proposalId: value.proposal.id,
        proposalRevisionId: value.revision.id,
        baseRevisionId: value.proposal.baseRevisionId,
      });
  }, [value, onView]);
  function switchTo(id: string, revisionId = "") {
    if (
      (dirty.current || savedViewSession?.dirty || savedViewSession?.pending) &&
      !window.confirm("Есть несохранённые команды или изменения вида. Перейти и оставить их?")
    )
      return;
    dirty.current = false;
    onDirty?.(false);
    setSelected(id);
    setRevision(revisionId);
    onView?.(null);
  }
  async function create() {
    if (creating || (!createAttempt && !name.trim())) return;
    if (
      !createAttempt &&
      dirty.current &&
      !window.confirm("Есть несохранённые команды. Создать и открыть другое предложение?")
    )
      return;
    const input = createAttempt ?? {
      name: name.trim(),
      baseRevisionId: context.revisionId,
      repositoryId,
      datastoreId: context.datastoreId,
      facetKey: context.facetKey,
      idempotencyKey: crypto.randomUUID(),
    };
    setCreateAttempt(input);
    setCreating(true);
    setError("");
    try {
      const response = await createBackendProposal(context.projectId, input);
      if (!alive.current) return;
      if (response.status !== 200) throw new Error("Не удалось создать предложение");
      setCreateAttempt(null);
      setSelected(response.data.proposal.id);
      setRevision("");
      client.setQueryData(
        ["backend-proposal", context.projectId, response.data.proposal.id, ""],
        response.data,
      );
      void client.invalidateQueries({ queryKey: listKey });
    } catch (failure) {
      if (alive.current) {
        setError(message(failure));
        if (!uncertain(failure)) setCreateAttempt(null);
      }
    } finally {
      if (alive.current) setCreating(false);
    }
  }
  return (
    <Paper withBorder p="md" component="section" aria-label="Предложения изменений базы данных">
      <Stack>
        <Title order={3}>Предложения изменений</Title>
        <Text size="sm">
          Желаемая схема и критерии проверки. Данные, писатели и применение миграции пока не
          проверены.
        </Text>
        {list.isError && (
          <Alert role="alert" color="red">
            {message(list.error)}
            <Button variant="subtle" onClick={() => void list.refetch()}>
              Повторить загрузку предложений
            </Button>
          </Alert>
        )}
        <NativeSelect
          label="Предложение изменений"
          disabled={!!createAttempt}
          value={selected}
          onChange={(event) => switchTo(event.currentTarget.value)}
          data={[
            { value: "", label: "Исходная схема" },
            ...(list.data ?? []).map((p) => ({ value: p.id, label: p.name })),
          ]}
        />
        <Group align="flex-end">
          <TextInput
            label="Название предложения"
            value={createAttempt?.name ?? name}
            disabled={!!createAttempt}
            onChange={(event) => setName(event.currentTarget.value)}
            style={{ flex: "1 1 200px" }}
          />
          <Button
            disabled={creating || (!name.trim() && !createAttempt)}
            onClick={() => void create()}
          >
            {createAttempt && !creating ? "Повторить создание" : "Создать предложение"}
          </Button>
        </Group>
        {error && (
          <Alert role="alert" color="red">
            {error}
          </Alert>
        )}
        {selected && detail.isPending && <Text component="output">Загружаем предложение</Text>}
        {detail.isError && (
          <Alert role="alert" color="red">
            {message(detail.error)}
            <Button onClick={() => void detail.refetch()}>Повторить загрузку</Button>
          </Alert>
        )}
        {value && (
          <>
            {value.baseOutdated && (
              <Alert color="yellow">
                Источник обновился. Предложение закреплено за прежней базой{" "}
                {value.proposal.baseRevisionId}. Автоматического переноса нет.
              </Alert>
            )}
            <NativeSelect
              label="История предложения"
              value={revision}
              onChange={(event) => switchTo(selected, event.currentTarget.value)}
              data={[
                { value: "", label: "Текущий черновик" },
                ...value.history.map((item) => ({
                  value: item.id,
                  label: `${item.createdAt} · ${item.summary}`,
                })),
              ]}
            />
            <Text size="xs" style={databaseWrap}>
              База: {value.proposal.baseRevisionId} · черновик: {value.revision.id} · версия:{" "}
              {value.proposal.version}
            </Text>
            <ProposalEditor
              key={`${selected}:${revision}`}
              detail={value}
              context={context}
              readOnly={!!revision}
              initialColumnId={initialColumnId}
              reread={() => detail.refetch().then((result) => result.data)}
              onDirty={(next) => {
                dirty.current = next;
                onDirty?.(next);
              }}
              onSaved={(saved) => {
                client.setQueryData(detailKey, saved);
                void client.invalidateQueries({ queryKey: listKey });
              }}
            />
            <BackendAPIArtifacts
              projectId={context.projectId}
              revisionId={value.proposal.baseRevisionId}
              readOnly
            />
          </>
        )}
      </Stack>
    </Paper>
  );
}

function ProposalEditor({
  detail,
  context,
  readOnly,
  reread,
  onSaved,
  onDirty,
  initialColumnId,
}: {
  detail: BackendProposalDetail;
  context: DatabaseContext;
  readOnly: boolean;
  reread: () => Promise<BackendProposalDetail | undefined>;
  onSaved: (detail: BackendProposalDetail) => void;
  onDirty: (dirty: boolean) => void;
  initialColumnId?: string;
}) {
  const [base, setBase] = useState(detail);
  const recoveryKey = `backend-proposal-apply:${context.projectId}:${detail.proposal.id}`;
  const [attempt, updateAttempt] = useState<ApplyBackendProposalCommandsRequest | null>(() =>
    readOnly
      ? null
      : (readProposalRecovery(recoveryKey, "apply") as ApplyBackendProposalCommandsRequest | null),
  );
  function setAttempt(input: ApplyBackendProposalCommandsRequest | null) {
    writeProposalRecovery(recoveryKey, input);
    updateAttempt(input);
  }
  const [commands, setCommands] = useState<BackendProposalCommand[]>(() => attempt?.commands ?? []);
  const [preview, setPreview] = useState<BackendProposalPreview | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [conflict, setConflict] = useState(false);
  const [reconciliation, setReconciliation] = useState<BackendProposalDetail | null>(null);
  const epoch = useRef(0);
  const controller = useRef<AbortController | null>(null);
  useEffect(
    () => () => {
      epoch.current++;
      controller.current?.abort();
    },
    [],
  );
  useEffect(() => {
    const dirty = commands.length > 0 || !!attempt;
    onDirty(dirty);
    const beforeUnload = (event: BeforeUnloadEvent) => {
      if (dirty) {
        event.preventDefault();
        event.returnValue = "";
      }
    };
    window.addEventListener("beforeunload", beforeUnload);
    return () => window.removeEventListener("beforeunload", beforeUnload);
  }, [commands, attempt, onDirty]);
  const nodes = useQuery({
    queryKey: [
      "backend-proposal-source",
      context.projectId,
      base.proposal.baseRevisionId,
      base.proposal.baseSemanticHash,
      base.proposal.datastoreId,
      base.proposal.facetKey,
    ],
    retry: false,
    staleTime: Infinity,
    queryFn: async ({ signal }) => {
      const graph = await readDatabaseGraph(
        context.projectId,
        { revisionId: base.proposal.baseRevisionId, recordType: "nodes" },
        signal,
      );
      return datastoreScope(graph.nodes, base.proposal.datastoreId).filter(
        (node): node is BackendNode => typeof node.externalKey === "string",
      );
    },
  });
  function change(next: BackendProposalCommand[]) {
    epoch.current++;
    controller.current?.abort();
    setBusy(false);
    setCommands(next);
    setPreview(null);
    setError("");
    setNotice("");
  }
  const authored: BackendProposalCriterionInput[] =
    commands.findLast((command) => command.type === "set_criteria")?.criteria ??
    base.revision.criteria
      .filter((criterion) => criterion.origin === "authored")
      .map(({ key, kind, targetIds, description }) => ({ key, kind, targetIds, description }));
  async function runPreview() {
    const token = ++epoch.current;
    controller.current?.abort();
    controller.current = new AbortController();
    setBusy(true);
    setError("");
    setPreview(null);
    try {
      const response = await previewBackendProposalCommands(
        context.projectId,
        base.proposal.id,
        {
          expectedVersion: base.proposal.version,
          draftRevisionId: base.proposal.draftRevisionId,
          commands,
        },
        { signal: controller.current.signal },
      );
      if (token !== epoch.current) return;
      if (response.status !== 200) throw new Error("Предпросмотр недоступен");
      setPreview(response.data);
    } catch (failure) {
      if (token === epoch.current) {
        setError(message(failure));
        if (failure instanceof ApiFailure && failure.status === 409) setConflict(true);
      }
    } finally {
      if (token === epoch.current) setBusy(false);
    }
  }
  async function save() {
    if (busy || conflict || (!attempt && !preview?.candidateHash)) return;
    const input = attempt ?? {
      expectedVersion: base.proposal.version,
      draftRevisionId: base.proposal.draftRevisionId,
      candidateHash: preview!.candidateHash!,
      commands: structuredClone(commands),
      idempotencyKey: crypto.randomUUID(),
    };
    const token = ++epoch.current;
    controller.current?.abort();
    controller.current = new AbortController();
    setAttempt(input);
    setBusy(true);
    setError("");
    try {
      const response = await applyBackendProposalCommands(
        context.projectId,
        base.proposal.id,
        input,
        { signal: controller.current.signal },
      );
      if (token !== epoch.current) return;
      if (response.status !== 200) throw new Error("Сохранение не подтверждено");
      const result = response.data;
      const saved = {
        ...base,
        proposal: result.proposal,
        revision: result.revision,
        lastApplyReceipt: result,
        effectiveGraphHash: result.candidateGraphHash,
        history: [
          {
            id: result.revision.id,
            parentRevisionId: result.revision.parentRevisionId,
            semanticHash: result.revision.semanticHash,
            createdAt: result.revision.createdAt,
            author: result.revision.author,
            summary: result.revision.summary,
          },
          ...base.history,
        ],
      };
      setBase(saved);
      onSaved(saved);
      setCommands([]);
      setAttempt(null);
      setPreview(null);
      setNotice("Предложение сохранено");
    } catch (failure) {
      if (token !== epoch.current) return;
      setError(message(failure));
      if (failure instanceof ApiFailure && failure.status === 409) {
        setConflict(true);
        setPreview(null);
        setAttempt(null);
      } else if (!uncertain(failure)) setAttempt(null);
    } finally {
      if (token === epoch.current) setBusy(false);
    }
  }
  async function reload() {
    const token = ++epoch.current;
    setBusy(true);
    setError("");
    try {
      const value = await reread();
      if (value && token === epoch.current) setReconciliation(value);
    } catch (failure) {
      if (token === epoch.current) setError(message(failure));
    } finally {
      if (token === epoch.current) setBusy(false);
    }
  }
  return (
    <Stack>
      {readOnly && <Text>Историческая ревизия · только чтение</Text>}
      {nodes.isError && (
        <Alert color="red" role="alert">
          {message(nodes.error)}
          <Button onClick={() => void nodes.refetch()}>Повторить загрузку объектов</Button>
        </Alert>
      )}
      {!readOnly && nodes.data && (
        <fieldset
          disabled={!!attempt || conflict || commands.length >= 100}
          style={{ border: 0, padding: 0, minWidth: 0 }}
        >
          <legend>Изменения схемы</legend>
          <BackendDatabaseEditForm
            key={`${base.revision.id}:form:${initialColumnId ?? ""}`}
            initialColumnId={initialColumnId}
            nodes={nodes.data}
            facetKey={base.proposal.facetKey}
            criteria={authored}
            designedConstraints={base.revision.overlays
              .filter((overlay) => overlay.kind === "constraint" && overlay.base === null)
              .map((overlay) => ({
                id: overlay.subjectId,
                name: overlay.name ?? overlay.subjectId,
                parentId: overlay.parentId ?? null,
              }))}
            onAdd={(command) => change([...commands, command])}
          />
        </fieldset>
      )}
      <Stack aria-label="Буфер команд">
        {commands.map((command, index) => (
          <Paper withBorder p="sm" key={command.commandId}>
            <Text>
              {index + 1}. {command.type} · {command.reason}
            </Text>
            <Button
              variant="subtle"
              aria-label={`Удалить команду ${index + 1}`}
              disabled={!!attempt}
              onClick={() => change(commands.filter((_, i) => i !== index))}
            >
              Удалить
            </Button>
          </Paper>
        ))}
      </Stack>
      {error && (
        <Alert color="red" role="alert">
          {error}
        </Alert>
      )}
      {notice && <Text component="output">{notice}</Text>}
      {conflict && (
        <>
          <Alert color="yellow">
            Черновик изменился. Перечитайте предложение и согласуйте команды с новой ревизией.
          </Alert>
          <Button disabled={busy} onClick={() => void reload()}>
            Перечитать предложение
          </Button>
          {reconciliation && (
            <>
              <Text style={databaseWrap}>
                Новый черновик: {reconciliation.proposal.draftRevisionId}. Проверьте и удалите
                команды, которые больше не нужны.
              </Text>
              <Button
                onClick={() => {
                  change(commands);
                  setBase(reconciliation);
                  setConflict(false);
                  setReconciliation(null);
                }}
              >
                Команды согласованы; перейти к новой базе
              </Button>
            </>
          )}
        </>
      )}
      {!readOnly && !conflict && (
        <Group>
          <Button
            disabled={busy || !!attempt || commands.length === 0}
            onClick={() => void runPreview()}
          >
            Предпросмотр
          </Button>
          {(preview?.candidateHash || attempt) && (
            <Button disabled={busy} onClick={() => void save()}>
              {attempt ? "Повторить сохранение" : "Сохранить предложение"}
            </Button>
          )}
        </Group>
      )}
      {attempt && !busy && (
        <Text component="output">
          Ответ не подтверждён. Повтор использует те же команды, закреплённый черновик и ключ
          запроса.
        </Text>
      )}
      {preview && (
        <Stack aria-label="Предпросмотр изменений">
          <Title order={4}>До / после</Title>
          {preview.changes.map((change) => (
            <Paper withBorder p="sm" key={change.commandId}>
              <Text style={databaseWrap}>
                {change.subjectId} · {change.type}
              </Text>
              {"nullable" in change.after && (
                <Badge>
                  {(change.after.nullable as { value?: boolean }).value === false
                    ? "Желаемое NOT NULL"
                    : "Желаемое NULL"}
                </Badge>
              )}
              <Text>До · источник или предыдущий черновик</Text>
              <Code block style={databaseWrap}>
                {JSON.stringify(change.before, null, 2)}
              </Code>
              <Text>После · намерение</Text>
              <Code block style={databaseWrap}>
                {JSON.stringify(change.after, null, 2)}
              </Code>
              {Object.entries(change.generatedIds).map(([role, id]) => (
                <Text key={role} size="xs" style={databaseWrap}>
                  {role}: {id}
                </Text>
              ))}
            </Paper>
          ))}
          {preview.diagnostics.map((diagnostic, i) => (
            <Alert role="alert" color="yellow" key={i}>
              {diagnostic.message} · {diagnostic.property}
            </Alert>
          ))}
          {preview.limitations.map((text) => (
            <Text key={text} size="sm">
              {text}
            </Text>
          ))}
        </Stack>
      )}
      <Stack aria-label="Критерии проверки">
        {(preview?.criteria ?? base.revision.criteria).map((criterion) => (
          <Paper withBorder p="sm" key={criterion.key}>
            <Badge>
              Не проверено · {criterion.origin === "required" ? "Обязательный" : "Авторский"}
            </Badge>
            <Text>{criterion.description}</Text>
            <Text size="xs" style={databaseWrap}>
              {criterion.kind} · {criterion.targetIds.join(", ")}
            </Text>
          </Paper>
        ))}
      </Stack>
      <Text size="sm">Анализ влияния пока недоступен.</Text>
    </Stack>
  );
}
import { BackendAPIArtifacts } from "./BackendAPIArtifacts";

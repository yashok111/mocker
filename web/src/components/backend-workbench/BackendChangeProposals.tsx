import { useBackendChangeRecovery } from "./useBackendChangeRecovery";
import { BackendChangeRecoveryNotice } from "./BackendChangeRecoveryNotice";
import { useEffect, useRef, useState } from "react";
import { Alert, Button, Group, NativeSelect, Stack, Text, TextInput, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { ApiFailure } from "@/api/client";
import { createBackendChangeProposal } from "@/api/generated/backend-projects/backend-projects";
import type { BackendChangeProposalDetail } from "@/api/generated/schemas";
import { BackendChangeEditor } from "./BackendChangeEditor";
import { readChangeProposal, readChangeProposals, verifyChangeDetail } from "./backendChangeReads";
import {
  changeMessage,
  changeUncertain,
  makeChangeAttempt,
  changeCreateRecoveryKey,
} from "./backendChangeRecovery";

export function BackendChangeProposals(props: {
  projectId: string;
  baseRevisionId: string;
  baseSchemaVersion: string;
  readOnly?: boolean;
  onDirty?: (dirty: boolean) => void;
}) {
  return <ChangeProposalPanel key={`${props.projectId}:${props.baseRevisionId}`} {...props} />;
}
function ChangeProposalPanel({
  projectId,
  baseRevisionId,
  baseSchemaVersion,
  readOnly = false,
  onDirty,
}: {
  projectId: string;
  baseRevisionId: string;
  baseSchemaVersion: string;
  readOnly?: boolean;
  onDirty?: (dirty: boolean) => void;
}) {
  const recovery = useBackendChangeRecovery(changeCreateRecoveryKey(projectId), projectId);
  const attempt = recovery.attempt;
  const [name, setName] = useState(() => (attempt?.kind === "create" ? attempt.input.name : ""));
  const [statusFilter, setStatusFilter] = useState("");
  const [selected, setSelected] = useState("");
  const [detail, setDetail] = useState<BackendChangeProposalDetail | null>(null);
  const [historical, setHistorical] = useState("");
  const [editorEpoch, setEditorEpoch] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [editorDirty, setEditorDirty] = useState(false);
  const lifecycle = useRef({ alive: true, epoch: 0, controller: null as AbortController | null });
  const onDirtyRef = useRef(onDirty);
  useEffect(() => {
    onDirtyRef.current = onDirty;
  }, [onDirty]);
  const supported = baseSchemaVersion === "5" || baseSchemaVersion === "6";
  const list = useQuery({
    queryKey: ["backend-change-proposals", projectId],
    queryFn: ({ signal }) => readChangeProposals(projectId, signal),
    retry: false,
  });
  useEffect(() => {
    onDirtyRef.current?.(editorDirty || !!attempt || recovery.blocked);
  }, [editorDirty, attempt, recovery.blocked]);
  useEffect(() => {
    const state = lifecycle.current;
    state.alive = true;
    return () => {
      state.alive = false;
      state.epoch++;
      state.controller?.abort();
      onDirtyRef.current?.(false);
    };
  }, []);
  async function load(proposalId: string, revisionId?: string) {
    if (editorDirty || attempt || busy || recovery.blocked) return;
    const token = ++lifecycle.current.epoch;
    lifecycle.current.controller?.abort();
    lifecycle.current.controller = new AbortController();
    setBusy(true);
    setError("");
    try {
      const value = await readChangeProposal(
        projectId,
        proposalId,
        revisionId,
        lifecycle.current.controller.signal,
      );
      if (token !== lifecycle.current.epoch) return;
      setDetail(value);
      setSelected(proposalId);
      setHistorical(value.revision.id);
      setEditorEpoch((value) => value + 1);
    } catch (failure) {
      if (token === lifecycle.current.epoch) setError(changeMessage(failure));
    } finally {
      if (token === lifecycle.current.epoch) setBusy(false);
    }
  }
  async function create() {
    if (
      busy ||
      readOnly ||
      (!attempt && (!supported || recovery.blocked)) ||
      attempt?.phase === "conflict" ||
      recovery.cleanupPending
    )
      return;
    let next = attempt;
    try {
      if (!next) {
        if (!name.trim()) return;
        next = makeChangeAttempt("create", {
          name: name.trim(),
          baseRevisionId,
          idempotencyKey: crypto.randomUUID(),
        });
      }
    } catch (failure) {
      setError(changeMessage(failure));
      return;
    }
    if (next.kind !== "create" || !recovery.persist(next)) return;
    const pending = next;
    const token = ++lifecycle.current.epoch;
    const active = () => lifecycle.current.alive && token === lifecycle.current.epoch;
    setBusy(true);
    setError("");
    try {
      const response = await createBackendChangeProposal(projectId, pending.input);
      if (!active()) return;
      if (response.status !== 200) throw new Error("Создание не подтверждено");
      const value = verifyChangeDetail(response.data, projectId);
      if (value.revision.baseRevisionId !== pending.input.baseRevisionId)
        throw new Error("Создание вернуло другую базовую ревизию");
      setSelected(value.proposal.id);
      setDetail(value);
      setHistorical(value.revision.id);
      setEditorEpoch((value) => value + 1);
      setName("");
      recovery.clear(true);
      void list.refetch();
    } catch (failure) {
      if (!active()) return;
      setError(changeMessage(failure));
      if (failure instanceof ApiFailure && failure.status === 409)
        recovery.persist({ ...pending, phase: "conflict" });
      else if (!changeUncertain(failure)) recovery.clear(true);
    } finally {
      if (active()) setBusy(false);
    }
  }
  return (
    <Stack gap="md">
      <Title order={3} id="backend-change-proposals-title" tabIndex={-1}>
        Изменения модели
      </Title>
      <Text size="sm" c="dimmed">
        Предложение закрепляет исходную ревизию. Локальные команды сохраняются после предпросмотра.
      </Text>
      {!supported && (
        <Alert color="yellow">
          Создание доступно для исходной схемы 5 или 6. Выберите соответствующую ревизию.
        </Alert>
      )}
      {error && (
        <Alert color="red" role="alert">
          {error}
        </Alert>
      )}
      <BackendChangeRecoveryNotice recovery={recovery} busy={busy} />
      <fieldset
        disabled={readOnly || !supported || busy || editorDirty || !!attempt || recovery.blocked}
        style={{ border: 0, padding: 0, minWidth: 0 }}
      >
        <legend>Новое предложение</legend>
        <Group align="end">
          <TextInput
            label="Название предложения"
            value={name}
            onChange={(event) => setName(event.currentTarget.value)}
            maxLength={200}
          />
          <Button disabled={!name.trim()} onClick={() => void create()}>
            Создать предложение
          </Button>
        </Group>
      </fieldset>
      {attempt?.kind === "create" && attempt.phase === "unknown" && !recovery.cleanupPending && (
        <Alert color="yellow">
          <Stack gap="xs">
            <Text>
              {recovery.unsent
                ? "Запрос ещё не отправлен. Повтор сначала сохранит исходные параметры."
                : "Ответ на создание неизвестен. Повторение сохранит исходные параметры."}
            </Text>
            <Text size="sm">Исходная база запроса: {attempt.input.baseRevisionId}</Text>
            {attempt.input.baseRevisionId !== baseRevisionId && (
              <Text size="sm">
                Выбрана другая исходная ревизия. Повторить создание означает вернуться к сохранённой
                базе; запрос на новую базу не переносится.
              </Text>
            )}
            <Button disabled={busy || readOnly} onClick={() => void create()}>
              Повторить создание
            </Button>
          </Stack>
        </Alert>
      )}
      {attempt?.phase === "conflict" && (
        <Alert color="orange">
          <Stack gap="xs">
            <Text>
              Создание отклонено с конфликтом. Сверьте выбранную исходную ревизию и список
              предложений.
            </Text>
            <Button onClick={() => void list.refetch()}>Обновить список предложений</Button>
            <Button
              variant="outline"
              onClick={() => {
                if (recovery.clear()) setError("");
              }}
            >
              Начать новое создание после сверки
            </Button>
          </Stack>
        </Alert>
      )}
      {list.isError && (
        <Alert color="red">
          {changeMessage(list.error)}
          <Button variant="subtle" onClick={() => void list.refetch()}>
            Повторить список
          </Button>
        </Alert>
      )}
      <NativeSelect
        label="Статус предложений"
        value={statusFilter}
        data={[
          { value: "", label: "Все статусы" },
          ...["draft", "ready", "implemented", "archived"].map((value) => ({
            value,
            label: value,
          })),
        ]}
        disabled={busy || editorDirty || !!attempt || recovery.blocked}
        onChange={(e) => setStatusFilter(e.currentTarget.value)}
      />
      <Group align="end">
        <NativeSelect
          label="Полное предложение"
          value={selected}
          disabled={busy || editorDirty || !!attempt || recovery.blocked}
          data={[
            { value: "", label: list.isPending ? "Загрузка…" : "Выберите предложение" },
            ...(list.data ?? [])
              .filter(
                (item) => !statusFilter || item.status === statusFilter || item.id === selected,
              )
              .map((item) => ({
                value: item.id,
                label: `${item.name} · ${item.status} · версия ${item.version}`,
              })),
          ]}
          onChange={(event) => {
            if (event.currentTarget.value) void load(event.currentTarget.value);
            else {
              setSelected("");
              setDetail(null);
            }
          }}
        />
        {selected && (
          <Button
            variant="subtle"
            disabled={busy || editorDirty || !!attempt || recovery.blocked}
            onClick={() => void load(selected)}
          >
            Открыть текущий черновик
          </Button>
        )}
      </Group>
      {detail && (
        <>
          <NativeSelect
            label="История черновика"
            value={historical}
            disabled={busy || editorDirty || !!attempt || recovery.blocked}
            data={detail.history.map((item) => ({
              value: item.id,
              label: `${item.createdAt} · ${item.summary} · ${item.id}`,
            }))}
            onChange={(event) => void load(detail.proposal.id, event.currentTarget.value)}
          />
          <BackendChangeEditor
            key={`${detail.proposal.id}:${editorEpoch}`}
            projectId={projectId}
            detail={detail}
            readOnly={readOnly}
            onDirty={setEditorDirty}
            onSaved={(value) => {
              setDetail(value);
              setHistorical(value.revision.id);
              void list.refetch();
            }}
          />
        </>
      )}
    </Stack>
  );
}

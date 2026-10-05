import { useDiagramChangeHandoff } from "./BackendDiagramChangeContext";
import { BackendChangeLifecycle } from "./BackendChangeLifecycle";
import { BackendAnalysisJobs } from "./BackendAnalysisJobs";
import { BackendChangeRebase } from "./BackendChangeRebase";
import { useBackendAnalysisRecovery, type AnalysisAttempt } from "./backendAnalysisRecovery";
import { useBackendChangeRecovery } from "./useBackendChangeRecovery";
import { BackendChangeRecoveryNotice } from "./BackendChangeRecoveryNotice";
import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Checkbox,
  Group,
  NativeSelect,
  Paper,
  Stack,
  Text,
  Title,
} from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { ApiFailure } from "@/api/client";
import {
  applyBackendChangeProposalCommands,
  previewBackendChangeProposalCommands,
  restoreBackendChangeProposal,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendChangeProposalApplyResult,
  BackendChangeProposalCandidate,
  BackendChangeProposalCommand,
  BackendChangeProposalDetail,
} from "@/api/generated/schemas";
import { BackendEffectiveViews } from "./BackendEffectiveViews";
import { BackendChangeCommandForms, changeCommandLabels } from "./BackendChangeCommandForms";
import { readChangeIdentities, readChangeProposal } from "./backendChangeReads";
import {
  changeMessage,
  changeUncertain,
  makeChangeAttempt,
  type ChangeAttempt,
} from "./backendChangeRecovery";
import { changePendingIdentities } from "./backendChangeState";

const wrap = { overflowWrap: "anywhere" as const };
export function BackendChangeEditor({
  projectId,
  detail,
  onSaved,
  onDirty,
  readOnly = false,
}: {
  projectId: string;
  detail: BackendChangeProposalDetail;
  onSaved: (detail: BackendChangeProposalDetail) => void;
  onDirty?: (dirty: boolean) => void;
  readOnly?: boolean;
}) {
  const handoff = useDiagramChangeHandoff()?.handoff;
  const recoveryKey = `backend-change-attempt:${projectId}:${detail.proposal.id}`;
  const [base, setBase] = useState(detail);
  const [selectedReport, setSelectedReport] = useState<{
    jobId: string;
    resultVersion: number;
    requestId?: string;
  }>();
  const analysisRecovery = useBackendAnalysisRecovery(projectId);
  const [rebaseDirty, setRebaseDirty] = useState(false);
  const [inputEpoch, setInputEpoch] = useState(0);
  const recovery = useBackendChangeRecovery(recoveryKey);
  const attempt = recovery.attempt;
  const [commands, setCommands] = useState<BackendChangeProposalCommand[]>(() => {
    const recovered = recovery.attempt;
    return recovered?.kind === "apply" ? recovered.input.commands : [];
  });
  const [undo, setUndo] = useState<BackendChangeProposalCommand[][]>([]);
  const [editing, setEditing] = useState<string | null>(null);
  const [formKey, setFormKey] = useState(0);
  const [formDirty, setFormDirty] = useState(false);
  const [preview, setPreview] = useState<{
    value: BackendChangeProposalCandidate;
    body: string;
  } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [requestConflict, setConflict] = useState(false);
  const conflict = requestConflict || attempt?.phase === "conflict";
  const [reconciled, setReconciled] = useState<BackendChangeProposalDetail | null>(null);
  const [restoreId, setRestoreId] = useState("");
  const [confirmRestore, setConfirmRestore] = useState(false);
  const epoch = useRef(0);
  const controller = useRef<AbortController | null>(null);
  const onDirtyRef = useRef(onDirty);
  useEffect(() => {
    onDirtyRef.current = onDirty;
  }, [onDirty]);
  const historical = base.revision.id !== base.proposal.currentDraftRevisionId;
  const terminal = base.proposal.status === "implemented" || base.proposal.status === "archived";
  const locked =
    terminal ||
    rebaseDirty ||
    analysisRecovery.blocked ||
    busy ||
    !!attempt ||
    conflict ||
    readOnly ||
    historical ||
    recovery.blocked;
  const dirty =
    rebaseDirty ||
    analysisRecovery.blocked ||
    commands.length > 0 ||
    !!attempt ||
    formDirty ||
    recovery.blocked;
  useEffect(() => {
    onDirtyRef.current?.(dirty);
    const leave = (event: BeforeUnloadEvent) => {
      if (dirty) {
        event.preventDefault();
        event.returnValue = "";
      }
    };
    window.addEventListener("beforeunload", leave);
    return () => window.removeEventListener("beforeunload", leave);
  }, [dirty]);
  useEffect(
    () => () => {
      epoch.current++;
      controller.current?.abort();
      onDirtyRef.current?.(false);
    },
    [],
  );
  function storeAttempt(value: ChangeAttempt | null) {
    return value ? recovery.persist(value) : recovery.clear();
  }
  useEffect(() => {
    const acceptRecovered = (event: Event) => {
      const accepted = (event as CustomEvent<{ attempt: AnalysisAttempt; result: unknown }>).detail;
      if (
        !accepted ||
        !["ready", "rebase", "implemented", "archive", "unarchive"].includes(
          accepted.attempt.kind,
        ) ||
        accepted.attempt.owner.proposalId !== base.proposal.id ||
        !("proposalRevisionId" in accepted.attempt.input) ||
        accepted.attempt.input.proposalRevisionId !== base.revision.id
      )
        return;
      const result = accepted.result as BackendChangeProposalApplyResult;
      const next = {
        ...base,
        proposal: result.proposal,
        revision: result.revision,
        history:
          accepted.attempt.kind === "rebase"
            ? [
                {
                  id: result.revision.id,
                  parentRevisionId: result.revision.parentRevisionId,
                  semanticHash: result.revision.semanticHash,
                  author: result.revision.author,
                  summary: result.revision.summary,
                  createdAt: result.revision.createdAt,
                },
                ...base.history.filter((r) => r.id !== result.revision.id),
              ]
            : base.history,
      };
      setBase(next);
      setPreview(null);
      if (accepted.attempt.kind === "rebase") setInputEpoch((value) => value + 1);
      onSaved(next);
    };
    window.addEventListener("backend-analysis-accepted", acceptRecovered);
    return () => window.removeEventListener("backend-analysis-accepted", acceptRecovered);
  }, [base, onSaved]);
  const identities = useQuery({
    queryKey: [
      "backend-change-identities",
      projectId,
      base.proposal.id,
      base.revision.id,
      base.revision.semanticHash,
    ],
    retry: false,
    staleTime: Infinity,
    queryFn: ({ signal }) => readChangeIdentities(projectId, base, signal),
  });
  function localChange(next: BackendChangeProposalCommand[]) {
    setInputEpoch((value) => value + 1);
    epoch.current++;
    controller.current?.abort();
    setUndo((previous) => [...previous.slice(-49), structuredClone(commands)]);
    setCommands(next);
    setPreview(null);
    setError("");
    setNotice("");
  }
  function requestStart() {
    const id = ++epoch.current;
    controller.current?.abort();
    controller.current = new AbortController();
    setBusy(true);
    setError("");
    return { id, signal: controller.current.signal };
  }
  function requestError(failure: unknown) {
    setError(changeMessage(failure));
    if (failure instanceof ApiFailure && failure.status === 409) {
      setConflict(true);
      setPreview(null);
      if (attempt) storeAttempt({ ...attempt, phase: "conflict" });
    }
  }
  const previewInput = () => ({
    expectedVersion: base.proposal.version,
    proposalRevisionId: base.revision.id,
    commands: structuredClone(commands),
  });
  async function runPreview() {
    if (locked || commands.length === 0) return;
    const token = requestStart();
    const input = previewInput();
    setPreview(null);
    try {
      const response = await previewBackendChangeProposalCommands(
        projectId,
        base.proposal.id,
        input,
        { signal: token.signal },
      );
      if (token.id !== epoch.current) return;
      if (response.status !== 200) throw new Error("Предпросмотр не подтверждён");
      const value = response.data;
      if (
        value.proposalId !== base.proposal.id ||
        value.proposalRevisionId !== base.revision.id ||
        value.expectedVersion !== base.proposal.version ||
        value.baseRevisionId !== base.revision.baseRevisionId ||
        value.baseSemanticHash !== base.revision.baseSemanticHash ||
        value.draftHash !== base.revision.semanticHash
      )
        throw new Error("Предпросмотр относится к другой ревизии");
      setPreview({ value, body: JSON.stringify(input) });
    } catch (failure) {
      if (token.id === epoch.current) requestError(failure);
    } finally {
      if (token.id === epoch.current) setBusy(false);
    }
  }
  function accept(result: BackendChangeProposalApplyResult) {
    if (
      result.proposal.projectId !== projectId ||
      result.proposal.id !== base.proposal.id ||
      result.revision.proposalId !== base.proposal.id ||
      result.revision.baseRevisionId !== base.revision.baseRevisionId ||
      result.revision.baseSemanticHash !== base.revision.baseSemanticHash
    )
      throw new Error("Сохранение вернуло другую базовую ревизию");
    const next = {
      ...base,
      proposal: result.proposal,
      revision: result.revision,
      history: [
        {
          id: result.revision.id,
          parentRevisionId: result.revision.parentRevisionId,
          semanticHash: result.revision.semanticHash,
          author: result.revision.author,
          summary: result.revision.summary,
          createdAt: result.revision.createdAt,
        },
        ...base.history.filter((item) => item.id !== result.revision.id),
      ],
    };
    setInputEpoch((value) => value + 1);
    setBase(next);
    setCommands([]);
    setUndo([]);
    setPreview(null);
    setEditing(null);
    setFormDirty(false);
    setFormKey((value) => value + 1);
    setRestoreId("");
    setConfirmRestore(false);
    recovery.clear(true);
    setNotice("Предложение сохранено. Базовая ревизия не изменилась.");
    onSaved(next);
  }
  async function mutate(kind: "apply" | "restore") {
    if (
      analysisRecovery.busy ||
      analysisRecovery.attempt ||
      analysisRecovery.error ||
      rebaseDirty ||
      busy ||
      conflict ||
      readOnly ||
      recovery.cleanupPending ||
      (!attempt && recovery.blocked)
    )
      return;
    if (attempt && attempt.kind !== kind) return;
    let next = attempt;
    try {
      if (!next) {
        if (historical) return;
        if (kind === "apply") {
          if (!preview?.value.candidateHash || preview.body !== JSON.stringify(previewInput()))
            return;
          next = makeChangeAttempt("apply", {
            ...previewInput(),
            candidateHash: preview.value.candidateHash,
            idempotencyKey: crypto.randomUUID(),
          });
        } else {
          if (!restoreId || !confirmRestore || commands.length || formDirty) return;
          next = makeChangeAttempt("restore", {
            expectedVersion: base.proposal.version,
            proposalRevisionId: base.revision.id,
            restoreRevisionId: restoreId,
            idempotencyKey: crypto.randomUUID(),
          });
        }
      }
    } catch (failure) {
      setError(changeMessage(failure));
      return;
    }
    const pending = next;
    if (!storeAttempt(pending)) return;
    const token = requestStart();
    try {
      const response =
        pending.kind === "apply"
          ? await applyBackendChangeProposalCommands(projectId, base.proposal.id, pending.input, {
              signal: token.signal,
            })
          : await restoreBackendChangeProposal(projectId, base.proposal.id, pending.input, {
              signal: token.signal,
            });
      if (token.id !== epoch.current) return;
      if (response.status !== 200) throw new Error("Ответ на сохранение не подтверждён");
      accept(response.data);
    } catch (failure) {
      if (token.id !== epoch.current) return;
      setError(changeMessage(failure));
      if (failure instanceof ApiFailure && failure.status === 409) {
        setConflict(true);
        setPreview(null);
        storeAttempt({ ...pending, phase: "conflict" });
      } else if (!changeUncertain(failure)) {
        recovery.clear(true);
        setPreview(null);
      }
    } finally {
      if (token.id === epoch.current) setBusy(false);
    }
  }
  async function reload() {
    const token = requestStart();
    try {
      const fresh = await readChangeProposal(projectId, base.proposal.id, undefined, token.signal);
      if (token.id === epoch.current) setReconciled(fresh);
    } catch (failure) {
      if (token.id === epoch.current) setError(changeMessage(failure));
    } finally {
      if (token.id === epoch.current) setBusy(false);
    }
  }
  const editIndex = commands.findIndex((command) => command.commandId === editing);
  const availableIdentities = changePendingIdentities(
    identities.data ?? [],
    editIndex >= 0 ? commands.slice(0, editIndex) : commands,
  );
  return (
    <Stack gap="md">
      <Group>
        <Title order={4}>{base.proposal.name}</Title>
        <Badge>{historical ? "Исторический черновик" : "Текущий черновик"}</Badge>
        <Badge variant="outline">Схема {base.revision.baseSchemaVersion}</Badge>
      </Group>
      <Text size="xs" style={wrap}>
        Ревизия предложения: {base.revision.id} · База: {base.revision.baseRevisionId} · Версия:{" "}
        {base.proposal.version}
      </Text>
      {base.baseOutdated && (
        <Alert color="yellow">
          Исходная модель обновилась. Предложение сохраняет выбранную базовую ревизию.
        </Alert>
      )}
      <BackendChangeRecoveryNotice
        recovery={recovery}
        busy={busy}
        onReload={() => {
          const recovered = recovery.reload();
          if (recovered?.kind === "apply") {
            setCommands(recovered.input.commands);
            setPreview(null);
          }
        }}
      />
      {error && (
        <Alert color="red" role="alert">
          {error}
        </Alert>
      )}
      {notice && (
        <output>
          <Alert color="green">{notice}</Alert>
        </output>
      )}
      {attempt && !conflict && !recovery.cleanupPending && (
        <Alert color="yellow">
          <Stack gap="xs">
            <Text>
              {recovery.unsent
                ? "Запрос ещё не отправлен: сначала нужно сохранить восстановление."
                : "Результат запроса пока неизвестен. Сохранены исходное тело и ключ повторения."}
            </Text>
            <Button
              disabled={busy}
              onClick={() => void mutate(attempt.kind === "restore" ? "restore" : "apply")}
            >
              Повторить тот же запрос
            </Button>
          </Stack>
        </Alert>
      )}
      {conflict && (
        <Alert color="orange">
          <Stack gap="xs">
            <Text>
              Конфликт 409. Старый запрос заблокирован. Загрузите текущий черновик и сверяйте
              локальные команды перед новым предпросмотром.
            </Text>
            <Button disabled={busy} onClick={() => void reload()}>
              Загрузить для сверки
            </Button>
            {reconciled && (
              <>
                <Text size="sm" style={wrap}>
                  Новый черновик: {reconciled.revision.id} · версия {reconciled.proposal.version}.
                  Локальные идентификаторы пока сохранены.
                </Text>
                <Button
                  onClick={() => {
                    if (!recovery.clear()) return;
                    setBase(reconciled);
                    onSaved(reconciled);
                    setReconciled(null);
                    setConflict(false);
                    setPreview(null);
                    setUndo([]);
                  }}
                >
                  Продолжить с локальными командами
                </Button>
                <Button
                  variant="outline"
                  onClick={() => {
                    if (!recovery.clear()) return;
                    setBase(reconciled);
                    onSaved(reconciled);
                    setCommands(
                      commands.map((command) => ({ ...command, commandId: crypto.randomUUID() })),
                    );
                    setReconciled(null);
                    setConflict(false);
                    setPreview(null);
                    setUndo([]);
                  }}
                >
                  Перенести как новые правки
                </Button>
              </>
            )}
          </Stack>
        </Alert>
      )}
      {!historical && !readOnly && !terminal && (
        <Paper withBorder p="md">
          <fieldset
            disabled={locked || (commands.length >= 100 && editing === null)}
            style={{ border: 0, padding: 0, minWidth: 0 }}
          >
            <legend>Локальная команда</legend>
            <BackendChangeCommandForms
              key={`${base.revision.id}:${editing ?? formKey}`}
              baseSchemaVersion={base.revision.baseSchemaVersion}
              initial={editIndex >= 0 ? commands[editIndex] : undefined}
              identities={availableIdentities}
              implementationRefs={
                handoff?.projectId === projectId &&
                ("revisionId" in handoff.target
                  ? handoff.target.revisionId === base.revision.baseRevisionId
                  : handoff.target.changeProposal.proposalId === base.proposal.id &&
                    handoff.target.changeProposal.proposalRevisionId === base.revision.id)
                  ? handoff.refs
                  : undefined
              }

              criteria={
                (editIndex >= 0 ? commands.slice(0, editIndex) : commands).findLast(
                  (command) => command.type === "set_criteria",
                )?.criteria ?? base.revision.criteria
              }
              onAdd={(command) => {
                localChange(
                  editIndex >= 0
                    ? commands.map((item, index) => (index === editIndex ? command : item))
                    : [...commands, command],
                );
                setEditing(null);
                setFormKey((value) => value + 1);
              }}
              onDirtyChange={(value) => {
                setFormDirty(value);
                if (value) {
                  setPreview(null);
                  setInputEpoch((epoch) => epoch + 1);
                }
              }}
              onCancel={
                editing
                  ? () => {
                      setEditing(null);
                      setFormDirty(false);
                    }
                  : undefined
              }
            />
          </fieldset>
        </Paper>
      )}
      {formDirty && !locked && (
        <Group>
          <Text size="sm">Добавьте текущую команду или отмените ввод перед предпросмотром.</Text>
          <Button
            variant="subtle"
            onClick={() => {
              setEditing(null);
              setFormKey((value) => value + 1);
              setFormDirty(false);
            }}
          >
            Отменить ввод команды
          </Button>
        </Group>
      )}
      {identities.isError && (
        <Alert color="yellow">
          Идентичности не загружены: {changeMessage(identities.error)}
          <Button variant="subtle" onClick={() => void identities.refetch()}>
            Повторить идентичности
          </Button>
        </Alert>
      )}
      <Stack gap="xs">
        <Group>
          <Title order={5}>Порядок локальных команд ({commands.length}/100)</Title>
          <Button
            variant="subtle"
            disabled={locked || undo.length === 0}
            onClick={() => {
              setInputEpoch((value) => value + 1);
              const previous = undo.at(-1)!;
              setCommands(previous);
              setUndo(undo.slice(0, -1));
              setPreview(null);
              setEditing(null);
            }}
          >
            Отменить локальное действие
          </Button>
        </Group>
        <ol>
          {commands.map((command, index) => (
            <li key={command.commandId}>
              <Group py="xs">
                <Text>
                  {changeCommandLabels[command.type]} · {command.reason}
                </Text>
                <Button
                  variant="subtle"
                  size="xs"
                  disabled={locked}
                  onClick={() => setEditing(command.commandId)}
                >
                  Править команду {index + 1}
                </Button>
                <Button
                  aria-label={`Команда ${index + 1} вверх`}
                  variant="subtle"
                  size="xs"
                  disabled={locked || index === 0}
                  onClick={() => {
                    const next = [...commands];
                    [next[index - 1], next[index]] = [next[index]!, next[index - 1]!];
                    localChange(next);
                  }}
                >
                  ↑
                </Button>
                <Button
                  aria-label={`Команда ${index + 1} вниз`}
                  variant="subtle"
                  size="xs"
                  disabled={locked || index === commands.length - 1}
                  onClick={() => {
                    const next = [...commands];
                    [next[index + 1], next[index]] = [next[index]!, next[index + 1]!];
                    localChange(next);
                  }}
                >
                  ↓
                </Button>
                <Button
                  variant="subtle"
                  size="xs"
                  color="red"
                  disabled={locked}
                  onClick={() => localChange(commands.filter((_, at) => at !== index))}
                >
                  Убрать команду {index + 1}
                </Button>
              </Group>
            </li>
          ))}
        </ol>
      </Stack>
      {!readOnly && !historical && !terminal && (
        <Group>
          <Button
            variant="light"
            disabled={locked || formDirty || commands.length === 0}
            onClick={() => void runPreview()}
          >
            Проверить изменения
          </Button>
          <Button
            disabled={
              locked ||
              formDirty ||
              !preview?.value.candidateHash ||
              preview.body !== JSON.stringify(previewInput())
            }
            onClick={() => void mutate("apply")}
          >
            Сохранить предложение
          </Button>
        </Group>
      )}
      {preview && (
        <Stack gap="xs">
          <Title order={5}>Предпросмотр</Title>
          <Text>
            {preview.value.candidateHash
              ? "Граф готов к сохранению"
              : "Исправьте диагностику в локальных командах"}
          </Text>
          {preview.value.diagnostics.map((diagnostic, index) => (
            <Alert key={index} color="yellow">
              {diagnostic.message}
            </Alert>
          ))}
          <Text size="sm">
            Проверки описывают желаемое состояние; выполнение кода не подтверждено.
          </Text>
        </Stack>
      )}
      {!readOnly && !historical && !terminal && (
        <fieldset
          disabled={locked || formDirty || commands.length > 0}
          style={{ border: "1px solid var(--mantine-color-default-border)", padding: 12 }}
        >
          <legend>Восстановить историю на той же базе</legend>
          <Stack gap="sm">
            <NativeSelect
              label="Ревизия для восстановления"
              value={restoreId}
              onChange={(event) => {
                setRestoreId(event.currentTarget.value);
                setConfirmRestore(false);
              }}
              data={[
                { value: "", label: "Выберите историческую ревизию" },
                ...base.history
                  .filter((item) => item.id !== base.revision.id)
                  .map((item) => ({
                    value: item.id,
                    label: `${item.createdAt} · ${item.summary} · ${item.id}`,
                  })),
              ]}
            />
            <Checkbox
              label="Создать новый черновик с выбранным состоянием"
              checked={confirmRestore}
              onChange={(event) => setConfirmRestore(event.currentTarget.checked)}
            />
            <Button disabled={!restoreId || !confirmRestore} onClick={() => void mutate("restore")}>
              Восстановить выбранную ревизию
            </Button>
          </Stack>
        </fieldset>
      )}
      <details>
        <summary>Критерии выбранного черновика ({base.revision.criteria.length})</summary>
        <ol>
          {base.revision.criteria.map((criterion) => (
            <li key={criterion.key}>
              <Text size="sm">
                {criterion.key} · {criterion.kind} ·{" "}
                {criterion.required ? "обязательный" : "необязательный"}: {criterion.description}
              </Text>
            </li>
          ))}
        </ol>
      </details>
      <BackendChangeLifecycle
        onReport={(report) => setSelectedReport({ ...report, requestId: crypto.randomUUID() })}
        projectId={projectId}
        proposal={base}
        disabled={
          readOnly ||
          historical ||
          busy ||
          commands.length > 0 ||
          formDirty ||
          rebaseDirty ||
          recovery.blocked
        }
        onSaved={(value) => {
          setBase(value);
          onSaved(value);
        }}
      />
      <BackendAnalysisJobs
        key={selectedReport?.requestId ?? "analysis"}
        selectedReport={selectedReport}
        projectId={projectId}
        sourceRevisionId={base.revision.baseRevisionId}
        proposal={readOnly || historical ? undefined : base}
        dirty={commands.length > 0 || formDirty || rebaseDirty}
        inputEpoch={inputEpoch}
        disabled={formDirty || (commands.length > 0 && !preview?.value.candidateHash)}
        target={
          commands.length && preview?.value.candidateHash
            ? {
                commandPreview: {
                  changeProposal: {
                    proposalId: base.proposal.id,
                    proposalRevisionId: base.revision.id,
                  },
                  expectedVersion: base.proposal.version,
                  commands,
                  candidateHash: preview.value.candidateHash,
                },
              }
            : {
                changeProposal: {
                  proposalId: base.proposal.id,
                  proposalRevisionId: base.revision.id,
                },
              }
        }
        onSaved={(value) => {
          setBase(value);
          onSaved(value);
        }}
      />
      {!readOnly && !historical && !terminal && (
        <BackendChangeRebase
          key={base.revision.id}
          projectId={projectId}
          detail={base}
          identities={identities.data}
          disabled={busy || commands.length > 0 || formDirty || !!attempt || recovery.blocked}
          onDirty={setRebaseDirty}
          onSaved={(value) => {
            setBase(value);
            setInputEpoch((epoch) => epoch + 1);
            setPreview(null);
            onSaved(value);
          }}
        />
      )}
      <BackendEffectiveViews
        projectId={projectId}
        target={{
          changeProposal: { proposalId: base.proposal.id, proposalRevisionId: base.revision.id },
        }}
      />
    </Stack>
  );
}

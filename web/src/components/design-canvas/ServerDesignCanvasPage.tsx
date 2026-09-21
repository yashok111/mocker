import { useCallback, useEffect, useRef, useState, type ReactElement } from "react";
import { Alert, Button, Group, Loader, Text } from "@mantine/core";
import { IconCheck, IconAlertCircle, IconPlayerPlay } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ApiFailure } from "@/api/client";
import { describeApiFailureDetailed } from "@/api/errors";
import { getListApiDesignsQueryKey } from "@/api/generated/api-designs/api-designs";
import { useApplyDesignScenarioCommands } from "@/api/generated/design-scenarios/design-scenarios";
import { DesignCanvasEditor } from "./DesignCanvasPage";
import { ScenarioContractsPanel } from "./ScenarioContractsPanel";
import { ScenarioHistoryPanel } from "./ScenarioHistoryPanel";
import { ScenarioValidationAction } from "./ScenarioValidationAction";
import { ScenarioExecutionPanel } from "./ScenarioExecutionPanel";
import {
  runScenario,
  listScenarioRuns,
  getScenarioRun,
  cancelScenarioRun,
} from "./canvasExecutionApi";
import {
  designScenarioKeys,
  asDesignScenarioDetail,
  useCreateDesignScenario,
  useGetDesignScenario,
  useSaveDesignScenarioDraft,
  type DesignScenarioDetail,
} from "./designScenarioApi";
import { useCanvasDraft } from "./useCanvasDraft";
import type { CanvasDocument } from "./types";

const POLL_MS = 5_000;
const AUTOSAVE_DELAY_MS = 800;

export function ServerDesignCanvasPage({ id }: { id: number }): ReactElement {
  const query = useGetDesignScenario(id, {
    refetchInterval: POLL_MS,
    refetchOnWindowFocus: true,
  });
  const detail = query.data?.status === 200 ? query.data.data : null;

  if (query.isPending || detail === null) {
    if (query.isError) {
      return (
        <Alert color="red" role="alert">
          {describeApiFailureDetailed(query.error)}
        </Alert>
      );
    }
    return <Loader aria-label="Загружаем сценарий" />;
  }

  return (
    <ServerCanvasEditor
      key={id}
      id={id}
      detail={detail}
      onReload={async () => {
        const result = await query.refetch();
        if (result.isError) throw result.error;
        return result.data?.status === 200 ? result.data.data : null;
      }}
    />
  );
}

function ServerCanvasEditor({
  id,
  detail,
  onReload,
}: {
  id: number;
  detail: DesignScenarioDetail;
  onReload: () => Promise<DesignScenarioDetail | null>;
}): ReactElement {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [stored] = useState(() => readStoredDraft(id));
  const [saveInFlight, setSaveInFlight] = useState(false);
  const [saveUnconfirmed, setSaveUnconfirmed] = useState(stored?.pendingSave ?? false);
  const [reloading, setReloading] = useState(false);
  const [contractsOpened, setContractsOpened] = useState(false);
  const writeInFlight = useRef(false);
  const initialDocument = stored?.document ?? detail.draft.document;
  const initialFormDrafts = stored?.formDrafts ?? detail.draft.formDrafts.all ?? "{}";
  const initialBaseDocument = stored?.baseDocument ?? detail.draft.document;
  const initialBaseFormDrafts = stored?.baseFormDrafts ?? detail.draft.formDrafts.all ?? "{}";
  const initialFormDraftEnvelope = stored?.formDraftEnvelope ?? detail.draft.formDrafts;
  const draft = useCanvasDraft(
    {
      document: initialDocument,
      formDrafts: { all: initialFormDrafts },
      baseline: {
        document: initialBaseDocument,
        formDrafts: { all: initialBaseFormDrafts },
      },
      saved: true,
      error: "",
    },
    { pendingWrite: saveUnconfirmed, shortcutsEnabled: !contractsOpened },
  );
  const {
    dirty: draftDirty,
    document: draftDocument,
    fingerprint: draftFingerprint,
    serializeFormDrafts,
    setError: setDraftError,
  } = draft;
  const [baseDocument, setBaseDocument] = useState(initialBaseDocument);
  const [baseFormDrafts, setBaseFormDrafts] = useState(initialBaseFormDrafts);
  const [formDraftEnvelope, setFormDraftEnvelope] = useState(initialFormDraftEnvelope);
  const [baseVersion, setBaseVersion] = useState(stored?.baseVersion ?? detail.scenario.version);
  const [baseRevisionId, setBaseRevisionId] = useState(
    stored?.baseRevisionId ?? detail.scenario.draftRevisionId,
  );
  const [externalDetail, setExternalDetail] = useState<DesignScenarioDetail | null>(null);
  const [conflict, setConflict] = useState(stored?.pendingSave ?? false);
  const [historyOpened, setHistoryOpened] = useState(false);
  const [executionOpened, setExecutionOpened] = useState(false);
  const [diagnostics, setDiagnostics] = useState(detail.diagnostics);
  const [createdCopyID, setCreatedCopyID] = useState<number | null>(null);
  const submittedDocument = useRef<CanvasDocument | null>(null);
  const submittedFormDrafts = useRef("");
  const submittedCopyFingerprint = useRef("");

  useEffect(() => {
    const key = storageKey(id);
    try {
      if (!draftDirty && !saveUnconfirmed && !conflict && externalDetail === null) {
        localStorage.removeItem(key);
        return;
      }
      const serializedDrafts = serializeFormDrafts();
      const storedDraft: StoredScenarioDraft = {
        document: draftDocument,
        baseDocument,
        formDrafts: serializedDrafts,
        baseFormDrafts,
        formDraftEnvelope: { ...formDraftEnvelope, all: serializedDrafts },
        baseVersion,
        baseRevisionId,
        pendingSave: saveUnconfirmed,
        savedAt: Date.now(),
      };
      localStorage.setItem(key, JSON.stringify(storedDraft));
    } catch {
      setDraftError(
        "Не удалось сохранить аварийную копию в браузере. Черновик остаётся открыт в этой вкладке.",
      );
    }
  }, [
    baseDocument,
    baseFormDrafts,
    baseRevisionId,
    baseVersion,
    conflict,
    draftDirty,
    draftDocument,
    draftFingerprint,
    externalDetail,
    formDraftEnvelope,
    id,
    serializeFormDrafts,
    saveUnconfirmed,
    setDraftError,
  ]);

  const save = useSaveDesignScenarioDraft({
    onSuccess: (response) => {
      const submitted = submittedDocument.current;
      const serverDrafts = response.data.draft.formDrafts.all ?? "{}";
      if (submitted !== null) {
        draft.acknowledgeServerSave(
          submitted,
          response.data.draft.document,
          submittedFormDrafts.current,
          serverDrafts,
        );
      }
      setBaseDocument(response.data.draft.document);
      setBaseFormDrafts(response.data.draft.formDrafts.all ?? "{}");
      setFormDraftEnvelope(response.data.draft.formDrafts);
      setBaseVersion(response.data.scenario.version);
      setBaseRevisionId(response.data.scenario.draftRevisionId);
      setDiagnostics(response.data.diagnostics);
      setExternalDetail(null);
      setConflict(false);
      setSaveUnconfirmed(false);
      queryClient.setQueryData(designScenarioKeys.detail(id), response);
      void queryClient.invalidateQueries({ queryKey: designScenarioKeys.all });
      void queryClient.invalidateQueries({ queryKey: getListApiDesignsQueryKey() });
    },
    onError: (error) => {
      if (error instanceof ApiFailure && error.code === "design_scenario_conflict") {
        setConflict(true);
      }
    },
    onSettled: () => {
      writeInFlight.current = false;
      setSaveInFlight(false);
    },
  });
  const createCopy = useCreateDesignScenario({
    onSuccess: (response) => {
      void queryClient.invalidateQueries({ queryKey: designScenarioKeys.all });
      if (draft.fingerprint !== submittedCopyFingerprint.current) {
        setCreatedCopyID(response.data.scenario.id);
        setExternalDetail(null);
        setConflict(false);
        return;
      }
      draft.markSavedFingerprint(submittedCopyFingerprint.current);
      void navigate({
        to: "/design-scenarios/$id" as never,
        params: { id: response.data.scenario.id } as never,
        ignoreBlocker: true,
      });
    },
    onSettled: () => {
      writeInFlight.current = false;
    },
  });
  const commands = useApplyDesignScenarioCommands({
    mutation: {
      onSuccess: (response) => {
        if (response.status !== 200) return;
        const next = asDesignScenarioDetail(response.data);
        draft.replaceFromServer(next.draft.document, next.draft.formDrafts.all ?? "{}");
        setBaseDocument(next.draft.document);
        setBaseFormDrafts(next.draft.formDrafts.all ?? "{}");
        setFormDraftEnvelope(next.draft.formDrafts);
        setBaseVersion(next.scenario.version);
        setBaseRevisionId(next.scenario.draftRevisionId);
        setDiagnostics(next.diagnostics);
        setExternalDetail(null);
        setConflict(false);
        queryClient.setQueryData(designScenarioKeys.detail(id), response);
        void queryClient.invalidateQueries({ queryKey: designScenarioKeys.all });
        void queryClient.invalidateQueries({ queryKey: getListApiDesignsQueryKey() });
      },
      onError: (error) => {
        if (error instanceof ApiFailure && error.code === "design_scenario_conflict") {
          setConflict(true);
        }
      },
      onSettled: () => {
        writeInFlight.current = false;
      },
    },
  });
  const { mutate: saveDraft } = save;
  const savingBlocked =
    conflict ||
    externalDetail !== null ||
    createdCopyID !== null ||
    commands.isPending ||
    createCopy.isPending ||
    reloading ||
    historyOpened ||
    detail.scenario.version > baseVersion;

  const handleSave = useCallback((): void => {
    if (writeInFlight.current || savingBlocked) return;
    writeInFlight.current = true;
    setSaveInFlight(true);
    setSaveUnconfirmed(true);
    submittedDocument.current = draftDocument;
    submittedFormDrafts.current = serializeFormDrafts();
    saveDraft({
      id,
      data: {
        expectedVersion: baseVersion,
        document: draftDocument,
        formDrafts: { ...formDraftEnvelope, all: submittedFormDrafts.current },
      },
    });
  }, [
    baseVersion,
    draftDocument,
    formDraftEnvelope,
    id,
    saveDraft,
    savingBlocked,
    serializeFormDrafts,
  ]);

  useEffect(() => {
    if (!draftDirty || saveInFlight || savingBlocked || save.isError) return;
    const timer = window.setTimeout(handleSave, AUTOSAVE_DELAY_MS);
    return () => window.clearTimeout(timer);
  }, [draftDirty, draftFingerprint, handleSave, saveInFlight, savingBlocked, save.isError]);

  useEffect(() => {
    if (
      detail.scenario.version <= baseVersion ||
      saveInFlight ||
      commands.isPending ||
      createCopy.isPending ||
      reloading ||
      historyOpened
    )
      return;
    if (draft.dirty || draft.pendingForms || conflict || save.isError) {
      setExternalDetail(detail);
      return;
    }
    draft.replaceFromServer(detail.draft.document, detail.draft.formDrafts.all ?? "{}");
    setBaseDocument(detail.draft.document);
    setBaseFormDrafts(detail.draft.formDrafts.all ?? "{}");
    setFormDraftEnvelope(detail.draft.formDrafts);
    setBaseVersion(detail.scenario.version);
    setBaseRevisionId(detail.scenario.draftRevisionId);
    setDiagnostics(detail.diagnostics);
    setExternalDetail(null);
    setConflict(false);
  }, [
    baseVersion,
    detail,
    draft,
    saveInFlight,
    commands.isPending,
    createCopy.isPending,
    reloading,
    historyOpened,
    conflict,
    save.isError,
  ]);

  function saveAsCopy(): void {
    if (writeInFlight.current) return;
    writeInFlight.current = true;
    submittedCopyFingerprint.current = draft.fingerprint;
    createCopy.mutate({
      document: detachLinkedContracts(draft.document),
      formDrafts: { ...formDraftEnvelope, all: draft.serializeFormDrafts() },
      summary: "Сохранено как независимая копия после конфликта",
    });
  }

  async function forceReload(): Promise<void> {
    if (writeInFlight.current) return;
    writeInFlight.current = true;
    setReloading(true);
    try {
      const next = await onReload();
      if (next === null) return;
      draft.replaceFromServer(next.draft.document, next.draft.formDrafts.all ?? "{}");
      setBaseDocument(next.draft.document);
      setBaseFormDrafts(next.draft.formDrafts.all ?? "{}");
      setFormDraftEnvelope(next.draft.formDrafts);
      setBaseVersion(next.scenario.version);
      setBaseRevisionId(next.scenario.draftRevisionId);
      setDiagnostics(next.diagnostics);
      setExternalDetail(null);
      setConflict(false);
      setSaveUnconfirmed(false);
      save.reset();
    } catch (error) {
      draft.setError(describeApiFailureDetailed(error));
    } finally {
      writeInFlight.current = false;
      setReloading(false);
    }
  }

  const notice =
    externalDetail !== null || conflict || save.isError || createCopy.isError || createdCopyID ? (
      <Alert color={conflict ? "red" : "yellow"} role="alert">
        <Group justify="space-between" align="center">
          <Text size="sm">
            {conflict
              ? stored?.pendingSave
                ? "Предыдущее сохранение было прервано. Локальные правки восстановлены; выберите серверную версию или сохраните копию."
                : "Сценарий или связанный API изменился на сервере. Ваши локальные правки сохранены."
              : externalDetail !== null
                ? `На сервере появилась версия ${externalDetail.scenario.version}. Локальные правки не заменены.`
                : createCopy.isError
                  ? describeApiFailureDetailed(createCopy.error)
                  : createdCopyID !== null
                    ? "Новый сценарий создан, а более поздние правки остались в этом редакторе."
                    : describeApiFailureDetailed(save.error)}
          </Text>
          {conflict ? (
            <Group gap="xs">
              <Button
                size="xs"
                variant="default"
                disabled={createCopy.isPending}
                loading={reloading}
                onClick={() => void forceReload()}
              >
                Загрузить серверный сценарий
              </Button>
              <Button
                size="xs"
                disabled={reloading}
                loading={createCopy.isPending}
                onClick={saveAsCopy}
              >
                Сохранить как новый сценарий
              </Button>
            </Group>
          ) : externalDetail !== null ? (
            <Group gap="xs">
              <Button
                size="xs"
                color="yellow"
                disabled={createCopy.isPending}
                onClick={() => {
                  draft.replaceFromServer(
                    externalDetail.draft.document,
                    externalDetail.draft.formDrafts.all ?? "{}",
                  );
                  setBaseDocument(externalDetail.draft.document);
                  setBaseFormDrafts(externalDetail.draft.formDrafts.all ?? "{}");
                  setFormDraftEnvelope(externalDetail.draft.formDrafts);
                  setBaseVersion(externalDetail.scenario.version);
                  setBaseRevisionId(externalDetail.scenario.draftRevisionId);
                  setDiagnostics(externalDetail.diagnostics);
                  setExternalDetail(null);
                  setConflict(false);
                  setSaveUnconfirmed(false);
                  save.reset();
                }}
              >
                Загрузить серверную версию
              </Button>
              <Button size="xs" loading={createCopy.isPending} onClick={saveAsCopy}>
                Сохранить как новый сценарий
              </Button>
            </Group>
          ) : createdCopyID !== null ? (
            <Link to="/design-scenarios/$id" params={{ id: createdCopyID }} target="_blank">
              Открыть созданный сценарий
            </Link>
          ) : null}
        </Group>
      </Alert>
    ) : undefined;

  return (
    <>
      <DesignCanvasEditor
        draft={draft}
        persistence={{
          automatic: true,
          status: (
            <Group component="span" gap={6} wrap="nowrap">
              {conflict || externalDetail !== null || save.isError ? (
                <IconAlertCircle size={16} />
              ) : saveInFlight || draft.dirty ? (
                <Loader size={14} />
              ) : (
                <IconCheck size={16} />
              )}
              <span>
                {conflict || externalDetail !== null
                  ? "Конфликт изменений"
                  : save.isError
                    ? "Не удалось сохранить"
                    : savingBlocked && draft.dirty
                      ? "Автосохранение приостановлено"
                      : saveInFlight || draft.dirty
                        ? "Сохраняется…"
                        : "Сохранено"}
              </span>
              {save.isError && !savingBlocked ? (
                <Button
                  size="compact-xs"
                  variant="subtle"
                  aria-label="Повторить сохранение"
                  onClick={handleSave}
                >
                  Повторить
                </Button>
              ) : null}
            </Group>
          ),
          actions: (
            <Group gap="xs">
              <Button
                size="sm"
                leftSection={<IconPlayerPlay size={16} />}
                onClick={() => setExecutionOpened(true)}
              >
                Запуск
              </Button>
              <ScenarioValidationAction
                id={id}
                document={draft.document}
                disabled={draft.pendingForms}
              />
              <Button size="sm" variant="default" onClick={() => setContractsOpened(true)}>
                Контракты API
              </Button>
              <Button
                size="sm"
                variant="default"
                disabled={
                  draft.dirty ||
                  saveInFlight ||
                  commands.isPending ||
                  createCopy.isPending ||
                  reloading
                }
                onClick={() => setHistoryOpened(true)}
              >
                История
              </Button>
            </Group>
          ),
          notice: (
            <>
              {notice}
              {diagnostics.length > 0 ? (
                <Alert color="yellow">
                  {diagnostics.map((diagnostic) => (
                    <Text key={`${diagnostic.pointer}:${diagnostic.message}`} size="sm">
                      {diagnostic.message}
                    </Text>
                  ))}
                </Alert>
              ) : null}
            </>
          ),
        }}
      />
      <ScenarioExecutionPanel
        opened={executionOpened}
        onClose={() => setExecutionOpened(false)}
        document={draft.document}
        revisionId={baseRevisionId}
        version={baseVersion}
        disabled={
          draft.dirty ||
          draft.pendingForms ||
          saveInFlight ||
          saveUnconfirmed ||
          savingBlocked ||
          save.isError
        }
        onChangeDocument={draft.update}
        runScenario={(input, signal) => runScenario(id, input, signal)}
        listRuns={(signal) => listScenarioRuns(id, signal)}
        getRun={(runId, signal) => getScenarioRun(id, runId, signal)}
        cancelRun={(runId, signal) => cancelScenarioRun(id, runId, signal)}
      />
      <ScenarioContractsPanel
        opened={contractsOpened}
        onClose={() => setContractsOpened(false)}
        document={draft.document}
        version={baseVersion}
        updates={detail.contractUpdates}
        dirty={draft.dirty || conflict || externalDetail !== null || save.isError}
        pendingForms={draft.pendingForms}
        pending={commands.isPending || saveInFlight}
        error={commands.isError ? describeApiFailureDetailed(commands.error) : undefined}
        onCommand={(nextCommands, summary) => {
          if (
            writeInFlight.current ||
            draft.dirty ||
            conflict ||
            externalDetail !== null ||
            save.isError
          )
            return;
          writeInFlight.current = true;
          commands.mutate({
            id,
            data: { expectedVersion: baseVersion, commands: nextCommands, summary },
          });
        }}
        onCreateFromSchema={async (nextCommands, expectedVersion) => {
          if (writeInFlight.current || draft.dirty)
            throw new Error("Дождитесь завершения сохранения");
          writeInFlight.current = true;
          const response = await commands.mutateAsync({
            id,
            data: {
              expectedVersion,
              commands: nextCommands,
              summary: "Создан API-контракт по всей схеме",
            },
          });
          if (response.status !== 200) throw new Error("Не удалось создать API-проект");
        }}
      />
      <ScenarioHistoryPanel
        opened={historyOpened}
        onClose={() => setHistoryOpened(false)}
        scenarioId={id}
        currentRevisionId={baseRevisionId}
        baseVersion={baseVersion}
        revisions={detail.revisions}
        dirty={draft.dirty}
        onRestored={(restored) => {
          draft.replaceFromServer(restored.draft.document, restored.draft.formDrafts.all ?? "{}");
          setBaseDocument(restored.draft.document);
          setBaseFormDrafts(restored.draft.formDrafts.all ?? "{}");
          setFormDraftEnvelope(restored.draft.formDrafts);
          setBaseVersion(restored.scenario.version);
          setBaseRevisionId(restored.scenario.draftRevisionId);
          setExternalDetail(null);
          setConflict(false);
          setSaveUnconfirmed(false);
          save.reset();
          queryClient.setQueryData<{
            status: 200;
            data: DesignScenarioDetail;
            headers: Headers;
          }>(designScenarioKeys.detail(id), (cached) =>
            cached
              ? { ...cached, data: restored }
              : { status: 200, data: restored, headers: new Headers() },
          );
          void queryClient.invalidateQueries({ queryKey: designScenarioKeys.all });
        }}
      />
    </>
  );
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

interface StoredScenarioDraft {
  pendingSave?: boolean;
  document: CanvasDocument;
  baseDocument: CanvasDocument;
  formDrafts: string;
  baseFormDrafts: string;
  formDraftEnvelope: Record<string, string>;
  baseVersion: number;
  baseRevisionId: number;
  savedAt: number;
}

function storageKey(id: number): string {
  return `mocker:design-scenario:${id}:draft`;
}

function readStoredDraft(id: number): StoredScenarioDraft | null {
  try {
    const raw = localStorage.getItem(storageKey(id));
    if (raw === null) return null;
    const value: unknown = JSON.parse(raw);
    if (typeof value !== "object" || value === null || Array.isArray(value)) return null;
    const stored = value as Record<string, unknown>;
    if (
      !isCanvasDocument(stored.document) ||
      !isCanvasDocument(stored.baseDocument) ||
      typeof stored.formDrafts !== "string" ||
      typeof stored.baseFormDrafts !== "string" ||
      (stored.formDraftEnvelope !== undefined && !isStringRecord(stored.formDraftEnvelope)) ||
      (stored.pendingSave !== undefined && typeof stored.pendingSave !== "boolean") ||
      typeof stored.baseVersion !== "number" ||
      typeof stored.baseRevisionId !== "number" ||
      typeof stored.savedAt !== "number"
    )
      return null;
    return {
      ...(stored as unknown as StoredScenarioDraft),
      formDraftEnvelope:
        stored.formDraftEnvelope && typeof stored.formDraftEnvelope === "object"
          ? (stored.formDraftEnvelope as Record<string, string>)
          : { all: stored.formDrafts as string },
    };
  } catch {
    return null;
  }
}

function isCanvasDocument(value: unknown): value is CanvasDocument {
  if (!isRecord(value)) return false;
  return (
    value.formatVersion === 1 &&
    typeof value.title === "string" &&
    Array.isArray(value.participants) &&
    Array.isArray(value.messages) &&
    Array.isArray(value.fragments) &&
    Array.isArray(value.contracts)
  );
}

function isStringRecord(value: unknown): value is Record<string, string> {
  return isRecord(value) && Object.values(value).every((item) => typeof item === "string");
}

function detachLinkedContracts(document: CanvasDocument): CanvasDocument {
  return {
    ...document,
    contracts: document.contracts.map((contract) =>
      contract.mode === "linked" ? { ...contract, mode: "copy" } : contract,
    ),
  };
}

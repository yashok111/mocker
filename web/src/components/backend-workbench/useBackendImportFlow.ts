import { useCallback, useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  abortBackendImport,
  beginBackendImport,
  commitBackendImport,
  getBackendImport,
  getGetBackendProjectQueryKey,
  getListBackendProjectsQueryKey,
  previewBackendImport,
  putBackendImportBatch,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendBatchReceipt,
  BackendComposedImportSession,
  BackendImportCommand,
  BackendImportPreviewResponse,
  BackendImportStatusResponse,
} from "@/api/generated/schemas";
import { ApiFailure } from "@/api/client";
import {
  captureImportAttempt,
  captureImportBatch,
  importVersion,
  uncertainImportFailure,
  type ImportAttempt,
  type ImportCandidateTarget,
} from "./backendImportAttempts";
import {
  clearImportRecovery,
  loadImportRecovery,
  writeImportRecovery,
} from "./backendImportRecovery";

export type ImportAttemptState = {
  attempt: ImportAttempt;
  state: "pending" | "uncertain" | "rejected" | "unsent";
  error?: unknown;
};

export function useBackendImportFlow({
  projectId,
  initialSessionId,
  onCandidateChange,
}: {
  projectId: string;
  initialSessionId?: string;
  onCandidateChange?: (target: ImportCandidateTarget | null) => void;
}) {
  const client = useQueryClient();
  const callbacks = useRef({ onCandidateChange });
  useEffect(() => {
    callbacks.current = { onCandidateChange };
  }, [onCandidateChange]);
  const notifyCandidate = useCallback((target: ImportCandidateTarget | null) => {
    callbacks.current.onCandidateChange?.(target);
  }, []);
  const control = useRef({
    mounted: true,
    generation: 0,
    busy: false,
    recoveryReady: false,
    recoveryRaw: null as string | null,
    storageBlocked: false,
    scopeMismatch: false,
    sessionId: initialSessionId,
    session: null as BackendComposedImportSession | null,
    attempt: null as ImportAttemptState | null,
  });
  const [session, setSession] = useState<BackendComposedImportSession | null>(null);
  const [status, setStatus] = useState<BackendImportStatusResponse | null>(null);
  const [preview, setPreview] = useState<BackendImportPreviewResponse | null>(null);
  const [attempt, setAttempt] = useState<ImportAttemptState | null>(null);
  const [receipt, setReceipt] = useState<BackendBatchReceipt | null>(null);
  const [committedRevisionId, setCommittedRevisionId] = useState<string | null>(null);
  const [readError, setReadError] = useState<unknown>(null);
  const [reading, setReading] = useState(false);
  const [fresh, setFresh] = useState(false);
  const [preparing, setPreparing] = useState(false);
  const [conflict, setConflict] = useState(false);
  const [previewProjectVersion, setPreviewProjectVersion] = useState<number | null>(null);
  const [sessionId, setSessionId] = useState(initialSessionId);
  const [recoveryLoading, setRecoveryLoading] = useState(true);
  const [recoveryError, setRecoveryError] = useState<Error | null>(null);
  const [cleanupPending, setCleanupPending] = useState(false);
  const [recoveryMismatch, setRecoveryMismatch] = useState<{ sessionId?: string } | null>(null);
  const [hasRecoveryRecord, setHasRecoveryRecord] = useState(false);

  useEffect(() => {
    const state = control.current;
    state.mounted = true;
    return () => {
      state.mounted = false;
      state.generation++;
      notifyCandidate(null);
    };
  }, [notifyCandidate]);
  const frame = useCallback((value: ImportAttemptState | null) => {
    control.current.attempt = value;
    setAttempt(value);
  }, []);
  const invalidatePreview = useCallback(() => {
    setPreview(null);
    setPreviewProjectVersion(null);
    notifyCandidate(null);
  }, [notifyCandidate]);
  const adopt = useCallback(
    (value: BackendComposedImportSession) => {
      if (
        value.projectId !== projectId ||
        (control.current.session && value.id !== control.current.session.id)
      )
        throw new Error("Получена другая сессия импорта.");
      importVersion(value.version);
      if (control.current.session && value.version < control.current.session.version)
        throw new Error("Получена старая версия сессии. Перечитайте статус.");
      const previous = control.current.session;
      if (
        !previous ||
        previous.version !== value.version ||
        previous.candidateHash !== value.candidateHash ||
        previous.state !== value.state
      )
        invalidatePreview();
      control.current.session = value;
      control.current.sessionId = value.id;
      setSessionId(value.id);
      setSession(value);
    },
    [projectId, invalidatePreview],
  );
  const reload = useCallback(async () => {
    const id = control.current.session?.id ?? control.current.sessionId;
    if (!id || !control.current.recoveryReady || control.current.scopeMismatch) return false;
    const generation = control.current.generation;
    setReading(true);
    setReadError(null);
    try {
      const response = await getBackendImport(projectId, id, { limit: 100 });
      if (!control.current.mounted || generation !== control.current.generation) return false;
      if (
        response.status !== 200 ||
        response.data.session.mode !== "composed" ||
        response.data.session.id !== id
      )
        throw new Error("Не удалось прочитать составную сессию.");
      adopt(response.data.session);
      setStatus(response.data);
      setFresh(true);
      const saved = response.data.preview;
      if (
        saved &&
        saved.sessionId === id &&
        saved.version === response.data.session.version &&
        saved.candidateHash === response.data.session.candidateHash
      )
        setPreview(saved);
      else invalidatePreview();
      if (response.data.session.state === "aborted" || response.data.committedRevisionId) {
        setConflict(false);
        if (response.data.committedRevisionId)
          setCommittedRevisionId(response.data.committedRevisionId);
      }
      return true;
    } catch (error) {
      if (control.current.mounted && generation === control.current.generation) {
        setReadError(error);
        setFresh(false);
      }
      return false;
    } finally {
      if (control.current.mounted && generation === control.current.generation) setReading(false);
    }
  }, [projectId, adopt, invalidatePreview]);

  const readRecovery = useCallback(async () => {
    if (control.current.busy) return;
    const generation = ++control.current.generation;
    control.current.recoveryReady = false;
    setRecoveryLoading(true);
    const saved = await loadImportRecovery(projectId);
    if (!control.current.mounted || generation !== control.current.generation) return;
    control.current.recoveryReady = true;
    control.current.recoveryRaw = saved.raw;
    control.current.storageBlocked = !!saved.error;
    setHasRecoveryRecord(saved.raw !== null);
    setRecoveryError(saved.error);
    setCleanupPending(false);
    const recoveredAttempt =
      saved.attempt ??
      (control.current.attempt?.state === "uncertain" ? control.current.attempt.attempt : null);
    const restoredId =
      recoveredAttempt && recoveredAttempt.kind !== "begin"
        ? recoveredAttempt.sessionId
        : undefined;
    const mismatch = !!recoveredAttempt && !!initialSessionId && restoredId !== initialSessionId;
    control.current.scopeMismatch = mismatch;
    setRecoveryMismatch(mismatch ? { sessionId: restoredId } : null);
    frame(recoveredAttempt ? { attempt: recoveredAttempt, state: "uncertain" } : null);
    if (recoveredAttempt && !mismatch) {
      if (control.current.session?.id !== restoredId) {
        control.current.session = null;
        setSession(null);
        setStatus(null);
        setReceipt(null);
        setCommittedRevisionId(null);
        setFresh(false);
        invalidatePreview();
      }
      control.current.sessionId = restoredId;
      setSessionId(restoredId);
    }
    setRecoveryLoading(false);
    if (!saved.error && !mismatch && control.current.sessionId) await reload();
  }, [projectId, initialSessionId, frame, reload, invalidatePreview]);
  useEffect(() => {
    void readRecovery();
  }, [readRecovery]);

  const clearRecovery = useCallback(() => {
    try {
      if (control.current.recoveryRaw !== null)
        clearImportRecovery(projectId, control.current.recoveryRaw);
      control.current.recoveryRaw = null;
      control.current.storageBlocked = false;
      setHasRecoveryRecord(false);
      setRecoveryError(null);
      setCleanupPending(false);
      return true;
    } catch (error) {
      control.current.storageBlocked = true;
      setRecoveryError(
        error instanceof Error ? error : new Error("Не удалось очистить запись восстановления."),
      );
      setCleanupPending(true);
      return false;
    }
  }, [projectId]);

  async function execute(input: ImportAttempt, reviewedProjectVersion?: number) {
    if (
      !control.current.recoveryReady ||
      control.current.scopeMismatch ||
      control.current.busy ||
      (control.current.storageBlocked && control.current.attempt?.attempt !== input) ||
      ((control.current.attempt?.state === "uncertain" ||
        control.current.attempt?.state === "unsent") &&
        control.current.attempt.attempt !== input)
    )
      return;
    const captured =
      control.current.attempt?.attempt === input ? input : captureImportAttempt(input);
    const wasUncertain = control.current.attempt?.state === "uncertain";
    const generation = ++control.current.generation;
    const active = () => control.current.mounted && generation === control.current.generation;
    control.current.busy = true;
    setReading(false);
    setFresh(false);
    setReadError(null);
    frame({ attempt: captured, state: "pending" });
    invalidatePreview();
    try {
      const raw = await writeImportRecovery(projectId, captured, control.current.recoveryRaw);
      if (!active()) return;
      control.current.recoveryRaw = raw;
      control.current.storageBlocked = false;
      setHasRecoveryRecord(true);
      setRecoveryError(null);
    } catch (error) {
      if (!active()) return;
      control.current.storageBlocked = true;
      control.current.busy = false;
      const storageError =
        error instanceof Error
          ? error
          : new Error("Не удалось сохранить запрос для восстановления.");
      setRecoveryError(storageError);
      frame({
        attempt: captured,
        state: wasUncertain ? "uncertain" : "unsent",
        error: storageError,
      });
      return false;
    }
    let success = false;
    try {
      switch (captured.kind) {
        case "begin": {
          const response = await beginBackendImport(projectId, captured.body);
          if (!active()) return;
          if (
            response.status !== 200 ||
            response.data.mode !== "composed" ||
            response.data.baseRevisionId !== captured.body.baseRevisionId
          )
            throw new Error("Ответ Begin не совпадает с зафиксированной базой.");
          adopt(response.data);
          break;
        }
        case "batch": {
          const response = await putBackendImportBatch(
            projectId,
            captured.sessionId,
            captured.batchId,
            captured.body,
          );
          if (!active()) return;
          if (
            response.status !== 200 ||
            response.data.batchId !== captured.batchId ||
            response.data.payloadHash !== captured.body.payloadHash
          )
            throw new Error("Квитанция пакета не совпадает с исходным запросом.");
          importVersion(response.data.acceptedVersion);
          setReceipt(response.data);
          if (
            control.current.session &&
            response.data.acceptedVersion > control.current.session.version
          ) {
            adopt({
              ...control.current.session,
              version: response.data.acceptedVersion,
              state: "collecting",
              candidateHash: null,
              acceptedBatchCount: control.current.session.acceptedBatchCount + 1,
            });
          }
          break;
        }
        case "preview": {
          const response = await previewBackendImport(projectId, captured.sessionId, captured.body);
          if (!active()) return;
          if (
            response.status !== 200 ||
            response.data.sessionId !== captured.sessionId ||
            response.data.version < captured.body.expectedImportVersion
          )
            throw new Error("Получен другой Preview.");
          const current = control.current.session;
          if (!current) throw new Error("Сессия недоступна.");
          adopt({
            ...current,
            version: importVersion(response.data.version),
            state: response.data.state,
            candidateHash: response.data.candidateHash,
          });
          setPreview(response.data);
          setPreviewProjectVersion(reviewedProjectVersion ?? null);
          setFresh(true);
          break;
        }
        case "commit": {
          const response = await commitBackendImport(projectId, captured.sessionId, captured.body);
          if (!active()) return;
          if (
            response.status !== 200 ||
            response.data.sessionId !== captured.sessionId ||
            response.data.project.id !== projectId
          )
            throw new Error("Получена другая квитанция Commit.");
          setCommittedRevisionId(response.data.revision.id);
          break;
        }
        case "abort": {
          const response = await abortBackendImport(projectId, captured.sessionId, captured.body);
          if (!active()) return;
          if (
            response.status !== 200 ||
            response.data.mode !== "composed" ||
            response.data.id !== captured.sessionId ||
            response.data.state !== "aborted"
          )
            throw new Error("Результат отмены требует проверки статуса.");
          adopt(response.data);
          setFresh(true);
          break;
        }
      }
      success = true;
      clearRecovery();
      frame(null);
      setConflict(false);
    } catch (error) {
      if (!active()) return;
      const uncertain = uncertainImportFailure(error);
      if (!uncertain) clearRecovery();
      frame({
        attempt: captured,
        state: uncertain ? "uncertain" : "rejected",
        error,
      });
      if (error instanceof ApiFailure && error.status === 409) setConflict(true);
    } finally {
      if (active()) control.current.busy = false;
    }
    if (!active()) return;
    if (captured.kind !== "preview" && control.current.sessionId) await reload();
    if (
      success &&
      (captured.kind === "commit" || captured.kind === "abort" || captured.kind === "begin")
    ) {
      void client.invalidateQueries({ queryKey: getGetBackendProjectQueryKey(projectId) });
      void client.invalidateQueries({ queryKey: getListBackendProjectsQueryKey() });
      void client.invalidateQueries({ queryKey: ["backend-imports", projectId] });
    }
    return success;
  }
  async function upload(commands: readonly BackendImportCommand[]) {
    const current = control.current.session;
    if (
      !current ||
      control.current.busy ||
      !control.current.recoveryReady ||
      control.current.storageBlocked ||
      control.current.attempt?.state === "uncertain" ||
      control.current.attempt?.state === "unsent"
    )
      return;
    const generation = control.current.generation;
    setPreparing(true);
    control.current.busy = true;
    try {
      const captured = await captureImportBatch(current, commands);
      if (
        !control.current.mounted ||
        generation !== control.current.generation ||
        control.current.session?.version !== current.version
      )
        return;
      control.current.busy = false;
      return await execute(captured);
    } catch (error) {
      if (control.current.mounted && generation === control.current.generation) setReadError(error);
    } finally {
      if (control.current.mounted && generation === control.current.generation) {
        control.current.busy = false;
        setPreparing(false);
      } else if (control.current.mounted) setPreparing(false);
    }
  }
  const terminal =
    !!committedRevisionId || session?.state === "aborted" || session?.state === "committed";
  return {
    session,
    status,
    preview,
    attempt,
    receipt,
    committedRevisionId,
    readError,
    reading,
    fresh,
    conflict,
    previewProjectVersion,
    sessionId,
    recoveryLoading,
    recoveryError,
    recoveryMismatch,
    hasRecoveryRecord,
    cleanupPending,
    recoveryBlocked:
      recoveryLoading || !!recoveryError || !!recoveryMismatch || attempt?.state === "unsent",
    readRecovery,
    clearRecovery,
    discardRecovery: () => {
      if (control.current.busy || !control.current.recoveryReady || !clearRecovery()) return false;
      frame(null);
      invalidatePreview();
      return true;
    },
    busy: preparing || attempt?.state === "pending",
    uncertain: attempt?.state === "uncertain",
    terminal,
    execute,
    upload,
    reload,
    acknowledgeReload: () => {
      if (
        control.current.session &&
        !control.current.busy &&
        !control.current.storageBlocked &&
        control.current.attempt?.state !== "uncertain" &&
        control.current.attempt?.state !== "unsent"
      ) {
        setConflict(false);
        frame(null);
        invalidatePreview();
      }
    },
  };
}

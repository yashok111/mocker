import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  createBackendSavedView,
  saveBackendSavedView,
  getBackendSavedView,
  getGetBackendSavedViewQueryKey,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendSavedViewResponse,
  BackendSavedViewV2Target,
  BackendSavedViewState,
  BackendReadTarget,
  CreateBackendSavedViewRequest,
  SaveBackendSavedViewRequest,
} from "@/api/generated/schemas";
import { ApiFailure } from "@/api/client";
import { sameViewBinding, savedViewStateEqual, sameSavedViewTarget } from "./backendSavedViewState";
import { checkBackendSavedView } from "./backendSavedViewReads";
import { backendReadTargetKey } from "./backendReadTargets";
export type SavedViewAttempt =
  | { kind: "create"; input: CreateBackendSavedViewRequest }
  | { kind: "save"; viewId: string; input: SaveBackendSavedViewRequest };
const uncertain = (failure: unknown) =>
  !(failure instanceof ApiFailure) ||
  failure.status >= 500 ||
  failure.status === 408 ||
  failure.status === 429;
export function useBackendSavedViewSession(
  projectId: string,
  initial?: BackendSavedViewResponse,
  onSaved?: (saved: BackendSavedViewResponse) => void,
) {
  const client = useQueryClient();
  const [saved, setSaved] = useState(initial);
  const [documentVersion, setDocumentVersion] = useState<
    BackendSavedViewResponse["documentVersion"]
  >(initial?.documentVersion ?? "saved-view-v1");
  const [restoreGeneration, setRestoreGeneration] = useState(0);
  const [boundaryGeneration, setBoundaryGeneration] = useState(0);
  const [state, setState] = useState(initial?.state);
  const [target, setTarget] = useState<BackendSavedViewV2Target | undefined>(initial?.target);
  const [name, setName] = useState(initial?.name ?? "");
  const [preview, setPreview] = useState(false);
  const [pending, setPending] = useState<SavedViewAttempt | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [conflict, setConflict] = useState(false);
  const running = useRef(false);
  const attemptRef = useRef<SavedViewAttempt | null>(null);
  const controller = useRef<AbortController | null>(null);
  const epoch = useRef(0);
  const callback = useRef(onSaved);
  useEffect(() => {
    callback.current = onSaved;
  });
  useEffect(
    () => () => {
      epoch.current++;
      controller.current?.abort();
    },
    [],
  );
  useLayoutEffect(() => {
    epoch.current++;
    controller.current?.abort();
    controller.current = null;
    running.current = false;
    attemptRef.current = null;
  }, [boundaryGeneration]);
  const initialIdentity = initial && `${initial.id}:${initial.version}`;
  const [observedInitial, setObservedInitial] = useState(initialIdentity);
  if (observedInitial !== initialIdentity) {
    setObservedInitial(initialIdentity);
    const externallyChanged =
      !initial || saved?.id !== initial.id || saved?.version !== initial.version;
    if (externallyChanged) {
      setBoundaryGeneration((generation) => generation + 1);
      setPending(null);
      setBusy(false);
      setError("");
      setConflict(false);
    }
    if (!initial) {
      setRestoreGeneration((v) => v + 1);
      setSaved(undefined);
      setDocumentVersion("saved-view-v1");
      setState(undefined);
      setTarget(undefined);
      setName("");
      setPreview(false);
    } else if (saved?.id !== initial.id || saved?.version !== initial.version) {
      setRestoreGeneration((v) => v + 1);
      setSaved(initial);
      setDocumentVersion(initial.documentVersion);
      setState(initial.state);
      setTarget(initial.target);
      setName(initial.name);
      setPreview(false);
    }
  }
  const bound =
    !!saved &&
    !!state &&
    !!target &&
    ((saved.documentVersion === "saved-view-v2" &&
      sameSavedViewTarget(saved.target, target) &&
      saved.state.kind === state.kind) ||
      sameViewBinding(saved.target, saved.state, target, state) ||
      ("revisionId" in saved.target &&
        "revisionId" in target &&
        saved.target.revisionId === target.revisionId &&
        saved.state.kind === state.kind));
  const dirty =
    !!state && (!bound || !savedViewStateEqual(state, saved?.state) || name !== saved?.name);
  function capture(
    nextTarget: BackendReadTarget,
    nextState: BackendSavedViewState,
    version?: BackendSavedViewResponse["documentVersion"],
  ) {
    if (attemptRef.current || running.current || preview) return;
    if (nextTarget.importCandidate) {
      setError("Сохранение подготовленного графа импорта не поддерживается.");
      return;
    }
    const selectedVersion = nextTarget.changeProposal
      ? "saved-view-v2"
      : (version ?? documentVersion);
    if (selectedVersion === "saved-view-v2") {
      try {
        backendReadTargetKey(nextTarget);
      } catch {
        setError("Выберите точный граф перед сохранением вида.");
        return;
      }
    }
    setDocumentVersion(selectedVersion);
    setTarget(structuredClone(nextTarget) as BackendSavedViewV2Target);
    setState(nextState);
    setPreview(false);
    setError("");
    setConflict(false);
  }
  async function execute(attempt: SavedViewAttempt) {
    if (running.current || preview) return;
    running.current = true;
    const current = ++epoch.current;
    controller.current?.abort();
    const abort = new AbortController();
    controller.current = abort;
    attemptRef.current = attempt;
    setPending(attempt);
    setBusy(true);
    setError("");
    try {
      const response =
        attempt.kind === "create"
          ? await createBackendSavedView(projectId, attempt.input, { signal: abort.signal })
          : await saveBackendSavedView(projectId, attempt.viewId, attempt.input, {
              signal: abort.signal,
            });
      abort.signal.throwIfAborted();
      if (current !== epoch.current) return;
      if (response.status !== 200) throw new Error("Не удалось сохранить вид");
      const value = checkBackendSavedView(response.data, projectId, {
        ...(attempt.kind === "save" ? { id: attempt.viewId } : {}),
        target: attempt.kind === "create" ? attempt.input.target : saved?.target,
        documentVersion:
          "documentVersion" in attempt.input ? attempt.input.documentVersion : "saved-view-v1",
      });
      if (value.state.kind !== attempt.input.state.kind)
        throw new Error("Ответ сохранения содержит другой тип вида");
      // Adopt only the acknowledged baseline. Edits made during the request remain dirty.
      setState((current) =>
        savedViewStateEqual(current, attempt.input.state) ? value.state : current,
      );
      setName((current) => (current.trim() === attempt.input.name ? value.name : current));
      setSaved(value);
      setDocumentVersion(value.documentVersion);
      setTarget(value.target);
      attemptRef.current = null;
      setPending(null);
      setConflict(false);
      client.setQueryData(
        getGetBackendSavedViewQueryKey(projectId, value.id, { version: value.version }),
        value,
      );
      void client.invalidateQueries({ queryKey: ["backend-saved-views", projectId] });
      callback.current?.(value);
    } catch (failure) {
      if (current !== epoch.current || abort.signal.aborted) return;
      setError(failure instanceof Error ? failure.message : "Не удалось сохранить вид");
      setConflict(failure instanceof ApiFailure && failure.status === 409);
      if (!uncertain(failure)) {
        attemptRef.current = null;
        setPending(null);
      }
    } finally {
      if (current === epoch.current) {
        running.current = false;
        setBusy(false);
      }
    }
  }
  async function save(asNew = false) {
    if (
      attemptRef.current ||
      running.current ||
      preview ||
      !state ||
      !target ||
      !name.trim() ||
      (conflict && !asNew)
    )
      return;
    if (!asNew && saved && !bound) {
      setError("Источник или область изменились. Используйте «Сохранить как новый».");
      return;
    }
    const input = structuredClone({
      name: name.trim(),
      state,
      idempotencyKey: crypto.randomUUID(),
    });
    if (asNew || !saved) {
      if (documentVersion === "saved-view-v2")
        await execute({
          kind: "create",
          input: { ...input, documentVersion: "saved-view-v2", target: structuredClone(target) },
        });
      else if (target.revisionId)
        await execute({
          kind: "create",
          input: { ...input, target: { revisionId: target.revisionId } },
        });
      else if (target.proposal)
        await execute({
          kind: "create",
          input: { ...input, target: { proposal: structuredClone(target.proposal) } },
        });
    } else if (Number.isSafeInteger(saved.version) && saved.version > 0) {
      await execute({
        kind: "save",
        viewId: saved.id,
        input: {
          ...input,
          expectedVersion: saved.version,
          ...(saved.documentVersion === "saved-view-v2"
            ? { documentVersion: "saved-view-v2" as const }
            : {}),
        },
      });
    }
  }
  async function retry() {
    if (attemptRef.current) await execute(attemptRef.current);
  }
  async function reload() {
    if (!saved || running.current || attemptRef.current) return;
    running.current = true;
    setBusy(true);
    setError("");
    const current = ++epoch.current;
    const abort = new AbortController();
    controller.current = abort;
    try {
      const response = await getBackendSavedView(projectId, saved.id, undefined, {
        signal: abort.signal,
      });
      abort.signal.throwIfAborted();
      if (current !== epoch.current) return;
      if (
        response.status !== 200 ||
        !Number.isSafeInteger(response.data.version) ||
        response.data.projectId !== projectId
      )
        throw new Error("Не удалось загрузить текущий вид");
      checkBackendSavedView(response.data, projectId, {
        id: saved.id,
        target: saved.target,
        documentVersion: saved.documentVersion,
      });
      setRestoreGeneration((v) => v + 1);
      setSaved(response.data);
      setDocumentVersion(response.data.documentVersion);
      setState(response.data.state);
      setTarget(response.data.target);
      setName(response.data.name);
      setPreview(false);
      setConflict(false);
      client.setQueryData(
        getGetBackendSavedViewQueryKey(projectId, response.data.id, {
          version: response.data.version,
        }),
        response.data,
      );
      callback.current?.(response.data);
    } catch (failure) {
      if (current === epoch.current && !abort.signal.aborted)
        setError(failure instanceof Error ? failure.message : "Ошибка загрузки");
    } finally {
      if (current === epoch.current) {
        running.current = false;
        setBusy(false);
      }
    }
  }
  return {
    documentVersion,
    restoreGeneration,
    state,
    setState,
    name,
    setName,
    preview,
    setPreview,
    target,
    saved,
    bound,
    dirty,
    busy,
    pending,
    error,
    conflict,
    capture,
    save,
    retry,
    reload,
  };
}
export type BackendSavedViewSession = ReturnType<typeof useBackendSavedViewSession>;

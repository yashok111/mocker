import { readChangeProposal } from "./backendChangeReads";
import type { BackendChangeProposalDetail } from "@/api/generated/schemas";
import { hashBackendJSON } from "./backendImportHash";
import {
  createContext,
  createElement,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { ApiFailure } from "@/api/client";
import { parseBrowserSafeJson } from "@/api/preciseJson";
import {
  startBackendAnalysis,
  retryBackendAnalysis,
  cancelBackendAnalysis,
  applyBackendChangeProposalRebase,
  applyBackendChangeProposalLifecycle,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  StartBackendAnalysisRequest,
  RetryBackendAnalysisRequest,
  CancelBackendAnalysisRequest,
  ApplyBackendChangeProposalRebaseRequest,
  ApplyBackendChangeProposalLifecycleRequest,
  BackendChangeProposalApplyResult,
  BackendSourceVector,
} from "@/api/generated/schemas";
import { backendChangeSchemas } from "./backendChangeSchema";
import { changeSchemaIssues } from "./backendChangeFormModel";
import type { ChangeJSON } from "./backendChangeSchemaTypes";
import {
  changeMessage,
  changeUncertain,
  discoverProjectChangeRecovery,
} from "./backendChangeRecovery";

const schemas = {
  start: "StartBackendAnalysisRequest",
  retry: "RetryBackendAnalysisRequest",
  cancel: "CancelBackendAnalysisRequest",
  rebase: "ApplyBackendChangeProposalRebaseRequest",
  ready: "ApplyBackendChangeProposalLifecycleRequest",
} as const;
type Inputs = {
  start: StartBackendAnalysisRequest;
  retry: RetryBackendAnalysisRequest;
  cancel: CancelBackendAnalysisRequest;
  rebase: ApplyBackendChangeProposalRebaseRequest;
  ready: ApplyBackendChangeProposalLifecycleRequest;
};
export type AnalysisOwner = { projectId: string; proposalId?: string; jobId?: string };
export type AnalysisAcceptance = {
  jobId?: string;
  analysisInputHash?: string;
  draftHash?: string;
  oldRevisionId?: string;
  newBaseSemanticHash?: string;
  sourceVectorHash?: string;
  sourceVector?: BackendSourceVector;
  sourceSnapshotIds?: string[];
  candidateSemanticHash?: string;
};
export type AnalysisAttempt = {
  [K in keyof Inputs]: {
    kind: K;
    owner: AnalysisOwner;
    input: Inputs[K];
    body: string;
    phase: "unknown" | "conflict" | "accepted";
    acceptance: AnalysisAcceptance;
    conflict?: string;
  };
}[keyof Inputs];
export const analysisRecoveryKey = (projectId: string) => `backend-analysis-attempt:${projectId}`;
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
export function makeAnalysisAttempt<K extends keyof Inputs>(
  kind: K,
  owner: AnalysisOwner,
  input: Inputs[K],
  acceptance: AnalysisAcceptance = {},
): Extract<AnalysisAttempt, { kind: K }> {
  if (!owner || typeof owner !== "object") throw new Error("Нет владельца запроса");
  const body = JSON.stringify(input);
  const copy = parseBrowserSafeJson(body);
  if (
    new TextEncoder().encode(body).length > (kind === "start" ? 2 : 1) * 1024 * 1024 ||
    changeSchemaIssues(backendChangeSchemas[schemas[kind]]!, copy as ChangeJSON).length
  )
    throw new Error("Запрос не соответствует точной схеме или превышает лимит.");
  if (
    (kind === "start" && (owner.jobId || owner.proposalId)) ||
    ((kind === "retry" || kind === "cancel") && owner.proposalId) ||
    ((kind === "rebase" || kind === "ready") && owner.jobId) ||
    !uuid.test(owner.projectId) ||
    Object.keys(owner).some((k) => !["projectId", "proposalId", "jobId"].includes(k)) ||
    ((kind === "retry" || kind === "cancel") && (!owner.jobId || !uuid.test(owner.jobId))) ||
    ((kind === "ready" || kind === "rebase") && (!owner.proposalId || !uuid.test(owner.proposalId)))
  )
    throw new Error("Восстановление требует точного владельца запроса.");
  const hash = (v: unknown) => typeof v === "string" && /^[a-f0-9]{64}$/.test(v);
  if (
    (kind === "ready" && !hash(acceptance.draftHash)) ||
    (kind === "rebase" &&
      (!hash(acceptance.newBaseSemanticHash) ||
        !hash(acceptance.sourceVectorHash) ||
        !hash(acceptance.candidateSemanticHash) ||
        !Array.isArray(acceptance.sourceSnapshotIds)))
  )
    throw new Error("Отсутствуют точные основания принятия ответа.");
  if (
    acceptance.jobId !== undefined &&
    (typeof acceptance.jobId !== "string" || !uuid.test(acceptance.jobId))
  )
    throw new Error("Повреждён идентификатор принятого задания.");
  return {
    kind,
    owner: structuredClone(owner),
    input: copy,
    body,
    acceptance: structuredClone(acceptance),
    phase: "unknown",
  } as Extract<AnalysisAttempt, { kind: K }>;
}
function decode(raw: string, key: string): AnalysisAttempt {
  if (new TextEncoder().encode(raw).length > 5 * 1024 * 1024)
    throw new Error("Запись восстановления превышает лимит.");
  const value = parseBrowserSafeJson(raw) as Record<string, unknown>;
  if (
    !value ||
    typeof value !== "object" ||
    value.version !== 2 ||
    Object.keys(value).some(
      (k) => !["version", "kind", "owner", "body", "phase", "acceptance", "conflict"].includes(k),
    ) ||
    typeof value.kind !== "string" ||
    !(value.kind in schemas) ||
    typeof value.body !== "string" ||
    !["unknown", "conflict", "accepted"].includes(String(value.phase))
  )
    throw new Error("Повреждённая запись восстановления. Повтор отключён.");
  const acceptance = value.acceptance as AnalysisAcceptance;
  if (
    !acceptance ||
    typeof acceptance !== "object" ||
    Array.isArray(acceptance) ||
    Object.keys(acceptance).some(
      (k) =>
        ![
          "jobId",
          "analysisInputHash",
          "draftHash",
          "oldRevisionId",
          "newBaseSemanticHash",
          "sourceVectorHash",
          "sourceVector",
          "sourceSnapshotIds",
          "candidateSemanticHash",
        ].includes(k),
    )
  )
    throw new Error("Повреждены точные основания восстановления.");
  if (
    Object.entries(acceptance).some(
      ([k, v]) =>
        [
          "draftHash",
          "newBaseSemanticHash",
          "candidateSemanticHash",
          "sourceVectorHash",
          "analysisInputHash",
        ].includes(k) &&
        (typeof v !== "string" || !/^[a-f0-9]{64}$/.test(v)),
    )
  )
    throw new Error("Повреждены хеши восстановления");
  if (
    acceptance.sourceVector &&
    changeSchemaIssues(
      backendChangeSchemas.BackendSourceVector!,
      acceptance.sourceVector as unknown as ChangeJSON,
    ).length
  )
    throw new Error("Повреждён source vector");
  if (
    acceptance.sourceSnapshotIds &&
    (!Array.isArray(acceptance.sourceSnapshotIds) ||
      acceptance.sourceSnapshotIds.some((id) => !uuid.test(id)))
  )
    throw new Error("Повреждены снимки");
  const attempt = makeAnalysisAttempt(
    value.kind as keyof Inputs,
    value.owner as AnalysisOwner,
    parseBrowserSafeJson(value.body) as Inputs[keyof Inputs],
    acceptance,
  );
  if (
    attempt.body !== value.body ||
    key !== analysisRecoveryKey(attempt.owner.projectId) ||
    (value.conflict !== undefined && typeof value.conflict !== "string")
  )
    throw new Error("Владелец или исходные байты восстановления изменены.");
  return {
    ...attempt,
    phase: value.phase,
    ...(value.conflict ? { conflict: value.conflict } : {}),
  } as AnalysisAttempt;
}
export type AnalysisSlot = {
  key: string;
  raw: string | null;
  attempt: AnalysisAttempt | null;
  error: Error | null;
};
export function inspectAnalysisRecovery(key: string): AnalysisSlot {
  let raw: string | null = null;
  try {
    raw = sessionStorage.getItem(key);
    return { key, raw, attempt: raw === null ? null : decode(raw, key), error: null };
  } catch (cause) {
    return {
      key,
      raw,
      attempt: null,
      error: new Error(
        "Не удалось прочитать восстановление: повреждённая запись или хранилище недоступно.",
        { cause },
      ),
    };
  }
}
export function writeAnalysisRecovery(
  key: string,
  attempt: AnalysisAttempt | null,
  expectedRaw: string | null,
): string | null {
  if (attempt && JSON.stringify(attempt.input) !== attempt.body)
    throw new Error("Исходные байты запроса изменены");
  const current = sessionStorage.getItem(key);
  if (current !== expectedRaw)
    throw new Error("Запись восстановления изменилась. Перечитайте её перед продолжением.");
  const raw = attempt
    ? JSON.stringify({
        version: 2,
        kind: attempt.kind,
        owner: attempt.owner,
        body: attempt.body,
        phase: attempt.phase,
        acceptance: attempt.acceptance,
        ...(attempt.conflict ? { conflict: attempt.conflict } : {}),
      })
    : null;
  if (raw) decode(raw, key);
  if (raw === null) sessionStorage.removeItem(key);
  else sessionStorage.setItem(key, raw);
  if (sessionStorage.getItem(key) !== raw)
    throw new Error("Не удалось подтвердить запись восстановления. Запрос не отправлен.");
  window.dispatchEvent(new Event("backend-recovery-change"));
  return raw;
}
export async function verifyAnalysisMutation(attempt: AnalysisAttempt, result: unknown) {
  if (["start", "retry", "cancel"].includes(attempt.kind)) {
    const job = result as {
      id: string;
      projectId: string;
      version: number;
      analysisInputHash: string;
      status: string;
      kind: string;
    };
    if (
      !job ||
      !uuid.test(job.id) ||
      job.projectId !== attempt.owner.projectId ||
      !Number.isSafeInteger(job.version) ||
      job.version < 1 ||
      !/^[a-f0-9]{64}$/.test(job.analysisInputHash) ||
      (attempt.kind === "start" && job.kind !== attempt.input.kind) ||
      (attempt.acceptance.analysisInputHash !== undefined &&
        job.analysisInputHash !== attempt.acceptance.analysisInputHash) ||
      (attempt.kind === "cancel" &&
        (job.id !== attempt.owner.jobId || job.status !== "cancelled")) ||
      (attempt.kind === "retry" && job.id === attempt.owner.jobId)
    )
      throw new Error("Ответ относится к другому заданию.");
    return;
  }
  if (attempt.kind === "ready") {
    const d = result as BackendChangeProposalApplyResult;
    if (
      d.proposal.projectId !== attempt.owner.projectId ||
      d.proposal.id !== attempt.owner.proposalId ||
      d.revision.id !== attempt.input.proposalRevisionId ||
      d.revision.semanticHash !== attempt.acceptance.draftHash ||
      d.proposal.currentDraftRevisionId !== d.revision.id ||
      d.proposal.currentDraftHash !== d.revision.semanticHash ||
      d.proposal.version !== attempt.input.expectedVersion + 1 ||
      d.proposal.status !== "ready" ||
      JSON.stringify(d.proposal.readyReference?.report) !== JSON.stringify(attempt.input.report) ||
      JSON.stringify(d.proposal.readyReference?.acknowledgedGapIds) !==
        JSON.stringify(attempt.input.acknowledgedGapIds)
    )
      throw new Error("Ready не подтвердил точный черновик и выбранный отчёт.");
  } else if (attempt.kind === "rebase") {
    const r = result as BackendChangeProposalApplyResult;
    if (
      r.proposal.projectId !== attempt.owner.projectId ||
      r.proposal.id !== attempt.owner.proposalId ||
      r.revision.proposalId !== attempt.owner.proposalId ||
      r.revision.parentRevisionId !== attempt.input.proposalRevisionId ||
      r.revision.id === attempt.input.proposalRevisionId ||
      r.proposal.currentDraftRevisionId !== r.revision.id ||
      r.proposal.currentDraftHash !== r.revision.semanticHash ||
      r.proposal.version !== attempt.input.expectedVersion + 1 ||
      r.proposal.status !== "draft" ||
      r.proposal.readyReference ||
      r.revision.baseRevisionId !== attempt.input.newBaseRevisionId ||
      r.revision.baseSemanticHash !== attempt.acceptance.newBaseSemanticHash ||
      r.revision.semanticHash !== attempt.acceptance.candidateSemanticHash ||
      (await hashBackendJSON(r.revision.sourceVector)) !== attempt.acceptance.sourceVectorHash ||
      JSON.stringify(r.revision.sourceSnapshotIds) !==
        JSON.stringify(attempt.acceptance.sourceSnapshotIds) ||
      r.revision.rebase?.candidateHash !== attempt.input.candidateHash
    )
      throw new Error("Rebase не подтвердил выбранную новую базу, вектор и новый черновик.");
  }
}
async function send(attempt: AnalysisAttempt) {
  if (JSON.stringify(attempt.input) !== attempt.body)
    throw new Error("Исходные байты запроса изменены");
  const { projectId, proposalId, jobId } = attempt.owner;
  switch (attempt.kind) {
    case "start": {
      const r = await startBackendAnalysis(projectId, attempt.input);
      if (r.status !== 202) throw new Error("Старт не подтверждён");
      return r.data;
    }
    case "retry": {
      const r = await retryBackendAnalysis(projectId, jobId!, attempt.input);
      if (r.status !== 202) throw new Error("Повтор не подтверждён");
      return r.data;
    }
    case "cancel": {
      const r = await cancelBackendAnalysis(projectId, jobId!, attempt.input);
      if (r.status !== 200) throw new Error("Отмена не подтверждена");
      return r.data;
    }
    case "rebase": {
      const r = await applyBackendChangeProposalRebase(projectId, proposalId!, attempt.input);
      if (r.status !== 200) throw new Error("Rebase не подтверждён");
      return r.data;
    }
    case "ready": {
      const r = await applyBackendChangeProposalLifecycle(projectId, proposalId!, attempt.input);
      if (r.status !== 200) throw new Error("Ready не подтверждён");
      return r.data;
    }
  }
}
export type RebaseReconciliation = {
  attempt: Extract<AnalysisAttempt, { kind: "rebase" }>;
  detail: BackendChangeProposalDetail;
};
function useRecovery(projectId: string) {
  const key = analysisRecoveryKey(projectId);
  const [slot, setSlot] = useState(() => inspectAnalysisRecovery(key));
  const ref = useRef(slot);
  const flight = useRef(false);
  const [legacy, setLegacy] = useState(() => discoverProjectChangeRecovery(projectId));
  useEffect(() => {
    const update = () => setLegacy(discoverProjectChangeRecovery(projectId));
    window.addEventListener("backend-recovery-change", update);
    return () => window.removeEventListener("backend-recovery-change", update);
  }, [projectId]);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [lastReconciled, setLastReconciled] = useState<RebaseReconciliation | null>(null);
  const [lastAccepted, setLastAccepted] = useState<{
    attempt: AnalysisAttempt;
    result: unknown;
  } | null>(null);
  useEffect(() => {
    const changed = () => {
      if (!flight.current) {
        const next = inspectAnalysisRecovery(key);
        ref.current = next;
        setSlot(next);
      }
    };
    window.addEventListener("storage", changed);
    return () => window.removeEventListener("storage", changed);
  }, [key]);
  const adopt = (value: AnalysisSlot) => {
    ref.current = value;
    setSlot(value);
  };
  const persist = (attempt: AnalysisAttempt | null) => {
    try {
      const raw = writeAnalysisRecovery(key, attempt, ref.current.raw);
      adopt({ key, raw, attempt, error: null });
      return true;
    } catch (cause) {
      adopt({
        ...ref.current,
        error: new Error(
          "Не удалось сохранить или очистить восстановление. Новые запросы заблокированы.",
          { cause },
        ),
      });
      setMessage(changeMessage(cause));
      return false;
    }
  };
  async function execute(next?: AnalysisAttempt) {
    if (
      flight.current ||
      ref.current.error ||
      (next &&
        (ref.current.attempt ||
          discoverProjectChangeRecovery(projectId).some((x) => x.raw || x.error)))
    )
      return null;
    const pending = next ?? ref.current.attempt;
    if (
      !pending ||
      pending.phase !== "unknown" ||
      pending.owner.projectId !== projectId ||
      !persist(pending)
    )
      return null;
    flight.current = true;
    setBusy(true);
    setMessage("");
    try {
      const result = await send(pending);
      await verifyAnalysisMutation(pending, result);
      // Persist acceptance before cleanup: reload must never turn a confirmed action back into unknown.
      const accepted = {
        ...pending,
        phase: "accepted" as const,
        acceptance: {
          ...pending.acceptance,
          ...(["start", "retry", "cancel"].includes(pending.kind)
            ? { jobId: (result as { id: string }).id }
            : {}),
        },
      };
      const marked = persist(accepted);
      setLastAccepted({ attempt: accepted, result });
      if (marked) persist(null);
      window.dispatchEvent(
        new CustomEvent("backend-analysis-accepted", { detail: { attempt: accepted, result } }),
      );
      return result;
    } catch (failure) {
      setMessage(changeMessage(failure));
      if (failure instanceof ApiFailure && failure.status === 409)
        persist({ ...pending, phase: "conflict", conflict: changeMessage(failure) });
      else if (!changeUncertain(failure)) persist(null);
      return null;
    } finally {
      flight.current = false;
      setBusy(false);
    }
  }
  async function reconcileRebase() {
    const original = ref.current;
    const pending = original.attempt;
    if (
      flight.current ||
      original.error ||
      pending?.kind !== "rebase" ||
      pending.phase !== "conflict"
    )
      return false;
    flight.current = true;
    setBusy(true);
    setMessage("");
    try {
      const detail = await readChangeProposal(projectId, pending.owner.proposalId!);
      if (
        detail.proposal.currentDraftRevisionId !== detail.revision.id ||
        detail.proposal.currentDraftHash !== detail.revision.semanticHash ||
        detail.proposal.version < pending.input.expectedVersion
      )
        throw new Error("Не удалось подтвердить текущий черновик предложения.");
      if (ref.current.raw !== original.raw)
        throw new Error("Исходный конфликт изменился во время сверки.");
      if (!persist(null)) return false;
      const value = { attempt: pending, detail };
      setLastReconciled(value);
      window.dispatchEvent(new CustomEvent("backend-rebase-reconciled", { detail: value }));
      setMessage(
        "Текущее предложение перечитано. Прежние решения сохранены справочно; нужен новый предпросмотр переноса.",
      );
      return true;
    } catch (failure) {
      setMessage(changeMessage(failure));
      return false;
    } finally {
      flight.current = false;
      setBusy(false);
    }
  }
  return {
    ...slot,
    busy,
    message,
    lastAccepted,
    lastReconciled,
    reconcileRebase,
    execute,
    clear: () => persist(null),
    reload: () => adopt(inspectAnalysisRecovery(key)),
    blocked: busy || !!slot.attempt || !!slot.error || legacy.some((x) => x.raw || x.error),
  };
}
export type AnalysisRecovery = ReturnType<typeof useRecovery>;
const RecoveryContext = createContext<AnalysisRecovery | null>(null);
export function BackendAnalysisRecoveryProvider({
  projectId,
  children,
}: {
  projectId: string;
  children: ReactNode;
}) {
  const recovery = useRecovery(projectId);
  return createElement(RecoveryContext.Provider, { value: recovery }, children);
}
export function useBackendAnalysisRecovery(projectId: string) {
  const shared = useContext(RecoveryContext);
  const local = useRecovery(projectId);
  return shared ?? local;
}

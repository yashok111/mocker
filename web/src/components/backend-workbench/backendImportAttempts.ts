import type {
  AbortBackendImportRequest,
  BackendComposedImportSession,
  BackendImportCommand,
  BackendImportPreviewResponse,
  BeginBackendImportRequest,
  CommitBackendImportRequest,
  PreviewBackendImportRequest,
  PutBackendImportBatchRequest,
} from "@/api/generated/schemas";
import { ApiFailure } from "@/api/client";
import { hashBackendImportCommands } from "./backendImportHash";
import { maxCollectorBytes, validateSessionProof } from "./backendSourceInputs";

export type ImportCandidateTarget = {
  importCandidate: { importId: string; importVersion: number; candidateHash: string };
};
export type ImportAttempt =
  | { kind: "begin"; projectId: string; body: BeginBackendImportRequest }
  | {
      kind: "batch";
      projectId: string;
      sessionId: string;
      batchId: string;
      body: PutBackendImportBatchRequest;
    }
  | { kind: "preview"; projectId: string; sessionId: string; body: PreviewBackendImportRequest }
  | { kind: "commit"; projectId: string; sessionId: string; body: CommitBackendImportRequest }
  | { kind: "abort"; projectId: string; sessionId: string; body: AbortBackendImportRequest };

export function captureImportAttempt<T extends ImportAttempt>(value: T): T {
  const copy = structuredClone(value);
  const freeze = (item: unknown) => {
    if (!item || typeof item !== "object") return;
    for (const child of Object.values(item)) freeze(child);
    Object.freeze(item);
  };
  freeze(copy);
  return copy;
}
export function importVersion(value: number): number {
  if (!Number.isSafeInteger(value) || value < 1)
    throw new Error("Нужна точная положительная версия. Перечитайте состояние.");
  return value;
}
export async function captureImportBatch(
  session: BackendComposedImportSession,
  commands: readonly BackendImportCommand[],
): Promise<Extract<ImportAttempt, { kind: "batch" }>> {
  const stable = structuredClone(commands) as BackendImportCommand[];
  validateSessionProof(stable, session);
  const body: PutBackendImportBatchRequest = {
    expectedImportVersion: importVersion(session.version),
    payloadHash: await hashBackendImportCommands(stable),
    commands: stable,
  };
  if (
    stable.length < 1 ||
    stable.length > 500 ||
    new TextEncoder().encode(JSON.stringify(body)).length > maxCollectorBytes
  )
    throw new Error("Полный пакет превышает 500 команд или 1 МиБ.");
  return captureImportAttempt({
    kind: "batch",
    projectId: session.projectId,
    sessionId: session.id,
    batchId: crypto.randomUUID(),
    body,
  });
}
export function captureImportCommit(
  session: BackendComposedImportSession,
  preview: BackendImportPreviewResponse,
  projectVersion: number,
): Extract<ImportAttempt, { kind: "commit" }> {
  if (
    session.state !== "ready" ||
    preview.state !== "ready" ||
    preview.sessionId !== session.id ||
    preview.version !== session.version ||
    !preview.candidateHash ||
    preview.candidateHash !== session.candidateHash
  )
    throw new Error("Сохранённый Preview больше не соответствует сессии.");
  return captureImportAttempt({
    kind: "commit",
    projectId: session.projectId,
    sessionId: session.id,
    body: {
      expectedVersion: importVersion(projectVersion),
      expectedImportVersion: importVersion(session.version),
      candidateHash: preview.candidateHash,
      idempotencyKey: crypto.randomUUID(),
    },
  });
}
export function uncertainImportFailure(error: unknown): boolean {
  return (
    !(error instanceof ApiFailure) ||
    error.status < 400 ||
    error.status >= 500 ||
    error.status === 408 ||
    error.status === 429
  );
}
export function candidateTarget(
  session: BackendComposedImportSession,
  preview: BackendImportPreviewResponse,
): ImportCandidateTarget | null {
  if (
    session.state !== "ready" ||
    preview.state !== "ready" ||
    session.version !== preview.version ||
    session.id !== preview.sessionId ||
    !preview.candidateHash ||
    session.candidateHash !== preview.candidateHash
  )
    return null;
  return {
    importCandidate: {
      importId: session.id,
      importVersion: session.version,
      candidateHash: preview.candidateHash,
    },
  };
}

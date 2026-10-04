import {
  getBackendAssertions,
  getBackendCoverage,
  getBackendImportCandidateAssertions,
  getBackendRevision,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendComposedImportSession,
  BackendCoverageResponse,
  BackendImportPreviewResponse,
  BackendRevisionResponse,
} from "@/api/generated/schemas";
import { candidateTarget } from "./backendImportAttempts";
import type { SyncPartition } from "./backendSourceInputs";

export type SyncBase = {
  revision: BackendRevisionResponse;
  coverage: BackendCoverageResponse;
  partitions: SyncPartition[];
};
export async function loadSyncBase(
  projectId: string,
  revisionId: string,
  signal?: AbortSignal,
): Promise<SyncBase> {
  const [revision, coverage] = await Promise.all([
    getBackendRevision(projectId, revisionId, { signal }),
    getBackendCoverage(projectId, revisionId, { signal }),
  ]);
  signal?.throwIfAborted();
  if (
    revision.status !== 200 ||
    coverage.status !== 200 ||
    revision.data.id !== revisionId ||
    revision.data.projectId !== projectId
  )
    throw new Error("Не удалось прочитать точную базу импорта.");
  let partitions: SyncPartition[] = [];
  if (String(revision.data.schemaVersion) === "6") {
    const vector = "source" in coverage.data ? coverage.data.source?.sourceVector : undefined;
    if (!vector) throw new Error("В ответе source6 отсутствует полный вектор разделов.");
    partitions = vector.partitions.map((part) => {
      const snapshot = vector.snapshots.find(
        (s) =>
          s.id === part.snapshotId &&
          s.repositoryId === part.repositoryId &&
          s.provider.namespace === part.providerNamespace,
      );
      if (!snapshot) throw new Error("Активный снимок раздела отсутствует в точном векторе.");
      return {
        repositoryId: part.repositoryId,
        snapshotId: part.snapshotId,
        provider: part.provider,
        snapshot,
      };
    });
  } else if (String(revision.data.schemaVersion) === "5") {
    const primary = coverage.data.snapshots.filter(
      (s) => s.role === "primary" || (coverage.data.snapshots.length === 1 && !s.role),
    );
    if (primary.length !== 1) throw new Error("Не найден единственный исходный раздел source5.");
    partitions = primary.map((snapshot) => ({
      repositoryId: snapshot.repositoryId,
      snapshotId: snapshot.id,
      provider: snapshot.provider,
      snapshot,
    }));
  }
  return { revision: revision.data, coverage: coverage.data, partitions };
}

export async function loadSyncAssertions(
  base: SyncBase,
  session: BackendComposedImportSession,
  preview: BackendImportPreviewResponse | null,
  recordType: "node" | "edge",
  cursor: string,
) {
  const roster = base.partitions.filter((part) => part.repositoryId === session.repositoryId);
  if (session.sourceScope.kind === "add_repository" || !roster.length)
    throw new Error("В новом репозитории нет прежних утверждений для общего UUID.");
  const params = {
    recordType,
    repositoryId: session.repositoryId,
    limit: 100,
    ...(cursor ? { cursor } : {}),
  };
  const target = preview ? candidateTarget(session, preview) : null;
  const source5 = String(base.revision.schemaVersion) === "5";
  const preserved = roster.filter(
    (part) =>
      session.acceptedBatchCount === 0 ||
      part.provider.namespace !== session.manifest.provider.namespace,
  );
  if (source5 && !preserved.length)
    throw new Error(
      "После переобнаружения этого провайдера кандидат не доказывает прежний assertionHash. Выберите общий UUID до первого пакета в новой сессии.",
    );
  if (source5 && !target)
    throw new Error(
      "Для базы source5 сначала выполните явный начальный Preview. Утверждения доступны только для READY-кандидата; сохранённые пробелы нельзя обходить.",
    );
  const response = source5
    ? await getBackendImportCandidateAssertions(session.projectId, session.id, {
        ...params,
        importVersion: target!.importCandidate.importVersion,
        candidateHash: target!.importCandidate.candidateHash,
      })
    : await getBackendAssertions(session.projectId, base.revision.id, params);
  if (
    response.status !== 200 ||
    response.data.pins.baseRevisionId !== base.revision.id ||
    response.data.pins.baseSemanticHash !== base.revision.semanticHash
  )
    throw new Error("Утверждения относятся к другой базе.");
  const actual = response.data.target;
  if (
    source5
      ? !("importCandidate" in actual) ||
        !actual.importCandidate ||
        actual.importCandidate.importId !== session.id ||
        actual.importCandidate.importVersion !== target!.importCandidate.importVersion ||
        actual.importCandidate.candidateHash !== target!.importCandidate.candidateHash
      : !("revisionId" in actual) || actual.revisionId !== base.revision.id
  )
    throw new Error("Утверждения относятся к другому точному target.");
  const owners = source5 ? preserved : roster;
  return {
    items: response.data.items
      .map((item) => item.assertion)
      .filter(
        (a) =>
          a.recordType === recordType &&
          a.owner.repositoryId === session.repositoryId &&
          owners.some((part) => part.provider.namespace === a.owner.providerNamespace),
      ),
    nextCursor: response.data.nextCursor,
  };
}

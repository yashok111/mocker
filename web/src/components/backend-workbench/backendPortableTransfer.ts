import {
  beginBackendPortableImport,
  putBackendPortableImportChunk,
  previewBackendPortableImport,
  commitBackendPortableImport,
  abortBackendPortableImport,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendPortableManifest,
  BackendPortableSession,
  BackendPortablePreview,
  BackendPortableCommit,
  BeginBackendPortableImportRequest,
  PutBackendPortableImportChunkRequest,
  PreviewBackendPortableImportRequest,
  CommitBackendPortableImportRequest,
  AbortBackendPortableImportRequest,
} from "@/api/generated/schemas";
import { parseBrowserSafeJson } from "@/api/preciseJson";
import { hashBackendJSON, hashBackendUTF8 } from "./backendImportHash";

export type PortableBundle = { manifest: BackendPortableManifest; chunks: string[] };
export type PortableAttempt =
  | { kind: "begin"; body: BeginBackendPortableImportRequest }
  | { kind: "put"; id: string; body: PutBackendPortableImportChunkRequest }
  | { kind: "preview"; id: string; body: PreviewBackendPortableImportRequest }
  | { kind: "commit"; id: string; body: CommitBackendPortableImportRequest }
  | { kind: "abort"; id: string; body: AbortBackendPortableImportRequest };
export type PortableCheckpoint = {
  manifestHash: string;
  session?: BackendPortableSession;
  nextChunk: number;
  pending?: PortableAttempt;
  candidate?: {
    hash: string;
    projectId: string;
    request: string;
    recordCount: number;
    unresolvedCount: number;
  };
  committedProjectId?: string;
};
export type PortableReply = BackendPortableSession | BackendPortablePreview | BackendPortableCommit;
export function loadPortableCheckpoint(key: string): PortableCheckpoint | undefined {
  try {
    const raw = localStorage.getItem(key);
    if (!raw) return undefined;
    const value = parseBrowserSafeJson(raw) as PortableCheckpoint;
    if (
      !value ||
      typeof value.manifestHash !== "string" ||
      !Number.isSafeInteger(value.nextChunk) ||
      value.nextChunk < 0
    )
      throw new Error("bad checkpoint");
    return value;
  } catch {
    return undefined;
  }
}
export function savePortableCheckpoint(key: string, value: PortableCheckpoint): void {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {
    throw new Error(
      "Не удалось сохранить точный запрос для повтора. Освободите локальное хранилище; запрос не отправлен.",
    );
  }
}
export async function readPortableBundle(
  text: string,
): Promise<{ bundle: PortableBundle; hash: string }> {
  const value = parseBrowserSafeJson(text) as Partial<PortableBundle>;
  if (
    !value ||
    typeof value !== "object" ||
    Object.keys(value).some((k) => k !== "manifest" && k !== "chunks") ||
    !value.manifest ||
    !Array.isArray(value.chunks) ||
    value.manifest.format !== "backend-portable-v1" ||
    !Array.isArray(value.manifest.chunks) ||
    value.chunks.length !== value.manifest.chunks.length ||
    value.chunks.length === 0
  )
    throw new Error("Ожидается portable JSON с manifest и точными строками chunks");
  let total = 0;
  for (let index = 0; index < value.chunks.length; index++) {
    const body = value.chunks[index];
    const descriptor = value.manifest.chunks[index];
    if (typeof body !== "string" || !descriptor || descriptor.index !== index)
      throw new Error("Нарушен порядок portable chunks");
    const bytes = new TextEncoder().encode(body).byteLength;
    total += bytes;
    if (
      bytes > 1048576 ||
      bytes !== descriptor.bytes ||
      total > 268435456 ||
      descriptor.records < 1 ||
      descriptor.records > 500
    )
      throw new Error("Нарушен лимит или размер portable chunk");
    if ((await hashBackendUTF8(body)) !== descriptor.sha256)
      throw new Error(`Не совпадает SHA-256 chunk ${index}`);
  }
  return { bundle: value as PortableBundle, hash: await hashBackendJSON(value.manifest) };
}
export async function runPortableAttempt(attempt: PortableAttempt): Promise<PortableReply> {
  switch (attempt.kind) {
    case "begin": {
      const r = await beginBackendPortableImport(attempt.body);
      if (r.status !== 200) throw new Error("Не удалось создать staging");
      return r.data;
    }
    case "put": {
      const r = await putBackendPortableImportChunk(attempt.id, attempt.body);
      if (r.status !== 200) throw new Error("Chunk не принят");
      return r.data;
    }
    case "preview": {
      const r = await previewBackendPortableImport(attempt.id, attempt.body);
      if (r.status !== 200) throw new Error("Preview не получен");
      return r.data;
    }
    case "commit": {
      const r = await commitBackendPortableImport(attempt.id, attempt.body);
      if (r.status !== 200) throw new Error("Commit не подтверждён");
      return r.data;
    }
    case "abort": {
      const r = await abortBackendPortableImport(attempt.id, attempt.body);
      if (r.status !== 200) throw new Error("Abort не подтверждён");
      return r.data;
    }
  }
}
export function portableCheckpointAfter(
  state: PortableCheckpoint,
  attempt: PortableAttempt,
  result: PortableReply,
): PortableCheckpoint {
  const next: PortableCheckpoint = { ...state, pending: undefined };
  const session = "session" in result ? result.session : result;
  if (session.manifestHash !== state.manifestHash || ("id" in attempt && session.id !== attempt.id))
    throw new Error("Ответ относится к другому portable session/manifest");
  next.session = session;
  if (attempt.kind === "put") next.nextChunk = attempt.body.index + 1;
  if (attempt.kind === "preview" && "unresolved" in result)
    next.candidate = {
      hash: result.candidateHash,
      projectId: result.projectId,
      request: JSON.stringify([attempt.body.name, attempt.body.artifactMappings]),
      recordCount: result.recordCount,
      unresolvedCount: result.unresolved.length,
    };
  if (attempt.kind === "commit" && "project" in result) next.committedProjectId = result.project.id;
  if (attempt.kind === "abort") next.candidate = undefined;
  return next;
}

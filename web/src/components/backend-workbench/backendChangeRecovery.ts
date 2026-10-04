import { ApiFailure } from "@/api/client";
import { parseBrowserSafeJson } from "@/api/preciseJson";
import type {
  ApplyBackendChangeProposalCommandsRequest,
  CreateBackendChangeProposalRequest,
  RestoreBackendChangeProposalRequest,
} from "@/api/generated/schemas";
import { backendChangeSchemas } from "./backendChangeSchema";
import { changeSchemaIssues } from "./backendChangeFormModel";
import type { ChangeJSON } from "./backendChangeSchemaTypes";

export type ChangeAttempt =
  | {
      kind: "create";
      body: string;
      input: CreateBackendChangeProposalRequest;
      phase: "unknown" | "conflict";
    }
  | {
      kind: "apply";
      body: string;
      input: ApplyBackendChangeProposalCommandsRequest;
      phase: "unknown" | "conflict";
    }
  | {
      kind: "restore";
      body: string;
      input: RestoreBackendChangeProposalRequest;
      phase: "unknown" | "conflict";
    };
export const changeUncertain = (error: unknown) =>
  !(error instanceof ApiFailure) ||
  error.status >= 500 ||
  error.status === 408 ||
  error.status === 429;
export const changeMessage = (error: unknown) =>
  error instanceof Error ? error.message : "Не удалось выполнить запрос";
const schemas = {
  create: "CreateBackendChangeProposalRequest",
  apply: "ApplyBackendChangeProposalCommandsRequest",
  restore: "RestoreBackendChangeProposalRequest",
} as const;
export function makeChangeAttempt(
  kind: "create",
  input: CreateBackendChangeProposalRequest,
): Extract<ChangeAttempt, { kind: "create" }>;
export function makeChangeAttempt(
  kind: "apply",
  input: ApplyBackendChangeProposalCommandsRequest,
): Extract<ChangeAttempt, { kind: "apply" }>;
export function makeChangeAttempt(
  kind: "restore",
  input: RestoreBackendChangeProposalRequest,
): Extract<ChangeAttempt, { kind: "restore" }>;
export function makeChangeAttempt(
  kind: ChangeAttempt["kind"],
  input: ChangeAttempt["input"],
): ChangeAttempt {
  const body = JSON.stringify(input);
  if (new TextEncoder().encode(body).length > 1024 * 1024)
    throw new Error("Запрос превышает 1 МиБ.");
  const copy: unknown = parseBrowserSafeJson(body);
  if (changeSchemaIssues(backendChangeSchemas[schemas[kind]]!, copy as ChangeJSON).length)
    throw new Error("Запрос не соответствует typed схеме");
  return { kind, input: copy, body, phase: "unknown" } as ChangeAttempt;
}
const bodyLimit = 1024 * 1024;
const recordLimit = bodyLimit * 2 + 4096;
export type ChangeRecoverySlot = {
  key: string;
  raw: string | null;
  attempt: ChangeAttempt | null;
  error: Error | null;
};

function storageError(action: string, cause: unknown): Error {
  return new Error(
    `Не удалось ${action} восстановление в хранилище браузера. Для отправки требуется подтверждённая запись.`,
    { cause },
  );
}
function storedChange(key: string): string | null {
  try {
    return sessionStorage.getItem(key);
  } catch (cause) {
    throw storageError("прочитать", cause);
  }
}
function decodeChangeRecovery(raw: string): ChangeAttempt {
  if (raw.length > recordLimit || new TextEncoder().encode(raw).length > recordLimit)
    throw new Error("Запись восстановления превышает допустимый размер.");
  const value: unknown = parseBrowserSafeJson(raw);
  if (
    !value ||
    typeof value !== "object" ||
    Array.isArray(value) ||
    !("kind" in value) ||
    !("body" in value) ||
    !("phase" in value) ||
    typeof value.body !== "string" ||
    !["create", "apply", "restore"].includes(String(value.kind)) ||
    !["unknown", "conflict"].includes(String(value.phase)) ||
    Object.keys(value).some((key) => !["version", "kind", "body", "phase"].includes(key)) ||
    ("version" in value && value.version !== 1)
  )
    throw new Error("Повреждённая или несовместимая запись восстановления. Повтор отключён.");
  if (new TextEncoder().encode(value.body).length > bodyLimit)
    throw new Error("Сохранённый запрос превышает 1 МиБ.");
  const kind = value.kind as ChangeAttempt["kind"];
  const input: unknown = parseBrowserSafeJson(value.body);
  if (
    changeSchemaIssues(backendChangeSchemas[schemas[kind]]!, input as ChangeJSON).length ||
    JSON.stringify(input) !== value.body
  )
    throw new Error(
      "Повреждённая запись восстановления: невозможно повторить исходный запрос точно.",
    );
  return { kind, body: value.body, input, phase: value.phase } as ChangeAttempt;
}
export function readChangeRecovery(key: string): ChangeAttempt | null {
  const raw = storedChange(key);
  return raw === null ? null : decodeChangeRecovery(raw);
}
export function inspectChangeRecovery(key: string): ChangeRecoverySlot {
  let raw: string | null = null;
  try {
    raw = storedChange(key);
    return { key, raw, attempt: raw === null ? null : decodeChangeRecovery(raw), error: null };
  } catch (cause) {
    return {
      key,
      raw,
      attempt: null,
      error: cause instanceof Error ? cause : storageError("прочитать", cause),
    };
  }
}
export function changeCreateRecoveryKey(projectId: string): string {
  return `backend-change-create:${projectId}`;
}
export function discoverChangeCreateRecovery(projectId: string): ChangeRecoverySlot[] {
  const key = changeCreateRecoveryKey(projectId);
  const direct = inspectChangeRecovery(key);
  if (direct.error || direct.raw !== null) return [direct];
  try {
    const legacy: string[] = [];
    for (let index = 0; index < sessionStorage.length; index++) {
      const candidate = sessionStorage.key(index);
      if (candidate?.startsWith(`${key}:`)) legacy.push(candidate);
    }
    return legacy.length ? legacy.sort().map(inspectChangeRecovery) : [direct];
  } catch (cause) {
    return [{ ...direct, error: storageError("найти", cause) }];
  }
}
export function writeChangeRecovery(
  key: string,
  attempt: ChangeAttempt | null,
  expectedRaw?: string | null,
): string | null {
  if (attempt && JSON.stringify(attempt.input) !== attempt.body)
    throw new Error("Исходные байты запроса изменены");
  const current = storedChange(key);
  const raw = attempt
    ? JSON.stringify({ version: 1, kind: attempt.kind, body: attempt.body, phase: attempt.phase })
    : null;
  if (raw !== null) decodeChangeRecovery(raw);
  if (current === raw) return raw;
  if (expectedRaw !== undefined && current !== expectedRaw)
    throw new Error("Запись восстановления изменилась. Перечитайте её перед продолжением.");
  try {
    if (raw === null) sessionStorage.removeItem(key);
    else sessionStorage.setItem(key, raw);
  } catch (cause) {
    throw storageError(raw === null ? "удалить" : "сохранить", cause);
  }
  if (storedChange(key) !== raw)
    throw new Error("Не удалось подтвердить запись восстановления. Запрос не отправлен.");
  window.dispatchEvent(new Event("backend-recovery-change"));
  return raw;
}

/** Discover every owner-bound legacy slot without silently losing it on editor unmount. */
export function discoverProjectChangeRecovery(projectId: string): ChangeRecoverySlot[] {
  try {
    const keys = new Set<string>();
    for (let i = 0; i < sessionStorage.length; i++) {
      const key = sessionStorage.key(i);
      if (
        key &&
        (key === changeCreateRecoveryKey(projectId) ||
          key.startsWith(`${changeCreateRecoveryKey(projectId)}:`) ||
          key.startsWith(`backend-change-attempt:${projectId}:`))
      )
        keys.add(key);
    }
    return [...keys].sort().map(inspectChangeRecovery);
  } catch (cause) {
    return [
      {
        key: changeCreateRecoveryKey(projectId),
        raw: null,
        attempt: null,
        error: storageError("найти", cause),
      },
    ];
  }
}

import { parseBrowserSafeJson } from "@/api/preciseJson";
import { captureImportAttempt, importVersion, type ImportAttempt } from "./backendImportAttempts";
import { hashBackendImportCommands } from "./backendImportHash";
import {
  maxCollectorBytes,
  maxSourceFileBytes,
  parseChangeManifest,
  parseCollectorBatch,
  parseSourceInventory,
  parseSourceManifest,
  sourceProfiles,
  sourceUUID,
} from "./backendSourceInputs";

export type ImportRecoveryState = {
  attempt: ImportAttempt | null;
  raw: string | null;
  error: Error | null;
};

const maxRecoveryBytes = 12 * 1024 * 1024;

export function importRecoveryKey(projectId: string): string {
  return `mocker:backend-import-recovery:v1:${sourceUUID(projectId)}`;
}

function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("Запись восстановления должна содержать JSON-объект.");
  return value as Record<string, unknown>;
}

function keys(value: Record<string, unknown>, required: string[], optional: string[] = []) {
  if (
    required.some((key) => !Object.hasOwn(value, key)) ||
    Object.keys(value).some((key) => !required.includes(key) && !optional.includes(key))
  )
    throw new Error("В записи восстановления отсутствует поле или присутствует лишнее поле.");
}

function bounded(raw: string, limit: number) {
  if (raw.length > limit || new TextEncoder().encode(raw).length > limit)
    throw new Error(`Размер записи восстановления превышает ${limit / 1024 / 1024} МиБ.`);
}

function text(value: unknown, maxLength = Number.MAX_SAFE_INTEGER): string {
  if (
    typeof value !== "string" ||
    !value.trim() ||
    value.length > maxLength ||
    /\p{Cc}/u.test(value)
  )
    throw new Error("В записи восстановления нужна непустая строка без управляющих символов.");
  return value;
}

function token(value: unknown) {
  if (typeof value !== "string" || !/^[!-~]{1,128}$/.test(value))
    throw new Error("В записи восстановления неверный ключ идемпотентности.");
}

function hash(value: unknown) {
  if (typeof value !== "string" || !/^[a-f0-9]{64}$/.test(value))
    throw new Error("В записи восстановления неверный SHA-256.");
}

function version(value: unknown) {
  if (typeof value !== "number") throw new Error("В записи восстановления неверная версия.");
  importVersion(value);
}

function validateBegin(body: Record<string, unknown>) {
  keys(
    body,
    [
      "expectedVersion",
      "baseRevisionId",
      "idempotencyKey",
      "manifest",
      "inventory",
      "mode",
      "profile",
      "sourceScope",
      "scopeStatus",
      "syncPolicy",
    ],
    ["profileExtension", "changeManifest"],
  );
  version(body.expectedVersion);
  sourceUUID(body.baseRevisionId);
  token(body.idempotencyKey);
  if (body.mode !== "composed" || body.profile !== "composed-source-v1")
    throw new Error("Восстановление поддерживает только составную синхронизацию source6.");
  const manifest = parseSourceManifest(body.manifest);
  if (
    manifest.provider.profiles.length !== sourceProfiles.length ||
    sourceProfiles.some((profile) => !manifest.provider.profiles.includes(profile))
  )
    throw new Error("В записи восстановления нужны все шесть профилей source6.");
  parseSourceInventory(body.inventory);
  const scope = object(body.sourceScope);
  switch (scope.kind) {
    case "add_repository":
      keys(scope, ["kind"]);
      break;
    case "add_provider":
      keys(scope, ["kind", "repositoryId"]);
      sourceUUID(scope.repositoryId);
      break;
    case "reconcile":
      keys(scope, ["kind", "repositoryId", "providerNamespace"]);
      sourceUUID(scope.repositoryId);
      text(scope.providerNamespace, 200);
      if (scope.providerNamespace !== manifest.provider.namespace)
        throw new Error("Namespace запроса не совпадает с манифестом.");
      break;
    case "migrate_provider":
      keys(scope, ["kind", "repositoryId", "fromProviderNamespace", "fromSnapshotId", "reason"]);
      sourceUUID(scope.repositoryId);
      sourceUUID(scope.fromSnapshotId);
      text(scope.fromProviderNamespace, 200);
      text(scope.reason);
      break;
    default:
      throw new Error("Неизвестная область синхронизации в записи восстановления.");
  }
  const status = object(body.scopeStatus);
  keys(status, ["status", "gaps"]);
  if (!Array.isArray(status.gaps)) throw new Error("Неверные пробелы области синхронизации.");
  status.gaps.forEach((gap) => text(gap));
  if (
    !(
      (status.status === "complete" && status.gaps.length === 0) ||
      (status.status === "partial" && status.gaps.length > 0)
    )
  )
    throw new Error("Неверная полнота области синхронизации.");
  if (Object.hasOwn(body, "profileExtension")) {
    const extension = object(body.profileExtension);
    keys(extension, ["fromProfile", "toProfile"]);
    if (
      extension.fromProfile !== "events-service-v1" ||
      extension.toProfile !== "composed-source-v1"
    )
      throw new Error("Неверное расширение профиля source6.");
  }
  if (body.syncPolicy === "incremental-source-v1") {
    if (scope.kind !== "reconcile" || Object.hasOwn(body, "profileExtension"))
      throw new Error("Неверная область incremental-синхронизации.");
    parseChangeManifest(body.changeManifest);
  } else if (body.syncPolicy !== "whole-source-v1" || Object.hasOwn(body, "changeManifest")) {
    throw new Error("Неверная политика синхронизации или ChangeManifest.");
  }
}

async function validateRecord(projectId: string, raw: string): Promise<ImportAttempt> {
  bounded(raw, maxRecoveryBytes);
  const envelope = object(parseBrowserSafeJson(raw));
  if (
    envelope.version !== 1 ||
    envelope.projectId !== projectId ||
    JSON.stringify(envelope) !== raw
  )
    throw new Error("Неверный формат, проект или версия записи восстановления.");
  sourceUUID(envelope.projectId);
  const kind = envelope.kind;
  if (typeof kind !== "string" || !["begin", "batch", "preview", "commit", "abort"].includes(kind))
    throw new Error("Неизвестная операция в записи восстановления.");
  keys(envelope, [
    "version",
    "kind",
    "projectId",
    "body",
    ...(kind === "begin" ? [] : ["sessionId"]),
    ...(kind === "batch" ? ["batchId"] : []),
  ]);
  if (kind !== "begin") sourceUUID(envelope.sessionId);
  if (kind === "batch") sourceUUID(envelope.batchId);
  if (typeof envelope.body !== "string")
    throw new Error("Тело запроса должно быть сохранено строкой JSON.");
  bounded(envelope.body, kind === "begin" ? maxSourceFileBytes : maxCollectorBytes);
  const body = object(parseBrowserSafeJson(envelope.body));
  if (JSON.stringify(body) !== envelope.body)
    throw new Error("Браузер не может точно восстановить сохранённое тело запроса.");
  switch (kind) {
    case "begin":
      validateBegin(body);
      break;
    case "batch": {
      keys(body, ["expectedImportVersion", "payloadHash", "commands"]);
      version(body.expectedImportVersion);
      hash(body.payloadHash);
      const commands = parseCollectorBatch(body.commands);
      for (const command of commands) {
        if (command.op === "resolve_assertion") sourceUUID(command.resolution.decisionId);
        if (command.op === "claim_identity") sourceUUID(command.claimIdentity.decisionId);
      }
      if ((await hashBackendImportCommands(commands)) !== body.payloadHash)
        throw new Error("Хеш сохранённого пакета не совпадает с командами.");
      break;
    }
    case "preview":
      keys(body, ["expectedImportVersion", "baseRevisionId"]);
      version(body.expectedImportVersion);
      sourceUUID(body.baseRevisionId);
      break;
    case "commit":
      keys(body, ["expectedVersion", "expectedImportVersion", "candidateHash", "idempotencyKey"]);
      version(body.expectedVersion);
      version(body.expectedImportVersion);
      hash(body.candidateHash);
      token(body.idempotencyKey);
      break;
    case "abort":
      keys(body, ["expectedImportVersion", "idempotencyKey"]);
      version(body.expectedImportVersion);
      token(body.idempotencyKey);
  }
  const { version: _version, body: _body, ...identity } = envelope;
  return captureImportAttempt({ ...identity, body } as ImportAttempt);
}

function storageError(action: string, cause: unknown): Error {
  return new Error(
    `${action} запись восстановления в хранилище браузера. Разрешите хранение данных для этого сайта или освободите место; затем повторите.`,
    { cause },
  );
}

function stored(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch (cause) {
    throw storageError("Не удалось прочитать", cause);
  }
}

export async function loadImportRecovery(projectId: string): Promise<ImportRecoveryState> {
  let raw: string | null = null;
  try {
    raw = stored(importRecoveryKey(projectId));
    return {
      attempt: raw === null ? null : await validateRecord(projectId, raw),
      raw,
      error: null,
    };
  } catch (cause) {
    const error =
      raw === null && cause instanceof Error
        ? cause
        : new Error(
            "Сохранённая попытка повреждена или несовместима. Повтор отключён. Проверьте состояние сервера и явно подтвердите удаление этой записи.",
            { cause },
          );
    return { attempt: null, raw, error };
  }
}

export async function writeImportRecovery(
  projectId: string,
  attempt: ImportAttempt,
  expectedRaw: string | null,
): Promise<string> {
  const key = importRecoveryKey(projectId);
  const { body, ...identity } = attempt;
  const raw = JSON.stringify({ version: 1, ...identity, body: JSON.stringify(body) });
  await validateRecord(projectId, raw);
  // No await between compare, write and readback: a later validation result must
  // never overwrite a record that changed while a batch hash was being checked.
  const current = stored(key);
  if (current !== expectedRaw || (current !== null && current !== raw))
    throw new Error(
      "Сохранённая попытка изменилась. Перечитайте восстановление перед продолжением.",
    );
  if (current === raw) return raw;
  try {
    localStorage.setItem(key, raw);
  } catch (cause) {
    throw storageError("Не удалось сохранить", cause);
  }
  if (stored(key) !== raw)
    throw new Error(
      "Не удалось проверить сохранение попытки. Запрос не отправлен; перечитайте восстановление.",
    );
  return raw;
}

export function clearImportRecovery(projectId: string, expectedRaw: string): void {
  const key = importRecoveryKey(projectId);
  if (stored(key) !== expectedRaw)
    throw new Error("Сохранённая попытка изменилась. Перечитайте восстановление перед удалением.");
  try {
    localStorage.removeItem(key);
  } catch (cause) {
    throw storageError("Не удалось удалить", cause);
  }
  if (stored(key) !== null)
    throw new Error(
      "Не удалось проверить удаление записи восстановления. Перечитайте её перед продолжением.",
    );
}

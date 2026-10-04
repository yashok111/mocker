import type {
  BackendChangeManifest,
  BackendComposedImportSession,
  BackendImportCommand,
  BackendInventoryItem,
  BackendManifestFile,
  BackendProvider,
  BackendSourceManifest,
  BackendSourceScope,
  BackendSourceScopeStatus,
  BackendSourceSnapshot,
  BeginBackendImportRequest,
} from "@/api/generated/schemas";
import {
  BackendInventoryItemCategory,
  BackendInventoryItemStatus,
  BackendManifestFileAnalysisStatus,
  BackendProviderMethod,
  BackendSnapshotManifestConsistency,
} from "@/api/generated/schemas";
import { parseBrowserSafeJson } from "@/api/preciseJson";

export const sourceProfiles = [
  "foundation-graph-v1",
  "relational-graph-v1",
  "runtime-flow-v1",
  "field-lineage-v1",
  "events-service-v1",
  "composed-source-v1",
] as const;
export const maxSourceFileBytes = 8 * 1024 * 1024;
export const maxCollectorBytes = 1024 * 1024;
export type SyncPartition = {
  repositoryId: string;
  snapshotId: string;
  provider: BackendProvider;
  snapshot?: BackendSourceSnapshot;
};

function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("Требуется JSON-объект.");
  return value as Record<string, unknown>;
}
function keys(value: Record<string, unknown>, required: string[], optional: string[] = []) {
  if (
    required.some((key) => !Object.hasOwn(value, key)) ||
    Object.keys(value).some((key) => !required.includes(key) && !optional.includes(key))
  )
    throw new Error("В JSON отсутствует обязательное поле или присутствует лишнее поле.");
}
function text(value: unknown, label: string): string {
  if (typeof value !== "string" || !value.trim() || /\p{Cc}/u.test(value))
    throw new Error(`${label}: требуется непустая строка без управляющих символов.`);
  return value;
}
function strings(value: unknown): string[] {
  if (!Array.isArray(value) || value.some((item) => typeof item !== "string" || !item.trim()))
    throw new Error("Требуется массив непустых строк.");
  return value as string[];
}
function integer(value: unknown): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0)
    throw new Error("Счётчик должен быть точным неотрицательным целым числом.");
  return value;
}
function enumeration<T extends string>(value: unknown, values: readonly T[]): T {
  if (typeof value !== "string" || !values.includes(value as T))
    throw new Error("Неизвестное значение поля JSON.");
  return value as T;
}
export function sourceUUID(value: unknown): string {
  const id = text(value, "UUID");
  if (
    !/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(id) ||
    id === "00000000-0000-0000-0000-000000000000"
  )
    throw new Error("Требуется канонический UUID.");
  return id;
}
function fileHash(value: unknown): string {
  const hash = text(value, "Хеш файла");
  if (!/^[a-f0-9]{64}$/.test(hash)) throw new Error("Требуется объявленный SHA-256 файла.");
  return hash;
}
function filePath(value: unknown): string {
  const path = text(value, "Путь файла");
  if (
    path.startsWith("/") ||
    /[\\:]/.test(path) ||
    path.split("/").some((part) => !part || part === "." || part === "..")
  )
    throw new Error("Путь должен быть нормализованным и относительным.");
  return path;
}

export async function readSourceJson(file: File, limit = maxSourceFileBytes): Promise<unknown> {
  if (file.size > limit) throw new Error(`Файл превышает ограничение ${limit / 1024 / 1024} МиБ.`);
  const raw = await file.text();
  if (new TextEncoder().encode(raw).length > limit)
    throw new Error("JSON превышает ограничение размера.");
  return parseBrowserSafeJson(raw);
}

export function parseSourceManifest(value: unknown): BackendSourceManifest {
  const m = object(value),
    p = object(m.provider),
    s = object(m.snapshot);
  keys(m, ["repositoryName", "provider", "snapshot"]);
  keys(p, ["name", "version", "namespace", "method", "profiles", "limitations"]);
  keys(s, ["dirty", "consistency", "capturedAt", "files"], ["commit"]);
  if (typeof s.dirty !== "boolean" || !Array.isArray(s.files) || s.files.length > 100000)
    throw new Error("Неверный snapshot или число файлов.");
  const profiles = strings(p.profiles);
  if (
    !profiles.length ||
    new Set(profiles).size !== profiles.length ||
    profiles.some((profile) => !sourceProfiles.includes(profile as (typeof sourceProfiles)[number]))
  )
    throw new Error("Неверный набор профилей провайдера.");
  const paths = new Set<string>();
  const files: BackendManifestFile[] = s.files.map((raw) => {
    const f = object(raw);
    keys(f, ["path", "contentHash", "fileType", "analysisStatus"], ["reason"]);
    const path = filePath(f.path);
    if (paths.has(path)) throw new Error(`Повторяется путь ${path}.`);
    paths.add(path);
    return {
      path,
      contentHash: fileHash(f.contentHash),
      fileType: text(f.fileType, "Тип файла"),
      analysisStatus: enumeration(
        f.analysisStatus,
        Object.values(BackendManifestFileAnalysisStatus),
      ),
      ...(f.reason === undefined ? {} : { reason: text(f.reason, "Причина") }),
    };
  });
  const capturedAt = text(s.capturedAt, "Время снимка");
  if (!Number.isFinite(Date.parse(capturedAt))) throw new Error("Неверное время снимка.");
  return {
    repositoryName: text(m.repositoryName, "Репозиторий"),
    provider: {
      name: text(p.name, "Провайдер"),
      version: text(p.version, "Версия провайдера"),
      namespace: text(p.namespace, "Namespace"),
      method: enumeration(p.method, Object.values(BackendProviderMethod)),
      profiles,
      limitations: strings(p.limitations),
    },
    snapshot: {
      dirty: s.dirty,
      consistency: enumeration(s.consistency, Object.values(BackendSnapshotManifestConsistency)),
      capturedAt,
      files,
      ...(s.commit === undefined ? {} : { commit: text(s.commit, "Commit") }),
    },
  };
}

export function parseSourceInventory(value: unknown): BackendInventoryItem[] {
  if (!Array.isArray(value) || value.length !== 9)
    throw new Error("Inventory должен содержать все девять категорий.");
  const seen = new Set<string>();
  return value.map((raw) => {
    const row = object(raw);
    keys(row, [
      "category",
      "status",
      "knownCount",
      "denominator",
      "discoverySource",
      "gaps",
      "reason",
    ]);
    const category = enumeration(row.category, Object.values(BackendInventoryItemCategory));
    if (seen.has(category)) throw new Error("Категории inventory не должны повторяться.");
    seen.add(category);
    if (typeof row.reason !== "string") throw new Error("Причина inventory должна быть строкой.");
    return {
      category,
      status: enumeration(row.status, Object.values(BackendInventoryItemStatus)),
      knownCount: integer(row.knownCount),
      denominator: row.denominator === null ? null : integer(row.denominator),
      discoverySource: text(row.discoverySource, "Источник inventory"),
      gaps: strings(row.gaps),
      reason: row.reason,
    };
  });
}

export function parseChangeManifest(value: unknown): BackendChangeManifest {
  const m = object(value);
  keys(m, ["scope", "files", "affectedRoots"]);
  if (
    m.scope !== "affected-subgraph" ||
    !Array.isArray(m.files) ||
    !Array.isArray(m.affectedRoots) ||
    m.files.length > 100000 ||
    m.affectedRoots.length > 100000
  )
    throw new Error("Неверный ChangeManifest.");
  const files = m.files.map((raw) => {
    const f = object(raw),
      kind = enumeration(f.kind, ["added", "modified", "deleted"] as const);
    keys(f, [
      "kind",
      "path",
      ...(kind !== "added" ? ["beforeHash"] : []),
      ...(kind !== "deleted" ? ["afterHash"] : []),
    ]);
    const path = filePath(f.path);
    if (kind === "added") return { kind, path, afterHash: fileHash(f.afterHash) };
    if (kind === "deleted") return { kind, path, beforeHash: fileHash(f.beforeHash) };
    return { kind, path, beforeHash: fileHash(f.beforeHash), afterHash: fileHash(f.afterHash) };
  });
  if (new Set(files.map((file) => file.path)).size !== files.length)
    throw new Error("Файлы ChangeManifest не должны повторяться.");
  const affectedRoots = m.affectedRoots.map((raw) => {
    const r = object(raw);
    keys(r, ["recordType", "id"]);
    return {
      recordType: enumeration(r.recordType, ["node", "edge", "evidence"] as const),
      id: sourceUUID(r.id),
    };
  });
  if (new Set(affectedRoots.map((r) => `${r.recordType}:${r.id}`)).size !== affectedRoots.length)
    throw new Error("Корни ChangeManifest не должны повторяться.");
  return { scope: "affected-subgraph", files, affectedRoots };
}

export function sourceFileChanges(
  before: readonly BackendManifestFile[],
  after: readonly BackendManifestFile[],
): BackendChangeManifest["files"] {
  const old = new Map(before.map((file) => [file.path, file.contentHash])),
    next = new Map(after.map((file) => [file.path, file.contentHash]));
  return [...new Set([...old.keys(), ...next.keys()])]
    .sort()
    .flatMap((path): BackendChangeManifest["files"] => {
      const beforeHash = old.get(path),
        afterHash = next.get(path);
      if (beforeHash === afterHash) return [];
      if (beforeHash === undefined) return [{ kind: "added", path, afterHash: afterHash! }];
      if (afterHash === undefined) return [{ kind: "deleted", path, beforeHash }];
      return [{ kind: "modified", path, beforeHash, afterHash }];
    });
}

export function parseCollectorBatch(value: unknown): BackendImportCommand[] {
  let commands = value;
  if (!Array.isArray(value)) {
    const envelope = object(value);
    keys(envelope, ["commands"]);
    commands = envelope.commands;
  }
  if (!Array.isArray(commands) || commands.length < 1 || commands.length > 500)
    throw new Error("Пакет должен содержать от 1 до 500 команд.");
  const payloads: Record<string, string> = {
    upsert_node: "node",
    upsert_edge: "edge",
    upsert_evidence: "evidence",
    remove: "remove",
    map_identity: "identity",
    delete_assertion: "deletion",
    claim_identity: "claimIdentity",
    resolve_assertion: "resolution",
  };
  for (const raw of commands) {
    const command = object(raw),
      op = text(command.op, "Команда"),
      field = payloads[op];
    if (!field) throw new Error("Неизвестная команда коллектора.");
    keys(command, ["op", field]);
    const body = object(command[field]);
    if (op === "upsert_node")
      keys(body, ["externalKey", "kind", "name", "attributes", "evidenceKeys"], ["parentRef"]);
    if (op === "upsert_edge")
      keys(body, ["externalKey", "kind", "fromRef", "toRef", "attributes", "evidenceKeys"]);
    if (op === "upsert_node" || op === "upsert_edge") {
      text(body.externalKey, "Ключ объекта");
      text(body.kind, "Вид объекта");
      object(body.attributes);
      strings(body.evidenceKeys);
    }
    if (body.parentRef === null) throw new Error("parentRef не может быть null.");
    if (op === "upsert_evidence") {
      text(body.externalKey, "Ключ основания");
      text(body.subjectKey, "Ключ субъекта");
      enumeration(body.subjectType, ["node", "edge"]);
      object(body.source);
    }
  }
  if (new TextEncoder().encode(JSON.stringify(commands)).length > maxCollectorBytes)
    throw new Error("Пакет превышает 1 МиБ.");
  return commands as BackendImportCommand[];
}

export function validateSessionProof(
  commands: readonly BackendImportCommand[],
  session: BackendComposedImportSession,
) {
  for (const command of commands) {
    if (command.op !== "upsert_evidence") continue;
    const source = command.evidence.source;
    const file = session.manifest.snapshot.files.find((file) => file.path === source.file);
    if (
      source.repositoryId !== session.repositoryId ||
      source.snapshotId !== session.snapshotId ||
      !file ||
      file.contentHash !== source.contentHash ||
      file.analysisStatus !== "analyzed"
    )
      throw new Error(
        "Основание должно принадлежать входящему репозиторию, снимку и проанализированному файлу этой сессии.",
      );
  }
}

export type SourceBeginInput = {
  projectVersion: number;
  baseRevisionId: string;
  baseSchema: string;
  emptyBase?: boolean;
  sourceScope: BackendSourceScope;
  scopeStatus: BackendSourceScopeStatus;
  policy: "whole-source-v1" | "incremental-source-v1";
  extension: boolean;
  manifest: BackendSourceManifest;
  inventory: BackendInventoryItem[];
  partitions: readonly SyncPartition[];
  changeManifest?: BackendChangeManifest;
  idempotencyKey: string;
};
export function extensionRequired(
  baseSchema: string,
  scope: BackendSourceScope,
  partitions: readonly SyncPartition[],
): boolean {
  if (baseSchema === "5") return true;
  return (
    scope.kind === "reconcile" &&
    partitions.some(
      (part) =>
        part.repositoryId === scope.repositoryId &&
        part.provider.namespace === scope.providerNamespace &&
        part.provider.profiles.length === 5 &&
        sourceProfiles.slice(0, 5).every((profile) => part.provider.profiles.includes(profile)),
    )
  );
}
export function buildSourceBegin(input: SourceBeginInput): BeginBackendImportRequest {
  const { sourceScope: scope, manifest, partitions } = input;
  if (integer(input.projectVersion) < 1) throw new Error("Выберите точную версию проекта.");
  sourceUUID(input.baseRevisionId);
  if (
    !["5", "6"].includes(input.baseSchema) &&
    !(input.baseSchema === "1" && input.emptyBase && scope.kind === "add_repository")
  )
    throw new Error("Сначала выполните существующий переход источника до events-service-v1.");
  if (
    manifest.provider.profiles.length !== 6 ||
    sourceProfiles.some((profile) => !manifest.provider.profiles.includes(profile))
  )
    throw new Error("Для source6 нужны ровно шесть профилей провайдера.");
  const selected =
    scope.kind === "reconcile"
      ? partitions.find(
          (part) =>
            part.repositoryId === scope.repositoryId &&
            part.provider.namespace === scope.providerNamespace,
        )
      : scope.kind === "migrate_provider"
        ? partitions.find(
            (part) =>
              part.repositoryId === scope.repositoryId &&
              part.provider.namespace === scope.fromProviderNamespace &&
              part.snapshotId === scope.fromSnapshotId,
          )
        : undefined;
  if ((scope.kind === "reconcile" || scope.kind === "migrate_provider") && !selected)
    throw new Error("Выберите точный сохранённый раздел источника.");
  if (selected && ![5, 6].includes(selected.provider.profiles.length))
    throw new Error(
      "Для выбранного раздела сначала нужен соседний переход профиля до events-service-v1.",
    );
  if (
    scope.kind !== "add_repository" &&
    !partitions.some((part) => part.repositoryId === scope.repositoryId)
  )
    throw new Error("Репозиторий отсутствует в выбранной базе.");
  if (
    scope.kind === "reconcile" &&
    selected &&
    ["name", "version", "namespace", "method"].some(
      (key) =>
        manifest.provider[key as keyof BackendProvider] !==
        selected.provider[key as keyof BackendProvider],
    )
  )
    throw new Error("Описание входящего провайдера не совпадает с выбранным разделом.");
  if (
    (scope.kind === "add_provider" || scope.kind === "migrate_provider") &&
    partitions.some(
      (part) =>
        part.repositoryId === scope.repositoryId &&
        part.provider.namespace === manifest.provider.namespace,
    )
  )
    throw new Error("Для нового провайдера нужен ещё не занятый namespace.");
  if (scope.kind === "migrate_provider") text(scope.reason, "Причина миграции");
  const extension = extensionRequired(input.baseSchema, scope, partitions);
  if (input.scopeStatus.status === "partial" && !input.scopeStatus.gaps.some((gap) => gap.trim()))
    throw new Error("Укажите пробелы частичной области.");
  if (extension !== input.extension)
    throw new Error(
      extension
        ? "Подтвердите явное расширение пяти профилей до source6."
        : "Этот раздел уже составной: расширение профиля не требуется.",
    );
  if (input.policy === "incremental-source-v1") {
    if (scope.kind !== "reconcile" || extension || input.baseSchema !== "6")
      throw new Error(
        "Incremental доступен только для существующего составного раздела без расширения.",
      );
    if (!input.changeManifest) throw new Error("Загрузите ChangeManifest.");
    const expected = sourceFileChanges(selected?.snapshot?.files ?? [], manifest.snapshot.files);
    const changes = new Map(input.changeManifest.files.map((file) => [file.path, file]));
    if (
      selected?.snapshot &&
      (changes.size !== expected.length ||
        expected.some((file) => JSON.stringify(file) !== JSON.stringify(changes.get(file.path))))
    )
      throw new Error("ChangeManifest не соответствует полному изменению хешей файлов.");
  }
  return {
    expectedVersion: input.projectVersion,
    baseRevisionId: input.baseRevisionId,
    idempotencyKey: input.idempotencyKey,
    profile: "composed-source-v1",
    mode: "composed",
    sourceScope: scope,
    scopeStatus: input.scopeStatus,
    syncPolicy: input.policy,
    manifest,
    inventory: input.inventory,
    ...(extension
      ? {
          profileExtension: {
            fromProfile: "events-service-v1",
            toProfile: "composed-source-v1",
          } as const,
        }
      : {}),
    ...(input.policy === "incremental-source-v1" ? { changeManifest: input.changeManifest! } : {}),
  };
}

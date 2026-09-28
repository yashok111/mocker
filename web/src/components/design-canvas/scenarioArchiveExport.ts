import type { Zippable } from "fflate";
import type { ScenarioArchiveManifest, ScenarioExportFormat } from "@/api/generated/schemas";
import type { DesignScenarioRevision } from "./designScenarioApi";
import { loadScenarioArtifact } from "./scenarioExportApi";

export interface ScenarioArchiveItem {
  format: ScenarioExportFormat | "svg" | "png";
  contractId?: string;
}
export const MAX_ARCHIVE_ITEMS = 32;
const MAX_ARCHIVE_BYTES = 32 * 1024 * 1024;
const extensions: Record<ScenarioExportFormat | "svg" | "png", string> = {
  plantuml: "puml",
  mermaid: "mmd",
  "openapi-json": "json",
  "openapi-yaml": "yaml",
  "asyncapi-json": "json",
  "asyncapi-yaml": "yaml",
  postman: "postman_collection.json",
  curl: "sh",
  markdown: "md",
  html: "html",
  svg: "svg",
  png: "png",
};

export function archiveItemKey(item: ScenarioArchiveItem): string {
  return JSON.stringify([item.format, item.contractId ?? ""]);
}

/** Build only from the pinned revision; callers discard results after unmount/refresh. */
export async function buildScenarioArchive(
  revision: DesignScenarioRevision,
  items: ScenarioArchiveItem[],
  {
    isCancelled = () => false,
    maxBytes = MAX_ARCHIVE_BYTES,
  }: { isCancelled?: () => boolean; maxBytes?: number } = {},
): Promise<Blob> {
  const limit = Math.min(maxBytes, MAX_ARCHIVE_BYTES);
  function checkCancelled(): void {
    if (isCancelled()) throw new Error("Сборка архива отменена");
  }
  function checkSize(size: number): void {
    if (size > limit) throw new Error("Архив превышает допустимый размер. Выберите меньше файлов.");
  }
  if (items.length === 0) throw new Error("Выберите файлы для ZIP");
  if (items.length > MAX_ARCHIVE_ITEMS) throw new Error("Выберите не более 32 файлов");
  const seen = new Set<string>();
  const filenames = items.map((item) => {
    const key = archiveItemKey(item);
    if (seen.has(key)) throw new Error("В ZIP не должно быть повторных файлов");
    seen.add(key);
    if (!Object.hasOwn(extensions, item.format)) throw new Error("Неизвестный формат ZIP");
    const isAsyncAPI = item.format === "asyncapi-json" || item.format === "asyncapi-yaml";
    const isContract =
      isAsyncAPI || item.format === "openapi-json" || item.format === "openapi-yaml";
    const contracts = isAsyncAPI
      ? (revision.document.eventModel?.contracts ?? [])
      : revision.document.contracts;
    const index = contracts.findIndex((c) => c.id === item.contractId);
    if ((isContract && index < 0) || (!isContract && item.contractId))
      throw new Error("Проверьте выбранный контракт");
    return `scenario-${revision.scenarioId}-r${revision.id}.${isContract ? `${isAsyncAPI ? "asyncapi" : "api"}-${index + 1}.` : ""}${extensions[item.format]}`;
  });
  const manifest: ScenarioArchiveManifest = {
    schemaVersion: 1,
    kind: "mocker-scenario-artifacts",
    restorable: false,
    scenario: {
      id: revision.scenarioId,
      title: revision.document.title,
      revisionId: revision.id,
      version: revision.version,
      sourceHash: revision.hash,
    },
    files: [],
  };
  const files: Zippable = Object.create(null);
  // A fixed local DOS date makes the ZIP stable across repeated downloads.
  const fileOptions = { mtime: new Date(2000, 0, 1), level: 0 as const };
  const encoder = new TextEncoder();
  let bytes = 0;
  for (const [index, item] of items.entries()) {
    checkCancelled();
    const path = filenames[index]!;
    let payload: Uint8Array<ArrayBuffer>;
    let mediaType: string;
    let diagnostics: ScenarioArchiveManifest["files"][number]["diagnostics"] = [];
    if (item.format === "svg" || item.format === "png") {
      const { exportSequenceImage } = await import("./sequenceImageExport");
      checkCancelled();
      const blob = await exportSequenceImage(revision.document, item.format);
      checkCancelled();
      checkSize(bytes + blob.size);
      payload = new Uint8Array(await blob.arrayBuffer());
      mediaType = blob.type;
    } else {
      const artifact = await loadScenarioArtifact(
        revision.scenarioId,
        revision.id,
        item.format,
        item.contractId,
      );
      checkCancelled();
      if (
        artifact.scenarioId !== revision.scenarioId ||
        artifact.revisionId !== revision.id ||
        artifact.sourceHash !== revision.hash ||
        artifact.format !== item.format ||
        artifact.filename !== path
      ) {
        throw new Error(
          "Результат не соответствует выбранной ревизии или формату. Повторите экспорт.",
        );
      }
      checkSize(bytes + artifact.content.length);
      payload = encoder.encode(artifact.content);
      mediaType = artifact.mediaType;
      diagnostics = artifact.diagnostics ?? [];
    }
    bytes += payload.byteLength;
    checkSize(bytes);
    const hash = globalThis.crypto?.subtle
      ? new Uint8Array(await crypto.subtle.digest("SHA-256", payload))
      : (await import("@noble/hashes/sha2.js")).sha256(payload);
    checkCancelled();
    manifest.files.push({
      path,
      format: item.format,
      ...(item.contractId ? { contractId: item.contractId } : {}),
      mediaType,
      bytes: payload.byteLength,
      sha256: Array.from(hash, (n) => n.toString(16).padStart(2, "0")).join(""),
      diagnostics,
    });
    files[path] = [payload, fileOptions];
  }
  const manifestJSON = JSON.stringify(manifest, null, 2) + "\n";
  checkSize(bytes + manifestJSON.length);
  const manifestBytes = encoder.encode(manifestJSON);
  checkSize(bytes + manifestBytes.byteLength);
  files["manifest.json"] = [manifestBytes, fileOptions];
  const { zipSync } = await import("fflate");
  checkCancelled();
  const archive = zipSync(files, { level: 0 });
  checkSize(archive.byteLength);
  return new Blob([new Uint8Array(archive)], { type: "application/zip" });
}

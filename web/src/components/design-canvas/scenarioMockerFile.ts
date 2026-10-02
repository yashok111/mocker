import { parseBrowserSafeJson } from "@/api/preciseJson";
import { isRecord } from "../api-designer/documentModel";
import { parseSavedCanvas } from "./canvasStorage";
import type { CanvasDocument } from "./types";

export const MAX_MOCKER_FILE_BYTES = 2_000_000;

function checkSize(text: string): void {
  if (new TextEncoder().encode(text).byteLength > MAX_MOCKER_FILE_BYTES)
    throw new Error("Размер файла .mocker превышает 2 МБ");
}

export function parseMockerScenario(text: string): {
  document: CanvasDocument;
  formDrafts: Record<string, string>;
} {
  checkSize(text);
  let value: unknown;
  try {
    value = parseBrowserSafeJson(text);
  } catch (error) {
    if (error instanceof SyntaxError) throw new Error("Не удалось разобрать JSON файла .mocker");
    throw error;
  }
  if (!isRecord(value) || value.kind !== "mocker.scenario")
    throw new Error("Файл не содержит сценарий .mocker");
  if (value.formatVersion !== 1) throw new Error("Неподдерживаемая версия файла .mocker");
  const result = parseSavedCanvas(
    JSON.stringify({ document: value.document, formDrafts: value.formDrafts }),
  );
  result.document.contracts = result.document.contracts.map((contract) => {
    const copy = { ...contract, mode: "copy" as const };
    delete copy.source;
    return copy;
  });
  return result;
}

export function serializeMockerScenario(
  document: CanvasDocument,
  formDrafts: Record<string, string>,
): string {
  const envelope = { kind: "mocker.scenario", formatVersion: 1, document, formDrafts };
  const portable = parseMockerScenario(JSON.stringify(envelope));
  const text = JSON.stringify({ ...envelope, ...portable });
  checkSize(text);
  return text;
}

export interface MockerTransferRevision {
  document: CanvasDocument;
  formDrafts: Record<string, string>;
  summary: string;
  source: "ui" | "mcp";
  createdAt: number;
}
export interface MockerTransferBundle {
  kind: "mocker.scenarios";
  formatVersion: 1;
  scenarios: { revisions: MockerTransferRevision[] }[];
}

export function parseMockerTransfer(text: string): {
  bundle: MockerTransferBundle;
  single: boolean;
} {
  checkSize(text);
  let value: unknown;
  try {
    value = parseBrowserSafeJson(text);
  } catch (error) {
    if (error instanceof SyntaxError) throw new Error("Не удалось разобрать JSON файла .mocker");
    throw error;
  }
  if (isRecord(value) && value.kind === "mocker.scenario") {
    const snapshot = parseMockerScenario(text);
    return {
      single: true,
      bundle: {
        kind: "mocker.scenarios",
        formatVersion: 1,
        scenarios: [
          { revisions: [{ ...snapshot, summary: "Импорт .mocker", source: "ui", createdAt: 0 }] },
        ],
      },
    };
  }
  if (!isRecord(value) || value.kind !== "mocker.scenarios" || value.formatVersion !== 1)
    throw new Error("Неподдерживаемый формат пакета .mocker");
  if (!Array.isArray(value.scenarios) || value.scenarios.length < 1 || value.scenarios.length > 20)
    throw new Error("Пакет должен содержать от 1 до 20 сценариев");
  const scenarios = value.scenarios.map((item: unknown) => {
    if (
      !isRecord(item) ||
      !Array.isArray(item.revisions) ||
      item.revisions.length < 1 ||
      item.revisions.length > 200
    )
      throw new Error("Сценарий должен содержать от 1 до 200 ревизий");
    const revisions = item.revisions.map((revision: unknown): MockerTransferRevision => {
      if (
        !isRecord(revision) ||
        typeof revision.summary !== "string" ||
        (revision.source !== "ui" && revision.source !== "mcp") ||
        !Number.isSafeInteger(revision.createdAt) ||
        (revision.createdAt as number) < 0
      )
        throw new Error("Некорректные метаданные истории сценария");
      const snapshot = parseMockerScenario(
        JSON.stringify({
          kind: "mocker.scenario",
          formatVersion: 1,
          document: revision.document,
          formDrafts: revision.formDrafts,
        }),
      );
      return {
        ...snapshot,
        summary: revision.summary,
        source: revision.source,
        createdAt: revision.createdAt as number,
      };
    });
    return { revisions };
  });
  return { single: false, bundle: { kind: "mocker.scenarios", formatVersion: 1, scenarios } };
}

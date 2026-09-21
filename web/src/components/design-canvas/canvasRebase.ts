import type { CanvasDocument } from "./types";

// Keep edits made after submission while adopting server-assigned API pins and
// operation identities. Presence matters: deleting an entity/field is an edit.
export function rebaseCanvasDocument(
  submitted: CanvasDocument,
  current: CanvasDocument,
  canonical: CanvasDocument,
): CanvasDocument {
  return rebaseValue(submitted, current, canonical) as CanvasDocument;
}

function rebaseValue(submitted: unknown, current: unknown, canonical: unknown): unknown {
  if (sameJSON(current, submitted)) return canonical;
  if (sameJSON(canonical, submitted)) return current;
  if (Array.isArray(submitted) && Array.isArray(current) && Array.isArray(canonical)) {
    if ([submitted, current, canonical].every(hasStableIDs)) {
      const submittedByID = new Map(submitted.map((item) => [item.id, item]));
      const canonicalByID = new Map(canonical.map((item) => [item.id, item]));
      const currentIDs = new Set(current.map((item) => item.id));
      return [
        ...current
          .filter(
            (item) => canonicalByID.has(item.id) || !sameJSON(item, submittedByID.get(item.id)),
          )
          .map((item) =>
            rebaseValue(submittedByID.get(item.id), item, canonicalByID.get(item.id) ?? item),
          ),
        ...canonical.filter((item) => !currentIDs.has(item.id) && !submittedByID.has(item.id)),
      ];
    }
    if (submitted.length === current.length && current.length === canonical.length) {
      return current.map((item, index) => rebaseValue(submitted[index], item, canonical[index]));
    }
    return current;
  }
  if (isRecord(submitted) && isRecord(current) && isRecord(canonical)) {
    const result: Record<string, unknown> = {};
    for (const key of new Set([...Object.keys(canonical), ...Object.keys(current)])) {
      if (!Object.hasOwn(current, key) && Object.hasOwn(submitted, key)) continue;
      if (!Object.hasOwn(canonical, key) && sameJSON(current[key], submitted[key])) continue;
      result[key] = rebaseValue(submitted[key], current[key], canonical[key]);
    }
    return result;
  }
  return current;
}

function sameJSON(left: unknown, right: unknown): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasStableIDs(value: unknown[]): value is Array<Record<string, unknown> & { id: string }> {
  return value.every((item) => isRecord(item) && typeof item.id === "string");
}

import type { BackendImportCommand } from "@/api/generated/schemas";

function jsonString(value: string): string {
  if (/[\uD800-\uDFFF]/u.test(value)) {
    throw new Error("JSON содержит некорректную Unicode-строку.");
  }
  return JSON.stringify(value);
}

// Go sorts UTF-8 strings by code point; JavaScript's default sort uses UTF-16
// code units and puts supplementary characters before some BMP characters.
function compareKeys(left: string, right: string): number {
  let a = 0;
  let b = 0;
  while (a < left.length && b < right.length) {
    const x = left.codePointAt(a)!;
    const y = right.codePointAt(b)!;
    if (x !== y) return x - y;
    a += x > 0xffff ? 2 : 1;
    b += y > 0xffff ? 2 : 1;
  }
  return left.length - right.length;
}

function canonical(value: unknown, parents: Set<object>, depth: number): string {
  if (depth > 128) throw new Error("JSON вложен слишком глубоко.");
  if (value === null) return "null";
  switch (typeof value) {
    case "string":
      return jsonString(value);
    case "boolean":
      return value ? "true" : "false";
    case "number":
      if (!Number.isFinite(value) || (Number.isInteger(value) && !Number.isSafeInteger(value))) {
        throw new Error("Число в JSON нельзя передать без потери точности.");
      }
      // This is also the number spelling JSON.stringify sends over the wire;
      // the server keeps that numeric text when canonicalizing command hashes.
      return JSON.stringify(value);
    case "object":
      return canonicalObject(value, parents, depth);
    default:
      throw new Error("JSON содержит неподдерживаемое значение.");
  }
}

function canonicalObject(value: object, parents: Set<object>, depth: number): string {
  if (parents.has(value)) throw new Error("JSON содержит циклическую ссылку.");
  parents.add(value);
  try {
    const descriptors = Object.getOwnPropertyDescriptors(value);
    const keys = Reflect.ownKeys(value);
    const read = (key: string): string => {
      const descriptor = descriptors[key];
      if (!descriptor || !descriptor.enumerable || !("value" in descriptor)) {
        throw new Error("JSON должен содержать только обычные поля.");
      }
      return canonical(descriptor.value, parents, depth + 1);
    };
    if (Array.isArray(value)) {
      if (keys.length !== value.length + 1) {
        throw new Error("JSON-массив содержит пропуски или дополнительные поля.");
      }
      return `[${Array.from({ length: value.length }, (_, index) => read(String(index))).join(",")}]`;
    }
    const prototype = Object.getPrototypeOf(value);
    if (prototype !== Object.prototype && prototype !== null) {
      throw new Error("JSON должен содержать только обычные объекты.");
    }
    if (keys.some((key) => typeof key !== "string")) {
      throw new Error("Ключи JSON должны быть строками.");
    }
    // Write sorted entries directly: rebuilding an object would let stringify
    // reorder integer-like keys numerically instead of the protocol's ordering.
    return `{${(keys as string[])
      .sort(compareKeys)
      .map((key) => `${jsonString(key)}:${read(key)}`)
      .join(",")}}`;
  } finally {
    parents.delete(value);
  }
}

export async function hashBackendImportCommands(
  commands: readonly BackendImportCommand[],
): Promise<string> {
  if (!Array.isArray(commands)) throw new Error("Пакет команд должен быть массивом.");
  return hashBackendJSON(commands);
}

/** Canonical JSON digest shared with the backend receipt and source-vector protocol. */
export async function hashBackendJSON(value: unknown): Promise<string> {
  const bytes = new TextEncoder().encode(canonical(value, new Set(), 0));
  const digest = globalThis.crypto?.subtle
    ? new Uint8Array(await crypto.subtle.digest("SHA-256", bytes))
    : (await import("@noble/hashes/sha2.js")).sha256(bytes);
  return Array.from(digest, (value) => value.toString(16).padStart(2, "0")).join("");
}

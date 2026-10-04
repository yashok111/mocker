import { webcrypto } from "node:crypto";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { BackendImportCommand } from "@/api/generated/schemas";
import vectors from "../../../../internal/backendmodel/testdata/import_batch_hash_vectors.json";
import { hashBackendImportCommands } from "./backendImportHash";

afterEach(() => vi.unstubAllGlobals());

describe("backend import command hash", () => {
  for (const subtle of [false, true]) {
    for (const vector of vectors) {
      it(`matches Go ${vector.name} with ${subtle ? "Web Crypto" : "fallback"}`, async () => {
        vi.stubGlobal("crypto", subtle ? webcrypto : undefined);
        const commands = structuredClone(vector.commands) as BackendImportCommand[];
        const before = JSON.stringify(commands);
        expect(await hashBackendImportCommands(commands)).toBe(vector.sha256);
        expect(JSON.stringify(commands)).toBe(before);
      });
    }
  }

  it("preserves command and array order while ignoring object insertion order", async () => {
    const left = [{ op: "remove", remove: { recordType: "node", externalKey: "a" } }];
    const right = [{ remove: { externalKey: "a", recordType: "node" }, op: "remove" }];
    expect(await hashBackendImportCommands(left as BackendImportCommand[])).toBe(
      await hashBackendImportCommands(right as BackendImportCommand[]),
    );
    const ordered = vectors.find((vector) => vector.name === "command_order")!;
    expect(
      await hashBackendImportCommands([...ordered.commands].reverse() as BackendImportCommand[]),
    ).not.toBe(ordered.sha256);
  });

  it.each([
    Number.MAX_SAFE_INTEGER + 1,
    Number.NaN,
    Number.POSITIVE_INFINITY,
    Number.NEGATIVE_INFINITY,
    undefined,
    1n,
    Symbol("value"),
    () => 1,
    new Date("2026-01-01"),
    new Map(),
    new Set(),
    new Uint8Array([1]),
    "\ud800",
    "\udfff",
  ])("rejects unsupported JSON input without silently changing it: %s", async (value) => {
    await expect(
      hashBackendImportCommands([{ value }] as unknown as BackendImportCommand[]),
    ).rejects.toThrow();
  });

  it("rejects cycles, sparse arrays and getters without invoking user code", async () => {
    const cyclic: Record<string, unknown> = {};
    cyclic.self = cyclic;
    const sparse: unknown[] = [];
    sparse.length = 2;
    const getter = vi.fn(() => "value");
    const accessor = Object.defineProperty({}, "value", { enumerable: true, get: getter });
    for (const value of [cyclic, sparse, accessor]) {
      await expect(hashBackendImportCommands([value] as BackendImportCommand[])).rejects.toThrow();
    }
    expect(getter).not.toHaveBeenCalled();
  });
});

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { BackendImportCommand } from "@/api/generated/schemas";
import { captureImportAttempt, type ImportAttempt } from "./backendImportAttempts";
import { hashBackendImportCommands } from "./backendImportHash";
import {
  clearImportRecovery,
  importRecoveryKey,
  loadImportRecovery,
  writeImportRecovery,
} from "./backendImportRecovery";
import {
  syncCommands,
  syncHash,
  syncIds,
  syncInventory,
  syncManifest,
} from "./backendSyncTestFixtures";

const projectId = syncIds.project;
const sessionId = syncIds.session;
const batchId = syncIds.later;
const begin: ImportAttempt = {
  kind: "begin",
  projectId,
  body: {
    expectedVersion: 3,
    baseRevisionId: syncIds.revision,
    idempotencyKey: "begin.exact-1",
    profile: "composed-source-v1",
    mode: "composed",
    sourceScope: { kind: "reconcile", repositoryId: syncIds.repository, providerNamespace: "ast" },
    scopeStatus: { status: "complete", gaps: [] },
    syncPolicy: "whole-source-v1",
    manifest: syncManifest,
    inventory: syncInventory,
  },
};
const preview: ImportAttempt = {
  kind: "preview",
  projectId,
  sessionId,
  body: { expectedImportVersion: 4, baseRevisionId: syncIds.revision },
};
const commit: ImportAttempt = {
  kind: "commit",
  projectId,
  sessionId,
  body: {
    expectedVersion: 3,
    expectedImportVersion: 5,
    candidateHash: syncHash,
    idempotencyKey: "commit.exact-1",
  },
};
const abort: ImportAttempt = {
  kind: "abort",
  projectId,
  sessionId,
  body: { expectedImportVersion: 5, idempotencyKey: "abort.exact-1" },
};
const resolution: BackendImportCommand = {
  op: "resolve_assertion",
  resolution: {
    decisionId: syncIds.proof,
    recordType: "node",
    id: syncIds.node,
    property: { kind: "name" },
    conflictHash: syncHash,
    select: { repositoryId: syncIds.repository, providerNamespace: "ast", assertionHash: syncHash },
    reason: "Точное решение с Unicode: 𐀀 < &",
  },
};
async function batch(
  commands: BackendImportCommand[] = syncCommands() as BackendImportCommand[],
): Promise<ImportAttempt> {
  return {
    kind: "batch",
    projectId,
    sessionId,
    batchId,
    body: {
      expectedImportVersion: 2,
      commands,
      payloadHash: await hashBackendImportCommands(commands),
    },
  };
}
function rawRecord(attempt: ImportAttempt): string {
  const { body, ...identity } = attempt;
  return JSON.stringify({ version: 1, ...identity, body: JSON.stringify(body) });
}
function putRaw(raw: string) {
  localStorage.setItem(importRecoveryKey(projectId), raw);
}
async function expectInvalid(raw: string) {
  putRaw(raw);
  const restored = await loadImportRecovery(projectId);
  expect(restored.attempt).toBeNull();
  expect(restored.raw).toBe(raw);
  expect(restored.error).toBeInstanceOf(Error);
  expect(localStorage.getItem(importRecoveryKey(projectId))).toBe(raw);
}

beforeEach(() => localStorage.clear());
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("durable exact import recovery", () => {
  it("has one stable project-scoped key and loads an empty slot without requests", async () => {
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    expect(importRecoveryKey(projectId)).toBe(importRecoveryKey(projectId));
    expect(importRecoveryKey(projectId)).not.toBe(importRecoveryKey(syncIds.later));
    expect(await loadImportRecovery(projectId)).toEqual({ attempt: null, raw: null, error: null });
    expect(fetch).not.toHaveBeenCalled();
  });

  it.each([
    ["Begin", async () => begin],
    ["collector batch", () => batch()],
    ["decision batch", () => batch([resolution])],
    ["Preview", async () => preview],
    ["Commit", async () => commit],
    ["Abort", async () => abort],
  ] as const)(
    "restores %s detached/frozen with exactly the same wire body",
    async (_name, create) => {
      const attempt = captureImportAttempt(await create());
      const fetch = vi.fn();
      vi.stubGlobal("fetch", fetch);
      const raw = await writeImportRecovery(projectId, attempt, null);
      expect(localStorage.getItem(importRecoveryKey(projectId))).toBe(raw);
      expect(JSON.parse(raw).body).toBe(JSON.stringify(attempt.body));
      const restored = await loadImportRecovery(projectId);
      expect(restored).toEqual({ attempt, raw, error: null });
      expect(restored.attempt).not.toBe(attempt);
      expect(Object.isFrozen(restored.attempt)).toBe(true);
      expect(Object.isFrozen(restored.attempt!.body)).toBe(true);
      expect(JSON.stringify(restored.attempt!.body)).toBe(JSON.stringify(attempt.body));
      expect(await writeImportRecovery(projectId, restored.attempt!, raw)).toBe(raw);
      expect(fetch).not.toHaveBeenCalled();
    },
  );

  it("preserves command order, numeric spellings, and nested immutability", async () => {
    const commands = syncCommands() as BackendImportCommand[];
    if (commands[0]!.op !== "upsert_node") throw new Error("fixture");
    commands[0]!.node.attributes = {
      label: "\u0000𐀀",
      nativeDefinition: { z: 1e-7, a: 0.1, "2": 2, "1": 1 },
    } as never;
    const attempt = await batch(commands);
    const wire = JSON.stringify(attempt.body);
    await writeImportRecovery(projectId, attempt, null);
    commands.reverse();
    const loaded = (await loadImportRecovery(projectId)).attempt!;
    expect(JSON.stringify(loaded.body)).toBe(wire);
    if (loaded.kind !== "batch") throw new Error("fixture");
    expect(Object.isFrozen(loaded.body.commands[0])).toBe(true);
  });

  it.each(["{", "null", "[]", '"text"'])(
    "retains unreadable record %s for confirmed discard",
    async (raw) => {
      await expectInvalid(raw);
      clearImportRecovery(projectId, raw);
      expect(localStorage.getItem(importRecoveryKey(projectId))).toBeNull();
    },
  );

  it.each([
    [
      "unknown version",
      (e: Record<string, unknown>) => {
        e.version = 2;
      },
    ],
    [
      "wrong project",
      (e: Record<string, unknown>) => {
        e.projectId = syncIds.later;
      },
    ],
    [
      "unknown kind",
      (e: Record<string, unknown>) => {
        e.kind = "apply";
      },
    ],
    [
      "non-string kind",
      (e: Record<string, unknown>) => {
        e.kind = ["preview"];
      },
    ],
    [
      "extra field",
      (e: Record<string, unknown>) => {
        e.extra = true;
      },
    ],
    [
      "missing session",
      (e: Record<string, unknown>) => {
        delete e.sessionId;
      },
    ],
    [
      "invalid session",
      (e: Record<string, unknown>) => {
        e.sessionId = "../other";
      },
    ],
    [
      "unexpected batch",
      (e: Record<string, unknown>) => {
        e.batchId = batchId;
      },
    ],
    [
      "non-string body",
      (e: Record<string, unknown>) => {
        e.body = {};
      },
    ],
    [
      "non-exact body",
      (e: Record<string, unknown>) => {
        e.body = ` ${e.body}`;
      },
    ],
    [
      "duplicate body keys",
      (e: Record<string, unknown>) => {
        e.body = `{"expectedImportVersion":3,"expectedImportVersion":4,"baseRevisionId":"${syncIds.revision}"}`;
      },
    ],
  ])("rejects %s", async (_name, mutate) => {
    const envelope = JSON.parse(rawRecord(preview));
    mutate(envelope);
    await expectInvalid(JSON.stringify(envelope));
  });

  it.each([0, -1, 1.5, Number.MAX_SAFE_INTEGER + 1])(
    "rejects unsafe or non-positive CAS %s",
    async (value) => {
      const attempt = structuredClone(preview);
      attempt.body.expectedImportVersion = value;
      await expectInvalid(rawRecord(attempt));
      await expect(writeImportRecovery(projectId, attempt, null)).rejects.toThrow();
    },
  );

  it("rejects an imprecise numeric token before it can be replayed", async () => {
    const envelope = JSON.parse(rawRecord(preview));
    envelope.body = envelope.body.replace(":4,", ":9007199254740993,");
    await expectInvalid(JSON.stringify(envelope));
  });

  it.each(["", "with space", "Юникод", "x".repeat(129)])(
    "rejects invalid idempotency key %s",
    async (key) => {
      const attempt = structuredClone(abort);
      attempt.body.idempotencyKey = key;
      await expectInvalid(rawRecord(attempt));
    },
  );

  it("rejects additional request fields and invalid candidate hashes", async () => {
    await expectInvalid(
      rawRecord({ ...preview, body: { ...preview.body, unexpected: true } } as ImportAttempt),
    );
    await expectInvalid(rawRecord({ ...commit, body: { ...commit.body, candidateHash: "bad" } }));
  });

  it("checks the complete batch hash and command envelope before restoration", async () => {
    const attempt = await batch();
    if (attempt.kind !== "batch") throw new Error("fixture");
    await expectInvalid(
      rawRecord({ ...attempt, body: { ...attempt.body, payloadHash: "b".repeat(64) } }),
    );
    await expectInvalid(rawRecord({ ...attempt, batchId: "not-a-uuid" }));
    const commands = [{ op: "unrecognized", any: {} }] as unknown as BackendImportCommand[];
    await expectInvalid(rawRecord(await batch(commands)));
  });

  it("checks Begin nested source input and strict source6 scope", async () => {
    const invalids = [
      { ...begin.body, mode: "replace" },
      { ...begin.body, sourceScope: { kind: "add_repository", repositoryId: syncIds.repository } },
      { ...begin.body, scopeStatus: { status: "complete", gaps: ["hidden gap"] } },
      { ...begin.body, scopeStatus: { status: "partial", gaps: [] } },
      { ...begin.body, inventory: [] },
      { ...begin.body, manifest: { ...syncManifest, extra: true } },
      { ...begin.body, syncPolicy: "incremental-source-v1" },
      {
        ...begin.body,
        changeManifest: { scope: "affected-subgraph", files: [], affectedRoots: [] },
      },
      {
        ...begin.body,
        profileExtension: { fromProfile: "field-lineage-v1", toProfile: "composed-source-v1" },
      },
    ];
    for (const body of invalids)
      await expectInvalid(rawRecord({ ...begin, body } as ImportAttempt));
  });

  it("restores valid incremental and migration/extension Begin variants", async () => {
    const bodies = [
      {
        ...begin.body,
        syncPolicy: "incremental-source-v1",
        changeManifest: {
          scope: "affected-subgraph",
          files: [],
          affectedRoots: [{ recordType: "node", id: syncIds.node }],
        },
      },
      {
        ...begin.body,
        sourceScope: {
          kind: "migrate_provider",
          repositoryId: syncIds.repository,
          fromProviderNamespace: "old",
          fromSnapshotId: syncIds.snapshot,
          reason: "Migration",
        },
        scopeStatus: { status: "partial", gaps: ["Some data absent"] },
        profileExtension: { fromProfile: "events-service-v1", toProfile: "composed-source-v1" },
      },
      { ...begin.body, sourceScope: { kind: "add_provider", repositoryId: syncIds.repository } },
      { ...begin.body, sourceScope: { kind: "add_repository" } },
    ];
    for (const body of bodies) {
      const attempt = { ...begin, body } as ImportAttempt;
      const raw = await writeImportRecovery(projectId, attempt, null);
      expect((await loadImportRecovery(projectId)).attempt).toEqual(attempt);
      clearImportRecovery(projectId, raw);
    }
  });

  it("caps record bytes, Begin bytes, complete batch bytes and command count", async () => {
    await expectInvalid("x".repeat(12 * 1024 * 1024 + 1));
    const oversizedBegin = structuredClone(begin);
    oversizedBegin.body.manifest.provider.limitations = ["я".repeat(4 * 1024 * 1024)];
    await expect(writeImportRecovery(projectId, oversizedBegin, null)).rejects.toThrow(
      /размер|МиБ/,
    );
    const oversizedBatch = await batch([
      { ...resolution, resolution: { ...resolution.resolution, reason: "я".repeat(512 * 1024) } },
    ]);
    await expect(writeImportRecovery(projectId, oversizedBatch, null)).rejects.toThrow(
      /размер|МиБ/,
    );
    await expect(
      writeImportRecovery(
        projectId,
        await batch(Array.from({ length: 501 }, () => resolution)),
        null,
      ),
    ).rejects.toThrow();
  });

  it("does not overwrite another tab's record or replace an unresolved attempt", async () => {
    const raw = await writeImportRecovery(projectId, preview, null);
    await expect(writeImportRecovery(projectId, abort, null)).rejects.toThrow(/измен|сохран/);
    await expect(writeImportRecovery(projectId, abort, raw)).rejects.toThrow(/измен|сохран/);
    expect(localStorage.getItem(importRecoveryKey(projectId))).toBe(raw);
    clearImportRecovery(projectId, raw);
    await expect(writeImportRecovery(projectId, preview, raw)).rejects.toThrow();
  });

  it("guards against clearing another tab's newer attempt", async () => {
    const raw = await writeImportRecovery(projectId, preview, null);
    clearImportRecovery(projectId, raw);
    const next = await writeImportRecovery(projectId, abort, null);
    expect(() => clearImportRecovery(projectId, raw)).toThrow(/измен|сохран/);
    expect(localStorage.getItem(importRecoveryKey(projectId))).toBe(next);
  });

  it("checks the slot again after asynchronous batch verification", async () => {
    const attempt = await batch();
    const pending = writeImportRecovery(projectId, attempt, null);
    const other = rawRecord(abort);
    putRaw(other);
    await expect(pending).rejects.toThrow(/измен/);
    expect(localStorage.getItem(importRecoveryKey(projectId))).toBe(other);
  });

  it("accepts the largest exact positive CAS without altering its digits", async () => {
    const attempt = {
      ...preview,
      body: { ...preview.body, expectedImportVersion: Number.MAX_SAFE_INTEGER },
    };
    const raw = await writeImportRecovery(projectId, attempt, null);
    expect(raw).toContain("9007199254740991");
    expect((await loadImportRecovery(projectId)).attempt).toEqual(attempt);
  });

  it("blocks dispatch when storage is denied or quota is exhausted", async () => {
    const storage = {
      getItem: vi.fn(() => null),
      setItem: vi.fn(() => {
        throw new DOMException("Quota", "QuotaExceededError");
      }),
      removeItem: vi.fn(),
    };
    vi.stubGlobal("localStorage", storage);
    await expect(writeImportRecovery(projectId, preview, null)).rejects.toThrow(/сохран|хранилищ/);
    expect(storage.setItem).toHaveBeenCalledTimes(1);
    storage.getItem.mockImplementation(() => {
      throw new DOMException("Denied", "SecurityError");
    });
    expect(await loadImportRecovery(projectId)).toMatchObject({
      attempt: null,
      raw: null,
      error: expect.any(Error),
    });
    await expect(writeImportRecovery(projectId, preview, null)).rejects.toThrow(/хранилищ|прочит/);
    expect(storage.setItem).toHaveBeenCalledTimes(1);
  });

  it("reuses an already durable identical retry when new writes would exceed quota", async () => {
    const raw = await writeImportRecovery(projectId, preview, null);
    const storage = {
      getItem: vi.fn(() => raw),
      setItem: vi.fn(() => {
        throw new DOMException("Quota", "QuotaExceededError");
      }),
      removeItem: vi.fn(),
    };
    vi.stubGlobal("localStorage", storage);
    expect(await writeImportRecovery(projectId, preview, raw)).toBe(raw);
    expect(storage.setItem).not.toHaveBeenCalled();
  });

  it("verifies the storage write and clear readbacks", async () => {
    const raw = rawRecord(preview);
    const storage = {
      getItem: vi.fn<() => string | null>(() => null),
      setItem: vi.fn(),
      removeItem: vi.fn(),
    };
    vi.stubGlobal("localStorage", storage);
    await expect(writeImportRecovery(projectId, preview, null)).rejects.toThrow(/сохран|провер/);
    storage.getItem.mockReturnValue(raw);
    expect(() => clearImportRecovery(projectId, raw)).toThrow(/удал|очист|провер/);
    storage.removeItem.mockImplementation(() => {
      throw new Error("Denied");
    });
    expect(() => clearImportRecovery(projectId, raw)).toThrow(/удал|очист|хранилищ/);
  });

  it("rejects a mismatched write project without changing its slot", async () => {
    await expect(writeImportRecovery(syncIds.later, preview, null)).rejects.toThrow();
    expect(localStorage.length).toBe(0);
  });
});

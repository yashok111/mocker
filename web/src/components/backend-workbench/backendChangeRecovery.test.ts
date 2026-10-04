import { afterEach, expect, it, vi } from "vitest";
import {
  makeChangeAttempt,
  readChangeRecovery,
  writeChangeRecovery,
  inspectChangeRecovery,
  discoverChangeCreateRecovery,
  changeCreateRecoveryKey,
} from "./backendChangeRecovery";
import { changeDraftID, changeHash, changeTestID } from "./backendChangeTestFixtures";
afterEach(() => {
  sessionStorage.clear();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
it("detaches and restores the exact pending body including command IDs", () => {
  const input = {
    expectedVersion: 1,
    proposalRevisionId: changeDraftID,
    candidateHash: changeHash,
    idempotencyKey: "same",
    commands: [
      {
        type: "rename" as const,
        commandId: changeTestID,
        reason: "Reason",
        recordType: "node" as const,
        id: changeTestID,
        name: "Before",
      },
    ],
  };
  const attempt = makeChangeAttempt("apply", input);
  input.commands[0]!.name = "After";
  writeChangeRecovery("pending", attempt);
  const recovered = readChangeRecovery("pending");
  expect(recovered?.body).toBe(attempt.body);
  expect(recovered?.input).toEqual(attempt.input);
  expect(attempt.body).toContain('"name":"Before"');
  writeChangeRecovery("pending", { ...attempt, phase: "conflict" });
  expect(readChangeRecovery("pending")?.phase).toBe("conflict");
});
it("refuses unsafe numbers and request bodies from another operation", () => {
  sessionStorage.setItem(
    "unsafe",
    JSON.stringify({
      kind: "restore",
      phase: "unknown",
      body: `{"expectedVersion":9007199254740993,"proposalRevisionId":"${changeDraftID}","restoreRevisionId":"${changeDraftID}","idempotencyKey":"same"}`,
    }),
  );
  expect(() => readChangeRecovery("unsafe")).toThrow();
  expect(inspectChangeRecovery("unsafe").error).not.toBeNull();
  sessionStorage.setItem(
    "mixed",
    JSON.stringify({
      kind: "create",
      phase: "unknown",
      body: JSON.stringify({
        expectedVersion: 1,
        proposalRevisionId: changeDraftID,
        restoreRevisionId: changeDraftID,
        idempotencyKey: "same",
      }),
    }),
  );
  expect(() => readChangeRecovery("mixed")).toThrow();
});
it("reports storage failure so the caller can stop dispatch", () => {
  const attempt = makeChangeAttempt("create", {
    name: "New",
    baseRevisionId: changeTestID,
    idempotencyKey: "same",
  });
  const storage = sessionStorage;
  vi.stubGlobal("sessionStorage", {
    getItem: storage.getItem.bind(storage),
    setItem: () => {
      throw new Error("disabled");
    },
    removeItem: storage.removeItem.bind(storage),
    clear: storage.clear.bind(storage),
  });
  expect(() => writeChangeRecovery("pending", attempt)).toThrow(/сохранить/);
  expect(attempt.input.idempotencyKey).toBe("same");
});

it("discovers all old Create contexts for a project without choosing another project", () => {
  const first = makeChangeAttempt("create", {
    name: "A",
    baseRevisionId: changeTestID,
    idempotencyKey: "a",
  });
  const second = makeChangeAttempt("create", {
    name: "B",
    baseRevisionId: changeDraftID,
    idempotencyKey: "b",
  });
  writeChangeRecovery(`${changeCreateRecoveryKey(changeTestID)}:${changeTestID}`, first);
  writeChangeRecovery(`${changeCreateRecoveryKey(changeTestID)}:${changeDraftID}`, second);
  writeChangeRecovery(`${changeCreateRecoveryKey(changeDraftID)}:${changeTestID}`, first);
  const found = discoverChangeCreateRecovery(changeTestID);
  expect(found).toHaveLength(2);
  expect(found.map((item) => item.attempt?.body)).toEqual(
    expect.arrayContaining([first.body, second.body]),
  );
});
it("verifies storage readback and preserves a concurrently replaced record", () => {
  const first = makeChangeAttempt("create", {
    name: "A",
    baseRevisionId: changeTestID,
    idempotencyKey: "a",
  });
  const second = makeChangeAttempt("create", {
    name: "B",
    baseRevisionId: changeDraftID,
    idempotencyKey: "b",
  });
  const raw = writeChangeRecovery("guarded", first, null);
  writeChangeRecovery("guarded", second, raw);
  expect(() => writeChangeRecovery("guarded", null, raw)).toThrow(/изменилась/);
  expect(readChangeRecovery("guarded")?.body).toBe(second.body);
  const storage = sessionStorage;
  vi.stubGlobal("sessionStorage", {
    getItem: storage.getItem.bind(storage),
    setItem: () => {},
    removeItem: storage.removeItem.bind(storage),
    clear: storage.clear.bind(storage),
  });
  expect(() => writeChangeRecovery("no-write", first, null)).toThrow(/подтвердить/);
});

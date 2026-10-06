import { describe, expect, it } from "vitest";
import { clearReplayAttempt, readReplayAttempt, writeReplayAttempt } from "./backendReplayRecovery";
function harness() {
  const values = new Map<string, string>();
  const storage = {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => {
      values.set(key, value);
    },
    removeItem: (key: string) => {
      values.delete(key);
    },
  };
  let tail = Promise.resolve();
  const locks = {
    request<T>(_name: string, work: () => T | Promise<T>): Promise<T> {
      const task = tail.then(work);
      tail = task.then(
        () => {},
        () => {},
      );
      return task;
    },
  };
  return { storage, locks };
}
const intent = {
  projectId: "project",
  action: "start" as const,
  body: { idempotencyKey: "key", package: { id: "id", version: 2, contentHash: "hash" } },
};
describe("replay durable intent", () => {
  it("retains exact project, action, pins and key after reload", async () => {
    const { storage, locks } = harness();
    await writeReplayAttempt(storage, "project", intent, locks);
    expect(readReplayAttempt(storage, "project")).toEqual(intent);
    expect(readReplayAttempt(storage, "other")).toBeNull();
    await expect(
      writeReplayAttempt(storage, "project", { ...intent, action: "save" }, locks),
    ).rejects.toThrow();
  });
  it("refuses corrupted or mismatched saved intent", () => {
    for (const value of ["{", "null", '{"projectId":"other","action":"start","body":{}}'])
      expect(() => readReplayAttempt({ getItem: () => value }, "project")).toThrow();
  });
  it("late completion from another tab cannot remove a newer pending intent", async () => {
    const { storage, locks } = harness();
    await writeReplayAttempt(storage, "project", intent, locks);
    await clearReplayAttempt(storage, "project", intent, locks);
    const newer = { ...intent, body: { ...intent.body, idempotencyKey: "new-key" } };
    await writeReplayAttempt(storage, "project", newer, locks);
    expect(await clearReplayAttempt(storage, "project", intent, locks)).toBe(false);
    expect(readReplayAttempt(storage, "project")).toEqual(newer);
  });
  it("simultaneous tab admissions cannot overwrite each other", async () => {
    const { storage, locks } = harness();
    const other = { ...intent, body: { ...intent.body, idempotencyKey: "other-key" } };
    const outcomes = await Promise.allSettled([
      writeReplayAttempt(storage, "project", intent, locks),
      writeReplayAttempt(storage, "project", other, locks),
    ]);
    expect(outcomes.map((x) => x.status)).toEqual(["fulfilled", "rejected"]);
    expect(readReplayAttempt(storage, "project")).toEqual(intent);
  });
});

// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { json } from "@/test/http";
import { readLineagePage, lineageRefKey } from "./backendLineageReads";
afterEach(() => vi.unstubAllGlobals());
const seed = {
  kind: "port" as const,
  nodeId: "step",
  collection: "inputs" as const,
  portKey: "exact-key",
};
const input = { revisionId: "rev", seed, direction: "forward" as const };
it.each([
  { projectId: "foreign" },
  { revisionId: "head" },
  { seed: { ...seed, collection: "outputs" } },
  { seed: { ...seed, portKey: "other" } },
  { direction: "reverse" },
])("rejects foreign complete response scope %j", async (change) => {
  vi.stubGlobal("fetch", async () => json(200, { projectId: "project", ...input, ...change }));
  await expect(readLineagePage("project", input, new AbortController().signal)).rejects.toThrow(
    /друг/,
  );
});
it("rejects a cancelled late response without latest fallback", async () => {
  let finish!: (value: Response) => void;
  vi.stubGlobal(
    "fetch",
    () =>
      new Promise<Response>((resolve) => {
        finish = resolve;
      }),
  );
  const controller = new AbortController();
  const pending = readLineagePage("project", input, controller.signal);
  controller.abort();
  finish(json(200, { projectId: "project", ...input }));
  await expect(pending).rejects.toThrow();
});
it("compares full references independently of JSON property order", () => {
  expect(lineageRefKey(seed)).toBe(
    lineageRefKey({
      portKey: seed.portKey,
      collection: seed.collection,
      nodeId: seed.nodeId,
      kind: seed.kind,
    }),
  );
  expect(lineageRefKey(seed)).not.toBe(lineageRefKey({ ...seed, collection: "outputs" }));
});

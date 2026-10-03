// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { json } from "@/test/http";
import { readLineagePage, lineageRefKey, lineageRefLabel } from "./backendLineageReads";
afterEach(() => vi.unstubAllGlobals());
it("keeps each event endpoint and delivery route a distinct value and response seed", async () => {
  const event = {
    kind: "event_field" as const,
    nodeId: "field",
    endpointId: "consumer",
    routeId: "delivery-a",
  };
  expect(lineageRefKey(event)).not.toBe(lineageRefKey({ ...event, routeId: "delivery-b" }));
  expect(lineageRefKey(event)).not.toBe(lineageRefKey({ ...event, endpointId: "producer" }));
  expect(lineageRefLabel(event)).toContain("delivery-a");
  vi.stubGlobal("fetch", async () =>
    json(200, {
      projectId: "project",
      revisionId: "rev",
      direction: "forward",
      seed: { ...event, routeId: "delivery-b" },
    }),
  );
  await expect(
    readLineagePage(
      "project",
      { revisionId: "rev", direction: "forward", seed: event },
      new AbortController().signal,
    ),
  ).rejects.toThrow(/друг/);
});
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

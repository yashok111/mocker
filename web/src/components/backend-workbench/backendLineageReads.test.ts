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
      revisionId: "0197aaf9-5555-7000-8000-000000000002",
      direction: "forward",
      seed: { ...event, routeId: "delivery-b" },
    }),
  );
  await expect(
    readLineagePage(
      "project",
      { revisionId: "0197aaf9-5555-7000-8000-000000000002", direction: "forward", seed: event },
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
const input = {
  revisionId: "0197aaf9-5555-7000-8000-000000000002",
  seed,
  direction: "forward" as const,
};
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

it("pins full proposal lineage and rejects a source-only response", async () => {
  const target = {
    changeProposal: {
      proposalId: "0197aaf9-5555-7000-8000-000000000001",
      proposalRevisionId: "0197aaf9-5555-7000-8000-000000000003",
    },
  };
  const base = "0197aaf9-5555-7000-8000-000000000002";
  const hash = "a".repeat(64);
  const page = {
    projectId: "project",
    revisionId: base,
    semanticHash: hash,
    direction: "forward",
    seed: { kind: "representation_field" as const, nodeId: base },
    target,
    pins: {
      viewSchemaVersion: "proposal-graph-v1",
      structuralSchemaVersion: "6",
      targetHash: hash,
      effectiveSemanticHash: hash,
      baseRevisionId: base,
      baseSemanticHash: hash,
      sourceVectorHash: hash,
      sourceSnapshotIds: [],
      artifactPins: [],
      artifactContext: null,
    },
    items: [],
  };
  vi.stubGlobal("fetch", async () => json(200, page));
  await expect(
    readLineagePage(
      "project",
      { ...target, seed: page.seed, direction: "forward" },
      new AbortController().signal,
    ),
  ).resolves.toHaveProperty("target", target);
  vi.stubGlobal("fetch", async () => json(200, { ...page, target: undefined, pins: undefined }));
  await expect(
    readLineagePage(
      "project",
      { ...target, seed: page.seed, direction: "forward" },
      new AbortController().signal,
    ),
  ).rejects.toThrow();
});
it("labels representation fields independently from operation-specific API fields", () => {
  expect(lineageRefLabel({ kind: "representation_field", nodeId: "field" })).toBe(
    "field · поле представления",
  );
  expect(lineageRefKey({ kind: "representation_field", nodeId: "field" })).not.toBe(
    lineageRefKey({ kind: "api_field", nodeId: "field" }),
  );
});

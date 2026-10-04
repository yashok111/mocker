// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import type {
  BackendEffectiveGraphPins,
  BackendReadTarget,
  QueryBackendFlowRequest,
  QueryBackendEventsRequest,
} from "@/api/generated/schemas";
import { json } from "@/test/http";
import { readFlowPage } from "./backendFlowReads";
import { readEventsPage } from "./backendEventsReads";
import { readAPIArtifacts } from "./backendAPIArtifactReads";
const base = "0197aaf9-5555-7000-8000-000000000001";
const proposal = "0197aaf9-5555-7000-8000-000000000002";
const revision = "0197aaf9-5555-7000-8000-000000000003";
const target: BackendReadTarget = {
  changeProposal: { proposalId: proposal, proposalRevisionId: revision },
};
const pins: BackendEffectiveGraphPins = {
  baseRevisionId: base,
  baseSemanticHash: "a".repeat(64),
  effectiveSemanticHash: "b".repeat(64),
  sourceVectorHash: "c".repeat(64),
  targetHash: "d".repeat(64),
  viewSchemaVersion: "proposal-graph-v1",
  structuralSchemaVersion: "6",
  sourceSnapshotIds: [],
  artifactPins: [],
  artifactContext: null,
};
afterEach(() => vi.unstubAllGlobals());
it("accepts full Flow pins while the display revision remains baseline", async () => {
  vi.stubGlobal("fetch", async () =>
    json(200, {
      projectId: "project",
      revisionId: base,
      semanticHash: pins.effectiveSemanticHash,
      view: "entrypoints",
      target,
      pins,
    }),
  );
  const result = await readFlowPage(
    "project",
    { ...target, view: "entrypoints" } as QueryBackendFlowRequest,
    new AbortController().signal,
    pins,
  );
  expect(result.revisionId).toBe(base);
});
it("refuses a source-only Flow page for a full target", async () => {
  vi.stubGlobal("fetch", async () =>
    json(200, { projectId: "project", revisionId: base, view: "entrypoints" }),
  );
  await expect(
    readFlowPage(
      "project",
      { ...target, view: "entrypoints" } as QueryBackendFlowRequest,
      new AbortController().signal,
      pins,
    ),
  ).rejects.toThrow();
});
it("rejects staged Flow before sending a request", async () => {
  const fetch = vi.fn();
  vi.stubGlobal("fetch", fetch);
  await expect(
    readFlowPage(
      "project",
      {
        importCandidate: { importId: base, importVersion: 1, candidateHash: "a".repeat(64) },
        view: "entrypoints",
      } as QueryBackendFlowRequest,
      new AbortController().signal,
    ),
  ).rejects.toThrow();
  expect(fetch).not.toHaveBeenCalled();
});
it("rejects staged events before sending a request", async () => {
  const fetch = vi.fn();
  vi.stubGlobal("fetch", fetch);
  await expect(
    readEventsPage(
      "project",
      {
        importCandidate: { importId: base, importVersion: 1, candidateHash: "a".repeat(64) },
        view: "service_calls",
      } as QueryBackendEventsRequest,
      pins.effectiveSemanticHash,
      new AbortController().signal,
      pins,
    ),
  ).rejects.toThrow();
  expect(fetch).not.toHaveBeenCalled();
});
it("reads full API artifacts against desired scope and effectivePins namespace", async () => {
  const fetch = vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) =>
    json(200, {
      revisionId: base,
      semanticHash: pins.effectiveSemanticHash,
      sourceSnapshotIds: [],
      pins: [],
      effectivePins: pins,
      target,
      items: [],
      nextCursor: "",
    }),
  );
  vi.stubGlobal("fetch", fetch);
  const result = await readAPIArtifacts(
    {
      projectId: "project",
      revisionId: base,
      target,
      pins,
      semanticHash: pins.effectiveSemanticHash,
      sourceSnapshotIds: [],
      artifactPins: [],
    },
    undefined,
    new AbortController().signal,
  );
  expect(result.items).toEqual([]);
  expect(JSON.parse(String(fetch.mock.calls[0]?.[1]?.body))).toMatchObject(target);
});
it("keeps graph paging on the exact full pin vector and rejects page drift", async () => {
  const { readDatabaseGraph } = await import("./backendDatabaseReads");
  const calls: unknown[] = [];
  vi.stubGlobal("fetch", async (_url: RequestInfo | URL, init?: RequestInit) => {
    const input = JSON.parse(String(init?.body));
    calls.push(input);
    return json(200, {
      viewSchemaVersion: pins.viewSchemaVersion,
      target,
      pins: input.cursor ? { ...pins, targetHash: "e".repeat(64) } : pins,
      nodes: [],
      edges: [],
      origins: [],
      identities: [],
      baselineEvidence: [],
      nextCursor: input.cursor ? "" : "next",
    });
  });
  await expect(
    readDatabaseGraph(
      "project",
      { ...target, recordType: "nodes" },
      new AbortController().signal,
      pins,
    ),
  ).rejects.toThrow(/граф|версия/);
  expect(calls).toEqual([
    { ...target, recordType: "nodes", limit: 500 },
    { ...target, recordType: "nodes", limit: 500, cursor: "next" },
  ]);
});
it("binds contextual event endpoint and route graph reads to full pins", async () => {
  const { readEventsFieldSeeds } = await import("./backendEventsReads");
  const calls: Record<string, unknown>[] = [];
  vi.stubGlobal("fetch", async (_url: RequestInfo | URL, init?: RequestInit) => {
    const input = JSON.parse(String(init?.body));
    calls.push(input);
    const node =
      input.kind === "event_field"
        ? { id: "field", kind: "event_field", parentId: "message" }
        : { id: "consumer", kind: "consumer" };
    const edge = {
      id: "delivery",
      kind: "delivered_to",
      from: "channel",
      to: "consumer",
      attributes: { messageId: "message" },
    };
    return json(200, {
      viewSchemaVersion: pins.viewSchemaVersion,
      target,
      pins,
      nodes: input.recordType === "nodes" ? [node] : [],
      edges: input.recordType === "edges" ? [edge] : [],
      origins: [],
      identities: [],
      baselineEvidence: [],
      nextCursor: "",
    });
  });
  const result = await readEventsFieldSeeds(
    "project",
    target,
    {
      messageId: "message",
      channelId: "channel",
      consumerId: "consumer",
      deliveryEdgeId: "delivery",
    },
    new AbortController().signal,
    pins,
  );
  expect(result.addresses).toEqual([{ endpointId: "consumer", routeId: "delivery" }]);
  expect(calls).toHaveLength(3);
  for (const call of calls) expect(call).toMatchObject(target);
});
it("rejects a Flow descriptor or hash inconsistent with its otherwise valid full pins", async () => {
  for (const changed of [{ revisionId: revision }, { semanticHash: pins.baseSemanticHash }]) {
    vi.stubGlobal("fetch", async () =>
      json(200, {
        projectId: "project",
        revisionId: base,
        semanticHash: pins.effectiveSemanticHash,
        view: "entrypoints",
        target,
        pins,
        ...changed,
      }),
    );
    await expect(
      readFlowPage(
        "project",
        { ...target, view: "entrypoints" } as QueryBackendFlowRequest,
        new AbortController().signal,
        pins,
      ),
    ).rejects.toThrow();
  }
});

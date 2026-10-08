// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { json } from "@/test/http";
import {
  readEventsPage,
  readEventsGapEvidence,
  captureEventsFragment,
  readEventsFieldSeeds,
} from "./backendEventsReads";
afterEach(() => vi.unstubAllGlobals());
it("admits message members with each independently known exact endpoint and edge tuple", async () => {
  const refs = {
    producerId: "producer",
    consumerId: "consumer",
    messageId: "message",
    channelId: "channel",
    emitsEdgeId: "emit",
    deliveryEdgeId: "delivery",
  };
  let unresolved = false;
  vi.stubGlobal("fetch", async (_url: RequestInfo | URL, init?: RequestInit) => {
    const input = JSON.parse(String(init?.body));
    const records =
      input.kind === "event_field"
        ? {
            nodes: [{ id: "field", kind: "event_field", parentId: "message" }],
            edges: [],
          }
        : input.recordType === "edges"
          ? {
              nodes: [],
              edges: [
                {
                  id: input.id,
                  kind: input.id === "emit" ? "emits" : "delivered_to",
                  from: input.id === "emit" ? "producer" : "channel",
                  to: input.id === "emit" ? "message" : "consumer",
                  attributes:
                    input.id === "emit" ? { channelId: "channel" } : { messageId: "message" },
                },
              ],
            }
          : {
              nodes: [
                {
                  id: input.id,
                  kind:
                    input.id === "producer"
                      ? "flow_step"
                      : unresolved
                        ? "unresolved_target"
                        : "consumer",
                  attributes: input.id === "producer" ? { stepKind: "emit" } : {},
                },
              ],
              edges: [],
            };
    return json(200, { ...records, nextCursor: "" });
  });
  const known = await readEventsFieldSeeds(
    "project",
    "0197aaf9-5555-7000-8000-000000000119",
    refs,
    new AbortController().signal,
  );
  expect(known.addresses).toEqual([
    { endpointId: "producer", routeId: "emit" },
    { endpointId: "consumer", routeId: "delivery" },
  ]);
  expect(known.nodes.map((n) => n.id)).toEqual(["field"]);
  unresolved = true;
  const boundary = await readEventsFieldSeeds(
    "project",
    "0197aaf9-5555-7000-8000-000000000119",
    refs,
    new AbortController().signal,
  );
  expect(boundary.addresses).toEqual([{ endpointId: "producer", routeId: "emit" }]);
  expect(boundary.limitations.length).toBeGreaterThan(0);
  unresolved = false;
  const orphan = await readEventsFieldSeeds(
    "project",
    "0197aaf9-5555-7000-8000-000000000119",
    {
      messageId: "message",
      channelId: "channel",
      consumerId: "consumer",
      deliveryEdgeId: "delivery",
    },
    new AbortController().signal,
  );
  expect(orphan.addresses).toEqual([{ endpointId: "consumer", routeId: "delivery" }]);
});
const hash = "a".repeat(64);
const request = {
  revisionId: "0197aaf9-5555-7000-8000-000000000119",
  view: "routes" as const,
  seedNodeId: "consumer",
  limit: 50,
  cursor: "page2",
};
const page = {
  projectId: "project",
  ...request,
  semanticHash: hash,
  policy: "source-events-projection-v1",
  limits: {
    maxExaminedEdges: 100000,
    maxItems: 5000,
    maxAuxiliaryRecords: 20000,
    maxWitnessRecords: 256,
    defaultPageSize: 50,
    maxPageSize: 100,
    scanPolicy: "complete-scan-admission",
  },
};
it.each([
  { projectId: "other" },
  { revisionId: "0197aaf9-5555-7000-8000-000000000105" },
  { semanticHash: "b".repeat(64) },
  { view: "jobs" },
  { seedNodeId: "producer" },
  { serviceId: "unexpected" },
  { policy: "unknown" },
  { limits: { ...page.limits, maxItems: 9000 } },
])("rejects substituted event response identity %j", async (change) => {
  vi.stubGlobal("fetch", async () => json(200, { ...page, ...change }));
  await expect(
    readEventsPage("project", request, hash, new AbortController().signal),
  ).rejects.toThrow(/друг|границ/);
});
it("reads the exact source and forwards cancellation without replacing a pin with head", async () => {
  let captured: RequestInit | undefined;
  vi.stubGlobal("fetch", async (_url: RequestInfo | URL, init?: RequestInit) => {
    captured = init;
    return json(200, page);
  });
  const signal = new AbortController().signal;
  expect(await readEventsPage("project", request, hash, signal)).toMatchObject({
    revisionId: "0197aaf9-5555-7000-8000-000000000119",
    seedNodeId: "consumer",
  });
  expect(JSON.parse(String(captured?.body))).toEqual(request);
  expect(captured?.signal).toBe(signal);
});
it("discards cancelled late event replies", async () => {
  let finish!: (value: Response) => void;
  vi.stubGlobal(
    "fetch",
    () =>
      new Promise<Response>((resolve) => {
        finish = resolve;
      }),
  );
  const controller = new AbortController();
  const pending = readEventsPage("project", request, hash, controller.signal);
  controller.abort();
  finish(json(200, page));
  await expect(pending).rejects.toThrow();
});
it("keeps exports bounded and explicitly reports omitted fragment content", () => {
  const fragment = {
    kind: "boundary",
    boundary: {
      references: { consumerId: "exact" },
      witness: {
        nodeIds: ["exact"],
        edgeIds: [],
        evidenceIds: [],
        status: "unknown",
        provenance: "source",
        limitations: Array.from({ length: 2000 }, () => "x".repeat(4096)),
      },
      view: "routes",
      reason: "missing handler",
      dispatch: [],
      related: [],
    },
  } as const;
  const result = captureEventsFragment(fragment as never);
  expect(result.fragmentTruncated).toBe(true);
  expect(JSON.stringify(result.fragment).length).toBeLessThan(270000);
  expect(result.fragment).toMatchObject({
    kind: "boundary",
    boundary: { references: { consumerId: "exact" } },
  });
});
it("discloses missing and paginated proof and rejects evidence from another subject", async () => {
  const item = {
    kind: "boundary",
    boundary: {
      references: {},
      dispatch: [],
      related: [],
      witness: {
        nodeIds: ["consumer"],
        edgeIds: [],
        evidenceIds: ["missing"],
        provenance: "source",
        status: "unknown",
        limitations: [],
      },
    },
  } as never;
  vi.stubGlobal("fetch", async () => json(200, { items: [], nextCursor: "more" }));
  expect(
    await readEventsGapEvidence(
      "project",
      "0197aaf9-5555-7000-8000-000000000119",
      item,
      new AbortController().signal,
    ),
  ).toMatchObject({
    missingEvidenceIds: ["missing"],
    truncated: true,
    inspectedSubjects: ["consumer"],
  });
  vi.stubGlobal("fetch", async () =>
    json(200, { items: [{ id: "missing", subjectId: "foreign" }], nextCursor: "" }),
  );
  await expect(
    readEventsGapEvidence(
      "project",
      "0197aaf9-5555-7000-8000-000000000119",
      item,
      new AbortController().signal,
    ),
  ).rejects.toThrow(/другого/);
});

// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { queryBackendEvents } from "./generated/backend-projects/backend-projects";
import type {
  BackendEventsScalar,
  BackendEventsRoutesPage,
  BackendEventsJobsPage,
  BackendEventsServiceCallsPage,
  BackendLineageValueRef,
  QueryBackendEventsRequest,
} from "./generated/schemas";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

it.each<QueryBackendEventsRequest>([
  { revisionId: "revision", view: "routes" },
  { revisionId: "revision", view: "routes", seedNodeId: "emit", limit: 1, cursor: "pinned" },
  { revisionId: "revision", view: "jobs", serviceId: "service" },
  { revisionId: "revision", view: "service_calls", serviceId: "service", limit: 100 },
])("keeps $view source selectors in the POST body and passes cancellation", async (input) => {
  const fetch = vi
    .fn()
    .mockResolvedValue(
      new Response(JSON.stringify({ view: input.view, revisionId: "revision" }), { status: 200 }),
    );
  vi.stubGlobal("fetch", fetch);
  const controller = new AbortController();
  const response = await queryBackendEvents("project", input, { signal: controller.signal });
  expect(fetch).toHaveBeenCalledWith(
    "/api/backend-projects/project/events/query",
    expect.objectContaining({
      method: "POST",
      body: JSON.stringify(input),
      signal: controller.signal,
    }),
  );
  expect(response.data).toMatchObject({ view: input.view, revisionId: "revision" });
});

it("refuses unsafe scope counts before displaying a rounded value", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response('{"coverage":{"inventory":[{"knownCount":9007199254740993}]}}', {
        status: 200,
      }),
    ),
  );
  await expect(
    queryBackendEvents("project", { revisionId: "revision", view: "routes" }),
  ).rejects.toThrow(/без потери точности/);
});

const invalidRoutes: QueryBackendEventsRequest = {
  revisionId: "revision",
  view: "routes",
  // @ts-expect-error serviceId is only valid for jobs/service_calls.
  serviceId: "service",
};
const invalidJobs: QueryBackendEventsRequest = {
  revisionId: "revision",
  view: "jobs",
  // @ts-expect-error seedNodeId is only valid for routes.
  seedNodeId: "emit",
};
const contextual: BackendLineageValueRef = {
  kind: "event_field",
  nodeId: "field",
  endpointId: "consumer",
  routeId: "delivery",
};
// @ts-expect-error contextual field identity requires the exact route.
const missingRoute: BackendLineageValueRef = {
  kind: "event_field",
  nodeId: "field",
  endpointId: "consumer",
};
const known: BackendEventsScalar = { status: "known", value: "billing" };
const unknown: BackendEventsScalar = { status: "unknown", reason: "missing declaration" };
// @ts-expect-error known scalars carry the configured native value.
const missingValue: BackendEventsScalar = { status: "known" };
void [invalidRoutes, invalidJobs, contextual, missingRoute, known, unknown, missingValue];

it.each<QueryBackendEventsRequest["view"]>(["routes", "jobs", "service_calls"])(
  "preserves ownership depth incompleteness for %s",
  async (view) => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            view,
            revisionId: "revision",
            complete: false,
            truncated: true,
            truncationReasons: ["ownership_depth"],
          }),
          { status: 200 },
        ),
      ),
    );
    const response = await queryBackendEvents("project", { revisionId: "revision", view });
    expect(response.data).toMatchObject({
      view,
      complete: false,
      truncated: true,
      truncationReasons: ["ownership_depth"],
    });
  },
);

const ownershipDepthReasons: [
  BackendEventsRoutesPage["truncationReasons"][number],
  BackendEventsJobsPage["truncationReasons"][number],
  BackendEventsServiceCallsPage["truncationReasons"][number],
] = ["ownership_depth", "ownership_depth", "ownership_depth"];
void ownershipDepthReasons;

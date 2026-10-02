// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { queryBackendFlow } from "./generated/backend-projects/backend-projects";
import type { QueryBackendFlowRequest } from "./generated/schemas";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

it.each<QueryBackendFlowRequest>([
  { revisionId: "revision", view: "entrypoints", search: "cancel" },
  { revisionId: "revision", view: "steps", flowId: "flow" },
  { revisionId: "revision", view: "transitions", flowId: "flow" },
  { revisionId: "revision", view: "accesses", dataNodeId: "column", accessKind: "writes" },
  { revisionId: "revision", view: "accesses", entrypointId: "operation" },
])("keeps $view source selectors in the POST body and passes cancellation", async (input) => {
  const fetch = vi
    .fn()
    .mockResolvedValue(
      new Response(JSON.stringify({ view: input.view, revisionId: "revision" }), { status: 200 }),
    );
  vi.stubGlobal("fetch", fetch);
  const abort = new AbortController();
  const response = await queryBackendFlow("project", input, { signal: abort.signal });
  expect(fetch).toHaveBeenCalledWith(
    "/api/backend-projects/project/flow/query",
    expect.objectContaining({ method: "POST", body: JSON.stringify(input), signal: abort.signal }),
  );
  expect(response.status).toBe(200);
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
    queryBackendFlow("project", { revisionId: "revision", view: "entrypoints" }),
  ).rejects.toThrow(/без потери точности/);
});

const invalidSteps: QueryBackendFlowRequest = {
  revisionId: "revision",
  view: "steps",
  flowId: "flow",
  // @ts-expect-error entrypoint search does not belong to steps.
  search: "",
};
void invalidSteps;

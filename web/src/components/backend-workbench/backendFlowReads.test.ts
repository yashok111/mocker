// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { json } from "@/test/http";
import { parseBackendSourcePin, readFlowPage } from "./backendFlowReads";

afterEach(() => vi.unstubAllGlobals());

it("retains source URL selectors and ignores malformed selectors", () => {
  expect(
    parseBackendSourcePin({
      revisionId: "source",
      flowId: "flow",
      recordType: "edge",
      recordId: "transition",
      proposal: "proposal",
      entrypointId: [],
      facetKey: "x".repeat(201),
    }),
  ).toEqual({ revisionId: "source", flowId: "flow", recordType: "edge", recordId: "transition" });
});

it("keeps exact source pins and rejects a response from a different revision or view", async () => {
  const request = { revisionId: "source", view: "steps" as const, flowId: "flow", cursor: "page2" };
  const fetch = vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) =>
    json(200, { projectId: "project", revisionId: "new-head", view: "steps" }),
  );
  vi.stubGlobal("fetch", fetch);
  await expect(readFlowPage("project", request, new AbortController().signal)).rejects.toThrow(
    /ревизия/,
  );
  expect(JSON.parse(String(fetch.mock.calls[0]?.[1]?.body))).toEqual(request);
  vi.stubGlobal("fetch", async () =>
    json(200, { projectId: "project", revisionId: "source", view: "entrypoints" }),
  );
  await expect(readFlowPage("project", request, new AbortController().signal)).rejects.toThrow(
    /представление/,
  );
});

it("never resolves a late response after its selected pin was cancelled", async () => {
  const controller = new AbortController();
  let finish: (value: Response) => void = () => {};
  vi.stubGlobal(
    "fetch",
    () =>
      new Promise<Response>((resolve) => {
        finish = resolve;
      }),
  );
  const pending = readFlowPage(
    "project",
    { revisionId: "source", view: "entrypoints" },
    controller.signal,
  );
  controller.abort();
  finish(json(200, { projectId: "project", revisionId: "source", view: "entrypoints" }));
  await expect(pending).rejects.toThrow();
});

// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { json } from "@/test/http";
import { parseBackendSourcePin, readFlowPage } from "./backendFlowReads";

afterEach(() => vi.unstubAllGlobals());

it("retains source URL selectors and ignores malformed selectors", () => {
  expect(
    parseBackendSourcePin({
      revisionId: "0197aaf9-5555-7000-8000-000000000119",
      flowId: "flow",
      recordType: "edge",
      recordId: "transition",
      proposal: "0197aaf9-5555-7000-8000-000000000111",
      entrypointId: [],
      facetKey: "x".repeat(201),
    }),
  ).toEqual({
    revisionId: "0197aaf9-5555-7000-8000-000000000119",
    flowId: "flow",
    recordType: "edge",
    recordId: "transition",
  });
});

it("keeps exact source pins and rejects a response from a different revision or view", async () => {
  const request = {
    revisionId: "0197aaf9-5555-7000-8000-000000000119",
    view: "steps" as const,
    flowId: "flow",
    cursor: "page2",
  };
  const fetch = vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) =>
    json(200, {
      projectId: "project",
      revisionId: "0197aaf9-5555-7000-8000-000000000107",
      view: "steps",
    }),
  );
  vi.stubGlobal("fetch", fetch);
  await expect(readFlowPage("project", request, new AbortController().signal)).rejects.toThrow(
    /ревизия/,
  );
  expect(JSON.parse(String(fetch.mock.calls[0]?.[1]?.body))).toEqual(request);
  vi.stubGlobal("fetch", async () =>
    json(200, {
      projectId: "project",
      revisionId: "0197aaf9-5555-7000-8000-000000000119",
      view: "entrypoints",
    }),
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
    { revisionId: "0197aaf9-5555-7000-8000-000000000119", view: "entrypoints" },
    controller.signal,
  );
  controller.abort();
  finish(
    json(200, {
      projectId: "project",
      revisionId: "0197aaf9-5555-7000-8000-000000000119",
      view: "entrypoints",
    }),
  );
  await expect(pending).rejects.toThrow();
});
it("retains full backlink identity and malformed presence for the route gate", () => {
  const p = "0197aaf9-5555-7000-8000-000000000001",
    r = "0197aaf9-5555-7000-8000-000000000002";
  expect(parseBackendSourcePin({ changeProposalId: p, proposalRevisionId: r })).toEqual({
    changeProposalId: p,
    proposalRevisionId: r,
  });
  expect(parseBackendSourcePin({ changeProposalId: p })).toEqual({
    changeProposalId: p,
    proposalRevisionId: "",
  });
  expect(parseBackendSourcePin({ proposalRevisionId: [] })).toEqual({
    changeProposalId: "",
    proposalRevisionId: "",
  });
});

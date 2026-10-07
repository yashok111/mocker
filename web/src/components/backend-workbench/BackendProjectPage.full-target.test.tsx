import { afterEach, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderInRouter } from "@/test/render";
import { json } from "@/test/http";
import { exactIDs, isReplayList } from "@/test/backendExact";
import { BackendProjectPage } from "./BackendProjectPage";
vi.mock("./BackendFlowGraph", () => ({ BackendFlowGraph: () => null }));
vi.mock("./BackendDatabaseGraph", () => ({ BackendDatabaseGraph: () => null }));
afterEach(() => vi.unstubAllGlobals());
// The replay panel (be06f56) mounts beside any pin and reads its four
// project-scoped lists; they are not source reads, so each fixture answers
// them with the empty arrays the contract declares and leaves them out of
// the counts below.
const pinReads = (fetcher: { mock: { calls: unknown[][] } }) =>
  fetcher.mock.calls.filter(([input]) => !isReplayList(String(input)));
it.each([{}, { proposalRevisionId: "" }, { proposalRevisionId: "bad" }])(
  "refuses incomplete full URL pins before any source read %j",
  async (partial) => {
    const fetcher = vi.fn(async (input: RequestInfo | URL) =>
      isReplayList(String(input)) ? json(200, []) : json(500, { error: { message: "unrouted" } }),
    );
    vi.stubGlobal("fetch", fetcher);
    renderInRouter(
      <BackendProjectPage
        projectId={exactIDs.project}
        sourcePin={{ changeProposalId: exactIDs.project, ...partial }}
      />,
    );
    // The empty replay roster renders its own informational alert beside
    // the refusal (and its copy mentions drafts too), so the refusal is
    // found by its exact text.
    const refusal = await screen.findByText(
      "Укажите точные идентификаторы предложения и черновика.",
    );
    expect(refusal.closest('[role="alert"]')).not.toBeNull();
    expect(pinReads(fetcher)).toEqual([]);
  },
);
it.each([404, 503])(
  "does not fall back to a source while the exact full draft fails (%s)",
  async (status) => {
    const fetcher = vi.fn(async (input: RequestInfo | URL) =>
      isReplayList(String(input))
        ? json(200, [])
        : json(status, {
            error: { code: "backend_not_found", message: "Exact draft unavailable" },
          }),
    );
    vi.stubGlobal("fetch", fetcher);
    renderInRouter(
      <BackendProjectPage
        projectId={exactIDs.project}
        sourcePin={{
          changeProposalId: exactIDs.project,
          proposalRevisionId: exactIDs.snapshot,
          revisionId: exactIDs.revision,
        }}
      />,
    );
    expect(
      await screen.findByRole("button", { name: "Повторить загрузку предложения" }),
    ).toBeInTheDocument();
    const reads = pinReads(fetcher);
    expect(reads).toHaveLength(1);
    expect(String(reads[0]?.[0])).toContain(`/change-proposals/${exactIDs.project}`);
    expect(String(reads[0]?.[0])).toContain(`proposalRevisionId=${exactIDs.snapshot}`);
  },
);

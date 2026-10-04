import { afterEach, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderInRouter } from "@/test/render";
import { json } from "@/test/http";
import { exactIDs } from "@/test/backendExact";
import { BackendProjectPage } from "./BackendProjectPage";
vi.mock("./BackendFlowGraph", () => ({ BackendFlowGraph: () => null }));
vi.mock("./BackendDatabaseGraph", () => ({ BackendDatabaseGraph: () => null }));
afterEach(() => vi.unstubAllGlobals());
it.each([{}, { proposalRevisionId: "" }, { proposalRevisionId: "bad" }])(
  "refuses incomplete full URL pins before any source read %j",
  async (partial) => {
    const fetcher = vi.fn();
    vi.stubGlobal("fetch", fetcher);
    renderInRouter(
      <BackendProjectPage
        projectId={exactIDs.project}
        sourcePin={{ changeProposalId: exactIDs.project, ...partial }}
      />,
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(/точн.*предложен|черновик/);
    expect(fetcher).not.toHaveBeenCalled();
  },
);
it.each([404, 503])(
  "does not fall back to a source while the exact full draft fails (%s)",
  async (status) => {
    const fetcher = vi.fn(async (_input: RequestInfo | URL) =>
      json(status, { error: { code: "backend_not_found", message: "Exact draft unavailable" } }),
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
    expect(fetcher.mock.calls).toHaveLength(1);
    expect(String(fetcher.mock.calls[0]?.[0])).toContain(`/change-proposals/${exactIDs.project}`);
    expect(String(fetcher.mock.calls[0]?.[0])).toContain(`proposalRevisionId=${exactIDs.snapshot}`);
  },
);

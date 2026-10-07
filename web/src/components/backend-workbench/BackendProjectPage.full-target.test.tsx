import { afterEach, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderInRouter } from "@/test/render";
import { json } from "@/test/http";
import { BackendProjectPage } from "./BackendProjectPage";
import { projectId, revisionId, newerId, workspaceHTTP } from "./explorer/testFixtures";
vi.mock("./explorer/ExploreCanvas", () => ({ ExploreCanvas: () => null }));
afterEach(() => vi.unstubAllGlobals());
it.each([{}, { proposalRevisionId: "" }, { proposalRevisionId: "bad" }])(
  "refuses incomplete full URL pins before reads %j",
  async (partial) => {
    const fetcher = vi.fn();
    vi.stubGlobal("fetch", fetcher);
    renderInRouter(
      <BackendProjectPage
        projectId={projectId}
        sourcePin={{ changeProposalId: projectId, ...partial }}
      />,
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(/черновик.*предложен/);
    expect(fetcher).not.toHaveBeenCalled();
  },
);
it.each([404, 503])(
  "does not fall back to source when the exact full graph is unavailable (%s)",
  async (status) => {
    const fetcher = workspaceHTTP((path) =>
      path.endsWith("/explore/query")
        ? json(status, { error: { code: "backend_not_found", message: "Exact draft unavailable" } })
        : undefined,
    );
    renderInRouter(
      <BackendProjectPage
        projectId={projectId}
        sourcePin={{ changeProposalId: projectId, proposalRevisionId: newerId, revisionId }}
      />,
    );
    expect(await screen.findByRole("heading", { name: "Область недоступна" })).toBeVisible();
    const reads = fetcher.mock.calls.filter(([url]) => String(url).includes("/explore/query"));
    expect(reads).toHaveLength(1);
    expect(JSON.parse(String(reads[0]?.[1]?.body)).target).toEqual({
      changeProposal: { proposalId: projectId, proposalRevisionId: newerId },
    });
  },
);

it("keeps a historical diagram target on the first visit to another view", async () => {
  const diagramId = "0197aaf9-5555-7000-8000-000000000020";
  const rootId = "0197aaf9-5555-7000-8000-000000000021";
  const pin = { id: diagramId, version: 1, contentHash: "a".repeat(64) };
  workspaceHTTP((path, init) => {
    if (path.endsWith(`/diagrams/${diagramId}/versions/1`))
      return json(200, {
        projectId,
        pin,
        targetHash: "b".repeat(64),
        gaps: [],
        document: {
          format: "backend-diagram-v1",
          kind: "architecture",
          target: { revisionId },
          payload: {
            primarySystemId: rootId,
            elements: [
              {
                id: rootId,
                label: "Historical system",
                role: "software_system",
                responsibility: "",
                technology: "",
                origin: { kind: "authored", reason: "Fixture" },
                refs: [],
              },
            ],
            links: [],
          },
        },
      });
    if (path.endsWith("/diagrams/query")) {
      const body = JSON.parse(String(init?.body));
      return json(200, {
        pin,
        targetHash: "b".repeat(64),
        items: [],
        total: 0,
        nextCursor: "",
        gaps: [],
        truncated: false,
        projection: { policy: "architecture-v1", level: body.level, rootId: body.rootId },
      });
    }
    return undefined;
  });
  const navigate = vi.fn();
  renderInRouter(
    <BackendProjectPage
      projectId={projectId}
      sourcePin={{ diagramId, diagramVersion: 1, diagramHash: pin.contentHash }}
      onSourceNavigate={navigate}
    />,
  );
  await screen.findByRole("heading", { name: "Historical system" });
  const userEvent = (await import("@testing-library/user-event")).default;
  await userEvent.click(screen.getByRole("button", { name: "Данные" }));
  expect(navigate).toHaveBeenLastCalledWith(
    expect.objectContaining({ revisionId, wbView: "data", wbMode: "unmapped" }),
    false,
  );
  expect(navigate.mock.calls.at(-1)?.[0].revisionId).not.toBe(newerId);
});

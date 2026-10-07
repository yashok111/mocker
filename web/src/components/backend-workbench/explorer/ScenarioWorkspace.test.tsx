import { afterEach, expect, it, vi } from "vitest";
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ComponentProps } from "react";
import { renderWithProviders } from "@/test/render";
import { ScenarioWorkspace } from "./ScenarioWorkspace";
import { readBehavior } from "./behaviorReads";
import { readFlowMap, readSourceMap } from "./reads";
import { projectId, revisionId } from "./testFixtures";
vi.mock("./LazyExploreCanvas", () => ({ ExploreCanvas: () => null }));
vi.mock("./behaviorReads", () => ({ readBehavior: vi.fn() }));
vi.mock("./reads", () => ({ readFlowMap: vi.fn(), readSourceMap: vi.fn() }));
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});
it("keeps a saved Flow's entrypoint filter and reverse-access navigation reachable", async () => {
  const entrypoint = {
    id: "handler",
    kind: "handler",
    name: "Cancel",
    attributes: {},
    parentId: null,
    description: "",
    childCount: 0,
  };
  const scene = { nodes: [], edges: [], total: 0, title: "Flow", subtitle: "" };
  vi.mocked(readFlowMap).mockResolvedValue(scene);
  vi.mocked(readSourceMap).mockResolvedValue({ ...scene, page: {} as never });
  vi.mocked(readBehavior).mockResolvedValue({
    entrypoint,
    scene,
    representation: "sequence",
    flowId: "flow",
    expanded: [],
  });
  const search = {
    revisionId,
    flowId: "flow",
    dataNodeId: "field",
    wbQuery: "Cancel",
    wbReverseAccessKind: "reads" as const,
  };
  const navigate = vi.fn();
  const props = {
    projectId,
    target: { revisionId },
    search,
    onNavigate: navigate,
    list: false,
    saved: {
      state: { kind: "flow", filters: { search: "Cancel" }, positions: [], collapsedGroupIds: [] },
    },
    nav: { presentation: { search, scroll: 0 }, updateCamera: vi.fn() },
  } as unknown as ComponentProps<typeof ScenarioWorkspace>;
  renderWithProviders(<ScenarioWorkspace {...props} />);
  await waitFor(() =>
    expect(readFlowMap).toHaveBeenCalledWith(
      projectId,
      { revisionId },
      expect.objectContaining({ wbFlowPart: "entrypoints", wbQuery: "Cancel" }),
      expect.any(AbortSignal),
    ),
  );
  expect(await screen.findByText("Фильтр вида: Cancel")).toBeInTheDocument();
  await userEvent.click(await screen.findByRole("button", { name: "Обращения к данным" }));
  expect(navigate).toHaveBeenLastCalledWith(
    expect.objectContaining({
      revisionId,
      flowId: "flow",
      dataNodeId: "field",
      wbFlowPart: "reverse",
      wbReverseAccessKind: "reads",
    }),
  );
});

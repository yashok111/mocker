import { afterEach, expect, it, vi } from "vitest";
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ComponentProps } from "react";
import { renderWithProviders } from "@/test/render";
import { ScenarioWorkspace } from "./ScenarioWorkspace";
import { readBehavior } from "./behaviorReads";
import { readFlowMap, readSourceMap, readExploreNodes } from "./reads";
import { readBackendGraph } from "../backendGraphReads";
import { projectId, revisionId } from "./testFixtures";
vi.mock("./LazyExploreCanvas", () => ({ ExploreCanvas: () => null }));
vi.mock("./behaviorReads", () => ({ readBehavior: vi.fn() }));
vi.mock("./reads", () => ({
  readFlowMap: vi.fn(),
  readSourceMap: vi.fn(),
  readExploreNodes: vi.fn(),
}));
vi.mock("../backendGraphReads", () => ({ readBackendGraph: vi.fn() }));
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
  vi.mocked(readBackendGraph).mockResolvedValue({
    nodes: [],
    edges: [],
    total: 0,
    nextCursor: "",
  } as never);
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

function renderFlowAccessCase(
  candidates: { id: string; kind: string; name: string }[],
  explicit?: { id: string; kind: string; name: string },
) {
  const owner = {
    id: "owner",
    kind: "symbol",
    name: "Run",
    attributes: {},
    parentId: null,
    description: "",
    childCount: 0,
  };
  const scene = { nodes: [], edges: [], total: 0, title: "Flow", subtitle: "" };
  vi.mocked(readFlowMap).mockResolvedValue({ ...scene, flowId: "flow" });
  vi.mocked(readSourceMap).mockResolvedValue({ ...scene, page: {} as never });
  vi.mocked(readBehavior).mockResolvedValue({
    entrypoint: explicit ? { ...owner, ...explicit } : owner,
    scene,
    representation: "sequence",
    flowId: "flow",
    expanded: [],
  });
  vi.mocked(readBackendGraph).mockResolvedValue({
    nodes: [],
    edges: candidates.map((n) => ({
      id: `handle-${n.id}`,
      kind: "handles",
      from: n.id,
      to: owner.id,
      attributes: {},
    })),
    total: candidates.length,
    nextCursor: "",
  } as never);
  vi.mocked(readExploreNodes).mockResolvedValue(
    candidates.map((n) => ({ ...owner, ...n })) as never,
  );
  const search = { revisionId, flowId: "flow", ...(explicit ? { entrypointId: explicit.id } : {}) };
  const navigate = vi.fn();
  renderWithProviders(
    <ScenarioWorkspace
      {...({
        projectId,
        target: { revisionId },
        search,
        onNavigate: navigate,
        list: false,
        nav: { presentation: { search, scroll: 0 }, updateCamera: vi.fn() },
      } as unknown as ComponentProps<typeof ScenarioWorkspace>)}
    />,
  );
  return navigate;
}

it("uses a directly bound job for data when an architecture link opens only a Flow", async () => {
  const navigate = renderFlowAccessCase([{ id: "job", kind: "job", name: "Авточаты" }]);
  await userEvent.click(await screen.findByRole("button", { name: "Чтение и запись" }));
  expect(navigate).toHaveBeenLastCalledWith(
    expect.objectContaining({
      revisionId,
      flowId: "flow",
      entrypointId: "job",
      wbFlowPart: "accesses",
    }),
  );
  expect(readBackendGraph).toHaveBeenCalledWith(
    projectId,
    { revisionId },
    expect.objectContaining({ recordType: "edges", kind: "handles", to: "owner" }),
    expect.any(AbortSignal),
  );
});

it("keeps the explicit supported entrypoint ahead of a possible owner binding", async () => {
  const navigate = renderFlowAccessCase([], {
    id: "selected-http",
    kind: "http_operation",
    name: "Выбранная операция",
  });
  await userEvent.click(await screen.findByRole("button", { name: "Чтение и запись" }));
  expect(navigate).toHaveBeenLastCalledWith(
    expect.objectContaining({
      entrypointId: "selected-http",
      flowId: "flow",
      wbFlowPart: "accesses",
    }),
  );
  expect(readBackendGraph).not.toHaveBeenCalled();
});

it("requires a choice when the Flow owner has several direct entrypoints", async () => {
  const navigate = renderFlowAccessCase([
    { id: "job", kind: "job", name: "Задача" },
    { id: "http", kind: "http_operation", name: "API-операция" },
  ]);
  expect(await screen.findByText("Выберите точку входа для чтения и записи")).toBeInTheDocument();
  expect(navigate).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button", { name: "API-операция" }));
  expect(navigate).toHaveBeenLastCalledWith(
    expect.objectContaining({ entrypointId: "http", wbFlowPart: "accesses" }),
  );
});

it("offers the existing entrypoint catalog instead of inventing a method access scope", async () => {
  const navigate = renderFlowAccessCase([{ id: "caller-symbol", kind: "symbol", name: "Caller" }]);
  await userEvent.click(await screen.findByRole("button", { name: "Выбрать точку входа" }));
  expect(screen.queryByRole("button", { name: "Чтение и запись" })).not.toBeInTheDocument();
  expect(navigate).toHaveBeenLastCalledWith({
    revisionId,
    wbView: "scenarios",
    wbMode: "flow",
    wbFlowPart: "entrypoints",
  });
});

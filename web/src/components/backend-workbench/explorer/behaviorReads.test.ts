import { afterEach, expect, it, vi } from "vitest";
import { readBehavior } from "./behaviorReads";
import { readFlowMap, readExploreNodes } from "./reads";
import { readBackendGraph, readBackendNode } from "../backendGraphReads";
import { projectId, revisionId } from "./testFixtures";
vi.mock("./reads", async (original) => ({
  ...(await original<typeof import("./reads")>()),
  readFlowMap: vi.fn(),
  readExploreNodes: vi.fn(),
}));
vi.mock("../backendGraphReads", () => ({ readBackendGraph: vi.fn(), readBackendNode: vi.fn() }));
afterEach(() => vi.resetAllMocks());
const node = (id: string, kind = "symbol", attributes = {}) => ({
  id,
  kind,
  name: id,
  parentId: null,
  attributes,
  description: "",
  childCount: 0,
});
function fixture() {
  vi.mocked(readFlowMap).mockResolvedValue({
    nodes: [node("body", "flow_step", { stepKind: "opaque" })],
    edges: [],
    total: 1,
    title: "Flow",
    subtitle: "",
    entrypoint: node("job", "job"),
    handlerIds: ["handler"],
  });
  vi.mocked(readBackendGraph).mockImplementation(
    async (_p, _t, query) =>
      ({
        nodes: [],
        total: 1,
        nextCursor: "",
        edges:
          query.from === "job"
            ? [{ id: "handles", kind: "handles", from: "job", to: "handler", attributes: {} }]
            : query.from === "handler"
              ? [{ id: "calls", kind: "calls", from: "handler", to: "service", attributes: {} }]
              : [{ id: "nested", kind: "calls", from: "service", to: "leaf", attributes: {} }],
      }) as never,
  );
  vi.mocked(readExploreNodes).mockImplementation(async (_p, _t, ids) =>
    ids.map((id) => node(id, id === "handler" ? "handler" : "symbol")),
  );
}
it("shows imported calls rather than pretending an opaque body is an ordered scenario", async () => {
  fixture();
  const result = await readBehavior(
    projectId,
    { revisionId },
    { entrypointId: "job" },
    new AbortController().signal,
  );
  expect(result.representation).toBe("dependencies");
  expect(result.scene.nodes.map((n) => n.id)).toEqual(["job", "handler", "service"]);
  expect(result.scene.edges.map((e) => e.id)).toEqual(["handles", "calls"]);
  expect(result.scene.nodes.some((n) => n.id === "body")).toBe(false);
  expect(readBackendGraph).toHaveBeenCalledTimes(2);
});
it("expands only reachable requested callees and retains the original entrypoint", async () => {
  fixture();
  const result = await readBehavior(
    projectId,
    { revisionId },
    { entrypointId: "job", wbCalls: ["service", "unrelated"] },
    new AbortController().signal,
  );
  expect(result.scene.edges.map((e) => e.id)).toEqual(["handles", "calls", "nested"]);
  expect(result.entrypoint.id).toBe("job");
  expect(vi.mocked(readBackendGraph).mock.calls.map((c) => c[2].from)).toEqual([
    "job",
    "handler",
    "service",
  ]);
});
it("uses the imported step order when behavior is decomposed", async () => {
  fixture();
  vi.mocked(readFlowMap).mockResolvedValue({
    nodes: [
      node("one", "flow_step", { stepKind: "input" }),
      node("two", "flow_step", { stepKind: "return" }),
    ],
    edges: [{ id: "next", kind: "next", from: "one", to: "two", label: "" }],
    total: 2,
    title: "Flow",
    subtitle: "",
    flowId: "flow",
    entrypoint: node("job", "job"),
    handlerIds: ["handler"],
  });
  vi.mocked(readBackendNode).mockResolvedValue({
    ...node("flow", "flow", { entryStepId: "one" }),
    parentId: "handler",
  } as never);
  const result = await readBehavior(
    projectId,
    { revisionId },
    { entrypointId: "job" },
    new AbortController().signal,
  );
  expect(result.representation).toBe("sequence");
  expect(result.scene.edges.find((e) => e.id === "next")).toMatchObject({ from: "one", to: "two" });
  expect(result.scene.edges.some((e) => e.from === "job" && e.to === "one")).toBe(true);
  expect(readBackendGraph).not.toHaveBeenCalled();
});
it("rejects a Flow owned by a different handler instead of synthesizing an entry edge", async () => {
  fixture();
  vi.mocked(readFlowMap).mockResolvedValue({
    nodes: [node("body", "flow_step", { stepKind: "input" })],
    edges: [],
    total: 1,
    title: "Flow",
    subtitle: "",
    flowId: "other-flow",
    entrypoint: node("job", "job"),
    handlerIds: ["handler"],
  });
  vi.mocked(readBackendNode).mockResolvedValue({
    ...node("other-flow", "flow", { entryStepId: "body" }),
    parentId: "unrelated-handler",
  } as never);
  await expect(
    readBehavior(
      projectId,
      { revisionId },
      { entrypointId: "job", flowId: "other-flow" },
      new AbortController().signal,
    ),
  ).rejects.toThrow(/не принадлежит/);
  expect(readBackendGraph).not.toHaveBeenCalled();
});
it("limits the dependency context to the handler owning the selected opaque Flow", async () => {
  fixture();
  vi.mocked(readFlowMap).mockResolvedValue({
    nodes: [node("body", "flow_step", { stepKind: "opaque" })],
    edges: [],
    total: 1,
    title: "Flow",
    subtitle: "",
    flowId: "flow",
    entrypoint: node("job", "job"),
    handlerIds: ["handler", "other-handler"],
  });
  vi.mocked(readBackendNode).mockResolvedValue({
    ...node("flow", "flow", { entryStepId: "body" }),
    parentId: "handler",
  } as never);
  const original = vi.mocked(readBackendGraph).getMockImplementation()!;
  vi.mocked(readBackendGraph).mockImplementation(async (...args) => {
    const result = await original(...args);
    return args[2].from === "job"
      ? ({
          ...result,
          edges: [
            ...result.edges,
            {
              id: "other-handle",
              kind: "handles",
              from: "job",
              to: "other-handler",
              attributes: {},
            },
          ],
        } as never)
      : result;
  });
  const result = await readBehavior(
    projectId,
    { revisionId },
    { entrypointId: "job", flowId: "flow" },
    new AbortController().signal,
  );
  expect(result.scene.nodes.map((n) => n.id)).toEqual(["job", "handler", "service"]);
  expect(result.scene.edges.map((e) => e.id)).toEqual(["handles", "calls"]);
});
it("marks a zero-result expansion as unrecorded calls, never as proof of no behavior", async () => {
  fixture();
  const original = vi.mocked(readBackendGraph).getMockImplementation()!;
  vi.mocked(readBackendGraph).mockImplementation((...args) =>
    args[2].from === "service"
      ? Promise.resolve({ nodes: [], edges: [], total: 0, nextCursor: "" } as never)
      : original(...args),
  );
  const result = await readBehavior(
    projectId,
    { revisionId },
    { entrypointId: "job", wbCalls: ["service"] },
    new AbortController().signal,
  );
  expect(result.scene.nodes.find((n) => n.id === "service")).toMatchObject({
    badge: "Вызовы не описаны",
  });
  expect(
    result.scene.nodes.find((n) => n.id === "service")?.details?.["Дальнейшие вызовы"],
  ).toContain("не доказывает");
});
it("does not label a proposal's dependencies as implemented source behavior", async () => {
  fixture();
  const result = await readBehavior(
    projectId,
    { changeProposal: { proposalId: projectId, proposalRevisionId: revisionId } },
    { entrypointId: "job" },
    new AbortController().signal,
  );
  expect(result.scene.edges.every((e) => e.origin === "Модель предложения")).toBe(true);
  expect(result.scene.nodes.every((n) => n.origin === "Модель предложения")).toBe(true);
});

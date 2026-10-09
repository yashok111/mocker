import { afterEach, expect, it, vi } from "vitest";
import { readFlowAccessEntrypoints } from "./flowAccessEntrypoints";
import { readBackendGraph } from "../backendGraphReads";
import { readExploreNodes } from "./reads";
import { projectId, revisionId } from "./testFixtures";

vi.mock("../backendGraphReads", () => ({ readBackendGraph: vi.fn() }));
vi.mock("./reads", () => ({ readExploreNodes: vi.fn() }));
afterEach(() => vi.resetAllMocks());

it("pages exact handles bindings and keeps only supported endpoint identities", async () => {
  const edge = (from: string, kind = "handles", to = "owner") => ({ id: from, from, to, kind });
  vi.mocked(readBackendGraph)
    .mockResolvedValueOnce({
      nodes: [],
      edges: [
        edge("http"),
        edge("symbol"),
        edge("callee", "calls"),
        edge("other", "handles", "other-owner"),
      ],
      nextCursor: "next",
      total: 5,
    } as never)
    .mockResolvedValueOnce({
      nodes: [],
      edges: [edge("job"), edge("http")],
      nextCursor: "",
      total: 5,
    } as never);
  vi.mocked(readExploreNodes).mockResolvedValue([
    { id: "http", kind: "http_operation", name: "HTTP", attributes: {}, parentId: null },
    { id: "symbol", kind: "symbol", name: "Symbol", attributes: {}, parentId: null },
    { id: "job", kind: "job", name: "Job", attributes: {}, parentId: null },
    { id: "unrelated", kind: "consumer", name: "Unrelated", attributes: {}, parentId: null },
  ] as never);
  const signal = new AbortController().signal;
  const result = await readFlowAccessEntrypoints(projectId, { revisionId }, "owner", signal);
  expect(result.map((n) => n.id)).toEqual(["http", "job"]);
  expect(readBackendGraph).toHaveBeenNthCalledWith(
    2,
    projectId,
    { revisionId },
    expect.objectContaining({ kind: "handles", to: "owner", cursor: "next" }),
    signal,
  );
  expect(readExploreNodes).toHaveBeenCalledWith(
    projectId,
    { revisionId },
    ["http", "symbol", "job"],
    signal,
  );
});

it("does not invent a caller when no direct handles binding exists", async () => {
  vi.mocked(readBackendGraph).mockResolvedValue({
    nodes: [],
    edges: [],
    total: 0,
    nextCursor: "",
  } as never);
  const result = await readFlowAccessEntrypoints(
    projectId,
    { revisionId },
    "owner",
    new AbortController().signal,
  );
  expect(result).toEqual([]);
  expect(readExploreNodes).not.toHaveBeenCalled();
});

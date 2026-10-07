import { afterEach, expect, it } from "vitest";
import { vi } from "vitest";
import { readSourceMap, readExploreScope } from "./reads";
import { json } from "@/test/http";
import { workspaceHTTP, mapPage, projectId, revisionId } from "./testFixtures";
afterEach(() => vi.unstubAllGlobals());
const n = (i: number) => ({
  id: `node-${i}`,
  kind: "http_operation",
  name: `Operation ${i}`,
  parentId: null,
  attributes: { method: "GET", path: `/api/items/${i}` },
  description: "",
  childCount: 0,
});
it("opens the entire catalog and ignores a legacy display cursor", async () => {
  const calls: string[] = [];
  workspaceHTTP((path, init) => {
    if (!path.endsWith("/explore/query")) return;
    const body = JSON.parse(String(init?.body));
    calls.push(body.cursor);
    return json(200, {
      ...mapPage({ revisionId }),
      nodes: Array.from({ length: body.cursor ? 33 : 100 }, (_, i) =>
        n(i + (body.cursor ? 100 : 0)),
      ),
      total: 133,
      nextCursor: body.cursor ? "" : "second",
    });
  });
  const result = await readSourceMap(
    projectId,
    { revisionId },
    { wbMode: "objects", wbKind: "http_operation", wbCursor: "old-display-page" },
    new AbortController().signal,
  );
  expect(result.presentation).toBe("catalog");
  expect(result.nodes).toHaveLength(133);
  expect(result.nextCursor).toBe("");
  expect(calls).toEqual(["", "second"]);
});
it("merges neighborhood pages without duplicating their repeated anchor", async () => {
  workspaceHTTP((path, init) => {
    if (!path.endsWith("/explore/query")) return;
    const cursor = JSON.parse(String(init?.body)).cursor;
    return json(200, {
      ...mapPage({ revisionId }),
      nodes: [n(0), n(cursor ? 2 : 1)],
      edges: [
        {
          id: cursor ? "e2" : "e1",
          kind: "calls",
          from: "node-0",
          to: cursor ? "node-2" : "node-1",
          label: "",
        },
      ],
      total: 3,
      nextCursor: cursor ? "" : "second",
    });
  });
  const result = await readExploreScope(
    projectId,
    { target: { revisionId }, mode: "neighborhood", scopeId: "node-0" },
    new AbortController().signal,
  );
  expect(result.nodes).toHaveLength(3);
  expect(result.edges.map((e) => e.id)).toEqual(["e1", "e2"]);
});

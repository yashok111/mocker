import { vi } from "vitest";
import { json } from "@/test/http";
export const projectId = "0197aaf9-5555-7000-8000-000000000001";
export const revisionId = "0197aaf9-5555-7000-8000-000000000002";
export const nodeId = "0197aaf9-5555-7000-8000-000000000004";
export const newerId = "0197aaf9-5555-7000-8000-000000000007";
export const project = {
  id: projectId,
  name: "Orders",
  version: 2,
  currentRevisionId: newerId,
  repositories: [],
  capabilities: [],
  createdAt: "2026-09-30T10:00:00Z",
  updatedAt: "2026-09-30T10:00:00Z",
};
export function mapPage(target: unknown = { revisionId }) {
  return {
    target,
    targetHash: "a".repeat(64),
    semanticHash: "b".repeat(64),
    coverageStatus: "partial",
    nodes: [
      {
        id: nodeId,
        kind: "service",
        name: "Orders service",
        description: "Обрабатывает заказы",
        parentId: null,
        attributes: {},
        childCount: 10,
      },
    ],
    edges: [],
    groups: [],
    counts: { service: 1, http_operation: 33 },
    total: 1,
    edgeTotal: 0,
    nextCursor: "",
  };
}
export function workspaceHTTP(
  override?: (
    path: string,
    init: RequestInit | undefined,
  ) => Response | Promise<Response> | undefined,
) {
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const result = override?.(url.pathname, init);
    if (result) return result;
    if (url.pathname === `/api/backend-projects/${projectId}`) return json(200, project);
    if (url.pathname.endsWith("/explore/query"))
      return json(200, mapPage(JSON.parse(String(init?.body)).target));
    if (url.pathname.endsWith("/diagrams"))
      return json(200, { items: [], nextCursor: "", catalogVersion: 0 });
    if (
      ["/change-proposals", "/analyses", "/materializations", "/revisions", "/imports"].some((p) =>
        url.pathname.endsWith(p),
      )
    )
      return json(200, { items: [], nextCursor: "" });
    return json(404, { error: { code: "backend_not_found", message: "Unavailable" } });
  });
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}

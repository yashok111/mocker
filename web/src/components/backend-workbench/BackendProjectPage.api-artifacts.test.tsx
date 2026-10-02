import { afterEach, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderInRouter } from "@/test/render";
import { json } from "@/test/http";
import { BackendProjectPage } from "./BackendProjectPage";
vi.mock("./BackendDatabaseGraph", () => ({ BackendDatabaseGraph: () => <div>ER canvas</div> }));
vi.mock("./BackendFlowGraph", () => ({ BackendFlowGraph: () => <div>Flow canvas</div> }));
const projectId = "0197aaf9-5555-7000-8000-000000000001",
  revisionId = "0197aaf9-5555-7000-8000-000000000002",
  nextId = "0197aaf9-5555-7000-8000-000000000003",
  viewId = "0197aaf9-5555-7000-8000-000000000004",
  sourceId = "0197aaf9-5555-7000-8000-000000000005";
const hash = "a".repeat(64);
const coverage = { status: "partial", knownObjects: 0, denominator: null, gaps: [] };
const pin = { kind: "api_design", id: "12", revisionId: "23", contentHash: hash };
const revision = {
  id: revisionId,
  projectId,
  parentRevisionId: null,
  schemaVersion: "4",
  semanticHash: hash,
  sourceSnapshotIds: [],
  artifactPins: [pin],
  coverage,
  author: "agent",
  summary: "Pinned",
  createdAt: "2026-10-02",
};
const project = {
  id: projectId,
  name: "Project",
  version: 4,
  currentRevisionId: revisionId,
  repositories: [],
  capabilities: [],
  createdAt: "2026-10-02",
  updatedAt: "2026-10-02",
};
const ref = {
  kind: "api_design",
  artifactId: "12",
  revisionId: "23",
  contentHash: hash,
  selector: { jsonPointer: "/components/schemas/Flag" },
  objectHash: hash,
  lastKnownLabel: "Frozen API Flag",
  resolvedPointer: "/components/schemas/Flag",
};
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
function server(
  historical: boolean,
  delayedApply?: (body: unknown) => Promise<Response>,
  readProject?: () => typeof project,
) {
  const queries: string[] = [];
  vi.stubGlobal("fetch", async (url: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(url), "http://localhost").pathname;
    const input = init?.body ? JSON.parse(String(init.body)) : {};
    if (path === `/api/backend-projects/${projectId}`)
      return json(
        200,
        readProject
          ? readProject()
          : historical
            ? { ...project, currentRevisionId: nextId, version: 9 }
            : project,
      );
    if (path.includes("/saved-views/"))
      return json(200, {
        id: viewId,
        projectId,
        version: 1,
        name: "Historical flow",
        documentVersion: "saved-view-v1",
        createdAt: "2026-10-02",
        updatedAt: "2026-10-02",
        target: { revisionId },
        pins: { revisionId, semanticHash: hash, proposal: null },
        state: {
          kind: "flow",
          scope: {},
          filters: { search: "", accessKind: "", reverseAccessKind: "" },
          selection: null,
          positions: [],
          collapsedGroupIds: [],
        },
      });
    if (path === `/api/backend-projects/${projectId}/revisions/${revisionId}`)
      return json(200, revision);
    if (path === `/api/backend-projects/${projectId}/revisions/${nextId}`)
      return json(200, { ...revision, id: nextId, parentRevisionId: revisionId, artifactPins: [] });
    if (path.endsWith("/api-artifacts/query")) {
      queries.push(input.revisionId);
      return json(200, {
        revisionId: input.revisionId,
        semanticHash: hash,
        sourceSnapshotIds: [],
        pins: input.revisionId === nextId ? [] : [pin],
        nextCursor: "",
        items:
          input.revisionId === nextId
            ? []
            : [
                {
                  binding: {
                    sourceNodeId: sourceId,
                    sourceKind: "api_field",
                    sourceLastKnownLabel: "Deleted source field",
                    origin: "manual",
                    reason: "Intent",
                    ref,
                  },
                  resolution: {
                    status: "orphaned",
                    diagnostics: [],
                    currentDraftRevisionId: "24",
                    updateAvailable: true,
                  },
                },
              ],
      });
    }
    if (path.endsWith("/flow/query"))
      return json(200, {
        projectId,
        revisionId,
        semanticHash: hash,
        view: input.view,
        entrypointItems: [],
        coverage: { coverage, snapshots: [], inventory: [] },
        limitations: [],
        truncated: false,
        truncationReasons: [],
        nextCursor: "",
      });
    if (path.endsWith("/graph/query")) return json(200, { nodes: [], edges: [], nextCursor: "" });
    if (path.endsWith("/api-artifacts/preview"))
      return json(200, {
        baseRevisionId: revisionId,
        expectedVersion: 4,
        candidateHash: hash,
        semanticHash: hash,
        pins: [],
        bindings: [],
        sourceSnapshotIds: [],
        diagnostics: [],
        diff: [
          {
            sourceNodeId: sourceId,
            status: "removed",
            before: ref,
            contextChanged: false,
            changes: [],
          },
        ],
        canApply: true,
        diffTruncated: false,
      });
    if (path.endsWith("/api-artifacts/commands")) {
      const response = await delayedApply!(input);
      return response;
    }
    if (path === "/api/designs") return json(200, { designs: [{ id: 12, name: "Flags" }] });
    if (path === "/api/designs/12")
      return json(200, { design: { id: 12 }, draft: { id: 24 }, revisions: [] });
    return json(200, { items: [], nextCursor: "", coverage, snapshots: [], inventory: [] });
  });
  return queries;
}
it("keeps historical SavedView artifacts on its immutable backend revision after current API/source heads advance", async () => {
  const queries = server(true);
  renderInRouter(
    <BackendProjectPage projectId={projectId} sourcePin={{ viewId, viewVersion: 1 }} />,
  );
  expect(await screen.findByText("Frozen API Flag")).toBeInTheDocument();
  expect(screen.getByText("Дизайн API 12 · ревизия 23")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Изменить связь API" })).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Обновить состояния API" }));
  await waitFor(() => expect(queries.length).toBeGreaterThan(1));
  expect(queries.every((id) => id === revisionId)).toBe(true);
  expect(screen.getByText("Дизайн API 12 · ревизия 23")).toBeInTheDocument();
});
it("adopts acknowledged API apply revision and project version only after the reply", async () => {
  let resolve!: (response: Response) => void;
  const queries = server(
    false,
    () =>
      new Promise((done) => {
        resolve = done;
      }),
  );
  const navigate = vi.fn();
  renderInRouter(<BackendProjectPage projectId={projectId} onSourceNavigate={navigate} />);
  await userEvent.click(await screen.findByRole("button", { name: "Изменить связь API" }));
  await userEvent.type(screen.getByLabelText(/Причина связи API/), "Remove orphan");
  await userEvent.click(screen.getByRole("button", { name: "Удалить группу API" }));
  await userEvent.click(await screen.findByRole("button", { name: "Применить связи API" }));
  await waitFor(() => expect(resolve).toBeDefined());
  expect(screen.getByText("Версия проекта 4")).toBeInTheDocument();
  expect(queries).not.toContain(nextId);
  resolve(
    json(200, {
      project: { ...project, currentRevisionId: nextId, version: 5 },
      revision: { ...revision, id: nextId, parentRevisionId: revisionId, artifactPins: [] },
    }),
  );
  expect(await screen.findByText("Версия проекта 5")).toBeInTheDocument();
  await waitFor(() => expect(queries).toContain(nextId));
  expect(navigate).toHaveBeenCalledWith(expect.objectContaining({ revisionId: nextId }));
});
it("retries the lost reply exactly after explicit project refresh and keeps fresher project metadata on old receipt replay", async () => {
  let observed = false,
    writes = 0;
  const bodies: unknown[] = [];
  server(
    false,
    async (body) => {
      bodies.push(body);
      if (++writes === 1) throw new TypeError("Lost reply after commit");
      return json(200, {
        project: { ...project, currentRevisionId: nextId, version: 5 },
        revision: { ...revision, id: nextId, parentRevisionId: revisionId, artifactPins: [] },
      });
    },
    () => (observed ? { ...project, version: 10, currentRevisionId: viewId } : project),
  );
  renderInRouter(<BackendProjectPage projectId={projectId} />);
  await userEvent.click(await screen.findByRole("button", { name: "Изменить связь API" }));
  await userEvent.type(screen.getByLabelText(/Причина связи API/), "Remove orphan");
  await userEvent.click(screen.getByRole("button", { name: "Удалить группу API" }));
  await userEvent.click(await screen.findByRole("button", { name: "Применить связи API" }));
  await screen.findByText(/Результат применения неизвестен/);
  observed = true;
  await userEvent.click(screen.getByRole("button", { name: "Обновить состояние проекта" }));
  expect(await screen.findByText("Версия проекта 10")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Повторить точную попытку API" }));
  await screen.findByText("Ручных связей API нет.");
  expect(bodies[1]).toEqual(bodies[0]);
  expect(screen.getByText("Версия проекта 10")).toBeInTheDocument();
  expect(screen.queryByText("Версия проекта 5")).not.toBeInTheDocument();
});

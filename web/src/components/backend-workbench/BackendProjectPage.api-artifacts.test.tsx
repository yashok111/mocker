import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
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
  extra?: (
    path: string,
    input: Record<string, unknown>,
    signal?: AbortSignal | null,
  ) => Response | Promise<Response> | undefined,
) {
  const queries: string[] = [];
  vi.stubGlobal("fetch", async (url: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(url), "http://localhost").pathname;
    const input = init?.body ? JSON.parse(String(init.body)) : {};
    const custom = extra?.(path, input, init?.signal);
    if (custom !== undefined) return custom;
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
    if (path.endsWith("/artifacts/query"))
      return json(200, {
        revisionId: input.revisionId,
        semanticHash: hash,
        sourceSnapshotIds: [],
        pins: [pin],
        selectedPin: pin,
        view: input.view,
        hashPolicy: "api-design-raw-document-v1",
        apiBindings: [
          {
            sourceNodeId: sourceId,
            sourceKind: "api_field",
            sourceLastKnownLabel: "Deleted source field",
            origin: "manual",
            reason: "Intent",
            ref,
          },
        ],
        editorBindings: [],
        bindingsComplete: true,
        items: [],
        nextCursor: "",
        resolution: { status: "orphaned", diagnostics: [], updateAvailable: false },
        diagnostics: [],
        coverage: {
          itemsReturned: 0,
          totalItems: 0,
          nodesReturned: 0,
          edgesReturned: 0,
          diagnosticsReturned: 0,
          truncatedReasons: [],
        },
        complete: true,
      });
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

const editorBinding = {
  artifactKind: "api_design",
  artifactId: "12",
  selector: { kind: "state_diagram", diagramId: "d" },
  sourceNodeIds: [sourceId],
  sourceLabels: ["Frozen model source"],
  objectHash: hash,
  lastKnownLabel: "Frozen model",
  origin: "manual",
  reason: "Original intent",
};
function genericPage(base = revisionId, additional = false) {
  return {
    revisionId: base,
    semanticHash: hash,
    sourceSnapshotIds: [],
    pins: [pin],
    selectedPin: pin,
    view: "states",
    hashPolicy: "api-design-raw-document-v1",
    apiBindings: [
      {
        sourceNodeId: sourceId,
        sourceKind: "api_field",
        sourceLastKnownLabel: "Deleted source field",
        origin: "manual",
        reason: "Intent",
        ref,
      },
    ],
    editorBindings: [
      editorBinding,
      ...(additional
        ? [
            {
              ...editorBinding,
              selector: { kind: "state_diagram", diagramId: "external" },
              lastKnownLabel: "Externally added model",
              sourceNodeIds: [sourceId],
              sourceLabels: ["External source"],
            },
          ]
        : []),
    ],
    bindingsComplete: true,
    items: [],
    nextCursor: "",
    resolution: { status: "resolved", diagnostics: [], updateAvailable: false },
    diagnostics: [],
    coverage: {
      itemsReturned: 0,
      totalItems: 0,
      nodesReturned: 0,
      edgesReturned: 0,
      diagnosticsReturned: 0,
      truncatedReasons: [],
    },
    complete: true,
  };
}
function genericPreview(input: Record<string, unknown>) {
  return {
    baseRevisionId: input.baseRevisionId,
    expectedVersion: input.expectedVersion,
    candidateHash: hash,
    semanticHash: hash,
    pins: [pin],
    apiBindings: [],
    editorBindings: [editorBinding],
    sourceSnapshotIds: [],
    diagnostics: [],
    diff: [],
    canApply: true,
    diffTruncated: false,
  };
}
it.each(["delayed", "unknown"])(
  "generic API unlink ignores a confirmed late receipt after a newer observed host head: %s",
  async (mode) => {
    let observed = false;
    let resolve!: (r: Response) => void;
    const bodies: unknown[] = [];
    server(
      false,
      undefined,
      () => (observed ? { ...project, currentRevisionId: viewId, version: 6 } : project),
      (path, input) => {
        if (path.endsWith("/artifacts/query")) return json(200, genericPage());
        if (path.endsWith("/artifacts/preview")) return json(200, genericPreview(input));
        if (path.endsWith("/artifacts/commands")) {
          bodies.push(input);
          if (mode === "unknown" && bodies.length === 1)
            return Promise.reject(new TypeError("Lost generic reply"));
          return new Promise<Response>((r) => (resolve = r));
        }
      },
    );
    const navigate = vi.fn();
    const rendered = renderInRouter(
      <BackendProjectPage projectId={projectId} onSourceNavigate={navigate} />,
    );
    await userEvent.click(await screen.findByRole("button", { name: "Изменить связь API" }));
    await userEvent.type(screen.getByLabelText(/Причина связи API/), "Only unlink API");
    await userEvent.click(screen.getByRole("button", { name: "Удалить связь этого узла" }));
    await userEvent.click(
      await screen.findByRole("button", { name: "Применить полную группу API и моделей" }),
    );
    if (mode === "unknown") await screen.findByText(/Результат применения неизвестен/);
    else await waitFor(() => expect(resolve).toBeDefined());
    expect(bodies[0]).toMatchObject({
      baseRevisionId: revisionId,
      expectedVersion: 4,
      commands: [
        {
          apiBindings: [],
          editorBindings: [{ selector: editorBinding.selector, sourceNodeIds: [sourceId] }],
        },
      ],
    });
    observed = true;
    await userEvent.click(screen.getByRole("button", { name: "Обновить состояние проекта" }));
    await screen.findByText("Версия проекта 6");
    const navCount = navigate.mock.calls.length;
    if (mode === "unknown") {
      await userEvent.click(
        screen.getByRole("button", { name: "Повторить точное применение API" }),
      );
      await waitFor(() => expect(resolve).toBeDefined());
      expect(bodies[1]).toEqual(bodies[0]);
    }
    resolve(
      json(200, {
        project: { ...project, currentRevisionId: nextId, version: 5 },
        revision: { ...revision, id: nextId, parentRevisionId: revisionId },
      }),
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: "Применить полную группу API и моделей" }),
      ).not.toBeInTheDocument(),
    );
    expect(navigate.mock.calls.slice(navCount)).toEqual([]);
    await screen.findByText(/Применение API подтверждено/);
    expect(screen.getByText("Версия проекта 6")).toBeVisible();
    expect(rendered.queryClient.getQueryData(["/api/backend-projects/" + projectId])).toMatchObject(
      { data: { version: 6, currentRevisionId: viewId } },
    );
    expect(screen.getByText("Ручные связи API")).toBeVisible();
    expect(screen.getByText("Frozen API Flag")).toBeVisible();
  },
);
it("generic409 recovery reads the actual new head and preserves its full roster for the next deliberate preview", async () => {
  let advanced = false;
  const previews: Record<string, unknown>[] = [];
  const reads: string[] = [];
  server(
    false,
    undefined,
    () => (advanced ? { ...project, currentRevisionId: nextId, version: 5 } : project),
    (path, input) => {
      if (path === `/api/backend-projects/${projectId}/revisions/${nextId}`)
        return json(200, { ...revision, id: nextId, parentRevisionId: revisionId });
      if (path.endsWith("/artifacts/query")) {
        reads.push(String(input.revisionId));
        return json(200, genericPage(String(input.revisionId), input.revisionId === nextId));
      }
      if (path.endsWith("/artifacts/preview")) {
        previews.push(input);
        if (input.baseRevisionId === revisionId) {
          advanced = true;
          return json(409, {
            error: { code: "backend_version_conflict", message: "Head advanced" },
          });
        }
        return json(200, genericPreview(input));
      }
    },
  );
  const navigate = vi.fn();
  renderInRouter(<BackendProjectPage projectId={projectId} onSourceNavigate={navigate} />);
  const panel = await screen.findByRole("region", { name: "Сценарии и модели" });
  await within(panel).findByText("Frozen model");
  await userEvent.click(
    within(panel).getByRole("button", { name: "Изменить закреплённую группу" }),
  );
  await userEvent.type(within(panel).getByLabelText("Причина изменения"), "Initial intent");
  await userEvent.click(within(panel).getByRole("button", { name: "Предпросмотр полной группы" }));
  await within(panel).findByText("Head advanced");
  await userEvent.click(
    within(panel).getByRole("button", { name: "Закрыть конфликт и перечитать контекст" }),
  );
  await waitFor(() => expect(reads).toContain(nextId));
  expect(previews).toHaveLength(1);
  await waitFor(() =>
    expect(navigate).toHaveBeenCalledWith(expect.objectContaining({ revisionId: nextId })),
  );
  const recovered = screen.getByRole("region", { name: "Сценарии и модели" });
  await within(recovered).findByText("Externally added model");
  await userEvent.click(
    within(recovered).getByRole("button", { name: "Изменить закреплённую группу" }),
  );
  await userEvent.type(
    within(recovered).getByLabelText("Причина изменения"),
    "Deliberate current intent",
  );
  await userEvent.click(
    within(recovered).getByRole("button", { name: "Предпросмотр полной группы" }),
  );
  await waitFor(() => expect(previews).toHaveLength(2));
  expect(previews[1]).toMatchObject({
    baseRevisionId: nextId,
    expectedVersion: 5,
    commands: [
      {
        editorBindings: [
          { selector: editorBinding.selector, sourceNodeIds: [sourceId] },
          { selector: { kind: "state_diagram", diagramId: "external" }, sourceNodeIds: [sourceId] },
        ],
        apiBindings: [{ sourceNodeId: sourceId, selector: ref.selector }],
      },
    ],
  });
});

async function startGenericConflict() {
  const panel = await screen.findByRole("region", { name: "Сценарии и модели" });
  await within(panel).findByText("Frozen model");
  await userEvent.click(
    within(panel).getByRole("button", { name: "Изменить закреплённую группу" }),
  );
  await userEvent.type(within(panel).getByLabelText("Причина изменения"), "Keep explicit intent");
  await userEvent.click(within(panel).getByRole("button", { name: "Предпросмотр полной группы" }));
  await within(panel).findByText("Head advanced");
  return panel;
}
it.each(["project", "roster", "removed"])(
  "409 recovery keeps the old edit explicit when current %s cannot recover",
  async (failure) => {
    let advanced = false;
    let previewCount = 0;
    let newHeadReads = 0;
    server(
      false,
      undefined,
      () => (advanced ? { ...project, currentRevisionId: nextId, version: 5 } : project),
      (path, input) => {
        if (path === `/api/backend-projects/${projectId}` && advanced && failure === "project")
          return json(503, {
            error: { code: "unavailable", message: "Fresh project unavailable" },
          });
        if (path === `/api/backend-projects/${projectId}/revisions/${nextId}`) {
          newHeadReads++;
          return json(200, {
            ...revision,
            id: nextId,
            parentRevisionId: revisionId,
            artifactPins: failure === "removed" ? [] : [pin],
          });
        }
        if (path.endsWith("/artifacts/query")) {
          if (input.revisionId === nextId && failure === "roster")
            return json(503, {
              error: { code: "unavailable", message: "Current roster unavailable" },
            });
          return json(200, genericPage(String(input.revisionId)));
        }
        if (path.endsWith("/artifacts/preview")) {
          previewCount++;
          advanced = true;
          return json(409, {
            error: { code: "backend_version_conflict", message: "Head advanced" },
          });
        }
      },
    );
    const navigate = vi.fn();
    renderInRouter(<BackendProjectPage projectId={projectId} onSourceNavigate={navigate} />);
    const panel = await startGenericConflict();
    const navCount = navigate.mock.calls.length;
    await userEvent.click(
      within(panel).getByRole("button", { name: "Закрыть конфликт и перечитать контекст" }),
    );
    await within(panel).findByText(
      failure === "project"
        ? "Fresh project unavailable"
        : failure === "roster"
          ? "Current roster unavailable"
          : /больше не закреплена/,
    );
    expect(navigate.mock.calls.slice(navCount)).toEqual([]);
    expect(previewCount).toBe(1);
    expect(within(panel).getByLabelText("Причина изменения")).toHaveValue("Keep explicit intent");
    expect(within(panel).getByText("Frozen model")).toBeVisible();
    if (failure === "project") expect(newHeadReads).toBe(0);
  },
);
it("late409 roster completion cannot navigate a newly selected immutable context", async () => {
  let advanced = false;
  let resolve!: (r: Response) => void;
  let recoverySignal: AbortSignal | null | undefined;
  server(
    false,
    undefined,
    () => (advanced ? { ...project, currentRevisionId: nextId, version: 5 } : project),
    (path, input, signal) => {
      if (
        path === `/api/backend-projects/${projectId}/revisions/${nextId}` ||
        path === `/api/backend-projects/${projectId}/revisions/${viewId}`
      )
        return json(200, {
          ...revision,
          id: path.endsWith(viewId) ? viewId : nextId,
          parentRevisionId: revisionId,
        });
      if (path.endsWith("/artifacts/query")) {
        if (input.revisionId === nextId) {
          recoverySignal = signal;
          return new Promise<Response>((r) => (resolve = r));
        }
        return json(200, genericPage(String(input.revisionId)));
      }
      if (path.endsWith("/artifacts/preview")) {
        advanced = true;
        return json(409, { error: { code: "backend_version_conflict", message: "Head advanced" } });
      }
    },
  );
  const navigate = vi.fn();
  function Host() {
    const [selected, setSelected] = useState<string | undefined>(undefined);
    return (
      <>
        <button onClick={() => setSelected(viewId)}>Select another immutable revision</button>
        <BackendProjectPage
          projectId={projectId}
          sourcePin={selected ? { revisionId: selected } : undefined}
          onSourceNavigate={navigate}
        />
      </>
    );
  }
  renderInRouter(<Host />);
  const panel = await startGenericConflict();
  await userEvent.click(
    within(panel).getByRole("button", { name: "Закрыть конфликт и перечитать контекст" }),
  );
  await waitFor(() => expect(resolve).toBeDefined());
  await userEvent.click(screen.getByRole("button", { name: "Select another immutable revision" }));
  await waitFor(() => expect(recoverySignal?.aborted).toBe(true));
  const count = navigate.mock.calls.length;
  await act(async () => resolve(json(200, genericPage(nextId, true))));
  expect(navigate.mock.calls.slice(count)).toEqual([]);
  expect(screen.queryByText("Externally added model")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("Причина изменения")).not.toBeInTheDocument();
});

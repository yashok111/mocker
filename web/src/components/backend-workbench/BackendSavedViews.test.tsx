import { afterEach, expect, it, vi } from "vitest";
import { screen, waitFor, act } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { isReplayList } from "@/test/backendExact";
import { useBackendSavedViewSession } from "./useBackendSavedViewSession";
import { BackendSavedViews } from "./BackendSavedViews";
import type { BackendSavedView, BackendSavedViewResponse } from "@/api/generated/schemas";
vi.mock("./BackendDatabaseGraph", () => ({ BackendDatabaseGraph: () => <div>DB canvas</div> }));
vi.mock("./BackendFlowGraph", () => ({
  BackendFlowGraph: (props: {
    positions?: { nodeId: string; x: number; y: number }[];
    collapsedGroupIds?: string[];
    onPositionsChange?: (positions: { nodeId: string; x: number; y: number }[]) => void;
  }) => (
    <div>
      Flow canvas<output data-testid="flowpositions">{JSON.stringify(props.positions)}</output>
      <output data-testid="flowgroups">{JSON.stringify(props.collapsedGroupIds)}</output>
      <button onClick={() => props.onPositionsChange?.([{ nodeId: "step", x: 320, y: -20 }])}>
        Move flow fixture
      </button>
    </div>
  ),
}));
const saved: BackendSavedView = {
  id: "00000000-0000-0000-0000-000000000001",
  projectId: "project",
  version: 1,
  name: "Pinned flow",
  documentVersion: "saved-view-v1",
  createdAt: "2026-10-01",
  updatedAt: "2026-10-01",
  target: { revisionId: "0197aaf9-5555-7000-8000-000000000011" },
  pins: {
    revisionId: "0197aaf9-5555-7000-8000-000000000011",
    semanticHash: "a".repeat(64),
    proposal: null,
  },
  state: {
    kind: "flow",
    scope: { entrypointId: "endpoint", flowId: "flow", dataNodeId: "table" },
    filters: { search: "orders", accessKind: "writes", reverseAccessKind: "reads" },
    selection: { recordType: "edge", id: "access" },
    positions: [{ nodeId: "step", x: 300, y: -20 }],
    collapsedGroupIds: ["transaction"],
  },
};
function Harness() {
  const session = useBackendSavedViewSession("project", saved);
  return (
    <>
      <BackendSavedViews projectId="project" session={session} onOpen={vi.fn()} />
      <button
        onClick={() =>
          session.setState(
            session.state!.kind === "flow"
              ? { ...session.state!, filters: { ...session.state!.filters, search: "new edit" } }
              : session.state!,
          )
        }
      >
        Edit
      </button>
      <output data-testid="dirty">{String(session.dirty)}</output>
      <output data-testid="search">{session.state?.filters.search}</output>
    </>
  );
}
afterEach(() => vi.unstubAllGlobals());
it("retries an uncertain save with identical expectedVersion, body and key and retains newer edits", async () => {
  const bodies: string[] = [];
  let resolve: (value: Response) => void = () => {};
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    if (!url.endsWith("/save")) return json(200, { items: [], nextCursor: "" });
    bodies.push(String(init?.body));
    if (bodies.length === 1) throw new TypeError("lost response");
    return new Promise<Response>((r) => {
      resolve = r;
    });
  });
  renderWithProviders(<Harness />);
  await userEvent.click(screen.getByRole("button", { name: "Edit" }));
  await userEvent.click(screen.getByRole("button", { name: "Сохранить" }));
  await userEvent.click(await screen.findByRole("button", { name: "Повторить сохранение" }));
  await waitFor(() => expect(bodies).toHaveLength(2));
  expect(bodies[1]).toBe(bodies[0]);
  expect(JSON.parse(bodies[0]!)).toMatchObject({
    expectedVersion: 1,
    state: { filters: { search: "new edit" } },
  });
  await userEvent.clear(screen.getByLabelText("Название вида"));
  await userEvent.type(screen.getByLabelText("Название вида"), "New name while saving");
  await act(async () =>
    resolve(
      json(200, {
        ...saved,
        version: 2,
        state: { ...saved.state, filters: { ...saved.state.filters, search: "new edit" } },
      }),
    ),
  );
  expect(screen.getByTestId("dirty")).toHaveTextContent("true");
  expect(screen.getByLabelText("Название вида")).toHaveValue("New name while saving");
});
it("uses opened old version for CAS and keeps local changes on conflict", async () => {
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    if (!url.endsWith("/save")) return json(200, { items: [], nextCursor: "" });
    expect(JSON.parse(String(init?.body)).expectedVersion).toBe(1);
    return json(409, { code: "backend_version_conflict", error: "Conflict", currentVersion: 2 });
  });
  renderWithProviders(<Harness />);
  await userEvent.click(screen.getByRole("button", { name: "Edit" }));
  await userEvent.click(screen.getByRole("button", { name: "Сохранить" }));
  expect(await screen.findByRole("button", { name: "Загрузить текущий вид" })).toBeInTheDocument();
  expect(screen.getByTestId("search")).toHaveTextContent("new edit");
  expect(screen.getByTestId("dirty")).toHaveTextContent("true");
});

it("restores submitted Flow search, both access kinds, focus, coordinates and collapsed groups in actual reads", async () => {
  const requests: Record<string, unknown>[] = [];
  vi.stubGlobal("fetch", async (_url: string, init?: RequestInit) => {
    const input = init?.body ? JSON.parse(String(init.body)) : {};
    requests.push(input);
    if (input.view)
      return json(200, {
        projectId: "project",
        revisionId: "0197aaf9-5555-7000-8000-000000000011",
        semanticHash: "a".repeat(64),
        view: input.view,
        coverage: {
          coverage: { status: "partial", denominator: null, knownObjects: 0, gaps: [] },
          snapshots: [],
          inventory: [],
        },
        limitations: [],
        truncated: false,
        truncationReasons: [],
        nextCursor: "",
        entrypointItems: [],
        stepItems: [],
        transitionItems: [],
        accessItems: [],
      });
    return json(200, { nodes: [], edges: [], nextCursor: "", items: [] });
  });
  const { BackendFlow } = await import("./BackendFlow");
  const { BackendSavedViewContext } = await import("./backendSavedViewState");
  function FlowHarness() {
    const session = useBackendSavedViewSession("project", {
      ...saved,
      state: { ...saved.state, selection: null },
    });
    return (
      <BackendSavedViewContext value={session}>
        <BackendFlow projectId="project" revisionId="0197aaf9-5555-7000-8000-000000000011" />
        <button onClick={() => session.capture(saved.target, session.state!)}>Capture</button>
        <output data-testid="state">{JSON.stringify(session.state)}</output>
      </BackendSavedViewContext>
    );
  }
  renderWithProviders(<FlowHarness />);
  await waitFor(() =>
    expect(
      requests.some(
        (r) => r.view === "accesses" && r.dataNodeId === "table" && r.accessKind === "reads",
      ),
    ).toBe(true),
  );
  expect(requests).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ view: "entrypoints", search: "orders" }),
      expect.objectContaining({ view: "steps", flowId: "flow" }),
      expect.objectContaining({ view: "accesses", entrypointId: "endpoint", accessKind: "writes" }),
    ]),
  );
  expect(screen.getByLabelText("Endpoint или имя операции")).toHaveValue("orders");
  expect(screen.getByLabelText("Тип доступа endpoint")).toHaveValue("writes");
  expect(screen.getByLabelText("Тип обратного доступа")).toHaveValue("reads");
  expect(screen.getByTestId("state")).toHaveTextContent('"x":300');
  expect(screen.getByTestId("state")).toHaveTextContent('"collapsedGroupIds":["transaction"]');
});

it.each([404, 503])(
  "does not read a fallback source while saved GET fails (%s)",
  async (status) => {
    const requests: string[] = [];
    vi.stubGlobal("fetch", async (url: string) => {
      // The replay panel's project-scoped lists (be06f56) load beside any
      // pin and are not a source read; they are left out of the whitelist.
      if (!isReplayList(url)) requests.push(url);
      return json(status, { error: "Unavailable" });
    });
    const { BackendProjectPage } = await import("./BackendProjectPage");
    const { renderInRouter } = await import("@/test/render");
    renderInRouter(
      <BackendProjectPage
        projectId="project"
        sourcePin={{ viewId: saved.id, viewVersion: 1, revisionId: "conflicting" }}
      />,
    );
    expect(
      await screen.findByRole("button", { name: "Повторить загрузку вида" }),
    ).toBeInTheDocument();
    expect(requests).toEqual([`/api/backend-projects/project/saved-views/${saved.id}?version=1`]);
    expect(screen.queryByLabelText("Flow исходников")).not.toBeInTheDocument();
  },
);

it.each([false, true])(
  "restores Database complete state at an exact source or old designed-FK proposal (%s)",
  async (proposal) => {
    const { BackendDatabase } = await import("./BackendDatabase");
    const { BackendSavedViewContext } = await import("./backendSavedViewState");
    const { proposalNodes, proposalDetail } = await import("./backendProposalTestFixtures");
    const requests: { url: string; body: Record<string, unknown> }[] = [];
    const coverage = {
      coverage: { status: "partial", denominator: null, knownObjects: 3, gaps: [] },
      snapshots: [],
      inventory: [],
    };
    const schema = {
      ...proposalNodes[1]!,
      id: "schema",
      kind: "db_schema",
      name: "public",
      parentId: "db",
      attributes: {
        facets: {
          sql: {
            ...("facets" in proposalNodes[1]!.attributes
              ? proposalNodes[1]!.attributes.facets.sql
              : {}),
            qualifiedName: "public",
            nativeDefinition: null,
          },
        },
      },
    };
    const nodes = [
      ...proposalNodes.map((n) => (n.kind === "table" ? { ...n, parentId: "schema" } : n)),
      schema,
    ];
    const source = proposal
      ? "0197aaf9-5555-7000-8000-000000000012"
      : "0197aaf9-5555-7000-8000-000000000011";
    const target = proposal
      ? {
          proposal: {
            proposalId: "0197aaf9-5555-7000-8000-000000000013",
            proposalRevisionId: "0197aaf9-5555-7000-8000-000000000014",
          },
        }
      : { revisionId: source };
    const state: BackendSavedView["state"] = {
      kind: "database",
      scope: { datastoreId: "db", facetKey: "sql" },
      filters: { search: "orders", relationshipTableId: "orders" },
      selection: proposal
        ? { recordType: "edge", id: "designed-fk" }
        : { recordType: "node", id: "orders" },
      positions: [
        { nodeId: "orders", x: 320, y: -20 },
        { nodeId: "offpage", x: 1, y: 2 },
      ],
      collapsedGroupIds: ["schema"],
    };
    const pinned: BackendSavedView = {
      ...saved,
      target,
      state,
      pins: {
        ...saved.pins,
        revisionId: source,
        proposal: proposal
          ? {
              proposalId: "0197aaf9-5555-7000-8000-000000000013",
              proposalRevisionId: "0197aaf9-5555-7000-8000-000000000014",
              proposalSemanticHash: "b".repeat(64),
              baseRevisionId: "0197aaf9-5555-7000-8000-000000000012",
              baseSemanticHash: "a".repeat(64),
              repositoryId: "repository",
              datastoreId: "db",
              facetKey: "sql",
              effectiveGraphHash: "c".repeat(64),
            }
          : null,
      },
    };
    vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
      const input = init?.body ? JSON.parse(String(init.body)) : {};
      requests.push({ url, body: input });
      const legacyRead = Boolean(input.proposal) || /\/proposals\/[^/]+\/revisions\//.test(url);
      const scoped = (value: Record<string, unknown>) =>
        json(
          200,
          legacyRead
            ? {
                ...value,
                viewSchemaVersion: "proposal-relational-v1",
                proposalPins: pinned.pins.proposal,
              }
            : value,
        );

      if (url.includes("/saved-views")) return json(200, { items: [], nextCursor: "" });
      if (url.includes("/proposals/0197aaf9-5555-7000-8000-000000000013?"))
        return json(200, {
          ...proposalDetail,
          proposal: {
            ...proposalDetail.proposal,
            id: "0197aaf9-5555-7000-8000-000000000013",
            baseRevisionId: source,
            draftRevisionId: "0197aaf9-5555-7000-8000-000000000015",
            version: 5,
          },
          revision: {
            ...proposalDetail.revision,
            proposalId: "0197aaf9-5555-7000-8000-000000000013",
            baseRevisionId: source,
            id: "0197aaf9-5555-7000-8000-000000000014",
          },
          baseOutdated: true,
        });
      if (url.endsWith("/proposals?limit=500"))
        return json(200, { items: [proposalDetail.proposal], nextCursor: "" });
      if (url.includes("/coverage")) return scoped(coverage);
      if (url.includes("/nodes/"))
        return json(
          200,
          nodes.find((n) => n.id === "orders"),
        );
      if (input.recordType === "edges")
        return scoped({
          nodes: [],
          edges: [],
          nextCursor: "",
          proposalProjection: {
            nodes: [],
            edges: [
              {
                id: "designed-fk",
                kind: "references",
                from: "constraint",
                to: "users",
                sourceRecord: null,
                effectiveFacet: {
                  facetKey: "sql",
                  values: {},
                  propertyOrigins: {},
                  limitations: [],
                },
                limitations: [],
              },
            ],
          },
        });
      if (url.endsWith("/database/query"))
        return scoped({
          projectId: "project",
          revisionId: source,
          semanticHash: "a".repeat(64),
          datastoreId: "db",
          facetKey: "sql",
          recordType: input.recordType,
          coverage,
          facetStatus: "current",
          limitations: [],
          nextCursor: "",
          tableItems:
            input.recordType === "tables"
              ? [
                  {
                    tableId: "orders",
                    schemaId: "schema",
                    qualifiedName: "public.orders",
                    columnCount: 2,
                    facetKeys: ["sql"],
                    driftStatus: "unknown",
                  },
                ]
              : [],
          relationshipItems: [],
        });
      return scoped({ nodes, edges: [], nextCursor: "", items: [] });
    });
    function DBHarness() {
      const session = useBackendSavedViewSession("project", pinned);
      return (
        <BackendSavedViewContext value={session}>
          <BackendDatabase
            projectId="project"
            revisionId={source}
            repositoryId={proposal ? "repository" : undefined}
            pin={{ datastoreId: "db", facetKey: "sql" }}
          />
          <output data-testid="dbstate">{JSON.stringify(session.state)}</output>
        </BackendSavedViewContext>
      );
    }
    renderWithProviders(<DBHarness />);
    expect(await screen.findByLabelText("Название таблицы")).toHaveValue("orders");
    expect(await screen.findByLabelText("Связи таблицы")).toHaveValue("orders");
    expect(await screen.findByRole("button", { name: "Развернуть схему public" })).toHaveAttribute(
      "aria-expanded",
      "false",
    );
    await screen.findByLabelText("Инспектор базы данных");
    expect(screen.getByTestId("dbstate")).toHaveTextContent('"x":320');
    expect(screen.getByTestId("dbstate")).toHaveTextContent('"nodeId":"offpage"');
    await waitFor(() =>
      expect(
        requests.some(
          (r) => r.url.endsWith("/database/query") && r.body.recordType === "relationships",
        ),
      ).toBe(true),
    );
    expect(requests.filter((r) => r.url.endsWith("/database/query")).map((r) => r.body)).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ ...target, recordType: "tables", search: "orders", cursor: "" }),
        expect.objectContaining({
          ...target,
          recordType: "relationships",
          tableId: "orders",
          cursor: "",
        }),
      ]),
    );
    if (proposal) {
      await screen.findByText(/Источник обновился/);
      expect(screen.getByTestId("dbstate")).toHaveTextContent('"id":"designed-fk"');
      expect(
        requests.some(
          (r) =>
            r.url.includes("/proposals/0197aaf9-5555-7000-8000-000000000013?") &&
            !r.url.includes("proposalRevisionId=0197aaf9-5555-7000-8000-000000000014"),
        ),
      ).toBe(false);
      expect(
        requests.some(
          (r) =>
            r.body.recordType === "edges" &&
            r.body.id === "designed-fk" &&
            JSON.stringify(r.body.proposal) === JSON.stringify(target.proposal),
        ),
      ).toBe(true);
    }
  },
);

it("cancels saved A before B resolves and rejects a late foreign version without model reads", async () => {
  const { BackendProjectPage } = await import("./BackendProjectPage");
  const { renderInRouter } = await import("@/test/render");
  const { useState } = await import("react");
  let finish: (value: Response) => void = () => {};
  let oldSignal: AbortSignal | null | undefined;
  const calls: string[] = [];
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    // The replay panel's project-scoped lists (be06f56) load beside any pin
    // and are not model reads; everything else must be the saved-view read.
    if (isReplayList(url)) return json(200, []);
    calls.push(url);
    if (url.includes(saved.id)) {
      oldSignal = init?.signal;
      return new Promise<Response>((r) => {
        finish = r;
      });
    }
    return json(200, { ...saved, id: "00000000-0000-0000-0000-000000000002", version: 3 });
  });
  function GateHarness() {
    const [viewId, setViewId] = useState(saved.id);
    return (
      <>
        <button onClick={() => setViewId("00000000-0000-0000-0000-000000000002")}>
          Navigate B
        </button>
        <BackendProjectPage
          projectId="project"
          sourcePin={{ viewId, viewVersion: 1, revisionId: "conflicting" }}
        />
      </>
    );
  }
  renderInRouter(<GateHarness />);
  await waitFor(() => expect(oldSignal).toBeDefined());
  await userEvent.click(screen.getByRole("button", { name: "Navigate B" }));
  expect(await screen.findByText("Получен другой сохранённый вид или версия")).toBeInTheDocument();
  expect(oldSignal?.aborted).toBe(true);
  await act(async () => finish(json(200, saved)));
  expect(screen.queryByLabelText("Flow исходников")).not.toBeInTheDocument();
  expect(calls.every((url) => url.includes("/saved-views/"))).toBe(true);
});

it("captures actual Flow controls, creates and saves complete versions, then remounts an exact version1 GET", async () => {
  const { BackendFlow } = await import("./BackendFlow");
  const { BackendSavedViewContext } = await import("./backendSavedViewState");
  const { getBackendSavedView } = await import("@/api/generated/backend-projects/backend-projects");
  const reads: Record<string, unknown>[] = [];
  const writes: Record<string, unknown>[] = [];
  const versions: BackendSavedView[] = [];
  const operation = {
    id: "endpoint",
    externalKey: "endpoint",
    kind: "http_operation",
    name: "Cancel orders",
    parentId: null,
    attributes: {},
    evidenceIds: [],
  };
  const step = {
    ...operation,
    id: "step",
    externalKey: "step",
    kind: "flow_step",
    name: "Commit",
    parentId: "flow",
    attributes: {
      stepKind: "transaction_commit",
      analysisStatus: "partial",
      gaps: [],
      nativeText: "tx.Commit()",
      transactionContext: { status: "known", transactionId: "transaction" },
    },
  };
  const coverage = {
    coverage: { status: "partial", denominator: null, knownObjects: 3, gaps: [] },
    snapshots: [],
    inventory: [],
  };
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    const input = init?.body ? JSON.parse(String(init.body)) : {};
    if (url.includes("/saved-views")) {
      if (init?.method === "POST") {
        writes.push(input);
        const value = {
          ...saved,
          name: input.name,
          state: input.state,
          target: input.target ?? saved.target,
          version: versions.length + 1,
        };
        versions.push(value);
        return json(200, value);
      }
      if (url.includes("?version=1")) return json(200, versions[0]);
      return json(200, { items: [], nextCursor: "" });
    }
    if (url.endsWith("/flow/query")) {
      reads.push(input);
      return json(200, {
        projectId: "project",
        revisionId: "0197aaf9-5555-7000-8000-000000000011",
        semanticHash: "a".repeat(64),
        view: input.view,
        coverage,
        limitations: [],
        truncated: false,
        truncationReasons: [],
        nextCursor: "",
        entrypointItems:
          input.view === "entrypoints"
            ? [
                {
                  operation,
                  handlerIds: [],
                  flowIds: ["flow"],
                  unresolvedHandles: [],
                  evidenceIds: [],
                  limitations: [],
                },
              ]
            : [],
        stepItems: input.view === "steps" ? [step] : [],
        transitionItems: [],
        accessItems: [],
      });
    }
    if (url.includes("/nodes/step")) return json(200, step);
    if (url.endsWith("/graph/query"))
      return json(200, {
        nodes:
          input.kind === "transaction"
            ? [
                {
                  ...operation,
                  id: "transaction",
                  kind: "transaction",
                  name: "Orders transaction",
                  parentId: "flow",
                },
              ]
            : [],
        edges: [],
        nextCursor: "",
      });
    return json(200, { items: [], nextCursor: "" });
  });
  function CaptureHarness({ initial }: { initial?: BackendSavedViewResponse }) {
    const session = useBackendSavedViewSession("project", initial);
    return (
      <BackendSavedViewContext value={session}>
        <BackendSavedViews projectId="project" session={session} onOpen={() => {}} />
        <BackendFlow
          projectId="project"
          revisionId="0197aaf9-5555-7000-8000-000000000011"
          pin={{ entrypointId: "endpoint", flowId: "flow", dataNodeId: "table" }}
        />
      </BackendSavedViewContext>
    );
  }
  const mounted = renderWithProviders(<CaptureHarness />);
  await screen.findByRole("button", { name: "Открыть шаг Commit" });
  await userEvent.type(screen.getByLabelText("Endpoint или имя операции"), "orders");
  await userEvent.click(screen.getByRole("button", { name: "Найти точки входа" }));
  await userEvent.type(screen.getByLabelText("Endpoint или имя операции"), " unfinished draft");
  await userEvent.selectOptions(screen.getByLabelText("Тип доступа endpoint"), "writes");
  await userEvent.selectOptions(screen.getByLabelText("Тип обратного доступа"), "reads");
  await userEvent.click(screen.getByRole("button", { name: "Открыть шаг Commit" }));
  await userEvent.click(
    screen.getByRole("button", { name: /Свернуть транзакцию Orders transaction/ }),
  );
  await userEvent.click(screen.getByRole("button", { name: "Move flow fixture" }));
  await userEvent.click(screen.getByRole("button", { name: "Сохранить этот Flow" }));
  await userEvent.type(screen.getByLabelText("Название вида"), "Captured Flow");
  await userEvent.click(screen.getByRole("button", { name: "Сохранить" }));
  await waitFor(() => expect(versions).toHaveLength(1));
  expect(writes[0]).toMatchObject({
    target: { revisionId: "0197aaf9-5555-7000-8000-000000000011" },
    state: {
      kind: "flow",
      scope: { entrypointId: "endpoint", flowId: "flow", dataNodeId: "table" },
      filters: { search: "orders", accessKind: "writes", reverseAccessKind: "reads" },
      selection: { recordType: "node", id: "step" },
      positions: [{ nodeId: "step", x: 320, y: -20 }],
      collapsedGroupIds: ["transaction"],
    },
  });
  await userEvent.clear(screen.getByLabelText("Название вида"));
  await userEvent.type(screen.getByLabelText("Название вида"), "Version2");
  await userEvent.click(screen.getByRole("button", { name: "Сохранить" }));
  await waitFor(() => expect(versions).toHaveLength(2));
  expect(writes[1]).toMatchObject({ expectedVersion: 1, name: "Version2" });
  mounted.unmount();
  const response = await getBackendSavedView("project", saved.id, { version: 1 });
  if (response.status !== 200) throw new Error("Expected exact saved GET");
  reads.length = 0;
  renderWithProviders(<CaptureHarness initial={response.data} />);
  expect(await screen.findByLabelText("Endpoint или имя операции")).toHaveValue("orders");
  expect(screen.getByLabelText("Тип доступа endpoint")).toHaveValue("writes");
  expect(screen.getByLabelText("Тип обратного доступа")).toHaveValue("reads");
  expect(
    await screen.findByRole("button", { name: /Развернуть транзакцию Orders transaction/ }),
  ).toHaveAttribute("aria-expanded", "false");
  expect(screen.getByTestId("flowpositions")).toHaveTextContent('"x":320,"y":-20');
  expect(screen.getByTestId("flowgroups")).toHaveTextContent('["transaction"]');
  expect(await screen.findByLabelText("Инспектор Flow")).toBeInTheDocument();
  expect(
    reads.every(
      (input) => input.revisionId === "0197aaf9-5555-7000-8000-000000000011" && input.cursor === "",
    ),
  ).toBe(true);
});

it("saves a validated source Database scope change with the opened version while keeping the same source target", async () => {
  const initial: BackendSavedView = {
    ...saved,
    state: {
      kind: "database",
      scope: { datastoreId: "db", facetKey: "sql" },
      filters: { search: "" },
      selection: null,
      positions: [],
      collapsedGroupIds: [],
    },
  };
  const writes: Record<string, unknown>[] = [];
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    if (!url.endsWith("/save")) return json(200, { items: [], nextCursor: "" });
    const body = JSON.parse(String(init?.body));
    writes.push(body);
    return json(200, { ...initial, state: body.state, version: 2 });
  });
  function ScopeHarness() {
    const session = useBackendSavedViewSession("project", initial);
    return (
      <>
        <BackendSavedViews projectId="project" session={session} onOpen={() => {}} />
        <button
          onClick={() =>
            session.capture(initial.target, {
              ...initial.state,
              kind: "database",
              scope: { datastoreId: "other-db", facetKey: "orm" },
            })
          }
        >
          Change scope
        </button>
      </>
    );
  }
  renderWithProviders(<ScopeHarness />);
  await userEvent.click(screen.getByRole("button", { name: "Change scope" }));
  expect(screen.getByRole("button", { name: "Сохранить" })).toBeEnabled();
  await userEvent.click(screen.getByRole("button", { name: "Сохранить" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toMatchObject({
    expectedVersion: 1,
    state: { scope: { datastoreId: "other-db", facetKey: "orm" } },
  });
  expect(writes[0]).not.toHaveProperty("target");
});

it.each([false, true])(
  "cannot capture a workspace preview and enable Save (already captured=%s)",
  async (captured) => {
    const { BackendSavedViewContext, useWorkspaceSavedState } =
      await import("./backendSavedViewState");
    const { useSavedViewLayout } = await import("./backendSavedViewLayout");
    const writes: string[] = [];
    vi.stubGlobal("fetch", async (_url: string, init?: RequestInit) => {
      if (init?.method === "POST") writes.push(String(init.body));
      return json(200, { items: [], nextCursor: "" });
    });
    function PreviewWorkspace() {
      if (saved.state.kind !== "flow") throw new Error("Expected Flow fixture");
      const workspace = useWorkspaceSavedState(saved.target, saved.state);
      const layout = useSavedViewLayout(
        { nodes: [{ id: "step", x: 0, y: 0, width: 260, height: 120 }], edges: [] },
        workspace.state.positions,
        (p) => workspace.onStateChange({ ...workspace.state, positions: p }),
        workspace.preview,
      );
      return (
        <>
          <button onClick={layout.startPreview}>Start local preview</button>
          <button disabled={workspace.captureDisabled} onClick={workspace.capture}>
            Capture preview workspace
          </button>
          <button onClick={workspace.capture}>Force capture preview</button>
          <button onClick={layout.cancelPreview}>Cancel local preview</button>
          <output data-testid="local-preview">{String(layout.preview)}</output>
        </>
      );
    }
    function PreviewHarness() {
      const session = useBackendSavedViewSession("project", captured ? saved : undefined);
      return (
        <BackendSavedViewContext value={session}>
          <BackendSavedViews projectId="project" session={session} onOpen={() => {}} />
          <PreviewWorkspace />
        </BackendSavedViewContext>
      );
    }
    renderWithProviders(<PreviewHarness />);
    if (!captured) await userEvent.type(screen.getByLabelText("Название вида"), "Preview capture");
    await userEvent.click(screen.getByRole("button", { name: "Start local preview" }));
    expect(screen.getByRole("button", { name: "Capture preview workspace" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Force capture preview" }));
    expect(screen.getByTestId("local-preview")).toHaveTextContent("true");
    expect(screen.getByRole("button", { name: "Сохранить" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Сохранить" }));
    expect(writes).toEqual([]);
    await userEvent.click(screen.getByRole("button", { name: "Cancel local preview" }));
    await userEvent.click(screen.getByRole("button", { name: "Capture preview workspace" }));
    expect(screen.getByRole("button", { name: "Сохранить" })).toBeEnabled();
  },
);

it.each(["delayed", "uncertain", "conflict"])(
  "isolates recovery and late acknowledgements when externally navigating from cached A to cached B (%s)",
  async (outcome) => {
    const { useState } = await import("react");
    const savedB = { ...saved, id: "00000000-0000-0000-0000-000000000002", name: "Cached B" };
    let finish: (response: Response) => void = () => {};
    let oldSignal: AbortSignal | null | undefined;
    const writes: string[] = [];
    const acknowledged: string[] = [];
    vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
      if (!url.endsWith("/save")) return json(200, { items: [], nextCursor: "" });
      writes.push(url);
      oldSignal = init?.signal;
      if (outcome === "uncertain") throw new TypeError("Dropped A acknowledgement");
      if (outcome === "conflict")
        return json(409, {
          code: "backend_version_conflict",
          error: "A conflict",
          currentVersion: 2,
        });
      return new Promise<Response>((resolve) => {
        finish = resolve;
      });
    });
    function CachedHarness() {
      const [current, setCurrent] = useState(saved);
      const session = useBackendSavedViewSession("project", current, (v) =>
        acknowledged.push(v.id),
      );
      return (
        <>
          <BackendSavedViews projectId="project" session={session} onOpen={() => {}} />
          <button onClick={() => setCurrent(savedB)}>Navigate cached B</button>
          <output data-testid="current-saved">{session.saved?.id}</output>
          <output data-testid="pending-recovery">{String(!!session.pending)}</output>
          <output data-testid="conflicted">{String(session.conflict)}</output>
        </>
      );
    }
    renderWithProviders(<CachedHarness />);
    await userEvent.click(screen.getByRole("button", { name: "Сохранить" }));
    await waitFor(() => expect(writes).toHaveLength(1));
    if (outcome === "uncertain")
      await screen.findByRole("button", { name: "Повторить сохранение" });
    if (outcome === "conflict")
      await screen.findByRole("button", { name: "Загрузить текущий вид" });
    await userEvent.click(screen.getByRole("button", { name: "Navigate cached B" }));
    expect(screen.getByLabelText("Название вида")).toHaveValue("Cached B");
    expect(screen.getByTestId("pending-recovery")).toHaveTextContent("false");
    expect(screen.getByTestId("conflicted")).toHaveTextContent("false");
    expect(screen.getByRole("button", { name: "Сохранить" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "Повторить сохранение" })).not.toBeInTheDocument();
    expect(oldSignal?.aborted).toBe(true);
    if (outcome === "delayed") await act(async () => finish(json(200, { ...saved, version: 2 })));
    expect(screen.getByTestId("current-saved")).toHaveTextContent(savedB.id);
    expect(acknowledged).toEqual([]);
  },
);

it("resolves every Open-latest intent freshly and gates a cached alias until server advancement resolves", async () => {
  const { BackendProjectPage } = await import("./BackendProjectPage");
  const { renderInRouter, makeQueryClient } = await import("@/test/render");
  const { useState } = await import("react");
  const client = makeQueryClient();
  client.setDefaultOptions({
    ...client.getDefaultOptions(),
    queries: { ...client.getDefaultOptions().queries, gcTime: Infinity },
  });
  let latestReads = 0;
  let finish: (response: Response) => void = () => {};
  const adopted: number[] = [];
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    if (url === `/api/backend-projects/project/saved-views/${saved.id}`) {
      latestReads++;
      if (latestReads === 1) return json(200, saved);
      return new Promise<Response>((r) => {
        finish = r;
      });
    }
    if (url.startsWith(`/api/backend-projects/project/saved-views/${saved.id}?`))
      return json(200, {
        ...saved,
        version: Number(new URL(url, "http://localhost").searchParams.get("version")),
      });
    if (url === "/api/backend-projects/project")
      return json(200, {
        id: "project",
        name: "Pinned project",
        version: 1,
        currentRevisionId: "0197aaf9-5555-7000-8000-000000000011",
        repositories: [],
      });
    if (url === "/api/backend-projects/project/revisions/0197aaf9-5555-7000-8000-000000000011")
      return json(200, {
        id: "0197aaf9-5555-7000-8000-000000000011",
        schemaVersion: "1",
        sourceSnapshotIds: [],
        coverage: { knownObjects: 0 },
        createdAt: "2026-10-01",
        semanticHash: "a".repeat(64),
      });
    if (init?.method === "POST") throw new Error("No schema1 workspace reads expected");
    if (isReplayList(url)) return json(200, []);
    return json(200, { items: [], nextCursor: "" });
  });
  function LatestHarness() {
    const [pin, setPin] = useState<{ viewId: string; viewVersion?: number }>({ viewId: saved.id });
    return (
      <>
        <button onClick={() => setPin({ viewId: saved.id })}>Open latest intent</button>
        <BackendProjectPage
          projectId="project"
          sourcePin={pin}
          onSourceNavigate={(next) => {
            if (next.viewVersion) adopted.push(next.viewVersion);
            setPin(next as { viewId: string; viewVersion?: number });
          }}
        />
      </>
    );
  }
  renderInRouter(<LatestHarness />, { queryClient: client });
  await screen.findByRole("heading", { name: "Pinned project" });
  expect(adopted).toEqual([1]);
  await userEvent.click(screen.getByRole("button", { name: "Open latest intent" }));
  await waitFor(() => expect(latestReads).toBe(2));
  expect(screen.queryByRole("heading", { name: "Pinned project" })).not.toBeInTheDocument();
  expect(screen.getByLabelText("Загружаем точный вид")).toBeInTheDocument();
  expect(adopted).toEqual([1]);
  await act(async () => finish(json(200, { ...saved, version: 2 })));
  await screen.findByRole("heading", { name: "Pinned project" });
  expect(adopted).toEqual([1, 2]);
});

it("keeps newer presentation edits when a legitimate save acknowledgement canonicalizes the incoming version", async () => {
  const { useState } = await import("react");
  let finish: (response: Response) => void = () => {};
  let submitted: Record<string, unknown> | undefined;
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    if (!url.endsWith("/save")) return json(200, { items: [], nextCursor: "" });
    submitted = JSON.parse(String(init?.body));
    return new Promise<Response>((r) => {
      finish = r;
    });
  });
  function AckHarness() {
    const [initial, setInitial] = useState<BackendSavedViewResponse>(saved);
    const session = useBackendSavedViewSession("project", initial, setInitial);
    return (
      <>
        <BackendSavedViews projectId="project" session={session} onOpen={() => {}} />
        <button
          onClick={() => {
            if (session.state?.kind === "flow")
              session.setState({
                ...session.state,
                filters: { ...session.state.filters, search: "newest edit" },
              });
          }}
        >
          Edit during request
        </button>
        <output data-testid="ack-search">{session.state?.filters.search}</output>
        <output data-testid="ack-dirty">{String(session.dirty)}</output>
        <output data-testid="ack-version">{session.saved?.version}</output>
      </>
    );
  }
  renderWithProviders(<AckHarness />);
  await userEvent.click(screen.getByRole("button", { name: "Сохранить" }));
  await waitFor(() => expect(submitted).toBeDefined());
  await userEvent.click(screen.getByRole("button", { name: "Edit during request" }));
  await act(async () => finish(json(200, { ...saved, version: 2 })));
  expect(screen.getByTestId("ack-version")).toHaveTextContent("2");
  expect(screen.getByTestId("ack-search")).toHaveTextContent("newest edit");
  expect(screen.getByTestId("ack-dirty")).toHaveTextContent("true");
  expect(screen.getByRole("button", { name: "Сохранить" })).toBeEnabled();
});

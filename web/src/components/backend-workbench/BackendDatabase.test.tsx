import { afterEach, expect, it, vi } from "vitest";
import { useState } from "react";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import type {
  BackendColumnFacet,
  BackendNode,
  BackendEdge,
  BackendRevisionCoverage,
} from "@/api/generated/schemas";
import { BackendDatabase } from "./BackendDatabase";
import { proposalDetail } from "./backendProposalTestFixtures";

vi.mock("./BackendDatabaseGraph", () => ({ BackendDatabaseGraph: () => <div>ER canvas</div> }));

it("uses old baseline objects when a proposal is selected under a newer source head", async () => {
  const user = userEvent.setup();
  const migration: BackendNode = {
    id: "new-migration",
    externalKey: "migration:new",
    kind: "migration",
    name: "003_column_change",
    parentId: "db",
    evidenceIds: ["proof"],
    attributes: {
      facets: {
        sql: {
          ...common,
          sourceKind: "migration",
          order: { status: "known", value: 3 },
          parentIds: [],
          definition: "ALTER TABLE orders ADD COLUMN note text;",
          changes: [],
          derivationStatus: "complete",
        },
      },
    },
  };
  const currentNodes = [...nodes, migration];
  const original = fakeServer({
    graph: (input) =>
      input.recordType === "nodes"
        ? json(200, {
            nodes: input.revisionId === "base" ? nodes : currentNodes,
            edges: [],
            nextCursor: "",
          })
        : undefined,
  });
  vi.stubGlobal("fetch", async (url: RequestInfo | URL, init?: RequestInit) => {
    const pathname = new URL(String(url), "http://localhost").pathname;
    if (pathname.endsWith("/proposals"))
      return json(200, { items: [proposalDetail.proposal], nextCursor: "" });
    if (pathname.endsWith("/proposals/proposal"))
      return json(200, {
        ...proposalDetail,
        baseOutdated: true,
        currentSourceRevisionId: "current",
      });
    return original(url, init);
  });
  renderWithProviders(
    <BackendDatabase projectId="project" revisionId="current" repositoryId="repository" />,
  );
  expect(
    await screen.findByRole("button", { name: "Открыть миграцию 003_column_change" }),
  ).toBeInTheDocument();
  await screen.findByRole("option", { name: "Required users" });
  await user.selectOptions(screen.getByLabelText("Предложение изменений"), "proposal");
  await screen.findByText(/Источник обновился/);
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "Открыть миграцию 003_column_change" }),
    ).not.toBeInTheDocument(),
  );
  const objects = await screen.findByLabelText("Хранилище и другие объекты схемы");
  expect(
    within(objects).getByRole("button", { name: "Открыть хранилище Orders" }),
  ).toBeInTheDocument();
  expect(
    within(objects).queryByRole("button", { name: "Открыть миграцию 003_column_change" }),
  ).not.toBeInTheDocument();
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const common = {
  sourceKind: "sql" as const,
  dialect: "postgresql" as const,
  analysisStatus: "complete" as const,
  gaps: [],
  evidenceIds: ["proof"],
  sourceSnapshotId: "snapshot",
  freshness: { status: "current" as const, confirmedSnapshotId: "snapshot", reasons: [] },
};
const tableFacet = {
  ...common,
  qualifiedName: "public.orders",
  nativeDefinition: "CREATE TABLE orders (tenant_id bigint, user_id bigint);",
  columnsStatus: "complete" as const,
  constraintsStatus: "complete" as const,
};
const columnFacet: BackendColumnFacet = {
  ...common,
  nativeType: { status: "known", value: "bigint" },
  typeFamily: { status: "known", value: "integer" },
  nullable: { status: "known", value: false },
  defaultExpression: { status: "known", value: null },
  generatedExpression: { status: "known", value: null },
  identity: { status: "unknown", reason: "Not declared" },
  ordinal: { status: "known", value: 1 },
};
const nodes: BackendNode[] = [
  {
    id: "db",
    externalKey: "db",
    kind: "datastore",
    name: "Orders",
    parentId: null,
    evidenceIds: ["proof"],
    attributes: {
      relational: {
        facets: {
          sql: {
            ...common,
            qualifiedName: "orders",
            databaseName: "orders",
            nativeDefinition: null,
          },
        },
      },
    },
  },
  {
    id: "schema",
    externalKey: "schema",
    kind: "db_schema",
    name: "public",
    parentId: "db",
    evidenceIds: ["proof"],
    attributes: { facets: { sql: { ...common, qualifiedName: "public", nativeDefinition: null } } },
  },
  {
    id: "orders",
    externalKey: "orders",
    kind: "table",
    name: "orders",
    parentId: "schema",
    evidenceIds: ["proof"],
    attributes: { facets: { sql: tableFacet, orm: { ...tableFacet, sourceKind: "orm" } } },
  },
  {
    id: "users",
    externalKey: "users",
    kind: "table",
    name: "users",
    parentId: "schema",
    evidenceIds: ["proof"],
    attributes: {
      facets: {
        sql: { ...tableFacet, qualifiedName: "public.users" },
        orm: { ...tableFacet, sourceKind: "orm", qualifiedName: "public.users" },
      },
    },
  },
  {
    id: "tenant",
    externalKey: "tenant",
    kind: "column",
    name: "tenant_id",
    parentId: "orders",
    evidenceIds: ["proof"],
    attributes: { facets: { sql: columnFacet } },
  },
  {
    id: "column",
    externalKey: "column",
    kind: "column",
    name: "user_id",
    parentId: "orders",
    evidenceIds: ["proof"],
    attributes: {
      facets: {
        sql: columnFacet,
        orm: {
          ...columnFacet,
          sourceKind: "orm",
          nullable: { status: "known", value: true },
          freshness: { status: "stale", confirmedSnapshotId: "old", reasons: ["ORM unavailable"] },
        },
      },
    },
    facetComparison: {
      status: "different",
      pairs: [
        {
          leftFacetKey: "sql",
          rightFacetKey: "orm",
          status: "different",
          changedPaths: ["/nullable"],
          definitionDifferent: false,
        },
      ],
    },
  },
  {
    id: "constraint",
    externalKey: "constraint",
    kind: "constraint",
    name: "orders_users_fk",
    parentId: "orders",
    evidenceIds: ["proof"],
    attributes: {
      facets: {
        sql: {
          ...common,
          constraintKind: "foreign_key",
          columnIds: ["tenant", "column"],
          expression: { status: "known", value: null },
          nativeDefinition: "FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)",
          deferrable: { status: "unknown", reason: "Not established" },
          initiallyDeferred: { status: "unknown", reason: "Not established" },
        },
      },
    },
  },
];
const edge: BackendEdge = {
  id: "fk",
  externalKey: "fk",
  kind: "references",
  from: "constraint",
  to: "users",
  evidenceIds: ["proof"],
  attributes: {
    facets: {
      sql: {
        ...common,
        columnPairs: [
          { fromColumnId: "tenant", toColumnId: "target-tenant" },
          { fromColumnId: "column", toColumnId: "target-user" },
        ],
        updateAction: { status: "known", value: "cascade" },
        deleteAction: { status: "known", value: "restrict" },
        matchType: { status: "known", value: "simple" },
      },
    },
  },
};
const coverage: BackendRevisionCoverage = {
  coverage: {
    status: "partial",
    denominator: null,
    knownObjects: 7,
    gaps: ["Runtime not inspected"],
  },
  inventory: [],
  snapshots: [],
  staleCounts: { nodes: 1, edges: 0, evidence: 0 },
};

function fakeServer(
  overrides: {
    database?: (
      input: Record<string, unknown>,
      signal?: AbortSignal | null,
    ) => Response | Promise<Response>;
    evidence?: () => Response | Promise<Response>;
    graph?: (input: Record<string, unknown>) => Response | undefined;
    node?: (id: string, signal?: AbortSignal | null) => Response | Promise<Response>;
  } = {},
) {
  const fetchMock = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(url), "http://localhost");
    if (path.pathname.endsWith("/graph/query")) {
      const input = JSON.parse(String(init?.body));
      const override = overrides.graph?.(input);
      if (override) return override;
      return json(200, {
        nodes:
          input.recordType === "nodes"
            ? nodes.filter((n) => !input.parentId || n.parentId === input.parentId)
            : [],
        edges:
          input.recordType === "edges" &&
          (!input.from || input.from === "constraint") &&
          (!input.id || input.id === "fk")
            ? [edge]
            : [],
        nextCursor: "",
      });
    }
    if (path.pathname.includes("/nodes/"))
      return (
        overrides.node?.(path.pathname.split("/").at(-1)!, init?.signal) ??
        json(
          200,
          nodes.find((node) => path.pathname.endsWith(`/${node.id}`)),
        )
      );
    if (path.pathname.endsWith("/evidence")) {
      if (overrides.evidence) return overrides.evidence();
      return json(200, {
        items: [
          {
            id: "proof",
            externalKey: "proof",
            subjectId: "column",
            status: "explicit",
            method: "sql",
            source: {
              repositoryId: "repository",
              snapshotId: "snapshot",
              file: "schema.sql",
              startLine: 3,
              endLine: 5,
              contentHash: "a".repeat(64),
            },
            explanation: "Composite source proof",
            snippet: "FOREIGN KEY (tenant_id, user_id)",
          },
        ],
        nextCursor: "",
      });
    }
    if (path.pathname.endsWith("/database/query")) {
      const input = JSON.parse(String(init?.body));
      if (overrides.database) return overrides.database(input, init?.signal);
      return json(200, databasePage(input));
    }
    return json(500, { error: { code: "unrouted", message: path.pathname } });
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function databasePage(input: Record<string, unknown>) {
  return {
    projectId: "project",
    revisionId: input.revisionId,
    semanticHash: "a".repeat(64),
    datastoreId: input.datastoreId,
    facetKey: input.facetKey,
    recordType: input.recordType,
    coverage,
    facetStatus: input.facetKey === "orm" ? "unknown" : "current",
    limitations: input.facetKey === "orm" ? ["Datastore has no selected ORM proof"] : [],
    tableItems:
      input.recordType === "tables"
        ? [
            {
              tableId: "orders",
              schemaId: "schema",
              qualifiedName: "public.orders",
              columnCount: 2,
              facetKeys: ["sql", "orm"],
              driftStatus: "different",
            },
          ]
        : [],
    relationshipItems:
      input.recordType === "relationships"
        ? [
            {
              edgeId: "fk",
              constraintId: "constraint",
              sourceTableId: "orders",
              targetTableId: "users",
              columnPairs: [
                { fromColumnId: "tenant", toColumnId: "target-tenant" },
                { fromColumnId: "column", toColumnId: "target-user" },
              ],
              evidenceIds: ["proof"],
              sourceCardinality: { min: null, max: null, basis: ["Stale constraint proof"] },
              targetCardinality: {
                min: null,
                max: "1",
                basis: ["Stale constraint proof", "Independent unique target"],
              },
              status: "stale",
              targetReason: null,
            },
          ]
        : [],
    nextCursor: "",
  };
}

it("inspects ordered composite FK/actions/proof and discovers ORM from children with keyboard", async () => {
  const fetchMock = fakeServer();
  renderWithProviders(<BackendDatabase projectId="project" revisionId="revision" />);
  const facet = await screen.findByRole("combobox", { name: "Источник схемы" });
  expect(within(facet).getByRole("option", { name: "orm" })).toBeInTheDocument();
  const fk = await screen.findByRole("button", { name: "Открыть FK fk" });
  fk.focus();
  await userEvent.keyboard("{Enter}");
  const inspector = await screen.findByRole("region", { name: "Инспектор базы данных" });
  expect(await within(inspector).findByText("cascade")).toBeInTheDocument();
  expect(within(inspector).getByText("tenant → target-tenant")).toBeInTheDocument();
  expect(within(inspector).getByText("column → target-user")).toBeInTheDocument();
  expect(await within(inspector).findByText("Composite source proof")).toBeInTheDocument();
  await userEvent.selectOptions(facet, "orm");
  expect(screen.queryByRole("region", { name: "Инспектор базы данных" })).not.toBeInTheDocument();
  expect(
    (await screen.findAllByText("Datastore has no selected ORM proof")).length,
  ).toBeGreaterThan(0);
  await userEvent.click(screen.getByRole("button", { name: "Открыть таблицу public.orders" }));
  await userEvent.click(await screen.findByRole("button", { name: "Открыть колонку user_id" }));
  const column = await screen.findByRole("region", { name: "Инспектор базы данных" });
  expect(await within(column).findByText("/nullable")).toBeInTheDocument();
  expect(within(column).getByText("ORM unavailable")).toBeInTheDocument();
  expect(within(column).getAllByText(/Устарело/).length).toBeGreaterThan(0);
  const reads = fetchMock.mock.calls.filter(([url]) => String(url).endsWith("/database/query"));
  expect(reads.every(([, init]) => JSON.parse(String(init?.body)).revisionId === "revision")).toBe(
    true,
  );
});

it("cancels and ignores a late database response after revision switch", async () => {
  let resolve: ((value: Response) => void) | undefined;
  let oldSignal: AbortSignal | null | undefined;
  fakeServer({
    database: (input, signal) =>
      input.revisionId === "old"
        ? new Promise((done) => {
            if (input.recordType === "tables") {
              resolve = done;
              oldSignal = signal;
            }
          })
        : json(200, databasePage(input)),
  });
  function Host() {
    const [revision, setRevision] = useState("old");
    return (
      <>
        <button onClick={() => setRevision("new")}>Сменить ревизию</button>
        <BackendDatabase projectId="project" revisionId={revision} />
      </>
    );
  }
  renderWithProviders(<Host />);
  await waitFor(() => expect(resolve).toBeDefined());
  await userEvent.click(screen.getByRole("button", { name: "Сменить ревизию" }));
  expect(oldSignal?.aborted).toBe(true);
  await act(async () =>
    resolve?.(
      json(200, {
        ...databasePage({ revisionId: "old", recordType: "tables", facetKey: "sql" }),
        tableItems: [{ tableId: "old-table", qualifiedName: "obsolete", columnCount: 1 }],
      }),
    ),
  );
  expect(
    await screen.findByRole("button", { name: "Открыть таблицу public.orders" }),
  ).toBeInTheDocument();
  expect(screen.queryByText("obsolete")).not.toBeInTheDocument();
});

it.each(["ordinal", "order"])(
  "refuses unsafe native %s through the real generated precision boundary",
  async (property) => {
    const record =
      property === "ordinal"
        ? {
            ...nodes[4],
            attributes: {
              facets: { sql: { ...columnFacet, ordinal: { status: "known", value: "RAW_INT64" } } },
            },
          }
        : {
            id: "migration",
            externalKey: "migration",
            kind: "migration",
            name: "Create orders",
            parentId: "db",
            evidenceIds: ["proof"],
            attributes: {
              facets: {
                sql: {
                  ...common,
                  order: { status: "known", value: "RAW_INT64" },
                  parentIds: [],
                  definition: "CREATE TABLE orders (id bigint);",
                  changes: [],
                  derivationStatus: "complete",
                },
              },
            },
          };
    const records =
      property === "ordinal"
        ? nodes.map((node) => (node.id === record.id ? record : node))
        : [...nodes, record];
    const body = JSON.stringify({ nodes: records, edges: [], nextCursor: "" }).replace(
      '"RAW_INT64"',
      property === "ordinal" ? "9007199254740993" : "9007199254740995",
    );
    fakeServer({ graph: () => new Response(body, { status: 200 }) });
    renderWithProviders(<BackendDatabase projectId="project" revisionId="revision" />);
    expect(await screen.findByRole("alert")).toHaveTextContent("без потери точности");
    expect(screen.queryByRole("combobox", { name: "Источник схемы" })).not.toBeInTheDocument();
  },
);

it("retries inspector reads and does not label an incomplete evidence aggregate complete", async () => {
  let nodeReads = 0;
  let proofReads = 0;
  fakeServer({
    node: () =>
      ++nodeReads === 1
        ? json(500, { error: { code: "failed", message: "Inspector unavailable" } })
        : json(200, nodes[2]),
    evidence: () =>
      ++proofReads === 1
        ? json(200, { items: [], nextCursor: "missing-proof-page" })
        : json(500, { error: { code: "failed", message: "Second proof page unavailable" } }),
  });
  renderWithProviders(<BackendDatabase projectId="project" revisionId="revision" />);
  await userEvent.click(
    await screen.findByRole("button", { name: "Открыть таблицу public.orders" }),
  );
  const inspector = screen.getByRole("region", { name: "Инспектор базы данных" });
  expect(await within(inspector).findByRole("alert")).toHaveTextContent("Inspector unavailable");
  await userEvent.click(
    within(inspector).getByRole("button", { name: "Повторить загрузку объекта базы данных" }),
  );
  expect(await within(inspector).findByRole("alert")).toHaveTextContent(
    "Second proof page unavailable",
  );
  expect(
    within(inspector).queryByText(/Доказательства загружены полностью/),
  ).not.toBeInTheDocument();
  expect(
    within(inspector).getByRole("button", { name: "Повторить загрузку доказательств" }),
  ).toBeInTheDocument();
});

it("shows schema unavailable responses as errors instead of an empty database", async () => {
  fakeServer({
    database: () =>
      json(422, {
        error: {
          code: "backend_database_schema_unavailable",
          message: "Database facet is unavailable",
        },
      }),
  });
  renderWithProviders(<BackendDatabase projectId="project" revisionId="revision" />);
  expect(await screen.findAllByRole("alert")).not.toHaveLength(0);
  expect(screen.getAllByRole("alert")[0]).toHaveTextContent("Database facet is unavailable");
  expect(screen.queryByText("В выбранном источнике таблицы не найдены")).not.toBeInTheDocument();
  expect(
    screen.queryByText("В выбранном источнике внешние ключи не найдены"),
  ).not.toBeInTheDocument();
});

it("shows discovery read errors with retry and a separate no-relational state", async () => {
  let calls = 0;
  fakeServer({
    graph: () =>
      ++calls === 1
        ? json(500, { error: { code: "failed", message: "Discovery unavailable" } })
        : json(200, { nodes: [], edges: [], nextCursor: "" }),
  });
  renderWithProviders(<BackendDatabase projectId="project" revisionId="revision" />);
  expect(await screen.findByRole("alert")).toHaveTextContent("Discovery unavailable");
  expect(
    screen.queryByText("Реляционная схема в этой ревизии отсутствует"),
  ).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Повторить загрузку схемы" }));
  expect(
    await screen.findByText("Реляционная схема в этой ревизии отсутствует"),
  ).toBeInTheDocument();
});

it("follows discovery, child, outgoing FK and evidence cursors without hiding cross-source target disagreement", async () => {
  let proofPages = 0;
  const ormEdge: BackendEdge = {
    ...edge,
    id: "orm-fk",
    externalKey: "orm-fk",
    to: "external-users",
  };
  const fetchMock = fakeServer({
    graph: (input) => {
      if (input.recordType === "nodes" && !input.parentId)
        return json(200, {
          nodes: input.cursor ? nodes.slice(2) : nodes.slice(0, 2),
          edges: [],
          nextCursor: input.cursor ? "" : "discover-next",
        });
      if (input.recordType === "nodes" && input.parentId === "orders")
        return json(200, {
          nodes: input.cursor ? [nodes[6]] : [nodes[4], nodes[5]],
          edges: [],
          nextCursor: input.cursor ? "" : "children-next",
        });
      if (input.recordType === "edges" && input.from === "constraint")
        return json(200, {
          nodes: [],
          edges: input.cursor ? [ormEdge] : [edge],
          nextCursor: input.cursor ? "" : "targets-next",
        });
    },
    evidence: () =>
      json(200, {
        items: [
          {
            id: `proof-${++proofPages}`,
            externalKey: `proof-${proofPages}`,
            subjectId: "orders",
            status: "explicit",
            method: "sql",
            source: {
              repositoryId: "repository",
              snapshotId: "snapshot",
              file: "source.sql",
              contentHash: "a".repeat(64),
            },
            explanation: proofPages === 1 ? "First proof page" : "Second proof page",
          },
        ],
        nextCursor: proofPages === 1 ? "proof-next" : "",
      }),
  });
  renderWithProviders(<BackendDatabase projectId="project" revisionId="revision" />);
  await userEvent.click(
    await screen.findByRole("button", { name: "Открыть таблицу public.orders" }),
  );
  expect(await screen.findByText("Second proof page")).toBeInTheDocument();
  expect(
    await screen.findByText("Источники указывают разные цели; утверждения сохранены отдельно"),
  ).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /orm-fk → external-users/ })).toBeInTheDocument();
  const graphCalls = fetchMock.mock.calls
    .filter(([url]) => String(url).endsWith("/graph/query"))
    .map(([, init]) => JSON.parse(String(init?.body)));
  expect(graphCalls.some((input) => input.cursor === "discover-next")).toBe(true);
  expect(graphCalls.some((input) => input.cursor === "children-next")).toBe(true);
  expect(graphCalls.some((input) => input.cursor === "targets-next")).toBe(true);
  const close = screen.getByRole("button", { name: "Закрыть инспектор" });
  await userEvent.click(close);
  expect(screen.getByRole("button", { name: "Открыть таблицу public.orders" })).toHaveFocus();
});

it("keeps off-page targets inspectable and sends disjoint search/relationship selectors", async () => {
  const fetchMock = fakeServer({
    database: (input) =>
      json(200, {
        ...databasePage(input),
        nextCursor:
          input.recordType === "tables" && !input.cursor && !input.search ? "tables-next" : "",
      }),
  });
  renderWithProviders(<BackendDatabase projectId="project" revisionId="revision" />);
  expect(
    await screen.findByText("Целевая таблица вне текущей страницы; доступна в инспекторе"),
  ).toBeInTheDocument();
  await userEvent.click(await screen.findByRole("button", { name: "Целевая таблица users" }));
  expect(await screen.findByRole("heading", { name: "users" })).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Закрыть инспектор" }));
  await userEvent.click(screen.getByRole("button", { name: "Следующие таблицы" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Предыдущие таблицы" })).toBeEnabled(),
  );
  await userEvent.type(screen.getByRole("textbox", { name: "Название таблицы" }), "orders");
  await userEvent.click(screen.getByRole("button", { name: "Найти таблицы" }));
  await userEvent.selectOptions(screen.getByRole("combobox", { name: "Связи таблицы" }), "orders");
  await waitFor(() =>
    expect(
      fetchMock.mock.calls.some(
        ([, init]) => typeof init?.body === "string" && JSON.parse(init.body).tableId === "orders",
      ),
    ).toBe(true),
  );
  const calls = fetchMock.mock.calls
    .filter(([url]) => String(url).endsWith("/database/query"))
    .map(([, init]) => JSON.parse(String(init?.body)));
  expect(calls.find((input) => input.search === "orders").cursor).toBe("");
  expect(
    calls
      .filter((input) => input.recordType === "relationships")
      .every((input) => !Object.hasOwn(input, "search")),
  ).toBe(true);
  expect(
    calls
      .filter((input) => input.recordType === "tables")
      .every((input) => !Object.hasOwn(input, "tableId")),
  ).toBe(true);
});

it.each(["facet", "datastore"])(
  "ignores late old pages and clears selection when switching %s",
  async (switcher) => {
    let resolve: ((response: Response) => void) | undefined;
    let oldSignal: AbortSignal | null | undefined;
    fakeServer({
      graph: (input) =>
        input.recordType === "nodes" && !input.parentId
          ? json(200, {
              nodes: [...nodes, { ...nodes[0], id: "db2", name: "Second database" }],
              edges: [],
              nextCursor: "",
            })
          : undefined,
      database: (input, signal) =>
        input.recordType === "tables" && input.datastoreId === "db" && input.facetKey === "sql"
          ? new Promise((done) => {
              resolve = done;
              oldSignal = signal;
            })
          : json(200, databasePage(input)),
    });
    renderWithProviders(<BackendDatabase projectId="project" revisionId="revision" />);
    await waitFor(() => expect(resolve).toBeDefined());
    await userEvent.click(await screen.findByRole("button", { name: "Открыть FK fk" }));
    await screen.findByRole("region", { name: "Инспектор базы данных" });
    await userEvent.selectOptions(
      screen.getByRole("combobox", {
        name: switcher === "facet" ? "Источник схемы" : "Хранилище базы данных",
      }),
      switcher === "facet" ? "orm" : "db2",
    );
    expect(oldSignal?.aborted).toBe(true);
    expect(screen.queryByRole("region", { name: "Инспектор базы данных" })).not.toBeInTheDocument();
    await act(async () =>
      resolve?.(
        json(200, {
          ...databasePage({ revisionId: "revision", recordType: "tables" }),
          tableItems: [{ tableId: "old", qualifiedName: "obsolete", columnCount: 1 }],
        }),
      ),
    );
    expect(
      await screen.findByRole("button", { name: "Открыть таблицу public.orders" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("obsolete")).not.toBeInTheDocument();
  },
);

it.each(["page", "search"])(
  "retains the active FK table label while loading disjoint %s results",
  async (change) => {
    let resolveTables: ((response: Response) => void) | undefined;
    const fetchMock = fakeServer({
      database: (input) => {
        if (input.recordType === "tables" && (input.cursor || input.search))
          return new Promise((resolve) => {
            resolveTables = resolve;
          });
        return json(200, {
          ...databasePage(input),
          nextCursor: input.recordType === "tables" ? "tables-next" : input.cursor ? "" : "fk-next",
        });
      },
    });
    renderWithProviders(<BackendDatabase projectId="project" revisionId="revision" />);
    await screen.findByRole("button", { name: "Открыть таблицу public.orders" });
    const scope = screen.getByRole("combobox", { name: "Связи таблицы" });
    await userEvent.selectOptions(scope, "orders");
    await userEvent.click(await screen.findByRole("button", { name: "Следующие связи" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Предыдущие связи" })).toBeEnabled(),
    );
    if (change === "page")
      await userEvent.click(screen.getByRole("button", { name: "Следующие таблицы" }));
    else {
      await userEvent.type(screen.getByRole("textbox", { name: "Название таблицы" }), "users");
      await userEvent.click(screen.getByRole("button", { name: "Найти таблицы" }));
    }
    await waitFor(() => expect(resolveTables).toBeDefined());
    expect(scope).toHaveValue("orders");
    expect(within(scope).getByRole("option", { name: "public.orders" })).toHaveProperty(
      "selected",
      true,
    );
    await act(async () =>
      resolveTables?.(
        json(200, {
          ...databasePage({ recordType: "tables", facetKey: "sql" }),
          tableItems: [
            {
              tableId: "users",
              schemaId: "schema",
              qualifiedName: "public.users",
              columnCount: 2,
              facetKeys: ["sql"],
              driftStatus: "unknown",
            },
          ],
          nextCursor: "",
        }),
      ),
    );
    await screen.findByRole("button", { name: "Открыть таблицу public.users" });
    expect(scope).toHaveValue("orders");
    expect(within(scope).getAllByRole("option", { name: "public.orders" })).toHaveLength(1);
    expect(within(scope).getByRole("option", { name: "public.orders" })).toHaveProperty(
      "selected",
      true,
    );
    const calls = () =>
      fetchMock.mock.calls
        .filter(([url]) => String(url).endsWith("/database/query"))
        .map(([, init]) => JSON.parse(String(init?.body)));
    expect(
      calls()
        .filter((input) => input.recordType === "relationships")
        .at(-1),
    ).toMatchObject({
      tableId: "orders",
      cursor: "fk-next",
      revisionId: "revision",
      facetKey: "sql",
    });
    await userEvent.selectOptions(scope, "users");
    await waitFor(() =>
      expect(
        calls()
          .filter((input) => input.recordType === "relationships")
          .at(-1),
      ).toMatchObject({ tableId: "users", cursor: "" }),
    );
    const lastTable = calls()
      .filter((input) => input.recordType === "tables")
      .at(-1);
    expect(lastTable).toMatchObject(
      change === "page" ? { cursor: "tables-next" } : { search: "users", cursor: "" },
    );
    expect(
      calls()
        .filter((input) => input.recordType === "tables")
        .every((input) => !Object.hasOwn(input, "tableId")),
    ).toBe(true);
    expect(
      calls()
        .filter((input) => input.recordType === "relationships")
        .every((input) => !Object.hasOwn(input, "search")),
    ).toBe(true);
  },
);

const nativeObjects: BackendNode[] = [
  {
    id: "migration",
    externalKey: "migration",
    kind: "migration",
    name: "Drop obsolete",
    parentId: "db",
    evidenceIds: ["proof"],
    attributes: {
      facets: {
        migration: {
          ...common,
          sourceKind: "migration",
          order: { status: "known", value: 2 },
          parentIds: [],
          definition: "DROP TABLE obsolete;",
          changes: [
            {
              operation: "drop",
              description: "Historical drop",
              target: { kind: "historical", objectId: "old-orders", revisionId: "old-revision" },
            },
            {
              operation: "unknown",
              description: "Source target only",
              target: {
                kind: "source_only",
                externalKey: "source-obsolete",
                expectedKind: "table",
                qualifiedName: "public.source_obsolete",
                reason: "No graph object established",
              },
            },
          ],
          derivationStatus: "partial",
        },
      },
    },
  },
  {
    id: "view",
    externalKey: "view",
    kind: "view",
    name: "public.active_orders",
    parentId: "schema",
    evidenceIds: ["proof"],
    attributes: {
      facets: {
        sql: {
          ...common,
          analysisStatus: "partial",
          gaps: ["View dependencies incomplete"],
          qualifiedName: "public.active_orders",
          definition: "SELECT * FROM orders;",
          dependencyIds: ["orders"],
          materialized: false,
          dependenciesStatus: "partial",
        },
      },
    },
  },
  {
    id: "routine",
    externalKey: "routine",
    kind: "symbol",
    name: "public.audit_orders",
    parentId: "schema",
    evidenceIds: ["proof"],
    attributes: {
      language: "sql",
      databaseRoutine: {
        facets: {
          sql: {
            ...common,
            analysisStatus: "unsupported",
            gaps: ["Routine body unsupported"],
            qualifiedName: "public.audit_orders",
            definition: "BEGIN ... END;",
            dependencyIds: [],
            routineKind: "procedure",
            bodyStatus: "unsupported",
          },
        },
      },
    },
  },
];

it("opens native hierarchy objects and navigates migration historical targets with their own pin", async () => {
  const fetchMock = fakeServer({
    graph: (input) =>
      input.recordType === "nodes" && !input.parentId
        ? json(200, { nodes: [...nodes, ...nativeObjects], edges: [], nextCursor: "" })
        : undefined,
    node: (id) =>
      json(
        200,
        id === "old-orders"
          ? { ...nodes[2], id, name: "Historical orders" }
          : [...nodes, ...nativeObjects].find((node) => node.id === id),
      ),
  });
  renderWithProviders(<BackendDatabase projectId="project" revisionId="revision" />);
  for (const [name, heading] of [
    ["Открыть хранилище Orders", "Orders"],
    ["Открыть схему public", "public"],
  ]) {
    await userEvent.click(await screen.findByRole("button", { name }));
    expect(
      await within(screen.getByRole("region", { name: "Инспектор базы данных" })).findByRole(
        "heading",
        { name: heading },
      ),
    ).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Закрыть инспектор" }));
    expect(screen.getByRole("button", { name })).toHaveFocus();
  }
  const migration = screen.getByRole("button", { name: "Открыть миграцию Drop obsolete" });
  migration.focus();
  await userEvent.keyboard("{Enter}");
  const inspector = screen.getByRole("region", { name: "Инспектор базы данных" });
  expect(
    await within(inspector).findByText(
      "Выбранный источник не содержит утверждения об этом объекте; другие источники доступны ниже",
    ),
  ).toBeInTheDocument();
  await userEvent.click(within(inspector).getByText("Источник migration · migration · postgresql"));
  expect(within(inspector).getByText(/Только в исходниках: public.source_obsolete/)).toBeVisible();
  expect(within(inspector).getByText("DROP TABLE obsolete;")).toBeVisible();
  await userEvent.click(
    within(inspector).getByRole("button", { name: "Исторический объект: old-orders" }),
  );
  expect(
    await within(screen.getByRole("region", { name: "Инспектор базы данных" })).findByRole(
      "heading",
      { name: "Historical orders" },
    ),
  ).toBeInTheDocument();
  expect(
    fetchMock.mock.calls.some(([url]) =>
      String(url).includes("/revisions/old-revision/nodes/old-orders"),
    ),
  ).toBe(true);
  expect(
    fetchMock.mock.calls.some(([url]) =>
      String(url).includes("/revisions/revision/nodes/old-orders"),
    ),
  ).toBe(false);
  expect(fetchMock.mock.calls.some(([url]) => String(url).includes("/nodes/source-obsolete"))).toBe(
    false,
  );
  await userEvent.click(screen.getByRole("button", { name: "Закрыть инспектор" }));
  expect(migration).toHaveFocus();
});

it.each([
  [
    "Открыть представление public.active_orders",
    "View dependencies incomplete",
    "Полнота зависимостей",
    "SELECT * FROM orders;",
  ],
  [
    "Открыть тело базы данных public.audit_orders",
    "Routine body unsupported",
    "Полнота анализа тела",
    "BEGIN ... END;",
  ],
])("opens %s with native incomplete source states", async (name, gap, property, definition) => {
  fakeServer({
    graph: (input) =>
      input.recordType === "nodes" && !input.parentId
        ? json(200, { nodes: [...nodes, ...nativeObjects], edges: [], nextCursor: "" })
        : undefined,
    node: (id) =>
      json(
        200,
        [...nodes, ...nativeObjects].find((node) => node.id === id),
      ),
  });
  renderWithProviders(<BackendDatabase projectId="project" revisionId="revision" />);
  const entry = await screen.findByRole("button", { name });
  await userEvent.click(entry);
  const inspector = screen.getByRole("region", { name: "Инспектор базы данных" });
  expect(await within(inspector).findByText(gap)).toBeVisible();
  expect(within(inspector).getByText(property)).toBeVisible();
  expect(within(inspector).getByText(definition)).toBeVisible();
  await userEvent.click(screen.getByRole("button", { name: "Закрыть инспектор" }));
  expect(entry).toHaveFocus();
});

it("cancels a late native migration inspector read after switching facet", async () => {
  let resolveNode: ((response: Response) => void) | undefined;
  let oldSignal: AbortSignal | null | undefined;
  fakeServer({
    graph: (input) =>
      input.recordType === "nodes" && !input.parentId
        ? json(200, { nodes: [...nodes, ...nativeObjects], edges: [], nextCursor: "" })
        : undefined,
    node: (id, signal) =>
      id === "migration"
        ? new Promise((resolve) => {
            resolveNode = resolve;
            oldSignal = signal;
          })
        : json(
            200,
            nodes.find((node) => node.id === id),
          ),
  });
  renderWithProviders(<BackendDatabase projectId="project" revisionId="revision" />);
  await userEvent.click(
    await screen.findByRole("button", { name: "Открыть миграцию Drop obsolete" }),
  );
  await waitFor(() => expect(resolveNode).toBeDefined());
  await userEvent.selectOptions(screen.getByRole("combobox", { name: "Источник схемы" }), "orm");
  expect(oldSignal?.aborted).toBe(true);
  await act(async () => resolveNode?.(json(200, nativeObjects[0])));
  expect(screen.queryByRole("region", { name: "Инспектор базы данных" })).not.toBeInTheDocument();
  expect(screen.queryByText("DROP TABLE obsolete;")).not.toBeInTheDocument();
});

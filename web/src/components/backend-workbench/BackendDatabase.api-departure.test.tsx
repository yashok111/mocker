import { useCallback, useRef } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { fill } from "@/test/user";
import { renderInRouter, renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { BackendDatabase } from "./BackendDatabase";
import { BackendAPIArtifactsContext } from "./BackendAPIArtifacts";
import { BackendSavedViewContext } from "./backendSavedViewState";
import { BackendGraphInventory, BackendRecordInspector } from "./BackendGraphInventory";
import { BackendProjectPage } from "./BackendProjectPage";

vi.mock("./BackendDatabaseGraph", () => ({ BackendDatabaseGraph: () => <div>ER canvas</div> }));
vi.mock("./BackendFlowGraph", () => ({ BackendFlowGraph: () => <div>Flow canvas</div> }));
const hash = "a".repeat(64);
const common = {
  sourceKind: "sql",
  dialect: "postgresql",
  analysisStatus: "complete",
  gaps: [],
  evidenceIds: [],
  sourceSnapshotId: "snapshot",
  freshness: { status: "current", confirmedSnapshotId: "snapshot", reasons: [] },
};
const tableFacet = {
  ...common,
  qualifiedName: "public.orders",
  nativeDefinition: null,
  columnsStatus: "complete",
  constraintsStatus: "complete",
};
const nodes = [
  { id: "operation", kind: "http_operation", name: "GET /orders", parentId: null, attributes: {} },
  {
    id: "db",
    kind: "datastore",
    name: "Orders",
    parentId: null,
    attributes: {
      relational: {
        facets: {
          sql: {
            ...common,
            qualifiedName: "orders",
            databaseName: "orders",
            nativeDefinition: null,
          },
          orm: {
            ...common,
            sourceKind: "orm",
            qualifiedName: "orders",
            databaseName: "orders",
            nativeDefinition: null,
          },
        },
      },
    },
  },
  {
    id: "db2",
    kind: "datastore",
    name: "Reports",
    parentId: null,
    attributes: {
      relational: {
        facets: {
          sql: {
            ...common,
            qualifiedName: "reports",
            databaseName: "reports",
            nativeDefinition: null,
          },
        },
      },
    },
  },
  {
    id: "orders",
    kind: "table",
    name: "orders",
    parentId: "db",
    attributes: { facets: { sql: tableFacet } },
  },
  {
    id: "users",
    kind: "table",
    name: "users",
    parentId: "db",
    attributes: { facets: { sql: { ...tableFacet, qualifiedName: "public.users" } } },
  },
  {
    id: "column",
    kind: "column",
    name: "user_id",
    parentId: "orders",
    attributes: {
      facets: {
        sql: {
          ...common,
          nativeType: { status: "known", value: "bigint" },
          typeFamily: { status: "known", value: "integer" },
          nullable: { status: "known", value: false },
          defaultExpression: { status: "known", value: null },
          generatedExpression: { status: "known", value: null },
          identity: { status: "unknown", reason: "Unknown" },
          ordinal: { status: "known", value: 1 },
        },
      },
    },
  },
  ...["field", "field2"].map((id) => ({
    id,
    kind: "api_field",
    name: id,
    parentId: "operation",
    attributes: {
      direction: "response",
      location: "body",
      selector: { kind: "json_pointer", value: "/from/source" },
    },
  })),
].map((node) => ({ ...node, externalKey: node.id, evidenceIds: [] }));
const pin = { kind: "api_design", id: "12", revisionId: "23", contentHash: hash };
const revision = {
  id: "base",
  projectId: "project",
  schemaVersion: "4",
  semanticHash: hash,
  sourceSnapshotIds: ["snapshot"],
  artifactPins: [pin],
  parentRevisionId: null,
  coverage: { status: "partial", knownObjects: nodes.length, denominator: null, gaps: [] },
  author: "agent",
  summary: "Pinned source",
  createdAt: "2026-10-02",
};
const project = {
  id: "project",
  name: "Project",
  version: 4,
  currentRevisionId: "base",
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
  objectHash: hash,
  selector: { jsonPointer: "/components/schemas/Flag" },
  resolvedPointer: "/components/schemas/Flag",
  lastKnownLabel: "Frozen Flag",
};
const coverage = {
  coverage: { status: "partial", knownObjects: nodes.length, denominator: null, gaps: [] },
  snapshots: [],
  inventory: [],
};
function server() {
  const writes: string[] = [];
  vi.stubGlobal("fetch", async (url: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(url), "http://localhost").pathname;
    const body = init?.body ? JSON.parse(String(init.body)) : {};
    if (path === "/api/backend-projects/project")
      return json(200, {
        id: "project",
        name: "Project",
        version: 4,
        currentRevisionId: "base",
        repositories: [],
        capabilities: [],
        createdAt: "2026-10-02",
        updatedAt: "2026-10-02",
      });
    if (path.endsWith("/coverage")) return json(200, coverage);
    if (path.endsWith("/revisions/base")) return json(200, revision);
    if (path.endsWith("/revisions/applied"))
      return json(200, { ...revision, id: "applied", parentRevisionId: "base" });
    if (path.includes("/nodes/"))
      return json(
        200,
        nodes.find((node) => path.endsWith(`/${node.id}`)),
      );
    if (path.endsWith("/graph/query"))
      return json(200, {
        nodes:
          body.recordType === "nodes"
            ? nodes.filter((node) => !body.parentId || node.parentId === body.parentId)
            : [],
        edges:
          body.recordType === "edges" && (body.from === "operation" || body.id === "graph-edge")
            ? [
                {
                  id: "graph-edge",
                  externalKey: "graph-edge",
                  kind: "calls",
                  from: "operation",
                  to: "users",
                  attributes: {},
                  evidenceIds: [],
                },
              ]
            : [],
        nextCursor: "",
      });
    if (path.endsWith("/flow/query"))
      return json(200, {
        projectId: "project",
        revisionId: body.revisionId,
        semanticHash: hash,
        view: body.view,
        entrypointItems: [],
        coverage,
        limitations: [],
        truncated: false,
        truncationReasons: [],
        nextCursor: "",
      });
    if (path.endsWith("/database/query"))
      return json(200, {
        projectId: "project",
        revisionId: "base",
        semanticHash: hash,
        datastoreId: "db",
        facetKey: "sql",
        recordType: body.recordType,
        coverage,
        facetStatus: "current",
        limitations: [],
        tableItems:
          body.recordType === "tables"
            ? ["orders", "users"].map((id) => ({
                tableId: id,
                qualifiedName: `public.${id}`,
                columnCount: 1,
                facetKeys: ["sql"],
                driftStatus: "same",
              }))
            : [],
        relationshipItems: [],
        nextCursor: "",
      });
    if (path.endsWith("/lineage/query"))
      return json(200, {
        projectId: "project",
        revisionId: "base",
        semanticHash: hash,
        seed: body.seed,
        direction: body.direction,
        policy: "field-lineage-traversal-v1",
        coverage,
        limitations: [],
        truncated: false,
        truncationReasons: [],
        nextCursor: "",
        visitedValueCount: 3,
        examinedMappingCount: 1,
        items: [
          {
            mapping: {
              id: "mapping",
              name: "Field mapping",
              kind: "field_mapping",
              evidenceIds: [],
              attributes: {
                sources: [
                  { kind: "api_field", nodeId: "field" },
                  { kind: "api_field", nodeId: "field2" },
                ],
                destination: { kind: "column", nodeId: "column", facetKey: "sql" },
                transform: { kind: "copy", description: "Copy", redacted: false },
                analysisStatus: "complete",
                gaps: [],
              },
            },
            via: body.seed,
            depth: 1,
            witnessMappingIds: ["mapping"],
            status: "resolved",
            expansion: "boundary",
            expandedValues: [],
            reasons: [],
            requiresReview: false,
          },
        ],
      });
    if (path.endsWith("/api-artifacts/query"))
      return json(200, {
        revisionId: body.revisionId,
        semanticHash: hash,
        sourceSnapshotIds: ["snapshot"],
        pins: [pin],
        nextCursor: "",
        items: [
          {
            binding: {
              sourceNodeId: "field",
              sourceKind: "api_field",
              sourceLastKnownLabel: "Field",
              origin: "manual",
              reason: "Prior intent",
              ref,
            },
            resolution: {
              status: "resolved",
              currentDraftRevisionId: "24",
              updateAvailable: true,
              diagnostics: [],
            },
          },
        ],
      });
    if (path === "/api/designs") return json(200, { designs: [{ id: 12, name: "Flags" }] });
    if (path === "/api/designs/12")
      return json(200, {
        design: { id: 12 },
        draft: { id: 24 },
        revisions: [
          { id: 23, summary: "Frozen" },
          { id: 24, summary: "New" },
        ],
      });
    if (path.endsWith("/api-artifacts/preview"))
      return json(200, {
        baseRevisionId: "base",
        expectedVersion: 4,
        candidateHash: hash,
        semanticHash: hash,
        sourceSnapshotIds: ["snapshot"],
        pins: [pin],
        bindings: [],
        diagnostics: [],
        diff: [],
        diffTruncated: false,
        canApply: true,
      });
    if (path.endsWith("/api-artifacts/commands")) {
      writes.push(String(init?.body));
      if (writes.length === 1) throw new TypeError("Lost reply after commit");
      return json(200, {
        project: { ...project, version: 5, currentRevisionId: "applied" },
        revision: { ...revision, id: "applied", parentRevisionId: "base" },
      });
    }
    return json(200, { items: [], nextCursor: "" });
  });
  return writes;
}
function Host({
  savedDirty = false,
  mode = "database",
}: {
  savedDirty?: boolean;
  mode?: "database" | "inventory" | "record";
}) {
  const dirty = useRef(false);
  const onDirty = useCallback((value: boolean) => {
    dirty.current = value;
  }, []);
  return (
    <BackendAPIArtifactsContext
      value={{
        revision: revision as never,
        projectVersion: 4,
        canEdit: true,
        onDirty,
        onApplied: () => {},
        guard: () => !dirty.current || window.confirm("Leave pending API editor?"),
      }}
    >
      <BackendSavedViewContext value={savedDirty ? ({ dirty: true } as never) : null}>
        {mode === "database" ? (
          <BackendDatabase projectId="project" revisionId="base" />
        ) : mode === "inventory" ? (
          <BackendGraphInventory projectId="project" revisionId="base" schemaVersion="4" />
        ) : (
          <BackendRecordInspector
            projectId="project"
            revisionId="base"
            recordType="node"
            id="operation"
          />
        )}
      </BackendSavedViewContext>
    </BackendAPIArtifactsContext>
  );
}
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
async function openFieldEditor(savedDirty = false) {
  renderWithProviders(<Host savedDirty={savedDirty} />);
  await userEvent.click(
    await screen.findByRole("button", { name: "Открыть таблицу public.orders" }),
  );
  await userEvent.click(await screen.findByRole("button", { name: "Открыть колонку user_id" }));
  await userEvent.click(await screen.findByRole("button", { name: "Происхождение значения" }));
  await userEvent.click(
    await screen.findByRole("button", { name: "Открыть значение field · поле API" }),
  );
  await startEdit();
}
async function startEdit(scope?: HTMLElement) {
  await userEvent.click(
    await (scope ? within(scope) : screen).findByRole("button", { name: "Изменить связь API" }),
  );
  await userEvent.selectOptions(screen.getByLabelText("Дизайн API"), "12");
  await userEvent.selectOptions(await screen.findByLabelText("Ревизия API"), "24");
  await fill(screen.getByLabelText("Авторский JSON Pointer поля"), "/components/schemas/Changed");
  await fill(screen.getByLabelText(/Причина связи API/), "Keep exact intent");
}
async function leave(action: string) {
  if (action === "inner close")
    await userEvent.click(screen.getByRole("button", { name: "Закрыть инспектор Flow" }));
  if (action === "lineage replacement")
    await userEvent.click(
      screen.getByRole("button", { name: "Открыть значение field2 · поле API" }),
    );
  if (action === "outer selection")
    await userEvent.click(screen.getByRole("button", { name: "Открыть таблицу public.users" }));
  if (action === "search reset")
    await userEvent.click(screen.getByRole("button", { name: "Найти таблицы" }));
  if (action === "relationship filter")
    await userEvent.selectOptions(screen.getByLabelText("Связи таблицы"), "users");
  if (action === "datastore switch")
    await userEvent.selectOptions(screen.getByLabelText("Хранилище базы данных"), "db2");
  if (action === "facet switch")
    await userEvent.selectOptions(screen.getByLabelText("Источник схемы"), "orm");
  if (action === "outer close")
    await userEvent.click(
      within(screen.getByRole("region", { name: "Инспектор базы данных" })).getByRole("button", {
        name: "Закрыть инспектор",
      }),
    );
}
it.each([
  "inner close",
  "lineage replacement",
  "outer selection",
  "outer close",
  "search reset",
  "relationship filter",
  "datastore switch",
  "facet switch",
])(
  "preserves unknown exact retry and dirty intent on denied %s, and confirms departure with one prompt",
  async (action) => {
    const writes = server();
    const confirm = vi.fn(() => false);
    vi.stubGlobal("confirm", confirm);
    await openFieldEditor();
    await userEvent.click(screen.getByRole("button", { name: "Предпросмотр связей API" }));
    await userEvent.click(await screen.findByRole("button", { name: "Применить связи API" }));
    await screen.findByTestId("api-pin-unknown-outcome");
    const mounted = screen.getByTestId("backend-api-artifacts");
    await leave(action);
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId("backend-api-artifacts")).toBe(mounted);
    expect(screen.getByLabelText(/Причина связи API/)).toHaveValue("Keep exact intent");
    expect(screen.getByLabelText(/Причина связи API/)).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Повторить точную попытку API" }));
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(writes[1]).toBe(writes[0]);
    await startEdit();
    confirm.mockClear();
    await leave(action);
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText("Авторский JSON Pointer поля")).toHaveValue(
      "/components/schemas/Changed",
    );
    confirm.mockClear().mockReturnValue(true);
    await leave(action);
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(screen.queryByLabelText(/Причина связи API/)).not.toBeInTheDocument();
  },
);
it.each(["datastore switch", "facet switch"])(
  "confirms combined saved-view/API dirty %s once while preserving denied intent",
  async (action) => {
    server();
    const confirm = vi.fn(() => false);
    vi.stubGlobal("confirm", confirm);
    await openFieldEditor(true);
    await leave(action);
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText(/Причина связи API/)).toHaveValue("Keep exact intent");
    confirm.mockClear().mockReturnValue(true);
    await leave(action);
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(screen.queryByLabelText(/Причина связи API/)).not.toBeInTheDocument();
  },
);
async function graphDeparture(action: string) {
  if (action === "replace value")
    await userEvent.click(screen.getByRole("button", { name: "Открыть точное значение field2" }));
  if (action === "relationship")
    await userEvent.click(screen.getByRole("button", { name: "Основания связи calls graph-edge" }));
  if (action === "close graph")
    await userEvent.click(
      within(screen.getByRole("region", { name: "Инспектор объекта" })).getByRole("button", {
        name: "Закрыть инспектор",
      }),
    );
  if (action === "replace graph node")
    await userEvent.click(screen.getByRole("button", { name: "Открыть объект users" }));
  if (action === "reset graph search")
    await userEvent.click(screen.getByRole("button", { name: "Найти объекты" }));
}
it.each([
  ["inventory", "replace value"],
  ["inventory", "relationship"],
  ["inventory", "close graph"],
  ["inventory", "replace graph node"],
  ["inventory", "reset graph search"],
  ["record", "relationship"],
] as const)(
  "keeps the actual %s graph host pending API editor on denied %s and retries exactly",
  async (mode, action) => {
    const writes = server();
    const confirm = vi.fn(() => false);
    vi.stubGlobal("confirm", confirm);
    renderWithProviders(<Host mode={mode} />);
    if (mode === "inventory")
      await userEvent.click(
        await screen.findByRole("button", { name: "Открыть объект GET /orders" }),
      );
    await userEvent.click(
      await screen.findByRole("button", { name: "Открыть точное значение field" }),
    );
    const inner = await screen.findByRole("region", { name: "Инспектор Flow" });
    await startEdit(inner);
    await userEvent.click(screen.getByRole("button", { name: "Предпросмотр связей API" }));
    await userEvent.click(await screen.findByRole("button", { name: "Применить связи API" }));
    await screen.findByTestId("api-pin-unknown-outcome");
    const mounted = within(inner).getByTestId("backend-api-artifacts");
    await graphDeparture(action);
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(
      within(screen.getByRole("region", { name: "Инспектор Flow" })).getByTestId(
        "backend-api-artifacts",
      ),
    ).toBe(mounted);
    await userEvent.click(screen.getByRole("button", { name: "Повторить точную попытку API" }));
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(writes[1]).toBe(writes[0]);
    await startEdit(inner);
    confirm.mockClear().mockReturnValue(true);
    await graphDeparture(action);
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(screen.queryByLabelText(/Причина связи API/)).not.toBeInTheDocument();
  },
);
it("keeps the real project Flow instance's unknown claim when a duplicate graph instance mounts and unmounts", async () => {
  const writes = server();
  const confirm = vi.fn(() => true);
  vi.stubGlobal("confirm", confirm);
  renderInRouter(
    <BackendProjectPage
      projectId="project"
      sourcePin={{ revisionId: "base", recordId: "field", recordType: "node" }}
    />,
  );
  const inner = await screen.findByRole("region", { name: "Инспектор Flow" });
  await startEdit(inner);
  await userEvent.click(screen.getByRole("button", { name: "Предпросмотр связей API" }));
  await userEvent.click(await screen.findByRole("button", { name: "Применить связи API" }));
  await screen.findByTestId("api-pin-unknown-outcome");
  await userEvent.click(screen.getByRole("button", { name: "Открыть объект GET /orders" }));
  const graph = await screen.findByRole("region", { name: "Инспектор объекта" });
  const second = (await within(graph).findAllByTestId("backend-api-artifacts")).find((panel) =>
    within(panel).queryByRole("button", { name: "Изменить связь API" }),
  );
  expect(second).toBeDefined();
  await userEvent.click(within(second!).getByRole("button", { name: "Изменить связь API" }));
  expect(within(second!).queryByLabelText(/Причина связи API/)).not.toBeInTheDocument();
  expect(await within(second!).findByRole("alert")).toHaveTextContent(
    "Завершите изменение связи API в другом инспекторе",
  );
  await userEvent.click(within(graph).getByRole("button", { name: "Закрыть инспектор" }));
  expect(inner).toBeInTheDocument();
  expect(within(inner).getByTestId("api-pin-unknown-outcome")).toBeInTheDocument();
  confirm.mockClear().mockReturnValue(false);
  await userEvent.click(within(inner).getByRole("button", { name: "Закрыть инспектор Flow" }));
  expect(confirm).toHaveBeenCalledTimes(1);
  expect(inner).toBeInTheDocument();
  await userEvent.click(
    within(inner).getByRole("button", { name: "Повторить точную попытку API" }),
  );
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(writes[1]).toBe(writes[0]);
  expect(await screen.findByText("Версия проекта 5")).toBeInTheDocument();
});

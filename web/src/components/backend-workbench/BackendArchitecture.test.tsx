import { useState } from "react";
import type { BackendWorkspaceSearch } from "./backendWorkspaceSearch";
import type { BackendDiagramViewState } from "@/api/generated/schemas";
import { afterEach, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders, makeQueryClient } from "@/test/render";
import { json } from "@/test/http";
import { BackendArchitecture } from "./BackendArchitecture";
vi.mock("./BackendArchitectureGraph", () => ({
  BackendArchitectureGraph: ({ state }: { state: BackendDiagramViewState }) => (
    <div>
      C4 canvas<output aria-label="Позиции C4">{JSON.stringify(state.positions)}</output>
    </div>
  ),
}));
afterEach(() => {
  vi.unstubAllGlobals();
  sessionStorage.clear();
});
const id = "10000000-0000-4000-8000-000000000001";
const rid = "10000000-0000-4000-8000-000000000002";
const did = "10000000-0000-4000-8000-000000000003";
function mockEmpty() {
  vi.stubGlobal("fetch", async (url: string) => {
    if (
      url.includes("/diagram-views") ||
      url.includes("/saved-views") ||
      url.endsWith("/diagrams?limit=100")
    )
      return json(200, { items: [], nextCursor: "", catalogVersion: 0 });
    if (url.endsWith("/" + id)) return json(200, { id, currentRevisionId: rid, name: "Orders" });
    return json(404, { error: { message: "Exact version unavailable" } });
  });
}
it("fresh project shows explicit setup without fabricated C4", async () => {
  mockEmpty();
  const user = userEvent.setup();
  renderWithProviders(
    <BackendArchitecture
      projectId={id}
      search={{}}
      onNavigate={vi.fn()}
      onDetailedNavigate={vi.fn()}
    />,
  );
  expect(await screen.findByText(/Архитектурный mapping ещё не создан/)).toBeVisible();
  expect(screen.queryByText("C4 canvas")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Создать mapping" }));
  expect(screen.getByLabelText("Название системы")).toBeVisible();
});
it("unavailable explicit pin does not fall back to current mapping", async () => {
  mockEmpty();
  renderWithProviders(
    <BackendArchitecture
      projectId={id}
      search={{ diagramId: did, diagramVersion: 8, diagramHash: "a".repeat(64) }}
      onNavigate={vi.fn()}
      onDetailedNavigate={vi.fn()}
    />,
  );
  expect(await screen.findByRole("alert")).toHaveTextContent("Точный mapping");
  expect(screen.queryByText("C4 canvas")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Создать mapping" })).toBeDisabled();
});

it("uses explicit historical view pin, drills down by keyboard and saves against exact version", async () => {
  const user = userEvent.setup();
  const root = "10000000-0000-4000-8000-000000000004";
  const app = "10000000-0000-4000-8000-000000000005";
  const pin = { id: did, version: 1, contentHash: "a".repeat(64) };
  const element = (
    elementId: string,
    label: string,
    role: "software_system" | "application",
    parentId?: string,
  ) => ({
    id: elementId,
    label,
    role,
    ...(parentId ? { parentId } : {}),
    responsibility: "Orders",
    technology: "Go",
    origin: { kind: "authored" as const, reason: "Reviewed boundary" },
    refs: [],
  });
  const nodes = [
    element(root, "Orders", "software_system"),
    element(app, "Orders API", "application", root),
  ];
  const diagram = {
    pin,
    projectId: id,
    targetHash: "b".repeat(64),
    document: {
      format: "backend-diagram-v1",
      kind: "architecture",
      target: { revisionId: rid },
      payload: { elements: nodes, links: [], primarySystemId: root },
    },
    author: "Reviewer",
    createdAt: "2026-10-05T00:00:00Z",
    gaps: [],
    provenance: { format: "backend-diagram-provenance-v1", action: "create", elements: [] },
    provenanceHash: "c".repeat(64),
  };
  const seen: Array<Record<string, unknown>> = [];
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    if (url.includes("/diagrams/query")) {
      const body = JSON.parse(String(init?.body));
      seen.push(body);
      return json(200, {
        projection: { policy: "architecture-v1", level: body.level, rootId: body.rootId },
        pin,
        targetHash: diagram.targetHash,
        total: body.section === "elements" ? 2 : 0,
        items:
          body.section === "elements"
            ? nodes.map((data) => ({ rowType: "architecture_element", data }))
            : [],
        gaps: [],
        truncated: false,
        nextCursor: "",
      });
    }
    if (url.includes(`/diagrams/${did}/versions/1`)) return json(200, diagram);
    if (url.includes(`/diagrams/${did}/save`)) {
      seen.push(JSON.parse(String(init?.body)));
      return json(200, diagram);
    }
    if (url.includes("/diagram-views") || url.includes("/saved-views"))
      return json(200, { items: [], nextCursor: "", catalogVersion: 1 });
    if (url.includes("/diagrams?"))
      return json(200, {
        items: [
          {
            id: did,
            kind: "architecture",
            pin: { ...pin, version: 9 },
            targetHash: diagram.targetHash,
          },
        ],
        nextCursor: "",
        catalogVersion: 9,
      });
    // Selecting an element mounts the scoped diagnostics (a6f1139), which list
    // revisions and analysis jobs; the project-shaped fallback below has no
    // `items` and crashed BackendAnalysisJobs, unmounting the whole screen.
    if (url.includes("/revisions?") || url.includes("/analyses?"))
      return json(200, { items: [], nextCursor: "" });
    return json(200, { id, currentRevisionId: rid, name: "Orders" });
  });
  const navigate = vi.fn();
  function Harness() {
    const [search, setSearch] = useState<BackendWorkspaceSearch>({
      diagramId: did,
      diagramVersion: 1,
      diagramHash: pin.contentHash,
      diagramLevel: "context",
      diagramRoot: root,
    });
    return (
      <BackendArchitecture
        projectId={id}
        search={search}
        onNavigate={(next) => {
          navigate(next);
          setSearch(next);
        }}
        onDetailedNavigate={vi.fn()}
      />
    );
  }
  renderWithProviders(<Harness />);
  expect(await screen.findByText("Mapping v1")).toBeVisible();
  await user.type(screen.getByLabelText("Поиск по всей проекции"), "Orders");
  await user.selectOptions(screen.getByLabelText("Элемент для расположения"), root);
  await user.clear(screen.getByLabelText("Координата X"));
  await user.type(screen.getByLabelText("Координата X"), "75");
  await user.click(screen.getByRole("button", { name: "Применить координаты" }));
  await user.click(screen.getByRole("button", { name: "Orders · software_system · Замысел" }));
  expect(screen.getByLabelText("Поиск по всей проекции")).toHaveValue("Orders");
  expect(await screen.findByLabelText("Позиции C4")).toHaveTextContent('"x":75');
  await user.click(screen.getByRole("button", { name: "Закрыть инспектор" }));
  expect(screen.getByLabelText("Поиск по всей проекции")).toHaveValue("Orders");
  expect(screen.getByLabelText("Позиции C4")).toHaveTextContent('"x":75');
  const drill = await screen.findByRole("button", { name: "Что внутри Orders API" });
  drill.focus();
  await user.keyboard("{Enter}");
  expect(navigate).toHaveBeenCalledWith(
    expect.objectContaining({ diagramVersion: 1, diagramLevel: "components", diagramRoot: app }),
  );
  await user.click(screen.getByRole("button", { name: "Редактировать mapping" }));
  expect(screen.getByRole("button", { name: "Создать mapping" })).toBeDisabled();
  await user.clear(screen.getByLabelText("Название системы"));
  await user.type(screen.getByLabelText("Название системы"), "Renamed");
  await user.click(screen.getByRole("button", { name: "Сохранить mapping" }));
  await screen.findByText("Сохранено");
  expect(
    seen.some((input) => input.expectedVersion === 1 && typeof input.idempotencyKey === "string"),
  ).toBe(true);
  expect(
    seen.filter((input) => input.pin).every((input) => (input.pin as typeof pin).version === 1),
  ).toBe(true);
});

it("keeps saved view names scoped to exact identity and version including Back", async () => {
  const viewA = "10000000-0000-4000-8000-000000000010",
    viewB = "10000000-0000-4000-8000-000000000011";
  const pin = { id: did, version: 1, contentHash: "a".repeat(64) };
  const state = {
    diagram: pin,
    level: "context",
    rootId: id,
    search: "",
    origin: "all",
    selection: null,
    positions: [],
    collapsedIds: [],
  };
  const diagram = {
    pin,
    projectId: id,
    targetHash: "b".repeat(64),
    author: "Reviewer",
    createdAt: "2026-10-05",
    gaps: [],
    provenance: { elements: [] },
    document: {
      format: "backend-diagram-v1",
      kind: "architecture",
      target: { revisionId: rid },
      payload: {
        primarySystemId: id,
        elements: [
          {
            id,
            label: "Orders",
            role: "software_system",
            origin: { kind: "authored", reason: "Explicit" },
            refs: [],
            responsibility: "",
            technology: "",
          },
        ],
        links: [],
      },
    },
  };
  let createdView: { id: string; version: number; name: string; state: typeof state } | undefined;
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    if (url.endsWith("/diagram-views") && init?.method === "POST") {
      const input = JSON.parse(String(init.body));
      createdView = {
        id: "10000000-0000-4000-8000-000000000099",
        version: 1,
        name: input.name,
        state: input.state,
      };
      return json(200, createdView);
    }
    if (url.includes("/diagram-views?"))
      return json(200, {
        items: createdView
          ? [{ id: createdView.id, version: 1, kind: "architecture", name: createdView.name }]
          : [],
        nextCursor: "",
        catalogVersion: createdView ? 2 : 1,
      });
    if (url.includes(`/diagram-views/${viewA}/versions/`))
      return json(200, {
        id: viewA,
        version: url.endsWith("/2") ? 2 : 1,
        name: url.endsWith("/2") ? "A renamed" : "A original",
        state,
      });
    if (url.includes(`/diagram-views/${viewB}/versions/`))
      return json(200, { id: viewB, version: 1, name: "B name", state });
    if (url.includes(`/diagrams/${did}/versions/1`)) return json(200, diagram);
    if (url.includes("/diagrams/query")) {
      const input = JSON.parse(String(init?.body));
      return json(200, {
        pin,
        projection: { policy: "architecture-v1", level: input.level, rootId: input.rootId },
        targetHash: diagram.targetHash,
        total: 0,
        items: [],
        gaps: [],
        truncated: false,
        nextCursor: "",
      });
    }
    if (url.endsWith("/" + id)) return json(200, { id, currentRevisionId: rid, name: "Orders" });
    return json(200, { items: [], nextCursor: "", catalogVersion: 1 });
  });
  const user = userEvent.setup();
  function Harness() {
    const [selected, setSelected] = useState({ id: viewA, version: 1 });
    return (
      <>
        <button onClick={() => setSelected({ id: viewB, version: 1 })}>View B</button>
        <button onClick={() => setSelected({ id: viewA, version: 2 })}>View A2</button>
        <button onClick={() => setSelected({ id: viewA, version: 1 })}>Back A1</button>
        <BackendArchitecture
          projectId={id}
          search={{ diagramViewId: selected.id, diagramViewVersion: selected.version }}
          onNavigate={vi.fn()}
          onDetailedNavigate={vi.fn()}
        />
      </>
    );
  }
  const client = makeQueryClient();
  client.setDefaultOptions({ queries: { retry: false, gcTime: Infinity, staleTime: Infinity } });
  for (const v of [
    { id: viewA, version: 1, name: "A original", state },
    { id: viewB, version: 1, name: "B name", state },
    { id: viewA, version: 2, name: "A renamed", state },
  ])
    client.setQueryData(["backend-diagram-view", id, v.id, v.version], v);
  renderWithProviders(<Harness />, { queryClient: client });
  await waitFor(() => expect(screen.getByLabelText("Название C4 вида")).toHaveValue("A original"));
  await user.click(screen.getByRole("button", { name: "View B" }));
  await waitFor(() => expect(screen.getByLabelText("Название C4 вида")).toHaveValue("B name"));
  await user.click(screen.getByRole("button", { name: "View A2" }));
  await waitFor(() => expect(screen.getByLabelText("Название C4 вида")).toHaveValue("A renamed"));
  await user.click(screen.getByRole("button", { name: "Back A1" }));
  await waitFor(() => expect(screen.getByLabelText("Название C4 вида")).toHaveValue("A original"));
  await user.clear(screen.getByLabelText("Название C4 вида"));
  await user.type(screen.getByLabelText("Название C4 вида"), "Newly saved view");
  await user.click(screen.getByRole("button", { name: "Сохранить новый C4 вид" }));
  expect(
    await screen.findByRole("option", { name: "C4 · Newly saved view · v1" }),
  ).toBeInTheDocument();
});

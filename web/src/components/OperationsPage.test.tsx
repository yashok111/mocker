import { afterEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { OperationsPage } from "./OperationsPage";
import { renderInRouter } from "@/test/render";
import { json, route } from "@/test/http";
import {
  mergedOperationViewFixture,
  opOverrideSummaryViewFixture,
  operationViewFixture,
  sessionListViewFixture,
  settingsFixture,
  workspaceFixture,
} from "@/test/fixtures";
import type { ScenarioDetailView } from "@/api/generated/schemas";

const WS = 7;
const WORKSPACE = `GET /api/workspaces/${WS}`;
const MERGED_OPS = `GET /api/workspaces/${WS}/operations`;
const SESSION = `GET /api/workspaces/${WS}/session`;
const SPEC_OPS_LIMIT = (specId: number) => `GET /api/specs/${specId}/operations?limit=500`;
const SCENARIO_ID = 42;
const SCENARIO = `GET /api/workspaces/${WS}/scenarios/${SCENARIO_ID}`;

// Local, not test/fixtures.ts: ScenarioDetailView is new in this slice and
// test/fixtures.ts is not in this run's file-ownership lane for this file
// (P2b context §F lists neither OperationsPage.test.tsx nor fixtures.ts for
// any owner — the gap the banner blocker itself calls out). ScenariosPage.test.tsx
// hit the same gap and built its own local fixture rather than touch a file
// nobody owns; this mirrors that choice instead of re-litigating it.
function scenarioDetailFixture(overrides: Partial<ScenarioDetailView> = {}): ScenarioDetailView {
  return {
    id: SCENARIO_ID,
    name: "staging",
    createdAt: 1_700_000_000,
    isActive: true,
    settings: settingsFixture(),
    basePath: "",
    spec: { hash: "abc123", name: "petstore", inline: null },
    overrides: [],
    editVersion: 1,
    ...overrides,
  };
}

// specOps for both PETS_OP and ORDERS_OP below (§3.3: MergedOperationView
// carries no tag/summary of its own — the tree join comes from here).
const PETS_OP = mergedOperationViewFixture({
  method: "GET",
  path: "/pets/{petId}",
  opKey: "GET%20%2Fpets%2F%7BpetId%7D",
});
const ORDERS_OP = mergedOperationViewFixture({
  method: "POST",
  path: "/orders",
  opKey: "POST%20%2Forders",
});
const PETS_SPEC_OP = operationViewFixture({
  method: "GET",
  path: "/pets/{petId}",
  tag: "pets",
  summary: "Get a pet",
  operationId: "getPet",
});
const ORDERS_SPEC_OP = operationViewFixture({
  id: 2,
  method: "POST",
  path: "/orders",
  tag: "orders",
  summary: "Create an order",
  operationId: "createOrder",
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("OperationsPage", () => {
  it("renders its outer marker and the session-controls marker before any query settles", async () => {
    // A request that never resolves keeps every combined query pending —
    // both markers must still be on screen (§3.9's marker contract: the
    // OUTERMOST element and SessionControls render OUTSIDE the four-state
    // switch, never only on success).
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    renderInRouter(<OperationsPage id={WS} />);

    expect(await screen.findByTestId("operations-page")).toBeInTheDocument();
    expect(await screen.findByTestId("session-controls")).toBeInTheDocument();
    // getAllBy, not getBy: OperationsPage's own pending state AND
    // SessionControls' own directive-list query (which also never resolves
    // here) both render the same "Загрузка…" text — the point of this
    // assertion is that at least one of them is on screen, not which.
    expect(screen.getAllByText("Загрузка…").length).toBeGreaterThan(0);
  });

  it("offers a retry and still shows session-controls when the workspace fails", async () => {
    route({
      [WORKSPACE]: () => json(500, { error: { code: "internal", message: "db is down" } }),
      [MERGED_OPS]: () => json(200, []),
      [SESSION]: () => json(200, sessionListViewFixture({ directives: [] })),
    });
    renderInRouter(<OperationsPage id={WS} />);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Ошибка на сервере. Попробуйте ещё раз");
    expect(screen.getByTestId("operations-retry")).toBeInTheDocument();
    // The marker contract again: an error in the primary query must not
    // take SessionControls down with it.
    expect(screen.getByTestId("session-controls")).toBeInTheDocument();
  });

  it("points at /specs instead of erroring when the workspace has no spec attached", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, specId: null })),
      [MERGED_OPS]: () => json(200, []),
      [SESSION]: () => json(200, sessionListViewFixture({ directives: [] })),
    });
    renderInRouter(<OperationsPage id={WS} />);

    const empty = await screen.findByTestId("operations-empty");
    expect(empty).toHaveTextContent("нет привязанной спеки");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Загрузите спеку" })).toHaveAttribute("href", "/specs");
  });

  it("groups operations by tag and narrows the tree on search", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, specId: 1 })),
      [MERGED_OPS]: () => json(200, [PETS_OP, ORDERS_OP]),
      [SPEC_OPS_LIMIT(1)]: () => json(200, [PETS_SPEC_OP, ORDERS_SPEC_OP]),
      [SESSION]: () => json(200, sessionListViewFixture({ directives: [] })),
    });
    renderInRouter(<OperationsPage id={WS} />);

    await screen.findByText("pets");
    expect(screen.getByText("orders")).toBeInTheDocument();

    await userEvent.type(screen.getByTestId("operations-search"), "orders");

    // A search that filtered nothing would leave both tags on screen —
    // this only goes red if the filtering logic actually ran.
    expect(screen.queryByText("pets")).not.toBeInTheDocument();
    expect(screen.getByText("orders")).toBeInTheDocument();
  });

  it("says spec operations were capped at the server's own limit", async () => {
    const many = Array.from({ length: 500 }, (_, i) =>
      operationViewFixture({ id: i, method: "GET", path: `/x/${i}`, tag: "x" }),
    );
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, specId: 1 })),
      [MERGED_OPS]: () => json(200, []),
      [SPEC_OPS_LIMIT(1)]: () => json(200, many),
      [SESSION]: () => json(200, sessionListViewFixture({ directives: [] })),
    });
    renderInRouter(<OperationsPage id={WS} />);

    expect(await screen.findByTestId("operations-capped-note")).toHaveTextContent("500");
  });

  it("does NOT claim a cap when fewer than the limit came back", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, specId: 1 })),
      [MERGED_OPS]: () => json(200, [PETS_OP]),
      [SPEC_OPS_LIMIT(1)]: () => json(200, [PETS_SPEC_OP]),
      [SESSION]: () => json(200, sessionListViewFixture({ directives: [] })),
    });
    renderInRouter(<OperationsPage id={WS} />);

    await screen.findByText("pets");
    expect(screen.queryByTestId("operations-capped-note")).not.toBeInTheDocument();
  });

  it("never renders screen 5's forbidden P2 vocabulary, even when an override carries recipes", async () => {
    // DESIGN.md:890-891 forbids "рецепт" (and the rest of the P2 vocabulary)
    // on this screen and gives the exact wording to use instead
    // ("сохранено: закреплённый 409, 3 значения на 200"). The login
    // operation right after the auth preset is applied is exactly the case
    // that regressed here: recipeCount > 0 on a pinned 200.
    const opWithRecipes = mergedOperationViewFixture({
      opKey: "POST%20%2Flogin",
      method: "POST",
      path: "/login",
      override: opOverrideSummaryViewFixture({
        overrideOn: true,
        responses: { "200": { mode: "pinned", recipeCount: 1, hasSchemaPatch: false } },
      }),
    });
    const loginSpecOp = operationViewFixture({
      id: 3,
      method: "POST",
      path: "/login",
      tag: "auth",
      summary: "Login",
      operationId: "login",
    });
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, specId: 1 })),
      [MERGED_OPS]: () => json(200, [opWithRecipes]),
      [SPEC_OPS_LIMIT(1)]: () => json(200, [loginSpecOp]),
      [SESSION]: () => json(200, sessionListViewFixture({ directives: [] })),
    });
    renderInRouter(<OperationsPage id={WS} />);

    // Wait for the tree itself to render before inspecting its text — the
    // outer marker is present from the first paint (§ marker contract), long
    // before the queries this assertion cares about have settled.
    await screen.findByText("auth");
    const row = screen.getByTestId("operations-page");
    // The single value this line was supposed to report ("1 значения")
    // must show up, and none of the P2 words this screen forbids may.
    expect(row).toHaveTextContent("значения");
    expect(row.textContent).not.toMatch(/рецепт|рец\.|JSON Patch|матчер/i);
  });

  it("selecting an operation renders the operation-editor marker and re-targets session-controls", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, specId: 1 })),
      [MERGED_OPS]: () => json(200, [PETS_OP, ORDERS_OP]),
      [SPEC_OPS_LIMIT(1)]: () => json(200, [PETS_SPEC_OP, ORDERS_SPEC_OP]),
      [SESSION]: () => json(200, sessionListViewFixture({ directives: [] })),
      // OperationEditor's own GET — 404 is the normal "nothing overridden
      // yet" answer (§3.3), not an error.
      [`GET /api/workspaces/${WS}/operations/${PETS_OP.opKey}`]: () =>
        json(404, { error: { code: "not_found", message: "no override for this operation" } }),
    });
    renderInRouter(<OperationsPage id={WS} />);

    await screen.findByText("pets");
    // Before selection, the session strip targets the whole workspace.
    expect(screen.getByTestId("session-controls")).toHaveTextContent("весь воркспейс");
    expect(screen.queryByTestId("operation-editor")).not.toBeInTheDocument();

    const rows = await screen.findAllByTestId("operation-row");
    const petRow = rows.find((r) => r.textContent?.includes("/pets/{petId}"));
    if (!petRow) throw new Error("pet row not found");
    await userEvent.click(petRow);

    // The one assertion nothing else in this phase's test suite makes: that
    // picking an operation actually mounts OperationEditor, not just that
    // the three override routes exist somewhere in the API surface.
    expect(await screen.findByTestId("operation-editor")).toBeInTheDocument();
    expect(screen.getByTestId("session-controls")).toHaveTextContent("GET /pets/{petId}");
  });

  // A18's banner (ScenarioMaskBanner) had zero coverage: every existing test
  // above relies on workspaceFixture's default scenarioId: null, so the whole
  // render path — the useGetScenario query, the masked-key list, the
  // loading/error no-op — was exercised by nothing. These four cover exactly
  // the review vector this component was flagged on.
  it("A18: names the masked operations when the active scenario overrides at least one", async () => {
    route({
      [WORKSPACE]: () =>
        json(200, workspaceFixture({ id: WS, specId: null, scenarioId: SCENARIO_ID })),
      [MERGED_OPS]: () => json(200, []),
      [SESSION]: () => json(200, sessionListViewFixture({ directives: [] })),
      [SCENARIO]: () =>
        json(
          200,
          scenarioDetailFixture({
            overrides: [
              {
                method: "GET",
                path: "/pets/{petId}",
                overrideOn: true,
                routeOff: false,
                responses: {},
              },
            ],
          }),
        ),
    });
    renderInRouter(<OperationsPage id={WS} />);

    expect(await screen.findByTestId("scenario-active-banner")).toBeInTheDocument();
    expect(screen.getByTestId("scenario-masked-keys")).toHaveTextContent("GET /pets/{petId}");
  });

  it("A18: falls back to the settings-only note when the active scenario overrides no operation", async () => {
    route({
      [WORKSPACE]: () =>
        json(200, workspaceFixture({ id: WS, specId: null, scenarioId: SCENARIO_ID })),
      [MERGED_OPS]: () => json(200, []),
      [SESSION]: () => json(200, sessionListViewFixture({ directives: [] })),
      [SCENARIO]: () => json(200, scenarioDetailFixture({ overrides: [] })),
    });
    renderInRouter(<OperationsPage id={WS} />);

    expect(await screen.findByTestId("scenario-active-banner")).toBeInTheDocument();
    expect(screen.queryByTestId("scenario-masked-keys")).not.toBeInTheDocument();
    expect(screen.getByText(/маскирует только настройки воркспейса/)).toBeInTheDocument();
  });

  it("A18: renders no banner at all when the workspace has no active scenario", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, specId: null, scenarioId: null })),
      [MERGED_OPS]: () => json(200, []),
      [SESSION]: () => json(200, sessionListViewFixture({ directives: [] })),
    });
    renderInRouter(<OperationsPage id={WS} />);

    await screen.findByTestId("operations-empty");
    expect(screen.queryByTestId("scenario-active-banner")).not.toBeInTheDocument();
  });

  it("A18: the banner's own fetch never blocks the rest of the page — pending forever stays silent", async () => {
    // Not route(): that helper answers every stub synchronously, and the
    // whole point here is a scenario detail request that NEVER settles while
    // the page's other three queries do — proof the banner is advisory-only
    // (ScenarioMaskBanner returns null on anything but a 200, per its own
    // comment) rather than gating isPending/isError up in OperationsPage.
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const key = `${(init?.method ?? "GET").toUpperCase()} ${String(input)}`;
        if (key === SCENARIO) {
          return new Promise<Response>(() => {});
        }
        const handlers: Record<string, () => Response> = {
          [WORKSPACE]: () =>
            json(200, workspaceFixture({ id: WS, specId: null, scenarioId: SCENARIO_ID })),
          [MERGED_OPS]: () => json(200, []),
          [SESSION]: () => json(200, sessionListViewFixture({ directives: [] })),
        };
        return Promise.resolve(
          handlers[key]?.() ??
            json(500, { error: { code: "internal", message: `unrouted ${key}` } }),
        );
      }),
    );
    renderInRouter(<OperationsPage id={WS} />);

    // The rest of the page still resolves to its normal no-spec state...
    await screen.findByTestId("operations-empty");
    // ...and the banner never appears, since scenario.data never becomes 200 —
    // not "eventually", just never, for as long as this test runs.
    expect(screen.queryByTestId("scenario-active-banner")).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});

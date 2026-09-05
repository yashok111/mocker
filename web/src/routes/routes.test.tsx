import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryHistory, createRouter } from "@tanstack/react-router";
import { routeTree } from "@/routeTree.gen";
import { theme } from "@/theme/mantine";
import {
  authResponseFixture,
  endpointListViewFixture,
  endpointViewFixture,
  mergedOperationViewFixture,
  opOverrideSummaryViewFixture,
  workspaceFixture,
} from "@/test/fixtures";
import type { CheckpointSummaryView } from "@/api/generated/schemas";

// This file mounts the REAL route tree, guard and all. The component tests
// beside it deliberately mount one screen at a time; nothing there would
// notice a guard that redirects the wrong way, a path that stopped matching,
// or a route parameter that arrives as the wrong type — all of which are
// silent in a unit test and total in a browser.

type FetchArgs = [input: RequestInfo | URL, init?: RequestInit];

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function route(handlers: Record<string, () => Response>) {
  const fn = vi.fn<(...args: FetchArgs) => Promise<Response>>((input, init) => {
    const key = `${(init?.method ?? "GET").toUpperCase()} ${String(input)}`;
    const handler = handlers[key];
    if (!handler) {
      return Promise.resolve(
        json(404, { error: { code: "not_found", message: `unrouted ${key}` } }),
      );
    }
    return Promise.resolve(handler());
  });
  vi.stubGlobal("fetch", fn);
  return fn;
}

const anonymous = () => json(401, { error: { code: "unauthorized", message: "no session" } });

function mount(path: string) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  const router = createRouter({
    routeTree,
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  render(
    <QueryClientProvider client={queryClient}>
      <MantineProvider theme={theme} defaultColorScheme="light">
        <ModalsProvider>
          <RouterProvider router={router as never} />
        </ModalsProvider>
      </MantineProvider>
    </QueryClientProvider>,
  );
  return router;
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("route tree", () => {
  it("sends an anonymous visitor from / to the login screen", async () => {
    route({ "GET /api/me": anonymous });

    const router = mount("/");

    expect(await screen.findByTestId("login-form")).toBeInTheDocument();
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
  });

  it("remembers where an anonymous visitor was headed", async () => {
    route({ "GET /api/me": anonymous });

    const router = mount("/workspaces/7");

    await screen.findByTestId("login-form");
    // Without this the person lands on the workspace list after logging in and
    // has to find their way back to the page they actually asked for.
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ redirect: "/workspaces/7" }),
    );
  });

  it("sends a logged-in visitor away from /login", async () => {
    route({
      "GET /api/me": () => json(200, authResponseFixture()),
      "GET /api/workspaces": () => json(200, []),
    });

    const router = mount("/login");

    await waitFor(() => expect(router.state.location.pathname).toBe("/"));
    expect(await screen.findByTestId("workspaces-empty")).toBeInTheDocument();
  });

  it("renders the workspace list inside the shell for a logged-in visitor", async () => {
    route({
      "GET /api/me": () => json(200, authResponseFixture()),
      "GET /api/workspaces": () => json(200, []),
    });

    mount("/");

    // The shell is what carries the user's name and the way out; a screen
    // rendered outside it would look fine in a unit test and strand the user.
    expect(await screen.findByTestId("current-user-name")).toHaveTextContent("alex");
    expect(screen.getByTestId("logout-button")).toBeInTheDocument();
  });

  it("passes {id} to the workspace layout as a NUMBER, not the raw string", async () => {
    const fetchMock = route({
      "GET /api/me": () => json(200, authResponseFixture()),
      "GET /api/workspaces/7": () => json(200, workspaceFixture({ id: 7, name: "Alex" })),
      "GET /api/workspaces/7/traffic": () => json(200, { rows: [], rate1m: 0, dropped: 0 }),
    });

    mount("/workspaces/7");

    // The route declares a param parser; if the router ever stops honouring it,
    // the id arrives as "7" and the screen's own integer check turns EVERY
    // workspace into a 404 — invisible to any test that mounts the screen
    // directly with a number already in hand.
    expect(await screen.findByTestId("workspace-detail-name")).toHaveTextContent("Alex");
    expect(fetchMock.mock.calls.map(([url]) => String(url))).toContain("/api/workspaces/7");
  });

  it("answers a non-numeric workspace id with not-found instead of calling the API", async () => {
    const fetchMock = route({ "GET /api/me": () => json(200, authResponseFixture()) });

    mount("/workspaces/not-a-number");

    expect(await screen.findByText("Такой страницы нет")).toBeInTheDocument();
    const workspaceCalls = fetchMock.mock.calls.filter(([url]) =>
      String(url).startsWith("/api/workspaces/"),
    );
    expect(workspaceCalls).toHaveLength(0);
  });

  it("shows the not-found screen for a path no route claims", async () => {
    route({ "GET /api/me": () => json(200, authResponseFixture()) });

    mount("/nothing/here");

    // internal/webui answers 200 with this very app for any unknown GET, so
    // this component is the only thing that ever says the path was wrong.
    expect(await screen.findByText("Такой страницы нет")).toBeInTheDocument();
  });

  // --- New in this phase: every route DESIGN §14 names, reachable. ---------
  //
  // Each assertion below finds the screen's own root data-testid (from the
  // component signature block, not a heading or a piece of content the
  // phase-2 producers will legitimately rewrite) — that is what makes this
  // file survive a screen's content changing without becoming a maintenance
  // burden for whoever writes that content.

  it("mounts /specs inside the shell", async () => {
    route({
      "GET /api/me": () => json(200, authResponseFixture()),
    });

    mount("/specs");

    expect(await screen.findByTestId("specs-page")).toBeInTheDocument();
  });

  it("mounts /guide inside the shell, with no request past the session", async () => {
    const fetchMock = route({
      "GET /api/me": () => json(200, authResponseFixture()),
    });

    mount("/guide");

    expect(await screen.findByTestId("guide-page")).toBeInTheDocument();
    // The manual is compiled into the bundle: the only call the mount makes
    // is the guard's own /api/me — plus, since A20, the SHELL's two probes
    // (/readyz and /healthz, the header's server status), which every
    // authenticated screen shares and which are not this screen's. A call
    // outside those three would mean the screen grew an API dependency the
    // contract does not know about.
    const shell = ["/api/me", "/readyz", "/healthz"];
    const calls = fetchMock.mock.calls.map(([input]) => String(input));
    expect(calls.filter((url) => !shell.some((s) => url.endsWith(s)))).toEqual([]);
  });

  it("mounts /workspaces/$id with the overview, the auth preset panel and settings", async () => {
    route({
      "GET /api/me": () => json(200, authResponseFixture()),
      "GET /api/workspaces/7": () => json(200, workspaceFixture({ id: 7, name: "Alex" })),
      "GET /api/workspaces/7/traffic": () => json(200, { rows: [], rate1m: 0, dropped: 0 }),
    });

    mount("/workspaces/7");

    expect(await screen.findByTestId("overview-page")).toBeInTheDocument();
    // A sub-component that stops being rendered is otherwise invisible to
    // both halves of the phase criterion — the coverage scan counts its hook
    // calls wherever the file sits, not whether anything mounts it.
    expect(screen.getByTestId("auth-preset-panel")).toBeInTheDocument();
    expect(screen.getByTestId("settings-panel")).toBeInTheDocument();
  });

  it("mounts /workspaces/$id/operations with the session controls", async () => {
    route({
      "GET /api/me": () => json(200, authResponseFixture()),
      "GET /api/workspaces/7": () => json(200, workspaceFixture({ id: 7, name: "Alex" })),
    });

    mount("/workspaces/7/operations");

    expect(await screen.findByTestId("operations-page")).toBeInTheDocument();
    expect(screen.getByTestId("session-controls")).toBeInTheDocument();
    // The active tab is derived from the real, nested pathname here — not
    // the always-"/" memory route WorkspaceLayout.test.tsx mounts against.
    expect(screen.getByRole("tab", { name: "Endpoint'ы" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });

  it("mounts /workspaces/$id/endpoints", async () => {
    route({
      "GET /api/me": () => json(200, authResponseFixture()),
      "GET /api/workspaces/7": () => json(200, workspaceFixture({ id: 7, name: "Alex" })),
    });

    mount("/workspaces/7/endpoints");

    expect(await screen.findByTestId("custom-endpoints-page")).toBeInTheDocument();
  });

  it("mounts /workspaces/$id/traffic", async () => {
    route({
      "GET /api/me": () => json(200, authResponseFixture()),
      "GET /api/workspaces/7": () => json(200, workspaceFixture({ id: 7, name: "Alex" })),
    });

    mount("/workspaces/7/traffic");

    expect(await screen.findByTestId("traffic-page")).toBeInTheDocument();
  });

  it("mounts /workspaces/$id/scenarios", async () => {
    route({
      "GET /api/me": () => json(200, authResponseFixture()),
      "GET /api/workspaces/7": () => json(200, workspaceFixture({ id: 7, name: "Alex" })),
      "GET /api/workspaces/7/scenarios": () => json(200, { scenarios: [] }),
    });

    mount("/workspaces/7/scenarios");

    expect(await screen.findByTestId("scenarios-page")).toBeInTheDocument();
  });

  it("mounts /workspaces/$id/history", async () => {
    route({
      "GET /api/me": () => json(200, authResponseFixture()),
      "GET /api/workspaces/7": () => json(200, workspaceFixture({ id: 7, name: "Alex" })),
      "GET /api/workspaces/7/checkpoints": () => json(200, { checkpoints: [] }),
    });

    mount("/workspaces/7/history");

    expect(await screen.findByTestId("history-page")).toBeInTheDocument();
  });

  it("mounts /workspaces/$id/connections (P6e)", async () => {
    route({
      "GET /api/me": () => json(200, authResponseFixture()),
      "GET /api/workspaces/7": () => json(200, workspaceFixture({ id: 7, name: "Alex" })),
      "GET /api/workspaces/7/connections": () => json(200, { open: 0, cap: 200, connections: [] }),
    });

    mount("/workspaces/7/connections");

    expect(await screen.findByTestId("connections-page")).toBeInTheDocument();
  });

  it("mounts /workspaces/$id/assets (A10)", async () => {
    route({
      "GET /api/me": () => json(200, authResponseFixture()),
      "GET /api/workspaces/7": () => json(200, workspaceFixture({ id: 7, name: "Alex" })),
      "GET /api/workspaces/7/assets": () =>
        json(200, { assets: [], totalBytes: 0, maxAssetBytes: 1, maxTotalBytes: 1 }),
    });

    mount("/workspaces/7/assets");

    expect(await screen.findByTestId("assets-page")).toBeInTheDocument();
  });

  it("mounts /workspaces/$id/contract (P7b) and its links open the editors (B6)", async () => {
    route({
      "GET /api/me": () => json(200, authResponseFixture()),
      "GET /api/workspaces/7": () => json(200, workspaceFixture({ id: 7, name: "Alex" })),
      "GET /api/workspaces/7/openapi.json": () =>
        json(200, {
          openapi: "3.1.0",
          info: { title: "Alex", version: "0.0.0-draft.2" },
          paths: {
            "/things": {
              get: { operationId: "listThings", responses: { "200": { description: "OK" } } },
            },
            "/users": {
              get: { operationId: "listUsers", responses: { "200": { description: "OK" } } },
            },
          },
        }),
      "GET /api/workspaces/7/operations": () =>
        json(200, [
          mergedOperationViewFixture({
            method: "GET",
            path: "/users",
            opKey: "GET%20%2Fusers",
            override: opOverrideSummaryViewFixture({ overrideOn: true, routeOff: true }),
          }),
        ]),
      "GET /api/workspaces/7/endpoints": () =>
        json(
          200,
          endpointListViewFixture({
            endpoints: [endpointViewFixture({ id: 12, method: "GET", path: "/things" })],
          }),
        ),
      "GET /api/workspaces/7/traffic": () => json(200, { rows: [], rate1m: 0 }),
      "GET /api/workspaces/7/session": () => json(200, { directives: [] }),
    });

    const router = mount("/workspaces/7/contract");
    expect(await screen.findByTestId("contract-page")).toBeInTheDocument();
    const links = await screen.findAllByTestId("contract-op-link");
    expect(links).toHaveLength(2);

    // The «добавлено» row (the custom /things) opens «Кастомные» with its id.
    const ops = screen.getAllByTestId("contract-op");
    const added = ops.find((el) => el.getAttribute("data-badge") === "added");
    if (added === undefined) {
      throw new Error("no added row");
    }
    await userEvent.click(within(added).getByTestId("contract-op-link"));
    await waitFor(() => expect(router.state.location.pathname).toBe("/workspaces/7/endpoints"));
    expect(router.state.location.search).toEqual({ endpointId: "12" });
  });

  it("the «удалено» spec operation on /contract opens Endpoint'ы with its opKey (B6)", async () => {
    route({
      "GET /api/me": () => json(200, authResponseFixture()),
      "GET /api/workspaces/7": () => json(200, workspaceFixture({ id: 7, name: "Alex" })),
      "GET /api/workspaces/7/openapi.json": () =>
        json(200, {
          openapi: "3.1.0",
          info: { title: "Alex", version: "0.0.0-draft.2" },
          paths: {
            "/users": {
              get: { operationId: "listUsers", responses: { "200": { description: "OK" } } },
            },
          },
        }),
      "GET /api/workspaces/7/operations": () =>
        json(200, [
          mergedOperationViewFixture({
            method: "GET",
            path: "/users",
            opKey: "GET%20%2Fusers",
            override: opOverrideSummaryViewFixture({ overrideOn: true, routeOff: true }),
          }),
        ]),
      "GET /api/workspaces/7/endpoints": () =>
        json(200, endpointListViewFixture({ endpoints: [] })),
      "GET /api/specs/1/operations?limit=500": () => json(200, []),
      "GET /api/workspaces/7/session": () => json(200, { directives: [] }),
    });

    const router = mount("/workspaces/7/contract");
    const link = await screen.findByTestId("contract-op-link");
    await userEvent.click(link);
    await waitFor(() => expect(router.state.location.pathname).toBe("/workspaces/7/operations"));
    expect(router.state.location.search).toEqual({ opKey: "GET%20%2Fusers" });
  });

  it("mounts /workspaces/$id/resources", async () => {
    route({
      "GET /api/me": () => json(200, authResponseFixture()),
      "GET /api/workspaces/7": () =>
        json(200, workspaceFixture({ id: 7, name: "Alex", specId: null })),
      "GET /api/workspaces/7/resources": () => json(200, { families: [] }),
    });

    mount("/workspaces/7/resources");

    expect(await screen.findByTestId("resources-page")).toBeInTheDocument();
  });

  it("navigates from the workspace layout to all seven children via its own tabs", async () => {
    // Five tabs existed before this slice (P2b added Сценарии, the fifth);
    // P2c added the sixth (История), and P3a adds the seventh (Ресурсы).
    // Clicking only four of them, as this test once did, is exactly how
    // WorkspaceLayout.test.tsx:82 and this test's own former title ("all
    // four children") both went stale without a single bar turning red — see
    // coverage.test.ts's own note on the same defect shape (obs 17 of the
    // P2c context). getAllByRole("tab").length below is what actually
    // catches an eighth tab landing unclicked the same way; the individual
    // clicks after it only prove each of the seven actually navigates.
    route({
      "GET /api/me": () => json(200, authResponseFixture()),
      "GET /api/workspaces/7": () =>
        json(200, workspaceFixture({ id: 7, name: "Alex", specId: null })),
      "GET /api/workspaces/7/traffic": () => json(200, { rows: [], rate1m: 0, dropped: 0 }),
      "GET /api/workspaces/7/scenarios": () => json(200, { scenarios: [] }),
      "GET /api/workspaces/7/checkpoints": () => json(200, { checkpoints: [] }),
      "GET /api/workspaces/7/resources": () => json(200, { families: [] }),
    });

    const routerInstance = mount("/workspaces/7");
    await screen.findByTestId("overview-page");
    expect(screen.getAllByRole("tab")).toHaveLength(10);

    await userEvent.click(screen.getByRole("tab", { name: "Трафик" }));
    expect(await screen.findByTestId("traffic-page")).toBeInTheDocument();
    await waitFor(() =>
      expect(routerInstance.state.location.pathname).toBe("/workspaces/7/traffic"),
    );

    await userEvent.click(screen.getByRole("tab", { name: "Кастомные" }));
    expect(await screen.findByTestId("custom-endpoints-page")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("tab", { name: "Endpoint'ы" }));
    expect(await screen.findByTestId("operations-page")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("tab", { name: "Сценарии" }));
    expect(await screen.findByTestId("scenarios-page")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("tab", { name: "История" }));
    expect(await screen.findByTestId("history-page")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("tab", { name: "Ресурсы" }));
    expect(await screen.findByTestId("resources-page")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("tab", { name: "Обзор" }));
    expect(await screen.findByTestId("overview-page")).toBeInTheDocument();
  });

  // --- P2c: obs 17 — the UI calls all four checkpoint routes and shows all
  // three warnings (§G of the P2c context). Mounted through the REAL route
  // tree, not HistoryPage.test.tsx's direct render: a screen that mounts and
  // calls nothing (or that never reaches the real /history route at all)
  // passes both the static coverage scan and a standalone component test —
  // this is the one place that catches that gap.
  describe("/workspaces/$id/history", () => {
    const WS = 7;
    const LIST = `GET /api/workspaces/${WS}/checkpoints`;
    const CREATE = `POST /api/workspaces/${WS}/checkpoints`;

    function checkpointFixture(
      overrides: Partial<CheckpointSummaryView> = {},
    ): CheckpointSummaryView {
      return {
        id: 1,
        kind: "manual",
        label: "перед деплоем",
        createdAt: 1_700_000_000,
        createdBy: 1,
        hasData: true,
        ...overrides,
      };
    }

    it("fetches the list on mount, then creates, rolls back and resets on request", async () => {
      const fetchMock = route({
        "GET /api/me": () => json(200, authResponseFixture()),
        "GET /api/workspaces/7": () => json(200, workspaceFixture({ id: 7, name: "Alex" })),
        [LIST]: () => json(200, { checkpoints: [checkpointFixture()] }),
        [CREATE]: () => json(201, checkpointFixture({ id: 2, label: "новая точка" })),
        "POST /api/workspaces/7/rollback/1": () =>
          json(200, { revision: 5, scenarioActive: false }),
        "POST /api/workspaces/7/reset-overrides": () =>
          json(200, { revision: 6, scenarioActive: false, changed: true }),
      });

      mount("/workspaces/7/history");
      expect(await screen.findByTestId("history-page")).toBeInTheDocument();
      await screen.findByTestId("checkpoint-row");

      // The initial GET fired on mount, unconditionally.
      expect(
        fetchMock.mock.calls.some(([url]) => String(url) === "/api/workspaces/7/checkpoints"),
      ).toBe(true);

      // POST /checkpoints — the create form, no confirmation needed.
      await userEvent.type(screen.getByTestId("checkpoint-create-label"), "новая точка");
      await userEvent.click(screen.getByTestId("checkpoint-create-submit"));
      expect(await screen.findByTestId("checkpoint-created")).toHaveTextContent("новая точка");

      // POST /rollback/{cid} — behind its own confirm modal. The LIST stub
      // always answers with the same single row (id 1), including on the
      // refetch the checkpoint-create above triggers, so there is exactly
      // one rollback button here.
      await userEvent.click(screen.getByTestId("checkpoint-rollback"));
      const rollbackDialog = await screen.findByRole("dialog");
      await userEvent.click(within(rollbackDialog).getByTestId("checkpoint-rollback-confirm"));
      await waitFor(() =>
        expect(
          fetchMock.mock.calls.some(
            ([url, init]) =>
              String(url) === "/api/workspaces/7/rollback/1" && init?.method === "POST",
          ),
        ).toBe(true),
      );

      // POST /reset-overrides — behind its own confirm modal.
      await userEvent.click(screen.getByTestId("reset-overrides-button"));
      const resetDialog = await screen.findByRole("dialog");
      await userEvent.click(within(resetDialog).getByTestId("reset-confirm-submit"));
      await waitFor(() =>
        expect(
          fetchMock.mock.calls.some(
            ([url, init]) =>
              String(url) === "/api/workspaces/7/reset-overrides" && init?.method === "POST",
          ),
        ).toBe(true),
      );
    });

    it("names custom endpoints and the auth preset before the reset request (C9)", async () => {
      route({
        "GET /api/me": () => json(200, authResponseFixture()),
        "GET /api/workspaces/7": () => json(200, workspaceFixture({ id: 7, name: "Alex" })),
        [LIST]: () => json(200, { checkpoints: [] }),
      });

      mount("/workspaces/7/history");
      await userEvent.click(await screen.findByTestId("reset-overrides-button"));

      const dialog = await screen.findByRole("dialog");
      expect(dialog).toHaveTextContent("кастомные endpoint");
      expect(dialog).toHaveTextContent("пресет авторизации");
      expect(dialog).toHaveTextContent("перестанет логиниться");
      // Named BEFORE the request: cancelling must not have fired it.
      await userEvent.click(within(dialog).getByText("Отмена"));
    });

    it("names basePath and the signing key before the rollback request (C10)", async () => {
      route({
        "GET /api/me": () => json(200, authResponseFixture()),
        "GET /api/workspaces/7": () => json(200, workspaceFixture({ id: 7, name: "Alex" })),
        [LIST]: () => json(200, { checkpoints: [checkpointFixture()] }),
      });

      mount("/workspaces/7/history");
      await userEvent.click(await screen.findByTestId("checkpoint-rollback"));

      const dialog = await screen.findByRole("dialog");
      expect(dialog).toHaveTextContent("basePath");
      expect(dialog).toHaveTextContent("signingKey");
      await userEvent.click(within(dialog).getByText("Отмена"));
    });

    it("with a scenario active, warns before rollback that part of the layer is masked, and before checkpoint-create that the snapshot is workspace-only (C8)", async () => {
      route({
        "GET /api/me": () => json(200, authResponseFixture()),
        "GET /api/workspaces/7": () =>
          json(200, workspaceFixture({ id: 7, name: "Alex", scenarioId: 3 })),
        [LIST]: () => json(200, { checkpoints: [checkpointFixture()] }),
      });

      mount("/workspaces/7/history");

      // Visible the moment the screen has a workspace to read scenarioId
      // off — before any click, i.e. before the checkpoint-create request.
      expect(await screen.findByTestId("checkpoint-create-scenario-warning")).toBeInTheDocument();

      await userEvent.click(await screen.findByTestId("checkpoint-rollback"));
      const rollbackDialog = await screen.findByRole("dialog");
      expect(within(rollbackDialog).getByTestId("rollback-scenario-warning")).toHaveTextContent(
        "замаскирована",
      );
      await userEvent.click(within(rollbackDialog).getByText("Отмена"));

      // The reset half of C8's flag (obs 13's UI counterpart): reset also
      // warns while a scenario is active.
      await userEvent.click(screen.getByTestId("reset-overrides-button"));
      const resetDialog = await screen.findByRole("dialog");
      expect(within(resetDialog).getByTestId("reset-scenario-warning")).toHaveTextContent(
        "замаскирована",
      );
      await userEvent.click(within(resetDialog).getByText("Отмена"));
    });
  });

  it("links to /specs from the app shell", async () => {
    route({
      "GET /api/me": () => json(200, authResponseFixture()),
      "GET /api/workspaces": () => json(200, []),
    });

    mount("/");

    await screen.findByTestId("workspaces-empty");
    await userEvent.click(screen.getByTestId("nav-specs"));
    expect(await screen.findByTestId("specs-page")).toBeInTheDocument();
  });
});

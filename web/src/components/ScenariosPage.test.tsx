import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ScenariosPage } from "./ScenariosPage";
import { makeQueryClient, renderInRouter } from "@/test/render";
import { json, route } from "@/test/http";
import { getListScenariosQueryKey } from "@/api/generated/scenarios/scenarios.ts";
import { getGetWorkspaceQueryKey } from "@/api/generated/workspaces/workspaces.ts";
import type { ScenarioDetailView, ScenarioSummaryView } from "@/api/generated/schemas";

// No shared fixture exists for the scenario wire types (they are new in this
// slice, and test/fixtures.ts belongs to a different file-ownership lane —
// see the P2b context's §F) — these two tiny builders are local to this file
// on purpose, the same way a screen with a brand-new wire shape has always
// started here before a shared fixture was worth extracting.
function scenarioSummaryFixture(overrides: Partial<ScenarioSummaryView> = {}): ScenarioSummaryView {
  return {
    id: 1,
    name: "baseline",
    createdAt: 1_700_000_000,
    isActive: false,
    editVersion: 1,
    ...overrides,
  };
}

function scenarioDetailFixture(overrides: Partial<ScenarioDetailView> = {}): ScenarioDetailView {
  return {
    id: 1,
    name: "baseline",
    createdAt: 1_700_000_000,
    isActive: false,
    settings: {
      seed: 1,
      basePath: "",
      listSize: 3,
      nullRate: 0,
      envelope: null,
      identity: { id: 1, name: "Alex", email: "alex@example.com", roles: ["user"] },
      auth: { jwtTtlSec: 3600, alg: "HS256", signingKey: "", requireHeader: false },
      cors: { mode: "reflect", credentials: true },
      validateRequests: false,
      delayMs: 0,
    },
    basePath: "",
    spec: { hash: "abc123", name: "petstore", inline: null },
    overrides: [],
    editVersion: 1,
    ...overrides,
  };
}

const WS = 7;
const LIST = `GET /api/workspaces/${WS}/scenarios`;
const CREATE = `POST /api/workspaces/${WS}/scenarios`;
const DEACTIVATE = `POST /api/workspaces/${WS}/scenarios/deactivate`;

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("ScenariosPage", () => {
  it("renders its outer marker and says it is loading before the list answers", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    renderInRouter(<ScenariosPage id={WS} />);

    expect(await screen.findByTestId("scenarios-page")).toBeInTheDocument();
    expect(screen.getByText("Загрузка…")).toBeInTheDocument();
  });

  it("offers a retry when the list fails, in Russian, without dropping the outer marker", async () => {
    let calls = 0;
    route({
      [LIST]: () => {
        calls += 1;
        return calls === 1
          ? json(500, { error: { code: "internal", message: "db is down" } })
          : json(200, { scenarios: [] });
      },
    });
    renderInRouter(<ScenariosPage id={WS} />);

    // The outer marker survives the error branch — it sits outside the
    // four-state switch, not inside the success branch alone.
    expect(await screen.findByTestId("scenarios-page")).toBeInTheDocument();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Ошибка на сервере. Попробуйте ещё раз");
    expect(alert).not.toHaveTextContent("db is down");

    await userEvent.click(screen.getByTestId("scenarios-retry"));
    expect(await screen.findByTestId("scenarios-empty")).toBeInTheDocument();
  });

  it("explains that there is nothing to switch to when the list is empty", async () => {
    route({ [LIST]: () => json(200, { scenarios: [] }) });
    renderInRouter(<ScenariosPage id={WS} />);

    expect(await screen.findByTestId("scenarios-empty")).toHaveTextContent("Сценариев пока нет");
  });

  it("lists name, creation date and marks the active one", async () => {
    route({
      [LIST]: () =>
        json(200, {
          scenarios: [
            scenarioSummaryFixture({ id: 1, name: "baseline", isActive: false }),
            scenarioSummaryFixture({ id: 2, name: "black-friday", isActive: true }),
          ],
        }),
    });
    renderInRouter(<ScenariosPage id={WS} />);

    const rows = await screen.findAllByTestId("scenario-row");
    expect(rows).toHaveLength(2);
    const activeRow = rows.find((r) => r.textContent?.includes("black-friday"));
    if (!activeRow) throw new Error("active row not found");
    expect(within(activeRow).getByTestId("scenario-active-badge")).toHaveTextContent("активен");
    const inactiveRow = rows.find((r) => r.textContent?.includes("baseline"));
    if (!inactiveRow) throw new Error("inactive row not found");
    expect(within(inactiveRow).queryByTestId("scenario-active-badge")).not.toBeInTheDocument();
  });

  it("the save button is worded «сохранить настройки и правки операций», not «текущее состояние»", async () => {
    route({ [LIST]: () => json(200, { scenarios: [] }) });
    renderInRouter(<ScenariosPage id={WS} />);

    await screen.findByTestId("scenario-create-form");
    const button = screen.getByTestId("scenario-create-submit");
    expect(button).toHaveTextContent("Сохранить настройки и правки операций");
    expect(button.textContent).not.toMatch(/текущее состояние/i);
  });

  it("refuses an empty name without calling the server", async () => {
    const fetchMock = route({ [LIST]: () => json(200, { scenarios: [] }) });
    renderInRouter(<ScenariosPage id={WS} />);

    await screen.findByTestId("scenario-create-form");
    await userEvent.click(screen.getByTestId("scenario-create-submit"));

    expect(await screen.findByText("Укажите имя сценария")).toBeInTheDocument();
    const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
    expect(posts).toHaveLength(0);
  });

  it("creates a scenario from the current state and invalidates the list", async () => {
    const fetchMock = route({
      [LIST]: () => json(200, { scenarios: [] }),
      [CREATE]: () => json(201, scenarioDetailFixture({ id: 9, name: "release-1" })),
    });
    const queryClient = makeQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    renderInRouter(<ScenariosPage id={WS} />, { queryClient });

    await screen.findByTestId("scenario-create-form");
    await userEvent.type(screen.getByTestId("scenario-create-name"), "release-1");
    await userEvent.click(screen.getByTestId("scenario-create-submit"));

    expect(await screen.findByTestId("scenario-created")).toHaveTextContent("release-1");
    const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
    expect(posts).toHaveLength(1);
    expect(JSON.parse(String(posts[0]?.[1]?.body))).toEqual({ name: "release-1" });

    await waitFor(() => {
      const keys = invalidateSpy.mock.calls.map((call) => call[0]?.queryKey);
      expect(keys).toContainEqual(getListScenariosQueryKey(WS));
    });
  });

  it("shows the server's own message on a 409 (a scenario is already active, or the name exists)", async () => {
    route({
      [LIST]: () => json(200, { scenarios: [] }),
      [CREATE]: () =>
        json(409, { error: { code: "conflict", message: "scenario name already exists" } }),
    });
    renderInRouter(<ScenariosPage id={WS} />);

    await screen.findByTestId("scenario-create-form");
    await userEvent.type(screen.getByTestId("scenario-create-name"), "dup");
    await userEvent.click(screen.getByTestId("scenario-create-submit"));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Такое значение уже используется");
    expect(alert).toHaveTextContent("scenario name already exists");
  });

  it("A10: while a scenario is active, the button reads «деактивировать и сохранить», and submitting deactivates THEN creates", async () => {
    const fetchMock = route({
      [LIST]: () =>
        json(200, {
          scenarios: [scenarioSummaryFixture({ id: 1, name: "baseline", isActive: true })],
        }),
      [DEACTIVATE]: () => json(200, { scenarioId: null, revision: 5 }),
      [CREATE]: () => json(201, scenarioDetailFixture({ id: 9, name: "release-2" })),
    });
    renderInRouter(<ScenariosPage id={WS} />);

    // The form renders on the first paint, before the list query that tells
    // it whether a scenario is active has resolved — wait for that note
    // (only shown once activeScenario is known) before reading the button's
    // label, or this assertion races the list request.
    expect(await screen.findByTestId("scenario-create-active-note")).toHaveTextContent("baseline");
    const button = screen.getByTestId("scenario-create-submit");
    expect(button).toHaveTextContent("Деактивировать и сохранить");

    await userEvent.type(screen.getByTestId("scenario-create-name"), "release-2");
    await userEvent.click(button);

    expect(await screen.findByTestId("scenario-created")).toHaveTextContent("release-2");
    const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
    const urls = posts.map(([url]) => String(url));
    const deactivateIndex = urls.indexOf(`/api/workspaces/${WS}/scenarios/deactivate`);
    const createIndex = urls.indexOf(`/api/workspaces/${WS}/scenarios`);
    expect(deactivateIndex).toBeGreaterThanOrEqual(0);
    expect(createIndex).toBeGreaterThan(deactivateIndex);
  });

  it("activates a scenario and invalidates both the list and the workspace", async () => {
    const fetchMock = route({
      [LIST]: () =>
        json(200, {
          scenarios: [scenarioSummaryFixture({ id: 3, name: "beta", isActive: false })],
        }),
      [`POST /api/workspaces/${WS}/scenarios/3/activate`]: () =>
        json(200, { scenarioId: 3, revision: 2 }),
    });
    const queryClient = makeQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    renderInRouter(<ScenariosPage id={WS} />, { queryClient });

    await userEvent.click(await screen.findByTestId("scenario-activate"));

    await waitFor(() => {
      const activates = fetchMock.mock.calls.filter(
        ([url, init]) =>
          String(url) === `/api/workspaces/${WS}/scenarios/3/activate` && init?.method === "POST",
      );
      expect(activates).toHaveLength(1);
    });
    await waitFor(() => {
      const keys = invalidateSpy.mock.calls.map((call) => call[0]?.queryKey);
      expect(keys).toContainEqual(getListScenariosQueryKey(WS));
      expect(keys).toContainEqual(getGetWorkspaceQueryKey(WS));
    });
  });

  it("deactivates the active scenario from its own row", async () => {
    const fetchMock = route({
      [LIST]: () =>
        json(200, {
          scenarios: [scenarioSummaryFixture({ id: 3, name: "beta", isActive: true })],
        }),
      [DEACTIVATE]: () => json(200, { scenarioId: null, revision: 3 }),
    });
    renderInRouter(<ScenariosPage id={WS} />);

    await userEvent.click(await screen.findByTestId("scenario-deactivate"));

    await waitFor(() => {
      const deactivates = fetchMock.mock.calls.filter(
        ([url, init]) =>
          String(url) === `/api/workspaces/${WS}/scenarios/deactivate` && init?.method === "POST",
      );
      expect(deactivates).toHaveLength(1);
    });
  });

  it("asks before deleting, and names the scenario in the confirmation", async () => {
    const fetchMock = route({
      [LIST]: () =>
        json(200, {
          scenarios: [scenarioSummaryFixture({ id: 5, name: "old-run", isActive: false })],
        }),
    });
    renderInRouter(<ScenariosPage id={WS} />);

    await userEvent.click(await screen.findByTestId("scenario-delete"));

    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("old-run");
    expect(dialog).toHaveTextContent("необратимо");
    await userEvent.click(within(dialog).getByText("Отмена"));

    await waitFor(() => {
      const deletes = fetchMock.mock.calls.filter(([, init]) => init?.method === "DELETE");
      expect(deletes).toHaveLength(0);
    });
  });

  it("warns that deleting the ACTIVE scenario also deactivates it", async () => {
    route({
      [LIST]: () =>
        json(200, {
          scenarios: [scenarioSummaryFixture({ id: 5, name: "old-run", isActive: true })],
        }),
    });
    renderInRouter(<ScenariosPage id={WS} />);

    await userEvent.click(await screen.findByTestId("scenario-delete"));

    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("сейчас активен");
  });

  it("deletes on confirmation and invalidates the list and the workspace", async () => {
    const fetchMock = route({
      [LIST]: () =>
        json(200, {
          scenarios: [scenarioSummaryFixture({ id: 5, name: "old-run", isActive: false })],
        }),
      [`DELETE /api/workspaces/${WS}/scenarios/5`]: () => new Response(null, { status: 204 }),
    });
    const queryClient = makeQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    renderInRouter(<ScenariosPage id={WS} />, { queryClient });

    await userEvent.click(await screen.findByTestId("scenario-delete"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("scenario-delete-confirm"));

    await waitFor(() => {
      const deletes = fetchMock.mock.calls.filter(([, init]) => init?.method === "DELETE");
      expect(deletes).toHaveLength(1);
    });
    await waitFor(() => {
      const keys = invalidateSpy.mock.calls.map((call) => call[0]?.queryKey);
      expect(keys).toContainEqual(getListScenariosQueryKey(WS));
      expect(keys).toContainEqual(getGetWorkspaceQueryKey(WS));
    });
  });

  it("names the scenario when a delete fails", async () => {
    route({
      [LIST]: () =>
        json(200, {
          scenarios: [scenarioSummaryFixture({ id: 5, name: "old-run", isActive: false })],
        }),
      [`DELETE /api/workspaces/${WS}/scenarios/5`]: () =>
        json(404, { error: { code: "not_found", message: "gone" } }),
    });
    renderInRouter(<ScenariosPage id={WS} />);

    await userEvent.click(await screen.findByTestId("scenario-delete"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("scenario-delete-confirm"));

    expect(await screen.findByRole("alert")).toHaveTextContent("«old-run»: Не найдено");
  });

  it("clones a scenario under a new name by posting `from`, available while a scenario is active", async () => {
    // isActive: true on purpose — SIG-CLONE's whole point is that `from`
    // bypasses A10's refusal, so this is the one case that would fail if
    // clone reused the create form's own deactivate-then-create sequence.
    const fetchMock = route({
      [LIST]: () =>
        json(200, {
          scenarios: [scenarioSummaryFixture({ id: 3, name: "beta", isActive: true })],
        }),
      [CREATE]: () => json(201, scenarioDetailFixture({ id: 9, name: "beta-copy" })),
    });
    const queryClient = makeQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    renderInRouter(<ScenariosPage id={WS} />, { queryClient });

    await userEvent.click(await screen.findByTestId("scenario-clone"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByTestId("scenario-clone-name"), "beta-copy");
    await userEvent.click(within(dialog).getByTestId("scenario-clone-submit"));

    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(
        ([url, init]) =>
          String(url) === `/api/workspaces/${WS}/scenarios` && init?.method === "POST",
      );
      expect(posts).toHaveLength(1);
      expect(JSON.parse(String(posts[0]?.[1]?.body))).toEqual({ name: "beta-copy", from: 3 });
    });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await waitFor(() => {
      const keys = invalidateSpy.mock.calls.map((call) => call[0]?.queryKey);
      expect(keys).toContainEqual(getListScenariosQueryKey(WS));
      expect(keys).toContainEqual(getGetWorkspaceQueryKey(WS));
    });
  });

  it("shows the server's own message on a 409 while cloning (name taken)", async () => {
    route({
      [LIST]: () =>
        json(200, {
          scenarios: [scenarioSummaryFixture({ id: 3, name: "beta", isActive: false })],
        }),
      [CREATE]: () =>
        json(409, { error: { code: "conflict", message: "scenario name already exists" } }),
    });
    renderInRouter(<ScenariosPage id={WS} />);

    await userEvent.click(await screen.findByTestId("scenario-clone"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByTestId("scenario-clone-name"), "dup");
    await userEvent.click(within(dialog).getByTestId("scenario-clone-submit"));

    const alert = await within(dialog).findByRole("alert");
    expect(alert).toHaveTextContent("Такое значение уже используется");
    expect(alert).toHaveTextContent("scenario name already exists");
  });

  it("renames a scenario, warning that a mock-plane test suite naming it will break, and PUTs the new name", async () => {
    const fetchMock = route({
      [LIST]: () =>
        json(200, {
          scenarios: [scenarioSummaryFixture({ id: 3, name: "beta", isActive: false })],
        }),
      [`PUT /api/workspaces/${WS}/scenarios/3`]: () =>
        json(200, scenarioDetailFixture({ id: 3, name: "beta-v2" })),
    });
    const queryClient = makeQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    renderInRouter(<ScenariosPage id={WS} />, { queryClient });

    await userEvent.click(await screen.findByTestId("scenario-rename"));
    const dialog = await screen.findByRole("dialog");
    // The one-sentence warning names both the JSON shape a mock-plane
    // switch uses and the fact that renaming breaks it for a suite keyed
    // on the old name — this is the only screen where an operator learns
    // that before it bites them.
    expect(dialog).toHaveTextContent('{"scenario":"…"}');
    expect(dialog).toHaveTextContent("мок-плоскости");

    const input = within(dialog).getByTestId("scenario-rename-name") as HTMLInputElement;
    expect(input.value).toBe("beta");
    await userEvent.clear(input);
    await userEvent.type(input, "beta-v2");
    await userEvent.click(within(dialog).getByTestId("scenario-rename-submit"));

    await waitFor(() => {
      const puts = fetchMock.mock.calls.filter(
        ([url, init]) =>
          String(url) === `/api/workspaces/${WS}/scenarios/3` && init?.method === "PUT",
      );
      expect(puts).toHaveLength(1);
      // A3: editVersion travels from this row's own preceding read
      // (scenarioSummaryFixture's default, 1) alongside the new name.
      expect(JSON.parse(String(puts[0]?.[1]?.body))).toEqual({
        name: "beta-v2",
        editVersion: 1,
      });
    });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await waitFor(() => {
      const keys = invalidateSpy.mock.calls.map((call) => call[0]?.queryKey);
      expect(keys).toContainEqual(getListScenariosQueryKey(WS));
      expect(keys).toContainEqual(getGetWorkspaceQueryKey(WS));
    });
  });

  it("shows the server's own message on a 409 while renaming (name taken)", async () => {
    route({
      [LIST]: () =>
        json(200, {
          scenarios: [scenarioSummaryFixture({ id: 3, name: "beta", isActive: false })],
        }),
      [`PUT /api/workspaces/${WS}/scenarios/3`]: () =>
        json(409, { error: { code: "conflict", message: "scenario name already exists" } }),
    });
    renderInRouter(<ScenariosPage id={WS} />);

    await userEvent.click(await screen.findByTestId("scenario-rename"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("scenario-rename-submit"));

    const alert = await within(dialog).findByRole("alert");
    expect(alert).toHaveTextContent("Такое значение уже используется");
    expect(alert).toHaveTextContent("scenario name already exists");
  });

  // A3/property 7 (UI half): distinct from the "name taken" 409 above —
  // edit_conflict must translate differently, carry `details` (name +
  // editVersion), and the reload affordance must adopt the conflict's own
  // editVersion so the retry sends it, not the stale one from the list.
  it("shows the edit_conflict affordance while renaming, distinct from a name-taken 409, and retries with the new editVersion", async () => {
    let putCount = 0;
    let lastBody: { editVersion?: number } | undefined;
    route({
      [LIST]: () =>
        json(200, {
          scenarios: [scenarioSummaryFixture({ id: 3, name: "beta", isActive: false })],
        }),
      [`PUT /api/workspaces/${WS}/scenarios/3`]: () => {
        putCount += 1;
        if (putCount === 1) {
          return json(409, {
            error: {
              code: "edit_conflict",
              message: "stale",
              details: { name: "beta-renamed-elsewhere", editVersion: 8 },
            },
          });
        }
        return json(200, scenarioDetailFixture({ id: 3, name: "beta-v2" }));
      },
    });
    const original = globalThis.fetch;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        if (init?.method === "PUT" && init.body) {
          lastBody = JSON.parse(String(init.body)) as { editVersion?: number };
        }
        return original(input, init);
      }),
    );
    renderInRouter(<ScenariosPage id={WS} />);

    await userEvent.click(await screen.findByTestId("scenario-rename"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("scenario-rename-submit"));

    const conflict = await within(dialog).findByTestId("scenario-rename-conflict");
    expect(conflict).toHaveTextContent("Кто-то другой изменил это, пока вы редактировали");
    expect(lastBody?.editVersion).toBe(1);

    await userEvent.click(within(dialog).getByTestId("scenario-rename-conflict-reload"));
    expect((within(dialog).getByTestId("scenario-rename-name") as HTMLInputElement).value).toBe(
      "beta-renamed-elsewhere",
    );

    await userEvent.click(within(dialog).getByTestId("scenario-rename-submit"));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    expect(lastBody?.editVersion).toBe(8);
    expect(putCount).toBe(2);
  });

  it("cancels clone and rename modals without calling the server", async () => {
    const fetchMock = route({
      [LIST]: () =>
        json(200, {
          scenarios: [scenarioSummaryFixture({ id: 3, name: "beta", isActive: false })],
        }),
    });
    renderInRouter(<ScenariosPage id={WS} />);

    await userEvent.click(await screen.findByTestId("scenario-rename"));
    let dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByText("Отмена"));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    await userEvent.click(await screen.findByTestId("scenario-clone"));
    dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByText("Отмена"));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    const mutating = fetchMock.mock.calls.filter(([, init]) => (init?.method ?? "GET") !== "GET");
    expect(mutating).toHaveLength(0);
  });

  // A21 (G2): what a scenario holds, before activating it.
  it("opens a row into the snapshot's overrides and settings on «что внутри»", async () => {
    route({
      [LIST]: () =>
        json(200, {
          scenarios: [scenarioSummaryFixture({ id: 1, name: "baseline", isActive: false })],
        }),
      [`GET /api/workspaces/${WS}/scenarios/1`]: () =>
        json(
          200,
          scenarioDetailFixture({
            overrides: [
              {
                method: "GET",
                path: "/pets",
                overrideOn: true,
                routeOff: true,
                activeStatus: 500,
                responses: { "500": { mode: "pinned", body: {} } },
              },
            ],
          }),
        ),
    });
    renderInRouter(<ScenariosPage id={WS} />);
    await userEvent.click(await screen.findByTestId("scenario-details-toggle"));
    const details = await screen.findByTestId("scenario-details");
    expect(details).toHaveTextContent("seed 1");
    expect(within(details).getByTestId("scenario-details-override")).toHaveTextContent(
      "GET /pets → 500 (статусы: 500) · маршрут выключен",
    );
  });
});

import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CustomEndpointsPage } from "./CustomEndpointsPage";
import { makeQueryClient, renderInRouter } from "@/test/render";
import { json, route } from "@/test/http";
import {
  endpointListViewFixture,
  endpointViewFixture,
  variantFixture,
  workspaceFixture,
} from "@/test/fixtures";
import { getListEndpointsQueryKey } from "@/api/generated/endpoints/endpoints.ts";
import { getGetWorkspaceQueryKey } from "@/api/generated/workspaces/workspaces.ts";

const WS = 7;
const LIST = `GET /api/workspaces/${WS}/endpoints`;
const CREATE = `POST /api/workspaces/${WS}/endpoints`;

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("CustomEndpointsPage", () => {
  it("renders its outer marker and says it is loading before the list answers", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    renderInRouter(<CustomEndpointsPage id={WS} />);

    expect(await screen.findByTestId("custom-endpoints-page")).toBeInTheDocument();
    expect(screen.getByText("Загрузка…")).toBeInTheDocument();
  });

  it("offers a retry when the list fails, in Russian, without dropping the outer marker", async () => {
    let calls = 0;
    route({
      [LIST]: () => {
        calls += 1;
        return calls === 1
          ? json(500, { error: { code: "internal", message: "db is down" } })
          : json(200, endpointListViewFixture({ endpoints: [] }));
      },
    });
    renderInRouter(<CustomEndpointsPage id={WS} />);

    // The outer marker survives the error branch — it sits outside the
    // four-state switch, not inside the success branch alone.
    expect(await screen.findByTestId("custom-endpoints-page")).toBeInTheDocument();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Ошибка на сервере. Попробуйте ещё раз");
    expect(alert).not.toHaveTextContent("db is down");

    await userEvent.click(screen.getByTestId("endpoints-retry"));
    expect(await screen.findByTestId("endpoints-empty")).toBeInTheDocument();
  });

  it("explains what a custom endpoint is for when the list is empty", async () => {
    route({ [LIST]: () => json(200, endpointListViewFixture({ endpoints: [] })) });
    renderInRouter(<CustomEndpointsPage id={WS} />);

    expect(await screen.findByTestId("endpoints-empty")).toHaveTextContent("Кастомных endpoint");
  });

  it("points to the traffic screen as the primary way to create one", async () => {
    route({ [LIST]: () => json(200, endpointListViewFixture({ endpoints: [] })) });
    renderInRouter(<CustomEndpointsPage id={WS} />);

    await screen.findByTestId("custom-endpoints-page");
    expect(
      screen.getByRole("link", { name: "«создать endpoint из запроса» на экране трафика" }),
    ).toHaveAttribute("href", `/workspaces/${WS}/traffic`);
  });

  it("lists method, path, canonicalPath, active status, routeOff and the response statuses present", async () => {
    const ep = endpointViewFixture({
      id: 42,
      method: "POST",
      path: "/custom/widgets",
      canonicalPath: "/custom/widgets",
      activeStatus: 201,
      routeOff: true,
      responses: { "201": variantFixture(), "404": variantFixture() },
      createdAt: 1_700_000_000,
      updatedAt: 1_700_000_100,
    });
    route({ [LIST]: () => json(200, endpointListViewFixture({ endpoints: [ep] })) });
    renderInRouter(<CustomEndpointsPage id={WS} />);

    const row = await screen.findByTestId("endpoint-row");
    expect(row).toHaveTextContent("POST /custom/widgets");
    expect(row).toHaveTextContent("канонический путь /custom/widgets");
    expect(row).toHaveTextContent("активный статус 201");
    expect(row).toHaveTextContent("статусы: 201, 404");
    expect(row).toHaveTextContent("маршрут выключен");
  });

  it("does NOT show the routeOff badge for an active route", async () => {
    const ep = endpointViewFixture({ routeOff: false });
    route({ [LIST]: () => json(200, endpointListViewFixture({ endpoints: [ep] })) });
    renderInRouter(<CustomEndpointsPage id={WS} />);

    const row = await screen.findByTestId("endpoint-row");
    expect(row).not.toHaveTextContent("маршрут выключен");
  });

  it("opens an edit form pre-filled with the endpoint's current values", async () => {
    const ep = endpointViewFixture({
      id: 5,
      method: "GET",
      path: "/custom/ping",
      activeStatus: 200,
      responses: {
        "200": variantFixture({ mode: "pinned", body: { ok: true }, mediaType: "text/plain" }),
      },
    });
    route({ [LIST]: () => json(200, endpointListViewFixture({ endpoints: [ep] })) });
    renderInRouter(<CustomEndpointsPage id={WS} />);

    await userEvent.click(await screen.findByTestId("endpoint-edit-toggle"));

    const form = await screen.findByTestId("endpoint-edit-form");
    expect(within(form).getByTestId("endpoint-edit-path")).toHaveValue("/custom/ping");
    expect(within(form).getByTestId("endpoint-edit-status")).toHaveValue("200");
    expect(within(form).getByTestId("endpoint-edit-media-type")).toHaveValue("text/plain");
    expect(within(form).getByTestId("endpoint-edit-body")).toHaveValue(
      JSON.stringify({ ok: true }, null, 2),
    );
  });

  it("cancels the edit form without calling the server", async () => {
    const ep = endpointViewFixture({ id: 5 });
    const fetchMock = route({
      [LIST]: () => json(200, endpointListViewFixture({ endpoints: [ep] })),
    });
    renderInRouter(<CustomEndpointsPage id={WS} />);

    await userEvent.click(await screen.findByTestId("endpoint-edit-toggle"));
    await screen.findByTestId("endpoint-edit-form");
    await userEvent.click(screen.getByTestId("endpoint-edit-cancel"));

    expect(screen.queryByTestId("endpoint-edit-form")).not.toBeInTheDocument();
    const puts = fetchMock.mock.calls.filter(([, init]) => init?.method === "PUT");
    expect(puts).toHaveLength(0);
  });

  it("saves an edit through PUT and invalidates the endpoints list and the workspace", async () => {
    const ep = endpointViewFixture({
      id: 5,
      method: "GET",
      path: "/custom/ping",
      activeStatus: 200,
      responses: {
        "200": variantFixture({ mode: "pinned", body: { ok: true }, mediaType: undefined }),
        "404": variantFixture({ mode: "pinned", body: { missing: true } }),
      },
    });
    const fetchMock = route({
      [LIST]: () => json(200, endpointListViewFixture({ endpoints: [ep] })),
      [`PUT /api/workspaces/${WS}/endpoints/5`]: () =>
        json(200, endpointViewFixture({ ...ep, path: "/custom/pong" })),
    });
    const queryClient = makeQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    renderInRouter(<CustomEndpointsPage id={WS} />, { queryClient });

    await userEvent.click(await screen.findByTestId("endpoint-edit-toggle"));
    const form = await screen.findByTestId("endpoint-edit-form");
    const pathField = within(form).getByTestId("endpoint-edit-path");
    await userEvent.clear(pathField);
    await userEvent.type(pathField, "/custom/pong");
    await userEvent.click(within(form).getByTestId("endpoint-edit-submit"));

    await waitFor(() => {
      expect(screen.queryByTestId("endpoint-edit-form")).not.toBeInTheDocument();
    });

    const puts = fetchMock.mock.calls.filter(([, init]) => init?.method === "PUT");
    expect(puts).toHaveLength(1);
    const sentBody: unknown = JSON.parse(String(puts[0]?.[1]?.body));
    // The edited variant (200) is MUTATED from what the endpoint already
    // carried, not replaced wholesale: the form only knows mode/body/
    // mediaType, but bodyEncoding/when/headers/recipes/schemaPatch on that
    // same status must survive an edit that only touches path — the
    // anti-pattern from_traffic.go's pinObservedBody names by comment
    // (contract-2, mocker-a-mcp round-2 review). The variant this form
    // doesn't touch (404) is resent unchanged, byte for byte, through a
    // JSON round-trip of the fixture the endpoint already carried — PUT is a
    // full replacement, so silently dropping it would delete that status.
    expect(sentBody).toEqual({
      method: "GET",
      path: "/custom/pong",
      activeStatus: 200,
      responses: {
        "200": JSON.parse(JSON.stringify(ep.responses["200"])),
        "404": JSON.parse(JSON.stringify(ep.responses["404"])),
      },
      overrideOn: true,
      routeOff: false,
      listSize: { min: 1, max: 5 },
      delayMs: 0,
      // A3: sent back exactly what this row's own preceding read
      // (endpointViewFixture's editVersion above) carried, never re-fetched.
      editVersion: ep.editVersion,
    });

    await waitFor(() => {
      const keys = invalidateSpy.mock.calls.map((call) => call[0]?.queryKey);
      expect(keys).toContainEqual(getListEndpointsQueryKey(WS));
      expect(keys).toContainEqual(getGetWorkspaceQueryKey(WS));
    });
  });

  // A3/property 7 (UI half): the 409's `details` must reach this row's edit
  // form, translate to Russian, and the reload affordance must adopt the
  // conflict's own editVersion so the retry — not a re-fetch — carries it.
  it("shows the edit_conflict affordance and retries with the conflict's own editVersion", async () => {
    const ep = endpointViewFixture({ id: 5, method: "GET", path: "/custom/ping" });
    let putCount = 0;
    let lastBody: { editVersion?: number } | undefined;
    route({
      [LIST]: () => json(200, endpointListViewFixture({ endpoints: [ep] })),
      [`PUT /api/workspaces/${WS}/endpoints/5`]: () => {
        putCount += 1;
        if (putCount === 1) {
          return json(409, {
            error: {
              code: "edit_conflict",
              message: "stale",
              details: {
                method: "GET",
                path: "/custom/ping",
                overrideOn: true,
                routeOff: false,
                activeStatus: 200,
                responses: ep.responses,
                listSize: ep.listSize,
                delayMs: ep.delayMs,
                editVersion: 77,
              },
            },
          });
        }
        return json(200, ep);
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
    renderInRouter(<CustomEndpointsPage id={WS} />);

    await userEvent.click(await screen.findByTestId("endpoint-edit-toggle"));
    const form = await screen.findByTestId("endpoint-edit-form");
    await userEvent.click(within(form).getByTestId("endpoint-edit-submit"));

    const conflict = await within(form).findByTestId("endpoint-edit-conflict");
    expect(conflict).toHaveTextContent("Кто-то другой изменил это, пока вы редактировали");
    expect(lastBody?.editVersion).toBe(ep.editVersion);

    await userEvent.click(within(conflict).getByTestId("endpoint-conflict-reload"));
    await userEvent.click(within(form).getByTestId("endpoint-edit-submit"));

    await waitFor(() => {
      expect(screen.queryByTestId("endpoint-edit-form")).not.toBeInTheDocument();
    });
    expect(lastBody?.editVersion).toBe(77);
    expect(putCount).toBe(2);
  });

  // contract-2 (mocker-a-mcp round-2 review): the failure scenario named by
  // the finding — an endpoint created via "endpoint из трафика" carries
  // bodyEncoding:"base64" on its pinned variant (internal/admin/
  // from_traffic.go's encodeBodyForVariant); editing an unrelated field
  // (here, path) must not silently drop it, or the mock plane serves the
  // raw base64 text literally instead of decoding it.
  it("preserves bodyEncoding on an edit that only touches an unrelated field", async () => {
    const ep = endpointViewFixture({
      id: 9,
      method: "GET",
      path: "/custom/blob",
      activeStatus: 200,
      responses: {
        "200": variantFixture({
          mode: "pinned",
          body: "c29tZSBieXRlcw==",
          bodyEncoding: "base64",
          mediaType: "application/octet-stream",
        }),
      },
    });
    const fetchMock = route({
      [LIST]: () => json(200, endpointListViewFixture({ endpoints: [ep] })),
      [`PUT /api/workspaces/${WS}/endpoints/9`]: () =>
        json(200, endpointViewFixture({ ...ep, path: "/custom/blob2" })),
    });
    renderInRouter(<CustomEndpointsPage id={WS} />);

    await userEvent.click(await screen.findByTestId("endpoint-edit-toggle"));
    const form = await screen.findByTestId("endpoint-edit-form");
    const pathField = within(form).getByTestId("endpoint-edit-path");
    await userEvent.clear(pathField);
    await userEvent.type(pathField, "/custom/blob2");
    await userEvent.click(within(form).getByTestId("endpoint-edit-submit"));

    await waitFor(() => {
      expect(screen.queryByTestId("endpoint-edit-form")).not.toBeInTheDocument();
    });

    const puts = fetchMock.mock.calls.filter(([, init]) => init?.method === "PUT");
    expect(puts).toHaveLength(1);
    const sentBody = JSON.parse(String(puts[0]?.[1]?.body)) as {
      responses: Record<string, { bodyEncoding?: string }>;
    };
    expect(sentBody.responses["200"]?.bodyEncoding).toBe("base64");
  });

  // A18 on the screen (2026-09-05): a function variant is visible as a badge
  // and its Lua is shown back in the edit form; saving sends the function
  // with no body and the neutral mode; a body typed beside it is refused
  // before any request, in the form's own words.
  it("shows a function variant's Lua in the edit form and saves it with no body", async () => {
    const ep = endpointViewFixture({
      id: 5,
      activeStatus: 200,
      responses: {
        "200": variantFixture({
          mode: "generated",
          body: undefined,
          mediaType: undefined,
          function: "return 200, { ok = true }",
          // Left over from before the function; the server refuses them
          // beside one (function_and_body), so a save from here drops them.
          recipes: { "$.id": { kind: "sequence" } },
          schemaPatch: [{ op: "add", path: "/x", value: 1 }],
          when: [{ in: "header", name: "x-test", op: "exists" }],
        }),
      },
    });
    const fetchMock = route({
      [LIST]: () => json(200, endpointListViewFixture({ endpoints: [ep] })),
      [`PUT /api/workspaces/${WS}/endpoints/5`]: () => json(200, ep),
    });
    renderInRouter(<CustomEndpointsPage id={WS} />);

    expect(await screen.findByTestId("endpoint-function")).toHaveTextContent("функция Lua");
    await userEvent.click(screen.getByTestId("endpoint-edit-toggle"));
    const form = await screen.findByTestId("endpoint-edit-form");
    const lua = within(form).getByTestId("endpoint-edit-function");
    expect(lua).toHaveValue("return 200, { ok = true }");
    expect(within(form).getByTestId("endpoint-edit-body")).toHaveValue("");

    await userEvent.type(within(form).getByTestId("endpoint-edit-body"), "{{}");
    await userEvent.click(within(form).getByTestId("endpoint-edit-submit"));
    expect(within(form).getByText(/Функция и тело ответа взаимоисключающие/)).toBeInTheDocument();
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "PUT")).toHaveLength(0);

    await userEvent.clear(within(form).getByTestId("endpoint-edit-body"));
    await userEvent.clear(lua);
    await userEvent.type(lua, "return 404, {{ gone = true }");
    await userEvent.click(within(form).getByTestId("endpoint-edit-submit"));
    await waitFor(() => {
      expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "PUT")).toHaveLength(1);
    });
    const put = fetchMock.mock.calls.find(([, init]) => init?.method === "PUT");
    const sent = JSON.parse(String(put?.[1]?.body)) as {
      responses: Record<string, { mode: string; body?: unknown; function?: string }>;
    };
    expect(sent.responses["200"]).toMatchObject({
      mode: "generated",
      function: "return 404, { gone = true }",
      when: [{ in: "header", name: "x-test", op: "exists" }],
    });
    for (const gone of ["body", "mediaType", "bodyRef", "recipes", "schemaPatch"]) {
      expect(sent.responses["200"]).not.toHaveProperty(gone);
    }
  });

  it("creates an endpoint with a function and no body", async () => {
    const fetchMock = route({
      [LIST]: () => json(200, endpointListViewFixture({ endpoints: [] })),
      [CREATE]: () => json(201, endpointViewFixture({ method: "POST", path: "/login" })),
    });
    renderInRouter(<CustomEndpointsPage id={WS} />);
    await screen.findByTestId("endpoint-create-form");
    await userEvent.type(screen.getByTestId("endpoint-create-path"), "/login");
    await userEvent.type(screen.getByTestId("endpoint-create-function"), "return 200, {{}");
    await userEvent.click(screen.getByTestId("endpoint-create-submit"));
    await waitFor(() => {
      expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(1);
    });
    const post = fetchMock.mock.calls.find(([, init]) => init?.method === "POST");
    expect(JSON.parse(String(post?.[1]?.body))).toEqual({
      method: "GET",
      path: "/login",
      function: "return 200, {}",
    });
  });

  it("refuses malformed JSON in the body field without calling the server", async () => {
    const fetchMock = route({
      [LIST]: () => json(200, endpointListViewFixture({ endpoints: [] })),
    });
    renderInRouter(<CustomEndpointsPage id={WS} />);

    await screen.findByTestId("endpoint-create-form");
    await userEvent.type(screen.getByTestId("endpoint-create-path"), "/custom/ping");
    // user-event v14 treats { as the start of a special-key sequence, so a
    // literal brace is typed as {{ — this is still just "{not json" landing
    // in the field.
    await userEvent.type(screen.getByTestId("endpoint-create-body"), "{{not json");
    await userEvent.click(screen.getByTestId("endpoint-create-submit"));

    // Shows WHERE the JSON is broken, not just that it is broken.
    expect(await screen.findByText(/JSON невалиден/)).toBeInTheDocument();
    const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
    expect(posts).toHaveLength(0);
  });

  it("refuses an empty path without calling the server", async () => {
    const fetchMock = route({
      [LIST]: () => json(200, endpointListViewFixture({ endpoints: [] })),
    });
    renderInRouter(<CustomEndpointsPage id={WS} />);

    await screen.findByTestId("endpoint-create-form");
    await userEvent.click(screen.getByTestId("endpoint-create-submit"));

    expect(await screen.findByText("Укажите путь")).toBeInTheDocument();
    const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
    expect(posts).toHaveLength(0);
  });

  it("creates an endpoint, sends the omitted fields as omitted, and invalidates the endpoints list and the workspace", async () => {
    const fetchMock = route({
      [LIST]: () => json(200, endpointListViewFixture({ endpoints: [] })),
      [CREATE]: () =>
        json(201, endpointViewFixture({ id: 9, method: "GET", path: "/custom/ping" })),
    });
    const queryClient = makeQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    renderInRouter(<CustomEndpointsPage id={WS} />, { queryClient });

    await screen.findByTestId("endpoint-create-form");
    await userEvent.type(screen.getByTestId("endpoint-create-path"), "/custom/ping");
    await userEvent.click(screen.getByTestId("endpoint-create-submit"));

    expect(await screen.findByTestId("endpoint-created")).toHaveTextContent("GET /custom/ping");

    const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
    expect(posts).toHaveLength(1);
    const sentBody: unknown = JSON.parse(String(posts[0]?.[1]?.body));
    // status/body/mediaType all left out of the payload entirely, so the
    // server's own defaults (200, empty body) are what actually apply — not
    // a value this form silently chose on the person's behalf.
    expect(sentBody).toEqual({ method: "GET", path: "/custom/ping" });

    await waitFor(() => {
      const keys = invalidateSpy.mock.calls.map((call) => call[0]?.queryKey);
      expect(keys).toContainEqual(getListEndpointsQueryKey(WS));
      expect(keys).toContainEqual(getGetWorkspaceQueryKey(WS));
    });
  });

  it("sends a typed status, body and mediaType through to the request", async () => {
    const fetchMock = route({
      [LIST]: () => json(200, endpointListViewFixture({ endpoints: [] })),
      [CREATE]: () => json(201, endpointViewFixture({ id: 9, method: "GET", path: "/custom/x" })),
    });
    renderInRouter(<CustomEndpointsPage id={WS} />);

    await screen.findByTestId("endpoint-create-form");
    await userEvent.type(screen.getByTestId("endpoint-create-path"), "/custom/x");
    await userEvent.type(screen.getByTestId("endpoint-create-status"), "404");
    await userEvent.type(screen.getByTestId("endpoint-create-media-type"), "text/plain");
    // user-event v14: only "{" needs escaping (as "{{"); a bare "}" outside
    // an open {keyword} sequence is already literal, so doubling it would
    // type two closing braces instead of one.
    await userEvent.type(screen.getByTestId("endpoint-create-body"), '{{"ok":true}');
    await userEvent.click(screen.getByTestId("endpoint-create-submit"));

    await screen.findByTestId("endpoint-created");
    const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
    const sentBody: unknown = JSON.parse(String(posts[0]?.[1]?.body));
    expect(sentBody).toEqual({
      method: "GET",
      path: "/custom/x",
      status: 404,
      mediaType: "text/plain",
      body: { ok: true },
    });
  });

  it("shows the server's own message on a 409 (the route already exists)", async () => {
    route({
      [LIST]: () => json(200, endpointListViewFixture({ endpoints: [] })),
      [CREATE]: () =>
        json(409, {
          error: { code: "conflict", message: "endpoint GET /custom/ping already exists" },
        }),
    });
    renderInRouter(<CustomEndpointsPage id={WS} />);

    await screen.findByTestId("endpoint-create-form");
    await userEvent.type(screen.getByTestId("endpoint-create-path"), "/custom/ping");
    await userEvent.click(screen.getByTestId("endpoint-create-submit"));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Такое значение уже используется");
    expect(alert).toHaveTextContent("endpoint GET /custom/ping already exists");
  });

  it("asks before deleting, and does nothing if the confirmation is dismissed", async () => {
    const ep = endpointViewFixture({ id: 5, method: "GET", path: "/custom/ping" });
    const fetchMock = route({
      [LIST]: () => json(200, endpointListViewFixture({ endpoints: [ep] })),
    });
    renderInRouter(<CustomEndpointsPage id={WS} />);

    await userEvent.click(await screen.findByTestId("endpoint-delete"));

    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("GET /custom/ping");
    // Points at the edit affordance up front, since delete has no undo — the
    // PUT route (and the "Изменить" button) exist now, so this is no longer
    // the delete-and-recreate-only sentence it used to be.
    expect(dialog).toHaveTextContent("Изменить»");
    await userEvent.click(within(dialog).getByText("Отмена"));

    await waitFor(() => {
      const deletes = fetchMock.mock.calls.filter(([, init]) => init?.method === "DELETE");
      expect(deletes).toHaveLength(0);
    });
  });

  it("deletes on confirmation and invalidates the endpoints list and the workspace", async () => {
    const ep = endpointViewFixture({ id: 5, method: "GET", path: "/custom/ping" });
    const fetchMock = route({
      [LIST]: () => json(200, endpointListViewFixture({ endpoints: [ep] })),
      [`DELETE /api/workspaces/${WS}/endpoints/5`]: () => new Response(null, { status: 204 }),
    });
    const queryClient = makeQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    renderInRouter(<CustomEndpointsPage id={WS} />, { queryClient });

    await userEvent.click(await screen.findByTestId("endpoint-delete"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("endpoint-delete-confirm"));

    await waitFor(() => {
      const deletes = fetchMock.mock.calls.filter(([, init]) => init?.method === "DELETE");
      expect(deletes).toHaveLength(1);
    });
    await waitFor(() => {
      const keys = invalidateSpy.mock.calls.map((call) => call[0]?.queryKey);
      expect(keys).toContainEqual(getListEndpointsQueryKey(WS));
      expect(keys).toContainEqual(getGetWorkspaceQueryKey(WS));
    });
  });

  it("names the endpoint when a delete fails", async () => {
    const ep = endpointViewFixture({ id: 5, method: "GET", path: "/custom/ping" });
    route({
      [LIST]: () => json(200, endpointListViewFixture({ endpoints: [ep] })),
      [`DELETE /api/workspaces/${WS}/endpoints/5`]: () =>
        json(404, { error: { code: "not_found", message: "gone" } }),
    });
    renderInRouter(<CustomEndpointsPage id={WS} />);

    await userEvent.click(await screen.findByTestId("endpoint-delete"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("endpoint-delete-confirm"));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Не удалось удалить «GET /custom/ping»",
    );
  });

  // --- P6e: streams on this screen ------------------------------------------

  it("creates an sse stream: the type selector pins GET, the four behaviours replace the body, and kind+stream go on the wire", async () => {
    const fetchMock = route({
      [LIST]: () => json(200, endpointListViewFixture({ endpoints: [] })),
      [CREATE]: () =>
        json(
          201,
          endpointViewFixture({
            id: 12,
            method: "GET",
            path: "/events",
            kind: "sse",
            responses: {},
            stream: { timeline: { frames: [{ delayMs: 1000, data: {} }] } },
          }),
        ),
    });
    renderInRouter(<CustomEndpointsPage id={WS} />);
    await screen.findByTestId("endpoint-create-form");

    await userEvent.selectOptions(screen.getByTestId("endpoint-create-kind"), "sse");
    expect(screen.getByTestId("endpoint-create-method")).toBeDisabled();
    expect(screen.getByTestId("endpoint-create-stream-editor")).toBeInTheDocument();
    expect(screen.getByTestId("endpoint-create-body")).not.toBeVisible();
    await userEvent.type(screen.getByTestId("endpoint-create-path"), "/events");
    await userEvent.clear(screen.getByTestId("endpoint-create-frame-event-0"));
    await userEvent.type(screen.getByTestId("endpoint-create-frame-event-0"), "tick");
    await userEvent.click(screen.getByTestId("endpoint-create-submit"));

    expect(await screen.findByTestId("endpoint-created")).toHaveTextContent("GET /events");
    const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
    expect(posts).toHaveLength(1);
    expect(JSON.parse(String(posts[0]?.[1]?.body))).toEqual({
      method: "GET",
      path: "/events",
      kind: "sse",
      stream: { timeline: { frames: [{ delayMs: 1000, event: "tick", data: {} }] } },
    });
  });

  it("refuses an invalid stream draft in the browser without a request", async () => {
    const fetchMock = route({
      [LIST]: () => json(200, endpointListViewFixture({ endpoints: [] })),
    });
    renderInRouter(<CustomEndpointsPage id={WS} />);
    await screen.findByTestId("endpoint-create-form");
    await userEvent.selectOptions(screen.getByTestId("endpoint-create-kind"), "ws");
    await userEvent.type(screen.getByTestId("endpoint-create-path"), "/chat");
    await userEvent.click(screen.getByTestId("endpoint-create-schedule-on"));
    await userEvent.click(screen.getByTestId("endpoint-create-submit"));
    expect(await screen.findByTestId("endpoint-create-stream-error")).toHaveTextContent(
      "Включите хотя бы одно поведение",
    );
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(0);
  });

  it("marks a stream row, offers the browser test client when the workspace URL is known, and edits it through the stream form", async () => {
    const fetchMock = route({
      [LIST]: () =>
        json(
          200,
          endpointListViewFixture({
            endpoints: [
              endpointViewFixture({
                id: 12,
                method: "GET",
                path: "/events",
                kind: "sse",
                responses: {},
                editVersion: 3,
                stream: { timeline: { frames: [{ delayMs: 500, event: "tick", data: { n: 1 } }] } },
              }),
            ],
          }),
        ),
      [`GET /api/workspaces/${WS}`]: () =>
        json(200, workspaceFixture({ id: WS, url: "http://alex.mock.local" })),
      [`PUT /api/workspaces/${WS}/endpoints/12`]: () =>
        json(200, endpointViewFixture({ id: 12, kind: "sse", path: "/events2", responses: {} })),
    });
    renderInRouter(<CustomEndpointsPage id={WS} />);
    const row = await screen.findByTestId("endpoint-row");
    expect(within(row).getByTestId("endpoint-kind")).toHaveTextContent("SSE");

    await userEvent.click(await within(row).findByTestId("endpoint-test-toggle"));
    expect(screen.getByTestId("endpoint-test-client")).toBeInTheDocument();
    expect(screen.getByTestId("endpoint-12-client")).toHaveTextContent(
      "http://alex.mock.local/events",
    );
    await userEvent.click(screen.getByTestId("endpoint-test-close"));

    await userEvent.click(screen.getByTestId("endpoint-edit-toggle"));
    expect(screen.getByTestId("endpoint-edit-stream-form")).toBeInTheDocument();
    expect(screen.getByTestId("endpoint-edit-frame-event-0")).toHaveValue("tick");
    await userEvent.clear(screen.getByTestId("endpoint-edit-path"));
    await userEvent.type(screen.getByTestId("endpoint-edit-path"), "/events2");
    await userEvent.click(screen.getByTestId("endpoint-edit-submit"));

    await waitFor(() => {
      const puts = fetchMock.mock.calls.filter(([, init]) => init?.method === "PUT");
      expect(puts).toHaveLength(1);
      expect(JSON.parse(String(puts[0]?.[1]?.body))).toEqual({
        method: "GET",
        path: "/events2",
        kind: "sse",
        stream: { timeline: { frames: [{ delayMs: 500, event: "tick", data: { n: 1 } }] } },
        activeStatus: 200,
        overrideOn: true,
        routeOff: false,
        // A20: the fields a stream never uses still ride along — a full
        // replacement that omitted them would reset the row's defaults.
        listSize: { min: 1, max: 5 },
        delayMs: 0,
        editVersion: 3,
      });
    });
  });

  // A21 (review B2/B5): the edit form used to force `mode: pinned, body:
  // undefined` on whatever the active status served from — a P7a schema
  // became an empty pinned body on a path edit, and a file (bodyRef) showed
  // an empty box whose first keystroke was a 400. Now an empty box leaves
  // the stored producer alone and the line above the box says what it is.
  it("keeps a schema-backed generated variant when only the path is edited, and says so", async () => {
    const ep = endpointViewFixture({
      id: 5,
      activeStatus: 200,
      responses: {
        "200": variantFixture({ mode: "generated", body: undefined, schema: { type: "object" } }),
      },
    });
    const fetchMock = route({
      [LIST]: () => json(200, endpointListViewFixture({ endpoints: [ep] })),
      [`PUT /api/workspaces/${WS}/endpoints/5`]: () => json(200, ep),
    });
    renderInRouter(<CustomEndpointsPage id={WS} />);
    await userEvent.click(await screen.findByTestId("endpoint-edit-toggle"));
    const form = await screen.findByTestId("endpoint-edit-form");
    expect(within(form).getByTestId("endpoint-edit-producer-note")).toHaveTextContent(
      "строится по схеме",
    );
    const pathField = within(form).getByTestId("endpoint-edit-path");
    await userEvent.clear(pathField);
    await userEvent.type(pathField, "/custom/renamed");
    await userEvent.click(within(form).getByTestId("endpoint-edit-submit"));
    await waitFor(() => {
      expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "PUT")).toHaveLength(1);
    });
    const put = fetchMock.mock.calls.find(([, init]) => init?.method === "PUT");
    const sent = JSON.parse(String(put?.[1]?.body)) as {
      responses: Record<string, { mode: string; schema?: unknown; body?: unknown }>;
    };
    expect(sent.responses["200"]).toMatchObject({ mode: "generated", schema: { type: "object" } });
    expect(sent.responses["200"]).not.toHaveProperty("body");
  });

  it("keeps a file-backed variant on an empty box and replaces the file when a body is typed", async () => {
    const ep = endpointViewFixture({
      id: 5,
      activeStatus: 200,
      responses: {
        "200": variantFixture({
          mode: "pinned",
          body: undefined,
          mediaType: undefined,
          bodyRef: "asset:logo.png",
        }),
      },
    });
    const fetchMock = route({
      [LIST]: () => json(200, endpointListViewFixture({ endpoints: [ep] })),
      [`PUT /api/workspaces/${WS}/endpoints/5`]: () => json(200, ep),
    });
    renderInRouter(<CustomEndpointsPage id={WS} />);
    await userEvent.click(await screen.findByTestId("endpoint-edit-toggle"));
    const form = await screen.findByTestId("endpoint-edit-form");
    expect(within(form).getByTestId("endpoint-edit-producer-note")).toHaveTextContent("logo.png");
    await userEvent.type(within(form).getByTestId("endpoint-edit-body"), '{{"ok": true}');
    await userEvent.click(within(form).getByTestId("endpoint-edit-submit"));
    await waitFor(() => {
      expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "PUT")).toHaveLength(1);
    });
    const put = fetchMock.mock.calls.find(([, init]) => init?.method === "PUT");
    const sent = JSON.parse(String(put?.[1]?.body)) as {
      responses: Record<string, { mode: string; bodyRef?: string; body?: unknown }>;
    };
    expect(sent.responses["200"]).toMatchObject({ mode: "pinned", body: { ok: true } });
    expect(sent.responses["200"]).not.toHaveProperty("bodyRef");
  });

  // A21 (G9): the two switches the badge had no control for.
  it("turns a custom route off from the edit form", async () => {
    const ep = endpointViewFixture({ id: 5, routeOff: false, overrideOn: true });
    const fetchMock = route({
      [LIST]: () => json(200, endpointListViewFixture({ endpoints: [ep] })),
      [`PUT /api/workspaces/${WS}/endpoints/5`]: () => json(200, { ...ep, routeOff: true }),
    });
    renderInRouter(<CustomEndpointsPage id={WS} />);
    await userEvent.click(await screen.findByTestId("endpoint-edit-toggle"));
    const form = await screen.findByTestId("endpoint-edit-form");
    await userEvent.click(within(form).getByTestId("endpoint-edit-route-off"));
    await userEvent.click(within(form).getByTestId("endpoint-edit-submit"));
    await waitFor(() => {
      const put = fetchMock.mock.calls.find(([, init]) => init?.method === "PUT");
      expect(JSON.parse(String(put?.[1]?.body))).toMatchObject({
        routeOff: true,
        overrideOn: true,
      });
    });
  });
});

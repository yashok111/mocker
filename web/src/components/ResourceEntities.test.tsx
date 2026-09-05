import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ResourceEntities, familySegment } from "./ResourceEntities";
import { renderWithProviders } from "@/test/render";
import { json, route } from "@/test/http";
import type { ResourceEntityView, ResourceFamilyView } from "@/api/generated/schemas";

const WS = 7;
const users: ResourceFamilyView = {
  routeFamily: "/users",
  name: "users",
  decision: "confirmed",
  resourceId: 1,
  idField: "id",
  writeForm: "bare",
  entityCount: 1,
  byBaseScope: null,
};
const LIST = `GET /api/workspaces/${WS}/resources/%2Fusers/entities?limit=50`;

function entity(overrides: Partial<ResourceEntityView> = {}): ResourceEntityView {
  return {
    id: 1,
    entityKey: "42",
    scopeKey: "",
    baseScopeKey: "",
    data: { id: 42, name: "Alex" },
    createdAt: "2026-09-05T00:00:00Z",
    updatedAt: "2026-09-05T00:00:00Z",
    ...overrides,
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("familySegment", () => {
  it("percent-encodes the family so a nested one travels as ONE path segment", () => {
    expect(familySegment({ routeFamily: "/orgs/{}/users" })).toBe("%2Forgs%2F%7B%7D%2Fusers");
  });
});

describe("ResourceEntities", () => {
  it("lists the rows with their key and JSON, and says when there are none", async () => {
    route({ [LIST]: () => json(200, { rows: [], lastId: 0 }) });
    renderWithProviders(<ResourceEntities id={WS} family={users} />);
    expect(await screen.findByTestId("resource-entities-empty")).toBeInTheDocument();
  });

  it("edits a row as JSON through PUT .../entities/{key}, scope omitted for a top-level row", async () => {
    const fetchMock = route({
      [LIST]: () => json(200, { rows: [entity()], lastId: 1 }),
      [`PUT /api/workspaces/${WS}/resources/%2Fusers/entities/42`]: () =>
        json(200, { created: false, row: entity({ data: { id: 42, name: "Bob" } }) }),
    });
    renderWithProviders(<ResourceEntities id={WS} family={users} />);
    const row = await screen.findByTestId("entity-row");
    expect(row).toHaveTextContent("id = 42");
    expect(within(row).getByTestId("entity-data")).toHaveTextContent('"name": "Alex"');

    await userEvent.click(within(row).getByTestId("entity-edit"));
    const box = within(row).getByTestId("entity-edit-data");
    expect(box).toHaveValue(JSON.stringify({ id: 42, name: "Alex" }, null, 2));

    await userEvent.clear(box);
    await userEvent.type(box, "[[1]");
    await userEvent.click(within(row).getByTestId("entity-edit-submit"));
    expect(within(row).getByText("Запись — JSON-объект")).toBeInTheDocument();
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "PUT")).toHaveLength(0);

    await userEvent.clear(box);
    await userEvent.type(box, '{{"id": 42, "name": "Bob"}');
    await userEvent.click(within(row).getByTestId("entity-edit-submit"));
    await waitFor(() => {
      const put = fetchMock.mock.calls.find(([, init]) => init?.method === "PUT");
      expect(put).toBeDefined();
      expect(JSON.parse(String(put?.[1]?.body))).toEqual({ data: { id: 42, name: "Bob" } });
    });
    await waitFor(() => expect(within(row).queryByTestId("entity-edit-form")).toBeNull());
  });

  it("deletes after confirmation, sending the nested row's own scope back", async () => {
    const fetchMock = route({
      [LIST]: () => json(200, { rows: [entity({ scopeKey: "7", baseScopeKey: "" })], lastId: 1 }),
      [`DELETE /api/workspaces/${WS}/resources/%2Fusers/entities/42`]: () =>
        new Response(null, { status: 204 }),
    });
    renderWithProviders(<ResourceEntities id={WS} family={users} />);
    const row = await screen.findByTestId("entity-row");
    expect(within(row).getByTestId("entity-scope")).toHaveTextContent("родитель 7");
    await userEvent.click(within(row).getByTestId("entity-delete"));
    await userEvent.click(await screen.findByTestId("entity-delete-confirm"));
    await waitFor(() => {
      const del = fetchMock.mock.calls.find(([, init]) => init?.method === "DELETE");
      expect(del).toBeDefined();
      expect(JSON.parse(String(del?.[1]?.body))).toEqual({ scopeKey: "7" });
    });
  });

  it("refuses to save a draft over a row that changed underneath, until it is reopened", async () => {
    let calls = 0;
    route({
      [LIST]: () => {
        calls += 1;
        return json(200, {
          rows: [entity({ updatedAt: calls === 1 ? "t1" : "t2", data: { id: 42, n: calls } })],
          lastId: 1,
        });
      },
    });
    const { queryClient } = renderWithProviders(<ResourceEntities id={WS} family={users} />);
    const row = await screen.findByTestId("entity-row");
    await userEvent.click(within(row).getByTestId("entity-edit"));
    expect(within(row).queryByTestId("entity-edit-stale")).toBeNull();

    // Something else wrote the row: the list refetches with a newer updatedAt.
    await queryClient.invalidateQueries();
    expect(await screen.findByTestId("entity-edit-stale")).toBeInTheDocument();
    expect(screen.getByTestId("entity-edit-submit")).toBeDisabled();

    await userEvent.click(screen.getByTestId("entity-edit-cancel"));
    await userEvent.click(screen.getByTestId("entity-edit"));
    expect(screen.queryByTestId("entity-edit-stale")).toBeNull();
    expect(screen.getByTestId("entity-edit-data")).toHaveValue(
      JSON.stringify({ id: 42, n: 2 }, null, 2),
    );
  });

  it("offers «Ещё» only after a full page and asks for the next one after lastId", async () => {
    const full = Array.from({ length: 50 }, (_, i) =>
      entity({ id: i + 1, entityKey: String(i + 1), data: { id: i + 1 } }),
    );
    const fetchMock = route({
      [LIST]: () => json(200, { rows: full, lastId: 50 }),
      [`GET /api/workspaces/${WS}/resources/%2Fusers/entities?limit=50&after=50`]: () =>
        json(200, { rows: [entity({ id: 51, entityKey: "51", data: { id: 51 } })], lastId: 51 }),
    });
    renderWithProviders(<ResourceEntities id={WS} family={users} />);
    await userEvent.click(await screen.findByTestId("resource-entities-more"));
    await waitFor(() => expect(screen.getAllByTestId("entity-row")).toHaveLength(51));
    expect(screen.queryByTestId("resource-entities-more")).toBeNull();
    expect(fetchMock.mock.calls.map(([url]) => String(url))).toContain(
      `/api/workspaces/${WS}/resources/%2Fusers/entities?limit=50&after=50`,
    );
  });

  // A21 (G6): a row can be CREATED, and a nested or base-scoped family is
  // filtered by scope — both were agent-only.
  it("creates a row by key through PUT, with the scope prefilled from the filter", async () => {
    const nested: ResourceFamilyView = {
      ...users,
      routeFamily: "/orgs/{}/users",
      name: "orgs.users",
    };
    const seg = "%2Forgs%2F%7B%7D%2Fusers";
    const fetchMock = route({
      [`GET /api/workspaces/${WS}/resources/${seg}/entities?limit=50`]: () =>
        json(200, { rows: [], lastId: 0 }),
      [`GET /api/workspaces/${WS}/resources/${seg}/entities?limit=50&scopeKey=7`]: () =>
        json(200, { rows: [], lastId: 0 }),
      [`PUT /api/workspaces/${WS}/resources/${seg}/entities/5`]: () =>
        json(200, { created: true, row: entity({ entityKey: "5", scopeKey: "7" }) }),
    });
    renderWithProviders(<ResourceEntities id={WS} family={nested} />);
    // The filter commits on Enter (or blur), not per keystroke.
    await userEvent.type(await screen.findByTestId("resource-entities-scope"), "7{enter}");
    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([u]) => String(u))).toContain(
        `/api/workspaces/${WS}/resources/${seg}/entities?limit=50&scopeKey=7`,
      ),
    );
    await userEvent.click(screen.getByTestId("resource-entities-add"));
    expect(screen.getByTestId("entity-new-scope")).toHaveValue("7");
    await userEvent.type(screen.getByTestId("entity-new-key"), "5");
    const data = screen.getByTestId("entity-new-data");
    await userEvent.clear(data);
    await userEvent.type(data, '{{"name": "Eve"}');
    await userEvent.click(screen.getByTestId("entity-new-submit"));
    await waitFor(() => {
      const put = fetchMock.mock.calls.find(([, init]) => init?.method === "PUT");
      expect(String(put?.[0])).toBe(`/api/workspaces/${WS}/resources/${seg}/entities/5`);
      expect(JSON.parse(String(put?.[1]?.body))).toEqual({ data: { name: "Eve" }, scopeKey: "7" });
    });
    await waitFor(() => expect(screen.queryByTestId("entity-new-form")).toBeNull());
  });

  it("refuses a bad key or a non-object before any request", async () => {
    const fetchMock = route({ [LIST]: () => json(200, { rows: [], lastId: 0 }) });
    renderWithProviders(<ResourceEntities id={WS} family={users} />);
    await userEvent.click(await screen.findByTestId("resource-entities-add"));
    await userEvent.type(screen.getByTestId("entity-new-key"), "no spaces");
    await userEvent.click(screen.getByTestId("entity-new-submit"));
    expect(screen.getByText(/Ключ — от 1 до 128/)).toBeInTheDocument();
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "PUT")).toHaveLength(0);
  });
});

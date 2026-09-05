import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ResourcesPage } from "./ResourcesPage";
import { renderInRouter } from "@/test/render";
import { json, route } from "@/test/http";
import { workspaceFixture } from "@/test/fixtures";
import type { ResourceFamilyView } from "@/api/generated/schemas";

const WS = 7;
const WORKSPACE = `GET /api/workspaces/${WS}`;
const SUGGESTIONS = `GET /api/specs/3/resource-suggestions`;
const RESOURCES = `GET /api/workspaces/${WS}/resources`;
const DECIDE = `POST /api/workspaces/${WS}/resource-decisions`;

function familyFixture(overrides: Partial<ResourceFamilyView> = {}): ResourceFamilyView {
  return {
    routeFamily: "/users",
    name: "users",
    decision: null,
    resourceId: null,
    idField: null,
    writeForm: null,
    entityCount: null,
    // Present on every row the server sends, confirmed or not — the handler
    // tags it without omitempty and api/openapi.json lists it as required
    // beside its four sibling confirmed-only fields, so a fixture that omits
    // it is not a shape this endpoint can ever answer with.
    byBaseScope: null,
    ...overrides,
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("ResourcesPage", () => {
  it("renders its outer marker and says it is loading before the list answers", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    renderInRouter(<ResourcesPage id={WS} />);

    expect(await screen.findByTestId("resources-page")).toBeInTheDocument();
    expect(screen.getByText("Загрузка…")).toBeInTheDocument();
  });

  it("offers a retry when the list fails, in Russian, without dropping the outer marker", async () => {
    let calls = 0;
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex", specId: 3 })),
      [SUGGESTIONS]: () => json(200, { suggestions: [] }),
      [RESOURCES]: () => {
        calls += 1;
        return calls === 1
          ? json(500, { error: { code: "internal", message: "db is down" } })
          : json(200, { families: [] });
      },
    });
    renderInRouter(<ResourcesPage id={WS} />);

    expect(await screen.findByTestId("resources-page")).toBeInTheDocument();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Ошибка на сервере. Попробуйте ещё раз");
    expect(alert).not.toHaveTextContent("db is down");

    await userEvent.click(screen.getByTestId("resources-retry"));
    expect(await screen.findByTestId("resources-empty")).toBeInTheDocument();
  });

  it("explains that there is nothing yet when the list is empty", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex", specId: 3 })),
      [SUGGESTIONS]: () => json(200, { suggestions: [] }),
      [RESOURCES]: () => json(200, { families: [] }),
    });
    renderInRouter(<ResourcesPage id={WS} />);

    expect(await screen.findByTestId("resources-empty")).toBeInTheDocument();
  });

  it("fires the suggestions call (which is also what triggers the backfill) alongside the resources call", async () => {
    const fetchMock = route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex", specId: 3 })),
      [SUGGESTIONS]: () => json(200, { suggestions: [] }),
      [RESOURCES]: () => json(200, { families: [] }),
    });
    renderInRouter(<ResourcesPage id={WS} />);

    await screen.findByTestId("resources-empty");
    const urls = fetchMock.mock.calls.map(([url]) => String(url));
    expect(urls).toContain("/api/specs/3/resource-suggestions");
    expect(urls).toContain(`/api/workspaces/${WS}/resources`);
  });

  it("does not call resource-suggestions when the workspace has no bound spec, and says so", async () => {
    const fetchMock = route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex", specId: null })),
      [RESOURCES]: () => json(200, { families: [] }),
    });
    renderInRouter(<ResourcesPage id={WS} />);

    expect(await screen.findByTestId("resources-no-spec")).toBeInTheDocument();
    const urls = fetchMock.mock.calls.map(([url]) => String(url));
    expect(urls.some((u) => u.includes("resource-suggestions"))).toBe(false);
    // A21 (review B7): the no-spec alert links to the overview and the
    // «в привязанной спеке не нашлось…» sentence — which contradicted it —
    // is not rendered beside it.
    expect(screen.getByTestId("resources-no-spec-link")).toHaveAttribute(
      "href",
      `/workspaces/${WS}`,
    );
    expect(screen.queryByTestId("resources-empty")).toBeNull();
  });

  it("shows entityCount and the GET pointer for a confirmed family, and offers only «отклонить»", async () => {
    route({
      [WORKSPACE]: () =>
        json(
          200,
          workspaceFixture({
            id: WS,
            name: "Alex",
            specId: 3,
            url: "http://alex.mock.corp.internal:8080",
          }),
        ),
      [SUGGESTIONS]: () => json(200, { suggestions: [] }),
      [RESOURCES]: () =>
        json(200, {
          families: [
            familyFixture({
              routeFamily: "/users",
              name: "users",
              decision: "confirmed",
              resourceId: 1,
              idField: "id",
              writeForm: "bare",
              entityCount: 5,
            }),
          ],
        }),
    });
    renderInRouter(<ResourcesPage id={WS} />);

    const row = await screen.findByTestId("resource-row");
    expect(row).toHaveTextContent("Записей: 5");
    // «Записи» opens the entity browser (ResourceEntities.tsx) under the row
    // and toggles back; its own requests are its own test's business.
    expect(screen.queryByTestId("resource-entities")).toBeNull();
    await userEvent.click(within(row).getByTestId("resource-entities-toggle"));
    expect(await screen.findByTestId("resource-entities")).toBeInTheDocument();
    await userEvent.click(within(row).getByTestId("resource-entities-toggle"));
    expect(screen.queryByTestId("resource-entities")).toBeNull();
    expect(row).toHaveTextContent("GET http://alex.mock.corp.internal:8080/users");
    expect(within(row).queryByTestId("resource-confirm")).not.toBeInTheDocument();
    expect(within(row).getByTestId("resource-decline")).toBeInTheDocument();
    expect(within(row).queryByTestId("resource-nested-hint")).not.toBeInTheDocument();
  });

  it("adds a dimmed hint under the GET pointer for a nested family, because {} is not a pastable URL", async () => {
    route({
      [WORKSPACE]: () =>
        json(
          200,
          workspaceFixture({
            id: WS,
            name: "Alex",
            specId: 3,
            url: "http://alex.mock.corp.internal:8080",
          }),
        ),
      [SUGGESTIONS]: () => json(200, { suggestions: [] }),
      [RESOURCES]: () =>
        json(200, {
          families: [
            familyFixture({
              routeFamily: "/orgs/{}/users",
              name: "orgs.users",
              decision: "confirmed",
              resourceId: 2,
              idField: "id",
              writeForm: "bare",
              entityCount: 5,
            }),
          ],
        }),
    });
    renderInRouter(<ResourcesPage id={WS} />);

    const row = await screen.findByTestId("resource-row");
    expect(row).toHaveTextContent("GET http://alex.mock.corp.internal:8080/orgs/{}/users");
    expect(within(row).getByTestId("resource-nested-hint")).toHaveTextContent(
      "идентификатор родительской записи",
    );
  });

  // P3g raises nesting past P3e's one level (D12.2): a depth-2+ family's
  // path carries more than one "{}", and the P3e-era singular hint above
  // ("идентификатор родительской записи, подставьте свой") would be a small
  // lie about it. Both Russian strings are DATA, asserted verbatim — this
  // one is not a translation of the singular, it is a separately written
  // plural, and the assertion below is what would catch either one
  // regressing to the other's wording.
  it("uses the PLURAL hint for a family two or more levels deep, verbatim", async () => {
    route({
      [WORKSPACE]: () =>
        json(
          200,
          workspaceFixture({
            id: WS,
            name: "Alex",
            specId: 3,
            url: "http://alex.mock.corp.internal:8080",
          }),
        ),
      [SUGGESTIONS]: () => json(200, { suggestions: [] }),
      [RESOURCES]: () =>
        json(200, {
          families: [
            familyFixture({
              routeFamily: "/orgs/{}/teams/{}/users",
              name: "users",
              decision: "confirmed",
              resourceId: 3,
              idField: "id",
              writeForm: "bare",
              entityCount: 5,
            }),
          ],
        }),
    });
    renderInRouter(<ResourcesPage id={WS} />);

    const row = await screen.findByTestId("resource-row");
    expect(within(row).getByTestId("resource-nested-hint")).toHaveTextContent(
      "«{}» в пути — это идентификаторы родительских записей, подставьте свои",
    );
  });

  // D12.2/P22: the list is ordered so an ancestor always precedes its
  // descendants — this falls out of GET .../resources's own
  // route_family-ASC sort (buildFamiliesView, pinned server-side by
  // TestHandler_workspaceResources_ordersAncestorBeforeDescendants), and
  // this screen renders that server order verbatim rather than re-sorting
  // by anything of its own. The fixture below deliberately answers already
  // in ancestor order with an alphabetical-by-name order that DISAGREES
  // with it ("badges" sorts before "orgs"/"teams"/"users") — the same
  // defence D13's own P22 requires of the server fixture: a screen that
  // silently re-sorted by `name` would put the leaf first, and this is the
  // one fixture shape that can catch it.
  it("renders the ancestor-before-descendant order the server sent, not alphabetical by name", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex", specId: 3 })),
      [SUGGESTIONS]: () => json(200, { suggestions: [] }),
      [RESOURCES]: () =>
        json(200, {
          families: [
            familyFixture({ routeFamily: "/orgs", name: "orgs" }),
            familyFixture({ routeFamily: "/orgs/{}/teams", name: "teams" }),
            familyFixture({ routeFamily: "/orgs/{}/teams/{}/badges", name: "badges" }),
          ],
        }),
    });
    renderInRouter(<ResourcesPage id={WS} />);

    const rows = await screen.findAllByTestId("resource-row");
    expect(rows).toHaveLength(3);
    // Each expected family string is looked up EXACTLY within the row DOM
    // order already gave it — the three fixtures are prefixes of one
    // another ("/orgs" is a substring of both descendants' strings), so an
    // exact match is what tells rows apart, and asking row[i] for
    // ancestorOrder[i] is what a re-sort by `name` would break: the leaf
    // "badges" would land in rows[0] instead of rows[2].
    const ancestorOrder = ["/orgs", "/orgs/{}/teams", "/orgs/{}/teams/{}/badges"];
    ancestorOrder.forEach((routeFamily, i) => {
      const row = rows[i];
      if (row === undefined) {
        throw new Error(`row ${i} is missing`);
      }
      expect(within(row).getByText(routeFamily, { exact: true })).toBeInTheDocument();
    });
  });

  it("says the write form was not recognised, verbatim, when writeForm is null on a confirmed row", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex", specId: 3 })),
      [SUGGESTIONS]: () => json(200, { suggestions: [] }),
      [RESOURCES]: () =>
        json(200, {
          families: [
            familyFixture({
              decision: "confirmed",
              resourceId: 1,
              idField: "id",
              writeForm: null,
              entityCount: 0,
            }),
          ],
        }),
    });
    renderInRouter(<ResourcesPage id={WS} />);

    expect(await screen.findByTestId("resource-no-write-form")).toHaveTextContent(
      "форма создания не распознана — POST идёт как раньше, из генератора",
    );
  });

  it("confirms an undecided suggestion with no slug prompt", async () => {
    const fetchMock = route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex", specId: 3 })),
      [SUGGESTIONS]: () => json(200, { suggestions: [] }),
      [RESOURCES]: () => json(200, { families: [familyFixture()] }),
      [DECIDE]: () =>
        json(200, {
          family: familyFixture({
            decision: "confirmed",
            resourceId: 1,
            idField: "id",
            writeForm: "bare",
            entityCount: 0,
          }),
        }),
    });
    renderInRouter(<ResourcesPage id={WS} />);

    await userEvent.click(await screen.findByTestId("resource-confirm"));
    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
      expect(posts).toHaveLength(1);
      expect(JSON.parse(String(posts[0]?.[1]?.body))).toEqual({
        routeFamily: "/users",
        state: "confirmed",
      });
    });
  });

  it("declines an undecided suggestion immediately, with no slug prompt", async () => {
    const fetchMock = route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex", specId: 3 })),
      [SUGGESTIONS]: () => json(200, { suggestions: [] }),
      [RESOURCES]: () => json(200, { families: [familyFixture()] }),
      [DECIDE]: () => json(200, { family: familyFixture({ decision: "declined" }) }),
    });
    renderInRouter(<ResourcesPage id={WS} />);

    await userEvent.click(await screen.findByTestId("resource-decline"));
    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
      expect(posts).toHaveLength(1);
      expect(JSON.parse(String(posts[0]?.[1]?.body))).toEqual({
        routeFamily: "/users",
        state: "declined",
      });
    });
    expect(screen.queryByTestId("resource-decline-slug")).not.toBeInTheDocument();
  });

  it("declining a CONFIRMED resource states irreversibility and asks for the workspace slug", async () => {
    const fetchMock = route({
      [WORKSPACE]: () =>
        json(200, workspaceFixture({ id: WS, name: "Alex", specId: 3, slug: "alex" })),
      [SUGGESTIONS]: () => json(200, { suggestions: [] }),
      [RESOURCES]: () =>
        json(200, {
          families: [
            familyFixture({
              decision: "confirmed",
              resourceId: 1,
              idField: "id",
              writeForm: "bare",
              entityCount: 2,
            }),
          ],
        }),
      [DECIDE]: () => json(200, { family: familyFixture({ decision: "declined" }) }),
    });
    renderInRouter(<ResourcesPage id={WS} />);

    await userEvent.click(await screen.findByTestId("resource-decline"));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("необратимо");
    // No request fired yet — the modal opening must not itself call the server.
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(0);

    await userEvent.type(screen.getByTestId("resource-decline-slug"), "alex");
    await userEvent.click(screen.getByTestId("resource-decline-submit"));

    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
      expect(posts).toHaveLength(1);
      expect(JSON.parse(String(posts[0]?.[1]?.body))).toEqual({
        routeFamily: "/users",
        state: "declined",
        confirmSlug: "alex",
      });
    });
  });

  it("names the family and shows the server's own message when a decision is refused", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex", specId: 3 })),
      [SUGGESTIONS]: () => json(200, { suggestions: [] }),
      [RESOURCES]: () => json(200, { families: [familyFixture({ name: "users" })] }),
      [DECIDE]: () =>
        json(409, { error: { code: "already_confirmed", message: "already confirmed" } }),
    });
    renderInRouter(<ResourcesPage id={WS} />);

    await userEvent.click(await screen.findByTestId("resource-confirm"));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("«users»");
    expect(alert).toHaveTextContent("already confirmed");
  });

  it("says where the rows of a confirmed family are, not that paging is a later slice", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex", specId: 3 })),
      [SUGGESTIONS]: () => json(200, { suggestions: [] }),
      [RESOURCES]: () => json(200, { families: [] }),
    });
    renderInRouter(<ResourcesPage id={WS} />);

    expect(await screen.findByText(/кнопка «Записи», по 50 на страницу/)).toBeInTheDocument();
  });

  it("shows the per-base-scope counts beside the total (A21, G6)", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, specId: 3 })),
      [SUGGESTIONS]: () => json(200, { suggestions: [] }),
      [RESOURCES]: () =>
        json(200, {
          families: [
            familyFixture({
              decision: "confirmed",
              resourceId: 1,
              idField: "id",
              writeForm: "bare",
              entityCount: 5,
              byBaseScope: [
                { baseScope: "acme", entityCount: 3 },
                { baseScope: "globex", entityCount: 2 },
              ],
            }),
          ],
        }),
    });
    renderInRouter(<ResourcesPage id={WS} />);
    expect(await screen.findByTestId("resource-entity-count")).toHaveTextContent(
      "Записей: 5 (acme: 3, globex: 2)",
    );
  });
});

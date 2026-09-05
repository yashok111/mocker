import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { WorkspacesPage } from "./WorkspacesPage";
import { renderInRouter } from "@/test/render";
import { specViewFixture, workspaceFixture } from "@/test/fixtures";

// Every route() below now stubs GET /api/specs too: WorkspacesPage added a
// second query (useListSpecs) to pick its empty-state copy, so a stub that
// only knew about /api/workspaces would leave that request unrouted — a 500
// nobody asserts on, but noise this file doesn't need. Its own content is
// asserted explicitly in the two "спеки ещё нет" tests below; everywhere
// else, an arbitrary non-empty answer keeps the generic hint branch stable.
const specsPresent = () => json(200, [specViewFixture()]);

type FetchArgs = [input: RequestInfo | URL, init?: RequestInit];

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

// route() dispatches on method+path so a test can describe the whole admin
// surface it expects, instead of a queue that silently shifts out of order the
// moment a component makes one extra call.
function route(handlers: Record<string, () => Response>) {
  const fn = vi.fn<(...args: FetchArgs) => Promise<Response>>((input, init) => {
    const key = `${(init?.method ?? "GET").toUpperCase()} ${String(input)}`;
    const handler = handlers[key];
    if (!handler) {
      return Promise.resolve(
        json(500, { error: { code: "internal", message: `unrouted ${key}` } }),
      );
    }
    return Promise.resolve(handler());
  });
  vi.stubGlobal("fetch", fn);
  return fn;
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("WorkspacesPage", () => {
  it("says the list is loading before the answer arrives", async () => {
    // A request that never settles, so the pending branch is what is on
    // screen — an unrouted stub would answer 500 and race the assertion into
    // the error branch instead.
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    renderInRouter(<WorkspacesPage />);

    // findBy, not getBy: the router mounts its matched route asynchronously,
    // so nothing at all is in the DOM on the tick render() returns.
    expect(await screen.findByRole("status")).toHaveTextContent("Загрузка…");
  });

  it("offers a retry when the list fails, in Russian", async () => {
    let calls = 0;
    route({
      "GET /api/workspaces": () => {
        calls += 1;
        return calls === 1
          ? json(500, { error: { code: "internal", message: "db is down" } })
          : json(200, []);
      },
      "GET /api/specs": specsPresent,
    });
    renderInRouter(<WorkspacesPage />);

    const alert = await screen.findByRole("alert");
    // Never the server's own English sentence.
    expect(alert).toHaveTextContent("Ошибка на сервере. Попробуйте ещё раз");
    expect(alert).not.toHaveTextContent("db is down");

    await userEvent.click(screen.getByTestId("workspaces-retry"));
    expect(await screen.findByTestId("workspaces-empty")).toBeInTheDocument();
  });

  it("explains what an empty list means instead of just being blank", async () => {
    route({ "GET /api/workspaces": () => json(200, []), "GET /api/specs": specsPresent });
    renderInRouter(<WorkspacesPage />);

    expect(await screen.findByTestId("workspaces-empty")).toHaveTextContent(
      "У вас пока нет воркспейсов",
    );
    expect(screen.getByTestId("workspaces-empty-hint")).toBeInTheDocument();
    // The create form is the only thing to do from here, so it must be there.
    expect(screen.getByTestId("workspace-create-form")).toBeInTheDocument();
  });

  it("points to /specs when there are no workspaces AND no specs in the database at all", async () => {
    route({ "GET /api/workspaces": () => json(200, []), "GET /api/specs": () => json(200, []) });
    renderInRouter(<WorkspacesPage />);

    await screen.findByTestId("workspaces-empty");
    const hint = await screen.findByTestId("workspaces-empty-hint");
    expect(hint).toHaveTextContent("Спеки ещё нет");
    expect(within(hint).getByRole("link", { name: "загрузите её" })).toHaveAttribute(
      "href",
      "/specs",
    );
  });

  it("does NOT claim there are no specs when the workspace list is merely empty", async () => {
    route({ "GET /api/workspaces": () => json(200, []), "GET /api/specs": specsPresent });
    renderInRouter(<WorkspacesPage />);

    const hint = await screen.findByTestId("workspaces-empty-hint");
    expect(hint).not.toHaveTextContent("Спеки ещё нет");
    expect(hint).toHaveTextContent("можно создать и без спеки");
  });

  it("lists the slug, revision and spec state of every workspace", async () => {
    route({
      "GET /api/workspaces": () =>
        json(200, [
          workspaceFixture({ id: 1, name: "Alex", slug: "alex", revision: 3, specId: null }),
          workspaceFixture({ id: 2, name: "Bob", slug: "bob", revision: 0, specId: 7 }),
        ]),
      "GET /api/specs": specsPresent,
    });
    renderInRouter(<WorkspacesPage />);

    const rows = await screen.findAllByTestId("workspace-row");
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent("alex · ревизия 3 · спека не привязана");
    expect(rows[1]).toHaveTextContent("bob · ревизия 0 · спека #7");
  });

  it("shows the slug the SERVER derived, not one guessed from the name", async () => {
    route({
      "GET /api/workspaces": () => json(200, []),
      "GET /api/specs": specsPresent,
      "POST /api/workspaces": () =>
        // The server resolves a collision with a deterministic suffix; a UI
        // that echoed the typed name would show "Алекс" and be wrong.
        json(201, workspaceFixture({ slug: "alex-2", name: "Алекс" })),
    });
    renderInRouter(<WorkspacesPage />);

    await screen.findByTestId("workspace-create-form");
    await userEvent.type(screen.getByTestId("workspace-create-name"), "Алекс");
    await userEvent.click(screen.getByTestId("workspace-create-submit"));

    expect(await screen.findByTestId("workspace-created-slug")).toHaveTextContent("alex-2");
  });

  it("refuses an empty name without calling the server", async () => {
    const fetchMock = route({
      "GET /api/workspaces": () => json(200, []),
      "GET /api/specs": specsPresent,
    });
    renderInRouter(<WorkspacesPage />);

    await screen.findByTestId("workspace-create-form");
    await userEvent.click(screen.getByTestId("workspace-create-submit"));

    expect(await screen.findByText("Введите имя")).toBeInTheDocument();
    const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
    expect(posts).toHaveLength(0);
  });

  it("translates a create failure rather than showing the server's message", async () => {
    route({
      "GET /api/workspaces": () => json(200, []),
      "GET /api/specs": specsPresent,
      "POST /api/workspaces": () =>
        json(409, { error: { code: "conflict", message: "slug already in use" } }),
    });
    renderInRouter(<WorkspacesPage />);

    await screen.findByTestId("workspace-create-form");
    await userEvent.type(screen.getByTestId("workspace-create-name"), "Alex");
    await userEvent.click(screen.getByTestId("workspace-create-submit"));

    expect(await screen.findByRole("alert")).toHaveTextContent("Такое значение уже используется");
  });

  it("asks before deleting, and does nothing if the confirmation is dismissed", async () => {
    const fetchMock = route({
      "GET /api/workspaces": () => json(200, [workspaceFixture({ id: 5, name: "Alex" })]),
      "GET /api/specs": specsPresent,
    });
    renderInRouter(<WorkspacesPage />);

    await userEvent.click(await screen.findByTestId("workspace-delete"));

    const dialog = await screen.findByRole("dialog");
    // The confirmation names the workspace, so a mis-click is visible before
    // it is irreversible.
    expect(dialog).toHaveTextContent("Alex");
    await userEvent.click(within(dialog).getByText("Отмена"));

    await waitFor(() => {
      const deletes = fetchMock.mock.calls.filter(([, init]) => init?.method === "DELETE");
      expect(deletes).toHaveLength(0);
    });
  });

  it("deletes on confirmation and names the workspace when the delete fails", async () => {
    const fetchMock = route({
      "GET /api/workspaces": () => json(200, [workspaceFixture({ id: 5, name: "Alex" })]),
      "GET /api/specs": specsPresent,
      "DELETE /api/workspaces/5": () =>
        json(404, { error: { code: "not_found", message: "gone" } }),
    });
    renderInRouter(<WorkspacesPage />);

    await userEvent.click(await screen.findByTestId("workspace-delete"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("workspace-delete-confirm"));

    await waitFor(() => {
      const deletes = fetchMock.mock.calls.filter(([, init]) => init?.method === "DELETE");
      expect(deletes).toHaveLength(1);
    });
    // The mutation itself carries no memory of which row it was deleting, so
    // a bare error would leave the reader guessing.
    expect(await screen.findByRole("alert")).toHaveTextContent("Не удалось удалить «Alex»");
  });

  // P4b's import half (2026-09-05): the chosen file's JSON goes on the wire
  // as `bundle`, the three overrides are omitted when blank, and the page
  // goes to the workspace the server created.
  it("imports a bundle file through POST /api/workspaces/import and goes to the new workspace", async () => {
    const doc = { mockerBundle: 6, workspace: { name: "Alex" } };
    const fetchMock = route({
      "GET /api/workspaces": () => json(200, []),
      "GET /api/specs": specsPresent,
      "POST /api/workspaces/import": () =>
        json(201, {
          workspace: workspaceFixture({ id: 9, slug: "alex" }),
          specId: null,
          specCreated: false,
          entitiesRestored: 0,
        }),
    });
    renderInRouter(<WorkspacesPage />);

    await userEvent.click(await screen.findByTestId("workspace-import"));
    const form = await screen.findByTestId("workspace-import-form");
    await userEvent.click(within(form).getByTestId("workspace-import-submit"));
    expect(within(form).getByText("Выберите файл экспорта")).toBeInTheDocument();

    const file = new File([JSON.stringify(doc)], "mocker-alex.json", { type: "application/json" });
    await userEvent.upload(within(form).getByTestId("workspace-import-file"), file);
    await userEvent.type(within(form).getByTestId("workspace-import-slug"), "alex-copy");
    await userEvent.click(within(form).getByTestId("workspace-import-submit"));

    await waitFor(() => {
      const post = fetchMock.mock.calls.find(([, init]) => init?.method === "POST");
      expect(post).toBeDefined();
      expect(JSON.parse(String(post?.[1]?.body))).toEqual({ bundle: doc, slug: "alex-copy" });
    });
    expect(await screen.findByTestId("test-router-elsewhere")).toBeInTheDocument();
  });

  it("refuses a file that is not a JSON object before any request, and names a server refusal", async () => {
    const fetchMock = route({
      "GET /api/workspaces": () => json(200, []),
      "GET /api/specs": specsPresent,
      "POST /api/workspaces/import": () =>
        json(400, {
          error: { code: "bad_request", message: "mockerBundle 3: this build reads 5..6" },
        }),
    });
    renderInRouter(<WorkspacesPage />);
    await userEvent.click(await screen.findByTestId("workspace-import"));
    const form = await screen.findByTestId("workspace-import-form");

    await userEvent.upload(
      within(form).getByTestId("workspace-import-file"),
      new File(["[1, 2]"], "list.json", { type: "application/json" }),
    );
    await userEvent.click(within(form).getByTestId("workspace-import-submit"));
    expect(await within(form).findByText(/должен быть JSON-объект/)).toBeInTheDocument();
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(0);

    await userEvent.upload(
      within(form).getByTestId("workspace-import-file"),
      new File(['{"mockerBundle": 3}'], "old.json", { type: "application/json" }),
    );
    await userEvent.click(within(form).getByTestId("workspace-import-submit"));
    expect(await within(form).findByRole("alert")).toHaveTextContent("this build reads 5..6");
  });

  it("renders the error state, not an empty list, when the list answers a status the contract does not declare (A21, B9)", async () => {
    route({ "GET /api/workspaces": () => json(201, []), "GET /api/specs": specsPresent });
    renderInRouter(<WorkspacesPage />);
    expect(await screen.findByTestId("workspaces-error")).toBeInTheDocument();
    expect(screen.queryByTestId("workspaces-empty")).toBeNull();
  });

  // A21 (G3): slug and spec on the create card; the created line opens the
  // workspace; the specs screen's ?specId= preselects.
  it("creates with the typed slug and the chosen spec, omitting both when blank, and links to the result", async () => {
    const fetchMock = route({
      "GET /api/workspaces": () => json(200, []),
      "GET /api/specs": () =>
        json(200, [specViewFixture({ id: 4, name: "Petstore", version: "2" })]),
      "POST /api/workspaces": () => json(201, workspaceFixture({ id: 12, slug: "shop" })),
    });
    renderInRouter(<WorkspacesPage initialSpecId={4} />);
    await screen.findByTestId("workspace-create-form");
    await waitFor(() => expect(screen.getByTestId("workspace-create-spec")).toHaveValue("4"));
    await userEvent.type(screen.getByTestId("workspace-create-name"), "Shop");
    await userEvent.type(screen.getByTestId("workspace-create-slug"), "shop");
    await userEvent.click(screen.getByTestId("workspace-create-submit"));
    await waitFor(() => {
      const post = fetchMock.mock.calls.find(([, init]) => init?.method === "POST");
      expect(JSON.parse(String(post?.[1]?.body))).toEqual({
        name: "Shop",
        slug: "shop",
        specId: 4,
      });
    });
    expect(await screen.findByTestId("workspace-created-open")).toHaveAttribute(
      "href",
      "/workspaces/12",
    );
  });

  // A21 (G14): every workspace, not only the caller's, with its owner.
  it("lists other people's workspaces with their owner on «показать чужие»", async () => {
    const fetchMock = route({
      "GET /api/workspaces": () => json(200, [workspaceFixture({ id: 1, ownerId: 1 })]),
      "GET /api/workspaces?all=1": () =>
        json(200, [
          workspaceFixture({ id: 1, ownerId: 1 }),
          workspaceFixture({ id: 2, name: "Bob", slug: "bob", ownerId: 5, forkedFrom: 1 }),
        ]),
      "GET /api/specs": specsPresent,
    });
    renderInRouter(<WorkspacesPage />);
    expect(await screen.findAllByTestId("workspace-row")).toHaveLength(1);
    await userEvent.click(screen.getByTestId("workspaces-show-all"));
    await waitFor(() => expect(screen.getAllByTestId("workspace-row")).toHaveLength(2));
    expect(fetchMock.mock.calls.map(([u]) => String(u))).toContain("/api/workspaces?all=1");
    const bob = screen.getAllByTestId("workspace-row").find((r) => r.textContent?.includes("bob"));
    expect(bob).toHaveTextContent("копия воркспейса #1");
    expect(bob).toHaveTextContent("владелец #5");
  });
});

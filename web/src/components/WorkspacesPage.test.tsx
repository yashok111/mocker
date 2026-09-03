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
});

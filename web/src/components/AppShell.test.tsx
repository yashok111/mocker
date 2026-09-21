import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  Link,
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import { AppShell, WorkspaceSwitcher } from "./AppShell";
import { WorkspaceLayout } from "./WorkspaceLayout";
import { renderInRouter, renderWithProviders } from "@/test/render";
import { userFixture, workspaceFixture } from "@/test/fixtures";
import { json, route } from "@/test/http";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// A20: the header's server status over the two probes. The three words
// and the one trap — after a failed refetch TanStack Query keeps the last
// good answer in `data`, so the word must come from the error state, not
// from stale data.
describe("AppShell server status", () => {
  it("places all workspace sections in the shared navigation rail", async () => {
    route({
      "GET /readyz": () => json(200, { ok: true }),
      "GET /healthz": () => json(200, { ok: true }),
      "GET /api/workspaces/7": () => json(200, workspaceFixture({ id: 7 })),
    });
    renderInRouter(
      <AppShell user={userFixture()}>
        <WorkspaceLayout id={7}>Workspace content</WorkspaceLayout>
      </AppShell>,
    );
    const navigation = await screen.findByRole("navigation", { name: "Основная навигация" });
    await waitFor(() => expect(within(navigation).getAllByRole("tab")).toHaveLength(10));
    expect(within(navigation).getByTestId("nav-workspaces")).toBeInTheDocument();
    expect(within(screen.getByRole("main")).queryByRole("tablist")).toBeNull();
    const activeTab = within(navigation).getByRole("tab", { name: "Обзор" });
    const panel = screen.getByRole("tabpanel");
    expect(activeTab).toHaveAttribute("aria-controls", panel.id);
    expect(panel).toHaveAttribute("aria-labelledby", activeTab.id);
  });

  it("opens mobile navigation in a dialog and closes it with Escape", async () => {
    const originalMatchMedia = window.matchMedia.bind(window);
    vi.spyOn(window, "matchMedia").mockImplementation((query) => {
      const result = originalMatchMedia(query);
      if (query === "(max-width: 48em)") {
        Object.defineProperty(result, "matches", { value: true });
      }
      return result;
    });
    route({
      "GET /readyz": () => json(200, { ok: true }),
      "GET /healthz": () => json(200, { ok: true }),
      "GET /api/workspaces/7": () => json(200, workspaceFixture({ id: 7 })),
    });
    renderInRouter(
      <AppShell user={userFixture()}>
        <WorkspaceLayout id={7}>Workspace content</WorkspaceLayout>
      </AppShell>,
    );
    const toggle = await screen.findByRole("button", { name: "Открыть навигацию" });
    await userEvent.click(toggle);
    const dialog = await screen.findByRole("dialog", { name: "Навигация" });
    await waitFor(() => expect(within(dialog).getAllByRole("tab")).toHaveLength(10));
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    await waitFor(() => expect(toggle).toHaveFocus());
  });

  it("offers desktop designer navigation in a drawer and returns focus after closing", async () => {
    renderDesignerShell("/designs/12");

    const toggle = await screen.findByRole("button", { name: "Открыть навигацию" });
    expect(screen.queryByRole("navigation", { name: "Основная навигация" })).toBeNull();
    const mainContent = screen.getByText("Designer content").parentElement;
    expect(mainContent).toHaveAttribute("data-designer");
    expect(mainContent).not.toHaveAttribute("data-canvas");

    await userEvent.click(toggle);
    const dialog = await screen.findByRole("dialog", { name: "Навигация" });
    expect(
      within(dialog).getByRole("navigation", { name: "Основная навигация" }),
    ).toBeInTheDocument();
    expect(within(dialog).getByTestId("nav-designs")).toHaveAttribute("aria-current", "page");
    await userEvent.keyboard("{Escape}");

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    await waitFor(() => expect(toggle).toHaveFocus());
  });

  it("restores the desktop rail when leaving a designer for the project list", async () => {
    renderDesignerShell("/designs");

    const openDesign = await screen.findByRole("link", { name: "Открыть проект" });
    expect(screen.getByRole("navigation", { name: "Основная навигация" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Открыть навигацию" })).toBeNull();
    expect(openDesign.parentElement).not.toHaveAttribute("data-designer");

    await userEvent.click(openDesign);
    const toggle = await screen.findByRole("button", { name: "Открыть навигацию" });
    expect(screen.queryByRole("navigation", { name: "Основная навигация" })).toBeNull();
    await userEvent.click(toggle);
    const dialog = await screen.findByRole("dialog", { name: "Навигация" });
    await userEvent.click(within(dialog).getByTestId("nav-designs"));

    expect(await screen.findByRole("link", { name: "Открыть проект" })).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByRole("button", { name: "Открыть навигацию" })).toBeNull();
    expect(screen.getAllByRole("navigation", { name: "Основная навигация" })).toHaveLength(1);
    expect(screen.getByRole("link", { name: "Открыть проект" }).parentElement).not.toHaveAttribute(
      "data-designer",
    );
  });

  it("gives the sequence canvas a full-width workspace and active drawer navigation", async () => {
    renderDesignerShell("/design-scenarios/12");
    const toggle = await screen.findByRole("button", { name: "Открыть навигацию" });
    const mainContent = screen.getByText("Canvas content").parentElement;
    expect(mainContent).toHaveAttribute("data-designer");
    expect(mainContent).toHaveAttribute("data-canvas");
    expect(screen.queryByRole("navigation", { name: "Основная навигация" })).toBeNull();
    await userEvent.click(toggle);
    const dialog = await screen.findByRole("dialog", { name: "Навигация" });
    expect(within(dialog).getByTestId("nav-design-scenarios")).toHaveAttribute(
      "aria-current",
      "page",
    );
  });

  it("says «готов» when /readyz answers ok", async () => {
    route({
      "GET /readyz": () => json(200, { ok: true }),
      "GET /healthz": () => json(200, { ok: true }),
    });
    renderInRouter(<AppShell user={userFixture()}>x</AppShell>);
    await waitFor(() =>
      expect(screen.getByTestId("server-status")).toHaveTextContent("сервер: готов"),
    );
  });

  it("names the database when /readyz is a 503 and /healthz still answers", async () => {
    route({
      "GET /readyz": () =>
        json(503, { error: { code: "internal", message: "database not ready" } }),
      "GET /healthz": () => json(200, { ok: true }),
    });
    renderInRouter(<AppShell user={userFixture()}>x</AppShell>);
    await waitFor(() =>
      expect(screen.getByTestId("server-status")).toHaveTextContent(
        "сервер: жив, база данных не готова",
      ),
    );
  });

  it("says «недоступен» on any other failure, including after a good answer went stale", async () => {
    let calls = 0;
    route({
      "GET /readyz": () => {
        calls += 1;
        return calls === 1
          ? json(200, { ok: true })
          : json(502, { error: { code: "internal", message: "bad gateway" } });
      },
      "GET /healthz": () => json(502, { error: { code: "internal", message: "bad gateway" } }),
    });
    const { queryClient } = renderInRouter(<AppShell user={userFixture()}>x</AppShell>);
    await waitFor(() =>
      expect(screen.getByTestId("server-status")).toHaveTextContent("сервер: готов"),
    );
    await queryClient.invalidateQueries();
    await waitFor(() =>
      expect(screen.getByTestId("server-status")).toHaveTextContent("сервер: недоступен"),
    );
  });

  describe("AppShell navigation (A21, U2)", () => {
    it("has a «Воркспейсы» item and no switcher outside a workspace route", async () => {
      route({
        "GET /readyz": () => json(200, { ok: true }),
        "GET /healthz": () => json(200, { ok: true }),
      });
      renderInRouter(<AppShell user={userFixture()}>x</AppShell>);
      expect(await screen.findByTestId("nav-workspaces")).toBeInTheDocument();
      expect(screen.getByTestId("nav-designs")).toHaveTextContent("Проектирование API");
      expect(screen.queryByTestId("workspace-switcher")).toBeNull();
    });

    it("offers a switcher on a workspace route, current workspace selected, and navigates on change", async () => {
      route({
        "GET /readyz": () => json(200, { ok: true }),
        "GET /healthz": () => json(200, { ok: true }),
        "GET /api/workspaces": () =>
          json(200, [
            workspaceFixture({ id: 7, name: "Alex" }),
            workspaceFixture({ id: 8, name: "Bob" }),
          ]),
      });
      renderInRouter(<WorkspaceSwitcher pathname="/workspaces/7/traffic" />);
      const switcher = await screen.findByTestId("workspace-switcher");
      expect(switcher).toHaveValue("7");
      await userEvent.selectOptions(switcher, "8");
      // The memory router's catch-all renders for /workspaces/8.
      expect(await screen.findByTestId("test-router-elsewhere")).toBeInTheDocument();
    });
  });
});

function renderDesignerShell(path: string): void {
  route({
    "GET /readyz": () => json(200, { ok: true }),
    "GET /healthz": () => json(200, { ok: true }),
  });
  const rootRoute = createRootRoute({
    component: () => (
      <AppShell user={userFixture()}>
        <Outlet />
      </AppShell>
    ),
  });
  const listRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/designs",
    component: () => (
      <Link to="/designs/$id" params={{ id: 12 }}>
        Открыть проект
      </Link>
    ),
  });
  const detailRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/designs/$id",
    component: () => <div>Designer content</div>,
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([
      listRoute,
      detailRoute,
      createRoute({
        getParentRoute: () => rootRoute,
        path: "/design-canvas",
        component: () => <div>Canvas content</div>,
      }),
      createRoute({
        getParentRoute: () => rootRoute,
        path: "/design-scenarios/$id",
        component: () => <div>Canvas content</div>,
      }),
    ]),
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  renderWithProviders(<RouterProvider router={router as never} />);
}

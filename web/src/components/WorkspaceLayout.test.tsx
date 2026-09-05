import { afterEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { WorkspaceLayout } from "./WorkspaceLayout";
import { renderInRouter } from "@/test/render";
import { workspaceFixture } from "@/test/fixtures";
import { json, route } from "@/test/http";

// This file carries the coverage WorkspacePage.test.tsx used to hold for the
// workspace identity fetch (name/slug/revision, the four states, the retry
// button) — that fetch now lives in WorkspaceLayout rather than in a single
// route-owning page component.

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("WorkspaceLayout", () => {
  it("shows the workspace's own identity once it loads, and renders children", async () => {
    route({
      "GET /api/workspaces/7": () =>
        json(200, workspaceFixture({ id: 7, name: "Alex", slug: "alex", revision: 4 })),
    });
    renderInRouter(
      <WorkspaceLayout id={7}>
        <div data-testid="child-marker" />
      </WorkspaceLayout>,
    );

    expect(await screen.findByTestId("workspace-detail-name")).toHaveTextContent("Alex");
    expect(screen.getByTestId("workspace-detail-meta")).toHaveTextContent("alex · ревизия 4");
    expect(screen.getByTestId("child-marker")).toBeInTheDocument();
  });

  it("translates a 404 and offers a retry, without rendering children", async () => {
    let calls = 0;
    route({
      "GET /api/workspaces/7": () => {
        calls += 1;
        return calls === 1
          ? json(404, { error: { code: "not_found", message: "workspace not found" } })
          : json(200, workspaceFixture({ id: 7, name: "Alex" }));
      },
    });
    renderInRouter(
      <WorkspaceLayout id={7}>
        <div data-testid="child-marker" />
      </WorkspaceLayout>,
    );

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Не найдено");
    expect(alert).not.toHaveTextContent("workspace not found");
    // One failed fetch, one alert — not the alert AND the outlet.
    expect(screen.queryByTestId("child-marker")).not.toBeInTheDocument();

    await userEvent.click(screen.getByTestId("workspace-layout-retry"));
    expect(await screen.findByTestId("workspace-detail-name")).toHaveTextContent("Alex");
    expect(screen.getByTestId("child-marker")).toBeInTheDocument();
  });

  it("renders its own outer marker in every state, pending included", async () => {
    // A request that never settles, so the pending branch is what is on
    // screen — an unrouted stub would answer 500 and race the assertion into
    // the error branch instead. findBy, not getBy: renderInRouter's own
    // memory router still resolves its match asynchronously even though the
    // fetch it triggers never will.
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    renderInRouter(
      <WorkspaceLayout id={7}>
        <div data-testid="child-marker" />
      </WorkspaceLayout>,
    );

    expect(await screen.findByTestId("workspace-layout")).toBeInTheDocument();
  });

  it("offers navigation to all seven children, defaulting to the overview tab", async () => {
    // P2c adds the sixth (История) alongside P2b's fifth (Сценарии), and P3a
    // adds the seventh (Ресурсы) — this test asserted "all four" through
    // both earlier additions without noticing, because it never counted the
    // tabs it found, only asserted the ones it happened to name: naming six
    // out of seven still passes when a seventh lands unnoticed. The
    // toHaveLength(7) below is what actually catches an eighth tab landing
    // the same way — the individual name assertions after it only prove
    // WHICH seven, not that there are no more.
    route({
      "GET /api/workspaces/7": () => json(200, workspaceFixture({ id: 7, name: "Alex" })),
    });
    renderInRouter(
      <WorkspaceLayout id={7}>
        <div data-testid="child-marker" />
      </WorkspaceLayout>,
    );

    await screen.findByTestId("workspace-detail-name");
    // Mounted at "/" here — renderInRouter's own memory route is always "/",
    // so this exercises the default branch of the pathname switch rather
    // than a real nested /workspaces/7/operations location. The pathname
    // ⇒ active-tab mapping itself is exercised end to end, against the REAL
    // route tree, in routes.test.tsx.
    expect(screen.getAllByRole("tab")).toHaveLength(10);
    expect(screen.getByRole("tab", { name: "Обзор" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tab", { name: "Операции спеки" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Свои эндпоинты" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Трафик" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Сценарии" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "История" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Ресурсы" })).toBeInTheDocument();
    // P6e adds the eighth (Соединения).
    expect(screen.getByRole("tab", { name: "Соединения" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Файлы" })).toBeInTheDocument();
    // P7b adds the tenth (Контракт).
    expect(screen.getByRole("tab", { name: "Контракт" })).toBeInTheDocument();
  });

  it("offers the list beside «Повторить» on a failed workspace (A21, U10)", async () => {
    route({
      "GET /api/workspaces/7": () =>
        json(404, { error: { code: "not_found", message: "workspace not found" } }),
    });
    renderInRouter(
      <WorkspaceLayout id={7}>
        <div />
      </WorkspaceLayout>,
    );
    // The button is beside «Повторить»; where it goes ("/") is the memory
    // router's own index in this harness, so the click is not observable
    // here — the AppShell test covers the same navigate.
    await screen.findByTestId("workspace-layout-retry");
    expect(screen.getByTestId("workspace-to-list")).toHaveTextContent("К списку воркспейсов");
  });
});

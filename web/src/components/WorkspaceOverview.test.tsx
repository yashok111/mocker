import { afterEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { WorkspaceOverview } from "./WorkspaceOverview";
import { renderInRouter } from "@/test/render";
import { serverConfigFixture, workspaceFixture } from "@/test/fixtures";
import { json, route } from "@/test/http";

// This file carries the coverage WorkspacePage.test.tsx used to hold for the
// connect-panel mounting behaviour: it now lives on the index child of
// /workspaces/$id rather than on the route that also owned the identity
// header (that part moved to WorkspaceLayout.test.tsx).

const config = serverConfigFixture();

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("WorkspaceOverview", () => {
  it("renders its own outer marker in every state, pending included", async () => {
    // findBy, not getBy: renderInRouter's own memory router still resolves
    // its match asynchronously even though the fetch it triggers never will.
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    renderInRouter(<WorkspaceOverview id={7} config={config} />);

    expect(await screen.findByTestId("overview-page")).toBeInTheDocument();
  });

  it("translates a 404 and offers a retry", async () => {
    let calls = 0;
    route({
      "GET /api/workspaces/7": () => {
        calls += 1;
        return calls === 1
          ? json(404, { error: { code: "not_found", message: "workspace not found" } })
          : json(200, workspaceFixture({ id: 7, name: "Alex" }));
      },
      "GET /api/workspaces/7/traffic": () => json(200, { rows: [], rate1m: 0, dropped: 0 }),
    });
    renderInRouter(<WorkspaceOverview id={7} config={config} />);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Не найдено");
    expect(alert).not.toHaveTextContent("workspace not found");

    await userEvent.click(screen.getByTestId("overview-retry"));
    expect(await screen.findByTestId("connect-panel")).toBeInTheDocument();
  });

  it("mounts the connect panel, the auth preset panel and the settings panel", async () => {
    route({
      "GET /api/workspaces/7": () => json(200, workspaceFixture({ id: 7 })),
      "GET /api/workspaces/7/traffic": () => json(200, { rows: [], rate1m: 0, dropped: 0 }),
    });
    renderInRouter(<WorkspaceOverview id={7} config={config} />);

    expect(await screen.findByTestId("connect-panel")).toBeInTheDocument();
    expect(screen.getByTestId("auth-preset-panel")).toBeInTheDocument();
    expect(screen.getByTestId("settings-panel")).toBeInTheDocument();
  });

  it("keeps a failing traffic poll inside the connect panel", async () => {
    route({
      "GET /api/workspaces/7": () => json(200, workspaceFixture({ id: 7, name: "Alex" })),
      // The workspace can be deleted out from under an open panel; the
      // background poll then fails while the page itself is perfectly fine.
      "GET /api/workspaces/7/traffic": () =>
        json(500, { error: { code: "internal", message: "boom" } }),
    });
    renderInRouter(<WorkspaceOverview id={7} config={config} />);

    expect(await screen.findByTestId("connect-panel")).toBeInTheDocument();
    expect(await screen.findByText("Не удалось получить данные о трафике")).toBeInTheDocument();
    // The page did NOT collapse into its own error branch.
    expect(screen.queryByTestId("overview-error")).not.toBeInTheDocument();
  });
});

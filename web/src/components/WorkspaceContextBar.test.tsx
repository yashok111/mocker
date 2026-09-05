import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { WorkspaceContextBar } from "./WorkspaceContextBar";
import { renderInRouter } from "@/test/render";
import { json, route } from "@/test/http";
import { directiveFixture, specViewFixture, workspaceFixture } from "@/test/fixtures";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// A21 (U1): the four pieces of live state under the workspace's name. Each
// is its own query and degrades on its own — the bar is never a fifth
// state of the layout.
describe("WorkspaceContextBar", () => {
  it("names the spec, the active scenario and the directives in force, each linking to its tab", async () => {
    route({
      "GET /api/specs": () =>
        json(200, [specViewFixture({ id: 3, name: "Petstore", version: "2.1" })]),
      "GET /api/workspaces/7/scenarios": () =>
        json(200, {
          scenarios: [
            { id: 11, name: "Чёрная пятница", createdAt: 0, isActive: true, editVersion: 1 },
          ],
        }),
      "GET /api/workspaces/7/session": () =>
        json(200, { directives: [directiveFixture(), directiveFixture()] }),
    });
    renderInRouter(
      <WorkspaceContextBar
        workspace={workspaceFixture({
          id: 7,
          slug: "alex",
          revision: 4,
          specId: 3,
          scenarioId: 11,
        })}
      />,
    );
    expect(await screen.findByTestId("workspace-detail-meta")).toHaveTextContent(
      "alex · ревизия 4",
    );
    await waitFor(() =>
      expect(screen.getByTestId("workspace-context-spec")).toHaveTextContent(
        "спека: Petstore (v2.1)",
      ),
    );
    await waitFor(() =>
      expect(screen.getByTestId("workspace-context-scenario")).toHaveTextContent(
        "сценарий: Чёрная пятница",
      ),
    );
    await waitFor(() =>
      expect(screen.getByTestId("workspace-context-directives")).toHaveTextContent(
        "директив сессии: 2",
      ),
    );
    expect(screen.getByTestId("workspace-context-scenario").querySelector("a")).toHaveAttribute(
      "href",
      "/workspaces/7/scenarios",
    );
  });

  it("falls back to the ids when the lists fail, and offers to bind a spec when there is none", async () => {
    route({});
    renderInRouter(
      <WorkspaceContextBar workspace={workspaceFixture({ id: 7, specId: 3, scenarioId: 11 })} />,
    );
    await waitFor(() =>
      expect(screen.getByTestId("workspace-context-spec")).toHaveTextContent("спека: #3"),
    );
    expect(screen.getByTestId("workspace-context-scenario")).toHaveTextContent("сценарий: #11");
    expect(screen.queryByTestId("workspace-context-directives")).toBeNull();
  });

  it("says the spec is not bound and links to the overview", async () => {
    route({ "GET /api/workspaces/7/session": () => json(200, { directives: [] }) });
    renderInRouter(<WorkspaceContextBar workspace={workspaceFixture({ id: 7, specId: null })} />);
    expect(await screen.findByTestId("workspace-context-bind-spec")).toHaveAttribute(
      "href",
      "/workspaces/7",
    );
    expect(screen.queryByTestId("workspace-context-scenario")).toBeNull();
  });
});

import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DriftPanel } from "./DriftPanel";
import { renderWithProviders } from "@/test/render";
import { json, route } from "@/test/http";
import type { DriftReportView } from "@/api/generated/schemas";

const WS = 7;
const DRIFT = `GET /api/workspaces/${WS}/drift`;

const clean: DriftReportView = {
  hasDrift: false,
  orphanedOverrides: [],
  orphanedResources: [],
  shadowedEndpoints: [],
};

const dirty: DriftReportView = {
  hasDrift: true,
  orphanedOverrides: [{ method: "GET", path: "/old", opKey: "GET%20%2Fold" }],
  orphanedResources: [{ routeFamily: "/gone", name: "gone", resourceId: 3, entityCount: 4 }],
  shadowedEndpoints: [
    { endpointId: 9, method: "POST", path: "/users", canonicalPath: "/users", precededSpec: true },
  ],
};

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("DriftPanel", () => {
  it("fetches nothing until the button, then says the workspace is clean", async () => {
    const fetchMock = route({ [DRIFT]: () => json(200, clean) });
    renderWithProviders(<DriftPanel id={WS} />);
    expect(fetchMock).not.toHaveBeenCalled();
    await userEvent.click(screen.getByTestId("drift-check"));
    expect(await screen.findByTestId("drift-clean")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("lists the three kinds of drift and repairs each with its own verb, opKey verbatim", async () => {
    let reports = 0;
    const fetchMock = route({
      [DRIFT]: () => {
        reports += 1;
        return json(200, reports === 1 ? dirty : clean);
      },
      [`DELETE /api/workspaces/${WS}/operations/GET%20%2Fold`]: () =>
        new Response(null, { status: 204 }),
      [`DELETE /api/workspaces/${WS}/endpoints/9`]: () => new Response(null, { status: 204 }),
      [`POST /api/workspaces/${WS}/resource-decisions`]: () =>
        json(200, { routeFamily: "/gone", state: "declined" }),
    });
    renderWithProviders(<DriftPanel id={WS} />);
    await userEvent.click(screen.getByTestId("drift-check"));
    await screen.findByTestId("drift-report");
    expect(screen.getByTestId("drift-override")).toHaveTextContent("GET /old");
    expect(screen.getByTestId("drift-resource")).toHaveTextContent("записей: 4");
    expect(screen.getByTestId("drift-endpoint-preceded")).toBeInTheDocument();

    await userEvent.click(screen.getByTestId("drift-override-delete"));
    // Nothing fires until the confirm — a drift report is a list.
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "DELETE")).toHaveLength(0);
    await userEvent.click(await screen.findByTestId("drift-override-delete-confirm"));
    await waitFor(() => {
      const del = fetchMock.mock.calls.find(([, init]) => init?.method === "DELETE");
      expect(String(del?.[0])).toBe(`/api/workspaces/${WS}/operations/GET%20%2Fold`);
    });
    // The repair refetches the report; the second answer is clean.
    expect(await screen.findByTestId("drift-clean")).toBeInTheDocument();
  });

  it("declines an orphaned resource through the slug modal", async () => {
    const fetchMock = route({
      [DRIFT]: () => json(200, dirty),
      [`POST /api/workspaces/${WS}/resource-decisions`]: () =>
        json(200, { routeFamily: "/gone", state: "declined" }),
    });
    renderWithProviders(<DriftPanel id={WS} />);
    await userEvent.click(screen.getByTestId("drift-check"));
    await userEvent.click(await screen.findByTestId("drift-resource-decline"));
    await userEvent.type(await screen.findByTestId("resource-decline-slug"), "alex");
    await userEvent.click(screen.getByTestId("resource-decline-submit"));
    await waitFor(() => {
      const post = fetchMock.mock.calls.find(([, init]) => init?.method === "POST");
      expect(JSON.parse(String(post?.[1]?.body))).toEqual({
        routeFamily: "/gone",
        state: "declined",
        confirmSlug: "alex",
      });
    });
  });

  it("names the row when a repair is refused", async () => {
    route({
      [DRIFT]: () => json(200, dirty),
      [`DELETE /api/workspaces/${WS}/endpoints/9`]: () =>
        json(409, { error: { code: "conflict", message: "busy" } }),
    });
    renderWithProviders(<DriftPanel id={WS} />);
    await userEvent.click(screen.getByTestId("drift-check"));
    await userEvent.click(await screen.findByTestId("drift-endpoint-delete"));
    await userEvent.click(await screen.findByTestId("drift-endpoint-delete-confirm"));
    expect(await screen.findByRole("alert")).toHaveTextContent("«POST /users»");
  });
});

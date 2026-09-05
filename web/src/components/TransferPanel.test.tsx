import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TransferPanel } from "./TransferPanel";
import { renderInRouter } from "@/test/render";
import { workspaceFixture } from "@/test/fixtures";
import { json, route } from "@/test/http";

const ws = workspaceFixture({ id: 7, slug: "alex", name: "Alex" });

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// jsdom has no URL.createObjectURL; the stub returns the Blob it was handed
// through a side channel so the test can read the bytes the browser would
// have saved. HTMLAnchorElement.click on a download link is a no-op there.
function stubDownload(): { blobs: Blob[] } {
  const blobs: Blob[] = [];
  vi.stubGlobal("URL", {
    ...URL,
    createObjectURL: (b: Blob) => {
      blobs.push(b);
      return "blob:mock";
    },
    revokeObjectURL: () => {},
  });
  return { blobs };
}

describe("TransferPanel", () => {
  it("downloads the export as mocker-<slug>.json, with the two flags only when ticked", async () => {
    const doc = { mockerBundle: 6, workspace: { name: "Alex" } };
    const fetchMock = route({
      [`GET /api/workspaces/7/export`]: () => json(200, doc),
      [`GET /api/workspaces/7/export?includeData=true&includeSpec=true`]: () =>
        json(200, { ...doc, data: {} }),
    });
    const { blobs } = stubDownload();
    renderInRouter(<TransferPanel workspace={ws} />);

    await userEvent.click(await screen.findByTestId("transfer-export"));
    await waitFor(() => expect(blobs).toHaveLength(1));
    expect(JSON.parse(await blobs[0]!.text())).toEqual(doc);
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe("/api/workspaces/7/export");

    await userEvent.click(screen.getByTestId("transfer-export-data"));
    await userEvent.click(screen.getByTestId("transfer-export-spec"));
    await userEvent.click(screen.getByTestId("transfer-export"));
    await waitFor(() => expect(blobs).toHaveLength(2));
    expect(String(fetchMock.mock.calls[1]?.[0])).toBe(
      "/api/workspaces/7/export?includeData=true&includeSpec=true",
    );
  });

  it("shows the server's sentence when the export is refused, and saves nothing", async () => {
    route({
      [`GET /api/workspaces/7/export`]: () =>
        json(404, { error: { code: "not_found", message: "workspace not found" } }),
    });
    const { blobs } = stubDownload();
    renderInRouter(<TransferPanel workspace={ws} />);
    await userEvent.click(await screen.findByTestId("transfer-export"));
    expect(await screen.findByTestId("transfer-export-error")).toHaveTextContent(
      "workspace not found",
    );
    expect(blobs).toHaveLength(0);
  });

  it("forks through POST .../fork with the omitted fields omitted and includeData explicit, then goes to the copy", async () => {
    const fetchMock = route({
      [`POST /api/workspaces/7/fork`]: () =>
        json(201, workspaceFixture({ id: 8, slug: "alex-2", name: "Alex (копия)" })),
    });
    renderInRouter(<TransferPanel workspace={ws} />);

    await userEvent.click(await screen.findByTestId("transfer-fork"));
    await screen.findByTestId("workspace-fork-form");
    // Checked by default (the server's own default is true); unticking it
    // must travel as an explicit false, since an omitted flag copies rows.
    expect(screen.getByTestId("workspace-fork-data")).toBeChecked();
    await userEvent.click(screen.getByTestId("workspace-fork-data"));
    await userEvent.click(screen.getByTestId("workspace-fork-submit"));

    await waitFor(() => {
      const post = fetchMock.mock.calls.find(([, init]) => init?.method === "POST");
      expect(post).toBeDefined();
      expect(JSON.parse(String(post?.[1]?.body))).toEqual({ includeData: false });
    });
    // The memory router's catch-all renders for /workspaces/8.
    expect(await screen.findByTestId("test-router-elsewhere")).toBeInTheDocument();
  });

  it("names the refusal inside the fork modal", async () => {
    route({
      [`POST /api/workspaces/7/fork`]: () =>
        json(409, { error: { code: "conflict", message: "slug already taken" } }),
    });
    renderInRouter(<TransferPanel workspace={ws} />);
    await userEvent.click(await screen.findByTestId("transfer-fork"));
    await userEvent.type(await screen.findByTestId("workspace-fork-slug"), "taken");
    await userEvent.click(screen.getByTestId("workspace-fork-submit"));
    expect(await screen.findByRole("alert")).toHaveTextContent("slug already taken");
  });
});

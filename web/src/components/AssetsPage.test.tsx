import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AssetsPage, suggestName } from "./AssetsPage";
import { renderWithProviders } from "@/test/render";
import { json, route } from "@/test/http";
import type { AssetView } from "@/api/generated/schemas";

const WS = 7;
const LIST = `GET /api/workspaces/${WS}/assets`;

function asset(overrides: Partial<AssetView> = {}): AssetView {
  return {
    name: "photo.jpg",
    mediaType: "image/jpeg",
    sizeBytes: 2048,
    sha256: "abc",
    createdAt: 1700000000,
    updatedAt: 1700000000,
    url: "http://alex.mock.local/__mocker/assets/photo.jpg",
    ...overrides,
  };
}

function listView(assets: AssetView[]) {
  return {
    assets,
    totalBytes: 2048,
    maxAssetBytes: 8 * 1024 * 1024,
    maxTotalBytes: 64 * 1024 * 1024,
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("suggestName", () => {
  it("keeps a legal name and repairs an illegal one the way the server's alphabet demands", () => {
    expect(suggestName("photo.jpg")).toBe("photo.jpg");
    expect(suggestName("мой файл (1).PNG")).toBe("1-.PNG");
    expect(suggestName("..")).toBe("file");
  });
});

describe("AssetsPage", () => {
  it("renders its marker in every state and the empty state with the caps", async () => {
    route({ [LIST]: () => json(200, listView([])) });
    renderWithProviders(<AssetsPage id={WS} />);
    expect(screen.getByTestId("assets-page")).toBeInTheDocument();
    expect(await screen.findByTestId("assets-empty")).toBeInTheDocument();
    expect(screen.getByTestId("assets-usage")).toHaveTextContent("один файл до 8.0 МБ");
  });

  it("lists files with their public URL", async () => {
    route({ [LIST]: () => json(200, listView([asset()])) });
    renderWithProviders(<AssetsPage id={WS} />);
    const row = await screen.findByTestId("asset-row");
    expect(row).toHaveTextContent("photo.jpg");
    expect(row).toHaveTextContent("image/jpeg");
    expect(row).toHaveTextContent("http://alex.mock.local/__mocker/assets/photo.jpg");
  });

  it("uploads a dropped file as a raw PUT under the file's own media type", async () => {
    const fetchMock = route({
      [LIST]: () => json(200, listView([])),
      [`PUT /api/workspaces/${WS}/assets/logo.png`]: () =>
        json(201, asset({ name: "logo.png", mediaType: "image/png", sizeBytes: 3 })),
    });
    renderWithProviders(<AssetsPage id={WS} />);
    await screen.findByTestId("asset-upload-form");

    const file = new File([new Uint8Array([1, 2, 3])], "logo.png", { type: "image/png" });
    await userEvent.upload(screen.getByTestId("asset-file-input"), file);
    expect(await screen.findByTestId("asset-name")).toHaveValue("logo.png");
    await userEvent.click(screen.getByTestId("asset-upload-submit"));

    expect(await screen.findByTestId("asset-uploaded")).toHaveTextContent("logo.png");
    const put = fetchMock.mock.calls.find(([, init]) => init?.method === "PUT");
    expect(put).toBeDefined();
    const headers = new Headers(put?.[1]?.headers);
    // The body is the file itself and the header is ITS type — not the JSON
    // type customFetch sets on every other write (api/client.ts).
    expect(headers.get("Content-Type")).toBe("image/png");
    expect(put?.[1]?.body).toBeInstanceOf(Blob);
  });

  it("refuses a name outside the server's alphabet before any request", async () => {
    const fetchMock = route({ [LIST]: () => json(200, listView([])) });
    renderWithProviders(<AssetsPage id={WS} />);
    await screen.findByTestId("asset-upload-form");
    await userEvent.upload(
      screen.getByTestId("asset-file-input"),
      new File(["x"], "a.txt", { type: "text/plain" }),
    );
    const name = await screen.findByTestId("asset-name");
    await userEvent.clear(name);
    await userEvent.type(name, "bad name!");
    expect(screen.getByTestId("asset-upload-submit")).toBeDisabled();
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "PUT")).toHaveLength(0);
  });

  it("deletes only with the typed slug, sent as confirmSlug", async () => {
    const fetchMock = route({
      [LIST]: () => json(200, listView([asset()])),
      [`DELETE /api/workspaces/${WS}/assets/photo.jpg`]: () => new Response(null, { status: 204 }),
    });
    renderWithProviders(<AssetsPage id={WS} />);
    await userEvent.click(await screen.findByTestId("asset-delete"));
    const confirm = await screen.findByTestId("asset-delete-confirm");
    expect(confirm).toBeDisabled();
    await userEvent.type(screen.getByTestId("asset-delete-slug"), "alex");
    await userEvent.click(confirm);
    await waitFor(() => {
      const del = fetchMock.mock.calls.find(([, init]) => init?.method === "DELETE");
      expect(del).toBeDefined();
      expect(JSON.parse(String(del?.[1]?.body))).toEqual({ confirmSlug: "alex" });
    });
  });

  it("names the file when a delete is refused", async () => {
    route({
      [LIST]: () => json(200, listView([asset()])),
      [`DELETE /api/workspaces/${WS}/assets/photo.jpg`]: () =>
        json(403, { error: { code: "forbidden", message: "confirmSlug does not match" } }),
    });
    renderWithProviders(<AssetsPage id={WS} />);
    await userEvent.click(await screen.findByTestId("asset-delete"));
    await userEvent.type(await screen.findByTestId("asset-delete-slug"), "wrong");
    await userEvent.click(screen.getByTestId("asset-delete-confirm"));
    expect(await screen.findByRole("alert")).toHaveTextContent("photo.jpg");
  });
});

import { afterEach, expect, it, vi } from "vitest";
import { screen, fireEvent } from "@testing-library/react";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { BackendValueInspector } from "./BackendValueInspector";
afterEach(() => vi.unstubAllGlobals());
it("opens the exact query result collection/key and its owner in the pinned inspector", async () => {
  const urls: string[] = [];
  vi.stubGlobal("fetch", async (url: RequestInfo | URL, init?: RequestInit) => {
    urls.push(String(url));
    if (init?.method === "POST") return json(200, { nodes: [], edges: [], nextCursor: "" });
    if (String(url).endsWith("/nodes/query"))
      return json(200, {
        id: "query",
        name: "Query",
        kind: "query",
        parentId: "flow",
        evidenceIds: [],
        attributes: {
          results: [{ key: "same-key", name: "Result" }],
          parameters: [{ key: "same-key", name: "Input" }],
        },
      });
    if (String(url).endsWith("/nodes/flow"))
      return json(200, {
        id: "flow",
        name: "Owner flow",
        kind: "flow",
        parentId: null,
        evidenceIds: [],
        attributes: {},
      });
    if (String(url).endsWith("/revisions/rev")) return json(200, { id: "rev", schemaVersion: "4" });
    return json(200, { items: [], nextCursor: "" });
  });
  renderWithProviders(
    <BackendValueInspector
      projectId="project"
      revisionId="rev"
      value={{ kind: "port", nodeId: "query", collection: "results", portKey: "same-key" }}
      onClose={() => {}}
    />,
  );
  const exact = await screen.findByRole("button", {
    name: "Открыть точное значение Result · results · same-key",
  });
  await vi.waitFor(() => expect(exact).toHaveFocus());
  expect(screen.getByText("Выбранное значение: results · same-key")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Открыть владельца значения flow" }));
  await screen.findByRole("heading", { name: "Owner flow" });
  expect(
    urls.filter((url) => url.includes("/nodes/")).every((url) => url.includes("/revisions/rev/")),
  ).toBe(true);
  expect(screen.queryByText(/Выбранное значение:/)).not.toBeInTheDocument();
});

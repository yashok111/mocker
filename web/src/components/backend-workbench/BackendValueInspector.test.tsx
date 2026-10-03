import { afterEach, expect, it, vi } from "vitest";
import { screen, fireEvent } from "@testing-library/react";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { BackendValueInspector } from "./BackendValueInspector";
import { BackendAPIArtifactsContext } from "./BackendAPIArtifacts";
import { useState } from "react";
afterEach(() => vi.unstubAllGlobals());
it("updates the full event address when the selected field stays the same", async () => {
  vi.stubGlobal("fetch", async (url: RequestInfo | URL, init?: RequestInit) => {
    if (String(url).endsWith("/revisions/rev"))
      return json(200, {
        id: "rev",
        projectId: "project",
        schemaVersion: "5",
        semanticHash: "a".repeat(64),
      });
    if (String(url).endsWith("/nodes/field"))
      return json(200, {
        id: "field",
        kind: "event_field",
        parentId: "message",
        name: "Same field",
        attributes: {},
        evidenceIds: [],
      });
    if (init?.method === "POST") return json(422, {});
    return json(200, { items: [], nextCursor: "" });
  });
  function Harness() {
    const [routeId, setRouteId] = useState("delivery-a");
    return (
      <>
        <button onClick={() => setRouteId("delivery-b")}>Other route</button>
        <BackendValueInspector
          projectId="project"
          revisionId="rev"
          value={{ kind: "event_field", nodeId: "field", endpointId: "consumer", routeId }}
          onClose={() => {}}
        />
      </>
    );
  }
  renderWithProviders(<Harness />);
  await screen.findByRole("heading", { name: "Same field" });
  fireEvent.click(screen.getByRole("button", { name: "Other route" }));
  expect(await screen.findByText(/Маршрут значения: delivery-b/)).toBeVisible();
  expect(screen.queryByText(/Маршрут значения: delivery-a/)).not.toBeInTheDocument();
  await vi.waitFor(() => expect(screen.getByRole("heading", { name: "Same field" })).toHaveFocus());
});
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
it("opens a lineage API field as its source node with pinned manual artifact evidence and guards owner navigation", async () => {
  const hash = "a".repeat(64);
  const revision = {
    id: "historical",
    projectId: "project",
    semanticHash: hash,
    sourceSnapshotIds: ["snap"],
    artifactPins: [{ kind: "api_design", id: "12", revisionId: "23", contentHash: hash }],
  };
  const node = {
    id: "field",
    kind: "api_field",
    name: "Original source field",
    parentId: "operation",
    evidenceIds: [],
    attributes: {
      direction: "response",
      location: "body",
      selector: { kind: "json_pointer", value: "/from/source" },
    },
  };
  const calls: Array<{ url: string; body: unknown }> = [];
  vi.stubGlobal("fetch", async (url: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ url: String(url), body: init?.body ? JSON.parse(String(init.body)) : null });
    if (String(url).endsWith("/nodes/field")) return json(200, node);
    if (String(url).endsWith("/api-artifacts/query"))
      return json(200, {
        revisionId: revision.id,
        semanticHash: hash,
        sourceSnapshotIds: revision.sourceSnapshotIds,
        pins: revision.artifactPins,
        nextCursor: "",
        items: [
          {
            binding: {
              sourceNodeId: "field",
              sourceKind: "api_field",
              sourceLastKnownLabel: "Frozen source field",
              origin: "manual",
              reason: "Explicit association",
              ref: {
                kind: "api_design",
                artifactId: "12",
                revisionId: "23",
                contentHash: hash,
                objectHash: hash,
                selector: { jsonPointer: "/components/schemas/Flag" },
                resolvedPointer: "/components/schemas/Flag",
                lastKnownLabel: "Frozen Flag",
              },
            },
            resolution: {
              status: "broken",
              updateAvailable: false,
              diagnostics: [{ code: "snapshot_missing", message: "Owner snapshot unavailable" }],
            },
          },
        ],
      });
    return json(
      200,
      init?.method === "POST"
        ? { nodes: [], edges: [], nextCursor: "" }
        : { items: [], nextCursor: "" },
    );
  });
  renderWithProviders(
    <BackendAPIArtifactsContext
      value={{
        revision: revision as never,
        projectVersion: 9,
        canEdit: false,
        onDirty: vi.fn(),
        onApplied: vi.fn(),
        guard: () => false,
      }}
    >
      <BackendValueInspector
        projectId="project"
        revisionId="historical"
        value={{ kind: "api_field", nodeId: "field" }}
        onClose={vi.fn()}
      />
    </BackendAPIArtifactsContext>,
  );
  expect(await screen.findByText("Frozen Flag")).toBeVisible();
  expect(screen.getByText(/Закреплённый API недоступен/)).toBeVisible();
  expect(screen.getByText(/Состояние текущего черновика API неизвестно/)).toBeVisible();
  const link = screen.getByRole("link", { name: "Открыть закреплённый API" });
  expect(link.getAttribute("href")).toContain(
    "pinnedSelectorPointer=%2Fcomponents%2Fschemas%2FFlag",
  );
  expect(link.getAttribute("href")).toContain("returnSourceNodeId=field");
  expect(screen.queryByRole("button", { name: "Изменить связь API" })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Открыть владельца значения operation" }));
  expect(calls.some((call) => call.url.endsWith("/nodes/operation"))).toBe(false);
  expect(
    calls.filter((call) => call.url.endsWith("api-artifacts/query")).map((call) => call.body),
  ).toEqual([{ revisionId: "historical", limit: 100, cursor: "" }]);
});

import { afterEach, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { BackendNamespacedArtifactInspector } from "./BackendNamespacedArtifactInspector";

const id = "11111111-1111-4111-8111-111111111111";
afterEach(() => vi.unstubAllGlobals());
it("keeps a foreign numeric collision behind the namespaced read route", async () => {
  const namespace = { scope: "foreign" as const, installationId: id };
  const pin = { kind: "design_scenario", id: "1", revisionId: "1", contentHash: "a".repeat(64) };
  const fetch = vi.fn(async (_url: string, _init?: RequestInit) => json(200, { targetHash: "b".repeat(64), pin: { namespace, pin }, status: "foreign_unresolved", reason: "Exact mapping required", apiBindings: [], editorBindings: [] }));
  vi.stubGlobal("fetch", fetch);
  renderWithProviders(<BackendNamespacedArtifactInspector projectId={id} target={{ revisionId: id }} targetHash={"b".repeat(64)} reference={{ kind: "namespaced_artifact", rowId: "row", namespacedLocator: { namespace, locator: { pin, view: "sequence", owner: { pointer: "/participants/0", participantId: "client" } } } }} onClose={vi.fn()} />);
  await screen.findByText(/Foreign ref не разрешён/);
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(fetch.mock.calls[0]?.[0]).toBe(`/api/backend-projects/${id}/artifacts/namespaced/query`);
  expect(JSON.parse(String(fetch.mock.calls[0]?.[1]?.body))).toMatchObject({ namespace, artifact: { kind: "design_scenario", id: "1" } });
});

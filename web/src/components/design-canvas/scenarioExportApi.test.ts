import { afterEach, expect, it, vi } from "vitest";
import { json, route } from "@/test/http";
import { loadScenarioArtifact, loadScenarioExportOptions } from "./scenarioExportApi";

afterEach(() => vi.unstubAllGlobals());

it("addresses an immutable revision and keeps the selected contract in the request", async () => {
  const fetch = route({
    "GET /api/design-scenarios/7/revisions/11/export-options": () =>
      json(200, { scenarioId: 7, revisionId: 11, sourceHash: "hash", options: [] }),
    "GET /api/design-scenarios/7/revisions/11/exports/openapi-json?contractId=api-1": () =>
      json(200, {
        scenarioId: 7,
        revisionId: 11,
        content: "{}",
        filename: "api.json",
        mediaType: "application/json",
        diagnostics: [],
      }),
  });
  expect((await loadScenarioExportOptions(7, 11)).revisionId).toBe(11);
  expect((await loadScenarioArtifact(7, 11, "openapi-json", "api-1")).content).toBe("{}");
  expect(fetch).toHaveBeenCalledTimes(2);
});

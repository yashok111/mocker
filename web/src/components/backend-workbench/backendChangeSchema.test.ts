import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { backendChangeSchemas } from "./backendChangeSchema";

describe("full command form contract", () => {
  it("keeps every included definition byte-semantically equal to canonical OpenAPI", () => {
    const document = JSON.parse(readFileSync("../api/openapi.json", "utf8"));
    for (const [name, schema] of Object.entries(backendChangeSchemas)) {
      expect(schema, name).toEqual(document.components.schemas[name]);
    }
    expect(backendChangeSchemas.BackendChangeProposalCommand!.oneOf).toHaveLength(72);
  });
  it("includes the complete local dependency closure", () => {
    function walk(value: unknown) {
      if (Array.isArray(value)) {
        value.forEach(walk);
        return;
      }
      if (value === null || typeof value !== "object") return;
      if ("$ref" in value && typeof value.$ref === "string")
        expect(backendChangeSchemas).toHaveProperty(value.$ref.split("/").at(-1)!);
      Object.values(value).forEach(walk);
    }
    Object.values(backendChangeSchemas).forEach(walk);
  });
});

it("exports every analysis, rebase and ready request root for exact recovery", () => {
  for (const name of [
    "StartBackendAnalysisRequest",
    "CancelBackendAnalysisRequest",
    "RetryBackendAnalysisRequest",
    "PreviewBackendChangeProposalRebaseRequest",
    "ApplyBackendChangeProposalRebaseRequest",
    "ApplyBackendChangeProposalLifecycleRequest",
    "BackendAnalysisJobDetail",
    "BackendAnalysisResultPage",
  ]) {
    expect(backendChangeSchemas).toHaveProperty(name);
  }
  const targets = backendChangeSchemas.BackendChangeIdentityTarget!.oneOf!;
  const carry = targets.find((arm) => arm.properties?.kind?.const === "carried_source_identity")!;
  expect(carry.required).toContain("basis");
  expect(carry.additionalProperties).toBe(false);
  const output = backendChangeSchemas.BackendAnalysisResultPage!;
  expect(output.properties!.items!.type).toBe("array");
});

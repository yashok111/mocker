import { describe, expect, it } from "vitest";
import { rebaseCanvasDocument } from "./canvasRebase";
import type { CanvasDocument } from "./types";

function document(): CanvasDocument {
  return {
    formatVersion: 1,
    title: "Original",
    participants: [
      { id: "a", name: "A", kind: "client", description: "" },
      { id: "b", name: "B", kind: "service", description: "" },
    ],
    messages: [],
    fragments: [],
    contracts: [
      {
        id: "api",
        name: "API",
        mode: "linked",
        source: { designId: 1, revisionId: 1, version: 1 },
        document: { openapi: "3.1.0", info: { title: "API", version: "1" }, paths: {} },
      },
    ],
  };
}

describe("rebaseCanvasDocument", () => {
  it("adopts canonical linked pins while retaining edits entered during save", () => {
    const submitted = document();
    const current = structuredClone(submitted);
    current.title = "New local title";
    current.contracts[0]!.document.info = { title: "Next API edit", version: "1" };
    const canonical = structuredClone(submitted);
    canonical.contracts[0]!.source = { designId: 1, revisionId: 2, version: 2 };
    const merged = rebaseCanvasDocument(submitted, current, canonical);
    expect(merged.title).toBe("New local title");
    expect(merged.contracts[0]!.source?.version).toBe(2);
    expect(merged.contracts[0]!.document.info).toMatchObject({ title: "Next API edit" });
    expect(submitted.contracts[0]!.source?.version).toBe(1);
  });

  it("keeps local deletions and ordering while accepting newly assigned server entities", () => {
    const submitted = document();
    const current = structuredClone(submitted);
    current.participants = [current.participants[1]!];
    const canonical = structuredClone(submitted);
    canonical.participants.push({ id: "c", name: "C", kind: "database", description: "" });
    expect(
      rebaseCanvasDocument(submitted, current, canonical).participants.map((item) => item.id),
    ).toEqual(["b", "c"]);
  });

  it("does not resurrect removed OpenAPI fields or operations", () => {
    const submitted = document();
    submitted.contracts[0]!.document.paths = {
      "/orders": { get: { summary: "Removed", responses: {} } },
    };
    const current = structuredClone(submitted);
    current.contracts[0]!.document.paths = {};
    const canonical = structuredClone(submitted);
    canonical.contracts[0]!.source = { designId: 1, revisionId: 2, version: 2 };
    canonical.contracts[0]!.document.paths = {
      "/orders": {
        get: { summary: "Before", responses: {}, "x-mocker-canvas-operation-id": "server-key" },
      },
    };
    const merged = rebaseCanvasDocument(submitted, current, canonical);
    expect(merged.contracts[0]!.document.paths).toEqual({});
    expect(merged.contracts[0]!.source?.version).toBe(2);
  });

  it("adopts server-generated operation keys alongside local operation edits", () => {
    const submitted = document();
    submitted.contracts[0]!.document.paths = {
      "/orders": { get: { summary: "Before", responses: {} } },
    };
    const current = structuredClone(submitted);
    current.contracts[0]!.document.paths = {
      "/orders": { get: { summary: "After", responses: {} } },
    };
    const canonical = structuredClone(submitted);
    canonical.contracts[0]!.document.paths = {
      "/orders": {
        get: { summary: "Before", responses: {}, "x-mocker-canvas-operation-id": "server-key" },
      },
    };
    const merged = rebaseCanvasDocument(submitted, current, canonical);
    expect(merged.contracts[0]!.document.paths).toMatchObject({
      "/orders": { get: { summary: "After", "x-mocker-canvas-operation-id": "server-key" } },
    });
  });
});

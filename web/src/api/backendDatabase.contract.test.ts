// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { queryBackendDatabase } from "./generated/backend-projects/backend-projects";
import type { BackendDatabasePage, QueryBackendDatabaseRequest } from "./generated/schemas";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const input: QueryBackendDatabaseRequest = {
  revisionId: "revision",
  datastoreId: "datastore",
  facetKey: "sql",
  recordType: "tables",
};
const page: BackendDatabasePage = {
  projectId: "project",
  revisionId: "revision",
  datastoreId: "datastore",
  semanticHash: "a".repeat(64),
  facetKey: "sql",
  recordType: "tables",
  coverage: {
    coverage: { status: "partial", denominator: null, knownObjects: 0, gaps: [] },
    inventory: [],
    snapshots: [],
    staleCounts: { nodes: 0, edges: 0, evidence: 0 },
  },
  facetStatus: "current",
  limitations: [],
  tableItems: [],
  relationshipItems: [],
  nextCursor: "",
};

describe("generated database client contract", () => {
  it("sends the pinned selectors as a POST body and carries cancellation", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(page), { status: 200 }));
    vi.stubGlobal("fetch", fetch);
    const abort = new AbortController();
    const response = await queryBackendDatabase("project", input, { signal: abort.signal });
    expect(fetch).toHaveBeenCalledWith(
      "/api/backend-projects/project/database/query",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify(input),
        signal: abort.signal,
      }),
    );
    expect(response.status).toBe(200);
    expect(response.data).toEqual(page);
  });
  it.each([
    '{"tableItems":[{"columnCount":9007199254740993}]}',
    '{"relationshipItems":[{"sourceCardinality":{"min":9007199254740993}}]}',
    '{"coverage":{"inventory":[{"denominator":9007199254740993}]}}',
  ])("fails the whole projection before returning a rounded number", async (raw) => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(raw, { status: 200 })));
    await expect(queryBackendDatabase("project", input)).rejects.toThrow(/без потери точности/);
  });
  it.each(["ordinal", "order"])("refuses unsafe nested %s from graph reads", async (field) => {
    const { customFetch } = await import("./client");
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            nodes: [
              {
                attributes: {
                  facets: { sql: { [field]: { status: "known", value: 9007199254740992 } } },
                },
              },
            ],
          }),
          { status: 200 },
        ),
      ),
    );
    await expect(customFetch("/api/backend-projects/project/graph/query")).rejects.toThrow(
      /без потери точности/,
    );
  });
});

// Compile-time correlation matters: a relationship selector has no table search.
const invalidRelationship: QueryBackendDatabaseRequest = {
  ...input,
  recordType: "relationships",
  // @ts-expect-error search belongs only to tables.
  search: "",
};
void invalidRelationship;

import { afterEach, expect, it, vi } from "vitest";
import { readDatabaseMap } from "./databaseMap";
import { readDatabasePage } from "../backendDatabaseReads";
import { readExploreNodes } from "./reads";
import { projectId, revisionId } from "./testFixtures";
import type { BackendDatabaseRelationshipItem } from "@/api/generated/schemas";

vi.mock("../backendDatabaseReads", async (original) => ({
  ...(await original<typeof import("../backendDatabaseReads")>()),
  readDatabasePage: vi.fn(),
}));
vi.mock("./reads", async (original) => ({
  ...(await original<typeof import("./reads")>()),
  readExploreNodes: vi.fn(),
}));
afterEach(() => vi.resetAllMocks());
const target = { revisionId };
const relation = (id: string, column: string): BackendDatabaseRelationshipItem => ({
  edgeId: id,
  sourceTableId: "kladr",
  targetTableId: "kladr",
  constraintId: `fk-${id}`,
  columnPairs: [{ fromColumnId: column, toColumnId: "id" }],
  evidenceIds: [],
  status: "explicit",
  targetReason: null,
  sourceCardinality: { min: 0, max: null, basis: ["Incomplete uniqueness proof"] },
  targetCardinality: { min: 1, max: "1", basis: ["Declared key"] },
});
function fixture() {
  vi.mocked(readDatabasePage).mockImplementation(async (_context, input) => ({
    status: 200,
    headers: new Headers(),
    data: {
      projectId,
      revisionId,
      semanticHash: "a".repeat(64),
      datastoreId: "db",
      facetKey: "migration",
      recordType: input.recordType,
      facetStatus: "current",
      coverage: {
        coverage: { status: "partial", knownObjects: 1, denominator: null, gaps: [] },
        inventory: [],
        snapshots: [],
      },
      limitations: [],
      nextCursor: "",
      tableItems:
        input.recordType === "tables"
          ? [
              {
                tableId: "kladr",
                schemaId: "schema",
                qualifiedName: "kladr",
                columnCount: 7,
                facetKeys: ["migration"],
                driftStatus: "unknown",
              },
            ]
          : [],
      relationshipItems:
        input.recordType === "relationships"
          ? [relation("parent", "parent_id"), relation("region", "region_id")]
          : [],
    },
  }));
  vi.mocked(readExploreNodes).mockResolvedValue(
    ["parent_id", "region_id", "id"].map((id) => ({
      id,
      name: id,
      kind: "column",
      attributes: {},
      parentId: "kladr",
      description: "",
      childCount: 0,
    })),
  );
}
it("distinguishes two self references by their actual fields and reads cardinality values without stringifying objects", async () => {
  fixture();
  const signal = new AbortController().signal;
  const map = await readDatabaseMap(
    projectId,
    target,
    { datastoreId: "db", facetKey: "migration" },
    signal,
  );
  expect(map.edges.map((e) => e.label)).toEqual(["parent_id → id", "region_id → id"]);
  expect(map.edges[0]?.details).toMatchObject({
    "Кратность исходной таблицы": "0…неизвестно",
    "Кратность целевой таблицы": "1…1",
  });
  expect(map.edges.map((e) => [e.id, e.from, e.to])).toEqual([
    ["parent", "kladr", "kladr"],
    ["region", "kladr", "kladr"],
  ]);
  expect(readExploreNodes).toHaveBeenCalledWith(
    projectId,
    target,
    ["parent_id", "id", "region_id"],
    signal,
  );
});
it("keeps relations readable if optional field-name lookup fails", async () => {
  fixture();
  vi.mocked(readExploreNodes).mockRejectedValue(new Error("unavailable"));
  const map = await readDatabaseMap(
    projectId,
    target,
    { datastoreId: "db", facetKey: "migration" },
    new AbortController().signal,
  );
  expect(map.edges).toHaveLength(2);
  expect(map.edges.every((e) => e.label === "Внешний ключ")).toBe(true);
  expect(JSON.stringify(map.edges)).not.toContain("[object Object]");
});

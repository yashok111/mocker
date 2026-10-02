// @vitest-environment node
import { describe, expect, it } from "vitest";
import type {
  BackendDatabaseRelationshipItem,
  BackendDatabaseTableItem,
} from "@/api/generated/schemas";
import { buildDatabaseScene } from "./backendDatabaseLayout";

const table = (id: string): BackendDatabaseTableItem => ({
  tableId: id,
  schemaId: "schema",
  qualifiedName: id,
  columnCount: 2,
  facetKeys: ["sql"],
  driftStatus: "unknown",
});
const relationship = (
  id: string,
  source: string,
  target: string | null,
): BackendDatabaseRelationshipItem => ({
  edgeId: id,
  constraintId: `constraint-${id}`,
  sourceTableId: source,
  targetTableId: target,
  columnPairs: [],
  evidenceIds: [],
  sourceCardinality: { min: null, max: null, basis: ["Uncertain"] },
  targetCardinality: { min: null, max: null, basis: ["Uncertain"] },
  status: "stale",
  targetReason: target ? null : "Missing target",
});

describe("bounded ER projection", () => {
  it("preserves parallel/self edge identities while keeping unresolved and off-page edges out of geometry", () => {
    const edges = [
      relationship("one", "a", "b"),
      relationship("two", "a", "b"),
      relationship("self", "a", "a"),
      relationship("outside", "a", "c"),
      relationship("unknown", "a", null),
    ];
    const scene = buildDatabaseScene([table("a"), table("b")], edges);
    expect(scene.input.edges.map((edge) => edge.id)).toEqual(["one", "two", "self"]);
    expect(scene.excludedEndpoints).toBe(2);
    expect(edges).toHaveLength(5);
  });
  it("caps only canvas at 200 tables and 600 edges without truncating query data", () => {
    const tables = Array.from({ length: 201 }, (_, i) => table(`table-${i}`));
    const edges = Array.from({ length: 605 }, (_, i) =>
      relationship(`edge-${i}`, "table-0", "table-1"),
    );
    const scene = buildDatabaseScene(tables, edges);
    expect(scene.input.nodes).toHaveLength(200);
    expect(scene.input.edges).toHaveLength(600);
    expect(scene.excludedTables).toBe(1);
    expect(scene.excludedEdges).toBe(5);
    expect(tables).toHaveLength(201);
    expect(edges).toHaveLength(605);
  });
});

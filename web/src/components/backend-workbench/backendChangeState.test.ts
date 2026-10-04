import { describe, expect, it } from "vitest";
import { changePendingIdentities } from "./backendChangeState";
import type { BackendChangeProposalCommand } from "@/api/generated/schemas";
import { changeTestID } from "./backendChangeTestFixtures";
describe("local identity intent", () => {
  it("includes specialized newly created records with an explicit unassigned key", () => {
    const commands: BackendChangeProposalCommand[] = [
      {
        type: "alter_index",
        action: "create",
        commandId: changeTestID,
        reason: "New index",
        indexId: changeTestID,
        facetKey: "sql",
        name: "Index",
        tableId: changeTestID,
        definition: {
          dialect: "postgresql",
          analysisStatus: "complete",
          gaps: [],
          terms: [{ columnId: changeTestID, direction: "asc", nulls: "unknown" }],
          unique: { status: "known", value: false },
          predicate: { status: "known", value: null },
          method: { status: "known", value: null },
          nativeDefinition: null,
        },
      },
    ];
    expect(changePendingIdentities([], commands)).toEqual([
      expect.objectContaining({
        target: { kind: "intent_identity", recordType: "node", id: changeTestID },
        externalKey: null,
      }),
    ]);
  });
});

it("removes a carried qualified identity with its exact underlying record", () => {
  const source = {
    recordType: "node" as const,
    id: changeTestID,
    repositoryId: changeTestID,
    providerNamespace: "compiler",
    externalKey: "old",
    assertionHash: "a".repeat(64),
  };
  const target = {
    kind: "carried_source_identity" as const,
    source,
    basis: { revisionId: changeTestID, semanticHash: "b".repeat(64) },
  };
  const baseline = [
    {
      target,
      externalKey: "desired",
      origin: {
        kind: "intent" as const,
        recordType: "node" as const,
        subjectId: changeTestID,
        selector: { kind: "edge_name" as const },
        sourceClaims: [],
        evidenceIds: [],
      },
    },
  ];
  expect(
    changePendingIdentities(baseline, [
      {
        type: "remove_node",
        commandId: changeTestID,
        reason: "Remove exact record",
        id: changeTestID,
      },
    ]),
  ).toEqual([]);
  expect(baseline[0]!.target.basis.semanticHash).toBe("b".repeat(64));
});

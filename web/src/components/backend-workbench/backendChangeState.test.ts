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

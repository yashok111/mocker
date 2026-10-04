import { expect, it } from "vitest";
import { verifyRebaseAcceptance } from "./backendChangeReads";
import { hashBackendJSON } from "./backendImportHash";
import { changeTestDetail, changeNextID, changeHash } from "./backendChangeTestFixtures";
it("accepts only the selected changed base, canonical source vector and appended draft receipt", async () => {
  const base = changeTestDetail();
  const vector = { documentVersion: "source-vector-v1" as const, partitions: [], snapshots: [] };
  const expected = {
    newBaseRevisionId: changeNextID,
    newBaseSemanticHash: changeHash,
    sourceVectorHash: await hashBackendJSON(vector),
    sourceSnapshotIds: [],
    candidateHash: changeHash,
    semanticHash: changeHash,
  };
  const revision = {
    ...base.revision,
    id: changeNextID,
    parentRevisionId: base.revision.id,
    baseRevisionId: changeNextID,
    sourceVector: vector,
    rebase: {
      protocol: "backend-change-rebase-v1" as const,
      candidateHash: changeHash,
      input: {
        expectedVersion: 1,
        proposalRevisionId: base.revision.id,
        newBaseRevisionId: changeNextID,
        identityResolutions: [],
        resolutions: [],
        repairCommands: [],
      },
    },
  };
  const result = {
    proposal: { ...base.proposal, version: 2, currentDraftRevisionId: changeNextID },
    revision,
    semanticHash: changeHash,
    changes: [],
  };
  expect((await verifyRebaseAcceptance(result, base, expected)).revision.baseRevisionId).toBe(
    changeNextID,
  );
  await expect(
    verifyRebaseAcceptance(
      { ...result, revision: { ...revision, baseRevisionId: base.revision.baseRevisionId } },
      base,
      expected,
    ),
  ).rejects.toThrow(/Rebase/);
  await expect(
    verifyRebaseAcceptance(result, base, { ...expected, sourceVectorHash: "f".repeat(64) }),
  ).rejects.toThrow(/Rebase/);
  await expect(
    verifyRebaseAcceptance(
      { ...result, revision: { ...revision, id: base.revision.id } },
      base,
      expected,
    ),
  ).rejects.toThrow(/Rebase/);
});

it("hashes the real source5 synthetic vector exactly like the server, without treating it as empty", async () => {
  const oracle = await import("@/test/fixtures/backendSource5Vector.json");
  expect(oracle.vector.partitions.length).toBeGreaterThan(0);
  expect(await hashBackendJSON(oracle.vector)).toBe(oracle.hash);
  expect(
    await hashBackendJSON({ documentVersion: "source-vector-v1", partitions: [], snapshots: [] }),
  ).not.toBe(oracle.hash);
});

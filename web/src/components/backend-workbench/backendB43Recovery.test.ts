import { afterEach, expect, it } from "vitest";
import {
  makeAnalysisAttempt,
  verifyAnalysisMutation,
  analysisRecoveryKey,
  writeAnalysisRecovery,
  inspectAnalysisRecovery,
} from "./backendAnalysisRecovery";
import {
  changeTestDetail,
  changeTestID,
  changeNextID,
  changeHash,
} from "./backendChangeTestFixtures";
afterEach(() => sessionStorage.clear());
const base = changeTestDetail();
const report = {
  jobId: changeNextID,
  resultVersion: 2,
  inputHash: changeHash,
  resultHash: changeHash,
};
const exceptions = [
  { criterionKey: "z-runtime", author: "reviewer", reason: "Not executed" },
  { criterionKey: "a-runtime", author: "reviewer", reason: "External check" },
];
function attempt() {
  return makeAnalysisAttempt(
    "implemented",
    { projectId: changeTestID, proposalId: changeTestID },
    {
      action: "implemented",
      expectedVersion: 1,
      proposalRevisionId: base.revision.id,
      report,
      resultRevisionId: changeNextID,
      exceptions,
      idempotencyKey: "implemented-exact",
    },
    { draftHash: changeHash, resultSemanticHash: changeHash },
  );
}
function result() {
  return {
    proposal: {
      ...base.proposal,
      version: 2,
      status: "implemented",
      implementedReference: {
        report,
        proposalRevisionId: base.revision.id,
        draftHash: changeHash,
        resultRevisionId: changeNextID,
        resultSemanticHash: changeHash,
        exceptions: [...exceptions].reverse(),
        behaviorStatus: "unverified",
      },
    },
    revision: base.revision,
  };
}
it("persists a distinct implemented action and accepts canonical multi-exception order without changing original bytes", async () => {
  const a = attempt(),
    key = analysisRecoveryKey(changeTestID);
  writeAnalysisRecovery(key, a, null);
  expect(inspectAnalysisRecovery(key).attempt?.kind).toBe("implemented");
  await expect(verifyAnalysisMutation(a, result())).resolves.toBeUndefined();
  expect(inspectAnalysisRecovery(key).attempt?.body).toBe(a.body);
});
it.each(["draft", "owner", "version", "status", "report", "source", "exceptions", "behavior"])(
  "rejects mismatched implemented %s",
  async (field) => {
    const r = result();
    if (field === "draft") r.revision = { ...r.revision, semanticHash: "b".repeat(64) };
    if (field === "owner") r.revision = { ...r.revision, proposalId: changeNextID };
    if (field === "version") r.proposal.version = 3;
    if (field === "status") r.proposal.status = "ready";
    if (field === "report")
      r.proposal.implementedReference.report = { ...report, resultVersion: 1 };
    if (field === "source") r.proposal.implementedReference.resultSemanticHash = "b".repeat(64);
    if (field === "exceptions") r.proposal.implementedReference.exceptions = [];
    if (field === "behavior") r.proposal.implementedReference.behaviorStatus = "verified";
    await expect(verifyAnalysisMutation(attempt(), r)).rejects.toThrow();
  },
);
it("rejects a mismatched action tag and verifies unarchive clears associations", async () => {
  expect(() =>
    makeAnalysisAttempt(
      "archive",
      { projectId: changeTestID, proposalId: changeTestID },
      {
        action: "unarchive",
        expectedVersion: 1,
        proposalRevisionId: base.revision.id,
        idempotencyKey: "a",
      } as never,
      { draftHash: changeHash },
    ),
  ).toThrow();
  const a = makeAnalysisAttempt(
    "unarchive",
    { projectId: changeTestID, proposalId: changeTestID },
    {
      action: "unarchive",
      expectedVersion: 1,
      proposalRevisionId: base.revision.id,
      idempotencyKey: "u",
    },
    { draftHash: changeHash },
  );
  await expect(
    verifyAnalysisMutation(a, {
      proposal: { ...base.proposal, version: 2 },
      revision: base.revision,
    }),
  ).resolves.toBeUndefined();
  await expect(
    verifyAnalysisMutation(a, { ...result(), proposal: { ...result().proposal, status: "draft" } }),
  ).rejects.toThrow();
});

it("archive refuses association loss instead of accepting the status alone", async () => {
  const { hashBackendJSON } = await import("./backendImportHash");
  const readyReference = {
    report,
    proposalRevisionId: base.revision.id,
    draftHash: changeHash,
    acknowledgedGapIds: [],
  };
  const a = makeAnalysisAttempt(
    "archive",
    { projectId: changeTestID, proposalId: changeTestID },
    {
      action: "archive",
      expectedVersion: 1,
      proposalRevisionId: base.revision.id,
      idempotencyKey: "archive",
    },
    {
      draftHash: changeHash,
      associationHash: await hashBackendJSON({ readyReference, implementedReference: null }),
    },
  );
  await expect(
    verifyAnalysisMutation(a, {
      proposal: { ...base.proposal, version: 2, status: "archived" },
      revision: base.revision,
    }),
  ).rejects.toThrow();
  await expect(
    verifyAnalysisMutation(a, {
      proposal: { ...base.proposal, version: 2, status: "archived", readyReference },
      revision: base.revision,
    }),
  ).resolves.toBeUndefined();
});

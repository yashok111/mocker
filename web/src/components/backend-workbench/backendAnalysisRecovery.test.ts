import { afterEach, expect, it, vi } from "vitest";
import {
  makeAnalysisAttempt,
  inspectAnalysisRecovery,
  writeAnalysisRecovery,
  analysisRecoveryKey,
} from "./backendAnalysisRecovery";
import { changeTestID, changeNextID } from "./backendChangeTestFixtures";
afterEach(() => {
  sessionStorage.clear();
  vi.restoreAllMocks();
});
const attempt = () =>
  makeAnalysisAttempt(
    "start",
    { projectId: changeTestID },
    {
      kind: "impact",
      fromRevisionId: changeTestID,
      target: { revisionId: changeNextID },
      scope: {},
      limits: {},
      observationMode: "none",
      idempotencyKey: "exact-key",
    },
  );
it("retains closed exact owner/body/key and refuses concurrent replacement", () => {
  const key = analysisRecoveryKey(changeTestID),
    a = attempt();
  const raw = writeAnalysisRecovery(key, a, null);
  expect(inspectAnalysisRecovery(key).attempt).toEqual(a);
  sessionStorage.setItem(key, "concurrent");
  expect(() => writeAnalysisRecovery(key, null, raw)).toThrow(/изменилась/);
  expect(sessionStorage.getItem(key)).toBe("concurrent");
});
it("blocks corrupt records and failed readback", () => {
  const key = analysisRecoveryKey(changeTestID);
  sessionStorage.setItem(key, "corrupt");
  expect(inspectAnalysisRecovery(key).error).toBeTruthy();
  sessionStorage.clear();
  vi.spyOn(sessionStorage, "setItem").mockImplementation(() => {});
  expect(() => writeAnalysisRecovery(key, attempt(), null)).toThrow(/подтвердить/);
});
it("rejects unknown fields and browser unsafe CAS before sending", () => {
  expect(() =>
    makeAnalysisAttempt("start", { projectId: changeTestID }, {
      ...attempt().input,
      unknown: true,
    } as never),
  ).toThrow();
  expect(() =>
    makeAnalysisAttempt("ready", { projectId: changeTestID, proposalId: changeNextID }, {
      expectedVersion: 9007199254740992,
    } as never),
  ).toThrow();
});
it("rejects action-specific owner confusion and mutable request bytes", () => {
  expect(() =>
    makeAnalysisAttempt(
      "start",
      { projectId: changeTestID, proposalId: changeNextID },
      attempt().input,
    ),
  ).toThrow();
  expect(() =>
    makeAnalysisAttempt(
      "cancel",
      { projectId: changeTestID, jobId: changeNextID, proposalId: changeTestID },
      { idempotencyKey: "k" },
    ),
  ).toThrow();
  const a = attempt();
  a.input.idempotencyKey = "changed";
  expect(() => writeAnalysisRecovery(analysisRecoveryKey(changeTestID), a, null)).toThrow();
});

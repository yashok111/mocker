import { describe, expect, it } from "vitest";
import type { BackendSavedViewV2 } from "@/api/generated/schemas";
import { exactEnvelope, exactHash, exactIDs } from "@/test/backendExact";
import { checkBackendSavedView } from "./backendSavedViewReads";
const target = {
  changeProposal: { proposalId: exactIDs.project, proposalRevisionId: exactIDs.snapshot },
};
const view: BackendSavedViewV2 = {
  id: exactIDs.node,
  projectId: exactIDs.project,
  version: 2,
  name: "view",
  documentVersion: "saved-view-v2",
  target,
  pins: {
    revisionId: exactIDs.revision,
    semanticHash: exactHash,
    proposal: null,
    effective: exactEnvelope(target).pins,
  },
  state: {
    kind: "flow",
    scope: {},
    filters: { search: "", accessKind: "", reverseAccessKind: "" },
    selection: null,
    positions: [],
    collapsedGroupIds: [],
  },
  createdAt: "2026-10-03",
  updatedAt: "2026-10-03",
};
describe("saved-view-v2 immutable document pins", () => {
  it("accepts the requested full historical document before graph reads", () => {
    expect(
      checkBackendSavedView(view, exactIDs.project, {
        id: exactIDs.node,
        version: 2,
        target,
        documentVersion: "saved-view-v2",
      }),
    ).toBe(view);
  });
  it.each([
    { pins: { ...view.pins, revisionId: exactIDs.node } },
    { pins: { ...view.pins, semanticHash: "b".repeat(64) } },
    { pins: { ...view.pins, effective: { ...view.pins.effective, viewSchemaVersion: "6" } } },
    { pins: { ...view.pins, effective: { ...view.pins.effective, targetHash: "" } } },
    { target: { ...target, revisionId: exactIDs.revision } },
    {
      target: {
        importCandidate: { importId: exactIDs.project, importVersion: 1, candidateHash: exactHash },
      },
    },
    { version: Number.MAX_SAFE_INTEGER + 1 },
  ])("rejects inconsistent saved pins %j", (change) => {
    expect(() =>
      checkBackendSavedView({ ...view, ...change } as BackendSavedViewV2, exactIDs.project),
    ).toThrow();
  });
  it("rejects a different requested history version or document version", () => {
    expect(() => checkBackendSavedView(view, exactIDs.project, { version: 1 })).toThrow();
    expect(() =>
      checkBackendSavedView(view, exactIDs.project, { documentVersion: "saved-view-v1" }),
    ).toThrow();
  });
});

it("rejects zero UUIDs in the effective source pins", () => {
  const zero = "00000000-0000-0000-0000-000000000000";
  expect(() =>
    checkBackendSavedView(
      {
        ...view,
        pins: {
          ...view.pins,
          revisionId: zero,
          effective: { ...view.pins.effective, baseRevisionId: zero },
        },
      },
      exactIDs.project,
    ),
  ).toThrow();
  expect(() =>
    checkBackendSavedView(
      {
        ...view,
        pins: { ...view.pins, effective: { ...view.pins.effective, sourceSnapshotIds: [zero] } },
      },
      exactIDs.project,
    ),
  ).toThrow();
});

// @vitest-environment node
import { expect, it } from "vitest";
import { parseBackendSourcePin } from "./backendFlowReads";
import { savedViewSourcePin, sameViewBinding, savedViewStateEqual } from "./backendSavedViewState";
import type { BackendSavedView } from "@/api/generated/schemas";
export const saved: BackendSavedView = {
  id: "00000000-0000-0000-0000-000000000001",
  projectId: "project",
  version: 1,
  name: "Pinned flow",
  documentVersion: "saved-view-v1",
  createdAt: "2026-10-01",
  updatedAt: "2026-10-01",
  target: { revisionId: "source" },
  pins: { revisionId: "source", semanticHash: "a".repeat(64), proposal: null },
  state: {
    kind: "flow",
    scope: { entrypointId: "endpoint", flowId: "flow", dataNodeId: "table" },
    filters: { search: "orders", accessKind: "writes", reverseAccessKind: "reads" },
    selection: { recordType: "edge", id: "access" },
    positions: [{ nodeId: "step", x: 300, y: -20 }],
    collapsedGroupIds: ["transaction"],
  },
};
it("restores saved pins and all focus fields over conflicting source URL fields", () => {
  expect(savedViewSourcePin(saved)).toEqual({
    revisionId: "source",
    viewId: saved.id,
    viewVersion: 1,
    entrypointId: "endpoint",
    flowId: "flow",
    dataNodeId: "table",
    recordId: "access",
    recordType: "edge",
  });
  expect(sameViewBinding(saved.target, saved.state, { revisionId: "other" }, saved.state)).toBe(
    false,
  );
});
it("rejects unsafe, zero, fractional and malformed saved URL versions without losing the saved-view gate", () => {
  for (const version of [0, -1, 1.5, Number.MAX_SAFE_INTEGER + 1, "1x", "", "01"]) {
    expect(parseBackendSourcePin({ viewId: saved.id, viewVersion: version })).toMatchObject({
      viewId: saved.id,
      viewVersion: 0,
    });
  }
  expect(parseBackendSourcePin({ viewId: saved.id, viewVersion: "2" })).toMatchObject({
    viewId: saved.id,
    viewVersion: 2,
  });
});

it("compares normalized server arrays without marking acknowledged presentation dirty", () => {
  const state = {
    ...saved.state,
    positions: [
      { nodeId: "z", x: 1, y: 2 },
      { nodeId: "a", x: 3, y: 4 },
    ],
    collapsedGroupIds: ["z", "a"],
  };
  expect(
    savedViewStateEqual(state, {
      ...state,
      positions: [...state.positions].reverse(),
      collapsedGroupIds: [...state.collapsedGroupIds].reverse(),
    }),
  ).toBe(true);
});

import type { QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import {
  invalidateAssetChange,
  invalidateAuthPresetApply,
  invalidateCheckpointChange,
  invalidateDriftChange,
  invalidateEndpointChange,
  invalidateOperationChange,
  invalidateResourceChange,
  invalidateScenarioChange,
  invalidateWorkspace,
} from "./cachePolicy";
import { getListAssetsQueryKey } from "@/api/generated/assets/assets.ts";
import { getListCheckpointsQueryKey } from "@/api/generated/checkpoints/checkpoints.ts";
import { getGetWorkspaceDriftQueryKey } from "@/api/generated/drift/drift.ts";
import { getListEndpointsQueryKey } from "@/api/generated/endpoints/endpoints.ts";
import {
  getGetAuthPresetQueryKey,
  getGetOperationOverrideQueryKey,
  getListWorkspaceOperationsQueryKey,
} from "@/api/generated/operations/operations.ts";
import {
  getListResourceEntitiesQueryKey,
  getListWorkspaceResourcesQueryKey,
} from "@/api/generated/resources/resources.ts";
import { getListScenariosQueryKey } from "@/api/generated/scenarios/scenarios.ts";
import { getGetWorkspaceQueryKey } from "@/api/generated/workspaces/workspaces.ts";

// These policies replaced eighteen hand-written invalidation sites, and the
// screens' own tests assert the KEYS those sites passed (they spy
// invalidateQueries and read call[0].queryKey). This file pins the same thing
// from the other side, once, against a spy rather than a real client: a policy
// that quietly grows or loses a key would otherwise only be caught by whichever
// screen test happened to enumerate it. The assertions are exact SETS, not
// "contains" — widening a policy is a decision, and it should have to be made
// here, in the same commit as its reason.

const WS = 7;

function spyClient(): { qc: QueryClient; keys: () => unknown[]; calls: () => unknown[] } {
  const invalidateQueries = vi.fn();
  const qc = { invalidateQueries } as unknown as QueryClient;
  return {
    qc,
    keys: () =>
      invalidateQueries.mock.calls.map(
        (call) => (call[0] as { queryKey?: unknown }).queryKey ?? "<predicate>",
      ),
    calls: () => invalidateQueries.mock.calls.map((call) => call[0]),
  };
}

describe("cachePolicy", () => {
  it("invalidateWorkspace touches the workspace document alone", () => {
    const { qc, keys } = spyClient();
    invalidateWorkspace(qc, WS);
    expect(keys()).toEqual([getGetWorkspaceQueryKey(WS)]);
  });

  it("invalidateEndpointChange touches the endpoints list and the workspace", () => {
    const { qc, keys } = spyClient();
    invalidateEndpointChange(qc, WS);
    expect(keys()).toEqual([getListEndpointsQueryKey(WS), getGetWorkspaceQueryKey(WS)]);
  });

  it("invalidateOperationChange also touches THIS operation's override document", () => {
    const { qc, keys } = spyClient();
    invalidateOperationChange(qc, WS, "GET%20%2Fpets");
    expect(keys()).toEqual([
      getGetWorkspaceQueryKey(WS),
      getListWorkspaceOperationsQueryKey(WS),
      getGetOperationOverrideQueryKey(WS, "GET%20%2Fpets"),
    ]);
  });

  it("invalidateAuthPresetApply adds the preset GET and a prefix predicate over override docs", () => {
    const { qc, keys, calls } = spyClient();
    invalidateAuthPresetApply(qc, WS);
    expect(keys()).toEqual([
      getGetWorkspaceQueryKey(WS),
      getGetAuthPresetQueryKey(WS),
      getListWorkspaceOperationsQueryKey(WS),
      "<predicate>",
    ]);

    // The predicate is the half a key comparison cannot express: it must take
    // an override DOCUMENT's key and leave the list's own key alone, since the
    // list key is a proper prefix of every document key.
    const { predicate } = calls()[3] as { predicate: (q: { queryKey: unknown[] }) => boolean };
    const listKey = getListWorkspaceOperationsQueryKey(WS)[0] as string;
    expect(predicate({ queryKey: [`${listKey}/GET%20%2Fpets`] })).toBe(true);
    expect(predicate({ queryKey: [listKey] })).toBe(false);
    expect(predicate({ queryKey: [getListEndpointsQueryKey(WS)[0]] })).toBe(false);
  });

  it("invalidateScenarioChange touches the scenarios list and the workspace", () => {
    const { qc, keys } = spyClient();
    invalidateScenarioChange(qc, WS);
    expect(keys()).toEqual([getListScenariosQueryKey(WS), getGetWorkspaceQueryKey(WS)]);
  });

  it("invalidateCheckpointChange touches the checkpoints list and the workspace", () => {
    const { qc, keys } = spyClient();
    invalidateCheckpointChange(qc, WS);
    expect(keys()).toEqual([getListCheckpointsQueryKey(WS), getGetWorkspaceQueryKey(WS)]);
  });

  it("invalidateAssetChange touches the assets list and the workspace", () => {
    const { qc, keys } = spyClient();
    invalidateAssetChange(qc, WS);
    expect(keys()).toEqual([getListAssetsQueryKey(WS), getGetWorkspaceQueryKey(WS)]);
  });

  it("invalidateDriftChange touches the drift report and the workspace", () => {
    const { qc, keys } = spyClient();
    invalidateDriftChange(qc, WS);
    expect(keys()).toEqual([getGetWorkspaceDriftQueryKey(WS), getGetWorkspaceQueryKey(WS)]);
  });

  it("invalidateResourceChange touches the family's entities and the family list, NOT the workspace", () => {
    const { qc, keys } = spyClient();
    const segment = encodeURIComponent("/users/{}/posts");
    invalidateResourceChange(qc, WS, segment);
    expect(keys()).toEqual([
      getListResourceEntitiesQueryKey(WS, segment),
      getListWorkspaceResourcesQueryKey(WS),
    ]);
    // An entity write is DATA and moves no revision — the workspace document
    // must stay out of this one.
    expect(keys()).not.toContainEqual(getGetWorkspaceQueryKey(WS));
  });
});

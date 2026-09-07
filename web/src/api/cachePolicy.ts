import type { QueryClient } from "@tanstack/react-query";
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

// cachePolicy.ts is the one place that answers "what goes stale when this
// write lands". Eighteen call sites across ten screens re-derived
// `getGetWorkspaceQueryKey(id)` by hand, most of them paired with the list the
// write actually touched, and the pairing is not incidental: DESIGN §3.9 says
// a write that moves the workspace REVISION invalidates the workspace query
// too, and the tab bar's «ревизия N» reads that number straight off it. A
// screen that invalidated only its own list left the revision stale on every
// other tab, which is the bug this file makes hard to write.
//
// Every function below is a POLICY, not a convenience: its key set is exactly
// what the sites it replaced invalidated on the day it was extracted, no
// wider. Widening one is a decision about server behaviour (does this verb
// move the revision?) and belongs in the same commit as the reason, with
// cachePolicy.test.ts updated to pin the new set. Nothing here awaits: the
// callers all fire-and-forget, and an invalidation that a component unmounts
// out from under is not an error.

/**
 * invalidateWorkspace is the base every other policy builds on: the workspace
 * document itself, which carries revision, scenarioId and the settings the
 * layout renders. Used alone by a write that touches NO list — deactivating a
 * scenario, patching settings.
 */
export function invalidateWorkspace(qc: QueryClient, id: number): void {
  void qc.invalidateQueries({ queryKey: getGetWorkspaceQueryKey(id) });
}

/**
 * invalidateEndpointChange: create, update and delete of a custom endpoint,
 * plus the traffic tab's "make an endpoint from this request". The endpoints
 * list moves for the obvious reason; the workspace moves because a new or
 * removed endpoint bumps the revision.
 */
export function invalidateEndpointChange(qc: QueryClient, id: number): void {
  void qc.invalidateQueries({ queryKey: getListEndpointsQueryKey(id) });
  invalidateWorkspace(qc, id);
}

/**
 * invalidateOperationChange: a PUT or DELETE of one operation override, from
 * the operation editor or from the traffic tab's "make an override from this
 * request". Three keys, and the third is per-operation — the override DOCUMENT
 * the editor reads, which a list invalidation does not cover because its key
 * carries opKey as a second segment.
 */
export function invalidateOperationChange(qc: QueryClient, id: number, opKey: string): void {
  invalidateWorkspace(qc, id);
  void qc.invalidateQueries({ queryKey: getListWorkspaceOperationsQueryKey(id) });
  void qc.invalidateQueries({ queryKey: getGetOperationOverrideQueryKey(id, opKey) });
}

/**
 * invalidateAuthPresetApply is the widest policy here and the only one with a
 * predicate, because applying a preset (§3.9) writes an unknown NUMBER of
 * override documents at once: every currently cached one for this workspace has
 * to go, and their query keys all begin with the operations-list key plus a
 * "/" segment — the list key alone has no trailing one, so the prefix test is
 * what tells a document apart from the list. The auth-preset GET is invalidated
 * as well so a later remount does not seed a new proposal off a stale
 * editVersions map.
 */
export function invalidateAuthPresetApply(qc: QueryClient, id: number): void {
  invalidateWorkspace(qc, id);
  void qc.invalidateQueries({ queryKey: getGetAuthPresetQueryKey(id) });
  void qc.invalidateQueries({ queryKey: getListWorkspaceOperationsQueryKey(id) });
  const overrideDocPrefix = `${getListWorkspaceOperationsQueryKey(id)[0]}/`;
  void qc.invalidateQueries({
    predicate: (query) => {
      const key = query.queryKey[0];
      return typeof key === "string" && key.startsWith(overrideDocPrefix);
    },
  });
}

/**
 * invalidateScenarioChange: activate, deactivate or delete. All three move
 * workspace.scenarioId and bump the revision (A9: deleting the ACTIVE scenario
 * deactivates it), so the workspace goes with the list rather than only on the
 * activate path.
 */
export function invalidateScenarioChange(qc: QueryClient, id: number): void {
  void qc.invalidateQueries({ queryKey: getListScenariosQueryKey(id) });
  invalidateWorkspace(qc, id);
}

/**
 * invalidateCheckpointChange: rollback and reset-data. Both put a new
 * pre-destructive point in the list and both can move the revision. NOT used
 * by checkpoint create or delete, which touch the list alone — delete bumps no
 * revision (SIG-DELCP), and widening it here would cost an idle GET on every
 * row removal.
 */
export function invalidateCheckpointChange(qc: QueryClient, id: number): void {
  void qc.invalidateQueries({ queryKey: getListCheckpointsQueryKey(id) });
  invalidateWorkspace(qc, id);
}

/**
 * invalidateAssetChange: upload and delete. An asset write bumps the workspace
 * revision (DESIGN §32.5), so the usage strip and the tab bar both move with
 * the list.
 */
export function invalidateAssetChange(qc: QueryClient, id: number): void {
  void qc.invalidateQueries({ queryKey: getListAssetsQueryKey(id) });
  invalidateWorkspace(qc, id);
}

/**
 * invalidateDriftChange: the drift panel's own refresh, run after every repair
 * it offers. The report is derived from the two layers the repair just
 * changed, and the workspace's revision moved with them.
 */
export function invalidateDriftChange(qc: QueryClient, id: number): void {
  void qc.invalidateQueries({ queryKey: getGetWorkspaceDriftQueryKey(id) });
  invalidateWorkspace(qc, id);
}

/**
 * invalidateResourceChange is the one policy that does NOT touch the workspace:
 * writing an entity is DATA, not a configuration layer, and it moves no
 * revision (the same reason these routes sit in
 * autoCheckpointExcludedNeverTouchesLayer on the server). The family list goes
 * because the row above the table shows entityCount; `segment` is the
 * route_family path segment, so every open page of that family (prefix match)
 * is covered by the one key.
 */
export function invalidateResourceChange(qc: QueryClient, id: number, segment: string): void {
  void qc.invalidateQueries({ queryKey: getListResourceEntitiesQueryKey(id, segment) });
  void qc.invalidateQueries({ queryKey: getListWorkspaceResourcesQueryKey(id) });
}

import { createContext, useContext, useEffect, useState } from "react";
import type {
  BackendSavedViewResponse,
  BackendSavedViewState,
  BackendReadTarget,
} from "@/api/generated/schemas";
import type { BackendSourcePin } from "./backendFlowReads";
import type { BackendSavedViewSession } from "./useBackendSavedViewSession";
export type SavedViewState = BackendSavedViewState;
export type SavedViewSessionProps = {
  state: SavedViewState;
  onStateChange: (next: SavedViewState) => void;
};
export type SavedLayoutProps = {
  positions: SavedViewState["positions"];
  collapsedGroupIds: string[];
  onPositionsChange: (next: SavedViewState["positions"]) => void;
  onCollapsedGroupsChange: (ids: string[]) => void;
};
export function savedViewSourcePin(view: BackendSavedViewResponse): BackendSourcePin {
  const state = view.state;
  return {
    revisionId: view.pins.revisionId,
    viewId: view.id,
    viewVersion: view.version,
    ...state.scope,
    ...(state.selection
      ? { recordId: state.selection.id, recordType: state.selection.recordType }
      : {}),
  };
}
export function savedViewStateEqual(a: SavedViewState | undefined, b: SavedViewState | undefined) {
  const normalized = (state: SavedViewState | undefined) =>
    state
      ? {
          ...state,
          positions: [...state.positions].sort((x, y) => x.nodeId.localeCompare(y.nodeId)),
          collapsedGroupIds: [...state.collapsedGroupIds].sort(),
        }
      : undefined;
  return stableSavedValue(normalized(a)) === stableSavedValue(normalized(b));
}
function stableSavedValue(value: unknown) {
  return JSON.stringify(value, (_key, item) =>
    item && typeof item === "object" && !Array.isArray(item)
      ? Object.fromEntries(Object.entries(item).sort(([a], [b]) => a.localeCompare(b)))
      : item,
  );
}
export function sameSavedViewTarget(a: BackendReadTarget, b: BackendReadTarget) {
  return stableSavedValue(a) === stableSavedValue(b);
}
export function sameViewBinding(
  a: BackendReadTarget,
  stateA: SavedViewState,
  b: BackendReadTarget,
  stateB: SavedViewState,
) {
  return (
    sameSavedViewTarget(a, b) &&
    stateA.kind === stateB.kind &&
    (stateA.kind !== "database" ||
      stateB.kind !== "database" ||
      (stateA.scope.datastoreId === stateB.scope.datastoreId &&
        stateA.scope.facetKey === stateB.scope.facetKey))
  );
}
export const BackendSavedViewContext = createContext<BackendSavedViewSession | null>(null);
export function useWorkspaceSavedState<S extends SavedViewState>(
  target: BackendReadTarget,
  initial: S,
  externalIdentity?: string,
  documentVersion?: BackendSavedViewResponse["documentVersion"],
) {
  const session = useContext(BackendSavedViewContext);
  const [local, setLocal] = useState(initial);
  const [localPreview, setLocalPreview] = useState(false);
  const active =
    !!session?.state &&
    !!session.target &&
    sameViewBinding(session.target, session.state, target, initial);
  const [observedExternal, setObservedExternal] = useState(externalIdentity);
  const externalChanged = observedExternal !== externalIdentity;
  let restored = local;
  if (externalChanged) {
    setObservedExternal(externalIdentity);
    if (!active) {
      const changedFlow =
        local.kind === "flow" &&
        initial.kind === "flow" &&
        local.scope.flowId !== initial.scope.flowId;
      restored = {
        ...local,
        scope: initial.scope,
        selection: initial.selection,
        ...(changedFlow ? { positions: [], collapsedGroupIds: [] } : {}),
      };
      setLocal(restored);
    }
  }
  const state = (active ? session.state : restored) as S;
  useEffect(() => {
    if (
      session?.state?.kind === initial.kind &&
      session.target &&
      session.target.proposal &&
      target.proposal &&
      session.target.proposal.proposalId === target.proposal.proposalId &&
      session.target.proposal.proposalRevisionId !== target.proposal.proposalRevisionId
    )
      session.capture(target, initial, documentVersion);
  }, [session, target, initial, documentVersion]);
  function onStateChange(next: S) {
    setLocal(next);
    if (active) session?.setState(next);
  }
  return {
    state,
    onStateChange,
    capture: () => {
      if (!localPreview && !session?.preview) session?.capture(target, state, documentVersion);
    },
    captureDisabled: localPreview || !!session?.preview || !!session?.pending || !!session?.busy,
    canCapture: !!session,
    preview: (value: boolean) => {
      setLocalPreview(value);
      if (active) session?.setPreview(value);
    },
  };
}

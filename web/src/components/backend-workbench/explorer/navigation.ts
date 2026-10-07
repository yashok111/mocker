import { useCallback, useEffect, useLayoutEffect, useMemo, useState } from "react";
import { useLocation, useRouter } from "@tanstack/react-router";
import type { BackendReadTarget } from "@/api/generated/schemas";
import {
  parseBackendWorkspaceSearch,
  type BackendWorkspaceSearch,
} from "../backendWorkspaceSearch";
export type View = "structure" | "scenarios" | "data";
export type Camera = { x: number; y: number; zoom: number };
export type Presentation = { search: BackendWorkspaceSearch; camera?: Camera; scroll: number };
type Bookmarks = Record<string, Presentation>;
export const bookmarkKey = (project: string, target: string, view: View) =>
  JSON.stringify([project, target, view]);
export function viewOf(s: BackendWorkspaceSearch): View {
  return (
    s.wbView ??
    (s.flowId || s.entrypointId
      ? "scenarios"
      : s.dataNodeId || s.datastoreId
        ? "data"
        : "structure")
  );
}
export function rememberView(
  bookmarks: Bookmarks,
  project: string,
  target: string,
  p: Presentation,
) {
  if (!p.search.wbPanel)
    bookmarks[bookmarkKey(project, target, viewOf(p.search))] = structuredClone(p);
}
export function restoreView(bookmarks: Bookmarks, project: string, target: string, view: View) {
  const p = bookmarks[bookmarkKey(project, target, view)];
  return p ? structuredClone(p) : undefined;
}
export function restoreHistory(
  entry: Presentation | undefined,
  bookmark: Presentation | undefined,
) {
  return entry ? structuredClone(entry) : bookmark ? structuredClone(bookmark) : undefined;
}
export function resolvedTargetSearch(target: BackendReadTarget): BackendWorkspaceSearch {
  return target.revisionId
    ? { revisionId: target.revisionId }
    : target.changeProposal
      ? {
          changeProposalId: target.changeProposal.proposalId,
          proposalRevisionId: target.changeProposal.proposalRevisionId,
        }
      : { wbTarget: JSON.stringify(target) };
}
export function targetSearch(s: BackendWorkspaceSearch): BackendWorkspaceSearch {
  if (s.wbTarget) return { wbTarget: s.wbTarget };
  return s.changeProposalId
    ? { changeProposalId: s.changeProposalId, proposalRevisionId: s.proposalRevisionId }
    : { revisionId: s.revisionId };
}

// Camera changes are high-frequency imperative canvas events. Store them outside
// React rendering; route changes select immutable snapshots from this local store.
class Places {
  bookmarks: Bookmarks = {};
  history: Bookmarks = {};
  pending = new Map<string, Presentation>();
  constructor(
    readonly storageKey: string,
    readonly project: string,
  ) {
    try {
      const saved = JSON.parse(sessionStorage.getItem(storageKey) ?? "null");
      if (saved?.history && saved?.bookmarks) {
        this.history = saved.history;
        this.bookmarks = saved.bookmarks;
      }
    } catch {
      /* Private storage is optional. */
    }
  }
  resolve(key: string, search: BackendWorkspaceSearch): Presentation {
    const selected = restoreHistory(this.history[key], this.pending.get(JSON.stringify(search)));
    if (!selected || !Number.isFinite(selected.scroll)) return { search, scroll: 0 };
    const camera =
      selected.camera && Object.values(selected.camera).every(Number.isFinite)
        ? selected.camera
        : undefined;
    return { ...selected, search, camera };
  }
  enter(key: string, target: string, p: Presentation) {
    this.pending.delete(JSON.stringify(p.search));
    this.history[key] = structuredClone(p);
    rememberView(this.bookmarks, this.project, target, p);
    this.persist();
  }
  patch(key: string, target: string, patch: Partial<Presentation>) {
    const old = this.history[key];
    if (!old) return;
    this.enter(key, target, { ...old, ...patch });
  }
  prepare(
    key: string,
    target: string,
    next: BackendWorkspaceSearch,
    replace: boolean,
    p?: Presentation,
  ) {
    const old = this.history[key];
    if (old) rememberView(this.bookmarks, this.project, target, old);
    this.pending.set(
      JSON.stringify(next),
      p
        ? { ...structuredClone(p), search: next }
        : replace && old
          ? { ...structuredClone(old), search: next }
          : { search: next, scroll: 0 },
    );
    this.persist();
  }
  persist() {
    const keys = Object.keys(this.history);
    for (const key of keys.slice(0, Math.max(0, keys.length - 120))) delete this.history[key];
    try {
      sessionStorage.setItem(
        this.storageKey,
        JSON.stringify({ bookmarks: this.bookmarks, history: this.history }),
      );
    } catch {
      /* Browsing remains available without persistence. */
    }
  }
}
export function useExplorerNavigation(
  project: string,
  target: string,
  search: BackendWorkspaceSearch,
  onNavigate: (s: BackendWorkspaceSearch, replace?: boolean) => void,
  resolvedSearch: BackendWorkspaceSearch,
  routeSearch: BackendWorkspaceSearch = search,
) {
  const location = useLocation();
  const router = useRouter();
  const [places] = useState(() => new Places(`backend-workbench-places:${project}`, project));
  const historyKey = location.state.__TSR_key ?? location.href;
  const searchKey = JSON.stringify(search);
  // Router location changes before route props commit. Never store the old view
  // under the incoming history key during that intermediate render.
  const committed =
    JSON.stringify(parseBackendWorkspaceSearch(location.search)) ===
    JSON.stringify(parseBackendWorkspaceSearch(routeSearch));
  const presentation = useMemo(
    () => places.resolve(historyKey, JSON.parse(searchKey) as BackendWorkspaceSearch),
    [places, historyKey, searchKey],
  );
  useLayoutEffect(() => {
    if (committed) places.enter(historyKey, target, presentation);
  }, [places, historyKey, target, presentation, committed]);
  useEffect(() => {
    const save = () => places.persist();
    window.addEventListener("pagehide", save);
    return () => {
      save();
      window.removeEventListener("pagehide", save);
    };
  }, [places]);
  const go = useCallback(
    (next: BackendWorkspaceSearch, replace = false, p?: Presentation) => {
      places.prepare(historyKey, target, next, replace, p);
      onNavigate(next, replace);
    },
    [places, historyKey, target, onNavigate],
  );
  const switchView = useCallback(
    (view: View) => {
      const saved = restoreView(places.bookmarks, project, target, view);
      go(
        saved?.search ?? {
          ...resolvedSearch,
          wbView: view,
          wbMode: "unmapped",
          wbScope:
            search.wbScope ??
            search.entrypointId ??
            search.flowId ??
            search.datastoreId ??
            search.dataNodeId,
        },
        false,
        saved,
      );
    },
    [
      places,
      project,
      target,
      search.wbScope,
      search.entrypointId,
      search.flowId,
      search.datastoreId,
      search.dataNodeId,
      go,
      resolvedSearch,
    ],
  );
  return {
    go,
    switchView,
    back: () => {
      places.persist();
      router.history.back();
    },
    presentation,
    updateCamera: (camera: Camera) => committed && places.patch(historyKey, target, { camera }),
    updateScroll: (scroll: number) => committed && places.patch(historyKey, target, { scroll }),
  };
}

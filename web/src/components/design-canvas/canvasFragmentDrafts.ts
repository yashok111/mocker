import type { FormDraftStore } from "../api-designer/forms/formDraftStore";
import type { CanvasDocument } from "./types";

// Execution buffers are scoped by stable IDs, so structural edits must remove
// buffers whose editor no longer exists. Other pending fields stay untouched.
export function clearObsoleteFragmentDrafts(
  store: FormDraftStore,
  before: CanvasDocument,
  after: CanvasDocument,
): void {
  const remaining = new Map(after.fragments.map((fragment) => [fragment.id, fragment]));
  for (const fragment of before.fragments) {
    const prefix = `/canvas-fragment/${encodeURIComponent(fragment.id)}`;
    const current = remaining.get(fragment.id);
    if (!current || current.kind !== fragment.kind) {
      store.removeTree(prefix);
      continue;
    }
    const branchIDs = new Set(current.branches?.map((branch) => branch.id) ?? []);
    for (const branch of fragment.branches ?? []) {
      if (!branchIDs.has(branch.id))
        store.removeTree(`${prefix}/branch/${encodeURIComponent(branch.id)}`);
    }
  }
}

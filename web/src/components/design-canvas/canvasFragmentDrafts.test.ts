import { describe, expect, it } from "vitest";
import { createFormDraftStore } from "../api-designer/forms/formDraftStore";
import { clearObsoleteFragmentDrafts } from "./canvasFragmentDrafts";
import type { CanvasDocument } from "./types";

function fixture(): CanvasDocument {
  return {
    formatVersion: 2,
    title: "Flow",
    participants: [],
    messages: [],
    contracts: [],
    fragments: [
      { id: "parent", kind: "opt", label: "Parent", fromMessageId: "a", toMessageId: "b" },
      {
        id: "child",
        kind: "loop",
        label: "Child",
        fromMessageId: "a",
        toMessageId: "b",
        parentFragmentId: "parent",
      },
      { id: "other", kind: "opt", label: "Other", fromMessageId: "c", toMessageId: "d" },
    ],
  };
}

const draft = { source: "pending", propertySource: "saved" };

describe("fragment execution draft lifecycle", () => {
  it("clears removed parent and descendant drafts but preserves unrelated drafts", () => {
    const before = fixture();
    const after = { ...before, fragments: [before.fragments[2]!] };
    const store = createFormDraftStore();
    store.set("/canvas-fragment/parent/execution", draft);
    store.set("/canvas-fragment/child/execution", draft);
    store.set("/canvas-fragment/other/execution", draft);
    clearObsoleteFragmentDrafts(store, before, after);
    expect(store.get("/canvas-fragment/parent/execution")).toBeUndefined();
    expect(store.get("/canvas-fragment/child/execution")).toBeUndefined();
    expect(store.get("/canvas-fragment/other/execution")).toEqual(draft);
  });

  it("clears kind-inapplicable and removed-branch drafts", () => {
    const before = fixture();
    before.fragments[0] = {
      ...before.fragments[0]!,
      kind: "alt",
      branches: [
        { id: "kept", label: "K", fromMessageId: "a", toMessageId: "a" },
        { id: "removed", label: "R", fromMessageId: "b", toMessageId: "b" },
      ],
    };
    const store = createFormDraftStore();
    store.set("/canvas-fragment/parent/branch/kept/execution", draft);
    store.set("/canvas-fragment/parent/branch/removed/execution", draft);
    store.set("/canvas-fragment/child/execution", draft);
    const merged = {
      ...before,
      fragments: [
        { ...before.fragments[0]!, branches: [before.fragments[0]!.branches![0]!] },
        ...before.fragments.slice(1),
      ],
    };
    clearObsoleteFragmentDrafts(store, before, merged);
    expect(store.get("/canvas-fragment/parent/branch/removed/execution")).toBeUndefined();
    expect(store.get("/canvas-fragment/parent/branch/kept/execution")).toEqual(draft);
    const changed: CanvasDocument = {
      ...merged,
      fragments: [
        { ...merged.fragments[0]!, kind: "loop", branches: undefined },
        ...merged.fragments.slice(1),
      ],
    };
    clearObsoleteFragmentDrafts(store, merged, changed);
    expect(store.get("/canvas-fragment/parent/branch/kept/execution")).toBeUndefined();
    expect(store.get("/canvas-fragment/child/execution")).toEqual(draft);
  });
});

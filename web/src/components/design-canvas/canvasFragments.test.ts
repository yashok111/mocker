import { describe, expect, it } from "vitest";
import { addCanvasReply } from "./canvasQuickActions";
import {
  upgradeFragmentDocument,
  validateFragmentTree,
  setFragmentKind,
  splitFragmentBranch,
  mergeFragmentBranch,
  removeCanvasFragment,
  addCanvasFragment,
} from "./canvasFragments";
import { emptyCanvas, moveMessage, removeMessage } from "./canvasModel";
import { parseCanvas } from "./canvasStorage";
import type { CanvasDocument } from "./types";

export function branchingFixture(): CanvasDocument {
  return {
    ...emptyCanvas(),
    formatVersion: 2,
    participants: [{ id: "a", name: "A", kind: "service", description: "" }],
    messages: Array.from({ length: 6 }, (_, i) => ({
      id: `m${i}`,
      fromId: "a",
      toId: "a",
      kind: "event",
      label: `Step ${i}`,
      description: "",
    })),
    fragments: [
      {
        id: "outer",
        kind: "alt",
        label: "Outcome",
        fromMessageId: "m0",
        toMessageId: "m5",
        branches: [
          { id: "yes", label: "success", fromMessageId: "m0", toMessageId: "m2" },
          { id: "no", label: "else", fromMessageId: "m3", toMessageId: "m5" },
        ],
      },
      {
        id: "retry",
        kind: "loop",
        label: "3 attempts",
        fromMessageId: "m3",
        toMessageId: "m5",
        parentFragmentId: "outer",
        parentBranchId: "no",
      },
    ],
  };
}

describe("fragment trees", () => {
  it("round-trips nested alt/loop without changing branch ownership", () => {
    const document = branchingFixture();
    expect(parseCanvas(JSON.stringify(document))).toEqual(document);
  });
});

describe("v2 validation and editing", () => {
  it.each([
    (d: CanvasDocument) => {
      d.fragments[1]!.parentFragmentId = "missing";
    },
    (d: CanvasDocument) => {
      d.fragments[0]!.parentFragmentId = "retry";
    },
    (d: CanvasDocument) => {
      d.fragments[1]!.fromMessageId = "m2";
    },
    (d: CanvasDocument) => {
      delete d.fragments[1]!.parentBranchId;
    },
    (d: CanvasDocument) => {
      d.fragments[0]!.branches![1]!.fromMessageId = "m4";
    },
    (d: CanvasDocument) => {
      d.fragments[0]!.branches![1]!.id = "yes";
    },
    (d: CanvasDocument) => {
      d.fragments[0]!.branches!.pop();
    },
    (d: CanvasDocument) => {
      d.fragments.push({ ...d.fragments[1]!, id: "sibling" });
    },
    (d: CanvasDocument) => {
      d.formatVersion = 1;
    },
  ])("rejects invalid trees %#", (change) => {
    const document = branchingFixture();
    change(document);
    expect(() => parseCanvas(JSON.stringify(document))).toThrow();
  });
  it("migrates legacy equal and reversed ranges without mutating source", () => {
    const doc = branchingFixture();
    doc.formatVersion = 1;
    doc.fragments = [
      { id: "a", kind: "loop", label: "A", fromMessageId: "m5", toMessageId: "m0" },
      { id: "b", kind: "opt", label: "B", fromMessageId: "m0", toMessageId: "m5" },
    ];
    const old = structuredClone(doc);
    const upgraded = upgradeFragmentDocument(doc);
    expect(upgraded.formatVersion).toBe(2);
    expect(upgraded.fragments[1]!.parentFragmentId).toBe("a");
    expect(doc).toEqual(old);
    expect(() => validateFragmentTree(upgraded)).not.toThrow();
  });
  it("rejects crossing legacy frames without discarding them", () => {
    const doc = branchingFixture();
    doc.formatVersion = 1;
    doc.fragments = [
      { id: "a", kind: "loop", label: "A", fromMessageId: "m0", toMessageId: "m3" },
      { id: "b", kind: "opt", label: "B", fromMessageId: "m2", toMessageId: "m5" },
    ];
    expect(() => upgradeFragmentDocument(doc)).toThrow(/пересекаются/);
    expect(doc.fragments).toHaveLength(2);
  });
  it("keeps children when switching alt to loop and back", () => {
    const doc = setFragmentKind(branchingFixture(), "outer", "loop");
    expect(doc.fragments[1]!.parentBranchId).toBeUndefined();
    const alt = setFragmentKind(doc, "outer", "alt");
    expect(alt.fragments[1]!.parentBranchId).toBe(alt.fragments[0]!.branches![1]!.id);
    expect(() => validateFragmentTree(alt)).not.toThrow();
  });
  it("splits and merges branches while preserving child frames", () => {
    const doc = splitFragmentBranch(branchingFixture(), "outer", "yes");
    expect(doc.fragments[0]!.branches).toHaveLength(3);
    const merged = mergeFragmentBranch(doc, "outer", doc.fragments[0]!.branches![1]!.id);
    expect(merged.fragments[0]!.branches).toHaveLength(2);
    expect(merged.fragments[1]).toEqual(branchingFixture().fragments[1]);
  });
  it("removes only the chosen frame subtree, leaving all messages", () => {
    const doc = removeCanvasFragment(branchingFixture(), "outer");
    expect(doc.fragments).toEqual([]);
    expect(doc.messages).toHaveLength(6);
  });
  it("creates a child within the selected message's branch", () => {
    const doc = addCanvasFragment(branchingFixture(), { kind: "message", id: "m4" }, "child");
    expect(doc.fragments.at(-1)).toMatchObject({
      parentFragmentId: "retry",
      fromMessageId: "m4",
      toMessageId: "m5",
    });
  });
  it("blocks moving messages between branches even when ranges stay valid", () => {
    expect(() => moveMessage(branchingFixture(), "m1", 4)).toThrow(/ветк|блок/);
  });
  it("blocks deleting a branch boundary and keeps other deletion available", () => {
    expect(() => removeMessage(branchingFixture(), "m2")).toThrow(/границ/);
    expect(removeMessage(branchingFixture(), "m1").messages).toHaveLength(5);
  });
  it("inserts a reply at a branch end into the same branch", () => {
    const doc = branchingFixture();
    doc.messages[2]!.kind = "request";
    const next = addCanvasReply(doc, "m2", "reply");
    expect(next.fragments[0]!.branches![0]!.toMessageId).toBe("reply");
    expect(() => validateFragmentTree(next)).not.toThrow();
  });
});

it("removes legacy nested frames by migrating a copy", () => {
  const doc = branchingFixture();
  doc.formatVersion = 1;
  doc.fragments = [
    { id: "outer", kind: "loop", label: "A", fromMessageId: "m0", toMessageId: "m5" },
    { id: "inner", kind: "opt", label: "B", fromMessageId: "m1", toMessageId: "m4" },
  ];
  const result = removeCanvasFragment(doc, "outer");
  expect(result.fragments).toEqual([]);
  expect(result.formatVersion).toBe(2);
  expect(doc.fragments).toHaveLength(2);
  expect(result.messages).toEqual(doc.messages);
});

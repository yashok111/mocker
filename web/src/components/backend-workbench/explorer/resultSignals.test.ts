import { describe, it, expect } from "vitest";
import { changedResults } from "./resultSignals";
describe("agent result discovery", () => {
  it("uses the first read as a baseline", () =>
    expect(
      changedResults(undefined, [{ id: "a", kind: "check", name: "A", status: "completed" }]),
    ).toEqual([]));
  it("finds a proposal and completed job without a source-head change", () =>
    expect(
      changedResults(
        [{ id: "job", kind: "check", name: "A", status: "running" }],
        [
          { id: "job", kind: "check", name: "A", status: "completed", version: 1 },
          { id: "p", kind: "proposal", name: "B", revisionId: "r" },
        ],
      ),
    ).toEqual(["check:job", "proposal:p"]));
  it("does not call unchanged immutable results new", () => {
    const items = [{ id: "a", kind: "proposal", name: "A", revisionId: "r" }];
    expect(changedResults(items, structuredClone(items))).toEqual([]);
  });
});

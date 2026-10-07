import { describe, it, expect } from "vitest";
import {
  bookmarkKey,
  rememberView,
  restoreView,
  restoreHistory,
  type Presentation,
} from "./navigation";
const a: Presentation = {
  search: { revisionId: "r", wbView: "scenarios", flowId: "A", recordId: "step" },
  camera: { x: 40, y: 60, zoom: 1.2 },
  scroll: 200,
};
const b: Presentation = {
  search: { revisionId: "r", wbView: "data", dataNodeId: "B" },
  camera: { x: 0, y: 5, zoom: 0.8 },
  scroll: 0,
};
describe("workspace places", () => {
  it("restores each view independently and namespaces exact targets", () => {
    const bookmarks = {};
    rememberView(bookmarks, "project", "source:r", a);
    rememberView(bookmarks, "project", "source:r", b);
    expect(restoreView(bookmarks, "project", "source:r", "scenarios")).toEqual(a);
    expect(restoreView(bookmarks, "project", "source:new", "scenarios")).toBeUndefined();
    expect(bookmarkKey("other", "source:r", "scenarios")).not.toBe(
      bookmarkKey("project", "source:r", "scenarios"),
    );
  });
  it("history restores its own snapshot before a newer view bookmark", () => {
    const newer = { ...a, scroll: 999 };
    expect(restoreHistory(a, newer)).toEqual(a);
  });
  it("does not share mutable camera or search state", () => {
    const bookmarks = {};
    rememberView(bookmarks, "p", "r", a);
    const result = restoreView(bookmarks, "p", "r", "scenarios")!;
    result.camera!.x = 1000;
    expect(restoreView(bookmarks, "p", "r", "scenarios")!.camera!.x).toBe(40);
  });
});

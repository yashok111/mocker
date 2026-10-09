import { expect, it } from "vitest";
import { projectEntrySearch } from "./projectStartView";

it("opens the exact project start version only without an explicit destination", () => {
  const start = { kind: "diagram_view" as const, id: "start", version: 2 };
  expect(projectEntrySearch({}, start)).toEqual({ diagramViewId: "start", diagramViewVersion: 2 });
  expect(projectEntrySearch({ revisionId: "historic" }, start)).toEqual({ revisionId: "historic" });
  expect(projectEntrySearch({ wbMode: "overview" }, start)).toEqual({ wbMode: "overview" });
  expect(projectEntrySearch({}, { ...start, kind: "saved_view" })).toEqual({
    viewId: "start",
    viewVersion: 2,
  });
  expect(projectEntrySearch({}, undefined)).toEqual({});
});

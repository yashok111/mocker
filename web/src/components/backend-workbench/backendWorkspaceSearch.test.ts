import { expect, it } from "vitest";
import { parseBackendSourcePin } from "./backendFlowReads";
import {
  parseBackendWorkspaceSearch,
  reconcileDiagramPresentation,
  workspacePinError,
} from "./backendWorkspaceSearch";

const id = "10000000-0000-4000-8000-000000000001";
it("delegates legacy input unchanged", () => {
  const input = { revisionId: id, recordId: id, recordType: "node", viewId: id, viewVersion: "2" };
  expect(parseBackendWorkspaceSearch(input)).toEqual(parseBackendSourcePin(input));
});
it("retains exact diagram pin and rejects mixed selectors and unsafe versions", () => {
  const exact = {
    diagramId: id,
    diagramVersion: "2",
    diagramHash: "a".repeat(64),
    diagramLevel: "context",
    diagramRoot: id,
  };
  expect(workspacePinError(parseBackendWorkspaceSearch(exact))).toBeUndefined();
  expect(
    workspacePinError(
      parseBackendWorkspaceSearch({ ...exact, diagramVersion: "9007199254740993" }),
    ),
  ).toBeTruthy();
  expect(
    workspacePinError(
      parseBackendWorkspaceSearch({ ...exact, diagramViewId: id, diagramViewVersion: "1" }),
    ),
  ).toBeTruthy();
  expect(workspacePinError(parseBackendWorkspaceSearch({ diagramViewId: id }))).toBeTruthy();
});

it("keeps filters and layout for selection and Back on the same pin and root", () => {
  const state = {
    diagram: { id, version: 1, contentHash: "a".repeat(64) },
    level: "context" as const,
    rootId: id,
    search: "Orders",
    origin: "authored" as const,
    selection: null,
    positions: [{ id, x: 50, y: 60 }],
    collapsedIds: [id],
  };
  const selected = reconcileDiagramPresentation(state, {
    ...state,
    search: "",
    origin: "all",
    positions: [],
    collapsedIds: [],
    selection: { type: "element", id },
  });
  expect(selected).toEqual({ ...state, selection: { type: "element", id } });
  expect(
    reconcileDiagramPresentation(selected, {
      ...state,
      search: "",
      origin: "all",
      positions: [],
      collapsedIds: [],
    }),
  ).toEqual(state);
  expect(
    reconcileDiagramPresentation(state, {
      ...state,
      diagram: { ...state.diagram, version: 2 },
      search: "",
      positions: [],
    }).positions,
  ).toEqual([]);
});

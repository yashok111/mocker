import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderInRouter } from "@/test/render";
import { Workspace } from "./Workspace";
import { projectId, revisionId, nodeId, newerId, workspaceHTTP } from "./testFixtures";
import { readAccessMap } from "./accessMap";
import { readDatabaseMap } from "./databaseMap";

vi.mock("./ExploreCanvas", () => ({
  ExploreCanvas: ({ nodes }: { nodes: { id: string; name: string }[] }) => (
    <div>
      {nodes.map((n) => (
        <p key={n.id}>{n.name}</p>
      ))}
    </div>
  ),
}));
vi.mock("./accessMap", () => ({ readAccessMap: vi.fn() }));
vi.mock("./databaseMap", () => ({ readDatabaseMap: vi.fn() }));
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.clearAllMocks();
  sessionStorage.clear();
});

it.each(["reverse", "database"])(
  "loads the selected data object when switching A → B → A in %s",
  async (mode) => {
    workspaceHTTP();
    const reader = vi.mocked(mode === "reverse" ? readAccessMap : readDatabaseMap);
    reader.mockImplementation(async (_project, _target, search) => ({
      nodes: [
        {
          id: search.dataNodeId!,
          name: search.dataNodeId === nodeId ? "Relations A" : "Relations B",
          kind: "table",
          description: "",
          attributes: {},
          parentId: null,
          childCount: 0,
        },
      ],
      edges: [],
      total: 1,
      title: "Data",
      subtitle: "",
    }));
    function Journey() {
      const [dataNodeId, select] = useState(nodeId);
      return (
        <>
          <button onClick={() => select(newerId)}>B</button>
          <button onClick={() => select(nodeId)}>A</button>
          <Workspace
            projectId={projectId}
            sourcePin={{
              revisionId,
              wbView: "data",
              wbMode: "neighborhood",
              dataNodeId,
              ...(mode === "reverse"
                ? { wbFlowPart: "reverse" }
                : { datastoreId: projectId, facetKey: "declared" }),
            }}
          />
        </>
      );
    }
    renderInRouter(<Journey />);
    await screen.findByText("Relations A");
    await userEvent.click(screen.getByRole("button", { name: "B" }));
    await screen.findByText("Relations B");
    expect(reader.mock.calls.map((call) => call[2].dataNodeId)).toEqual([nodeId, newerId]);
    await userEvent.click(screen.getByRole("button", { name: "A" }));
    await screen.findByText("Relations A");
  },
);

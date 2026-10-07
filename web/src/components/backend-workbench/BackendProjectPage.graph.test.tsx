import { afterEach, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { BackendProjectPage } from "./BackendProjectPage";
import { renderInRouter } from "@/test/render";
import { projectId, revisionId, nodeId, workspaceHTTP } from "./explorer/testFixtures";
import { mapPage } from "./explorer/testFixtures";
import { json } from "@/test/http";
vi.mock("./explorer/ExploreCanvas", () => ({
  ExploreCanvas: ({
    nodes,
    onSelect,
    onEnter,
  }: {
    nodes: { id: string; name: string }[];
    onSelect: (id: string) => void;
    onEnter?: (node: { id: string; name: string }) => void;
  }) => (
    <div aria-label="Карта системы">
      {nodes.map((n) => (
        <button key={n.id} onClick={() => onSelect(n.id)} onDoubleClick={() => onEnter?.(n)}>
          {n.name}
        </button>
      ))}
    </div>
  ),
}));
afterEach(() => {
  vi.unstubAllGlobals();
  sessionStorage.clear();
  localStorage.clear();
});
it.each([false, true])(
  "opens a card's architecture scope by double click at its exact revision (list=%s)",
  async (wbList) => {
    workspaceHTTP();
    const navigate = vi.fn();
    renderInRouter(
      <BackendProjectPage
        projectId={projectId}
        sourcePin={{ revisionId, wbList }}
        onSourceNavigate={navigate}
      />,
    );
    const card = await screen.findByRole("button", { name: /Orders service/ });
    await userEvent.dblClick(card);
    const entries = navigate.mock.calls.filter(([s]) => s.wbMode === "architecture");
    expect(entries).toHaveLength(1);
    expect(entries[0]).toEqual([
      expect.objectContaining({
        revisionId,
        wbScope: nodeId,
        wbMode: "architecture",
        wbView: "structure",
      }),
      false,
    ]);
  },
);
it.each(["1", "2", "3", "4", "5"])(
  "opens schema%s through the bounded source overview without mounting authoring panels",
  async () => {
    const fetcher = workspaceHTTP();
    const navigate = vi.fn();
    renderInRouter(
      <BackendProjectPage
        projectId={projectId}
        sourcePin={{ revisionId }}
        onSourceNavigate={navigate}
      />,
    );
    expect(await screen.findByRole("button", { name: "Orders service" })).toBeVisible();
    expect(screen.getByRole("heading", { name: "Обзор из исходников" })).toBeInTheDocument();
    expect(
      screen.queryByRole("textbox", { name: /JSON|Название проекта/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Применить|Сохранить|Создать mapping/ }),
    ).not.toBeInTheDocument();
    const reads = fetcher.mock.calls.filter(([url]) => String(url).includes("/explore/query"));
    expect(reads).toHaveLength(1);
    expect(JSON.parse(String(reads[0]?.[1]?.body))).toMatchObject({
      target: { revisionId },
      mode: "overview",
      limit: 100,
    });
    expect(
      fetcher.mock.calls.some(
        ([url]) =>
          String(url).includes("/graph/query") ||
          String(url).includes("/flow/query") ||
          String(url).includes("/database/query"),
      ),
    ).toBe(false);
    await userEvent.click(screen.getByRole("button", { name: "Orders service" }));
    expect(navigate).toHaveBeenLastCalledWith(
      expect.objectContaining({ revisionId, recordId: nodeId }),
      true,
    );
  },
);
it("does not advance a historical source when current project metadata changes", async () => {
  workspaceHTTP();
  const navigate = vi.fn();
  renderInRouter(
    <BackendProjectPage
      projectId={projectId}
      sourcePin={{ revisionId }}
      onSourceNavigate={navigate}
    />,
  );
  await screen.findByRole("button", { name: "Orders service" });
  expect(screen.getByText("Доступна новая версия")).toBeVisible();
  await waitFor(() => expect(navigate).not.toHaveBeenCalled());
});
it.each(["column", "api_field", "query", "index", "constraint"])(
  "opens a catalog leaf's inspection context (%s)",
  async (kind) => {
    workspaceHTTP((path) =>
      path.endsWith("/explore/query")
        ? json(200, {
            ...mapPage(),
            nodes: [
              {
                id: nodeId,
                name: "Selected leaf",
                kind,
                parentId: null,
                attributes: {},
                description: "",
                childCount: 0,
              },
            ],
          })
        : undefined,
    );
    const navigate = vi.fn();
    renderInRouter(
      <BackendProjectPage
        projectId={projectId}
        sourcePin={{ revisionId, wbMode: "objects", wbKind: kind }}
        onSourceNavigate={navigate}
      />,
    );
    await userEvent.click(await screen.findByRole("button", { name: /Selected leaf/ }));
    await waitFor(() =>
      expect(navigate).toHaveBeenLastCalledWith(
        expect.objectContaining({
          revisionId,
          wbMode: "neighborhood",
          wbScope: nodeId,
          recordId: nodeId,
          recordType: "node",
        }),
        false,
      ),
    );
  },
);

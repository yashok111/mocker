import { afterEach, expect, it, vi } from "vitest";
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { SourceCatalog, restoreCatalog } from "./SourceCatalog";
afterEach(cleanup);
it("browses all jobs as searchable rows with their declared trigger", async () => {
  const open = vi.fn();
  renderWithProviders(
    <SourceCatalog
      data={{
        title: "Фоновые задачи",
        subtitle: "",
        total: 28,
        edges: [],
        nodes: Array.from({ length: 28 }, (_, i) => ({
          id: String(i),
          name: i === 0 ? "autochat" : `job-${i}`,
          kind: "job",
          parentId: null,
          childCount: 0,
          description: "",
          attributes: {
            trigger: { kind: "cron", expression: { status: "known", value: "*/10 8-19 * * *" } },
          },
        })),
      }}
      onOpen={open}
    />,
  );
  expect(screen.getAllByRole("button")).toHaveLength(28);
  expect(screen.queryByText("Следующие объекты")).not.toBeInTheDocument();
  const user = userEvent.setup();
  await user.type(screen.getByRole("textbox", { name: "Поиск в каталоге" }), "autochat");
  expect(screen.getAllByRole("button")).toHaveLength(1);
  await user.dblClick(screen.getByRole("button", { name: /autochat/ }));
  await waitFor(() => expect(open).toHaveBeenCalledTimes(1));
});
it("restores only catalog scope, never a target override from catalog state", () => {
  expect(
    restoreCatalog(
      JSON.stringify({ wbMode: "objects", wbKind: "job", revisionId: "other", wbScope: "service" }),
    ),
  ).toEqual({ wbMode: "objects", wbKind: "job", wbScope: "service" });
});

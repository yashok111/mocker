import { afterEach, expect, it, vi } from "vitest";
import { cleanup, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { ArchitectureScope } from "./ArchitectureScope";
import { readArchitectureChoices } from "./architectureReads";
import { projectId, revisionId } from "./testFixtures";
vi.mock("./architectureReads", () => ({ readArchitectureChoices: vi.fn() }));
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});
it("shows every primary and nested map in one searchable list without pagination", async () => {
  const entries = Array.from({ length: 12 }, (_, i) => ({
    key: `view-${i}`,
    diagramId: `diagram-${i}`,
    name: `Область ${i + 1}`,
    description: "Основная схема",
    search: { diagramViewId: `view-${i}`, diagramViewVersion: 1 },
  }));
  const nested = {
    key: "nested",
    diagramId: "nested",
    name: "Литрес — Операции и сценарии области",
    description: "Компоненты",
    nested: true,
    search: { diagramId: "nested", diagramLevel: "components" as const },
  };
  vi.mocked(readArchitectureChoices).mockResolvedValue([...entries, nested]);
  const navigate = vi.fn();
  renderWithProviders(
    <ArchitectureScope projectId={projectId} target={{ revisionId }} onNavigate={navigate} />,
  );
  const main = await screen.findByRole("region", { name: "Список схем" });
  expect(within(main).getAllByRole("button")).toHaveLength(13);
  expect(screen.getByRole("button", { name: /Литрес/ })).toBeVisible();
  expect(screen.queryByRole("button", { name: "Далее" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Открыть исходники" })).not.toBeInTheDocument();
  expect(main.closest("details")).toBeNull();
  await userEvent.type(screen.getByRole("textbox", { name: "Поиск схем" }), "Литрес");
  const result = screen.getByRole("button", { name: /Литрес/ });
  expect(result).toBeVisible();
  await userEvent.click(result);
  expect(navigate).toHaveBeenCalledWith(nested.search);
  await userEvent.clear(screen.getByRole("textbox", { name: "Поиск схем" }));
  expect(screen.getByRole("button", { name: "Область 1 Основная схема" })).toBeVisible();
});

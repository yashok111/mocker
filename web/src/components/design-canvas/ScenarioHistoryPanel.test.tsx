import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json, route } from "@/test/http";
import { emptyCanvas } from "./canvasModel";
import { ScenarioHistoryPanel } from "./ScenarioHistoryPanel";
import type { DesignScenarioRevision } from "./designScenarioApi";

// X6 needs browser SVG geometry; its interactions are covered in SequenceGraph tests
// and the real preview. Keep the query, comparison, and restore flows real here.
vi.mock("./SequenceGraph", () => ({ default: () => null }));

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function revision(version: number, title: string): DesignScenarioRevision {
  return {
    id: 40 + version,
    scenarioId: 12,
    version,
    hash: `hash-${version}`,
    source: version === 1 ? "mcp" : "ui",
    summary: version === 1 ? "Создан сценарий" : "Обновлено название",
    createdAt: 1_700_000_000 + version,
    document: { ...emptyCanvas(), title },
    formDrafts: {},
  };
}

const old = revision(1, "Заказ");
const current = revision(2, "Оплата");

function props() {
  return {
    opened: true,
    onClose: vi.fn(),
    scenarioId: 12,
    currentRevisionId: 42,
    baseVersion: 2,
    revisions: [current, old],
    dirty: false,
    onRestored: vi.fn(),
  };
}

function readRoutes() {
  return {
    "GET /api/design-scenarios/12/revisions/41": () => json(200, old),
    "GET /api/design-scenarios/12/revisions/42": () => json(200, current),
  };
}

describe("ScenarioHistoryPanel", () => {
  it("opens the last saved change with named before/after snapshots and readable fields", async () => {
    const fetch = route(readRoutes());
    renderWithProviders(<ScenarioHistoryPanel {...props()} />);

    expect(await screen.findByRole("region", { name: "Было: версия 1" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Стало: версия 2" })).toBeInTheDocument();
    const changes = screen.getByRole("region", { name: "Изменения версий" });
    expect(within(changes).getByRole("cell", { name: "Заказ" })).toBeInTheDocument();
    expect(within(changes).getByRole("cell", { name: "Оплата" })).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Было" })).toHaveValue("41");
    expect(screen.getByRole("combobox", { name: "Стало" })).toHaveValue("42");
    expect(fetch.mock.calls.every(([, init]) => !init?.method || init.method === "GET")).toBe(true);
  });

  it("compares the chosen pair including equal versions without a stuck loader", async () => {
    route(readRoutes());
    renderWithProviders(<ScenarioHistoryPanel {...props()} />);
    await screen.findByRole("region", { name: "Было: версия 1" });
    fireEvent.change(screen.getByRole("combobox", { name: "Стало" }), { target: { value: "41" } });
    expect(await screen.findByText("Различий нет.")).toBeInTheDocument();
    expect(screen.queryByLabelText("Загружаем версии")).not.toBeInTheDocument();
    fireEvent.change(screen.getByRole("combobox", { name: "Было" }), { target: { value: "42" } });
    const changes = await screen.findByRole("region", { name: "Изменения версий" });
    expect(within(changes).getByText("Оплата")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Восстановить версию 2" })).toBeDisabled();
  });

  it("shows one revision as an equal pair and disables restoring the current snapshot", async () => {
    route(readRoutes());
    renderWithProviders(<ScenarioHistoryPanel {...props()} revisions={[current]} />);
    expect(await screen.findByText("Различий нет.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Восстановить версию 2" })).toBeDisabled();
    expect(screen.queryByLabelText("Загружаем версии")).not.toBeInTheDocument();
  });

  it("shows read errors instead of an empty comparison and allows retry", async () => {
    let failed = true;
    route({
      ...readRoutes(),
      "GET /api/design-scenarios/12/revisions/41": () =>
        failed
          ? json(500, { error: { code: "internal", message: "revision unavailable" } })
          : json(200, old),
    });
    renderWithProviders(<ScenarioHistoryPanel {...props()} />);
    expect(await screen.findByRole("alert")).toHaveTextContent("revision unavailable");
    expect(screen.queryByText("Различий нет.")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Восстановить версию 1" })).toBeDisabled();
    failed = false;
    await userEvent.click(screen.getByRole("button", { name: "Повторить загрузку" }));
    expect(await screen.findByRole("region", { name: "Было: версия 1" })).toBeInTheDocument();
  });

  it("guards local changes and restores the selected before version through CAS", async () => {
    const restored = {
      scenario: { version: 3 },
      draft: old,
      revisions: [],
      diagnostics: [],
      contractUpdates: [],
    };
    const fetch = route({
      ...readRoutes(),
      "POST /api/design-scenarios/12/restore": () => json(200, restored),
    });
    const handlers = props();
    const confirm = vi.fn(() => false);
    vi.stubGlobal("confirm", confirm);
    renderWithProviders(<ScenarioHistoryPanel {...handlers} dirty />);
    await screen.findByRole("region", { name: "Было: версия 1" });
    expect(screen.getByText(/Несохранённые правки не включены/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Восстановить версию 1" }));
    expect(fetch.mock.calls.some(([, init]) => init?.method === "POST")).toBe(false);
    confirm.mockReturnValue(true);
    await userEvent.click(screen.getByRole("button", { name: "Восстановить версию 1" }));
    await waitFor(() => expect(handlers.onRestored).toHaveBeenCalledWith(restored));
    const write = fetch.mock.calls.find(([, init]) => init?.method === "POST");
    expect(JSON.parse(String(write?.[1]?.body))).toMatchObject({
      expectedVersion: 2,
      revisionId: 41,
    });
    expect(handlers.onClose).toHaveBeenCalledOnce();
  });

  it("shows an empty history without issuing invalid revision requests", () => {
    const fetch = route({});
    renderWithProviders(<ScenarioHistoryPanel {...props()} revisions={[]} />);
    expect(screen.getByText("Сохранённых версий пока нет.")).toBeInTheDocument();
    expect(fetch).not.toHaveBeenCalled();
  });
});

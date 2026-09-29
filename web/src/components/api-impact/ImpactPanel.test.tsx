import { afterEach, describe, expect, it, vi } from "vitest";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json, route } from "@/test/http";
import ImpactPanel, { type ImpactPanelProps } from "./ImpactPanel";
import { reportFixture } from "./fixtures.test-support";

vi.mock("./ImpactGraph", () => ({ default: () => <div data-testid="impact-graph" /> }));
afterEach(() => vi.unstubAllGlobals());
const props: ImpactPanelProps = {
  designId: 12,
  document: '{"value":null}',
  baseDocument: '{"value":9007199254740993}',
  baseRevisionId: 41,
  revisions: [
    { id: 41, version: 1 },
    { id: 45, version: 2 },
  ],
  pendingForm: false,
  pendingLayout: false,
  sourceError: null,
  onSource: vi.fn(),
  onScenario: vi.fn(),
};

describe("ImpactPanel", () => {
  it("shows explanations and exact values, hides harmless metadata and keeps risky identity changes", async () => {
    const fetch = route({ "POST /api/designs/12/impact": () => json(200, reportFixture()) });
    renderWithProviders(<ImpactPanel {...props} />);
    expect(fetch).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "Проанализировать" }));
    await screen.findByText("Изменено поле общей схемы");
    expect(screen.queryByText("Изменено описание")).not.toBeInTheDocument();
    expect(screen.getByText("Изменилась привязка")).toBeInTheDocument();
    expect(screen.getByText("9007199254740993")).toBeInTheDocument();
    expect(screen.getByText("null")).toBeInTheDocument();
    expect(screen.getByText(/Копия контракта могла быть изменена/)).toBeInTheDocument();
    expect(screen.getByText(/Ревизия контракта шага отличается/)).toBeInTheDocument();
    await userEvent.click(screen.getByLabelText("Показать совместимые метаданные"));
    expect(screen.getByText("Изменено описание")).toBeInTheDocument();
  });

  it("opens removed content on the before side of the exact local snapshots", async () => {
    route({ "POST /api/designs/12/impact": () => json(200, reportFixture()) });
    renderWithProviders(<ImpactPanel {...props} />);
    await userEvent.click(screen.getByRole("button", { name: "Проанализировать" }));
    await userEvent.click(await screen.findByRole("button", { name: "Сравнить изменение" }));
    const before = screen.getByLabelText<HTMLTextAreaElement>("Было");
    expect(before).toHaveValue(props.baseDocument);
    expect(before).toHaveFocus();
    expect(screen.getByLabelText("Стало")).toHaveValue(props.document);
  });

  it("allows historical analysis despite unapplied forms and fetches only its exact diff pair", async () => {
    const fetch = route({
      "POST /api/designs/12/impact": () => json(200, { ...reportFixture(), toRevisionId: 45 }),
      "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=45": () =>
        json(200, {
          from: { document: '{"value":1}' },
          to: { document: '{"value":2}' },
          changes: [],
        }),
    });
    renderWithProviders(<ImpactPanel {...props} pendingForm />);
    expect(screen.getByRole("button", { name: "Проанализировать" })).toBeDisabled();
    await userEvent.click(screen.getByRole("radio", { name: "Ревизии" }));
    await userEvent.click(screen.getByRole("button", { name: "Проанализировать" }));
    await userEvent.click(await screen.findByRole("button", { name: "Сравнить изменение" }));
    await waitFor(() => expect(screen.getByLabelText("Было")).toHaveValue('{"value":1}'));
    expect(JSON.parse(String(fetch.mock.calls[0]![1]?.body))).toEqual({
      fromRevisionId: 41,
      toRevisionId: 45,
    });
    expect(screen.queryByRole("button", { name: "Исходник" })).not.toBeInTheDocument();
  });

  it("labels incomplete results and limits without claiming safety", async () => {
    const report = reportFixture();
    report.complete = false;
    report.coverage.truncatedReasons = ["references"];
    report.diagnostics = [
      {
        code: "external",
        severity: "warning",
        side: "after",
        pointer: "/value",
        message: "Внешняя ссылка не разрешена",
      },
    ];
    route({ "POST /api/designs/12/impact": () => json(200, report) });
    renderWithProviders(<ImpactPanel {...props} />);
    await userEvent.click(screen.getByRole("button", { name: "Проанализировать" }));
    expect(await screen.findByText("Результат неполный")).toBeInTheDocument();
    expect(screen.getByText("Внешняя ссылка не разрешена")).toBeInTheDocument();
    expect(screen.getByText(/Лимит ссылок/)).toBeInTheDocument();
  });

  it("shows empty state and retries request errors", async () => {
    let failed = true;
    const report = reportFixture();
    report.changes = [];
    report.affected = [];
    report.evidence = [];
    route({
      "POST /api/designs/12/impact": () =>
        failed ? json(500, { error: { code: "internal" } }) : json(200, report),
    });
    renderWithProviders(<ImpactPanel {...props} />);
    await userEvent.click(screen.getByRole("button", { name: "Проанализировать" }));
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    failed = false;
    await userEvent.click(screen.getByRole("button", { name: "Проанализировать" }));
    expect(await screen.findByText("Изменений нет")).toBeInTheDocument();
  });

  it("discards a late local result after switching to revision mode", async () => {
    let resolve!: (value: Response) => void;
    const fetch = vi.fn().mockImplementation(
      () =>
        new Promise<Response>((finish) => {
          resolve = finish;
        }),
    );
    vi.stubGlobal("fetch", fetch);
    renderWithProviders(<ImpactPanel {...props} />);
    await userEvent.click(screen.getByRole("button", { name: "Проанализировать" }));
    await userEvent.click(screen.getByRole("radio", { name: "Ревизии" }));
    expect(fetch.mock.calls[0]![1].signal.aborted).toBe(true);
    await act(async () => resolve(json(200, reportFixture())));
    expect(screen.queryByText("Изменено поле общей схемы")).not.toBeInTheDocument();
    expect(screen.queryByTestId("impact-graph")).not.toBeInTheDocument();
  });

  it("labels omitted excerpts separately and opens the current scenario with report provenance visible", async () => {
    const report = reportFixture();
    report.changes[0]!.beforeJSON = undefined;
    report.changes[0]!.beforeTruncated = true;
    const onScenario = vi.fn();
    route({ "POST /api/designs/12/impact": () => json(200, report) });
    renderWithProviders(<ImpactPanel {...props} onScenario={onScenario} />);
    await userEvent.click(screen.getByRole("button", { name: "Проанализировать" }));
    expect(await screen.findByText("Значение сокращено; откройте сравнение")).toBeInTheDocument();
    expect(screen.queryByText("Результат неполный")).not.toBeInTheDocument();
    expect(screen.getByText(/В отчёте показана ревизия 9/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Открыть текущий сценарий" }));
    expect(onScenario).toHaveBeenCalledWith(7);
  });

  it("identifies and opens scenario entities whose unresolved joins have diagnostics without evidence", async () => {
    const report = reportFixture();
    report.evidence = [];
    report.complete = false;
    report.diagnostics = [
      {
        code: "scenario_unknown",
        severity: "warning",
        side: "before",
        pointer: "/messages/0/operation",
        entityId: "scenario",
        message: "Привязку операции не удалось подтвердить",
      },
    ];
    const onScenario = vi.fn();
    route({ "POST /api/designs/12/impact": () => json(200, report) });
    renderWithProviders(<ImpactPanel {...props} onScenario={onScenario} />);
    await userEvent.click(screen.getByRole("button", { name: "Проанализировать" }));
    expect(await screen.findByText("Шаг сценария · Создать заказ")).toBeInTheDocument();
    expect(screen.getByText(/Покупка · ревизия сценария 9/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Открыть текущий сценарий" }));
    expect(onScenario).toHaveBeenCalledWith(7);
  });
});

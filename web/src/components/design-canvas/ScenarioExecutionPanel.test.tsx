import { useState, type ReactNode } from "react";
import { MantineProvider } from "@mantine/core";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ScenarioExecutionPanel } from "./ScenarioExecutionPanel";
import type { CanvasDocument, DataFlowAnalysis } from "./types";
import { defaultStepExecution } from "./canvasExecution";
import type { CanvasExecutionReport } from "./canvasExecution";
import type { ScenarioExecutionPanelProps } from "./ScenarioExecutionPanel";
import { ApiFailure } from "@/api/client";

afterEach(() => vi.unstubAllGlobals());

vi.mock("./SequenceGraph", () => ({
  default: ({
    document,
    executionStatuses,
    selection,
  }: {
    document: CanvasDocument;
    executionStatuses?: Record<string, string>;
    selection?: { kind: string; id: string } | null;
  }) => (
    <div data-testid="execution-graph">
      {document.title} {JSON.stringify(executionStatuses)}{" "}
      {selection ? `${selection.kind}:${selection.id}` : ""}
    </div>
  ),
}));

function renderPanel(ui: ReactNode) {
  return render(ui, {
    wrapper: ({ children }) => <MantineProvider env="test">{children}</MantineProvider>,
  });
}

function fixture(): CanvasDocument {
  return {
    formatVersion: 1,
    title: "Вход",
    participants: [
      { id: "client", name: "Клиент", kind: "client", description: "" },
      { id: "api", name: "API", kind: "service", description: "" },
    ],
    messages: [
      {
        id: "login",
        fromId: "client",
        toId: "api",
        kind: "request",
        label: "Войти",
        description: "",
        operation: { contractId: "auth", operationKey: "login" },
      },
    ],
    fragments: [],
    contracts: [
      {
        id: "auth",
        name: "Auth",
        mode: "linked",
        source: { designId: 3, revisionId: 8 },
        document: {
          openapi: "3.1.0",
          info: { title: "Auth", version: "1" },
          paths: {
            "/login": {
              post: {
                "x-mocker-canvas-operation-id": "login",
                responses: { "200": { description: "OK" } },
              },
            },
          },
        },
      },
    ],
  };
}

function successfulResponse() {
  return {
    scenarioRevisionId: 42,
    designId: 3,
    designRevisionId: 8,
    workspaceRevision: 7,
    method: "POST",
    path: "/login",
    status: 200,
    headers: { "content-type": "application/json" },
    body: '{"token":"abc"}',
    durationMs: 12,
  };
}

function report(
  id = "run-one",
  status: CanvasExecutionReport["status"] = "passed",
): CanvasExecutionReport {
  return {
    id,
    scenarioId: 12,
    revisionId: 42,
    version: 2,
    name: "",
    source: "ui",
    status,
    startedAt: 1000,
    ...(status === "running" ? {} : { finishedAt: 1012 }),
    document: fixture(),
    inputVariables: {},
    variables: {},
    steps: [
      {
        messageId: "login",
        status: status === "running" ? "running" : status,
        assertions: [],
        ...(status === "running" ? {} : { response: successfulResponse() }),
      },
    ],
  };
}

function props() {
  return {
    opened: true,
    onClose: vi.fn(),
    document: fixture(),
    revisionId: 42,
    version: 2,
    disabled: false,
    onChangeDocument: vi.fn(),
    runScenario: vi
      .fn<ScenarioExecutionPanelProps["runScenario"]>()
      .mockImplementation(async (input) => report(input.runId)),
    listRuns: vi.fn<ScenarioExecutionPanelProps["listRuns"]>().mockResolvedValue([]),
    getRun: vi
      .fn<ScenarioExecutionPanelProps["getRun"]>()
      .mockImplementation(async (id) => report(id)),
    cancelRun: vi
      .fn<ScenarioExecutionPanelProps["cancelRun"]>()
      .mockImplementation(async (id) => report(id, "cancelled")),
    getCoverage: vi
      .fn<NonNullable<ScenarioExecutionPanelProps["getCoverage"]>>()
      .mockResolvedValue({ revisionId: 42, runCount: 0, sampleLimit: 50, messages: [], paths: [] }),
  };
}

describe("ScenarioExecutionPanel", () => {
  it("runs a generated variant with its exact name and variables without saving settings", async () => {
    const handlers = props();
    const suggestTests = vi.fn().mockResolvedValue({
      revisionId: 42,
      runCount: 0,
      sampleLimit: 50,
      checkedCandidates: 1,
      truncated: false,
      unresolved: [],
      cases: [
        {
          id: "branch-test-1",
          name: "Отказ оплаты",
          variables: { paid: "no" },
          targets: [{ fragmentId: "payment", outcome: "skipped" }],
        },
      ],
    });
    renderPanel(<ScenarioExecutionPanel {...handlers} suggestTests={suggestTests} />);
    fireEvent.click(screen.getByRole("tab", { name: "Тесты для веток" }));
    expect(screen.queryByRole("button", { name: "Запустить" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Подобрать тесты" })).not.toBeInTheDocument();
    await screen.findByText("Отказ оплаты");
    fireEvent.click(screen.getByRole("button", { name: "Запустить вариант" }));
    await waitFor(() => expect(handlers.runScenario).toHaveBeenCalledTimes(1));
    expect(handlers.runScenario.mock.calls[0]![0]).toEqual({
      runId: expect.any(String),
      revisionId: 42,
      name: "Отказ оплаты",
      variables: { paid: "no" },
    });
    expect(handlers.runScenario.mock.calls[0]![0].runId).not.toBe("branch-test-1");
    expect(handlers.onChangeDocument).not.toHaveBeenCalled();
  });

  it("shows every repeated occurrence and revision coverage with diagram selection", async () => {
    const handlers = props();
    handlers.getCoverage = vi.fn().mockResolvedValue({
      revisionId: 42,
      runCount: 1,
      sampleLimit: 50,
      messages: [{ messageId: "login", attempted: 2, passed: 2, failed: 0, skipped: 0 }],
      paths: [],
    });
    handlers.listRuns.mockResolvedValue([report()]);
    handlers.getRun.mockResolvedValue({
      ...report(),
      steps: [
        { messageId: "login", occurrence: 1, status: "passed", assertions: [] },
        { messageId: "login", occurrence: 2, status: "passed", assertions: [] },
      ],
    });
    renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.click(screen.getByRole("tab", { name: /Результат/ }));
    expect(await screen.findByRole("button", { name: /Войти · повтор 2/ })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Войти · повтор 2/ }));
    expect(screen.getByText(/Повтор 2/)).toBeInTheDocument();
    expect(await screen.findByText(/Покрытие ревизии 42/)).toBeInTheDocument();
    expect(handlers.getCoverage).toHaveBeenCalledWith(42, expect.any(AbortSignal));
    fireEvent.click(screen.getByRole("button", { name: /Войти: 2 попыток/ }));
    expect(screen.getByTestId("execution-graph")).toHaveTextContent("message:login");
  });

  it("shows zero-hit coverage before any run and selects a fragment", async () => {
    const handlers = props();
    handlers.document.fragments = [
      {
        id: "loop",
        kind: "loop",
        label: "Повтор",
        fromMessageId: "login",
        toMessageId: "login",
        execution: { iterations: 2 },
      },
    ];
    handlers.getCoverage.mockResolvedValue({
      revisionId: 42,
      runCount: 0,
      sampleLimit: 50,
      messages: [{ messageId: "login", attempted: 0, passed: 0, failed: 0, skipped: 0 }],
      paths: [{ fragmentId: "loop", outcome: "taken", hits: 0 }],
    });
    renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.click(screen.getByRole("tab", { name: /Результат/ }));
    fireEvent.click(await screen.findByRole("button", { name: /Повтор · выполнено: не покрыто/ }));
    expect(screen.getByTestId("execution-graph")).toHaveTextContent("fragment:loop");
  });

  it("does not present saved coverage as coverage for an unsaved draft", async () => {
    const handlers = props();
    const result = renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.click(screen.getByRole("tab", { name: /Результат/ }));
    expect(await screen.findByText(/Прогонов 0 · максимум 50/)).toBeInTheDocument();
    result.rerender(<ScenarioExecutionPanel {...handlers} disabled />);
    expect(
      screen.getByText(/Сохраните черновик, чтобы увидеть покрытие этой ревизии/),
    ).toBeInTheDocument();
    expect(screen.queryByText(/Прогонов 0 · максимум 50/)).not.toBeInTheDocument();
  });

  it("clears historical run status when coverage focuses a newer revision with the same message ID", async () => {
    const handlers = props();
    handlers.getCoverage.mockResolvedValue({
      revisionId: 43,
      runCount: 0,
      sampleLimit: 50,
      messages: [{ messageId: "login", attempted: 0, passed: 0, failed: 0, skipped: 0 }],
      paths: [],
    });
    handlers.listRuns.mockResolvedValue([report()]);
    renderPanel(<ScenarioExecutionPanel {...handlers} revisionId={43} />);
    fireEvent.click(screen.getByRole("tab", { name: /Результат/ }));
    await waitFor(() =>
      expect(screen.getByTestId("execution-graph")).toHaveTextContent('"login":"passed"'),
    );
    fireEvent.click(await screen.findByRole("button", { name: /Войти: не покрыто/ }));
    expect(screen.getByTestId("execution-graph")).toHaveTextContent("message:login");
    expect(screen.getByTestId("execution-graph")).not.toHaveTextContent('"login":"passed"');
  });
  it("starts and displays a run without randomUUID on HTTP", async () => {
    vi.stubGlobal("crypto", { getRandomValues: crypto.getRandomValues.bind(crypto) });
    const handlers = props();
    renderPanel(<ScenarioExecutionPanel {...handlers} />);

    fireEvent.click(screen.getByRole("button", { name: "Запустить" }));

    expect(await screen.findByText("Прогон завершён")).toBeInTheDocument();
    expect(handlers.runScenario.mock.calls[0]![0].runId).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
    );
  });

  it("allows disabling a dangling request and running the remaining valid HTTP step", async () => {
    const handlers = props();
    const valid = handlers.document.messages[0]!;
    handlers.document.messages = [
      {
        ...valid,
        id: "broken",
        label: "Удалённый контракт",
        operation: { contractId: "removed", operationKey: "missing" },
      },
      valid,
    ];
    function Harness() {
      const [document, setDocument] = useState(handlers.document);
      return (
        <ScenarioExecutionPanel {...handlers} document={document} onChangeDocument={setDocument} />
      );
    }
    renderPanel(<Harness />);
    fireEvent.click(screen.getByRole("checkbox", { name: "Выполнять шаг" }));
    fireEvent.click(screen.getByRole("button", { name: "Сохранить настройки" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Запустить" })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: "Запустить" }));
    expect(await screen.findByText("Прогон завершён")).toBeInTheDocument();
    expect(handlers.runScenario).toHaveBeenCalledOnce();
    expect(handlers.runScenario).toHaveBeenCalledWith(
      expect.objectContaining({ revisionId: 42 }),
      expect.any(AbortSignal),
    );
  });

  it("saves valid configuration through the document and blocks running an unfinished form", async () => {
    const handlers = props();
    function Harness() {
      const [document, setDocument] = useState(handlers.document);
      return (
        <ScenarioExecutionPanel
          {...handlers}
          document={document}
          onChangeDocument={(next) => {
            handlers.onChangeDocument(next);
            setDocument(next);
          }}
        />
      );
    }
    renderPanel(<Harness />);
    fireEvent.click(screen.getByRole("button", { name: "Добавить переменную" }));
    fireEvent.change(screen.getByLabelText("Переменная 1: имя"), { target: { value: "token" } });
    fireEvent.change(screen.getByLabelText("Переменная 1: значение"), {
      target: { value: "secret" },
    });
    fireEvent.change(screen.getByLabelText("Ожидаемый статус"), { target: { value: "201" } });
    expect(screen.getByRole("button", { name: "Запустить" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Сохранить настройки" }));
    expect(handlers.onChangeDocument).toHaveBeenCalledWith(
      expect.objectContaining({
        execution: { variables: { token: "secret" } },
        messages: [
          expect.objectContaining({ execution: expect.objectContaining({ expectedStatus: 201 }) }),
        ],
      }),
    );
    expect(handlers.runScenario).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.getByRole("button", { name: "Запустить" })).toBeEnabled());
  });

  it("keeps invalid expected JSON editable and does not save or run it", () => {
    const handlers = props();
    renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.click(screen.getByRole("button", { name: "Добавить проверку" }));
    fireEvent.change(screen.getByLabelText("Ожидаемый JSON 1"), { target: { value: "{invalid" } });
    fireEvent.click(screen.getByRole("button", { name: "Сохранить настройки" }));
    expect(screen.getByRole("alert")).toHaveTextContent(/JSON/);
    expect(screen.getByLabelText("Ожидаемый JSON 1")).toHaveValue("{invalid");
    expect(screen.getByRole("button", { name: "Запустить" })).toBeDisabled();
    expect(handlers.onChangeDocument).not.toHaveBeenCalled();
  });

  it("runs the saved version, shows the response and retains its diagram after later edits", async () => {
    const handlers = props();
    const result = renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.click(screen.getByRole("button", { name: "Запустить" }));
    expect(await screen.findByText("Прогон завершён")).toBeInTheDocument();
    expect(handlers.runScenario).toHaveBeenCalledWith(
      expect.objectContaining({ revisionId: 42, runId: expect.any(String) }),
      expect.any(AbortSignal),
    );
    expect(screen.getByTestId("execution-graph")).toHaveTextContent('"login":"passed"');
    expect(screen.getByText('{"token":"abc"}')).toBeInTheDocument();
    expect(screen.getByText(/API #3.*ревизия 8.*мок 7/)).toBeInTheDocument();
    result.rerender(
      <ScenarioExecutionPanel
        {...handlers}
        version={3}
        revisionId={43}
        document={{ ...handlers.document, title: "Другой сценарий" }}
      />,
    );
    expect(screen.getByTestId("execution-graph")).toHaveTextContent("Вход");
    expect(screen.getByText(/Результат.*версии 2/)).toBeInTheDocument();
    result.rerender(<ScenarioExecutionPanel {...handlers} opened={false} />);
    expect(screen.queryByTestId("execution-graph")).not.toBeInTheDocument();
    result.rerender(<ScenarioExecutionPanel {...handlers} />);
    expect(screen.getByTestId("execution-graph")).toHaveTextContent('"login":"passed"');
    expect(handlers.runScenario).toHaveBeenCalledOnce();
  });

  it("cancels only its own server run on close and isolates editor undo", async () => {
    const handlers = props();
    handlers.runScenario.mockImplementation(async (input) => report(input.runId, "running"));
    handlers.getRun.mockImplementation(async (id) => report(id, "running"));
    const windowKey = vi.fn();
    window.addEventListener("keydown", windowKey);
    renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "z", ctrlKey: true });
    expect(windowKey).not.toHaveBeenCalled();
    window.removeEventListener("keydown", windowKey);
    fireEvent.click(screen.getByRole("button", { name: "Запустить" }));
    await screen.findByRole("button", { name: "Отменить запуск" });
    const id = handlers.runScenario.mock.calls[0]![0].runId;
    fireEvent.click(screen.getByRole("button", { name: "Закрыть запуск" }));
    await waitFor(() =>
      expect(handlers.cancelRun).toHaveBeenCalledWith(id, expect.any(AbortSignal)),
    );
    expect(handlers.onClose).toHaveBeenCalledOnce();
  });

  it("disables execution while autosave is pending or fragments cannot execute", () => {
    const handlers = props();
    const result = renderPanel(<ScenarioExecutionPanel {...handlers} disabled />);
    expect(screen.getByRole("button", { name: "Запустить" })).toBeDisabled();
    const document = fixture();
    document.fragments = [
      {
        id: "loop",
        kind: "loop",
        label: "Повторить",
        fromMessageId: "login",
        toMessageId: "login",
      },
    ];
    result.rerender(<ScenarioExecutionPanel {...handlers} document={document} />);
    expect(screen.getByRole("button", { name: "Запустить" })).toBeDisabled();
    expect(screen.getByText(/задайте число повторений блока/i)).toBeInTheDocument();
  });

  it("keeps local settings during an API pin update and merges into the latest document", () => {
    const handlers = props();
    const result = renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.change(screen.getByLabelText("Тело запроса"), { target: { value: "local body" } });
    const latest = fixture();
    latest.title = "Новое название";
    latest.contracts[0]!.source!.revisionId = 99;
    result.rerender(
      <ScenarioExecutionPanel {...handlers} document={latest} version={3} revisionId={43} />,
    );
    expect(screen.getByLabelText("Тело запроса")).toHaveValue("local body");
    fireEvent.click(screen.getByRole("button", { name: "Сохранить настройки" }));
    expect(handlers.onChangeDocument).toHaveBeenCalledWith(
      expect.objectContaining({
        title: "Новое название",
        contracts: [
          expect.objectContaining({ source: expect.objectContaining({ revisionId: 99 }) }),
        ],
        messages: [
          expect.objectContaining({ execution: expect.objectContaining({ body: "local body" }) }),
        ],
      }),
    );
  });

  it("protects local input when execution settings change elsewhere", () => {
    const handlers = props();
    const result = renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.change(screen.getByLabelText("Тело запроса"), { target: { value: "local body" } });
    const latest = fixture();
    latest.execution = { variables: { token: "remote" } };
    result.rerender(<ScenarioExecutionPanel {...handlers} document={latest} />);
    expect(screen.getByLabelText("Тело запроса")).toHaveValue("local body");
    expect(screen.getByRole("button", { name: "Сохранить настройки" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Запустить" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Сбросить локальный ввод" }));
    expect(screen.getByLabelText("Тело запроса")).toHaveValue("");
    expect(screen.getByLabelText("Переменная 1: значение")).toHaveValue("remote");
  });

  it("does not add unchanged default step settings when only variables were edited", () => {
    const handlers = props();
    renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.click(screen.getByRole("button", { name: "Добавить переменную" }));
    fireEvent.change(screen.getByLabelText("Переменная 1: имя"), { target: { value: "token" } });
    fireEvent.change(screen.getByLabelText("Переменная 1: значение"), {
      target: { value: "value" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Сохранить настройки" }));
    const updated = handlers.onChangeDocument.mock.calls[0]![0] as CanvasDocument;
    expect(updated.messages[0]!.execution).toBeUndefined();
    expect(updated.execution).toEqual({ variables: { token: "value" } });
  });

  it("sends explicit cancellation when the page unmounts", async () => {
    const handlers = props();
    handlers.runScenario.mockImplementation(async (input) => report(input.runId, "running"));
    handlers.getRun.mockImplementation(async (id) => report(id, "running"));
    const result = renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.click(screen.getByRole("button", { name: "Запустить" }));
    await screen.findByRole("button", { name: "Отменить запуск" });
    const id = handlers.runScenario.mock.calls[0]![0].runId;
    await act(async () => result.unmount());
    expect(handlers.cancelRun).toHaveBeenCalledWith(id, expect.any(AbortSignal));
  });

  it("loads persisted MCP reports without taking ownership or cancelling on close", async () => {
    const handlers = props();
    const fromAgent = {
      ...report("agent-run", "running"),
      source: "mcp" as const,
      name: "Гость",
      document: { ...fixture(), title: "Снимок агента" },
      version: 1,
      revisionId: 41,
    };
    handlers.listRuns.mockResolvedValue([fromAgent]);
    handlers.getRun.mockResolvedValue(fromAgent);
    renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.click(screen.getByRole("tab", { name: "Результат" }));
    const selector = await screen.findByRole("combobox", { name: "Сохранённый прогон" });
    await waitFor(() => expect(selector).toHaveValue("agent-run"));
    fireEvent.click(screen.getByRole("tab", { name: "Результат" }));
    expect(await screen.findByTestId("execution-graph")).toHaveTextContent("Снимок агента");
    expect(screen.getByText(/Результат.*версии 1/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Отменить запуск" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Закрыть запуск" }));
    expect(handlers.cancelRun).not.toHaveBeenCalled();
    expect(handlers.runScenario).not.toHaveBeenCalled();
  });

  it("recovers a lost create response by its known run id without replaying requests", async () => {
    const handlers = props();
    handlers.runScenario.mockRejectedValue(new Error("Ответ потерян"));
    renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.click(screen.getByRole("button", { name: "Запустить" }));
    expect(await screen.findByText("Прогон завершён")).toBeInTheDocument();
    const id = handlers.runScenario.mock.calls[0]![0].runId;
    expect(handlers.getRun).toHaveBeenCalledWith(id, expect.any(AbortSignal));
    expect(handlers.runScenario).toHaveBeenCalledOnce();
  });

  it("lets the user recover an offline start with the same input after closing and a revision update", async () => {
    const handlers = props();
    let created = false;
    const missing = new Error("Прогон не найден", {
      cause: new ApiFailure("not found", 404, "not_found"),
    });
    handlers.runScenario.mockImplementationOnce(async () => {
      throw new TypeError("Failed to fetch");
    });
    handlers.runScenario.mockImplementation(async (input) => {
      created = true;
      return report(input.runId);
    });
    handlers.getRun.mockImplementation(async (id) => {
      if (!created) throw missing;
      return report(id);
    });
    handlers.cancelRun.mockRejectedValue(missing);
    const result = renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.click(screen.getByRole("button", { name: "Запустить" }));
    expect(await screen.findByRole("button", { name: "Повторить запрос запуска" })).toBeEnabled();
    const savedInput = handlers.runScenario.mock.calls[0]![0];
    expect(savedInput.revisionId).toBe(42);
    expect(handlers.runScenario).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole("button", { name: "Закрыть запуск" }));
    result.rerender(<ScenarioExecutionPanel {...handlers} opened={false} />);
    await waitFor(() => expect(handlers.cancelRun).toHaveBeenCalled());
    result.rerender(
      <ScenarioExecutionPanel
        {...handlers}
        revisionId={43}
        version={3}
        document={{ ...handlers.document, title: "Изменён после сбоя" }}
      />,
    );
    fireEvent.click(await screen.findByRole("button", { name: "Повторить запрос запуска" }));
    expect(await screen.findByText("Прогон завершён")).toBeInTheDocument();
    expect(handlers.runScenario).toHaveBeenCalledTimes(2);
    expect(handlers.runScenario.mock.calls[1]![0]).toEqual(savedInput);
    expect(screen.getByRole("button", { name: "Запустить" })).toBeEnabled();
    expect(
      screen.queryByRole("button", { name: "Повторить запрос запуска" }),
    ).not.toBeInTheDocument();
    expect(screen.getByText(/Результат.*версии 2/)).toBeInTheDocument();
  });

  it("polls a running report until terminal and displays JSON assertion numbers exactly", async () => {
    const handlers = props();
    handlers.runScenario.mockImplementation(async (input) => report(input.runId, "running"));
    handlers.getRun.mockImplementation(async (id) => ({
      ...report(id),
      steps: [
        {
          ...report(id).steps[0]!,
          assertions: [
            {
              pointer: "/id",
              expectedJson: "9007199254740993",
              actualJson: "9007199254740993",
              passed: true,
            },
          ],
        },
      ],
    }));
    renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.click(screen.getByRole("button", { name: "Запустить" }));
    expect(await screen.findByText("Прогон завершён", {}, { timeout: 2500 })).toBeInTheDocument();
    expect(screen.getByText(/ожидалось 9007199254740993/)).toBeInTheDocument();
    expect(handlers.getRun).toHaveBeenCalled();
    expect(handlers.runScenario).toHaveBeenCalledOnce();
  });

  it("repeats cancellation after a late create response when closed before creation completed", async () => {
    const handlers = props();
    let resolve!: (value: CanvasExecutionReport) => void;
    handlers.runScenario.mockImplementation(
      () =>
        new Promise((done) => {
          resolve = done;
        }),
    );
    handlers.cancelRun.mockRejectedValueOnce(new Error("Прогон ещё не создан"));
    const result = renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.click(screen.getByRole("button", { name: "Запустить" }));
    const id = handlers.runScenario.mock.calls[0]![0].runId;
    fireEvent.click(screen.getByRole("button", { name: "Закрыть запуск" }));
    result.unmount();
    await act(async () => resolve(report(id, "running")));
    await waitFor(() => expect(handlers.cancelRun).toHaveBeenCalledTimes(2));
    expect(handlers.cancelRun.mock.calls.every(([runId]) => runId === id)).toBe(true);
  });

  it("follows new MCP runs through polling until the user explicitly selects an older report", async () => {
    const handlers = props();
    const first = { ...report("agent-first"), source: "mcp" as const };
    const second = { ...report("agent-second"), source: "mcp" as const, startedAt: 2000 };
    const third = { ...report("agent-third"), source: "mcp" as const, startedAt: 3000 };
    let list = [first];
    handlers.listRuns.mockImplementation(async () => list);
    handlers.getRun.mockImplementation(async (id) =>
      [first, second, third].find((item) => item.id === id)!,
    );
    renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.click(screen.getByRole("tab", { name: "Результат" }));
    const selector = screen.getByRole("combobox", { name: "Сохранённый прогон" });
    await waitFor(() => expect(selector).toHaveValue(first.id));
    list = [second, first];
    await waitFor(() => expect(selector).toHaveValue(second.id), { timeout: 3000 });
    fireEvent.change(selector, { target: { value: first.id } });
    list = [third, second, first];
    fireEvent.click(screen.getByRole("button", { name: "Обновить отчёт" }));
    await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(3));
    expect(selector).toHaveValue(first.id);
    expect(handlers.runScenario).not.toHaveBeenCalled();
  });

  it("announces new MCP activity while preserving an unfinished settings form", async () => {
    const handlers = props();
    const agentRun = {
      ...report("agent-live", "running"),
      source: "mcp" as const,
      name: "Проверка гостя",
    };
    let list: CanvasExecutionReport[] = [];
    handlers.listRuns.mockImplementation(async () => list);
    handlers.getRun.mockResolvedValue(agentRun);
    renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.change(screen.getByLabelText("Тело запроса"), { target: { value: "unfinished" } });
    list = [agentRun];
    expect(
      await screen.findByRole("status", { name: /Прогон MCP: Проверка гостя/ }, { timeout: 3000 }),
    ).toHaveTextContent("Выполняется");
    expect(screen.getByRole("tab", { name: "Настройка" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByLabelText("Тело запроса")).toHaveValue("unfinished");
  });

  it("cancels its own run even when an MCP report is currently selected", async () => {
    const handlers = props();
    const fromAgent = { ...report("agent-past"), source: "mcp" as const };
    handlers.listRuns.mockResolvedValue([fromAgent]);
    handlers.runScenario.mockImplementation(async (input) => report(input.runId, "running"));
    handlers.getRun.mockImplementation(async (id) =>
      id === fromAgent.id ? fromAgent : report(id, "running"),
    );
    renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.click(screen.getByRole("button", { name: "Запустить" }));
    await screen.findByRole("button", { name: "Отменить запуск" });
    const ownId = handlers.runScenario.mock.calls[0]![0].runId;
    await waitFor(() =>
      expect(
        screen
          .getAllByRole("option")
          .some((item) => (item as HTMLOptionElement).value === fromAgent.id),
      ).toBe(true),
    );
    fireEvent.change(screen.getByRole("combobox", { name: "Сохранённый прогон" }), {
      target: { value: fromAgent.id },
    });
    fireEvent.click(screen.getByRole("button", { name: "Закрыть запуск" }));
    await waitFor(() =>
      expect(handlers.cancelRun).toHaveBeenCalledWith(ownId, expect.any(AbortSignal)),
    );
    expect(handlers.cancelRun.mock.calls.every(([id]) => id !== fromAgent.id)).toBe(true);
  });

  it("releases a rejected creation without claiming ownership or replaying it", async () => {
    const handlers = props();
    handlers.runScenario.mockRejectedValue(
      new Error("Сценарий уже выполняется", { cause: new ApiFailure("busy", 409, "conflict") }),
    );
    handlers.getRun.mockRejectedValue(new Error("not found"));
    renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.click(screen.getByRole("button", { name: "Запустить" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Запустить" })).toBeEnabled());
    expect(handlers.runScenario).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole("button", { name: "Закрыть запуск" }));
    expect(handlers.cancelRun).not.toHaveBeenCalled();
  });
});

describe("execution data bindings", () => {
  it("keeps legacy runs available during catalog analysis and after a catalog budget error", async () => {
    const handlers = props();
    let resolveAnalysis!: (analysis: DataFlowAnalysis) => void;
    const analyzeDataFlow = vi
      .fn<NonNullable<ScenarioExecutionPanelProps["analyzeDataFlow"]>>()
      .mockImplementation(
        () =>
          new Promise((resolve) => {
            resolveAnalysis = resolve;
          }),
      );
    renderPanel(<ScenarioExecutionPanel {...handlers} analyzeDataFlow={analyzeDataFlow} />);
    await waitFor(() => expect(analyzeDataFlow).toHaveBeenCalled());
    expect(screen.getByRole("button", { name: "Запустить" })).toBeEnabled();
    await act(async () =>
      resolveAnalysis({
        messages: [],
        bindings: [],
        diagnostics: [
          { pointer: "/contracts", severity: "error", message: "schema analysis budget exhausted" },
        ],
      }),
    );
    expect(screen.getByText("Ошибка: schema analysis budget exhausted")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Запустить" })).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: "Запустить" }));
    await waitFor(() => expect(handlers.runScenario).toHaveBeenCalled());
  });

  it("keeps legacy runs available when the optional catalog request fails", async () => {
    const analyzeDataFlow = vi
      .fn<NonNullable<ScenarioExecutionPanelProps["analyzeDataFlow"]>>()
      .mockRejectedValue(new Error("offline"));
    renderPanel(<ScenarioExecutionPanel {...props()} analyzeDataFlow={analyzeDataFlow} />);
    await screen.findByRole("button", { name: "Повторить анализ" });
    expect(screen.getByRole("button", { name: "Запустить" })).toBeEnabled();
  });

  const binding = {
    id: "token",
    sourceMessageId: "login",
    sourcePointer: "/token",
    target: { kind: "header" as const, name: "Authorization" },
    prefix: "Bearer ",
  };
  const analysis: DataFlowAnalysis = { messages: [], bindings: [], diagnostics: [] };

  function bindingProps() {
    const handlers = props();
    handlers.document.messages.push({
      ...handlers.document.messages[0]!,
      id: "next",
      label: "Продолжить",
      execution: { ...defaultStepExecution(), bindings: [binding] },
    });
    return {
      ...handlers,
      analyzeDataFlow: vi
        .fn<NonNullable<ScenarioExecutionPanelProps["analyzeDataFlow"]>>()
        .mockResolvedValue(analysis),
    };
  }

  it("offers contract parameters inline and restores literal values after removing their binding", async () => {
    const handlers = bindingProps();
    handlers.analyzeDataFlow.mockResolvedValue({
      messages: [
        {
          messageId: "login",
          requestFields: [],
          responseFields: [{ kind: "response", pointer: "/id", type: "integer", required: true }],
        },
        {
          messageId: "next",
          responseFields: [],
          requestFields: [{ kind: "path", name: "id", type: "integer", required: true }],
        },
      ],
      bindings: [],
      diagnostics: [],
    });
    const view = renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.click(screen.getByRole("button", { name: /2. Продолжить/ }));
    const take = await screen.findByRole("button", { name: "Взять из ответа: id" });
    fireEvent.change(screen.getByLabelText("Параметр пути 1: значение"), {
      target: { value: "17" },
    });
    fireEvent.click(take);
    fireEvent.change(screen.getByLabelText("Шаг-источник связи 2"), { target: { value: "login" } });
    fireEvent.change(screen.getByLabelText("Поле ответа связи 2"), {
      target: { value: "pointer:/id" },
    });
    expect(screen.queryByLabelText("Параметр пути 1: значение")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Сохранить настройки" }));
    const saved = handlers.onChangeDocument.mock.calls[0]![0].messages[1].execution;
    expect(saved.pathParams).toEqual({ id: "17" });
    expect(saved.bindings[1]).toMatchObject({
      sourceMessageId: "login",
      sourcePointer: "/id",
      target: { kind: "path", name: "id" },
    });
    expect(saved.bindings[0]).toEqual(binding);
    view.rerender(
      <ScenarioExecutionPanel
        {...handlers}
        document={handlers.onChangeDocument.mock.calls[0]![0]}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Удалить связь 2" }));
    expect(screen.getByLabelText("Параметр пути 1: значение")).toHaveValue("17");
  });

  it("keeps contract rows in place when the user first enters a value in the second row", async () => {
    const handlers = bindingProps();
    handlers.analyzeDataFlow.mockResolvedValue({
      messages: [
        {
          messageId: "next",
          responseFields: [],
          requestFields: [
            { kind: "query", name: "limit", type: "integer", required: false },
            { kind: "query", name: "offset", type: "integer", required: false },
          ],
        },
      ],
      bindings: [],
      diagnostics: [],
    });
    renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.click(screen.getByRole("button", { name: /2. Продолжить/ }));
    const input = await screen.findByLabelText("Query-параметр 2: значение");
    input.focus();
    fireEvent.change(input, { target: { value: "2" } });
    expect(screen.getByLabelText("Query-параметр 2: имя")).toHaveValue("offset");
    expect(input).toHaveFocus();
    fireEvent.change(input, { target: { value: "20" } });
    fireEvent.click(screen.getByRole("button", { name: "Сохранить настройки" }));
    expect(handlers.onChangeDocument.mock.calls[0]![0].messages[1].execution.query).toEqual({
      offset: "20",
    });
  });

  it("keeps an unfinished manual destination editable when changing its kind", async () => {
    renderPanel(<ScenarioExecutionPanel {...bindingProps()} />);
    fireEvent.click(screen.getByRole("button", { name: /2. Продолжить/ }));
    fireEvent.click(screen.getByText("Другое поле запроса"));
    fireEvent.click(screen.getByRole("button", { name: "Добавить связь" }));
    fireEvent.change(screen.getByLabelText("Тип назначения связи 2"), {
      target: { value: "query" },
    });
    expect(screen.getByLabelText("Имя назначения связи 2")).toHaveValue("");
    fireEvent.change(screen.getByLabelText("Имя назначения связи 2"), { target: { value: "ref" } });
    expect(screen.getByLabelText("Имя назначения связи 2")).toHaveValue("ref");
  });

  it("preserves bindings when saving other settings and keeps incomplete drafts across steps", async () => {
    const handlers = bindingProps();
    renderPanel(<ScenarioExecutionPanel {...handlers} />);
    await waitFor(() => expect(screen.getByRole("button", { name: "Запустить" })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: /2. Продолжить/ }));
    fireEvent.change(screen.getByLabelText("Тело запроса"), { target: { value: "{}" } });
    fireEvent.click(screen.getByRole("button", { name: "Сохранить настройки" }));
    expect(handlers.onChangeDocument.mock.calls[0]![0].messages[1].execution.bindings).toEqual([
      binding,
    ]);
    fireEvent.click(screen.getByText("Другое поле запроса"));
    fireEvent.click(screen.getByRole("button", { name: "Добавить связь" }));
    fireEvent.change(screen.getByLabelText("JSON Pointer источника связи 2"), {
      target: { value: "/draft" },
    });
    fireEvent.click(screen.getByRole("button", { name: /1. Войти/ }));
    expect(screen.getByRole("button", { name: "Запустить" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: /2. Продолжить/ }));
    expect(screen.getByLabelText("JSON Pointer источника связи 2")).toHaveValue("/draft");
  });

  it("blocks run on analysis failure and semantic errors while allowing broken bindings to be removed", async () => {
    const handlers = bindingProps();
    handlers.analyzeDataFlow
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce({
        ...analysis,
        diagnostics: [
          {
            pointer: "/messages/1/execution/bindings/0",
            severity: "error",
            message: "Источник недоступен",
          },
        ],
      })
      .mockResolvedValue(analysis);
    renderPanel(<ScenarioExecutionPanel {...handlers} />);
    await screen.findByRole("button", { name: "Повторить анализ" });
    expect(screen.getByRole("button", { name: "Запустить" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Повторить анализ" }));
    await screen.findByText("Ошибка: Источник недоступен");
    expect(screen.getByRole("button", { name: "Запустить" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: /2. Продолжить/ }));
    fireEvent.click(screen.getByRole("button", { name: "Удалить связь 1" }));
    fireEvent.click(screen.getByRole("button", { name: "Сохранить настройки" }));
    expect(handlers.onChangeDocument.mock.calls[0]![0].messages[1].execution.bindings).toEqual([]);
  });

  it("shows the selected occurrence provenance using the report snapshot and preserves precise JSON", async () => {
    const handlers = bindingProps();
    const snapshot = { ...report(), document: structuredClone(handlers.document) };
    snapshot.document.messages[0]!.label = "Исторический источник";
    snapshot.steps = [1, 2].map((occurrence) => ({
      messageId: "next",
      occurrence,
      status: "passed",
      assertions: [],
      bindingResults: [
        {
          bindingId: "token",
          sourceMessageId: "login",
          sourcePointer: "/token",
          sourceOccurrence: occurrence,
          sourceIterations: [{ fragmentId: "loop", iteration: occurrence }],
          target: binding.target,
          valueJson: occurrence === 1 ? '"first"' : "9007199254740993",
        },
      ],
    }));
    handlers.listRuns.mockResolvedValue([snapshot]);
    handlers.getRun.mockResolvedValue(snapshot);
    renderPanel(<ScenarioExecutionPanel {...handlers} />);
    fireEvent.click(screen.getByRole("tab", { name: /Результат/ }));
    fireEvent.click(await screen.findByRole("button", { name: /Продолжить · повтор 2/ }));
    expect(screen.getByText(/Передали Authorization:/)).toHaveTextContent("9007199254740993");
    expect(screen.getByText(/Источник: выполнение 2.*цикл loop: 2/)).not.toBeVisible();
    fireEvent.click(screen.getByText("Откуда взялось значение"));
    expect(screen.getByText(/Исторический источник.*Authorization/)).toBeVisible();
    expect(screen.getByText(/Источник: выполнение 2.*цикл loop: 2/)).toBeInTheDocument();
    expect(screen.getByText("9007199254740993")).toBeInTheDocument();
    expect(screen.queryByText('"first"')).not.toBeInTheDocument();
  });
});

import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderInRouter } from "@/test/render";
import { json, route } from "@/test/http";
import { exampleCanvas } from "./canvasModel";
import { designScenarioKeys, type DesignScenarioDetail } from "./designScenarioApi";
import { ServerDesignCanvasPage } from "./ServerDesignCanvasPage";

vi.mock("./SequenceGraph", () => ({ default: () => <div data-testid="sequence-graph" /> }));

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  localStorage.clear();
});

function detailFixture(version = 1, title = "Оформление заказа"): DesignScenarioDetail {
  const document = { ...exampleCanvas(), title };
  return {
    scenario: {
      id: 12,
      name: title,
      version,
      draftRevisionId: 40 + version,
      createdAt: 1_700_000_000,
      updatedAt: 1_700_000_000 + version,
    },
    draft: {
      id: 40 + version,
      scenarioId: 12,
      version,
      hash: `hash-${version}`,
      source: "ui",
      summary: "",
      createdAt: 1_700_000_000 + version,
      document,
      formDrafts: {},
    },
    revisions: [],
    diagnostics: [],
    contractUpdates: [],
  };
}

describe("ServerDesignCanvasPage", () => {
  it("runs the saved revision through the shared server runner without saving results into the document", async () => {
    const detail = detailFixture();
    detail.draft.document.fragments = [];
    detail.draft.document.messages = [detail.draft.document.messages[0]!];
    detail.draft.document.contracts[0] = {
      ...detail.draft.document.contracts[0]!,
      mode: "linked",
      source: { designId: 7, revisionId: 11, version: 1 },
    };
    const runId = "00000000-0000-4000-8000-000000000012";
    vi.spyOn(crypto, "randomUUID").mockReturnValue(runId);
    const report = {
      id: runId,
      scenarioId: 12,
      revisionId: 41,
      version: 1,
      name: "",
      source: "ui",
      status: "passed",
      startedAt: 1000,
      finishedAt: 1002,
      document: detail.draft.document,
      inputVariables: {},
      variables: {},
      steps: [
        {
          messageId: "create-order",
          status: "passed",
          assertions: [],
          response: {
            scenarioRevisionId: 41,
            designId: 7,
            designRevisionId: 11,
            workspaceRevision: 3,
            method: "POST",
            path: "/orders",
            status: 201,
            headers: {},
            body: "{}",
            durationMs: 2,
          },
        },
      ],
    };
    const fetchMock = route({
      "GET /api/design-scenarios/12": () => json(200, detail),
      "GET /api/design-scenarios/12/runs": () => json(200, { runs: [] }),
      "POST /api/design-scenarios/12/runs": () => json(202, report),
      [`GET /api/design-scenarios/12/runs/${runId}`]: () => json(200, report),
    });
    renderInRouter(<ServerDesignCanvasPage id={12} />);
    await userEvent.click(await screen.findByRole("button", { name: "Запуск" }));
    const dialog = await screen.findByRole("dialog", { name: /Запуск сценария/ });
    await userEvent.click(within(dialog).getByRole("button", { name: "Запустить" }));
    expect(await within(dialog).findByText("Прогон завершён")).toBeInTheDocument();
    const call = fetchMock.mock.calls.find(
      ([input, init]) => String(input).endsWith("/runs") && init?.method === "POST",
    );
    expect(JSON.parse(String(call?.[1]?.body))).toMatchObject({
      revisionId: 41,
      runId: expect.any(String),
    });
    expect(fetchMock.mock.calls.some(([, init]) => init?.method === "PUT")).toBe(false);
  });

  it("automatically saves the edited document with its baseline version", async () => {
    const fetchMock = route({
      "GET /api/design-scenarios/12": () => json(200, detailFixture()),
      "PUT /api/design-scenarios/12/draft": () => json(200, detailFixture(2, "Оплата заказа")),
    });
    renderInRouter(<ServerDesignCanvasPage id={12} />);

    const title = await screen.findByRole("textbox", { name: "Название сценария" });
    expect(title).toHaveValue("Оформление заказа");
    expect(screen.queryByRole("button", { name: "Сохранить на сервере" })).not.toBeInTheDocument();
    await userEvent.clear(title);
    await userEvent.type(title, "Оплата заказа");

    await waitFor(() => expect(screen.getByText("Сохранено")).toBeInTheDocument());
    const write = fetchMock.mock.calls.find(
      ([input, init]) =>
        String(input) === "/api/design-scenarios/12/draft" && init?.method === "PUT",
    );
    expect(JSON.parse(String(write?.[1]?.body))).toMatchObject({
      expectedVersion: 1,
      document: { formatVersion: 1, title: "Оплата заказа" },
      formDrafts: { all: "{}" },
    });
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "PUT")).toHaveLength(1);
    expect(screen.getByRole("button", { name: "Отменить" })).toBeEnabled();
  });

  it("keeps edits made while a save request is pending", async () => {
    let resolveSave: ((response: Response) => void) | undefined;
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const key = `${(init?.method ?? "GET").toUpperCase()} ${String(input)}`;
        if (key === "GET /api/design-scenarios/12") {
          return Promise.resolve(json(200, detailFixture()));
        }
        if (key === "PUT /api/design-scenarios/12/draft") {
          return new Promise<Response>((resolve) => {
            resolveSave = resolve;
          });
        }
        if (key === "GET /api/design-scenarios") {
          return Promise.resolve(json(200, { scenarios: [] }));
        }
        return Promise.resolve(json(500, { error: { code: "internal", message: key } }));
      }),
    );
    renderInRouter(<ServerDesignCanvasPage id={12} />);

    const title = await screen.findByRole("textbox", { name: "Название сценария" });
    await userEvent.clear(title);
    await userEvent.type(title, "Отправленная версия");
    await waitFor(() => expect(resolveSave).toBeDefined());
    await userEvent.clear(title);
    await userEvent.type(title, "Правка после отправки");

    resolveSave?.(json(200, detailFixture(2, "Отправленная версия")));

    await waitFor(() => expect(title).toHaveValue("Правка после отправки"));
    expect(screen.getByText("Сохраняется…")).toBeInTheDocument();
  });

  it("rebases edits made during a linked save onto the server-assigned contract pin", async () => {
    const initial = detailFixture();
    initial.draft.document.contracts[0] = {
      ...initial.draft.document.contracts[0]!,
      mode: "linked",
      source: { designId: 7, revisionId: 11, version: 1 },
    };
    const canonical = detailFixture(2, "Отправленная версия");
    canonical.draft.document.contracts[0] = {
      ...canonical.draft.document.contracts[0]!,
      mode: "linked",
      source: { designId: 7, revisionId: 12, version: 2 },
    };
    let resolveFirst: ((response: Response) => void) | undefined;
    const writes: RequestInit[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const key = `${(init?.method ?? "GET").toUpperCase()} ${String(input)}`;
        if (key === "GET /api/design-scenarios/21") return Promise.resolve(json(200, initial));
        if (key === "PUT /api/design-scenarios/21/draft") {
          writes.push(init ?? {});
          if (writes.length === 1) {
            return new Promise<Response>((resolve) => {
              resolveFirst = resolve;
            });
          }
          return Promise.resolve(json(200, detailFixture(3, "Правка после отправки")));
        }
        if (key === "GET /api/design-scenarios") {
          return Promise.resolve(json(200, { scenarios: [] }));
        }
        return Promise.resolve(json(500, { error: { code: "internal", message: key } }));
      }),
    );
    renderInRouter(<ServerDesignCanvasPage id={21} />);

    const title = await screen.findByRole("textbox", { name: "Название сценария" });
    await userEvent.clear(title);
    await userEvent.type(title, "Отправленная версия");
    await waitFor(() => expect(resolveFirst).toBeDefined());
    await userEvent.clear(title);
    await userEvent.type(title, "Правка после отправки");
    resolveFirst?.(json(200, canonical));

    await waitFor(() => expect(title).toHaveValue("Правка после отправки"));
    await waitFor(() => expect(writes).toHaveLength(2));
    const second = JSON.parse(String(writes[1]?.body));
    expect(second.expectedVersion).toBe(2);
    expect(second.document.title).toBe("Правка после отправки");
    expect(second.document.contracts[0].source).toEqual({
      designId: 7,
      revisionId: 12,
      version: 2,
    });
  });

  it("recovers an unsaved per-scenario draft after remounting", async () => {
    route({
      "GET /api/design-scenarios/19": () => json(200, detailFixture(2)),
    });
    const first = renderInRouter(<ServerDesignCanvasPage id={19} />);

    const title = await screen.findByRole("textbox", { name: "Название сценария" });
    await userEvent.clear(title);
    await userEvent.type(title, "Восстановленный черновик");

    await waitFor(() => {
      const raw = localStorage.getItem("mocker:design-scenario:19:draft");
      expect(raw).not.toBeNull();
      expect(JSON.parse(String(raw))).toMatchObject({
        document: { title: "Восстановленный черновик" },
        baseDocument: { title: "Оформление заказа" },
        baseVersion: 2,
        baseRevisionId: 42,
      });
    });
    first.unmount();

    renderInRouter(<ServerDesignCanvasPage id={19} />);
    expect(await screen.findByRole("textbox", { name: "Название сценария" })).toHaveValue(
      "Восстановленный черновик",
    );
    expect(screen.getByText("Сохраняется…")).toBeInTheDocument();
  });

  it("pauses after a failed autosave and retries the latest edits explicitly", async () => {
    let writes = 0;
    const fetchMock = route({
      "GET /api/design-scenarios/12": () => json(200, detailFixture()),
      "PUT /api/design-scenarios/12/draft": () => {
        writes++;
        return writes === 1
          ? json(503, { error: { code: "unavailable", message: "Offline" } })
          : json(200, detailFixture(2, "Актуальная правка"));
      },
    });
    renderInRouter(<ServerDesignCanvasPage id={12} />);
    const title = await screen.findByRole("textbox", { name: "Название сценария" });
    fireEvent.change(title, { target: { value: "Первая правка" } });
    expect(await screen.findByText("Не удалось сохранить")).toBeInTheDocument();
    fireEvent.change(title, { target: { value: "Актуальная правка" } });
    await new Promise((resolve) => setTimeout(resolve, 1000));
    expect(writes).toBe(1);
    await userEvent.click(screen.getByRole("button", { name: "Повторить сохранение" }));
    await screen.findByText("Сохранено");
    const calls = fetchMock.mock.calls.filter(([, init]) => init?.method === "PUT");
    expect(calls).toHaveLength(2);
    expect(JSON.parse(String(calls[1]?.[1]?.body))).toMatchObject({
      expectedVersion: 1,
      document: { title: "Актуальная правка" },
    });
  });

  it("keeps an undo to the old baseline while a write and poll are in flight", async () => {
    const original = detailFixture();
    const sent = detailFixture(2, "Отправленная версия");
    let current = original;
    let finish: ((value: Response) => void) | undefined;
    const writes: RequestInit[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input) === "/api/design-scenarios/12" && (init?.method ?? "GET") === "GET")
          return Promise.resolve(json(200, current));
        if (init?.method === "PUT") {
          writes.push(init);
          if (writes.length === 1)
            return new Promise<Response>((resolve) => {
              finish = resolve;
            });
          return Promise.resolve(json(200, detailFixture(3)));
        }
        return Promise.resolve(json(200, { scenarios: [], designs: [] }));
      }),
    );
    const { queryClient } = renderInRouter(<ServerDesignCanvasPage id={12} />);
    const title = await screen.findByRole("textbox", { name: "Название сценария" });
    fireEvent.change(title, { target: { value: "Отправленная версия" } });
    await waitFor(() => expect(finish).toBeDefined());
    expect(screen.getByRole("button", { name: "История" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Отменить" }));
    expect(title).toHaveValue(original.draft.document.title);
    current = sent;
    await queryClient.invalidateQueries({ queryKey: designScenarioKeys.detail(12) });
    expect(title).toHaveValue(original.draft.document.title);
    const recovery = JSON.parse(String(localStorage.getItem("mocker:design-scenario:12:draft")));
    expect(recovery.document.title).toBe(original.draft.document.title);
    finish?.(json(200, sent));
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(JSON.parse(String(writes[1]?.body))).toMatchObject({
      expectedVersion: 2,
      document: { title: original.draft.document.title },
    });
    expect(title).toHaveValue(original.draft.document.title);
    await screen.findByText("Сохранено");
    expect(screen.getByRole("button", { name: "Повторить" })).toBeEnabled();
  });

  it("keeps the in-memory draft when browser recovery storage fails", async () => {
    route({ "GET /api/design-scenarios/18": () => json(200, detailFixture()) });
    renderInRouter(<ServerDesignCanvasPage id={18} />);
    const title = await screen.findByRole("textbox", { name: "Название сценария" });
    vi.stubGlobal("localStorage", {
      getItem: () => null,
      removeItem: () => {},
      setItem: () => {
        throw new Error("quota");
      },
    });

    await userEvent.clear(title);
    await userEvent.type(title, "Черновик остаётся в памяти");

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Не удалось сохранить аварийную копию в браузере",
    );
    expect(title).toHaveValue("Черновик остаётся в памяти");
  });

  it("recovers an interrupted write even when undo returned to the old baseline", async () => {
    const document = detailFixture().draft.document;
    localStorage.setItem(
      "mocker:design-scenario:12:draft",
      JSON.stringify({
        document,
        baseDocument: document,
        formDrafts: "{}",
        baseFormDrafts: "{}",
        baseVersion: 1,
        baseRevisionId: 41,
        savedAt: Date.now(),
        pendingSave: true,
      }),
    );
    const fetchMock = route({
      "GET /api/design-scenarios/12": () =>
        json(200, detailFixture(2, "Запись успела завершиться")),
    });
    renderInRouter(<ServerDesignCanvasPage id={12} />);
    const title = await screen.findByRole("textbox", { name: "Название сценария" });
    expect(title).toHaveValue(document.title);
    expect(await screen.findByText(/Предыдущее сохранение было прервано/)).toBeInTheDocument();
    await new Promise((resolve) => setTimeout(resolve, 1000));
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "PUT")).toHaveLength(0);
    expect(localStorage.getItem("mocker:design-scenario:12:draft")).not.toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Загрузить серверный сценарий" }));
    await waitFor(() => expect(title).toHaveValue("Запись успела завершиться"));
    expect(screen.getByText("Сохранено")).toBeInTheDocument();
  });

  it.each([503, 409])(
    "keeps a failed %s write recoverable after undo and remount",
    async (status) => {
      let current = detailFixture();
      let finish: ((value: Response) => void) | undefined;
      let writes = 0;
      vi.stubGlobal(
        "fetch",
        vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
          if (String(input) === "/api/design-scenarios/12" && (init?.method ?? "GET") === "GET")
            return Promise.resolve(json(200, current));
          if (init?.method === "PUT") {
            writes++;
            return new Promise<Response>((resolve) => {
              finish = resolve;
            });
          }
          return Promise.resolve(json(200, { scenarios: [], designs: [] }));
        }),
      );
      const first = renderInRouter(<ServerDesignCanvasPage id={12} />);
      const title = await screen.findByRole("textbox", { name: "Название сценария" });
      fireEvent.change(title, { target: { value: "Отправленная версия" } });
      await waitFor(() => expect(finish).toBeDefined());
      await userEvent.click(screen.getByRole("button", { name: "Отменить" }));
      current = detailFixture(2, "Отправленная версия");
      finish?.(
        json(status, {
          error: {
            code: status === 409 ? "design_scenario_conflict" : "unavailable",
            message: "Write not confirmed",
          },
        }),
      );
      await screen.findByText(status === 409 ? "Конфликт изменений" : "Не удалось сохранить");
      const recovery = JSON.parse(String(localStorage.getItem("mocker:design-scenario:12:draft")));
      expect(recovery).toMatchObject({
        pendingSave: true,
        document: { title: "Оформление заказа" },
      });
      first.unmount();
      renderInRouter(<ServerDesignCanvasPage id={12} />);
      expect(await screen.findByRole("textbox", { name: "Название сценария" })).toHaveValue(
        "Оформление заказа",
      );
      expect(screen.getByText(/Предыдущее сохранение было прервано/)).toBeInTheDocument();
      expect(writes).toBe(1);
    },
  );

  it("round-trips pending invalid API form buffers from the server", async () => {
    const serializedDrafts = JSON.stringify({
      "/canvas-contract/api-1/paths/~1orders/get": {
        source: "{invalid",
        propertySource: "",
        error: "Некорректный JSON",
      },
    });
    const initial = detailFixture();
    initial.draft.formDrafts = { all: serializedDrafts };
    const saved = detailFixture(2, "Сценарий с буфером");
    saved.draft.formDrafts = { all: serializedDrafts };
    const fetchMock = route({
      "GET /api/design-scenarios/12": () => json(200, initial),
      "PUT /api/design-scenarios/12/draft": () => json(200, saved),
    });
    renderInRouter(<ServerDesignCanvasPage id={12} />);

    expect(
      await screen.findByText(/Есть незавершённые поля API\. Их текст сохранится/),
    ).toBeInTheDocument();
    const title = screen.getByRole("textbox", { name: "Название сценария" });
    await userEvent.clear(title);
    await userEvent.type(title, "Сценарий с буфером");

    await screen.findByText("Сохранено");
    const write = fetchMock.mock.calls.find(
      ([input, init]) =>
        String(input) === "/api/design-scenarios/12/draft" && init?.method === "PUT",
    );
    expect(JSON.parse(String(write?.[1]?.body)).formDrafts).toEqual({ all: serializedDrafts });
  });

  it("preserves an invalid raw form buffer and opaque form draft keys", async () => {
    const initial = detailFixture();
    initial.draft.formDrafts = { all: "{invalid", futureEditor: "opaque" };
    const saved = detailFixture(2, "Сценарий с raw-буфером");
    saved.draft.formDrafts = initial.draft.formDrafts;
    const fetchMock = route({
      "GET /api/design-scenarios/12": () => json(200, initial),
      "PUT /api/design-scenarios/12/draft": () => json(200, saved),
    });
    renderInRouter(<ServerDesignCanvasPage id={12} />);

    expect(
      await screen.findByText(/Есть незавершённые поля API\. Их текст сохранится/),
    ).toBeInTheDocument();
    const title = screen.getByRole("textbox", { name: "Название сценария" });
    await userEvent.clear(title);
    await userEvent.type(title, "Сценарий с raw-буфером");

    await screen.findByText("Сохранено");
    const write = fetchMock.mock.calls.find(
      ([input, init]) =>
        String(input) === "/api/design-scenarios/12/draft" && init?.method === "PUT",
    );
    expect(JSON.parse(String(write?.[1]?.body)).formDrafts).toEqual({
      all: "{invalid",
      futureEditor: "opaque",
    });
  });

  it("keeps saved unfinished API buffers when polling observes a newer server version", async () => {
    const serializedDrafts = JSON.stringify({
      "/canvas-contract/orders-api/paths/~1orders/post": {
        source: "{unfinished",
        propertySource: "",
        error: "Некорректный JSON",
      },
    });
    let current = detailFixture();
    current.draft.formDrafts = { all: serializedDrafts };
    route({ "GET /api/design-scenarios/12": () => json(200, current) });
    const first = renderInRouter(<ServerDesignCanvasPage id={12} />);
    await screen.findByText(/Есть незавершённые поля API/);
    expect(screen.getByText("Сохранено")).toBeInTheDocument();

    current = detailFixture(2, "Из MCP");
    await first.queryClient.invalidateQueries({ queryKey: designScenarioKeys.detail(12) });

    expect(await screen.findByText(/Локальные правки не заменены/)).toBeInTheDocument();
    expect(screen.getByText(/Есть незавершённые поля API/)).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Название сценария" })).toHaveValue(
      "Оформление заказа",
    );
    const recovery = JSON.parse(String(localStorage.getItem("mocker:design-scenario:12:draft")));
    expect(recovery.formDrafts).toBe(serializedDrafts);
    first.unmount();
    renderInRouter(<ServerDesignCanvasPage id={12} />);
    await screen.findByText(/Локальные правки не заменены/);
    expect(screen.getByText(/Есть незавершённые поля API/)).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Название сценария" })).toHaveValue(
      "Оформление заказа",
    );
    await userEvent.click(screen.getByRole("button", { name: "Загрузить серверную версию" }));
    expect(screen.getByRole("textbox", { name: "Название сценария" })).toHaveValue("Из MCP");
    expect(screen.queryByText(/Есть незавершённые поля API/)).not.toBeInTheDocument();
  });

  it("blocks canvas undo during a pending contract command", async () => {
    let current = detailFixture();
    let finish: ((response: Response) => void) | undefined;
    const fallback = route({
      "GET /api/design-scenarios/12": () => json(200, current),
      "GET /api/api-designs": () => json(200, { designs: [] }),
      "PUT /api/design-scenarios/12/draft": () => {
        current = detailFixture(2, "Сохранённая правка");
        return json(200, current);
      },
    });
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input) === "/api/design-scenarios/12/commands" && init?.method === "POST") {
          return new Promise<Response>((resolve) => {
            finish = resolve;
          });
        }
        return fallback(input, init);
      }),
    );
    renderInRouter(<ServerDesignCanvasPage id={12} />);
    const title = await screen.findByRole("textbox", { name: "Название сценария" });
    fireEvent.change(title, { target: { value: "Сохранённая правка" } });
    await screen.findByText("Сохранено");
    await userEvent.click(screen.getByRole("button", { name: "Контракты API" }));
    const dialog = await screen.findByRole("dialog", { name: "Контракты API" });
    await userEvent.click(
      within(dialog).getByRole("button", {
        name: "Создать API-проект Orders API (локальная копия)",
      }),
    );
    await waitFor(() => expect(finish).toBeDefined());
    for (const modifier of [{ metaKey: true }, { ctrlKey: true }]) {
      fireEvent.keyDown(dialog, { key: "z", ...modifier });
      expect(title).toHaveValue("Сохранённая правка");
    }
    current = detailFixture(3, "Сохранённая правка");
    finish!(json(200, current));
    await waitFor(() =>
      expect(within(dialog).getByRole("button", { name: "Закрыть" })).toBeEnabled(),
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "Закрыть" }));
    expect(title).toHaveValue("Сохранённая правка");
  });

  it("resumes canvas undo and redo after closing nested contract dialogs", async () => {
    let current = detailFixture();
    route({
      "GET /api/design-scenarios/12": () => json(200, current),
      "GET /api/api-designs": () => json(200, { designs: [] }),
      "PUT /api/design-scenarios/12/draft": () => {
        current = detailFixture(2, "Сохранённая правка");
        return json(200, current);
      },
    });
    renderInRouter(<ServerDesignCanvasPage id={12} />);
    const title = await screen.findByRole("textbox", { name: "Название сценария" });
    fireEvent.change(title, { target: { value: "Сохранённая правка" } });
    await screen.findByText("Сохранено");
    await userEvent.click(screen.getByRole("button", { name: "Контракты API" }));
    const dialog = await screen.findByRole("dialog", { name: "Контракты API" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Добавить API" }));
    const picker = await screen.findByRole("dialog", { name: "Добавить существующий API" });
    fireEvent.keyDown(picker, { key: "z", ctrlKey: true });
    expect(title).toHaveValue("Сохранённая правка");
    await userEvent.click(within(picker).getByRole("button", { name: "Отмена" }));
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Создать контракт по всей схеме" }),
    );
    const conversion = await screen.findByRole("dialog", { name: "Контракт по всей схеме" });
    fireEvent.keyDown(conversion, { key: "z", metaKey: true });
    expect(title).toHaveValue("Сохранённая правка");
    await userEvent.click(within(conversion).getByRole("button", { name: "Отмена" }));
    const reopened = await screen.findByRole("dialog", { name: "Контракты API" });
    await userEvent.click(within(reopened).getByRole("button", { name: "Закрыть" }));
    fireEvent.keyDown(window, { key: "z", ctrlKey: true });
    expect(title).toHaveValue("Оформление заказа");
    fireEvent.keyDown(window, { key: "z", ctrlKey: true, shiftKey: true });
    expect(title).toHaveValue("Сохранённая правка");
  });

  it("keeps a dirty draft when polling observes an MCP version", async () => {
    let current = detailFixture();
    route({
      "GET /api/design-scenarios/12": () => json(200, current),
    });
    const { queryClient } = renderInRouter(<ServerDesignCanvasPage id={12} />);
    const title = await screen.findByRole("textbox", { name: "Название сценария" });
    await userEvent.clear(title);
    await userEvent.type(title, "Локальная правка");

    current = detailFixture(2, "Из MCP");
    await queryClient.invalidateQueries({ queryKey: designScenarioKeys.detail(12) });

    expect(
      await screen.findByText("На сервере появилась версия 2. Локальные правки не заменены."),
    ).toBeInTheDocument();
    expect(title).toHaveValue("Локальная правка");

    expect(screen.getByRole("button", { name: "Сохранить как новый сценарий" })).toBeEnabled();

    await userEvent.click(screen.getByRole("button", { name: "Загрузить серверную версию" }));
    expect(title).toHaveValue("Из MCP");
  });

  it("keeps a conflicting linked edit and can save it as an independent copy", async () => {
    const initial = detailFixture();
    initial.draft.document.contracts[0] = {
      ...initial.draft.document.contracts[0]!,
      mode: "linked",
      source: { designId: 7, revisionId: 11, version: 3 },
    };
    const fetchMock = route({
      "GET /api/design-scenarios/12": () => json(200, initial),
      "PUT /api/design-scenarios/12/draft": () =>
        json(409, {
          error: {
            code: "design_scenario_conflict",
            message: "stale",
            details: { version: 2, draftRevisionId: 42 },
          },
        }),
      "POST /api/design-scenarios": () => json(201, detailFixture(1, "Локальная копия")),
    });
    renderInRouter(<ServerDesignCanvasPage id={12} />);

    const title = await screen.findByRole("textbox", { name: "Название сценария" });
    await userEvent.clear(title);
    await userEvent.type(title, "Локальная копия");

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Сценарий или связанный API изменился на сервере. Ваши локальные правки сохранены.",
    );
    expect(title).toHaveValue("Локальная копия");
    await userEvent.click(screen.getByRole("button", { name: "Сохранить как новый сценарий" }));

    expect(await screen.findByTestId("test-router-elsewhere")).toBeInTheDocument();
    const create = fetchMock.mock.calls.find(
      ([input, init]) => String(input) === "/api/design-scenarios" && init?.method === "POST",
    );
    const body = JSON.parse(String(create?.[1]?.body));
    expect(body.document.title).toBe("Локальная копия");
    expect(body.document.contracts[0]).toMatchObject({
      mode: "copy",
      source: { designId: 7, revisionId: 11, version: 3 },
    });
  });

  it("keeps local edits when an explicit conflict reload fails", async () => {
    let reads = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const key = `${(init?.method ?? "GET").toUpperCase()} ${String(input)}`;
        if (key === "GET /api/design-scenarios/22") {
          reads += 1;
          return Promise.resolve(
            reads === 1
              ? json(200, detailFixture())
              : json(500, { error: { code: "internal", message: "reload failed" } }),
          );
        }
        if (key === "PUT /api/design-scenarios/22/draft") {
          return Promise.resolve(
            json(409, {
              error: {
                code: "design_scenario_conflict",
                message: "stale",
                details: { version: 1, draftRevisionId: 41 },
              },
            }),
          );
        }
        return Promise.resolve(json(200, { scenarios: [] }));
      }),
    );
    renderInRouter(<ServerDesignCanvasPage id={22} />);
    const title = await screen.findByRole("textbox", { name: "Название сценария" });
    await userEvent.clear(title);
    await userEvent.type(title, "Локальная правка");
    await screen.findByText(/Сценарий или связанный API изменился/);

    await userEvent.click(screen.getByRole("button", { name: "Загрузить серверный сценарий" }));

    expect(await screen.findByText(/reload failed/)).toBeInTheDocument();
    expect(title).toHaveValue("Локальная правка");
  });

  it("shows a revision diff and restores history through CAS", async () => {
    const current = detailFixture(2, "Текущая версия");
    current.revisions = [
      {
        id: 42,
        scenarioId: 12,
        version: 2,
        hash: "hash-2",
        source: "ui",
        summary: "Переименован сценарий",
        createdAt: 1_700_000_002,
      },
      {
        id: 41,
        scenarioId: 12,
        version: 1,
        hash: "hash-1",
        source: "mcp",
        summary: "Первая версия",
        createdAt: 1_700_000_001,
      },
    ];
    const restored = detailFixture(3, "Первая версия");
    const fetchMock = route({
      "GET /api/design-scenarios/12": () => json(200, current),
      "GET /api/design-scenarios/12/revisions/42": () => json(200, current.draft),
      "GET /api/design-scenarios/12/revisions/41": () =>
        json(200, detailFixture(1, "Первая версия").draft),
      "POST /api/design-scenarios/12/restore": () => json(200, restored),
    });
    renderInRouter(<ServerDesignCanvasPage id={12} />);

    await screen.findByRole("textbox", { name: "Название сценария" });
    await userEvent.click(screen.getByRole("button", { name: "История" }));
    const dialog = await screen.findByRole("dialog", { name: "История сценария" });
    await userEvent.click(within(dialog).getByRole("button", { name: /Версия 1/ }));

    const changes = await within(dialog).findByRole("region", { name: "Изменения версий" });
    expect(within(changes).getByRole("cell", { name: "Первая версия" })).toBeInTheDocument();
    expect(within(changes).getByRole("cell", { name: "Текущая версия" })).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Восстановить версию 1" }));

    expect(await screen.findByRole("textbox", { name: "Название сценария" })).toHaveValue(
      "Первая версия",
    );
    const write = fetchMock.mock.calls.find(
      ([input, init]) =>
        String(input) === "/api/design-scenarios/12/restore" && init?.method === "POST",
    );
    expect(JSON.parse(String(write?.[1]?.body))).toMatchObject({
      expectedVersion: 2,
      revisionId: 41,
    });
  });

  it("does not undo the editor when using undo and redo shortcuts inside history", async () => {
    const initial = detailFixture();
    const saved = detailFixture(2, "Сохранённая правка");
    saved.revisions = [saved.draft, initial.draft];
    const fetch = route({
      "GET /api/design-scenarios/12": () => json(200, initial),
      "PUT /api/design-scenarios/12/draft": () => json(200, saved),
      "GET /api/design-scenarios/12/revisions/41": () => json(200, initial.draft),
      "GET /api/design-scenarios/12/revisions/42": () => json(200, saved.draft),
    });
    renderInRouter(<ServerDesignCanvasPage id={12} />);
    const title = await screen.findByRole("textbox", { name: "Название сценария" });
    fireEvent.change(title, { target: { value: "Сохранённая правка" } });
    await waitFor(() => expect(screen.getByText("Сохранено")).toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Отменить" })).toBeEnabled();
    await userEvent.click(screen.getByRole("button", { name: "История" }));
    const dialog = await screen.findByRole("dialog", { name: "История сценария" });
    await within(dialog).findByRole("region", { name: "Изменения версий" });
    const version = within(dialog).getByRole("button", { name: /^Версия 1 ·/ });
    version.focus();
    fireEvent.keyDown(version, { key: "z", metaKey: true });
    expect(title).toHaveValue("Сохранённая правка");
    fireEvent.keyDown(version, { key: "z", ctrlKey: true });
    expect(title).toHaveValue("Сохранённая правка");
    fireEvent.keyDown(version, { key: "z", metaKey: true, shiftKey: true });
    expect(title).toHaveValue("Сохранённая правка");
    await userEvent.click(within(dialog).getByRole("button", { name: "Закрыть" }));
    expect(screen.getByText("Сохранено")).toBeInTheDocument();
    expect(fetch.mock.calls.filter(([, init]) => init?.method === "PUT")).toHaveLength(1);
  });

  it("updates warnings automatically from autosave responses without manual validation", async () => {
    let current = detailFixture();
    let writes = 0;
    const fetchMock = route({
      "GET /api/design-scenarios/12": () => json(200, current),
      "PUT /api/design-scenarios/12/draft": () => {
        writes++;
        current = detailFixture(
          writes + 1,
          writes === 1 ? "С предупреждением" : "Исправленный сценарий",
        );
        if (writes === 1) {
          current.diagnostics = [
            {
              pointer: "/messages/0/operation",
              message: "Связь с удалённой операцией",
              severity: "warning",
            },
          ];
        }
        return json(200, current);
      },
    });
    renderInRouter(<ServerDesignCanvasPage id={12} />);

    const title = await screen.findByRole("textbox", { name: "Название сценария" });
    expect(screen.queryByRole("button", { name: "Проверить" })).not.toBeInTheDocument();
    fireEvent.change(title, { target: { value: "С предупреждением" } });
    expect(await screen.findByText("Связь с удалённой операцией")).toBeInTheDocument();
    expect(screen.getByText("Сохранено")).toBeInTheDocument();

    fireEvent.change(title, { target: { value: "Исправленный сценарий" } });
    await waitFor(() => expect(writes).toBe(2));
    await waitFor(() =>
      expect(screen.queryByText("Связь с удалённой операцией")).not.toBeInTheDocument(),
    );
    expect(screen.getByText("Сохранено")).toBeInTheDocument();
    expect(
      fetchMock.mock.calls.some(
        ([input, init]) =>
          String(input) === "/api/design-scenarios/12/validate" && init?.method === "POST",
      ),
    ).toBe(false);
  });

  it("materializes a copied contract through an atomic scenario command", async () => {
    const initial = detailFixture();
    const contract = initial.draft.document.contracts[0]!;
    contract.mode = "copy";
    const materialized = detailFixture(2);
    materialized.draft.document.contracts[0] = {
      ...materialized.draft.document.contracts[0]!,
      mode: "linked",
      source: { designId: 7, revisionId: 11, version: 1 },
    };
    let designReads = 0;
    const fetchMock = route({
      "GET /api/design-scenarios/12": () => json(200, initial),
      "GET /api/designs": () => {
        designReads += 1;
        return json(200, {
          designs:
            designReads === 1
              ? []
              : [{ id: 7, name: contract.name, version: 1, draftUrl: "http://api-draft.test" }],
        });
      },
      "POST /api/design-scenarios/12/commands": () => json(200, materialized),
    });
    renderInRouter(<ServerDesignCanvasPage id={12} />);

    await screen.findByRole("textbox", { name: "Название сценария" });
    await userEvent.click(screen.getByRole("button", { name: "Контракты API" }));
    const dialog = await screen.findByRole("dialog", { name: "Контракты API" });
    await userEvent.click(
      within(dialog).getByRole("button", { name: `Создать API-проект ${contract.name}` }),
    );

    expect(
      await within(dialog).findByRole("link", { name: "Открыть API Designer" }),
    ).toHaveAttribute("href", "/designs/7");
    expect(await within(dialog).findByRole("link", { name: "Открыть draft mock" })).toHaveAttribute(
      "href",
      "http://api-draft.test",
    );
    expect(designReads).toBe(2);
    const call = fetchMock.mock.calls.find(
      ([input, init]) =>
        String(input) === "/api/design-scenarios/12/commands" && init?.method === "POST",
    );
    expect(JSON.parse(String(call?.[1]?.body))).toEqual({
      expectedVersion: 1,
      commands: [{ type: "materialize_contract", contractId: contract.id }],
      summary: `Создан API-проект ${contract.name}`,
    });
  });

  it("imports an existing API explicitly as a linked contract", async () => {
    const initial = detailFixture();
    initial.draft.document.contracts = [];
    const imported = detailFixture(2);
    imported.draft.document.contracts[0] = {
      ...imported.draft.document.contracts[0]!,
      name: "Каталог",
      mode: "linked",
      source: { designId: 7, revisionId: 11, version: 2 },
    };
    const fetchMock = route({
      "GET /api/design-scenarios/12": () => json(200, initial),
      "GET /api/designs": () => json(200, { designs: [{ id: 7, name: "Каталог", version: 2 }] }),
      "POST /api/design-scenarios/12/commands": () => json(200, imported),
    });
    renderInRouter(<ServerDesignCanvasPage id={12} />);

    await screen.findByRole("textbox", { name: "Название сценария" });
    await userEvent.click(screen.getByRole("button", { name: "Контракты API" }));
    const contracts = await screen.findByRole("dialog", { name: "Контракты API" });
    await userEvent.click(within(contracts).getByRole("button", { name: "Добавить API" }));
    const picker = await screen.findByRole("dialog", { name: "Добавить существующий API" });
    await userEvent.selectOptions(within(picker).getByLabelText("Проект API"), "7");
    await userEvent.selectOptions(within(picker).getByLabelText("Режим контракта"), "linked");
    await userEvent.click(within(picker).getByRole("button", { name: "Добавить связанный API" }));

    expect(await within(contracts).findByText("Общий API")).toBeInTheDocument();
    const call = fetchMock.mock.calls.find(
      ([input, init]) =>
        String(input) === "/api/design-scenarios/12/commands" && init?.method === "POST",
    );
    expect(JSON.parse(String(call?.[1]?.body))).toMatchObject({
      expectedVersion: 1,
      commands: [{ type: "import_contract", designId: 7, mode: "linked" }],
    });
  });

  it("creates one project from the whole diagram in a single command batch", async () => {
    const initial = detailFixture();
    const fetchMock = route({
      "GET /api/design-scenarios/12": () => json(200, initial),
      "GET /api/designs": () => json(200, { designs: [] }),
      "POST /api/design-scenarios/12/commands": () => json(200, detailFixture(2)),
    });
    renderInRouter(<ServerDesignCanvasPage id={12} />);
    await screen.findByRole("textbox", { name: "Название сценария" });
    await userEvent.click(screen.getByRole("button", { name: "Контракты API" }));
    await userEvent.click(screen.getByRole("button", { name: "Создать контракт по всей схеме" }));
    const preview = await screen.findByRole("dialog", { name: "Контракт по всей схеме" });
    expect(within(preview).getByText("Операций: 2 · Вызовов: 2")).toBeInTheDocument();
    await userEvent.click(within(preview).getByRole("button", { name: "Создать API-проект" }));
    await waitFor(() =>
      expect(
        screen.queryByRole("dialog", { name: "Контракт по всей схеме" }),
      ).not.toBeInTheDocument(),
    );
    const call = fetchMock.mock.calls.find(
      ([input, init]) => String(input).endsWith("/commands") && init?.method === "POST",
    );
    const body = JSON.parse(String(call?.[1]?.body));
    expect(body.expectedVersion).toBe(1);
    expect(body.commands.map((command: { type: string }) => command.type)).toEqual([
      "create_contract",
      "bind_operation",
      "bind_operation",
      "materialize_contract",
    ]);
    const contract = body.commands[0].contract;
    expect(Object.keys(contract.document.paths)).toEqual(["/orders", "/orders/{id}"]);
    expect(contract.document.paths["/orders"].post.responses["201"]).toBeDefined();
    expect(
      body.commands
        .slice(1)
        .every(
          (command: { contractId?: string; operation?: { contractId: string } }) =>
            (command.contractId ?? command.operation?.contractId) === contract.id,
        ),
    ).toBe(true);
    expect(screen.getByText("Сохранено")).toBeInTheDocument();
  });

  it("keeps preview inputs and the opening version when polling changes the scenario", async () => {
    let current = detailFixture();
    const fetchMock = route({
      "GET /api/design-scenarios/12": () => json(200, current),
      "GET /api/designs": () => json(200, { designs: [] }),
      "POST /api/design-scenarios/12/commands": () =>
        json(409, {
          error: { code: "design_scenario_conflict", message: "Preview is stale" },
        }),
    });
    const { queryClient } = renderInRouter(<ServerDesignCanvasPage id={12} />);
    await screen.findByRole("textbox", { name: "Название сценария" });
    await userEvent.click(screen.getByRole("button", { name: "Контракты API" }));
    await userEvent.click(screen.getByRole("button", { name: "Создать контракт по всей схеме" }));
    const preview = await screen.findByRole("dialog", { name: "Контракт по всей схеме" });
    const name = within(preview).getByRole("textbox", { name: "Название API" });
    await userEvent.clear(name);
    await userEvent.type(name, "Мой контракт");
    current = detailFixture(2, "Из MCP");
    current.draft.document.messages = [];
    current.diagnostics = [
      {
        pointer: "/contracts/0",
        message: "Контракт изменён на сервере",
        severity: "warning",
      },
    ];
    await queryClient.invalidateQueries({ queryKey: designScenarioKeys.detail(12) });
    await waitFor(() =>
      expect(screen.getByRole("textbox", { name: "Название сценария" })).toHaveValue("Из MCP"),
    );
    expect(await screen.findByText("Контракт изменён на сервере")).toBeInTheDocument();
    expect(within(preview).getByText("Операций: 2 · Вызовов: 2")).toBeInTheDocument();
    await userEvent.click(within(preview).getByRole("button", { name: "Создать API-проект" }));
    expect(await within(preview).findByRole("alert")).toHaveTextContent(/Preview is stale/);
    expect(name).toHaveValue("Мой контракт");
    const call = fetchMock.mock.calls.find(
      ([input, init]) => String(input).endsWith("/commands") && init?.method === "POST",
    );
    expect(JSON.parse(String(call?.[1]?.body))).toMatchObject({
      expectedVersion: 1,
      commands: [{ type: "create_contract", contract: { name: "Мой контракт" } }, {}, {}, {}],
    });
  });

  it.each(["dirty", "pending-form"])(
    "blocks whole-diagram conversion with %s edits",
    async (state) => {
      const initial = detailFixture();
      if (state === "pending-form") initial.draft.formDrafts = { all: "{invalid" };
      route({
        "GET /api/design-scenarios/12": () => json(200, initial),
        "GET /api/designs": () => json(200, { designs: [] }),
      });
      renderInRouter(<ServerDesignCanvasPage id={12} />);
      const title = await screen.findByRole("textbox", { name: "Название сценария" });
      if (state === "dirty") await userEvent.type(title, "!");
      await userEvent.click(screen.getByRole("button", { name: "Контракты API" }));
      expect(screen.getByRole("button", { name: "Создать контракт по всей схеме" })).toBeDisabled();
    },
  );
});

import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderInRouter } from "@/test/render";
import { fill } from "@/test/user";
import { json, route } from "@/test/http";
import { DesignCanvasPage } from "./DesignCanvasPage";
import { STORAGE_KEY, parseSavedCanvas, serializeSavedCanvas } from "./canvasStorage";
import { exampleCanvas, importContract, resolveOperation } from "./canvasModel";

vi.mock("./SequenceGraph", () => ({ default: () => <div data-testid="sequence-graph" /> }));

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  localStorage.clear();
});

async function addFromCanvasMenu(name: string): Promise<void> {
  await userEvent.click(await screen.findByRole("button", { name: "Добавить на канвас" }));
  await userEvent.click(await screen.findByRole("menuitem", { name }));
}

describe("DesignCanvasPage", () => {
  it("creates and saves canvas elements when randomUUID is unavailable on HTTP", async () => {
    vi.stubGlobal("crypto", { getRandomValues: crypto.getRandomValues.bind(crypto) });
    renderInRouter(<DesignCanvasPage />);

    await addFromCanvasMenu("Добавить объект");
    await addFromCanvasMenu("Добавить сообщение");
    await addFromCanvasMenu("Условный блок");
    await userEvent.click(screen.getByRole("button", { name: "Сохранить в браузере" }));

    const { document } = parseSavedCanvas(localStorage.getItem(STORAGE_KEY)!);
    expect(document.participants).toHaveLength(4);
    expect(document.messages).toHaveLength(7);
    expect(document.fragments).toHaveLength(2);
    const ids = [
      document.participants.at(-1)!.id,
      document.messages.at(-1)!.id,
      document.fragments.at(-1)!.id,
    ];
    expect(new Set(ids).size).toBe(3);
    for (const id of ids)
      expect(id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  });

  it("clears an undone selection before adding a fragment", async () => {
    renderInRouter(<DesignCanvasPage />);
    await addFromCanvasMenu("Добавить сообщение");
    await userEvent.click(screen.getByRole("button", { name: "Отменить" }));
    expect(
      screen.queryByRole("region", { name: "Свойства выбранного объекта" }),
    ).not.toBeInTheDocument();
    await addFromCanvasMenu("Условный блок");
    await userEvent.click(screen.getByRole("button", { name: "Сохранить в браузере" }));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(parseSavedCanvas(localStorage.getItem(STORAGE_KEY)!).document.fragments).toHaveLength(2);
  });

  it("keeps a message bound when its API method and path change", async () => {
    renderInRouter(<DesignCanvasPage />);
    await userEvent.click(await screen.findByRole("button", { name: "Структура сценария" }));
    await userEvent.click(screen.getByRole("button", { name: "1. POST /orders Вызов" }));
    await userEvent.clear(screen.getByRole("textbox", { name: "Путь" }));
    await fill(screen.getByRole("textbox", { name: "Путь" }), "/checkout");
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Метод" }), "put");
    await userEvent.click(screen.getByRole("button", { name: "Изменить адрес" }));
    await userEvent.click(screen.getByRole("button", { name: "Сохранить в браузере" }));
    const saved = parseSavedCanvas(localStorage.getItem(STORAGE_KEY)!);
    expect(
      resolveOperation(saved.document, saved.document.messages[0]!.operation!)?.location,
    ).toEqual({ method: "put", path: "/checkout" });
  });

  it("separates unfinished API fields by contract and restores them after saving", async () => {
    const document = exampleCanvas();
    const firstContract = document.contracts[0]!;
    firstContract.document.components = { schemas: { Order: { type: "object" } } };
    const secondContract = importContract("Second API", firstContract.document);
    document.contracts.push(secondContract);
    document.messages[4]!.operation = {
      contractId: secondContract.id,
      operationKey: document.messages[0]!.operation!.operationKey,
    };
    // Import assigns its own operation IDs.
    const secondPaths = secondContract.document.paths as Record<
      string,
      Record<string, Record<string, unknown>>
    >;
    document.messages[4]!.operation!.operationKey = secondPaths["/orders"]!.post![
      "x-mocker-canvas-operation-id"
    ] as string;
    localStorage.setItem(STORAGE_KEY, serializeSavedCanvas(document, {}));
    const first = renderInRouter(<DesignCanvasPage />);
    const selectSchema = async (name: string) => {
      await userEvent.click(screen.getByRole("button", { name }));
      await userEvent.selectOptions(
        screen.getByRole("combobox", { name: "Редактируемый объект API" }),
        "Order",
      );
      return screen.getByRole("textbox", { name: "Полная схема JSON" });
    };
    await userEvent.click(await screen.findByRole("button", { name: "Структура сценария" }));
    const firstField = await selectSchema("1. POST /orders Вызов");
    await userEvent.clear(firstField);
    await fill(firstField, "{unfinished-first");
    const secondField = await selectSchema("5. GET /orders/{id} Вызов");
    expect(secondField).not.toHaveValue("{unfinished-first");
    await userEvent.clear(secondField);
    await fill(secondField, "{unfinished-second");
    expect(screen.getByRole("button", { name: "Отменить" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Сохранить в браузере" }));
    first.unmount();
    renderInRouter(<DesignCanvasPage />);
    await userEvent.click(await screen.findByRole("button", { name: "Структура сценария" }));
    expect(await selectSchema("1. POST /orders Вызов")).toHaveValue("{unfinished-first");
    expect(await selectSchema("5. GET /orders/{id} Вызов")).toHaveValue("{unfinished-second");
  });

  it("edits a participant, undoes the change, and saves the restored scenario", async () => {
    renderInRouter(<DesignCanvasPage />);
    await addFromCanvasMenu("Добавить объект");
    const name = screen.getByRole("textbox", { name: "Название объекта" });
    await userEvent.clear(name);
    await userEvent.type(name, "Billing");
    await userEvent.click(screen.getByRole("button", { name: "Сохранить в браузере" }));
    expect(localStorage.getItem(STORAGE_KEY)).toContain("Billing");
    await userEvent.click(screen.getByRole("button", { name: "Отменить" }));
    expect(name).not.toHaveValue("Billing");
    await userEvent.click(screen.getByRole("button", { name: "Повторить" }));
    expect(name).toHaveValue("Billing");
  });

  it("restores the saved document after remount", async () => {
    const first = renderInRouter(<DesignCanvasPage />);
    const title = await screen.findByRole("textbox", { name: "Название сценария" });
    await userEvent.clear(title);
    await userEvent.type(title, "Согласование платежа");
    await userEvent.click(screen.getByRole("button", { name: "Сохранить в браузере" }));
    first.unmount();
    renderInRouter(<DesignCanvasPage />);
    expect(await screen.findByRole("textbox", { name: "Название сценария" })).toHaveValue(
      "Согласование платежа",
    );
  });

  it("reports a storage failure without claiming a save succeeded", async () => {
    renderInRouter(<DesignCanvasPage />);
    await screen.findByTestId("design-canvas-page");
    await userEvent.click(screen.getByRole("button", { name: "Сохранить в браузере" }));
    expect(screen.getByText("Сохранено в браузере")).toBeInTheDocument();
    vi.stubGlobal("localStorage", {
      getItem: () => null,
      setItem: () => {
        throw new Error("quota");
      },
    });
    await userEvent.click(screen.getByRole("button", { name: "Сохранить в браузере" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Не удалось сохранить");
    expect(screen.queryByText("Сохранено в браузере")).not.toBeInTheDocument();
  });

  it("imports an existing API as a local copy using only GET requests", async () => {
    const fetchMock = route({
      "GET /api/designs": () => json(200, { designs: [{ id: 12, name: "Каталог", version: 2 }] }),
      "GET /api/designs/12": () =>
        json(200, {
          design: { id: 12, name: "Каталог", version: 2 },
          draft: {
            id: 41,
            document: JSON.stringify({
              openapi: "3.1.0",
              info: { title: "Каталог", version: "1" },
              paths: {
                "/products": {
                  get: { summary: "Товары", responses: { "200": { description: "OK" } } },
                },
              },
            }),
          },
        }),
    });
    renderInRouter(<DesignCanvasPage />);
    await addFromCanvasMenu("Добавить существующий API");
    const modal = await screen.findByRole("dialog", { name: "Добавить существующий API" });
    await userEvent.selectOptions(await within(modal).findByLabelText("Проект API"), "12");
    await userEvent.click(within(modal).getByRole("button", { name: "Добавить локальную копию" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "Сохранить в браузере" }));
    expect(localStorage.getItem(STORAGE_KEY)).toContain("/products");
    expect(fetchMock.mock.calls.every(([, init]) => (init?.method ?? "GET") === "GET")).toBe(true);
  });

  it("transfers the local draft to the server without deleting its browser backup", async () => {
    const document = { ...exampleCanvas(), title: "Локальный сценарий" };
    const backup = serializeSavedCanvas(document, { all: "{}" });
    localStorage.setItem(STORAGE_KEY, backup);
    const fetchMock = route({
      "POST /api/design-scenarios": () =>
        json(201, {
          scenario: {
            id: 77,
            name: document.title,
            version: 1,
            draftRevisionId: 91,
            createdAt: 1_700_000_000,
            updatedAt: 1_700_000_000,
          },
          draft: {
            id: 91,
            scenarioId: 77,
            version: 1,
            hash: "hash",
            source: "ui",
            summary: "Перенос локального сценария",
            createdAt: 1_700_000_000,
            document,
            formDrafts: { all: "{}" },
          },
          revisions: [],
          diagnostics: [],
          contractUpdates: [],
        }),
    });
    renderInRouter(<DesignCanvasPage />);

    await userEvent.click(await screen.findByRole("button", { name: "Перенести на сервер" }));

    expect(await screen.findByTestId("test-router-elsewhere")).toBeInTheDocument();
    expect(localStorage.getItem(STORAGE_KEY)).toBe(backup);
    const write = fetchMock.mock.calls.find(
      ([input, init]) => String(input) === "/api/design-scenarios" && init?.method === "POST",
    );
    expect(JSON.parse(String(write?.[1]?.body))).toMatchObject({
      document: { title: "Локальный сценарий" },
      formDrafts: { all: "{}" },
      summary: "Перенос локального сценария",
    });
  });

  it("keeps edits made while local migration is pending", async () => {
    let resolveTransfer: ((response: Response) => void) | undefined;
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input) === "/api/design-scenarios" && init?.method === "POST") {
          return new Promise<Response>((resolve) => {
            resolveTransfer = resolve;
          });
        }
        if (String(input) === "/api/design-scenarios") {
          return Promise.resolve(json(200, { scenarios: [] }));
        }
        return Promise.resolve(json(500, { error: { code: "internal", message: "unexpected" } }));
      }),
    );
    renderInRouter(<DesignCanvasPage />);
    const title = await screen.findByRole("textbox", { name: "Название сценария" });

    await userEvent.click(screen.getByRole("button", { name: "Перенести на сервер" }));
    await userEvent.clear(title);
    await userEvent.type(title, "Новая правка во время переноса");
    const responseDocument = exampleCanvas();
    resolveTransfer?.(
      json(201, {
        scenario: { id: 88, name: responseDocument.title, version: 1, draftRevisionId: 91 },
        draft: { id: 91, document: responseDocument, formDrafts: {} },
        revisions: [],
        diagnostics: [],
        contractUpdates: [],
      }),
    );

    expect(await screen.findByText(/Серверный сценарий создан/)).toBeInTheDocument();
    expect(title).toHaveValue("Новая правка во время переноса");
    expect(screen.queryByTestId("test-router-elsewhere")).not.toBeInTheDocument();
  });
});

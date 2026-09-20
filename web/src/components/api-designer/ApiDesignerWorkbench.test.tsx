import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ApiDesignerWorkbench } from "./ApiDesignerWorkbench";
import { renderInRouter } from "@/test/render";
import { json, route } from "@/test/http";
import type {
  ApiDesignDetail,
  ApiDesignReview,
  ApiDesignRevisionSummary,
} from "@/api/generated/schemas";

// SVG layout requires browser text measurements. The workbench still exercises
// the real schema converter; the real renderer is verified in a browser.
vi.mock("./renderSchemaDiagram", () => ({
  renderSchemaDiagram: async () =>
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 100"></svg>',
}));

afterEach(() => {
  localStorage.clear();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("ApiDesignerWorkbench", () => {
  it("diagrams the unsaved document and clears the diagram when its source is invalid", async () => {
    route({
      "GET /api/designs/12": () => json(200, detailFixture()),
      "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=41": () =>
        json(200, diffFixture(detailFixture())),
    });
    renderInRouter(<ApiDesignerWorkbench id={12} />);
    await userEvent.click(await screen.findByRole("tab", { name: "Редактор" }));
    await userEvent.click(screen.getByRole("radio", { name: "Исходник" }));
    const source = screen.getByRole("textbox", { name: "Исходник OpenAPI" });
    await userEvent.clear(source);
    source.focus();
    await userEvent.paste(
      JSON.stringify({
        openapi: "3.1.0",
        info: { title: "Local", version: "1" },
        paths: {},
        components: { schemas: { LocalOnly: { properties: { unsaved: { type: "string" } } } } },
      }),
    );
    await userEvent.click(screen.getByRole("tab", { name: "Диаграмма" }));
    await userEvent.click(await screen.findByText("Исходник Mermaid"));
    const mermaidSource = screen.getByRole<HTMLTextAreaElement>("textbox", {
      name: "Исходник Mermaid",
    });
    expect(mermaidSource.value).toContain("LocalOnly");
    expect(mermaidSource.value).toContain("unsaved?");
    await waitFor(() => expect(screen.getByRole("button", { name: "Скачать SVG" })).toBeEnabled());

    await userEvent.click(screen.getByRole("tab", { name: "Редактор" }));
    await userEvent.clear(source);
    source.focus();
    await userEvent.paste("{invalid");
    await userEvent.click(screen.getByRole("tab", { name: "Диаграмма" }));
    expect(
      await screen.findByText("Исправьте JSON в редакторе, чтобы построить диаграмму."),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Скачать SVG" })).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Исходник Mermaid" })).not.toBeInTheDocument();
  });

  it("keeps a dirty source buffer when polling observes an external revision", async () => {
    let current = detailFixture();
    route({
      "GET /api/designs/12": () => json(200, current),
      "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=41": () =>
        json(200, diffFixture(current)),
      "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=42": () =>
        json(200, diffFixture(current)),
    });
    const { queryClient } = renderInRouter(<ApiDesignerWorkbench id={12} />);

    await userEvent.click(await screen.findByRole("tab", { name: "Редактор" }));
    await userEvent.click(screen.getByRole("radio", { name: "Исходник" }));
    const source = await screen.findByRole("textbox", { name: "Исходник OpenAPI" });
    await userEvent.clear(source);
    source.focus();
    await userEvent.paste(
      '{"openapi":"3.1.0","info":{"title":"Локально","version":"1"},"paths":{}}',
    );

    current = detailFixture({ version: 2, title: "Из MCP", revisionId: 42 });
    await queryClient.invalidateQueries({ queryKey: ["/api/designs/12"] });

    expect(await screen.findByText("На сервере появилась версия 2")).toBeInTheDocument();
    expect((source as HTMLTextAreaElement).value).toContain("Локально");
    expect((source as HTMLTextAreaElement).value).not.toContain("Из MCP");
  });

  it("shows an explicit conflict and never retries a stale save with the newer version", async () => {
    const fetchMock = route({
      "GET /api/designs/12": () => json(200, detailFixture()),
      "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=41": () =>
        json(200, diffFixture(detailFixture())),
      "PUT /api/designs/12/draft": () =>
        json(409, {
          error: {
            code: "design_conflict",
            message: "stale",
            details: { version: 2, draftRevisionId: 42 },
          },
        }),
    });
    renderInRouter(<ApiDesignerWorkbench id={12} />);

    await userEvent.click(await screen.findByRole("tab", { name: "Редактор" }));
    await userEvent.click(screen.getByRole("radio", { name: "Исходник" }));
    const source = await screen.findByRole("textbox", { name: "Исходник OpenAPI" });
    await userEvent.clear(source);
    source.focus();
    await userEvent.paste(
      '{"openapi":"3.1.0","info":{"title":"Локально","version":"1"},"paths":{}}',
    );
    await userEvent.type(screen.getByLabelText("Описание изменения"), "Уточнила контракт");
    await userEvent.click(screen.getByRole("button", { name: "Сохранить черновик" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Черновик изменился на сервере. Сравните версии и решите конфликт вручную.",
    );
    const writes = fetchMock.mock.calls.filter(
      ([input, init]) => String(input) === "/api/designs/12/draft" && init?.method === "PUT",
    );
    expect(writes).toHaveLength(1);
    expect(JSON.parse(String(writes[0]?.[1]?.body)).expectedVersion).toBe(1);
  });

  it("restores an unsaved per-project buffer after the workbench remounts", async () => {
    route({
      "GET /api/designs/12": () => json(200, detailFixture()),
      "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=41": () =>
        json(200, diffFixture(detailFixture())),
    });
    const first = renderInRouter(<ApiDesignerWorkbench id={12} />);

    await userEvent.click(await screen.findByRole("tab", { name: "Редактор" }));
    await userEvent.click(screen.getByRole("radio", { name: "Исходник" }));
    const source = await screen.findByRole("textbox", { name: "Исходник OpenAPI" });
    await userEvent.clear(source);
    source.focus();
    await userEvent.paste(
      '{"openapi":"3.1.0","info":{"title":"Буфер в браузере","version":"1"},"paths":{}}',
    );
    await waitForStoredDraft();
    first.unmount();

    renderInRouter(<ApiDesignerWorkbench id={12} />);
    await userEvent.click(await screen.findByRole("tab", { name: "Редактор" }));
    await userEvent.click(screen.getByRole("radio", { name: "Исходник" }));

    expect(
      ((await screen.findByRole("textbox", { name: "Исходник OpenAPI" })) as HTMLTextAreaElement)
        .value,
    ).toContain("Буфер в браузере");
  });

  it("keeps a dirty source buffer while focus mode hides the mounted API tree", async () => {
    route({
      "GET /api/designs/12": () => json(200, detailFixture()),
      "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=41": () =>
        json(200, diffFixture(detailFixture())),
    });
    renderInRouter(<ApiDesignerWorkbench id={12} />);

    await userEvent.click(await screen.findByRole("tab", { name: "Редактор" }));
    await userEvent.click(screen.getByRole("radio", { name: "Исходник" }));
    const source = screen.getByRole("textbox", { name: "Исходник OpenAPI" });
    await userEvent.clear(source);
    source.focus();
    await userEvent.paste(
      '{"openapi":"3.1.0","info":{"title":"Локальный буфер","version":"1"},"paths":{}}',
    );

    const focusMode = screen.getByRole("button", { name: "Только редактор" });
    const tree = screen.getByRole("complementary", { name: "Дерево API" });
    await userEvent.click(focusMode);

    expect(focusMode).toHaveAttribute("aria-pressed", "true");
    expect(tree).toHaveAttribute("hidden");
    expect(screen.getByRole("button", { name: "Открыть дерево API" })).toBeInTheDocument();
    expect((source as HTMLTextAreaElement).value).toContain("Локальный буфер");

    await userEvent.click(focusMode);
    expect(focusMode).toHaveAttribute("aria-pressed", "false");
    expect(tree).not.toHaveAttribute("hidden");
    expect((source as HTMLTextAreaElement).value).toContain("Локальный буфер");
  });

  it("keeps an invalid nested form draft while focus mode toggles", async () => {
    route({
      "GET /api/designs/12": () => json(200, detailFixture()),
      "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=41": () =>
        json(200, diffFixture(detailFixture())),
    });
    renderInRouter(<ApiDesignerWorkbench id={12} />);

    await userEvent.click(await screen.findByRole("button", { name: "Order" }));
    await userEvent.click(screen.getByRole("tab", { name: "Редактор" }));
    const schema = screen.getByRole("textbox", { name: "Полная схема JSON" });
    await userEvent.clear(schema);
    schema.focus();
    await userEvent.paste("{invalid in focus mode");

    const focusMode = screen.getByRole("button", { name: "Только редактор" });
    await userEvent.click(focusMode);
    expect(schema).toHaveValue("{invalid in focus mode");
    expect(screen.getByRole("button", { name: "Сохранить черновик" })).toBeDisabled();

    await userEvent.click(focusMode);
    expect(schema).toHaveValue("{invalid in focus mode");
    expect(screen.getByRole("button", { name: "Сохранить черновик" })).toBeDisabled();
  });

  it("keeps the inline operation composer draft across tree drawer reopenings", async () => {
    route({
      "GET /api/designs/12": () => json(200, detailFixture()),
      "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=41": () =>
        json(200, diffFixture(detailFixture())),
    });
    renderInRouter(<ApiDesignerWorkbench id={12} />);

    await userEvent.click(await screen.findByRole("button", { name: "Добавить операцию" }));
    const inlinePath = screen.getByLabelText("Путь");
    const inlineMethod = screen.getByLabelText("Метод");
    await userEvent.clear(inlinePath);
    await userEvent.type(inlinePath, "/draft-orders");
    await userEvent.selectOptions(inlineMethod, "post");

    await userEvent.click(screen.getByRole("button", { name: "Только редактор" }));
    const openTree = screen.getByRole("button", { name: "Открыть дерево API" });
    await userEvent.click(openTree);
    let drawer = await screen.findByRole("dialog", { name: "Дерево API" });

    expect(within(drawer).getByLabelText("Путь")).toHaveValue("/draft-orders");
    expect(within(drawer).getByLabelText("Метод")).toHaveValue("post");

    await userEvent.keyboard("{Escape}");
    await vi.waitFor(() => expect(screen.queryByRole("dialog", { name: "Дерево API" })).toBeNull());
    await userEvent.click(openTree);
    drawer = await screen.findByRole("dialog", { name: "Дерево API" });

    expect(within(drawer).getByLabelText("Путь")).toHaveValue("/draft-orders");
    expect(within(drawer).getByLabelText("Метод")).toHaveValue("post");
  });

  it.each([
    ["Изменения", "Diff"],
    ["История", "История"],
    ["Проверки", "Проверки"],
    ["Мок", "Мок"],
  ])(
    "opens the %s inspector section and returns focus to its trigger on Escape",
    async (label, section) => {
      route({
        "GET /api/designs/12": () => json(200, detailFixture()),
        "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=41": () =>
          json(200, diffFixture(detailFixture())),
      });
      renderInRouter(<ApiDesignerWorkbench id={12} />);

      const trigger = await screen.findByRole("button", { name: label });
      await userEvent.click(trigger);
      const inspector = await screen.findByRole("dialog", { name: "Инспектор" });

      expect(trigger).toHaveAttribute("aria-expanded", "true");
      expect(within(inspector).getByRole("radio", { name: section })).toBeChecked();

      await userEvent.keyboard("{Escape}");
      await vi.waitFor(() =>
        expect(screen.queryByRole("dialog", { name: "Инспектор" })).toBeNull(),
      );
      await vi.waitFor(() => expect(trigger).toHaveFocus());
      expect(trigger).toHaveAttribute("aria-expanded", "false");
    },
  );

  it("closes the changes inspector and opens comparison for the selected change", async () => {
    const detail = detailFixture();
    route({
      "GET /api/designs/12": () => json(200, detail),
      "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=41": () =>
        json(200, {
          ...diffFixture(detail),
          changes: [
            {
              pointer: "/info/title",
              kind: "changed",
              impact: "compatible",
              description: "Название API изменено",
              before: "Заказы",
              after: "Заказы API",
            },
          ],
        }),
    });
    renderInRouter(<ApiDesignerWorkbench id={12} />);

    await userEvent.click(await screen.findByRole("button", { name: "Изменения" }));
    const inspector = await screen.findByRole("dialog", { name: "Инспектор" });
    await userEvent.click(within(inspector).getByRole("button", { name: /Название API изменено/ }));

    expect(screen.queryByRole("dialog", { name: "Инспектор" })).toBeNull();
    expect(screen.getByRole("tab", { name: "Сравнение" })).toHaveAttribute("aria-selected", "true");
  });

  it("marks a frozen review stale and disables publication after the draft moves", async () => {
    route({
      "GET /api/designs/12": () =>
        json(
          200,
          detailFixture({
            version: 3,
            revisionId: 43,
            reviews: [
              {
                id: 8,
                designId: 12,
                revisionId: 42,
                baseRevisionId: 41,
                status: "pending",
                summary: "Добавлен ответ 422",
                source: "mcp",
                createdAt: 1_795_000_010,
              },
            ],
          }),
        ),
      "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=42": () =>
        json(200, diffFixture(detailFixture())),
      "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=43": () =>
        json(200, diffFixture(detailFixture({ version: 3, revisionId: 43 }))),
      "POST /api/designs/12/reviews": () =>
        json(201, {
          id: 9,
          designId: 12,
          revisionId: 43,
          baseRevisionId: 41,
          status: "pending",
          summary: "Текущий черновик",
          source: "ui",
          createdAt: 1_795_000_020,
        }),
    });
    renderInRouter(<ApiDesignerWorkbench id={12} reviewId={8} />);

    await userEvent.click(await screen.findByRole("tab", { name: "Проверка" }));
    expect(screen.getByText("Кандидат устарел")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Опубликовать версию" })).toBeDisabled();
    expect(screen.getByText(/зафиксирована на версии 2/i)).toBeInTheDocument();

    await userEvent.type(
      screen.getByLabelText("Что нужно проверить в новом кандидате"),
      "Текущий черновик",
    );
    await userEvent.click(screen.getByRole("button", { name: "Создать новый кандидат" }));

    expect(await screen.findByText("Кандидат #9")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Опубликовать версию" })).toBeEnabled();
  });

  it("uses fresh server status for a candidate created in this session", async () => {
    let current = detailFixture({ version: 2, revisionId: 42 });
    const created: ApiDesignReview = {
      id: 9,
      designId: 12,
      revisionId: 42,
      baseRevisionId: 41,
      status: "pending",
      summary: "К выпуску",
      source: "ui",
      createdAt: 1_795_000_020,
    };
    route({
      "GET /api/designs/12": () => json(200, current),
      "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=42": () =>
        json(200, diffFixture(current)),
      "POST /api/designs/12/reviews": () => json(201, created),
    });
    const { queryClient } = renderInRouter(<ApiDesignerWorkbench id={12} />);
    await userEvent.click(await screen.findByRole("tab", { name: "Проверка" }));
    await userEvent.type(screen.getByLabelText("Что нужно проверить"), "К выпуску");
    await userEvent.click(screen.getByRole("button", { name: "Создать кандидат" }));
    expect(await screen.findByText("Готов к публикации")).toBeInTheDocument();

    current = detailFixture({
      version: 2,
      revisionId: 42,
      reviews: [{ ...created, status: "published" }],
    });
    await queryClient.invalidateQueries({ queryKey: ["/api/designs/12"] });

    expect(await screen.findByText("Опубликован")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Опубликовать версию" })).toBeDisabled();
  });

  it("keys the default diff by the adopted draft revision", async () => {
    let current = detailFixture();
    const fetchMock = route({
      "GET /api/designs/12": () => json(200, current),
      "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=41": () =>
        json(200, diffFixture(current)),
      "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=42": () =>
        json(200, diffFixture(current)),
    });
    const { queryClient } = renderInRouter(<ApiDesignerWorkbench id={12} />);
    await screen.findByRole("heading", { level: 1, name: "Заказы API" });

    current = detailFixture({ version: 2, title: "Из MCP", revisionId: 42 });
    await queryClient.invalidateQueries({ queryKey: ["/api/designs/12"] });

    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/designs/12/diff?fromRevisionId=41&toRevisionId=42",
        expect.objectContaining({ method: "GET" }),
      );
    });
  });

  it("keeps edits typed while a save request is pending", async () => {
    let resolveSave: ((response: Response) => void) | undefined;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const key = `${(init?.method ?? "GET").toUpperCase()} ${String(input)}`;
      if (key === "GET /api/designs/12") return Promise.resolve(json(200, detailFixture()));
      if (key === "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=41") {
        return Promise.resolve(json(200, diffFixture(detailFixture())));
      }
      if (key === "PUT /api/designs/12/draft") {
        return new Promise<Response>((resolve) => {
          resolveSave = resolve;
        });
      }
      return Promise.resolve(json(500, { error: { code: "internal", message: key } }));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderInRouter(<ApiDesignerWorkbench id={12} />);

    await userEvent.click(await screen.findByRole("tab", { name: "Редактор" }));
    await userEvent.click(screen.getByRole("radio", { name: "Исходник" }));
    const source = await screen.findByRole("textbox", { name: "Исходник OpenAPI" });
    await userEvent.clear(source);
    source.focus();
    await userEvent.paste(
      '{"openapi":"3.1.0","info":{"title":"Отправлено","version":"1"},"paths":{}}',
    );
    await userEvent.type(screen.getByLabelText("Описание изменения"), "Первая правка");
    await userEvent.click(screen.getByRole("button", { name: "Сохранить черновик" }));

    await userEvent.clear(source);
    source.focus();
    await userEvent.paste(
      '{"openapi":"3.1.0","info":{"title":"После отправки","version":"1"},"paths":{}}',
    );
    const write = fetchMock.mock.calls.find(
      ([input, init]) => String(input) === "/api/designs/12/draft" && init?.method === "PUT",
    );
    const submitted = JSON.parse(String(write?.[1]?.body)) as { document: string };
    const saved = detailFixture({ version: 2, title: "Отправлено", revisionId: 42 });
    saved.draft.document = submitted.document;
    resolveSave?.(json(200, saved));

    await screen.findByText("Черновик · версия 2");
    await vi.waitFor(() =>
      expect((source as HTMLTextAreaElement).value).toContain("После отправки"),
    );
    await vi.waitFor(() =>
      expect(localStorage.getItem("mocker:api-design:12:draft")).toContain("После отправки"),
    );
  });

  it("keeps nested field drafts created while a save request is pending", async () => {
    let resolveSave: ((response: Response) => void) | undefined;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const key = `${(init?.method ?? "GET").toUpperCase()} ${String(input)}`;
      if (key === "GET /api/designs/12") return Promise.resolve(json(200, detailFixture()));
      if (key === "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=41") {
        return Promise.resolve(json(200, diffFixture(detailFixture())));
      }
      if (key === "PUT /api/designs/12/draft") {
        return new Promise<Response>((resolve) => {
          resolveSave = resolve;
        });
      }
      return Promise.resolve(json(500, { error: { code: "internal", message: key } }));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderInRouter(<ApiDesignerWorkbench id={12} />);

    await userEvent.click(await screen.findByRole("button", { name: "Order" }));
    await userEvent.click(screen.getByRole("tab", { name: "Редактор" }));
    await userEvent.type(screen.getByLabelText("Описание схемы"), "Заказ");
    await userEvent.type(screen.getByLabelText("Описание изменения"), "Описание заказа");
    await userEvent.click(screen.getByRole("button", { name: "Сохранить черновик" }));

    const schema = screen.getByRole("textbox", { name: "Полная схема JSON" });
    await userEvent.clear(schema);
    schema.focus();
    await userEvent.paste("{invalid while saving");
    const write = fetchMock.mock.calls.find(
      ([input, init]) => String(input) === "/api/designs/12/draft" && init?.method === "PUT",
    );
    const submitted = JSON.parse(String(write?.[1]?.body)) as { document: string };
    const saved = detailFixture({ version: 2, revisionId: 42 });
    saved.draft.document = submitted.document;
    resolveSave?.(json(200, saved));

    await screen.findByText("Черновик · версия 2");
    await vi.waitFor(() => expect(schema).toHaveValue("{invalid while saving"));
    expect(screen.getByRole("button", { name: "Сохранить черновик" })).toBeDisabled();
    await vi.waitFor(() =>
      expect(localStorage.getItem("mocker:api-design:12:draft")).toContain("{invalid while saving"),
    );
  });

  it("creates, copies, and deletes API objects from the tree", async () => {
    route({
      "GET /api/designs/12": () => json(200, detailFixture()),
      "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=41": () =>
        json(200, diffFixture(detailFixture())),
    });
    renderInRouter(<ApiDesignerWorkbench id={12} />);
    await screen.findByRole("heading", { level: 1, name: "Заказы API" });

    await userEvent.click(screen.getByRole("button", { name: /GET.*\/orders/ }));
    await userEvent.click(screen.getByRole("button", { name: "Копировать GET /orders" }));
    await userEvent.clear(screen.getByLabelText("Новый путь копии"));
    await userEvent.type(screen.getByLabelText("Новый путь копии"), "/orders-copy");
    await userEvent.click(screen.getByRole("button", { name: "Создать копию" }));
    await userEvent.click(screen.getByRole("radio", { name: "Исходник" }));
    expect(
      (screen.getByRole("textbox", { name: "Исходник OpenAPI" }) as HTMLTextAreaElement).value,
    ).toContain("listOrdersCopy");

    await userEvent.click(screen.getByRole("button", { name: "Удалить GET /orders-copy" }));
    await userEvent.click(screen.getByRole("radio", { name: "Исходник" }));
    await vi.waitFor(() =>
      expect(
        (screen.getByRole("textbox", { name: "Исходник OpenAPI" }) as HTMLTextAreaElement).value,
      ).not.toContain("listOrdersCopy"),
    );

    await userEvent.click(screen.getByRole("button", { name: "Добавить операцию" }));
    await userEvent.clear(screen.getByLabelText("Путь"));
    await userEvent.type(screen.getByLabelText("Путь"), "/health");
    await userEvent.click(screen.getByRole("button", { name: "Создать операцию" }));
    await userEvent.click(screen.getByRole("radio", { name: "Исходник" }));
    await vi.waitFor(() =>
      expect(
        (screen.getByRole("textbox", { name: "Исходник OpenAPI" }) as HTMLTextAreaElement).value,
      ).toContain('"/health"'),
    );

    await userEvent.click(screen.getByRole("button", { name: "Добавить схему" }));
    await userEvent.type(screen.getByLabelText("Название схемы"), "Customer");
    await userEvent.click(screen.getByRole("button", { name: "Создать схему" }));
    await userEvent.click(screen.getByRole("radio", { name: "Исходник" }));
    await vi.waitFor(() =>
      expect(
        (screen.getByRole("textbox", { name: "Исходник OpenAPI" }) as HTMLTextAreaElement).value,
      ).toContain('"Customer"'),
    );
    await userEvent.click(screen.getByRole("button", { name: "Удалить схему Customer" }));
    await userEvent.click(screen.getByRole("radio", { name: "Исходник" }));
    await vi.waitFor(() =>
      expect(
        (screen.getByRole("textbox", { name: "Исходник OpenAPI" }) as HTMLTextAreaElement).value,
      ).not.toContain('"Customer"'),
    );
  });

  it("uses one exact revision pair for history source and structural diffs", async () => {
    const detail = detailFixture({
      version: 3,
      revisionId: 43,
      reviews: [
        {
          id: 8,
          designId: 12,
          revisionId: 42,
          baseRevisionId: 41,
          status: "pending",
          summary: "Кандидат",
          source: "mcp",
          createdAt: 1_795_000_010,
        },
      ],
    });
    const fetchMock = route({
      "GET /api/designs/12": () => json(200, detail),
      "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=42": () =>
        json(200, diffFixture(detail)),
      "GET /api/designs/12/diff?fromRevisionId=43&toRevisionId=43": () =>
        json(200, diffFixture(detail)),
      "GET /api/designs/12/revisions/43": () => json(200, detail.draft),
    });
    renderInRouter(<ApiDesignerWorkbench id={12} reviewId={8} />);

    const history = await screen.findByRole("button", { name: "История" });
    await userEvent.click(history);
    const inspector = await screen.findByRole("dialog", { name: "Инспектор" });
    await userEvent.click(within(inspector).getByRole("button", { name: /Версия 3/ }));

    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/designs/12/diff?fromRevisionId=43&toRevisionId=43",
        expect.objectContaining({ method: "GET" }),
      );
    });
    expect(screen.queryByRole("dialog", { name: "Инспектор" })).toBeNull();
    expect(screen.getByRole("tab", { name: "Сравнение" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("Сравнение revisions 43 → 43")).toBeInTheDocument();
  });

  it("keeps invalid form buffers in the draft and conflict lifecycle", async () => {
    let current = detailFixture();
    route({
      "GET /api/designs/12": () => json(200, current),
      "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=41": () =>
        json(200, diffFixture(current)),
      "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=42": () =>
        json(200, diffFixture(current)),
    });
    const { queryClient } = renderInRouter(<ApiDesignerWorkbench id={12} />);
    await userEvent.click(await screen.findByRole("button", { name: "Order" }));
    await userEvent.click(screen.getByRole("tab", { name: "Редактор" }));
    const schema = screen.getByRole("textbox", { name: "Полная схема JSON" });
    await userEvent.clear(schema);
    schema.focus();
    await userEvent.paste("{invalid schema");
    await userEvent.type(screen.getByLabelText("Описание изменения"), "Схема заказа");

    expect(screen.getByRole("button", { name: "Сохранить черновик" })).toBeDisabled();
    await vi.waitFor(() =>
      expect(localStorage.getItem("mocker:api-design:12:draft")).toContain("{invalid schema"),
    );

    await userEvent.click(screen.getByRole("button", { name: "Документ OpenAPI" }));
    await userEvent.click(screen.getByRole("button", { name: "Order" }));
    expect(screen.getByRole("textbox", { name: "Полная схема JSON" })).toHaveValue(
      "{invalid schema",
    );

    current = detailFixture({ version: 2, revisionId: 42, title: "Из MCP" });
    await queryClient.invalidateQueries({ queryKey: ["/api/designs/12"] });
    expect(await screen.findByText("На сервере появилась версия 2")).toBeInTheDocument();
  });

  it.each(["9007199254740993", "0.1234567890123456789", "1e400"])(
    "keeps the lossy numeric literal %s in source-only mode",
    async (literal) => {
      const detail = detailFixture();
      detail.draft.document = detail.draft.document.replace(
        '"description": "OK"',
        `"description": "OK", "example": ${literal}`,
      );
      route({
        "GET /api/designs/12": () => json(200, detail),
        "GET /api/designs/12/diff?fromRevisionId=41&toRevisionId=41": () =>
          json(200, diffFixture(detail)),
      });
      renderInRouter(<ApiDesignerWorkbench id={12} />);

      await userEvent.click(await screen.findByRole("tab", { name: "Редактор" }));
      expect(screen.getByRole("radio", { name: "Форма" })).toBeDisabled();
      expect(
        screen.getByText("Число нельзя безопасно представить в JavaScript"),
      ).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Добавить операцию" })).toBeDisabled();

      await userEvent.click(screen.getByRole("radio", { name: "Исходник" }));
      expect(
        (screen.getByRole("textbox", { name: "Исходник OpenAPI" }) as HTMLTextAreaElement).value,
      ).toContain(literal);
    },
  );
});

type DetailOptions = {
  version?: number;
  title?: string;
  revisionId?: number;
  reviews?: ApiDesignReview[];
};

function detailFixture(options: DetailOptions = {}): ApiDesignDetail {
  const version = options.version ?? 1;
  const revisionId = options.revisionId ?? 41;
  const document = JSON.stringify(
    {
      openapi: "3.1.0",
      info: { title: options.title ?? "Заказы API", version: "1" },
      paths: {
        "/orders": {
          get: {
            operationId: "listOrders",
            summary: "Список заказов",
            responses: { "200": { description: "OK" } },
          },
        },
      },
      components: { schemas: { Order: { type: "object" } } },
    },
    null,
    2,
  );
  const revisions: ApiDesignRevisionSummary[] = [];
  if (version > 1) {
    revisions.push({
      id: 41,
      designId: 12,
      version: 1,
      hash: "sha256:1",
      source: "ui",
      summary: "Проект создан",
      changeSetId: null,
      createdAt: 1_795_000_001,
    });
  }
  if (options.reviews?.length) {
    revisions.push({
      id: 42,
      designId: 12,
      version: 2,
      hash: "sha256:2",
      source: "mcp",
      summary: "Кандидат",
      changeSetId: null,
      createdAt: 1_795_000_002,
    });
  }
  revisions.push({
    id: revisionId,
    designId: 12,
    version,
    hash: `sha256:${version}`,
    source: version === 1 ? "ui" : "mcp",
    summary: version === 1 ? "Проект создан" : "Правка агента",
    changeSetId: null,
    createdAt: 1_795_000_000 + version,
  });
  return {
    design: {
      id: 12,
      name: "Заказы API",
      version,
      draftWorkspaceId: 31,
      publishedWorkspaceId: 32,
      draftUrl: "http://orders-draft.mock.local",
      publishedUrl: "http://orders.mock.local",
      draftRevisionId: revisionId,
      publishedRevisionId: null,
      latestReviewId: options.reviews?.at(-1)?.id ?? null,
      createdAt: 1_795_000_000,
      updatedAt: 1_795_000_000,
    },
    draft: {
      id: revisionId,
      designId: 12,
      version,
      hash: `sha256:${version}`,
      source: version === 1 ? "ui" : "mcp",
      summary: version === 1 ? "Проект создан" : "Правка агента",
      changeSetId: null,
      createdAt: 1_795_000_000 + version,
      document,
    },
    published: null,
    revisions,
    changeSets: [],
    reviews: options.reviews ?? [],
    releases: [],
  };
}

function diffFixture(detail: ReturnType<typeof detailFixture>) {
  return {
    from: detail.draft,
    to: detail.draft,
    changes: [],
  };
}

async function waitForStoredDraft(): Promise<void> {
  await vi.waitFor(() => {
    expect(localStorage.getItem("mocker:api-design:12:draft")).toContain("Буфер в браузере");
  });
}

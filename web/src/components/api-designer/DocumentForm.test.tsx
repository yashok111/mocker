import { useState } from "react";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "@/test/render";
import { fill } from "@/test/user";
import { DocumentForm, createFormDraftStore, type FormDraftStore } from "./DocumentForm";
import type { ApiDocument, DocumentSelection } from "./documentModel";

const DOCUMENT: ApiDocument = {
  openapi: "3.1.0",
  info: { title: "Orders", version: "1.0", "x-owner": "BI" },
  "x-root": "keep",
  paths: {
    "/orders": {
      get: {
        summary: "List",
        operationId: "listOrders",
        "x-op": "keep",
        parameters: [
          {
            name: "limit",
            in: "query",
            description: "Page size",
            schema: { type: "integer", minimum: 1, "x-unit": "rows" },
          },
        ],
        responses: {
          "200": {
            description: "OK",
            content: {
              "application/json": {
                schema: { type: "array", items: { $ref: "#/components/schemas/Order" } },
                example: [{ id: "1" }],
              },
            },
          },
          "404": {
            description: "Missing",
            headers: { "x-reason": { schema: { type: "string" } } },
          },
        },
      },
    },
  },
  components: {
    schemas: {
      Order: {
        type: "object",
        required: ["id"],
        properties: { id: { type: "string", format: "uuid", "x-prop": true } },
        oneOf: [{ type: "object" }],
      },
    },
  },
};

function Harness({
  selection,
  onChange,
  initialDocument = DOCUMENT,
  draftStore,
}: {
  selection: DocumentSelection;
  onChange?: (d: ApiDocument) => void;
  initialDocument?: ApiDocument;
  draftStore?: FormDraftStore;
}) {
  const [document, setDocument] = useState(initialDocument);
  return (
    <DocumentForm
      document={document}
      selection={selection}
      onChange={(next) => {
        setDocument(next);
        onChange?.(next);
      }}
      draftStore={draftStore}
    />
  );
}

function SelectionHarness({
  initialSelection,
  draftStore,
}: {
  initialSelection: DocumentSelection;
  draftStore: FormDraftStore;
}) {
  const [selection, setSelection] = useState(initialSelection);
  return (
    <>
      <button onClick={() => setSelection({ kind: "document" })}>Metadata</button>
      <button onClick={() => setSelection({ kind: "operation", path: "/orders", method: "get" })}>
        Operation
      </button>
      <button onClick={() => setSelection({ kind: "schema", name: "Order" })}>Schema</button>
      <Harness selection={selection} draftStore={draftStore} />
    </>
  );
}

describe("DocumentForm", () => {
  it("restores pending operation method and path after selection switches and hydration", async () => {
    const store = createFormDraftStore();
    const mounted = renderWithProviders(
      <SelectionHarness
        initialSelection={{ kind: "operation", path: "/orders", method: "get" }}
        draftStore={store}
      />,
    );

    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Метод" }), "post");
    const path = screen.getByRole("textbox", { name: "Путь" });
    await userEvent.clear(path);
    await fill(path, "/purchases/{id}");
    expect(store.getSnapshot()).toEqual({ dirty: true, invalid: false });

    await userEvent.click(screen.getByRole("button", { name: "Schema" }));
    await userEvent.click(screen.getByRole("button", { name: "Operation" }));
    expect(screen.getByRole("combobox", { name: "Метод" })).toHaveValue("post");
    expect(screen.getByRole("textbox", { name: "Путь" })).toHaveValue("/purchases/{id}");

    const serialized = store.serialize();
    mounted.unmount();
    const restored = createFormDraftStore();
    restored.hydrate(serialized);
    renderWithProviders(
      <SelectionHarness
        initialSelection={{ kind: "operation", path: "/orders", method: "get" }}
        draftStore={restored}
      />,
    );
    expect(screen.getByRole("combobox", { name: "Метод" })).toHaveValue("post");
    expect(screen.getByRole("textbox", { name: "Путь" })).toHaveValue("/purchases/{id}");
    expect(restored.getSnapshot()).toEqual({ dirty: true, invalid: false });
  });

  it("restores a pending schema rename after selection switches and hydration", async () => {
    const store = createFormDraftStore();
    const mounted = renderWithProviders(
      <SelectionHarness initialSelection={{ kind: "schema", name: "Order" }} draftStore={store} />,
    );

    const name = screen.getByRole("textbox", { name: "Имя схемы" });
    await userEvent.clear(name);
    await fill(name, "Purchase");
    expect(store.getSnapshot()).toEqual({ dirty: true, invalid: false });

    await userEvent.click(screen.getByRole("button", { name: "Metadata" }));
    await userEvent.click(screen.getByRole("button", { name: "Schema" }));
    expect(screen.getByRole("textbox", { name: "Имя схемы" })).toHaveValue("Purchase");

    const serialized = store.serialize();
    mounted.unmount();
    const restored = createFormDraftStore();
    restored.hydrate(serialized);
    renderWithProviders(
      <SelectionHarness
        initialSelection={{ kind: "schema", name: "Order" }}
        draftStore={restored}
      />,
    );
    expect(screen.getByRole("textbox", { name: "Имя схемы" })).toHaveValue("Purchase");
    expect(restored.getSnapshot()).toEqual({ dirty: true, invalid: false });
  });

  it("clears pending operation address drafts after the rename is applied", async () => {
    const store = createFormDraftStore();
    let changed: ApiDocument | undefined;
    renderWithProviders(
      <Harness
        selection={{ kind: "operation", path: "/orders", method: "get" }}
        draftStore={store}
        onChange={(document) => (changed = document)}
      />,
    );

    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Метод" }), "post");
    const path = screen.getByRole("textbox", { name: "Путь" });
    await userEvent.clear(path);
    await fill(path, "/purchases");
    expect(store.getSnapshot().dirty).toBe(true);
    await userEvent.click(screen.getByRole("button", { name: "Изменить адрес" }));

    expect(store.getSnapshot()).toEqual({ dirty: false, invalid: false });
    expect(store.serialize()).toBe("{}");
    expect(changed).toMatchObject({ paths: { "/purchases": { post: { summary: "List" } } } });
  });

  it("restores a pending response status and clears it after adding the response", async () => {
    const store = createFormDraftStore();
    const mounted = renderWithProviders(
      <SelectionHarness
        initialSelection={{ kind: "operation", path: "/orders", method: "get" }}
        draftStore={store}
      />,
    );

    const status = screen.getByRole("textbox", { name: "Новый статус ответа" });
    await userEvent.clear(status);
    await fill(status, "299");
    expect(store.getSnapshot()).toEqual({ dirty: true, invalid: false });
    await userEvent.click(screen.getByRole("button", { name: "Schema" }));
    await userEvent.click(screen.getByRole("button", { name: "Operation" }));
    expect(screen.getByRole("textbox", { name: "Новый статус ответа" })).toHaveValue("299");

    const serialized = store.serialize();
    mounted.unmount();
    const restored = createFormDraftStore();
    restored.hydrate(serialized);
    renderWithProviders(
      <SelectionHarness
        initialSelection={{ kind: "operation", path: "/orders", method: "get" }}
        draftStore={restored}
      />,
    );
    expect(screen.getByRole("textbox", { name: "Новый статус ответа" })).toHaveValue("299");
    await userEvent.click(screen.getByRole("button", { name: "Добавить ответ" }));
    expect(screen.getByRole("textbox", { name: "Новый статус ответа" })).toHaveValue("201");
    expect(restored.getSnapshot()).toEqual({ dirty: false, invalid: false });
  });

  it.each(["9007199254740993", "0.1234567890123456789", "1e400"])(
    "keeps lossy numeric token %s in a pending JSON field until corrected",
    async (token) => {
      const store = createFormDraftStore();
      const changed = vi.fn<(document: ApiDocument) => void>();
      renderWithProviders(
        <Harness
          selection={{ kind: "schema", name: "Order" }}
          draftStore={store}
          onChange={changed}
        />,
      );
      const json = screen.getByRole("textbox", { name: "Полная схема JSON" });
      const source = `{"example":${token}}`;
      await userEvent.clear(json);
      await fill(json, source);
      expect(changed).not.toHaveBeenCalled();
      expect(json).toHaveValue(source);
      expect(store.getSnapshot()).toEqual({ dirty: true, invalid: true });
      expect(screen.getByText(/без потери точности/)).toBeInTheDocument();
      await userEvent.clear(json);
      await fill(json, '{"example":1.25,"description":"9007199254740993"}');
      expect(changed).toHaveBeenCalledOnce();
      expect(store.getSnapshot()).toEqual({ dirty: false, invalid: false });
    },
  );

  it("moves invalid buffers with schema rename and clears buffers when their response is deleted", async () => {
    const store = createFormDraftStore();
    const mounted = renderWithProviders(
      <Harness selection={{ kind: "schema", name: "Order" }} draftStore={store} />,
    );
    const json = screen.getByRole("textbox", { name: "Полная схема JSON" });
    await userEvent.clear(json);
    await fill(json, "{unfinished");
    const name = screen.getByRole("textbox", { name: "Имя схемы" });
    await userEvent.clear(name);
    await fill(name, "Purchase");
    await userEvent.click(screen.getByRole("button", { name: "Переименовать" }));
    expect(screen.getByRole("textbox", { name: "Полная схема JSON" })).toHaveValue("{unfinished");
    expect(store.get("/components/schemas/Order")).toBeUndefined();
    expect(store.get("/components/schemas/Purchase")?.source).toBe("{unfinished");
    expect(store.get("/components/schemas/Purchase/$form/name")).toBeUndefined();
    mounted.unmount();
    const responseStore = createFormDraftStore();
    renderWithProviders(
      <Harness
        selection={{ kind: "operation", path: "/orders", method: "get" }}
        draftStore={responseStore}
      />,
    );
    const example = screen.getByRole("textbox", { name: "Пример ответа application/json" });
    await userEvent.clear(example);
    await fill(example, "{unfinished");
    expect(responseStore.getSnapshot().invalid).toBe(true);
    await userEvent.click(screen.getByRole("button", { name: "Удалить ответ 200" }));
    expect(responseStore.getSnapshot()).toEqual({ dirty: false, invalid: false });
  });
  it("restores invalid field buffers after form unmount and serialization and clears them only after correction", async () => {
    const store = createFormDraftStore();
    const changed = vi.fn<(document: ApiDocument) => void>();
    const mounted = renderWithProviders(
      <Harness
        selection={{ kind: "schema", name: "Order" }}
        draftStore={store}
        onChange={changed}
      />,
    );
    const json = screen.getByRole("textbox", { name: "Полная схема JSON" });
    await userEvent.clear(json);
    await fill(json, "{invalid schema");
    expect(store.getSnapshot()).toEqual({ dirty: true, invalid: true });
    expect(changed).not.toHaveBeenCalled();
    const serialized = store.serialize();
    mounted.unmount();
    const restored = createFormDraftStore();
    restored.hydrate(serialized);
    renderWithProviders(
      <Harness
        selection={{ kind: "schema", name: "Order" }}
        draftStore={restored}
        onChange={changed}
      />,
    );
    const restoredJson = screen.getByRole("textbox", { name: "Полная схема JSON" });
    expect(restoredJson).toHaveValue("{invalid schema");
    expect(restored.getSnapshot()).toEqual({ dirty: true, invalid: true });
    await userEvent.clear(restoredJson);
    await fill(restoredJson, '{"type":"string"}');
    expect(restored.getSnapshot()).toEqual({ dirty: false, invalid: false });
    expect(changed).toHaveBeenLastCalledWith(
      expect.objectContaining({
        components: expect.objectContaining({
          schemas: expect.objectContaining({ Order: { type: "string" } }),
        }),
      }),
    );
  });

  it("keeps field drafts on object selection changes and separates same-media response buffers", async () => {
    const input = structuredClone(DOCUMENT);
    const responses = (input.paths as Record<string, Record<string, Record<string, unknown>>>)[
      "/orders"
    ]!.get!.responses as Record<string, unknown>;
    responses["404"] = structuredClone(responses["200"]);
    function SelectionHarness() {
      const [selection, setSelection] = useState<DocumentSelection>({
        kind: "operation",
        path: "/orders",
        method: "get",
      });
      return (
        <>
          <button onClick={() => setSelection({ kind: "document" })}>Metadata</button>
          <button
            onClick={() => setSelection({ kind: "operation", path: "/orders", method: "get" })}
          >
            Operation
          </button>
          <Harness initialDocument={input} selection={selection} />
        </>
      );
    }
    renderWithProviders(<SelectionHarness />);
    const first = screen.getByRole("textbox", { name: "Пример ответа application/json" });
    await userEvent.clear(first);
    await fill(first, "{first");
    await userEvent.click(screen.getByRole("tab", { name: "Ответ 404" }));
    const second = screen.getByRole("textbox", { name: "Пример ответа application/json" });
    expect(second).not.toHaveValue("{first");
    await userEvent.clear(second);
    await fill(second, "{second");
    await userEvent.click(screen.getByRole("button", { name: "Metadata" }));
    await userEvent.click(screen.getByRole("button", { name: "Operation" }));
    expect(screen.getByRole("textbox", { name: "Пример ответа application/json" })).toHaveValue(
      "{first",
    );
    await userEvent.click(screen.getByRole("tab", { name: "Ответ 404" }));
    expect(screen.getByRole("textbox", { name: "Пример ответа application/json" })).toHaveValue(
      "{second",
    );
  });
  it("refuses colliding or empty property renames without changing required", async () => {
    const input = structuredClone(DOCUMENT);
    const components = input.components as {
      schemas: { Order: { properties: Record<string, unknown> } };
    };
    components.schemas.Order.properties.name = { type: "string" };
    const changed = vi.fn<(document: ApiDocument) => void>();
    renderWithProviders(
      <Harness
        initialDocument={input}
        selection={{ kind: "schema", name: "Order" }}
        onChange={changed}
      />,
    );
    const name = screen.getAllByRole("textbox", { name: "Имя свойства" })[0]!;
    await userEvent.clear(name);
    await fill(name, "name");
    await userEvent.tab();
    expect(changed).not.toHaveBeenCalled();
    expect(screen.getByRole("checkbox", { name: "id обязательно" })).toBeChecked();
    expect(screen.getByText('Свойство "name" уже существует')).toBeInTheDocument();
    await userEvent.clear(name);
    await userEvent.tab();
    expect(changed).not.toHaveBeenCalled();
    expect(screen.getByRole("checkbox", { name: "id обязательно" })).toBeChecked();
  });

  it("allows tags to be typed one character at a time", async () => {
    let last: ApiDocument | undefined;
    renderWithProviders(
      <Harness
        selection={{ kind: "operation", path: "/orders", method: "get" }}
        onChange={(next) => (last = next)}
      />,
    );
    const tags = screen.getByRole("textbox", { name: "Теги" });
    await userEvent.type(tags, "orders,admin");
    await userEvent.tab();
    expect(last).toMatchObject({ paths: { "/orders": { get: { tags: ["orders", "admin"] } } } });
  });

  it("edits enum values as typed JSON without converting numbers or booleans", async () => {
    let last: ApiDocument | undefined;
    renderWithProviders(
      <Harness selection={{ kind: "schema", name: "Order" }} onChange={(next) => (last = next)} />,
    );
    const field = screen.getByRole("textbox", { name: "Схема: enum" });
    await userEvent.clear(field);
    await userEvent.type(field, "[[1,2,true]");
    expect(last).toMatchObject({ components: { schemas: { Order: { enum: [1, 2, true] } } } });
  });

  it("stores required true when a parameter is changed to path", async () => {
    let last: ApiDocument | undefined;
    renderWithProviders(
      <Harness
        selection={{ kind: "operation", path: "/orders", method: "get" }}
        onChange={(next) => (last = next)}
      />,
    );
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Где передаётся" }), "path");
    expect(last).toMatchObject({
      paths: {
        "/orders": {
          get: { parameters: [expect.objectContaining({ in: "path", required: true })] },
        },
      },
    });
  });

  it("keeps invalid JSON through response tab switches", async () => {
    const changed = vi.fn<(document: ApiDocument) => void>();
    renderWithProviders(
      <Harness
        selection={{ kind: "operation", path: "/orders", method: "get" }}
        onChange={changed}
      />,
    );
    const example = screen.getByRole("textbox", { name: "Пример ответа application/json" });
    await userEvent.clear(example);
    await fill(example, "{oops");
    await userEvent.click(screen.getByRole("tab", { name: "Ответ 404" }));
    await userEvent.click(screen.getByRole("tab", { name: "Ответ 200" }));
    expect(screen.getByRole("textbox", { name: "Пример ответа application/json" })).toHaveValue(
      "{oops",
    );
    expect(changed).not.toHaveBeenCalled();
  });
  it("edits document metadata while preserving extensions and component siblings", async () => {
    let last: ApiDocument | undefined;
    renderWithProviders(
      <Harness selection={{ kind: "document" }} onChange={(document) => (last = document)} />,
    );

    const title = screen.getByRole("textbox", { name: "Название API" });
    await userEvent.clear(title);
    await fill(title, "Purchases");

    expect(last).toMatchObject({
      info: { title: "Purchases", version: "1.0", "x-owner": "BI" },
      "x-root": "keep",
      components: DOCUMENT.components,
    });
  });

  it("edits operation metadata and parameter schema without rebuilding responses", async () => {
    let last: ApiDocument | undefined;
    renderWithProviders(
      <Harness
        selection={{ kind: "operation", path: "/orders", method: "get" }}
        onChange={(document) => (last = document)}
      />,
    );

    const summary = screen.getByRole("textbox", { name: "Краткое название" });
    await userEvent.clear(summary);
    await fill(summary, "Все заказы");
    const parameterDescription = screen.getByRole("textbox", { name: "Описание параметра limit" });
    await userEvent.clear(parameterDescription);
    await fill(parameterDescription, "Строк на странице");

    expect(last).toMatchObject({
      paths: {
        "/orders": {
          get: {
            summary: "Все заказы",
            "x-op": "keep",
            parameters: [
              {
                name: "limit",
                description: "Строк на странице",
                schema: { type: "integer", minimum: 1, "x-unit": "rows" },
              },
            ],
            responses: {
              "200": {
                content: { "application/json": { example: [{ id: "1" }] } },
              },
              "404": {
                description: "Missing",
                headers: { "x-reason": { schema: { type: "string" } } },
              },
            },
          },
        },
      },
    });
  });

  it("keeps invalid example JSON local and emits the complete document only after it is valid", async () => {
    const onChange = vi.fn<(document: ApiDocument) => void>();
    renderWithProviders(
      <Harness
        selection={{ kind: "operation", path: "/orders", method: "get" }}
        onChange={onChange}
      />,
    );

    await userEvent.click(screen.getByRole("tab", { name: "Ответ 200" }));
    const example = screen.getByRole("textbox", { name: "Пример ответа application/json" });
    await userEvent.clear(example);
    await fill(example, "{oops");
    expect(screen.getByText("Некорректный JSON")).toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();

    await userEvent.clear(example);
    await fill(example, '{"id":"2"}');
    expect(onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({
        paths: expect.objectContaining({
          "/orders": expect.objectContaining({
            get: expect.objectContaining({
              responses: expect.objectContaining({
                "200": expect.objectContaining({
                  content: expect.objectContaining({
                    "application/json": expect.objectContaining({ example: { id: "2" } }),
                  }),
                }),
              }),
            }),
          }),
        }),
      }),
    );
  });

  it("edits schema properties and required without dropping advanced composition", async () => {
    let last: ApiDocument | undefined;
    renderWithProviders(
      <Harness
        selection={{ kind: "schema", name: "Order" }}
        onChange={(document) => (last = document)}
      />,
    );

    await userEvent.click(screen.getByRole("checkbox", { name: "id обязательно" }));
    const description = screen.getByRole("textbox", { name: "Описание свойства id" });
    await fill(description, "Identifier");

    expect(last).toMatchObject({
      components: {
        schemas: {
          Order: {
            properties: { id: { description: "Identifier", format: "uuid", "x-prop": true } },
            oneOf: [{ type: "object" }],
          },
        },
      },
    });
    const components = last?.components as Record<string, unknown> | undefined;
    const schemas = components?.schemas as Record<string, Record<string, unknown>> | undefined;
    expect(schemas?.Order?.required).toBeUndefined();
  });
});
